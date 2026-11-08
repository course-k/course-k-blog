package releasecheck

import (
	"bytes"
	"context"
	"coursek/blog/backend/internal/blog"
	"image"
	"io"
	"net/http"
	"testing"
)

type testStore struct{ sessions, cleaned, deleted int }

func (s *testStore) VerifySchema(context.Context) error             { return nil }
func (s *testStore) SchemaVersion(context.Context) (int, error)     { return 2, nil }
func (s *testStore) PutSession(context.Context, blog.Session) error { s.sessions++; return nil }
func (s *testStore) DeleteSession(context.Context, string) error    { s.deleted++; return nil }
func (s *testStore) CleanupReleaseCheck(context.Context, string) ([]string, error) {
	s.cleaned++
	return nil, nil
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBoundaryFixturesDecode(t *testing.T) {
	b, e := boundaryPNG()
	if e != nil || len(b) != 10_000_000 {
		t.Fatal("wrong boundary")
	}
	for _, data := range [][]byte{b, webP()} {
		cfg, _, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil || cfg.Width < 1 || cfg.Height < 1 {
			t.Fatal("invalid fixture", e)
		}
	}
}
func TestFailureRemovesTemporarySessionAndFixtures(t *testing.T) {
	s := &testStore{}
	backups := 0
	c := Check{Store: s, Schema: 2, Bucket: "course-k-blog-production-imagesbucket-fixture", Backup: func(context.Context) error { backups++; return nil }, DeleteBlob: func(context.Context, string) error { return nil }, Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "blog.course-k.com" || len(r.Cookies()) != 0 {
			t.Fatal("anonymous check leaked a session")
		}
		return &http.Response{StatusCode: 500, Body: io.NopCloser(bytes.NewReader(nil)), Header: http.Header{}}, nil
	})}}
	if c.Run(context.Background()) == nil {
		t.Fatal("failed endpoint accepted")
	}
	if s.sessions != 1 || s.cleaned != 1 || s.deleted != 1 || backups != 1 {
		t.Fatal("cleanup missing", s, backups)
	}
}
func TestWrongSchemaNeverCreatesSession(t *testing.T) {
	s := &testStore{}
	c := Check{Store: s, Schema: 3, Bucket: "course-k-blog-production-imagesbucket-fixture", Backup: func(context.Context) error { return nil }, DeleteBlob: func(context.Context, string) error { return nil }}
	if c.Run(context.Background()) == nil || s.sessions != 0 {
		t.Fatal("wrong schema allowed")
	}
}
