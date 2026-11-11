package server

import "testing"

// TestDeleteAccount: password confirmation, owners and the last
// server admin are stopped, the account is anonymised and gone everywhere.
func TestDeleteAccount(t *testing.T) {
	e := newEnv(t)
	admin := e.register("admin", true)
	anna := e.register("anna", false)
	bob := e.register("bob", false)
	carol := e.register("carol", false)
	sp, text, _ := e.spaceWithMembers(anna, bob, carol)
	e.befriend(anna, bob)
	e.befriend(anna, carol)
	dm := expect(t, anna.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{bob.id}}), 201).json()["id"].(string)
	group := expect(t, anna.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{bob.id, carol.id}}), 201).json()["id"].(string)
	msg := expect(t, bob.do("POST", "/api/v1/channels/"+text+"/messages", map[string]any{"content": "bye"}), 201).json()["id"].(string)
	expect(t, bob.do("POST", "/api/v1/channels/"+dm+"/messages", map[string]any{"content": "dm"}), 201)
	wa := e.dial(t, map[string]any{"token": anna.token})
	wa.wait("ready")
	del := func(c *client, pw string) resp {
		return c.do("POST", "/api/v1/users/@me/delete", map[string]any{"password": pw})
	}

	expect(t, del(bob, "wrong-password-x"), 403, "wrong_password")
	expect(t, del(anna, testPassword), 409, "owns_spaces")
	expect(t, del(admin, testPassword), 409, "last_server_admin")

	expect(t, del(bob, testPassword), 204)
	// signed out everywhere, no sign-in, username free again
	expect(t, bob.do("GET", "/api/v1/users/@me", nil), 401)
	expect(t, e.call("POST", "/api/v1/auth/login", "", map[string]any{"username": "bob", "password": testPassword}), 401)
	// gone from the space, the group DM and the friends list – live too
	wa.waitMatch("member_remove", func(d map[string]any) bool { return d["userId"] == bob.id })
	wa.waitMatch("relationship_update", func(d map[string]any) bool { return d["userId"] == bob.id && d["kind"] == "none" })
	wa.waitMatch("user_update", func(d map[string]any) bool { return d["id"] == bob.id && d["deleted"] == true })
	for _, m := range expect(t, anna.do("GET", "/api/v1/spaces/"+sp+"/members", nil), 200).list() {
		if m.(map[string]any)["userId"] == bob.id {
			t.Fatal("still a member")
		}
	}
	var n int
	_ = e.db.R.QueryRow(`SELECT COUNT(*) FROM channel_members WHERE channel_id=? AND user_id=?`, group, bob.id).Scan(&n)
	if n != 0 {
		t.Fatal("still in the group DM")
	}
	// messages stay, the author is anonymised
	u := expect(t, anna.do("GET", "/api/v1/users/"+bob.id, nil), 200).json()["user"].(map[string]any)
	if u["deleted"] != true || u["displayName"] != "" || u["username"] == "bob" {
		t.Fatalf("not anonymised: %v", u)
	}
	found := false
	for _, m := range expect(t, anna.do("GET", "/api/v1/channels/"+text+"/messages", nil), 200).list() {
		found = found || m.(map[string]any)["id"] == msg
	}
	if !found {
		t.Fatal("message of the deleted account is gone")
	}
	expect(t, anna.do("GET", "/api/v1/channels/"+dm+"/messages", nil), 200)
	e.register("bob", false)

	// a second server admin may leave
	e.register("admin2", true)
	expect(t, del(admin, testPassword), 204)
}
