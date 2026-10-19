// externalimport is a one-shot operator command, never a public import API.
package main

import (
	"context"
	"coursek/blog/backend/internal/external"
	"coursek/blog/backend/internal/mysql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

type metadata struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt"`
}

func parseItems(raw string) ([]external.Post, error) {
	if len(raw) > 4096 {
		return nil, external.ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var items []metadata
	if err := decoder.Decode(&items); err != nil {
		return nil, external.ErrInvalid
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || len(items) == 0 || len(items) > 20 {
		return nil, external.ErrInvalid
	}
	posts := make([]external.Post, 0, len(items))
	for _, item := range items {
		p := external.Post{Source: "wantedly", ID: item.ID, Title: item.Title, URL: item.URL, PublishedAt: item.PublishedAt}
		u, err := url.Parse(p.URL)
		if !external.ValidPost(p) || err != nil || u.RawQuery != "" || !strings.HasSuffix(u.Path, "/post_articles/"+p.ID) {
			return nil, external.ErrInvalid
		}
		posts = append(posts, p)
	}
	return posts, nil
}

func run(ctx context.Context, confirmed bool) (int, error) {
	u, err := url.Parse(os.Getenv("DATABASE_URL"))
	host := os.Getenv("EXPECTED_DB_HOST")
	if !confirmed || err != nil || u.Scheme != "mysql" || host == "" || u.Hostname() != host || u.Path != "/blog" || u.Query().Get("tls") != "verify-full" {
		return 0, errors.New("explicit owner confirmation and database identity required")
	}
	posts, err := parseItems(os.Getenv("EXTERNAL_IMPORT_JSON"))
	if err != nil {
		return 0, err
	}
	store, err := mysql.Open(ctx, u.String())
	if err != nil {
		return 0, err
	}
	defer store.Close()
	return store.ImportMissingExternal(ctx, posts)
}

func main() {
	confirmed := flag.Bool("confirm-owner-metadata", false, "Operator has verified every item belongs to the owner")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	n, err := run(ctx, *confirmed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "External metadata import failed; inspect inputs and task permissions")
		os.Exit(1)
	}
	fmt.Printf("External metadata import verified: inserted=%d\n", n)
}
