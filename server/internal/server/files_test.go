package server

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"
	"testing"
	"time"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, x%h, color.NRGBA{255, 0, 0, 128})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (c *client) upload(channel, name string, data []byte, hdr map[string]string) resp {
	h := map[string]string{"X-Filename": name, "Content-Type": "application/octet-stream"}
	for k, v := range hdr {
		h[k] = v
	}
	return c.env.raw("POST", "/api/v1/channels/"+channel+"/attachments", c.token, bytes.NewReader(data), h)
}

func TestUploadsAndSignedURLs(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	b := e.register("bob", false)
	out := e.register("outsider", false)
	_, text, _ := e.spaceWithMembers(a, b)

	img := expect(t, a.upload(text, "../../evil\u202egnp.exe", pngBytes(t, 1200, 600), nil), 201).json()
	if img["mime"] != "image/png" || img["width"].(float64) != 1200 || img["thumbUrl"] == nil {
		t.Fatalf("image upload: %v", img)
	}
	if name := img["filename"].(string); strings.ContainsAny(name, "/\\\u202e") {
		t.Fatalf("filename not sanitised: %q", name)
	}
	html := expect(t, a.upload(text, "page.html", []byte("<html><script>alert(1)</script></html>"), nil), 201).json()
	// The sniffed mime may be text/html, but it must never be served inline (checked below).
	expect(t, out.upload(text, "x.txt", []byte("hi"), nil), 404)
	expect(t, a.upload(text, "big.bin", make([]byte, 2<<20), nil), 413, "file_too_large")

	// attach: only own, unattached, same channel
	expect(t, b.do("POST", "/api/v1/channels/"+text+"/messages", map[string]any{"content": "steal", "attachmentIds": []any{img["id"]}}), 400, "invalid_attachments")
	msg := expect(t, a.do("POST", "/api/v1/channels/"+text+"/messages", map[string]any{"attachmentIds": []any{img["id"], html["id"]}}), 201).json()
	atts := msg["attachments"].([]any)
	if len(atts) != 2 {
		t.Fatal("attachments")
	}
	expect(t, a.do("POST", "/api/v1/channels/"+text+"/messages", map[string]any{"content": "again", "attachmentIds": []any{img["id"]}}), 400, "invalid_attachments")

	// signed URLs
	thumb := e.raw("GET", img["thumbUrl"].(string), "", nil, nil)
	if thumb.status != 200 || thumb.hdr.Get("Content-Type") != "image/png" {
		t.Fatalf("thumb: %d %s", thumb.status, thumb.hdr.Get("Content-Type"))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb.body))
	if err != nil || cfg.Width != 800 || cfg.Height != 400 {
		t.Fatalf("thumb size: %v %v", cfg, err)
	}
	orig := e.raw("GET", img["url"].(string), "", nil, nil)
	if orig.status != 200 || !strings.HasPrefix(orig.hdr.Get("Content-Disposition"), "attachment") || orig.hdr.Get("Content-Security-Policy") == "" {
		t.Fatalf("orig headers: %v", orig.hdr)
	}
	h := e.raw("GET", html["url"].(string), "", nil, nil)
	if h.hdr.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("html served as %q", h.hdr.Get("Content-Type"))
	}
	tampered := strings.Replace(img["url"].(string), "/files/", "/files/1", 1)
	expect(t, e.raw("GET", tampered, "", nil, nil), 403)
	u := img["url"].(string)
	badSig := u[:len(u)-3] + "AAA"
	expect(t, e.raw("GET", badSig, "", nil, nil), 403)
	// thumb signature cannot be used for the original
	sig := img["thumbUrl"].(string)[strings.Index(img["thumbUrl"].(string), "?"):]
	expect(t, e.raw("GET", "/files/"+img["id"].(string)+sig, "", nil, nil), 403)
	// expired
	past := time.Now().Add(-time.Hour).Unix()
	id, _ := strconv.ParseInt(img["id"].(string), 10, 64)
	expired := "/files/" + img["id"].(string) + "?exp=" + strconv.FormatInt(past, 10) + "&sig=" + e.s.files.sign(id, "orig", past)
	expect(t, e.raw("GET", expired, "", nil, nil), 403, "url_expired")

	// deleting the message removes the file
	expect(t, a.do("DELETE", "/api/v1/channels/"+text+"/messages/"+msg["id"].(string), nil), 204)
	expect(t, e.raw("GET", img["url"].(string), "", nil, nil), 404)
}

func TestQuotaAndOrphans(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", true)
	b := e.register("bob", false)
	_, text, _ := e.spaceWithMembers(a, b)
	for i := 0; i < 3; i++ {
		expect(t, a.upload(text, "f.bin", make([]byte, 1<<20-10), nil), 201)
	}
	expect(t, a.upload(text, "f.bin", make([]byte, 100), nil), 507, "quota_exceeded")
	// orphan purge (age 0) frees the quota
	n, err := e.s.files.purgeOrphans(t.Context(), -time.Second)
	if err != nil || n != 3 {
		t.Fatalf("purge: %d %v", n, err)
	}
	expect(t, a.upload(text, "f.bin", make([]byte, 100), nil), 201)
	// admin storage endpoints
	expect(t, b.do("GET", "/api/v1/admin/storage", nil), 403)
	rep := expect(t, a.do("GET", "/api/v1/admin/storage", nil), 200).json()
	if rep["fileCount"].(float64) != 1 {
		t.Fatalf("storage: %v", rep)
	}
	id := rep["largest"].([]any)[0].(map[string]any)["id"].(string)
	expect(t, b.do("DELETE", "/api/v1/admin/files/"+id, nil), 403)
	expect(t, a.do("DELETE", "/api/v1/admin/files/"+id, nil), 204)
}

func TestAvatar(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	r := expect(t, e.raw("POST", "/api/v1/users/@me/avatar", a.token, bytes.NewReader(pngBytes(t, 300, 200)), map[string]string{"Content-Type": "image/png"}), 200).json()
	av := e.raw("GET", r["avatarUrl"].(string), "", nil, nil)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(av.body))
	if av.status != 200 || err != nil || cfg.Width != 256 || cfg.Height != 256 {
		t.Fatalf("avatar: %d %v %v", av.status, cfg, err)
	}
	expect(t, e.raw("POST", "/api/v1/users/@me/avatar", a.token, strings.NewReader("not an image"), nil), 400, "invalid_image")
	expect(t, e.raw("GET", "/avatars/../../etc/passwd", "", nil, nil), 404)
	expect(t, a.do("DELETE", "/api/v1/users/@me/avatar", nil), 204)
	expect(t, e.raw("GET", r["avatarUrl"].(string), "", nil, nil), 404)
}

func TestDecodableMemoryBudget(t *testing.T) {
	cases := []struct {
		name string
		cfg  image.Config
		want bool
	}{
		{"jpeg 24 MP", image.Config{ColorModel: color.YCbCrModel, Width: 6000, Height: 4000}, true},
		{"png rgba 24 MP", image.Config{ColorModel: color.NRGBAModel, Width: 6000, Height: 4000}, true},
		{"png 16-bit 24 MP", image.Config{ColorModel: color.NRGBA64Model, Width: 6000, Height: 4000}, false},
		{"png rgba 50 MP", image.Config{ColorModel: color.RGBAModel, Width: 10000, Height: 5000}, false},
		{"gray 50 MP", image.Config{ColorModel: color.GrayModel, Width: 10000, Height: 5000}, true},
		{"too many pixels", image.Config{ColorModel: color.GrayModel, Width: 10000, Height: 6000}, false},
		{"empty", image.Config{ColorModel: color.RGBAModel}, false},
	}
	for _, c := range cases {
		if got := decodable(c.cfg); got != c.want {
			t.Errorf("%s: decodable=%v, want %v", c.name, got, c.want)
		}
	}
}
