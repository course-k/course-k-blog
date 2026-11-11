package publication

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
)

func fakeCloudFront(t *testing.T, status int) (Distribution, *[]string) {
	t.Helper()
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.Path+"\n"+string(body))
		w.Header().Set("Content-Type", "text/xml")
		if status != 201 {
			w.WriteHeader(status)
			io.WriteString(w, `<ErrorResponse><Error><Type>Sender</Type><Code>AccessDenied</Code><Message>sensitive-upstream-detail</Message></Error></ErrorResponse>`)
			return
		}
		w.WriteHeader(201)
		io.WriteString(w, `<Invalidation><Id>I1</Id><Status>InProgress</Status><CreateTime>2026-11-11T00:00:00Z</CreateTime><InvalidationBatch><Paths><Quantity>1</Quantity><Items><Path>/*</Path></Items></Paths><CallerReference>x</CallerReference></InvalidationBatch></Invalidation>`)
	}))
	t.Cleanup(server.Close)
	client := cloudfront.New(cloudfront.Options{Region: "us-east-1", BaseEndpoint: aws.String(server.URL), Credentials: aws.AnonymousCredentials{}})
	return Distribution{Client: client, ID: "EDISTRIBUTION"}, &requests
}

func TestPurgeInvalidatesEveryPathOfTheDistribution(t *testing.T) {
	distribution, requests := fakeCloudFront(t, 201)
	if err := distribution.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := distribution.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 2 {
		t.Fatalf("each purge must send one request: %d", len(*requests))
	}
	first, second := (*requests)[0], (*requests)[1]
	if !strings.HasPrefix(first, "POST /2020-05-31/distribution/EDISTRIBUTION/invalidation\n") {
		t.Fatalf("wrong distribution or operation: %q", first)
	}
	// Pages, article images and reader files all depend on publication, and one
	// wildcard path counts as a single invalidation.
	if !strings.Contains(first, "<Path>/*</Path>") || !strings.Contains(first, "<Quantity>1</Quantity>") {
		t.Fatalf("the purge must cover every path with one entry: %q", first)
	}
	reference := func(body string) string {
		start := strings.Index(body, "<CallerReference>")
		return body[start:strings.Index(body, "</CallerReference>")]
	}
	if reference(first) == reference(second) {
		t.Fatal("a repeated purge needs a new reference, or CloudFront treats it as the same request")
	}
}

func TestRefusedPurgeIsAnErrorWithoutUpstreamDetails(t *testing.T) {
	distribution, _ := fakeCloudFront(t, 403)
	err := distribution.Purge(context.Background())
	if err == nil || strings.Contains(err.Error(), "sensitive-upstream-detail") {
		t.Fatalf("a refused purge must fail without leaking details: %v", err)
	}
}
