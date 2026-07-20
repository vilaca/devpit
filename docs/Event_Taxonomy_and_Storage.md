# Event Taxonomy & Storage Schema

Resolves the Design_Decisions open question: the column-level storage
schema and how object facts are encoded as events for the fold (§2, §3).
Scope is v0.1. Decisions recorded here: facts are encoded as **snapshot
events**, payloads are **JSON with indexed key columns**, and connection
config lives **in the config file only** (the DB stores opaque
connection-id strings).

## 1. Event model

Every event belongs to one provider object, identified by the WorkItem
key `(connection_id, object_type, native_id)` (§3):

- `connection_id` — opaque id of a connection in the config file (§1b).
  The DB never stores connection details; if the id is unknown to the
  config, its rows are orphans eligible for purge.
- `object_type` — `merge_request` (covers GitHub PRs) or `issue`.
- `native_id` — plugin-defined, stable, human-debuggable. GitHub:
  `owner/repo#number`; GitLab: `group/project!iid`.

Two event streams share one log:

**Fact stream** — encodes object state for the fold:

| Type            | Meaning                                                                                                                                                                                                    |
|-----------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `item.observed` | Full normalized fact set (§2). Appended only when the fact set differs from the item's previous snapshot — the poll diff *is* the change detector. The fold reads the **latest** snapshot per item; earlier snapshots are history. |
| `item.removed`  | The reconciliation sweep no longer sees the item and no closing state was observed (lost access, repo deleted). Fold drops the item.                                                                        |

**Signal stream** — discrete "aimed at you" occurrences, stored raw and
never collapsed (§3). They feed the Mentioned bucket, the "×N" tag
counts (§6), the item's ranking timestamp, and "what's new since last
visit":

| Type                       | Payload highlights                                                | Synthesized from                                                                                          |
|----------------------------|-------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------|
| `signal.mentioned`         | direct vs team/group mention                                      | GitHub `mentions:@me` search / notification `mention`; GitLab todos `mentioned`, `directly_addressed`    |
| `signal.review_requested`  | direct vs team request                                            | entering the review-requested result set; GitLab todo `review_requested`                                  |
| `signal.review_submitted`  | `verdict: approved \| changes_requested \| commented`, reviewer   | `reviewDecision` / reviewer-state transitions; GitLab todo `review_submitted`                             |
| `signal.assigned`          | assigner if known                                                 | assigned result set / todos `assigned`                                                                    |
| `signal.ci_failed`         | check/pipeline name if known                                      | red checks on authored PRs (incl. non-gating, per §4); GitLab todo `build_failed`                        |

Seven types total. Merged/closed are not signals — they arrive as an
`item.observed` state change and the fold drops the item (§11: items
vanish only when the condition clears). Fine-grained notification
events remain a later option (§13); new types extend the taxonomy
without schema changes.

### Diff fidelity

Per ADR-0005, activity between two polls collapses into one diff: one
new snapshot, and one signal per *detected transition* (not per
underlying provider action). Signal exactness affects counts and
timestamps only — bucket membership always derives from the latest
facts, so missed intermediate signals never produce wrong buckets.

## 2. The fact set (`item.observed` payload)

Provider-neutral, produced by the plugin's normalizer:

```json
{
  "title": "Fix flaky sync test",
  "url": "https://github.com/acme/api/pull/412",
  "repo": "acme/api",
  "state": "open",                  // open | merged | closed
  "draft": false,
  "author": "jdoe",
  "my_roles": ["reviewer"],         // author | reviewer | assignee | mentioned
  "review_decision": "changes_requested", // approved | changes_requested | pending | none
  "my_review_state": "reviewed",    // requested | reviewed | approved | changes_requested | none
  "gate": "blocked",                // ready | blocked | unknown  (normalized class)
  "gate_detail": "BLOCKED",         // provider-raw value, for the detail view
  "failing_checks": true,           // any red check, gating or not (§4)
  "provider_updated_at": "2026-07-08T09:14:02Z"
}
```

- `gate` uses the merge-gate mappings in Provider_API_Analysis.md.
  **Transient gate values (`UNKNOWN`, `unchecked`, `checking`, …) never
  reach storage:** the synthesizer carries the previous known `gate`
  forward, so transient reads cause neither churn snapshots nor bucket
  flapping.
- Unknown/ungranted facts (capability gaps, §10) are omitted; the fold
  treats absent as "cannot say", never as false.

## 3. Fold rules (buckets from events)

Computed at read time — no materialized state (§2). For each item:
latest `item.observed` gives the facts; `state == open`, no
`item.removed`, else the item is dropped.

| Bucket (§4)        | Condition on latest facts                                                              |
|--------------------|----------------------------------------------------------------------------------------|
| Needs Review       | `my_roles` has `reviewer` ∧ `my_review_state == requested`                             |
| Changes Requested  | `my_roles` has `author` ∧ `review_decision == changes_requested`                       |
| Blocked            | `my_roles` has `author` ∧ ¬`draft` ∧ `gate == blocked`                                |
| Ready to Merge     | `my_roles` has `author` ∧ ¬`draft` ∧ `gate == ready`                                  |
| Mentioned          | ≥ 1 `signal.mentioned` event (item open)                                               |
| Waiting on Author  | `my_roles` has `reviewer` ∧ `my_review_state ∈ {reviewed, approved, changes_requested}` |

- **Ranking timestamp** (§6): newest `coalesce(occurred_at,
  observed_at)` across the item's signal events, else the latest
  snapshot's `provider_updated_at`. Stale badge when older than the
  threshold (§6).
- **Tag counts**: same-type signals per item → "Mentioned ×3";
  individual signals appear in the detail view.
- **What's new since last visit**: events with `id >` the stored
  `last_seen_event_id` (see `app_state`); the client advances the mark
  on visit.
- `failing_checks` renders the failure notification marker (§4), never
  a bucket.

## 4. Storage schema (SQLite)

WAL mode, single sync writer, REST readers (§14). Timestamps are RFC
3339 UTC strings. All facts beyond the indexed keys live in JSON
payloads — taxonomy changes need no migration.

```sql
CREATE TABLE events (
  id            INTEGER PRIMARY KEY,   -- insertion order; drives "new since last visit"
  connection_id TEXT    NOT NULL,
  object_type   TEXT    NOT NULL,      -- 'merge_request' | 'issue'
  native_id     TEXT    NOT NULL,
  event_type    TEXT    NOT NULL,      -- taxonomy §1
  occurred_at   TEXT,                  -- provider-reported time, when known
  observed_at   TEXT    NOT NULL,      -- when the poll saw it
  actor         TEXT,                  -- provider handle, when known
  dedupe_key    TEXT    NOT NULL,      -- §5
  payload       TEXT    NOT NULL DEFAULT '{}',  -- JSON, shape per event_type
  UNIQUE (connection_id, object_type, native_id, event_type, dedupe_key)
);
CREATE INDEX events_by_item ON events
  (connection_id, object_type, native_id, id);
```

The fold scans open items' events via `events_by_item`; at single-user
scale this is fast without further indexes (§2), and compaction remains
a later optimization.

```sql
CREATE TABLE flags (                   -- "Handle next" (§5, §11), local-only
  connection_id TEXT NOT NULL,
  object_type   TEXT NOT NULL,
  native_id     TEXT NOT NULL,
  flagged_at    TEXT NOT NULL,         -- pin order in the pinned zone
  PRIMARY KEY (connection_id, object_type, native_id)
);

CREATE TABLE sync_log (                -- §16; cycle rows + per-call detail
  id             INTEGER PRIMARY KEY,
  parent_id      INTEGER REFERENCES sync_log(id), -- NULL = cycle summary row;
                                                  -- set = per-call detail under a failed cycle
  connection_id  TEXT    NOT NULL,
  ts             TEXT    NOT NULL,
  operation      TEXT    NOT NULL,     -- cycle rows: 'fast_poll' | 'reconcile';
                                       -- call rows: endpoint label
  outcome        TEXT    NOT NULL,     -- 'ok' | 'auth' | 'rate_limited' | 'network' | 'parse'
  http_status    INTEGER,
  items_changed  INTEGER,
  rate_remaining INTEGER,
  retries        INTEGER,
  next_retry     TEXT,
  error          TEXT                  -- plain-language cause
);
CREATE INDEX sync_log_by_conn ON sync_log (connection_id, ts);

CREATE TABLE sync_cursors (            -- operational poll state, not events
  connection_id TEXT NOT NULL,
  cursor        TEXT NOT NULL,         -- e.g. 'mr_updated_after', 'notifications_last_modified'
  value         TEXT NOT NULL,
  PRIMARY KEY (connection_id, cursor)
);

CREATE TABLE app_state (               -- tiny KV: 'schema_version', 'last_seen_event_id'
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
```

Successful cycles write one row (`parent_id NULL`, no children). The
§12 rolling failure count is `COUNT(*)` over cycle rows with
`outcome != 'ok'` in the window.

## 5. Dedupe keys

Overlapping polls (watermark overlap, reconciliation sweep re-seeing
everything) must not duplicate events; the `UNIQUE` constraint enforces
this, writers use `INSERT OR IGNORE`.

- `item.observed`: hash of the canonical (key-sorted) fact-set JSON.
  Doubles as the "did anything change?" check against the previous
  snapshot.
- `item.removed`: constant `"removed"` — at most one live removal;
  reappearance resumes via new snapshots.
- Signals with a provider-native identity (todo id, review id, comment
  id): that id, prefixed with its kind (`todo:18442`).
- Signals synthesized from a diff (no native id, e.g. review request
  detected by entering a search result set): a plugin-defined
  transition fingerprint, e.g.
  `review_requested:{provider_updated_at}` — a re-request after a
  completed review produces a new fingerprint, a re-poll of the same
  state does not.

## 6. Retention & deletion (§2)

No automatic compaction. User-initiated "clear history older than X"
deletes, per item:

- signal events older than X, and
- superseded `item.observed` snapshots (all but the latest),

but **never** the latest snapshot of a still-open item — open items
must survive any cleanup. Items whose latest state is
merged/closed/removed and older than X are purged entirely, as are
rows whose `connection_id` no longer exists in the config. `sync_log`
is bounded by the same cleanup plus an optional row cap (§16).

## Effects on existing docs

- Design_Decisions.md: closes the "detailed storage schema / fact
  encoding" open question.
- Data_Model.md: the normalized entities are realized as the §2 fact
  set plus signal events; points here.
