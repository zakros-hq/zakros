// Command iris is the long-running conversational pod. It long-polls
// Hermes for messages addressed to "@iris" or "/iris", asks Claude
// what to do, and either answers state queries or commissions tasks
// through Minos's HTTP API.
//
// Phase 1 / Slice 0 posture per docs/phase-2-plan.md §4: bearer-token
// auth (no JWT yet), direct Anthropic Messages API (no Apollo yet),
// Claude-backed inference (no Athena Ollama yet — Iris flips to local
// inference once Athena is stood up).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakros-hq/zakros/agents/iris/internal/iris"
)

// envConfig captures the environment variables Iris reads at startup.
// Naming mirrors the existing Phase 1 worker-pod conventions
// (`ZAKROS_*`) where they overlap; Iris-specific knobs prefix `IRIS_`.
type envConfig struct {
	MinosURL              string
	IrisToken             string
	AdminToken            string
	AnthropicKey          string
	AnthropicModel        string
	DatabaseURL           string
	ProjectID             string
	DefaultRepoURL        string
	DefaultBranchPrefix   string
	LongPollSeconds       int
}

func loadEnv() (envConfig, error) {
	c := envConfig{
		MinosURL:            os.Getenv("ZAKROS_MINOS_URL"),
		IrisToken:           os.Getenv("IRIS_BEARER"),
		AdminToken:          os.Getenv("IRIS_ADMIN_TOKEN"),
		AnthropicKey:        os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:      os.Getenv("IRIS_MODEL"),
		DatabaseURL:         os.Getenv("IRIS_DATABASE_URL"),
		ProjectID:           os.Getenv("IRIS_PROJECT_ID"),
		DefaultRepoURL:      os.Getenv("IRIS_DEFAULT_REPO_URL"),
		DefaultBranchPrefix: os.Getenv("IRIS_DEFAULT_BRANCH_PREFIX"),
	}
	// CLAUDE_CODE_OAUTH_TOKEN is the credential the Phase 1 worker pod
	// already injects; Iris accepts it as a fallback so operators don't
	// need a separate Anthropic API key for Slice 0.
	if c.AnthropicKey == "" {
		c.AnthropicKey = os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
	}
	if c.AnthropicModel == "" {
		c.AnthropicModel = "claude-sonnet-4-5"
	}
	if c.DefaultBranchPrefix == "" {
		c.DefaultBranchPrefix = "iris/"
	}

	missing := []string{}
	if c.MinosURL == "" {
		missing = append(missing, "ZAKROS_MINOS_URL")
	}
	if c.IrisToken == "" {
		missing = append(missing, "IRIS_BEARER")
	}
	if c.AdminToken == "" {
		missing = append(missing, "IRIS_ADMIN_TOKEN")
	}
	if c.AnthropicKey == "" {
		missing = append(missing, "ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN")
	}
	if c.DatabaseURL == "" {
		missing = append(missing, "IRIS_DATABASE_URL")
	}
	if c.ProjectID == "" {
		missing = append(missing, "IRIS_PROJECT_ID")
	}
	if len(missing) > 0 {
		return envConfig{}, fmt.Errorf("missing required env: %v", missing)
	}
	return c, nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := loadEnv()
	if err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("postgres pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		logger.Error("postgres ping", "error", err)
		os.Exit(1)
	}

	minosClient := iris.NewMinosClient(cfg.MinosURL, cfg.IrisToken, cfg.AdminToken)
	hermesClient := iris.NewHermesClient(cfg.MinosURL, cfg.IrisToken)
	anthropic := iris.NewAnthropicClient(cfg.AnthropicKey, cfg.AnthropicModel)
	convStore := iris.NewConversationStore(pool)

	tools := &iris.ToolSet{
		Minos:               minosClient,
		ProjectID:           cfg.ProjectID,
		DefaultBranchPrefix: cfg.DefaultBranchPrefix,
		DefaultRepoURL:      cfg.DefaultRepoURL,
	}
	handler := &iris.Handler{
		Hermes:        hermesClient,
		Anthropic:     anthropic,
		Tools:         tools,
		Conversations: convStore,
	}
	poller := &iris.Poller{
		Hermes:          hermesClient,
		Handler:         handler,
		LongPollSeconds: cfg.LongPollSeconds,
		Logger:          logger,
	}

	logger.Info("iris ready",
		"minos_url", cfg.MinosURL,
		"project_id", cfg.ProjectID,
		"model", cfg.AnthropicModel)

	if err := poller.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("poller exited with error", "error", err)
		os.Exit(1)
	}
	logger.Info("iris stopped")
}
