package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateObjectsAreImmutableAndKeysCannotEscape(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	storage := Files{Directory: directory}
	key := "images/" + strings.Repeat("a", 43) + ".png"
	if err := storage.Put(ctx, key, []byte("original"), "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := storage.Put(ctx, key, []byte("replacement"), "image/png"); err == nil {
		t.Fatal("immutable object overwritten")
	}
	object, err := storage.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(object)
	object.Close()
	if err != nil || string(body) != "original" {
		t.Fatal("original content lost", err)
	}
	info, err := os.Stat(filepath.Join(directory, filepath.FromSlash(key)))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private file permissions", err)
	}
	for _, bad := range []string{"../escape", "/tmp/escape", "images/../../escape", "images/short.png", "images/" + strings.Repeat("a", 43) + ".svg"} {
		if storage.Put(ctx, bad, nil, "") == nil {
			t.Fatalf("invalid write accepted: %s", bad)
		}
		if _, err := storage.Get(ctx, bad); err == nil {
			t.Fatalf("invalid read accepted: %s", bad)
		}
		if storage.Delete(ctx, bad) == nil {
			t.Fatalf("invalid deletion accepted: %s", bad)
		}
	}
	if err := storage.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing object contract", err)
	}
	if err := storage.Delete(ctx, key); err != nil {
		t.Fatal("missing deletion must be idempotent", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if !errors.Is(storage.Put(canceled, key, []byte("ignored"), ""), context.Canceled) {
		t.Fatal("canceled write continued")
	}
}
