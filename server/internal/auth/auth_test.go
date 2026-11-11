package auth

import (
	"strings"
	"testing"
)

var fast = Params{MemoryKiB: 64, Time: 1, Threads: 1}

func TestHashVerify(t *testing.T) {
	h, err := Hash("correct horse", fast)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("format: %s", h)
	}
	if Verify("correct horse", h) != nil {
		t.Fatal("verify failed")
	}
	if Verify("wrong", h) != ErrMismatch {
		t.Fatal("wrong password accepted")
	}
	h2, _ := Hash("correct horse", fast)
	if h == h2 {
		t.Fatal("salt not random")
	}
	for _, bad := range []string{"", "$bcrypt$x", "$argon2id$v=1$m=1,t=1,p=1$AA$AA", "$argon2id$v=19$m=x$AA$AA"} {
		if Verify("x", bad) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	VerifyDummy(fast)
}

func TestTokens(t *testing.T) {
	a, ha, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := NewToken()
	if a == b || len(a) != 43 || len(ha) != 32 {
		t.Fatalf("token: %q", a)
	}
	if string(HashToken(a)) != string(ha) {
		t.Fatal("hash mismatch")
	}
	c := RandomCode(10)
	if len(c) != 10 || strings.ContainsAny(c, "0O1lI") {
		t.Fatalf("code: %q", c)
	}
}

func TestPasswordPolicy(t *testing.T) {
	cases := map[string]string{
		"short":                  "password_too_short",
		strings.Repeat("x", 257): "password_too_long",
		"password123":            "password_too_common",
		"Qwertyuiop":             "password_too_common",
		"my-name-is-alice!":      "password_contains_username",
		"tröte-blau-fahrrad":     "",
	}
	for pw, want := range cases {
		if got := CheckPassword(pw, "alice"); got != want {
			t.Errorf("%q: got %q want %q", pw, got, want)
		}
	}
}

func TestValidUsername(t *testing.T) {
	for u, ok := range map[string]bool{"anna": true, "a.b_c9": true, "ab": false, "Anna": false, "an na": false, "änna": false, strings.Repeat("a", 33): false} {
		if ValidUsername(u) != ok {
			t.Errorf("%q", u)
		}
	}
}
