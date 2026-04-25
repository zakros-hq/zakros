package github_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zakros-hq/zakros/cerberus/verification/github"
)

type memStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (s *memStore) Seen(_ context.Context, delivery string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[delivery]; ok {
		return true, nil
	}
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	s.seen[delivery] = at
	return false, nil
}

func sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func makeRequest(t *testing.T, body []byte, secret []byte, delivery, event string, signature string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	if signature == "" {
		signature = sign(secret, body)
	}
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestVerifySuccess(t *testing.T) {
	secret := []byte("dev-webhook-secret")
	body := []byte(`{"action":"opened"}`)
	v := github.NewVerifier(secret, &memStore{})

	ev, err := v.Verify(context.Background(), makeRequest(t, body, secret, "d-1", "pull_request", ""))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ev.Type != "pull_request" || ev.DeliveryID != "d-1" {
		t.Errorf("unexpected event: %+v", ev)
	}
	if string(ev.Body) != string(body) {
		t.Errorf("body not preserved")
	}
}

func TestVerifyBadSignature(t *testing.T) {
	secret := []byte("right-secret")
	body := []byte(`{"x":1}`)
	v := github.NewVerifier(secret, &memStore{})

	// Sign with the wrong secret.
	bad := sign([]byte("wrong-secret"), body)
	_, err := v.Verify(context.Background(), makeRequest(t, body, secret, "d-1", "push", bad))
	if !errors.Is(err, github.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestVerifyMalformedSignature(t *testing.T) {
	secret := []byte("s")
	body := []byte(`{}`)
	v := github.NewVerifier(secret, &memStore{})

	cases := []string{"notprefixed", "sha256=nothex", "sha256="}
	for _, sig := range cases {
		_, err := v.Verify(context.Background(), makeRequest(t, body, secret, "d-1", "push", sig))
		if err == nil {
			t.Errorf("expected error for signature %q", sig)
		}
	}
}

func TestVerifyMissingHeaders(t *testing.T) {
	secret := []byte("s")
	body := []byte(`{}`)
	v := github.NewVerifier(secret, &memStore{})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	_, err := v.Verify(context.Background(), req)
	if !errors.Is(err, github.ErrMissingHeader) {
		t.Errorf("expected ErrMissingHeader, got %v", err)
	}
}

func TestVerifyReplay(t *testing.T) {
	secret := []byte("s")
	body := []byte(`{"x":1}`)
	store := &memStore{}
	v := github.NewVerifier(secret, store)

	// First delivery succeeds.
	if _, err := v.Verify(context.Background(), makeRequest(t, body, secret, "dup-1", "push", "")); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Second delivery with the same ID fails.
	_, err := v.Verify(context.Background(), makeRequest(t, body, secret, "dup-1", "push", ""))
	if !errors.Is(err, github.ErrReplay) {
		t.Errorf("expected ErrReplay, got %v", err)
	}
}

func TestVerifyReplayStoreNil(t *testing.T) {
	secret := []byte("s")
	body := []byte(`{}`)
	v := github.NewVerifier(secret, nil)
	// Nil store ⇒ replay protection disabled; both calls succeed.
	if _, err := v.Verify(context.Background(), makeRequest(t, body, secret, "d-1", "push", "")); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := v.Verify(context.Background(), makeRequest(t, body, secret, "d-1", "push", "")); err != nil {
		t.Fatalf("second: %v", err)
	}
}
