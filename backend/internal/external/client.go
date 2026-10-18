package external

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/temoto/robotstxt"
)

const userAgent = "CourseKBlog/1.0"
const maxResponseBytes = 8 * 1024 * 1024

type Client struct{ HTTP *http.Client }

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Errors are stable codes, never upstream bodies or authentication details.
func (c *Client) get(ctx context.Context, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, errors.New("request_invalid")
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, errors.New("upstream_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("upstream_%d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return nil, errors.New("response_too_large_or_incomplete")
	}
	return body, nil
}
func (c *Client) Fetch(ctx context.Context, source string) (Batch, error) {
	var batch Batch
	var err error
	switch source {
	case "qiita":
		batch, err = c.qiita(ctx)
	case "note":
		batch, err = c.note(ctx)
	case "wantedly":
		batch, err = c.wantedly(ctx)
	default:
		return batch, errors.New("source_invalid")
	}
	if err != nil {
		return Batch{}, err
	}
	seen := map[string]bool{}
	for _, post := range batch.Posts {
		if !ValidPost(post) || seen[post.ID] {
			return Batch{}, errors.New("metadata_invalid")
		}
		seen[post.ID] = true
	}
	return batch, nil
}
func (c *Client) qiita(ctx context.Context) (Batch, error) {
	batch := Batch{Coverage: "recent", Posts: []Post{}}
	// Keep the unauthenticated hourly allowance available for manual retries.
	for page := 1; page <= 20; page++ {
		body, err := c.get(ctx, fmt.Sprintf("https://qiita.com/api/v2/users/course_k/items?per_page=100&page=%d", page))
		if err != nil {
			return Batch{}, err
		}
		var items []struct {
			ID, Title, URL string
			CreatedAt      time.Time `json:"created_at"`
			User           struct{ ID string }
			Private        bool
		}
		if json.Unmarshal(body, &items) != nil || string(body) == "null" {
			return Batch{}, errors.New("format_changed")
		}
		for _, item := range items {
			if item.User.ID != "course_k" || item.Private || !strings.HasPrefix(item.URL, "https://qiita.com/course_k/items/") {
				return Batch{}, errors.New("owner_mismatch")
			}
			batch.Posts = append(batch.Posts, Post{Source: "qiita", ID: item.ID, Title: item.Title, URL: item.URL, PublishedAt: item.CreatedAt})
		}
		if len(items) < 100 {
			batch.Coverage = "all"
			return batch, nil
		}
	}
	return batch, nil
}
func (c *Client) note(ctx context.Context) (Batch, error) {
	body, err := c.get(ctx, "https://note.com/course__k/rss")
	if err != nil {
		return Batch{}, err
	}
	var feed struct {
		XMLName xml.Name `xml:"rss"`
		Channel *struct {
			Items []struct {
				Title string `xml:"title"`
				Link  string `xml:"link"`
				Date  string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if xml.Unmarshal(body, &feed) != nil || feed.Channel == nil {
		return Batch{}, errors.New("format_changed")
	}
	batch := Batch{Coverage: "recent", Posts: []Post{}}
	for _, item := range feed.Channel.Items {
		if !strings.HasPrefix(item.Link, "https://note.com/course__k/n/") {
			return Batch{}, errors.New("owner_mismatch")
		}
		stamp, err := time.Parse(time.RFC1123Z, item.Date)
		if err != nil {
			stamp, err = time.Parse(time.RFC1123, item.Date)
		}
		if err != nil {
			return Batch{}, errors.New("date_invalid")
		}
		u, err := url.Parse(item.Link)
		if err != nil {
			return Batch{}, errors.New("url_invalid")
		}
		id := strings.TrimPrefix(u.Path, "/course__k/n/")
		batch.Posts = append(batch.Posts, Post{Source: "note", ID: id, Title: item.Title, URL: item.Link, PublishedAt: stamp})
	}
	return batch, nil
}

var nextDataScript = regexp.MustCompile(`(?s)<script\b[^>]*\bid=["']__NEXT_DATA__["'][^>]*>(.*?)</script>`)

func (c *Client) wantedly(ctx context.Context) (Batch, error) {
	robotsBody, err := c.get(ctx, "https://www.wantedly.com/robots.txt")
	if err != nil {
		return Batch{}, err
	}
	rules, err := robotstxt.FromBytes(robotsBody)
	if err != nil || !rules.TestAgent("/id/course_k/story-list", userAgent) {
		return Batch{}, errors.New("robots_disallowed")
	}
	// Respect a published delay without waiting beyond the bounded sync context.
	if delay := rules.FindGroup(userAgent).CrawlDelay; delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return Batch{}, errors.New("upstream_unavailable")
		}
	}
	body, err := c.get(ctx, "https://www.wantedly.com/id/course_k/story-list")
	if err != nil {
		return Batch{}, err
	}
	return parseWantedly(body)
}
func parseWantedly(body []byte) (Batch, error) {
	matches := nextDataScript.FindSubmatch(body)
	if len(matches) != 2 {
		return Batch{}, errors.New("format_changed")
	}
	var page struct {
		Props struct {
			PageProps struct {
				UserSlug string `json:"userSlug"`
				Apollo   struct {
					State map[string]json.RawMessage `json:"graphqlGatewayInitialState"`
				} `json:"__apollo"`
			} `json:"pageProps"`
		}
	}
	if json.Unmarshal(matches[1], &page) != nil || page.Props.PageProps.UserSlug != "course_k" {
		return Batch{}, errors.New("owner_mismatch")
	}
	batch := Batch{Coverage: "recent", Posts: []Post{}}
	for key, value := range page.Props.PageProps.Apollo.State {
		if !strings.HasPrefix(key, "ProfilePageStory:") {
			continue
		}
		var item struct {
			ID, Title   string
			URL         string `json:"storyUrl"`
			PublishedAt time.Time
		}
		if json.Unmarshal(value, &item) != nil || !strings.HasSuffix(item.URL, "/post_articles/"+item.ID) {
			return Batch{}, errors.New("format_changed")
		}
		batch.Posts = append(batch.Posts, Post{Source: "wantedly", ID: item.ID, Title: item.Title, URL: item.URL, PublishedAt: item.PublishedAt})
	}
	// An empty cache cannot distinguish no articles from a changed page schema.
	if len(batch.Posts) == 0 {
		return Batch{}, errors.New("format_changed")
	}
	return batch, nil
}
