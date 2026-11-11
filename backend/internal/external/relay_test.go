package external

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

// relayAnswer builds the relay function's reply for a fetched page.
func relayAnswer(status int, body string) []byte {
	out, _ := json.Marshal(map[string]any{"status": status, "body": base64.StdEncoding.EncodeToString([]byte(body))})
	return out
}

func TestRelayedHostsGoThroughTheRelayAndOthersDoNot(t *testing.T) {
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "direct") }))
	defer direct.Close()
	asked := []string{}
	transport := &RelayTransport{
		Hosts: map[string]bool{"note.com": true},
		Invoke: func(_ context.Context, payload []byte) ([]byte, error) {
			asked = append(asked, string(payload))
			return relayAnswer(200, "relayed"), nil
		},
		Direct: http.DefaultTransport,
	}
	client := &http.Client{Transport: transport}
	read := func(url string) (int, string) {
		t.Helper()
		response, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	if status, body := read("https://note.com/course__k/rss"); status != 200 || body != "relayed" {
		t.Fatalf("a relayed host must be answered by the relay: %d %q", status, body)
	}
	if len(asked) != 1 || asked[0] != `{"url":"https://note.com/course__k/rss"}` {
		t.Fatalf("the relay must receive exactly the requested URL: %v", asked)
	}
	if status, body := read(direct.URL); status != 200 || body != "direct" || len(asked) != 1 {
		t.Fatalf("other hosts must be fetched directly: %d %q, relay calls %d", status, body, len(asked))
	}
}

func TestRelayPassesUpstreamStatusesThrough(t *testing.T) {
	transport := &RelayTransport{Hosts: map[string]bool{"note.com": true}, Invoke: func(context.Context, []byte) ([]byte, error) {
		return relayAnswer(404, "gone"), nil
	}}
	response, err := (&http.Client{Transport: transport}).Get("https://note.com/course__k/rss")
	if err != nil || response.StatusCode != 404 {
		t.Fatalf("the caller must see the upstream status: %v %v", response, err)
	}
}

func TestRelayFailuresAreErrorsNotEmptyPages(t *testing.T) {
	cases := map[string]func(context.Context, []byte) ([]byte, error){
		"invocation failed":  func(context.Context, []byte) ([]byte, error) { return nil, errors.New("denied") },
		"relay refused":      func(context.Context, []byte) ([]byte, error) { return []byte(`{"error":"url_not_allowed"}`), nil },
		"malformed reply":    func(context.Context, []byte) ([]byte, error) { return []byte(`not json`), nil },
		"body is not base64": func(context.Context, []byte) ([]byte, error) { return []byte(`{"status":200,"body":"%%%"}`), nil },
		"status missing":     func(context.Context, []byte) ([]byte, error) { return []byte(`{"body":""}`), nil },
	}
	for name, invoke := range cases {
		transport := &RelayTransport{Hosts: map[string]bool{"note.com": true}, Invoke: invoke}
		if _, err := (&http.Client{Transport: transport}).Get("https://note.com/course__k/rss"); err == nil {
			t.Errorf("%s: must be an error", name)
		}
	}
}

func TestRelayOnlyCarriesPlainGetRequests(t *testing.T) {
	called := false
	transport := &RelayTransport{Hosts: map[string]bool{"note.com": true}, Invoke: func(context.Context, []byte) ([]byte, error) {
		called = true
		return relayAnswer(200, ""), nil
	}}
	if _, err := (&http.Client{Transport: transport}).Post("https://note.com/course__k/rss", "text/plain", strings.NewReader("x")); err == nil || called {
		t.Fatal("the relay fetches pages; it must refuse to send data")
	}
}

func TestClientFetchesNoteThroughTheRelay(t *testing.T) {
	feed := `<rss><channel><item><title>記事</title><link>https://note.com/course__k/n/n123</link><pubDate>Mon, 02 Nov 2026 10:00:00 +0900</pubDate></item></channel></rss>`
	client := NewRelayedClient(func(context.Context, []byte) ([]byte, error) { return relayAnswer(200, feed), nil })
	batch, err := client.Fetch(context.Background(), "note")
	if err != nil || len(batch.Posts) != 1 || batch.Posts[0].ID != "n123" {
		t.Fatalf("the feed fetched through the relay must be parsed as usual: %+v %v", batch, err)
	}
}

func TestLambdaRelayInvokesTheFunctionAndWaitsForItsAnswer(t *testing.T) {
	var kind, path, sent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		kind, path, sent = r.Header.Get("X-Amz-Invocation-Type"), r.URL.Path, string(body)
		w.Write(relayAnswer(200, "page"))
	}))
	defer server.Close()
	relay := LambdaRelay{Client: lambda.New(lambda.Options{Region: "us-west-2", BaseEndpoint: aws.String(server.URL), Credentials: aws.AnonymousCredentials{}}), Name: "blog-relay"}
	answer, err := relay.Invoke(context.Background(), []byte(`{"url":"https://note.com/course__k/rss"}`))
	if err != nil || string(answer) != string(relayAnswer(200, "page")) {
		t.Fatalf("the function's answer must be returned: %q %v", answer, err)
	}
	if kind != "RequestResponse" || path != "/2015-03-31/functions/blog-relay/invocations" || sent != `{"url":"https://note.com/course__k/rss"}` {
		t.Fatalf("invocation: %q %q %q", kind, path, sent)
	}
}

func TestLambdaRelayReportsAFunctionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Amz-Function-Error", "Unhandled")
		io.WriteString(w, `{"errorMessage":"sensitive-upstream-detail"}`)
	}))
	defer server.Close()
	relay := LambdaRelay{Client: lambda.New(lambda.Options{Region: "us-west-2", BaseEndpoint: aws.String(server.URL), Credentials: aws.AnonymousCredentials{}}), Name: "blog-relay"}
	if _, err := relay.Invoke(context.Background(), []byte(`{}`)); err == nil || strings.Contains(err.Error(), "sensitive-upstream-detail") {
		t.Fatalf("a crashed function must be an error without its details: %v", err)
	}
}

func TestRelayAllowsExactlyThePagesTheSyncRequests(t *testing.T) {
	source, err := os.ReadFile("../../../infra/relay/relay.py")
	if errors.Is(err, os.ErrNotExist) {
		// The image build copies only backend/. The repository checks run this
		// test from a full checkout, where the relay source is present.
		t.Skip("relay source is outside this build context")
	}
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, match := range regexp.MustCompile(`'(https://[^']+)'`).FindAllStringSubmatch(string(source), -1) {
		allowed[match[1]] = true
	}
	requested := map[string]bool{}
	client := NewRelayedClient(func(_ context.Context, payload []byte) ([]byte, error) {
		var asked struct{ URL string }
		if err := json.Unmarshal(payload, &asked); err != nil {
			t.Fatal(err)
		}
		requested[asked.URL] = true
		// Permissive robots rules let the Wantedly sync go on to its page.
		return relayAnswer(200, "User-agent: *\nAllow: /\n"), nil
	})
	for _, medium := range []string{"note", "wantedly"} {
		_, _ = client.Fetch(context.Background(), medium) // Parsing fails on this fixture; only the requests matter.
	}
	if len(requested) != 3 || len(allowed) != len(requested) {
		t.Fatalf("requested %v, relay allows %v", requested, allowed)
	}
	for url := range requested {
		if !allowed[url] {
			t.Fatalf("the sync requests %s, which the relay refuses", url)
		}
	}
}
