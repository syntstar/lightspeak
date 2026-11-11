package server

import (
	"bytes"
	"testing"
)

func envelope(seed byte) string { return b64(bytes.Repeat([]byte{seed}, 80)) }

func TestE2EEEnvelopes(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	bob := e.register("bob", false)
	out := e.register("outsider", false)
	sp, _, _ := e.spaceWithMembers(owner, bob)
	base := "/api/v1/e2ee/" + sp

	expect(t, out.do("GET", base+"/envelope", nil), 404)
	expect(t, out.do("GET", base+"/devices", nil), 404)
	expect(t, bob.do("GET", base+"/envelope", nil), 404, "no_key_yet")

	devs := expect(t, bob.do("GET", base+"/devices", nil), 200).json()
	if len(devs["devices"].([]any)) != 2 {
		t.Fatalf("devices: %v", devs)
	}
	// bootstrap must include own envelope; foreign devices rejected
	expect(t, bob.do("POST", base+"/envelopes", map[string]any{"epoch": 1, "envelopes": []map[string]any{{"deviceId": owner.deviceID, "ciphertext": envelope(1)}}}), 400, "missing_own_envelope")
	expect(t, bob.do("POST", base+"/envelopes", map[string]any{"epoch": 1, "envelopes": []map[string]any{{"deviceId": out.deviceID, "ciphertext": envelope(1)}}}), 400, "invalid_envelope_device")
	expect(t, bob.do("POST", base+"/envelopes", map[string]any{"epoch": 1, "envelopes": []map[string]any{{"deviceId": bob.deviceID, "ciphertext": "c2hvcnQ="}}}), 400, "invalid_envelopes")
	expect(t, bob.do("POST", base+"/envelopes", map[string]any{"epoch": 2, "envelopes": []map[string]any{{"deviceId": bob.deviceID, "ciphertext": envelope(1)}}}), 409, "epoch_conflict")
	expect(t, bob.do("POST", base+"/envelopes", map[string]any{"epoch": 1, "envelopes": []map[string]any{{"deviceId": bob.deviceID, "ciphertext": envelope(1)}}}), 200)
	got := expect(t, bob.do("GET", base+"/envelope", nil), 200).json()
	if got["ciphertext"] != envelope(1) {
		t.Fatal("own envelope")
	}
	// owner has no envelope yet: waiting state, and cannot overwrite the key.
	// The key holder is online, so it is asked to share (no rotation).
	wb := e.dial(t, map[string]any{"token": bob.token, "protocolVersion": 1})
	wb.wait("ready")
	expect(t, owner.do("GET", base+"/envelope", nil), 404, "envelope_missing")
	wb.wait("e2ee_key_request")
	if e.s.rotationPending(t.Context(), mustID(sp)) {
		t.Fatal("no rotation while a key holder is online")
	}
	expect(t, owner.do("POST", base+"/envelopes", map[string]any{"epoch": 1, "envelopes": []map[string]any{{"deviceId": owner.deviceID, "ciphertext": envelope(9)}}}), 409, "key_exists")
	// a key holder shares it; an existing envelope is never replaced
	expect(t, bob.do("POST", base+"/envelopes", map[string]any{"epoch": 1, "envelopes": []map[string]any{{"deviceId": owner.deviceID, "ciphertext": envelope(2)}, {"deviceId": bob.deviceID, "ciphertext": envelope(7)}}}), 200)
	if expect(t, owner.do("GET", base+"/envelope", nil), 200).json()["ciphertext"] != envelope(2) {
		t.Fatal("owner envelope")
	}
	if expect(t, bob.do("GET", base+"/envelope", nil), 200).json()["ciphertext"] != envelope(1) {
		t.Fatal("envelope was overwritten")
	}

	// rotation only when the server asked for one, not even by the owner
	rot := map[string]any{"newEpoch": 2, "envelopes": []map[string]any{{"deviceId": bob.deviceID, "ciphertext": envelope(3)}}}
	expect(t, bob.do("POST", base+"/rotate", rot), 409, "rotation_not_pending")
	ownerRot := map[string]any{"newEpoch": 2, "envelopes": []map[string]any{{"deviceId": owner.deviceID, "ciphertext": envelope(4)}, {"deviceId": bob.deviceID, "ciphertext": envelope(5)}}}
	expect(t, owner.do("POST", base+"/rotate", ownerRot), 409, "rotation_not_pending")
	e.s.requestRotation(t.Context(), mustID(sp), false)
	expect(t, owner.do("POST", base+"/rotate", map[string]any{"newEpoch": 3, "envelopes": []map[string]any{{"deviceId": owner.deviceID, "ciphertext": envelope(3)}}}), 409, "epoch_conflict")
	expect(t, owner.do("POST", base+"/rotate", map[string]any{"newEpoch": 2, "envelopes": []map[string]any{{"deviceId": bob.deviceID, "ciphertext": envelope(3)}}}), 400, "missing_own_envelope")
	expect(t, owner.do("POST", base+"/rotate", ownerRot), 200)
	// a second client racing for the same request must not rotate again
	expect(t, bob.do("POST", base+"/rotate", map[string]any{"newEpoch": 3, "envelopes": []map[string]any{{"deviceId": bob.deviceID, "ciphertext": envelope(6)}}}), 409, "rotation_not_pending")
	sp1 := expect(t, bob.do("GET", "/api/v1/spaces/"+sp, nil), 200).json()
	if str(sp1, "space", "e2eeEpoch") != "2" {
		t.Fatal("epoch not advanced")
	}
	// kicking bob marks rotation pending; afterwards bob has no access at all
	carol := e.register("carol", false)
	inv := expect(t, owner.do("POST", "/api/v1/spaces/"+sp+"/invites", map[string]any{}), 201).json()
	expect(t, carol.do("POST", "/api/v1/invites/"+inv["code"].(string)+"/accept", nil), 200)
	expect(t, owner.do("DELETE", "/api/v1/spaces/"+sp+"/members/"+bob.id, nil), 204)
	expect(t, bob.do("GET", base+"/envelope", nil), 404)
	if !e.s.rotationPending(t.Context(), mustID(sp)) {
		t.Fatal("rotation should be pending after kick")
	}
	// envelopes for the removed member's device are refused
	expect(t, owner.do("POST", base+"/envelopes", map[string]any{"epoch": 2, "envelopes": []map[string]any{{"deviceId": bob.deviceID, "ciphertext": envelope(6)}}}), 400, "invalid_envelope_device")
	// with a pending rotation a plain member may rotate (no admin online)
	carolRot := map[string]any{"newEpoch": 3, "envelopes": []map[string]any{{"deviceId": carol.deviceID, "ciphertext": envelope(8)}, {"deviceId": owner.deviceID, "ciphertext": envelope(8)}}}
	expect(t, carol.do("POST", base+"/rotate", carolRot), 200)
	if e.s.rotationPending(t.Context(), mustID(sp)) {
		t.Fatal("pending flag should be cleared")
	}
	var old int
	_ = e.db.R.QueryRow(`SELECT COUNT(*) FROM media_key_envelopes WHERE container_id=? AND epoch<2`, mustID(sp)).Scan(&old)
	if old != 0 {
		t.Fatal("old epochs should be pruned")
	}
}

func TestE2EEDevicesAndDMs(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	b := e.register("bob", false)
	c := e.register("carol", false)
	e.spaceWithMembers(a, b, c)
	e.befriend(a, b)
	dm := expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id}}), 201).json()
	base := "/api/v1/e2ee/" + dm["id"].(string)
	expect(t, c.do("GET", base+"/envelope", nil), 404, "unknown_container")
	expect(t, a.do("POST", base+"/envelopes", map[string]any{"epoch": 1, "envelopes": []map[string]any{{"deviceId": a.deviceID, "ciphertext": envelope(1)}, {"deviceId": b.deviceID, "ciphertext": envelope(2)}}}), 200)
	expect(t, b.do("GET", base+"/envelope", nil), 200)
	// DM participants rotate only on request (e.g. after a device revocation)
	dmRot := map[string]any{"newEpoch": 2, "envelopes": []map[string]any{{"deviceId": b.deviceID, "ciphertext": envelope(3)}}}
	expect(t, b.do("POST", base+"/rotate", dmRot), 409, "rotation_not_pending")
	e.s.requestRotation(t.Context(), mustID(dm["id"].(string)), true)
	expect(t, b.do("POST", base+"/rotate", dmRot), 200)
	expect(t, c.do("POST", base+"/rotate", map[string]any{"newEpoch": 3, "envelopes": []map[string]any{{"deviceId": c.deviceID, "ciphertext": envelope(3)}}}), 404)

	// device management: revoke own device kills sessions and envelopes
	l := expect(t, e.call("POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": testPassword, "deviceName": "laptop", "devicePublicKey": fakeKey("laptop")}), 200).json()
	laptop := str(l, "device", "id")
	expect(t, b.do("DELETE", "/api/v1/devices/"+laptop, nil), 404)
	expect(t, a.do("DELETE", "/api/v1/devices/"+laptop, nil), 204)
	expect(t, e.call("GET", "/api/v1/users/@me", l["token"].(string), nil), 401)
	// re-keying the current device creates a new device and revokes the old
	nd := expect(t, a.do("POST", "/api/v1/devices", map[string]any{"name": "pc", "publicKey": fakeKey("new")}), 201).json()
	if nd["id"] == a.deviceID {
		t.Fatal("new device expected")
	}
	expect(t, a.do("POST", "/api/v1/devices", map[string]any{"name": "pc", "publicKey": "short"}), 400, "invalid_public_key")
	var revoked int
	_ = e.db.R.QueryRow(`SELECT COUNT(*) FROM devices WHERE id=? AND revoked_at IS NOT NULL`, mustID(a.deviceID)).Scan(&revoked)
	if revoked != 1 {
		t.Fatal("old device should be revoked")
	}
	var envs int
	_ = e.db.R.QueryRow(`SELECT COUNT(*) FROM media_key_envelopes WHERE device_id=?`, mustID(a.deviceID)).Scan(&envs)
	if envs != 0 {
		t.Fatal("envelopes of revoked device must be deleted")
	}
}

// When no device holding the key is connected, a waiting device would wait
// forever; the server asks for a rotation instead (sealed to all members).
func TestE2EEKeyHolderOffline(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	bob := e.register("bob", false)
	sp, _, _ := e.spaceWithMembers(owner, bob)
	base := "/api/v1/e2ee/" + sp
	expect(t, bob.do("POST", base+"/envelopes", map[string]any{"epoch": 1, "envelopes": []map[string]any{{"deviceId": bob.deviceID, "ciphertext": envelope(1)}}}), 200)

	wo := e.dial(t, map[string]any{"token": owner.token, "protocolVersion": 1})
	wo.wait("ready")
	expect(t, owner.do("GET", base+"/envelope", nil), 404, "envelope_missing")
	wo.wait("e2ee_rotate_request")
	if wo.find("e2ee_key_request") != nil {
		t.Fatal("key request although no holder is online")
	}
	rot := map[string]any{"newEpoch": 2, "envelopes": []map[string]any{{"deviceId": owner.deviceID, "ciphertext": envelope(2)}, {"deviceId": bob.deviceID, "ciphertext": envelope(3)}}}
	expect(t, owner.do("POST", base+"/rotate", rot), 200)
	wo.wait("e2ee_epoch_changed")
	if got := expect(t, bob.do("GET", base+"/envelope", nil), 200).json(); got["ciphertext"] != envelope(3) {
		t.Fatal("bob should get the new epoch's envelope")
	}
	// an outsider still learns nothing and cannot trigger anything
	out := e.register("outsider", false)
	expect(t, out.do("GET", base+"/envelope", nil), 404)
}
