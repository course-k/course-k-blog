// Package publication writes the public state of the blog as files, so readers
// are served without the API or the database being awake.
package publication

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"

	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/external"
	"coursek/blog/backend/internal/images"
)

var ErrNotFound = errors.New("public file not found")

type Source interface {
	Page(ctx context.Context, page int, month string) (external.Page, error)
	GetPublic(ctx context.Context, id string) (blog.PublicPost, error)
	PublicImages(ctx context.Context) ([]images.Image, error)
}
type Media interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// Target is the public file store. Unlike private blobs, keys are overwritten.
type Target interface {
	Put(ctx context.Context, key string, body []byte, mime string) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}
type Publisher struct {
	Source Source
	Media  Media
	Target Target
}

const manifestKey = "data/manifest.json"
const jsonType = "application/json"

type manifest struct {
	Keys []string `json:"keys"`
}

// Refresh rewrites every public file from the current database state and
// removes files that stopped being public. It is safe to repeat: a run that
// fails part-way is completed by the next one.
func (p *Publisher) Refresh(ctx context.Context) error {
	previous, err := p.listed(ctx)
	if err != nil {
		return err
	}
	files, postIDs, err := p.pages(ctx)
	if err != nil {
		return err
	}
	for _, id := range postIDs {
		post, err := p.Source.GetPublic(ctx, id)
		if err != nil {
			return err
		}
		if files["data/posts/"+id+".json"], err = json.Marshal(post); err != nil {
			return err
		}
	}
	published, err := p.Source.PublicImages(ctx)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for key := range files {
		current[key] = true
	}
	for _, image := range published {
		current["api/images/"+image.ID] = true
	}
	// Record the union first. Whatever this run uploads stays listed even if it
	// fails, so a later run can still remove a file that is no longer public.
	if err = p.record(ctx, previous, current); err != nil {
		return err
	}
	for _, image := range published {
		key := "api/images/" + image.ID
		if previous[key] {
			continue // Image content never changes under one ID.
		}
		if err = p.copyImage(ctx, image, key); err != nil {
			return err
		}
	}
	for key, body := range files {
		if err = p.Target.Put(ctx, key, body, jsonType); err != nil {
			return err
		}
	}
	for key := range previous {
		if !current[key] {
			if err = p.Target.Delete(ctx, key); err != nil {
				return err
			}
		}
	}
	return p.record(ctx, current)
}

// pages renders the unfiltered list and one list per archive month, and
// returns the IDs of the owner's posts that appear in them.
func (p *Publisher) pages(ctx context.Context) (map[string][]byte, []string, error) {
	files := map[string][]byte{}
	postIDs := []string{}
	var months []external.ArchiveMonth
	render := func(month, directory string) error {
		for number := 1; ; number++ {
			page, err := p.Source.Page(ctx, number, month)
			if err != nil {
				return err
			}
			if month == "" {
				months = page.Months
				for _, article := range page.Articles {
					if article.Kind == "own" {
						postIDs = append(postIDs, article.ID)
					}
				}
			}
			if files["data/articles/"+directory+"/"+strconv.Itoa(number)+".json"], err = json.Marshal(page); err != nil {
				return err
			}
			if !page.HasNext {
				return nil
			}
		}
	}
	if err := render("", "all"); err != nil {
		return nil, nil, err
	}
	for _, month := range months {
		if err := render(month.Month, month.Month); err != nil {
			return nil, nil, err
		}
	}
	return files, postIDs, nil
}

func (p *Publisher) copyImage(ctx context.Context, image images.Image, key string) error {
	body, err := p.Media.Get(ctx, image.Key)
	if err != nil {
		return err
	}
	defer body.Close()
	content, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	return p.Target.Put(ctx, key, content, image.MIME)
}

func (p *Publisher) listed(ctx context.Context) (map[string]bool, error) {
	body, err := p.Target.Get(ctx, manifestKey)
	if errors.Is(err, ErrNotFound) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	var saved manifest
	if err = json.Unmarshal(body, &saved); err != nil {
		return nil, errors.New("public file list is unreadable")
	}
	keys := map[string]bool{}
	for _, key := range saved.Keys {
		keys[key] = true
	}
	return keys, nil
}

func (p *Publisher) record(ctx context.Context, sets ...map[string]bool) error {
	merged := map[string]bool{}
	for _, set := range sets {
		for key := range set {
			merged[key] = true
		}
	}
	saved := manifest{Keys: make([]string, 0, len(merged))}
	for key := range merged {
		saved.Keys = append(saved.Keys, key)
	}
	sort.Strings(saved.Keys)
	body, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return p.Target.Put(ctx, manifestKey, body, jsonType)
}
