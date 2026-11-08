package blob

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestStagingPostPolicyBindsKeyTypeSizeAndEncryption(t *testing.T) {
	storage := S3{Bucket: "private-bucket", Client: s3.New(s3.Options{Region: "us-west-2", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "session"}, nil
	})})}
	ctx := context.Background()
	key := "images/staging/" + strings.Repeat("s", 43)
	url, fields, err := storage.PresignStagingPost(ctx, key, "image/webp", 1234, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "https://private-bucket.s3.us-west-2.amazonaws.com") || fields["key"] != key || fields["Content-Type"] != "image/webp" || fields["x-amz-server-side-encryption"] != "AES256" || fields["acl"] != "" {
		t.Fatal("form fields", url, fields)
	}
	raw, err := base64.StdEncoding.DecodeString(fields["policy"])
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Expiration time.Time `json:"expiration"`
		Conditions []any     `json:"conditions"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	if time.Until(policy.Expiration) > 5*time.Minute+time.Second {
		t.Fatal("signature lives longer than five minutes", policy.Expiration)
	}
	encoded, _ := json.Marshal(policy.Conditions)
	for _, want := range []string{`{"key":"` + key + `"}`, `{"Content-Type":"image/webp"}`, `{"x-amz-server-side-encryption":"AES256"}`, `["content-length-range",1234,1234]`, `{"bucket":"private-bucket"}`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("policy lacks %s: %s", want, encoded)
		}
	}
	for _, bad := range []struct {
		key      string
		size     int64
		lifetime time.Duration
	}{
		{"images/" + strings.Repeat("s", 43) + ".png", 10, time.Minute},
		{"backups/" + strings.Repeat("s", 43) + ".json", 10, time.Minute},
		{key, 0, time.Minute},
		{key, 10_000_001, time.Minute},
		{key, 10, 6 * time.Minute},
	} {
		if _, _, err := storage.PresignStagingPost(ctx, bad.key, "image/png", bad.size, bad.lifetime); err == nil {
			t.Fatal("unsafe staging signature", bad)
		}
	}
}
