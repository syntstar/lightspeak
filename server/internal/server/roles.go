package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/syntstar/lightspeak/server/internal/store"
)

// Roles and permissions. Two fixed tiers sit above all roles: the
// server admin (the host) and the space owner. Everyone else gets the union
// of @everyone (spaces.everyone_perms) and their roles. Rank decides who may
// act on whom: a member's rank is their highest role position.

type Perm int64

const (
	PermManageSpace Perm = 1 << iota // name, icon
	PermManageChannels
	PermManageRoles
	PermInvite
	PermKick
	PermBan
	PermVoiceModerate // server mute, disconnect, ignore user limits
	PermManageMessages
	PermMentionEveryone
	PermAdministrator // every permission except the owner's
	// Channel permissions: set per role and @everyone, and
	// overridable per channel.
	PermConnect // join voice channels
	PermSpeak   // microphone
	PermStream  // share the screen
	PermSend    // write messages
	PermAttach  // send files and images
	PermReact   // add reactions
	PermView    // see the channel at all (private channels)
	permEnd
)

const PermAll = permEnd - 1

// permEveryoneDefault is what @everyone may do in a new space.
const permEveryoneDefault = PermConnect | PermSpeak | PermStream | PermSend | PermAttach | PermReact | PermView

const (
	rankOwner       int64 = 1 << 40
	rankServerAdmin int64 = 1 << 41
)

// Access is what a user may do in a space.
type Access struct {
	Member      bool
	Owner       bool
	ServerAdmin bool
	Perms       Perm // effective: PermAll for owner, server admin, Administrator
	Rank        int64
	RoleIDs     []int64
}

func (a Access) Has(p Perm) bool { return a.Perms&p == p }

// Outranks: a may moderate t.
func (a Access) Outranks(t Access) bool { return a.Rank > t.Rank }

// legacyRole keeps space.role/member.role for older clients.
func (a Access) legacyRole() string {
	switch {
	case a.Owner:
		return "owner"
	case a.Perms == PermAll:
		return "admin"
	}
	return "member"
}

func effective(base Perm, owner, serverAdmin bool) Perm {
	if owner || serverAdmin || base&PermAdministrator != 0 {
		return PermAll
	}
	return base & PermAll
}

// spaceAccess resolves the user's access; Member is false for non-members.
func spaceAccess(ctx context.Context, q querier, spaceID, userID int64) (Access, error) {
	var owner int64
	var everyone Perm
	var sadmin int
	err := q.QueryRowContext(ctx, `SELECT s.owner_id, s.everyone_perms, u.is_server_admin FROM space_members m
		JOIN spaces s ON s.id=m.space_id JOIN users u ON u.id=m.user_id WHERE m.space_id=? AND m.user_id=?`, spaceID, userID).
		Scan(&owner, &everyone, &sadmin)
	if errors.Is(err, sql.ErrNoRows) {
		return Access{}, nil
	}
	if err != nil {
		return Access{}, err
	}
	a := Access{Member: true, Owner: owner == userID, ServerAdmin: sadmin == 1}
	rows, err := q.QueryContext(ctx, `SELECT r.id, r.permissions, r.position FROM member_roles mr JOIN roles r ON r.id=mr.role_id
		WHERE mr.space_id=? AND mr.user_id=?`, spaceID, userID)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	base := everyone
	for rows.Next() {
		var id, pos int64
		var p Perm
		if err := rows.Scan(&id, &p, &pos); err != nil {
			return a, err
		}
		base |= p
		a.RoleIDs = append(a.RoleIDs, id)
		a.Rank = max(a.Rank, pos)
	}
	a.Perms = effective(base, a.Owner, a.ServerAdmin)
	switch {
	case a.ServerAdmin:
		a.Rank = rankServerAdmin
	case a.Owner:
		a.Rank = rankOwner
	}
	return a, rows.Err()
}

// spaceAccesses resolves the access of every member of a space at once
// (same rules as spaceAccess), keyed by user ID.
func spaceAccesses(ctx context.Context, q querier, spaceID int64) (map[int64]Access, error) {
	var owner int64
	var everyone Perm
	err := q.QueryRowContext(ctx, `SELECT owner_id, everyone_perms FROM spaces WHERE id=?`, spaceID).Scan(&owner, &everyone)
	if errors.Is(err, sql.ErrNoRows) {
		return map[int64]Access{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[int64]Access{}
	base := map[int64]Perm{}
	rows, err := q.QueryContext(ctx, `SELECT m.user_id, u.is_server_admin FROM space_members m JOIN users u ON u.id=m.user_id WHERE m.space_id=?`, spaceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var uid int64
		var sadmin int
		if err := rows.Scan(&uid, &sadmin); err != nil {
			rows.Close()
			return nil, err
		}
		out[uid] = Access{Member: true, Owner: owner == uid, ServerAdmin: sadmin == 1}
		base[uid] = everyone
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT mr.user_id, r.id, r.permissions, r.position FROM member_roles mr JOIN roles r ON r.id=mr.role_id
		WHERE mr.space_id=?`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid, id, pos int64
		var p Perm
		if err := rows.Scan(&uid, &id, &p, &pos); err != nil {
			return nil, err
		}
		a, ok := out[uid]
		if !ok {
			continue
		}
		base[uid] |= p
		a.RoleIDs = append(a.RoleIDs, id)
		a.Rank = max(a.Rank, pos)
		out[uid] = a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for uid, a := range out {
		a.Perms = effective(base[uid], a.Owner, a.ServerAdmin)
		switch {
		case a.ServerAdmin:
			a.Rank = rankServerAdmin
		case a.Owner:
			a.Rank = rankOwner
		}
		out[uid] = a
	}
	return out, nil
}

// targetAccess is spaceAccess for the subject of a moderation action. Server
// admins outrank everybody even when they are not (yet) members, so a ban
// cannot lock the host out.
func (s *Server) targetAccess(ctx context.Context, spaceID, uid int64) (Access, error) {
	a, err := spaceAccess(ctx, s.db.R, spaceID, uid)
	if err != nil || a.Member {
		return a, err
	}
	var sadmin int
	if err := s.db.R.QueryRowContext(ctx, `SELECT is_server_admin FROM users WHERE id=?`, uid).Scan(&sadmin); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return a, err
	}
	if sadmin == 1 {
		a.ServerAdmin, a.Rank = true, rankServerAdmin
	}
	return a, nil
}

// ---------- DTOs ----------

type RoleDTO struct {
	ID          ID     `json:"id"`
	Name        string `json:"name"`
	Position    int64  `json:"position"`
	Permissions Perm   `json:"permissions"`
}

type spaceRoles struct {
	roles    []RoleDTO // highest first
	everyone Perm
	owner    int64
}

func (s *Server) loadRoles(ctx context.Context, q querier, spaceID int64) (spaceRoles, error) {
	var sr spaceRoles
	if err := q.QueryRowContext(ctx, `SELECT everyone_perms, owner_id FROM spaces WHERE id=?`, spaceID).Scan(&sr.everyone, &sr.owner); err != nil {
		return sr, err
	}
	rows, err := q.QueryContext(ctx, `SELECT id, name, position, permissions FROM roles WHERE space_id=? ORDER BY position DESC, id`, spaceID)
	if err != nil {
		return sr, err
	}
	defer rows.Close()
	sr.roles = []RoleDTO{}
	for rows.Next() {
		var r RoleDTO
		var id int64
		if err := rows.Scan(&id, &r.Name, &r.Position, &r.Permissions); err != nil {
			return sr, err
		}
		r.ID = ID(id)
		sr.roles = append(sr.roles, r)
	}
	return sr, rows.Err()
}

// emitRoles sends the space's role list to all members; clients derive
// everyone's permissions from it.
func (s *Server) emitRoles(ctx context.Context, spaceID int64) {
	sr, err := s.loadRoles(ctx, s.db.R, spaceID)
	if err != nil {
		return
	}
	s.emitSpace(ctx, spaceID, "space_roles", map[string]any{"spaceId": ID(spaceID), "roles": sr.roles, "everyonePerms": sr.everyone})
	s.voice.refreshSpace(ctx, spaceID)             // speaking rights depend on roles
	s.syncChannels(ctx, spaceID, "space_channels") // and which channels one sees
}

// ---------- routes ----------

func (s *Server) routesRoles(r chi.Router) {
	r.Post("/spaces/{id}/roles", s.h(s.createRole))
	r.Patch("/spaces/{id}/roles/{rid}", s.h(s.patchRole))
	r.Delete("/spaces/{id}/roles/{rid}", s.h(s.deleteRole))
	r.Post("/spaces/{id}/roles/{rid}/move", s.h(s.moveRole))
	r.Put("/spaces/{id}/members/{uid}/roles/{rid}", s.h(s.setMemberRole))
	r.Delete("/spaces/{id}/members/{uid}/roles/{rid}", s.h(s.setMemberRole))
	r.Post("/spaces/{id}/owner", s.h(s.transferOwner))
	r.Get("/admin/spaces", s.h(s.adminSpaces))
	r.Post("/admin/spaces/{id}/join", s.h(s.adminJoinSpace))
	r.Post("/admin/spaces/{id}/owner", s.h(s.adminTransfer))
	r.Delete("/admin/spaces/{id}", s.h(s.adminDeleteSpace))
}

func cleanRoleName(n string) string { return cleanName(n, 32) }

// grantable: an actor may only set or clear permission bits they hold
// themselves, so nobody can raise their own rights through a role.
func grantable(a Access, changed Perm) bool { return changed&^a.Perms == 0 }

type roleRow struct {
	id, pos int64
	perms   Perm
}

func (s *Server) loadRole(ctx context.Context, spaceID, rid int64) (roleRow, error) {
	r := roleRow{id: rid}
	err := s.db.R.QueryRowContext(ctx, `SELECT position, permissions FROM roles WHERE id=? AND space_id=?`, rid, spaceID).Scan(&r.pos, &r.perms)
	if errors.Is(err, sql.ErrNoRows) {
		return r, notFound("unknown_role")
	}
	return r, err
}

func (s *Server) createRole(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name        string `json:"name"`
		Permissions Perm   `json:"permissions"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	spaceID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	a, err := s.requireSpace(ctx, se, spaceID, PermManageRoles)
	if err != nil {
		return err
	}
	name := cleanRoleName(req.Name)
	if name == "" {
		return badRequest("invalid_name")
	}
	if req.Permissions&^PermAll != 0 {
		return badRequest("invalid_permissions")
	}
	if !grantable(a, req.Permissions) {
		return errForbidden
	}
	id := store.NewID()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM roles WHERE space_id=?`, spaceID).Scan(&n); err != nil {
			return err
		}
		if n >= s.cfg.MaxSpaceRoles {
			return errf(http.StatusForbidden, "role_limit", "too many roles")
		}
		// New roles start at the bottom; the relative order of the others stays.
		if _, err := tx.ExecContext(ctx, `UPDATE roles SET position=position+1 WHERE space_id=?`, spaceID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO roles(id, space_id, name, position, permissions, created_at) VALUES (?,?,?,?,?,?)`,
			id, spaceID, name, 1, req.Permissions, s.nowMs())
		return err
	})
	if err != nil {
		return err
	}
	s.audit(ctx, se.UserID, spaceID, "role_create", id, map[string]any{"name": name})
	s.emitRoles(ctx, spaceID)
	writeJSON(w, 201, RoleDTO{ID: ID(id), Name: name, Position: 1, Permissions: req.Permissions})
	return nil
}

// patchRole edits a role; rid == space id edits @everyone (permissions only).
func (s *Server) patchRole(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name        *string `json:"name"`
		Permissions *Perm   `json:"permissions"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	spaceID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rid, err := pathID(r, "rid")
	if err != nil {
		return err
	}
	a, err := s.requireSpace(ctx, se, spaceID, PermManageRoles)
	if err != nil {
		return err
	}
	if req.Permissions != nil && *req.Permissions&^PermAll != 0 {
		return badRequest("invalid_permissions")
	}
	if rid == spaceID {
		if req.Name != nil {
			return badRequest("invalid_role")
		}
		if req.Permissions != nil {
			var cur Perm
			if err := s.db.R.QueryRowContext(ctx, `SELECT everyone_perms FROM spaces WHERE id=?`, spaceID).Scan(&cur); err != nil {
				return err
			}
			if !grantable(a, cur^*req.Permissions) {
				return errForbidden
			}
			if _, err := s.db.W.ExecContext(ctx, `UPDATE spaces SET everyone_perms=? WHERE id=?`, *req.Permissions, spaceID); err != nil {
				return err
			}
		}
	} else {
		role, err := s.loadRole(ctx, spaceID, rid)
		if err != nil {
			return err
		}
		if role.pos >= a.Rank {
			return errForbidden
		}
		name := ""
		if req.Name != nil {
			if name = cleanRoleName(*req.Name); name == "" {
				return badRequest("invalid_name")
			}
		}
		perms := role.perms
		if req.Permissions != nil {
			if !grantable(a, role.perms^*req.Permissions) {
				return errForbidden
			}
			perms = *req.Permissions
		}
		if _, err := s.db.W.ExecContext(ctx, `UPDATE roles SET name=COALESCE(NULLIF(?,''), name), permissions=? WHERE id=?`, name, perms, rid); err != nil {
			return err
		}
	}
	s.audit(ctx, se.UserID, spaceID, "role_update", rid, nil)
	s.emitRoles(ctx, spaceID)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	spaceID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rid, err := pathID(r, "rid")
	if err != nil {
		return err
	}
	a, err := s.requireSpace(ctx, se, spaceID, PermManageRoles)
	if err != nil {
		return err
	}
	role, err := s.loadRole(ctx, spaceID, rid)
	if err != nil {
		return err
	}
	if role.pos >= a.Rank || !grantable(a, role.perms) {
		return errForbidden
	}
	affected, err := queryIDs(ctx, s.db.R, `SELECT user_id FROM member_roles WHERE role_id=?`, rid)
	if err != nil {
		return err
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM roles WHERE id=?`, rid); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE roles SET position=position-1 WHERE space_id=? AND position>?`, spaceID, role.pos)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(ctx, se.UserID, spaceID, "role_delete", rid, nil)
	s.emitRoles(ctx, spaceID)
	for _, uid := range affected {
		if m, err := s.memberDTO(ctx, spaceID, uid); err == nil {
			s.emitSpace(ctx, spaceID, "member_update", m)
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// moveRole swaps a role with its upper (dir 1) or lower (dir -1) neighbour;
// both must stay below the actor's rank.
func (s *Server) moveRole(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Dir int `json:"dir"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Dir != 1 && req.Dir != -1 {
		return badRequest("invalid_dir")
	}
	se, ctx := sess(r), r.Context()
	spaceID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rid, err := pathID(r, "rid")
	if err != nil {
		return err
	}
	a, err := s.requireSpace(ctx, se, spaceID, PermManageRoles)
	if err != nil {
		return err
	}
	role, err := s.loadRole(ctx, spaceID, rid)
	if err != nil {
		return err
	}
	other := role.pos + int64(req.Dir)
	if role.pos >= a.Rank || other >= a.Rank || other < 1 {
		return errForbidden
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE roles SET position=? WHERE space_id=? AND position=?`, role.pos, spaceID, other)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return badRequest("invalid_dir")
		}
		_, err = tx.ExecContext(ctx, `UPDATE roles SET position=? WHERE id=?`, other, rid)
		return err
	})
	if err != nil {
		return err
	}
	s.emitRoles(ctx, spaceID)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// setMemberRole gives (PUT) or takes (DELETE) a role. The role must be below
// the actor's rank and carry only permissions the actor holds; the member
// must be the actor or ranked below them.
func (s *Server) setMemberRole(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	spaceID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	uid, err := pathID(r, "uid")
	if err != nil {
		return err
	}
	rid, err := pathID(r, "rid")
	if err != nil {
		return err
	}
	a, err := s.requireSpace(ctx, se, spaceID, PermManageRoles)
	if err != nil {
		return err
	}
	role, err := s.loadRole(ctx, spaceID, rid)
	if err != nil {
		return err
	}
	t, err := spaceAccess(ctx, s.db.R, spaceID, uid)
	if err != nil {
		return err
	}
	if !t.Member {
		return notFound("unknown_member")
	}
	if role.pos >= a.Rank || !grantable(a, role.perms) || (uid != se.UserID && !a.Outranks(t)) {
		return errForbidden
	}
	add := r.Method == http.MethodPut
	if add {
		_, err = s.db.W.ExecContext(ctx, `INSERT OR IGNORE INTO member_roles(space_id, user_id, role_id) VALUES (?,?,?)`, spaceID, uid, rid)
	} else {
		_, err = s.db.W.ExecContext(ctx, `DELETE FROM member_roles WHERE space_id=? AND user_id=? AND role_id=?`, spaceID, uid, rid)
	}
	if err != nil {
		return err
	}
	s.audit(ctx, se.UserID, spaceID, "member_role", uid, map[string]any{"role": ID(rid), "add": add})
	m, err := s.memberDTO(ctx, spaceID, uid)
	if err != nil {
		return err
	}
	s.emitSpace(ctx, spaceID, "member_update", m)
	s.voice.refreshSpace(ctx, spaceID)
	s.syncChannels(ctx, spaceID, "space_channels", uid)
	writeJSON(w, 200, m)
	return nil
}

// ---------- ownership ----------

func (s *Server) transferOwner(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	spaceID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireOwner(ctx, se, spaceID); err != nil {
		return err
	}
	return s.doTransfer(w, r, spaceID)
}

func (s *Server) doTransfer(w http.ResponseWriter, r *http.Request, spaceID int64) error {
	var req struct {
		UserID ID `json:"userId"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	uid := int64(req.UserID)
	var old int64
	if err := s.db.R.QueryRowContext(ctx, `SELECT owner_id FROM spaces WHERE id=?`, spaceID).Scan(&old); err != nil {
		return err
	}
	t, err := spaceAccess(ctx, s.db.R, spaceID, uid)
	if err != nil {
		return err
	}
	if !t.Member {
		return notFound("unknown_member")
	}
	if uid == old {
		return badRequest("already_owner")
	}
	if guest, err := s.isGuest(ctx, s.db.R, uid); err != nil {
		return err
	} else if guest {
		return errGuest // the space would be stuck once the guest is gone
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		actor, err := spaceAccess(ctx, tx, spaceID, se.UserID)
		if err != nil {
			return err
		}
		// Server admins may transfer through the admin route without joining.
		var admin int
		if err := tx.QueryRowContext(ctx, `SELECT is_server_admin FROM users WHERE id=? AND disabled_at IS NULL`, se.UserID).Scan(&admin); err != nil {
			return errUnauthorized
		}
		if !actor.Owner && admin != 1 {
			return errForbidden
		}
		var eligible int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_members m JOIN users u ON u.id=m.user_id WHERE m.space_id=? AND m.user_id=? AND u.is_guest=0 AND u.disabled_at IS NULL`, spaceID, uid).Scan(&eligible); err != nil {
			return err
		}
		if eligible != 1 {
			return notFound("unknown_member")
		}
		if err := tx.QueryRowContext(ctx, `SELECT owner_id FROM spaces WHERE id=?`, spaceID).Scan(&old); err != nil {
			return err
		}

		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`UPDATE spaces SET owner_id=? WHERE id=?`, []any{uid, spaceID}},
			{`UPDATE space_members SET role='member' WHERE space_id=? AND user_id=?`, []any{spaceID, old}},
			{`UPDATE space_members SET role='owner' WHERE space_id=? AND user_id=?`, []any{spaceID, uid}},
		} {
			if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.audit(ctx, se.UserID, spaceID, "space_transfer", uid, map[string]any{"from": ID(old)})
	s.spaceUpdate(ctx, spaceID)
	defer s.voice.refreshSpace(ctx, spaceID)
	s.syncChannels(ctx, spaceID, "space_channels", old, uid)
	for _, id := range []int64{old, uid} {
		if m, err := s.memberDTO(ctx, spaceID, id); err == nil {
			s.emitSpace(ctx, spaceID, "member_update", m)
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- server admin: all spaces ----------

func requireServerAdmin(r *http.Request) error {
	if !sess(r).Admin {
		return errForbidden
	}
	return nil
}

func (s *Server) adminSpaces(w http.ResponseWriter, r *http.Request) error {
	if err := requireServerAdmin(r); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	rows, err := s.db.R.QueryContext(ctx, `SELECT s.id, s.name, s.icon_key, s.owner_id, s.created_at,
		(SELECT COUNT(*) FROM space_members WHERE space_id=s.id),
		EXISTS(SELECT 1 FROM space_members WHERE space_id=s.id AND user_id=?)
		FROM spaces s ORDER BY s.name COLLATE NOCASE`, se.UserID)
	if err != nil {
		return err
	}
	type item struct {
		ID          ID     `json:"id"`
		Name        string `json:"name"`
		IconURL     string `json:"iconUrl,omitempty"`
		OwnerID     ID     `json:"ownerId"`
		Owner       *User  `json:"owner,omitempty"`
		CreatedAt   int64  `json:"createdAt"`
		MemberCount int    `json:"memberCount"`
		Member      bool   `json:"member"`
	}
	out := []item{}
	var owners []int64
	for rows.Next() {
		var it item
		var id, owner int64
		var icon sql.NullString
		if err := rows.Scan(&id, &it.Name, &icon, &owner, &it.CreatedAt, &it.MemberCount, &it.Member); err != nil {
			rows.Close()
			return err
		}
		it.ID, it.OwnerID, it.IconURL = ID(id), ID(owner), iconURL(icon)
		out = append(out, it)
		owners = append(owners, owner)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	users, err := s.loadUsers(ctx, owners)
	if err != nil {
		return err
	}
	byID := map[ID]User{}
	for _, u := range users {
		byID[u.ID] = u
	}
	for i := range out {
		if u, ok := byID[out[i].OwnerID]; ok {
			out[i].Owner = &u
		}
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) adminSpaceID(r *http.Request) (int64, error) {
	if err := requireServerAdmin(r); err != nil {
		return 0, err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return 0, err
	}
	var n int
	if err := s.db.R.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM spaces WHERE id=?`, id).Scan(&n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, notFound("unknown_space")
	}
	return id, nil
}

// adminJoinSpace adds the server admin as a member, visible to everyone like
// any other join; E2EE keys arrive from members as usual.
func (s *Server) adminJoinSpace(w http.ResponseWriter, r *http.Request) error {
	id, err := s.adminSpaceID(r)
	if err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	a, err := spaceAccess(ctx, s.db.R, id, se.UserID)
	if err != nil {
		return err
	}
	if !a.Member {
		err = s.db.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `DELETE FROM bans WHERE space_id=? AND user_id=?`, id, se.UserID); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id, user_id, role, joined_at) VALUES (?,?,?,?)`, id, se.UserID, "member", s.nowMs())
			return err
		})
		if err != nil {
			return err
		}
		s.audit(ctx, se.UserID, id, "admin_join", se.UserID, nil)
		s.afterMemberJoined(ctx, id, se.UserID, se.DeviceID)
	}
	sp, err := s.spaceDTO(ctx, id, se.UserID)
	if err != nil {
		return err
	}
	chans, err := s.spaceChannels(ctx, id, se.UserID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"space": sp, "channels": chans})
	return nil
}

func (s *Server) adminTransfer(w http.ResponseWriter, r *http.Request) error {
	id, err := s.adminSpaceID(r)
	if err != nil {
		return err
	}
	return s.doTransfer(w, r, id)
}

func (s *Server) adminDeleteSpace(w http.ResponseWriter, r *http.Request) error {
	id, err := s.adminSpaceID(r)
	if err != nil {
		return err
	}
	return s.doDeleteSpace(w, r, id)
}

// confirmMatches compares the typed space name with the real one.
func confirmMatches(typed, name string) bool { return strings.TrimSpace(typed) == name }
