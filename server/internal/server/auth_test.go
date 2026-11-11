package server

import (
	"context"
	"encoding/base64"
	"testing"
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func TestHealthAndVersion(t *testing.T) {
	e := newEnv(t)
	r := e.raw("GET", "/healthz", "", nil, nil)
	if r.status != 200 || string(r.body) != "ok\n" {
		t.Fatalf("healthz: %d %q", r.status, r.body)
	}
	v := expect(t, e.call("GET", "/version", "", nil), 200).json()
	if v["protocolVersion"].(float64) != ProtocolVersion {
		t.Fatal("protocol version")
	}
	if e.raw("GET", "/healthz", "", nil, nil).hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers missing")
	}
}

func TestUnauthenticatedRejected(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/v1/users/@me", "/api/v1/dms", "/api/v1/spaces/1", "/api/v1/search?q=x", "/api/v1/admin/storage"} {
		expect(t, e.call("GET", p, "", nil), 401, "unauthorized")
		expect(t, e.call("GET", p, "garbage-token", nil), 401, "unauthorized")
	}
}

func TestRegisterValidation(t *testing.T) {
	e := newEnv(t)
	inv := e.invite(0, false)
	base := func() map[string]any {
		return map[string]any{"inviteCode": inv, "username": "anna", "displayName": "Anna", "password": testPassword, "deviceName": "pc"}
	}
	b := base()
	b["inviteCode"] = "doesnotexist1"
	expect(t, e.call("POST", "/api/v1/auth/register", "", b), 404, "invite_invalid")
	b = base()
	b["password"] = "short"
	expect(t, e.call("POST", "/api/v1/auth/register", "", b), 400, "password_too_short")
	b = base()
	b["password"] = "password123"
	expect(t, e.call("POST", "/api/v1/auth/register", "", b), 400, "password_too_common")
	b = base()
	b["password"] = "anna-is-great-123"
	expect(t, e.call("POST", "/api/v1/auth/register", "", b), 400, "password_contains_username")
	b = base()
	b["username"] = "An Na"
	expect(t, e.call("POST", "/api/v1/auth/register", "", b), 400, "invalid_username")
	b = base()
	b["devicePublicKey"] = "AAAA"
	expect(t, e.call("POST", "/api/v1/auth/register", "", b), 400, "invalid_public_key")
	expect(t, e.call("POST", "/api/v1/auth/register", "", base()), 201)
	// username taken (case-insensitive) with a fresh invite
	b = base()
	b["inviteCode"] = e.invite(0, false)
	b["username"] = "ANNA"
	expect(t, e.call("POST", "/api/v1/auth/register", "", b), 409, "username_taken")
}

func TestInviteLimits(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	code, _, err := e.s.CreateInvite(ctx, 0, 3600, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	reg := func(u string) resp {
		return e.call("POST", "/api/v1/auth/register", "", map[string]any{"inviteCode": code, "username": u, "password": testPassword})
	}
	expect(t, reg("first"), 201)
	expect(t, reg("second"), 410, "invite_exhausted")
	// expired
	code2, _, _ := e.s.CreateInvite(ctx, 0, 3600, 0, false)
	if _, err := e.db.W.Exec(`UPDATE invites SET expires_at=1 WHERE code=?`, code2); err != nil {
		t.Fatal(err)
	}
	expect(t, e.call("GET", "/api/v1/invites/"+code2, "", nil), 410, "invite_expired")
	// revoked
	code3, _, _ := e.s.CreateInvite(ctx, 0, 0, 0, false)
	if _, err := e.db.W.Exec(`UPDATE invites SET revoked_at=1 WHERE code=?`, code3); err != nil {
		t.Fatal(err)
	}
	expect(t, e.call("GET", "/api/v1/invites/"+code3, "", nil), 404, "invite_invalid")
	// landing page renders without leaking anything for invalid codes
	if r := e.raw("GET", "/invite/"+code3, "", nil, nil); r.status != 404 {
		t.Fatalf("landing invalid: %d", r.status)
	}
}

func TestLoginLogoutSessions(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", true)
	expect(t, e.call("POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": "wrong-password-x"}), 401, "invalid_credentials")
	expect(t, e.call("POST", "/api/v1/auth/login", "", map[string]any{"username": "nobody", "password": testPassword}), 401, "invalid_credentials")
	l := expect(t, e.call("POST", "/api/v1/auth/login", "", map[string]any{"username": "Alice", "password": testPassword, "deviceId": a.deviceID}), 200).json()
	if str(l, "device", "id") != a.deviceID {
		t.Fatal("known device should be reused")
	}
	tok2 := l["token"].(string)
	me := expect(t, e.call("GET", "/api/v1/users/@me", tok2, nil), 200).json()
	if me["isServerAdmin"] != true || me["username"] != "alice" {
		t.Fatalf("me: %v", me)
	}
	sessions := expect(t, a.do("GET", "/api/v1/users/@me/sessions", nil), 200).list()
	if len(sessions) != 2 {
		t.Fatalf("sessions: %d", len(sessions))
	}
	var other string
	for _, s := range sessions {
		if sm := s.(map[string]any); sm["current"] != true {
			other = sm["id"].(string)
		}
	}
	expect(t, a.do("DELETE", "/api/v1/users/@me/sessions/"+other, nil), 204)
	expect(t, e.call("GET", "/api/v1/users/@me", tok2, nil), 401)
	// another user cannot revoke alice's session
	b := e.register("bob", false)
	expect(t, b.do("DELETE", "/api/v1/users/@me/sessions/"+other, nil), 404)
	expect(t, a.do("POST", "/api/v1/auth/logout", nil), 204)
	expect(t, a.do("GET", "/api/v1/users/@me", nil), 401)
	// disabled account
	if _, err := e.db.W.Exec(`UPDATE users SET disabled_at=1 WHERE username='bob'`); err != nil {
		t.Fatal(err)
	}
	expect(t, b.do("GET", "/api/v1/users/@me", nil), 401)
	expect(t, e.call("POST", "/api/v1/auth/login", "", map[string]any{"username": "bob", "password": testPassword}), 403, "account_disabled")
}

func TestPasswordChange(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	l := expect(t, e.call("POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": testPassword}), 200).json()
	other := l["token"].(string)
	expect(t, a.do("POST", "/api/v1/users/@me/password", map[string]any{"oldPassword": "nope-nope-nope", "newPassword": "another-good-pass"}), 403, "wrong_password")
	expect(t, a.do("POST", "/api/v1/users/@me/password", map[string]any{"oldPassword": testPassword, "newPassword": "short"}), 400, "password_too_short")
	expect(t, a.do("POST", "/api/v1/users/@me/password", map[string]any{"oldPassword": testPassword, "newPassword": "another-good-pass", "revokeOtherSessions": true}), 204)
	expect(t, e.call("GET", "/api/v1/users/@me", other, nil), 401)
	expect(t, a.do("GET", "/api/v1/users/@me", nil), 200)
	expect(t, e.call("POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": "another-good-pass"}), 200)
}

func TestProfileAndSettings(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	b := e.register("bob", false)
	c := e.register("carol", false)
	expect(t, a.do("PATCH", "/api/v1/users/@me", map[string]any{"displayName": "  Ali\u0000ce  ", "status": "dnd", "statusText": "busy"}), 200)
	me := expect(t, a.do("GET", "/api/v1/users/@me", nil), 200).json()
	if me["displayName"] != "Alice" || me["status"] != "dnd" {
		t.Fatalf("patch: %v", me)
	}
	expect(t, a.do("PATCH", "/api/v1/users/@me", map[string]any{"username": "bob"}), 409, "username_taken")
	expect(t, a.do("PATCH", "/api/v1/users/@me", map[string]any{"status": "partying"}), 400, "invalid_status")
	// profiles only for related users
	expect(t, b.do("GET", "/api/v1/users/"+a.id, nil), 404, "unknown_user")
	e.spaceWithMembers(a, b)
	p := expect(t, b.do("GET", "/api/v1/users/"+a.id, nil), 200).json()
	if len(p["devices"].([]any)) != 1 {
		t.Fatal("device keys expected")
	}
	expect(t, c.do("GET", "/api/v1/users/"+a.id, nil), 404)
	// settings
	expect(t, a.do("PUT", "/api/v1/users/@me/settings", map[string]any{"theme": "dark"}), 204)
	s := expect(t, a.do("GET", "/api/v1/users/@me/settings", nil), 200).json()
	if str(s, "settings", "theme") != "dark" {
		t.Fatalf("settings: %v", s)
	}
	expect(t, a.do("PUT", "/api/v1/users/@me/settings", []int{1}), 400, "invalid_settings")
	big := map[string]string{"x": string(make([]byte, 17000))}
	expect(t, a.do("PUT", "/api/v1/users/@me/settings", big), 413)
	// other users' settings are never readable: route only exposes @me
	s2 := expect(t, b.do("GET", "/api/v1/users/@me/settings", nil), 200).json()
	if str(s2, "settings", "theme") != "" {
		t.Fatal("settings leaked")
	}
}

func TestRelationshipsAndBlocking(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	b := e.register("bob", false)
	m := e.register("mallory", false)
	// friend request by username works without prior relation
	expect(t, a.do("POST", "/api/v1/relationships", map[string]any{"username": "bob"}), 200)
	rels := expect(t, b.do("GET", "/api/v1/relationships", nil), 200).list()
	if len(rels) != 1 || rels[0].(map[string]any)["kind"] != "pending_in" {
		t.Fatalf("rels: %v", rels)
	}
	r := expect(t, b.do("PUT", "/api/v1/relationships/"+a.id, map[string]any{"kind": "friend"}), 200).json()
	if r["kind"] != "friend" {
		t.Fatal("should be friends")
	}
	// friends can DM, strangers cannot
	expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id}}), 201)
	expect(t, m.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{a.id}}), 404)
	expect(t, m.do("PUT", "/api/v1/relationships/"+a.id, map[string]any{"kind": "friend"}), 404)
	// blocking stops DMs
	dm := expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id}}), 200).json()
	expect(t, b.do("PUT", "/api/v1/relationships/"+a.id, map[string]any{"kind": "blocked"}), 200)
	expect(t, a.do("POST", "/api/v1/channels/"+dm["id"].(string)+"/messages", map[string]any{"content": "hi"}), 403, "blocked")
	expect(t, a.do("POST", "/api/v1/calls/"+dm["id"].(string)+"/ring", nil), 403, "blocked")
	expect(t, a.do("POST", "/api/v1/relationships", map[string]any{"username": "bob"}), 403, "blocked")
	expect(t, b.do("DELETE", "/api/v1/relationships/"+a.id, nil), 204)
	// unblocked, but no longer friends: still no DMs
	expect(t, a.do("POST", "/api/v1/channels/"+dm["id"].(string)+"/messages", map[string]any{"content": "hi again"}), 403, "not_friends")
	e.befriend(a, b)
	expect(t, a.do("POST", "/api/v1/channels/"+dm["id"].(string)+"/messages", map[string]any{"content": "hi again"}), 201)
}

// Direct messages and private calls only between friends.
func TestDMsNeedFriendship(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	b := e.register("bob", false)
	c := e.register("carol", false)
	e.spaceWithMembers(a, b, c)
	// space members who are not friends: no new 1:1 or group DM
	expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id}}), 403, "not_friends")
	// a pending request is not a friendship
	expect(t, a.do("POST", "/api/v1/relationships", map[string]any{"username": "bob"}), 200)
	expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id}}), 403, "not_friends")
	expect(t, b.do("PUT", "/api/v1/relationships/"+a.id, map[string]any{"kind": "friend"}), 200)
	dm := expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id}}), 201).json()
	id := dm["id"].(string)
	// group DM needs a friendship with every recipient
	expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id, c.id}}), 403, "not_friends")
	expect(t, a.do("POST", "/api/v1/channels/"+id+"/messages", map[string]any{"content": "hi"}), 201)
	expect(t, a.do("POST", "/api/v1/calls/"+id+"/ring", nil), 204)
	// unfriended: history stays readable, but no messages, calls or voice joins
	expect(t, b.do("DELETE", "/api/v1/relationships/"+a.id, nil), 204)
	expect(t, a.do("GET", "/api/v1/channels/"+id+"/messages", nil), 200)
	expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id}}), 200)
	expect(t, a.do("POST", "/api/v1/channels/"+id+"/messages", map[string]any{"content": "x"}), 403, "not_friends")
	expect(t, b.do("POST", "/api/v1/channels/"+id+"/messages", map[string]any{"content": "x"}), 403, "not_friends")
	expect(t, a.do("POST", "/api/v1/calls/"+id+"/ring", nil), 403, "not_friends")
	expect(t, b.do("POST", "/api/v1/voice/join", map[string]any{"channelId": id}), 403, "not_friends")
}
