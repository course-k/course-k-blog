package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/backup"
	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/external"
	"coursek/blog/backend/internal/images"
	"coursek/blog/backend/internal/oidclogin"
)

const sessionCookie = "coursek_session"

type Server struct {
	store        blog.Store
	origin, hash string
	secure       bool
	now          func() time.Time
	features     Features
}

type Features struct {
	External  *external.Service
	Images    *images.Service
	Backups   *backup.Service
	Identity  *oidclogin.Service
	Creations blog.CreationStore
}

func New(store blog.Store, origin, hash string) (http.Handler, error) {
	return NewWithFeatures(store, origin, hash, Features{})
}
func NewWithFeatures(store blog.Store, origin, hash string, features Features) (http.Handler, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("APP_ORIGIN must be a bare origin without a trailing slash")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, errors.New("HTTPS required outside loopback")
	}
	if features.Identity == nil && !auth.ValidHash(hash) {
		return nil, errors.New("ADMIN_PASSWORD_HASH is missing or invalid")
	}
	s := &Server{store: store, origin: origin, hash: hash, secure: u.Scheme == "https", now: time.Now, features: features}
	m := http.NewServeMux()
	if features.Creations != nil {
		m.HandleFunc("GET /api/admin/creation-results/{key}", s.require(func(w http.ResponseWriter, r *http.Request) {
			post, err := features.Creations.FindCreation(r.Context(), r.PathValue("key"))
			if err != nil {
				failure(w, err)
				return
			}
			write(w, 200, post)
		}))
	}
	s.identityRoutes(m)
	m.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]bool{"ok": true}) })
	m.HandleFunc("GET /api/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.Ping(r.Context()); err != nil {
			write(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		write(w, 200, map[string]bool{"ok": true})
	})
	if features.External != nil {
		s.externalRoutes(m)
	}
	if features.Images != nil {
		s.imageRoutes(m)
	}
	if features.Backups != nil {
		s.operationRoutes(m)
	}
	m.HandleFunc("GET /api/posts", s.listPublic)
	m.HandleFunc("GET /api/posts/{id}", s.getPublic)
	m.HandleFunc("POST /api/session", s.login)
	m.HandleFunc("GET /api/session", s.require(s.session))
	m.HandleFunc("DELETE /api/session", s.require(s.logout))
	m.HandleFunc("GET /api/admin/posts", s.require(s.listAdmin))
	m.HandleFunc("GET /api/admin/posts/{id}", s.require(s.getAdmin))
	m.HandleFunc("POST /api/admin/posts", s.require(s.create))
	m.HandleFunc("PUT /api/admin/posts/{id}", s.require(s.save))
	m.HandleFunc("POST /api/admin/posts/{id}/publish", s.require(s.publish))
	m.HandleFunc("POST /api/admin/posts/{id}/unpublish", s.require(s.unpublish))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if r.Header.Get("Origin") != s.origin {
				write(w, 403, map[string]string{"error": "origin rejected"})
				return
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				write(w, 415, map[string]string{"error": "JSON required"})
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		m.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, err error) {
	status, msg := 500, "request failed"
	switch {
	case errors.Is(err, blog.ErrNotFound):
		status, msg = 404, "not found"
	case errors.Is(err, blog.ErrConflict):
		status, msg = 409, "別の変更が保存されています。再読み込みして確認してください。"
	case errors.Is(err, external.ErrInvalid):
		status, msg = 400, "補正するタイトルは1〜500文字、公開日時は有効な日時を指定してください。"
	case errors.Is(err, images.ErrInvalid):
		status, msg = 400, "画像と参照を確認してください。5MiB・1200万画素以内のPNG/JPEGを利用できます。"
	case errors.Is(err, blog.ErrInvalid):
		status, msg = 400, "タイトルは1〜200文字、本文は256KiB以内で入力してください。"
	default:
		slog.Error("request failed", "error", err)
	}
	write(w, status, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 300*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		write(w, 400, map[string]string{"error": "invalid JSON"})
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		write(w, 400, map[string]string{"error": "invalid JSON"})
		return false
	}
	return true
}
func (s *Server) require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie(sessionCookie)
		if e != nil || len(c.Value) > 128 {
			write(w, 401, map[string]string{"error": "login required"})
			return
		}
		ok, err := s.store.HasSession(r.Context(), auth.Digest(c.Value), s.now())
		if err != nil {
			failure(w, err)
			return
		}
		if !ok {
			write(w, 401, map[string]string{"error": "login required"})
			return
		}
		next(w, r)
	}
}
func (s *Server) cookie(value string, expiry time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, Expires: expiry, MaxAge: maxAge}
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.features.Identity != nil {
		write(w, 405, map[string]string{"error": "専用のログイン画面を利用してください。"})
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	ok, err := s.store.AllowLogin(r.Context(), s.now())
	if err != nil {
		failure(w, err)
		return
	}
	if !ok {
		w.Header().Set("Retry-After", "900")
		write(w, 429, map[string]string{"error": "試行回数の上限です。15分後にお試しください。"})
		return
	}
	if !auth.Verify(in.Password, s.hash) {
		write(w, 401, map[string]string{"error": "ログインできませんでした。"})
		return
	}
	token, err := auth.Token()
	if err != nil {
		failure(w, err)
		return
	}
	expiry := s.now().Add(8 * time.Hour)
	if err = s.store.PutSession(r.Context(), blog.Session{Hash: auth.Digest(token), ExpiresAt: expiry}); err != nil {
		failure(w, err)
		return
	}
	if old, e := r.Cookie(sessionCookie); e == nil {
		if err = s.store.DeleteSession(r.Context(), auth.Digest(old.Value)); err != nil {
			failure(w, err)
			return
		}
	}
	http.SetCookie(w, s.cookie(token, expiry, 28800))
	write(w, 200, map[string]bool{"authenticated": true})
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]bool{"authenticated": true})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(sessionCookie)
	if err := s.store.DeleteSession(r.Context(), auth.Digest(c.Value)); err != nil {
		failure(w, err)
		return
	}
	http.SetCookie(w, s.cookie("", time.Unix(1, 0), -1))
	write(w, 200, map[string]bool{"authenticated": false})
}
func (s *Server) listPublic(w http.ResponseWriter, r *http.Request) {
	page := 1
	if q := r.URL.Query().Get("page"); q != "" {
		n, e := strconv.Atoi(q)
		if e != nil || n < 1 || n > 10000 {
			write(w, 400, map[string]string{"error": "invalid page"})
			return
		}
		page = n
	}
	posts, err := s.store.ListPublic(r.Context(), 20, (page-1)*20)
	if err != nil {
		failure(w, err)
		return
	}
	write(w, 200, posts)
}
func (s *Server) getPublic(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetPublic(r.Context(), r.PathValue("id"))
	if err != nil {
		failure(w, err)
		return
	}
	write(w, 200, p)
}
func (s *Server) listAdmin(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.ListAdmin(r.Context())
	if err != nil {
		failure(w, err)
		return
	}
	write(w, 200, p)
}
func (s *Server) getAdmin(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetAdmin(r.Context(), r.PathValue("id"))
	if err != nil {
		failure(w, err)
		return
	}
	write(w, 200, p)
}

type edit struct {
	Title       string `json:"title"`
	Markdown    string `json:"markdown"`
	Version     int64  `json:"version"`
	CreationKey string `json:"creationKey,omitempty"`
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var in edit
	if !decode(w, r, &in) {
		return
	}
	if err := blog.Validate(in.Title, in.Markdown); err != nil {
		failure(w, err)
		return
	}
	id, err := auth.Token()
	if err != nil {
		failure(w, err)
		return
	}
	p := blog.Post{ID: id, Title: in.Title, Markdown: in.Markdown, Version: 1, UpdatedAt: s.now().UTC()}
	if in.CreationKey != "" {
		if s.features.Creations == nil || !auth.ValidToken(in.CreationKey) {
			failure(w, blog.ErrInvalid)
			return
		}
		payload, err := json.Marshal(struct{ Title, Markdown string }{in.Title, in.Markdown})
		if err != nil {
			failure(w, err)
			return
		}
		saved, err := s.features.Creations.CreateOnce(r.Context(), in.CreationKey, auth.Digest(string(payload)), p)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 201, saved)
		return
	}
	if err = s.store.Create(r.Context(), p); err != nil {
		failure(w, err)
		return
	}
	write(w, 201, p)
}
func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	var in edit
	if !decode(w, r, &in) {
		return
	}
	if err := blog.Validate(in.Title, in.Markdown); err != nil {
		failure(w, err)
		return
	}
	if in.Version < 1 {
		failure(w, blog.ErrInvalid)
		return
	}
	p, err := s.store.Save(r.Context(), r.PathValue("id"), in.Title, in.Markdown, in.Version, s.now().UTC())
	if err != nil {
		failure(w, err)
		return
	}
	write(w, 200, p)
}
func (s *Server) publish(w http.ResponseWriter, r *http.Request)   { s.change(w, r, true) }
func (s *Server) unpublish(w http.ResponseWriter, r *http.Request) { s.change(w, r, false) }
func (s *Server) change(w http.ResponseWriter, r *http.Request, publish bool) {
	var in struct {
		Version int64 `json:"version"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Version < 1 {
		failure(w, blog.ErrInvalid)
		return
	}
	var p blog.Post
	var err error
	if publish {
		p, err = s.store.Publish(r.Context(), r.PathValue("id"), in.Version, s.now().UTC())
	} else {
		if s.features.Backups != nil {
			current, readError := s.store.GetAdmin(r.Context(), r.PathValue("id"))
			if readError != nil {
				failure(w, readError)
				return
			}
			if current.Version != in.Version {
				failure(w, blog.ErrConflict)
				return
			}
			if backupError := s.features.Backups.Take(r.Context(), true); backupError != nil {
				write(w, 503, map[string]string{"error": "変更前バックアップを完了できませんでした。記事は変更していません。運用状態を確認してください。"})
				return
			}
		}
		p, err = s.store.Unpublish(r.Context(), r.PathValue("id"), in.Version, s.now().UTC())
	}
	if err != nil {
		failure(w, err)
		return
	}
	write(w, 200, p)
}
