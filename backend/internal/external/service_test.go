package external

import (
	"context"
	"errors"
	"testing"
)

type archiveRepository struct {
	Repository
	pageSize, offset int
	month            string
	archiveError     error
}

func (r *archiveRepository) ListArticles(_ context.Context, limit, offset int, month string) ([]Article, error) {
	r.pageSize, r.offset, r.month = limit, offset, month
	return make([]Article, 21), nil
}
func (r *archiveRepository) ArticleMonths(context.Context) ([]ArchiveMonth, error) {
	return []ArchiveMonth{{Month: "2026-10", Count: 42}}, r.archiveError
}
func TestMonthlyPageKeepsPaginationAndFullArchive(t *testing.T) {
	r := &archiveRepository{}
	s := Service{Repository: r}
	p, err := s.Page(context.Background(), 2, "2026-10")
	if err != nil || !p.HasNext || len(p.Articles) != 20 || len(p.Months) != 1 || r.pageSize != 21 || r.offset != 20 || r.month != "2026-10" {
		t.Fatalf("monthly pagination: %+v, %+v, %v", p, r, err)
	}
	r.archiveError = errors.New("storage unavailable")
	if _, err := s.Page(context.Background(), 1, ""); err == nil {
		t.Fatal("archive failure presented as empty success")
	}
	for _, month := range []string{"2026-00", "2026-13", "26-10", "2026-1", "0000-01", "2026-10 OR 1=1"} {
		if _, err := s.Page(context.Background(), 1, month); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q", month)
		}
	}
}
