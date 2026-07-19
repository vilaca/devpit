# Design Decisions

Decisions captured during design review. These refine and, where noted,
supersede statements in the ADRs and other docs. Status: agreed direction,
pre-implementation.

## 1. Users & deployment

- **Single-user core.** Each user runs their own instance with their own
  SQLite store and their own client. Localhost, no auth, no webhooks.
- **Team visibility = own-token observation.** A user (typically a lead)
  can watch other people/teams using their *own* token. Results live in a
  separate `[Me] / [Team]` scope, never mixed into the personal list.
  Post-v0.1. Sees provider-visible facts only (not a teammate's private
  mentions/notifications). Cost scales N× with watched entities, so it must
  be throttle-aware.
- **Team view focuses on the work, not the notifications.** It should center
  on **open / in-progress MRs and their events** (review state, CI, activity,
  staleness) — the shared state of the team's work — rather than reconstruct
  "what each teammate is being notified of". This is both the right framing
  for a lead ("where does the team's work stand / what's stalled") and the
  only thing own-token observation can actually see (a teammate's personal
  notification feed is private and out of reach).
- **Federation is an uncommitted "future maybe".** A push-based aggregation
  layer (each instance pushes a summary to a shared store) could later enrich
  the Team view with teammates' *private* signals (that the observer's token
  can't see), opt-in per user. Team visibility no longer depends on it, so it
  is neither roadmapped nor committed — documented only so the idea isn't
  lost; revisit if users ask.

## 1b. Multiple accounts per provider

- **Config is a list of independent, named connections.** Each connection
  has: type (github/gitlab/...), base URL, token, auto-detected identity, and
  a user label ("Personal", "Acme"). The same type and even the same host may
  appear multiple times, keyed by an internal connection id — supporting
  e.g. a personal and an org github.com account side by side.
- **Everything is per-connection:** discovery, identity ("me"), rate budget,
  and health are scoped to each connection independently.
- **WorkItem key includes the connection** (see §3): the same object seen via
  two connections yields two rows. Provenance is always clear and each
  account keeps its own rate/health. Identity-scoped discovery makes genuine
  cross-account duplicates rare (a signal targets one handle → one
  connection), so this is a small accepted edge, not the common case.

## 2. Attention engine

- **Event-based (per ADR-0005), fed by polling.** DevPit is polling-first,
  so events are *synthesized by diffing each polled snapshot against stored
  state*. There are no webhooks. See updated ADR-0005.
- **Persisted event log, fold-on-read.** Diffing appends events to a durable
  log (SQLite); attention state and object facts are the fold over that log,
  computed at read time (pure event-sourcing — no separate materialized
  state table). At single-user scale the log is small and folding is fast.
  Powers "what's new since last visit". The log is internal — invisible to
  the user, adds no setup steps.
- **No automatic compaction in v0.1.** Snapshotting/compaction is a *later*
  optimization if a real instance proves it necessary (per-object rolling
  snapshot is the sketched approach). Log growth is instead managed by
  **optional, user-initiated deletion** ("clear history" / delete events
  older than X) — the user chooses to trade history for size.

## 3. Data model

- **WorkItem is a derived logical grouping, not a stored primary key.**
- **Store all signals raw** — never collapse at ingestion (this *is* the
  event log). Every mention / review-request / CI event is stored with its
  own event identity, including signal type.
- **Fold in the logic layer:** group events by parent provider object
  (a PR/MR/Issue) into a derived WorkItem plus its state tags.
- **Dedup in the rendering layer:** one row per WorkItem.
- **WorkItem key includes the connection** — `(connection-id, object-type,
  native-id)`. Items never merge across providers, across hosts, or across
  connections; each connection's view of an object is its own row (see §1b).
- **Storage is events-first:** the primary table is the event/signal log.
  There is no separate materialized current-state table in v0.1 (see §2);
  object facts and attention states are folded from events on read.
  Local-only tables: provider config (url, plaintext token per §15, resolved
  identity) and "Handle next" flags (§5).
- **Event taxonomy, fold rules, and column-level schema:** see
  docs/Event_Taxonomy_and_Storage.md.

## 4. Attention states (buckets)

- **v0.1 set:** Needs Review, Blocked, Ready to Merge, Mentioned,
  Changes Requested, Waiting on Author.
- **Needs Backport deferred** — it requires label/branch heuristics and
  per-repo config, which conflicts with the token-only ethos. Later version.
- A WorkItem can carry **multiple states simultaneously**, shown as tags.

### Blocked — edge definitions

- **Scope:** PRs *you authored*, non-draft. A PR you are *reviewing* with red
  CI is the author's problem, not your Blocked.
- **Blocked = the provider's merge-gate reports the PR not mergeable /
  blocked.** This defers to each organization's rules (required checks,
  required approvals, conflicts) rather than DevPit re-deriving them.
  "No approvals" is Blocked *only* where the org requires approvals (the gate
  reflects it); it is not Blocked where approvals aren't required, so
  just-opened PRs awaiting review are not marked Blocked by default.
- **Non-gating check failures are a failure *notification*, not Blocked.**
  Any red check is surfaced so nothing is missed, but only merge-gating
  failures put the PR in the Blocked bucket — keeping Blocked trustworthy.
- **Drafts** are never Blocked and never Ready to Merge (their unmergeable
  state is expected); they still surface for Mentioned and explicit review
  requests. Normal rules resume once marked ready.

### Ready to Merge

- **= the provider's merge-gate reports the PR mergeable**, non-draft,
  authored by you. Symmetric with Blocked.

### Changes Requested / Waiting on Author — the two sides of a review round-trip

Splits the earlier single "Waiting on Author" state, which conflated two
perspectives with opposite actionability:

- **Changes Requested** (author side): PRs *you authored* where a reviewer
  requested changes — the ball is in your court, high precedence. Where the
  org's merge gate enforces approvals this co-occurs with Blocked; the item
  carries both tags and ranks by the more actionable Changes Requested.
- **Waiting on Author** (reviewer side): PRs *you are reviewing* where your
  review is done and the ball is back with the author — not your turn.
  Informational, lowest precedence; the stale badge (§6) is the safety net
  for round-trips the author has forgotten.

## 5. Presentation

- **Single ranked list**, one row per item, states shown as tags.
- **Buckets are optional filters**, not the primary layout.
- **Pinned "Handle next" zone** at the top: user-flagged items, in flag
  order, above the auto-ranked list. Flagged items are lifted out of the
  auto list (not shown twice). The flag is local-only, never written back.

## 6. Ranking

- **Fixed state precedence + age tiebreak. No numeric score, no config.**
- **Precedence (highest first):**
  `Ready to Merge → Needs Review → Changes Requested → Blocked → Mentioned →
  Waiting on Author`.
- **Within a bucket: newest-first** (item timestamp = its newest signal),
  plus a **"stale" badge** once an item's age exceeds a threshold (value
  TBD) as the anti-rot safety net. (Supersedes the earlier "oldest-first".)
- **Repeated same-type signals** collapse to one tag with a count
  (e.g. "Mentioned ×3"); the individual signals remain visible in the detail
  view.

## 7. Provider setup

- **Token-only ethos.** No webhooks, no callback URLs, no provider-side
  config. A token (and, for self-hosted, a base URL) is enough.
- **Connect = base URL + token.** URL pre-filled for github.com / gitlab.com.
- **Identity auto-detected** via the provider's `/user` endpoint, with a
  **required manual fallback** when the token/provider can't resolve a usable
  handle (e.g. GitLab project/group access tokens, deploy tokens, bots).

## 8. Discovery

- **Notifications / todos feed as a cheap change-signal**, plus
  **identity-scoped queries** (review-requested, assigned, authored,
  involves-me) for the actionable states.
- The exact call set is per-provider: see **docs/Provider_API_Analysis.md**.
  Notably, on GitHub the notifications feed requires a classic PAT, so
  search-based polling is the baseline there and notifications are an
  optional fast-signal. Identity-scoped discovery is O(your work), not
  O(repos), which is what makes large orgs feasible (per ADR-0004).

## 9. Synchronization

- **Tiered polling:** frequent lightweight poll (notifications/todos +
  conditional requests where the provider supports them, fetch detail only
  for changed items) + a slow full identity-scoped reconciliation sweep.
  Conditional ETag/If-Modified-Since requests are GitHub-only; GitLab's
  public API has no 304 support, so it uses `updated_after` watermark
  polling (see docs/Provider_API_Analysis.md).
- **v0.1:** fixed slow cadence + honor `Retry-After` / 429 (basic backoff).
- **Later:** full adaptive rate-budget controller (target a fraction of the
  rate limit, speed up/slow down dynamically). On-mission per the Sync doc,
  but deferred to avoid gold-plating before real usage data exists.
- **No manual sync trigger.** Polling is automatic; the API exposes no
  `POST /sync`. Keeps the REST surface minimal and avoids a button that
  invites hammering the rate limit.

## 10. Provider capability gaps

- **Plugins declare their capabilities.** Buckets a provider can't feed
  simply produce no items from it.
- **Scoped "unsupported" markers:** a small capability badge on the affected
  provider/item and a line in provider settings. Never an "unsupported"
  bucket, never a marker on every row.

## 11. Actions (read-only by default)

- **Click = deep-link to the item on the provider.** All real actions happen
  there.
- **No snooze / dismiss / hide.** Items vanish only when the underlying
  condition clears — DevPit never hides your truth.
- **Local "Handle next" flag** (SQLite, never written to the provider) is the
  only user-applied state; it drives the pinned zone (§5).

## 12. Failure & empty-state UX

- **Per-provider health.** Each provider shows a health dot + "last synced
  Xm ago" + a **rolling failure count over a fixed recent window** (default
  **60 minutes**, e.g. "GitLab ⚠ 3 failures / 60m"). The window is a fixed
  constant, not user-configurable (keep-it-simple); derived from the sync log
  (§16). The count is the glanceable teaser that invites opening the log.
- **Graceful degradation.** On failure (token expired, provider down,
  rate-limited), keep showing the last good data marked **stale**, plus a
  **non-blocking banner** naming the provider and the cause. One provider
  failing never blanks the others.
- **Never conflate empty with failed.** An empty list means genuinely clear
  and says so explicitly ("All clear — synced 1m ago"). A failed/stale
  provider is always visibly distinct from "nothing to do".
- **The health indicator is the entry point to the sync log (§16):** clicking
  it opens the detailed poll log; the failure banner deep-links into it,
  pre-filtered to the failing connection.

## 13. Frontend & live-update transport

- **Live updates = Server-Sent Events (SSE), not WebSocket.** Updates are
  one-directional (server → client: "your list changed"); client actions go
  via REST. SSE is simpler, rides plain HTTP, and auto-reconnects. Revises
  **ADR-0008** (REST + WebSocket → REST + SSE); `SSE_Events.md` replaces the
  old WebSocket events doc.
- **Coarse event set:** `attention.changed`, `sync.completed`, `sync.failed`.
  Events tell the client *that* something changed; it re-fetches rather than
  patching state from event payloads. Fine-grained domain events
  (review.requested, ...) are a later option for notification features.
- **Frontend = lightweight SPA (Svelte).** Subscribes to the SSE stream and
  patches the DOM in place (no page refresh); keyboard shortcuts handled
  client-side. Built separately and embedded as static assets via `go:embed`
  (satisfies ADR-0010). Accepted cost: a JS build step in the repo.

## 14. Concurrency & database access

- **The polling writer must never block GUI reads.** The GUI must be able to
  refresh at any time, even mid-sync.
- **SQLite in WAL (Write-Ahead Logging) mode.** Readers proceed against the
  last committed snapshot while the sync writer appends events — non-blocking
  *and* consistent. This delivers "reads never wait" without true dirty reads
  (avoiding half-written/torn-row anomalies).
- **Single writer, many readers.** The sync process is the sole writer; REST
  handlers are readers. Writes use **short, batched transactions** so the
  committed snapshot advances promptly and readers never lag far behind.
- Noted on ADR-0007 (Storage).

## 15. Secret storage

- **Tokens are stored in plaintext in the config file.** No encryption at
  rest — deliberate simplicity for a personal, single-user, self-hosted tool
  where the user owns the machine.
- **Trade-off (accepted):** anyone who can read the config file, a backup, or
  the host has the tokens.
- **Mitigations (no added complexity):** restrictive file permissions (0600)
  with a startup warning if the file is more permissive; least-privilege /
  read-only token scopes so a leaked token cannot write.
- Corrects the Security doc, which previously claimed "encrypted secret
  storage".

## 16. Sync / poll log (user-facing)

A user-facing view of the polling activity — so a user can see sync working
and understand exactly how/where/when it failed. Serves debugging *and*
trust: a visible, healthy sync log makes an empty attention list believable.

- **Persisted bounded table.** `sync_log(ts, connection, operation, outcome,
  http_status, items_changed, rate_remaining, retries, next_retry, error)`.
  Survives restart, so "how long has it been failing / down since when" is
  answerable. Bounded by the same user-initiated cleanup as events (§2) plus
  an optional cap.
- **Granularity: cycle summary by default, per-call detail on failure.** One
  human-readable row per poll cycle per connection (e.g. "GitHub Personal —
  OK, 3 changed, 2m ago"). Successful calls are not itemized (no noise). On
  failure/retry, the individual calls, HTTP statuses, retry attempts, and
  next-retry time are captured under that row and shown on expand.
- **Human-readable.** Relative + absolute timestamps, plain-language error
  causes (auth / rate-limited / network / parse), retry counts + next-retry,
  items changed, rate-limit remaining. Per connection.
- **Placement: hidden until asked for, one click away** (progressive
  disclosure per UI_Principles). Reached from the per-provider health
  indicator (§12) / a "Sync activity" entry; it never appears in the main
  attention view. The §12 failure banner deep-links into it, pre-filtered to
  the failing connection.
- **Progressive-disclosure ladder:**
  1. Health dot colour — instant per-provider status.
  2. Rolling failure count over a fixed 60m window (§12) — glanceable
     severity, computed from this log.
  3. Click → the sync log: per-cycle summary rows.
  4. Expand a failed row → per-call detail, retries, next-retry, cause.
- **Live-updating** via the SSE stream (the existing `sync.completed` event;
  §13).

## Open questions (not yet decided)

- Staleness-badge threshold value (§6) — recommended default: 7 days.
- REST response shapes: see docs/REST_API.md (settled).
- Observability and auth if exposed beyond localhost (stubs).
