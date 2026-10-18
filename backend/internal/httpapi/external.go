package httpapi

import (
	"errors"
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
		result, err := service.Page(r.Context(), page)
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
	mux.HandleFunc("PATCH /api/admin/external-posts/{source}/{id}", s.require(func(w http.ResponseWriter, r *http.Request) {
		var in external.Correction
		if !decode(w, r, &in) {
			return
		}
		if err := service.Repository.CorrectExternal(r.Context(), r.PathValue("source"), r.PathValue("id"), in); err != nil {
			failure(w, err)
			return
		}
		write(w, 200, map[string]bool{"saved": true})
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
		if errors.Is(err, external.ErrBusy) {
			write(w, 409, map[string]string{"error": "同期中または前回の試行から10分以内です。状態を確認してください。"})
			return
		}
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 202, map[string]bool{"queued": true})
	}))
}
