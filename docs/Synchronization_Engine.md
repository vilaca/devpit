# Synchronization Engine

> **Status:** Implemented in `internal/engine`. This spec is the design behind
> that code; the exact types, method signatures, and constants are in the
> package. Decisions: `ADR/ADR-0004_User_Centric_Synchronization.md`,
> `ADR/ADR-0007_Storage.md`.

The write side of DevPit. It owns one goroutine per configured connection,
drives the tiered poll loop, persists events + cursors + sync-log rows through
`internal/storage`, and notifies the API layer so the SSE stream can fire. It
computes no attention state itself — buckets are folded on read by
`internal/attention` (`docs/Attention_Engine.md`).

## Responsibilities

- Build one provider per connection from a **static** connection list read once
  at startup. Connections do not change while running (no runtime add/remove).
- Resolve identity, distinguishing **permanent** from **transient** failure.
- Run two tiers per connection on a **single goroutine** — a fast change-signal
  poll (~60 s) and a slow full reconcile (~3 min) — never overlapping.
- Per cycle: load cursors → call the provider → **on success** persist events
  then cursors → write one `sync_log` row → notify. **On error**, persist
  nothing and leave cursors untouched.
- Basic backoff only: honor the provider's rate-limit floor, exponential on
  transient failure. The adaptive rate-budget controller is deferred
  (`docs/Roadmap.md`).
- Graceful shutdown: cancel in-flight calls via context, then close each
  provider under a bounded timeout.

## Concurrency model

- **One goroutine per connection**, selecting over both tickers, so the fast
  and reconcile tiers for the same provider are strictly serialized — this is
  what satisfies the SDK's "no concurrency within a single instance" contract
  without any per-provider lock. A tier that overruns its interval simply skips
  the missed tick (Go tickers drop ticks) — no overlap, no queue buildup.
- **All mutable per-connection state (backoff, resolved flag) is
  goroutine-local** — no shared map, no mutex.
- **Reads never block writes and vice versa.** Storage exposes two pools — a
  single-writer pool the engine uses and a read-only pool the API uses, both in
  WAL — so a long reconcile write never stalls the attention query
  (`ADR/ADR-0007_Storage.md`).

## One poll cycle

The governing rule: **the poll result is consumed only on success. On any error
the engine persists nothing and leaves cursors untouched**, so the next cycle
re-covers the whole batch from the unchanged cursor — the `INSERT OR IGNORE`
dedupe makes the re-fetch idempotent. Backoff waits out the rate-limit floor
first, so the retry runs with budget to finish.

Why not persist partial progress: advancing a conditional cursor (e.g.
GitHub's `If-Modified-Since`) after a mid-batch failure would make the next
request return **304** and silently skip the items never fetched. Discarding
avoids that class of bug entirely — so providers need not accumulate partial
results alongside an error.

On success, ordering is **events first (durable), then cursors** — a crash
between them leaves the cursor unadvanced, so events are re-fetched, never lost.

## Backoff

A goroutine-local gate — no rescheduling, no timers. Tickers keep firing; a
throttled cycle returns early until the gate opens. Transient and auth failures
back off exponentially (one minute doubling to a cap of sixteen). On a
rate-limit signal the delay is `max(exponential streak, provider retry hint)`,
so the engine never retries before the provider's floor but waits longer if the
streak already demands it. The provider normalizes all rate signals (GitHub's
`Retry-After` and `X-RateLimit-Reset`, GitLab's `Retry-After`) into a single
retry hint — the engine owns backoff timing, not the provider.

Backoff is the **only** retry mechanism, and it operates at cycle granularity: a
failure within a cycle is not re-issued in place. An enrichment call that fails
mid-cycle (e.g. a GitLab GraphQL batch) is logged, skipped, and the cycle still
succeeds as `degraded` — the failed work is simply re-attempted on the next
cycle, not retried before the current one returns.

## Sync-log outcomes

One cycle-summary row per cycle per connection. The engine classifies each
cycle into an outcome (the value set is an open `TEXT` column in
`internal/storage/schema.go`, extended without migration — the values
themselves are the constants in `internal/engine/cycle.go`). The
classification the engine applies:

| Outcome | Trigger | Cause shown in the log |
|---|---|---|
| ok | 2xx / 304 | — |
| degraded | cycle succeeded but enrichment partially failed (`PollResult.Degraded`) | — |
| auth | 401 (permanent 403) | Authentication failed — check the token |
| rate_limited | 429 / rate 403 | Rate limited — retry in Ns |
| network | transport only (DNS/TCP/TLS/timeout, no HTTP response) | Couldn't reach {provider} |
| server | provider 5xx | {provider} server error — will retry |
| parse | 2xx but body decode failed | Unexpected response format |
| storage | local write failure | Local storage error |
| unexpected | any other odd status | Unexpected status |

**Shutdown is not a failure**: when the context is cancelled the cycle writes
no row and applies no backoff — it is a clean exit. Per-call detail rows (a
child row per HTTP call under a failed cycle) are **Deferred**; v0.1 records
cycle summaries only (`ADR/ADR-0018_Sync_Observability.md`).

## Startup & shutdown

- **Startup**: each connection seeds with one immediate reconcile so the first
  render isn't empty while waiting for the slow tier. Identity is resolved up
  front; a permanent failure (bad token, unresolvable handle) leaves the
  connection dead until the config is fixed and the process restarts, while a
  transient failure (network/server) starts the goroutine anyway and re-attempts
  identity at the top of each cycle.
- **Shutdown**: the caller cancels the root context; each goroutine closes its
  provider under a fresh bounded context (so cleanup runs even though the root
  is cancelled) and returns.
