package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// anthropicProviderConfig is the constructor input for newAnthropicProvider.
type anthropicProviderConfig struct {
	// Endpoint is the upstream base URL, e.g. https://api.anthropic.com.
	Endpoint string

	// APIKey is the Anthropic API key Apollo presents on the egress
	// side. Resolved from Hecate at startup (see main.go).
	APIKey string

	// AllowedModels gates which model identifiers Apollo will accept
	// for this provider. Apollo refuses unknown models at the
	// registry layer before the JWT-scope check runs, so callers see
	// "unknown model" rather than an opaque "scope denied".
	AllowedModels []string

	// HTTPClient is the egress client; tests stub it.
	HTTPClient *http.Client

	// AnthropicVer is the value Apollo sends in the `anthropic-version`
	// header. Defaults to 2023-06-01 (the bare-API GA value).
	AnthropicVer string
}

// anthropicProvider is the upstream-Anthropic Provider implementation.
type anthropicProvider struct {
	endpoint     string
	apiKey       string
	allowed      []string
	client       *http.Client
	anthropicVer string
}

func newAnthropicProvider(cfg anthropicProviderConfig) *anthropicProvider {
	if cfg.AnthropicVer == "" {
		cfg.AnthropicVer = "2023-06-01"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	return &anthropicProvider{
		endpoint:     strings.TrimRight(cfg.Endpoint, "/"),
		apiKey:       cfg.APIKey,
		allowed:      cfg.AllowedModels,
		client:       cfg.HTTPClient,
		anthropicVer: cfg.AnthropicVer,
	}
}

// Name implements Provider.
func (p *anthropicProvider) Name() string { return "anthropic" }

// Models implements Provider — returns the allowlist verbatim.
func (p *anthropicProvider) Models() []string { return p.allowed }

// Forward POSTs the inbound /v1/messages body to the upstream
// Anthropic API. The caller's bearer is *not* propagated; instead
// Apollo sets x-api-key from its own credential. This is the
// boundary that takes the credential off the pod's surface.
func (p *anthropicProvider) Forward(ctx context.Context, body []byte) (*ProviderResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", p.anthropicVer)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read upstream body: %w", err)
	}

	tokensIn, tokensOut := extractAnthropicUsage(respBody)
	return &ProviderResponse{
		Status:    resp.StatusCode,
		Headers:   resp.Header,
		Body:      respBody,
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
	}, nil
}

// extractAnthropicUsage parses the Anthropic Messages API response
// body for the `usage.input_tokens` / `usage.output_tokens` fields.
// Errors collapse to (0, 0) so non-2xx upstream responses (which may
// not carry the usage block) don't crash the audit. Streaming bodies
// also fall through here; H2 ships non-streaming so this is safe for
// now and grows a streaming aggregator when streaming lands.
func extractAnthropicUsage(body []byte) (int, int) {
	var parsed struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, 0
	}
	return parsed.Usage.InputTokens, parsed.Usage.OutputTokens
}
