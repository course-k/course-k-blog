package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blob"
	"coursek/blog/backend/internal/images"
)

var ErrBusy = errors.New("backup is in progress")

type State struct {
	Generation         int64      `json:"generation"`
	BackedUpGeneration int64      `json:"backedUpGeneration"`
	DueAt              *time.Time `json:"dueAt"`
	Running            bool       `json:"running"`
	LastAttempt        *time.Time `json:"lastAttempt"`
	LastSuccess        *time.Time `json:"lastSuccess"`
	LastError          string     `json:"lastError"`
}
type Record struct {
	ID         string    `json:"id"`
	Generation int64     `json:"generation"`
	Key        string    `json:"-"`
	CreatedAt  time.Time `json:"createdAt"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
}
type Repository interface {
	BackupState(context.Context) (State, error)
	QueueBackup(context.Context) error
	ClaimBackup(context.Context, string, bool) (bool, error)
	ExportSnapshot(context.Context) (Snapshot, error)
	CompleteBackup(context.Context, string, Record) error
	FailBackup(context.Context, string, string) error
	ListBackups(context.Context) ([]Record, error)
	ForgetBackup(context.Context, string) error
	BackupRetentionStatus(context.Context, string) error
	ValidateRestore(context.Context, Snapshot) error
	RestoreSnapshot(context.Context, Snapshot) error
}
type Service struct {
	Repository Repository
	Media      images.Blobs
	Archives   images.Blobs
	Retain     int
}
type imageRecord struct {
	ID     string `json:"id"`
	Key    string `json:"object_key"`
	MIME   string `json:"mime"`
	Size   int    `json:"byte_size"`
	SHA256 string `json:"sha256"`
}

func imagesIn(snapshot Snapshot) ([]imageRecord, error) {
	var records []imageRecord
	if json.Unmarshal(snapshot.Tables["images"], &records) != nil {
		return nil, ErrInvalid
	}
	for _, record := range records {
		extension := ".png"
		if record.MIME == "image/jpeg" {
			extension = ".jpg"
		} else if record.MIME != "image/png" {
			return nil, ErrInvalid
		}
		digest, decodeError := hex.DecodeString(record.SHA256)
		if record.Key != "images/"+record.ID+extension || decodeError != nil || len(digest) != 32 || !images.ValidID(record.ID) || record.Size < 1 || record.Size > images.MaxUploadBytes {
			return nil, ErrInvalid
		}
	}
	return records, nil
}
func copyImage(ctx context.Context, from, to images.Blobs, image imageRecord) error {
	// Reusing an immutable object is safe only after checking its content hash.
	existing, err := to.Get(ctx, image.Key)
	if err == nil {
		defer existing.Close()
		body, e := io.ReadAll(io.LimitReader(existing, int64(image.Size)+1))
		sum := sha256.Sum256(body)
		if e != nil || len(body) != image.Size || hex.EncodeToString(sum[:]) != image.SHA256 {
			return errors.New("backup image integrity mismatch")
		}
		return nil
	}
	if !errors.Is(err, blob.ErrNotFound) {
		return errors.New("backup image lookup failed")
	}
	body, err := from.Get(ctx, image.Key)
	if err != nil {
		return errors.New("source image unavailable")
	}
	defer body.Close()
	content, err := io.ReadAll(io.LimitReader(body, int64(image.Size)+1))
	sum := sha256.Sum256(content)
	if err != nil || len(content) != image.Size || hex.EncodeToString(sum[:]) != image.SHA256 {
		return errors.New("source image integrity mismatch")
	}
	return to.Put(ctx, image.Key, content, image.MIME)
}
func (s *Service) Take(ctx context.Context, force bool) (err error) {
	defer func() {
		if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
			slog.Error("operation_failed", "operation", "backup", "code", "snapshot_failed")
		}
	}()
	token, err := auth.Token()
	if err != nil {
		return err
	}
	claimed, err := s.Repository.ClaimBackup(ctx, token, force)
	if err != nil {
		return err
	}
	if !claimed {
		state, err := s.Repository.BackupState(ctx)
		if err != nil {
			return err
		}
		if force && state.Generation > state.BackedUpGeneration {
			return ErrBusy
		}
		s.prune(ctx)
		return nil
	}
	completed := false
	defer func() {
		if !completed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = s.Repository.FailBackup(cleanup, token, "snapshot_failed")
		}
	}()
	snapshot, err := s.Repository.ExportSnapshot(ctx)
	if err != nil {
		return err
	}
	records, err := imagesIn(snapshot)
	if err != nil {
		return err
	}
	for _, image := range records {
		if err = copyImage(ctx, s.Media, s.Archives, image); err != nil {
			return err
		}
	}
	archive, err := Encode(snapshot)
	if err != nil {
		return err
	}
	id, err := auth.Token()
	if err != nil {
		return err
	}
	key := "backups/" + id + ".json.gz"
	if err = s.Archives.Put(ctx, key, archive, "application/gzip"); err != nil {
		return err
	}
	sum := sha256.Sum256(archive)
	if err = s.Repository.CompleteBackup(ctx, token, Record{ID: id, Generation: snapshot.Generation, Key: key, Size: int64(len(archive)), SHA256: hex.EncodeToString(sum[:])}); err != nil {
		return err
	}
	completed = true
	s.prune(ctx)
	return nil
}
func (s *Service) prune(ctx context.Context) {
	records, err := s.Repository.ListBackups(ctx)
	if err == nil {
		retain := s.Retain
		if retain < 1 {
			retain = 4
		}
		for _, record := range records[min(retain, len(records)):] {
			if err = s.Archives.Delete(ctx, record.Key); err != nil {
				break
			}
			if err = s.Repository.ForgetBackup(ctx, record.ID); err != nil {
				break
			}
		}
	}
	code := ""
	if err != nil {
		code = "retention_failed"
		if !errors.Is(ctx.Err(), context.Canceled) {
			slog.Error("operation_failed", "operation", "backup_retention", "code", code)
		}
	}
	if s.Repository.BackupRetentionStatus(ctx, code) != nil {
		slog.Error("backup retention state unavailable")
	}
}
func (s *Service) Restore(ctx context.Context, id string) error {
	if !images.ValidID(id) {
		return ErrInvalid
	}
	body, err := s.Archives.Get(ctx, "backups/"+id+".json.gz")
	if err != nil {
		return err
	}
	defer body.Close()
	content, err := io.ReadAll(io.LimitReader(body, maxArchiveBytes+1))
	if err != nil || len(content) > maxArchiveBytes {
		return ErrInvalid
	}
	snapshot, err := Decode(content)
	if err != nil {
		return err
	}
	if err = s.Repository.ValidateRestore(ctx, snapshot); err != nil {
		return err
	}
	records, err := imagesIn(snapshot)
	if err != nil {
		return err
	}
	for _, image := range records {
		if err = copyImage(ctx, s.Archives, s.Media, image); err != nil {
			return err
		}
	}
	return s.Repository.RestoreSnapshot(ctx, snapshot)
}
func (s *Service) Run(ctx context.Context) {
	timer := time.NewTicker(15 * time.Second)
	defer timer.Stop()
	for {
		work, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if err := s.Take(work, false); err != nil && ctx.Err() == nil {
			slog.Error("backup incomplete; inspect operation state")
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
