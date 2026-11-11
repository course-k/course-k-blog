package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Publication writes the files that readers are served from.
type Publication interface {
	Refresh(context.Context) error
	Current(context.Context) (bool, error)
}

// Refreshing rewrites every list page, so it gets more time than one request.
const publicationTimeout = 25 * time.Second

// refreshPublication runs after a change is committed. The change stays saved
// when the refresh fails; the response header tells the editor that readers do
// not see it yet, and the owner can retry from the publication endpoint.
func (s *Server) refreshPublication(w http.ResponseWriter, r *http.Request) {
	if s.features.Publication == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), publicationTimeout)
	defer cancel()
	if err := s.features.Publication.Refresh(ctx); err != nil {
		slog.Error("operation_failed", "operation", "publication", "code", "refresh_failed")
		w.Header().Set("X-Publication", "pending")
	}
}

func (s *Server) publicationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/publication", s.require(func(w http.ResponseWriter, r *http.Request) {
		current, err := s.features.Publication.Current(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, map[string]bool{"current": current})
	}))
	mux.HandleFunc("POST /api/admin/publication", s.require(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), publicationTimeout)
		defer cancel()
		if err := s.features.Publication.Refresh(ctx); err != nil {
			slog.Error("operation_failed", "operation", "publication", "code", "refresh_failed")
			write(w, 503, map[string]string{"error": "公開ページへ反映できませんでした。時間をおいて再実行してください。"})
			return
		}
		write(w, 200, map[string]bool{"current": true})
	}))
}
