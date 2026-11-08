package mysql

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"coursek/blog/backend/internal/backup"
	"coursek/blog/backend/internal/blob"
	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/images"
)

// emptyStore opens a fresh database without running migrations.
func emptyStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated MySQL not configured")
	}
	target, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(target.Path, "_test") {
		t.Fatal("dedicated _test database required")
	}
	owner, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	var suffix [8]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := "migration_test_" + hex.EncodeToString(suffix[:])
	if _, err = owner.db.ExecContext(context.Background(), "CREATE DATABASE "+schema+" CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin"); err != nil {
		t.Fatal(err)
	}
	target.Path = "/" + schema
	store, err := Open(context.Background(), target.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		owner.db.ExecContext(context.Background(), "DROP DATABASE "+schema)
		owner.Close()
	})
	return store
}

// schemaShape lists table definitions and CHECK constraints for comparison.
func schemaShape(t *testing.T, store *Store) string {
	t.Helper()
	ctx := context.Background()
	rows, err := store.db.QueryContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() ORDER BY TABLE_NAME")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	var shape []string
	for _, table := range tables {
		var name, definition string
		if err := store.db.QueryRowContext(ctx, "SHOW CREATE TABLE "+table).Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		shape = append(shape, definition)
	}
	checks, err := store.db.QueryContext(ctx, `SELECT tc.TABLE_NAME,cc.CONSTRAINT_NAME,cc.CHECK_CLAUSE FROM information_schema.CHECK_CONSTRAINTS cc
 JOIN information_schema.TABLE_CONSTRAINTS tc ON tc.CONSTRAINT_SCHEMA=cc.CONSTRAINT_SCHEMA AND tc.CONSTRAINT_NAME=cc.CONSTRAINT_NAME
 WHERE cc.CONSTRAINT_SCHEMA=DATABASE()`)
	if err != nil {
		t.Fatal(err)
	}
	for checks.Next() {
		var table, name, clause string
		if err := checks.Scan(&table, &name, &clause); err != nil {
			t.Fatal(err)
		}
		shape = append(shape, table+"."+name+":"+clause)
	}
	checks.Close()
	sort.Strings(shape)
	return strings.Join(shape, "\n")
}

func migrationStatements(t *testing.T, version string) []string {
	t.Helper()
	files, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), version+"_") {
			script, err := migrationFiles.ReadFile("migrations/" + file.Name())
			if err != nil {
				t.Fatal(err)
			}
			return strings.Split(string(script), "-- statement-break")
		}
	}
	t.Fatal("migration not found", version)
	return nil
}

func TestMigrationReachesSameSchemaFromNewAppliedAndInterruptedDatabases(t *testing.T) {
	ctx := context.Background()
	fresh := emptyStore(t)
	if err := fresh.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	want := schemaShape(t, fresh)
	if strings.Contains(want, "between 1 and 200") || strings.Contains(want, "5242880") {
		t.Fatal("legacy check survived on a new database")
	}

	// An existing production-like database: 001 applied, with data in place.
	existing := emptyStore(t)
	if err := existing.migrateTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := existing.db.ExecContext(ctx, `INSERT INTO posts(id,title,markdown,published_title,published_markdown,published,published_at,public_updated_at,updated_at)
 VALUES('kept','公開記事','本文','公開記事','本文',true,'2026-01-02','2026-01-02','2026-01-02')`); err != nil {
		t.Fatal(err)
	}
	if _, err := existing.db.ExecContext(ctx, `INSERT INTO images(id,object_key,mime,byte_size,sha256,created_at) VALUES(?,?,'image/png',5242880,?,NOW(6))`, strings.Repeat("i", 43), "images/"+strings.Repeat("i", 43)+".png", strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := existing.db.ExecContext(ctx, `INSERT INTO posts(id,title,markdown,updated_at) VALUES('untitled','','x',NOW(6))`); err == nil {
		t.Fatal("schema 1 accepted an empty title; the test does not reproduce the old schema")
	}
	if err := existing.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := schemaShape(t, existing); got != want {
		t.Fatalf("existing database schema differs:\n%s\n---\n%s", got, want)
	}
	var title string
	var publishedAt time.Time
	if err := existing.db.QueryRowContext(ctx, "SELECT published_title,published_at FROM posts WHERE id='kept'").Scan(&title, &publishedAt); err != nil || title != "公開記事" || publishedAt.Format("2006-01-02") != "2026-01-02" {
		t.Fatal("migration changed existing article", title, publishedAt, err)
	}
	for _, source := range []string{"qiita", "note", "wantedly"} {
		if state := syncState(t, existing, source); state.Status != "idle" || state.Requested {
			t.Fatalf("scheduled wait not migrated to idle: %+v", state)
		}
	}

	// Stop part-way through 002 (after dropping the title check and adding the
	// column) and rerun: implicit DDL commits must not block completion.
	interrupted := emptyStore(t)
	if err := interrupted.migrateTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	statements := migrationStatements(t, "002")
	conn, err := interrupted.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	executed := 0
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
		executed++
		if strings.Contains(statement, "ADD COLUMN requested") {
			break
		}
	}
	conn.Close()
	if executed < 5 || executed == len(statements) {
		t.Fatal("partial run did not stop mid-migration", executed)
	}
	if err := interrupted.Migrate(ctx); err != nil {
		t.Fatal("rerun after interruption:", err)
	}
	if got := schemaShape(t, interrupted); got != want {
		t.Fatalf("rerun schema differs:\n%s\n---\n%s", got, want)
	}
	// Rerunning a completed migration file is also harmless.
	conn, err = interrupted.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal("full rerun:", err)
		}
	}
	conn.Close()
	if got := schemaShape(t, interrupted); got != want {
		t.Fatal("second full run changed the schema")
	}
}

func TestUntitledDraftsAndLargerImagesAreStored(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	post := blog.Post{ID: "untitled", Title: "", Markdown: "本文だけ", UpdatedAt: now, Version: 1}
	if err := store.Create(ctx, post); err != nil {
		t.Fatal("empty title rejected", err)
	}
	saved, err := store.Save(ctx, post.ID, "", "", 1, now)
	if err != nil || saved.Title != "" || saved.Markdown != "" {
		t.Fatal("emptying an existing draft was not saved", saved, err)
	}
	if _, err := store.Save(ctx, post.ID, strings.Repeat("猫", 201), "", saved.Version, now); err == nil {
		t.Fatal("201 code points stored")
	}
	for size, ok := range map[int]bool{10_000_000: true, 10_000_001: false} {
		id := strings.Repeat(map[bool]string{true: "a", false: "b"}[ok], 43)
		err := store.SaveImage(ctx, images.Image{ID: id, Key: "images/" + id + ".png", MIME: "image/png", Size: size, SHA256: strings.Repeat("0", 64), CreatedAt: now})
		if (err == nil) != ok {
			t.Fatalf("image size %d accepted=%v", size, err == nil)
		}
	}
}

func TestAutosaveOnPublishedArticleWaitsForNightlyBackup(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	post := blog.Post{ID: "autosaved", Title: "公開", Markdown: "v1", UpdatedAt: now, Version: 1}
	if err := store.Create(ctx, post); err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(ctx, post.ID, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE operation_state SET backed_up_generation=generation,due_at=NULL"); err != nil {
		t.Fatal(err)
	}
	version := published.Version
	for i := 0; i < 5; i++ {
		saved, err := store.Save(ctx, post.ID, "公開", "autosave "+string(rune('a'+i)), version, now)
		if err != nil {
			t.Fatal(err)
		}
		version = saved.Version
		if saved.Published != true {
			t.Fatal("autosave changed publication")
		}
	}
	public, err := store.GetPublic(ctx, post.ID)
	if err != nil || public.Markdown != "v1" {
		t.Fatal("autosave changed the published body", public.Markdown, err)
	}
	state, err := store.BackupState(ctx)
	if err != nil || state.DueAt == nil || !state.DueAt.After(time.Now().UTC()) {
		t.Fatal("working-copy autosaves must not request an immediate full backup", state, err)
	}
}

func TestSchemaOneArchiveRestoresIntoCurrentSchema(t *testing.T) {
	ctx := context.Background()
	source, target := isolatedStore(t), isolatedStore(t)
	post := blog.Post{ID: "old-archive", Title: "旧バックアップ", Markdown: "本文", UpdatedAt: time.Now().UTC(), Version: 1}
	if err := source.Create(ctx, post); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.ExportSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != LatestSchemaVersion() {
		t.Fatal("snapshot schema", snapshot.SchemaVersion)
	}
	snapshot.SchemaVersion = 1
	if err := target.RestoreSnapshot(ctx, snapshot); err != nil {
		t.Fatal("schema 1 archive rejected", err)
	}
	restored, err := target.GetAdmin(ctx, post.ID)
	if err != nil || restored.Title != post.Title {
		t.Fatal(restored, err)
	}
	newer := snapshot
	newer.SchemaVersion = LatestSchemaVersion() + 1
	if err := isolatedStore(t).ValidateRestore(ctx, newer); !errors.Is(err, backup.ErrInvalid) {
		t.Fatal("archive from a newer schema accepted", err)
	}
}

func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

type crashAfterPut struct {
	images.Blobs
	crashed bool
}

func (c *crashAfterPut) Put(ctx context.Context, key string, body []byte, mime string) error {
	if err := c.Blobs.Put(ctx, key, body, mime); err != nil {
		return err
	}
	if !c.crashed && !strings.HasPrefix(key, "images/staging/") {
		c.crashed = true
		return errors.New("simulated process stop after object write")
	}
	return nil
}

func TestImageUploadCompletesOnceAcrossRetriesAndRestarts(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	media := blob.Files{Directory: t.TempDir()}
	crash := &crashAfterPut{Blobs: media}
	service := &images.UploadService{Repository: store, Blobs: crash, Staging: images.LocalStaging{}}
	body := pngBytes(t, 40, 30)
	sum := sha256.Sum256(body)
	request := images.UploadRequest{ClientImageID: "client-image-0001", FileName: "図.png", ContentType: "image/png", ByteSize: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
	first, target, err := service.Begin(ctx, request)
	if err != nil || target == nil || target.Method != "PUT" || first.Status != images.UploadPending {
		t.Fatal(first, target, err)
	}
	again, target, err := service.Begin(ctx, request)
	if err != nil || again.ID != first.ID || again.StagingKey != first.StagingKey || target == nil {
		t.Fatal("same clientImageId must resolve to the same upload and key", again, err)
	}
	other := request
	other.SHA256 = strings.Repeat("f", 64)
	if _, _, err := service.Begin(ctx, other); !errors.Is(err, images.ErrUploadReused) {
		t.Fatal("reused clientImageId with different bytes accepted", err)
	}
	if _, err := service.Complete(ctx, first.ID); !errors.Is(err, images.ErrUploadMissing) {
		t.Fatal("completion before upload", err)
	}
	if err := service.Receive(ctx, first.ID, body[:10]); !errors.Is(err, images.ErrInvalid) {
		t.Fatal("short body accepted", err)
	}
	if err := service.Receive(ctx, first.ID, body); err != nil {
		t.Fatal(err)
	}
	// The official object is written, then the process stops before the DB records it.
	if _, err := service.Complete(ctx, first.ID); err == nil {
		t.Fatal("simulated stop not observed")
	}
	// A stopped process cannot release its lease; reproduce the row it leaves.
	if _, err := store.db.ExecContext(ctx, "UPDATE image_uploads SET status='processing',lease_id='stopped',lease_until=NOW(6)+INTERVAL 1 MINUTE WHERE upload_id=?", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Complete(ctx, first.ID); !errors.Is(err, images.ErrUploadProcessing) {
		t.Fatal("live lease must not be completed twice", err)
	}
	if status, err := service.Status(ctx, first.ID); err != nil || status.Status != images.UploadProcessing {
		t.Fatal("live lease must be reported as processing", status.Status, err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE image_uploads SET lease_until=NOW(6)-INTERVAL 1 SECOND WHERE upload_id=?", first.ID); err != nil {
		t.Fatal(err)
	}
	// The browser only sees status/begin: a stopped completion must look resumable.
	if status, err := service.Status(ctx, first.ID); err != nil || status.Status != images.UploadPending {
		t.Fatal("expired lease not reported as resumable", status.Status, err)
	}
	if again, target, err := service.Begin(ctx, request); err != nil || again.ID != first.ID || target == nil {
		t.Fatal("begin must offer a target to resume a stopped completion", again.Status, target, err)
	}
	// Concurrent completion after restart: exactly one registration.
	var wait sync.WaitGroup
	results := make([]images.Upload, 4)
	errs := make([]error, 4)
	for i := range results {
		wait.Go(func() { results[i], errs[i] = service.Complete(ctx, first.ID) })
	}
	wait.Wait()
	completed := 0
	for i := range results {
		if errs[i] == nil && results[i].Status == images.UploadCompleted {
			completed++
			if results[i].ImageID != first.ImageID || results[i].Width != 40 || results[i].Height != 30 {
				t.Fatal("result", results[i])
			}
		} else if !errors.Is(errs[i], images.ErrUploadProcessing) {
			t.Fatal(errs[i])
		}
	}
	if completed == 0 {
		t.Fatal("no completion")
	}
	final, err := service.Complete(ctx, first.ID)
	if err != nil || final.Status != images.UploadCompleted || final.Result().URL != "/api/images/"+first.ImageID {
		t.Fatal("repeated complete must return the same registration", final, err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM images").Scan(&count); err != nil || count != 1 {
		t.Fatal("official images", count, err)
	}
	if _, err := media.Get(ctx, first.StagingKey); !errors.Is(err, blob.ErrNotFound) {
		t.Fatal("staging bytes kept after completion", err)
	}
	resumed, target, err := service.Begin(ctx, request)
	if err != nil || target != nil || resumed.Result() == nil || resumed.Result().ID != first.ImageID {
		t.Fatal("completed upload must return its result instead of a new target", resumed, target, err)
	}
	reader, err := media.Get(ctx, "images/"+first.ImageID+".png")
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(reader)
	reader.Close()
	if _, format, err := image.DecodeConfig(bytes.NewReader(stored)); err != nil || format != "png" {
		t.Fatal("stored image", format, err)
	}
}

func TestImageUploadRejectionsAndExpiry(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	media := blob.Files{Directory: t.TempDir()}
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	service := &images.UploadService{Repository: store, Blobs: media, Staging: images.LocalStaging{}, Now: clock}
	disguised := []byte("GIF89a-not-a-png")
	sum := sha256.Sum256(disguised)
	upload, _, err := service.Begin(ctx, images.UploadRequest{ClientImageID: "client-image-0002", FileName: "fake.png", ContentType: "image/png", ByteSize: int64(len(disguised)), SHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Receive(ctx, upload.ID, disguised); err != nil {
		t.Fatal(err)
	}
	failed, err := service.Complete(ctx, upload.ID)
	if err != nil || failed.Status != images.UploadFailed || failed.FailureCode != "format" {
		t.Fatal("declared MIME trusted", failed, err)
	}
	body := pngBytes(t, 2, 2)
	tampered := append([]byte{}, body...)
	tampered[len(tampered)-1] ^= 1
	sum = sha256.Sum256(body)
	mismatch, _, err := service.Begin(ctx, images.UploadRequest{ClientImageID: "client-image-0003", FileName: "a.png", ContentType: "image/png", ByteSize: int64(len(body)), SHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Receive(ctx, mismatch.ID, tampered); err != nil {
		t.Fatal(err)
	}
	if result, err := service.Complete(ctx, mismatch.ID); err != nil || result.FailureCode != "checksum_mismatch" {
		t.Fatal("checksum not verified", result, err)
	}
	stale, _, err := service.Begin(ctx, images.UploadRequest{ClientImageID: "client-image-0004", FileName: "b.png", ContentType: "image/png", ByteSize: int64(len(body)), SHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Receive(ctx, stale.ID, body); err != nil {
		t.Fatal(err)
	}
	now = now.Add(images.UploadSessionLifetime + time.Minute)
	if _, err := store.db.ExecContext(ctx, "UPDATE image_uploads SET expires_at=NOW(6)-INTERVAL 1 MINUTE WHERE upload_id=?", stale.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Begin(ctx, images.UploadRequest{ClientImageID: "client-image-0004", FileName: "b.png", ContentType: "image/png", ByteSize: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}); !errors.Is(err, images.ErrUploadExpired) {
		t.Fatal("expired session re-signed", err)
	}
	if err := service.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx, stale.ID)
	if err != nil || status.Status != images.UploadExpired {
		t.Fatal(status, err)
	}
	if _, err := media.Get(ctx, stale.StagingKey); !errors.Is(err, blob.ErrNotFound) {
		t.Fatal("expired staging bytes kept", err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM images").Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected upload registered an image", count, err)
	}
}
