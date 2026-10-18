package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestHealthCheckRejectsFailureAndRedirect(t *testing.T) {
	for _, status := range []int{200, 302, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status == 302 {
				w.Header().Set("Location", "/login")
			}
			w.WriteHeader(status)
		}))
		err := checkHealth(server.URL)
		server.Close()
		if (err == nil) != (status == 200) {
			t.Fatalf("status %d: error %v", status, err)
		}
	}
}

func TestRestoreTargetCannotSelectLiveDatabase(t *testing.T) {
	cases := []struct {
		dsn     string
		allowed bool
	}{
		{"postgres://localhost/blog", false},
		{"https://example.invalid/blog_restore", false},
		{"postgres://localhost/blog_restore", true},
		{"postgres://localhost/blog_test", true},
		{"postgres://localhost/blog_test?other=production", true},
		{"postgres://localhost/production?label=_test", false},
	}
	for _, tc := range cases {
		if got := restoreTarget(tc.dsn); got != tc.allowed {
			t.Errorf("allowed=%v want=%v", got, tc.allowed)
		}
	}
}

func TestStorageCannotTreatSourceAsItsOwnBackup(t *testing.T) {
	t.Setenv("MEDIA_BUCKET", "")
	t.Setenv("BACKUP_BUCKET", "")
	t.Setenv("MEDIA_DIRECTORY", t.TempDir())
	t.Setenv("BACKUP_DIRECTORY", os.Getenv("MEDIA_DIRECTORY")+"/.")
	if _, _, err := storage(context.Background(), false); err == nil {
		t.Fatal("same directory accepted as independent backup")
	}
	if _, _, err := storage(context.Background(), true); err == nil {
		t.Fatal("public deployment accepted local files")
	}
	t.Setenv("MEDIA_BUCKET", "same-private-bucket")
	t.Setenv("BACKUP_BUCKET", "same-private-bucket")
	if _, _, err := storage(context.Background(), true); err == nil {
		t.Fatal("same bucket accepted as independent backup")
	}
}
