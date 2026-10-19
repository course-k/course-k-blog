package mysql

import (
	"context"
	"coursek/blog/backend/internal/external"
	"testing"
	"time"
)

func TestHistoryImportPreservesExistingRowsAndRejectsInvalidBatch(t *testing.T) {
	s := isolatedStore(t)
	ctx := context.Background()
	p := external.Post{Source: "wantedly", ID: "1", Title: "original", URL: "https://www.wantedly.com/companies/example/post_articles/1", PublishedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	if n, err := s.ImportMissingExternal(ctx, []external.Post{p}); err != nil || n != 1 {
		t.Fatalf("first import: %d %v", n, err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE external_posts SET title_override='owner title',hidden=TRUE,version=7 WHERE source='wantedly' AND source_id='1'"); err != nil {
		t.Fatal(err)
	}
	p.Title = "changed upstream"
	if n, err := s.ImportMissingExternal(ctx, []external.Post{p, p}); err != nil || n != 0 {
		t.Fatalf("repeat import: %d %v", n, err)
	}
	var title, override string
	var hidden bool
	var version int
	if err := s.db.QueryRowContext(ctx, "SELECT title,title_override,hidden,version FROM external_posts WHERE source='wantedly' AND source_id='1'").Scan(&title, &override, &hidden, &version); err != nil {
		t.Fatal(err)
	}
	if title != "original" || override != "owner title" || !hidden || version != 7 {
		t.Fatal("existing metadata or owner correction changed")
	}
	p.ID = "2"
	invalid := p
	invalid.URL = "http://untrusted.example"
	if _, err := s.ImportMissingExternal(ctx, []external.Post{p, invalid}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM external_posts").Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial batch persisted: %d %v", count, err)
	}
}
