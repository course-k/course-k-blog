package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/images"
)

func (s *Server) imageRoutes(mux *http.ServeMux) {
	service := s.features.Images
	// Legacy base64 route kept for compatibility; the editor uses image-uploads.
	mux.HandleFunc("POST /api/admin/images", s.require(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Data []byte `json:"data"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 7*1024*1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
			write(w, 400, map[string]string{"error": "画像を読み取れませんでした。"})
			return
		}
		image, err := service.Register(r.Context(), input.Data, s.now().UTC())
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 201, map[string]string{"id": image.ID, "url": "/api/images/" + image.ID})
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
	if s.features.Uploads != nil {
		s.uploadRoutes(mux)
	}
}

type uploadResponse struct {
	UploadID    string         `json:"uploadId"`
	Status      string         `json:"status"`
	Upload      *images.Target `json:"upload,omitempty"`
	Result      *images.Result `json:"result,omitempty"`
	FailureCode string         `json:"failureCode,omitempty"`
	Error       string         `json:"error,omitempty"`
}

func uploadBody(upload images.Upload) uploadResponse {
	body := uploadResponse{UploadID: upload.ID, Status: upload.Status, Result: upload.Result(), FailureCode: upload.FailureCode}
	if upload.Status == images.UploadFailed {
		body.Error = images.Message(upload.FailureCode)
	}
	return body
}

func uploadFailure(w http.ResponseWriter, upload images.Upload, err error) {
	switch {
	case errors.Is(err, images.ErrUploadNotFound):
		write(w, 404, map[string]string{"error": "not found"})
	case errors.Is(err, images.ErrUploadExpired):
		write(w, 410, uploadResponse{UploadID: upload.ID, Status: images.UploadExpired, Error: "画像の送信期限（24時間）を過ぎました。再試行してください。"})
	case errors.Is(err, images.ErrUploadProcessing):
		write(w, 409, uploadResponse{UploadID: upload.ID, Status: images.UploadProcessing, Error: "画像を処理しています。状態を確認してください。"})
	case errors.Is(err, images.ErrUploadMissing):
		write(w, 409, uploadResponse{UploadID: upload.ID, Status: images.UploadPending, FailureCode: "not_uploaded", Error: "画像の送信が完了していません。"})
	case errors.Is(err, images.ErrUploadReused):
		write(w, 409, map[string]string{"error": "同じ画像IDで異なる画像が送信されました。"})
	default:
		failure(w, err)
	}
}

func (s *Server) uploadRoutes(mux *http.ServeMux) {
	uploads := s.features.Uploads
	mux.HandleFunc("POST /api/admin/image-uploads", s.require(func(w http.ResponseWriter, r *http.Request) {
		var in images.UploadRequest
		if !decode(w, r, &in) {
			return
		}
		if !images.ValidRequest(in) {
			write(w, 400, map[string]string{"error": "PNG・JPEG・WebP、1〜10,000,000バイトの画像を指定してください。"})
			return
		}
		upload, target, err := uploads.Begin(r.Context(), in)
		if err != nil {
			uploadFailure(w, upload, err)
			return
		}
		body := uploadBody(upload)
		body.Upload = target
		write(w, 200, body)
	}))
	mux.HandleFunc("GET /api/admin/image-uploads/{id}", s.require(func(w http.ResponseWriter, r *http.Request) {
		upload, err := uploads.Status(r.Context(), r.PathValue("id"))
		if err != nil {
			uploadFailure(w, upload, err)
			return
		}
		write(w, 200, uploadBody(upload))
	}))
	mux.HandleFunc("POST /api/admin/image-uploads/{id}/complete", s.require(func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !decode(w, r, &in) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), images.CompletionTimeout)
		defer cancel()
		upload, err := uploads.Complete(ctx, r.PathValue("id"))
		if err != nil {
			uploadFailure(w, upload, err)
			return
		}
		status := 200
		if upload.Status == images.UploadFailed {
			status = 400
		}
		write(w, status, uploadBody(upload))
	}))
	if s.features.LocalUploads {
		// The only route that accepts raw bytes; it exists only outside production.
		mux.HandleFunc("PUT /api/admin/image-uploads/{id}/content", s.require(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, images.MaxUploadBytes))
			if err != nil {
				write(w, 413, map[string]string{"error": images.Message(images.ErrTooLarge.Code)})
				return
			}
			if err = uploads.Receive(r.Context(), r.PathValue("id"), body); err != nil {
				if errors.Is(err, images.ErrInvalid) {
					write(w, 400, map[string]string{"error": "申告したサイズと送信した画像のサイズが一致しません。"})
					return
				}
				uploadFailure(w, images.Upload{ID: r.PathValue("id")}, err)
				return
			}
			write(w, 200, map[string]bool{"stored": true})
		}))
	}
}
