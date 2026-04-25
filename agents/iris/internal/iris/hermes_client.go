package iris

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// HermesClient is Iris's HTTP wrapper for Minos-hosted Hermes pull/post
// endpoints. Same Iris bearer as MinosClient (state API uses it too) —
// kept on its own type so callers don't conflate "talk to Hermes" with
// "talk to the task store."
type HermesClient struct {
	BaseURL    string
	IrisToken  string
	HTTPClient *http.Client
}

// NewHermesClient pairs with NewMinosClient — same baseURL, same token.
// Uses a longer HTTP timeout because EventsNext long-polls.
func NewHermesClient(baseURL, irisToken string) *HermesClient {
	return &HermesClient{
		BaseURL:    baseURL,
		IrisToken:  irisToken,
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
	}
}

// PullEvent mirrors hermescore.PullEvent — defined here to avoid the
// pod binary depending on the broker package directly.
type PullEvent struct {
	Seq     uint64 `json:"seq"`
	Message struct {
		Surface       string    `json:"Surface"`
		SurfaceUserID string    `json:"SurfaceUserID"`
		ThreadRef     string    `json:"ThreadRef"`
		Content       string    `json:"Content"`
		Timestamp     time.Time `json:"Timestamp"`
	} `json:"message"`
}

// EventsNext long-polls the Iris pull buffer. since is the highest Seq
// the caller has processed; max bounds the response; timeout (seconds)
// is the long-poll budget. Returns the events plus the broker's instance
// ID (from the X-Hermes-Instance header) so the caller can detect a
// broker restart and reset its cursor.
func (c *HermesClient) EventsNext(ctx context.Context, since uint64, maxEvents, timeoutSec int) ([]PullEvent, string, error) {
	q := url.Values{}
	if since > 0 {
		q.Set("since", fmt.Sprintf("%d", since))
	}
	if maxEvents > 0 {
		q.Set("max", fmt.Sprintf("%d", maxEvents))
	}
	if timeoutSec > 0 {
		q.Set("timeout", fmt.Sprintf("%d", timeoutSec))
	}
	req, err := http.NewRequestWithContext(ctx, "GET",
		c.BaseURL+"/hermes/events.next?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.IrisToken)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("events.next: %s: %s", resp.Status, readSnippet(resp.Body))
	}
	instance := resp.Header.Get("X-Hermes-Instance")
	var events []PullEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, instance, fmt.Errorf("events.next decode: %w", err)
	}
	return events, instance, nil
}

// PostAsIris posts a reply to a thread on Iris's behalf. The Phase 2
// Slice I per-message identity render lands at the Hermes-plugin layer;
// from the pod's perspective the call shape doesn't change.
func (c *HermesClient) PostAsIris(ctx context.Context, surface, threadRef, content string) error {
	body, _ := json.Marshal(map[string]string{
		"surface":    surface,
		"thread_ref": threadRef,
		"content":    content,
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.BaseURL+"/hermes/post_as_iris", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.IrisToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("post_as_iris: %s: %s", resp.Status, readSnippet(resp.Body))
	}
	return nil
}
