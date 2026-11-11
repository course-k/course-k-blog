package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blog"
)

// fakePublication stands in for the writer of the reader-facing files.
type fakePublication struct {
	refreshes int
	failure   error
	current   bool
}

func (f *fakePublication) Refresh(context.Context) error {
	f.refreshes++
	return f.failure
}
func (f *fakePublication) Current(context.Context) (bool, error) { return f.current, nil }

func publicationFixture(t *testing.T, publication *fakePublication) (http.Handler, *fakeStore, *http.Cookie) {
	t.Helper()
	store := fake()
	now := time.Now()
	store.posts["draft"] = blog.Post{ID: "draft", Title: "ready", Markdown: "body", Version: 1, UpdatedAt: now}
	store.posts["live"] = blog.Post{ID: "live", Title: "visible", Markdown: "public", Published: true, PublishedAt: &now, Version: 2, UpdatedAt: now}
	store.public["live"] = blog.PublicPost{ID: "live", Title: "visible", Markdown: "public", PublishedAt: now, UpdatedAt: now}
	token := "fixture-session"
	store.sessions[auth.Digest(token)] = now.Add(time.Hour)
	hash, err := auth.HashPassword("fixture-password-for-test-only")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithFeatures(store, "https://blog.example", hash, Features{Publication: publication})
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, &http.Cookie{Name: sessionCookie, Value: token}
}

func TestPublishingAndUnpublishingRefreshReaderFiles(t *testing.T) {
	publication := &fakePublication{}
	handler, _, cookie := publicationFixture(t, publication)
	published := request(handler, "POST", "/api/admin/posts/draft/publish", `{"version":1}`, "https://blog.example", cookie)
	if published.Code != 200 || publication.refreshes != 1 || published.Header().Get("X-Publication") != "" {
		t.Fatalf("publish: status %d, refreshes %d, header %q", published.Code, publication.refreshes, published.Header().Get("X-Publication"))
	}
	hidden := request(handler, "POST", "/api/admin/posts/live/unpublish", `{"version":2}`, "https://blog.example", cookie)
	if hidden.Code != 200 || publication.refreshes != 2 {
		t.Fatalf("unpublish: status %d, refreshes %d", hidden.Code, publication.refreshes)
	}
}

func TestRejectedPublicationChangeDoesNotRefreshReaderFiles(t *testing.T) {
	publication := &fakePublication{}
	handler, _, cookie := publicationFixture(t, publication)
	stale := request(handler, "POST", "/api/admin/posts/draft/publish", `{"version":9}`, "https://blog.example", cookie)
	anonymous := request(handler, "POST", "/api/admin/posts/draft/publish", `{"version":1}`, "https://blog.example", nil)
	if stale.Code != 409 || anonymous.Code != 401 || publication.refreshes != 0 {
		t.Fatalf("stale %d, anonymous %d, refreshes %d", stale.Code, anonymous.Code, publication.refreshes)
	}
}

func TestPublishReportsWhenReaderFilesCouldNotBeRefreshed(t *testing.T) {
	publication := &fakePublication{failure: errors.New("fixture storage unavailable")}
	handler, store, cookie := publicationFixture(t, publication)
	published := request(handler, "POST", "/api/admin/posts/draft/publish", `{"version":1}`, "https://blog.example", cookie)
	// The database change is kept; the owner is told that readers do not see it yet.
	if published.Code != 200 || !store.posts["draft"].Published {
		t.Fatalf("the saved publication must be reported as saved: %d", published.Code)
	}
	if published.Header().Get("X-Publication") != "pending" {
		t.Fatalf("missing delivery warning: %q", published.Header().Get("X-Publication"))
	}
}

func TestOwnerCanCheckAndRetryReaderFiles(t *testing.T) {
	publication := &fakePublication{failure: errors.New("fixture storage unavailable")}
	handler, _, cookie := publicationFixture(t, publication)
	if anonymous := request(handler, "POST", "/api/admin/publication", `{}`, "https://blog.example", nil); anonymous.Code != 401 || publication.refreshes != 0 {
		t.Fatalf("anonymous retry: %d, refreshes %d", anonymous.Code, publication.refreshes)
	}
	if failed := request(handler, "POST", "/api/admin/publication", `{}`, "https://blog.example", cookie); failed.Code != 503 {
		t.Fatalf("a failed retry must be reported: %d", failed.Code)
	}
	if state := request(handler, "GET", "/api/admin/publication", "", "", cookie); state.Code != 200 || state.Body.String() != "{\"current\":false}\n" {
		t.Fatalf("state: %d %q", state.Code, state.Body.String())
	}
	publication.failure, publication.current = nil, true
	if retried := request(handler, "POST", "/api/admin/publication", `{}`, "https://blog.example", cookie); retried.Code != 200 || publication.refreshes != 2 {
		t.Fatalf("retry: %d, refreshes %d", retried.Code, publication.refreshes)
	}
	if state := request(handler, "GET", "/api/admin/publication", "", "", cookie); state.Body.String() != "{\"current\":true}\n" {
		t.Fatalf("state after retry: %q", state.Body.String())
	}
}
