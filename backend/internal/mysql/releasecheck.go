package mysql

import (
	"context"
	"coursek/blog/backend/internal/auth"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"strings"
)

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version)
	return version, err
}

// CleanupReleaseCheck can only remove the newly created fixture associated with
// a random creation key and matching marker, never a user-selected article.
func (s *Store) CleanupReleaseCheck(ctx context.Context, proof string) ([]string, error) {
	if !auth.ValidToken(proof) {
		return nil, errors.New("invalid check proof")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id, title, markdown string
	err = tx.QueryRowContext(ctx, `SELECT p.id,p.title,p.markdown FROM creation_requests c JOIN posts p ON p.id=c.post_id WHERE c.request_key=? FOR UPDATE`, proof).Scan(&id, &title, &markdown)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		if title != "CI production check "+proof[:12] || !strings.HasPrefix(markdown, "<!-- course-k-release:"+proof+" -->\n") {
			return nil, errors.New("fixture changed; refusing cleanup")
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM creation_requests WHERE request_key=? AND post_id=?", proof, id); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM posts WHERE id=?", id); err != nil {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT u.image_id,u.staging_key,i.object_key FROM image_uploads u LEFT JOIN images i ON i.id=u.image_id WHERE u.client_image_id IN (?,?) FOR UPDATE`, "release-"+auth.Digest(proof)[:48]+"0", "release-"+auth.Digest(proof)[:48]+"1")
	if err != nil {
		return nil, err
	}
	type object struct {
		id, staging string
		key         sql.NullString
	}
	var objects []object
	for rows.Next() {
		var o object
		if err = rows.Scan(&o.id, &o.staging, &o.key); err != nil {
			rows.Close()
			return nil, err
		}
		objects = append(objects, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, o := range objects {
		var refs int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM post_images WHERE image_id=?", o.id).Scan(&refs); err != nil {
			return nil, err
		}
		if refs != 0 {
			return nil, errors.New("check image referenced elsewhere; refusing cleanup")
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM image_uploads WHERE image_id=? AND client_image_id IN (?,?)", o.id, "release-"+auth.Digest(proof)[:48]+"0", "release-"+auth.Digest(proof)[:48]+"1"); err != nil {
			return nil, err
		}
		if o.key.Valid {
			if !strings.HasPrefix(o.key.String, "images/") || strings.HasPrefix(o.key.String, "images/staging/") {
				return nil, errors.New("invalid check image key")
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM images WHERE id=?", o.id); err != nil {
				return nil, err
			}
			keys = append(keys, o.key.String)
		}
		if !strings.HasPrefix(o.staging, "images/staging/") {
			return nil, errors.New("invalid check staging key")
		}
		keys = append(keys, o.staging)
	}
	if id != "" || len(objects) > 0 {
		if _, err = tx.ExecContext(ctx, "UPDATE operation_state SET generation=generation+1,due_at=NOW(6) WHERE singleton"); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return keys, nil
}

// VerifySchema is read-only: applied SQL checksums must match this image.
func (s *Store) VerifySchema(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT version,checksum FROM schema_migrations")
	if err != nil {
		return err
	}
	defer rows.Close()
	applied := map[int]string{}
	for rows.Next() {
		var v int
		var hash string
		if err = rows.Scan(&v, &hash); err != nil {
			return err
		}
		applied[v] = hash
	}
	if err = rows.Err(); err != nil {
		return err
	}
	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return err
	}
	if len(files) != len(applied) {
		return errors.New("schema version count differs")
	}
	for _, name := range files {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		data, err := migrationFiles.ReadFile(name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if applied[version] != hex.EncodeToString(sum[:]) {
			return errors.New("schema checksum differs")
		}
	}
	return nil
}
