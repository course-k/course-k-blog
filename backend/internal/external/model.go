// Package external imports only metadata of the owner's articles, never their bodies.
package external

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid external article correction")
var ErrBusy = errors.New("sync lease is no longer owned")

func ValidSource(source string) bool {
	return source == "qiita" || source == "note" || source == "wantedly"
}

type Post struct {
	Source        string     `json:"source"`
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	URL           string     `json:"url"`
	PublishedAt   time.Time  `json:"publishedAt"`
	TitleOverride *string    `json:"titleOverride"`
	DateOverride  *time.Time `json:"dateOverride"`
	Hidden        bool       `json:"hidden"`
	Version       int64      `json:"version"`
}
type Correction struct {
	Title       *string    `json:"titleOverride"`
	PublishedAt *time.Time `json:"dateOverride"`
	Hidden      bool       `json:"hidden"`
	Version     int64      `json:"version"`
}
type Article struct {
	Kind        string    `json:"kind"`
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	PublishedAt time.Time `json:"publishedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Source      string    `json:"source,omitempty"`
	URL         string    `json:"url,omitempty"`
}
type ArchiveMonth struct {
	Month string `json:"month"`
	Count int    `json:"count"`
}

var monthPattern = regexp.MustCompile(`^[1-9][0-9]{3}-(0[1-9]|1[0-2])$`)

func ValidMonth(month string) bool { return month == "" || monthPattern.MatchString(month) }

type Page struct {
	Months   []ArchiveMonth `json:"months"`
	Articles []Article      `json:"articles"`
	HasNext  bool           `json:"hasNext"`
}
type SyncState struct {
	Source      string     `json:"source"`
	Status      string     `json:"status"`
	Coverage    string     `json:"coverage"`
	Count       int        `json:"count"`
	ErrorCode   string     `json:"errorCode"`
	LastAttempt *time.Time `json:"lastAttempt"`
	LastSuccess *time.Time `json:"lastSuccess"`
	NextRun     time.Time  `json:"nextRun"`
}
type Batch struct {
	Posts    []Post
	Coverage string
}
type Repository interface {
	ListArticles(context.Context, int, int, string) ([]Article, error)
	ArticleMonths(context.Context) ([]ArchiveMonth, error)
	ListExternal(context.Context) ([]Post, error)
	CorrectExternal(context.Context, string, string, Correction) error
	SyncStates(context.Context) ([]SyncState, error)
	RequestSync(context.Context, string) error
	ClaimSync(context.Context, string) (string, error)
	FinishSync(context.Context, string, string, Batch, string) error
}

func ValidPost(p Post) bool {
	u, err := url.Parse(p.URL)
	hosts := map[string]string{"qiita": "qiita.com", "note": "note.com", "wantedly": "www.wantedly.com"}
	return err == nil && ValidSource(p.Source) && p.ID != "" && len(p.ID) <= 200 && u.Scheme == "https" && u.Host == hosts[p.Source] && u.User == nil && u.Fragment == "" && strings.TrimSpace(p.Title) != "" && utf8.ValidString(p.Title) && utf8.RuneCountInString(p.Title) <= 500 && !p.PublishedAt.IsZero()
}
