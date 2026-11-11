package config

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestValidPublicURL(t *testing.T) {
	ok := []string{"https://chat.example.org", "https://chat.example.org/", "https://1-2-3-4.sslip.io:8443", "http://localhost:8080", "http://127.0.0.1:8080"}
	bad := []string{"", "chat.example.org", "http://chat.example.org", "http://192.168.1.10:8080", "https://chat.example.org/sub", "https://u:p@chat.example.org", "https://x.org/?a=1", "ftp://x.org"}
	for _, u := range ok {
		if !validPublicURL(u) {
			t.Errorf("%q should be valid", u)
		}
	}
	for _, u := range bad {
		if validPublicURL(u) {
			t.Errorf("%q should be invalid", u)
		}
	}
}

func TestSignupPolicyAndName(t *testing.T) {
	t.Setenv("LS_PUBLIC_URL", "http://127.0.0.1:8080")
	c, err := Load(true)
	if err != nil {
		t.Fatal(err)
	}
	if c.Signup != SignupAdmin || c.ServerName != "127.0.0.1:8080" || c.PublicHost() != "127.0.0.1:8080" || c.DownloadURL != DefaultDownloadURL {
		t.Fatalf("defaults: %+v", c)
	}
	t.Setenv("LS_SIGNUP", "everyone")
	if _, err := Load(true); err == nil {
		t.Fatal("invalid LS_SIGNUP accepted")
	}
	t.Setenv("LS_SIGNUP", "closed")
	t.Setenv("LS_SERVER_NAME", "Freunde")
	c, err = Load(true)
	if err != nil || c.Signup != SignupClosed || c.ServerName != "Freunde" {
		t.Fatalf("overrides: %+v %v", c, err)
	}
}

func TestMaxSpaces(t *testing.T) {
	t.Setenv("LS_PUBLIC_URL", "http://127.0.0.1:8080")
	c, err := Load(true)
	if err != nil || c.MaxSpaces != 3 {
		t.Fatalf("default: %+v %v", c, err)
	}
	t.Setenv("LS_MAX_SPACES", "0")
	if c, err = Load(true); err != nil || c.MaxSpaces != 0 {
		t.Fatalf("zero: %+v %v", c, err)
	}
	for _, bad := range []string{"-1", "101", "x"} {
		t.Setenv("LS_MAX_SPACES", bad)
		if _, err := Load(true); err == nil {
			t.Fatalf("LS_MAX_SPACES=%s accepted", bad)
		}
	}
}

func TestLimitsDefaultsAndBounds(t *testing.T) {
	c, err := Load(true)
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxMessageChars != 4000 || c.MaxSpaceMembers != 500 || c.MaxSpaceChannels != 200 || c.MaxSpaceRoles != 25 ||
		c.MaxVoiceParticipants != 25 || c.Invites != InvitesMembers || c.MaxUploadBytes != 200<<20 {
		t.Fatalf("defaults changed: %+v", c)
	}
	for _, bad := range [][2]string{
		{"LS_MAX_MESSAGE_CHARS", "99"}, {"LS_MAX_MESSAGE_CHARS", "20001"}, {"LS_MAX_SPACE_MEMBERS", "1"}, {"LS_MAX_SPACE_MEMBERS", "1001"},
		{"LS_MAX_SPACE_CHANNELS", "9"}, {"LS_MAX_SPACE_ROLES", "101"}, {"LS_MAX_VOICE_PARTICIPANTS", "1"}, {"LS_MAX_UPLOAD_MB", "0"},
		{"LS_MAX_SPACES", "abc"}, {"LS_INVITES", "everyone"},
	} {
		t.Run(bad[0]+"="+bad[1], func(t *testing.T) {
			t.Setenv(bad[0], bad[1])
			if _, err := Load(true); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	t.Setenv("LS_MAX_MESSAGE_CHARS", "20000")
	t.Setenv("LS_INVITES", InvitesAdmins)
	if c, err = Load(true); err != nil || c.MaxMessageChars != 20000 || c.Invites != InvitesAdmins {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestSettingsHideSecrets(t *testing.T) {
	t.Setenv("LS_FILE_URL_SECRET", "file-secret-file-secret-file-secret-1")
	t.Setenv("LIVEKIT_API_SECRET", "livekit-secret")
	c, err := Load(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range c.Settings() {
		if strings.Contains(s[1], "secret") || strings.Contains(s[0], "SECRET") {
			t.Fatalf("secret in settings: %v", s)
		}
	}
}

func TestUnknownEnv(t *testing.T) {
	t.Setenv("LS_MAX_SPCAES", "5")
	t.Setenv("LS_MAX_SPACES", "5")
	if got := UnknownEnv(); !slices.Contains(got, "LS_MAX_SPCAES") || slices.Contains(got, "LS_MAX_SPACES") {
		t.Fatalf("unknown: %v", got)
	}
}

// The operator template must list real keys with the real defaults.
func TestServerEnvExample(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/server.env.example")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(true)
	if err != nil {
		t.Fatal(err)
	}
	effective := map[string]string{}
	for _, s := range c.Settings() {
		effective[s[0]] = s[1]
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "# "), "=")
		if !ok || !strings.HasPrefix(k, "LS_") {
			continue
		}
		n++
		if !slices.Contains(knownEnv, k) {
			t.Errorf("%s is not a known setting", k)
		}
		if want, shown := effective[k]; shown && want != v {
			t.Errorf("%s: template says %q, default is %q", k, v, want)
		}
	}
	if n < 12 {
		t.Fatalf("only %d settings found in the template", n)
	}
}
