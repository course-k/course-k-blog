package mysql

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
		t.Skip("dedicated MySQL not configured")
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
	if _, err = owner.db.ExecContext(context.Background(), "CREATE DATABASE "+schema+" CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin"); err != nil {
		t.Fatal(err)
	}
	target.Path = "/" + schema
	store, err := Open(context.Background(), target.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		owner.db.ExecContext(context.Background(), "DROP DATABASE "+schema)
		owner.Close()
	})
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}
