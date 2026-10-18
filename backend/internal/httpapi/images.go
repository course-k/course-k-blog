package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/images"
)

func (s *Server) imageRoutes(mux *http.ServeMux) {
	service := s.features.Images
	mux.HandleFunc("POST /api/admin/images", s.require(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Data []byte `json:"data"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 7*1024*1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
			write(w, 400, map[string]string{"error": "画像を読み取れませんでした。5MiB以内のPNG/JPEGを選んでください。"})
			return
		}
		body, mime, err := images.Sanitize(input.Data)
		if err != nil {
			failure(w, err)
			return
		}
		id, err := auth.Token()
		if err != nil {
			failure(w, err)
			return
		}
		suffix := ".png"
		if mime == "image/jpeg" {
			suffix = ".jpg"
		}
		key := "images/" + id + suffix
		if err = service.Blobs.Put(r.Context(), key, body, mime); err != nil {
			failure(w, err)
			return
		}
		digest := sha256.Sum256(body)
		image := images.Image{ID: id, Key: key, MIME: mime, Size: len(body), SHA256: hex.EncodeToString(digest[:]), CreatedAt: s.now().UTC()}
		if err = service.Repository.SaveImage(r.Context(), image); err != nil {
			// A lost DB response can follow a committed registration. Keep the private
			// object rather than deleting an asset that the DB might already reference.
			failure(w, err)
			return
		}
		write(w, 201, map[string]string{"id": id, "url": "/api/images/" + id})
	}))
	mux.HandleFunc("GET /api/images/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !images.ValidID(id) {
			write(w, 404, map[string]string{"error": "not found"})
			return
		}
		visible, err := service.Repository.ImagePublic(r.Context(), id)
		if err != nil {
			failure(w, err)
			return
		}
		if !visible {
			cookie, cookieError := r.Cookie(sessionCookie)
			if cookieError != nil || len(cookie.Value) > 128 {
				write(w, 404, map[string]string{"error": "not found"})
				return
			}
			owner, err := s.store.HasSession(r.Context(), auth.Digest(cookie.Value), s.now())
			if err != nil {
				failure(w, err)
				return
			}
			if !owner {
				write(w, 404, map[string]string{"error": "not found"})
				return
			}
		}
		metadata, err := service.Repository.GetImage(r.Context(), id)
		if err != nil {
			failure(w, err)
			return
		}
		body, err := service.Blobs.Get(r.Context(), metadata.Key)
		if err != nil {
			failure(w, err)
			return
		}
		defer body.Close()
		w.Header().Set("Content-Type", metadata.MIME)
		w.Header().Set("Content-Disposition", "inline")
		_, _ = io.Copy(w, body)
	})
}
