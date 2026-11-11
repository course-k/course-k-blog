package publication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/external"
	"coursek/blog/backend/internal/images"
)

// fakeSource holds the public state the way the database would report it.
type fakeSource struct {
	articles []external.Article
	posts    map[string]blog.PublicPost
	images   []images.Image
}

func (f *fakeSource) Page(_ context.Context, page int, month string) (external.Page, error) {
	matching := []external.Article{}
	counts := map[string]int{}
	for _, article := range f.articles {
		key := article.PublishedAt.Format("2006-01")
		counts[key]++
		if month == "" || month == key {
			matching = append(matching, article)
		}
	}
	months := []external.ArchiveMonth{}
	for month, count := range counts {
		months = append(months, external.ArchiveMonth{Month: month, Count: count})
	}
	sort.Slice(months, func(i, j int) bool { return months[i].Month > months[j].Month })
	start := min((page-1)*20, len(matching))
	end := min(start+20, len(matching))
	return external.Page{Months: months, Articles: matching[start:end], HasNext: end < len(matching)}, nil
}
func (f *fakeSource) GetPublic(_ context.Context, id string) (blog.PublicPost, error) {
	post, ok := f.posts[id]
	if !ok {
		return post, blog.ErrNotFound
	}
	return post, nil
}
func (f *fakeSource) PublicImages(context.Context) ([]images.Image, error) { return f.images, nil }

type fakeMedia map[string]string

func (f fakeMedia) Get(_ context.Context, key string) (io.ReadCloser, error) {
	body, ok := f[key]
	if !ok {
		return nil, errors.New("missing image body")
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

type object struct{ body, mime string }

// fakeTarget records the public files and can refuse one key to simulate an outage.
type fakeTarget struct {
	objects map[string]object
	failPut string
	puts    []string
}

func (f *fakeTarget) Put(_ context.Context, key string, body []byte, mime string) error {
	if key == f.failPut {
		return errors.New("write refused")
	}
	f.objects[key] = object{string(body), mime}
	f.puts = append(f.puts, key)
	return nil
}
func (f *fakeTarget) Get(_ context.Context, key string) ([]byte, error) {
	found, ok := f.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return []byte(found.body), nil
}
func (f *fakeTarget) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

func own(id string, day int) external.Article {
	return external.Article{Kind: "own", ID: id, Title: "post " + id, PublishedAt: time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC)}
}
func linked(id string, month time.Month) external.Article {
	return external.Article{Kind: "external", ID: "qiita:" + id, Title: "link " + id, Source: "qiita", URL: "https://qiita.com/course_k/items/" + id, PublishedAt: time.Date(2026, month, 1, 0, 0, 0, 0, time.UTC)}
}

func fixture() (*fakeSource, fakeMedia, *fakeTarget) {
	source := &fakeSource{
		articles: []external.Article{own("a", 2), linked("x", time.October)},
		posts:    map[string]blog.PublicPost{"a": {ID: "a", Title: "post a", Markdown: "body ![](/api/images/img1)"}},
		images:   []images.Image{{ID: "img1", Key: "images/img1.png", MIME: "image/png"}},
	}
	return source, fakeMedia{"images/img1.png": "png-bytes"}, &fakeTarget{objects: map[string]object{}}
}

func keys(target *fakeTarget) []string {
	out := []string{}
	for key := range target.objects {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func decodePage(t *testing.T, target *fakeTarget, key string) external.Page {
	t.Helper()
	var page external.Page
	if err := json.Unmarshal([]byte(target.objects[key].body), &page); err != nil {
		t.Fatalf("%s is not a page: %v", key, err)
	}
	return page
}

func TestRefreshWritesEveryPublicFile(t *testing.T) {
	source, media, target := fixture()
	if err := (&Publisher{Source: source, Media: media, Target: target}).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"api/images/img1",
		"data/articles/2026-10/1.json",
		"data/articles/2026-11/1.json",
		"data/articles/all/1.json",
		"data/manifest.json",
		"data/posts/a.json",
	}
	if got := keys(target); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("public files\n got %v\nwant %v", got, want)
	}
	if all := decodePage(t, target, "data/articles/all/1.json"); len(all.Articles) != 2 || len(all.Months) != 2 || all.HasNext {
		t.Fatalf("unfiltered page lost articles or months: %+v", all)
	}
	if october := decodePage(t, target, "data/articles/2026-10/1.json"); len(october.Articles) != 1 || october.Articles[0].ID != "qiita:x" {
		t.Fatalf("month page must hold only that month: %+v", october)
	}
	var post blog.PublicPost
	if err := json.Unmarshal([]byte(target.objects["data/posts/a.json"].body), &post); err != nil || post.Markdown != "body ![](/api/images/img1)" {
		t.Fatalf("post body not published: %+v %v", post, err)
	}
	if image := target.objects["api/images/img1"]; image.body != "png-bytes" || image.mime != "image/png" {
		t.Fatalf("image must keep its bytes and type: %+v", image)
	}
	if page := target.objects["data/posts/a.json"]; page.mime != "application/json" {
		t.Fatalf("JSON files need a JSON type: %q", page.mime)
	}
}

func TestRefreshSplitsLongListsIntoPages(t *testing.T) {
	source, media, target := fixture()
	source.articles, source.posts, source.images = nil, map[string]blog.PublicPost{}, nil
	for i := 0; i < 21; i++ {
		source.articles = append(source.articles, linked(fmt.Sprintf("n%02d", i), time.September))
	}
	if err := (&Publisher{Source: source, Media: media, Target: target}).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, second := decodePage(t, target, "data/articles/all/1.json"), decodePage(t, target, "data/articles/all/2.json")
	if len(first.Articles) != 20 || !first.HasNext || len(second.Articles) != 1 || second.HasNext {
		t.Fatalf("pages: first %d next=%v, second %d next=%v", len(first.Articles), first.HasNext, len(second.Articles), second.HasNext)
	}
	if _, extra := target.objects["data/articles/all/3.json"]; extra {
		t.Fatal("no page may follow the last one")
	}
}

func TestRefreshRemovesWhatIsNoLongerPublic(t *testing.T) {
	source, media, target := fixture()
	publisher := &Publisher{Source: source, Media: media, Target: target}
	if err := publisher.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The owner unpublishes the only own post; its image stops being public too.
	source.articles, source.posts, source.images = []external.Article{linked("x", time.October)}, map[string]blog.PublicPost{}, nil
	if err := publisher.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"data/articles/2026-10/1.json", "data/articles/all/1.json", "data/manifest.json"}
	if got := keys(target); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("after unpublishing\n got %v\nwant %v", got, want)
	}
}

func TestRefreshCopiesAnImageOnlyOnce(t *testing.T) {
	source, media, target := fixture()
	publisher := &Publisher{Source: source, Media: media, Target: target}
	if err := publisher.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	target.puts = nil
	if err := publisher.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, key := range target.puts {
		if key == "api/images/img1" {
			t.Fatal("an image already published must not be uploaded again")
		}
	}
}

func TestFailedRefreshKeepsTrackOfOldFiles(t *testing.T) {
	source, media, target := fixture()
	publisher := &Publisher{Source: source, Media: media, Target: target}
	if err := publisher.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	source.articles, source.posts, source.images = []external.Article{linked("x", time.October)}, map[string]blog.PublicPost{}, nil
	target.failPut = "data/articles/all/1.json"
	if err := publisher.Refresh(context.Background()); err == nil {
		t.Fatal("a refused write must fail the refresh")
	}
	if !strings.Contains(target.objects["data/manifest.json"].body, `"data/posts/a.json"`) {
		t.Fatal("the manifest must still list the old files so a retry can remove them")
	}
	// The retry succeeds and removes the unpublished post that the failed run left behind.
	target.failPut = ""
	if err := publisher.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, stale := target.objects["data/posts/a.json"]; stale {
		t.Fatal("retry must remove the unpublished post")
	}
}

func TestFilesFromAFailedRefreshAreRemovedOnceTheyStopBeingPublic(t *testing.T) {
	source, media, target := fixture()
	publisher := &Publisher{Source: source, Media: media, Target: target}
	// The very first run uploads the image, then fails before finishing.
	target.failPut = "data/articles/all/1.json"
	if err := publisher.Refresh(context.Background()); err == nil {
		t.Fatal("a refused write must fail the refresh")
	}
	// Before any retry, the owner unpublishes the post.
	source.articles, source.posts, source.images = []external.Article{linked("x", time.October)}, map[string]blog.PublicPost{}, nil
	target.failPut = ""
	if err := publisher.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"api/images/img1", "data/posts/a.json"} {
		if _, left := target.objects[key]; left {
			t.Fatalf("%s was uploaded by the failed run and must not stay public", key)
		}
	}
}

func TestRefreshWritesNothingWhenThePublicStateIsUnchanged(t *testing.T) {
	source, media, target := fixture()
	publisher := &Publisher{Source: source, Media: media, Target: target}
	if err := publisher.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	target.puts = nil
	if err := publisher.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(target.puts) != 0 {
		t.Fatalf("an unchanged blog must not be rewritten: %v", target.puts)
	}
}

func TestCurrentReportsWhetherReadersSeeTheLatestState(t *testing.T) {
	source, media, target := fixture()
	publisher := &Publisher{Source: source, Media: media, Target: target}
	current := func() bool {
		t.Helper()
		ok, err := publisher.Current(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if current() {
		t.Fatal("nothing has been published yet")
	}
	if err := publisher.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !current() {
		t.Fatal("a finished refresh must be current")
	}
	source.posts["a"] = blog.PublicPost{ID: "a", Title: "post a", Markdown: "edited and republished"}
	if current() {
		t.Fatal("a republished body must be reported as not yet delivered")
	}
	target.failPut = "data/posts/a.json"
	if err := publisher.Refresh(context.Background()); err == nil {
		t.Fatal("a refused write must fail the refresh")
	}
	if current() {
		t.Fatal("a failed refresh must not be reported as current")
	}
}

// A counter standing in for the cache in front of the public files.
type fakeCache struct {
	purges  int
	failure error
}

func (f *fakeCache) purge(context.Context) error {
	f.purges++
	return f.failure
}

func TestRefreshPurgesTheCacheOnlyWhenFilesChanged(t *testing.T) {
	source, media, target := fixture()
	cache := &fakeCache{}
	publisher := &Publisher{Source: source, Media: media, Target: target, Purge: cache.purge}
	if err := publisher.Refresh(context.Background()); err != nil || cache.purges != 1 {
		t.Fatalf("the first publication must purge once: %v, %d", err, cache.purges)
	}
	if err := publisher.Refresh(context.Background()); err != nil || cache.purges != 1 {
		t.Fatalf("an unchanged blog must not purge: %v, %d", err, cache.purges)
	}
	source.posts["a"] = blog.PublicPost{ID: "a", Title: "post a", Markdown: "republished"}
	if err := publisher.Refresh(context.Background()); err != nil || cache.purges != 2 {
		t.Fatalf("a republished post must purge again: %v, %d", err, cache.purges)
	}
}

func TestFailedPurgeIsRetriedByTheNextRefresh(t *testing.T) {
	source, media, target := fixture()
	cache := &fakeCache{failure: errors.New("cache unavailable")}
	publisher := &Publisher{Source: source, Media: media, Target: target, Purge: cache.purge}
	if err := publisher.Refresh(context.Background()); err == nil {
		t.Fatal("readers may still receive old pages, so the refresh must fail")
	}
	if current, err := publisher.Current(context.Background()); err != nil || current {
		t.Fatalf("a refresh whose purge failed must not be reported as delivered: %v %v", current, err)
	}
	cache.failure = nil
	if err := publisher.Refresh(context.Background()); err != nil || cache.purges != 2 {
		t.Fatalf("the retry must purge again even though the files are already written: %v, %d", err, cache.purges)
	}
}
