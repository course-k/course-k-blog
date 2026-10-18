package mysql

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/external"
)

func TestExternalSyncPersistenceAndTimeline(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated MySQL not configured")
	}
	target, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(target.Path, "_test") {
		t.Fatal("dedicated _test database required")
	}
	ctx := context.Background()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Only this disposable test DB is modified. Other sources are deferred to isolate leases.
	if _, err = store.db.ExecContext(ctx, "UPDATE sync_sources SET next_run=now()+INTERVAL 1 DAY,lease_until=NULL,run_id=NULL,last_attempt=NULL"); err != nil {
		t.Fatal(err)
	}
	defer store.db.ExecContext(ctx, "DELETE FROM external_posts WHERE source_id LIKE 'integration-%'")
	queue := func(id string) string {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, "UPDATE sync_sources SET last_attempt=NULL WHERE source='qiita'"); err != nil {
			t.Fatal(err)
		}
		if err := store.RequestSync(ctx, "qiita"); err != nil {
			t.Fatal(err)
		}
		source, err := store.ClaimSync(ctx, id)
		if err != nil || source != "qiita" {
			t.Fatalf("claim %q %v", source, err)
		}
		return source
	}
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	post := external.Post{Source: "qiita", ID: "integration-external", Title: "original", URL: "https://qiita.com/course_k/items/integration-external", PublishedAt: stamp}
	source := queue("first-run")
	if next, err := store.ClaimSync(ctx, "other-run"); err != nil || next != "" {
		t.Fatal("lease claimed twice", err)
	}
	if err := store.FinishSync(ctx, source, "obsolete-run", external.Batch{Posts: []external.Post{post}, Coverage: "all"}, ""); !errors.Is(err, external.ErrBusy) {
		t.Fatal("stale owner accepted", err)
	}
	if err := store.FinishSync(ctx, source, "first-run", external.Batch{Posts: []external.Post{post}, Coverage: "all"}, ""); err != nil {
		t.Fatal(err)
	}
	corrected := "manual correction"
	posts, err := store.ListExternal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var version int64
	for _, p := range posts {
		if p.ID == post.ID {
			version = p.Version
		}
	}
	correction := external.Correction{Title: &corrected, Hidden: true, Version: version}
	if err := store.CorrectExternal(ctx, source, post.ID, correction); err != nil {
		t.Fatal(err)
	}
	if err := store.CorrectExternal(ctx, source, post.ID, correction); !errors.Is(err, blog.ErrConflict) {
		t.Fatal("stale correction accepted", err)
	}
	queue("second-run")
	post.Title = "upstream changed"
	if err := store.FinishSync(ctx, source, "second-run", external.Batch{Posts: []external.Post{post}, Coverage: "all"}, ""); err != nil {
		t.Fatal(err)
	}
	posts, err = store.ListExternal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, p := range posts {
		if p.ID == post.ID {
			count++
			if !p.Hidden || p.TitleOverride == nil || *p.TitleOverride != corrected {
				t.Fatal("manual setting lost")
			}
			version = p.Version
		}
	}
	if count != 1 {
		t.Fatal("duplicate sync record", count)
	}
	queue("failed-run")
	if err := store.FinishSync(ctx, source, "failed-run", external.Batch{}, "upstream_429"); err != nil {
		t.Fatal(err)
	}
	posts, err = store.ListExternal(ctx)
	if err != nil || len(posts) == 0 {
		t.Fatal("failure erased records", err)
	}
	if err := store.CorrectExternal(ctx, source, post.ID, external.Correction{Title: &corrected, Version: version}); err != nil {
		t.Fatal(err)
	}
	articles, err := store.ListArticles(ctx, 21, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, article := range articles {
		if article.ID == "qiita:"+post.ID {
			found = article.Title == corrected
		}
	}
	if !found {
		t.Fatal("corrected external article not in timeline")
	}
}

func TestLimitedSyncKeepsGapUntilFullCoverageAndDoesNotConflictOnUnchangedMetadata(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, "UPDATE sync_sources SET next_run=now()+INTERVAL 1 DAY"); err != nil {
		t.Fatal(err)
	}
	finish := func(run string, posts []external.Post, coverage string) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, "UPDATE sync_sources SET next_run=now(),last_attempt=NULL WHERE source='wantedly'"); err != nil {
			t.Fatal(err)
		}
		source, err := store.ClaimSync(ctx, run)
		if err != nil || source != "wantedly" {
			t.Fatal("claim failed", source, err)
		}
		if err := store.FinishSync(ctx, source, run, external.Batch{Posts: posts, Coverage: coverage}, ""); err != nil {
			t.Fatal(err)
		}
	}
	first := external.Post{Source: "wantedly", ID: "1", Title: "old", URL: "https://www.wantedly.com/companies/example/post_articles/1", PublishedAt: time.Now().Add(-time.Hour)}
	second := external.Post{Source: "wantedly", ID: "2", Title: "recent", URL: "https://www.wantedly.com/companies/example/post_articles/2", PublishedAt: time.Now()}
	finish("first", []external.Post{first}, "recent")
	finish("gap", []external.Post{second}, "recent")
	finish("still-gap", []external.Post{second}, "recent")
	states, err := store.SyncStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.Source == "wantedly" && (state.Coverage != "gap" || state.Status != "partial") {
			t.Fatal("limited sync forgot missing window")
		}
	}
	posts, err := store.ListExternal(ctx)
	if err != nil || len(posts) != 2 {
		t.Fatal("limited sync removed old article", err)
	}
	for _, post := range posts {
		if post.Version != 1 {
			t.Fatal("unchanged upstream metadata caused correction conflict")
		}
	}
	finish("complete", []external.Post{first, second}, "all")
	states, err = store.SyncStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.Source == "wantedly" && (state.Coverage != "all" || state.Status != "success") {
			t.Fatal("complete sync did not resolve coverage")
		}
	}
}
