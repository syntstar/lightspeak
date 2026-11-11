package server

import (
	"testing"
)

func TestSpaceRolesAndChannels(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	bob := e.register("bob", false)
	carol := e.register("carol", false)
	out := e.register("outsider", false)
	sp, text, voice := e.spaceWithMembers(owner, bob, carol)
	admin := e.adminRole(owner, sp)

	// outsiders see nothing
	expect(t, out.do("GET", "/api/v1/spaces/"+sp, nil), 404, "unknown_space")
	expect(t, out.do("GET", "/api/v1/spaces/"+sp+"/members", nil), 404)
	expect(t, out.do("POST", "/api/v1/spaces/"+sp+"/channels", map[string]any{"kind": "text", "name": "x"}), 404)
	expect(t, out.do("PATCH", "/api/v1/channels/"+text, map[string]any{"name": "x"}), 404)
	expect(t, out.do("GET", "/api/v1/channels/"+text+"/messages", nil), 404)

	// members cannot administrate
	expect(t, bob.do("POST", "/api/v1/spaces/"+sp+"/channels", map[string]any{"kind": "text", "name": "x"}), 403)
	expect(t, bob.do("PATCH", "/api/v1/channels/"+text, map[string]any{"topic": "x"}), 403)
	expect(t, bob.do("DELETE", "/api/v1/channels/"+voice, nil), 403)
	expect(t, bob.do("POST", "/api/v1/spaces/"+sp+"/invites", map[string]any{}), 403)
	expect(t, bob.do("GET", "/api/v1/spaces/"+sp+"/invites", nil), 403)
	expect(t, bob.do("PATCH", "/api/v1/spaces/"+sp, map[string]any{"name": "Mine"}), 403)
	expect(t, bob.do("DELETE", "/api/v1/spaces/"+sp+"/members/"+carol.id, nil), 403)
	expect(t, bob.do("PUT", "/api/v1/spaces/"+sp+"/bans/"+carol.id, nil), 403)
	expect(t, bob.role(sp, carol.id, admin, true), 403)
	expect(t, bob.do("PATCH", "/api/v1/spaces/"+sp+"/channels/order", []map[string]any{{"id": text, "position": 5}}), 403)

	// owner promotes bob to admin; admins may manage channels and invites
	expect(t, owner.role(sp, bob.id, admin, true), 200)
	cat := expect(t, bob.do("POST", "/api/v1/spaces/"+sp+"/channels", map[string]any{"kind": "category", "name": "Games"}), 201).json()
	ch := expect(t, bob.do("POST", "/api/v1/spaces/"+sp+"/channels", map[string]any{"kind": "voice", "name": "Raid", "parentId": cat["id"], "userLimit": 5}), 201).json()
	if ch["parentId"] != cat["id"] || ch["userLimit"].(float64) != 5 {
		t.Fatalf("channel: %v", ch)
	}
	expect(t, bob.do("POST", "/api/v1/spaces/"+sp+"/channels", map[string]any{"kind": "dm", "name": "x"}), 400)
	expect(t, bob.do("POST", "/api/v1/spaces/"+sp+"/channels", map[string]any{"kind": "text", "name": "x", "parentId": text}), 400, "invalid_parent")
	expect(t, bob.do("POST", "/api/v1/spaces/"+sp+"/invites", map[string]any{"maxAgeSeconds": 86400, "maxUses": 5}), 201)
	expect(t, bob.do("POST", "/api/v1/spaces/"+sp+"/invites", map[string]any{"maxAgeSeconds": 12345}), 400, "invalid_max_age")
	// an admin may rename the space but cannot hand out the Admin role (same
	// rank), touch the owner or delete the space
	expect(t, bob.do("PATCH", "/api/v1/spaces/"+sp, map[string]any{"name": "Friends"}), 200)
	expect(t, bob.role(sp, carol.id, admin, true), 403)
	expect(t, bob.do("DELETE", "/api/v1/spaces/"+sp+"/members/"+owner.id, nil), 403)
	expect(t, bob.do("PUT", "/api/v1/spaces/"+sp+"/bans/"+owner.id, nil), 403)
	// admin vs admin: forbidden
	expect(t, owner.role(sp, carol.id, admin, true), 200)
	expect(t, bob.do("DELETE", "/api/v1/spaces/"+sp+"/members/"+carol.id, nil), 403)
	expect(t, owner.role(sp, carol.id, admin, false), 200)
	// cross-space channel id cannot be used as parent
	sp2, text2, _ := e.spaceWithMembers(out)
	_ = sp2
	expect(t, bob.do("PATCH", "/api/v1/channels/"+text2, map[string]any{"name": "pwned"}), 404)
	expect(t, bob.do("PATCH", "/api/v1/spaces/"+sp+"/channels/order", []map[string]any{{"id": text2, "position": 1}}), 400, "invalid_order")
	order := expect(t, bob.do("PATCH", "/api/v1/spaces/"+sp+"/channels/order", []map[string]any{{"id": voice, "position": 0}, {"id": text, "position": 1, "parentId": cat["id"]}}), 200).list()
	if len(order) != 4 {
		t.Fatalf("order: %d", len(order))
	}

	// kick: admin kicks member, member is gone
	expect(t, bob.do("DELETE", "/api/v1/spaces/"+sp+"/members/"+carol.id, nil), 204)
	expect(t, carol.do("GET", "/api/v1/spaces/"+sp, nil), 404)
	// owner cannot leave; member can
	expect(t, owner.do("DELETE", "/api/v1/spaces/"+sp+"/members/@me", nil), 403, "owner_cannot_leave")

	// ban prevents rejoin
	inv := expect(t, owner.do("POST", "/api/v1/spaces/"+sp+"/invites", map[string]any{}), 201).json()
	expect(t, carol.do("POST", "/api/v1/invites/"+inv["code"].(string)+"/accept", nil), 200)
	expect(t, bob.do("PUT", "/api/v1/spaces/"+sp+"/bans/"+carol.id, map[string]any{"reason": "spam"}), 204)
	expect(t, carol.do("POST", "/api/v1/invites/"+inv["code"].(string)+"/accept", nil), 403, "banned")
	bans := expect(t, owner.do("GET", "/api/v1/spaces/"+sp+"/bans", nil), 200).list()
	if len(bans) != 1 {
		t.Fatal("ban list")
	}
	expect(t, bob.do("DELETE", "/api/v1/spaces/"+sp+"/bans/"+carol.id, nil), 204)
	expect(t, carol.do("POST", "/api/v1/invites/"+inv["code"].(string)+"/accept", nil), 200)

	// invite revocation: only admins of the space
	expect(t, carol.do("DELETE", "/api/v1/invites/"+inv["code"].(string), nil), 403)
	expect(t, out.do("DELETE", "/api/v1/invites/"+inv["code"].(string), nil), 404)
	expect(t, bob.do("DELETE", "/api/v1/invites/"+inv["code"].(string), nil), 204)
	expect(t, out.do("POST", "/api/v1/invites/"+inv["code"].(string)+"/accept", nil), 404, "invite_invalid")

	// delete space: owner only, with confirmation
	expect(t, bob.do("DELETE", "/api/v1/spaces/"+sp, map[string]any{"confirmName": "Friends"}), 403)
	expect(t, owner.do("DELETE", "/api/v1/spaces/"+sp, map[string]any{"confirmName": "friends"}), 400, "confirm_name_mismatch")
	expect(t, owner.do("DELETE", "/api/v1/spaces/"+sp, map[string]any{"confirmName": "Friends"}), 204)
	expect(t, owner.do("GET", "/api/v1/spaces/"+sp, nil), 404)
	expect(t, bob.do("GET", "/api/v1/channels/"+text+"/messages", nil), 404)
}

func TestSpaceInviteRegistration(t *testing.T) {
	e := newEnv(t)
	e.cfg.Signup = "invite" // space invites of non-admins create accounts only under this policy
	owner := e.register("owner", false)
	sp, _, _ := e.spaceWithMembers(owner)
	inv := expect(t, owner.do("POST", "/api/v1/spaces/"+sp+"/invites", map[string]any{"maxUses": 1}), 201).json()
	prev := expect(t, e.call("GET", "/api/v1/invites/"+inv["code"].(string), "", nil), 200).json()
	if str(prev, "space", "name") != "Friends" {
		t.Fatalf("preview: %v", prev)
	}
	r := expect(t, e.call("POST", "/api/v1/auth/register", "", map[string]any{"inviteCode": inv["code"], "username": "newbie", "password": testPassword}), 201).json()
	tok := r["token"].(string)
	expect(t, e.call("GET", "/api/v1/spaces/"+sp, tok, nil), 200)
	me := expect(t, e.call("GET", "/api/v1/users/@me", tok, nil), 200).json()
	if me["isServerAdmin"] != false {
		t.Fatal("space invite must not grant admin")
	}
}

func TestDMs(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	b := e.register("bob", false)
	c := e.register("carol", false)
	d := e.register("dave", false)
	e.spaceWithMembers(a, b, c)
	e.befriend(a, b)
	e.befriend(a, c)
	dm := expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id}}), 201).json()
	again := expect(t, b.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{a.id}}), 200).json()
	if dm["id"] != again["id"] {
		t.Fatal("1:1 DM must be reused")
	}
	id := dm["id"].(string)
	expect(t, a.do("POST", "/api/v1/channels/"+id+"/messages", map[string]any{"content": "secret"}), 201)
	expect(t, c.do("GET", "/api/v1/channels/"+id+"/messages", nil), 404)
	expect(t, c.do("POST", "/api/v1/channels/"+id+"/messages", map[string]any{"content": "x"}), 404)
	expect(t, d.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{a.id}}), 404)
	expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{a.id}}), 400, "invalid_recipients")
	// group DM
	g := expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id, c.id}, "name": "Trio"}), 201).json()
	gid := g["id"].(string)
	if len(g["memberIds"].([]any)) != 3 {
		t.Fatal("group members")
	}
	expect(t, c.do("PATCH", "/api/v1/channels/"+gid, map[string]any{"name": "Best trio"}), 200)
	expect(t, c.do("PATCH", "/api/v1/channels/"+id, map[string]any{"name": "x"}), 404)
	expect(t, a.do("PATCH", "/api/v1/channels/"+id, map[string]any{"name": "x"}), 403)
	expect(t, a.do("DELETE", "/api/v1/channels/"+gid, nil), 403)
	expect(t, a.do("DELETE", "/api/v1/dms/"+id, nil), 400, "cannot_leave_dm")
	expect(t, c.do("DELETE", "/api/v1/dms/"+gid, nil), 204)
	expect(t, c.do("GET", "/api/v1/channels/"+gid+"/messages", nil), 404)
	list := expect(t, a.do("GET", "/api/v1/dms", nil), 200).json()
	if len(list["channels"].([]any)) != 2 {
		t.Fatalf("dms: %v", list)
	}
}

func TestSpaceOwnershipLimit(t *testing.T) {
	e := newEnv(t)
	admin := e.register("admin", true)
	friend := e.register("friend", false)
	create := func(c *client) resp {
		return c.do("POST", "/api/v1/spaces", map[string]any{"name": "S"})
	}

	// regular users own at most LS_MAX_SPACES (3 in tests)
	var last string
	for i := 0; i < 3; i++ {
		last = str(expect(t, create(friend), 201).json(), "space", "id")
	}
	expect(t, create(friend), 403, "space_limit")
	// deleting one frees a slot
	expect(t, friend.do("DELETE", "/api/v1/spaces/"+last, map[string]any{"confirmName": "S"}), 204)
	expect(t, create(friend), 201)
	// membership in someone else's space does not count
	e.spaceWithMembers(admin, friend)
	expect(t, create(friend), 403, "space_limit")

	// server admins are exempt
	for i := 0; i < 5; i++ {
		expect(t, create(admin), 201)
	}

	// 0 = only server admins create spaces
	e.cfg.MaxSpaces = 0
	other := e.register("other", false)
	expect(t, create(other), 403, "space_limit")
	expect(t, create(admin), 201)
}
