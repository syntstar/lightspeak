package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/syntstar/lightspeak/server/internal/config"
	"github.com/syntstar/lightspeak/server/internal/ratelimit"
	"github.com/syntstar/lightspeak/server/internal/store"
)

type testEnv struct {
	t   *testing.T
	s   *Server
	ts  *httptest.Server
	db  *store.DB
	cfg *config.Config
}

func generous() *ratelimit.Limiter { return ratelimit.New(10000, 10000) }

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Dev: true, DataDir: dir, PublicURL: "https://lv.test", LiveKitURL: "wss://rtc.test",
		LiveKitAPIURL: "http://127.0.0.1:1", LiveKitKey: "testkey", LiveKitSecret: "testsecret-testsecret-testsecret-1234",
		FileURLSecret: []byte("0123456789abcdef0123456789abcdef"), MaxUploadBytes: 1 << 20, QuotaBytes: 3 << 20,
		ArgonMemoryKiB: 64, ArgonTime: 1, AllowedOrigins: []string{"http://tauri.localhost"}, MinClientVersion: "0.1.0",
		MaxSpaces: 3, MaxMessageChars: 4000, MaxSpaceMembers: 500, MaxSpaceChannels: 200, MaxSpaceRoles: 25, MaxVoiceParticipants: 25,
		Invites: config.InvitesMembers,
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.lim = limits{login: generous(), register: generous(), upload: generous(), messages: generous(), ws: generous(), invitePreview: generous(), general: generous()}
	ts := httptest.NewServer(s.Router())
	t.Cleanup(func() {
		ts.Close()
		s.Close()
		_ = db.Close()
	})
	return &testEnv{t: t, s: s, ts: ts, db: db, cfg: cfg}
}

type client struct {
	env      *testEnv
	token    string
	id       string
	deviceID string
	name     string
}

type resp struct {
	status int
	body   []byte
	hdr    http.Header
}

func (r resp) json() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	return m
}

func (r resp) list() []any {
	var l []any
	_ = json.Unmarshal(r.body, &l)
	return l
}

func (r resp) code() string {
	m := r.json()
	if e, ok := m["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func (e *testEnv) raw(method, path, token string, body io.Reader, hdr map[string]string) resp {
	e.t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, b, res.Header}
}

func (e *testEnv) call(method, path, token string, body any) resp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	return e.raw(method, path, token, rd, map[string]string{"Content-Type": "application/json"})
}

func (c *client) do(method, path string, body any) resp {
	c.env.t.Helper()
	return c.env.call(method, path, c.token, body)
}

// befriend makes a and b friends (request + accept).
func (e *testEnv) befriend(a, b *client) {
	e.t.Helper()
	expect(e.t, a.do("POST", "/api/v1/relationships", map[string]any{"username": b.name}), 200)
	expect(e.t, b.do("PUT", "/api/v1/relationships/"+a.id, map[string]any{"kind": "friend"}), 200)
}

// expect asserts the status (and optionally the error code).
func expect(t *testing.T, r resp, status int, code ...string) resp {
	t.Helper()
	if r.status != status {
		t.Fatalf("status %d, want %d: %s", r.status, status, r.body)
	}
	if len(code) > 0 && r.code() != code[0] {
		t.Fatalf("code %q, want %q: %s", r.code(), code[0], r.body)
	}
	return r
}

func (e *testEnv) invite(spaceID int64, admin bool) string {
	e.t.Helper()
	code, _, err := e.s.CreateInvite(context.Background(), spaceID, 3600, 0, admin)
	if err != nil {
		e.t.Fatal(err)
	}
	return code
}

const testPassword = "correct-horse-battery"

func (e *testEnv) register(name string, admin bool) *client {
	e.t.Helper()
	r := e.call("POST", "/api/v1/auth/register", "", map[string]any{
		"inviteCode": e.invite(0, admin), "username": name, "displayName": strings.ToUpper(name[:1]) + name[1:],
		"password": testPassword, "deviceName": name + "-pc", "devicePublicKey": fakeKey(name),
	})
	expect(e.t, r, 201)
	m := r.json()
	u := m["user"].(map[string]any)
	d := m["device"].(map[string]any)
	return &client{env: e, token: m["token"].(string), id: u["id"].(string), deviceID: d["id"].(string), name: name}
}

// fakeKey returns a deterministic 32-byte base64 "public key".
func fakeKey(seed string) string {
	b := make([]byte, 32)
	copy(b, seed)
	return b64(b)
}

func str(m map[string]any, path ...string) string {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[p]
	}
	switch v := cur.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprint(int64(v))
	case bool:
		return fmt.Sprint(v)
	}
	return ""
}

// spaceWithMember creates a space owned by owner with a text and a voice
// channel and lets the members join via invite.
func (e *testEnv) spaceWithMembers(owner *client, members ...*client) (spaceID, textID, voiceID string) {
	e.t.Helper()
	r := expect(e.t, owner.do("POST", "/api/v1/spaces", map[string]any{"name": "Friends",
		"channels": []map[string]string{{"kind": "text", "name": "general"}, {"kind": "voice", "name": "Lounge"}}}), 201)
	m := r.json()
	spaceID = str(m, "space", "id")
	for _, c := range m["channels"].([]any) {
		cm := c.(map[string]any)
		if cm["kind"] == "text" {
			textID = cm["id"].(string)
		} else {
			voiceID = cm["id"].(string)
		}
	}
	if len(members) > 0 {
		inv := expect(e.t, owner.do("POST", "/api/v1/spaces/"+spaceID+"/invites", map[string]any{"maxAgeSeconds": 3600}), 201).json()
		for _, mem := range members {
			expect(e.t, mem.do("POST", "/api/v1/invites/"+inv["code"].(string)+"/accept", nil), 200)
		}
	}
	return
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// adminRole returns the ID of the space's default "Admin" role.
func (e *testEnv) adminRole(c *client, sp string) string {
	e.t.Helper()
	m := expect(e.t, c.do("GET", "/api/v1/spaces/"+sp, nil), 200).json()
	for _, r := range m["space"].(map[string]any)["roles"].([]any) {
		rm := r.(map[string]any)
		if rm["name"] == "Admin" {
			return rm["id"].(string)
		}
	}
	e.t.Fatal("no Admin role")
	return ""
}

// role gives (add) or takes a role from uid, acting as c.
func (c *client) role(sp, uid, rid string, add bool) resp {
	c.env.t.Helper()
	method := "DELETE"
	if add {
		method = "PUT"
	}
	return c.do(method, "/api/v1/spaces/"+sp+"/members/"+uid+"/roles/"+rid, nil)
}
