// Command minos runs the Minos control-plane service.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

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

	cfg, err := core.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	prov, err := file.Open(*providerPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var store storage.Store
	if *memMode {
		store = memstore.New(nil)
	} else {
		if cfg.DatabaseURL == "" {
			fmt.Fprintln(os.Stderr, "database_url required unless -mem-store is set")
			os.Exit(1)
		}
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer pool.Close()
		if err := pgstore.Migrate(ctx, pool); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		store = pgstore.New(pool)
	}

	em := audit.NewStdoutEmitter("minos")

	var dispatcher dispatch.Dispatcher
	if *fakeDispatch {
		dispatcher = fakedispatch.New()
	} else {
		d, err := k3s.NewFromKubeconfig(*kubeconfig)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		dispatcher = d
	}

	srv, err := core.New(*cfg, prov, store, dispatcher, em)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := srv.Run(ctx); err != nil && err != context.Canceled {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
