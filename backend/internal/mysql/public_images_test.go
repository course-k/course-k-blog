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

func TestPublicImagesFollowPublication(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated MySQL not configured")
	}
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
	published, draftOnly := strings.Repeat("p", 43), strings.Repeat("d", 43)
	for _, id := range []string{published, draftOnly} {
		if err = store.SaveImage(ctx, images.Image{ID: id, Key: "images/" + id + ".png", MIME: "image/png", Size: 1, SHA256: "fixture", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	defer store.db.ExecContext(ctx, "DELETE FROM images WHERE id=? OR id=?", published, draftOnly)
	post := blog.Post{ID: "public-images-integration", Title: "public images", Markdown: "![shown](/api/images/" + published + ")", Version: 1, UpdatedAt: time.Now()}
	if err = store.Create(ctx, post); err != nil {
		t.Fatal(err)
	}
	defer store.db.ExecContext(ctx, "DELETE FROM posts WHERE id=?", post.ID)
	listed := func() map[string]images.Image {
		t.Helper()
		found, err := store.PublicImages(ctx)
		if err != nil {
			t.Fatal(err)
		}
		byID := map[string]images.Image{}
		for _, image := range found {
			byID[image.ID] = image
		}
		return byID
	}
	if _, public := listed()[published]; public {
		t.Fatal("an image in a draft must not be public")
	}
	current, err := store.Publish(ctx, post.ID, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// After publication the owner swaps the image in the working copy only.
	current, err = store.Save(ctx, post.ID, post.Title, "![next](/api/images/"+draftOnly+")", current.Version, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	found := listed()
	if image, public := found[published]; !public || image.Key != "images/"+published+".png" || image.MIME != "image/png" {
		t.Fatalf("the published image must be listed with its key and type: %+v", image)
	}
	if _, public := found[draftOnly]; public {
		t.Fatal("an image only in the unpublished edit must not be public")
	}
	if _, err = store.Unpublish(ctx, post.ID, current.Version, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, public := listed()[published]; public {
		t.Fatal("unpublishing the post must hide its image")
	}
}
