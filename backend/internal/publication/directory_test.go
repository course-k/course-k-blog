package publication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryReplacesReadsAndDeletesFiles(t *testing.T) {
	ctx := context.Background()
	target := Directory{Path: t.TempDir()}
	key := "data/articles/all/1.json"
	if _, err := target.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing file must be reported as not found: %v", err)
	}
	for _, body := range []string{"first", "second"} {
		if err := target.Put(ctx, key, []byte(body), "application/json"); err != nil {
			t.Fatal(err)
		}
	}
	if body, err := target.Get(ctx, key); err != nil || string(body) != "second" {
		t.Fatalf("the later write must replace the earlier one: %q %v", body, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(target.Path, "data/articles/all/.writing-*")); len(leftovers) != 0 {
		t.Fatalf("temporary files must not remain: %v", leftovers)
	}
	if err := target.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := target.Delete(ctx, key); err != nil {
		t.Fatalf("deleting a file that is already gone must succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target.Path, key)); !os.IsNotExist(err) {
		t.Fatal("the deleted file must be gone")
	}
}

func TestDirectoryRejectsKeysOutsideThePublicLayout(t *testing.T) {
	target := Directory{Path: t.TempDir()}
	for _, key := range []string{"../secret", "data/../../secret.json", "/etc/passwd", "data/posts/../x.json", "backups/a.dump", "api/images/a/b", "data/articles/2026-13/1.json"} {
		if err := target.Put(context.Background(), key, []byte("x"), ""); err == nil {
			t.Fatalf("%q must be rejected", key)
		}
	}
}

func TestRefreshProducesOnlyKeysTheDirectoryAccepts(t *testing.T) {
	source, media, _ := fixture()
	target := Directory{Path: t.TempDir()}
	if err := (&Publisher{Source: source, Media: media, Target: target}).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(target.Path, "api/images/img1")); err != nil || string(body) != "png-bytes" {
		t.Fatalf("the image must be written to disk: %q %v", body, err)
	}
}
