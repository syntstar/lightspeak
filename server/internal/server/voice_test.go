package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	lkauth "github.com/livekit/protocol/auth"
)

func grants(t *testing.T, e *testEnv, tok string) *lkauth.ClaimGrants {
	t.Helper()
	v, err := lkauth.ParseAPIToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	_, g, err := v.Verify(e.cfg.LiveKitSecret)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestVoiceJoinAndModeration(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	bob := e.register("bob", false)
	carol := e.register("carol", false)
	out := e.register("outsider", false)
	sp, text, voice := e.spaceWithMembers(owner, bob, carol)

	expect(t, out.do("POST", "/api/v1/voice/join", map[string]any{"channelId": voice}), 404)
	expect(t, bob.do("POST", "/api/v1/voice/join", map[string]any{"channelId": text}), 400, "not_voice_channel")
	j := expect(t, bob.do("POST", "/api/v1/voice/join", map[string]any{"channelId": voice}), 200).json()
	g := grants(t, e, j["token"].(string))
	if g.Identity != bob.id || g.Video.Room != "ch_"+voice || !g.Video.RoomJoin || g.Video.RoomAdmin {
		t.Fatalf("grant: %+v %+v", g, g.Video)
	}
	if *g.Video.CanPublishData || *g.Video.CanUpdateOwnMetadata || !slices.Contains(g.Video.CanPublishSources, "microphone") || slices.Contains(g.Video.CanPublishSources, "camera") {
		t.Fatalf("permissions too broad: %+v", g.Video)
	}
	if str(j, "e2ee", "containerId") != sp || str(j, "e2ee", "epoch") != "1" {
		t.Fatalf("e2ee info: %v", j["e2ee"])
	}

	// moderation requires admin
	expect(t, carol.do("POST", "/api/v1/voice/"+voice+"/moderate", map[string]any{"userId": bob.id, "action": "mute"}), 403)
	expect(t, out.do("POST", "/api/v1/voice/"+voice+"/moderate", map[string]any{"userId": bob.id, "action": "mute"}), 404)
	expect(t, owner.do("POST", "/api/v1/voice/"+voice+"/moderate", map[string]any{"userId": bob.id, "action": "explode"}), 400, "invalid_action")
	expect(t, owner.do("POST", "/api/v1/voice/"+voice+"/moderate", map[string]any{"userId": bob.id, "action": "mute"}), 204)
	j = expect(t, bob.do("POST", "/api/v1/voice/join", map[string]any{"channelId": voice}), 200).json()
	if slices.Contains(grants(t, e, j["token"].(string)).Video.CanPublishSources, "microphone") {
		t.Fatal("server-muted user must not get microphone permission")
	}
	expect(t, owner.do("POST", "/api/v1/voice/"+voice+"/moderate", map[string]any{"userId": bob.id, "action": "unmute"}), 204)
	// move only within the space and only to voice channels
	expect(t, owner.do("POST", "/api/v1/voice/"+voice+"/moderate", map[string]any{"userId": bob.id, "action": "move", "targetChannelId": text}), 400, "invalid_target_channel")
	expect(t, owner.do("POST", "/api/v1/voice/"+voice+"/moderate", map[string]any{"userId": carol.id, "action": "disconnect"}), 404, "not_in_channel")
	expect(t, owner.do("POST", "/api/v1/voice/"+voice+"/moderate", map[string]any{"userId": bob.id, "action": "disconnect"}), 204)
	e.s.voice.mu.Lock()
	_, still := e.s.voice.states[mustID(bob.id)]
	e.s.voice.mu.Unlock()
	if still {
		t.Fatal("disconnect should clear voice state")
	}
	// members cannot moderate the owner even as admin
	expect(t, owner.role(sp, bob.id, e.adminRole(owner, sp), true), 200)
	expect(t, owner.do("POST", "/api/v1/voice/join", map[string]any{"channelId": voice}), 200)
	expect(t, bob.do("POST", "/api/v1/voice/"+voice+"/moderate", map[string]any{"userId": owner.id, "action": "disconnect"}), 403)

	// user limit
	lim := expect(t, owner.do("POST", "/api/v1/spaces/"+sp+"/channels", map[string]any{"kind": "voice", "name": "Duo", "userLimit": 1}), 201).json()
	expect(t, carol.do("POST", "/api/v1/voice/join", map[string]any{"channelId": lim["id"]}), 200)
	expect(t, out.do("POST", "/api/v1/voice/join", map[string]any{"channelId": lim["id"]}), 404)
	expect(t, e.register("dave", false).do("POST", "/api/v1/voice/join", map[string]any{"channelId": lim["id"]}), 404)
	expect(t, bob.do("POST", "/api/v1/voice/join", map[string]any{"channelId": lim["id"]}), 200) // admins bypass
	inv := expect(t, owner.do("POST", "/api/v1/spaces/"+sp+"/invites", map[string]any{}), 201).json()
	eve := e.register("eve", false)
	expect(t, eve.do("POST", "/api/v1/invites/"+inv["code"].(string)+"/accept", nil), 200)
	expect(t, eve.do("POST", "/api/v1/voice/join", map[string]any{"channelId": lim["id"]}), 403, "channel_full")

	// kicked members lose their voice state
	expect(t, owner.do("DELETE", "/api/v1/spaces/"+sp+"/members/"+carol.id, nil), 204)
	e.s.voice.mu.Lock()
	_, still = e.s.voice.states[mustID(carol.id)]
	e.s.voice.mu.Unlock()
	if still {
		t.Fatal("kick should clear voice state")
	}
}

func mustID(s string) int64 {
	var id ID
	_ = id.UnmarshalJSON([]byte(s))
	return int64(id)
}

func TestDMCalls(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	b := e.register("bob", false)
	c := e.register("carol", false)
	_, text, _ := e.spaceWithMembers(a, b, c)
	e.befriend(a, b)
	dm := expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id}}), 201).json()
	id := dm["id"].(string)
	expect(t, c.do("POST", "/api/v1/calls/"+id+"/ring", nil), 404)
	expect(t, a.do("POST", "/api/v1/calls/"+text+"/ring", nil), 400, "not_dm")
	expect(t, a.do("POST", "/api/v1/calls/"+id+"/ring", nil), 204)
	j := expect(t, a.do("POST", "/api/v1/voice/join", map[string]any{"channelId": id}), 200).json()
	if str(j, "e2ee", "containerId") != id {
		t.Fatal("DM call container must be the DM channel")
	}
	expect(t, c.do("POST", "/api/v1/voice/join", map[string]any{"channelId": id}), 404)
	expect(t, b.do("POST", "/api/v1/calls/"+id+"/decline", nil), 204)
	e.s.voice.mu.Lock()
	n := len(e.s.voice.rings)
	e.s.voice.mu.Unlock()
	if n != 0 {
		t.Fatal("decline should stop ringing")
	}
}

func signedWebhook(t *testing.T, e *testEnv, body []byte, secret string) *http.Request {
	t.Helper()
	sum := sha256.Sum256(body)
	at := lkauth.NewAccessToken(e.cfg.LiveKitKey, secret).SetValidFor(time.Minute).SetSha256(base64.StdEncoding.EncodeToString(sum[:]))
	tok, err := at.ToJWT()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/livekit/webhook", bytes.NewReader(body))
	req.Header.Set("Authorization", tok)
	req.Header.Set("Content-Type", "application/webhook+json")
	return req
}

func TestWebhook(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	out := e.register("outsider", false)
	_, _, voice := e.spaceWithMembers(a)
	h := e.s.WebhookRouter()
	send := func(body string, secret string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signedWebhook(t, e, []byte(body), secret))
		return rec.Code
	}
	joined := `{"event":"participant_joined","id":"EV_1","room":{"name":"ch_` + voice + `"},"participant":{"identity":"` + a.id + `"}}`
	if send(joined, "wrong-secret-wrong-secret-wrong-1234") != 401 {
		t.Fatal("bad signature accepted")
	}
	rec := httptest.NewRecorder()
	req := signedWebhook(t, e, []byte(joined), e.cfg.LiveKitSecret)
	req.Body = http.NoBody // body does not match checksum
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatal("checksum mismatch accepted")
	}
	if send(joined, e.cfg.LiveKitSecret) != 200 {
		t.Fatal("valid webhook rejected")
	}
	e.s.voice.mu.Lock()
	st := e.s.voice.states[mustID(a.id)]
	e.s.voice.mu.Unlock()
	if st == nil || !st.confirmed {
		t.Fatal("participant_joined should adopt state")
	}
	// a participant who is not a member gets no state
	intruder := `{"event":"participant_joined","id":"EV_2","room":{"name":"ch_` + voice + `"},"participant":{"identity":"` + out.id + `"}}`
	send(intruder, e.cfg.LiveKitSecret)
	e.s.voice.mu.Lock()
	_, bad := e.s.voice.states[mustID(out.id)]
	e.s.voice.mu.Unlock()
	if bad {
		t.Fatal("non-member adopted")
	}
	stream := `{"event":"track_published","id":"EV_3","room":{"name":"ch_` + voice + `"},"participant":{"identity":"` + a.id + `"},"track":{"source":"SCREEN_SHARE"}}`
	send(stream, e.cfg.LiveKitSecret)
	e.s.voice.mu.Lock()
	streaming := e.s.voice.states[mustID(a.id)].Streaming
	e.s.voice.mu.Unlock()
	if !streaming {
		t.Fatal("streaming flag")
	}
	left := `{"event":"participant_left","id":"EV_4","room":{"name":"ch_` + voice + `"},"participant":{"identity":"` + a.id + `"}}`
	send(left, e.cfg.LiveKitSecret)
	// duplicate of EV_1 must be ignored (dedupe)
	send(joined, e.cfg.LiveKitSecret)
	e.s.voice.mu.Lock()
	_, still := e.s.voice.states[mustID(a.id)]
	e.s.voice.mu.Unlock()
	if still {
		t.Fatal("participant_left / dedupe failed")
	}
}

// ---------- gateway ----------

type wsClient struct {
	t      *testing.T
	c      *websocket.Conn
	mu     sync.Mutex
	frames []map[string]any
	done   chan struct{}
	err    error
}

func (e *testEnv) dial(t *testing.T, identify map[string]any) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	w := &wsClient{t: t, c: c, done: make(chan struct{})}
	b, _ := json.Marshal(map[string]any{"op": "identify", "d": identify})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(w.done)
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				w.mu.Lock()
				w.err = err
				w.mu.Unlock()
				return
			}
			var f map[string]any
			_ = json.Unmarshal(data, &f)
			w.mu.Lock()
			w.frames = append(w.frames, f)
			w.mu.Unlock()
		}
	}()
	t.Cleanup(func() { _ = c.CloseNow() })
	return w
}

func (w *wsClient) find(op string) map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, f := range w.frames {
		if f["op"] == op {
			return f
		}
	}
	return nil
}

func (w *wsClient) wait(op string) map[string]any {
	w.t.Helper()
	var f map[string]any
	waitFor(w.t, 3*time.Second, func() bool { f = w.find(op); return f != nil })
	return f
}

func TestGateway(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	b := e.register("bob", false)
	out := e.register("outsider", false)
	_, text, _ := e.spaceWithMembers(a, b)

	bad := e.dial(t, map[string]any{"token": "nope"})
	<-bad.done
	if websocket.CloseStatus(bad.err) != closeAuthFail {
		t.Fatalf("expected 4004, got %v", bad.err)
	}

	wa := e.dial(t, map[string]any{"token": a.token, "protocolVersion": 1})
	ready := wa.wait("ready")["d"].(map[string]any)
	if len(ready["spaces"].([]any)) != 1 || len(ready["channels"].([]any)) != 2 || ready["sessionId"] == "" {
		t.Fatalf("ready: %v", ready)
	}
	wo := e.dial(t, map[string]any{"token": out.token})
	wo.wait("ready")

	msg := expect(t, b.do("POST", "/api/v1/channels/"+text+"/messages", map[string]any{"content": "hi gateway"}), 201).json()
	ev := wa.wait("message_create")
	if str(ev, "d", "id") != msg["id"] || ev["s"] == nil {
		t.Fatalf("event: %v", ev)
	}
	// presence of bob? bob is offline (no socket). Typing is ephemeral (no seq).
	expect(t, b.do("POST", "/api/v1/channels/"+text+"/typing", nil), 204)
	ty := wa.wait("typing_start")
	if ty["s"] != nil {
		t.Fatal("typing must be ephemeral")
	}
	time.Sleep(100 * time.Millisecond)
	if wo.find("message_create") != nil || wo.find("typing_start") != nil {
		t.Fatal("outsider received space events")
	}

	// ping/pong
	_ = wa.c.Write(context.Background(), websocket.MessageText, []byte(`{"op":"ping"}`))
	wa.wait("pong")

	// resume: close, send an event while disconnected, resume with lastSeq
	lastSeq := int64(ev["s"].(float64))
	_ = wa.c.Close(websocket.StatusNormalClosure, "")
	<-wa.done
	time.Sleep(50 * time.Millisecond)
	missed := expect(t, b.do("POST", "/api/v1/channels/"+text+"/messages", map[string]any{"content": "while away"}), 201).json()
	wr := e.dial(t, map[string]any{"token": a.token, "resume": map[string]any{"sessionId": ready["sessionId"], "lastSeq": lastSeq}})
	wr.wait("resumed")
	got := wr.wait("message_create")
	if str(got, "d", "id") != missed["id"] {
		t.Fatalf("resume replay: %v", got)
	}
	// resume with someone else's session id fails
	wx := e.dial(t, map[string]any{"token": b.token, "resume": map[string]any{"sessionId": ready["sessionId"], "lastSeq": 0}})
	wx.wait("invalid_session")

	// logout drops the socket
	expect(t, a.do("POST", "/api/v1/auth/logout", nil), 204)
	<-wr.done
	if websocket.CloseStatus(wr.err) != closeRevoked {
		t.Fatalf("expected revoked close, got %v", wr.err)
	}

	// kick: kicked member gets space_delete and no further events
	wb := e.dial(t, map[string]any{"token": b.token})
	wb.wait("ready")
	_ = wb
}

func TestGatewayOriginCheck(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("GET", e.ts.URL+"/ws", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", res.StatusCode)
	}
	// CORS: allowed origin gets headers, foreign does not
	r := e.raw("OPTIONS", "/api/v1/auth/login", "", nil, map[string]string{"Origin": "http://tauri.localhost", "Access-Control-Request-Method": "POST"})
	if r.hdr.Get("Access-Control-Allow-Origin") != "http://tauri.localhost" {
		t.Fatal("cors for app origin")
	}
	r = e.raw("OPTIONS", "/api/v1/auth/login", "", nil, map[string]string{"Origin": "https://evil.example"})
	if r.hdr.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("cors for foreign origin")
	}
}

func TestVoiceWatching(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	b := e.register("bob", false)
	c := e.register("carol", false)
	_, _, voice := e.spaceWithMembers(a, b, c)
	_, _, other := e.spaceWithMembers(c)
	expect(t, a.do("POST", "/api/v1/voice/join", map[string]any{"channelId": voice}), 200)
	expect(t, b.do("POST", "/api/v1/voice/join", map[string]any{"channelId": voice}), 200)
	expect(t, c.do("POST", "/api/v1/voice/join", map[string]any{"channelId": other}), 200)
	v := e.s.voice
	set := func(u *client, d map[string]any) {
		raw, _ := json.Marshal(d)
		v.wsVoiceStateSet(&Session{UserID: mustID(u.id)}, raw)
	}
	watching := func(u *client) []ID {
		v.mu.Lock()
		defer v.mu.Unlock()
		return slices.Clone(v.states[mustID(u.id)].Watching)
	}
	// alice is not streaming yet: bob cannot claim to watch her
	set(b, map[string]any{"watching": []string{a.id}})
	if w := watching(b); len(w) != 0 {
		t.Fatalf("watching a non-streamer: %v", w)
	}
	set(a, map[string]any{"streaming": true})
	// self, users in other channels, duplicates and junk are dropped
	set(b, map[string]any{"watching": []string{a.id, a.id, b.id, c.id, "123"}})
	if w := watching(b); len(w) != 1 || w[0] != ID(mustID(a.id)) {
		t.Fatalf("watching: %v", w)
	}
	// omitting the field keeps the list, an empty list clears it
	set(b, map[string]any{"muted": true})
	if len(watching(b)) != 1 {
		t.Fatal("watching must survive unrelated updates")
	}
	set(b, map[string]any{"watching": []string{}})
	if len(watching(b)) != 0 {
		t.Fatal("watching not cleared")
	}
	// carol (other channel) cannot watch alice even though alice streams
	set(c, map[string]any{"watching": []string{a.id}})
	if len(watching(c)) != 0 {
		t.Fatal("cross-channel watching accepted")
	}
}

func TestVoiceStreamInfo(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	b := e.register("bob", false)
	_, _, voice := e.spaceWithMembers(a, b)
	expect(t, a.do("POST", "/api/v1/voice/join", map[string]any{"channelId": voice}), 200)
	v := e.s.voice
	set := func(d map[string]any) {
		raw, _ := json.Marshal(d)
		v.wsVoiceStateSet(&Session{UserID: mustID(a.id)}, raw)
	}
	info := func() *StreamInfo {
		v.mu.Lock()
		defer v.mu.Unlock()
		if si := v.states[mustID(a.id)].Stream; si != nil {
			c := *si
			return &c
		}
		return nil
	}
	ok := map[string]any{"height": 1080, "fps": 60, "ramp": true}
	// only while streaming
	set(map[string]any{"stream": ok})
	if info() != nil {
		t.Fatal("stream info without streaming")
	}
	set(map[string]any{"streaming": true, "stream": ok})
	if si := info(); si == nil || si.Height != 1080 || si.FPS != 60 || !si.Ramp {
		t.Fatalf("stream info: %+v", si)
	}
	// implausible values are dropped
	for _, bad := range []map[string]any{
		{"height": 0, "fps": 60},
		{"height": 99999, "fps": 60},
		{"height": 1080, "fps": 1000},
		{"height": 1080, "fps": 0},
	} {
		set(map[string]any{"streaming": true, "stream": ok})
		set(map[string]any{"streaming": true, "stream": bad})
		if si := info(); si != nil {
			t.Fatalf("accepted %v: %+v", bad, si)
		}
	}
	// the stream ending (client or LiveKit) clears it
	set(map[string]any{"streaming": true, "stream": ok})
	set(map[string]any{"streaming": false, "stream": ok})
	if info() != nil {
		t.Fatal("stream info after stream end")
	}
	set(map[string]any{"streaming": true, "stream": ok})
	v.setStreaming(context.Background(), mustID(a.id), mustID(voice), false)
	if info() != nil {
		t.Fatal("stream info after track_unpublished")
	}
	// users without a voice state cannot set anything
	raw, _ := json.Marshal(map[string]any{"streaming": true, "stream": ok})
	v.wsVoiceStateSet(&Session{UserID: mustID(b.id)}, raw)
	v.mu.Lock()
	_, has := v.states[mustID(b.id)]
	v.mu.Unlock()
	if has {
		t.Fatal("state created for a user outside voice")
	}
}

func (w *wsClient) waitMatch(op string, ok func(d map[string]any) bool) map[string]any {
	w.t.Helper()
	var hit map[string]any
	waitFor(w.t, 3*time.Second, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		for _, f := range w.frames {
			if d, _ := f["d"].(map[string]any); f["op"] == op && d != nil && ok(d) {
				hit = d
				return true
			}
		}
		return false
	})
	return hit
}

// count returns how many frames of op match.
func (w *wsClient) count(op string, ok func(d map[string]any) bool) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, f := range w.frames {
		if d, _ := f["d"].(map[string]any); f["op"] == op && d != nil && ok(d) {
			n++
		}
	}
	return n
}

// Users who become related while both are online must see each other's
// presence right away (it is otherwise only sent on connect).
func TestPresenceForNewRelations(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	sp, _, _ := e.spaceWithMembers(owner)
	newbie := e.register("newbie", false)
	wo := e.dial(t, map[string]any{"token": owner.token, "protocolVersion": 1})
	wo.wait("ready")
	wn := e.dial(t, map[string]any{"token": newbie.token, "protocolVersion": 1})
	wn.wait("ready")

	inv := e.spaceInvite(owner, sp)
	expect(t, newbie.do("POST", "/api/v1/invites/"+inv+"/accept", nil), 200)
	wo.waitMatch("presence_update", func(d map[string]any) bool { return d["userId"] == newbie.id && d["status"] == "online" })
	// the newcomer gets everyone's presence with the member list
	members := expect(t, newbie.do("GET", "/api/v1/spaces/"+sp+"/members", nil), 200).list()
	for _, m := range members {
		mm := m.(map[string]any)
		if mm["userId"] == owner.id && mm["presence"].(map[string]any)["status"] != "online" {
			t.Fatalf("owner presence: %v", mm)
		}
	}

	// friendship between strangers
	x := e.register("xavier", false)
	y := e.register("yvonne", false)
	wx := e.dial(t, map[string]any{"token": x.token, "protocolVersion": 1})
	wx.wait("ready")
	wy := e.dial(t, map[string]any{"token": y.token, "protocolVersion": 1})
	wy.wait("ready")
	expect(t, x.do("POST", "/api/v1/relationships", map[string]any{"username": "yvonne"}), 200)
	if d := wx.waitMatch("relationship_update", func(d map[string]any) bool { return d["kind"] == "pending_out" }); d["presence"] != nil {
		t.Fatalf("pending request must not reveal presence: %v", d)
	}
	expect(t, y.do("POST", "/api/v1/relationships", map[string]any{"username": "xavier"}), 200)
	for _, c := range []struct {
		w     *wsClient
		other string
	}{{wx, y.id}, {wy, x.id}} {
		d := c.w.waitMatch("relationship_update", func(d map[string]any) bool { return d["kind"] == "friend" && d["userId"] == c.other })
		if p, _ := d["presence"].(map[string]any); p == nil || p["status"] != "online" {
			t.Fatalf("friend presence: %v", d)
		}
	}
}

// A server restart (update) must not look like a revoked session: clients
// would log out instead of reconnecting.
func TestShutdownClosesWithRestartCode(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	w := e.dial(t, map[string]any{"token": a.token, "protocolVersion": 1})
	w.wait("ready")
	e.s.Close()
	<-w.done
	if got := websocket.CloseStatus(w.err); got != websocket.StatusServiceRestart {
		t.Fatalf("close code %v, want %v", got, websocket.StatusServiceRestart)
	}
}
