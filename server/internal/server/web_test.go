package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testOrigin = "https://lv.test" // PublicURL of the test env

func (e *testEnv) webRaw(method, path string, body any, hdr map[string]string) resp {
	e.t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	h := map[string]string{"Content-Type": "application/json", webClientHeader: webClientValue}
	for k, v := range hdr {
		h[k] = v
	}
	for k, v := range h {
		if v == "" {
			delete(h, k)
		}
	}
	return e.raw(method, path, "", bytes.NewReader(b), h)
}

func sessionCookie(t *testing.T, r resp) *http.Cookie {
	t.Helper()
	for _, c := range (&http.Response{Header: r.hdr}).Cookies() {
		if c.Name == "__Host-ls_session" {
			return c
		}
	}
	t.Fatalf("no session cookie in %v", r.hdr)
	return nil
}

func TestWebLoginCookie(t *testing.T) {
	e := newEnv(t)
	inv := e.invite(0, false)
	reg := map[string]any{
		"inviteCode": inv, "username": "webby", "displayName": "Webby",
		"password": testPassword, "deviceName": "Browser", "devicePublicKey": fakeKey("webby"),
	}

	// Browser registration from a foreign page or without Origin: refused.
	expect(t, e.webRaw("POST", "/api/v1/auth/register", reg, map[string]string{"Origin": "https://evil.test"}), 403, "origin_not_allowed")
	expect(t, e.webRaw("POST", "/api/v1/auth/register", reg, nil), 403, "origin_not_allowed")

	r := expect(t, e.webRaw("POST", "/api/v1/auth/register", reg, map[string]string{"Origin": testOrigin}), 201)
	if tok := str(r.json(), "token"); tok != "" {
		t.Fatalf("browser client got a token in the body: %q", tok)
	}
	c := sessionCookie(t, r)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Value == "" {
		t.Fatalf("weak cookie: %+v", c)
	}
	ck := c.Name + "=" + c.Value

	// Desktop clients still get the bearer token in the body.
	desk := e.call("POST", "/api/v1/auth/login", "", map[string]any{"username": "webby", "password": testPassword, "deviceName": "PC"})
	expect(t, desk, 200)
	if str(desk.json(), "token") == "" || len(desk.hdr.Values("Set-Cookie")) != 0 {
		t.Fatalf("desktop login changed: %s %v", desk.body, desk.hdr)
	}

	// Cookie works only together with the marker header and our origin.
	expect(t, e.webRaw("GET", "/api/v1/users/@me", nil, map[string]string{"Cookie": ck}), 200)
	expect(t, e.webRaw("GET", "/api/v1/users/@me", nil, map[string]string{"Cookie": ck, "Sec-Fetch-Site": "same-origin"}), 200)
	expect(t, e.raw("GET", "/api/v1/users/@me", "", nil, map[string]string{"Cookie": ck}), 401)
	expect(t, e.webRaw("GET", "/api/v1/users/@me", nil, map[string]string{"Cookie": ck, "Origin": "https://evil.test"}), 401)
	expect(t, e.webRaw("GET", "/api/v1/users/@me", nil, map[string]string{"Cookie": ck, "Sec-Fetch-Site": "cross-site"}), 401)
	expect(t, e.webRaw("GET", "/api/v1/users/@me", nil, map[string]string{"Cookie": ck, "Sec-Fetch-Site": "same-site"}), 401)
	// State-changing requests need our Origin explicitly.
	patch := map[string]any{"displayName": "Web"}
	expect(t, e.webRaw("PATCH", "/api/v1/users/@me", patch, map[string]string{"Cookie": ck}), 401)
	expect(t, e.webRaw("PATCH", "/api/v1/users/@me", patch, map[string]string{"Cookie": ck, "Origin": "https://evil.test"}), 401)
	expect(t, e.webRaw("PATCH", "/api/v1/users/@me", patch, map[string]string{"Cookie": ck, "Origin": testOrigin}), 200)

	// A bearer token wins over the cookie (desktop behaviour unchanged).
	expect(t, e.raw("GET", "/api/v1/users/@me", "bogus", nil, map[string]string{"Cookie": ck, webClientHeader: webClientValue}), 401)

	// Browser login from our page: new cookie, no token.
	lr := expect(t, e.webRaw("POST", "/api/v1/auth/login", map[string]any{"username": "webby", "password": testPassword, "deviceName": "Browser"},
		map[string]string{"Origin": testOrigin}), 200)
	if str(lr.json(), "token") != "" {
		t.Fatal("token in browser login body")
	}
	ck2 := "__Host-ls_session=" + sessionCookie(t, lr).Value
	expect(t, e.webRaw("POST", "/api/v1/auth/login", map[string]any{"username": "webby", "password": testPassword, "deviceName": "Browser"},
		map[string]string{"Origin": "https://evil.test"}), 403, "origin_not_allowed")

	// Logout revokes the session and clears the cookie.
	out := expect(t, e.webRaw("POST", "/api/v1/auth/logout", nil, map[string]string{"Cookie": ck2, "Origin": testOrigin}), 204)
	cleared := sessionCookie(t, out)
	if cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatalf("cookie not cleared: %+v", cleared)
	}
	expect(t, e.webRaw("GET", "/api/v1/users/@me", nil, map[string]string{"Cookie": ck2}), 401)
	// The first browser session is unaffected.
	expect(t, e.webRaw("GET", "/api/v1/users/@me", nil, map[string]string{"Cookie": ck}), 200)
}

func TestWebGatewayCookie(t *testing.T) {
	e := newEnv(t)
	reg := map[string]any{
		"inviteCode": e.invite(0, false), "username": "webgw", "displayName": "W",
		"password": testPassword, "deviceName": "Browser", "devicePublicKey": fakeKey("webgw"),
	}
	r := expect(t, e.webRaw("POST", "/api/v1/auth/register", reg, map[string]string{"Origin": testOrigin}), 201)
	ck := "__Host-ls_session=" + sessionCookie(t, r).Value
	url := "ws" + strings.TrimPrefix(e.ts.URL, "http") + "/ws"

	dial := func(origin string) (*websocket.Conn, int) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h := http.Header{"Cookie": {ck}}
		if origin != "" {
			h.Set("Origin", origin)
		}
		c, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			if res != nil {
				return nil, res.StatusCode
			}
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.CloseNow() })
		return c, 101
	}
	identify := func(c *websocket.Conn) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		b, _ := json.Marshal(map[string]any{"op": "identify", "d": map[string]any{"token": "", "protocolVersion": 1}})
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			return "", err
		}
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return "", err
			}
			var f map[string]any
			_ = json.Unmarshal(data, &f)
			if op, _ := f["op"].(string); op == "ready" {
				return op, nil
			}
		}
	}

	// Our page: cookie accepted.
	c, st := dial(testOrigin)
	if st != 101 {
		t.Fatalf("upgrade %d", st)
	}
	if op, err := identify(c); err != nil || op != "ready" {
		t.Fatalf("cookie identify: %v %v", op, err)
	}
	// Foreign page: refused before the upgrade.
	if _, st := dial("https://evil.test"); st != http.StatusForbidden {
		t.Fatalf("foreign origin upgrade: %d", st)
	}
	// The desktop app's origin does not get to use a browser cookie.
	c, st = dial("http://tauri.localhost")
	if st != 101 {
		t.Fatalf("upgrade %d", st)
	}
	if _, err := identify(c); websocket.CloseStatus(err) != closeAuthFail {
		t.Fatalf("tauri origin with cookie: %v", err)
	}
	// No Origin at all (non-browser): cookie ignored.
	c, _ = dial("")
	if _, err := identify(c); websocket.CloseStatus(err) != closeAuthFail {
		t.Fatalf("no origin with cookie: %v", err)
	}
}

func TestWebAppServing(t *testing.T) {
	e := newEnv(t)
	// No build installed: nothing under /app, no browser button.
	expect(t, e.raw("GET", "/app/", "", nil, nil), 404)
	code := e.invite(0, false)
	if l := e.raw("GET", "/invite/"+code, "", nil, nil); bytes.Contains(l.body, []byte("/app/")) {
		t.Fatal("browser button without web build")
	}

	root := filepath.Join(e.cfg.PublicDir(), webDir)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "assets"), 0o755))
	must(os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html><title>app</title>"), 0o644))
	must(os.WriteFile(filepath.Join(root, "assets", "main-abc123.js"), []byte("console.log(1)"), 0o644))
	must(os.WriteFile(filepath.Join(root, "favicon.svg"), []byte("<svg/>"), 0o644))
	must(os.WriteFile(filepath.Join(e.cfg.DataDir, "secret.env"), []byte("SECRET=1"), 0o600))

	idx := expect(t, e.raw("GET", "/app/", "", nil, nil), 200)
	if !bytes.Contains(idx.body, []byte("<title>app</title>")) || idx.hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("index: %s %v", idx.body, idx.hdr)
	}
	csp := idx.hdr.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "connect-src 'self' wss://lv.test wss://rtc.test https://rtc.test", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval'") && !strings.Contains(csp, "wasm-unsafe-eval") {
		t.Fatal("unsafe-eval in CSP")
	}
	if !strings.Contains(idx.hdr.Get("Permissions-Policy"), "camera=()") {
		t.Fatal("permissions policy missing")
	}
	// Client-side routes fall back to index.html.
	expect(t, e.raw("GET", "/app/channels/123", "", nil, nil), 200)
	if r := e.raw("GET", "/app", "", nil, nil); r.status != 200 { // redirect followed
		t.Fatalf("/app: %d", r.status)
	}
	a := expect(t, e.raw("GET", "/app/assets/main-abc123.js", "", nil, nil), 200)
	if !strings.Contains(a.hdr.Get("Cache-Control"), "immutable") || string(a.body) != "console.log(1)" {
		t.Fatalf("asset: %v %s", a.hdr, a.body)
	}
	if f := expect(t, e.raw("GET", "/app/favicon.svg", "", nil, nil), 200); f.hdr.Get("Cache-Control") != "no-cache" {
		t.Fatalf("favicon cache: %v", f.hdr)
	}
	expect(t, e.raw("GET", "/app/assets/missing.js", "", nil, nil), 404)
	expect(t, e.raw("GET", "/app/missing.css", "", nil, nil), 404)
	// No escape from the web directory.
	for _, p := range []string{"/app/../secret.env", "/app/..%2f..%2fsecret.env", "/app/%2e%2e/%2e%2e/secret.env"} {
		if r := e.raw("GET", p, "", nil, nil); bytes.Contains(r.body, []byte("SECRET")) {
			t.Fatalf("%s leaked a file", p)
		}
	}

	// Invite page now offers the browser with the code in the fragment.
	l := expect(t, e.raw("GET", "/invite/"+code, "", nil, nil), 200)
	if !bytes.Contains(l.body, []byte(`href="/app/#invite=`+code+`"`)) {
		t.Fatalf("no browser button: %s", l.body)
	}
	if d := expect(t, e.raw("GET", "/download", "", nil, nil), 200); !bytes.Contains(d.body, []byte(`href="/app/"`)) {
		t.Fatal("no browser button on the download page")
	}
}

func TestFindWebRoot(t *testing.T) {
	dir := t.TempDir()
	pub := filepath.Join(dir, "public")
	rel := filepath.Join(dir, "releases", "1.2.3")
	if err := os.MkdirAll(filepath.Join(rel, webDir), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(rel, "lightspeak-server")
	// Release without a web build (or none at all): PublicDir/web.
	if got := findWebRoot(exe, pub); got != filepath.Join(pub, webDir) {
		t.Fatalf("no build: %s", got)
	}
	if got := findWebRoot("", pub); got != filepath.Join(pub, webDir) {
		t.Fatalf("no exe: %s", got)
	}
	// A release that ships web/index.html serves it.
	if err := os.WriteFile(filepath.Join(rel, webDir, "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findWebRoot(exe, pub); got != filepath.Join(rel, webDir) {
		t.Fatalf("release build: %s", got)
	}
}
