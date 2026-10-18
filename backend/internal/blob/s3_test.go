package blob

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

func TestS3UsesPrivateImmutableWritesAndDistinguishesMissingFromDenied(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	requests := 0
	forbidden := "/private-bucket/images/" + strings.Repeat("b", 43) + ".png"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		fail := func(status int, code string) {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(status)
			io.WriteString(w, "<Error><Code>"+code+"</Code><Message>sensitive-upstream-detail</Message></Error>")
		}
		if r.URL.Path == forbidden {
			fail(403, "AccessDenied")
			return
		}
		switch r.Method {
		case "PUT":
			if r.Header.Get("If-None-Match") != "*" || r.Header.Get("X-Amz-Server-Side-Encryption") != "AES256" || r.Header.Get("X-Amz-Acl") != "" {
				t.Error("unsafe object write headers")
			}
			if _, exists := objects[r.URL.Path]; exists {
				fail(412, "PreconditionFailed")
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				fail(400, "InvalidRequest")
				return
			}
			objects[r.URL.Path] = body
		case "GET":
			body, ok := objects[r.URL.Path]
			if !ok {
				fail(404, "NoSuchKey")
				return
			}
			w.Write(body)
		case "DELETE":
			if r.URL.Query().Has("versionId") {
				t.Error("permanent version deletion requested")
			}
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
		default:
			fail(400, "UnexpectedMethod")
		}
	}))
	defer server.Close()
	storage := S3{Bucket: "private-bucket", Client: s3.New(s3.Options{Region: "us-west-2", BaseEndpoint: aws.String(server.URL), UsePathStyle: true, Credentials: aws.AnonymousCredentials{}})}
	ctx := context.Background()
	key := "images/" + strings.Repeat("a", 43) + ".png"
	if _, err := storage.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing object not recognized", err)
	}
	if _, err := storage.Get(ctx, "images/"+strings.Repeat("b", 43)+".png"); err == nil || errors.Is(err, ErrNotFound) || strings.Contains(err.Error(), "sensitive-upstream-detail") {
		t.Fatal("denied access treated as missing or upstream details exposed")
	}
	if err := storage.Put(ctx, key, []byte("immutable-image"), "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := storage.Put(ctx, key, []byte("replacement"), "image/png"); err == nil || strings.Contains(err.Error(), "sensitive-upstream-detail") {
		t.Fatal("immutable overwrite accepted or error leaked")
	}
	object, err := storage.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(object)
	object.Close()
	if err != nil || string(body) != "immutable-image" {
		t.Fatal("original object changed", err)
	}
	mu.Lock()
	before := requests
	mu.Unlock()
	if storage.Put(ctx, "../outside", nil, "") == nil {
		t.Fatal("unsafe key accepted")
	}
	mu.Lock()
	after := requests
	mu.Unlock()
	if before != after {
		t.Fatal("unsafe key reached S3")
	}
	if err := storage.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted object still present", err)
	}
}
