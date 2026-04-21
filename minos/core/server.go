// Package core is the Minos control-plane service — task registry, project
// config, lifecycle management, and broker-subprocess supervision per
// architecture.md §6. The cmd/minos binary is a thin wrapper around
// core.Server.
//
// Phase 1 scope is the minimum Server state machine; handlers, dispatch,
// reconciliation, and broker supervision land per the Slice A task list in
// docs/phase-1-plan.md.
package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/GoodOlClint/daedalus/pkg/audit"
	"github.com/GoodOlClint/daedalus/pkg/provider"
)

// Config is the operator-supplied configuration loaded at startup. Phase 1
// holds the hardcoded single-admin identity and single-project config here;
// Phase 2 migrates to a registry.
type Config struct {
	ListenAddr string
	// ProviderPath is the filesystem path the file-backed provider reads
	// at startup. Ignored if a different provider is wired in.
	ProviderPath string
}

// Server is the Minos core service instance.
type Server struct {
	cfg      Config
	provider provider.Provider
	audit    audit.Emitter
}

// New returns a Server wired with its dependencies. It does not start any
// I/O; call Run.
func New(cfg Config, p provider.Provider, em audit.Emitter) (*Server, error) {
	if p == nil {
		return nil, errors.New("minos/core: provider is required")
	}
	if em == nil {
		return nil, errors.New("minos/core: audit emitter is required")
	}
	return &Server{cfg: cfg, provider: p, audit: em}, nil
}

// Run blocks until ctx is cancelled or a fatal error occurs.
//
// Phase 1 Slice A task list: HTTP listener on ListenAddr, POST /tasks for
// CLI commissioning, startup reconciliation against Postgres and k3s,
// dispatch loop spawning Labyrinth pods.
func (s *Server) Run(ctx context.Context) error {
	s.audit.Emit(audit.Event{
		Category: "lifecycle",
		Outcome:  "started",
		Message:  fmt.Sprintf("minos core listening target %s", s.cfg.ListenAddr),
	})
	<-ctx.Done()
	s.audit.Emit(audit.Event{
		Category: "lifecycle",
		Outcome:  "stopped",
	})
	return ctx.Err()
}
