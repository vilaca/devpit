// Command devpit runs the local sync engine: it loads the config, opens the
// SQLite store, and drives each configured provider connection until the
// process is signalled to stop.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/vilaca/devpit/internal/config"
	"github.com/vilaca/devpit/internal/engine"
	"github.com/vilaca/devpit/internal/storage"

	// Register the built-in providers so config type validation (∈ Registry)
	// and the engine's factory lookup can find them.
	_ "github.com/vilaca/devpit/provider/github"
	_ "github.com/vilaca/devpit/provider/gitlab"
)

func main() {
	// run holds the deferred cleanup (db.Close, signal stop); main only reports
	// the error, so a fatal exit still runs those defers first.
	if err := run(); err != nil {
		log.Fatalf("devpit: %v", err)
	}
}

func run() error {
	configPath := flag.String("config", config.DefaultPath(),
		"path to the YAML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	for _, w := range cfg.Warnings {
		_, _ = fmt.Fprintf(os.Stderr, "devpit: warning: %s\n", w)
	}

	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer func() { _ = db.Close() }()

	log.Printf("devpit: loaded %d connection(s), db=%s",
		len(cfg.Connections), cfg.DBPath)

	// Cancel the root context on SIGINT/SIGTERM so the engine drains and Closes
	// each provider under its bounded timeout before we exit.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	eng := engine.New(db, cfg.Connections)
	if err := eng.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("engine: %w", err)
	}
	return nil
}
