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
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vilaca/devpit/internal/api"
	"github.com/vilaca/devpit/internal/attention"
	"github.com/vilaca/devpit/internal/config"
	"github.com/vilaca/devpit/internal/engine"
	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/sdk"

	// Register the built-in providers so config type validation (∈ Registry)
	// and the engine's factory lookup can find them.
	_ "github.com/vilaca/devpit/provider/github"
	_ "github.com/vilaca/devpit/provider/gitlab"
)

// httpAddr is the loopback address the local dashboard API listens on. Fixed
// for v0.1 (not yet a config field); loopback-only since the API is unauthenticated.
const httpAddr = "localhost:7474"

// httpShutdownTimeout bounds how long graceful HTTP shutdown waits for in-flight
// handlers to drain once the root context is cancelled.
const httpShutdownTimeout = 10 * time.Second

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

	// The API Server doubles as the engine's Notifier: its SSE hub fans
	// attention/sync events out to connected clients (structural satisfaction —
	// internal/api must not import internal/engine).
	srv := api.New(db, connectionMeta(cfg.Connections),
		attention.DefaultStaleThreshold, attention.DefaultAbandonedThreshold)
	var _ engine.Notifier = srv

	// Bind before serving so a port clash (e.g. another instance) is a fatal
	// startup error rather than a lost goroutine.
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", httpAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", httpAddr, err)
	}
	httpServer := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		// Derive request contexts from ctx so SIGINT cancels in-flight SSE
		// streams; without this, Shutdown would block on them until its timeout.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("devpit: http server: %v", err)
		}
	}()
	log.Printf("devpit: API listening on http://%s", httpAddr)

	eng := engine.New(db, cfg.Connections, engine.WithNotifier(srv))
	runErr := eng.Run(ctx)

	// ctx is cancelled by now; drain HTTP handlers before the deferred db.Close.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("devpit: http shutdown: %v", err)
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return fmt.Errorf("engine: %w", runErr)
	}
	return nil
}

// connectionMeta projects the resolved config connections into the API's
// view. Identity is the config-supplied handle (a manual override for bot
// tokens); the engine resolves live identities at runtime but does not yet feed
// them back to the API, so anything else stays empty until then.
func connectionMeta(conns []sdk.ConnectionConfig) []api.ConnectionMeta {
	metas := make([]api.ConnectionMeta, len(conns))
	for i, c := range conns {
		metas[i] = api.ConnectionMeta{
			ID:       c.ID,
			Type:     c.Type,
			BaseURL:  c.BaseURL,
			Label:    c.Label,
			Identity: c.Handle,
		}
	}
	return metas
}
