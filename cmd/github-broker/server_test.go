package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/brokerauth"
	"github.com/zakros-hq/zakros/pkg/githubapp"
	"github.com/zakros-hq/zakros/pkg/jwt"
)

const testInstallationID int64 = 777

// fakeGitHub stands in for api.github.com: answers the installation
// access-token mint for testInstallationID and 404s everything else
// (which the broker must surface as 502).
func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/app/installations/777/access_tokens") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		repos, _ := body["repositories"].([]any)
		if len(repos) != 1 {
			http.Error(w, "expected exactly one repository", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_faketoken",
			"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		})
	}))
}

func genRSAKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func newRig(t *testing.T, installationID int64) (*httptest.Server, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	gh := fakeGitHub(t)
	t.Cleanup(gh.Close)
	client, err := githubapp.NewClient(42, genRSAKeyPEM(t))
	if err != nil {
		t.Fatalf("github client: %v", err)
	}
	client.BaseURL = gh.URL

	em := audit.NewWriterEmitter("github-broker-test", io.Discard)
	srv := &server{
		audit: em,
		verifier: &brokerauth.Verifier{
			Broker:    "github",
			PublicKey: pub,
			Replay:    brokerauth.NopReplayStore{},
			Audit:     em,
		},
		github:         client,
		installationID: installationID,
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts, priv
}

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

func postToken(t *testing.T, url, bearer, repo string) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"repo": repo})
	req, _ := http.NewRequest("POST", url+"/github/installation-token", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestInstallationTokenHappyPath(t *testing.T) {
	ts, priv := newRig(t, testInstallationID)
	tok := mintJWT(t, priv, []string{"github"}, map[string][]string{"github": {"clone"}})
	resp := postToken(t, ts.URL, tok, "zakros-hq/zakros")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}
	var got installationTokenResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Token != "ghs_faketoken" {
		t.Errorf("token: %q", got.Token)
	}
	if got.ExpiresAt == "" {
		t.Error("expires_at empty")
	}
}

func TestInstallationTokenScopeDenied(t *testing.T) {
	ts, priv := newRig(t, testInstallationID)
	tok := mintJWT(t, priv, []string{"github"}, map[string][]string{"github": {"pr.comment"}})
	resp := postToken(t, ts.URL, tok, "zakros-hq/zakros")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestInstallationTokenAudienceMismatch(t *testing.T) {
	ts, priv := newRig(t, testInstallationID)
	tok := mintJWT(t, priv, []string{"hecate"}, map[string][]string{"hecate": {"clone"}})
	resp := postToken(t, ts.URL, tok, "zakros-hq/zakros")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestInstallationTokenMissingBearer(t *testing.T) {
	ts, _ := newRig(t, testInstallationID)
	resp := postToken(t, ts.URL, "", "zakros-hq/zakros")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestInstallationTokenBadRepoShape(t *testing.T) {
	ts, priv := newRig(t, testInstallationID)
	tok := mintJWT(t, priv, []string{"github"}, map[string][]string{"github": {"clone"}})
	resp := postToken(t, ts.URL, tok, "no-owner-separator")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestInstallationTokenUpstreamFailureIs502(t *testing.T) {
	// Broker configured with an installation id the fake GitHub does
	// not know — the mint fails and must surface as 502, not 500/200.
	ts, priv := newRig(t, 12345)
	tok := mintJWT(t, priv, []string{"github"}, map[string][]string{"github": {"clone"}})
	resp := postToken(t, ts.URL, tok, "zakros-hq/zakros")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	ts, _ := newRig(t, testInstallationID)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
