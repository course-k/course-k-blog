package main

import (
	"context"
	"time"

	"coursek/blog/backend/internal/backup"
	"coursek/blog/backend/internal/external"
	"coursek/blog/backend/internal/images"
	"coursek/blog/backend/internal/ondemand"
	"coursek/blog/backend/internal/publication"
)

// A request may arrive while the database is paused; resuming takes about 15
// seconds and can take 30 or more after a long pause.
const onDemandRequestTimeout = 45 * time.Second

// The owner can queue one sync per source before the work invocation runs.
const syncSources = 3

// pendingWork lists what the timers of the long-running server do, for one
// work invocation: accepted syncs, a due backup, expired uploads, and reader
// files that an interrupted run left stale.
func pendingWork(syncEnabled, backupEnabled bool, syncs *external.Service, backups *backup.Service, uploads *images.UploadService, publisher *publication.Publisher) ondemand.Worker {
	worker := ondemand.Worker{}
	if syncEnabled {
		worker = append(worker, ondemand.Task{Name: "external_sync", Run: func(ctx context.Context) error {
			for range syncSources {
				if err := syncs.RunOnce(ctx); err != nil {
					return err
				}
			}
			return nil
		}})
	}
	if publisher != nil {
		worker = append(worker, ondemand.Task{Name: "publication", Run: publisher.Refresh})
	}
	if backupEnabled {
		worker = append(worker, ondemand.Task{Name: "backup", Run: func(ctx context.Context) error { return backups.Take(ctx, false) }})
	}
	return append(worker, ondemand.Task{Name: "upload_sweep", Run: uploads.Sweep})
}
