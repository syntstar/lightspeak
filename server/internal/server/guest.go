package server

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/syntstar/lightspeak/server/internal/auth"
	"github.com/syntstar/lightspeak/server/internal/store"
)

// Guest access: an invite with the guest flag admits people who only give a
// name. A guest is a user without a password, so nobody can sign in to it
// again: it lives as long as its session and is removed when the guest signs
// out, leaves or is removed from the space, or the session runs out (app or
// browser data deleted). Guests belong to the one space they were invited to
// and cannot create spaces or invites, add friends or own a space.

// guestSessionTTL is how long an unused guest session (and with it the guest
// account) is kept. Sliding, like regular sessions.
const guestSessionTTL = 30 * 24 * time.Hour

var errGuest = errf(http.StatusForbidden, "guest_restricted", "not available with guest access")

func sessionTTLFor(guest bool) time.Duration {
	if guest {
		return guestSessionTTL
	}
	return sessionTTL
}

func (s *Server) isGuest(ctx context.Context, q querier, uid int64) (bool, error) {
	var g int
	err := q.QueryRowContext(ctx, `SELECT is_guest FROM users WHERE id=?`, uid).Scan(&g)
	return g == 1, err
}

// guestJoin creates a guest account from a guest invite and signs it in.
func (s *Server) guestJoin(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		InviteCode      string `json:"inviteCode"`
		DisplayName     string `json:"displayName"`
		DeviceName      string `json:"deviceName"`
		DevicePublicKey string `json:"devicePublicKey"`
	}
	if err := s.checkWebLogin(r); err != nil {
		return err
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if !s.lim.guest.Allow("ip" + s.clientIP(r)) {
		return errRateLimited
	}
	ctx := r.Context()
	display := cleanName(req.DisplayName, 32)
	if display == "" {
		return badRequest("invalid_display_name")
	}
	pub, err := decodeKey(req.DevicePublicKey)
	if err != nil {
		return err
	}
	iv, err := s.loadInvite(ctx, s.db.R, req.InviteCode)
	if err != nil {
		return err
	}
	if err := s.inviteUsable(ctx, iv); err != nil {
		return err
	}
	if ok, err := s.inviteAdmitsGuests(ctx, s.db.R, iv); err != nil {
		return err
	} else if !ok {
		return errf(http.StatusForbidden, "guest_not_allowed", "this invite does not allow guests")
	}
	userID, deviceID := store.NewID(), store.NewID()
	// Never shown as a sign-in name; only has to be unique.
	username := "guest_" + strings.ToLower(auth.RandomCode(12))
	var token string
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.consumeInvite(ctx, tx, iv.Code); err != nil {
			return err
		}
		var members int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_members WHERE space_id=?`, iv.SpaceID).Scan(&members); err != nil {
			return err
		}
		if members >= s.cfg.MaxSpaceMembers {
			return errf(http.StatusForbidden, "space_full", "space is full")
		}
		now := s.nowMs()
		if _, err := tx.ExecContext(ctx, `INSERT INTO users(id, username, display_name, password_hash, created_at, is_guest) VALUES (?,?,?,'',?,1)`,
			userID, username, display, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO devices(id, user_id, name, public_key, created_at, last_seen_at) VALUES (?,?,?,?,?,?)`,
			deviceID, userID, cleanName(req.DeviceName, 64), pub, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id, user_id, role, joined_at) VALUES (?,?,?,?)`, iv.SpaceID, userID, "member", now); err != nil {
			return err
		}
		var err error
		token, err = s.newSession(ctx, tx, userID, deviceID, r.UserAgent(), guestSessionTTL)
		return err
	})
	if err != nil {
		return err
	}
	s.afterMemberJoined(ctx, iv.SpaceID, userID, deviceID)
	me, err := s.loadMe(ctx, userID)
	if err != nil {
		return err
	}
	writeJSON(w, 201, authResponse{Token: s.issueToken(w, r, token), User: me, Device: deviceDTO{ID(deviceID), cleanName(req.DeviceName, 64)}})
	return nil
}

// inviteAdmitsGuests reports whether iv lets people in without an account.
// A guest is a new account on the server, so the signup policy (LS_SIGNUP)
// applies to guest invites exactly as it does to registration.
func (s *Server) inviteAdmitsGuests(ctx context.Context, q querier, iv *inviteRow) (bool, error) {
	if !iv.Guest || iv.SpaceID == 0 {
		return false, nil
	}
	return s.inviteCreatesAccounts(ctx, q, iv)
}

// removeGuest removes uid if it is a guest account (no-op otherwise).
func (s *Server) removeGuest(ctx context.Context, uid int64) {
	var g int
	err := s.db.R.QueryRowContext(ctx, `SELECT is_guest FROM users WHERE id=? AND deleted_at IS NULL`, uid).Scan(&g)
	if err != nil || g != 1 {
		return
	}
	if err := s.removeAccount(ctx, uid); err != nil {
		s.log.Warn("guest removal failed", "err", err)
		return
	}
	// A guest who never wrote anything leaves no trace at all; otherwise the
	// anonymised row stays as the author of their messages.
	_, _ = s.db.W.ExecContext(ctx, `DELETE FROM users WHERE id=?1 AND is_guest=1
		AND NOT EXISTS (SELECT 1 FROM messages WHERE author_id=?1)
		AND NOT EXISTS (SELECT 1 FROM attachments WHERE uploader_id=?1)`, uid)
}

// purgeGuests removes guests whose session is gone (expired, revoked) or who
// are in no space any more (space deleted).
func (s *Server) purgeGuests(ctx context.Context) int {
	ids, err := queryIDs(ctx, s.db.R, `SELECT id FROM users WHERE is_guest=1 AND deleted_at IS NULL AND (
		NOT EXISTS (SELECT 1 FROM sessions x WHERE x.user_id=users.id AND x.revoked_at IS NULL AND x.expires_at>?)
		OR NOT EXISTS (SELECT 1 FROM space_members m WHERE m.user_id=users.id))`, s.nowMs())
	if err != nil {
		s.log.Warn("guest purge failed", "err", err)
		return 0
	}
	for _, id := range ids {
		s.removeGuest(ctx, id)
	}
	return len(ids)
}
