// Command devpit runs the local sync engine: it loads the config, opens the
// SQLite store, and drives each configured provider connection until the
// process is signalled to stop.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
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
	"github.com/vilaca/devpit/internal/jira"
	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/internal/update"
	"github.com/vilaca/devpit/sdk"

	// Register the built-in providers so config type validation (∈ Registry)
	// and the engine's factory lookup can find them.
	_ "github.com/vilaca/devpit/provider/github"
	_ "github.com/vilaca/devpit/provider/gitlab"
)

// version is the running build's version. It stays "dev" for local builds and
// is overridden at release time via -ldflags -X main.version=… (goreleaser,
// ADR-0023). The update checker treats "dev" as "never check".
var version = "dev"

// httpShutdownTimeout bounds how long graceful HTTP shutdown waits for in-flight
// handlers to drain once the root context is cancelled.
const httpShutdownTimeout = 10 * time.Second

func main() {
	// run holds the deferred cleanup (db.Close, signal stop); main only reports
	// the error, so a fatal exit still runs those defers first.
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// parseLogLevel maps the -log-level flag to a slog.Level, defaulting to Info
// for any unrecognized value so a typo never silences the log entirely.
func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func run() error {
	configPath := flag.String("config", config.DefaultPath(),
		"path to the YAML config file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	logLevel := flag.String("log-level", "info",
		"log verbosity: debug, info, warn, or error")
	flag.Parse()

	if *showVersion {
		_, _ = fmt.Fprintln(os.Stdout, version)
		return nil
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr,
		&slog.HandlerOptions{Level: parseLogLevel(*logLevel)})))

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	for _, w := range cfg.Warnings {
		slog.Warn("config", "warning", w)
	}

	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer func() { _ = db.Close() }()

	slog.Info("starting", "version", version, "config", *configPath,
		"connections", len(cfg.Connections), "db", cfg.DBPath)

	// Cancel the root context on SIGINT/SIGTERM so the engine drains and Closes
	// each provider under its bounded timeout before we exit.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The API Server doubles as the engine's Notifier: its SSE hub fans
	// attention/sync events out to connected clients (structural satisfaction —
	// internal/api must not import internal/engine).
	srv := api.New(db, connectionMeta(cfg.Connections),
		attention.DefaultStaleThreshold, attention.DefaultOldThreshold)
	var _ engine.Notifier = srv
	var _ update.Sink = srv

	// Bind before serving so a port clash (e.g. another instance) is a fatal
	// startup error rather than a lost goroutine.
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Listen, err)
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
			slog.Error("http server", "err", err)
		}
	}()
	slog.Info("listening", "addr", "http://"+cfg.Listen)

	// Surface a newer release as a quiet TopBar chip (ADR-0023). Skipped for
	// dev builds; failures are quiet. inContainer picks the upgrade hint.
	update.New(version, inContainer(), srv).Start(ctx)

	if cfg.Jira != nil {
		r := jira.NewRefresher(jira.Config{
			BaseURL:  cfg.Jira.BaseURL,
			Email:    cfg.Jira.Email,
			APIToken: cfg.Jira.APIToken,
		}, db, srv)
		r.Start(ctx)
		slog.Info("jira enricher started", "base_url", cfg.Jira.BaseURL)
	}

	eng := engine.New(db, cfg.Connections, engine.WithNotifier(srv))
	runErr := eng.Run(ctx)

	// ctx is cancelled by now; drain HTTP handlers before the deferred db.Close.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown", "err", err)
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return fmt.Errorf("engine: %w", runErr)
	}
	return nil
}

// inContainer reports whether we are running inside a Docker container, so the
// update hint can suggest `docker pull` instead of `brew upgrade`. Checked once
// at startup via the marker file the runtime creates.
func inContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
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
