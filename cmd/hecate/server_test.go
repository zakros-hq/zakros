package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/brokerauth"
	"github.com/zakros-hq/zakros/pkg/jwt"
)

// fakeVault stands in for OpenBao so the server tests don't need a
// real backend. Routes only `GET /v1/<mount>/data/<ref>` and matches
// the response envelope Vault uses.
func fakeVault(t *testing.T, mount string, kv map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/v1/" + mount + "/data/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		ref := r.URL.Path[len(prefix):]
		val, ok := kv[ref]
		if !ok {
			http.NotFound(w, r)
			return
		}
		// Vault's response envelope.
		body, _ := json.Marshal(map[string]any{
			"data": map[string]any{
				"data":     map[string]string{"value": val},
				"metadata": map[string]any{},
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

func newRig(t *testing.T, kv map[string]string) (*httptest.Server, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	vault := fakeVault(t, "secret", kv)
	t.Cleanup(vault.Close)

	em := audit.NewWriterEmitter("hecate-test", io.Discard)
	srv := &server{
		audit: em,
		verifier: &brokerauth.Verifier{
			Broker:    "hecate",
			PublicKey: pub,
			Replay:    brokerauth.NopReplayStore{},
			Audit:     em,
		},
		vault: newVaultClient(vault.URL, "test-token", "secret"),
	}

	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts, priv
}

// mintJWT mints a token with the given scopes; helper for table-driven
// access-control tests.
func mintJWT(t *testing.T, priv ed25519.PrivateKey, audience []string, scopes map[string][]string) string {
	t.Helper()
	now := time.Now().UTC()
	tok, err := jwt.Sign(priv, jwt.Claims{
		Subject:   "pod:test:run-1",
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

func TestFetchHappyPath(t *testing.T) {
	ts, priv := newRig(t, map[string]string{
		"claude-code-token": "sk-ant-secret-value",
	})

	tok := mintJWT(t, priv, []string{"hecate"}, map[string][]string{
		"hecate": {"credentials.fetch:claude-code-token"},
	})

	req, _ := http.NewRequest("GET", ts.URL+"/credentials/fetch/claude-code-token", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var got map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got["value"] != "sk-ant-secret-value" {
		t.Errorf("value: %q", got["value"])
	}
}

func TestFetchScopeDeniedForOtherCredential(t *testing.T) {
	// Pod has scope for claude-code-token but tries to fetch
	// github-app-private-key — must 403.
	ts, priv := newRig(t, map[string]string{
		"claude-code-token":      "x",
		"github-app-private-key": "y",
	})
	tok := mintJWT(t, priv, []string{"hecate"}, map[string][]string{
		"hecate": {"credentials.fetch:claude-code-token"},
	})

	req, _ := http.NewRequest("GET", ts.URL+"/credentials/fetch/github-app-private-key", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestFetchAudienceMismatch(t *testing.T) {
	// Token addresses a different broker — must 403.
	ts, priv := newRig(t, map[string]string{"claude-code-token": "x"})
	tok := mintJWT(t, priv, []string{"github"}, map[string][]string{
		"github": {"clone"},
	})

	req, _ := http.NewRequest("GET", ts.URL+"/credentials/fetch/claude-code-token", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestFetchUnknownRefReturns404(t *testing.T) {
	ts, priv := newRig(t, map[string]string{}) // empty Vault
	tok := mintJWT(t, priv, []string{"hecate"}, map[string][]string{
		"hecate": {"credentials.fetch:does-not-exist"},
	})
	req, _ := http.NewRequest("GET", ts.URL+"/credentials/fetch/does-not-exist", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestFetchMissingBearer(t *testing.T) {
	ts, _ := newRig(t, map[string]string{"x": "y"})
	resp, err := http.Get(ts.URL + "/credentials/fetch/x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestFetchHealthz(t *testing.T) {
	ts, _ := newRig(t, nil)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
