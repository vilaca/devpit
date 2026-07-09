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
