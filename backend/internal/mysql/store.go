package mysql

import (
	"context"
	"coursek/blog/backend/internal/blog"
	"database/sql"
	"errors"
	"time"
)

const loginWindowSeconds = 15 * 60
const maxLoginAttempts = 10

type Store struct{ db *sql.DB }

func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

const adminColumns = "id,title,markdown,published_title,published_markdown,published_at,updated_at,version,published"

type scanner interface{ Scan(...any) error }

func scanAdmin(row scanner) (blog.Post, error) {
	var p blog.Post
	err := row.Scan(&p.ID, &p.Title, &p.Markdown, &p.PublishedTitle, &p.PublishedMarkdown, &p.PublishedAt, &p.UpdatedAt, &p.Version, &p.Published)
	if err == nil {
		p.HasUnpublishedChanges = p.PublishedAt == nil || p.Title != p.PublishedTitle || p.Markdown != p.PublishedMarkdown
	}
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, published_title, published_at, public_updated_at
		FROM posts
		WHERE published
		ORDER BY published_at DESC, id LIMIT ? OFFSET ?`, limit, offset)
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
	return scanPublic(s.db.QueryRowContext(ctx, `SELECT id, published_title, published_markdown, published_at, public_updated_at
        FROM posts
        WHERE id=? AND published`, id))
}
func (s *Store) ListAdmin(ctx context.Context) ([]blog.Post, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+adminColumns+" FROM posts ORDER BY updated_at DESC,id")
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
	return scanAdmin(s.db.QueryRowContext(ctx, "SELECT "+adminColumns+" FROM posts WHERE id=?", id))
}
func (s *Store) Create(ctx context.Context, p blog.Post) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO posts (id,title,markdown,updated_at) VALUES(?,?,?,?)", p.ID, p.Title, p.Markdown, p.UpdatedAt); err != nil {
		return err
	}
	if err = replaceImageRefs(ctx, tx, p, "working"); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Save(ctx context.Context, id, title, markdown string, version int64, now time.Time) (blog.Post, error) {
	return s.updatePost(ctx, id, `UPDATE posts SET title=?,markdown=?,updated_at=?,version=version+1 WHERE id=? AND version=?`, "working", title, markdown, now, id, version)
}
func (s *Store) Publish(ctx context.Context, id string, version int64, now time.Time) (blog.Post, error) {
	return s.updatePost(ctx, id, `UPDATE posts SET published=true,published_title=title,published_markdown=markdown,published_at=COALESCE(published_at,?),public_updated_at=?,updated_at=?,version=version+1 WHERE id=? AND version=?`, "published", now, now, now, id, version)
}
func (s *Store) Unpublish(ctx context.Context, id string, version int64, now time.Time) (blog.Post, error) {
	return s.updatePost(ctx, id, `UPDATE posts SET published=false,updated_at=?,version=version+1 WHERE id=? AND version=?`, "", now, id, version)
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
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions (token_hash,expires_at) VALUES (?,?)", se.Hash, se.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) HasSession(ctx context.Context, hash string, now time.Time) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sessions WHERE token_hash=? AND expires_at>?)", hash, now).Scan(&ok)
	return ok, err
}
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", hash)
	return err
}

// Global owner-only login limit is shared across task replacements. Deliberate
// tradeoff: an attacker can exhaust it. Add stronger edge controls before public use.
func (s *Store) AllowLogin(ctx context.Context, now time.Time) (bool, error) {
	window := now.Unix() / loginWindowSeconds
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM login_attempts WHERE window_start < ?", window-1); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO login_attempts VALUES (?,1) ON DUPLICATE KEY UPDATE attempts=LEAST(attempts+1,?)`, window, maxLoginAttempts+1); err != nil {
		return false, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT attempts FROM login_attempts WHERE window_start=? FOR UPDATE", window).Scan(&count); err != nil {
		return false, err
	}
	return count <= maxLoginAttempts, tx.Commit()
}
