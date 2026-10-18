package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"
)

func isolatedStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL not configured")
	}
	target, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(target.Path, "_test") {
		t.Fatal("dedicated _test database required")
	}
	owner, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	var suffix [8]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := "feature_test_" + hex.EncodeToString(suffix[:])
	if _, err = owner.db.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	query := target.Query()
	query.Set("search_path", schema+",public")
	target.RawQuery = query.Encode()
	store, err := Open(context.Background(), target.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		owner.db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		owner.Close()
	})
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}
