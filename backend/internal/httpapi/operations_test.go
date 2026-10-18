package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/backup"
	"coursek/blog/backend/internal/blog"
)

type unavailableBackup struct {
	backup.Repository
	calls int
}

func (r *unavailableBackup) ClaimBackup(context.Context, string, bool) (bool, error) {
	r.calls++
	return false, errors.New("fixture storage unavailable")
}

func TestUnpublishRequiresSuccessfulPreChangeBackup(t *testing.T) {
	store := fake()
	now := time.Now()
	post := blog.Post{ID: "published-fixture", Title: "visible", Markdown: "public content", Published: true, PublishedAt: &now, Version: 2, UpdatedAt: now}
	store.posts[post.ID] = post
	store.public[post.ID] = blog.PublicPost{ID: post.ID, Title: post.Title, Markdown: post.Markdown, PublishedAt: now, UpdatedAt: now}
	token := "fixture-session"
	store.sessions[auth.Digest(token)] = now.Add(time.Hour)
	hash, err := auth.HashPassword("fixture-password-for-test-only")
	if err != nil {
		t.Fatal(err)
	}
	repository := &unavailableBackup{}
	handler, err := NewWithFeatures(store, "https://blog.example", hash, Features{Backups: &backup.Service{Repository: repository}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/posts/" + post.ID + "/unpublish"
	cookie := &http.Cookie{Name: sessionCookie, Value: token}
	denied := request(handler, "POST", path, `{"version":2}`, "https://blog.example", nil)
	if denied.Code != 401 || repository.calls != 0 {
		t.Fatal("unauthorized request reached backup")
	}
	stale := request(handler, "POST", path, `{"version":1}`, "https://blog.example", cookie)
	if stale.Code != 409 || repository.calls != 0 {
		t.Fatal("conflicting request reached backup")
	}
	failed := request(handler, "POST", path, `{"version":2}`, "https://blog.example", cookie)
	if failed.Code != 503 || repository.calls != 1 {
		t.Fatal("backup failure contract", failed.Code)
	}
	public := request(handler, "GET", "/api/posts/"+post.ID, "", "", nil)
	if public.Code != 200 || !store.posts[post.ID].Published || store.posts[post.ID].Version != 2 {
		t.Fatal("failed backup changed publication")
	}
}
