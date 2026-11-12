package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // register decoder
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register decoder

	"github.com/syntstar/lightspeak/server/internal/store"
	"github.com/syntstar/lightspeak/server/internal/worklimit"
)

const (
	maxImagePixels = 50_000_000
	// maxDecodeBytes caps the memory of one decoded image (a 16-bit PNG
	// needs 8 bytes per pixel); the service runs with MemoryMax=384M.
	maxDecodeBytes = 160 << 20
	thumbMax       = 800
	avatarSize     = 256
	maxAvatarBytes = 5 << 20
)

// decodeLimit lets only one image decode run at a time, so parallel uploads
// cannot add up to more than maxDecodeBytes.
var decodeLimit = worklimit.New(1, 8)

// decodable rejects images whose decoded form would be too large.
func decodable(cfg image.Config) bool {
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return false
	}
	px := int64(cfg.Width) * int64(cfg.Height)
	if px > maxImagePixels {
		return false
	}
	bpp := int64(4)
	switch cfg.ColorModel {
	case color.GrayModel, color.AlphaModel:
		bpp = 1
	case color.Gray16Model, color.Alpha16Model:
		bpp = 2
	case color.YCbCrModel:
		bpp = 3 // 4:4:4 worst case
	case color.RGBA64Model, color.NRGBA64Model:
		bpp = 8
	default:
		if _, ok := cfg.ColorModel.(color.Palette); ok {
			bpp = 1
		}
	}
	return px*bpp <= maxDecodeBytes
}

type Files struct {
	s        *Server
	mu       sync.Mutex
	reserved int64
}

func newFiles(s *Server) *Files { return &Files{s: s} }

func (s *Server) routesFiles(r chi.Router) {
	r.Post("/channels/{id}/attachments", s.h(s.files.upload))
}

type AttachmentDTO struct {
	ID       ID     `json:"id"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	URL      string `json:"url"`
	ThumbURL string `json:"thumbUrl,omitempty"`
}

// ---------- paths & keys ----------

func randomKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (f *Files) path(key string) string {
	return filepath.Join(f.s.cfg.FilesDir(), key[:2], key)
}

func (f *Files) avatarPath(key string) string {
	return filepath.Join(f.s.cfg.FilesDir(), "avatars", key)
}

var avatarKeyRe = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg)$`)

func (f *Files) removeKeys(keys []string) {
	for _, k := range keys {
		if k != "" {
			_ = os.Remove(f.path(k))
		}
	}
}

func (f *Files) removeAvatar(key string) {
	if avatarKeyRe.MatchString(key) {
		_ = os.Remove(f.avatarPath(key))
	}
}

func (f *Files) keysWhere(ctx context.Context, where string, args ...any) ([]string, error) {
	rows, err := f.s.db.R.QueryContext(ctx, `SELECT storage_key, COALESCE(thumb_key,'') FROM attachments WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		out = append(out, a, b)
	}
	return out, rows.Err()
}

func (f *Files) keysForSpace(ctx context.Context, spaceID int64) ([]string, error) {
	return f.keysWhere(ctx, `channel_id IN (SELECT id FROM channels WHERE space_id=?)`, spaceID)
}
func (f *Files) keysForChannel(ctx context.Context, id int64) ([]string, error) {
	return f.keysWhere(ctx, `channel_id=?`, id)
}
func (f *Files) keysForMessage(ctx context.Context, id int64) ([]string, error) {
	return f.keysWhere(ctx, `message_id=?`, id)
}

// ---------- signed URLs ----------

func (f *Files) sign(id int64, variant string, exp int64) string {
	m := hmac.New(sha256.New, f.s.cfg.FileURLSecret)
	fmt.Fprintf(m, "%d|%s|%d", id, variant, exp)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// signedURL expires 24–25 h from now; the expiry is rounded to the hour so
// URLs stay stable (and cacheable) within that hour.
func (f *Files) signedURL(id int64, variant string) string {
	exp := (f.s.now().Unix()/3600 + 25) * 3600
	p := "/files/" + strconv.FormatInt(id, 10)
	if variant == "thumb" {
		p += "/thumb"
	}
	return p + "?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + f.sign(id, variant, exp)
}

func (f *Files) dto(id int64, filename, mime string, size int64, w, h sql.NullInt64, thumb sql.NullString) AttachmentDTO {
	d := AttachmentDTO{ID: ID(id), Filename: filename, Mime: mime, Size: size, Width: int(w.Int64), Height: int(h.Int64), URL: f.signedURL(id, "orig")}
	if thumb.Valid && thumb.String != "" {
		d.ThumbURL = f.signedURL(id, "thumb")
	} else if strings.HasPrefix(mime, "image/") && w.Valid {
		d.ThumbURL = d.URL
	}
	return d
}

func (f *Files) attachmentsFor(ctx context.Context, msgIDs []int64) (map[int64][]AttachmentDTO, error) {
	out := map[int64][]AttachmentDTO{}
	if len(msgIDs) == 0 {
		return out, nil
	}
	rows, err := f.s.db.R.QueryContext(ctx, `SELECT id, message_id, filename, mime, size_bytes, width, height, thumb_key FROM attachments
		WHERE message_id IN (`+placeholders(len(msgIDs))+`) ORDER BY id`, idArgs(msgIDs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, mid, size int64
		var name, mt string
		var w, h sql.NullInt64
		var thumb sql.NullString
		if err := rows.Scan(&id, &mid, &name, &mt, &size, &w, &h, &thumb); err != nil {
			return nil, err
		}
		out[mid] = append(out[mid], f.dto(id, name, mt, size, w, h, thumb))
	}
	return out, rows.Err()
}

// ---------- quota ----------

func (f *Files) used(ctx context.Context) (int64, error) {
	var n int64
	err := f.s.db.R.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes),0) FROM attachments`).Scan(&n)
	return n, err
}

func (f *Files) reserve(ctx context.Context, size int64) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	used, err := f.used(ctx)
	if err != nil {
		return nil, err
	}
	if used+f.reserved+size > f.s.cfg.QuotaBytes {
		return nil, errf(http.StatusInsufficientStorage, "quota_exceeded", "storage quota exceeded")
	}
	f.reserved += size
	return func() {
		f.mu.Lock()
		f.reserved -= size
		f.mu.Unlock()
	}, nil
}

// ---------- upload ----------

var unsafeName = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f\x7f]`)

func sanitizeFilename(n string) string {
	if u, err := url.PathUnescape(n); err == nil {
		n = u
	}
	if !utf8.ValidString(n) {
		n = strings.ToValidUTF8(n, "")
	}
	n = unsafeName.ReplaceAllString(n, "_")
	n = strings.Trim(strings.TrimSpace(n), ".")
	// Strip bidi overrides that could disguise extensions.
	n = strings.Map(func(r rune) rune {
		if (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
			return -1
		}
		return r
	}, n)
	for len(n) > 200 {
		_, size := utf8.DecodeLastRuneInString(n)
		n = n[:len(n)-size]
	}
	if n == "" {
		n = "file"
	}
	return n
}

func (f *Files) upload(w http.ResponseWriter, r *http.Request) error {
	s := f.s
	se, ctx := sess(r), r.Context()
	if !s.lim.upload.Allow("u" + ID(se.UserID).String()) {
		return errRateLimited
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, a, err := s.requireTextChannel(ctx, se, id)
	if err != nil {
		return err
	}
	if !c.may(a, PermSend|PermAttach) {
		return errf(http.StatusForbidden, "attach_denied", "you cannot send files here")
	}
	if c.Kind == "dm" {
		if err := s.checkDMAllowed(ctx, c, se.UserID); err != nil {
			return err
		}
	}
	size := r.ContentLength
	if size <= 0 {
		return errf(http.StatusLengthRequired, "length_required", "Content-Length required")
	}
	if size > s.cfg.MaxUploadBytes {
		return errf(http.StatusRequestEntityTooLarge, "file_too_large", "file too large")
	}
	name := sanitizeFilename(r.Header.Get("X-Filename"))
	release, err := f.reserve(ctx, size)
	if err != nil {
		return err
	}
	defer release()

	tmpDir := filepath.Join(s.cfg.FilesDir(), "tmp")
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(tmpDir, "up-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	h := sha256.New()
	head := make([]byte, 512)
	body := io.LimitReader(r.Body, size+1)
	nHead, _ := io.ReadFull(body, head)
	head = head[:nHead]
	n, err := io.Copy(io.MultiWriter(tmp, h), io.MultiReader(bytes.NewReader(head), body))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return errf(http.StatusBadRequest, "upload_failed", "upload interrupted")
	}
	if n != size {
		return badRequest("size_mismatch")
	}
	mt := http.DetectContentType(head)
	if i := strings.IndexByte(mt, ';'); i > 0 {
		mt = mt[:i]
	}
	// DetectContentType does not know a few common containers.
	if mt == "application/octet-stream" || mt == "text/plain" {
		if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); byExt != "" && !isActiveType(byExt) {
			if i := strings.IndexByte(byExt, ';'); i > 0 {
				byExt = byExt[:i]
			}
			mt = byExt
		}
	}

	key := randomKey()
	var width, height any
	var thumbKey any
	if strings.HasPrefix(mt, "image/") {
		if tw, th, tk, err := f.makeThumb(r.Context(), tmpName, mt); err == nil {
			width, height = tw, th
			if tk != "" {
				thumbKey = tk
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(f.path(key)), 0o750); err != nil {
		return err
	}
	if err := os.Rename(tmpName, f.path(key)); err != nil {
		return err
	}
	attID := store.NewID()
	if _, err := s.db.W.ExecContext(ctx, `INSERT INTO attachments(id, uploader_id, channel_id, filename, mime, size_bytes, sha256, width, height, storage_key, thumb_key, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, attID, se.UserID, id, name, mt, size, h.Sum(nil), width, height, key, thumbKey, s.nowMs()); err != nil {
		_ = os.Remove(f.path(key))
		if tk, ok := thumbKey.(string); ok {
			_ = os.Remove(f.path(tk))
		}
		return err
	}
	var wv, hv sql.NullInt64
	if v, ok := width.(int); ok {
		wv = sql.NullInt64{Int64: int64(v), Valid: true}
		hv = sql.NullInt64{Int64: int64(height.(int)), Valid: true}
	}
	var tv sql.NullString
	if v, ok := thumbKey.(string); ok {
		tv = sql.NullString{String: v, Valid: true}
	}
	writeJSON(w, 201, f.dto(attID, name, mt, size, wv, hv, tv))
	return nil
}

// isActiveType: content that a browser could execute if served inline.
func isActiveType(mt string) bool {
	mt = strings.ToLower(mt)
	return strings.Contains(mt, "html") || strings.Contains(mt, "javascript") || strings.Contains(mt, "svg") || strings.Contains(mt, "xml")
}

// makeThumb decodes the image header (pixel limit against decompression
// bombs) and writes a ≤800 px thumbnail when the image is larger or not a
// cheap format to display. Returns the original dimensions.
func (f *Files) makeThumb(ctx context.Context, path, mt string) (int, int, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, "", err
	}
	defer file.Close()
	cfg, _, err := image.DecodeConfig(bufio.NewReader(file))
	if err != nil {
		return 0, 0, "", err
	}
	if !decodable(cfg) {
		return 0, 0, "", errors.New("image too large")
	}
	small := cfg.Width <= thumbMax && cfg.Height <= thumbMax
	if small && (mt == "image/png" || mt == "image/jpeg") {
		return cfg.Width, cfg.Height, "", nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, 0, "", err
	}
	release, err := decodeLimit.Acquire(ctx)
	if err != nil {
		return 0, 0, "", err
	}
	defer release()
	img, _, err := image.Decode(bufio.NewReader(file))
	if err != nil {
		return 0, 0, "", err
	}
	tw, th := fit(cfg.Width, cfg.Height, thumbMax)
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	alpha := hasAlpha(img)
	key := randomKey() + ".jpg"
	if alpha {
		key = randomKey() + ".png"
	}
	if err := os.MkdirAll(filepath.Dir(f.path(key)), 0o750); err != nil {
		return 0, 0, "", err
	}
	if err := writeImage(f.path(key), dst, alpha); err != nil {
		return 0, 0, "", err
	}
	return cfg.Width, cfg.Height, key, nil
}

func fit(w, h, max int) (int, int) {
	if w <= max && h <= max {
		return w, h
	}
	if w >= h {
		return max, maxInt(1, h*max/w)
	}
	return maxInt(1, w*max/h), max
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func hasAlpha(img image.Image) bool {
	switch img.ColorModel() {
	case color.YCbCrModel, color.GrayModel, color.Gray16Model, color.CMYKModel:
		return false
	}
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return !o.Opaque()
	}
	return true
}

func writeImage(path string, img image.Image, alpha bool) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(out)
	if alpha {
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(bw, img)
	} else {
		err = jpeg.Encode(bw, img, &jpeg.Options{Quality: 82})
	}
	if err == nil {
		err = bw.Flush()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

// ---------- serving ----------

var inlineTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"video/mp4": true, "video/webm": true, "audio/mpeg": true, "audio/ogg": true, "audio/wav": true, "audio/webm": true, "audio/mp4": true,
}

func (f *Files) serveFile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	variant := "orig"
	if strings.HasSuffix(r.URL.Path, "/thumb") {
		variant = "thumb"
	}
	exp, err := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	if err != nil || exp < f.s.now().Unix() {
		return errf(http.StatusForbidden, "url_expired", "link expired")
	}
	if !hmac.Equal([]byte(r.URL.Query().Get("sig")), []byte(f.sign(id, variant, exp))) {
		return errForbidden
	}
	var key, name, mt string
	var thumb sql.NullString
	var created int64
	err = f.s.db.R.QueryRowContext(r.Context(), `SELECT storage_key, thumb_key, filename, mime, created_at FROM attachments WHERE id=?`, id).
		Scan(&key, &thumb, &name, &mt, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	disp := "attachment"
	if variant == "thumb" {
		if !thumb.Valid {
			return errNotFound
		}
		key, disp = thumb.String, "inline"
		mt = "image/jpeg"
		if strings.HasSuffix(key, ".png") {
			mt = "image/png"
		}
	}
	file, err := os.Open(f.path(key))
	if err != nil {
		return errNotFound
	}
	defer file.Close()
	if !inlineTypes[mt] {
		mt = "application/octet-stream"
	}
	hd := w.Header()
	hd.Set("Content-Type", mt)
	hd.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": name}))
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	hd.Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", time.UnixMilli(created), file)
	return nil
}

func (f *Files) serveAvatar(w http.ResponseWriter, r *http.Request) error {
	key := chi.URLParam(r, "key")
	if !avatarKeyRe.MatchString(key) {
		return errNotFound
	}
	file, err := os.Open(f.avatarPath(key))
	if err != nil {
		return errNotFound
	}
	defer file.Close()
	mt := "image/png"
	if strings.HasSuffix(key, ".jpg") {
		mt = "image/jpeg"
	}
	w.Header().Set("Content-Type", mt)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "", time.Time{}, file)
	return nil
}

// ---------- avatars & icons ----------

// processAvatar decodes an uploaded image, center-crops it square, scales to
// 256 px and stores it under a random key.
func (f *Files) processAvatar(r *http.Request) (string, error) {
	release, err := decodeLimit.Acquire(r.Context())
	if err != nil {
		return "", err
	}
	defer release()
	if r.ContentLength > maxAvatarBytes {
		return "", errf(http.StatusRequestEntityTooLarge, "file_too_large", "image too large")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxAvatarBytes+1))
	if err != nil {
		return "", errBadRequest
	}
	if len(data) > maxAvatarBytes {
		return "", errf(http.StatusRequestEntityTooLarge, "file_too_large", "image too large")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !decodable(cfg) {
		return "", badRequest("invalid_image")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", badRequest("invalid_image")
	}
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	src := image.Rect(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2, 0, 0)
	src.Max = src.Min.Add(image.Pt(side, side))
	dst := image.NewRGBA(image.Rect(0, 0, avatarSize, avatarSize))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, src, draw.Src, nil)
	alpha := hasAlpha(img)
	key := randomKey() + ".jpg"
	if alpha {
		key = randomKey() + ".png"
	}
	if err := os.MkdirAll(filepath.Join(f.s.cfg.FilesDir(), "avatars"), 0o750); err != nil {
		return "", err
	}
	if err := writeImage(f.avatarPath(key), dst, alpha); err != nil {
		return "", err
	}
	return key, nil
}

func (s *Server) uploadAvatar(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	if !s.lim.upload.Allow("u" + ID(se.UserID).String()) {
		return errRateLimited
	}
	key, err := s.files.processAvatar(r)
	if err != nil {
		return err
	}
	var old sql.NullString
	_ = s.db.R.QueryRowContext(ctx, `SELECT avatar_key FROM users WHERE id=?`, se.UserID).Scan(&old)
	if _, err := s.db.W.ExecContext(ctx, `UPDATE users SET avatar_key=? WHERE id=?`, key, se.UserID); err != nil {
		s.files.removeAvatar(key)
		return err
	}
	if old.Valid {
		s.files.removeAvatar(old.String)
	}
	s.broadcastUser(ctx, se.UserID)
	writeJSON(w, 200, map[string]string{"avatarUrl": "/avatars/" + key})
	return nil
}

func (s *Server) deleteAvatar(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	var old sql.NullString
	_ = s.db.R.QueryRowContext(ctx, `SELECT avatar_key FROM users WHERE id=?`, se.UserID).Scan(&old)
	if _, err := s.db.W.ExecContext(ctx, `UPDATE users SET avatar_key=NULL WHERE id=?`, se.UserID); err != nil {
		return err
	}
	if old.Valid {
		s.files.removeAvatar(old.String)
	}
	s.broadcastUser(ctx, se.UserID)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) uploadSpaceIcon(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireSpace(ctx, se, id, PermManageSpace); err != nil {
		return err
	}
	if !s.lim.upload.Allow("u" + ID(se.UserID).String()) {
		return errRateLimited
	}
	key, err := s.files.processAvatar(r)
	if err != nil {
		return err
	}
	var old sql.NullString
	_ = s.db.R.QueryRowContext(ctx, `SELECT icon_key FROM spaces WHERE id=?`, id).Scan(&old)
	if _, err := s.db.W.ExecContext(ctx, `UPDATE spaces SET icon_key=? WHERE id=?`, key, id); err != nil {
		s.files.removeAvatar(key)
		return err
	}
	if old.Valid {
		s.files.removeAvatar(old.String)
	}
	s.spaceUpdate(ctx, id)
	writeJSON(w, 200, map[string]string{"iconUrl": "/avatars/" + key})
	return nil
}

func (s *Server) deleteSpaceIcon(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := s.requireSpace(ctx, se, id, PermManageSpace); err != nil {
		return err
	}
	var old sql.NullString
	_ = s.db.R.QueryRowContext(ctx, `SELECT icon_key FROM spaces WHERE id=?`, id).Scan(&old)
	if _, err := s.db.W.ExecContext(ctx, `UPDATE spaces SET icon_key=NULL WHERE id=?`, id); err != nil {
		return err
	}
	if old.Valid {
		s.files.removeAvatar(old.String)
	}
	s.spaceUpdate(ctx, id)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- cleanup & admin ----------

type attRef struct {
	id, msg, channel int64
	keys             []string
}

func (f *Files) deleteWhere(ctx context.Context, notify bool, where string, args ...any) (int, error) {
	rows, err := f.s.db.R.QueryContext(ctx, `SELECT id, COALESCE(message_id,0), channel_id, storage_key, COALESCE(thumb_key,'') FROM attachments WHERE `+where+` LIMIT 1000`, args...)
	if err != nil {
		return 0, err
	}
	var refs []attRef
	for rows.Next() {
		var a attRef
		var k1, k2 string
		if err := rows.Scan(&a.id, &a.msg, &a.channel, &k1, &k2); err != nil {
			rows.Close()
			return 0, err
		}
		a.keys = []string{k1, k2}
		refs = append(refs, a)
	}
	rows.Close()
	for _, a := range refs {
		if _, err := f.s.db.W.ExecContext(ctx, `DELETE FROM attachments WHERE id=?`, a.id); err != nil {
			return 0, err
		}
		f.removeKeys(a.keys)
		if notify && a.msg != 0 {
			if c, err := f.s.loadChannel(ctx, a.channel); err == nil {
				f.s.emitChannel(ctx, c, "attachment_delete", map[string]any{"id": ID(a.id), "messageId": ID(a.msg), "channelId": ID(a.channel)})
			}
		}
	}
	return len(refs), nil
}

func (f *Files) purgeOrphans(ctx context.Context, age time.Duration) (int, error) {
	n, err := f.deleteWhere(ctx, false, `attached=0 AND created_at<?`, f.s.nowMs()-age.Milliseconds())
	// Stale temp files from interrupted uploads.
	tmp := filepath.Join(f.s.cfg.FilesDir(), "tmp")
	if ents, e := os.ReadDir(tmp); e == nil {
		for _, en := range ents {
			if info, e := en.Info(); e == nil && time.Since(info.ModTime()) > age {
				_ = os.Remove(filepath.Join(tmp, en.Name()))
			}
		}
	}
	return n, err
}

func (f *Files) purgeOlderThan(ctx context.Context, age time.Duration) (int, error) {
	return f.deleteWhere(ctx, true, `created_at<?`, f.s.nowMs()-age.Milliseconds())
}

// PurgeAttachmentsOlderThan is used by the admin CLI.
func (s *Server) PurgeAttachmentsOlderThan(ctx context.Context, days int) (int, error) {
	total := 0
	for {
		n, err := s.files.purgeOlderThan(ctx, time.Duration(days)*24*time.Hour)
		total += n
		if err != nil || n < 1000 {
			return total, err
		}
	}
}

type StorageReport struct {
	UsedBytes  int64         `json:"usedBytes"`
	QuotaBytes int64         `json:"quotaBytes"`
	FileCount  int64         `json:"fileCount"`
	Largest    []storageItem `json:"largest"`
}

type storageItem struct {
	ID         ID     `json:"id"`
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	UploaderID ID     `json:"uploaderId"`
	ChannelID  ID     `json:"channelId"`
	CreatedAt  int64  `json:"createdAt"`
}

func (s *Server) Storage(ctx context.Context, limit int) (StorageReport, error) {
	rep := StorageReport{QuotaBytes: s.cfg.QuotaBytes, Largest: []storageItem{}}
	if err := s.db.R.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes),0), COUNT(*) FROM attachments`).Scan(&rep.UsedBytes, &rep.FileCount); err != nil {
		return rep, err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, filename, size_bytes, uploader_id, channel_id, created_at FROM attachments ORDER BY size_bytes DESC LIMIT ?`, limit)
	if err != nil {
		return rep, err
	}
	defer rows.Close()
	for rows.Next() {
		var it storageItem
		var id, up, ch int64
		if err := rows.Scan(&id, &it.Filename, &it.Size, &up, &ch, &it.CreatedAt); err != nil {
			return rep, err
		}
		it.ID, it.UploaderID, it.ChannelID = ID(id), ID(up), ID(ch)
		rep.Largest = append(rep.Largest, it)
	}
	return rep, rows.Err()
}

func (s *Server) adminStorage(w http.ResponseWriter, r *http.Request) error {
	if !sess(r).Admin {
		return errForbidden
	}
	rep, err := s.Storage(r.Context(), 50)
	if err != nil {
		return err
	}
	writeJSON(w, 200, rep)
	return nil
}

func (s *Server) adminDeleteFile(w http.ResponseWriter, r *http.Request) error {
	se := sess(r)
	if !se.Admin {
		return errForbidden
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	n, err := s.files.deleteWhere(r.Context(), true, `id=?`, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound("unknown_file")
	}
	s.audit(r.Context(), se.UserID, 0, "file_delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
