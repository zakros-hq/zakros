// Package file implements the pkg/provider.Provider interface backed by a
// local JSON file. It is the Phase 1 default secret provider per
// architecture.md §22 MVP Blockers — Secret provider, intended for clean
// installs and local development where no external secret store is running.
//
// Rotation and revocation semantics under the file provider are restart-
// driven: the operator edits the file, reloads Minos, and new pods pick up
// the new credentials. In-pod credential refresh requires Hecate (Phase 2).
package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/zakros-hq/zakros/pkg/provider"
)

// Store is the on-disk JSON shape. Credentials map ref → base64-or-literal
// string value plus an optional ISO8601 expiry.
type Store struct {
	Credentials map[string]Credential `json:"credentials"`
}

// Credential carries the stored value and optional lifetime.
type Credential struct {
	Value     string  `json:"value"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

// Provider is the file-backed implementation.
type Provider struct {
	path  string
	mu    sync.RWMutex
	store Store
}

// Open loads the store at path and returns a Provider. The file must exist;
// callers that need bootstrap behavior should create the file themselves.
func Open(path string) (*Provider, error) {
	p := &Provider{path: path}
	if err := p.reload(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Provider) reload() error {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return fmt.Errorf("file provider: read %s: %w", p.path, err)
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("file provider: parse %s: %w", p.path, err)
	}
	p.mu.Lock()
	p.store = s
	p.mu.Unlock()
	return nil
}

// Resolve returns the credential value for ref. Missing refs yield
// provider.ErrNotFound.
func (p *Provider) Resolve(_ context.Context, ref string) (*provider.Value, error) {
	p.mu.RLock()
	cred, ok := p.store.Credentials[ref]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", provider.ErrNotFound, ref)
	}
	v := &provider.Value{
		Ref:  ref,
		Data: []byte(cred.Value),
	}
	if cred.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *cred.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("file provider: parse expires_at for %s: %w", ref, err)
		}
		v.ExpiresAt = &t
	}
	return v, nil
}

// Rotate is a no-op under the file provider — rotation is operator-driven.
var errNotSupported = errors.New("operation not supported by file provider")

func (p *Provider) Rotate(_ context.Context, _ string) error {
	return errNotSupported
}

// Revoke is a no-op under the file provider — the operator removes the entry
// from the file and restarts Minos.
func (p *Provider) Revoke(_ context.Context, _ string) error {
	return errNotSupported
}

// AuditList returns an empty slice — the file provider does not retain
// operation history. Audit-grade retention is Ariadne's responsibility via
// pkg/audit.
func (p *Provider) AuditList(_ context.Context, _ string) ([]provider.AuditEntry, error) {
	return nil, nil
}
