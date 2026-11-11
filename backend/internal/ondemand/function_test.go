package ondemand

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

type invocation struct{ path, kind, body string }

func fakeLambda(t *testing.T, status int) (Function, *[]invocation) {
	t.Helper()
	seen := []invocation{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, invocation{r.URL.Path, r.Header.Get("X-Amz-Invocation-Type"), string(body)})
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	client := lambda.New(lambda.Options{Region: "us-west-2", BaseEndpoint: aws.String(server.URL), Credentials: aws.AnonymousCredentials{}, RetryMaxAttempts: 1})
	return Function{Client: client, Name: "blog-api"}, &seen
}

func TestRequestStartsWorkWithoutWaitingForIt(t *testing.T) {
	function, seen := fakeLambda(t, 202)
	function.Request(context.Background())
	if len(*seen) != 1 {
		t.Fatalf("one request must start one invocation: %d", len(*seen))
	}
	got := (*seen)[0]
	if got.path != "/2015-03-31/functions/blog-api/invocations" {
		t.Fatalf("the function must invoke itself by name: %q", got.path)
	}
	// "Event" queues the invocation; the caller's response is not held up by the work.
	if got.kind != "Event" {
		t.Fatalf("work must be queued, not awaited: %q", got.kind)
	}
	if got.body != `{"work":"pending"}` {
		t.Fatalf("unexpected payload: %q", got.body)
	}
}

func TestRefusedRequestDoesNotFailTheOwnerChange(t *testing.T) {
	// The change is already saved. Pending work stays recorded in the database
	// and is picked up by the next request or the daily schedule.
	function, seen := fakeLambda(t, 403)
	done := make(chan struct{})
	go func() { function.Request(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a refused request must return promptly")
	}
	if len(*seen) == 0 {
		t.Fatal("the invocation was never attempted")
	}
}
