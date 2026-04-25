package jwt_test

import (
	"errors"
	"testing"
	"time"

	"github.com/zakros-hq/zakros/pkg/jwt"
)

func sampleClaims() jwt.Claims {
	now := time.Now().UTC()
	return jwt.Claims{
		Subject:  "pod:task-123:run-1",
		Issuer:   "minos",
		Audience: []string{"github", "mnemosyne"},
		IssuedAt: now,
		Expires:  now.Add(2 * time.Hour),
		JTI:      "token-1",
		McpScopes: map[string][]string{
			"github":    {"pr.create", "pr.update"},
			"mnemosyne": {"memory.lookup"},
		},
	}
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	secret := []byte("secret-for-testing")
	c := sampleClaims()
	tok, err := jwt.SignBearer(secret, c)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	got, err := jwt.VerifyBearer(secret, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Subject != c.Subject || got.Issuer != c.Issuer {
		t.Errorf("claim mismatch: %+v vs %+v", got, c)
	}
	if !got.HasScope("github", "pr.create") {
		t.Errorf("expected github:pr.create scope to survive round trip")
	}
	if got.HasScope("github", "nonexistent") {
		t.Errorf("unexpected scope present")
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	c := sampleClaims()
	tok, err := jwt.SignBearer([]byte("right"), c)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := jwt.VerifyBearer([]byte("wrong"), tok); !errors.Is(err, jwt.ErrInvalidBearer) {
		t.Errorf("expected ErrInvalidBearer, got %v", err)
	}
}

func TestVerifyExpired(t *testing.T) {
	secret := []byte("secret")
	c := sampleClaims()
	c.IssuedAt = time.Now().Add(-3 * time.Hour)
	c.Expires = time.Now().Add(-1 * time.Hour)
	tok, err := jwt.SignBearer(secret, c)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := jwt.VerifyBearer(secret, tok); !errors.Is(err, jwt.ErrInvalidBearer) {
		t.Errorf("expected ErrInvalidBearer for expired token, got %v", err)
	}
}

func TestSignRequiresSecret(t *testing.T) {
	if _, err := jwt.SignBearer(nil, sampleClaims()); !errors.Is(err, jwt.ErrInvalidBearer) {
		t.Errorf("expected ErrInvalidBearer for empty secret, got %v", err)
	}
}
