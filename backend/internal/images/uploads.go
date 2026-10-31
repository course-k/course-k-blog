package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blob"
)

// Direct uploads keep image bytes out of the JSON API path. The browser sends
// the file to a private staging key; completion verifies it, re-encodes it and
// registers the official image exactly once per upload session.
const (
	UploadSessionLifetime = 24 * time.Hour
	SignatureLifetime     = 5 * time.Minute
	CompletionTimeout     = 20 * time.Second
	completionLease       = 45 * time.Second
	// Completed sessions are kept as long as the editor may restore a pending
	// image, so the same clientImageId resolves to the same official image.
	uploadRecordRetention = 31 * 24 * time.Hour
)

var (
	ErrUploadExpired    = errors.New("upload session expired")
	ErrUploadProcessing = errors.New("upload is being processed")
	ErrUploadMissing    = errors.New("uploaded bytes not found")
	ErrUploadReused     = errors.New("client image id reused with other metadata")
	ErrUploadNotFound   = errors.New("upload not found")
	ErrLeaseLost        = errors.New("upload lease lost")
)

const (
	UploadPending    = "pending"
	UploadProcessing = "processing"
	UploadCompleted  = "completed"
	UploadFailed     = "failed"
	UploadExpired    = "expired"
)

var (
	clientImageIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)
	sha256Pattern        = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type UploadRequest struct {
	ClientImageID string `json:"clientImageId"`
	FileName      string `json:"fileName"`
	ContentType   string `json:"contentType"`
	ByteSize      int64  `json:"byteSize"`
	SHA256        string `json:"sha256"`
}

type Upload struct {
	ID            string
	ClientImageID string
	FileName      string
	MIME          string
	Size          int64
	SHA256        string
	StagingKey    string
	Status        string
	ImageID       string
	ImageKey      string
	FailureCode   string
	Width, Height int
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// Result is the registered image returned by complete and status.
type Result struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func (u Upload) Result() *Result {
	if u.Status != UploadCompleted {
		return nil
	}
	return &Result{ID: u.ImageID, URL: "/api/images/" + u.ImageID, Width: u.Width, Height: u.Height}
}

// Target tells the browser how to send bytes: a signed S3 POST form in
// production, or an authenticated same-origin PUT outside production.
type Target struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Fields    map[string]string `json:"fields,omitempty"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

type Staging interface {
	Target(ctx context.Context, upload Upload, lifetime time.Duration) (Target, error)
}

type UploadRepository interface {
	// CreateUpload inserts the session or returns the existing one for the same
	// clientImageId; differing metadata is ErrUploadReused.
	CreateUpload(context.Context, Upload) (Upload, error)
	GetUpload(context.Context, string) (Upload, error)
	// LeaseUpload claims a pending upload, or a processing one whose lease expired.
	LeaseUpload(ctx context.Context, id, leaseID string, until time.Time) (bool, error)
	// SetUploadKey fixes the official key once; an existing key is kept and returned.
	SetUploadKey(ctx context.Context, id, leaseID, key string) (string, error)
	ReleaseUpload(ctx context.Context, id, leaseID string) error
	CompleteUpload(ctx context.Context, id, leaseID string, image Image, width, height int) error
	FailUpload(ctx context.Context, id, leaseID, code string) error
	ExpireUploads(ctx context.Context, now time.Time) ([]Upload, error)
	PurgeUploads(ctx context.Context, before time.Time) error
}

type UploadService struct {
	Repository UploadRepository
	Blobs      Blobs
	Staging    Staging
	Now        func() time.Time
}

func (s *UploadService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func cleanFileName(name string) (string, bool) {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 255 {
		return "", false
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, name)
	return strings.TrimSpace(name), true
}

// ValidRequest checks the declared metadata; the bytes are verified on completion.
func ValidRequest(in UploadRequest) bool {
	_, nameOK := cleanFileName(in.FileName)
	return clientImageIDPattern.MatchString(in.ClientImageID) && nameOK &&
		(in.ContentType == "image/png" || in.ContentType == "image/jpeg" || in.ContentType == "image/webp") &&
		in.ByteSize >= 1 && in.ByteSize <= MaxUploadBytes && sha256Pattern.MatchString(in.SHA256)
}

// Begin creates or resumes the session for clientImageId. A completed session
// returns its result instead of a target; a pending one gets a fresh signature.
func (s *UploadService) Begin(ctx context.Context, in UploadRequest) (Upload, *Target, error) {
	if !ValidRequest(in) {
		return Upload{}, nil, ErrInvalid
	}
	name, _ := cleanFileName(in.FileName)
	id, err := auth.Token()
	if err != nil {
		return Upload{}, nil, err
	}
	imageID, err := auth.Token()
	if err != nil {
		return Upload{}, nil, err
	}
	now := s.now()
	upload, err := s.Repository.CreateUpload(ctx, Upload{
		ID: id, ClientImageID: in.ClientImageID, FileName: name, MIME: in.ContentType, Size: in.ByteSize,
		SHA256: in.SHA256, StagingKey: "images/staging/" + id, Status: UploadPending, ImageID: imageID,
		CreatedAt: now, ExpiresAt: now.Add(UploadSessionLifetime),
	})
	if err != nil {
		return Upload{}, nil, err
	}
	switch {
	case upload.Status == UploadCompleted || upload.Status == UploadFailed || upload.Status == UploadProcessing:
		return upload, nil, nil
	case upload.Status == UploadExpired || !now.Before(upload.ExpiresAt):
		return upload, nil, ErrUploadExpired
	}
	target, err := s.Staging.Target(ctx, upload, SignatureLifetime)
	if err != nil {
		return Upload{}, nil, err
	}
	return upload, &target, nil
}

func (s *UploadService) Status(ctx context.Context, id string) (Upload, error) {
	if !ValidID(id) {
		return Upload{}, ErrUploadNotFound
	}
	upload, err := s.Repository.GetUpload(ctx, id)
	if err == nil && upload.Status == UploadPending && !s.now().Before(upload.ExpiresAt) {
		upload.Status = UploadExpired
	}
	return upload, err
}

// Complete verifies the staged bytes and registers the official image. Repeated
// calls, lost DB responses and restarts resolve to the same image ID and key.
func (s *UploadService) Complete(ctx context.Context, id string) (Upload, error) {
	upload, err := s.Status(ctx, id)
	if err != nil {
		return Upload{}, err
	}
	switch upload.Status {
	case UploadCompleted, UploadFailed:
		return upload, nil
	case UploadExpired:
		return upload, ErrUploadExpired
	}
	leaseID, err := auth.Token()
	if err != nil {
		return Upload{}, err
	}
	leased, err := s.Repository.LeaseUpload(ctx, id, leaseID, s.now().Add(completionLease))
	if err != nil {
		return Upload{}, err
	}
	if !leased {
		current, err := s.Repository.GetUpload(ctx, id)
		if err == nil && (current.Status == UploadCompleted || current.Status == UploadFailed) {
			return current, nil
		}
		return upload, ErrUploadProcessing
	}
	release := func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.Repository.ReleaseUpload(cleanup, id, leaseID)
	}
	fail := func(code string) (Upload, error) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.Repository.FailUpload(cleanup, id, leaseID, code); err != nil {
			return Upload{}, err
		}
		upload.Status, upload.FailureCode = UploadFailed, code
		return upload, nil
	}
	reader, err := s.Blobs.Get(ctx, upload.StagingKey)
	if err != nil {
		release()
		if errors.Is(err, blob.ErrNotFound) {
			return upload, ErrUploadMissing
		}
		return Upload{}, err
	}
	body, err := io.ReadAll(io.LimitReader(reader, upload.Size+1))
	reader.Close()
	if err != nil {
		release()
		return Upload{}, err
	}
	digest := sha256.Sum256(body)
	switch {
	case int64(len(body)) != upload.Size:
		return fail("size_mismatch")
	case hex.EncodeToString(digest[:]) != upload.SHA256:
		return fail("checksum_mismatch")
	}
	mime := StoredMIME(body)
	if mime == "" {
		return fail(ErrFormat.Code)
	}
	extension := ".png"
	if mime == "image/jpeg" {
		extension = ".jpg"
	}
	// The official key is fixed before any object is written.
	key, err := s.Repository.SetUploadKey(ctx, id, leaseID, "images/"+upload.ImageID+extension)
	if err != nil {
		release()
		return Upload{}, err
	}
	output, outputMIME, err := Sanitize(body)
	var rejection Rejection
	if errors.As(err, &rejection) {
		return fail(rejection.Code)
	}
	if err != nil {
		release()
		return Upload{}, err
	}
	if err = s.Blobs.Put(ctx, key, output, outputMIME); errors.Is(err, blob.ErrExists) {
		// A previous attempt stored the object but lost the DB result. Adopt it
		// rather than deleting an image the owner may already reference.
		output, err = s.readObject(ctx, key)
	}
	if err != nil {
		release()
		return Upload{}, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(output))
	if err != nil {
		release()
		return Upload{}, err
	}
	sum := sha256.Sum256(output)
	registered := Image{ID: upload.ImageID, Key: key, MIME: outputMIME, Size: len(output), SHA256: hex.EncodeToString(sum[:]), CreatedAt: s.now()}
	if err = s.Repository.CompleteUpload(ctx, id, leaseID, registered, config.Width, config.Height); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			if current, readErr := s.Repository.GetUpload(ctx, id); readErr == nil && current.Status == UploadCompleted {
				return current, nil
			}
			return upload, ErrUploadProcessing
		}
		return Upload{}, err
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.Blobs.Delete(cleanup, upload.StagingKey) // lifecycle removes leftovers after one day
	upload.Status, upload.ImageKey, upload.Width, upload.Height = UploadCompleted, key, config.Width, config.Height
	return upload, nil
}

func (s *UploadService) readObject(ctx context.Context, key string) ([]byte, error) {
	reader, err := s.Blobs.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, MaxUploadBytes+1))
	if err != nil || len(body) > MaxUploadBytes {
		return nil, errors.New("existing object unreadable")
	}
	return body, nil
}

// Sweep expires unfinished sessions after 24 hours, removes their staged bytes
// where possible and forgets old session records. Official images are untouched.
func (s *UploadService) Sweep(ctx context.Context) error {
	now := s.now()
	expired, err := s.Repository.ExpireUploads(ctx, now)
	if err != nil {
		return err
	}
	for _, upload := range expired {
		_ = s.Blobs.Delete(ctx, upload.StagingKey)
	}
	return s.Repository.PurgeUploads(ctx, now.Add(-uploadRecordRetention))
}

func (s *UploadService) RunSweeper(ctx context.Context) {
	timer := time.NewTicker(time.Hour)
	defer timer.Stop()
	for {
		work, cancel := context.WithTimeout(ctx, time.Minute)
		_ = s.Sweep(work)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

// Presigner is implemented by blob.S3.
type Presigner interface {
	PresignStagingPost(ctx context.Context, key, mime string, size int64, lifetime time.Duration) (string, map[string]string, error)
}

// SignedStaging sends bytes straight to the private bucket with a signed POST.
type SignedStaging struct{ Signer Presigner }

func (s SignedStaging) Target(ctx context.Context, upload Upload, lifetime time.Duration) (Target, error) {
	url, fields, err := s.Signer.PresignStagingPost(ctx, upload.StagingKey, upload.MIME, upload.Size, lifetime)
	if err != nil {
		return Target{}, err
	}
	return Target{Method: "POST", URL: url, Fields: fields, ExpiresAt: time.Now().UTC().Add(lifetime)}, nil
}

// LocalStaging replaces S3 outside production with an authenticated binary PUT
// to the same API. It is only registered when objects are stored in files.
type LocalStaging struct{}

func (LocalStaging) Target(_ context.Context, upload Upload, lifetime time.Duration) (Target, error) {
	return Target{Method: "PUT", URL: "/api/admin/image-uploads/" + upload.ID + "/content", ExpiresAt: time.Now().UTC().Add(lifetime)}, nil
}

// Receive stores bytes for a pending local upload. Re-sending replaces bytes
// that were never completed.
func (s *UploadService) Receive(ctx context.Context, id string, body []byte) error {
	upload, err := s.Status(ctx, id)
	if err != nil {
		return err
	}
	switch upload.Status {
	case UploadPending:
	case UploadExpired:
		return ErrUploadExpired
	default:
		return ErrUploadProcessing
	}
	if int64(len(body)) != upload.Size {
		return ErrInvalid
	}
	err = s.Blobs.Put(ctx, upload.StagingKey, body, upload.MIME)
	if errors.Is(err, blob.ErrExists) {
		if err = s.Blobs.Delete(ctx, upload.StagingKey); err == nil {
			err = s.Blobs.Put(ctx, upload.StagingKey, body, upload.MIME)
		}
	}
	return err
}

// Register validates, normalizes and stores one image (legacy JSON route).
func (s *Service) Register(ctx context.Context, input []byte, now time.Time) (Image, error) {
	body, mime, err := Sanitize(input)
	if err != nil {
		return Image{}, err
	}
	id, err := auth.Token()
	if err != nil {
		return Image{}, err
	}
	suffix := ".png"
	if mime == "image/jpeg" {
		suffix = ".jpg"
	}
	key := "images/" + id + suffix
	if err = s.Blobs.Put(ctx, key, body, mime); err != nil {
		return Image{}, err
	}
	digest := sha256.Sum256(body)
	image := Image{ID: id, Key: key, MIME: mime, Size: len(body), SHA256: hex.EncodeToString(digest[:]), CreatedAt: now}
	// A lost DB response can follow a committed registration. Keep the private
	// object rather than deleting an asset that the DB might already reference.
	return image, s.Repository.SaveImage(ctx, image)
}
