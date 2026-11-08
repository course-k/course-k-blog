package mysql

import (
	"context"
	"errors"
	"testing"
	"time"

	"coursek/blog/backend/internal/external"
)

func syncState(t *testing.T, store *Store, source string) external.SyncState {
	t.Helper()
	states, err := store.SyncStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.Source == source {
			return state
		}
	}
	t.Fatal("missing source", source)
	return external.SyncState{}
}

func TestSyncRunsOnlyOnExplicitRequest(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	states, err := store.SyncStates(ctx)
	if err != nil || len(states) != 3 || states[0].Source != "qiita" || states[1].Source != "wantedly" || states[2].Source != "note" {
		t.Fatalf("display order %+v %v", states, err)
	}
	for _, state := range states {
		if state.Status != "idle" || state.Requested {
			t.Fatalf("initial state %+v", state)
		}
	}
	// Initial start, the next day and one hour after a failure: next_run alone never starts work.
	for _, shift := range []string{"NOW(6)", "NOW(6)-INTERVAL 1 DAY", "NOW(6)-INTERVAL 1 HOUR"} {
		if _, err := store.db.ExecContext(ctx, "UPDATE sync_sources SET next_run="+shift+",last_attempt=NULL,lease_until=NULL"); err != nil {
			t.Fatal(err)
		}
		if source, err := store.ClaimSync(ctx, "scheduler"); err != nil || source != "" {
			t.Fatalf("scheduled claim %q %v", source, err)
		}
	}
	if err := store.RequestSync(ctx, "qiita"); err != nil {
		t.Fatal(err)
	}
	if state := syncState(t, store, "qiita"); state.Status != "waiting" || !state.Requested {
		t.Fatalf("requested state %+v", state)
	}
	if !errors.Is(store.RequestSync(ctx, "qiita"), external.ErrRunning) {
		t.Fatal("duplicate request accepted")
	}
	if syncState(t, store, "note").Status != "idle" {
		t.Fatal("request affected another source")
	}
	source, err := store.ClaimSync(ctx, "first-run")
	if err != nil || source != "qiita" {
		t.Fatalf("claim %q %v", source, err)
	}
	if next, err := store.ClaimSync(ctx, "other-run"); err != nil || next != "" {
		t.Fatal("lease claimed twice", err)
	}
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	post := external.Post{Source: "qiita", ID: "integration-external", Title: "original", URL: "https://qiita.com/course_k/items/integration-external", PublishedAt: stamp}
	if err := store.FinishSync(ctx, source, "obsolete-run", external.Batch{Posts: []external.Post{post}, Coverage: "all"}, ""); !errors.Is(err, external.ErrBusy) {
		t.Fatal("stale owner accepted", err)
	}
	if err := store.FinishSync(ctx, source, "first-run", external.Batch{Posts: []external.Post{post}, Coverage: "all"}, ""); err != nil {
		t.Fatal(err)
	}
	state := syncState(t, store, "qiita")
	if state.Status != "success" || state.Requested || state.Count != 1 || state.NextAllowedAt == nil {
		t.Fatalf("finished state %+v", state)
	}
	var cooldown external.CooldownError
	if err := store.RequestSync(ctx, "qiita"); !errors.As(err, &cooldown) || cooldown.RetryAfter <= 9*time.Minute || cooldown.RetryAfter > 10*time.Minute {
		t.Fatalf("10-minute limit: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE sync_sources SET next_run=NOW(6)-INTERVAL 2 DAY,last_attempt=NOW(6)-INTERVAL 11 MINUTE WHERE source='qiita'"); err != nil {
		t.Fatal(err)
	}
	if source, err := store.ClaimSync(ctx, "after-success"); err != nil || source != "" {
		t.Fatal("success scheduled another run", source, err)
	}
	// An explicit request interrupted by a process stop resumes after its lease expires.
	if err := store.RequestSync(ctx, "qiita"); err != nil {
		t.Fatal(err)
	}
	if source, err := store.ClaimSync(ctx, "crashed-run"); err != nil || source != "qiita" {
		t.Fatal(source, err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE sync_sources SET lease_until=NOW(6)-INTERVAL 1 SECOND WHERE source='qiita'"); err != nil {
		t.Fatal(err)
	}
	if source, err := store.ClaimSync(ctx, "recovered-run"); err != nil || source != "qiita" {
		t.Fatal("interrupted request not resumed", source, err)
	}
	if err := store.FinishSync(ctx, "qiita", "recovered-run", external.Batch{}, "upstream_429"); err != nil {
		t.Fatal(err)
	}
	state = syncState(t, store, "qiita")
	if state.Status != "failed" || state.Requested || state.ErrorCode != "upstream_429" {
		t.Fatalf("failed state %+v", state)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE sync_sources SET next_run=NOW(6)-INTERVAL 2 HOUR,last_attempt=NOW(6)-INTERVAL 2 HOUR WHERE source='qiita'"); err != nil {
		t.Fatal(err)
	}
	if source, err := store.ClaimSync(ctx, "retry-after-failure"); err != nil || source != "" {
		t.Fatal("failure scheduled a retry", source, err)
	}
	posts, err := store.ListExternal(ctx)
	if err != nil || len(posts) != 1 {
		t.Fatal("failure erased records", posts, err)
	}
}

func TestLegacyScheduledStatesBecomeIdle(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, "UPDATE sync_sources SET status='running',lease_until=NOW(6)-INTERVAL 1 MINUTE,requested=false WHERE source='note'"); err != nil {
		t.Fatal(err)
	}
	if state := syncState(t, store, "note"); state.Status != "idle" {
		t.Fatalf("stale scheduled run shown as %q", state.Status)
	}
	if source, err := store.ClaimSync(ctx, "legacy"); err != nil || source != "" {
		t.Fatal("legacy scheduled run resumed", source, err)
	}
	if err := store.RequestSync(ctx, "note"); err != nil {
		t.Fatal("owner cannot request after legacy run", err)
	}
}

func TestLimitedSyncKeepsGapUntilFullCoverageAndDoesNotConflictOnUnchangedMetadata(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	finish := func(run string, posts []external.Post, coverage string) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, "UPDATE sync_sources SET last_attempt=NULL WHERE source='wantedly'"); err != nil {
			t.Fatal(err)
		}
		if err := store.RequestSync(ctx, "wantedly"); err != nil {
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
	if state := syncState(t, store, "wantedly"); state.Coverage != "gap" || state.Status != "partial" || state.Count != 1 {
		t.Fatal("limited sync forgot missing window", state)
	}
	posts, err := store.ListExternal(ctx)
	if err != nil || len(posts) != 2 {
		t.Fatal("limited sync removed old article", err)
	}
	for _, post := range posts {
		if post.Version != 1 {
			t.Fatal("unchanged upstream metadata changed the row version")
		}
	}
	finish("complete", []external.Post{first, second}, "all")
	if state := syncState(t, store, "wantedly"); state.Coverage != "all" || state.Status != "success" {
		t.Fatal("complete sync did not resolve coverage", state)
	}
}
