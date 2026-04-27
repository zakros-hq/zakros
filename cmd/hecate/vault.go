package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// vaultClient is the minimal Vault HTTP client Hecate needs for KV-v2
// reads. Avoiding hashicorp/vault/api keeps the dependency surface
// flat and the implementation small enough to audit.
type vaultClient struct {
	addr    string
	token   string
	mount   string
	http    *http.Client
}

func newVaultClient(addr, token, mount string) *vaultClient {
	return &vaultClient{
		addr:  strings.TrimRight(addr, "/"),
		token: token,
		mount: mount,
		http:  &http.Client{Timeout: 10 * time.Second},
	}
}

// ErrVaultNotFound is returned when the requested KV path is missing
// (Vault returns 404). Callers map this to 404 on the broker API.
var ErrVaultNotFound = errors.New("vault: secret not found")

// kvReadResponse mirrors the relevant Vault KV-v2 response shape:
//
//	{
//	  "data": {
//	    "data": { "value": "<plaintext>" },
//	    "metadata": { ... }
//	  }
//	}
//
// Hecate's contract: every credential is stored as { "value": "..." }
// at `secret/data/<ref>`. The convention keeps the broker's contract
// tight — no per-credential schema variation.
type kvReadResponse struct {
	Data struct {
		Data map[string]string `json:"data"`
	} `json:"data"`
}

// readKV fetches the credential at <mount>/data/<ref>. Returns the
// raw value bytes (the operator-stored plaintext).
func (v *vaultClient) readKV(ctx context.Context, ref string) ([]byte, error) {
	if v.token == "" {
		return nil, errors.New("vault: token not configured")
	}
	// Defense in depth: ref must not contain `..` or path-traversal
	// characters; the JWT scope already binds it but a second check is
	// cheap.
	if strings.ContainsAny(ref, "/.") {
		return nil, fmt.Errorf("vault: ref must not contain '/' or '.': %q", ref)
	}
	u := fmt.Sprintf("%s/v1/%s/data/%s", v.addr, v.mount, url.PathEscape(ref))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", v.token)
	resp, err := v.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrVaultNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vault: status %d: %s", resp.StatusCode, string(body))
	}
	var out kvReadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("vault: decode: %w", err)
	}
	val, ok := out.Data.Data["value"]
	if !ok {
		return nil, fmt.Errorf("vault: secret/%s missing 'value' key", ref)
	}
	return []byte(val), nil
}
