package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/syntstar/lightspeak/server/internal/auth"
	"github.com/syntstar/lightspeak/server/internal/config"
	"github.com/syntstar/lightspeak/server/internal/store"
)

const (
	maxGroupDMMembers = 10
)

func (s *Server) routesSpaces(r chi.Router) {
	r.Post("/spaces", s.h(s.createSpace))
	r.Get("/spaces/{id}", s.h(s.getSpace))
	r.Patch("/spaces/{id}", s.h(s.patchSpace))
	r.Delete("/spaces/{id}", s.h(s.deleteSpace))
	r.Post("/spaces/{id}/icon", s.h(s.uploadSpaceIcon))
	r.Delete("/spaces/{id}/icon", s.h(s.deleteSpaceIcon))
	r.Get("/spaces/{id}/members", s.h(s.listMembers))
	r.Delete("/spaces/{id}/members/{uid}", s.h(s.removeMember))
	r.Get("/spaces/{id}/bans", s.h(s.listBans))
	r.Put("/spaces/{id}/bans/{uid}", s.h(s.banMember))
	r.Delete("/spaces/{id}/bans/{uid}", s.h(s.unban))
	r.Post("/spaces/{id}/invites", s.h(s.createInvite))
	r.Get("/spaces/{id}/invites", s.h(s.listInvites))
	r.Delete("/invites/{code}", s.h(s.revokeInvite))
	r.Post("/spaces/{id}/channels", s.h(s.createChannel))
	r.Patch("/spaces/{id}/channels/order", s.h(s.reorderChannels))
	r.Patch("/channels/{id}", s.h(s.patchChannel))
	r.Delete("/channels/{id}", s.h(s.deleteChannel))
	r.Post("/dms", s.h(s.openDM))
	r.Get("/dms", s.h(s.listDMs))
	r.Delete("/dms/{id}", s.h(s.leaveDM))
	s.routesRoles(r)
}

// ---------- spaces ----------

func (s *Server) spaceDTO(ctx context.Context, spaceID, userID int64) (SpaceDTO, error) {
	var d SpaceDTO
	var id, owner int64
	var icon sql.NullString
	err := s.db.R.QueryRowContext(ctx, `SELECT s.id, s.name, s.icon_key, s.owner_id, s.e2ee_epoch FROM spaces s
		JOIN space_members m ON m.space_id=s.id AND m.user_id=? WHERE s.id=?`, userID, spaceID).
		Scan(&id, &d.Name, &icon, &owner, &d.E2EEEpoch)
	if err != nil {
		return d, err
	}
	d.ID, d.OwnerID, d.IconURL = ID(id), ID(owner), iconURL(icon)
	a, err := spaceAccess(ctx, s.db.R, spaceID, userID)
	if err != nil {
		return d, err
	}
	sr, err := s.loadRoles(ctx, s.db.R, spaceID)
	if err != nil {
		return d, err
	}
	d.Role, d.Roles, d.EveryonePerms, d.RoleIDs = a.legacyRole(), sr.roles, sr.everyone, toIDs(a.RoleIDs)
	return d, nil
}

func toIDs(v []int64) []ID {
	out := make([]ID, len(v))
	for i, x := range v {
		out[i] = ID(x)
	}
	return out
}

// loadSpaceChannels loads every channel of a space with its overrides.
func (s *Server) loadSpaceChannels(ctx context.Context, spaceID int64) ([]*Channel, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+channelCols+` FROM channels WHERE space_id=? ORDER BY position, id`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chans []*Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		chans = append(chans, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	over, err := spaceOverrides(ctx, s.db.R, spaceID)
	if err != nil {
		return nil, err
	}
	for _, c := range chans {
		c.setOverrides(over[c.ID])
	}
	return chans, nil
}

// visibleDTOs returns the channels a may see.
func visibleDTOs(chans []*Channel, a Access) []ChannelDTO {
	out := make([]ChannelDTO, 0, len(chans))
	for _, c := range chans {
		if c.visible(a) {
			out = append(out, c.dto())
		}
	}
	return out
}

// spaceChannels lists the channels of a space that uid may see.
func (s *Server) spaceChannels(ctx context.Context, spaceID, uid int64) ([]ChannelDTO, error) {
	a, err := spaceAccess(ctx, s.db.R, spaceID, uid)
	if err != nil {
		return nil, err
	}
	if !a.Member {
		return []ChannelDTO{}, nil
	}
	chans, err := s.loadSpaceChannels(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return visibleDTOs(chans, a), nil
}

// visibleChannelIDs lists the space channels (all kinds) uid may view.
func (s *Server) visibleChannelIDs(ctx context.Context, uid int64) ([]int64, error) {
	spaceIDs, err := queryIDs(ctx, s.db.R, `SELECT space_id FROM space_members WHERE user_id=?`, uid)
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, sid := range spaceIDs {
		a, err := spaceAccess(ctx, s.db.R, sid, uid)
		if err != nil {
			return nil, err
		}
		chans, err := s.loadSpaceChannels(ctx, sid)
		if err != nil {
			return nil, err
		}
		for _, c := range chans {
			if c.visible(a) {
				out = append(out, c.ID)
			}
		}
	}
	return out, nil
}

func idsJSON(ids []int64) string {
	b, _ := json.Marshal(ids)
	if ids == nil {
		return "[]"
	}
	return string(b)
}

// syncChannels sends members (all, or only the given users) the channels
// of the space they may see now. op is channels_reorder (same set, new
// order) or space_channels (the set may have changed: the client
// replaces it).
func (s *Server) syncChannels(ctx context.Context, spaceID int64, op string, only ...int64) {
	chans, err := s.loadSpaceChannels(ctx, spaceID)
	if err != nil {
		s.log.Warn("sync channels", "err", err)
		return
	}
	all, err := spaceAccesses(ctx, s.db.R, spaceID)
	if err != nil {
		s.log.Warn("sync channels", "err", err)
		return
	}
	for uid, a := range all {
		if len(only) > 0 && !slices.Contains(only, uid) {
			continue
		}
		s.hub.toUser(uid, op, map[string]any{"spaceId": ID(spaceID), "channels": visibleDTOs(chans, a)})
	}
}

func validChannelKind(k string) bool { return k == "text" || k == "voice" || k == "category" }

func (s *Server) createSpace(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name     string `json:"name"`
		Channels []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			// Parent: index of an earlier category in this list.
			Parent *int `json:"parent"`
		} `json:"channels"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	if se.Guest {
		return errGuest
	}
	name := cleanName(req.Name, 64)
	if name == "" {
		return badRequest("invalid_name")
	}
	if len(req.Channels) > 10 {
		return badRequest("too_many_channels")
	}
	id, now := store.NewID(), s.nowMs()
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND disabled_at IS NULL AND is_guest=0`, se.UserID).Scan(&active); err != nil {
			return err
		}
		if active != 1 {
			return errUnauthorized
		}

		// Regular users own at most LS_MAX_SPACES spaces; server admins are
		// exempt. Counted inside the write transaction so parallel requests
		// cannot exceed the limit.
		if !se.Admin {
			var owned int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM spaces WHERE owner_id=?`, se.UserID).Scan(&owned); err != nil {
				return err
			}
			if owned >= s.cfg.MaxSpaces {
				return errf(http.StatusForbidden, "space_limit", "too many spaces")
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO spaces(id, name, owner_id, everyone_perms, created_at) VALUES (?,?,?,?,?)`, id, name, se.UserID, permEveryoneDefault, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id, user_id, role, joined_at) VALUES (?,?,?,?)`, id, se.UserID, "owner", now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO roles(id, space_id, name, position, permissions, created_at) VALUES (?,?,?,?,?,?)`,
			store.NewID(), id, "Admin", 1, PermAdministrator, now); err != nil {
			return err
		}
		ids := make([]int64, len(req.Channels))
		for i, c := range req.Channels {
			cn := cleanName(c.Name, 64)
			if !validChannelKind(c.Kind) || cn == "" {
				return badRequest("invalid_channel")
			}
			var parent any
			if c.Parent != nil {
				p := *c.Parent
				if c.Kind == "category" || p < 0 || p >= i || req.Channels[p].Kind != "category" {
					return badRequest("invalid_parent")
				}
				parent = ids[p]
			}
			ids[i] = store.NewID()
			if _, err := tx.ExecContext(ctx, `INSERT INTO channels(id, space_id, kind, name, parent_id, position, created_at) VALUES (?,?,?,?,?,?,?)`,
				ids[i], id, c.Kind, cn, parent, i, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	sp, err := s.spaceDTO(ctx, id, se.UserID)
	if err != nil {
		return err
	}
	chans, err := s.spaceChannels(ctx, id, se.UserID)
	if err != nil {
		return err
	}
	payload := map[string]any{"space": sp, "channels": chans}
	s.hub.toUser(se.UserID, "space_create", payload)
	writeJSON(w, 201, payload)
	return nil
}

func (s *Server) getSpace(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireSpace(ctx, se, id, 0); err != nil {
		return err
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

// spaceUpdate emits space_update with role-neutral fields.
func (s *Server) spaceUpdate(ctx context.Context, id int64) {
	var name string
	var icon sql.NullString
	var owner int64
	if err := s.db.R.QueryRowContext(ctx, `SELECT name, icon_key, owner_id FROM spaces WHERE id=?`, id).Scan(&name, &icon, &owner); err != nil {
		return
	}
	s.emitSpace(ctx, id, "space_update", map[string]any{"id": ID(id), "name": name, "iconUrl": iconURL(icon), "ownerId": ID(owner)})
}

func (s *Server) patchSpace(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name *string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireSpace(ctx, se, id, PermManageSpace); err != nil {
		return err
	}
	if req.Name != nil {
		n := cleanName(*req.Name, 64)
		if n == "" {
			return badRequest("invalid_name")
		}
		if _, err := s.db.W.ExecContext(ctx, `UPDATE spaces SET name=? WHERE id=?`, n, id); err != nil {
			return err
		}
	}
	s.spaceUpdate(ctx, id)
	sp, err := s.spaceDTO(ctx, id, se.UserID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, sp)
	return nil
}

func (s *Server) deleteSpace(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireOwner(r.Context(), sess(r), id); err != nil {
		return err
	}
	return s.doDeleteSpace(w, r, id)
}

// doDeleteSpace deletes a space after the caller typed its name (owner or
// server admin; authorised by the caller).
func (s *Server) doDeleteSpace(w http.ResponseWriter, r *http.Request, id int64) error {
	var req struct {
		ConfirmName string `json:"confirmName"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	var name string
	var icon sql.NullString
	if err := s.db.R.QueryRowContext(ctx, `SELECT name, icon_key FROM spaces WHERE id=?`, id).Scan(&name, &icon); err != nil {
		return err
	}
	if !confirmMatches(req.ConfirmName, name) {
		return badRequest("confirm_name_mismatch")
	}
	audience, err := s.spaceAudience(ctx, id)
	if err != nil {
		return err
	}
	voiceChans, err := queryIDs(ctx, s.db.R, `SELECT id FROM channels WHERE space_id=? AND kind='voice'`, id)
	if err != nil {
		return err
	}
	keys, err := s.files.keysForSpace(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(ctx, `DELETE FROM spaces WHERE id=?`, id); err != nil {
		return err
	}
	_, _ = s.db.W.ExecContext(ctx, `DELETE FROM media_key_envelopes WHERE container_id=?`, id)
	_, _ = s.db.W.ExecContext(ctx, `DELETE FROM notification_settings WHERE target_id=?`, id)
	s.files.removeKeys(keys)
	if icon.Valid {
		s.files.removeAvatar(icon.String)
	}
	for _, c := range voiceChans {
		s.voice.closeChannel(ctx, c)
	}
	s.audit(ctx, se.UserID, id, "space_delete", id, map[string]any{"name": name})
	s.hub.toUsers(audience, "space_delete", map[string]any{"id": ID(id)})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- members ----------

type memberDTO struct {
	SpaceID     ID     `json:"spaceId"`
	UserID      ID     `json:"userId"`
	Role        string `json:"role"` // legacy: owner, admin (Administrator) or member
	RoleIDs     []ID   `json:"roleIds"`
	ServerAdmin bool   `json:"serverAdmin,omitempty"`
	JoinedAt    int64  `json:"joinedAt"`
	User        User   `json:"user"`
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireSpace(ctx, se, id, 0); err != nil {
		return err
	}
	sr, err := s.loadRoles(ctx, s.db.R, id)
	if err != nil {
		return err
	}
	perms := map[ID]Perm{}
	for _, ro := range sr.roles {
		perms[ro.ID] = ro.Permissions
	}
	assigned := map[int64][]ID{}
	mr, err := s.db.R.QueryContext(ctx, `SELECT user_id, role_id FROM member_roles WHERE space_id=?`, id)
	if err != nil {
		return err
	}
	for mr.Next() {
		var uid, rid int64
		if err := mr.Scan(&uid, &rid); err != nil {
			mr.Close()
			return err
		}
		assigned[uid] = append(assigned[uid], ID(rid))
	}
	mr.Close()
	if err := mr.Err(); err != nil {
		return err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT m.joined_at, u.is_server_admin, u.id, u.username, u.display_name, u.avatar_key, u.is_guest
		FROM space_members m JOIN users u ON u.id=m.user_id WHERE m.space_id=? ORDER BY u.display_name COLLATE NOCASE`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	type withPresence struct {
		memberDTO
		Presence presence `json:"presence"`
	}
	out := []withPresence{}
	for rows.Next() {
		var m withPresence
		var uid int64
		var sadmin int
		var av sql.NullString
		if err := rows.Scan(&m.JoinedAt, &sadmin, &uid, &m.User.Username, &m.User.DisplayName, &av, &m.User.Guest); err != nil {
			return err
		}
		m.SpaceID, m.UserID, m.User.ID, m.User.AvatarURL = ID(id), ID(uid), ID(uid), avatarURL(av)
		m.ServerAdmin, m.RoleIDs = sadmin == 1, assigned[uid]
		if m.RoleIDs == nil {
			m.RoleIDs = []ID{}
		}
		base := sr.everyone
		for _, rid := range m.RoleIDs {
			base |= perms[rid]
		}
		m.Role = Access{Owner: uid == sr.owner, Perms: effective(base, uid == sr.owner, m.ServerAdmin)}.legacyRole()
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for i := range out {
		out[i].Presence = s.effectivePresence(ctx, int64(out[i].UserID))
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) memberDTO(ctx context.Context, spaceID, uid int64) (memberDTO, error) {
	m := memberDTO{SpaceID: ID(spaceID), UserID: ID(uid)}
	if err := s.db.R.QueryRowContext(ctx, `SELECT joined_at FROM space_members WHERE space_id=? AND user_id=?`, spaceID, uid).Scan(&m.JoinedAt); err != nil {
		return m, err
	}
	a, err := spaceAccess(ctx, s.db.R, spaceID, uid)
	if err != nil {
		return m, err
	}
	m.Role, m.RoleIDs, m.ServerAdmin = a.legacyRole(), toIDs(a.RoleIDs), a.ServerAdmin
	u, err := s.loadUser(ctx, uid)
	m.User = u
	return m, err
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var uid int64
	if chi.URLParam(r, "uid") == "@me" {
		uid = se.UserID
	} else if uid, err = pathID(r, "uid"); err != nil {
		return err
	}
	actor, err := s.requireSpace(ctx, se, id, 0)
	if err != nil {
		return err
	}
	target, err := spaceAccess(ctx, s.db.R, id, uid)
	if err != nil {
		return err
	}
	if !target.Member {
		return notFound("unknown_member")
	}
	if uid == se.UserID {
		if actor.Owner {
			return errf(http.StatusForbidden, "owner_cannot_leave", "owner must transfer or delete the space")
		}
	} else if !actor.Has(PermKick) || !actor.Outranks(target) {
		return errForbidden
	}
	if _, err := s.db.W.ExecContext(ctx, `DELETE FROM space_members WHERE space_id=? AND user_id=?`, id, uid); err != nil {
		return err
	}
	if uid != se.UserID {
		s.audit(ctx, se.UserID, id, "member_kick", uid, nil)
	}
	s.afterMemberLeft(ctx, id, uid)
	s.removeGuest(context.WithoutCancel(ctx), uid) // a guest has no other space
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listBans(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireSpace(ctx, se, id, PermBan); err != nil {
		return err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT b.reason, b.created_at, u.id, u.username, u.display_name, u.avatar_key
		FROM bans b JOIN users u ON u.id=b.user_id WHERE b.space_id=? ORDER BY b.created_at DESC`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	type banDTO struct {
		User      User   `json:"user"`
		Reason    string `json:"reason"`
		CreatedAt int64  `json:"createdAt"`
	}
	out := []banDTO{}
	for rows.Next() {
		var b banDTO
		var uid int64
		var av sql.NullString
		if err := rows.Scan(&b.Reason, &b.CreatedAt, &uid, &b.User.Username, &b.User.DisplayName, &av); err != nil {
			return err
		}
		b.User.ID, b.User.AvatarURL = ID(uid), avatarURL(av)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) banMember(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Reason string `json:"reason"`
	}
	_ = decode(r, &req) // body optional
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	uid, err := pathID(r, "uid")
	if err != nil {
		return err
	}
	actor, err := s.requireSpace(ctx, se, id, PermBan)
	if err != nil {
		return err
	}
	if uid == se.UserID {
		return badRequest("cannot_ban_self")
	}
	target, err := s.targetAccess(ctx, id, uid)
	if err != nil {
		return err
	}
	if !actor.Outranks(target) {
		return errForbidden
	}
	var exists int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=?`, uid).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return notFound("unknown_user")
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO bans(space_id, user_id, banned_by, reason, created_at) VALUES (?,?,?,?,?)
			ON CONFLICT(space_id, user_id) DO UPDATE SET reason=excluded.reason`, id, uid, se.UserID, clampText(req.Reason, 256), s.nowMs()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM space_members WHERE space_id=? AND user_id=?`, id, uid)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(ctx, se.UserID, id, "member_ban", uid, nil)
	if target.Member {
		s.afterMemberLeft(ctx, id, uid)
	}
	s.removeGuest(context.WithoutCancel(ctx), uid)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) unban(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	uid, err := pathID(r, "uid")
	if err != nil {
		return err
	}
	if _, err := s.requireSpace(ctx, se, id, PermBan); err != nil {
		return err
	}
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM bans WHERE space_id=? AND user_id=?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("unknown_ban")
	}
	s.audit(ctx, se.UserID, id, "member_unban", uid, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// afterMemberJoined announces a new member and asks key holders for envelopes.
func (s *Server) afterMemberJoined(ctx context.Context, spaceID, uid, deviceID int64) {
	m, err := s.memberDTO(ctx, spaceID, uid)
	if err != nil {
		return
	}
	s.emitSpace(ctx, spaceID, "member_add", m)
	// Members who were not related to the newcomer before have no presence
	// for them yet (it is only sent on connect and status changes).
	s.broadcastPresence(ctx, uid)
	if sp, err := s.spaceDTO(ctx, spaceID, uid); err == nil {
		if chans, err := s.spaceChannels(ctx, spaceID, uid); err == nil {
			s.hub.toUser(uid, "space_create", map[string]any{"space": sp, "channels": chans})
		}
	}
	devs, _ := queryIDs(ctx, s.db.R, `SELECT id FROM devices WHERE user_id=? AND revoked_at IS NULL AND public_key IS NOT NULL`, uid)
	if len(devs) > 0 {
		s.requestKeys(ctx, spaceID, false, devs)
	}
}

// afterMemberLeft removes the user from voice, informs everyone and asks an
// admin to rotate the space media key.
func (s *Server) afterMemberLeft(ctx context.Context, spaceID, uid int64) {
	s.voice.removeUserFromSpace(ctx, spaceID, uid)
	s.emitSpace(ctx, spaceID, "member_remove", map[string]any{"spaceId": ID(spaceID), "userId": ID(uid)})
	s.hub.toUser(uid, "space_delete", map[string]any{"id": ID(spaceID)})
	_, _ = s.db.W.ExecContext(ctx, `DELETE FROM read_states WHERE user_id=? AND channel_id IN (SELECT id FROM channels WHERE space_id=?)`, uid, spaceID)
	s.requestRotation(ctx, spaceID, false)
}

// ---------- invites ----------

type inviteDTO struct {
	Code      string `json:"code"`
	SpaceID   *ID    `json:"spaceId,omitempty"`
	MaxUses   *int64 `json:"maxUses"`
	Uses      int64  `json:"uses"`
	ExpiresAt *int64 `json:"expiresAt"`
	Guest     bool   `json:"guest,omitempty"`
	CreatedBy ID     `json:"createdBy"`
	CreatedAt int64  `json:"createdAt"`
	URL       string `json:"url"`
}

func (s *Server) inviteURL(code string) string { return s.cfg.PublicURL + "/invite/" + code }

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		MaxAgeSeconds int64 `json:"maxAgeSeconds"`
		MaxUses       int64 `json:"maxUses"`
		// Guest: people may also join without an account (guest.go).
		Guest bool `json:"guest"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireSpace(ctx, se, id, PermInvite); err != nil {
		return err
	}
	if se.Guest {
		return errGuest
	}
	if s.cfg.Invites == config.InvitesAdmins && !se.Admin {
		return errf(http.StatusForbidden, "invites_restricted", "only server admins may invite on this server")
	}
	switch req.MaxAgeSeconds {
	case 0, 3600, 86400, 7 * 86400:
	default:
		return badRequest("invalid_max_age")
	}
	if req.MaxUses < 0 || req.MaxUses > 100 {
		return badRequest("invalid_max_uses")
	}
	var active int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM invites WHERE space_id=? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?)`, id, s.nowMs()).Scan(&active); err != nil {
		return err
	}
	if active >= 50 {
		return errf(http.StatusForbidden, "invite_limit", "too many invites")
	}
	d, err := s.insertInvite(ctx, id, se.UserID, req.MaxAgeSeconds, req.MaxUses, false, req.Guest)
	if err != nil {
		return err
	}
	s.audit(ctx, se.UserID, id, "invite_create", 0, map[string]any{"code": d.Code[:4] + "…", "guest": d.Guest})
	writeJSON(w, 201, d)
	return nil
}

// InsertInvite is also used by the admin CLI (spaceID 0 = registration only).
func (s *Server) insertInvite(ctx context.Context, spaceID, by, maxAgeSec, maxUses int64, grantsAdmin, guest bool) (inviteDTO, error) {
	code, now := auth.RandomCode(10), s.nowMs()
	d := inviteDTO{Code: code, SpaceID: optID(spaceID), Guest: guest, CreatedBy: ID(by), CreatedAt: now, URL: s.inviteURL(code)}
	var exp, mu, sp, cb any
	if maxAgeSec > 0 {
		e := now + maxAgeSec*1000
		exp, d.ExpiresAt = e, &e
	}
	if maxUses > 0 {
		mu, d.MaxUses = maxUses, &maxUses
	}
	if spaceID != 0 {
		sp = spaceID
	}
	if by != 0 {
		cb = by
	}
	ga := 0
	if grantsAdmin {
		ga = 1
	}
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO invites(code, space_id, created_by, max_uses, expires_at, grants_admin, guest, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		code, sp, cb, mu, exp, ga, guest, now)
	return d, err
}

// CreateInvite exposes invite creation to the admin CLI.
func (s *Server) CreateInvite(ctx context.Context, spaceID int64, maxAgeSec, maxUses int64, grantsAdmin bool) (code, url string, err error) {
	d, err := s.insertInvite(ctx, spaceID, 0, maxAgeSec, maxUses, grantsAdmin, false)
	return d.Code, d.URL, err
}

// CreateOwnerInvite issues a single-use invite that makes its redeemer a
// server admin. Pending owner invites from earlier runs are revoked first, so
// at most one can be open at any time (re-running the installer or
// "admin bootstrap" must not leave several valid admin invites behind).
func (s *Server) CreateOwnerInvite(ctx context.Context, maxAgeSec int64) (code, url string, err error) {
	if _, err := s.db.W.ExecContext(ctx, `UPDATE invites SET revoked_at=? WHERE grants_admin=1 AND revoked_at IS NULL
		AND (max_uses IS NULL OR uses < max_uses)`, s.nowMs()); err != nil {
		return "", "", err
	}
	return s.CreateInvite(ctx, 0, maxAgeSec, 1, true)
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireSpace(ctx, se, id, PermInvite); err != nil {
		return err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT code, max_uses, uses, expires_at, COALESCE(created_by,0), created_at, guest FROM invites
		WHERE space_id=? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?) ORDER BY created_at DESC`, id, s.nowMs())
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []inviteDTO{}
	for rows.Next() {
		var d inviteDTO
		var mu, exp sql.NullInt64
		var by int64
		if err := rows.Scan(&d.Code, &mu, &d.Uses, &exp, &by, &d.CreatedAt, &d.Guest); err != nil {
			return err
		}
		if mu.Valid {
			d.MaxUses = &mu.Int64
			if d.Uses >= mu.Int64 {
				continue
			}
		}
		if exp.Valid {
			d.ExpiresAt = &exp.Int64
		}
		d.SpaceID, d.CreatedBy, d.URL = optID(id), ID(by), s.inviteURL(d.Code)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	iv, err := s.loadInvite(ctx, s.db.R, chi.URLParam(r, "code"))
	if err != nil {
		return err
	}
	allowed := se.Admin || (iv.CreatedBy == se.UserID && iv.CreatedBy != 0)
	if !allowed && iv.SpaceID != 0 {
		a, err := spaceAccess(ctx, s.db.R, iv.SpaceID, se.UserID)
		if err != nil {
			return err
		}
		if !a.Member {
			return notFound("invite_invalid")
		}
		allowed = a.Has(PermInvite)
	}
	if !allowed {
		return errForbidden
	}
	if _, err := s.db.W.ExecContext(ctx, `UPDATE invites SET revoked_at=? WHERE code=? AND revoked_at IS NULL`, s.nowMs(), iv.Code); err != nil {
		return err
	}
	s.audit(ctx, se.UserID, iv.SpaceID, "invite_revoke", 0, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	iv, err := s.loadInvite(ctx, s.db.R, chi.URLParam(r, "code"))
	if err != nil {
		return err
	}
	if err := s.inviteUsable(ctx, iv); err != nil {
		return err
	}
	if iv.SpaceID == 0 {
		return badRequest("invite_not_for_space")
	}
	if se.Guest {
		return errGuest // a guest belongs to the space it was invited to
	}
	a, err := spaceAccess(ctx, s.db.R, iv.SpaceID, se.UserID)
	if err != nil {
		return err
	}
	if !a.Member {
		var banned, members int
		if err := s.db.R.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM bans WHERE space_id=? AND user_id=?), (SELECT COUNT(*) FROM space_members WHERE space_id=?)`,
			iv.SpaceID, se.UserID, iv.SpaceID).Scan(&banned, &members); err != nil {
			return err
		}
		if banned > 0 {
			return errf(http.StatusForbidden, "banned", "banned from this space")
		}
		if members >= s.cfg.MaxSpaceMembers {
			return errf(http.StatusForbidden, "space_full", "space is full")
		}
		err = s.db.Tx(ctx, func(tx *sql.Tx) error {
			if err := s.consumeInvite(ctx, tx, iv.Code); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id, user_id, role, joined_at) VALUES (?,?,?,?)`, iv.SpaceID, se.UserID, "member", s.nowMs())
			return err
		})
		if err != nil {
			return err
		}
		s.afterMemberJoined(ctx, iv.SpaceID, se.UserID, se.DeviceID)
	}
	sp, err := s.spaceDTO(ctx, iv.SpaceID, se.UserID)
	if err != nil {
		return err
	}
	chans, err := s.spaceChannels(ctx, iv.SpaceID, se.UserID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"space": sp, "channels": chans})
	return nil
}

// ---------- channels ----------

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Topic     string `json:"topic"`
		ParentID  ID     `json:"parentId"`
		UserLimit int    `json:"userLimit"`
		rulesReq
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	spaceID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	a, err := s.requireSpace(ctx, se, spaceID, PermManageChannels)
	if err != nil {
		return err
	}
	name := cleanName(req.Name, 64)
	if !validChannelKind(req.Kind) || name == "" {
		return badRequest("invalid_channel")
	}
	if req.UserLimit < 0 || req.UserLimit > 99 {
		return badRequest("invalid_user_limit")
	}
	var count, maxPos int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(position),0) FROM channels WHERE space_id=?`, spaceID).Scan(&count, &maxPos); err != nil {
		return err
	}
	if count >= s.cfg.MaxSpaceChannels {
		return errf(http.StatusForbidden, "channel_limit", "too many channels")
	}
	var parent any
	if req.ParentID != 0 {
		if req.Kind == "category" {
			return badRequest("invalid_parent")
		}
		if err := s.checkParent(ctx, spaceID, int64(req.ParentID)); err != nil {
			return err
		}
		parent = int64(req.ParentID)
	}
	var limit any
	if req.Kind == "voice" && req.UserLimit > 0 {
		limit = req.UserLimit
	}
	id := store.NewID()
	c := &Channel{ID: id, SpaceID: spaceID, Kind: req.Kind, Mode: modeOpen, Audio: "speech"}
	c.setOverrides(nil)
	if err := s.applyRules(ctx, a, c, &req.rulesReq); err != nil {
		return err
	}
	var demoted []int64
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channels(id, space_id, kind, parent_id, name, topic, position, user_limit, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
			id, spaceID, req.Kind, parent, name, clampText(req.Topic, 1024), maxPos+1, limit, s.nowMs()); err != nil {
			return err
		}
		var err error
		demoted, err = saveRules(ctx, tx, c)
		return err
	})
	if err != nil {
		return err
	}
	if c, err = s.loadChannel(ctx, id); err != nil {
		return err
	}
	if err := s.fillOverrides(ctx, s.db.R, c); err != nil {
		return err
	}
	d := c.dto()
	s.emitChannelIDs(ctx, demoted)
	s.emitChannel(ctx, c, "channel_create", d)
	writeJSON(w, 201, d)
	return nil
}

func (s *Server) checkParent(ctx context.Context, spaceID, parentID int64) error {
	var kind string
	err := s.db.R.QueryRowContext(ctx, `SELECT kind FROM channels WHERE id=? AND space_id=?`, parentID, spaceID).Scan(&kind)
	if err != nil || kind != "category" {
		return badRequest("invalid_parent")
	}
	return nil
}

func (s *Server) patchChannel(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name      *string `json:"name"`
		Topic     *string `json:"topic"`
		ParentID  *ID     `json:"parentId"`
		UserLimit *int    `json:"userLimit"`
		rulesReq
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, a, err := s.channelAccess(ctx, se, id)
	if err != nil {
		return err
	}
	if c.Kind == "dm" {
		return errForbidden
	}
	if !c.isDM() && !a.Has(PermManageChannels) {
		return errForbidden
	}
	if c.isDM() && (req.ParentID != nil || req.UserLimit != nil) {
		return badRequest("invalid_channel")
	}
	before, err := s.channelAudience(ctx, c)
	if err != nil {
		return err
	}
	if err := s.applyRules(ctx, a, c, &req.rulesReq); err != nil {
		return err
	}
	if req.Name != nil {
		n := cleanName(*req.Name, 64)
		if n == "" && !c.isDM() {
			return badRequest("invalid_name")
		}
		c.Name = n
	}
	if req.Topic != nil {
		c.Topic = clampText(*req.Topic, 1024)
	}
	if req.ParentID != nil {
		if *req.ParentID != 0 {
			if c.Kind == "category" {
				return badRequest("invalid_parent")
			}
			if err := s.checkParent(ctx, c.SpaceID, int64(*req.ParentID)); err != nil {
				return err
			}
		}
		c.ParentID = int64(*req.ParentID)
	}
	if req.UserLimit != nil {
		if *req.UserLimit < 0 || *req.UserLimit > 99 || c.Kind != "voice" {
			return badRequest("invalid_user_limit")
		}
		c.UserLimit = *req.UserLimit
	}
	var parent, limit any
	if c.ParentID != 0 {
		parent = c.ParentID
	}
	if c.UserLimit > 0 {
		limit = c.UserLimit
	}
	var demoted []int64
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET name=?, topic=?, parent_id=?, user_limit=? WHERE id=?`, c.Name, c.Topic, parent, limit, c.ID); err != nil {
			return err
		}
		var err error
		demoted, err = saveRules(ctx, tx, c)
		return err
	})
	if err != nil {
		return err
	}
	d := c.dto()
	if c.isDM() {
		d.MemberIDs, _ = s.dmMemberIDs(ctx, c.ID)
	}
	s.emitChannelIDs(ctx, demoted)
	if err := s.emitChannelChange(ctx, c, before, d); err != nil {
		return err
	}
	if c.Kind == "voice" && req.any() {
		s.voice.refreshSpace(ctx, c.SpaceID)
	}
	writeJSON(w, 200, d)
	return nil
}

// emitChannelChange announces an edited channel. When its overrides
// changed who may view it, those who lost it get channel_delete
// and those who gained it channel_create; everyone else channel_update.
func (s *Server) emitChannelChange(ctx context.Context, c *Channel, before []int64, d ChannelDTO) error {
	if c.isDM() {
		s.emitChannel(ctx, c, "channel_update", d)
		return nil
	}
	after, err := s.channelAudience(ctx, c)
	if err != nil {
		return err
	}
	var lost, gained, kept []int64
	for _, u := range before {
		if !slices.Contains(after, u) {
			lost = append(lost, u)
		}
	}
	for _, u := range after {
		if slices.Contains(before, u) {
			kept = append(kept, u)
		} else {
			gained = append(gained, u)
		}
	}
	s.hub.toUsers(kept, "channel_update", d)
	s.hub.toUsers(gained, "channel_create", d)
	s.hub.toUsers(lost, "channel_delete", map[string]any{"id": ID(c.ID), "spaceId": ID(c.SpaceID)})
	return nil
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, a, err := s.channelAccess(ctx, se, id)
	if err != nil {
		return err
	}
	if c.isDM() || !a.Has(PermManageChannels) {
		return errForbidden
	}
	viewers, err := s.channelAudience(ctx, c)
	if err != nil {
		return err
	}
	keys, err := s.files.keysForChannel(ctx, id)
	if err != nil {
		return err
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if c.Kind == "category" {
			if _, err := tx.ExecContext(ctx, `UPDATE channels SET parent_id=NULL WHERE parent_id=?`, id); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE id=?`, id)
		return err
	})
	if err != nil {
		return err
	}
	_, _ = s.db.W.ExecContext(ctx, `DELETE FROM notification_settings WHERE target_id=?`, id)
	s.files.removeKeys(keys)
	if c.Kind == "voice" {
		s.voice.closeChannel(ctx, id)
	}
	s.audit(ctx, se.UserID, c.SpaceID, "channel_delete", id, map[string]any{"name": c.Name})
	s.hub.toUsers(viewers, "channel_delete", map[string]any{"id": ID(id), "spaceId": ID(c.SpaceID)})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) reorderChannels(w http.ResponseWriter, r *http.Request) error {
	var req []struct {
		ID       ID  `json:"id"`
		Position int `json:"position"`
		ParentID *ID `json:"parentId"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	spaceID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireSpace(ctx, se, spaceID, PermManageChannels); err != nil {
		return err
	}
	if len(req) == 0 || len(req) > s.cfg.MaxSpaceChannels {
		return badRequest("invalid_order")
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, it := range req {
			var kind string
			if err := tx.QueryRowContext(ctx, `SELECT kind FROM channels WHERE id=? AND space_id=?`, int64(it.ID), spaceID).Scan(&kind); err != nil {
				return badRequest("invalid_order")
			}
			if it.ParentID != nil {
				var parent any
				if *it.ParentID != 0 {
					var pk string
					if kind == "category" || tx.QueryRowContext(ctx, `SELECT kind FROM channels WHERE id=? AND space_id=?`, int64(*it.ParentID), spaceID).Scan(&pk) != nil || pk != "category" {
						return badRequest("invalid_parent")
					}
					parent = int64(*it.ParentID)
				}
				if _, err := tx.ExecContext(ctx, `UPDATE channels SET position=?, parent_id=? WHERE id=?`, it.Position, parent, int64(it.ID)); err != nil {
					return err
				}
			} else if _, err := tx.ExecContext(ctx, `UPDATE channels SET position=? WHERE id=?`, it.Position, int64(it.ID)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	chans, err := s.spaceChannels(ctx, spaceID, se.UserID)
	if err != nil {
		return err
	}
	s.syncChannels(ctx, spaceID, "channels_reorder")
	writeJSON(w, 200, chans)
	return nil
}

// ---------- DMs ----------

func (s *Server) dmMemberIDs(ctx context.Context, channelID int64) ([]ID, error) {
	ids, err := queryIDs(ctx, s.db.R, `SELECT user_id FROM channel_members WHERE channel_id=? ORDER BY user_id`, channelID)
	out := make([]ID, len(ids))
	for i, v := range ids {
		out[i] = ID(v)
	}
	return out, err
}

func (s *Server) dmDTO(ctx context.Context, c *Channel) (ChannelDTO, error) {
	d := c.dto()
	var err error
	d.MemberIDs, err = s.dmMemberIDs(ctx, c.ID)
	return d, err
}

func (s *Server) openDM(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		UserIDs []ID   `json:"userIds"`
		Name    string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	others := map[int64]bool{}
	for _, id := range req.UserIDs {
		if int64(id) != se.UserID && id > 0 {
			others[int64(id)] = true
		}
	}
	if len(others) == 0 || len(others)+1 > maxGroupDMMembers {
		return badRequest("invalid_recipients")
	}
	// Existing 1:1 DMs stay readable after an unfriend; new DMs and groups
	// need a friendship with every recipient.
	notFriends := false
	for uid := range others {
		ok, err := s.related(ctx, se.UserID, uid)
		if err != nil {
			return err
		}
		if !ok {
			return notFound("unknown_user")
		}
		blocked, err := s.blockedEither(ctx, se.UserID, uid)
		if err != nil {
			return err
		}
		if blocked {
			return errf(http.StatusForbidden, "blocked", "cannot message this user")
		}
		friends, err := s.areFriends(ctx, se.UserID, uid)
		if err != nil {
			return err
		}
		if !friends {
			notFriends = true
		}
	}
	if len(others) == 1 {
		var other int64
		for k := range others {
			other = k
		}
		var existing int64
		err := s.db.R.QueryRowContext(ctx, `SELECT c.id FROM channels c
			JOIN channel_members a ON a.channel_id=c.id AND a.user_id=?
			JOIN channel_members b ON b.channel_id=c.id AND b.user_id=?
			WHERE c.kind='dm' LIMIT 1`, se.UserID, other).Scan(&existing)
		if err == nil {
			c, err := s.loadChannel(ctx, existing)
			if err != nil {
				return err
			}
			d, err := s.dmDTO(ctx, c)
			if err != nil {
				return err
			}
			writeJSON(w, 200, d)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if notFriends {
		return errf(http.StatusForbidden, "not_friends", "direct messages need a friendship")
	}
	kind := "dm"
	if len(others) > 1 {
		kind = "group_dm"
	}
	id, now := store.NewID(), s.nowMs()
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channels(id, kind, name, owner_id, created_at) VALUES (?,?,?,?,?)`,
			id, kind, cleanName(req.Name, 64), se.UserID, now); err != nil {
			return err
		}
		members := []int64{se.UserID}
		for uid := range others {
			members = append(members, uid)
		}
		for _, uid := range members {
			if _, err := tx.ExecContext(ctx, `INSERT INTO channel_members(channel_id, user_id) VALUES (?,?)`, id, uid); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	c, err := s.loadChannel(ctx, id)
	if err != nil {
		return err
	}
	d, err := s.dmDTO(ctx, c)
	if err != nil {
		return err
	}
	users, _ := s.loadUsers(ctx, idsOf(d.MemberIDs))
	s.emitChannel(ctx, c, "channel_create", map[string]any{"channel": d, "users": users})
	writeJSON(w, 201, d)
	return nil
}

func idsOf(ids []ID) []int64 {
	out := make([]int64, len(ids))
	for i, v := range ids {
		out[i] = int64(v)
	}
	return out
}

func (s *Server) userDMs(ctx context.Context, uid int64) ([]ChannelDTO, []int64, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+channelCols+` FROM channels WHERE id IN (SELECT channel_id FROM channel_members WHERE user_id=?)
		ORDER BY COALESCE(last_message_id, id) DESC`, uid)
	if err != nil {
		return nil, nil, err
	}
	var chans []*Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			rows.Close()
			return nil, nil, err
		}
		chans = append(chans, c)
	}
	rows.Close()
	out := []ChannelDTO{}
	userSet := map[int64]bool{}
	var users []int64
	for _, c := range chans {
		d, err := s.dmDTO(ctx, c)
		if err != nil {
			return nil, nil, err
		}
		for _, m := range d.MemberIDs {
			if !userSet[int64(m)] {
				userSet[int64(m)] = true
				users = append(users, int64(m))
			}
		}
		out = append(out, d)
	}
	return out, users, nil
}

func (s *Server) listDMs(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	dms, uids, err := s.userDMs(ctx, sess(r).UserID)
	if err != nil {
		return err
	}
	users, err := s.loadUsers(ctx, uids)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"channels": dms, "users": users})
	return nil
}

func (s *Server) leaveDM(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, _, err := s.channelAccess(ctx, se, id)
	if err != nil {
		return err
	}
	if c.Kind != "group_dm" {
		return badRequest("cannot_leave_dm")
	}
	if _, err := s.db.W.ExecContext(ctx, `DELETE FROM channel_members WHERE channel_id=? AND user_id=?`, id, se.UserID); err != nil {
		return err
	}
	s.voice.removeUserFromChannel(ctx, id, se.UserID)
	s.hub.toUser(se.UserID, "channel_delete", map[string]any{"id": ID(id)})
	remaining, err := s.dmMemberIDs(ctx, id)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		keys, _ := s.files.keysForChannel(ctx, id)
		_, _ = s.db.W.ExecContext(ctx, `DELETE FROM channels WHERE id=?`, id)
		_, _ = s.db.W.ExecContext(ctx, `DELETE FROM media_key_envelopes WHERE container_id=?`, id)
		s.files.removeKeys(keys)
	} else {
		d := c.dto()
		d.MemberIDs = remaining
		s.emitChannel(ctx, c, "channel_update", d)
		s.requestRotation(ctx, id, true)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
