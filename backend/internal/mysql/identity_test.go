package mysql

import (
	"context"
	"testing"
	"time"

	"coursek/blog/backend/internal/blog"
	"coursek/blog/backend/internal/oidclogin"
)

func TestLoginFlowIsConsumedOnceAndRevocable(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	expiry := time.Now().Add(time.Minute)
	flow := oidclogin.Flow{Hash: "fixture-state-hash", Nonce: "nonce", Verifier: "verifier", ExpiresAt: expiry}
	if err := store.PutLoginFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	if found, err := store.ConsumeLoginFlow(ctx, flow.Hash); err != nil || found.Verifier != flow.Verifier {
		t.Fatal("flow not persisted", err)
	}
	if _, err := store.ConsumeLoginFlow(ctx, flow.Hash); err == nil {
		t.Fatal("flow replay accepted")
	}
	if err := store.PutLoginFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSession(ctx, blog.Session{Hash: "fixture-session-hash", ExpiresAt: expiry}); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeLoginFlow(ctx, flow.Hash); err == nil {
		t.Fatal("pending flow survived revocation")
	}
	if valid, err := store.HasSession(ctx, "fixture-session-hash", time.Now()); err != nil || valid {
		t.Fatal("session survived revocation", err)
	}
}
