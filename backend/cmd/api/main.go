package main

import (
	"context"
	"coursek/blog/backend/internal/httpapi"
	"coursek/blog/backend/internal/postgres"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func run() error {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		return errors.New("cannot connect to PostgreSQL")
	}
	defer store.Close()
	if len(os.Args) == 2 && os.Args[1] == "migrate" {
		return store.Migrate(ctx)
	}
	hash := os.Getenv("ADMIN_PASSWORD_HASH")
	if hash == "" && os.Getenv("ADMIN_PASSWORD_HASH_FILE") != "" {
		b, err := os.ReadFile(os.Getenv("ADMIN_PASSWORD_HASH_FILE"))
		if err != nil {
			return errors.New("cannot read admin password hash file")
		}
		hash = strings.TrimSpace(string(b))
	}
	handler, err := httpapi.New(store, origin, hash)
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
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
