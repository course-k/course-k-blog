package httpapi

import (
	"bytes"
	"context"
	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blog"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// Test double only. Production has no in-memory store or bypass login.
type fakeStore struct {
	posts     map[string]blog.Post
	public    map[string]blog.PublicPost
	sessions  map[string]time.Time
	attempts  int
	pingError error
}

func fake() *fakeStore {
	return &fakeStore{posts: map[string]blog.Post{}, public: map[string]blog.PublicPost{}, sessions: map[string]time.Time{}}
}
func (f *fakeStore) Ping(context.Context) error { return f.pingError }
func (f *fakeStore) ListPublic(_ context.Context, limit, offset int) ([]blog.PublicPost, error) {
	out := []blog.PublicPost{}
	for _, p := range f.public {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublishedAt.After(out[j].PublishedAt) })
	if offset >= len(out) {
		return []blog.PublicPost{}, nil
	}
	end := min(len(out), offset+limit)
	return out[offset:end], nil
}
func (f *fakeStore) GetPublic(_ context.Context, id string) (blog.PublicPost, error) {
	p, ok := f.public[id]
	if !ok {
		return p, blog.ErrNotFound
	}
	return p, nil
}
func (f *fakeStore) ListAdmin(context.Context) ([]blog.Post, error) {
	out := []blog.Post{}
	for _, p := range f.posts {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeStore) GetAdmin(_ context.Context, id string) (blog.Post, error) {
	p, ok := f.posts[id]
	if !ok {
		return p, blog.ErrNotFound
	}
	return p, nil
}
func (f *fakeStore) Create(_ context.Context, p blog.Post) error { f.posts[p.ID] = p; return nil }
func (f *fakeStore) Save(_ context.Context, id, title, md string, v int64, now time.Time) (blog.Post, error) {
	p, ok := f.posts[id]
	if !ok {
		return p, blog.ErrNotFound
	}
	if p.Version != v {
		return p, blog.ErrConflict
	}
	p.Title = title
	p.Markdown = md
	p.Version++
	p.UpdatedAt = now
	f.posts[id] = p
	return p, nil
}
func (f *fakeStore) Publish(_ context.Context, id string, v int64, now time.Time) (blog.Post, error) {
	p, ok := f.posts[id]
	if !ok {
		return p, blog.ErrNotFound
	}
	if p.Version != v {
		return p, blog.ErrConflict
	}
	if p.PublishedAt == nil {
		p.PublishedAt = &now
	}
	p.Published = true
	p.Version++
	f.posts[id] = p
	f.public[id] = blog.PublicPost{ID: id, Title: p.Title, Markdown: p.Markdown, PublishedAt: *p.PublishedAt, UpdatedAt: now}
	return p, nil
}
func (f *fakeStore) Unpublish(_ context.Context, id string, v int64, _ time.Time) (blog.Post, error) {
	p, ok := f.posts[id]
	if !ok {
		return p, blog.ErrNotFound
	}
	if p.Version != v {
		return p, blog.ErrConflict
	}
	p.Published = false
	p.Version++
	f.posts[id] = p
	delete(f.public, id)
	return p, nil
}
func (f *fakeStore) PutSession(_ context.Context, s blog.Session) error {
	f.sessions[s.Hash] = s.ExpiresAt
	return nil
}
func (f *fakeStore) HasSession(_ context.Context, s string, now time.Time) (bool, error) {
	return f.sessions[s].After(now), nil
}
func (f *fakeStore) DeleteSession(_ context.Context, s string) error {
	delete(f.sessions, s)
	return nil
}
func (f *fakeStore) AllowLogin(context.Context, time.Time) (bool, error) {
	f.attempts++
	return f.attempts <= 10, nil
}

func request(h http.Handler, method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAuthenticationDraftPublishAndLogout(t *testing.T) {
	store := fake()
	hash, _ := auth.HashPassword("test password long enough")
	h, err := New(store, "https://blog.example", hash)
	if err != nil {
		t.Fatal(err)
	}
	expect := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body.String())
		}
	}
	expect(request(h, "GET", "/api/admin/posts", "", "", nil), 401)
	expect(request(h, "POST", "/api/session", `{"password":"test password long enough"}`, "https://evil.example", nil), 403)
	expect(request(h, "POST", "/api/session", `{"password":"wrong"}`, "https://blog.example", nil), 401)
	login := request(h, "POST", "/api/session", `{"password":"test password long enough"}`, "https://blog.example", nil)
	expect(login, 200)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing cookie")
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie flags")
	}
	if _, ok := store.sessions[cookie.Value]; ok {
		t.Fatal("raw session persisted")
	}
	created := request(h, "POST", "/api/admin/posts", `{"title":"最初の記事","markdown":"未公開の秘密"}`, "https://blog.example", cookie)
	expect(created, 201)
	var post blog.Post
	if err = json.Unmarshal(created.Body.Bytes(), &post); err != nil {
		t.Fatal(err)
	}
	expect(request(h, "GET", "/api/posts/"+post.ID, "", "", nil), 404)
	list := request(h, "GET", "/api/posts", "", "", nil)
	expect(list, 200)
	if strings.Contains(list.Body.String(), "未公開の秘密") {
		t.Fatal("draft leaked")
	}
	expect(request(h, "POST", "/api/admin/posts/"+post.ID+"/publish", `{"version":1}`, "https://blog.example", cookie), 200)
	published := request(h, "GET", "/api/posts/"+post.ID, "", "", nil)
	expect(published, 200)
	var before blog.PublicPost
	_ = json.Unmarshal(published.Body.Bytes(), &before)
	expect(request(h, "PUT", "/api/admin/posts/"+post.ID, `{"title":"新しい下書き","markdown":"別の秘密","version":2}`, "https://blog.example", cookie), 200)
	public := request(h, "GET", "/api/posts/"+post.ID, "", "", nil)
	if strings.Contains(public.Body.String(), "別の秘密") || !strings.Contains(public.Body.String(), "最初の記事") {
		t.Fatal("draft replaced published snapshot")
	}
	expect(request(h, "PUT", "/api/admin/posts/"+post.ID, `{"title":"古い編集","markdown":"lost update","version":2}`, "https://blog.example", cookie), 409)
	expect(request(h, "POST", "/api/admin/posts/"+post.ID+"/publish", `{"version":3}`, "https://blog.example", cookie), 200)
	afterResponse := request(h, "GET", "/api/posts/"+post.ID, "", "", nil)
	var after blog.PublicPost
	_ = json.Unmarshal(afterResponse.Body.Bytes(), &after)
	if !before.PublishedAt.Equal(after.PublishedAt) || before.ID != after.ID {
		t.Fatal("stable URL/date changed")
	}
	expect(request(h, "POST", "/api/admin/posts/"+post.ID+"/unpublish", `{"version":4}`, "https://blog.example", cookie), 200)
	expect(request(h, "GET", "/api/posts/"+post.ID, "", "", nil), 404)
	expect(request(h, "DELETE", "/api/session", `{}`, "https://blog.example", cookie), 200)
	expect(request(h, "GET", "/api/admin/posts", "", "", cookie), 401)
}
func TestInputAndSessionBoundaries(t *testing.T) {
	store := fake()
	hash, _ := auth.HashPassword("test password long enough")
	h, _ := New(store, "http://localhost:8080", hash)
	cookie := &http.Cookie{Name: sessionCookie, Value: "fixture-session"}
	store.sessions[auth.Digest(cookie.Value)] = time.Now().Add(time.Hour)
	cases := []struct {
		method, path, body, origin string
		status                     int
	}{
		{"POST", "/api/admin/posts", `{"title":"","markdown":"x"}`, "http://localhost:8080", 400},
		{"POST", "/api/admin/posts", `{"title":"x","markdown":"x","published":true}`, "http://localhost:8080", 400},
		{"POST", "/api/admin/posts", `{"title":"x","markdown":"x"} {}`, "http://localhost:8080", 400},
		{"POST", "/api/admin/posts", `{"title":"x","markdown":"x"}`, "", 403},
		{"GET", "/api/posts?page=-1", "", "", 400},
		{"GET", "/api/posts?page=oops", "", "", 400},
	}
	for _, tc := range cases {
		w := request(h, tc.method, tc.path, tc.body, tc.origin, cookie)
		if w.Code != tc.status {
			t.Fatalf("%s: %d != %d", tc.path, w.Code, tc.status)
		}
	}
	store.sessions[auth.Digest(cookie.Value)] = time.Now().Add(-time.Second)
	if request(h, "GET", "/api/admin/posts", "", "", cookie).Code != 401 {
		t.Fatal("expired session accepted")
	}
	if _, err := New(store, "http://example.com", hash); err == nil {
		t.Fatal("nonlocal HTTP accepted")
	}
}
func TestLoginLimitSharedByHandlers(t *testing.T) {
	store := fake()
	hash, _ := auth.HashPassword("test password long enough")
	a, _ := New(store, "https://blog.example", hash)
	b, _ := New(store, "https://blog.example", hash)
	store.attempts = 10
	for _, h := range []http.Handler{a, b} {
		if request(h, "POST", "/api/session", `{"password":"test password long enough"}`, "https://blog.example", nil).Code != 429 {
			t.Fatal("login limit bypassed")
		}
	}
}

func TestHealthAndProtectionDuringDatabaseFailure(t *testing.T) {
	store := fake()
	store.pingError = fmt.Errorf("database offline")
	hash, _ := auth.HashPassword("test-only-long-password")
	handler, err := New(store, "http://localhost:8080", hash)
	if err != nil {
		t.Fatal(err)
	}
	for path, status := range map[string]int{"/api/healthz": 200, "/api/readyz": 503, "/api/admin/posts": 401} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != status {
			t.Fatalf("%s: got %d", path, recorder.Code)
		}
		for key, value := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "strict-origin-when-cross-origin"} {
			if recorder.Header().Get(key) != value {
				t.Fatalf("%s missing %s", path, key)
			}
		}
	}
}
