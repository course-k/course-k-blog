package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// MySQL DDL commits implicitly. A connection-scoped named lock serializes
// migrations; each idempotent statement is rerunnable after a partial failure.
const migrationLockName = "coursek-schema"

// Migrations are an explicit command, never a side effect of starting a task.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,30)", migrationLockName).Scan(&locked); err != nil {
		return err
	}
	if locked != 1 {
		return errors.New("schema lock unavailable")
	}
	defer conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", migrationLockName)
	if _, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY,checksum varchar(64) NOT NULL)"); err != nil {
		return err
	}
	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, name := range files {
		version, err := strconv.Atoi(strings.Split(strings.TrimPrefix(name, "migrations/"), "_")[0])
		if err != nil {
			return err
		}
		script, err := migrationFiles.ReadFile(name)
		if err != nil {
			return err
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(script))
		var existing string
		err = conn.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version=?", version).Scan(&existing)
		if err == nil {
			if existing != checksum {
				return errors.New("migration checksum changed")
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		for _, statement := range strings.Split(string(script), "-- statement-break") {
			if _, err = conn.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("migration %d: %w", version, err)
			}
		}
		if _, err = conn.ExecContext(ctx, "INSERT INTO schema_migrations VALUES (?,?)", version, checksum); err != nil {
			return err
		}
	}
	return nil
}
