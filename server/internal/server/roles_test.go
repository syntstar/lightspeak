package server

import (
	"fmt"
	"testing"
)

func permsOf(p ...Perm) Perm {
	var out Perm
	for _, x := range p {
		out |= x
	}
	return out
}

func spaceJSON(t *testing.T, c *client, sp string) map[string]any {
	t.Helper()
	return expect(t, c.do("GET", "/api/v1/spaces/"+sp, nil), 200).json()["space"].(map[string]any)
}

func TestRolePermissionsAndHierarchy(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	mod := e.register("mod", false)
	mod2 := e.register("modtwo", false)
	mem := e.register("member", false)
	adm := e.register("adm", false)
	sp, text, voice := e.spaceWithMembers(owner, mod, mod2, mem, adm)
	admin := e.adminRole(owner, sp)
	base := "/api/v1/spaces/" + sp

	// members cannot manage roles at all
	expect(t, mem.do("POST", base+"/roles", map[string]any{"name": "x"}), 403)
	expect(t, mem.do("PATCH", base+"/roles/"+sp, map[string]any{"permissions": PermInvite}), 403)

	// owner creates "Mod" (kick + voice moderation); it starts below Admin
	r := expect(t, owner.do("POST", base+"/roles", map[string]any{"name": "Mod", "permissions": permsOf(PermKick, PermVoiceModerate)}), 201).json()
	modRole := r["id"].(string)
	roles := spaceJSON(t, owner, sp)["roles"].([]any)
	if len(roles) != 2 || roles[0].(map[string]any)["name"] != "Admin" || str(roles[1].(map[string]any), "position") != "1" {
		t.Fatalf("roles: %v", roles)
	}
	expect(t, owner.do("POST", base+"/roles", map[string]any{"name": " ", "permissions": 0}), 400, "invalid_name")
	expect(t, owner.do("POST", base+"/roles", map[string]any{"name": "x", "permissions": int64(PermAll) + 1}), 400, "invalid_permissions")
	expect(t, owner.role(sp, mod.id, modRole, true), 200)
	expect(t, owner.role(sp, mod2.id, modRole, true), 200)
	expect(t, owner.role(sp, adm.id, admin, true), 200)
	expect(t, owner.role(sp, "1", modRole, true), 404)

	// Mod: kick yes, everything else no
	expect(t, mod.do("POST", base+"/channels", map[string]any{"kind": "text", "name": "x"}), 403)
	expect(t, mod.do("PUT", base+"/bans/"+mem.id, nil), 403)
	expect(t, mod.do("POST", base+"/invites", map[string]any{}), 403)
	expect(t, mod.do("PATCH", base, map[string]any{"name": "x"}), 403)
	expect(t, mod.do("DELETE", base+"/members/"+mod2.id, nil), 403) // same rank
	expect(t, mod.do("DELETE", base+"/members/"+adm.id, nil), 403)  // higher rank
	expect(t, mod.do("DELETE", base+"/members/"+owner.id, nil), 403)
	expect(t, mod.do("POST", "/api/v1/voice/"+voice+"/moderate", map[string]any{"userId": adm.id, "action": "mute"}), 403)
	expect(t, mod.do("POST", "/api/v1/voice/"+voice+"/moderate", map[string]any{"userId": mem.id, "action": "mute"}), 204)
	m := expect(t, mem.do("POST", "/api/v1/channels/"+text+"/messages", map[string]any{"content": "hi"}), 201).json()
	expect(t, mod.do("DELETE", "/api/v1/channels/"+text+"/messages/"+m["id"].(string), nil), 403)

	// Admin role: everything except owner rights
	expect(t, adm.do("POST", base+"/channels", map[string]any{"kind": "text", "name": "x"}), 201)
	expect(t, adm.do("PATCH", base, map[string]any{"name": "Friends"}), 200)
	expect(t, adm.do("DELETE", "/api/v1/channels/"+text+"/messages/"+m["id"].(string), nil), 204)
	expect(t, adm.do("DELETE", base+"/members/"+mod2.id, nil), 204) // outranks Mod
	expect(t, adm.do("DELETE", base, map[string]any{"confirmName": "Friends"}), 403)
	expect(t, adm.do("POST", base+"/owner", map[string]any{"userId": adm.id}), 403)
	expect(t, adm.do("PUT", base+"/bans/"+owner.id, nil), 403)
	expect(t, adm.role(sp, mem.id, admin, true), 403) // only below own rank
	if got := spaceJSON(t, adm, sp)["role"]; got != "admin" {
		t.Fatalf("legacy role: %v", got)
	}

	// role manager without Ban: cannot grant or set what they lack
	mgr, err := e.s.db.W.Exec(`UPDATE roles SET permissions=? WHERE id=?`, permsOf(PermKick, PermManageRoles), mustID(modRole))
	_ = mgr
	if err != nil {
		t.Fatal(err)
	}
	expect(t, mod.do("POST", base+"/roles", map[string]any{"name": "Banner", "permissions": PermBan}), 403)
	low := expect(t, mod.do("POST", base+"/roles", map[string]any{"name": "Helper", "permissions": PermKick}), 201).json()["id"].(string)
	expect(t, mod.do("PATCH", base+"/roles/"+low, map[string]any{"permissions": permsOf(PermKick, PermBan)}), 403)
	expect(t, mod.do("PATCH", base+"/roles/"+low, map[string]any{"name": "Helfer", "permissions": 0}), 204)
	expect(t, mod.do("PATCH", base+"/roles/"+sp, map[string]any{"permissions": PermBan}), 403) // @everyone too
	expect(t, mod.do("PATCH", base+"/roles/"+modRole, map[string]any{"name": "x"}), 403)       // own rank
	expect(t, mod.do("PATCH", base+"/roles/"+admin, map[string]any{"permissions": 0}), 403)    // above
	expect(t, mod.do("DELETE", base+"/roles/"+admin, nil), 403)                                // above
	expect(t, mod.role(sp, mod.id, admin, true), 403)                                          // no self-promotion
	expect(t, mod.role(sp, mem.id, low, true), 200)                                            // below, subset
	expect(t, mod.do("POST", base+"/roles/"+low+"/move", map[string]any{"dir": 1}), 403)       // would reach own rank
	expect(t, mod.do("POST", base+"/roles/"+low+"/move", map[string]any{"dir": -1}), 403)      // already lowest
	expect(t, owner.do("POST", base+"/roles/"+low+"/move", map[string]any{"dir": 5}), 400, "invalid_dir")
	expect(t, owner.do("POST", base+"/roles/"+admin+"/move", map[string]any{"dir": 1}), 400, "invalid_dir") // top

	// @everyone: owner allows invites for everybody
	expect(t, mem.do("POST", base+"/invites", map[string]any{}), 403)
	expect(t, owner.do("PATCH", base+"/roles/"+sp, map[string]any{"permissions": PermInvite}), 204)
	expect(t, mem.do("POST", base+"/invites", map[string]any{}), 201)
	expect(t, owner.do("PATCH", base+"/roles/"+sp, map[string]any{"name": "all"}), 400, "invalid_role")
	if got := spaceJSON(t, mem, sp)["everyonePerms"]; got != float64(PermInvite) {
		t.Fatalf("everyonePerms: %v", got)
	}

	// deleting a role takes its rights away and closes the position gap
	expect(t, owner.do("DELETE", base+"/roles/"+modRole, nil), 204)
	expect(t, mod.do("DELETE", base+"/members/"+mem.id, nil), 403)
	for _, ro := range spaceJSON(t, owner, sp)["roles"].([]any) {
		rm := ro.(map[string]any)
		if (rm["name"] == "Admin") != (str(rm, "position") == "2") {
			t.Fatalf("positions after delete: %v", rm)
		}
	}

	// role limit
	for i := 0; i < e.cfg.MaxSpaceRoles-2; i++ {
		expect(t, owner.do("POST", base+"/roles", map[string]any{"name": fmt.Sprint("r", i)}), 201)
	}
	expect(t, owner.do("POST", base+"/roles", map[string]any{"name": "one too many"}), 403, "role_limit")
}

func TestOwnershipAndServerAdmin(t *testing.T) {
	e := newEnv(t)
	host := e.register("host", true)
	owner := e.register("owner", false)
	bob := e.register("bob", false)
	carol := e.register("carol", false)
	sp, _, _ := e.spaceWithMembers(owner, bob, carol)
	base := "/api/v1/spaces/" + sp

	// transfer: owner only, target must be a member
	expect(t, bob.do("POST", base+"/owner", map[string]any{"userId": bob.id}), 403)
	expect(t, owner.do("POST", base+"/owner", map[string]any{"userId": host.id}), 404, "unknown_member")
	expect(t, owner.do("POST", base+"/owner", map[string]any{"userId": owner.id}), 400, "already_owner")
	expect(t, owner.do("POST", base+"/owner", map[string]any{"userId": bob.id}), 204)
	if s := spaceJSON(t, owner, sp); s["ownerId"] != bob.id || s["role"] != "member" {
		t.Fatalf("after transfer: %v", s)
	}
	expect(t, owner.do("DELETE", base, map[string]any{"confirmName": "Friends"}), 403)
	expect(t, bob.do("DELETE", base+"/members/@me", nil), 403, "owner_cannot_leave")
	expect(t, bob.do("DELETE", base+"/members/"+owner.id, nil), 204) // new owner kicks the old one

	// server admin overview: admins only, sees spaces without being a member
	expect(t, bob.do("GET", "/api/v1/admin/spaces", nil), 403)
	expect(t, bob.do("POST", "/api/v1/admin/spaces/"+sp+"/join", nil), 403)
	list := expect(t, host.do("GET", "/api/v1/admin/spaces", nil), 200).list()
	if len(list) != 1 || list[0].(map[string]any)["member"] != false || str(list[0].(map[string]any), "memberCount") != "2" ||
		str(list[0].(map[string]any), "owner", "id") != bob.id {
		t.Fatalf("admin spaces: %v", list)
	}
	expect(t, host.do("GET", base, nil), 404) // no silent access to contents

	// a ban cannot lock the host out; joining is a normal, visible membership
	expect(t, bob.do("PUT", base+"/bans/"+host.id, nil), 403)
	expect(t, host.do("POST", "/api/v1/admin/spaces/"+sp+"/join", nil), 200)
	expect(t, host.do("GET", base, nil), 200)
	members := expect(t, bob.do("GET", base+"/members", nil), 200).list()
	if len(members) != 3 {
		t.Fatalf("members: %d", len(members))
	}

	// the host outranks the owner, the owner cannot touch the host
	expect(t, bob.do("DELETE", base+"/members/"+host.id, nil), 403)
	expect(t, bob.do("PUT", base+"/bans/"+host.id, nil), 403)
	expect(t, host.do("PATCH", base, map[string]any{"name": "Friends"}), 200)
	expect(t, host.do("POST", base+"/roles", map[string]any{"name": "Mod", "permissions": PermKick}), 201)
	expect(t, host.do("PUT", base+"/bans/"+carol.id, nil), 204)

	// owner change and deletion through the overview
	expect(t, host.do("POST", "/api/v1/admin/spaces/"+sp+"/owner", map[string]any{"userId": host.id}), 204)
	expect(t, host.do("POST", "/api/v1/admin/spaces/"+sp+"/owner", map[string]any{"userId": bob.id}), 204)
	expect(t, bob.do("DELETE", "/api/v1/admin/spaces/"+sp, map[string]any{"confirmName": "Friends"}), 403)
	expect(t, host.do("DELETE", "/api/v1/admin/spaces/"+sp, map[string]any{"confirmName": "nope"}), 400, "confirm_name_mismatch")
	expect(t, host.do("DELETE", "/api/v1/admin/spaces/"+sp, map[string]any{"confirmName": "Friends"}), 204)
	expect(t, host.do("DELETE", "/api/v1/admin/spaces/"+sp, map[string]any{"confirmName": "Friends"}), 404)
	expect(t, bob.do("GET", base, nil), 404)
}
