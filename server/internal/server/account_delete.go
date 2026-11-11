package server

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/syntstar/lightspeak/server/internal/auth"
)

// deleteAccount lets a user delete their own account after
// confirming the password. Spaces they own must be transferred or deleted
// first, and the last server admin cannot leave the server without one.
// The user row stays so messages keep an author, but it is anonymised:
// name and avatar are gone, the username is free again, sign-in is
// impossible. Memberships, friendships, sessions, devices and personal
// settings are removed.
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	uid := se.UserID
	if !s.lim.login.Allow("pw" + ID(uid).String()) {
		return errRateLimited
	}
	var hash string
	var admin int
	var avatar sql.NullString
	if err := s.db.R.QueryRowContext(ctx, `SELECT password_hash, is_server_admin, avatar_key FROM users WHERE id=?`, uid).Scan(&hash, &admin, &avatar); err != nil {
		return err
	}
	if len(req.Password) > 256 || auth.Verify(req.Password, hash) != nil {
		return errf(http.StatusForbidden, "wrong_password", "wrong password")
	}
	var owned int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM spaces WHERE owner_id=?`, uid).Scan(&owned); err != nil {
		return err
	}
	if owned > 0 {
		return errf(http.StatusConflict, "owns_spaces", "transfer or delete your spaces first")
	}
	if admin == 1 {
		var others int
		if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_server_admin=1 AND id<>? AND disabled_at IS NULL`, uid).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return errf(http.StatusConflict, "last_server_admin", "the server needs another admin first")
		}
	}

	// Everyone who knew the user gets the anonymised profile afterwards.
	related, err := s.relatedUsers(ctx, uid)
	if err != nil {
		return err
	}
	spaces, err := queryIDs(ctx, s.db.R, `SELECT space_id FROM space_members WHERE user_id=?`, uid)
	if err != nil {
		return err
	}
	groups, err := queryIDs(ctx, s.db.R, `SELECT c.id FROM channel_members m JOIN channels c ON c.id=m.channel_id WHERE m.user_id=? AND c.kind='group_dm'`, uid)
	if err != nil {
		return err
	}
	rels, err := queryIDs(ctx, s.db.R, `SELECT other_id FROM relationships WHERE user_id=? UNION SELECT user_id FROM relationships WHERE other_id=?`, uid, uid)
	if err != nil {
		return err
	}
	var sessions []int64
	now := s.nowMs()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if sessions, err = queryIDs(ctx, tx, `SELECT id FROM sessions WHERE user_id=? AND revoked_at IS NULL`, uid); err != nil {
			return err
		}
		for _, q := range []string{
			`DELETE FROM member_roles WHERE user_id=?`,
			`DELETE FROM space_members WHERE user_id=?`,
			`DELETE FROM channel_members WHERE user_id=? AND channel_id IN (SELECT id FROM channels WHERE kind='group_dm')`,
			`DELETE FROM relationships WHERE user_id=?1 OR other_id=?1`,
			`DELETE FROM media_key_envelopes WHERE device_id IN (SELECT id FROM devices WHERE user_id=?)`,
			`DELETE FROM user_settings WHERE user_id=?`,
			`DELETE FROM notification_settings WHERE user_id=?`,
			`DELETE FROM read_states WHERE user_id=?`,
		} {
			if _, err := tx.ExecContext(ctx, q, uid); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`, now, uid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE devices SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`, now, uid); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE users SET username='deleted-'||id, display_name='', password_hash='', avatar_key=NULL,
			status='invisible', status_text='', is_server_admin=0, disabled_at=?, deleted_at=? WHERE id=?`, now, now, uid)
		return err
	})
	if err != nil {
		return err
	}
	s.audit(ctx, uid, 0, "account_deleted", uid, nil)
	s.afterAccountDeleted(context.WithoutCancel(ctx), uid, sessions, spaces, groups, rels, related)
	if avatar.Valid {
		s.files.removeAvatar(avatar.String)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// afterAccountDeleted tells everyone affected and cleans up live state.
func (s *Server) afterAccountDeleted(ctx context.Context, uid int64, sessions, spaces, groups, rels, related []int64) {
	s.voice.mu.Lock()
	var cid int64
	if st := s.voice.states[uid]; st != nil && st.ChannelID != nil {
		cid = int64(*st.ChannelID)
	}
	s.voice.mu.Unlock()
	if cid != 0 {
		s.voice.clear(ctx, uid, cid)
		go s.voice.removeFromRoom(cid, uid)
	}
	for _, sid := range spaces {
		s.afterMemberLeft(ctx, sid, uid)
	}
	for _, cid := range groups {
		c, err := s.loadChannel(ctx, cid)
		if err != nil {
			continue
		}
		remaining, err := s.dmMemberIDs(ctx, cid)
		if err != nil {
			continue
		}
		if len(remaining) == 0 {
			keys, _ := s.files.keysForChannel(ctx, cid)
			_, _ = s.db.W.ExecContext(ctx, `DELETE FROM channels WHERE id=?`, cid)
			_, _ = s.db.W.ExecContext(ctx, `DELETE FROM media_key_envelopes WHERE container_id=?`, cid)
			s.files.removeKeys(keys)
			continue
		}
		d := c.dto()
		d.MemberIDs = remaining
		s.emitChannel(ctx, c, "channel_update", d)
		s.requestRotation(ctx, cid, true)
	}
	for _, other := range rels {
		s.emitRel(ctx, other, uid, "none")
	}
	if u, err := s.loadUser(ctx, uid); err == nil {
		s.hub.toUsers(related, "user_update", u)
	}
	for _, sid := range sessions {
		s.hub.dropAuthSession(sid)
	}
}
