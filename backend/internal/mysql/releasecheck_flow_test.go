package mysql

import (
	"context"
	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/backup"
	"coursek/blog/backend/internal/blob"
	"coursek/blog/backend/internal/external"
	"coursek/blog/backend/internal/httpapi"
	"coursek/blog/backend/internal/images"
	"coursek/blog/backend/internal/releasecheck"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type checkSigner struct{}

func (checkSigner) PresignStagingPost(_ context.Context, key, mime string, _ int64, _ time.Duration) (string, map[string]string, error) {
	return "https://course-k-blog-production-imagesbucket-fixture.s3.us-west-2.amazonaws.com/", map[string]string{"key": key, "Content-Type": mime}, nil
}

type checkTransport func(*http.Request) (*http.Response, error)

func (f checkTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestProductionAcceptanceFlowAndCleanupAgainstRealMySQL(t *testing.T) {
	s := isolatedStore(t)
	media := blob.Files{Directory: t.TempDir()}
	archives := blob.Files{Directory: t.TempDir()}
	backups := &backup.Service{Repository: s, Media: media, Archives: archives, Retain: 4}
	upload := &images.UploadService{Repository: s, Blobs: media, Staging: images.SignedStaging{Signer: checkSigner{}}}
	hash, err := auth.HashPassword("dedicated-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewWithFeatures(s, releasecheck.Origin, hash, httpapi.Features{Creations: s, Images: &images.Service{Repository: s, Blobs: media}, Uploads: upload, External: &external.Service{Repository: s}, Backups: backups})
	if err != nil {
		t.Fatal(err)
	}
	sizes := []int{}
	client := &http.Client{Transport: checkTransport(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		if r.URL.Host == "blog.course-k.com" {
			handler.ServeHTTP(recorder, r)
			return recorder.Result(), nil
		}
		if len(r.Cookies()) != 0 || r.URL.Host != "course-k-blog-production-imagesbucket-fixture.s3.us-west-2.amazonaws.com" {
			t.Fatal("credential destination changed")
		}
		if err := r.ParseMultipartForm(11_000_000); err != nil {
			t.Fatal(err)
		}
		defer r.MultipartForm.RemoveAll()
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(data))
		if err = media.Put(r.Context(), r.FormValue("key"), data, r.FormValue("Content-Type")); err != nil {
			t.Fatal(err)
		}
		recorder.Header().Set("Access-Control-Allow-Origin", releasecheck.Origin)
		recorder.WriteHeader(204)
		return recorder.Result(), nil
	})}
	check := releasecheck.Check{Store: s, Schema: LatestSchemaVersion(), Client: client, Bucket: "course-k-blog-production-imagesbucket-fixture", DeleteBlob: media.Delete, Backup: func(ctx context.Context) error { return backups.Take(ctx, true) }}
	if err := check.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[0] != 10_000_000 || sizes[1] < 1 {
		t.Fatal("transport boundary not exercised", sizes)
	}
	for _, table := range []string{"posts", "images", "image_uploads", "creation_requests", "sessions", "post_images"} {
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("fixture residue", table, n, err)
		}
	}
	state, err := s.BackupState(context.Background())
	if err != nil || state.Generation != state.BackedUpGeneration {
		t.Fatal("cleanup not backed up", err)
	}
}
