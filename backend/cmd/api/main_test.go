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
		{"mysql://localhost/blog", false},
		{"https://example.invalid/blog_restore", false},
		{"mysql://localhost/blog_restore", true},
		{"mysql://localhost/blog_test", true},
		{"mysql://localhost/blog_test?other=production", true},
		{"mysql://localhost/production?label=_test", false},
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

func TestRestoreIntoANewDatabaseNeedsItsHostNamedExactly(t *testing.T) {
	// Moving to a new database server restores into its real, still empty
	// database. The operator must name that server, so a command aimed at the
	// wrong connection cannot proceed. Emptiness is checked by the restore itself.
	live := "mysql://app:secret@old.example.internal:3306/blog?tls=verify-full"
	fresh := "mysql://app:secret@new.cluster.example.internal:3306/blog?tls=verify-full"
	cases := []struct {
		name, dsn, named string
		allowed          bool
	}{
		{"dedicated restore database needs no host", "mysql://app@db/blog_restore", "", true},
		{"real database without a named host", fresh, "", false},
		{"real database with its own host named", fresh, "new.cluster.example.internal", true},
		{"host of another server", live, "new.cluster.example.internal", false},
		{"host with a port is not a host name", fresh, "new.cluster.example.internal:3306", false},
		{"prefix of the host", fresh, "new.cluster", false},
		{"not a MySQL URL", "postgres://app@new.cluster.example.internal/blog", "new.cluster.example.internal", false},
		{"unparseable URL", "://", "new.cluster.example.internal", false},
	}
	for _, tc := range cases {
		if got := restoreAllowed(tc.dsn, tc.named); got != tc.allowed {
			t.Errorf("%s: allowed=%v, want %v", tc.name, got, tc.allowed)
		}
	}
}

func TestClearingARestoreTargetNeedsConfirmationAndItsHost(t *testing.T) {
	// Rejections happen before the store is touched, so a nil store is safe.
	fresh := "mysql://app:secret@new.cluster.example.internal:3306/blog?tls=verify-full"
	for _, args := range [][]string{
		{"--new-database-host", "new.cluster.example.internal"},
		{"--confirm-delete-content"},
		{"--confirm-delete-content", "--new-database-host", "old.example.internal"},
		{"--confirm-delete-content", "--new-database-host", "new.cluster.example.internal", "extra"},
	} {
		if err := runDataCommand(context.Background(), "clear-restore-target", args, fresh, nil, nil); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
