package mysql

import (
	"context"
	"errors"
	"testing"
	"time"

	"coursek/blog/backend/internal/backup"
	"coursek/blog/backend/internal/blog"
)

func TestBackupScheduleTracksContentRatherThanSaveAttempts(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	post := blog.Post{ID: "schedule", Title: "下書き", Markdown: "body", UpdatedAt: now, Version: 1}
	if err := store.Create(ctx, post); err != nil {
		t.Fatal(err)
	}
	draft, err := store.BackupState(ctx)
	if err != nil || draft.Generation != 1 || draft.DueAt == nil || !draft.DueAt.After(now) {
		t.Fatal("draft must schedule tonight", draft, err)
	}
	post, err = store.Save(ctx, post.ID, post.Title, post.Markdown, 1, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := store.BackupState(ctx)
	if err != nil || unchanged.Generation != draft.Generation {
		t.Fatal("identical save marked content changed", err)
	}
	if _, err = store.Publish(ctx, post.ID, post.Version, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	published, err := store.BackupState(ctx)
	if err != nil || published.Generation != draft.Generation+1 || published.DueAt == nil || published.DueAt.After(time.Now().UTC()) {
		t.Fatal("publication must schedule immediately", published, err)
	}
	target := isolatedStore(t)
	snapshot, err := store.ExportSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.DatabaseEngine = "postgres"
	if err = target.ValidateRestore(ctx, snapshot); !errors.Is(err, backup.ErrInvalid) {
		t.Fatal("different database engine accepted", err)
	}
}
