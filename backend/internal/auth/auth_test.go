package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashAndVerification(t *testing.T) {
	h, err := HashPassword("a long test password only")
	if err != nil {
		t.Fatal(err)
	}
	if !Verify("a long test password only", h) || Verify("wrong", h) {
		t.Fatal("password validation failed")
	}
	if strings.Contains(h, "a long test password only") {
		t.Fatal("plaintext password stored")
	}
	for _, bad := range []string{"", "pbkdf2-sha256-v1$bad$bad", h + "$extra"} {
		if ValidHash(bad) || Verify("test", bad) {
			t.Fatal("malformed hash accepted")
		}
	}
}
func TestTokensAreRandomAndStoredAsDigests(t *testing.T) {
	a, e := Token()
	if e != nil {
		t.Fatal(e)
	}
	b, e := Token()
	if e != nil {
		t.Fatal(e)
	}
	if a == b || len(a) != 43 || len(Digest(a)) != 64 || Digest(a) == a {
		t.Fatal("invalid session token")
	}
}
