package mysql

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/images"
)

func TestImageReferencesFollowPublishedSnapshot(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated MySQL not configured")
	}
	// Open uses the same dedicated-DB guard as the persistence suite.
	if !strings.Contains(dsn, "_test?") {
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
	ids := []string{strings.Repeat("i", 43), strings.Repeat("j", 43)}
	for _, id := range ids {
		if err = store.SaveImage(ctx, images.Image{ID: id, Key: "images/" + id + ".png", MIME: "image/png", Size: 1, SHA256: "fixture", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	defer store.db.ExecContext(ctx, "DELETE FROM images WHERE id=? OR id=?", ids[0], ids[1])
	post := blog.Post{ID: "image-integration", Title: "image test", Markdown: "![first](/api/images/" + ids[0] + ")", Version: 1, UpdatedAt: time.Now()}
	if err = store.Create(ctx, post); err != nil {
		t.Fatal(err)
	}
	defer store.db.ExecContext(ctx, "DELETE FROM posts WHERE id=?", post.ID)
	visible := func(id string, want bool) {
		t.Helper()
		got, err := store.ImagePublic(ctx, id)
		if err != nil || got != want {
			t.Fatalf("image=%s public=%v want=%v err=%v", id, got, want, err)
		}
	}
	visible(ids[0], false)
	post, err = store.Publish(ctx, post.ID, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	visible(ids[0], true)
	post, err = store.Save(ctx, post.ID, post.Title, "![new](/api/images/"+ids[1]+")", post.Version, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	visible(ids[0], true)
	visible(ids[1], false)
	post, err = store.Publish(ctx, post.ID, post.Version, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	visible(ids[0], false)
	visible(ids[1], true)
	post, err = store.Unpublish(ctx, post.ID, post.Version, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	visible(ids[1], false)
	before := post.Version
	if _, err = store.Save(ctx, post.ID, post.Title, "![missing](/api/images/"+strings.Repeat("z", 43)+")", before, time.Now()); err == nil {
		t.Fatal("missing image accepted")
	}
	current, err := store.GetAdmin(ctx, post.ID)
	if err != nil || current.Version != before || current.Markdown != post.Markdown {
		t.Fatal("failed image save did not roll back article", err)
	}
}
