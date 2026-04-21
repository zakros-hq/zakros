// Package provider defines the secret-provider interface Minos (Phase 1)
// and Hecate (Phase 2) consume. The interface is deliberately minimal: four
// operations per architecture.md §17 MVP Blockers — Secret provider.
//
// Phase 1 reference implementations live under minos/secrets/ (file-backed
// default; Infisical for homelab deployments). Any implementation meeting
// this contract is a valid substitute per environment.md §3.
package provider

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a credential reference does not resolve.
var ErrNotFound = errors.New("credential not found")

// Value holds the resolved credential plus metadata about its lifetime.
// Implementations SHOULD zero the Data slice when the value is no longer
// needed; callers SHOULD treat Data as confidential.
type Value struct {
	Ref       string
	Data      []byte
	ExpiresAt *time.Time
}

// AuditEntry is a past-tense record of one provider operation — emitted to
// Ariadne via pkg/audit so every credential touch is accountable.
type AuditEntry struct {
	At      time.Time
	Op      string
	Ref     string
	Outcome string
}

// Provider is the contract Minos (Phase 1) and Hecate (Phase 2) require of
// any secret backend.
type Provider interface {
	Resolve(ctx context.Context, ref string) (*Value, error)
	Rotate(ctx context.Context, ref string) error
	Revoke(ctx context.Context, ref string) error
	AuditList(ctx context.Context, ref string) ([]AuditEntry, error)
}
