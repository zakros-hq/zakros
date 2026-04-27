// Command argus is the Slice J extracted watcher per
// architecture.md §7 + docs/phase-2-plan.md §7. It owns the rules
// engine + heartbeat ingest + push-event ingest as a standalone
// systemd unit on the Minos VM. Minos no longer bundles the watcher;
// it just shares the Postgres LXC for state + uses the Argus binary's
// public HTTP surface for everything else.
//
// JWTs are verified with the Minos-issued Ed25519 public key
// (`minos/signing-key-pub` in the secret store, same key the github-
// broker uses). Pods present `audience=argus, scope=heartbeat`;
// brokers present `audience=argus, scope=event`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakros-hq/zakros/minos/argus"
	"github.com/zakros-hq/zakros/minos/dispatch/k3s"
	"github.com/zakros-hq/zakros/minos/secrets/file"
	"github.com/zakros-hq/zakros/minos/storage/pgstore"
	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/brokerauth"
	"github.com/zakros-hq/zakros/pkg/jwt"
)

// Config is the argus daemon configuration. Read once at startup from
// a JSON file; live edits require a systemd restart.
type Config struct {
	ListenAddr string `json:"listen_addr"`

	// SecretsFile points at the shared deploy/secrets.json. Argus
	// uses the file-backed provider for now; Phase 2 H1 swaps in
	// Hecate for both Minos and Argus.
	SecretsFile string `json:"secrets_file"`

	// SigningKeyPubRef resolves to the PEM public key matching Minos's
	// signing private key.
	SigningKeyPubRef string `json:"signing_key_pub_ref"`

	// DatabaseURL points at the shared Postgres LXC; Argus reads
	// minos.tasks for discovery and writes its persisted state to
	// argus.task_states (created by an earlier migration).
	DatabaseURL string `json:"database_url"`

	// KubeconfigPath gives Argus its own k3s connection so it can
	// terminate pods without a Minos round-trip.
	KubeconfigPath string `json:"kubeconfig_path"`

	// Namespace is the k8s namespace pods run in. Defaults to "zakros"
	// when empty.
	Namespace string `json:"namespace"`

	// MinosURL lets Argus mutual-monitor Minos's /healthz. Empty
	// disables the cross-monitor goroutine.
	MinosURL string `json:"minos_url"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	configPath := flag.String("config", "/etc/zakros/argus.json", "path to config")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	prov, err := file.Open(cfg.SecretsFile)
	if err != nil {
		logger.Error("open secret provider", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pubVal, err := prov.Resolve(ctx, cfg.SigningKeyPubRef)
	if err != nil {
		logger.Error("resolve signing pub key", "error", err)
		os.Exit(1)
	}
	pub, err := jwt.ParsePublicKey(pubVal.Data)
	if err != nil {
		logger.Error("parse signing pub key", "error", err)
		os.Exit(1)
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("postgres pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pgstore.Migrate(ctx, pool); err != nil {
		logger.Error("postgres migrate", "error", err)
		os.Exit(1)
	}
	store := pgstore.New(pool)

	dispatcher, err := k3s.NewFromKubeconfig(cfg.KubeconfigPath)
	if err != nil {
		logger.Error("k3s dispatcher", "error", err)
		os.Exit(1)
	}

	em := audit.NewWriterEmitter("argus", os.Stdout)
	a, err := argus.New(argus.DefaultConfig(), dispatcher, store, nil, em)
	if err != nil {
		logger.Error("argus.New", "error", err)
		os.Exit(1)
	}
	a.WithPersister(argus.NewPGPersister(pool))

	verifier := &brokerauth.Verifier{
		Broker:    "argus",
		PublicKey: pub,
		Replay:    brokerauth.NopReplayStore{},
		Audit:     em,
	}

	srv := &server{
		logger:   logger,
		audit:    em,
		argus:    a,
		verifier: verifier,
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	a.Start(ctx)
	defer a.Stop()

	logger.Info("argus ready", "listen", cfg.ListenAddr, "namespace", cfg.Namespace)

	if cfg.MinosURL != "" {
		go runMinosHealthMonitor(ctx, logger, em, cfg.MinosURL)
	}

	listenErr := make(chan error, 1)
	go func() {
		err := httpSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
		close(listenErr)
	}()

	select {
	case err, ok := <-listenErr:
		if ok {
			logger.Error("listen error", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("shutdown error", "error", err)
	}
	logger.Info("argus stopped")
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := decodeJSON(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.ListenAddr == "" {
		return nil, errors.New("listen_addr required")
	}
	if c.SecretsFile == "" {
		return nil, errors.New("secrets_file required")
	}
	if c.SigningKeyPubRef == "" {
		return nil, errors.New("signing_key_pub_ref required")
	}
	if c.DatabaseURL == "" {
		return nil, errors.New("database_url required")
	}
	if c.KubeconfigPath == "" {
		return nil, errors.New("kubeconfig_path required")
	}
	if c.Namespace == "" {
		c.Namespace = "zakros"
	}
	return &c, nil
}

// runMinosHealthMonitor polls Minos's /healthz on a 30s cadence. Logs
// transitions so Loki/Clio captures Minos availability from Argus's
// perspective. Phase 3 Asclepius replaces this with proper
// service-health monitoring; for Slice J it's just enough so a
// stalled Minos is observable from Argus's audit stream.
func runMinosHealthMonitor(ctx context.Context, logger *slog.Logger, em audit.Emitter, minosURL string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	client := &http.Client{Timeout: 5 * time.Second}
	healthy := true
	check := func() {
		req, _ := http.NewRequestWithContext(ctx, "GET", minosURL+"/healthz", nil)
		resp, err := client.Do(req)
		if err != nil {
			if healthy {
				em.Emit(audit.Event{
					Category: "argus", Outcome: "minos-unhealthy",
					Message: err.Error(),
				})
				logger.Warn("minos unhealthy", "error", err)
			}
			healthy = false
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			if healthy {
				em.Emit(audit.Event{
					Category: "argus", Outcome: "minos-unhealthy",
					Fields: map[string]string{"status": resp.Status},
				})
			}
			healthy = false
			return
		}
		if !healthy {
			em.Emit(audit.Event{
				Category: "argus", Outcome: "minos-recovered",
			})
			logger.Info("minos recovered")
		}
		healthy = true
	}
	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
