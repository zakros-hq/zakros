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

	"github.com/GoodOlClint/daedalus/minos/dispatch"
	"github.com/GoodOlClint/daedalus/minos/storage"
	"github.com/GoodOlClint/daedalus/pkg/audit"
	"github.com/GoodOlClint/daedalus/pkg/provider"
)

// Server is the Minos core service instance.
type Server struct {
	cfg        Config
	provider   provider.Provider
	store      storage.Store
	dispatcher dispatch.Dispatcher
	audit      audit.Emitter
	namespace  string
	now        func() time.Time
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
