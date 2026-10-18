package mysql

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in: a disposable TLS database with a localhost-only server certificate.
func TestTLSVerification(t *testing.T) {
	dsn := os.Getenv("TEST_TLS_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_TLS_DATABASE_URL not set: real TLS test not executed")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(u.Path, "_test") || u.Hostname() != "localhost" {
		t.Fatal("dedicated localhost TLS test database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("trusted TLS connection failed: %v", err)
	}
	s.Close()
	wrongHost := *u
	wrongHost.Host = "127.0.0.1:" + u.Port()
	if s, err := Open(ctx, wrongHost.String()); err == nil {
		s.Close()
		t.Fatal("wrong hostname accepted")
	}
	wrongCA := os.Getenv("TEST_TLS_WRONG_CA_FILE")
	if wrongCA == "" {
		t.Fatal("wrong CA fixture required")
	}
	q := u.Query()
	q.Set("sslrootcert", wrongCA)
	u.RawQuery = q.Encode()
	if s, err := Open(ctx, u.String()); err == nil {
		s.Close()
		t.Fatal("untrusted CA accepted")
	}
}
