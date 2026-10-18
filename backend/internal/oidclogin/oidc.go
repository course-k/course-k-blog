// Package oidclogin verifies an external identity before issuing the local session.
package oidclogin

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"coursek/blog/backend/internal/auth"
)

var ErrIdentity = errors.New("identity could not be verified")

type Config struct{ Issuer, ClientID, AdminSubject, RedirectURL string }
type Flow struct {
	Hash, Nonce, Verifier string
	ExpiresAt             time.Time
}
type Repository interface {
	PutLoginFlow(context.Context, Flow) error
	ConsumeLoginFlow(context.Context, string) (Flow, error)
}
type Service struct {
	repository Repository
	oauth      oauth2.Config
	verifier   *oidc.IDTokenVerifier
	subject    string
	client     *http.Client
}

func New(ctx context.Context, settings Config, repository Repository) (*Service, error) {
	issuer, err := url.Parse(settings.Issuer)
	redirect, redirectError := url.Parse(settings.RedirectURL)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || redirectError != nil || redirect.Host == "" || redirect.User != nil || redirect.RawQuery != "" || redirect.Fragment != "" || settings.ClientID == "" || settings.AdminSubject == "" || repository == nil {
		return nil, errors.New("complete HTTPS identity provider configuration required")
	}
	local := redirect.Hostname() == "localhost" || redirect.Hostname() == "127.0.0.1"
	if redirect.Scheme != "https" && !(redirect.Scheme == "http" && local) {
		return nil, errors.New("secure authentication callback required")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if custom, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok {
		client = custom
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), settings.Issuer)
	if err != nil {
		return nil, errors.New("identity provider discovery unavailable")
	}
	endpoint := provider.Endpoint()
	for _, address := range []string{endpoint.AuthURL, endpoint.TokenURL} {
		parsed, err := url.Parse(address)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return nil, errors.New("secure identity endpoints required")
		}
	}
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	return &Service{repository: repository, oauth: oauth2.Config{ClientID: settings.ClientID, Endpoint: endpoint, RedirectURL: settings.RedirectURL, Scopes: []string{oidc.ScopeOpenID}}, verifier: provider.Verifier(&oidc.Config{ClientID: settings.ClientID, SupportedSigningAlgs: []string{oidc.RS256}}), subject: settings.AdminSubject, client: client}, nil
}
func (s *Service) Begin(ctx context.Context) (string, string, error) {
	state, err := auth.Token()
	if err != nil {
		return "", "", err
	}
	nonce, err := auth.Token()
	if err != nil {
		return "", "", err
	}
	verifier, err := auth.Token()
	if err != nil {
		return "", "", err
	}
	if err = s.repository.PutLoginFlow(ctx, Flow{Hash: auth.Digest(state), Nonce: nonce, Verifier: verifier, ExpiresAt: time.Now().Add(10 * time.Minute)}); err != nil {
		return "", "", ErrIdentity
	}
	endpoint := s.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	return state, endpoint, nil
}
func (s *Service) Complete(ctx context.Context, state, cookie, code string) error {
	if len(state) != 43 || len(cookie) != 43 || len(code) == 0 || len(code) > 4096 || subtle.ConstantTimeCompare([]byte(state), []byte(cookie)) != 1 {
		return ErrIdentity
	}
	flow, err := s.repository.ConsumeLoginFlow(ctx, auth.Digest(state))
	if err != nil || !flow.ExpiresAt.After(time.Now()) {
		return ErrIdentity
	}
	ctx = oidc.ClientContext(ctx, s.client)
	token, err := s.oauth.Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		return ErrIdentity
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return ErrIdentity
	}
	identity, err := s.verifier.Verify(ctx, raw)
	if err != nil || identity.Subject != s.subject || subtle.ConstantTimeCompare([]byte(identity.Nonce), []byte(flow.Nonce)) != 1 {
		return ErrIdentity
	}
	return nil
}

// Cognito's managed logout endpoint is separate from OIDC token validation.
func (s *Service) CognitoLogoutURL() string {
	endpoint, _ := url.Parse(s.oauth.Endpoint.AuthURL)
	endpoint.Path = "/logout"
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	callback, _ := url.Parse(s.oauth.RedirectURL)
	callback.Path = "/api/auth/signed-out"
	callback.RawQuery = ""
	query := endpoint.Query()
	query.Set("client_id", s.oauth.ClientID)
	query.Set("logout_uri", callback.String())
	endpoint.RawQuery = query.Encode()
	return endpoint.String()
}
