package server

import (
	"slices"
	"testing"
	"time"
)

type ov = map[string]any

func overrides(list ...ov) map[string]any {
	if list == nil {
		list = []ov{}
	}
	return map[string]any{"overrides": list}
}

func TestChannelPermsText(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	news := e.register("news", false)
	mem := e.register("member", false)
	mod := e.register("mod", false)
	out := e.register("outsider", false)
	sp, text, voice := e.spaceWithMembers(owner, news, mem, mod)
	base := "/api/v1/spaces/" + sp
	ch := "/api/v1/channels/" + text
	newsRole := expect(t, owner.do("POST", base+"/roles", map[string]any{"name": "News", "permissions": 0}), 201).json()["id"].(string)
	expect(t, owner.role(sp, news.id, newsRole, true), 200)
	msg := func(c *client) resp {
		return c.do("POST", ch+"/messages", map[string]any{"content": "hi"})
	}

	// @everyone starts with the channel permissions
	if got := spaceJSON(t, mem, sp)["everyonePerms"]; got != float64(permEveryoneDefault) {
		t.Fatalf("everyonePerms: %v", got)
	}

	// authorization and validation
	announce := overrides(ov{"roleId": sp, "deny": PermSend}, ov{"roleId": newsRole, "allow": PermSend})
	expect(t, mem.do("PATCH", ch, announce), 403)
	expect(t, out.do("PATCH", ch, announce), 404)
	expect(t, owner.do("PATCH", ch, map[string]any{"mode": "announce"}), 400, "invalid_mode")
	expect(t, owner.do("PATCH", ch, map[string]any{"mode": "away"}), 400, "invalid_mode")
	expect(t, owner.do("PATCH", ch, map[string]any{"slowmode": 7}), 400, "invalid_slowmode")
	expect(t, owner.do("PATCH", ch, map[string]any{"audio": "music"}), 400, "invalid_audio")
	expect(t, owner.do("PATCH", "/api/v1/channels/"+voice, map[string]any{"slowmode": 5}), 400, "invalid_slowmode")
	expect(t, owner.do("PATCH", ch, overrides(ov{"roleId": sp, "deny": PermSpeak})), 400, "invalid_overrides")
	expect(t, owner.do("PATCH", ch, overrides(ov{"roleId": sp, "deny": PermSend, "allow": PermSend})), 400, "invalid_overrides")
	expect(t, owner.do("PATCH", ch, overrides(ov{"roleId": sp, "deny": PermSend}, ov{"roleId": sp, "allow": PermAttach})), 400, "invalid_overrides")
	expect(t, owner.do("PATCH", ch, overrides(ov{"roleId": "12345", "deny": PermSend})), 400, "unknown_role")
	sp2, _, _ := e.spaceWithMembers(out)
	other := expect(t, out.do("POST", "/api/v1/spaces/"+sp2+"/roles", map[string]any{"name": "X", "permissions": 0}), 201).json()["id"].(string)
	expect(t, owner.do("PATCH", ch, overrides(ov{"roleId": other, "deny": PermSend})), 400, "unknown_role")

	// announcement channel: only News (and Administrators) write
	d := expect(t, owner.do("PATCH", ch, announce), 200).json()
	if len(d["overrides"].([]any)) != 2 {
		t.Fatalf("dto: %v", d)
	}
	expect(t, msg(mem), 403, "send_denied")
	expect(t, msg(news), 201)
	expect(t, msg(owner), 201)
	// the list endpoint carries the overrides too
	found := false
	for _, c := range expect(t, mem.do("GET", base, nil), 200).json()["channels"].([]any) {
		if cm := c.(map[string]any); cm["id"] == text {
			found = len(cm["overrides"].([]any)) == 2
		}
	}
	if !found {
		t.Fatal("space channels lack overrides")
	}
	// deleting the role drops its override and closes the channel to its former members
	expect(t, owner.do("DELETE", base+"/roles/"+newsRole, nil), 204)
	expect(t, msg(news), 403, "send_denied")
	expect(t, owner.do("PATCH", ch, overrides()), 200)
	expect(t, msg(mem), 201)

	// files and reactions
	expect(t, owner.do("PATCH", ch, overrides(ov{"roleId": sp, "deny": PermAttach | PermReact})), 200)
	expect(t, mem.do("POST", ch+"/messages", map[string]any{"content": "x", "attachmentIds": []string{"1"}}), 403, "attach_denied")
	expect(t, mem.upload(text, "a.txt", []byte("hello"), nil), 403, "attach_denied")
	mid := str(expect(t, msg(mem), 201).json(), "id")
	expect(t, mem.do("PUT", ch+"/messages/"+mid+"/reactions/%F0%9F%91%8D", nil), 403, "react_denied")
	expect(t, owner.do("PUT", ch+"/messages/"+mid+"/reactions/%F0%9F%91%8D", nil), 204)

	// the space-wide value is the default; a channel can allow it again
	expect(t, owner.do("PATCH", ch, overrides()), 200)
	expect(t, owner.do("PATCH", base+"/roles/"+sp, map[string]any{"permissions": permEveryoneDefault &^ PermSend}), 204)
	expect(t, msg(mem), 403, "send_denied")
	expect(t, owner.do("PATCH", ch, overrides(ov{"roleId": sp, "allow": PermSend})), 200)
	expect(t, msg(mem), 201)

	// a moderator needs Manage roles for overrides, may only touch roles
	// below them and only bits they hold
	modRole := expect(t, owner.do("POST", base+"/roles", map[string]any{"name": "Mod", "permissions": PermManageChannels}), 201).json()["id"].(string)
	expect(t, owner.role(sp, mod.id, modRole, true), 200)
	expect(t, mod.do("PATCH", ch, map[string]any{"name": "news", "overrides": []ov{{"roleId": sp, "allow": PermSend}}}), 200) // unchanged
	expect(t, mod.do("PATCH", ch, overrides()), 403)
	expect(t, owner.do("PATCH", base+"/roles/"+modRole, map[string]any{"permissions": PermManageChannels | PermManageRoles}), 204)
	expect(t, mod.do("PATCH", ch, overrides(ov{"roleId": sp, "allow": PermSend, "deny": PermReact})), 200)
	expect(t, mod.do("PATCH", ch, overrides(ov{"roleId": sp, "allow": PermSend}, ov{"roleId": modRole, "deny": PermAttach})), 403) // own rank
	low := expect(t, owner.do("POST", base+"/roles", map[string]any{"name": "Low", "permissions": 0}), 201).json()["id"].(string)
	expect(t, mod.do("PATCH", ch, overrides(ov{"roleId": sp, "allow": PermSend}, ov{"roleId": low, "allow": PermMentionEveryone})), 403) // not held
	expect(t, mod.do("PATCH", ch, overrides(ov{"roleId": sp, "allow": PermSend}, ov{"roleId": low, "deny": PermAttach})), 200)

	// slow mode: one message per 30 s; managers of messages are exempt
	now := time.Now()
	e.s.now = func() time.Time { return now }
	expect(t, owner.do("PATCH", ch, map[string]any{"slowmode": 30}), 200)
	expect(t, msg(mem), 201)
	expect(t, msg(mem), 429, "slow_mode")
	expect(t, msg(owner), 201)
	expect(t, msg(owner), 201)
	now = now.Add(31 * time.Second)
	expect(t, msg(mem), 201)
	// an invalid message does not use up the slot
	now = now.Add(31 * time.Second)
	expect(t, mem.do("POST", ch+"/messages", map[string]any{"content": ""}), 400)
	expect(t, msg(mem), 201)
}

func TestChannelPermsVoice(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	host := e.register("host", false)
	mem := e.register("member", false)
	sp, _, voice := e.spaceWithMembers(owner, host, mem)
	base := "/api/v1/spaces/" + sp
	ch := "/api/v1/channels/" + voice
	hostRole := expect(t, owner.do("POST", base+"/roles", map[string]any{"name": "Host", "permissions": 0}), 201).json()["id"].(string)
	expect(t, owner.role(sp, host.id, hostRole, true), 200)
	join := func(c *client, id string) resp {
		return c.do("POST", "/api/v1/voice/join", map[string]any{"channelId": id})
	}
	sources := func(j map[string]any) []string {
		g := grants(t, e, j["token"].(string)).Video
		if !g.GetCanPublish() {
			return nil
		}
		return g.CanPublishSources
	}
	state := func(c *client) VoiceState {
		e.s.voice.mu.Lock()
		defer e.s.voice.mu.Unlock()
		if st := e.s.voice.states[mustID(c.id)]; st != nil {
			return *st
		}
		return VoiceState{}
	}

	expect(t, owner.do("PATCH", ch, overrides(ov{"roleId": sp, "deny": PermSend})), 400, "invalid_overrides")

	// stage: only Host speaks and streams, everyone else listens
	stage := overrides(ov{"roleId": sp, "deny": PermSpeak | PermStream}, ov{"roleId": hostRole, "allow": PermSpeak | PermStream})
	expect(t, owner.do("PATCH", ch, stage), 200)
	if src := sources(expect(t, join(mem, voice), 200).json()); src != nil || !state(mem).Listener || !state(mem).NoScreen {
		t.Fatalf("stage listener publishes %v", src)
	}
	if src := sources(expect(t, join(host, voice), 200).json()); !slices.Contains(src, "microphone") || state(host).Listener {
		t.Fatalf("stage speaker: %v", src)
	}
	if !slices.Contains(sources(expect(t, join(owner, voice), 200).json()), "microphone") {
		t.Fatal("administrators always speak")
	}
	// giving the role later lifts the listener state live
	expect(t, owner.role(sp, mem.id, hostRole, true), 200)
	if state(mem).Listener {
		t.Fatal("role grant should make member a speaker")
	}
	expect(t, owner.role(sp, mem.id, hostRole, false), 200)
	if !state(mem).Listener {
		t.Fatal("role removal should make member a listener again")
	}

	// no screen sharing only: microphone yes, screen no
	expect(t, owner.do("PATCH", ch, overrides(ov{"roleId": sp, "deny": PermStream})), 200)
	if st := state(mem); st.Listener || !st.NoScreen {
		t.Fatalf("live update: %+v", st)
	}
	if src := sources(expect(t, join(mem, voice), 200).json()); !slices.Equal(src, []string{"microphone"}) {
		t.Fatalf("no-stream sources: %v", src)
	}

	// audio quality
	if d := expect(t, owner.do("PATCH", ch, map[string]any{"audio": "music"}), 200).json(); d["audio"] != "music" {
		t.Fatalf("audio: %v", d)
	}
	expect(t, owner.do("PATCH", ch, map[string]any{"audio": "loud"}), 400, "invalid_audio")

	// away channel: nobody talks, even the owner; only one per space
	d := expect(t, owner.do("POST", base+"/channels", map[string]any{"kind": "voice", "name": "AFK", "mode": "away", "awayAfter": 15}), 201).json()
	afk := d["id"].(string)
	if d["mode"] != "away" || str(d, "awayAfter") != "15" || d["audio"] != "speech" {
		t.Fatalf("away dto: %v", d)
	}
	if sources(expect(t, join(owner, afk), 200).json()) != nil {
		t.Fatal("nobody talks in the away channel")
	}
	expect(t, owner.do("PATCH", "/api/v1/channels/"+afk, map[string]any{"awayAfter": 7}), 400, "invalid_away_after")
	// everyone may enter the away channel, whatever its overrides say
	expect(t, owner.do("PATCH", "/api/v1/channels/"+afk, overrides(ov{"roleId": sp, "deny": PermConnect})), 200)
	expect(t, join(mem, afk), 200)
	expect(t, owner.do("PATCH", "/api/v1/channels/"+afk, overrides()), 200)
	expect(t, join(mem, voice), 200) // back where the next steps expect them
	expect(t, owner.do("POST", base+"/channels", map[string]any{"kind": "voice", "name": "AFK2", "mode": "away"}), 201)
	for _, c := range expect(t, owner.do("GET", base, nil), 200).json()["channels"].([]any) {
		if cm := c.(map[string]any); cm["id"] == afk && cm["mode"] != "open" {
			t.Fatalf("previous away channel should be open again: %v", cm)
		}
	}

	// connect denied: only Host may join; who loses access is disconnected
	expect(t, owner.do("PATCH", ch, overrides(ov{"roleId": sp, "deny": PermConnect}, ov{"roleId": hostRole, "allow": PermConnect})), 200)
	if state(mem).ChannelID != nil || state(host).ChannelID == nil {
		t.Fatalf("locking: member %+v host %+v", state(mem), state(host))
	}
	expect(t, join(mem, voice), 403, "channel_locked")
	expect(t, join(host, voice), 200)
	expect(t, join(owner, voice), 200)
	// a moderator may move someone in; that person then stays
	expect(t, join(mem, afk), 200)
	expect(t, owner.do("POST", "/api/v1/voice/"+afk+"/moderate", map[string]any{"userId": mem.id, "action": "move", "targetChannelId": voice}), 204)
	expect(t, join(mem, voice), 200)
	expect(t, owner.do("PATCH", ch, map[string]any{"topic": "x", "audio": "speech"}), 200)
	if state(mem).ChannelID == nil {
		t.Fatal("moved-in member should stay")
	}
	// the ticket is used up
	expect(t, join(mem, afk), 200)
	expect(t, join(mem, voice), 403, "channel_locked")
	// connect denied space-wide, allowed in one channel
	expect(t, owner.do("PATCH", ch, overrides(ov{"roleId": sp, "allow": PermConnect})), 200)
	expect(t, owner.do("PATCH", base+"/roles/"+sp, map[string]any{"permissions": permEveryoneDefault &^ PermConnect}), 204)
	expect(t, join(mem, afk), 403, "channel_locked")
	expect(t, join(mem, voice), 200)
}
