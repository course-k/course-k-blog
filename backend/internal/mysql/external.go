package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/external"
)

// Shared visibility and date corrections keep the archive and list consistent.
const publicArticles = `SELECT 'own' AS kind,id,published_title AS title,published_at,public_updated_at AS updated_at,'' AS source,'' AS url
   FROM posts WHERE published
   UNION ALL
   SELECT 'external',CONCAT(source,':',source_id),COALESCE(title_override,title),COALESCE(date_override,published_at),fetched_at,source,url
   FROM external_posts WHERE NOT hidden`

func (s *Store) ListArticles(ctx context.Context, limit, offset int, month string) ([]external.Article, error) {
	query := `SELECT kind,id,title,published_at,updated_at,source,url FROM (` + publicArticles + `) AS articles`
	args := []any{}
	if month != "" {
		// Match the Asia/Tokyo date displayed on article cards, without MySQL timezone tables.
		query += ` WHERE DATE_FORMAT(published_at + INTERVAL 9 HOUR, '%Y-%m') = ?`
		args = append(args, month)
	}
	query += ` ORDER BY published_at DESC,kind,id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	articles := []external.Article{}
	for rows.Next() {
		var article external.Article
		if err := rows.Scan(&article.Kind, &article.ID, &article.Title, &article.PublishedAt, &article.UpdatedAt, &article.Source, &article.URL); err != nil {
			return nil, err
		}
		articles = append(articles, article)
	}
	return articles, rows.Err()
}
func (s *Store) ArticleMonths(ctx context.Context) ([]external.ArchiveMonth, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DATE_FORMAT(published_at + INTERVAL 9 HOUR, '%Y-%m') AS month,COUNT(*) FROM (`+publicArticles+`) AS articles GROUP BY month ORDER BY month DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	months := []external.ArchiveMonth{}
	for rows.Next() {
		var month external.ArchiveMonth
		if err := rows.Scan(&month.Month, &month.Count); err != nil {
			return nil, err
		}
		months = append(months, month)
	}
	return months, rows.Err()
}

func (s *Store) ListExternal(ctx context.Context) ([]external.Post, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source,source_id,title,url,published_at,title_override,date_override,hidden,version FROM external_posts ORDER BY published_at DESC,source,source_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	posts := []external.Post{}
	for rows.Next() {
		var p external.Post
		if err := rows.Scan(&p.Source, &p.ID, &p.Title, &p.URL, &p.PublishedAt, &p.TitleOverride, &p.DateOverride, &p.Hidden, &p.Version); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}
func (s *Store) CorrectExternal(ctx context.Context, source, id string, in external.Correction) error {
	if !external.ValidSource(source) || in.Version < 1 || (in.Title != nil && (!utf8.ValidString(*in.Title) || strings.TrimSpace(*in.Title) == "" || utf8.RuneCountInString(*in.Title) > 500)) || (in.PublishedAt != nil && in.PublishedAt.IsZero()) {
		return external.ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE external_posts SET title_override=?,date_override=?,hidden=?,version=version+1 WHERE source=? AND source_id=? AND version=?`, in.Title, in.PublishedAt, in.Hidden, source, id, in.Version)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return blog.ErrConflict
	}
	return nil
}
func (s *Store) SyncStates(ctx context.Context) ([]external.SyncState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source,status,coverage,imported_count,error_code,last_attempt,last_success,next_run FROM sync_sources ORDER BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := []external.SyncState{}
	for rows.Next() {
		var state external.SyncState
		if err := rows.Scan(&state.Source, &state.Status, &state.Coverage, &state.Count, &state.ErrorCode, &state.LastAttempt, &state.LastSuccess, &state.NextRun); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}
func (s *Store) RequestSync(ctx context.Context, source string) error {
	if !external.ValidSource(source) {
		return blog.ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sync_sources SET next_run=NOW(6),status='waiting'
 WHERE source=? AND (lease_until IS NULL OR lease_until<NOW(6)) AND (last_attempt IS NULL OR last_attempt<NOW(6)-INTERVAL 10 MINUTE)`, source)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return external.ErrBusy
	}
	return nil
}
func (s *Store) ClaimSync(ctx context.Context, runID string) (string, error) {
	var source string
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT source FROM sync_sources WHERE next_run<=NOW(6) AND (lease_until IS NULL OR lease_until<NOW(6)) ORDER BY next_run,source LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sync_sources SET status='running',last_attempt=NOW(6),lease_until=NOW(6)+INTERVAL 3 MINUTE,run_id=? WHERE source=?`, runID, source); err != nil {
		return "", err
	}
	return source, tx.Commit()
}

func (s *Store) FinishSync(ctx context.Context, source, runID string, batch external.Batch, errorCode string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var coverage string
	err = tx.QueryRowContext(ctx, `SELECT coverage FROM sync_sources WHERE source=? AND run_id=? AND lease_until>NOW(6) FOR UPDATE`, source, runID).Scan(&coverage)
	if errors.Is(err, sql.ErrNoRows) {
		return external.ErrBusy
	}
	if err != nil {
		return err
	}
	if errorCode != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE sync_sources SET status='failed',error_code=?,lease_until=NULL,run_id=NULL,next_run=NOW(6)+INTERVAL 1 HOUR WHERE source=?`, errorCode, source); err != nil {
			return err
		}
		return tx.Commit()
	}
	if batch.Coverage != "all" && batch.Coverage != "recent" {
		return blog.ErrInvalid
	}
	var previous int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM external_posts WHERE source=?", source).Scan(&previous); err != nil {
		return err
	}
	overlap := false
	for _, p := range batch.Posts {
		if p.Source != source || !external.ValidPost(p) {
			return blog.ErrInvalid
		}
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM external_posts WHERE source=? AND source_id=?)", source, p.ID).Scan(&exists); err != nil {
			return err
		}
		overlap = overlap || exists
		_, err = tx.ExecContext(ctx, `INSERT INTO external_posts(source,source_id,title,url,published_at) VALUES(?,?,?,?,?) AS incoming
 ON DUPLICATE KEY UPDATE version=external_posts.version+IF(external_posts.title <=> incoming.title AND external_posts.url <=> incoming.url AND external_posts.published_at <=> incoming.published_at,0,1),title=incoming.title,url=incoming.url,published_at=incoming.published_at,fetched_at=NOW(6)`, source, p.ID, p.Title, p.URL, p.PublishedAt)
		if err != nil {
			return err
		}
	}
	// A missing window stays unresolved after later overlapping polls; do not forget the gap.
	if batch.Coverage != "all" && (coverage == "gap" || previous > 0 && !overlap) {
		batch.Coverage = "gap"
	}
	status := "success"
	if batch.Coverage != "all" {
		status = "partial"
	}
	_, err = tx.ExecContext(ctx, `UPDATE sync_sources SET status=?,coverage=?,imported_count=?,error_code='',last_success=NOW(6),next_run=NOW(6)+INTERVAL 1 DAY,lease_until=NULL,run_id=NULL WHERE source=?`, status, batch.Coverage, len(batch.Posts), source)
	if err != nil {
		return err
	}
	return tx.Commit()
}
