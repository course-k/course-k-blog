package mysql

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/backup"
	"coursek/blog/backend/internal/blob"
	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/images"
)

type failedArchive struct{ images.Blobs }

func (f failedArchive) Put(context.Context, string, []byte, string) error {
	return errors.New("simulated unavailable storage")
}
func TestBackupRestoreImagesAndFailureRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	source, target := isolatedStore(t), isolatedStore(t)
	media := blob.Files{Directory: t.TempDir()}
	archives := blob.Files{Directory: t.TempDir()}
	restoredMedia := blob.Files{Directory: t.TempDir()}
	imageID := strings.Repeat("k", 43)
	imageKey := "images/" + imageID + ".png"
	body := []byte("immutable fixture image bytes")
	sum := sha256.Sum256(body)
	if err := media.Put(ctx, imageKey, body, "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := source.SaveImage(ctx, images.Image{ID: imageID, Key: imageKey, MIME: "image/png", Size: len(body), SHA256: hex.EncodeToString(sum[:]), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	post := blog.Post{ID: "backup-post", Title: "restore me", Markdown: "![fixture](/api/images/" + imageID + ")", Version: 1, UpdatedAt: time.Now()}
	creationKey, err := auth.Token()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.CreateOnce(ctx, creationKey, auth.Digest("backup fixture"), post); err != nil {
		t.Fatal(err)
	}
	post, err = source.Publish(ctx, post.ID, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	service := backup.Service{Repository: source, Media: media, Archives: failedArchive{archives}, Retain: 4}
	if err := service.Take(ctx, true); err == nil {
		t.Fatal("failed archive accepted")
	}
	state, err := source.BackupState(ctx)
	if err != nil || state.LastError == "" || state.BackedUpGeneration >= state.Generation {
		t.Fatal("failure not retained", err)
	}
	service.Archives = archives
	if err = service.Take(ctx, true); err != nil {
		t.Fatal(err)
	}
	records, err := source.ListBackups(ctx)
	if err != nil || len(records) != 1 {
		t.Fatal("backup missing", err)
	}
	first := records[0]
	if err = service.Take(ctx, false); err != nil {
		t.Fatal(err)
	}
	records, err = source.ListBackups(ctx)
	if err != nil || len(records) != 1 {
		t.Fatal("unchanged data created another backup", err)
	}
	if err = target.PutSession(ctx, blog.Session{Hash: "old-target-session", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	recovery := backup.Service{Repository: target, Media: restoredMedia, Archives: archives, Retain: 4}
	if err = recovery.Restore(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := target.GetPublic(ctx, post.ID)
	if err != nil || restored.Markdown != post.Markdown || restored.Title != post.Title {
		t.Fatal("restored article differs", err)
	}
	if recovered, err := target.FindCreation(ctx, creationKey); err != nil || recovered.ID != post.ID {
		t.Fatal("creation retry mapping not restored", err)
	}
	visible, err := target.ImagePublic(ctx, imageID)
	if err != nil || !visible {
		t.Fatal("restored image reference differs", err)
	}
	file, err := restoredMedia.Get(ctx, imageKey)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(file)
	file.Close()
	if err != nil || !bytes.Equal(content, body) {
		t.Fatal("image data not restored", err)
	}
	if valid, err := target.HasSession(ctx, "old-target-session", time.Now()); err != nil || valid {
		t.Fatal("old session survived restore", err)
	}
	if err = recovery.Restore(ctx, first.ID); err == nil {
		t.Fatal("nonempty restore destination accepted")
	}
	// More than four real content changes retain only the configured number of archives.
	for i := 0; i < 5; i++ {
		post, err = source.Save(ctx, post.ID, post.Title, post.Markdown+"\nchange", post.Version, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err = service.Take(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	records, err = source.ListBackups(ctx)
	if err != nil || len(records) != 4 {
		t.Fatal("retention not applied", len(records), err)
	}
	if _, err = archives.Get(ctx, first.Key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatal("expired archive not removed", err)
	}
}

func TestRepresentativeBlogBackupAndRestore(t *testing.T) {
	source, target := isolatedStore(t), isolatedStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// 200 public articles with separate 50 KiB working/public snapshots, not an empty DB.
	for i := 0; i < 200; i++ {
		post := blog.Post{ID: fmt.Sprintf("capacity-%d", i), Title: fmt.Sprintf("記事 %d", i), Markdown: strings.Repeat("<>&", 50*1024/3) + "xx", UpdatedAt: time.Now().UTC(), Version: 1}
		if err := source.Create(ctx, post); err != nil {
			t.Fatal(err)
		}
		if _, err := source.Publish(ctx, post.ID, 1, post.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}

	service := backup.Service{Repository: source, Media: blob.Files{Directory: t.TempDir()}, Archives: blob.Files{Directory: t.TempDir()}, Retain: 4}
	started := time.Now()
	if err := service.Take(ctx, true); err != nil {
		t.Fatal(err)
	}
	records, err := source.ListBackups(ctx)
	if err != nil || len(records) != 1 {
		t.Fatal("representative backup missing", err)
	}
	restore := backup.Service{Repository: target, Media: blob.Files{Directory: t.TempDir()}, Archives: service.Archives}
	if err := restore.Restore(ctx, records[0].ID); err != nil {
		t.Fatal(err)
	}
	var count int
	var contentBytes int64
	if err := target.db.QueryRowContext(ctx, "SELECT count(*),sum(octet_length(markdown)+octet_length(published_markdown)) FROM posts WHERE published").Scan(&count, &contentBytes); err != nil {
		t.Fatal(err)
	}
	if count != 200 || contentBytes != 200*2*50*1024 {
		t.Fatal("representative content not restored", count, contentBytes)
	}
	t.Logf("200 articles, %.1f MiB of working/public text, backup+restore %s; repetitive fixture does not measure production compression or AWS latency", float64(contentBytes)/(1024*1024), time.Since(started).Round(time.Millisecond))
}
