# User-centric Synchronization

## Scope

Tiered polling with basic `Retry-After`/429 backoff **Implemented (v0.1)**,
extended through v0.1.6 (`internal/engine`). The adaptive rate-budget scheduler is **Planned**. See
`docs/Roadmap.md`.

## Context

Mirroring entire organizations does not scale — a large org has hundreds of
repositories, almost none relevant to a given user on a given day — and it
burns API budget.

## Decision

Synchronize the work relevant to the *user*, not whole organizations.

- **Discovery** combines a cheap change-signal (notifications/todos) with
  identity-scoped queries (review-requested, assigned, authored) for the
  actionable states, plus a **sole-approver** scope: open PRs/MRs on
  repos where the user is the only account that can merge (the `sole_approver`
  role, `ADR/ADR-0016_Presentation_And_Ranking.md`), excluding drafts and the
  user's own work, which `author` already covers.
- **A change signal is not scope.** GitHub's notifications feed also covers
  repos the user merely watches; a notification that yields no signal, on an
  open PR the user holds no role in, is dropped rather than snapshotted.
- **Synchronization is tiered polling**: a frequent lightweight `FastPoll`
  plus a slower full `Reconcile` sweep. There are no webhooks — events
  are synthesized by diffing each polled snapshot against stored state
  (`ADR/ADR-0005_Event_Based_Attention_Engine.md`). On GitLab, `FastPoll` also
  refreshes the volatile GraphQL-derived fields (pipeline, approval, rebase,
  conflict) of **every known-open item**, not only those a todo touched —
  pipeline transitions create no todo, so badges would otherwise wait for the
  sweep.
- **The cadence is fixed** and honors `Retry-After` / 429 with basic
  exponential backoff. There is **no manual sync trigger** — polling is
  automatic, keeping the REST surface minimal and avoiding a button that
  invites rate-limit hammering.

## Rationale

Identity-scoped discovery is O(your work), not O(repos), which is what makes
large orgs feasible while keeping API usage low; snapshotting watched-only
notifications would quietly make it O(repos you watch). Polling-and-diff
preserves the token-only promise (no provider-side webhook configuration).

With the fast tier keeping known-open items fresh, the sweep's unique jobs are
new-item discovery and self-heal (deleted todos, watermark gaps, GitHub search
lag) — on a fine-grained GitHub PAT it is the *only* discovery path
(`docs/Token_Setup.md`) — so its interval is the worst-case latency for a new
item to appear at all. Rate budget does not bind: GitHub's fast tier costs
nothing on an unchanged poll and at most a page of PR fetches on a changed one,
GitLab's is <1% of its budget, and a 3-minute sweep adds single-digit percent
(`docs/Provider_API_Analysis.md`). The sweep stays
well above the fast cadence because the two-tier design only holds while the
full sweep runs meaningfully less often; driving it toward 60 s collapses the
tiers into constant full sweeps for a latency the fast tier already delivers.

## Consequences

- The engine is `internal/engine`; its full implementation is specified in
  `docs/Synchronization_Engine.md`, and the per-provider call sets, rate
  budgets, GitLab's open-set refresh, and sole-approver discovery in
  `docs/Provider_API_Analysis.md`.
- Cadences and the staleness threshold are engine constants (direct code), not
  configuration.
- Both tiers of a connection run on one goroutine, so provider-side caches (the
  open-item snapshots, sole-approver counts) need no lock.
- The full adaptive rate-budget controller is deferred to avoid gold-plating
  before real usage data exists (`docs/Roadmap.md`).
