package mysql

import (
	"context"
	"coursek/blog/backend/internal/external"
)

// ImportMissingExternal supplements verified history without overwriting later
// owner corrections or claiming that a limited upstream feed covers all history.
func (s *Store) ImportMissingExternal(ctx context.Context, posts []external.Post) (int, error) {
	if len(posts) == 0 || len(posts) > 20 {
		return 0, external.ErrInvalid
	}
	for _, p := range posts {
		if !external.ValidPost(p) {
			return 0, external.ErrInvalid
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	inserted := 0
	for _, p := range posts {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM external_posts WHERE source=? AND source_id=?)", p.Source, p.ID).Scan(&exists); err != nil {
			return 0, err
		}
		if exists {
			continue
		}
		// A concurrent insertion fails the whole transaction safely; retrying then
		// skips that row. Never turn a duplicate into an update of existing data.
		if _, err := tx.ExecContext(ctx, "INSERT INTO external_posts(source,source_id,title,url,published_at) VALUES(?,?,?,?,?)", p.Source, p.ID, p.Title, p.URL, p.PublishedAt); err != nil {
			return 0, err
		}
		inserted++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return inserted, nil
}
