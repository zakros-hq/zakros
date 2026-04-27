// Package slack verifies Slack webhook (Events API + interactivity)
// requests per the Slack signing-secret spec.
//
// Slack's signing scheme:
//   - sender computes HMAC-SHA256(signing_secret, "v0:<ts>:<body>")
//   - sends X-Slack-Signature: v0=<hex>
//   - sends X-Slack-Request-Timestamp: <unix epoch seconds>
//
// The verifier rejects requests whose timestamp is more than ReplayWindow
// in the past or future to bound replay attacks. Slack's own client
// recommends a 5-minute window — we default to that.
//
// Slack's request-id-equivalent is X-Slack-Request-Timestamp combined
// with the body hash; we synthesize a DeliveryID by hashing both so
// the shared replay store can dedupe. Slack does not retransmit on
// 2xx, so duplicates are rare in practice; the tracking is defense
// in depth.
package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/zakros-hq/zakros/cerberus/verification"
)

// Aliased sentinels matching the verification package — call sites
// can errors.Is against either name.
var (
	ErrInvalidSignature = verification.ErrInvalidSignature
	ErrMissingHeader    = verification.ErrMissingHeader
	ErrTimestampSkew    = verification.ErrTimestampSkew
	ErrReplay           = verification.ErrReplay
)

// DefaultReplayWindow matches Slack's recommendation. Operators can
// override at construction.
const DefaultReplayWindow = 5 * time.Minute

// ReplayStore tracks delivery ids — same shape as the github
// package's, kept package-local so this verifier doesn't depend on
// the github subpackage.
type ReplayStore interface {
	Seen(ctx context.Context, delivery string, at time.Time) (bool, error)
}

// Verifier authenticates Slack webhook requests.
type Verifier struct {
	secret       []byte
	store        ReplayStore
	replayWindow time.Duration
	now          func() time.Time
}

// NewVerifier constructs a Verifier. secret is the Slack app's
// signing secret (Slack admin → "Basic Information" → "App Credentials"
// → "Signing Secret"). store may be nil to disable replay protection
// for tests.
func NewVerifier(secret []byte, store ReplayStore) *Verifier {
	return &Verifier{
		secret:       secret,
		store:        store,
		replayWindow: DefaultReplayWindow,
		now:          func() time.Time { return time.Now().UTC() },
	}
}

// WithClock overrides the verifier's clock — for deterministic tests.
func (v *Verifier) WithClock(now func() time.Time) *Verifier {
	v.now = now
	return v
}

// WithReplayWindow overrides the default replay window.
func (v *Verifier) WithReplayWindow(d time.Duration) *Verifier {
	v.replayWindow = d
	return v
}

// Name implements verification.Verifier.
func (v *Verifier) Name() string { return "slack" }

// Verify validates the request and returns the parsed Event. Body is
// fully drained — callers must not read r.Body afterwards.
func (v *Verifier) Verify(ctx context.Context, r *http.Request) (*verification.Event, error) {
	if v == nil || len(v.secret) == 0 {
		return nil, errors.New("slack webhook: verifier not configured")
	}

	sigHeader := r.Header.Get("X-Slack-Signature")
	if sigHeader == "" {
		return nil, fmt.Errorf("%w: X-Slack-Signature", ErrMissingHeader)
	}
	tsHeader := r.Header.Get("X-Slack-Request-Timestamp")
	if tsHeader == "" {
		return nil, fmt.Errorf("%w: X-Slack-Request-Timestamp", ErrMissingHeader)
	}
	tsEpoch, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: X-Slack-Request-Timestamp not an integer", ErrMissingHeader)
	}
	ts := time.Unix(tsEpoch, 0).UTC()

	now := v.now()
	if d := now.Sub(ts); d > v.replayWindow || d < -v.replayWindow {
		return nil, fmt.Errorf("%w: ts=%s now=%s skew=%s window=%s",
			ErrTimestampSkew, ts, now, d, v.replayWindow)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("slack webhook: read body: %w", err)
	}

	if err := verifySignature(v.secret, tsHeader, body, sigHeader); err != nil {
		return nil, err
	}

	// Synthesize a delivery id: ts + body-hash. Stable across retransmits
	// of the same payload at the same ts.
	bodyHash := sha256.Sum256(body)
	deliveryID := tsHeader + ":" + hex.EncodeToString(bodyHash[:8])

	if v.store != nil {
		seen, err := v.store.Seen(ctx, deliveryID, now)
		if err != nil {
			return nil, fmt.Errorf("slack webhook: replay store: %w", err)
		}
		if seen {
			return nil, fmt.Errorf("%w: %s", ErrReplay, deliveryID)
		}
	}

	// Slack delivers a JSON body whose top-level `type` field is the
	// event type ("event_callback", "url_verification", "block_actions",
	// etc.). For url_verification we further unwrap the nested challenge,
	// but at the verifier layer we just surface the type.
	eventType := "unknown"
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(body, &probe) == nil && probe.Type != "" {
		eventType = probe.Type
	}

	return &verification.Event{
		Source:     "slack",
		Type:       eventType,
		DeliveryID: deliveryID,
		Body:       body,
	}, nil
}

// verifySignature constant-time compares the v0=<hex> against
// HMAC-SHA256(secret, "v0:<ts>:<body>").
func verifySignature(secret []byte, ts string, body []byte, header string) error {
	const prefix = "v0="
	if len(header) < len(prefix) || header[:len(prefix)] != prefix {
		return fmt.Errorf("%w: missing v0= prefix", ErrInvalidSignature)
	}
	expected, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("v0:"))
	_, _ = mac.Write([]byte(ts))
	_, _ = mac.Write([]byte(":"))
	_, _ = mac.Write(body)
	got := mac.Sum(nil)
	if !hmac.Equal(expected, got) {
		return ErrInvalidSignature
	}
	return nil
}

var _ verification.Verifier = (*Verifier)(nil)
