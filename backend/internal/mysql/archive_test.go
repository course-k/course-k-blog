package mysql

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestMonthlyArchiveUsesVisibleCorrectedDatesInTokyoAndPaginates(t *testing.T) {
	s := isolatedStore(t)
	ctx := context.Background()
	// January UTC becomes February in Tokyo. One earlier timestamp stays January.
	stamps := []string{"2026-01-31 14:59:59", "2026-01-31 15:00:00", "2026-02-28 14:59:59", "2026-02-28 15:00:00"}
	for i, stamp := range stamps {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO posts(id,title,markdown,published_title,published,published_at,public_updated_at,updated_at) VALUES(?, 'working title','draft','public title',true,?,?,NOW())`, fmt.Sprint("own-", i), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO posts(id,title,markdown,updated_at) VALUES('draft','not public','draft',NOW())`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 22; i++ {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO external_posts(source,source_id,title,url,published_at,date_override,hidden) VALUES('qiita',?,'external','https://qiita.com/course_k/items/test','2025-12-01',?,?)`, fmt.Sprint(i), "2026-02-01 00:00:00", i == 21); err != nil {
			t.Fatal(err)
		}
	}
	months, err := s.ArticleMonths(ctx)
	if err != nil || len(months) != 3 || months[0].Month != "2026-03" || months[0].Count != 1 || months[1].Month != "2026-02" || months[1].Count != 23 || months[2].Count != 1 {
		t.Fatalf("archive: %+v %v", months, err)
	}
	first, err := s.ListArticles(ctx, 21, 0, "2026-02")
	if err != nil || len(first) != 21 {
		t.Fatalf("first page: %d %v", len(first), err)
	}
	second, err := s.ListArticles(ctx, 21, 20, "2026-02")
	if err != nil || len(second) != 3 {
		t.Fatalf("second page: %d %v", len(second), err)
	}
	jst := time.FixedZone("Asia/Tokyo", 9*3600)
	for _, a := range append(first, second...) {
		if a.PublishedAt.In(jst).Format("2006-01") != "2026-02" || a.ID == "qiita:21" {
			t.Fatalf("wrong month or hidden record: %+v", a)
		}
		if a.Kind == "own" && a.Title != "public title" {
			t.Fatal("draft title leaked")
		}
	}
	empty, err := s.ListArticles(ctx, 21, 0, "2024-01")
	if err != nil || len(empty) != 0 {
		t.Fatal("absent month", empty, err)
	}
}
