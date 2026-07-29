// Command apollo is the Slice H2a LLM broker per architecture.md §6
// External MCP Brokers and docs/phase-2-plan.md §9. It fronts every
// LLM provider Zakros calls so:
//
//   - pods don't hold provider credentials (they hold a JWT scoped
//     to `apollo.<provider>.<model>`),
//   - Apollo terminates the JWT, fetches the upstream credential from
//     Hecate at startup, and forwards the call to the upstream
//     provider with the credential on its egress side,
//   - usage events become broker-observable for future rate-limit /
//     Argus enforcement (H2b).
//
// Phase 2 H2a single-tenant simplification: in-process Provider
// interface, one Anthropic provider compiled in, no subprocess split.
// The §2 D4 "per-provider subprocess isolation" goal lands when a
// second real provider arrives; the Provider interface is shaped so
// that promotion is a contained refactor.
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

// Config is the apollo daemon configuration.
type Config struct {
	ListenAddr string `json:"listen_addr"`

	// SecretsFile points at the file-backed secret provider that
	// holds Apollo's *own* bootstrap auth — specifically the Apollo
	// service JWT in `minos/apollo-token` and the signing pub key
	// (so Apollo can verify incoming pod JWTs).
	SecretsFile string `json:"secrets_file"`

	// SigningKeyPubRef resolves to the PEM public key matching
	// Minos's signing private key. Apollo uses it to verify pod JWTs
	// at ingress.
	SigningKeyPubRef string `json:"signing_key_pub_ref"`

	// HecateURL is the broker URL Apollo calls to fetch upstream
	// provider credentials at startup (e.g. http://127.0.0.1:8084).
	HecateURL string `json:"hecate_url"`

	// ApolloTokenRef resolves through the file-backed provider to
	// the Apollo-as-caller JWT, minted by `minosctl mint-apollo-token`.
	// Apollo presents it to Hecate as `Authorization: Bearer ...`
	// at startup to fetch the per-provider credentials.
	ApolloTokenRef string `json:"apollo_token_ref"`

	// AnthropicCredentialRef is the Vault KV ref that holds the
	// Anthropic API key. Apollo's pod JWT must have
	// `credentials.fetch:<this-value>` in its hecate scope.
	AnthropicCredentialRef string `json:"anthropic_credential_ref"`

	// AnthropicEndpoint is the upstream Anthropic API base URL.
	// Defaults to https://api.anthropic.com.
	AnthropicEndpoint string `json:"anthropic_endpoint"`

	// AllowedAnthropicModels is the set of model names Apollo will
	// accept on /v1/messages. Pod JWTs need
	// `apollo.anthropic.<model>` per call. Empty rejects every
	// model — operator must seed at least one.
	AllowedAnthropicModels []string `json:"allowed_anthropic_models"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	configPath := flag.String("config", "/etc/zakros/apollo.json", "path to config")
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

	apolloTokenVal, err := prov.Resolve(ctx, cfg.ApolloTokenRef)
	if err != nil {
		logger.Error("resolve apollo token", "error", err)
		os.Exit(1)
	}

	em := audit.NewWriterEmitter("apollo", os.Stdout)

	// Fetch upstream credentials from Hecate at startup. Failure is
	// fatal — Apollo without an upstream key is non-functional, and
	// failing fast surfaces the misconfiguration in the systemd log
	// rather than silently 500ing every call.
	hecate := newHecateClient(cfg.HecateURL, string(apolloTokenVal.Data))
	anthropicKey, err := hecate.fetch(ctx, cfg.AnthropicCredentialRef)
	if err != nil {
		logger.Error("fetch anthropic credential", "ref", cfg.AnthropicCredentialRef, "error", err)
		os.Exit(1)
	}

	verifier := &brokerauth.Verifier{
		Broker:    "apollo",
		PublicKey: pub,
		Replay:    brokerauth.NopReplayStore{},
		Audit:     em,
	}

	registry := newProviderRegistry()
	registry.register(newAnthropicProvider(anthropicProviderConfig{
		Endpoint:       cfg.AnthropicEndpoint,
		APIKey:         anthropicKey,
		AllowedModels:  cfg.AllowedAnthropicModels,
		HTTPClient:     &http.Client{Timeout: 120 * time.Second},
		AnthropicVer:   "2023-06-01",
	}))

	srv := &server{
		logger:   logger,
		audit:    em,
		verifier: verifier,
		registry: registry,
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("apollo ready",
		"listen", cfg.ListenAddr,
		"hecate_url", cfg.HecateURL,
		"anthropic_endpoint", cfg.AnthropicEndpoint,
		"allowed_models", cfg.AllowedAnthropicModels)

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
	logger.Info("apollo stopped")
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
	if c.HecateURL == "" {
		return nil, errors.New("hecate_url required")
	}
	if c.ApolloTokenRef == "" {
		return nil, errors.New("apollo_token_ref required")
	}
	if c.AnthropicCredentialRef == "" {
		return nil, errors.New("anthropic_credential_ref required")
	}
	if c.AnthropicEndpoint == "" {
		c.AnthropicEndpoint = "https://api.anthropic.com"
	}
	if len(c.AllowedAnthropicModels) == 0 {
		return nil, errors.New("allowed_anthropic_models required (at least one model)")
	}
	return &c, nil
}
