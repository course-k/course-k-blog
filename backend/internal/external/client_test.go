package external

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureClient(body string, status int) *Client {
	return &Client{HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
}
func TestQiitaMetadataAndOwnerBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, owner string
		valid       bool
	}{{"owner", "course_k", true}, {"other author", "someone_else", false}} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`[{"id":"q1","title":"Article","url":"https://qiita.com/course_k/items/q1","created_at":"2026-01-01T00:00:00Z","user":{"id":%q},"body":"not retained"}]`, tc.owner)
			batch, err := fixtureClient(body, 200).Fetch(context.Background(), "qiita")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && (len(batch.Posts) != 1 || batch.Coverage != "all" || batch.Posts[0].Title != "Article") {
				t.Fatalf("unexpected metadata: %+v", batch)
			}
		})
	}
}
func TestNoteLimitedFeedAndUnsafeURL(t *testing.T) {
	for _, tc := range []struct {
		name, link string
		valid      bool
	}{{"owner", "https://note.com/course__k/n/n123", true}, {"foreign origin", "https://evil.invalid/n123", false}, {"other owner", "https://note.com/else/n/n123", false}} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`<rss><channel><item><title>A &amp; B</title><link>%s</link><pubDate>Mon, 05 Jan 2026 00:00:00 +0000</pubDate></item></channel></rss>`, tc.link)
			batch, err := fixtureClient(body, 200).Fetch(context.Background(), "note")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && (batch.Coverage != "recent" || batch.Posts[0].Title != "A & B") {
				t.Fatal("feed metadata not retained")
			}
		})
	}
}
func TestFetchFailureDoesNotReturnPartialSuccess(t *testing.T) {
	for _, status := range []int{429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			_, err := fixtureClient(`[]`, status).Fetch(context.Background(), "qiita")
			if err == nil {
				t.Fatal("upstream failure accepted")
			}
		})
	}
	if _, err := fixtureClient(`<html>sign in</html>`, 200).Fetch(context.Background(), "note"); err == nil {
		t.Fatal("HTML accepted as empty feed")
	}
}

func TestWantedlyMetadataAndSchemaChange(t *testing.T) {
	body := []byte(`<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"userSlug":"course_k","__apollo":{"graphqlGatewayInitialState":{"ProfilePageStory:1":{"id":"1","title":"Story","storyUrl":"https://www.wantedly.com/companies/example/post_articles/1","publishedAt":"2026-01-01T00:00:00Z"}}}}}}</script>`)
	batch, err := parseWantedly(body)
	if err != nil || len(batch.Posts) != 1 || !ValidPost(batch.Posts[0]) || batch.Coverage != "recent" {
		t.Fatalf("metadata invalid: %v %+v", err, batch)
	}
	if _, err := parseWantedly([]byte(strings.ReplaceAll(string(body), "course_k", "someone_else"))); err == nil {
		t.Fatal("foreign profile accepted")
	}
	if _, err := parseWantedly([]byte(`<html>new schema</html>`)); err == nil {
		t.Fatal("schema change accepted as no articles")
	}
}
func TestWantedlyRobotsDisallowPreventsProfileFetch(t *testing.T) {
	calls := 0
	client := &Client{HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/robots.txt" {
			t.Fatal("profile fetched despite disallow")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("User-agent: *\nDisallow: /id/"))}, nil
	})}}
	if _, err := client.Fetch(context.Background(), "wantedly"); err == nil || calls != 1 {
		t.Fatal("robots protection missing", err, calls)
	}
}

func TestLiveOwnerMetadata(t *testing.T) {
	if os.Getenv("TEST_LIVE_EXTERNAL") != "1" {
		t.Skip("live public metadata checks are opt-in")
	}
	for _, source := range []string{"qiita", "note", "wantedly"} {
		t.Run(source, func(t *testing.T) {
			batch, err := NewClient().Fetch(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("metadata count=%d coverage=%s; bodies are not persisted", len(batch.Posts), batch.Coverage)
		})
	}
}
