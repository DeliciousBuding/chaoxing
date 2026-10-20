package access

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

const passwordIterations = 600000

// HashPassword returns pbkdf2-sha256$600000$<base64url salt>$<base64url hash>.
// Persist this hash, never the owner password or an upstream password.
func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("owner password must be 12..1024 bytes")
	}
	salt := randomBytes(16)
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return "", err
	}
	return "pbkdf2-sha256$600000$" + base64.RawURLEncoding.EncodeToString(salt) + "$" + base64.RawURLEncoding.EncodeToString(key), nil
}
func parsePasswordHash(raw string) (int, []byte, []byte, error) {
	p := strings.Split(raw, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return 0, nil, nil, errors.New("invalid owner password hash")
	}
	n, err := strconv.Atoi(p[1])
	salt, e1 := base64.RawURLEncoding.DecodeString(p[2])
	key, e2 := base64.RawURLEncoding.DecodeString(p[3])
	if err != nil || e1 != nil || e2 != nil || n < passwordIterations || n > 1000000 || len(salt) != 16 || len(key) != 32 {
		return 0, nil, nil, errors.New("invalid owner password hash parameters")
	}
	return n, salt, key, nil
}
func verifyPassword(raw, password string) bool {
	n, salt, want, err := parsePasswordHash(raw)
	if err != nil || len(password) > 1024 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, n, 32)
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// VerifyPassword lets independently mounted operator UIs authenticate against
// the same private owner hash without learning the password or hash format.
func VerifyPassword(raw, password string) bool {
	return verifyPassword(raw, password)
}
