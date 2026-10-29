package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"coursek/blog/backend/internal/auth"
	"coursek/blog/backend/internal/external"
)

type archiveStore struct {
	external.Repository
	month  string
	offset int
	fail   bool
}

func (f *archiveStore) ListArticles(_ context.Context, _, offset int, month string) ([]external.Article, error) {
	f.month, f.offset = month, offset
	return []external.Article{}, nil
}
func (f *archiveStore) ArticleMonths(context.Context) ([]external.ArchiveMonth, error) {
	if f.fail {
		return nil, errors.New("database unavailable")
	}
	return []external.ArchiveMonth{{Month: "2026-10", Count: 22}}, nil
}
func TestPublicMonthlyArchiveContract(t *testing.T) {
	hash, err := auth.HashPassword("fixture-password-for-test-only")
	if err != nil {
		t.Fatal(err)
	}
	store := &archiveStore{}
	handler, err := NewWithFeatures(fake(), "https://blog.example", hash, Features{External: &external.Service{Repository: store}})
	if err != nil {
		t.Fatal(err)
	}
	response := request(handler, "GET", "/api/articles?month=2026-10&page=2", "", "", nil)
	var result external.Page
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || store.month != "2026-10" || store.offset != 20 || len(result.Months) != 1 || result.Months[0].Count != 22 {
		t.Fatal("monthly contract", response.Code, response.Body.String())
	}
	for _, path := range []string{"/api/articles?month=2026-13", "/api/articles?month=2026-1", "/api/articles?month=garbage", "/api/articles?page=0"} {
		if r := request(handler, "GET", path, "", "", nil); r.Code != 400 {
			t.Fatal("invalid filter", path, r.Code)
		}
	}
	store.fail = true
	if r := request(handler, "GET", "/api/articles", "", "", nil); r.Code != 500 {
		t.Fatal("archive failure hidden", r.Code)
	}
}
