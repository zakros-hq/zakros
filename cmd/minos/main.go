// Command minos runs the Minos control-plane service.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/GoodOlClint/daedalus/minos/core"
	"github.com/GoodOlClint/daedalus/minos/secrets/file"
	"github.com/GoodOlClint/daedalus/pkg/audit"
)

func main() {
	listen := flag.String("listen", ":8080", "address for the Minos HTTP API")
	providerPath := flag.String("provider", "/etc/minos/secrets.json", "path to the file-backed secret provider store")
	flag.Parse()

	prov, err := file.Open(*providerPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	em := audit.NewStdoutEmitter("minos")

	srv, err := core.New(core.Config{
		ListenAddr:   *listen,
		ProviderPath: *providerPath,
	}, prov, em)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := srv.Run(ctx); err != nil && err != context.Canceled {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
