package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// E2EE key distribution. The server only stores sealed-box envelopes (media
// key encrypted to each device's X25519 public key) and never sees a key.
// Containers are spaces (all voice channels of a space) and DM channels.

const (
	maxEnvelopes      = 2000 // one rotation: every device of up to 1000 members
	minEnvelopeBytes  = 48
	maxEnvelopeBytes  = 256
	rotatePendingPref = "rotate:"
)

func (s *Server) routesE2EE(r chi.Router) {
	r.Get("/e2ee/{cid}/envelope", s.h(s.getEnvelope))
	r.Post("/e2ee/{cid}/envelopes", s.h(s.postEnvelopes))
	r.Post("/e2ee/{cid}/rotate", s.h(s.rotate))
	r.Get("/e2ee/{cid}/devices", s.h(s.containerDevices))
}

func (s *Server) getEnvelope(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	cid, err := pathID(r, "cid")
	if err != nil {
		return err
	}
	isDM, current, err := s.containerAccess(ctx, se, cid)
	if err != nil {
		return err
	}
	epoch := current
	if e, ok := queryInt(r, "epoch"); ok && e > 0 {
		epoch = e
	}
	if se.DeviceID == 0 {
		return badRequest("device_key_missing")
	}
	var ct []byte
	var by int64
	err = s.db.R.QueryRowContext(ctx, `SELECT ciphertext, created_by_device FROM media_key_envelopes WHERE container_id=? AND epoch=? AND device_id=?`,
		cid, epoch, se.DeviceID).Scan(&ct, &by)
	if err == nil {
		writeJSON(w, 200, map[string]any{"containerId": ID(cid), "epoch": epoch, "currentEpoch": current,
			"ciphertext": base64.StdEncoding.EncodeToString(ct), "createdByDevice": ID(by)})
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var n int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_key_envelopes WHERE container_id=? AND epoch=?`, cid, epoch).Scan(&n); err != nil {
		return err
	}
	if n == 0 && epoch == current {
		return errf(http.StatusNotFound, "no_key_yet", "no key exists yet; create one")
	}
	var hasKey int
	_ = s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE id=? AND public_key IS NOT NULL AND revoked_at IS NULL`, se.DeviceID).Scan(&hasKey)
	if hasKey == 0 {
		return badRequest("device_key_missing")
	}
	if epoch == current && s.typing.Allow("keyreq:"+ID(cid).String()+":"+ID(se.DeviceID).String()) {
		if s.keyHolderOnline(ctx, cid, epoch) {
			s.requestKeys(ctx, cid, isDM, []int64{se.DeviceID})
		} else {
			// No connected device can share this epoch (every holder is
			// offline, reinstalled or gone): without a new epoch nobody
			// could ever talk. Rotation seals a fresh key to the current
			// member devices – the same set key_request would serve – so
			// it adds no trust and there is still no unencrypted path.
			s.requestRotation(ctx, cid, isDM)
		}
	}
	return errf(http.StatusNotFound, "envelope_missing", "waiting for a member to share the key")
}

type envelopeIn struct {
	DeviceID   ID     `json:"deviceId"`
	Ciphertext string `json:"ciphertext"`
}

type validEnvelope struct {
	device int64
	owner  int64
	ct     []byte
}

// validateEnvelopes checks every target device belongs to a current member
// of the container, is not revoked and has a public key.
func (s *Server) validateEnvelopes(ctx context.Context, q querier, cid int64, isDM bool, in []envelopeIn) ([]validEnvelope, error) {
	if len(in) == 0 || len(in) > maxEnvelopes {
		return nil, badRequest("invalid_envelopes")
	}
	memberQ := `SELECT user_id FROM space_members WHERE space_id=?`
	if isDM {
		memberQ = `SELECT user_id FROM channel_members WHERE channel_id=?`
	}
	seen := map[int64]bool{}
	out := make([]validEnvelope, 0, len(in))
	for _, e := range in {
		id := int64(e.DeviceID)
		if seen[id] {
			return nil, badRequest("invalid_envelopes")
		}
		seen[id] = true
		ct, err := base64.StdEncoding.DecodeString(e.Ciphertext)
		if err != nil || len(ct) < minEnvelopeBytes || len(ct) > maxEnvelopeBytes {
			return nil, badRequest("invalid_envelopes")
		}
		var owner int64
		err = q.QueryRowContext(ctx, `SELECT d.user_id FROM devices d WHERE d.id=? AND d.revoked_at IS NULL AND d.public_key IS NOT NULL
			AND EXISTS (SELECT 1 FROM users u WHERE u.id=d.user_id AND u.disabled_at IS NULL) AND d.user_id IN (`+memberQ+`)`, id, cid).Scan(&owner)
		if err != nil {
			return nil, badRequest("invalid_envelope_device")
		}
		out = append(out, validEnvelope{id, owner, ct})
	}
	return out, nil
}

func (s *Server) postEnvelopes(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Epoch     int64        `json:"epoch"`
		Envelopes []envelopeIn `json:"envelopes"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	cid, err := pathID(r, "cid")
	if err != nil {
		return err
	}
	isDM, current, err := s.containerAccess(ctx, se, cid)
	if err != nil {
		return err
	}
	if req.Epoch != current {
		return errf(http.StatusConflict, "epoch_conflict", "epoch changed")
	}
	envs, err := s.validateEnvelopes(ctx, s.db.R, cid, isDM, req.Envelopes)
	if err != nil {
		return err
	}
	inserted := 0
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireKeySession(ctx, tx, se, s.nowMs()); err != nil {
			return err
		}
		dm, epoch, err := containerAccessQuery(ctx, tx, se, cid)
		if err != nil {
			return err
		}
		if dm != isDM || epoch != req.Epoch {
			return conflict("epoch_conflict")
		}
		envs, err = s.validateEnvelopes(ctx, tx, cid, isDM, req.Envelopes)
		if err != nil {
			return err
		}

		var total, mine int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(device_id=?),0) FROM media_key_envelopes WHERE container_id=? AND epoch=?`,
			se.DeviceID, cid, current).Scan(&total, &mine); err != nil {
			return err
		}
		if total == 0 {
			// Bootstrap: the creator must include its own device.
			own := false
			for _, e := range envs {
				own = own || e.device == se.DeviceID
			}
			if !own {
				return badRequest("missing_own_envelope")
			}
		} else if mine == 0 {
			// Only devices that hold the key may distribute it.
			return errf(http.StatusConflict, "key_exists", "a key already exists; fetch your envelope")
		}
		for _, e := range envs {
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO media_key_envelopes(container_id, epoch, device_id, ciphertext, created_by_device, created_at) VALUES (?,?,?,?,?,?)`,
				cid, current, e.device, e.ct, se.DeviceID, s.nowMs())
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				inserted++
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	owners := map[int64]bool{}
	var ids []int64
	for _, e := range envs {
		if !owners[e.owner] {
			owners[e.owner] = true
			ids = append(ids, e.owner)
		}
	}
	s.hub.toUsers(ids, "e2ee_key_available", map[string]any{"containerId": ID(cid), "epoch": current})
	writeJSON(w, 200, map[string]any{"inserted": inserted})
	return nil
}

func (s *Server) rotate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		NewEpoch  int64        `json:"newEpoch"`
		Envelopes []envelopeIn `json:"envelopes"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	cid, err := pathID(r, "cid")
	if err != nil {
		return err
	}
	isDM, current, err := s.containerAccess(ctx, se, cid)
	if err != nil {
		return err
	}
	// Rotations happen only when the server asked for one (member removed,
	// device revoked). Several clients race for it; the first wins, the rest
	// get rotation_not_pending instead of rotating a second time.
	if !s.rotationPending(ctx, cid) {
		return errf(http.StatusConflict, "rotation_not_pending", "no rotation pending")
	}
	if req.NewEpoch != current+1 {
		return errf(http.StatusConflict, "epoch_conflict", "epoch changed")
	}
	envs, err := s.validateEnvelopes(ctx, s.db.R, cid, isDM, req.Envelopes)
	if err != nil {
		return err
	}
	own := false
	for _, e := range envs {
		own = own || e.device == se.DeviceID
	}
	if !own {
		return badRequest("missing_own_envelope")
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := requireKeySession(ctx, tx, se, s.nowMs()); err != nil {
			return err
		}
		dm, epoch, err := containerAccessQuery(ctx, tx, se, cid)
		if err != nil {
			return err
		}
		if dm != isDM || epoch != req.NewEpoch-1 {
			return conflict("epoch_conflict")
		}
		envs, err = s.validateEnvelopes(ctx, tx, cid, isDM, req.Envelopes)
		if err != nil {
			return err
		}
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM meta WHERE key=?`, rotatePendingPref+ID(cid).String()).Scan(&pending); err != nil {
			return err
		}
		if pending != 1 {
			return conflict("rotation_not_pending")
		}

		var res sql.Result
		if isDM {
			res, err = tx.ExecContext(ctx, `UPDATE channels SET e2ee_epoch=? WHERE id=? AND e2ee_epoch=?`, req.NewEpoch, cid, current)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE spaces SET e2ee_epoch=? WHERE id=? AND e2ee_epoch=?`, req.NewEpoch, cid, current)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errf(http.StatusConflict, "epoch_conflict", "epoch changed")
		}
		for _, e := range envs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO media_key_envelopes(container_id, epoch, device_id, ciphertext, created_by_device, created_at) VALUES (?,?,?,?,?,?)`,
				cid, req.NewEpoch, e.device, e.ct, se.DeviceID, s.nowMs()); err != nil {
				return err
			}
		}
		// Keep the previous epoch for in-flight frames; older ones are useless.
		if _, err := tx.ExecContext(ctx, `DELETE FROM media_key_envelopes WHERE container_id=? AND epoch<?`, cid, req.NewEpoch-1); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM meta WHERE key=?`, rotatePendingPref+ID(cid).String())
		return err
	})
	if err != nil {
		return err
	}
	ids, err := s.containerMembers(ctx, cid, isDM)
	if err != nil {
		return err
	}
	s.hub.toUsers(ids, "e2ee_epoch_changed", map[string]any{"containerId": ID(cid), "epoch": req.NewEpoch})
	writeJSON(w, 200, map[string]any{"containerId": ID(cid), "epoch": req.NewEpoch})
	return nil
}

func (s *Server) containerDevices(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	cid, err := pathID(r, "cid")
	if err != nil {
		return err
	}
	isDM, epoch, err := s.containerAccess(ctx, se, cid)
	if err != nil {
		return err
	}
	memberQ := `SELECT user_id FROM space_members WHERE space_id=?`
	if isDM {
		memberQ = `SELECT user_id FROM channel_members WHERE channel_id=?`
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT d.id, d.user_id, d.public_key,
		EXISTS(SELECT 1 FROM media_key_envelopes e WHERE e.container_id=? AND e.epoch=? AND e.device_id=d.id)
		FROM devices d WHERE d.revoked_at IS NULL AND d.public_key IS NOT NULL AND EXISTS (SELECT 1 FROM users u WHERE u.id=d.user_id AND u.disabled_at IS NULL) AND d.user_id IN (`+memberQ+`) ORDER BY d.user_id, d.id`, cid, epoch, cid)
	if err != nil {
		return err
	}
	defer rows.Close()
	type dev struct {
		DeviceID    ID     `json:"deviceId"`
		UserID      ID     `json:"userId"`
		PublicKey   string `json:"publicKey"`
		HasEnvelope bool   `json:"hasEnvelope"`
	}
	out := []dev{}
	for rows.Next() {
		var d dev
		var id, uid int64
		var k []byte
		if err := rows.Scan(&id, &uid, &k, &d.HasEnvelope); err != nil {
			return err
		}
		d.DeviceID, d.UserID, d.PublicKey = ID(id), ID(uid), base64.StdEncoding.EncodeToString(k)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"containerId": ID(cid), "epoch": epoch, "devices": out})
	return nil
}

// ---------- server-initiated requests ----------

func (s *Server) containerEpoch(ctx context.Context, cid int64, isDM bool) int64 {
	var e int64
	q := `SELECT e2ee_epoch FROM spaces WHERE id=?`
	if isDM {
		q = `SELECT e2ee_epoch FROM channels WHERE id=?`
	}
	_ = s.db.R.QueryRowContext(ctx, q, cid).Scan(&e)
	return e
}

// keyHolderOnline reports whether a device holding an envelope of the epoch
// has a live gateway connection (and could answer a key request). Errs on
// the side of "online" so a database hiccup never forces a rotation.
func (s *Server) keyHolderOnline(ctx context.Context, cid, epoch int64) bool {
	ids, err := queryIDs(ctx, s.db.R, `SELECT device_id FROM media_key_envelopes WHERE container_id=? AND epoch=?`, cid, epoch)
	if err != nil {
		return true
	}
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return s.hub.anyDeviceOnline(set)
}

// requestKeys asks online members to create envelopes for the given devices.
func (s *Server) requestKeys(ctx context.Context, cid int64, isDM bool, deviceIDs []int64) {
	ids, err := s.containerMembers(ctx, cid, isDM)
	if err != nil {
		return
	}
	devs := make([]ID, len(deviceIDs))
	for i, d := range deviceIDs {
		devs[i] = ID(d)
	}
	s.hub.toUsers(ids, "e2ee_key_request", map[string]any{"containerId": ID(cid), "epoch": s.containerEpoch(ctx, cid, isDM), "deviceIds": devs})
}

// requestRotation marks a container as needing a new epoch and asks online
// members to rotate (admins for spaces, all participants for DMs; once
// pending, any member may rotate so a missing admin cannot block it).
func (s *Server) requestRotation(ctx context.Context, cid int64, isDM bool) {
	epoch := s.containerEpoch(ctx, cid, isDM)
	if epoch == 0 {
		return
	}
	_, _ = s.db.W.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		rotatePendingPref+ID(cid).String(), strconv.FormatInt(epoch, 10))
	ids, err := s.containerMembers(ctx, cid, isDM)
	if err != nil {
		return
	}
	s.hub.toUsers(ids, "e2ee_rotate_request", map[string]any{"containerId": ID(cid), "epoch": epoch})
}

func (s *Server) rotationPending(ctx context.Context, cid int64) bool {
	var n int
	_ = s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM meta WHERE key=?`, rotatePendingPref+ID(cid).String()).Scan(&n)
	return n > 0
}

// pendingRotations lists containers of the user that wait for a rotation.
func (s *Server) pendingRotations(ctx context.Context, uid int64) []ID {
	rows, err := s.db.R.QueryContext(ctx, `SELECT key FROM meta WHERE key LIKE 'rotate:%'`)
	if err != nil {
		return []ID{}
	}
	var cids []int64
	for rows.Next() {
		var k string
		if rows.Scan(&k) == nil {
			if id, err := strconv.ParseInt(strings.TrimPrefix(k, rotatePendingPref), 10, 64); err == nil {
				cids = append(cids, id)
			}
		}
	}
	rows.Close()
	out := []ID{}
	for _, cid := range cids {
		var n int
		_ = s.db.R.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM space_members WHERE space_id=? AND user_id=?) + (SELECT COUNT(*) FROM channel_members WHERE channel_id=? AND user_id=?)`,
			cid, uid, cid, uid).Scan(&n)
		if n > 0 {
			out = append(out, ID(cid))
		}
	}
	return out
}

func (s *Server) userContainers(ctx context.Context, uid int64) (spaces, dms []int64) {
	spaces, _ = queryIDs(ctx, s.db.R, `SELECT space_id FROM space_members WHERE user_id=?`, uid)
	dms, _ = queryIDs(ctx, s.db.R, `SELECT channel_id FROM channel_members WHERE user_id=?`, uid)
	return
}

func (s *Server) afterDeviceAdded(ctx context.Context, uid, deviceID int64) {
	spaces, dms := s.userContainers(ctx, uid)
	for _, c := range spaces {
		s.requestKeys(ctx, c, false, []int64{deviceID})
	}
	for _, c := range dms {
		s.requestKeys(ctx, c, true, []int64{deviceID})
	}
}

func (s *Server) afterDeviceRevoked(ctx context.Context, uid int64) {
	spaces, dms := s.userContainers(ctx, uid)
	for _, c := range spaces {
		s.requestRotation(ctx, c, false)
	}
	for _, c := range dms {
		s.requestRotation(ctx, c, true)
	}
}
