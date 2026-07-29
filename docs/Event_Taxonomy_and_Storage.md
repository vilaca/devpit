# Event Taxonomy & Storage

> **Status:** Implemented. The event model is realized as `sdk.Event` and the
> payload structs in `sdk/provider.go`; the SQLite schema is in
> `internal/storage`. This spec is the design behind them — it does not restate
> the DDL or the struct fields, which are authoritative in code. Decisions:
> `ADR/ADR-0005_Event_Based_Attention_Engine.md`,
> `ADR/ADR-0006_Normalized_Data_Model.md`.

Storage is events-first: the primary table is the event/signal log. There is no
materialized current-state table in v0.1 — object facts and attention states
are folded from events on read (`docs/Attention_Engine.md`). Normalized,
provider-neutral entities exist only as the fold's output; they are not stored
rows.

## Event model

Every event belongs to one provider object, identified by the WorkItem key
`(connection_id, object_type, native_id)`:

- **connection_id** — opaque id of a connection in the config file. The DB
  never stores connection details; rows whose id is unknown to the config are
  orphans eligible for purge.
- **object_type** — `merge_request` (covers GitHub PRs) or `issue`.
- **native_id** — plugin-defined, stable, human-debuggable (GitHub
  `owner/repo#number`; GitLab `group/project!iid`).

Two event streams share one log:

**Fact stream** — encodes object state for the fold:

| Type | Meaning |
|---|---|
| `item.observed` | Full normalized fact set. Appended only when the fact set differs from the item's previous snapshot — the poll diff *is* the change detector. The fold reads the latest snapshot per item. |
| `item.removed` | The reconciliation sweep no longer sees the item and no closing state was observed (lost access, repo deleted). The fold drops the item. |

**Signal stream** — discrete "aimed at you" occurrences, stored raw and never
collapsed. They feed the Mentioned bucket, the "×N" tag counts, the item's
ranking timestamp, and "what's new since last visit":

| Type | Synthesized from |
|---|---|
| `signal.mentioned` | GitHub `mentions:@me` / notification `mention`; GitLab todos `mentioned`, `directly_addressed` |
| `signal.review_requested` | entering the review-requested result set; GitLab todo `review_requested` |
| `signal.review_submitted` | review-decision / reviewer-state transitions; GitLab todo `review_submitted` |
| `signal.assigned` | assigned result set / todos `assigned` |
| `signal.ci_failed` | red checks on authored PRs (incl. non-gating); GitLab todo `build_failed` |

Merged/closed are not signals — they arrive as an `item.observed` state change
and the fold drops the item. New signal types extend the taxonomy without
schema changes (the payload is JSON). The concrete payload shapes are the
structs in `sdk/provider.go`.

### Diff fidelity

Activity between two polls collapses into one diff: one new snapshot, and one
signal per *detected transition* (not per underlying provider action). Bucket
membership always derives from the latest facts, so missed intermediate signals
never produce wrong buckets — only counts and timestamps are affected.

## The fact set

The `item.observed` payload is a provider-neutral fact set produced by the
plugin's normalizer (the struct is `sdk.ItemObservedPayload`). Design notes on
its fields:

- **`gate`** (normalized `ready | blocked | unknown`) uses the per-provider
  merge-gate mappings in `docs/Provider_API_Analysis.md`. **Transient gate
  values never reach storage**: the synthesizer carries the previous known gate
  forward, so a mid-computation read causes neither churn snapshots nor bucket
  flapping.
- **Unknown/ungranted facts** (capability gaps) are omitted; the fold treats
  absent as "cannot say", never as false.
- **Marker fields** — diagnostic booleans that explain why an item is in a given
  state but never change the state itself:
  - `draft` — item is in draft / WIP mode (pre-existing since v0.1; providers
    set this from the native draft flag).
  - `failing_checks` (v0.1.1) — CI/checks red (GitHub: `mergeable_state ==
    "unstable"`; GitLab: `headPipeline.status` red via the GraphQL join — any
    pipeline, extended from `ci_must_pass` in v0.1.2). Previously this also
    included `"dirty"` (narrowed in v0.1.1).
  - `merge_conflict` (v0.1.1) — manual conflict resolution needed (GitHub:
    `mergeable_state == "dirty"`; GitLab: `has_conflicts` REST field).
  - `needs_rebase` (v0.1.1) — mechanical rebase / update-branch needed (GitHub:
    `mergeable_state == "behind"`; GitLab: `shouldBeRebased` via the GraphQL join).
  - `needs_approval` (v0.1.2) — required approvals not met (GitHub:
    `reviewDecision`; GitLab: `approved` — both via the GraphQL join).
  - `unresolved_discussions` (v0.1.2) — unresolved threads gate the merge
    (GitLab: `blocking_discussions_resolved` REST; GitHub excluded — gate rule
    unreadable for non-admins). Set only when the gate is `blocked`.
  - `policy_denied` (v0.1.2) — security/org policy denies merge (GitLab:
    `policies_denied` / `security_policy_violations` on `detailed_merge_status`;
    GitHub: no signal).
  - `gate_detail` (v0.1.1) — raw provider vocabulary for the merge gate (opaque
    string; powers the Blocked tooltip).
  - `auto_merge_armed` (v0.1.5) — provider's auto-merge / merge-when-pipeline-succeeds
    is set. Stored as a boolean; read by the fold as the `auto_merge_armed` signal
    (GitHub: GraphQL `autoMergeRequest{enabledAt}`, non-null ⇒ armed; GitLab:
    REST `merge_when_pipeline_succeeds`).
  - `checks_running` (v0.1.5) — a pipeline is in progress. Stored as a boolean;
    read by the fold as the `checks_running` signal (GitLab: `headPipeline.status`
    in the running set via GraphQL; GitHub: not set — documented ✗ gap, hidden
    inside `blocked`).
  Old `item.observed` events lack the v0.1.1/v0.1.2/v0.1.5 fields (unmarshal to
  `false`/`""`); the fold reads the latest snapshot, so items pick them up on
  the next poll cycle.

The fold rules that turn this fact set into buckets live in
`docs/Attention_Engine.md`.

## Storage schema

WAL mode, single writer, read-only reader pool (`ADR/ADR-0007_Storage.md`). All
facts beyond the indexed keys live in JSON payloads, so taxonomy changes need no
migration. The tables (see `internal/storage/schema.go` for the DDL):

- **`events`** — the log. Indexed keys (`connection_id`, `object_type`,
  `native_id`, `event_type`) plus timestamps, actor, a dedupe key, and a JSON
  payload. A `UNIQUE` constraint over the key columns plus the dedupe key makes
  overlapping polls idempotent via `INSERT OR IGNORE`. An autoincrement `id`
  gives insertion order, which drives "what's new since last visit".
- **`handle_next`** — the local-only "Handle next" pins, keyed by the item's
  opaque id, ordered by flag time (`ADR/ADR-0017_Read_Only_Action_Model.md`).
- **`sync_log`** — one row per poll cycle; bounded by user cleanup plus an
  optional cap (`ADR/ADR-0018_Sync_Observability.md`).
- **`sync_cursors`** — operational poll state (watermarks, last-modified
  tokens), opaque to the engine and owned by each provider.
- **`schema_version`** — migration bookkeeping.

The `app_state` KV and its `last_seen_event_id` key (backing "what's new since
last visit") are **Deferred** — they arrive when that feature is built, an API
concern.

## Dedupe keys

Overlapping polls (watermark overlap, the reconcile sweep re-seeing everything)
must not duplicate events; the `UNIQUE` constraint enforces this and writers use
`INSERT OR IGNORE`:

- `item.observed`: a hash of the canonical fact-set JSON — doubles as the "did
  anything change?" check against the previous snapshot.
- `item.removed`: a constant — at most one live removal; reappearance resumes
  via new snapshots.
- Signals with a provider-native identity (todo/review/comment id): that id,
  prefixed with its kind.
- Signals synthesized from a diff (no native id): a plugin-defined transition
  fingerprint (e.g. review-requested + provider-updated timestamp) — a
  re-request after a completed review yields a new fingerprint; a re-poll of the
  same state does not.

## Retention

Retention in the first public release is **maintainer-operated**, not a
user-facing feature (`ADR/ADR-0023_Packaging_Distribution_and_Release_Pipeline.md`).
Three raw-SQL scripts under `scripts/`, run with the instance stopped and
requiring the `sqlite3` CLI, apply these rules:

- `scripts/db-trim.sh` — a retention pass: per item, delete superseded snapshots
  (all but the latest) and signals older than a cutoff, but **never** the latest
  snapshot of a still-open item; purge items whose latest state is
  merged/closed/removed, and rows whose `connection_id` no longer exists in the
  config; bound `sync_log`.
- `scripts/db-cleanup.sh <connection-id>` — purge every row for one source.
- `scripts/db-reset.sh` — empty every table.

The scripts are named here with their contract; their internals live with the
scripts themselves (one home per fact). Binary-shipped, user-facing "clear
history older than X" retention stays **deferred to v0.2** (`docs/Roadmap.md`);
brew/Docker users have no trim story this release and rely on the maintainer
scripts or the disposable Docker DB (ADR-0023).
