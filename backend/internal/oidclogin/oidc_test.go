package oidclogin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
)

func TestInvalidProviderConfigurationRejected(t *testing.T) {
	for _, cfg := range []Config{{}, {Issuer: "http://evil.invalid", ClientID: "client", AdminSubject: "owner", RedirectURL: "https://blog.example/api/auth/callback"}, {Issuer: "https://issuer.example", ClientID: "client", RedirectURL: "https://blog.example/api/auth/callback"}} {
		if _, err := New(context.Background(), cfg, nil); err == nil {
			t.Fatal("invalid identity configuration accepted")
		}
	}
}

type memoryFlows struct{ flows map[string]Flow }

func (m *memoryFlows) PutLoginFlow(_ context.Context, flow Flow) error {
	m.flows[flow.Hash] = flow
	return nil
}
func (m *memoryFlows) ConsumeLoginFlow(_ context.Context, hash string) (Flow, error) {
	flow, ok := m.flows[hash]
	delete(m.flows, hash)
	if !ok {
		return Flow{}, ErrIdentity
	}
	return flow, nil
}

func TestSignedIdentityClaimsAndOneTimeFlow(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: private}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	var issuer *httptest.Server
	var token string
	var challenge string
	issuer = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize", "token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &private.PublicKey, KeyID: "fixture", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			if r.ParseForm() != nil {
				http.Error(w, "bad form", 400)
				return
			}
			digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(digest[:]) != challenge || r.Form.Get("client_id") != "blog-client" {
				http.Error(w, "PKCE rejected", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "test-only", "token_type": "Bearer", "id_token": token, "expires_in": 60})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()
	repository := &memoryFlows{flows: map[string]Flow{}}
	ctx := oidc.ClientContext(context.Background(), issuer.Client())
	service, err := New(ctx, Config{Issuer: issuer.URL, ClientID: "blog-client", AdminSubject: "owner", RedirectURL: "https://blog.example/api/auth/callback"}, repository)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
		valid  bool
	}{
		{"owner", func(map[string]any) {}, true},
		{"other subject", func(c map[string]any) { c["sub"] = "someone-else" }, false},
		{"other audience", func(c map[string]any) { c["aud"] = "other-client" }, false},
		{"other issuer", func(c map[string]any) { c["iss"] = "https://other.example" }, false},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }, false},
		{"bad signature", func(c map[string]any) {}, false},
		{"expired flow", func(c map[string]any) {}, false},
		{"nonce mismatch", func(c map[string]any) { c["nonce"] = "wrong" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, location, err := service.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := url.Parse(location)
			if err != nil {
				t.Fatal(err)
			}
			challenge = endpoint.Query().Get("code_challenge")
			if challenge == "" || endpoint.Query().Get("code_challenge_method") != "S256" {
				t.Fatal("PKCE challenge missing")
			}
			claims := map[string]any{"iss": issuer.URL, "aud": "blog-client", "sub": "owner", "nonce": endpoint.Query().Get("nonce"), "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix()}
			tc.mutate(claims)
			payload, err := json.Marshal(claims)
			if err != nil {
				t.Fatal(err)
			}
			signed, err := signer.Sign(payload)
			if err != nil {
				t.Fatal(err)
			}
			token, err = signed.CompactSerialize()
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "bad signature" {
				parts := strings.Split(token, ".")
				parts[2] = base64.RawURLEncoding.EncodeToString(make([]byte, 256))
				token = strings.Join(parts, ".")
			}
			if tc.name == "expired flow" {
				for hash, flow := range repository.flows {
					flow.ExpiresAt = time.Now().Add(-time.Minute)
					repository.flows[hash] = flow
				}
			}
			err = service.Complete(ctx, state, state, "fixture-code")
			if (err == nil) != tc.valid {
				t.Fatalf("accepted=%v expected=%v", err == nil, tc.valid)
			}
			if err != nil && strings.Contains(err.Error(), token) {
				t.Fatal("token leaked in diagnostic")
			}
			if err = service.Complete(ctx, state, state, "fixture-code"); err == nil {
				t.Fatal("authorization flow replay accepted")
			}
		})
	}
	state, _, err := service.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Complete(ctx, state, strings.Repeat("x", 43), "fixture-code"); err == nil {
		t.Fatal("state cookie mismatch accepted")
	}
}
