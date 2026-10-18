package blog

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("version conflict")
var ErrInvalid = errors.New("invalid article")

// Working text and the published snapshot are deliberately separate: saving a
// draft must never overwrite what readers currently see.
type Post struct {
	ID                    string     `json:"id"`
	Title                 string     `json:"title"`
	Markdown              string     `json:"markdown"`
	PublishedTitle        string     `json:"-"`
	PublishedMarkdown     string     `json:"-"`
	PublishedAt           *time.Time `json:"publishedAt"`
	Published             bool       `json:"published"`
	HasUnpublishedChanges bool       `json:"hasUnpublishedChanges"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	Version               int64      `json:"version"`
}

type PublicPost struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Markdown    string    `json:"markdown"`
	PublishedAt time.Time `json:"publishedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// List responses deliberately exclude article bodies.
type PublicPostSummary struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	PublishedAt time.Time `json:"publishedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Session struct {
	Hash      string
	ExpiresAt time.Time
}

type Store interface {
	ListPublic(context.Context, int, int) ([]PublicPostSummary, error)
	GetPublic(context.Context, string) (PublicPost, error)
	ListAdmin(context.Context) ([]Post, error)
	GetAdmin(context.Context, string) (Post, error)
	Create(context.Context, Post) error
	Save(context.Context, string, string, string, int64, time.Time) (Post, error)
	Publish(context.Context, string, int64, time.Time) (Post, error)
	Unpublish(context.Context, string, int64, time.Time) (Post, error)
	PutSession(context.Context, Session) error
	HasSession(context.Context, string, time.Time) (bool, error)
	DeleteSession(context.Context, string) error
	AllowLogin(context.Context, time.Time) (bool, error)
	Ping(context.Context) error
}

const (
	maxTitleCodePoints = 200
	maxMarkdownBytes   = 256 * 1024
)

func Validate(title, markdown string) error {
	if !utf8.ValidString(title) || !utf8.ValidString(markdown) {
		return ErrInvalid
	}
	if strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > maxTitleCodePoints {
		return ErrInvalid
	}
	if len(markdown) > maxMarkdownBytes {
		return ErrInvalid
	}
	return nil
}
