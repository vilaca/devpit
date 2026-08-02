# Provider SDK

> **Status:** Implemented. The contract — `ConnectionConfig`, `Identity`,
> `Capabilities`, `PollState`, `Event`, `PollResult`, the sentinel/typed
> errors, the `Provider` interface, the factory registry, and the payload
> structs — is defined in `sdk/provider.go`. This spec is the *semantics*
> behind that interface; the type definitions are authoritative in code.
> Decision: `ADR/ADR-0003_Provider_Plugin_Model.md`.

A provider plugin authenticates, discovers the user's work, synchronizes it,
normalizes it into events, declares its capabilities, and resolves identity.
One `Provider` instance is created per configured connection; the engine
manages its lifecycle and calls each method **sequentially on that
connection's goroutine** — there is no concurrency within a single instance, so
a provider needs no internal locking.

## The contract's semantics

- **Identity resolution.** `ResolveIdentity` fetches the authenticated user's
  handle, called once at startup (and on token change). The provider owns the
  manual-handle fallback: it tries the provider's `/user` endpoint, then falls
  back to the configured handle, and only returns `ErrManualIdentityRequired`
  when both fail (bot/deploy tokens). The engine classifies a no-handle or
  bad-token result as *permanent* and any other error as *transient*.
- **Capabilities.** Declared once after identity resolution and stable for the
  connection's life. The engine never asks a provider to produce a bucket it
  declared unavailable; such buckets simply yield no items from that provider.
- **Two poll tiers.** `FastPoll` (~60 s) is the lightweight change-signal tier;
  `Reconcile` (~3 min) is the full identity-scoped sweep that self-heals
  anything the fast tier missed. Both take an opaque `PollState` cursor map — one
  shared map with namespaced keys — that the engine persists; `FastPoll` advances
  it, while `Reconcile` is a cursorless full sweep that reads and returns none and
  instead sets `Complete` so the engine reaps items that left the sweep
  (`ADR/ADR-0024_Reconcile_Item_Reaping.md`). An empty result is valid (nothing changed).
- **Error contract.** When `FastPoll` or `Reconcile` returns a non-nil error the
  engine **discards the returned result entirely and leaves cursors
  untouched** — providers need not accumulate partial events or state alongside
  an error. This is what makes retry idempotent and avoids the 304-skip bug
  (`docs/Synchronization_Engine.md`).
- **Rate signals.** Providers normalize *all* rate-limit signals into a single
  typed retry hint (GitHub's `Retry-After` and `X-RateLimit-Reset`, GitLab's
  `Retry-After`); the engine reads the hint and owns backoff timing. Other
  errors are classified by the engine into the sync-log outcomes via typed
  errors carrying the HTTP status — the engine never parses status codes out of
  message strings.
- **Close.** Releases HTTP clients and any goroutines; providers do not rely on
  GC for teardown.

## What the engine owns (not the provider)

The engine stamps `connection_id` and `observed_at` on every event, serializes
the typed payload to JSON, writes events via `INSERT OR IGNORE` (dedupe on the
`UNIQUE` constraint), persists the returned cursor state, writes one `sync_log`
cycle row per call, and applies backoff. Providers return typed payload structs;
the engine handles serialization, so there is no round-trip ambiguity. The
payload shapes follow the fact set and signals in
`docs/Event_Taxonomy_and_Storage.md`.

## Registry

Built-in providers self-register at init time under their type string
(`"github"`, `"gitlab"`); the factory constructs a `Provider` for a given
connection. Config validation and the engine's lookup both consult the same
registry (`sdk.Registry`).
