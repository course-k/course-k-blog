package httpapi

import (
	"net/http"
	"time"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blog"
)

const loginFlowCookie = "coursek_login_flow"

func (s *Server) identityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/config", func(w http.ResponseWriter, r *http.Request) {
		mode := "password"
		if s.features.Identity != nil {
			mode = "oidc"
		}
		write(w, 200, map[string]string{"mode": mode})
	})
	if s.features.Identity == nil {
		return
	}
	mux.HandleFunc("GET /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.features.Identity.CognitoLogoutURL(), http.StatusFound)
	})
	mux.HandleFunc("GET /api/auth/signed-out", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.origin+"/admin", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		state, endpoint, err := s.features.Identity.Begin(r.Context())
		if err != nil {
			write(w, 503, map[string]string{"error": "ログイン画面を用意できませんでした。時間をおいて再試行してください。"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: loginFlowCookie, Value: state, Path: "/api/auth", MaxAge: 600, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, endpoint, http.StatusFound)
	})
	mux.HandleFunc("GET /api/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		flow, err := r.Cookie(loginFlowCookie)
		if err != nil || r.URL.Query().Get("error") != "" || s.features.Identity.Complete(r.Context(), r.URL.Query().Get("state"), flow.Value, r.URL.Query().Get("code")) != nil {
			write(w, 401, map[string]string{"error": "ログインを確認できませんでした。元の画面から再試行してください。"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: loginFlowCookie, Value: "", Path: "/api/auth", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
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
		http.Redirect(w, r, "/api/auth/complete", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /api/auth/complete", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		_, _ = w.Write([]byte(`<!doctype html><html lang="ja"><meta charset="utf-8"><title>ログイン完了</title><h1>ログインしました</h1><p>元の編集画面へ戻ってください。このウィンドウは自動的に閉じます。</p></html>`))
	})
}
