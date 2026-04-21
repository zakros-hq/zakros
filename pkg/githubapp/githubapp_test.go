package githubapp_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/GoodOlClint/daedalus/pkg/githubapp"
)

// genRSAKeyPEM returns a fresh RSA key encoded as PKCS#1 PEM — matches the
// format GitHub issues for App private keys.
func genRSAKeyPEM(t *testing.T) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	return pemBytes, key
}

func TestNewClientRejectsBadKey(t *testing.T) {
	if _, err := githubapp.NewClient(123, []byte("not-a-key")); err == nil {
		t.Fatal("expected error for non-PEM input")
	}
}

func TestMintInstallationTokenRoundTrip(t *testing.T) {
	pemBytes, privateKey := genRSAKeyPEM(t)
	const appID int64 = 42
	const installationID int64 = 777
	wantRepos := []string{"example/widget"}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != fmt.Sprintf("/app/installations/%d/access_tokens", installationID) {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "no bearer", http.StatusUnauthorized)
			return
		}
		// Verify the JWT was signed with the matching key.
		parsed, err := gojwt.Parse(strings.TrimPrefix(auth, "Bearer "), func(t *gojwt.Token) (any, error) {
			if _, ok := t.Method.(*gojwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("bad alg: %v", t.Header["alg"])
			}
			return &privateKey.PublicKey, nil
		}, gojwt.WithValidMethods([]string{"RS256"}))
		if err != nil || !parsed.Valid {
			http.Error(w, "bad jwt: "+fmt.Sprint(err), http.StatusUnauthorized)
			return
		}
		claims, _ := parsed.Claims.(gojwt.MapClaims)
		if int64(claims["iss"].(float64)) != appID {
			http.Error(w, "wrong iss", http.StatusBadRequest)
			return
		}
		// Verify body carries the requested repositories.
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		repos, _ := body["repositories"].([]any)
		if len(repos) != 1 || repos[0] != wantRepos[0] {
			http.Error(w, fmt.Sprintf("wrong repos: %v", repos), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_faketoken",
			"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		})
	}))
	defer ts.Close()

	client, err := githubapp.NewClient(appID, pemBytes)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.BaseURL = ts.URL

	tok, err := client.MintInstallationToken(context.Background(), installationID, wantRepos)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if tok.Token != "ghs_faketoken" {
		t.Errorf("unexpected token: %s", tok.Token)
	}
	if time.Until(tok.ExpiresAt) < 30*time.Minute {
		t.Errorf("expiry too soon: %v", tok.ExpiresAt)
	}
}

func TestMintInstallationTokenHandlesErrorResponse(t *testing.T) {
	pemBytes, _ := genRSAKeyPEM(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer ts.Close()

	client, err := githubapp.NewClient(42, pemBytes)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	client.BaseURL = ts.URL

	_, err = client.MintInstallationToken(context.Background(), 1, []string{"x/y"})
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error lacks status: %v", err)
	}
}
