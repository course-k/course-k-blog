package external

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"coursek/blog/backend/internal/auth"
)

type Fetcher interface {
	Fetch(context.Context, string) (Batch, error)
}
type Service struct {
	Repository Repository
	Client     Fetcher
}

func (s *Service) Page(ctx context.Context, page int) (Page, error) {
	articles, err := s.Repository.ListArticles(ctx, 21, (page-1)*20)
	if err != nil {
		return Page{}, err
	}
	hasNext := len(articles) > 20
	if hasNext {
		articles = articles[:20]
	}
	return Page{Articles: articles, HasNext: hasNext}, nil
}

// One poll is small and bounded. The DB lease also coordinates replacement tasks.
func (s *Service) RunOnce(ctx context.Context) error {
	runID, err := auth.Token()
	if err != nil {
		return err
	}
	source, err := s.Repository.ClaimSync(ctx, runID)
	if err != nil || source == "" {
		return err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	batch, fetchError := s.Client.Fetch(fetchCtx, source)
	code := ""
	if fetchError != nil {
		code = fetchError.Error()
	}
	finishCtx, finishCancel := context.WithTimeout(ctx, 10*time.Second)
	defer finishCancel()
	err = s.Repository.FinishSync(finishCtx, source, runID, batch, code)
	if code != "" && !errors.Is(ctx.Err(), context.Canceled) {
		slog.Error("operation_failed", "operation", "external_sync", "source", source, "code", code)
	}
	return err
}
func (s *Service) Run(ctx context.Context) {
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	for {
		if err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Error("operation_failed", "operation", "external_sync_state", "code", "state_unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
