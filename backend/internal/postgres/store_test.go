package postgres

import (
	"context"
	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blog"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in real PostgreSQL test. Never silently replace PostgreSQL with a fake.
func TestPostgreSQLPersistenceAndPublishedSnapshot(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set: PostgreSQL integration not executed")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(u.Path, "_test") {
		t.Fatal("integration test requires a dedicated database ending in _test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("migration is not repeatable", err)
	}
	id, _ := auth.Token()
	id = "integration-" + id
	defer s.db.ExecContext(context.Background(), "DELETE FROM posts WHERE id=$1", id)
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := blog.Post{ID: id, Title: "公開前", Markdown: "draft secret", UpdatedAt: now, Version: 1}
	if err = s.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetPublic(ctx, id); !errors.Is(err, blog.ErrNotFound) {
		t.Fatal("draft visible", err)
	}
	p, err = s.Publish(ctx, id, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	first := *p.PublishedAt
	p, err = s.Save(ctx, id, "編集中", "new secret", p.Version, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	visible, err := s.GetPublic(ctx, id)
	if err != nil || visible.Title != "公開前" || visible.Markdown != "draft secret" || !visible.UpdatedAt.Equal(now) {
		t.Fatal("draft overwrote published snapshot", visible, err)
	}
	if _, err = s.Save(ctx, id, "stale", "wrong", 1, now); !errors.Is(err, blog.ErrConflict) {
		t.Fatal("stale write accepted", err)
	}
	p, err = s.Publish(ctx, id, p.Version, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(*p.PublishedAt) {
		t.Fatal("first publication date changed")
	}
	second, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := second.GetPublic(ctx, id)
	second.Close()
	if err != nil || persisted.Markdown != "new secret" {
		t.Fatal("data did not persist across connections", err)
	}
	p, err = s.Unpublish(ctx, id, p.Version, now.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetPublic(ctx, id); !errors.Is(err, blog.ErrNotFound) {
		t.Fatal("unpublished article visible", err)
	}
	p, err = s.Publish(ctx, id, p.Version, now.Add(4*time.Hour))
	if err != nil || !first.Equal(*p.PublishedAt) {
		t.Fatal("republish changed date", err)
	}
	token, _ := auth.Token()
	hash := auth.Digest(token)
	defer s.DeleteSession(context.Background(), hash)
	if err = s.PutSession(ctx, blog.Session{Hash: hash, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.HasSession(ctx, hash, now); err != nil || !ok {
		t.Fatal("session not persisted", err)
	}
	if ok, err := s.HasSession(ctx, hash, now.Add(2*time.Hour)); err != nil || ok {
		t.Fatal("expired session accepted", err)
	}
	if err = s.DeleteSession(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.HasSession(ctx, hash, now); err != nil || ok {
		t.Fatal("revoked session accepted", err)
	}
}
