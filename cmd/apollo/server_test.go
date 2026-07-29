package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/brokerauth"
	"github.com/zakros-hq/zakros/pkg/jwt"
)

// stubProvider is a Provider used to verify Apollo's routing without
// hitting any upstream HTTP. It records the body it would forward and
// returns the configured ProviderResponse. Slice H2a's "synthetic
// second-provider plugin" acceptance bullet is exercised by
// TestSecondProviderRoutesByModel below.
type stubProvider struct {
	name      string
	models    []string
	resp      *ProviderResponse
	err       error
	called    bool
	bodySeen  []byte
}

func (s *stubProvider) Name() string     { return s.name }
func (s *stubProvider) Models() []string { return s.models }
func (s *stubProvider) Forward(_ context.Context, body []byte) (*ProviderResponse, error) {
	s.called = true
	s.bodySeen = append(s.bodySeen[:0], body...)
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func newApolloRig(t *testing.T, providers ...Provider) (*httptest.Server, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	em := audit.NewWriterEmitter("apollo-test", io.Discard)
	registry := newProviderRegistry()
	for _, p := range providers {
		registry.register(p)
	}
	srv := &server{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		audit:  em,
		verifier: &brokerauth.Verifier{
			Broker:    "apollo",
			PublicKey: pub,
			Replay:    brokerauth.NopReplayStore{},
			Audit:     em,
		},
		registry: registry,
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts, priv
}

func mintApolloJWT(t *testing.T, priv ed25519.PrivateKey, audience []string, scopes map[string][]string) string {
	t.Helper()
	now := time.Now().UTC()
	tok, err := jwt.Sign(priv, jwt.Claims{
		Subject:   "task:test-pod",
		Issuer:    "minos",
		Audience:  audience,
		IssuedAt:  now,
		Expires:   now.Add(time.Hour),
		JTI:       "test-jti",
		McpScopes: scopes,
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func anthropicHappyResp() *ProviderResponse {
	body, _ := json.Marshal(map[string]any{
		"id":      "msg_01",
		"type":    "message",
		"role":    "assistant",
		"content": []map[string]any{{"type": "text", "text": "hello"}},
		"usage":   map[string]int{"input_tokens": 12, "output_tokens": 5},
	})
	return &ProviderResponse{
		Status:    http.StatusOK,
		Headers:   http.Header{"Anthropic-Ratelimit-Tokens-Remaining": {"99999"}},
		Body:      body,
		TokensIn:  12,
		TokensOut: 5,
	}
}

func TestMessagesHappyPath(t *testing.T) {
	stub := &stubProvider{
		name:   "anthropic",
		models: []string{"claude-sonnet-4-5"},
		resp:   anthropicHappyResp(),
	}
	ts, priv := newApolloRig(t, stub)
	tok := mintApolloJWT(t, priv, []string{"apollo"}, map[string][]string{
		"apollo": {"apollo.anthropic.claude-sonnet-4-5"},
	})

	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, out)
	}
	if !stub.called {
		t.Fatal("provider Forward not called")
	}
	if got := resp.Header.Get("Anthropic-Ratelimit-Tokens-Remaining"); got != "99999" {
		t.Errorf("upstream header not relayed: %q", got)
	}
}

func TestMessagesScopeDeniedForOtherModel(t *testing.T) {
	stub := &stubProvider{
		name:   "anthropic",
		models: []string{"claude-sonnet-4-5", "claude-opus-4-5"},
		resp:   anthropicHappyResp(),
	}
	ts, priv := newApolloRig(t, stub)
	// JWT has scope for sonnet only; request opus.
	tok := mintApolloJWT(t, priv, []string{"apollo"}, map[string][]string{
		"apollo": {"apollo.anthropic.claude-sonnet-4-5"},
	})

	body := []byte(`{"model":"claude-opus-4-5","messages":[]}`)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
	if stub.called {
		t.Error("provider Forward should NOT have been called on scope denial")
	}
}

func TestMessagesAudienceMismatch(t *testing.T) {
	stub := &stubProvider{
		name:   "anthropic",
		models: []string{"claude-sonnet-4-5"},
		resp:   anthropicHappyResp(),
	}
	ts, priv := newApolloRig(t, stub)
	// JWT addresses "github" (audience), not apollo.
	tok := mintApolloJWT(t, priv, []string{"github"}, map[string][]string{
		"github": {"clone"},
	})

	body := []byte(`{"model":"claude-sonnet-4-5"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestMessagesMissingBearer(t *testing.T) {
	ts, _ := newApolloRig(t, &stubProvider{
		name:   "anthropic",
		models: []string{"claude-sonnet-4-5"},
		resp:   anthropicHappyResp(),
	})

	body := []byte(`{"model":"claude-sonnet-4-5"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(string(body)))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestMessagesUnknownModel(t *testing.T) {
	stub := &stubProvider{
		name:   "anthropic",
		models: []string{"claude-sonnet-4-5"},
		resp:   anthropicHappyResp(),
	}
	ts, priv := newApolloRig(t, stub)
	tok := mintApolloJWT(t, priv, []string{"apollo"}, map[string][]string{
		"apollo": {"apollo.anthropic.claude-sonnet-4-5"},
	})

	body := []byte(`{"model":"some-unknown-model"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// TestSecondProviderRoutesByModel exercises §9 acceptance bullet 3:
// "A synthetic second-provider plugin (OpenAI stub) loads into Apollo,
// accepts a JWT with apollo.openai.gpt-* scope, and the test pod
// commissions through it successfully." The two-provider registry
// fan-out is the load-bearing property; subprocess isolation is
// deferred per the §2 D4 H2a deviation.
func TestSecondProviderRoutesByModel(t *testing.T) {
	anthropic := &stubProvider{
		name:   "anthropic",
		models: []string{"claude-sonnet-4-5"},
		resp:   anthropicHappyResp(),
	}
	openai := &stubProvider{
		name:   "openai",
		models: []string{"gpt-4o-mini"},
		resp: &ProviderResponse{
			Status:    http.StatusOK,
			Headers:   http.Header{},
			Body:      []byte(`{"id":"chatcmpl-1"}`),
			TokensIn:  3,
			TokensOut: 1,
		},
	}
	ts, priv := newApolloRig(t, anthropic, openai)
	tok := mintApolloJWT(t, priv, []string{"apollo"}, map[string][]string{
		"apollo": {"apollo.openai.gpt-4o-mini"},
	})

	body := []byte(`{"model":"gpt-4o-mini","messages":[]}`)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 from openai stub, got %d: %s", resp.StatusCode, out)
	}
	if anthropic.called {
		t.Error("anthropic provider should NOT have been called")
	}
	if !openai.called {
		t.Error("openai provider was never called despite matching model")
	}
}

func TestUpstreamErrorYields502(t *testing.T) {
	stub := &stubProvider{
		name:   "anthropic",
		models: []string{"claude-sonnet-4-5"},
		err:    errors.New("upstream unreachable"),
	}
	ts, priv := newApolloRig(t, stub)
	tok := mintApolloJWT(t, priv, []string{"apollo"}, map[string][]string{
		"apollo": {"apollo.anthropic.claude-sonnet-4-5"},
	})

	body := []byte(`{"model":"claude-sonnet-4-5"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	ts, _ := newApolloRig(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
