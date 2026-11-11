package server

import (
	"context"
	"strings"
	"testing"

	"github.com/syntstar/lightspeak/server/internal/config"
)

func TestOperatorLimits(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	bob := e.register("bob", false)
	carol := e.register("carol", false)
	sp, text, voice := e.spaceWithMembers(owner, bob)
	base := "/api/v1/spaces/" + sp

	t.Run("message length", func(t *testing.T) {
		e.cfg.MaxMessageChars = 10
		msgs := "/api/v1/channels/" + text + "/messages"
		id := str(expect(t, owner.do("POST", msgs, map[string]any{"content": strings.Repeat("ä", 10)}), 201).json(), "id")
		expect(t, owner.do("POST", msgs, map[string]any{"content": strings.Repeat("a", 11)}), 400, "message_too_long")
		expect(t, owner.do("PATCH", msgs+"/"+id, map[string]any{"content": strings.Repeat("a", 11)}), 400, "message_too_long")
		e.cfg.MaxMessageChars = 4000
	})

	t.Run("members", func(t *testing.T) {
		e.cfg.MaxSpaceMembers = 2
		code := e.spaceInvite(owner, sp)
		expect(t, carol.do("POST", "/api/v1/invites/"+code+"/accept", nil), 403, "space_full")
		e.cfg.MaxSpaceMembers = 3
		expect(t, carol.do("POST", "/api/v1/invites/"+code+"/accept", nil), 200)
	})

	t.Run("channels", func(t *testing.T) {
		var n int
		if err := e.db.R.QueryRow(`SELECT COUNT(*) FROM channels WHERE space_id=?`, sp).Scan(&n); err != nil {
			t.Fatal(err)
		}
		e.cfg.MaxSpaceChannels = n
		expect(t, owner.do("POST", base+"/channels", map[string]any{"name": "more", "kind": "text"}), 403, "channel_limit")
		e.cfg.MaxSpaceChannels = 200
		expect(t, owner.do("POST", base+"/channels", map[string]any{"name": "more", "kind": "text"}), 201)
	})

	t.Run("roles", func(t *testing.T) {
		e.cfg.MaxSpaceRoles = 1 // the default "Admin" role already exists
		expect(t, owner.do("POST", base+"/roles", map[string]any{"name": "mods"}), 403, "role_limit")
		e.cfg.MaxSpaceRoles = 25
	})

	t.Run("voice participants bind moderators too", func(t *testing.T) {
		e.cfg.MaxVoiceParticipants = 1
		join := map[string]any{"channelId": voice}
		expect(t, bob.do("POST", "/api/v1/voice/join", join), 200)
		expect(t, owner.do("POST", "/api/v1/voice/join", join), 403, "channel_full")
		expect(t, bob.do("POST", "/api/v1/voice/join", join), 200) // rejoining keeps the seat
		e.cfg.MaxVoiceParticipants = 2
		expect(t, owner.do("POST", "/api/v1/voice/join", join), 200)
	})

	t.Run("ready carries the limits", func(t *testing.T) {
		e.cfg.MaxMessageChars, e.cfg.MaxSpaceRoles, e.cfg.Invites = 123, 7, config.InvitesAdmins
		defer func() { e.cfg.MaxMessageChars, e.cfg.MaxSpaceRoles, e.cfg.Invites = 4000, 25, config.InvitesMembers }()
		p, err := e.s.buildReady(context.Background(), &Session{UserID: mustID(owner.id)}, "s")
		if err != nil {
			t.Fatal(err)
		}
		want := limitsDTO{123, e.cfg.MaxUploadBytes, 7, config.InvitesAdmins}
		if p.Limits != want {
			t.Fatalf("limits: %+v, want %+v", p.Limits, want)
		}
	})
}

func TestInvitePolicyAdminsOnly(t *testing.T) {
	e := newEnv(t)
	host := e.register("host", true) // server admin
	owner := e.register("owner", false)
	bob := e.register("bob", false)
	carol := e.register("carol", false)
	dave := e.register("dave", false)
	sp, _, _ := e.spaceWithMembers(owner)
	mine := e.spaceInvite(owner, sp) // made while everyone may invite
	preview := func(code string, status int) { expect(t, e.call("GET", "/api/v1/invites/"+code, "", nil), status) }
	preview(mine, 200)

	e.cfg.Invites = config.InvitesAdmins
	e.cfg.Signup = config.SignupInvite // this rule must not depend on LS_SIGNUP

	// even the space owner may not create one any more ...
	expect(t, owner.do("POST", "/api/v1/spaces/"+sp+"/invites", map[string]any{}), 403, "invites_restricted")
	// ... and links from before the change are dead on every path
	preview(mine, 404)
	expect(t, bob.do("POST", "/api/v1/invites/"+mine+"/accept", nil), 404, "invite_invalid")
	expect(t, e.tryRegister(mine, "newbie"), 404, "invite_invalid")
	expect(t, e.call("GET", "/invite/"+mine, "", nil), 404)

	// a server admin may invite, also into a space they just joined
	expect(t, e.call("POST", "/api/v1/invites/"+e.invite(mustID(sp), false)+"/accept", host.token, nil), 200)
	code := e.spaceInvite(host, sp)
	preview(code, 200)
	expect(t, bob.do("POST", "/api/v1/invites/"+code+"/accept", nil), 200)

	// the command line stays the operator's way in
	cli := e.invite(mustID(sp), false)
	expect(t, carol.do("POST", "/api/v1/invites/"+cli+"/accept", nil), 200)

	// losing the admin flag ends that person's links as well
	if _, err := e.db.W.Exec(`UPDATE users SET is_server_admin=0 WHERE id=?`, mustID(host.id)); err != nil {
		t.Fatal(err)
	}
	expect(t, dave.do("POST", "/api/v1/invites/"+code+"/accept", nil), 404, "invite_invalid")

	// back to the default: the old links work again
	e.cfg.Invites = config.InvitesMembers
	preview(mine, 200)
	expect(t, owner.do("POST", "/api/v1/spaces/"+sp+"/invites", map[string]any{}), 201)
}
