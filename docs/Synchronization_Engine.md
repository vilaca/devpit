# Synchronization Engine

The write side of DevPit. It owns one goroutine per configured connection,
drives the tiered poll loop (§9), persists events + cursors + sync-log rows
through `internal/storage`, and notifies the API layer so the SSE stream can
fire (§13). It computes no attention state itself — buckets are folded on read
by `internal/attention` (§2, §3).

This is the v0.1 implementation spec. Section refs are to
`docs/Design_Decisions.md`. It was settled in a design review; the decisions
and their rationale are logged in §12, and the changes it requires in *other*
packages (SDK, storage, config) plus doc reconciliations are consolidated in
§11 — implement those first.

## 1. Responsibilities

- Build one `sdk.Provider` per connection via `sdk.Registry`, from a **static**
  connection list read once at startup (§1b). Connections do not change while
  running (§11 — the API's `POST`/`DELETE /connections` are dropped).
- Resolve identity, distinguishing **permanent** from **transient** failure.
- Run two tiers per connection on a **single goroutine** — FastPoll (~60 s)
  and Reconcile (~15 min) — never overlapping, per the SDK's "no concurrency
  within a single instance" contract.
- Per cycle: load cursors → call the provider → **on success** persist events
  then cursors → write one `sync_log` row → notify. **On error**, persist
  nothing and leave cursors untouched (§7).
- Basic backoff only (§9): honor the provider's rate-limit floor, exponential
  on transient failure. The adaptive rate-budget controller is deferred.
- Graceful shutdown: cancel in-flight calls via context, then `Close` each
  provider under a bounded timeout.

Non-goals (v0.1): dynamic connection add/remove, per-call `sync_log` detail
rows (§16 level 4), adaptive scheduling, manual sync trigger (there is none —
§9).

## 2. Package layout

```
internal/engine/
  engine.go      Engine: New, Run, static wiring, startup identity resolution
  connection.go  conn: the per-connection two-tier scheduler goroutine
  cycle.go       one poll cycle: call → (success) persist → classify → log → notify
  backoff.go     per-connection backoff gate (goroutine-local, no lock)
  notify.go      Notifier interface + no-op default
```

## 3. Collaborators (narrow interfaces)

The engine depends on interfaces, not concretes, so cycles are unit-testable
with fakes and no SQLite.

```go
// Store is the subset of the storage WRITE handle the engine needs (§9, Q9:
// the engine holds the single-writer pool; the API holds the reader pool).
type Store interface {
	LoadCursors(ctx context.Context, connID string) (sdk.PollState, error)
	SaveCursors(ctx context.Context, connID string, state sdk.PollState) error
	WriteEvents(ctx context.Context, connID string, events []sdk.Event) (int, error)
	WriteSyncLog(ctx context.Context, entry storage.SyncLogEntry) error
}

// Notifier bridges the engine to the SSE hub in internal/api (§13, coarse
// events). The engine defines it and imports nothing from api; main wires the
// hub in. The default is a no-op so the engine runs headless in tests.
type Notifier interface {
	AttentionChanged()               // global: client re-fetches GET /attention
	SyncCompleted(connID string)     // per-connection: health dot + sync log
	SyncFailed(connID, cause string) // per-connection
}
```

## 4. Concurrency model

- **One goroutine per connection.** A single `select` over both tickers means
  FastPoll and Reconcile for the *same* provider instance are strictly
  serialized — satisfying the SDK contract without any per-provider lock. (Go
  tickers drop ticks when the receiver is busy, so a FastPoll that overruns its
  interval simply skips the missed tick — no overlap, no queue buildup.)
- **All mutable per-connection state (backoff, `resolved` flag) is
  goroutine-local** — held on the `conn` value, touched only by that
  connection's goroutine. No shared map, no mutex.
- **Reads never block writes and vice versa (§14, Q9).** `storage.Open` returns
  **two pools**: a write handle (`MaxOpenConns(1)`) the engine uses, and a
  read-only pool (`MaxOpenConns(N)`) the API uses, both in WAL. A single pooled
  connection would have serialized API reads behind engine writes — defeating
  the reason WAL was chosen (ADR-0007). With split pools, a long reconcile
  write never stalls `GET /attention`.

## 5. Engine lifecycle

Connections are static (Q1): built once from config, alive until process exit.

```go
type Engine struct {
	store   Store
	notify  Notifier
	conns   []*conn
	fastEvery, reconEvery, closeTimeout time.Duration // constants, not config (§9)
	wg      sync.WaitGroup
}

func New(store Store, cfgs []sdk.ConnectionConfig, opts ...Option) *Engine

// Run builds every connection, then blocks until ctx is cancelled and every
// connection goroutine has drained and Closed its provider.
func (e *Engine) Run(ctx context.Context) error {
	for _, cfg := range e.cfgs {
		factory, ok := sdk.Registry[cfg.Type]
		if !ok {
			e.logStartupFailure(ctx, cfg.ID, "unexpected", "unknown provider type "+cfg.Type)
			continue
		}
		p, err := factory(cfg)
		if err != nil {
			e.logStartupFailure(ctx, cfg.ID, "unexpected", err.Error())
			continue
		}
		c := &conn{
			cfg: cfg, prov: p, caps: p.Capabilities(),
			store: e.store, notify: e.notify,
			fastEvery: e.fastEvery, reconEvery: e.reconEvery,
			closeTimeout: e.closeTimeout,
		}
		// Resolve identity up front; classify permanent vs transient (Q2).
		if err := c.resolveIdentity(ctx); err != nil && c.identityPermanent {
			e.logStartupFailure(ctx, cfg.ID, "auth", err.Error())
			continue // dead until config fix + restart
		}
		e.conns = append(e.conns, c)
		e.wg.Add(1)
		go func() { defer e.wg.Done(); c.run(ctx) }()
	}
	<-ctx.Done()
	e.wg.Wait()
	return ctx.Err()
}
```

**Identity resolution (Q2, Q4).** `resolveIdentity` calls
`prov.ResolveIdentity`. The provider itself owns the §7 manual-handle fallback:
it tries `/user`, then falls back to `cfg.Handle`, and only returns
`ErrManualIdentityRequired` when both fail. The engine then classifies:

- `ErrManualIdentityRequired` (no handle) or `ErrUnauthorized` (bad token) →
  **permanent**: `identityPermanent = true`; don't start the loop.
- Any other error (network/server/rate) → **transient**: start the goroutine
  anyway; the cycle re-attempts identity at its top until it succeeds
  (`GET /connections` shows `identity: null` + a degraded dot meanwhile).

## 6. Per-connection scheduler

```go
type conn struct {
	cfg      sdk.ConnectionConfig
	prov     sdk.Provider
	caps     sdk.Capabilities
	store    Store
	notify   Notifier
	bo       backoff // goroutine-local
	resolved bool
	identityPermanent bool
	fastEvery, reconEvery, closeTimeout time.Duration
}

func (c *conn) run(ctx context.Context) {
	fast := time.NewTicker(c.fastEvery)
	recon := time.NewTicker(c.reconEvery)
	defer fast.Stop()
	defer recon.Stop()

	// Seed state with one reconcile so the first render isn't empty while
	// waiting ~15 min for the slow tier.
	c.cycle(ctx, opReconcile)

	for {
		select {
		case <-ctx.Done():
			closeCtx, cancel := context.WithTimeout(context.Background(), c.closeTimeout)
			_ = c.prov.Close(closeCtx)
			cancel()
			return
		case <-fast.C:
			if c.caps.FastSignal {
				c.cycle(ctx, opFastPoll)
			}
		case <-recon.C:
			c.cycle(ctx, opReconcile)
		}
	}
}
```

## 7. One poll cycle

The governing rule (Q5): **`PollResult` is consumed only on success. On any
error the engine persists nothing and leaves cursors untouched**, so the next
cycle re-covers the whole batch from the unchanged cursor — `INSERT OR IGNORE`
(schema + taxonomy §5) makes the re-fetch idempotent. This is safe against
livelock because backoff waits out the rate-limit floor, so the retry runs with
budget to finish.

Why not persist partial progress: advancing a conditional cursor (e.g. GitHub's
`If-Modified-Since`) after a mid-batch failure would make the next request
return **304** and silently skip the items never fetched. Discarding avoids
that class of bug entirely. Providers therefore need not accumulate partial
`Events`/`State` alongside an error (§11).

On success, ordering is **events first (durable), then cursors** — a crash
between them leaves the cursor unadvanced, so events are re-fetched, never lost.

```go
func (c *conn) cycle(ctx context.Context, op operation) {
	if !c.bo.ready() { // still backing off
		return
	}
	if !c.resolved { // transient identity failure earlier (Q2)
		if err := c.resolveIdentity(ctx); err != nil {
			c.fail(ctx, op, err)
			return
		}
	}

	state, err := c.store.LoadCursors(ctx, c.cfg.ID)
	if err != nil {
		c.logCycle(ctx, op, outcomeStorage, 0, nil, err)
		c.bo.bump()
		return
	}

	var result sdk.PollResult
	switch op {
	case opFastPoll:
		result, err = c.prov.FastPoll(ctx, state)
	case opReconcile:
		result, err = c.prov.Reconcile(ctx, state)
	}
	if err != nil {
		if ctx.Err() != nil {
			return // shutdown, not a failure (Q6)
		}
		c.fail(ctx, op, err)
		return
	}

	// Success path: events, then cursors (both no-op on empty).
	inserted, werr := c.store.WriteEvents(ctx, c.cfg.ID, result.Events)
	if werr != nil {
		c.logCycle(ctx, op, outcomeStorage, 0, nil, werr)
		c.bo.bump()
		return
	}
	if werr = c.store.SaveCursors(ctx, c.cfg.ID, result.State); werr != nil {
		c.logCycle(ctx, op, outcomeStorage, inserted, nil, werr)
		c.bo.bump()
		return
	}

	c.logCycle(ctx, op, outcomeOK, inserted, result.RateRemaining, nil)
	c.bo.reset()
	if inserted > 0 {
		c.notify.AttentionChanged()
	}
	c.notify.SyncCompleted(c.cfg.ID)
}

// fail classifies a provider error into a sync-log outcome, applies backoff,
// and notifies. It relies on the classified SDK errors (§11).
func (c *conn) fail(ctx context.Context, op operation, err error) {
	switch {
	case errors.Is(err, sdk.ErrUnauthorized):
		c.resolved = false
		c.logCycle(ctx, op, outcomeAuth, 0, nil, err)
		c.bo.bump()
		c.notify.SyncFailed(c.cfg.ID, "authentication failed")

	case errors.Is(err, sdk.ErrRateLimited):
		var rle *sdk.RateLimitError
		var hint *time.Duration
		if errors.As(err, &rle) {
			hint = &rle.RetryAfter
		}
		d := c.bo.rateLimit(hint) // floor = max(engineBackoff, hint) — Q3
		c.logCycleRetry(ctx, op, outcomeRateLimited, nil, time.Now().Add(d), err)
		c.notify.SyncFailed(c.cfg.ID, "rate limited")

	default:
		outcome, status := classify(err) // server | parse | unexpected | network
		c.logCycle(ctx, op, outcome, 0, status, err)
		c.bo.bump()
		c.notify.SyncFailed(c.cfg.ID, causeText(outcome))
	}
}
```

## 8. Backoff (§9, basic)

Goroutine-local gate — no rescheduling, no timers. Tickers keep firing; a
throttled cycle returns early until `notBefore` passes.

```go
type backoff struct {
	notBefore time.Time
	streak    int
}

func (b *backoff) ready() bool { return !time.Now().Before(b.notBefore) }
func (b *backoff) reset()      { b.streak = 0; b.notBefore = time.Time{} }

// bump: exponential 1→2→4→8→16 min (capped) for transient/auth failures.
func (b *backoff) bump() {
	b.streak++
	b.notBefore = time.Now().Add(time.Minute << min(b.streak-1, 4))
}

// rateLimit: never retry before the provider's floor (Q3); wait longer if the
// exponential streak already demands it.
func (b *backoff) rateLimit(hint *time.Duration) time.Duration {
	b.streak++
	d := time.Minute << min(b.streak-1, 4)
	if hint != nil && *hint > d {
		d = *hint
	}
	b.notBefore = time.Now().Add(d)
	return d
}
```

## 9. Sync-log outcome taxonomy (§16, Q6)

`sync_log.outcome` is a free `TEXT` column (no migration to extend). One
cycle-summary row per cycle; per-call detail rows are deferred (§11). Accurate
causes matter for debugging, so the set is:

| Outcome | Trigger | Cause text (log view) |
|---|---|---|
| `ok` | 2xx / 304 | — |
| `auth` | 401 (perm 403) | Authentication failed — check the token |
| `rate_limited` | 429 / rate 403 | Rate limited — retry in Ns |
| `network` | transport only (DNS/TCP/TLS/timeout, no HTTP response) | Couldn't reach {provider} |
| `server` | provider **5xx** | {provider} server error ({status}) — will retry |
| `parse` | 2xx but body decode failed | Unexpected response format |
| `storage` | local `WriteEvents`/`SaveCursors` failure | Local storage error — {detail} |
| `unexpected` | any other odd status (stray 4xx) | Unexpected status {status} |

`http_status` is populated for `server`/`unexpected` (carried by the SDK
`StatusError`, §11); `retries` = the backoff streak; `next_retry` is set on
`rate_limited`. **Shutdown** (`ctx.Err() != nil`) writes no row and applies no
backoff — it is a clean exit, not a failure.

## 10. Startup & shutdown

- **Startup:** each connection seeds with one immediate reconcile. At single-
  user scale (2–4 connections) the simultaneous seed is negligible; no
  stagger needed for v0.1.
- **Shutdown:** the caller cancels the root context. Each goroutine observes
  `ctx.Done()`, `Close`s its provider under `closeTimeout` (a fresh context, so
  cleanup runs even though the root is cancelled), and returns. `Run` joins the
  `WaitGroup` and returns `ctx.Err()`.

## 11. Cross-cutting changes required (do these first)

The engine presupposes changes in other packages and a few doc reconciliations:

**SDK (`sdk/provider.go`)**
1. **`RetryAfter` as a typed error (Q3).** Add
   `type RateLimitError struct { RetryAfter time.Duration }` that unwraps to
   `ErrRateLimited`. Providers return it; the engine reads the floor via
   `errors.As`, and `errors.Is(err, ErrRateLimited)` still works. Providers
   normalize *all* rate signals into `RetryAfter` (GitHub: `Retry-After` **and**
   `X-RateLimit-Reset`). Delete the `gh.fast.retry_after` / `gl.fast.retry_after`
   cursors — the engine owns backoff timing.
2. **Classified errors (Q6).** Add sentinels `ErrServer`, `ErrParse`,
   `ErrUnexpected`, and `type StatusError struct { Status int; ... }` that
   unwraps to `ErrServer` (5xx) or `ErrUnexpected` (other non-success) so the
   engine gets the outcome via `errors.Is` and the code via `errors.As`.
   Transport/unclassified errors stay bare → `network`. Decode failures wrap
   `ErrParse`. (The engine must not parse status codes out of message strings.)
3. **Manual identity handle (Q4).** Add `Handle string` to `ConnectionConfig`;
   the provider's `ResolveIdentity` falls back to it before returning
   `ErrManualIdentityRequired`.
4. **Codify the error contract (Q5).** Document on `Provider` that when a method
   returns a non-nil error the engine ignores the returned `PollResult`
   entirely; cursors persist only on success. Providers need not accumulate
   partial results.

**Storage (`internal/storage`)**
5. **Fix the `events` `UNIQUE` (Q7).** Change to
   `UNIQUE (connection_id, object_type, native_id, event_type, dedupe_key)` —
   the current `(connection_id, dedupe_key)` collides on `item.removed`'s
   constant key and is fragile for observed/signals. Pre-release, edit
   `migrations[0]` in place and drop dev DBs (switch to append-only migrations
   at first release).
6. **Split pools (Q9).** `Open` returns a write handle (`MaxOpenConns(1)`) and
   a read-only pool (`MaxOpenConns(N)`), both WAL, to honor §14.

**Config (`internal/config`, Q8)**
7. Build it: YAML (already a transitive dep), `--config` flag defaulting to
   `$XDG_CONFIG_HOME/devpit/config.yaml`. Shape: `db_path` + `connections[]`
   with `id` (required, unique, stable — keys every DB row), `type` (∈ Registry),
   `token` (required), optional `base_url`/`label`/`handle`. On load: 0600
   permission *warning* (§15); validate. `Load` returns `[]sdk.ConnectionConfig`.
   Poll intervals + staleness threshold stay engine constants (§9), not config.

**Doc reconciliations**
8. `REST_API.md`: drop `POST /connections`, `DELETE /connections/{id}`, and the
   `409 conflict` code (connections are config-file only, static at runtime —
   Q1). Keep `GET /connections` (read-only health/identity view, §12).
9. `Event_Taxonomy_and_Storage.md` §16: add `server`, `storage`, `unexpected`
   to the outcome list. Note the kept lean names (`handle_next` not `flags`;
   `schema_version` table, with `app_state`/`last_seen_event_id` added only when
   "what's new since last visit" is built — an API concern).

## 12. Resolved decisions (design review)

1. **Static connections** from config, no runtime add/remove; REST create/delete
   dropped.
2. **Identity: permanent vs transient** — bad token / no-handle = dead until
   restart; network/server = start and retry in-loop.
3. **Rate-limit floor** via typed `RateLimitError`; `delay = max(backoff, hint)`;
   provider normalizes all rate signals.
4. **Manual handle** on `ConnectionConfig`; provider owns the fallback.
5. **On error, ignore `PollResult`; cursors advance only on success** —
   avoids the 304-skip bug; idempotent re-fetch on retry.
6. **8-outcome sync-log taxonomy** — `server`/`storage`/`unexpected` distinct
   from `network`; shutdown is not a failure.
7. **`events` `UNIQUE` → 5-column** now; lean names/deferred columns doc-fixed.
8. **Config: YAML**, static shape, intervals as constants.
9. **Split storage pools** so writes never block reads (§14).
10. **`Notifier` push interface** — `AttentionChanged()`, `SyncCompleted`,
    `SyncFailed`.
```
