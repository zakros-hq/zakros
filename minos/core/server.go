// Package core is the Minos control-plane service — task registry, project
// config, lifecycle management, and broker-subprocess supervision per
// architecture.md §6. The cmd/minos binary is a thin wrapper around
// core.Server.
package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	ghverify "github.com/GoodOlClint/daedalus/cerberus/verification/github"
	hermescore "github.com/GoodOlClint/daedalus/hermes/core"
	"github.com/GoodOlClint/daedalus/minos/dispatch"
	"github.com/GoodOlClint/daedalus/minos/storage"
	"github.com/GoodOlClint/daedalus/pkg/audit"
	"github.com/GoodOlClint/daedalus/pkg/provider"
)

// Server is the Minos core service instance.
type Server struct {
	cfg         Config
	provider    provider.Provider
	store       storage.Store
	dispatcher  dispatch.Dispatcher
	audit       audit.Emitter
	replayStore ghverify.ReplayStore
	hermes      *hermescore.Broker
	namespace   string
	now         func() time.Time
}

// Option configures a Server at construction time.
type Option func(*Server)

// WithClock overrides the Server's clock — for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(s *Server) { s.now = now }
}

// WithNamespace overrides the Kubernetes namespace used for dispatched pods.
// Default is "daedalus".
func WithNamespace(ns string) Option {
	return func(s *Server) { s.namespace = ns }
}

// WithReplayStore wires a Cerberus replay store into the server so the
// GitHub webhook handler can dedupe deliveries. When not set, replay
// protection is disabled — acceptable for -mem-store local dev, not for
// production.
func WithReplayStore(rs ghverify.ReplayStore) Option {
	return func(s *Server) { s.replayStore = rs }
}

// WithHermes wires the Hermes broker into the server. When set,
// Commission creates a task thread on the configured surface and
// populates envelope.Communication.ThreadRef; webhook handlers post
// task summaries back to that thread. When nil, Minos runs without
// surface integration (Slice A posture; CLI intake only).
func WithHermes(h *hermescore.Broker) Option {
	return func(s *Server) { s.hermes = h }
}

// New returns a Server wired with its dependencies. It does not start any
// I/O; call Run.
func New(cfg Config, p provider.Provider, store storage.Store, d dispatch.Dispatcher, em audit.Emitter, opts ...Option) (*Server, error) {
	if p == nil {
		return nil, errors.New("minos/core: provider is required")
	}
	if store == nil {
		return nil, errors.New("minos/core: store is required")
	}
	if d == nil {
		return nil, errors.New("minos/core: dispatcher is required")
	}
	if em == nil {
		return nil, errors.New("minos/core: audit emitter is required")
	}
	s := &Server{
		cfg:        cfg,
		provider:   p,
		store:      store,
		dispatcher: d,
		audit:      em,
		namespace:  "daedalus",
		now:        func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Run blocks until ctx is cancelled or the HTTP listener returns a fatal
// error. The HTTP listener serves the routes declared in api.go.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		s.audit.Emit(audit.Event{
			Category: "lifecycle",
			Outcome:  "started",
			Message:  fmt.Sprintf("minos core listening on %s", s.cfg.ListenAddr),
		})
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
		close(listenErr)
	}()

	select {
	case err, ok := <-listenErr:
		if ok {
			return err
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		s.audit.Emit(audit.Event{
			Category: "lifecycle",
			Outcome:  "shutdown-error",
			Message:  err.Error(),
		})
	}
	s.audit.Emit(audit.Event{Category: "lifecycle", Outcome: "stopped"})
	return ctx.Err()
}
