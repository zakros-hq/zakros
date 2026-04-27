// Package core is the Minos control-plane service — task registry, project
// config, lifecycle management, and broker-subprocess supervision per
// architecture.md §6. The cmd/minos binary is a thin wrapper around
// core.Server.
package core

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"time"

	ghverify "github.com/zakros-hq/zakros/cerberus/verification/github"
	hermescore "github.com/zakros-hq/zakros/hermes/core"
	mnemocore "github.com/zakros-hq/zakros/mnemosyne/core"
	"github.com/zakros-hq/zakros/minos/dispatch"
	"github.com/zakros-hq/zakros/minos/identity"
	"github.com/zakros-hq/zakros/minos/project"
	"github.com/zakros-hq/zakros/minos/storage"
	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/brokerauth"
	"github.com/zakros-hq/zakros/pkg/jwt"
	"github.com/zakros-hq/zakros/pkg/provider"
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
	mnemosyne   mnemocore.Store
	namespace   string
	now         func() time.Time

	// signingKey is the Ed25519 private key Minos uses to mint pod
	// JWTs. Loaded once at construction from cfg.SigningKeyRef and
	// held in-process so commissions don't pay the resolve+parse cost
	// per call. Public key is derived from this for in-process broker
	// verification.
	signingKey ed25519.PrivateKey
	// One Verifier per broker name Minos hosts in-process. Each gates
	// the routes whose architecture.md §6 broker mapping points here:
	//   minosVerifier     — pod lifecycle callbacks + state queries
	//   hermesVerifier    — Iris's events.next / post_as_iris pull surface
	//   mnemosyneVerifier — memory.lookup
	minosVerifier     *brokerauth.Verifier
	hermesVerifier    *brokerauth.Verifier
	mnemosyneVerifier *brokerauth.Verifier

	// Slice G registries. Both required — Server.New refuses to come up
	// without them (no fallback to the Phase 1 cfg.Admin/cfg.Project
	// scalar paths; greenfield posture per phase-2-plan §2).
	identities identity.Store
	projects   project.Store
}

// Option configures a Server at construction time.
type Option func(*Server)

// WithClock overrides the Server's clock — for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(s *Server) { s.now = now }
}

// WithNamespace overrides the Kubernetes namespace used for dispatched pods.
// Default is "zakros".
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

// WithMnemosyne wires the memory service so Commission populates
// envelope.ContextRef with assembled prior-run context, and the
// POST /tasks/{id}/memory endpoint persists the pod's run record
// (sanitized). When nil, commissions omit context and memory POSTs 404.
func WithMnemosyne(m mnemocore.Store) Option {
	return func(s *Server) { s.mnemosyne = m }
}

// WithIdentities wires the identity registry. Required as of Slice G —
// Server.New errors if no store is provided.
func WithIdentities(s identity.Store) Option {
	return func(srv *Server) { srv.identities = s }
}

// WithProjects wires the project registry. Required as of Slice G.
func WithProjects(s project.Store) Option {
	return func(srv *Server) { srv.projects = s }
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
		namespace:  "zakros",
		now:        func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}

	// Resolve + parse the Ed25519 signing key once at construction so
	// every commission and verifier check shares the parsed object.
	keyVal, err := p.Resolve(context.Background(), cfg.SigningKeyRef)
	if err != nil {
		return nil, fmt.Errorf("minos/core: resolve signing key %s: %w", cfg.SigningKeyRef, err)
	}
	priv, err := jwt.ParsePrivateKey(keyVal.Data)
	if err != nil {
		return nil, fmt.Errorf("minos/core: parse signing key: %w", err)
	}
	s.signingKey = priv

	// Build the in-process broker verifiers. Same signing key, distinct
	// per-broker audiences. Replay tracking is opt-out for these
	// endpoints because the handlers themselves are idempotent (pod
	// callbacks update task state by id; state queries are read-only;
	// pull/post are designed for repeated polling).
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("minos/core: derived public key is not ed25519")
	}
	mkVerifier := func(broker string) *brokerauth.Verifier {
		return &brokerauth.Verifier{
			Broker:    broker,
			PublicKey: pub,
			Replay:    brokerauth.NopReplayStore{},
			Audit:     em,
		}
	}
	s.minosVerifier = mkVerifier("minos")
	s.hermesVerifier = mkVerifier("hermes")
	s.mnemosyneVerifier = mkVerifier("mnemosyne")

	if s.identities == nil {
		return nil, errors.New("minos/core: identity store is required (use WithIdentities)")
	}
	if s.projects == nil {
		return nil, errors.New("minos/core: project store is required (use WithProjects)")
	}
	bootstrapCtx := context.Background()
	if err := bootstrapIdentities(bootstrapCtx, s.identities, cfg.Admins, cfg.SystemIdentities); err != nil {
		return nil, fmt.Errorf("minos/core: bootstrap identities: %w", err)
	}
	if err := bootstrapProject(bootstrapCtx, s.projects, cfg.Project); err != nil {
		return nil, fmt.Errorf("minos/core: bootstrap project: %w", err)
	}

	if s.hermes != nil {
		s.hermes.Subscribe(s.handleInbound)
		// Always register the Iris pull consumer — JWT scope on the
		// /hermes/events.next route is the access control. The buffer
		// is harmless when no Iris pod is reading.
		if err := s.hermes.RegisterPullConsumer(IrisPullConsumer, irisPullCapacity, IrisPullFilter); err != nil {
			return nil, fmt.Errorf("minos/core: register iris pull consumer: %w", err)
		}
	}
	return s, nil
}

// Run blocks until ctx is cancelled or the HTTP listener returns a fatal
// error. The HTTP listener serves the routes declared in api.go.
func (s *Server) Run(ctx context.Context) error {
	// Reconcile running tasks against live pod phases before we serve.
	// Errors audit but don't block startup — a reconcile failure is less
	// bad than refusing to serve at all.
	if err := s.Reconcile(ctx); err != nil {
		s.audit.Emit(audit.Event{
			Category: "lifecycle",
			Outcome:  "reconcile-failed",
			Message:  err.Error(),
		})
	}

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

	// Background sweeper for awaiting-review TTLs.
	go s.runHibernationSweeper(ctx)

	// Mutual health monitor against the extracted Argus binary
	// (Slice J). Argus runs the symmetric monitor against Minos.
	if s.cfg.ArgusURL != "" {
		go s.runArgusHealthMonitor(ctx)
	}

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
