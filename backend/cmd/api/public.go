package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/external"
	"coursek/blog/backend/internal/images"
	"coursek/blog/backend/internal/mysql"
	"coursek/blog/backend/internal/publication"
)

// publicSource reads the reader-facing state through the same queries the
// public API uses, so the files and the API cannot disagree.
type publicSource struct {
	store    *mysql.Store
	articles *external.Service
}

func (s publicSource) Page(ctx context.Context, page int, month string) (external.Page, error) {
	return s.articles.Page(ctx, page, month)
}
func (s publicSource) GetPublic(ctx context.Context, id string) (blog.PublicPost, error) {
	return s.store.GetPublic(ctx, id)
}
func (s publicSource) PublicImages(ctx context.Context) ([]images.Image, error) {
	return s.store.PublicImages(ctx)
}

// publicFiles returns nil unless a destination is configured: without one,
// readers are served by this API and no files are written.
func publicFiles(store *mysql.Store, articles *external.Service, media images.Blobs) (*publication.Publisher, error) {
	directory := os.Getenv("PUBLIC_DIRECTORY")
	if directory == "" {
		return nil, nil
	}
	publisher := &publication.Publisher{Source: publicSource{store, articles}, Media: media, Target: publication.Directory{Path: directory}}
	articles.Changed = func(ctx context.Context) {
		work, done := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer done()
		if err := publisher.Refresh(work); err != nil {
			slog.Error("operation_failed", "operation", "publication", "code", "refresh_failed")
		}
	}
	return publisher, nil
}
