package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blob"
	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/external"
	"coursek/blog/backend/internal/images"
)

func ownerHandler(t *testing.T, origin string, features Features) (http.Handler, *fakeStore, *http.Cookie) {
	t.Helper()
	store := fake()
	hash, err := auth.HashPassword("fixture-password-only")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithFeatures(store, origin, hash, features)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: "owner-fixture"}
	store.sessions[auth.Digest(cookie.Value)] = time.Now().Add(time.Hour)
	return handler, store, cookie
}

func TestUntitledDraftSavesButCannotBePublished(t *testing.T) {
	origin := "https://blog.example"
	h, _, cookie := ownerHandler(t, origin, Features{})
	created := request(h, "POST", "/api/admin/posts", `{"title":"","markdown":"本文だけ"}`, origin, cookie)
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var post blog.Post
	if err := json.Unmarshal(created.Body.Bytes(), &post); err != nil || post.Title != "" {
		t.Fatal("untitled draft not kept empty", post.Title, err)
	}
	refused := request(h, "POST", "/api/admin/posts/"+post.ID+"/publish", `{"version":1}`, origin, cookie)
	if refused.Code != 400 || !strings.Contains(refused.Body.String(), "タイトル") {
		t.Fatal("untitled publication accepted", refused.Code, refused.Body.String())
	}
	if request(h, "PUT", "/api/admin/posts/"+post.ID, `{"title":"題名","markdown":"本文","version":1}`, origin, cookie).Code != 200 {
		t.Fatal("title save failed")
	}
	if request(h, "POST", "/api/admin/posts/"+post.ID+"/publish", `{"version":1}`, origin, cookie).Code != 409 {
		t.Fatal("stale publication version accepted")
	}
	if request(h, "POST", "/api/admin/posts/"+post.ID+"/publish", `{"version":2}`, origin, cookie).Code != 200 {
		t.Fatal("titled publication failed")
	}
	// A blank working title on a published article is saved but never published.
	if request(h, "PUT", "/api/admin/posts/"+post.ID, `{"title":" 　","markdown":"変更","version":3}`, origin, cookie).Code != 200 {
		t.Fatal("blank working title rejected")
	}
	if request(h, "POST", "/api/admin/posts/"+post.ID+"/publish", `{"version":4}`, origin, cookie).Code != 400 {
		t.Fatal("blank title published")
	}
	public := request(h, "GET", "/api/posts/"+post.ID, "", "", nil)
	if !strings.Contains(public.Body.String(), "題名") {
		t.Fatal("published title changed", public.Body.String())
	}
	invalid := request(h, "PUT", "/api/admin/posts/"+post.ID, `{"title":"題名","markdown":"![a](/x.png \"blog-image:v1;width=9;align=left\")","version":4}`, origin, cookie)
	if invalid.Code != 400 || !strings.Contains(invalid.Body.String(), "blog-image:v1") {
		t.Fatal("invalid reserved image setting accepted", invalid.Code, invalid.Body.String())
	}
	plain := request(h, "PUT", "/api/admin/posts/"+post.ID, `{"title":"題名","markdown":"![a](/x.png \"blog-image is mentioned\")","version":4}`, origin, cookie)
	if plain.Code != 200 {
		t.Fatal("ordinary title rejected", plain.Body.String())
	}
}

type syncFake struct {
	external.Repository
	err error
}

func (s *syncFake) RequestSync(context.Context, string) error { return s.err }

func TestManualSyncConflictExplainsCooldownSeparately(t *testing.T) {
	origin := "https://blog.example"
	repository := &syncFake{}
	h, _, cookie := ownerHandler(t, origin, Features{External: &external.Service{Repository: repository}})
	repository.err = external.CooldownError{RetryAfter: 4*time.Minute + 100*time.Millisecond}
	cooldown := request(h, "POST", "/api/admin/sync/qiita", `{}`, origin, cookie)
	var body struct {
		Reason     string `json:"reason"`
		RetryAfter int    `json:"retryAfterSeconds"`
	}
	if cooldown.Code != 409 || json.Unmarshal(cooldown.Body.Bytes(), &body) != nil || body.Reason != "cooldown" || body.RetryAfter != 241 || cooldown.Header().Get("Retry-After") != "241" {
		t.Fatal("cooldown", cooldown.Code, cooldown.Body.String())
	}
	repository.err = external.ErrRunning
	running := request(h, "POST", "/api/admin/sync/qiita", `{}`, origin, cookie)
	if running.Code != 409 || !strings.Contains(running.Body.String(), `"reason":"running"`) {
		t.Fatal("running", running.Body.String())
	}
	repository.err = nil
	if accepted := request(h, "POST", "/api/admin/sync/qiita", `{}`, origin, cookie); accepted.Code != 202 {
		t.Fatal("accepted", accepted.Code)
	}
	if request(h, "PATCH", "/api/admin/external-posts/qiita/x", `{}`, origin, cookie).Code != 405 && request(h, "PATCH", "/api/admin/external-posts/qiita/x", `{}`, origin, cookie).Code != 404 {
		t.Fatal("external correction route still registered")
	}
}

// memoryUploads is a test double for the image_uploads table.
type memoryUploads struct {
	mu      sync.Mutex
	uploads map[string]images.Upload
	leases  map[string]string
	images  map[string]images.Image
}

func (m *memoryUploads) CreateUpload(_ context.Context, u images.Upload) (images.Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.uploads {
		if existing.ClientImageID == u.ClientImageID {
			if existing.SHA256 != u.SHA256 {
				return images.Upload{}, images.ErrUploadReused
			}
			return existing, nil
		}
	}
	m.uploads[u.ID] = u
	return u, nil
}
func (m *memoryUploads) GetUpload(_ context.Context, id string) (images.Upload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.uploads[id]
	if !ok {
		return u, images.ErrUploadNotFound
	}
	return u, nil
}
func (m *memoryUploads) LeaseUpload(_ context.Context, id, lease string, _ time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.uploads[id]
	if u.Status != images.UploadPending {
		return false, nil
	}
	u.Status = images.UploadProcessing
	m.uploads[id], m.leases[id] = u, lease
	return true, nil
}
func (m *memoryUploads) SetUploadKey(_ context.Context, id, lease, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.uploads[id]
	if u.ImageKey == "" {
		u.ImageKey = key
		m.uploads[id] = u
	}
	return u.ImageKey, nil
}
func (m *memoryUploads) ReleaseUpload(_ context.Context, id, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.uploads[id]
	u.Status = images.UploadPending
	m.uploads[id] = u
	return nil
}
func (m *memoryUploads) CompleteUpload(_ context.Context, id, _ string, image images.Image, width, height int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.uploads[id]
	u.Status, u.Width, u.Height = images.UploadCompleted, width, height
	m.uploads[id] = u
	m.images[image.ID] = image
	return nil
}
func (m *memoryUploads) FailUpload(_ context.Context, id, _ string, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.uploads[id]
	u.Status, u.FailureCode = images.UploadFailed, code
	m.uploads[id] = u
	return nil
}
func (m *memoryUploads) ExpireUploads(context.Context, time.Time) ([]images.Upload, error) {
	return nil, nil
}
func (m *memoryUploads) PurgeUploads(context.Context, time.Time) error { return nil }

func TestDirectUploadContractAndLocalBinaryException(t *testing.T) {
	origin := "http://localhost:8080"
	repository := &memoryUploads{uploads: map[string]images.Upload{}, leases: map[string]string{}, images: map[string]images.Image{}}
	storage := blob.Files{Directory: t.TempDir()}
	uploads := &images.UploadService{Repository: repository, Blobs: storage, Staging: images.LocalStaging{}}
	h, _, cookie := ownerHandler(t, origin, Features{Images: &images.Service{Blobs: storage}, Uploads: uploads, LocalUploads: true})
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(picture.Bytes())
	begin, _ := json.Marshal(images.UploadRequest{ClientImageID: "client-image-http", FileName: "a.png", ContentType: "image/png", ByteSize: int64(picture.Len()), SHA256: hex.EncodeToString(sum[:])})
	created := request(h, "POST", "/api/admin/image-uploads", string(begin), origin, cookie)
	var started struct {
		UploadID string         `json:"uploadId"`
		Status   string         `json:"status"`
		Upload   *images.Target `json:"upload"`
	}
	if created.Code != 200 || json.Unmarshal(created.Body.Bytes(), &started) != nil || started.Upload == nil || started.Upload.Method != "PUT" {
		t.Fatal("begin", created.Code, created.Body.String())
	}
	put := func(contentType, origin string, body []byte, session *http.Cookie) int {
		r := httptest.NewRequest("PUT", started.Upload.URL, bytes.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("Origin", origin)
		if session != nil {
			r.AddCookie(session)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := put("application/octet-stream", "https://evil.example", picture.Bytes(), cookie); code != 403 {
		t.Fatal("cross-origin binary upload", code)
	}
	if code := put("application/octet-stream", origin, picture.Bytes(), nil); code != 401 {
		t.Fatal("anonymous binary upload", code)
	}
	if code := put("application/octet-stream", origin, append(picture.Bytes(), make([]byte, images.MaxUploadBytes)...), cookie); code != 413 {
		t.Fatal("oversized binary upload", code)
	}
	if code := put("application/octet-stream", origin, picture.Bytes(), cookie); code != 200 {
		t.Fatal("binary upload", code)
	}
	// The binary exception does not loosen the JSON requirement elsewhere.
	r := httptest.NewRequest("POST", "/api/admin/posts", strings.NewReader(`{"title":"","markdown":""}`))
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("Origin", origin)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal("non-JSON article write accepted", w.Code)
	}
	complete := request(h, "POST", "/api/admin/image-uploads/"+started.UploadID+"/complete", `{}`, origin, cookie)
	var done struct {
		Status string         `json:"status"`
		Result *images.Result `json:"result"`
	}
	if complete.Code != 200 || json.Unmarshal(complete.Body.Bytes(), &done) != nil || done.Status != "completed" || done.Result == nil || done.Result.Width != 3 || !strings.HasPrefix(done.Result.URL, "/api/images/") {
		t.Fatal("complete", complete.Code, complete.Body.String())
	}
	again := request(h, "POST", "/api/admin/image-uploads/"+started.UploadID+"/complete", `{}`, origin, cookie)
	if again.Code != 200 || !strings.Contains(again.Body.String(), done.Result.ID) || len(repository.images) != 1 {
		t.Fatal("second completion", again.Body.String(), len(repository.images))
	}
	status := request(h, "GET", "/api/admin/image-uploads/"+started.UploadID, "", "", cookie)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"completed"`) {
		t.Fatal("status", status.Body.String())
	}
	if request(h, "GET", "/api/admin/image-uploads/"+started.UploadID, "", "", nil).Code != 401 {
		t.Fatal("anonymous status")
	}
}

func TestProductionHasNoBinaryUploadRoute(t *testing.T) {
	origin := "https://blog.example"
	repository := &memoryUploads{uploads: map[string]images.Upload{}, leases: map[string]string{}, images: map[string]images.Image{}}
	uploads := &images.UploadService{Repository: repository, Blobs: blob.Files{Directory: t.TempDir()}, Staging: images.LocalStaging{}}
	h, _, cookie := ownerHandler(t, origin, Features{Images: &images.Service{}, Uploads: uploads})
	r := httptest.NewRequest("PUT", "/api/admin/image-uploads/"+strings.Repeat("a", 43)+"/content", strings.NewReader("bytes"))
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("Origin", origin)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal("binary route reachable without LocalUploads", w.Code)
	}
}
