package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

func TestDatabaseURLComesFromTheEnvironmentOrTheNamedParameter(t *testing.T) {
	fetched := []string{}
	fetch := func(_ context.Context, name string) (string, error) {
		fetched = append(fetched, name)
		return "mysql://app@db/blog?tls=verify-full\n", nil
	}
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	direct, err := databaseURL(context.Background(), env(map[string]string{"DATABASE_URL": "mysql://direct", "DATABASE_URL_PARAMETER": "/blog/url"}), fetch)
	if err != nil || direct != "mysql://direct" || len(fetched) != 0 {
		t.Fatalf("a URL given directly must be used without a lookup: %q %v %v", direct, err, fetched)
	}
	looked, err := databaseURL(context.Background(), env(map[string]string{"DATABASE_URL_PARAMETER": "/blog/url"}), fetch)
	if err != nil || looked != "mysql://app@db/blog?tls=verify-full" || strings.Join(fetched, ",") != "/blog/url" {
		t.Fatalf("the named parameter must be read and trimmed: %q %v %v", looked, err, fetched)
	}
	if none, err := databaseURL(context.Background(), env(nil), fetch); err != nil || none != "" {
		t.Fatalf("without either setting the caller falls back to its other sources: %q %v", none, err)
	}
}

func TestUnreadableDatabaseParameterStopsTheStart(t *testing.T) {
	env := func(string) string { return "/blog/url" }
	_, err := databaseURL(context.Background(), func(key string) string {
		if key == "DATABASE_URL_PARAMETER" {
			return env(key)
		}
		return ""
	}, func(context.Context, string) (string, error) { return "", errors.New("sensitive-upstream-detail") })
	if err == nil || strings.Contains(err.Error(), "sensitive-upstream-detail") {
		t.Fatalf("a failed lookup must stop the start without leaking details: %v", err)
	}
	_, err = databaseURL(context.Background(), func(key string) string {
		if key == "DATABASE_URL_PARAMETER" {
			return "/blog/url"
		}
		return ""
	}, func(context.Context, string) (string, error) { return "  \n", nil })
	if err == nil {
		t.Fatal("an empty parameter must not be accepted as a database URL")
	}
}

func TestParameterStoreIsAskedToDecryptTheNamedParameter(t *testing.T) {
	var target, body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		target, body = r.Header.Get("X-Amz-Target"), string(raw)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		io.WriteString(w, `{"Parameter":{"Name":"/blog/url","Type":"SecureString","Value":"mysql://app@db/blog"}}`)
	}))
	defer server.Close()
	store := parameterStore{Client: ssm.New(ssm.Options{Region: "us-west-2", BaseEndpoint: aws.String(server.URL), Credentials: aws.AnonymousCredentials{}})}
	value, err := store.fetch(context.Background(), "/blog/url")
	if err != nil || value != "mysql://app@db/blog" {
		t.Fatalf("the parameter value must be returned: %q %v", value, err)
	}
	if target != "AmazonSSM.GetParameter" || !strings.Contains(body, `"Name":"/blog/url"`) || !strings.Contains(body, `"WithDecryption":true`) {
		t.Fatalf("request: %q %q", target, body)
	}
}
