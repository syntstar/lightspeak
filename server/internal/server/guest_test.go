package server

import (
	"context"
	"testing"
	"time"

	"github.com/syntstar/lightspeak/server/internal/config"
)

func (e *testEnv) guestInvite(by *client, spaceID string) string {
	e.t.Helper()
	r := expect(e.t, by.do("POST", "/api/v1/spaces/"+spaceID+"/invites", map[string]any{"guest": true}), 201).json()
	if r["guest"] != true {
		e.t.Fatalf("invite is not a guest invite: %v", r)
	}
	return r["code"].(string)
}

func (e *testEnv) tryGuest(code, name string) resp {
	return e.call("POST", "/api/v1/auth/guest", "", map[string]any{
		"inviteCode": code, "displayName": name, "deviceName": "browser", "devicePublicKey": fakeKey(name),
	})
}

func (e *testEnv) guest(code, name string) *client {
	e.t.Helper()
	m := expect(e.t, e.tryGuest(code, name), 201).json()
	if str(m, "user", "guest") != "true" || str(m, "user", "displayName") != name {
		e.t.Fatalf("guest user: %v", m["user"])
	}
	return &client{env: e, token: m["token"].(string), id: str(m, "user", "id"), deviceID: str(m, "device", "id"), name: str(m, "user", "username")}
}

func (e *testEnv) userExists(id string) (exists, deleted bool) {
	var del int
	err := e.db.R.QueryRow(`SELECT deleted_at IS NOT NULL FROM users WHERE id=?`, id).Scan(&del)
	return err == nil, del == 1
}

func isMember(t *testing.T, c *client, sp, uid string) (member, guest bool) {
	t.Helper()
	for _, m := range expect(t, c.do("GET", "/api/v1/spaces/"+sp+"/members", nil), 200).list() {
		mm := m.(map[string]any)
		if mm["userId"] == uid {
			return true, str(mm, "user", "guest") == "true"
		}
	}
	return false, false
}

// TestGuestAccess: a guest invite lets people in with just a name; the guest
// is limited to that space and disappears with its session.
func TestGuestAccess(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", true)
	sp, text, _ := e.spaceWithMembers(owner)
	code := e.guestInvite(owner, sp)

	if p := expect(t, e.call("GET", "/api/v1/invites/"+code, "", nil), 200).json(); p["guest"] != true {
		t.Fatalf("preview: %v", p)
	}
	plain := e.spaceInvite(owner, sp)
	if p := expect(t, e.call("GET", "/api/v1/invites/"+plain, "", nil), 200).json(); p["guest"] != false {
		t.Fatalf("plain preview: %v", p)
	}
	expect(t, e.tryGuest(plain, "Sneaky"), 403, "guest_not_allowed")
	expect(t, e.tryGuest(code, "   "), 400, "invalid_display_name")
	// a guest invite still creates regular accounts and admits existing ones
	expect(t, e.tryRegister(code, "regular"), 201)

	g := e.guest(code, "Tom")
	if in, guest := isMember(t, owner, sp, g.id); !in || !guest {
		t.Fatalf("guest not listed as guest member: %v %v", in, guest)
	}
	// the session is short-lived compared to accounts
	var exp int64
	_ = e.db.R.QueryRow(`SELECT expires_at FROM sessions WHERE user_id=?`, g.id).Scan(&exp)
	if d := time.Duration(exp-e.s.nowMs()) * time.Millisecond; d > guestSessionTTL || d < guestSessionTTL-time.Minute {
		t.Fatalf("guest session ttl: %v", d)
	}

	// what a guest may not do
	expect(t, g.do("POST", "/api/v1/spaces", map[string]any{"name": "Mine"}), 403, "guest_restricted")
	expect(t, g.do("POST", "/api/v1/spaces/"+sp+"/invites", map[string]any{}), 403)
	expect(t, g.do("POST", "/api/v1/invites/"+plain+"/accept", nil), 403, "guest_restricted")
	expect(t, g.do("POST", "/api/v1/relationships", map[string]any{"username": "owner"}), 403, "guest_restricted")
	expect(t, owner.do("PUT", "/api/v1/relationships/"+g.id, map[string]any{"kind": "friend"}), 403, "guest_restricted")
	expect(t, owner.do("PUT", "/api/v1/relationships/"+g.id, map[string]any{"kind": "blocked"}), 200)
	expect(t, owner.do("DELETE", "/api/v1/relationships/"+g.id, nil), 204)
	expect(t, g.do("PATCH", "/api/v1/users/@me", map[string]any{"username": "tom"}), 403, "guest_restricted")
	expect(t, g.do("PATCH", "/api/v1/users/@me", map[string]any{"displayName": "Tommy"}), 200)
	expect(t, g.do("POST", "/api/v1/users/@me/password", map[string]any{"oldPassword": "", "newPassword": testPassword}), 403, "guest_restricted")
	expect(t, g.do("POST", "/api/v1/users/@me/delete", map[string]any{"password": ""}), 403, "guest_restricted")
	expect(t, owner.do("POST", "/api/v1/spaces/"+sp+"/owner", map[string]any{"userId": g.id}), 403, "guest_restricted")
	// nobody can sign in to a guest
	expect(t, e.call("POST", "/api/v1/auth/login", "", map[string]any{"username": g.name, "password": ""}), 401)

	// signing out ends the guest; its message keeps an anonymised author
	expect(t, g.do("POST", "/api/v1/channels/"+text+"/messages", map[string]any{"content": "hi"}), 201)
	expect(t, g.do("POST", "/api/v1/auth/logout", nil), 204)
	expect(t, g.do("GET", "/api/v1/users/@me", nil), 401)
	if in, _ := isMember(t, owner, sp, g.id); in {
		t.Fatal("guest still a member after signing out")
	}
	if exists, deleted := e.userExists(g.id); !exists || !deleted {
		t.Fatalf("guest with messages: exists=%v deleted=%v", exists, deleted)
	}

	// a guest who wrote nothing leaves no row behind
	quiet := e.guest(code, "Quiet")
	expect(t, quiet.do("POST", "/api/v1/auth/logout", nil), 204)
	if exists, _ := e.userExists(quiet.id); exists {
		t.Fatal("silent guest was not removed completely")
	}

	// removed from the space: the guest is gone at once
	kicked := e.guest(code, "Kicked")
	expect(t, owner.do("DELETE", "/api/v1/spaces/"+sp+"/members/"+kicked.id, nil), 204)
	expect(t, kicked.do("GET", "/api/v1/users/@me", nil), 401)
	if exists, _ := e.userExists(kicked.id); exists {
		t.Fatal("kicked guest still exists")
	}

	// session lost (app data deleted): maintenance removes the guest
	lost := e.guest(code, "Lost")
	stays := e.guest(code, "Stays")
	if _, err := e.db.W.Exec(`UPDATE sessions SET expires_at=1 WHERE user_id=?`, lost.id); err != nil {
		t.Fatal(err)
	}
	if n := e.s.purgeGuests(context.Background()); n != 1 {
		t.Fatalf("purged %d guests, want 1", n)
	}
	if exists, _ := e.userExists(lost.id); exists {
		t.Fatal("guest without a session still exists")
	}
	expect(t, stays.do("GET", "/api/v1/users/@me", nil), 200)
	// regular accounts are never touched
	if _, err := e.db.W.Exec(`UPDATE sessions SET expires_at=1 WHERE user_id=?`, owner.id); err != nil {
		t.Fatal(err)
	}
	if n := e.s.purgeGuests(context.Background()); n != 0 {
		t.Fatalf("purged %d, want 0", n)
	}
	if exists, deleted := e.userExists(owner.id); !exists || deleted {
		t.Fatal("regular account was purged")
	}
}

// TestGuestSignupPolicy: guests are new accounts, so LS_SIGNUP decides whose
// guest invites work.
func TestGuestSignupPolicy(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", true)
	friend := e.register("friend", false)
	ownerSpace, _, _ := e.spaceWithMembers(owner)
	friendSpace := str(expect(t, friend.do("POST", "/api/v1/spaces", map[string]any{"name": "Friend's"}), 201).json(), "space", "id")
	adminInv := e.guestInvite(owner, ownerSpace)
	friendInv := e.guestInvite(friend, friendSpace)

	// default "admin": only the server admin's invites admit guests
	if p := expect(t, e.call("GET", "/api/v1/invites/"+friendInv, "", nil), 200).json(); p["guest"] != false {
		t.Fatalf("friend invite preview: %v", p)
	}
	expect(t, e.tryGuest(friendInv, "Ann"), 403, "guest_not_allowed")
	e.guest(adminInv, "Ann")

	e.cfg.Signup = config.SignupInvite
	e.guest(friendInv, "Ben")

	e.cfg.Signup = config.SignupClosed
	expect(t, e.tryGuest(adminInv, "Cem"), 403, "guest_not_allowed")

	// a full space takes no guests either
	e.cfg.Signup = config.SignupInvite
	e.cfg.MaxSpaceMembers = 2 // friend + Ben
	expect(t, e.tryGuest(friendInv, "Dan"), 403, "space_full")
}
