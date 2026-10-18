package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"coursek/blog/backend/internal/blob"
	"coursek/blog/backend/internal/images"
)

func storage(ctx context.Context, production bool) (images.Blobs, images.Blobs, error) {
	var media, archives images.Blobs
	mediaBucket, backupBucket := os.Getenv("MEDIA_BUCKET"), os.Getenv("BACKUP_BUCKET")
	if production && (mediaBucket == "" || backupBucket == "") {
		return nil, nil, errors.New("separate MEDIA_BUCKET and BACKUP_BUCKET are required for production")
	}
	if mediaBucket != "" && mediaBucket == backupBucket {
		return nil, nil, errors.New("backup and media buckets must be separate")
	}
	var err error
	if mediaBucket != "" {
		media, err = blob.NewS3(ctx, mediaBucket)
	} else {
		directory := os.Getenv("MEDIA_DIRECTORY")
		if directory == "" {
			directory = "./config/media"
		}
		media = blob.Files{Directory: directory}
	}
	if err != nil {
		return nil, nil, err
	}
	if backupBucket != "" {
		archives, err = blob.NewS3(ctx, backupBucket)
	} else {
		directory := os.Getenv("BACKUP_DIRECTORY")
		if directory == "" {
			directory = "./config/backups"
		}
		archives = blob.Files{Directory: directory}
	}
	if mediaFiles, ok := media.(blob.Files); ok {
		if archiveFiles, ok := archives.(blob.Files); ok {
			mediaPath, mediaError := filepath.Abs(mediaFiles.Directory)
			archivePath, archiveError := filepath.Abs(archiveFiles.Directory)
			if mediaError != nil || archiveError != nil || mediaPath == archivePath {
				return nil, nil, errors.New("separate media and backup directories required")
			}
		}
	}
	return media, archives, err
}
func backupRetention() (int, error) {
	value := os.Getenv("BACKUP_RETAIN")
	if value == "" {
		return 4, nil
	}
	count, err := strconv.Atoi(value)
	if err != nil || count < 1 || count > 30 {
		return 0, errors.New("BACKUP_RETAIN must be between 1 and 30")
	}
	return count, nil
}
