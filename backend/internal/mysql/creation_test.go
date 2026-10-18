package mysql

import (
	"context"
	"errors"
	"testing"
	"time"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blog"
)

func TestConcurrentCreationRetryKeepsOneArticle(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	key, err := auth.Token()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := auth.Digest("creation payload")
	type result struct {
		post blog.Post
		err  error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			id, err := auth.Token()
			if err != nil {
				results <- result{err: err}
				return
			}
			post, err := store.CreateOnce(ctx, key, fingerprint, blog.Post{ID: id, Title: "same input", Markdown: "body", UpdatedAt: time.Now(), Version: 1})
			results <- result{post, err}
		}()
	}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.post.ID != second.post.ID {
		t.Fatal("creation retry duplicated or failed", first.err, second.err)
	}
	rows, err := store.ListAdmin(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatal("ghost article persisted", len(rows), err)
	}
	found, err := store.FindCreation(ctx, key)
	if err != nil || found.ID != first.post.ID {
		t.Fatal("creation result unavailable", err)
	}
	if _, err = store.CreateOnce(ctx, key, auth.Digest("changed payload"), blog.Post{ID: "different", Title: "changed input", Markdown: "body", UpdatedAt: time.Now(), Version: 1}); !errors.Is(err, blog.ErrConflict) {
		t.Fatal("different payload reused creation key", err)
	}
	rows, err = store.ListAdmin(ctx)
	if err != nil || len(rows) != 1 || rows[0].Title != "same input" {
		t.Fatal("conflicting retry changed saved content", err)
	}
}
