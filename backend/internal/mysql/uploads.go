package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	driver "github.com/go-sql-driver/mysql"

	"coursek/blog/backend/internal/images"
)

const uploadColumns = "upload_id,client_image_id,file_name,declared_mime,declared_size,declared_sha256,staging_key,status,image_id,COALESCE(image_key,''),failure_code,COALESCE(result_width,0),COALESCE(result_height,0),created_at,expires_at,lease_until"

func scanUpload(row scanner) (images.Upload, error) {
	var u images.Upload
	err := row.Scan(&u.ID, &u.ClientImageID, &u.FileName, &u.MIME, &u.Size, &u.SHA256, &u.StagingKey, &u.Status, &u.ImageID, &u.ImageKey, &u.FailureCode, &u.Width, &u.Height, &u.CreatedAt, &u.ExpiresAt, &u.LeaseUntil)
	if errors.Is(err, sql.ErrNoRows) {
		err = images.ErrUploadNotFound
	}
	return u, err
}

func (s *Store) CreateUpload(ctx context.Context, u images.Upload) (images.Upload, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO image_uploads(upload_id,client_image_id,file_name,declared_mime,declared_size,declared_sha256,staging_key,status,image_id,created_at,expires_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?)`, u.ID, u.ClientImageID, u.FileName, u.MIME, u.Size, u.SHA256, u.StagingKey, images.UploadPending, u.ImageID, u.CreatedAt, u.ExpiresAt)
	var duplicate *driver.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		existing, err := scanUpload(s.db.QueryRowContext(ctx, "SELECT "+uploadColumns+" FROM image_uploads WHERE client_image_id=?", u.ClientImageID))
		if err != nil {
			return images.Upload{}, err
		}
		if existing.MIME != u.MIME || existing.Size != u.Size || existing.SHA256 != u.SHA256 {
			return images.Upload{}, images.ErrUploadReused
		}
		return existing, nil
	}
	if err != nil {
		return images.Upload{}, err
	}
	return s.GetUpload(ctx, u.ID)
}

func (s *Store) GetUpload(ctx context.Context, id string) (images.Upload, error) {
	return scanUpload(s.db.QueryRowContext(ctx, "SELECT "+uploadColumns+" FROM image_uploads WHERE upload_id=?", id))
}

func (s *Store) LeaseUpload(ctx context.Context, id, leaseID string, until time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE image_uploads SET status='processing',lease_id=?,lease_until=?
 WHERE upload_id=? AND expires_at>NOW(6) AND (status='pending' OR (status='processing' AND lease_until<NOW(6)))`, leaseID, until, id)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) SetUploadKey(ctx context.Context, id, leaseID, key string) (string, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE image_uploads SET image_key=? WHERE upload_id=? AND lease_id=? AND image_key IS NULL`, key, id, leaseID); err != nil {
		return "", err
	}
	var stored sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT image_key FROM image_uploads WHERE upload_id=? AND lease_id=?`, id, leaseID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !stored.Valid) {
		return "", images.ErrLeaseLost
	}
	return stored.String, err
}

func (s *Store) ReleaseUpload(ctx context.Context, id, leaseID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE image_uploads SET status='pending',lease_id=NULL,lease_until=NULL WHERE upload_id=? AND lease_id=? AND status='processing'`, id, leaseID)
	return err
}

// CompleteUpload registers the official image and the session result together.
// The image row is keyed by the ID fixed at session creation, so a repeated
// completion cannot create a second official image.
func (s *Store) CompleteUpload(ctx context.Context, id, leaseID string, image images.Image, width, height int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE image_uploads SET status='completed',lease_id=NULL,lease_until=NULL,completed_at=NOW(6),result_width=?,result_height=?
 WHERE upload_id=? AND lease_id=? AND status='processing' AND image_id=? AND image_key=?`, width, height, id, leaseID, image.ID, image.Key)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return images.ErrLeaseLost
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO images(id,object_key,mime,byte_size,sha256,created_at) VALUES(?,?,?,?,?,?) AS incoming
 ON DUPLICATE KEY UPDATE id=images.id`, image.ID, image.Key, image.MIME, image.Size, image.SHA256, image.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FailUpload(ctx context.Context, id, leaseID, code string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE image_uploads SET status='failed',failure_code=?,lease_id=NULL,lease_until=NULL WHERE upload_id=? AND lease_id=?`, code, id, leaseID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return errors.Join(err, images.ErrLeaseLost)
	}
	return nil
}

func (s *Store) ExpireUploads(ctx context.Context, now time.Time) ([]images.Upload, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+uploadColumns+` FROM image_uploads
 WHERE expires_at<=? AND (status IN ('pending','failed') OR (status='processing' AND lease_until<?))`, now, now)
	if err != nil {
		return nil, err
	}
	var expired []images.Upload
	for rows.Next() {
		upload, err := scanUpload(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		expired = append(expired, upload)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, upload := range expired {
		if _, err = s.db.ExecContext(ctx, `UPDATE image_uploads SET status='expired',lease_id=NULL,lease_until=NULL WHERE upload_id=? AND status<>'completed'`, upload.ID); err != nil {
			return nil, err
		}
	}
	return expired, nil
}

func (s *Store) PurgeUploads(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM image_uploads WHERE created_at<? AND status<>'processing'`, before)
	return err
}
