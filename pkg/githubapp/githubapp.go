// Package githubapp mints GitHub App installation access tokens per
// architecture.md §6 Credential Handling. Minos calls this at pod spawn:
// sign an RS256 JWT with the App private key (resolved via the secret
// provider), exchange it for a 1-hour installation token scoped to the
// single task-target repo, inject the token into the pod as GITHUB_TOKEN.
package githubapp

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

// DefaultBaseURL is the public GitHub API. Override via Client.BaseURL for
// GitHub Enterprise Server.
const DefaultBaseURL = "https://api.github.com"

// jwtTTL is GitHub's maximum for App JWTs (10 minutes). We use 9 to leave
// slack for clock skew.
const jwtTTL = 9 * time.Minute

// Client mints installation access tokens for a single GitHub App.
type Client struct {
	AppID      int64
	PrivateKey *rsa.PrivateKey
	HTTPClient *http.Client
	BaseURL    string
	Now        func() time.Time
}

// NewClient parses a PEM-encoded private key and returns a ready Client.
// Callers resolve the key PEM via the secret provider before calling here.
func NewClient(appID int64, privateKeyPEM []byte) (*Client, error) {
	key, err := gojwt.ParseRSAPrivateKeyFromPEM(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("githubapp: parse private key: %w", err)
	}
	return &Client{
		AppID:      appID,
		PrivateKey: key,
		HTTPClient: http.DefaultClient,
		BaseURL:    DefaultBaseURL,
		Now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

// InstallationToken is the short-lived token GitHub issues for an
// installation; callers inject Token into the pod as GITHUB_TOKEN.
type InstallationToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// MintInstallationToken exchanges an App JWT for an installation token.
// When repos is non-empty, the token is scoped to those repositories by
// name (e.g., ["example/widget"]) — Phase 1 always passes a single repo
// per the task envelope's execution.repo_url.
func (c *Client) MintInstallationToken(ctx context.Context, installationID int64, repos []string) (*InstallationToken, error) {
	if c == nil || c.PrivateKey == nil {
		return nil, errors.New("githubapp: client not initialized")
	}
	appJWT, err := c.signAppJWT()
	if err != nil {
		return nil, err
	}

	var body io.Reader
	if len(repos) > 0 {
		payload := map[string]any{"repositories": repos}
		buf, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("githubapp: marshal body: %w", err)
		}
		body = bytes.NewReader(buf)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.baseURL(), installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("githubapp: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: http: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("githubapp: read: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("githubapp: status %d: %s", resp.StatusCode, string(data))
	}
	var out InstallationToken
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("githubapp: decode response: %w", err)
	}
	return &out, nil
}

// signAppJWT produces the JWT the App presents to GitHub to authenticate
// as the App (pre-installation-token step).
func (c *Client) signAppJWT() (string, error) {
	now := c.now()
	claims := gojwt.MapClaims{
		"iat": now.Add(-30 * time.Second).Unix(), // small backdate for clock skew
		"exp": now.Add(jwtTTL).Unix(),
		"iss": c.AppID,
	}
	tok := gojwt.NewWithClaims(gojwt.SigningMethodRS256, claims)
	s, err := tok.SignedString(c.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("githubapp: sign app jwt: %w", err)
	}
	return s, nil
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return c.BaseURL
}

func (c *Client) client() *http.Client {
	if c.HTTPClient == nil {
		return http.DefaultClient
	}
	return c.HTTPClient
}

func (c *Client) now() time.Time {
	if c.Now == nil {
		return time.Now().UTC()
	}
	return c.Now()
}
