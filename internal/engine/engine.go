package engine

import (
	"context"
	"sync"
	"time"

	"github.com/vilaca/devpit/internal/storage"
	"github.com/vilaca/devpit/sdk"
)

// Default scheduler timings. These are engine constants, not config (§9): the
// tiered cadence is a property of the design, not something a user tunes.
const (
	defaultFastEvery    = 60 * time.Second
	defaultReconEvery   = 15 * time.Minute
	defaultCloseTimeout = 10 * time.Second
)

// Store is the subset of the storage write handle the engine needs (§9, Q9: the
// engine holds the single-writer pool; the API holds the reader pool). Depending
// on this narrow interface rather than *storage.DB keeps cycles unit-testable
// with fakes and no SQLite.
type Store interface {
	LoadCursors(ctx context.Context, connID string) (sdk.PollState, error)
	SaveCursors(ctx context.Context, connID string, state sdk.PollState) error
	WriteEvents(ctx context.Context, connID string, events []sdk.Event) (int, error)
	WriteSyncLog(ctx context.Context, entry storage.SyncLogEntry) error
}

// Engine owns the set of connection goroutines and their shared collaborators.
// Connections are static (Q1): built once from config at Run, alive until the
// process exits.
type Engine struct {
	store  Store
	notify Notifier
	cfgs   []sdk.ConnectionConfig
	conns  []*conn

	fastEvery, reconEvery, closeTimeout time.Duration

	wg sync.WaitGroup
}

// Option customises an Engine at construction. Tests use these to shorten the
// tiers and close timeout; production relies on the defaults.
type Option func(*Engine)

// WithNotifier wires the SSE hub in. The default is a no-op, so the engine runs
// headless (tests, API not yet started).
func WithNotifier(n Notifier) Option {
	return func(e *Engine) {
		if n != nil {
			e.notify = n
		}
	}
}

// WithIntervals overrides the FastPoll and Reconcile cadences (tests only).
func WithIntervals(fast, recon time.Duration) Option {
	return func(e *Engine) {
		e.fastEvery = fast
		e.reconEvery = recon
	}
}

// WithCloseTimeout overrides the per-provider shutdown budget.
func WithCloseTimeout(d time.Duration) Option {
	return func(e *Engine) { e.closeTimeout = d }
}

// New builds an Engine over store for the given static connection list. The
// connections are not built until Run, so New never touches the network.
func New(store Store, cfgs []sdk.ConnectionConfig, opts ...Option) *Engine {
	e := &Engine{
		store:        store,
		notify:       noopNotifier{},
		cfgs:         cfgs,
		fastEvery:    defaultFastEvery,
		reconEvery:   defaultReconEvery,
		closeTimeout: defaultCloseTimeout,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Run builds every connection, starts one goroutine per live connection, then
// blocks until ctx is cancelled and every goroutine has drained and Closed its
// provider (§5, §10). It returns ctx.Err() — the cancellation cause — once the
// WaitGroup joins. A connection that fails to build (unknown type, factory
// error) or whose identity is permanently unresolvable (bad token / no handle)
// is logged to sync_log and skipped; it does not abort startup of the others.
func (e *Engine) Run(ctx context.Context) error {
	for _, cfg := range e.cfgs {
		factory, ok := sdk.Registry[cfg.Type]
		if !ok {
			e.logStartupFailure(cfg.ID, outcomeUnexpected, "unknown provider type "+cfg.Type)
			continue
		}
		prov, err := factory(cfg)
		if err != nil {
			e.logStartupFailure(cfg.ID, outcomeUnexpected, err.Error())
			continue
		}

		c := &conn{
			cfg:          cfg,
			prov:         prov,
			caps:         prov.Capabilities(),
			store:        e.store,
			notify:       e.notify,
			fastEvery:    e.fastEvery,
			reconEvery:   e.reconEvery,
			closeTimeout: e.closeTimeout,
		}

		// Resolve identity up front; classify permanent vs transient (Q2). A
		// permanent failure is dead until a config fix + restart, so don't start
		// its loop. A transient failure still starts: the cycle re-attempts
		// identity at its top until it succeeds.
		if err := c.resolveIdentity(ctx); err != nil && c.identityPermanent {
			e.logStartupFailure(cfg.ID, outcomeAuth, err.Error())
			c.shutdown() // release the provider we built but won't run
			continue
		}

		e.conns = append(e.conns, c)
		e.wg.Go(func() {
			c.run(ctx)
		})
	}

	<-ctx.Done()
	e.wg.Wait()
	return ctx.Err()
}

// logStartupFailure records a connection that never entered the poll loop. It
// applies no backoff (there is no goroutine to back off) and uses the "startup"
// operation so the row is distinguishable from cycle rows.
func (e *Engine) logStartupFailure(connID, outcome, detail string) {
	logCtx, cancel := context.WithTimeout(context.Background(), e.closeTimeout)
	defer cancel()
	_ = e.store.WriteSyncLog(logCtx, storage.SyncLogEntry{
		Ts:           time.Now(),
		ConnectionID: connID,
		Operation:    "startup",
		Outcome:      outcome,
		Error:        &detail,
	})
}
