// Package config reads the server configuration from environment variables
// (systemd EnvironmentFile /etc/lightspeak/server.env in production).
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type Config struct {
	Dev               bool
	Addr              string // public API (behind Caddy)
	WebhookAddr       string // LiveKit webhooks, localhost only
	DataDir           string
	PublicURL         string // e.g. https://lightspeak.example
	LiveKitURL        string // URL handed to clients (wss://rtc...)
	LiveKitAPIURL     string // URL the server uses for RoomService (http://127.0.0.1:7880)
	LiveKitKey        string
	LiveKitSecret     string
	FileURLSecret     []byte
	MaxUploadBytes    int64
	QuotaBytes        int64
	RetentionDays     int
	MinClientVersion  string
	ArgonMemoryKiB    uint32
	ArgonTime         uint32
	AllowedOrigins    []string
	TrustForwardedFor bool
	// ServerName is shown to clients before they log in (default: public host).
	ServerName string
	// Signup decides which invites may create new accounts (see Signup* consts).
	Signup string
	// DownloadURL is linked from the landing pages when no installer is hosted
	// locally under public/downloads (default: GitHub releases).
	DownloadURL string
	// MaxSpaces is how many spaces a regular user may own (LS_MAX_SPACES,
	// default 3; 0 = only server admins create spaces). Server admins are exempt.
	MaxSpaces int
	// Capacity limits, adjustable by the operator; see the bounds in Load.
	MaxMessageChars      int // LS_MAX_MESSAGE_CHARS
	MaxSpaceMembers      int // LS_MAX_SPACE_MEMBERS
	MaxSpaceChannels     int // LS_MAX_SPACE_CHANNELS (categories count)
	MaxSpaceRoles        int // LS_MAX_SPACE_ROLES
	MaxVoiceParticipants int // LS_MAX_VOICE_PARTICIPANTS, also LiveKit's room limit
	// Invites decides who may create space invites (see Invites* consts).
	Invites string
}

// Account creation policies (LS_SIGNUP).
const (
	// SignupAdmin: only invites created by a server admin (or on the command
	// line) create new accounts; other invites only work for existing accounts.
	SignupAdmin = "admin"
	// SignupInvite: every valid invite creates new accounts.
	SignupInvite = "invite"
	// SignupClosed: no new accounts except owner invites from `admin bootstrap`.
	SignupClosed = "closed"
)

// Invite policies (LS_INVITES).
const (
	// InvitesMembers: everyone with the space permission "Invite people".
	InvitesMembers = "members"
	// InvitesAdmins: only server admins (and the command line). Invites other
	// people created earlier stop working as well.
	InvitesAdmins = "admins"
)

// DefaultDownloadURL is where official client builds are published.
const DefaultDownloadURL = "https://github.com/syntstar/lightspeak/releases/latest/download/Lightspeak-setup.exe"

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envInt(k string, def int64) (int64, error) {
	v := env(k, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return n, nil
}

// envRange reads an integer setting and rejects values outside [lo, hi].
func envRange(k string, def, lo, hi int64) (int, error) {
	n, err := envInt(k, def)
	if err != nil {
		return 0, err
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%s must be between %d and %d", k, lo, hi)
	}
	return int(n), nil
}

// knownEnv lists every variable the server or install.sh reads, so that a
// mistyped setting is reported instead of being silently ignored.
var knownEnv = []string{
	"LS_ADDR", "LS_WEBHOOK_ADDR", "LS_DATA_DIR", "LS_PUBLIC_URL", "LS_LIVEKIT_URL", "LS_LIVEKIT_API_URL",
	"LS_MIN_CLIENT_VERSION", "LS_TRUST_PROXY", "LS_SIGNUP", "LS_INVITES", "LS_DOWNLOAD_URL", "LS_MAX_SPACES",
	"LS_MAX_MESSAGE_CHARS", "LS_MAX_SPACE_MEMBERS", "LS_MAX_SPACE_CHANNELS", "LS_MAX_SPACE_ROLES",
	"LS_MAX_VOICE_PARTICIPANTS", "LS_SERVER_NAME", "LS_FILE_URL_SECRET", "LS_MAX_UPLOAD_MB",
	"LS_STORAGE_QUOTA_GB", "LS_RETENTION_DAYS", "LS_ARGON_MEMORY_KIB", "LS_ARGON_TIME", "LS_EXTRA_ORIGINS",
	"LS_RELEASE_URL", "LS_RELEASE_PUBKEY",
}

// UnknownEnv returns the LS_* variables in the environment that nothing reads.
func UnknownEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "LS_") && !slices.Contains(knownEnv, k) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// Settings lists the effective settings an operator can change, for
// `check-config`. It never contains a secret.
func (c *Config) Settings() [][2]string {
	num := strconv.Itoa
	return [][2]string{
		{"LS_PUBLIC_URL", c.PublicURL},
		{"LS_SERVER_NAME", c.ServerName},
		{"LS_SIGNUP", c.Signup},
		{"LS_INVITES", c.Invites},
		{"LS_MAX_SPACES", num(c.MaxSpaces)},
		{"LS_MAX_MESSAGE_CHARS", num(c.MaxMessageChars)},
		{"LS_MAX_SPACE_MEMBERS", num(c.MaxSpaceMembers)},
		{"LS_MAX_SPACE_CHANNELS", num(c.MaxSpaceChannels)},
		{"LS_MAX_SPACE_ROLES", num(c.MaxSpaceRoles)},
		{"LS_MAX_VOICE_PARTICIPANTS", num(c.MaxVoiceParticipants)},
		{"LS_MAX_UPLOAD_MB", strconv.FormatInt(c.MaxUploadBytes>>20, 10)},
		{"LS_STORAGE_QUOTA_GB", strconv.FormatInt(c.QuotaBytes>>30, 10)},
		{"LS_RETENTION_DAYS", num(c.RetentionDays)},
		{"LS_MIN_CLIENT_VERSION", c.MinClientVersion},
		{"LS_DOWNLOAD_URL", c.DownloadURL},
	}
}

// Load builds the configuration. In dev mode sensible local defaults are used
// (data in ./dev-data, LiveKit --dev keys) so `serve --dev` works out of the box.
func Load(dev bool) (*Config, error) {
	c := &Config{Dev: dev}
	c.Addr = env("LS_ADDR", "127.0.0.1:8080")
	c.WebhookAddr = env("LS_WEBHOOK_ADDR", "127.0.0.1:8081")
	defData := "/var/lib/lightspeak"
	if dev {
		defData = "dev-data"
	}
	c.DataDir = env("LS_DATA_DIR", defData)
	c.PublicURL = strings.TrimRight(env("LS_PUBLIC_URL", map[bool]string{true: "http://127.0.0.1:8080", false: ""}[dev]), "/")
	c.LiveKitURL = env("LS_LIVEKIT_URL", map[bool]string{true: "ws://127.0.0.1:7880", false: ""}[dev])
	c.LiveKitAPIURL = env("LS_LIVEKIT_API_URL", "http://127.0.0.1:7880")
	c.LiveKitKey = env("LIVEKIT_API_KEY", map[bool]string{true: "devkey", false: ""}[dev])
	c.LiveKitSecret = env("LIVEKIT_API_SECRET", map[bool]string{true: "secret", false: ""}[dev])
	c.MinClientVersion = env("LS_MIN_CLIENT_VERSION", "0.1.0")
	c.TrustForwardedFor = env("LS_TRUST_PROXY", "1") == "1"
	c.Signup = env("LS_SIGNUP", SignupAdmin)
	if c.Signup != SignupAdmin && c.Signup != SignupInvite && c.Signup != SignupClosed {
		return nil, fmt.Errorf("LS_SIGNUP must be %q, %q or %q", SignupAdmin, SignupInvite, SignupClosed)
	}
	c.DownloadURL = env("LS_DOWNLOAD_URL", DefaultDownloadURL)
	c.Invites = env("LS_INVITES", InvitesMembers)
	if c.Invites != InvitesMembers && c.Invites != InvitesAdmins {
		return nil, fmt.Errorf("LS_INVITES must be %q or %q", InvitesMembers, InvitesAdmins)
	}
	// The upper bounds keep a request, the ready snapshot and one voice room
	// within what a small server handles; 1000 members fit one E2EE rotation
	// (maxEnvelopes). Protective limits (rate limits, image sizes, WebSocket
	// frames) are deliberately not configurable: a bad value there opens the
	// server to abuse.
	for _, l := range []struct {
		key         string
		dst         *int
		def, lo, hi int64
	}{
		{"LS_MAX_SPACES", &c.MaxSpaces, 3, 0, 100},
		{"LS_MAX_MESSAGE_CHARS", &c.MaxMessageChars, 4000, 100, 20000},
		{"LS_MAX_SPACE_MEMBERS", &c.MaxSpaceMembers, 500, 2, 1000},
		{"LS_MAX_SPACE_CHANNELS", &c.MaxSpaceChannels, 200, 10, 1000},
		{"LS_MAX_SPACE_ROLES", &c.MaxSpaceRoles, 25, 2, 100},
		{"LS_MAX_VOICE_PARTICIPANTS", &c.MaxVoiceParticipants, 25, 2, 100},
	} {
		n, err := envRange(l.key, l.def, l.lo, l.hi)
		if err != nil {
			return nil, err
		}
		*l.dst = n
	}
	c.ServerName = env("LS_SERVER_NAME", "")
	if c.ServerName == "" {
		if u, err := url.Parse(c.PublicURL); err == nil {
			c.ServerName = u.Host
		}
	}
	if len([]rune(c.ServerName)) > 64 {
		return nil, errors.New("LS_SERVER_NAME must be at most 64 characters")
	}

	fs := env("LS_FILE_URL_SECRET", "")
	if fs == "" && dev {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		fs = base64.StdEncoding.EncodeToString(b)
	}
	c.FileURLSecret = []byte(fs)

	mb, err := envRange("LS_MAX_UPLOAD_MB", 200, 1, 51200)
	if err != nil {
		return nil, err
	}
	c.MaxUploadBytes = int64(mb) << 20
	gb, err := envInt("LS_STORAGE_QUOTA_GB", 4)
	if err != nil {
		return nil, err
	}
	c.QuotaBytes = gb << 30
	rd, err := envInt("LS_RETENTION_DAYS", 0)
	if err != nil {
		return nil, err
	}
	c.RetentionDays = int(rd)
	am, err := envInt("LS_ARGON_MEMORY_KIB", 64*1024)
	if err != nil {
		return nil, err
	}
	c.ArgonMemoryKiB = uint32(am)
	at, err := envInt("LS_ARGON_TIME", 3)
	if err != nil {
		return nil, err
	}
	c.ArgonTime = uint32(at)

	c.AllowedOrigins = []string{"http://tauri.localhost", "https://tauri.localhost", "tauri://localhost"}
	if dev {
		c.AllowedOrigins = append(c.AllowedOrigins, "http://localhost:1420", "http://127.0.0.1:1420")
	}
	if extra := env("LS_EXTRA_ORIGINS", ""); extra != "" {
		c.AllowedOrigins = append(c.AllowedOrigins, strings.Split(extra, ",")...)
	}

	if !dev {
		var missing []string
		for k, v := range map[string]string{
			"LS_PUBLIC_URL": c.PublicURL, "LS_LIVEKIT_URL": c.LiveKitURL,
			"LIVEKIT_API_KEY": c.LiveKitKey, "LIVEKIT_API_SECRET": c.LiveKitSecret, "LS_FILE_URL_SECRET": fs,
		} {
			if v == "" {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("missing configuration: %s", strings.Join(missing, ", "))
		}
		if !validPublicURL(c.PublicURL) {
			return nil, errors.New("LS_PUBLIC_URL must be an https origin like https://chat.example.org (http only for localhost)")
		}
		if len(c.FileURLSecret) < 32 {
			return nil, errors.New("LS_FILE_URL_SECRET must be at least 32 characters")
		}
	}
	return c, nil
}

// validPublicURL accepts a bare origin: https, or http for loopback hosts
// (clients refuse plain http anywhere else).
func validPublicURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		h := u.Hostname()
		return h == "localhost" || h == "127.0.0.1" || h == "::1"
	}
	return false
}

// PublicHost is the host[:port] of PublicURL (used in app invite links).
func (c *Config) PublicHost() string {
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return ""
	}
	return u.Host
}

func (c *Config) DBPath() string    { return filepath.Join(c.DataDir, "data", "lightspeak.db") }
func (c *Config) FilesDir() string  { return filepath.Join(c.DataDir, "files") }
func (c *Config) BackupDir() string { return filepath.Join(c.DataDir, "backups") }
func (c *Config) PublicDir() string { return filepath.Join(c.DataDir, "public") }

// EnsureDirs creates the data directories with restrictive permissions.
func (c *Config) EnsureDirs() error {
	for _, d := range []string{filepath.Dir(c.DBPath()), c.FilesDir(), c.BackupDir()} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}
	return nil
}
