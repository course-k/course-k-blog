package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/external"
)

func (s *Store) ListArticles(ctx context.Context, limit, offset int) ([]external.Article, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind,id,title,published_at,updated_at,source,url FROM (
   SELECT 'own' AS kind,id,published_title AS title,published_at,public_updated_at AS updated_at,'' AS source,'' AS url
   FROM posts WHERE published
   UNION ALL
   SELECT 'external',source||':'||source_id,COALESCE(title_override,title),COALESCE(date_override,published_at),fetched_at,source,url
   FROM external_posts WHERE NOT hidden
  ) AS articles ORDER BY published_at DESC,kind,id LIMIT $1 OFFSET $2`, limit, offset)
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
	result, err := s.db.ExecContext(ctx, `UPDATE external_posts SET title_override=$3,date_override=$4,hidden=$5,version=version+1 WHERE source=$1 AND source_id=$2 AND version=$6`, source, id, in.Title, in.PublishedAt, in.Hidden, in.Version)
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
	result, err := s.db.ExecContext(ctx, `UPDATE sync_sources SET next_run=now(),status='waiting'
 WHERE source=$1 AND (lease_until IS NULL OR lease_until<now()) AND (last_attempt IS NULL OR last_attempt<now()-interval '10 minutes')`, source)
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
	err := s.db.QueryRowContext(ctx, `UPDATE sync_sources SET status='running',last_attempt=now(),lease_until=now()+interval '3 minutes',run_id=$1
 WHERE source=(SELECT source FROM sync_sources WHERE next_run<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY next_run,source FOR UPDATE SKIP LOCKED LIMIT 1)
 RETURNING source`, runID).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return source, err
}
func (s *Store) FinishSync(ctx context.Context, source, runID string, batch external.Batch, errorCode string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var coverage string
	err = tx.QueryRowContext(ctx, `SELECT coverage FROM sync_sources WHERE source=$1 AND run_id=$2 AND lease_until>now() FOR UPDATE`, source, runID).Scan(&coverage)
	if errors.Is(err, sql.ErrNoRows) {
		return external.ErrBusy
	}
	if err != nil {
		return err
	}
	if errorCode != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE sync_sources SET status='failed',error_code=$2,lease_until=NULL,run_id=NULL,next_run=now()+interval '1 hour' WHERE source=$1`, source, errorCode); err != nil {
			return err
		}
		return tx.Commit()
	}
	if batch.Coverage != "all" && batch.Coverage != "recent" {
		return blog.ErrInvalid
	}
	var previous int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM external_posts WHERE source=$1", source).Scan(&previous); err != nil {
		return err
	}
	overlap := false
	for _, p := range batch.Posts {
		if p.Source != source || !external.ValidPost(p) {
			return blog.ErrInvalid
		}
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM external_posts WHERE source=$1 AND source_id=$2)", source, p.ID).Scan(&exists); err != nil {
			return err
		}
		overlap = overlap || exists
		_, err = tx.ExecContext(ctx, `INSERT INTO external_posts(source,source_id,title,url,published_at) VALUES($1,$2,$3,$4,$5)
   ON CONFLICT(source,source_id) DO UPDATE SET title=EXCLUDED.title,url=EXCLUDED.url,published_at=EXCLUDED.published_at,fetched_at=now(),version=external_posts.version+CASE
     WHEN (external_posts.title,external_posts.url,external_posts.published_at)
       IS DISTINCT FROM (EXCLUDED.title,EXCLUDED.url,EXCLUDED.published_at) THEN 1 ELSE 0 END`, source, p.ID, p.Title, p.URL, p.PublishedAt)
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
	_, err = tx.ExecContext(ctx, `UPDATE sync_sources SET status=$2,coverage=$3,imported_count=$4,error_code='',last_success=now(),next_run=now()+interval '1 day',lease_until=NULL,run_id=NULL WHERE source=$1`, source, status, batch.Coverage, len(batch.Posts))
	if err != nil {
		return err
	}
	return tx.Commit()
}
