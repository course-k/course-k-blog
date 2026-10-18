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
	"coursek/blog/backend/internal/external"
	"coursek/blog/backend/internal/httpapi"
	"coursek/blog/backend/internal/images"
	"coursek/blog/backend/internal/oidclogin"
	"coursek/blog/backend/internal/postgres"
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
	case "serve", "migrate", "backup", "restore", "revoke-sessions":
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
		u := &url.URL{Scheme: "postgres", Host: os.Getenv("DATABASE_HOST"), Path: "/" + os.Getenv("DATABASE_NAME"), User: url.UserPassword(os.Getenv("DATABASE_USER"), strings.TrimSpace(string(password)))}
		q := u.Query()
		q.Set("sslmode", os.Getenv("DATABASE_SSLMODE"))
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
		if e != nil || dbURL.Scheme != "postgres" && dbURL.Scheme != "postgresql" || dbURL.Query().Get("sslmode") != "verify-full" {
			return errors.New("HTTPS deployment requires a PostgreSQL URL with sslmode=verify-full")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		return errors.New("cannot connect to PostgreSQL")
	}
	defer store.Close()
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
	externalService := &external.Service{Repository: store, Client: external.NewClient()}
	imageService := &images.Service{Repository: store, Blobs: objects}
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	defer func() { stopWorkers(); workers.Wait() }()
	if os.Getenv("EXTERNAL_SYNC_ENABLED") == "true" {
		workers.Go(func() { externalService.Run(workerCtx) })
	}
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
	handler, err := httpapi.NewWithFeatures(store, origin, hash, httpapi.Features{External: externalService, Images: imageService, Backups: backupService, Identity: identity, Creations: store})
	if err != nil {
		return err
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8081"
	}
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
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
