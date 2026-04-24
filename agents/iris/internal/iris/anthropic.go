package iris

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// AnthropicClient is a minimal client for Anthropic's Messages API, just
// enough for Iris's tool-use loop. Phase 2 Slice H2 routes this traffic
// through Apollo (same Messages API shape on Apollo's frontend), at
// which point the Endpoint changes and the auth header carries a
// Minos-minted JWT instead of a bearer credential.
type AnthropicClient struct {
	Endpoint string // default "https://api.anthropic.com/v1/messages"
	APIKey   string // OAuth token or API key — sent as Authorization: Bearer
	Model    string // e.g. "claude-sonnet-4-5"
	Version  string // anthropic-version header, e.g. "2023-06-01"

	HTTPClient *http.Client
}

// NewAnthropicClient defaults Endpoint and Version. Caller supplies the
// API key (CLAUDE_CODE_OAUTH_TOKEN works for the Messages API) and the
// model name.
func NewAnthropicClient(apiKey, model string) *AnthropicClient {
	return &AnthropicClient{
		Endpoint:   "https://api.anthropic.com/v1/messages",
		APIKey:     apiKey,
		Model:      model,
		Version:    "2023-06-01",
		HTTPClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// Tool is the tool definition shape Anthropic accepts.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Message is one element of the conversation passed to /v1/messages.
// Content is either a plain string or a slice of content blocks.
type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// ContentBlock represents one block in a structured content array.
// Type is one of "text", "tool_use", "tool_result".
type ContentBlock struct {
	Type string `json:"type"`

	// type=text
	Text string `json:"text,omitempty"`

	// type=tool_use
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// type=tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// CreateRequest is the body shape /v1/messages accepts.
type CreateRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
	Tools     []Tool    `json:"tools,omitempty"`
}

// CreateResponse is the subset Iris consumes.
type CreateResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Model      string         `json:"model"`
}

// Create posts one Messages request and returns the parsed response.
func (a *AnthropicClient) Create(ctx context.Context, req CreateRequest) (*CreateResponse, error) {
	if req.Model == "" {
		req.Model = a.Model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = 1024
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic encode: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", a.Version)
	resp, err := a.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic %s: %s", resp.Status, readSnippet(resp.Body))
	}
	var out CreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("anthropic decode: %w", err)
	}
	return &out, nil
}

// ErrToolError is returned by ToolDispatch.Run for tool-side failures
// the loop should report back to the model rather than terminating.
var ErrToolError = errors.New("tool error")
