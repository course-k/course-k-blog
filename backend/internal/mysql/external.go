package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/external"
)

// The list, monthly archive and counts share one definition. External articles
// use the fetched title and date for every row; legacy title_override,
// date_override and hidden columns are kept in the database but not applied.
const publicArticles = `SELECT 'own' AS kind,id,published_title AS title,published_at,public_updated_at AS updated_at,'' AS source,'' AS url
   FROM posts WHERE published
   UNION ALL
   SELECT 'external',CONCAT(source,':',source_id),title,published_at,fetched_at,source,url
   FROM external_posts`

// Manual sync waits 10 minutes after the previous attempt for each source.
const syncCooldown = 10 * time.Minute

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
func (s *Store) SyncStates(ctx context.Context) ([]external.SyncState, error) {
	// A legacy scheduled run whose lease expired is shown as idle: only explicit
	// requests are resumed after a process stops.
	rows, err := s.db.QueryContext(ctx, `SELECT source,
 CASE WHEN status='running' AND NOT requested AND (lease_until IS NULL OR lease_until<NOW(6)) THEN 'idle'
      WHEN status='waiting' AND NOT requested THEN 'idle' ELSE status END,
 requested,coverage,imported_count,error_code,last_attempt,last_success,
 IF(last_attempt>NOW(6)-INTERVAL 10 MINUTE,last_attempt+INTERVAL 10 MINUTE,NULL)
 FROM sync_sources ORDER BY FIELD(source,'qiita','wantedly','note')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := []external.SyncState{}
	for rows.Next() {
		var state external.SyncState
		if err := rows.Scan(&state.Source, &state.Status, &state.Requested, &state.Coverage, &state.Count, &state.ErrorCode, &state.LastAttempt, &state.LastSuccess, &state.NextAllowedAt); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

// RequestSync is the only way a sync starts. It returns ErrRunning while a
// request is queued or running and CooldownError during the 10-minute limit.
func (s *Store) RequestSync(ctx context.Context, source string) error {
	if !external.ValidSource(source) {
		return blog.ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sync_sources SET requested=true,status='waiting'
 WHERE source=? AND NOT (requested AND status IN ('waiting','running'))
 AND (lease_until IS NULL OR lease_until<NOW(6))
 AND (last_attempt IS NULL OR last_attempt<NOW(6)-INTERVAL 10 MINUTE)`, source)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 1 {
		return nil
	}
	var busy bool
	var remaining sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT (requested AND status IN ('waiting','running')) OR COALESCE(lease_until>=NOW(6),false),
 IF(last_attempt>=NOW(6)-INTERVAL 10 MINUTE,TIMESTAMPDIFF(MICROSECOND,NOW(6),last_attempt+INTERVAL 10 MINUTE),NULL)
 FROM sync_sources WHERE source=?`, source).Scan(&busy, &remaining)
	if err != nil {
		return err
	}
	if !busy && remaining.Valid {
		return external.CooldownError{RetryAfter: max(time.Duration(remaining.Int64)*time.Microsecond, time.Second)}
	}
	return external.ErrRunning
}

// ClaimSync takes an explicitly requested source, or resumes an explicit
// request whose lease expired. Reaching next_run alone never starts a sync.
func (s *Store) ClaimSync(ctx context.Context, runID string) (string, error) {
	var source string
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT source FROM sync_sources WHERE requested AND status IN ('waiting','running') AND (lease_until IS NULL OR lease_until<NOW(6)) ORDER BY source LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&source)
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
		if _, err = tx.ExecContext(ctx, `UPDATE sync_sources SET status='failed',requested=false,error_code=?,lease_until=NULL,run_id=NULL WHERE source=?`, errorCode, source); err != nil {
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
	_, err = tx.ExecContext(ctx, `UPDATE sync_sources SET status=?,requested=false,coverage=?,imported_count=?,error_code='',last_success=NOW(6),lease_until=NULL,run_id=NULL WHERE source=?`, status, batch.Coverage, len(batch.Posts), source)
	if err != nil {
		return err
	}
	return tx.Commit()
}
