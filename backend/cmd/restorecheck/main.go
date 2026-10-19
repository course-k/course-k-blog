// restorecheck rehearses an application backup on an isolated, disposable RDS.
// It never accepts the live database or either live object bucket as a target.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"coursek/blog/backend/internal/backup"
	"coursek/blog/backend/internal/blob"
	"coursek/blog/backend/internal/images"
	"coursek/blog/backend/internal/mysql"
)

func safeTarget(dsn, sourceHost, targetHost, media, archives string) bool {
	u, err := url.Parse(dsn)
	return err == nil && u.Scheme == "mysql" && u.Hostname() == targetHost && targetHost != "" &&
		sourceHost != "" && sourceHost != targetHost && u.Path == "/blog_restore" &&
		u.Query().Get("tls") == "verify-full" && media != archives &&
		strings.HasPrefix(media, "course-k-blog-production-restoremediabucket-") &&
		strings.HasPrefix(archives, "course-k-blog-production-backupsbucket-")
}

// SQL does not promise row order. Compare every value, preserving JSON numbers,
// while ignoring only the dirty-state generation that restoration must advance.
func canonicalRows(raw json.RawMessage) ([]string, error) {
	var rows []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&rows); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		body, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		result = append(result, string(body))
	}
	slices.Sort(result)
	return result, nil
}

func equalSnapshots(expected, actual backup.Snapshot) bool {
	if expected.DatabaseEngine != actual.DatabaseEngine || expected.SchemaVersion != actual.SchemaVersion || len(expected.Tables) != len(actual.Tables) {
		return false
	}
	for table, data := range expected.Tables {
		other, ok := actual.Tables[table]
		if !ok {
			return false
		}
		left, err := canonicalRows(data)
		right, otherErr := canonicalRows(other)
		if err != nil || otherErr != nil || !slices.Equal(left, right) {
			return false
		}
	}
	return true
}

func run(ctx context.Context, cleanup bool) error {
	dsn, mediaBucket, archiveBucket := os.Getenv("DATABASE_URL"), os.Getenv("MEDIA_BUCKET"), os.Getenv("BACKUP_BUCKET")
	if !safeTarget(dsn, os.Getenv("SOURCE_DB_HOST"), os.Getenv("RESTORE_DB_HOST"), mediaBucket, archiveBucket) || !images.ValidID(os.Getenv("BACKUP_ID")) {
		return errors.New("isolated restore target required")
	}
	store, err := mysql.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer store.Close()
	media, err := blob.NewS3(ctx, mediaBucket)
	if err != nil {
		return err
	}
	archives, err := blob.NewS3(ctx, archiveBucket)
	if err != nil {
		return err
	}
	id := os.Getenv("BACKUP_ID")
	body, err := archives.Get(ctx, "backups/"+id+".json.gz")
	if err != nil {
		return err
	}
	content, readErr := io.ReadAll(io.LimitReader(body, 128*1024*1024+1))
	body.Close()
	if readErr != nil || len(content) > 128*1024*1024 {
		return errors.New("archive read failed")
	}
	expected, err := backup.Decode(content)
	if err != nil {
		return err
	}
	service := backup.Service{Repository: store, Media: media, Archives: archives}
	if err := service.Restore(ctx, id); err != nil {
		return err
	}
	actual, err := store.ExportSnapshot(ctx)
	if err != nil || !equalSnapshots(expected, actual) {
		return errors.New("restored database differs from archive")
	}
	// The database archive uses column names, not the public image API names.
	var imageRows []struct {
		ID   string `json:"id"`
		Key  string `json:"object_key"`
		Hash string `json:"sha256"`
		Size int    `json:"byte_size"`
	}
	if err := json.Unmarshal(actual.Tables["images"], &imageRows); err != nil {
		return err
	}
	for _, image := range imageRows {
		r, err := media.Get(ctx, image.Key)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(r, images.MaxUploadBytes+1))
		r.Close()
		hash := sha256.Sum256(data)
		if readErr != nil || len(data) != image.Size || hex.EncodeToString(hash[:]) != image.Hash {
			return errors.New("restored image differs from archive")
		}
	}
	// Explicit cleanup touches only copied objects in the guarded, unversioned
	// rehearsal bucket. IAM grants no deletion from the source backup bucket.
	if cleanup {
		for _, image := range imageRows {
			if err := media.Delete(ctx, image.Key); err != nil {
				return err
			}
		}
	}
	fmt.Printf("Restore verified: schema=%d tables=%d images=%d cleanup=%t\n", actual.SchemaVersion, len(actual.Tables), len(imageRows), cleanup)
	return nil
}

func main() {
	cleanup := flag.Bool("cleanup-media", false, "Remove copied rehearsal images after successful verification")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if flag.NArg() != 0 || run(ctx, *cleanup) != nil {
		fmt.Fprintln(os.Stderr, "Restore rehearsal failed; inspect the isolated target. Raw errors suppressed.")
		os.Exit(1)
	}
}
