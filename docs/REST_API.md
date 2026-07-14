# REST API & SSE

> **Status: Implemented.** All endpoints below, including the SSE stream, are
> served by `internal/api` and wired up in `cmd/devpit`. The response shapes in
> code (`internal/api`) are authoritative; this doc describes the surface.
> Decision: `ADR/ADR-0008_API_Design.md`.

Deliberately minimal. Buckets have no dedicated endpoints — they are
client-side filters over the single attention list
(`docs/Attention_Engine.md`). There is **no manual sync trigger**: polling is
automatic (`ADR/ADR-0004_User_Centric_Synchronization.md`), and connections are
config-file only and static at runtime (`ADR/ADR-0015_Multi_Account_Connections.md`),
so there are no create/delete endpoints.

## Endpoints

- `GET /attention` — the single ranked list; states as tags. `?state=` is
  optional client-side sugar.
- `GET /events` — the SSE stream (below).
- `GET /connections` — provider connections with health/identity, read-only.
- `GET /sync-log` — the user-facing sync/poll log; `?connection=` filters for
  banner deep-links.
- `PUT /items/{id}/flag` / `DELETE /items/{id}/flag` — set/clear the local
  "Handle next" flag (`ADR/ADR-0017_Read_Only_Action_Model.md`); return
  `204 No Content`.

Beyond these endpoints the same server also serves the embedded Svelte SPA
(`ADR/ADR-0010_Web_Frontend.md`): a catch-all static handler (`internal/web`,
`go:embed`) sits behind the API routes above and serves `index.html` for any
unmatched path so a browser refresh on any client route works.

## Conventions

- All responses are `application/json`; timestamps are RFC 3339 UTC strings.
- Item `id` is an opaque, stable, URL-safe string derived server-side from
  `(connection_id, object_type, native_id)`. Clients treat it as a black box
  and use it only for the `/flag` endpoints.
- Errors use a uniform shape (`{ "error": code, "message": text }`) with codes
  `not_found`, `bad_request`, `internal` mapping 1:1 to 404 / 400 / 500.

## `GET /attention`

The full ranked list. Pinned items (`flagged: true`) come first in flag order;
auto-ranked items follow, sorted by age band (fresh < stale < old) then by
most-recent update first (newest signal, else latest snapshot's provider-updated
time), with item ID as the stable tiebreak. Signal precedence and the
reviewed-done mute do not affect order (`docs/Attention_Engine.md`).

Each item carries:
- Connection provenance: `connection_id`, `connection_label`, `connection_type`.
- Item identity: `id`, `object_type`, `native_id`, `title`, `url`, `repo`,
  `author`, `draft`.
- `states` — array of provider signals in precedence order; `states[0]` is the
  leading chip (precedence orders chips, not item ranking). An authored MR is
  never bare: worst case `["checking"]` (gate
  `unknown`, including drafts). A non-authored involved item (assignee, etc.)
  with a known gate and no reviewer/mention signal may still have `states: []`
  (marker-only row). The array is never `null`. Nine wire values in precedence
  order: `changes_requested`, `review_requested`, `blocked`, `mentioned`,
  `ready_to_merge`, `auto_merge_armed`, `checks_running`, `checking`,
  `review_submitted`.
- `muted` — true when the item is reviewed-done for you (you are a reviewer, not
  the author or sole approver, and your review is submitted). Muted is a display cue only: the row
  renders de-emphasized and suppresses its signal chips, but muting does not
  affect ranking (the item sorts by age band + recency like any other). Omitted
  when false.
- `my_review_state` — your own review verdict when known: `approved`,
  `changes_requested`, or `reviewed` (comment-only). Omitted when empty/unknown.
  GitLab detects only `approved`. Drives `review_submitted`/`muted` and the
  "you + N approved" meta-row.
- `my_roles` — your roles on the item, any of `author`, `reviewer`, `assignee`,
  `sole_approver`.
  Omitted when empty. A faithful projection of the provider fact; the client uses
  it to fold reviewer items into the `mentioned` filter even before you've
  reviewed (when `my_review_state` is still empty).
- `flagged` — true when in the "Handle next" zone.
- `flagged_at` — RFC 3339 timestamp when the item was pinned; present only when
  `flagged: true`.
- `stale` — true when idle more than 7 days (and not old).
- `old` — true when idle more than 30 days; mutually exclusive with stale.
- `updated_at` — the item's ranking timestamp (newest signal or provider time).
- `signal_counts` — present only when a signal type has count > 1 (drives "×N"
  tags).
- **Markers** (diagnostic booleans, never affect state):
  - `draft` — item is in draft/WIP mode (pre-existing; also listed under item
    identity above).
  - `failing_checks` — CI/checks red (GitHub: `unstable`; GitLab: `headPipeline.status` red via GraphQL).
  - `merge_conflict` — manual conflict resolution needed (GitHub: `dirty`;
    GitLab: `has_conflicts` REST field).
  - `needs_rebase` — mechanical rebase needed (GitHub: `behind`; GitLab:
    `shouldBeRebased` GraphQL).
  - `needs_approval` — required approvals not met (GitHub: `reviewDecision`; GitLab: `approved` GraphQL).
  - `unresolved_discussions` — threads block the merge (GitLab: `blocking_discussions_resolved`; GitHub: excluded — gate rule unreadable for non-admins).
  - `policy_denied` — security/org policy denies merge (GitLab: `policies_denied` / `security_policy_violations`; GitHub: no signal).
  - `auto_merge_armed` — auto-merge is queued (GitHub: `autoMergeRequest`; GitLab: `merge_when_pipeline_succeeds`). Surfaces as the `auto_merge_armed` signal for authored MRs.
  - `checks_running` — a gating pipeline is in progress (GitLab only: `headPipeline.status` running; GitHub ✗ — gating pipeline hidden inside `blocked`). Surfaces as the `checks_running` signal for authored MRs.
  - `gate_detail` — raw provider vocabulary for the merge gate (omitted when
    empty); powers the Blocked tooltip.
- `since` — map from tag wire name to RFC 3339 onset time; only active tags
  appear. Onset = start of latest contiguous run of snapshots where the
  condition holds. `mentioned` onset = earliest mention signal time.
- `labels` — optional; the provider label names the item carries (GitLab MR
  labels, GitHub PR labels). Array of strings. Refreshed on reconcile only. The
  UI renders these as plain text on a dedicated row — provider metadata, distinct
  from the outline signal chips (ADR-0016).
- `jira` — optional; present when the item's first recognized Jira ticket key
  has a cached row with a non-empty status. Shape:
  `{ key: string, status: string, url: string }`.
  Omitted when no Jira config is present, no ticket key was found, or the ticket
  hasn't been fetched yet. The UI renders `[<status>]` as a link prefix on the
  title (ADR-0022).

## `GET /connections`

Each connection reports id, type, base_url, label (never empty — config
defaults it to the id), resolved `identity` (`null`
while pending/failed), and a `health` object: `status`
(`ok` | `degraded` | `failing`), `last_synced_at`, `failure_count`, and the
fixed `failure_window_minutes` (60). The token is never returned. Drives the
health dot (`ADR/ADR-0018_Sync_Observability.md`).

## `GET /sync-log`

One entry per poll cycle: `connection_id`, denormalized `connection_label` (so
the log survives a connection's removal), `ts`, `operation`
(`fast_poll` | `reconcile`), `outcome` (the set in
`docs/Synchronization_Engine.md`), `items_changed`, `rate_remaining`,
`retries`, `next_retry`, and a plain-language `error`. No pagination — the table
is user-bounded. Per-call detail rows are **Deferred**, so v0.1 returns cycle
summaries only.

## SSE stream (`GET /events`)

One-directional (server → client); client actions use REST. The event set is
deliberately coarse — events say *that* something changed and the client
re-fetches, rather than patching state from payloads:

- `attention.changed` — the ranked list changed; client re-fetches `/attention`.
- `sync.completed` — a poll cycle finished for a connection; feeds the health
  indicator and the live sync-log view.
- `sync.failed` — a poll cycle failed; drives the non-blocking failure banner.

Fine-grained domain events (`review.requested`, `mention.created`, …) may be
added later for notification/toast features.
