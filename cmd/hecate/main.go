// Command hecate is the Slice H1 credentials broker per
// architecture.md §6 Credential Handling and docs/phase-2-plan.md §8.
// It fronts OpenBao: callers (worker pods, brokers, future Iris) hit
// /credentials/fetch with a Minos-minted JWT carrying scope
// `credentials.fetch:<ref>`, Hecate verifies the scope matches the
// requested ref, reads the value from Vault KV, and returns the
// plaintext.
//
// Slice H1 single-tenant simplification: Hecate uses one well-
// permissioned Vault token (`hecate-app`) for every read; per-scope
// access control happens at the JWT layer. Phase 2 K hardening can
// upgrade this to per-call policy-bound token minting (one Vault
// policy per `credentials.fetch:<ref>` scope) without changing the
// caller-facing API.
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

	"github.com/zakros-hq/zakros/minos/secrets/file"
	"github.com/zakros-hq/zakros/pkg/audit"
	"github.com/zakros-hq/zakros/pkg/brokerauth"
	"github.com/zakros-hq/zakros/pkg/jwt"
)

// Config is the hecate daemon configuration.
type Config struct {
	ListenAddr string `json:"listen_addr"`

	// SecretsFile points at the file-backed secret provider that
	// holds Hecate's *own* bootstrap auth — specifically the Vault
	// token in `hecate/vault-token` and the signing pub key. Hecate
	// can't bootstrap from itself, so its own auth lives in the
	// file-backed provider until a chain-of-trust mechanism lands.
	SecretsFile string `json:"secrets_file"`

	// SigningKeyPubRef resolves to the PEM public key matching Minos's
	// signing private key — same one github-broker and argus use.
	SigningKeyPubRef string `json:"signing_key_pub_ref"`

	// VaultAddr is the OpenBao base URL (e.g. http://172.16.140.104:8200).
	VaultAddr string `json:"vault_addr"`

	// VaultTokenRef resolves through the file-backed provider to the
	// long-lived Vault token Hecate uses on every Vault HTTP call.
	// Created by deploy/openbao-bootstrap.sh as the `hecate-app`
	// token.
	VaultTokenRef string `json:"vault_token_ref"`

	// VaultKVMount is the KV v2 mount path. Defaults to "secret".
	VaultKVMount string `json:"vault_kv_mount"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	configPath := flag.String("config", "/etc/zakros/hecate.json", "path to config")
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

	vaultTokenVal, err := prov.Resolve(ctx, cfg.VaultTokenRef)
	if err != nil {
		logger.Error("resolve vault token", "error", err)
		os.Exit(1)
	}

	em := audit.NewWriterEmitter("hecate", os.Stdout)
	verifier := &brokerauth.Verifier{
		Broker:    "hecate",
		PublicKey: pub,
		Replay:    brokerauth.NopReplayStore{},
		Audit:     em,
	}

	vault := newVaultClient(cfg.VaultAddr, string(vaultTokenVal.Data), cfg.VaultKVMount)

	srv := &server{
		logger:   logger,
		audit:    em,
		verifier: verifier,
		vault:    vault,
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("hecate ready",
		"listen", cfg.ListenAddr,
		"vault_addr", cfg.VaultAddr,
		"vault_kv_mount", cfg.VaultKVMount)

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
	logger.Info("hecate stopped")
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
	if c.VaultAddr == "" {
		return nil, errors.New("vault_addr required")
	}
	if c.VaultTokenRef == "" {
		return nil, errors.New("vault_token_ref required")
	}
	if c.VaultKVMount == "" {
		c.VaultKVMount = "secret"
	}
	return &c, nil
}
