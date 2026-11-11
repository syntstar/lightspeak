package server

import (
	"slices"
	"testing"
)

// channelIDs returns the IDs of a channel list (JSON).
func channelIDs(list any) []string {
	var out []string
	for _, c := range list.([]any) {
		out = append(out, c.(map[string]any)["id"].(string))
	}
	return out
}

// TestPrivateChannels: without the view permission a channel does
// not exist for a member – not in lists, events, search or voice.
func TestPrivateChannels(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	vip := e.register("vip", false)
	mem := e.register("member", false)
	mod := e.register("mod", false)
	sp, text, voice := e.spaceWithMembers(owner, vip, mem, mod)
	base := "/api/v1/spaces/" + sp
	ch := "/api/v1/channels/" + text
	vipRole := expect(t, owner.do("POST", base+"/roles", map[string]any{"name": "VIP", "permissions": 0}), 201).json()["id"].(string)
	expect(t, owner.role(sp, vip.id, vipRole, true), 200)
	modRole := expect(t, owner.do("POST", base+"/roles", map[string]any{"name": "Mod", "permissions": PermManageChannels | PermManageRoles | PermVoiceModerate}), 201).json()["id"].(string)
	expect(t, owner.role(sp, mod.id, modRole, true), 200)
	if got := spaceJSON(t, mem, sp)["everyonePerms"]; got != float64(permEveryoneDefault) || permEveryoneDefault&PermView == 0 {
		t.Fatalf("everyonePerms: %v", got)
	}

	wm := e.dial(t, map[string]any{"token": mem.token})
	wm.wait("ready")
	wv := e.dial(t, map[string]any{"token": vip.token})
	wv.wait("ready")

	private := overrides(ov{"roleId": sp, "deny": PermView}, ov{"roleId": vipRole, "allow": PermView})
	expect(t, owner.do("PATCH", ch, private), 200)
	// who lost it is told it is gone, who keeps it gets the update
	wm.waitMatch("channel_delete", func(d map[string]any) bool { return d["id"] == text })
	wv.waitMatch("channel_update", func(d map[string]any) bool { return d["id"] == text })

	// it does not exist for the member
	lists := func(c *client) []string { return channelIDs(expect(t, c.do("GET", base, nil), 200).json()["channels"]) }
	if slices.Contains(lists(mem), text) || !slices.Contains(lists(vip), text) || !slices.Contains(lists(owner), text) {
		t.Fatal("channel list not filtered")
	}
	expect(t, mem.do("GET", ch+"/messages", nil), 404)
	expect(t, mem.do("POST", ch+"/messages", map[string]any{"content": "x"}), 404)
	expect(t, mem.do("PATCH", ch, map[string]any{"name": "x"}), 404)
	expect(t, mod.do("PATCH", ch, map[string]any{"name": "x"}), 404) // managers too, unless allowed
	expect(t, mem.upload(text, "a.txt", []byte("x"), nil), 404)

	// messages reach only viewers; mentions of others are dropped
	msg := expect(t, vip.do("POST", ch+"/messages", map[string]any{"content": "secret plan <@" + mem.id + ">"}), 201).json()
	wv.waitMatch("message_create", func(d map[string]any) bool { return d["id"] == msg["id"] })
	if wm.count("message_create", func(map[string]any) bool { return true }) != 0 {
		t.Fatal("message leaked to a non-viewer")
	}
	var mentions int
	_ = e.db.R.QueryRow(`SELECT COUNT(*) FROM read_states WHERE user_id=? AND mention_count>0`, mem.id).Scan(&mentions)
	if mentions != 0 {
		t.Fatal("non-viewer was mentioned")
	}
	if n := len(expect(t, mem.do("GET", "/api/v1/search?q=secret", nil), 200).list()); n != 0 {
		t.Fatal("search leaked")
	}
	if n := len(expect(t, vip.do("GET", "/api/v1/search?q=secret", nil), 200).list()); n != 1 {
		t.Fatalf("viewer search: %d", n)
	}
	// ready snapshot
	if ids := channelIDs(e.dial(t, map[string]any{"token": mem.token}).wait("ready")["d"].(map[string]any)["channels"]); slices.Contains(ids, text) {
		t.Fatal("ready leaked the channel")
	}

	// losing the role hides it at once, getting it back shows it
	expect(t, owner.role(sp, vip.id, vipRole, false), 200)
	wv.waitMatch("space_channels", func(d map[string]any) bool { return !slices.Contains(channelIDs(d["channels"]), text) })
	expect(t, vip.do("GET", ch+"/messages", nil), 404)
	expect(t, owner.role(sp, vip.id, vipRole, true), 200)
	wv.waitMatch("space_channels", func(d map[string]any) bool { return slices.Contains(channelIDs(d["channels"]), text) })
	// making it public again creates it for the member
	expect(t, owner.do("PATCH", ch, overrides()), 200)
	wm.waitMatch("channel_create", func(d map[string]any) bool { return d["id"] == text })
	expect(t, mem.do("GET", ch+"/messages", nil), 200)

	// voice: no join, no voice states, no moves into it
	join := func(c *client, id string) resp {
		return c.do("POST", "/api/v1/voice/join", map[string]any{"channelId": id})
	}
	vc := "/api/v1/channels/" + voice
	expect(t, owner.do("PATCH", vc, private), 200)
	expect(t, join(mem, voice), 404)
	expect(t, join(vip, voice), 200)
	if wm.count("voice_state_update", func(d map[string]any) bool { return d["userId"] == vip.id && d["channelId"] != nil }) != 0 {
		t.Fatal("voice state leaked")
	}
	for _, st := range e.dial(t, map[string]any{"token": mem.token}).wait("ready")["d"].(map[string]any)["voiceStates"].([]any) {
		if st.(map[string]any)["userId"] == vip.id {
			t.Fatal("ready voice state leaked")
		}
	}
	other := expect(t, owner.do("POST", base+"/channels", map[string]any{"kind": "voice", "name": "Open"}), 201).json()["id"].(string)
	expect(t, join(mem, other), 200)
	expect(t, mod.do("POST", "/api/v1/voice/"+other+"/moderate", map[string]any{"userId": mem.id, "action": "move", "targetChannelId": voice}), 400, "invalid_target_channel")
	expect(t, owner.do("POST", "/api/v1/voice/"+other+"/moderate", map[string]any{"userId": mem.id, "action": "move", "targetChannelId": voice}), 400, "invalid_target_channel")
	// a viewer moving out of the private channel only shows up as "left" there
	// for others; taking the view away disconnects
	expect(t, owner.role(sp, vip.id, vipRole, false), 200)
	waitFor(t, 3e9, func() bool {
		e.s.voice.mu.Lock()
		defer e.s.voice.mu.Unlock()
		st := e.s.voice.states[mustID(vip.id)]
		return st == nil || st.ChannelID == nil
	})

	// the away channel is always visible
	expect(t, owner.do("PATCH", vc, map[string]any{"mode": "away", "overrides": []ov{{"roleId": sp, "deny": PermView}}}), 200)
	expect(t, join(mem, voice), 200)
}
