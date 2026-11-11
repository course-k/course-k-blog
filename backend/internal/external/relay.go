package external

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// Media that publish no IPv6 address cannot be reached from the API's network,
// whose only way out is IPv6.
var ipv4OnlyHosts = map[string]bool{"note.com": true, "www.wantedly.com": true}

// RelayTransport fetches pages of hosts that cannot be reached from where the
// API runs by asking a relay function outside that network. Every other host
// is fetched directly.
type RelayTransport struct {
	Hosts  map[string]bool
	Invoke func(ctx context.Context, payload []byte) ([]byte, error)
	Direct http.RoundTripper
}

func (t *RelayTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !t.Hosts[request.URL.Host] {
		return t.Direct.RoundTrip(request)
	}
	// The relay fetches public pages. It never carries a body or credentials.
	if request.Method != http.MethodGet || request.Body != nil && request.Body != http.NoBody {
		return nil, errors.New("relay only fetches pages")
	}
	payload, err := json.Marshal(map[string]string{"url": request.URL.String()})
	if err != nil {
		return nil, errors.New("relay request invalid")
	}
	answer, err := t.Invoke(request.Context(), payload)
	if err != nil {
		return nil, errors.New("relay unavailable")
	}
	var fetched struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(answer, &fetched) != nil || fetched.Error != "" || fetched.Status < 100 || fetched.Status > 599 {
		return nil, errors.New("relay could not fetch the page")
	}
	body, err := base64.StdEncoding.DecodeString(fetched.Body)
	if err != nil {
		return nil, errors.New("relay answer unreadable")
	}
	return &http.Response{
		StatusCode: fetched.Status, Status: http.StatusText(fetched.Status),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request,
	}, nil
}

// NewRelayedClient fetches the IPv4-only media through the relay and everything
// else directly.
func NewRelayedClient(invoke func(context.Context, []byte) ([]byte, error)) *Client {
	client := NewClient()
	client.HTTP.Transport = &RelayTransport{Hosts: ipv4OnlyHosts, Invoke: invoke, Direct: http.DefaultTransport}
	return client
}

// LambdaRelay invokes the relay function and waits for its answer.
type LambdaRelay struct {
	Client *lambda.Client
	Name   string
}

func NewLambdaRelay(ctx context.Context, name string) (LambdaRelay, error) {
	settings, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return LambdaRelay{}, errors.New("AWS function configuration unavailable")
	}
	return LambdaRelay{Client: lambda.NewFromConfig(settings), Name: name}, nil
}

func (r LambdaRelay) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
	result, err := r.Client.Invoke(ctx, &lambda.InvokeInput{FunctionName: aws.String(r.Name), InvocationType: types.InvocationTypeRequestResponse, Payload: payload})
	if err != nil {
		return nil, errors.New("relay invocation failed")
	}
	if result.FunctionError != nil {
		return nil, errors.New("relay function failed")
	}
	return result.Payload, nil
}
