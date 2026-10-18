package httpapi

import "net/http"

func (s *Server) operationRoutes(mux *http.ServeMux) {
	service := s.features.Backups
	mux.HandleFunc("GET /api/admin/operations", s.require(func(w http.ResponseWriter, r *http.Request) {
		state, err := service.Repository.BackupState(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		records, err := service.Repository.ListBackups(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, map[string]any{"state": state, "backups": records})
	}))
	mux.HandleFunc("POST /api/admin/backups", s.require(func(w http.ResponseWriter, r *http.Request) {
		if err := service.Repository.QueueBackup(r.Context()); err != nil {
			failure(w, err)
			return
		}
		write(w, 202, map[string]bool{"queued": true})
	}))
}
