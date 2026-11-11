package publication

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type storedObject struct {
	body        []byte
	contentType string
}

// fakeS3 answers the object requests the bucket target makes. Keys listed in
// denied are refused the way a missing permission would be.
func fakeS3(t *testing.T, denied ...string) (Bucket, map[string]storedObject, *int) {
	t.Helper()
	var mu sync.Mutex
	objects := map[string]storedObject{}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		fail := func(status int, code string) {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(status)
			io.WriteString(w, "<Error><Code>"+code+"</Code><Message>sensitive-upstream-detail</Message></Error>")
		}
		key := strings.TrimPrefix(r.URL.Path, "/public-bucket/")
		for _, refused := range denied {
			if key == refused {
				fail(403, "AccessDenied")
				return
			}
		}
		switch r.Method {
		case "PUT":
			if r.Header.Get("If-None-Match") != "" {
				t.Error("public files are replaced; the write must not be conditional")
			}
			if r.Header.Get("X-Amz-Server-Side-Encryption") != "AES256" || r.Header.Get("X-Amz-Acl") != "" {
				t.Error("objects must be encrypted and must not carry an ACL")
			}
			body, _ := io.ReadAll(r.Body)
			objects[key] = storedObject{body, r.Header.Get("Content-Type")}
		case "GET":
			object, ok := objects[key]
			if !ok {
				fail(404, "NoSuchKey")
				return
			}
			w.Write(object.body)
		case "DELETE":
			if r.URL.Query().Has("versionId") {
				t.Error("permanent version deletion requested")
			}
			delete(objects, key)
			w.WriteHeader(204)
		default:
			fail(400, "UnexpectedMethod")
		}
	}))
	t.Cleanup(server.Close)
	client := s3.New(s3.Options{Region: "us-west-2", BaseEndpoint: aws.String(server.URL), UsePathStyle: true, Credentials: aws.AnonymousCredentials{}})
	return Bucket{Client: client, Name: "public-bucket"}, objects, &requests
}

func TestBucketReplacesReadsAndDeletesObjectsWithTheirType(t *testing.T) {
	ctx := context.Background()
	target, objects, _ := fakeS3(t)
	key := "data/articles/all/1.json"
	if _, err := target.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing object must be reported as not found: %v", err)
	}
	for _, body := range []string{"first", "second"} {
		if err := target.Put(ctx, key, []byte(body), "application/json"); err != nil {
			t.Fatal(err)
		}
	}
	if body, err := target.Get(ctx, key); err != nil || string(body) != "second" {
		t.Fatalf("the later write must replace the earlier one: %q %v", body, err)
	}
	if err := target.Put(ctx, "api/images/img1", []byte("png"), "image/png"); err != nil {
		t.Fatal(err)
	}
	if objects["api/images/img1"].contentType != "image/png" || objects[key].contentType != "application/json" {
		t.Fatalf("each object must keep its type for the browser: %+v", objects)
	}
	if err := target.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, left := objects[key]; left {
		t.Fatal("the deleted object must be gone")
	}
}

func TestBucketDoesNotMistakeADeniedReadForAFirstRun(t *testing.T) {
	// Treating a refused manifest read as "nothing published yet" would forget
	// every old file, and unpublished content could then stay public.
	target, _, _ := fakeS3(t, "data/manifest.json")
	_, err := target.Get(context.Background(), "data/manifest.json")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("a denied read must be an error other than not found: %v", err)
	}
	if strings.Contains(err.Error(), "sensitive-upstream-detail") {
		t.Fatal("upstream details must not be exposed")
	}
	source, media, _ := fixture()
	if err := (&Publisher{Source: source, Media: media, Target: target}).Refresh(context.Background()); err == nil {
		t.Fatal("a refresh that cannot read the file list must fail")
	}
}

func TestBucketRejectsKeysOutsideThePublicLayout(t *testing.T) {
	target, _, requests := fakeS3(t)
	for _, key := range []string{"../secret", "backups/a.dump", "images/a.png", "data/posts/../x.json"} {
		if target.Put(context.Background(), key, []byte("x"), "") == nil || target.Delete(context.Background(), key) == nil {
			t.Fatalf("%q must be rejected", key)
		}
		if _, err := target.Get(context.Background(), key); err == nil {
			t.Fatalf("%q must be rejected", key)
		}
	}
	if *requests != 0 {
		t.Fatal("a rejected key must not reach storage")
	}
}

func TestRefreshWorksAgainstABucket(t *testing.T) {
	source, media, _ := fixture()
	target, objects, _ := fakeS3(t)
	if err := (&Publisher{Source: source, Media: media, Target: target}).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if string(objects["api/images/img1"].body) != "png-bytes" || len(objects["data/posts/a.json"].body) == 0 {
		t.Fatalf("published files are missing: %d objects", len(objects))
	}
}
