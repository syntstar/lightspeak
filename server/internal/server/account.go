package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/syntstar/lightspeak/server/internal/auth"
	"github.com/syntstar/lightspeak/server/internal/config"
	"github.com/syntstar/lightspeak/server/internal/store"
)

func (s *Server) routesAccount(r chi.Router) {
	r.Post("/auth/logout", s.h(s.logout))
	r.Get("/users/@me", s.h(s.getMe))
	r.Patch("/users/@me", s.h(s.patchMe))
	r.Post("/users/@me/password", s.h(s.changePassword))
	r.Post("/users/@me/delete", s.h(s.deleteAccount))
	r.Post("/users/@me/avatar", s.h(s.uploadAvatar))
	r.Delete("/users/@me/avatar", s.h(s.deleteAvatar))
	r.Get("/users/@me/sessions", s.h(s.listSessions))
	r.Delete("/users/@me/sessions/{id}", s.h(s.revokeSession))
	r.Get("/users/@me/settings", s.h(s.getSettings))
	r.Put("/users/@me/settings", s.h(s.putSettings))
	r.Get("/users/{id}", s.h(s.getUser))
	r.Post("/devices", s.h(s.registerDevice))
	r.Delete("/devices/{id}", s.h(s.revokeDevice))
	r.Get("/relationships", s.h(s.listRelationships))
	r.Post("/relationships", s.h(s.friendRequestByName))
	r.Put("/relationships/{id}", s.h(s.putRelationship))
	r.Delete("/relationships/{id}", s.h(s.deleteRelationship))
	r.Post("/invites/{code}/accept", s.h(s.acceptInvite))
	r.Get("/admin/storage", s.h(s.adminStorage))
	r.Delete("/admin/files/{id}", s.h(s.adminDeleteFile))
}

// ---------- invites ----------

type inviteRow struct {
	Code        string
	SpaceID     int64
	MaxUses     sql.NullInt64
	Uses        int64
	ExpiresAt   sql.NullInt64
	RevokedAt   sql.NullInt64
	GrantsAdmin bool
	CreatedBy   int64
	CreatedAt   int64
}

func (s *Server) loadInvite(ctx context.Context, q querier, code string) (*inviteRow, error) {
	if !validInviteCode(code) {
		return nil, notFound("invite_invalid")
	}
	var iv inviteRow
	var space, by sql.NullInt64
	var ga int
	err := q.QueryRowContext(ctx, `SELECT code, space_id, max_uses, uses, expires_at, revoked_at, grants_admin, created_by, created_at FROM invites WHERE code=?`, code).
		Scan(&iv.Code, &space, &iv.MaxUses, &iv.Uses, &iv.ExpiresAt, &iv.RevokedAt, &ga, &by, &iv.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("invite_invalid")
	}
	if err != nil {
		return nil, err
	}
	iv.SpaceID, iv.CreatedBy, iv.GrantsAdmin = space.Int64, by.Int64, ga == 1
	return &iv, nil
}

func (s *Server) inviteUsable(ctx context.Context, iv *inviteRow) error {
	if iv.RevokedAt.Valid {
		return notFound("invite_invalid")
	}
	// LS_INVITES=admins also ends links other people made before the change.
	if s.cfg.Invites == config.InvitesAdmins {
		if ok, err := s.inviteFromAdmin(ctx, s.db.R, iv); err != nil {
			return err
		} else if !ok {
			return notFound("invite_invalid")
		}
	}
	if iv.ExpiresAt.Valid && iv.ExpiresAt.Int64 <= s.nowMs() {
		return errf(http.StatusGone, "invite_expired", "invite expired")
	}
	if iv.MaxUses.Valid && iv.Uses >= iv.MaxUses.Int64 {
		return errf(http.StatusGone, "invite_exhausted", "invite used up")
	}
	return nil
}

// inviteCreatesAccounts reports whether iv may create a new account under the
// server's signup policy (LS_SIGNUP). Invites always work for joining a space
// with an existing account; this only gates registration.
func (s *Server) inviteCreatesAccounts(ctx context.Context, q querier, iv *inviteRow) (bool, error) {
	if iv.GrantsAdmin {
		return true, nil // owner invite from `admin bootstrap`
	}
	switch s.cfg.Signup {
	case config.SignupInvite:
		return true, nil
	case config.SignupClosed:
		return false, nil
	}
	return s.inviteFromAdmin(ctx, q, iv)
}

// inviteFromAdmin reports whether iv comes from the operator: the command
// line (no creator), an owner invite or a current server admin.
func (s *Server) inviteFromAdmin(ctx context.Context, q querier, iv *inviteRow) (bool, error) {
	if iv.CreatedBy == 0 || iv.GrantsAdmin {
		return true, nil
	}
	var admin int
	err := q.QueryRowContext(ctx, `SELECT is_server_admin FROM users WHERE id=? AND disabled_at IS NULL`, iv.CreatedBy).Scan(&admin)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return admin == 1, err
}

func validInviteCode(c string) bool {
	if len(c) < 8 || len(c) > 32 {
		return false
	}
	for _, r := range c {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// consumeInvite atomically increments uses if the invite is still usable.
func (s *Server) consumeInvite(ctx context.Context, tx *sql.Tx, code string) error {
	res, err := tx.ExecContext(ctx, `UPDATE invites SET uses=uses+1 WHERE code=? AND revoked_at IS NULL
		AND (expires_at IS NULL OR expires_at>?) AND (max_uses IS NULL OR uses<max_uses)`, code, s.nowMs())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errf(http.StatusGone, "invite_expired", "invite no longer valid")
	}
	return nil
}

func (s *Server) getInvite(w http.ResponseWriter, r *http.Request) error {
	if !s.lim.invitePreview.Allow("ip" + s.clientIP(r)) {
		return errRateLimited
	}
	iv, err := s.loadInvite(r.Context(), s.db.R, chi.URLParam(r, "code"))
	if err != nil {
		return err
	}
	if err := s.inviteUsable(r.Context(), iv); err != nil {
		return err
	}
	signup, err := s.inviteCreatesAccounts(r.Context(), s.db.R, iv)
	if err != nil {
		return err
	}
	out := map[string]any{"code": iv.Code, "expiresAt": nullInt(iv.ExpiresAt), "signup": signup}
	if iv.SpaceID != 0 {
		var name string
		var icon sql.NullString
		var members int
		if err := s.db.R.QueryRowContext(r.Context(), `SELECT name, icon_key, (SELECT COUNT(*) FROM space_members WHERE space_id=spaces.id) FROM spaces WHERE id=?`, iv.SpaceID).
			Scan(&name, &icon, &members); err != nil {
			return notFound("invite_invalid")
		}
		out["space"] = map[string]any{"id": ID(iv.SpaceID), "name": name, "iconUrl": iconURL(icon), "memberCount": members}
	}
	writeJSON(w, 200, out)
	return nil
}

func nullInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func iconURL(key sql.NullString) string { return avatarURL(key) }

// ---------- register / login ----------

type deviceDTO struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

type authResponse struct {
	Token  string    `json:"token"`
	User   Me        `json:"user"`
	Device deviceDTO `json:"device"`
}

func decodeKey(b64 string) ([]byte, error) {
	if b64 == "" {
		return nil, nil
	}
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(k) != 32 {
		return nil, badRequest("invalid_public_key")
	}
	return k, nil
}

func (s *Server) newSession(ctx context.Context, tx *sql.Tx, userID, deviceID int64, ua string) (string, error) {
	tok, hash, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	now := s.nowMs()
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions(id, user_id, device_id, token_hash, created_at, last_used_at, expires_at, user_agent) VALUES (?,?,?,?,?,?,?,?)`,
		store.NewID(), userID, deviceID, hash, now, now, now+sessionTTL.Milliseconds(), clampText(ua, 200))
	return tok, err
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		InviteCode      string `json:"inviteCode"`
		Username        string `json:"username"`
		DisplayName     string `json:"displayName"`
		Password        string `json:"password"`
		DeviceName      string `json:"deviceName"`
		DevicePublicKey string `json:"devicePublicKey"`
	}
	if err := s.checkWebLogin(r); err != nil {
		return err
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if !s.lim.register.Allow("ip" + s.clientIP(r)) {
		return errRateLimited
	}
	ctx := r.Context()
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if !auth.ValidUsername(username) {
		return badRequest("invalid_username")
	}
	if code := auth.CheckPassword(req.Password, username); code != "" {
		return badRequest(code)
	}
	display := cleanName(req.DisplayName, 32)
	if display == "" {
		display = username
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
	if ok, err := s.inviteCreatesAccounts(ctx, s.db.R, iv); err != nil {
		return err
	} else if !ok {
		return errf(http.StatusForbidden, "signup_restricted", "this invite cannot create new accounts")
	}
	hash, err := auth.Hash(req.Password, s.argon)
	if err != nil {
		return err
	}
	userID, deviceID := store.NewID(), store.NewID()
	var token string
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.consumeInvite(ctx, tx, iv.Code); err != nil {
			return err
		}
		var taken int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username=?`, username).Scan(&taken); err != nil {
			return err
		}
		if taken > 0 {
			return conflict("username_taken")
		}
		now := s.nowMs()
		admin := 0
		if iv.GrantsAdmin {
			admin = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO users(id, username, display_name, password_hash, created_at, is_server_admin) VALUES (?,?,?,?,?,?)`,
			userID, username, display, hash, now, admin); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO devices(id, user_id, name, public_key, created_at, last_seen_at) VALUES (?,?,?,?,?,?)`,
			deviceID, userID, cleanName(req.DeviceName, 64), pub, now, now); err != nil {
			return err
		}
		if iv.SpaceID != 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id, user_id, role, joined_at) VALUES (?,?,?,?)`, iv.SpaceID, userID, "member", now); err != nil {
				return err
			}
		}
		token, err = s.newSession(ctx, tx, userID, deviceID, r.UserAgent())
		return err
	})
	if err != nil {
		return err
	}
	if iv.GrantsAdmin {
		s.audit(ctx, userID, 0, "server_admin_registered", userID, nil)
	}
	if iv.SpaceID != 0 {
		s.afterMemberJoined(ctx, iv.SpaceID, userID, deviceID)
	}
	me, err := s.loadMe(ctx, userID)
	if err != nil {
		return err
	}
	writeJSON(w, 201, authResponse{Token: s.issueToken(w, r, token), User: me, Device: deviceDTO{ID(deviceID), cleanName(req.DeviceName, 64)}})
	return nil
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		DeviceName      string `json:"deviceName"`
		DevicePublicKey string `json:"devicePublicKey"`
		DeviceID        ID     `json:"deviceId"`
	}
	if err := s.checkWebLogin(r); err != nil {
		return err
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if !s.lim.login.Allow("ip"+s.clientIP(r)) || !s.lim.login.Allow("user"+username) {
		return errRateLimited
	}
	if len(req.Password) > 256 {
		return errf(http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
	}
	ctx := r.Context()
	pub, err := decodeKey(req.DevicePublicKey)
	if err != nil {
		return err
	}
	var userID int64
	var hash string
	var disabled sql.NullInt64
	err = s.db.R.QueryRowContext(ctx, `SELECT id, password_hash, disabled_at FROM users WHERE username=?`, username).Scan(&userID, &hash, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		auth.VerifyDummy(s.argon)
		return errf(http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
	}
	if err != nil {
		return err
	}
	if auth.Verify(req.Password, hash) != nil {
		return errf(http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
	}
	if disabled.Valid {
		return errf(http.StatusForbidden, "account_disabled", "account disabled")
	}
	deviceID := int64(req.DeviceID)
	name := cleanName(req.DeviceName, 64)
	var token string
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := s.nowMs()
		if deviceID != 0 {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE id=? AND user_id=? AND revoked_at IS NULL`, deviceID, userID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				deviceID = 0
			}
		}
		if deviceID == 0 {
			deviceID = store.NewID()
			if _, err := tx.ExecContext(ctx, `INSERT INTO devices(id, user_id, name, public_key, created_at, last_seen_at) VALUES (?,?,?,?,?,?)`,
				deviceID, userID, name, pub, now, now); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE devices SET last_seen_at=?, name=CASE WHEN ?<>'' THEN ? ELSE name END WHERE id=?`, now, name, name, deviceID); err != nil {
				return err
			}
		}
		var err error
		token, err = s.newSession(ctx, tx, userID, deviceID, r.UserAgent())
		return err
	})
	if err != nil {
		return err
	}
	me, err := s.loadMe(ctx, userID)
	if err != nil {
		return err
	}
	var dname string
	_ = s.db.R.QueryRowContext(ctx, `SELECT name FROM devices WHERE id=?`, deviceID).Scan(&dname)
	writeJSON(w, 200, authResponse{Token: s.issueToken(w, r, token), User: me, Device: deviceDTO{ID(deviceID), dname}})
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	se := sess(r)
	if _, err := s.db.W.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=? WHERE id=?`, s.nowMs(), se.ID); err != nil {
		return err
	}
	s.hub.dropAuthSession(se.ID)
	if isWebClient(r) {
		s.clearSessionCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- profile ----------

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) error {
	me, err := s.loadMe(r.Context(), sess(r).UserID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, me)
	return nil
}

func (s *Server) patchMe(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		DisplayName *string `json:"displayName"`
		Username    *string `json:"username"`
		Status      *string `json:"status"`
		StatusText  *string `json:"statusText"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if req.DisplayName != nil {
			d := cleanName(*req.DisplayName, 32)
			if d == "" {
				return badRequest("invalid_display_name")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE users SET display_name=? WHERE id=?`, d, se.UserID); err != nil {
				return err
			}
		}
		if req.Username != nil {
			u := strings.ToLower(strings.TrimSpace(*req.Username))
			if !auth.ValidUsername(u) {
				return badRequest("invalid_username")
			}
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username=? AND id<>?`, u, se.UserID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return conflict("username_taken")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE users SET username=? WHERE id=?`, u, se.UserID); err != nil {
				return err
			}
		}
		if req.Status != nil {
			if !validStatus(*req.Status) {
				return badRequest("invalid_status")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE users SET status=? WHERE id=?`, *req.Status, se.UserID); err != nil {
				return err
			}
		}
		if req.StatusText != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET status_text=? WHERE id=?`, clampText(*req.StatusText, 128), se.UserID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	me, err := s.loadMe(ctx, se.UserID)
	if err != nil {
		return err
	}
	s.broadcastUser(ctx, se.UserID)
	if req.Status != nil || req.StatusText != nil {
		s.broadcastPresence(ctx, se.UserID)
	}
	writeJSON(w, 200, me)
	return nil
}

// broadcastUser sends user_update (public profile) to everyone related.
func (s *Server) broadcastUser(ctx context.Context, uid int64) {
	u, err := s.loadUser(ctx, uid)
	if err != nil {
		return
	}
	ids, err := s.relatedUsers(ctx, uid)
	if err != nil {
		return
	}
	ids = append(ids, uid)
	s.hub.toUsers(ids, "user_update", u)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		OldPassword         string `json:"oldPassword"`
		NewPassword         string `json:"newPassword"`
		RevokeOtherSessions bool   `json:"revokeOtherSessions"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	if !s.lim.login.Allow("pw" + ID(se.UserID).String()) {
		return errRateLimited
	}
	var hash, username string
	if err := s.db.R.QueryRowContext(ctx, `SELECT password_hash, username FROM users WHERE id=?`, se.UserID).Scan(&hash, &username); err != nil {
		return err
	}
	if len(req.OldPassword) > 256 || auth.Verify(req.OldPassword, hash) != nil {
		return errf(http.StatusForbidden, "wrong_password", "wrong password")
	}
	if code := auth.CheckPassword(req.NewPassword, username); code != "" {
		return badRequest(code)
	}
	nh, err := auth.Hash(req.NewPassword, s.argon)
	if err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=?`, nh, se.UserID); err != nil {
		return err
	}
	if req.RevokeOtherSessions {
		ids, err := queryIDs(ctx, s.db.R, `SELECT id FROM sessions WHERE user_id=? AND id<>? AND revoked_at IS NULL`, se.UserID, se.ID)
		if err != nil {
			return err
		}
		if _, err := s.db.W.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE user_id=? AND id<>? AND revoked_at IS NULL`, s.nowMs(), se.UserID, se.ID); err != nil {
			return err
		}
		for _, id := range ids {
			s.hub.dropAuthSession(id)
		}
	}
	s.audit(ctx, se.UserID, 0, "password_changed", se.UserID, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) error {
	se := sess(r)
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT s.id, COALESCE(d.name,''), s.created_at, s.last_used_at, s.user_agent
		FROM sessions s LEFT JOIN devices d ON d.id=s.device_id
		WHERE s.user_id=? AND s.revoked_at IS NULL AND s.expires_at>? ORDER BY s.last_used_at DESC`, se.UserID, s.nowMs())
	if err != nil {
		return err
	}
	defer rows.Close()
	type sessDTO struct {
		ID         ID     `json:"id"`
		DeviceName string `json:"deviceName"`
		CreatedAt  int64  `json:"createdAt"`
		LastUsedAt int64  `json:"lastUsedAt"`
		UserAgent  string `json:"userAgent"`
		Current    bool   `json:"current"`
	}
	out := []sessDTO{}
	for rows.Next() {
		var d sessDTO
		var id int64
		if err := rows.Scan(&id, &d.DeviceName, &d.CreatedAt, &d.LastUsedAt, &d.UserAgent); err != nil {
			return err
		}
		d.ID, d.Current = ID(id), id == se.ID
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) error {
	se := sess(r)
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	res, err := s.db.W.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=? WHERE id=? AND user_id=? AND revoked_at IS NULL`, s.nowMs(), id, se.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("unknown_session")
	}
	s.hub.dropAuthSession(id)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- settings ----------

const maxSettingsBytes = 16 << 10

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) error {
	var js string
	var at int64
	err := s.db.R.QueryRowContext(r.Context(), `SELECT json, updated_at FROM user_settings WHERE user_id=?`, sess(r).UserID).Scan(&js, &at)
	if errors.Is(err, sql.ErrNoRows) {
		js, at = "{}", 0
	} else if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"settings": json.RawMessage(js), "updatedAt": at})
	return nil
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSettingsBytes+1))
	if err != nil {
		return errBadRequest
	}
	if len(body) > maxSettingsBytes {
		return errf(http.StatusRequestEntityTooLarge, "settings_too_large", "settings too large")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil || obj == nil {
		return badRequest("invalid_settings")
	}
	compact, _ := json.Marshal(obj)
	se, now := sess(r), s.nowMs()
	if _, err := s.db.W.ExecContext(r.Context(), `INSERT INTO user_settings(user_id, json, updated_at) VALUES (?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET json=excluded.json, updated_at=excluded.updated_at`, se.UserID, string(compact), now); err != nil {
		return err
	}
	s.hub.toUser(se.UserID, "settings_update", map[string]any{"settings": json.RawMessage(compact), "updatedAt": now})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- users, devices ----------

type devicePub struct {
	ID        ID     `json:"id"`
	PublicKey string `json:"publicKey"`
}

func (s *Server) userDevices(ctx context.Context, uid int64) ([]devicePub, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, public_key FROM devices WHERE user_id=? AND revoked_at IS NULL AND public_key IS NOT NULL ORDER BY id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []devicePub{}
	for rows.Next() {
		var id int64
		var k []byte
		if err := rows.Scan(&id, &k); err != nil {
			return nil, err
		}
		out = append(out, devicePub{ID(id), base64.StdEncoding.EncodeToString(k)})
	}
	return out, rows.Err()
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	ok, err := s.related(ctx, se.UserID, id)
	if err != nil {
		return err
	}
	if !ok {
		return notFound("unknown_user")
	}
	u, err := s.loadUser(ctx, id)
	if err != nil {
		return err
	}
	devs, err := s.userDevices(ctx, id)
	if err != nil {
		return err
	}
	var kind sql.NullString
	_ = s.db.R.QueryRowContext(ctx, `SELECT kind FROM relationships WHERE user_id=? AND other_id=?`, se.UserID, id).Scan(&kind)
	writeJSON(w, 200, map[string]any{"user": u, "devices": devs, "relationship": kind.String, "presence": s.effectivePresence(ctx, id)})
	return nil
}

func (s *Server) registerDevice(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name      string `json:"name"`
		PublicKey string `json:"publicKey"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	pub, err := decodeKey(req.PublicKey)
	if err != nil {
		return err
	}
	if pub == nil {
		return badRequest("invalid_public_key")
	}
	se, ctx := sess(r), r.Context()
	var existing []byte
	if se.DeviceID != 0 {
		if err := s.db.R.QueryRowContext(ctx, `SELECT public_key FROM devices WHERE id=?`, se.DeviceID).Scan(&existing); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if se.DeviceID != 0 && existing == nil {
		if _, err := s.db.W.ExecContext(ctx, `UPDATE devices SET public_key=? WHERE id=? AND public_key IS NULL`, pub, se.DeviceID); err != nil {
			return err
		}
		s.afterDeviceAdded(ctx, se.UserID, se.DeviceID)
		writeJSON(w, 200, deviceDTO{ID(se.DeviceID), cleanName(req.Name, 64)})
		return nil
	}
	// Key replaced (e.g. credential store lost): new device, old one revoked.
	newID, now, old := store.NewID(), s.nowMs(), se.DeviceID
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO devices(id, user_id, name, public_key, created_at, last_seen_at) VALUES (?,?,?,?,?,?)`,
			newID, se.UserID, cleanName(req.Name, 64), pub, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET device_id=? WHERE id=?`, newID, se.ID); err != nil {
			return err
		}
		if old != 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE devices SET revoked_at=? WHERE id=?`, now, old); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM media_key_envelopes WHERE device_id=?`, old); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if old != 0 {
		s.afterDeviceRevoked(ctx, se.UserID)
	}
	s.afterDeviceAdded(ctx, se.UserID, newID)
	writeJSON(w, 201, deviceDTO{ID(newID), cleanName(req.Name, 64)})
	return nil
}

func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var sessions []int64
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE devices SET revoked_at=? WHERE id=? AND user_id=? AND revoked_at IS NULL`, s.nowMs(), id, se.UserID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("unknown_device")
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM sessions WHERE device_id=? AND revoked_at IS NULL`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sid int64
			if err := rows.Scan(&sid); err != nil {
				rows.Close()
				return err
			}
			sessions = append(sessions, sid)
		}
		rows.Close()
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE device_id=? AND revoked_at IS NULL`, s.nowMs(), id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM media_key_envelopes WHERE device_id=?`, id)
		return err
	})
	if err != nil {
		return err
	}
	for _, sid := range sessions {
		s.hub.dropAuthSession(sid)
	}
	s.afterDeviceRevoked(ctx, se.UserID)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- relationships ----------

type relDTO struct {
	UserID ID     `json:"userId"`
	Kind   string `json:"kind"`
	User   User   `json:"user"`
}

func (s *Server) listRelationships(w http.ResponseWriter, r *http.Request) error {
	out, err := s.relationships(r.Context(), sess(r).UserID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) relationships(ctx context.Context, uid int64) ([]relDTO, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT r.kind, u.id, u.username, u.display_name, u.avatar_key FROM relationships r JOIN users u ON u.id=r.other_id WHERE r.user_id=?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []relDTO{}
	for rows.Next() {
		var d relDTO
		var id int64
		var av sql.NullString
		if err := rows.Scan(&d.Kind, &id, &d.User.Username, &d.User.DisplayName, &av); err != nil {
			return nil, err
		}
		d.User.ID, d.UserID, d.User.AvatarURL = ID(id), ID(id), avatarURL(av)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Server) relKind(ctx context.Context, q querier, a, b int64) (string, error) {
	var k string
	err := q.QueryRowContext(ctx, `SELECT kind FROM relationships WHERE user_id=? AND other_id=?`, a, b).Scan(&k)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return k, err
}

func (s *Server) emitRel(ctx context.Context, owner, other int64, kind string) {
	u, err := s.loadUser(ctx, other)
	if err != nil {
		return
	}
	d := map[string]any{"userId": ID(other), "kind": kind, "user": u}
	if kind == "friend" {
		// New friends may not have shared a space before: no presence yet.
		d["presence"] = s.effectivePresence(ctx, other)
	}
	s.hub.toUser(owner, "relationship_update", d)
}

func (s *Server) friendRequestByName(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Username string `json:"username"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	var id int64
	err := s.db.R.QueryRowContext(r.Context(), `SELECT id FROM users WHERE username=? AND disabled_at IS NULL`, strings.ToLower(strings.TrimSpace(req.Username))).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound("unknown_user")
	}
	if err != nil {
		return err
	}
	return s.setRelationship(w, r, id, "friend")
}

func (s *Server) putRelationship(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Kind string `json:"kind"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Kind != "friend" && req.Kind != "blocked" {
		return badRequest("invalid_kind")
	}
	if req.Kind == "friend" {
		// Friend requests by ID only to users one already knows.
		ok, err := s.related(r.Context(), sess(r).UserID, id)
		if err != nil {
			return err
		}
		if !ok {
			return notFound("unknown_user")
		}
	}
	return s.setRelationship(w, r, id, req.Kind)
}

func (s *Server) setRelationship(w http.ResponseWriter, r *http.Request, other int64, kind string) error {
	se, ctx := sess(r), r.Context()
	me := se.UserID
	if other == me {
		return badRequest("cannot_relate_self")
	}
	var exists int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=?`, other).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return notFound("unknown_user")
	}
	var mine, theirs string
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := s.nowMs()
		cur, err := s.relKind(ctx, tx, me, other)
		if err != nil {
			return err
		}
		back, err := s.relKind(ctx, tx, other, me)
		if err != nil {
			return err
		}
		set := func(a, b int64, k string) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO relationships(user_id, other_id, kind, created_at) VALUES (?,?,?,?)
				ON CONFLICT(user_id, other_id) DO UPDATE SET kind=excluded.kind`, a, b, k, now)
			return err
		}
		if kind == "blocked" {
			mine = "blocked"
			if err := set(me, other, "blocked"); err != nil {
				return err
			}
			if back != "blocked" && back != "" {
				theirs = "none"
				_, err := tx.ExecContext(ctx, `DELETE FROM relationships WHERE user_id=? AND other_id=?`, other, me)
				return err
			}
			return nil
		}
		// friend
		if back == "blocked" {
			return errf(http.StatusForbidden, "blocked", "cannot add this user")
		}
		switch {
		case cur == "friend":
			mine = "friend"
			return nil
		case back == "pending_out":
			mine, theirs = "friend", "friend"
			if err := set(me, other, "friend"); err != nil {
				return err
			}
			return set(other, me, "friend")
		default:
			mine, theirs = "pending_out", "pending_in"
			if err := set(me, other, "pending_out"); err != nil {
				return err
			}
			return set(other, me, "pending_in")
		}
	})
	if err != nil {
		return err
	}
	if mine != "" {
		s.emitRel(ctx, me, other, mine)
	}
	if theirs != "" {
		s.emitRel(ctx, other, me, theirs)
	}
	writeJSON(w, 200, map[string]any{"userId": ID(other), "kind": mine})
	return nil
}

func (s *Server) deleteRelationship(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	other, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var notifyOther bool
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		back, err := s.relKind(ctx, tx, other, se.UserID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM relationships WHERE user_id=? AND other_id=?`, se.UserID, other); err != nil {
			return err
		}
		if back != "" && back != "blocked" {
			notifyOther = true
			_, err := tx.ExecContext(ctx, `DELETE FROM relationships WHERE user_id=? AND other_id=?`, other, se.UserID)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.emitRel(ctx, se.UserID, other, "none")
	if notifyOther {
		s.emitRel(ctx, other, se.UserID, "none")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
