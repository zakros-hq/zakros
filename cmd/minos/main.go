// Command minos runs the Minos control-plane service.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GoodOlClint/daedalus/cerberus/core/replay"
	ghverify "github.com/GoodOlClint/daedalus/cerberus/verification/github"
	hermescore "github.com/GoodOlClint/daedalus/hermes/core"
	hermesdiscord "github.com/GoodOlClint/daedalus/hermes/plugins/discord"
	mnemocore "github.com/GoodOlClint/daedalus/mnemosyne/core"
	mnemomem "github.com/GoodOlClint/daedalus/mnemosyne/memstore"
	mnemopg "github.com/GoodOlClint/daedalus/mnemosyne/pgstore"
	"github.com/GoodOlClint/daedalus/minos/argus"
	"github.com/GoodOlClint/daedalus/minos/core"
	"github.com/GoodOlClint/daedalus/minos/dispatch"
	"github.com/GoodOlClint/daedalus/minos/dispatch/fakedispatch"
	"github.com/GoodOlClint/daedalus/minos/dispatch/k3s"
	"github.com/GoodOlClint/daedalus/minos/secrets/file"
	"github.com/GoodOlClint/daedalus/minos/storage"
	"github.com/GoodOlClint/daedalus/minos/storage/memstore"
	"github.com/GoodOlClint/daedalus/minos/storage/pgstore"
	"github.com/GoodOlClint/daedalus/pkg/audit"
)

func main() {
	configPath := flag.String("config", "/etc/minos/config.json", "path to Minos daemon config")
	providerPath := flag.String("provider", "/etc/minos/secrets.json", "path to the file-backed secret provider store")
	memMode := flag.Bool("mem-store", false, "use in-memory task store (tests/local dev; no persistence across restart)")
	fakeDispatch := flag.Bool("fake-dispatch", false, "use in-memory fake dispatcher (tests/local dev without k3s)")
	kubeconfig := flag.String("kubeconfig", "", "path to kubeconfig (empty = in-cluster config)")
	flag.Parse()

	if err := run(*configPath, *providerPath, *memMode, *fakeDispatch, *kubeconfig); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(configPath, providerPath string, memMode, fakeDispatch bool, kubeconfig string) error {
	cfg, err := core.LoadConfig(configPath)
	if err != nil {
		return err
	}
	prov, err := file.Open(providerPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, pool, err := openStore(ctx, cfg, memMode)
	if err != nil {
		return err
	}
	if pool != nil {
		defer pool.Close()
	}

	dispatcher, err := openDispatcher(fakeDispatch, kubeconfig)
	if err != nil {
		return err
	}

	replayStore := openReplayStore(pool, memMode)

	em := audit.NewStdoutEmitter("minos")

	hermes, err := openHermes(ctx, cfg, prov)
	if err != nil {
		return err
	}

	a, err := argus.New(argus.DefaultConfig(), dispatcher, store, hermes, em)
	if err != nil {
		return fmt.Errorf("argus: %w", err)
	}
	if pool != nil {
		a.WithPersister(argus.NewPGPersister(pool))
	} else {
		a.WithPersister(argus.NewMemPersister())
	}

	mnemosyne := openMnemosyne(pool, memMode)

	opts := []core.Option{core.WithReplayStore(replayStore), core.WithArgus(a), core.WithMnemosyne(mnemosyne)}
	if hermes != nil {
		opts = append(opts, core.WithHermes(hermes))
	}

	srv, err := core.New(*cfg, prov, store, dispatcher, em, opts...)
	if err != nil {
		return err
	}

	if hermes != nil {
		if err := hermes.Start(ctx); err != nil {
			return fmt.Errorf("start hermes: %w", err)
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = hermes.Stop(shutdownCtx)
		}()
	}
	a.Start(ctx)
	defer a.Stop()

	if err := srv.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

// openHermes wires the Hermes broker and registers whichever surface
// plugins the config enables. Returns nil when no plugin is configured —
// Minos runs CLI-only in that case.
func openHermes(ctx context.Context, cfg *core.Config, prov *file.Provider) (*hermescore.Broker, error) {
	if cfg.Discord.BotTokenRef == "" || cfg.Project.ThreadParent == "" {
		return nil, nil
	}
	tokenVal, err := prov.Resolve(ctx, cfg.Discord.BotTokenRef)
	if err != nil {
		return nil, fmt.Errorf("resolve discord token: %w", err)
	}
	plugin, err := hermesdiscord.New(hermesdiscord.Config{
		Token:          string(tokenVal.Data),
		WatchChannelID: cfg.Project.ThreadParent,
	})
	if err != nil {
		return nil, fmt.Errorf("discord plugin: %w", err)
	}
	broker := hermescore.New()
	if err := broker.RegisterPlugin(plugin); err != nil {
		return nil, fmt.Errorf("register discord plugin: %w", err)
	}
	return broker, nil
}

// openStore returns the configured task store. pool is non-nil for the
// Postgres path so the caller can share it with replay and close it.
func openStore(ctx context.Context, cfg *core.Config, memMode bool) (storage.Store, *pgxpool.Pool, error) {
	if memMode {
		return memstore.New(nil), nil, nil
	}
	if cfg.DatabaseURL == "" {
		return nil, nil, fmt.Errorf("database_url required unless -mem-store is set")
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := pgstore.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, err
	}
	return pgstore.New(pool), pool, nil
}

func openDispatcher(fakeDispatch bool, kubeconfig string) (dispatch.Dispatcher, error) {
	if fakeDispatch {
		return fakedispatch.New(), nil
	}
	return k3s.NewFromKubeconfig(kubeconfig)
}

// openReplayStore picks the webhook delivery dedup backend. Window default
// 24h covers GitHub's retry horizon; rows older get purged by operator cron.
func openReplayStore(pool *pgxpool.Pool, memMode bool) ghverify.ReplayStore {
	const window = 24 * time.Hour
	if memMode || pool == nil {
		return replay.NewMemStore(window)
	}
	return replay.NewPGStore(pool, "github", window)
}

// openMnemosyne picks the memory backend to match the task store: memstore
// when running with -mem-store, pgstore when a shared Postgres pool is up.
func openMnemosyne(pool *pgxpool.Pool, memMode bool) mnemocore.Store {
	if memMode || pool == nil {
		return mnemomem.New()
	}
	return mnemopg.New(pool)
}
