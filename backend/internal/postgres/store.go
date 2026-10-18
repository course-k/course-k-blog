package postgres

import (
	"context"
	"coursek/blog/backend/internal/blog"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"time"
)

//go:embed migrations/001_initial.sql
var initial string

type Store struct{ db *sql.DB }

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}
func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Migrations are an explicit command, never a side effect of starting a task.
func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(406188019)"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version integer PRIMARY KEY, checksum text NOT NULL)"); err != nil {
		return err
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(initial)))
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version=1").Scan(&existing)
	if err == nil {
		if existing != checksum {
			return errors.New("migration checksum changed")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, initial); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES (1,$1)", checksum); err != nil {
		return err
	}
	return tx.Commit()
}

const adminCols = "id,title,markdown,published_title,published_markdown,published_at,updated_at,version,published"

type scanner interface{ Scan(...any) error }

func scanAdmin(row scanner) (blog.Post, error) {
	var p blog.Post
	err := row.Scan(&p.ID, &p.Title, &p.Markdown, &p.PublishedTitle, &p.PublishedMarkdown, &p.PublishedAt, &p.UpdatedAt, &p.Version, &p.Published)
	p.HasUnpublishedChanges = p.PublishedAt == nil || p.Title != p.PublishedTitle || p.Markdown != p.PublishedMarkdown
	if errors.Is(err, sql.ErrNoRows) {
		err = blog.ErrNotFound
	}
	return p, err
}
func scanPublic(row scanner) (blog.PublicPost, error) {
	var p blog.PublicPost
	err := row.Scan(&p.ID, &p.Title, &p.Markdown, &p.PublishedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = blog.ErrNotFound
	}
	return p, err
}
func (s *Store) ListPublic(ctx context.Context, limit, offset int) ([]blog.PublicPostSummary, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,published_title,published_at,public_updated_at FROM posts WHERE published ORDER BY published_at DESC,id LIMIT $1 OFFSET $2", limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []blog.PublicPostSummary{}
	for rows.Next() {
		var p blog.PublicPostSummary
		if err := rows.Scan(&p.ID, &p.Title, &p.PublishedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) GetPublic(ctx context.Context, id string) (blog.PublicPost, error) {
	return scanPublic(s.db.QueryRowContext(ctx, "SELECT id,published_title,published_markdown,published_at,public_updated_at FROM posts WHERE id=$1 AND published", id))
}
func (s *Store) ListAdmin(ctx context.Context) ([]blog.Post, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+adminCols+" FROM posts ORDER BY updated_at DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []blog.Post{}
	for rows.Next() {
		p, e := scanAdmin(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) GetAdmin(ctx context.Context, id string) (blog.Post, error) {
	return scanAdmin(s.db.QueryRowContext(ctx, "SELECT "+adminCols+" FROM posts WHERE id=$1", id))
}
func (s *Store) Create(ctx context.Context, p blog.Post) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO posts (id,title,markdown,updated_at) VALUES ($1,$2,$3,$4)", p.ID, p.Title, p.Markdown, p.UpdatedAt)
	return err
}
func (s *Store) result(ctx context.Context, id string, row scanner) (blog.Post, error) {
	p, err := scanAdmin(row)
	if !errors.Is(err, blog.ErrNotFound) {
		return p, err
	}
	var exists bool
	if e := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM posts WHERE id=$1)", id).Scan(&exists); e != nil {
		return p, e
	}
	if exists {
		return p, blog.ErrConflict
	}
	return p, blog.ErrNotFound
}
func (s *Store) Save(ctx context.Context, id, title, markdown string, version int64, now time.Time) (blog.Post, error) {
	return s.result(ctx, id, s.db.QueryRowContext(ctx, "UPDATE posts SET title=$2,markdown=$3,updated_at=$5,version=version+1 WHERE id=$1 AND version=$4 RETURNING "+adminCols, id, title, markdown, version, now))
}
func (s *Store) Publish(ctx context.Context, id string, version int64, now time.Time) (blog.Post, error) {
	return s.result(ctx, id, s.db.QueryRowContext(ctx, "UPDATE posts SET published=true,published_title=title,published_markdown=markdown,published_at=COALESCE(published_at,$3),public_updated_at=$3,updated_at=$3,version=version+1 WHERE id=$1 AND version=$2 RETURNING "+adminCols, id, version, now))
}
func (s *Store) Unpublish(ctx context.Context, id string, version int64, now time.Time) (blog.Post, error) {
	return s.result(ctx, id, s.db.QueryRowContext(ctx, "UPDATE posts SET published=false,updated_at=$3,version=version+1 WHERE id=$1 AND version=$2 RETURNING "+adminCols, id, version, now))
}
func (s *Store) PutSession(ctx context.Context, se blog.Session) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= NOW()"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions (token_hash,expires_at) VALUES ($1,$2)", se.Hash, se.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) HasSession(ctx context.Context, hash string, now time.Time) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sessions WHERE token_hash=$1 AND expires_at>$2)", hash, now).Scan(&ok)
	return ok, err
}
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=$1", hash)
	return err
}

// Global owner-only login limit is shared across task replacements. Deliberate
// tradeoff: an attacker can exhaust it. Add stronger edge controls before public use.
func (s *Store) AllowLogin(ctx context.Context, now time.Time) (bool, error) {
	window := now.Unix() / 900
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM login_attempts WHERE window_start < $1", window-1); err != nil {
		return false, err
	}
	var count int
	err = tx.QueryRowContext(ctx, "INSERT INTO login_attempts VALUES ($1,1) ON CONFLICT(window_start) DO UPDATE SET attempts=LEAST(login_attempts.attempts+1,11) RETURNING attempts", window).Scan(&count)
	if err != nil {
		return false, err
	}
	return count <= 10, tx.Commit()
}
