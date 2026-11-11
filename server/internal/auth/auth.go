// Package auth contains password hashing (argon2id), session tokens and the
// small embedded list of common passwords.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

type Params struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
}

const (
	saltLen = 16
	keyLen  = 32
)

var ErrMismatch = errors.New("password mismatch")

// hashSem bounds concurrent argon2id runs (each needs MemoryKiB of RAM), so a
// burst of logins queues instead of exhausting the service memory limit.
var hashSem = make(chan struct{}, 2)

func idKey(pw, salt []byte, t, m uint32, p uint8, n uint32) []byte {
	hashSem <- struct{}{}
	defer func() { <-hashSem }()
	return argon2.IDKey(pw, salt, t, m, p, n)
}

// Hash returns an encoded argon2id hash ($argon2id$v=19$m=..,t=..,p=..$salt$key).
func Hash(password string, p Params) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := idKey([]byte(password), salt, p.Time, p.MemoryKiB, p.Threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.MemoryKiB, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify checks password against an encoded hash in constant time.
func Verify(password, encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return errors.New("unsupported hash")
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return errors.New("unsupported hash version")
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Time, &p.Threads); err != nil {
		return errors.New("bad hash params")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return err
	}
	got := idKey([]byte(password), salt, p.Time, p.MemoryKiB, p.Threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// VerifyDummy burns the same time as a real verification; used for unknown
// usernames so response time does not reveal whether an account exists.
func VerifyDummy(p Params) {
	dummyOnce.Do(func() { dummyHash, _ = Hash("lightspeak-dummy-password", p) })
	_ = Verify("not-the-password", dummyHash)
}

// NewToken returns a 256-bit random session token (base64url) and its SHA-256.
func NewToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// RandomCode returns an n-character code from an unambiguous alphabet.
func RandomCode(n int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

//go:embed common_passwords.txt
var commonList string

var common = sync.OnceValue(func() map[string]struct{} {
	m := map[string]struct{}{}
	for _, l := range strings.Split(commonList, "\n") {
		if l = strings.TrimSpace(strings.ToLower(l)); l != "" {
			m[l] = struct{}{}
		}
	}
	return m
})

// CheckPassword enforces the password policy; returns an error code or "".
func CheckPassword(pw, username string) string {
	if utf8.RuneCountInString(pw) < 10 {
		return "password_too_short"
	}
	if len(pw) > 256 {
		return "password_too_long"
	}
	lower := strings.ToLower(pw)
	if _, bad := common()[lower]; bad {
		return "password_too_common"
	}
	if username != "" && strings.Contains(lower, strings.ToLower(username)) {
		return "password_contains_username"
	}
	return ""
}

// ValidUsername: 3–32 characters of [a-z0-9_.].
func ValidUsername(u string) bool {
	if len(u) < 3 || len(u) > 32 {
		return false
	}
	for _, r := range u {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
