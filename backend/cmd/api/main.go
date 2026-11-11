package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"coursek/blog/backend/internal/backup"
	"coursek/blog/backend/internal/blob"
	"coursek/blog/backend/internal/external"
	"coursek/blog/backend/internal/httpapi"
	"coursek/blog/backend/internal/images"
	"coursek/blog/backend/internal/mysql"
	"coursek/blog/backend/internal/oidclogin"
	"coursek/blog/backend/internal/releasecheck"
)

// Health checks must not initialize a DB connection or require credentials.
func checkHealth(endpoint string) error {
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(endpoint)
	if err != nil {
		return errors.New("health endpoint unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("health endpoint not ready")
	}
	return nil
}

func run() error {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		return checkHealth("http://127.0.0.1:8081/api/healthz")
	}

	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case "serve", "migrate", "backup", "restore", "revoke-sessions", "release-check", "schema-version", "verify-schema", "refresh-public":
	default:
		return errors.New("unknown application command")
	}
	if (command == "serve" || command == "migrate") && len(os.Args) > 2 {
		return errors.New("unexpected application arguments")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" && os.Getenv("DATABASE_PASSWORD_FILE") != "" {
		password, err := os.ReadFile(os.Getenv("DATABASE_PASSWORD_FILE"))
		if err != nil {
			return errors.New("cannot read database password file")
		}
		u := &url.URL{Scheme: "mysql", Host: os.Getenv("DATABASE_HOST"), Path: "/" + os.Getenv("DATABASE_NAME"), User: url.UserPassword(os.Getenv("DATABASE_USER"), strings.TrimSpace(string(password)))}
		q := u.Query()
		q.Set("tls", os.Getenv("DATABASE_TLS"))
		u.RawQuery = q.Encode()
		dsn = u.String()
	}
	if dsn == "" {
		return errors.New("DATABASE_URL is required; no in-memory fallback")
	}
	origin := os.Getenv("APP_ORIGIN")
	if origin == "" {
		return errors.New("APP_ORIGIN is required")
	}
	u, e := url.Parse(origin)
	if e != nil {
		return e
	}
	if u.Scheme == "https" {
		dbURL, e := url.Parse(dsn)
		if e != nil || dbURL.Scheme != "mysql" || dbURL.Query().Get("tls") != "verify-full" {
			return errors.New("HTTPS deployment requires a MySQL URL with tls=verify-full")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := mysql.Open(ctx, dsn)
	if err != nil {
		return errors.New("cannot connect to MySQL")
	}
	defer store.Close()
	if command == "verify-schema" {
		if len(os.Args) != 2 {
			return errors.New("unexpected schema arguments")
		}
		if err := store.VerifySchema(ctx); err != nil {
			return errors.New("schema verification failed")
		}
		slog.Info("schema_verified", "version", mysql.LatestSchemaVersion())
		return nil
	}
	if command == "schema-version" {
		if len(os.Args) != 2 {
			return errors.New("unexpected schema arguments")
		}
		version, err := store.SchemaVersion(ctx)
		if err != nil {
			return errors.New("schema query failed")
		}
		slog.Info("schema_version", "version", version)
		return nil
	}
	if len(os.Args) == 2 && os.Args[1] == "migrate" {
		return store.Migrate(ctx)
	}
	if command == "revoke-sessions" {
		work, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		return runDataCommand(work, command, os.Args[2:], dsn, store, nil)
	}
	objects, archives, err := storage(ctx, u.Scheme == "https")
	if err != nil {
		return err
	}
	retain, err := backupRetention()
	if err != nil {
		return err
	}
	backupService := &backup.Service{Repository: store, Media: objects, Archives: archives, Retain: retain}
	if command == "restore" || command == "backup" {
		work, done := context.WithTimeout(context.Background(), 15*time.Minute)
		defer done()
		return runDataCommand(work, command, os.Args[2:], dsn, store, backupService)
	}
	if command == "release-check" {
		if len(os.Args) != 3 || os.Args[2] != "--confirm-production" || origin != releasecheck.Origin || os.Getenv("ECS_CONTAINER_METADATA_URI_V4") == "" {
			return errors.New("explicit production ECS check required")
		}
		work, done := context.WithTimeout(context.Background(), 5*time.Minute)
		defer done()
		check := releasecheck.Check{Store: store, Bucket: os.Getenv("MEDIA_BUCKET"), Schema: mysql.LatestSchemaVersion(), DeleteBlob: objects.Delete, Sync: os.Getenv("RELEASE_CHECK_SYNC") == "true", Backup: func(c context.Context) error {
			for attempt := 0; attempt < 12; attempt++ {
				err := backupService.Take(c, true)
				if err != nil && !errors.Is(err, backup.ErrBusy) {
					return err
				}
				state, e := store.BackupState(c)
				if e != nil {
					return e
				}
				if state.Generation == state.BackedUpGeneration && state.LastError == "" {
					return nil
				}
				select {
				case <-c.Done():
					return c.Err()
				case <-time.After(3 * time.Second):
				}
			}
			return errors.New("backup did not finish")
		}}
		if err := check.Run(work); err != nil {
			return err
		}
		slog.Info("production_checks_passed", "schema", mysql.LatestSchemaVersion(), "pngBytes", 10000000, "sync", check.Sync)
		return nil
	}
	externalService := &external.Service{Repository: store, Client: external.NewClient()}
	publisher, err := publicFiles(ctx, store, externalService, objects)
	if err != nil {
		return err
	}
	if command == "refresh-public" {
		if len(os.Args) != 2 || publisher == nil {
			return errors.New("refresh-public takes no arguments and needs PUBLIC_DIRECTORY or PUBLIC_BUCKET")
		}
		work, done := context.WithTimeout(context.Background(), 5*time.Minute)
		defer done()
		if err := publisher.Refresh(work); err != nil {
			return errors.New("public files could not be refreshed")
		}
		slog.Info("public_files_refreshed")
		return nil
	}
	hash := ""
	if os.Getenv("OIDC_ISSUER") == "" {
		hash = os.Getenv("ADMIN_PASSWORD_HASH")
		if hash == "" && os.Getenv("ADMIN_PASSWORD_HASH_FILE") != "" {
			content, err := os.ReadFile(os.Getenv("ADMIN_PASSWORD_HASH_FILE"))
			if err != nil {
				return errors.New("cannot read admin password hash file")
			}
			hash = strings.TrimSpace(string(content))
		}
	}
	imageService := &images.Service{Repository: store, Blobs: objects}
	uploadService := &images.UploadService{Repository: store, Blobs: objects, Staging: images.LocalStaging{}}
	_, localUploads := objects.(blob.Files)
	if signer, ok := objects.(*blob.S3); ok {
		uploadService.Staging = images.SignedStaging{Signer: signer}
	} else if !localUploads {
		return errors.New("image upload staging unavailable")
	}
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	defer func() { stopWorkers(); workers.Wait() }()
	// The sync worker only processes explicit owner requests (and resumes an
	// interrupted one); there is no scheduled sync. Disable it only for offline tests.
	if os.Getenv("EXTERNAL_SYNC_ENABLED") == "true" {
		workers.Go(func() { externalService.Run(workerCtx) })
	}
	workers.Go(func() { uploadService.RunSweeper(workerCtx) })
	if os.Getenv("BACKUP_ENABLED") == "true" {
		workers.Go(func() { backupService.Run(workerCtx) })
	}
	var identity *oidclogin.Service
	if issuer := os.Getenv("OIDC_ISSUER"); issuer != "" {
		identity, err = oidclogin.New(ctx, oidclogin.Config{Issuer: issuer, ClientID: os.Getenv("OIDC_CLIENT_ID"), AdminSubject: os.Getenv("OIDC_ADMIN_SUBJECT"), RedirectURL: origin + "/api/auth/callback"}, store)
		if err != nil {
			return err
		}
	} else if u.Scheme == "https" {
		return errors.New("OIDC configuration is required for public administration")
	}
	if publisher != nil {
		// Files may be missing after a first start or stale after an interrupted
		// run. Refreshing is a no-op when they already match the database.
		work, done := context.WithTimeout(context.Background(), time.Minute)
		if err := publisher.Refresh(work); err != nil {
			slog.Error("operation_failed", "operation", "publication", "code", "refresh_failed")
		}
		done()
	}
	features := httpapi.Features{External: externalService, Images: imageService, Uploads: uploadService, LocalUploads: localUploads, Backups: backupService, Identity: identity, Creations: store}
	if publisher != nil {
		features.Publication = publisher
	}
	handler, err := httpapi.NewWithFeatures(store, origin, hash, features)
	if err != nil {
		return err
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8081"
	}
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopped)
	go func() {
		<-stopped
		stopWorkers()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	slog.Info("API starting", "address", addr)
	if err = srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
