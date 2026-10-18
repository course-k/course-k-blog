package mysql

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"coursek/blog/backend/internal/blog"
)

// The TLS fixture bootstraps separate users using the real production command.
func TestRuntimeDatabasePrivileges(t *testing.T) {
	dsn := os.Getenv("TEST_RUNTIME_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated runtime-role fixture not configured")
	}
	target, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(target.Path, "_test") {
		t.Fatal("dedicated test database required")
	}
	ctx := context.Background()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	post := blog.Post{ID: "runtime-role-fixture", Title: "権限確認", Markdown: "content", UpdatedAt: now, Version: 1}
	if err = store.Create(ctx, post); err != nil {
		t.Fatal("runtime insert failed", err)
	}
	if _, err = store.Publish(ctx, post.ID, 1, now); err != nil {
		t.Fatal("runtime publish failed", err)
	}
	snapshot, err := store.ExportSnapshot(ctx)
	if err != nil || snapshot.Generation < 2 {
		t.Fatal("runtime cannot export or trigger cannot track changes", err)
	}
	if _, err = store.db.ExecContext(ctx, "CREATE TABLE runtime_must_not_create(id integer)"); err == nil {
		t.Fatal("runtime user can change schema")
	}
}
