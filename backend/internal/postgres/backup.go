package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"coursek/blog/backend/internal/backup"
)

var snapshotTables = []string{"images", "posts", "post_images", "external_posts", "creation_requests"}

func (s *Store) BackupState(ctx context.Context) (backup.State, error) {
	var state backup.State
	err := s.db.QueryRowContext(ctx, `SELECT generation,backed_up_generation,due_at,COALESCE(lease_until>now(),false),last_attempt,last_success,last_error FROM operation_state WHERE singleton`).Scan(&state.Generation, &state.BackedUpGeneration, &state.DueAt, &state.Running, &state.LastAttempt, &state.LastSuccess, &state.LastError)
	return state, err
}
func (s *Store) QueueBackup(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operation_state SET due_at=now() WHERE singleton AND generation>backed_up_generation`)
	return err
}
func (s *Store) ClaimBackup(ctx context.Context, token string, force bool) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE operation_state SET lease_id=$1,lease_until=now()+interval '3 minutes',last_attempt=now()
 WHERE singleton AND generation>backed_up_generation AND ($2 OR due_at<=now()) AND (lease_until IS NULL OR lease_until<now())`, token, force)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
func (s *Store) ExportSnapshot(ctx context.Context) (backup.Snapshot, error) {
	snapshot := backup.Snapshot{Tables: map[string]json.RawMessage{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return snapshot, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT generation FROM operation_state WHERE singleton").Scan(&snapshot.Generation); err != nil {
		return snapshot, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&snapshot.SchemaVersion); err != nil {
		return snapshot, err
	}
	var totalBytes int64
	for _, table := range snapshotTables {
		var size int64
		if err = tx.QueryRowContext(ctx, "SELECT COALESCE(sum(octet_length(row_to_json(record)::text))+count(*)*2,2) FROM "+table+" AS record").Scan(&size); err != nil {
			return snapshot, err
		}
		totalBytes += size
		if totalBytes > backup.MaxSnapshotBytes {
			return snapshot, backup.ErrInvalid
		}
	}
	for _, table := range snapshotTables {
		// Identifiers come only from the fixed application table list, never request data.
		var content []byte
		if err = tx.QueryRowContext(ctx, "SELECT COALESCE(json_agg(row_to_json(record)),'[]'::json) FROM "+table+" AS record").Scan(&content); err != nil {
			return snapshot, err
		}
		snapshot.Tables[table] = json.RawMessage(content)
	}
	return snapshot, tx.Commit()
}
func (s *Store) CompleteBackup(ctx context.Context, token string, record backup.Record) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE operation_state SET backed_up_generation=GREATEST(backed_up_generation,$2),due_at=CASE WHEN generation<=$2 THEN NULL ELSE due_at END,last_success=now(),last_error='',lease_id=NULL,lease_until=NULL
 WHERE singleton AND lease_id=$1 AND lease_until>now()`, token, record.Generation)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return backup.ErrBusy
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO backups(id,generation,object_key,byte_size,sha256) VALUES($1,$2,$3,$4,$5)`, record.ID, record.Generation, record.Key, record.Size, record.SHA256); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) FailBackup(ctx context.Context, token, code string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operation_state SET last_error=$2,due_at=now()+interval '15 minutes',lease_id=NULL,lease_until=NULL WHERE singleton AND lease_id=$1`, token, code)
	return err
}
func (s *Store) BackupRetentionStatus(ctx context.Context, code string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operation_state SET last_error=$1 WHERE singleton AND ($1<>'' OR last_error='retention_failed')`, code)
	return err
}
func (s *Store) ListBackups(ctx context.Context) ([]backup.Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,generation,object_key,created_at,byte_size,sha256 FROM backups ORDER BY created_at DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []backup.Record{}
	for rows.Next() {
		var record backup.Record
		if err = rows.Scan(&record.ID, &record.Generation, &record.Key, &record.CreatedAt, &record.Size, &record.SHA256); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}
func (s *Store) ForgetBackup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM backups WHERE id=$1", id)
	return err
}

type restoreQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validateRestore(ctx context.Context, query restoreQuery, snapshot backup.Snapshot) error {
	if len(snapshot.Tables) != len(snapshotTables) {
		return backup.ErrInvalid
	}
	var version int
	if err := query.QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version != snapshot.SchemaVersion {
		return backup.ErrInvalid
	}
	for _, table := range snapshotTables {
		var count int
		if err := query.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("restore destination is not empty")
		}
		if _, ok := snapshot.Tables[table]; !ok {
			return backup.ErrInvalid
		}
	}
	return nil
}
func (s *Store) ValidateRestore(ctx context.Context, snapshot backup.Snapshot) error {
	return validateRestore(ctx, s.db, snapshot)
}
func (s *Store) RestoreSnapshot(ctx context.Context, snapshot backup.Snapshot) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "LOCK TABLE images,posts,post_images,external_posts,creation_requests IN ACCESS EXCLUSIVE MODE"); err != nil {
		return err
	}
	if err = validateRestore(ctx, tx, snapshot); err != nil {
		return err
	}
	for _, table := range snapshotTables {
		if _, err = tx.ExecContext(ctx, "INSERT INTO "+table+" SELECT * FROM json_populate_recordset(NULL::"+table+",$1::json)", string(snapshot.Tables[table])); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM login_flows"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE operation_state SET generation=$1,backed_up_generation=0,due_at=now(),lease_id=NULL,lease_until=NULL,last_error='' WHERE singleton`, snapshot.Generation+1); err != nil {
		return err
	}
	return tx.Commit()
}
