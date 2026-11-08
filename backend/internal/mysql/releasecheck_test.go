package mysql

import (
	"context"
	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blog"
	"testing"
	"time"
)

func TestReleaseCleanupKeepsUnrelatedArticles(t *testing.T) {
	s := isolatedStore(t)
	ctx := context.Background()
	proof, _ := auth.Token()
	other, _ := auth.Token()
	if err := s.Create(ctx, blog.Post{ID: other, Title: "owner article", Markdown: "keep me", Version: 1, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	id, _ := auth.Token()
	p := blog.Post{ID: id, Title: "CI production check " + proof[:12], Markdown: "<!-- course-k-release:" + proof + " -->\nfixture", Version: 1, UpdatedAt: time.Now()}
	if _, err := s.CreateOnce(ctx, proof, auth.Digest("fixture"), p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanupReleaseCheck(ctx, proof); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAdmin(ctx, id); err == nil {
		t.Fatal("fixture remains")
	}
	if p, err := s.GetAdmin(ctx, other); err != nil || p.Markdown != "keep me" {
		t.Fatal("owner article changed")
	}
	if _, err := s.CleanupReleaseCheck(ctx, proof); err != nil {
		t.Fatal("cleanup retry must be safe", err)
	}
}
func TestReleaseCleanupRefusesOwnerDataAndSchemaVerificationChecksChecksums(t *testing.T) {
	s := isolatedStore(t)
	ctx := context.Background()
	proof, _ := auth.Token()
	id, _ := auth.Token()
	if _, err := s.CreateOnce(ctx, proof, auth.Digest("owner"), blog.Post{ID: id, Title: "owner", Markdown: "private", Version: 1, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanupReleaseCheck(ctx, proof); err == nil {
		t.Fatal("owner deletion allowed")
	}
	if _, err := s.GetAdmin(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifySchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE schema_migrations SET checksum=REPEAT('0',64) WHERE version=1"); err != nil {
		t.Fatal(err)
	}
	if s.VerifySchema(ctx) == nil {
		t.Fatal("wrong applied checksum accepted")
	}
}
