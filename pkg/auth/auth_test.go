package auth

import (
	"context"
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	m := NewTokenManager("secret", time.Hour)
	token, exp, err := m.Issue("user-1", RoleCourier, "Иван")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if time.Until(exp) <= 0 {
		t.Fatalf("expiration must be in the future")
	}
	id, err := m.Parse(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if id.UserID != "user-1" || id.Role != RoleCourier || id.Name != "Иван" {
		t.Fatalf("unexpected identity: %+v", id)
	}
}

func TestTokenRejectsWrongSecretAndExpired(t *testing.T) {
	issuer := NewTokenManager("secret-a", time.Hour)
	token, _, _ := issuer.Issue("u", RoleCustomer, "")

	if _, err := NewTokenManager("secret-b", time.Hour).Parse(token); err == nil {
		t.Fatal("token signed with another secret must be rejected")
	}

	expired := NewTokenManager("secret-a", time.Minute)
	expired.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	old, _, _ := expired.Issue("u", RoleCustomer, "")
	if _, err := issuer.Parse(old); err == nil {
		t.Fatal("expired token must be rejected")
	}

	if _, err := issuer.Parse("not-a-jwt"); err == nil {
		t.Fatal("garbage must be rejected")
	}
}

func TestIdentityContext(t *testing.T) {
	ctx := context.Background()
	if _, ok := IdentityFrom(ctx); ok {
		t.Fatal("empty context must not contain identity")
	}
	ctx = WithIdentity(ctx, Identity{UserID: "1", Role: RoleAdmin})
	id, ok := IdentityFrom(ctx)
	if !ok || !id.Privileged() || !id.Is(RoleCustomer, RoleAdmin) {
		t.Fatalf("unexpected identity %+v", id)
	}
	if System.Role != RoleSystem || !System.Privileged() {
		t.Fatal("system identity must be privileged")
	}
}
