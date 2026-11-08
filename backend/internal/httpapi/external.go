package httpapi

import (
	"errors"
	"math"
	"net/http"
	"strconv"

	"coursek/blog/backend/internal/external"
)

func (s *Server) externalRoutes(mux *http.ServeMux) {
	service := s.features.External
	mux.HandleFunc("GET /api/articles", func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if query := r.URL.Query().Get("page"); query != "" {
			n, err := strconv.Atoi(query)
			if err != nil || n < 1 || n > 10000 {
				write(w, 400, map[string]string{"error": "invalid page"})
				return
			}
			page = n
		}
		month := r.URL.Query().Get("month")
		if !external.ValidMonth(month) {
			write(w, 400, map[string]string{"error": "invalid month"})
			return
		}
		result, err := service.Page(r.Context(), page, month)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, result)
	})
	mux.HandleFunc("GET /api/admin/external-posts", s.require(func(w http.ResponseWriter, r *http.Request) {
		posts, err := service.Repository.ListExternal(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, posts)
	}))
	mux.HandleFunc("GET /api/admin/sync", s.require(func(w http.ResponseWriter, r *http.Request) {
		states, err := service.Repository.SyncStates(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, states)
	}))
	mux.HandleFunc("POST /api/admin/sync/{source}", s.require(func(w http.ResponseWriter, r *http.Request) {
		err := service.Repository.RequestSync(r.Context(), r.PathValue("source"))
		var cooldown external.CooldownError
		switch {
		case errors.As(err, &cooldown):
			seconds := int(math.Ceil(cooldown.RetryAfter.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			write(w, 409, map[string]any{"error": "前回の同期から10分経過するまで再実行できません。", "reason": "cooldown", "retryAfterSeconds": seconds})
			return
		case errors.Is(err, external.ErrRunning):
			write(w, 409, map[string]any{"error": "この媒体は同期の待機中または実行中です。", "reason": "running"})
			return
		case err != nil:
			failure(w, err)
			return
		}
		service.Notify()
		// 202 only acknowledges the request; GET /api/admin/sync reports the outcome.
		write(w, 202, map[string]bool{"queued": true})
	}))
}
