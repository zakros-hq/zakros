package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// hecateClient is Apollo's minimal HTTP client for Hecate's
// /credentials/fetch endpoint. Same shape as the entrypoint.sh fetch
// loop in agents/claude-code, kept private to this package because
// the surface is one call.
type hecateClient struct {
	baseURL string
	bearer  string
	client  *http.Client
}

func newHecateClient(baseURL, bearer string) *hecateClient {
	return &hecateClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		bearer:  bearer,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// fetch reads a credential by ref. Returns the plaintext value or an
// error annotating the HTTP status when the call fails.
func (c *hecateClient) fetch(ctx context.Context, ref string) (string, error) {
	url := c.baseURL + "/credentials/fetch/" + ref
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("build hecate request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("hecate call: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read hecate body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("hecate returned %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parse hecate body: %w", err)
	}
	if parsed.Value == "" {
		return "", fmt.Errorf("hecate returned empty value for %q", ref)
	}
	return parsed.Value, nil
}
