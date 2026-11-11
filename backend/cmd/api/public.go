package main

import (
	"context"
	"errors"
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

// publicTarget picks the local directory or the bucket. Naming both is a
// configuration mistake: files would be written to one and served from the other.
func publicTarget(ctx context.Context, directory, bucket string) (publication.Target, error) {
	switch {
	case directory != "" && bucket != "":
		return nil, errors.New("set only one of PUBLIC_DIRECTORY and PUBLIC_BUCKET")
	case bucket != "":
		return publication.NewBucket(ctx, bucket)
	case directory != "":
		return publication.Directory{Path: directory}, nil
	}
	return nil, nil
}

// publicFiles returns nil unless a destination is configured: without one,
// readers are served by this API and no files are written.
func publicFiles(ctx context.Context, store *mysql.Store, articles *external.Service, media images.Blobs) (*publication.Publisher, error) {
	target, err := publicTarget(ctx, os.Getenv("PUBLIC_DIRECTORY"), os.Getenv("PUBLIC_BUCKET"))
	if target == nil || err != nil {
		return nil, err
	}
	publisher := &publication.Publisher{Source: publicSource{store, articles}, Media: media, Target: target}
	// Without a distribution nothing is cached in front of the files.
	if id := os.Getenv("PUBLIC_DISTRIBUTION_ID"); id != "" {
		distribution, err := publication.NewDistribution(ctx, id)
		if err != nil {
			return nil, err
		}
		publisher.Purge = distribution.Purge
	}
	articles.Changed = func(ctx context.Context) {
		work, done := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer done()
		if err := publisher.Refresh(work); err != nil {
			slog.Error("operation_failed", "operation", "publication", "code", "refresh_failed")
		}
	}
	return publisher, nil
}
