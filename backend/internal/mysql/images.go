package mysql

import (
	"context"
	"database/sql"
	"errors"

	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/images"
)

func (s *Store) SaveImage(ctx context.Context, image images.Image) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO images(id,object_key,mime,byte_size,sha256,created_at) VALUES(?,?,?,?,?,?)`, image.ID, image.Key, image.MIME, image.Size, image.SHA256, image.CreatedAt)
	return err
}
func (s *Store) GetImage(ctx context.Context, id string) (images.Image, error) {
	var image images.Image
	err := s.db.QueryRowContext(ctx, `SELECT id,object_key,mime,byte_size,sha256,created_at FROM images WHERE id=?`, id).Scan(&image.ID, &image.Key, &image.MIME, &image.Size, &image.SHA256, &image.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return image, blog.ErrNotFound
	}
	return image, err
}
func (s *Store) ImagePublic(ctx context.Context, id string) (bool, error) {
	var visible bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM post_images ref JOIN posts post ON post.id=ref.post_id WHERE ref.image_id=? AND ref.snapshot='published' AND post.published)`, id).Scan(&visible)
	return visible, err
}
func replaceImageRefs(ctx context.Context, tx *sql.Tx, post blog.Post, snapshot string) error {
	markdown := post.Markdown
	if snapshot == "published" {
		markdown = post.PublishedMarkdown
	}
	refs, err := images.References(markdown)
	if err != nil {
		return images.ErrInvalid
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM post_images WHERE post_id=? AND snapshot=?", post.ID, snapshot); err != nil {
		return err
	}
	for _, id := range refs {
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM images WHERE id=?)", id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return images.ErrInvalid
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO post_images(post_id,image_id,snapshot) VALUES(?,?,?)", post.ID, id, snapshot); err != nil {
			return err
		}
	}
	return nil
}

// Post text, optimistic version and its access-control references commit together.
func (s *Store) updatePost(ctx context.Context, id, statement, snapshot string, args ...any) (blog.Post, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return blog.Post{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return blog.Post{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return blog.Post{}, err
	}
	post, err := scanAdmin(tx.QueryRowContext(ctx, "SELECT "+adminColumns+" FROM posts WHERE id=? FOR UPDATE", id))
	if err != nil {
		return post, err
	}
	if count == 0 {
		return post, blog.ErrConflict
	}
	if snapshot != "" {
		if err = replaceImageRefs(ctx, tx, post, snapshot); err != nil {
			return post, err
		}
	}
	return post, tx.Commit()
}
