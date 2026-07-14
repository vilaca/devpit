# User-centric Synchronization

## Status

Accepted

## Scope

Tiered polling with basic `Retry-After`/429 backoff **Implemented (v0.1)**
(`internal/engine`). The adaptive rate-budget scheduler is **Planned**. See
`docs/Roadmap.md`.

## Context

Mirroring entire organizations does not scale — a large org has hundreds of
repositories, almost none relevant to a given user on a given day — and it
burns API budget.

## Decision

Synchronize the work relevant to the *user*, not whole organizations.

- **Discovery** combines a cheap change-signal (notifications/todos) with
  identity-scoped queries (review-requested, assigned, authored, involves-me)
  for the actionable states.
- **Synchronization is tiered polling**: a frequent lightweight `FastPoll` plus
  a slow full `Reconcile` sweep. There are no webhooks — events are synthesized
  by diffing each polled snapshot against stored state
  (`ADR/ADR-0005_Event_Based_Attention_Engine.md`).
- **v0.1 uses a fixed cadence** and honors `Retry-After` / 429 with basic
  exponential backoff. There is **no manual sync trigger** — polling is
  automatic, keeping the REST surface minimal and avoiding a button that
  invites rate-limit hammering.

## Rationale

Identity-scoped discovery is O(your work), not O(repos), which is what makes
large orgs feasible while keeping API usage low. Polling-and-diff preserves the
token-only promise (no provider-side webhook configuration).

## Consequences

- The engine is `internal/engine`; its full implementation is specified in
  `docs/Synchronization_Engine.md`, and the per-provider call sets and rate
  budgets in `docs/Provider_API_Analysis.md`.
- Cadences and the staleness threshold are engine constants (direct code), not
  configuration.
- The full adaptive rate-budget controller is deferred to avoid gold-plating
  before real usage data exists (`docs/Roadmap.md`).

## Amendment — v0.1.3: fast_poll open-set refresh (2026-07-10)

FastPoll now also refreshes the three volatile GraphQL-derived booleans
(`failing_checks`, `needs_approval`, `needs_rebase`) for the full known-open
set on every cycle, not only for todo-bearing items. Motivation: pipeline status
transitions generate no todo, so those badges were up to 15 min stale.

Mechanism: Reconcile populates an `openSnapshots` in-memory cache (full
REST+GraphQL `ItemObservedPayload` keyed by native ID). On each FastPoll cycle,
after the todo-driven path, items in the cache not already covered by a todo
this cycle are queried via a batched GraphQL alias query. The three GraphQL
booleans are merged onto the cached full payload (REST-derived fields are
preserved). Deduplicated by `observedDedupeKey` so no-change cycles are
free. GraphQL failure degrades gracefully: logged, skipped, cycle succeeds.

No lock is needed: FastPoll and Reconcile are serialised on the same goroutine
per connection (`internal/engine/connection.go`). Cache is populated by the
startup reconcile before any FastPoll runs.

## Amendment — v0.1.5: FastPoll drops watched-only notifications (2026-07-14)

The GitHub notifications feed is not identity-scoped: watching a repo delivers
notifications for *any* PR activity in it (reason `subscribed`/`state_change`),
not just work you're involved in. FastPoll was snapshotting every PR
notification, so watched-repo PRs entered the event log with no role and no
signal — and the fold surfaces any open item it holds (`internal/attention/fold.go`,
ADR-0016), so they appeared as bare rows. This violated the O(your work) scoping
principle above (it became O(repos you watch)).

Fix: FastPoll now drops a notification whose reason produces no signal *and*
whose PR carries none of my roles (author/reviewer/assignee) — the item is
neither actionable nor mine, so it is never snapshotted
(`provider/github/fastpoll.go`). Notifications that do carry a signal (mention,
review_requested, assign, ci_activity) or a role are unaffected.
