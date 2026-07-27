# User-centric Synchronization

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

## Amendment — v0.1.5: sole-approver discovery scope (2026-07-14)

Reconcile now includes a fourth search scope for each provider that surfaces PRs
and MRs on repos where the authenticated user is the **only account with
merge-capable permission** — the sole-approver axis. These items are assigned the
`sole_approver` role (see ADR-0016 amendment for state mappings).

**Scope definition:**
- **GitHub**: `GET /search/issues?q=is:pr+is:open+user:<handle>` discovers open
  PRs on repos owned by the user. For each qualifying result, the provider calls
  `GET /repos/{owner}/{repo}/collaborators?affiliation=direct` to count accounts
  with `push`, `maintain`, or `admin` permission. Sole iff count == 1 and that
  account is the authenticated user.
- **GitLab**: `GET /projects?membership=true&min_access_level=40` lists projects
  where the user has Maintainer or higher access. For each project,
  `GET /projects/:path/members/all?min_access_level=40` counts members with
  `access_level >= 40`. Sole iff count == 1 and that member is the authenticated
  user.

**Guards (both providers):** draft PRs/MRs and self-authored PRs/MRs are excluded
from the sole-approver scope — own work is already covered by the `author` role.

**In-memory TTL cache:** collaborator/member counts are cached per repo for 15
minutes on the Provider struct (`approverCache map[string]approverEntry`). No lock
is required because FastPoll and Reconcile are serialised per connection.
**Opportunistic downgrade (GitHub only):** when `graphqlJoin` observes
`approvalsCount > myCount` for a PR, the repo is immediately written as
`isSole: false` in the cache — no collaborator probe needed on the next cycle.

**Architecture note:** providers cannot import `internal/storage`; the
`repo_approvers` DB table added in migration 3 is populated only by explicit
`UpsertRepoApprover` storage calls (future batch export), never from within a
provider. The in-memory cache is the authoritative hot path.
