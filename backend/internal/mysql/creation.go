package mysql

import (
	"context"
	"database/sql"
	"errors"
	driver "github.com/go-sql-driver/mysql"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/blog"
)

func (s *Store) CreateOnce(ctx context.Context, key, fingerprint string, post blog.Post) (blog.Post, error) {
	if !auth.ValidToken(key) {
		return blog.Post{}, blog.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return blog.Post{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO posts(id,title,markdown,updated_at) VALUES(?,?,?,?)", post.ID, post.Title, post.Markdown, post.UpdatedAt); err != nil {
		return blog.Post{}, err
	}
	if err = replaceImageRefs(ctx, tx, post, "working"); err != nil {
		return blog.Post{}, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO creation_requests(request_key,payload_hash,post_id) VALUES(?,?,?)", key, fingerprint, post.ID)
	var duplicate *driver.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		if err = tx.Rollback(); err != nil {
			return blog.Post{}, err
		}
		var expected string
		if err = s.db.QueryRowContext(ctx, "SELECT payload_hash FROM creation_requests WHERE request_key=?", key).Scan(&expected); err != nil {
			return blog.Post{}, err
		}
		if fingerprint != expected {
			return blog.Post{}, blog.ErrConflict
		}
		return s.FindCreation(ctx, key)
	}
	if err != nil {
		return blog.Post{}, err
	}
	if err = tx.Commit(); err != nil {
		return blog.Post{}, err
	}
	return s.GetAdmin(ctx, post.ID)
}
func (s *Store) FindCreation(ctx context.Context, key string) (blog.Post, error) {
	if !auth.ValidToken(key) {
		return blog.Post{}, blog.ErrNotFound
	}
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT post_id FROM creation_requests WHERE request_key=?", key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return blog.Post{}, blog.ErrNotFound
	}
	if err != nil {
		return blog.Post{}, err
	}
	return s.GetAdmin(ctx, id)
}
