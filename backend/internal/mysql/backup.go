package mysql

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"coursek/blog/backend/internal/backup"
)

var snapshotTables = []string{"images", "posts", "post_images", "external_posts", "creation_requests"}

// Explicit columns exclude generated indexes and stabilize the archive schema.
var snapshotColumns = map[string][]string{
	"images":            {"id", "object_key", "mime", "byte_size", "sha256", "created_at"},
	"posts":             {"id", "title", "markdown", "published_title", "published_markdown", "published", "published_at", "public_updated_at", "updated_at", "version"},
	"post_images":       {"post_id", "image_id", "snapshot"},
	"external_posts":    {"source", "source_id", "title", "url", "published_at", "fetched_at", "title_override", "date_override", "hidden", "version"},
	"creation_requests": {"request_key", "payload_hash", "post_id"},
}

func (s *Store) BackupState(ctx context.Context) (backup.State, error) {
	var state backup.State
	err := s.db.QueryRowContext(ctx, `SELECT generation,backed_up_generation,due_at,COALESCE(lease_until>NOW(6),false),last_attempt,last_success,last_error FROM operation_state WHERE singleton`).Scan(&state.Generation, &state.BackedUpGeneration, &state.DueAt, &state.Running, &state.LastAttempt, &state.LastSuccess, &state.LastError)
	return state, err
}
func (s *Store) QueueBackup(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operation_state SET due_at=NOW(6) WHERE singleton AND generation>backed_up_generation`)
	return err
}
func (s *Store) ClaimBackup(ctx context.Context, token string, force bool) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE operation_state SET lease_id=?,lease_until=NOW(6)+INTERVAL 3 MINUTE,last_attempt=NOW(6)
 WHERE singleton AND generation>backed_up_generation AND (? OR due_at<=NOW(6)) AND (lease_until IS NULL OR lease_until<NOW(6))`, token, force)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
func (s *Store) ExportSnapshot(ctx context.Context) (backup.Snapshot, error) {
	snapshot := backup.Snapshot{DatabaseEngine: "mysql", Tables: map[string]json.RawMessage{}}
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
	totalBytes := 0
	for _, table := range snapshotTables {
		// Both table and column identifiers are fixed by this application, never input.
		columns := snapshotColumns[table]
		rows, err := tx.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM "+table)
		if err != nil {
			return snapshot, err
		}
		var content bytes.Buffer
		content.WriteByte('[')
		first := true
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				rows.Close()
				return snapshot, err
			}
			record := map[string]any{}
			for i, col := range columns {
				if value, ok := values[i].([]byte); ok {
					values[i] = string(value)
				}
				record[col] = values[i]
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				rows.Close()
				return snapshot, err
			}
			if totalBytes+content.Len()+len(encoded)+2 > backup.MaxSnapshotBytes {
				rows.Close()
				return snapshot, backup.ErrInvalid
			}
			if !first {
				content.WriteByte(',')
			}
			first = false
			content.Write(encoded)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return snapshot, err
		}
		content.WriteByte(']')
		totalBytes += content.Len()
		snapshot.Tables[table] = json.RawMessage(content.Bytes())
	}
	return snapshot, tx.Commit()
}
func (s *Store) CompleteBackup(ctx context.Context, token string, record backup.Record) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE operation_state SET backed_up_generation=GREATEST(backed_up_generation,?),due_at=CASE WHEN generation<=? THEN NULL ELSE due_at END,last_success=NOW(6),last_error='',lease_id=NULL,lease_until=NULL
 WHERE singleton AND lease_id=? AND lease_until>NOW(6)`, record.Generation, record.Generation, token)
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
	if _, err = tx.ExecContext(ctx, `INSERT INTO backups(id,generation,object_key,byte_size,sha256) VALUES(?,?,?,?,?)`, record.ID, record.Generation, record.Key, record.Size, record.SHA256); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) FailBackup(ctx context.Context, token, code string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operation_state SET last_error=?,due_at=NOW(6)+INTERVAL 15 MINUTE,lease_id=NULL,lease_until=NULL WHERE singleton AND lease_id=?`, code, token)
	return err
}
func (s *Store) BackupRetentionStatus(ctx context.Context, code string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operation_state SET last_error=? WHERE singleton AND (?<>'' OR last_error='retention_failed')`, code, code)
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
	_, err := s.db.ExecContext(ctx, "DELETE FROM backups WHERE id=?", id)
	return err
}

type restoreQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validateRestore(ctx context.Context, query restoreQuery, snapshot backup.Snapshot) error {
	if snapshot.DatabaseEngine != "mysql" || len(snapshot.Tables) != len(snapshotTables) {
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
	// Restore runs against a dedicated, empty maintenance database. Lock the
	// dirty-state row shared by all content triggers before changing its contents.
	var generation int64
	if err = tx.QueryRowContext(ctx, "SELECT generation FROM operation_state WHERE singleton FOR UPDATE").Scan(&generation); err != nil {
		return err
	}
	if err = validateRestore(ctx, tx, snapshot); err != nil {
		return err
	}
	for _, table := range snapshotTables {
		columns := snapshotColumns[table]
		var records []map[string]json.RawMessage
		if err = json.Unmarshal(snapshot.Tables[table], &records); err != nil {
			return backup.ErrInvalid
		}
		statement := "INSERT INTO " + table + " (" + strings.Join(columns, ",") + ") VALUES (" + strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",") + ")"
		for _, record := range records {
			if len(record) != len(columns) {
				return backup.ErrInvalid
			}
			values := make([]any, len(columns))
			for i, column := range columns {
				raw, ok := record[column]
				if !ok {
					return backup.ErrInvalid
				}
				if bytes.Equal(raw, []byte("null")) {
					continue
				}
				var value any
				decoder := json.NewDecoder(bytes.NewReader(raw))
				decoder.UseNumber()
				if err = decoder.Decode(&value); err != nil {
					return backup.ErrInvalid
				}
				if number, ok := value.(json.Number); ok {
					value, err = number.Int64()
					if err != nil {
						return backup.ErrInvalid
					}
				}
				if strings.HasSuffix(column, "_at") {
					text, ok := value.(string)
					if !ok {
						return backup.ErrInvalid
					}
					value, err = time.Parse(time.RFC3339Nano, text)
					if err != nil {
						return backup.ErrInvalid
					}
				}
				values[i] = value
			}
			if _, err = tx.ExecContext(ctx, statement, values...); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM login_flows"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE operation_state SET generation=?,backed_up_generation=0,due_at=NOW(6),lease_id=NULL,lease_until=NULL,last_error='' WHERE singleton`, snapshot.Generation+1); err != nil {
		return err
	}
	return tx.Commit()
}
