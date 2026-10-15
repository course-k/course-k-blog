package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const iterations = 600000

// Use the standard-library KDF, never a fast hash for passwords. The format is
// versioned so parameter changes can be implemented explicitly later.
func HashPassword(password string) (string, error) {
	if len(password) < 16 || len(password) > 1024 {
		return "", errors.New("password must be 16–1024 bytes")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	return "pbkdf2-sha256-v1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func ValidHash(hash string) bool {
	p := strings.Split(hash, "$")
	if len(p) != 3 || p[0] != "pbkdf2-sha256-v1" {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(p[1])
	key, e2 := base64.RawStdEncoding.DecodeString(p[2])
	return e1 == nil && e2 == nil && len(salt) == 16 && len(key) == 32
}
func Verify(password, hash string) bool {
	if !ValidHash(hash) || len(password) > 1024 {
		return false
	}
	p := strings.Split(hash, "$")
	salt, _ := base64.RawStdEncoding.DecodeString(p[1])
	want, _ := base64.RawStdEncoding.DecodeString(p[2])
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}
func Token() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}
func Digest(token string) string { s := sha256.Sum256([]byte(token)); return hex.EncodeToString(s[:]) }
