package server

import (
	"context"
	"strings"
	"testing"

	"github.com/syntstar/lightspeak/server/internal/config"
)

func TestServerInfo(t *testing.T) {
	e := newEnv(t)
	e.cfg.ServerName = "Freunde"
	v := expect(t, e.call("GET", "/api/v1/info", "", nil), 200).json()
	if v["product"] != "lightspeak" || v["name"] != "Freunde" || v["signup"] != config.SignupAdmin || v["protocolVersion"].(float64) != ProtocolVersion {
		t.Fatalf("info: %v", v)
	}
}

// Space invite created through the API by `by` (who must be a space admin).
func (e *testEnv) spaceInvite(by *client, spaceID string) string {
	e.t.Helper()
	r := expect(e.t, by.do("POST", "/api/v1/spaces/"+spaceID+"/invites", map[string]any{}), 201).json()
	return r["code"].(string)
}

func (e *testEnv) tryRegister(code, name string) resp {
	return e.call("POST", "/api/v1/auth/register", "", map[string]any{
		"inviteCode": code, "username": name, "password": testPassword, "deviceName": "pc", "devicePublicKey": fakeKey(name),
	})
}

func TestSignupPolicy(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", true) // server admin
	friend := e.register("friend", false)
	ownerSpace, _, _ := e.spaceWithMembers(owner)
	// a regular user creates their own space and is its admin there
	fs := expect(t, friend.do("POST", "/api/v1/spaces", map[string]any{"name": "Friend's"}), 201).json()
	friendSpace := str(fs, "space", "id")

	adminInv := e.spaceInvite(owner, ownerSpace)
	friendInv := e.spaceInvite(friend, friendSpace)

	// default policy "admin": only the server admin's invites create accounts
	if p := expect(t, e.call("GET", "/api/v1/invites/"+friendInv, "", nil), 200).json(); p["signup"] != false {
		t.Fatalf("friend invite preview: %v", p)
	}
	if p := expect(t, e.call("GET", "/api/v1/invites/"+adminInv, "", nil), 200).json(); p["signup"] != true {
		t.Fatalf("admin invite preview: %v", p)
	}
	expect(t, e.tryRegister(friendInv, "stranger"), 403, "signup_restricted")
	expect(t, e.tryRegister(adminInv, "newbie"), 201)
	// the friend's invite still works for joining with an existing account
	newbie := expect(t, e.call("POST", "/api/v1/auth/login", "", map[string]any{"username": "newbie", "password": testPassword}), 200).json()
	expect(t, e.call("POST", "/api/v1/invites/"+friendInv+"/accept", newbie["token"].(string), nil), 200)

	// "invite": every invite creates accounts
	e.cfg.Signup = config.SignupInvite
	expect(t, e.tryRegister(friendInv, "stranger"), 201)

	// "closed": nothing creates accounts except owner invites
	e.cfg.Signup = config.SignupClosed
	expect(t, e.tryRegister(e.spaceInvite(owner, ownerSpace), "late"), 403, "signup_restricted")
	expect(t, e.tryRegister(e.invite(0, true), "owner2"), 201)
	if v := expect(t, e.call("GET", "/api/v1/info", "", nil), 200).json(); v["signup"] != config.SignupClosed {
		t.Fatalf("info signup: %v", v)
	}
}

func TestOwnerInviteSupersedesPending(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first, _, err := e.s.CreateOwnerInvite(ctx, 3600)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := e.s.CreateOwnerInvite(ctx, 3600)
	if err != nil {
		t.Fatal(err)
	}
	if r := e.tryRegister(first, "early"); r.status < 400 {
		t.Fatalf("superseded owner invite still usable: %d %s", r.status, r.body)
	}
	expect(t, e.tryRegister(second, "owner"), 201)
	// a used owner invite is left alone by the next bootstrap
	third, _, err := e.s.CreateOwnerInvite(ctx, 3600)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, e.tryRegister(third, "owner2"), 201)
	var revoked int
	if err := e.db.R.QueryRow(`SELECT COUNT(*) FROM invites WHERE code=? AND revoked_at IS NOT NULL`, second).Scan(&revoked); err != nil || revoked != 0 {
		t.Fatalf("used invite was revoked: %d %v", revoked, err)
	}
}

func TestLandingCarriesServer(t *testing.T) {
	e := newEnv(t)
	code := e.invite(0, false)
	r := e.raw("GET", "/invite/"+code, "", nil, map[string]string{"Host": "evil.example"})
	body := string(r.body)
	if r.status != 200 || !strings.Contains(body, `href="lightspeak://invite/lv.test/`+code+`"`) {
		t.Fatalf("landing deep link: %d %s", r.status, body)
	}
	if strings.Contains(body, "evil.example") {
		t.Fatal("landing must not use the request Host header")
	}
	e.cfg.DownloadURL = config.DefaultDownloadURL
	if r := e.raw("GET", "/download", "", nil, nil); !strings.Contains(string(r.body), config.DefaultDownloadURL) {
		t.Fatalf("download link: %s", r.body)
	}
}
