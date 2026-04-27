// Package verification is the plugin layer for Cerberus's webhook
// verifiers per architecture.md §6 Webhook Ingress: Cerberus and
// docs/phase-2-plan.md §7. Each verifier authenticates one source
// (GitHub HMAC, Slack signing, future Discord Ed25519); Cerberus
// dispatches by route prefix to the matching plugin.
//
// Slice J ships GitHub (existing, repackaged into the plugin shape)
// + Slack signing. Cerberus stays a library inside Minos for now —
// standalone-broker extraction earns its cost when ingress plugins
// (Tailscale Funnel, etc.) actually need to land per phase-2-plan §2 D8.
package verification

import (
	"context"
	"errors"
	"net/http"
)

// Common errors verifiers return so callers can pattern-match without
// importing each verifier subpackage.
var (
	// ErrMissingHeader — a required signing header is absent.
	ErrMissingHeader = errors.New("verification: missing header")
	// ErrInvalidSignature — the request signature failed cryptographic check.
	ErrInvalidSignature = errors.New("verification: invalid signature")
	// ErrReplay — the request's delivery id has already been processed.
	ErrReplay = errors.New("verification: replay detected")
	// ErrTimestampSkew — the request's timestamp is outside the verifier's
	// accepted window (used by Slack-style signers that include a ts header).
	ErrTimestampSkew = errors.New("verification: timestamp skew")
)

// Event is the verified-and-parsed shape every verifier returns.
// Type is the source-specific event identifier (e.g. GitHub's
// X-GitHub-Event, Slack's `event.type`, etc.); DeliveryID is the
// idempotency key the source supplies; Body is the raw verified
// payload for downstream handlers.
type Event struct {
	Source     string
	Type       string
	DeliveryID string
	Body       []byte
}

// Verifier is the contract each plugin implements. Name() identifies
// the plugin so Cerberus can route /webhooks/<name> by URL path.
// Verify reads + validates the request and returns the parsed Event,
// or one of the sentinel errors above.
type Verifier interface {
	Name() string
	Verify(ctx context.Context, r *http.Request) (*Event, error)
}
