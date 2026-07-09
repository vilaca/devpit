# Event-based Attention Engine

## Status

Accepted

## Scope

Event log and read-time fold **Implemented (v0.1)** (`internal/attention`,
`internal/storage`). Snapshot/compaction of the log is **Deferred**. See
`docs/Roadmap.md`.

## Context

The UI must show what requires action, not raw provider notifications, and it
must do so without webhooks (the token-only promise,
`ADR/ADR-0001_Local_First_Web_Application.md`).

## Decision

Normalize provider activity into events and derive actionable attention states
by folding the accumulated event stream at read time.

Because DevPit is polling-first, events are **synthesized by diffing** each
polled snapshot against stored state, then appended to a persisted event log.
Attention state is the fold over that log — there is no separate materialized
current-state table.

## Rationale

Synthesizing events from polled snapshots keeps the "a token is enough" setup
promise while still delivering the event-derived state, history, and
"what's new" signals of an event model. Folding at read time is fast at
single-user scale and keeps the write path a simple append.

## Consequences

- A persisted event log is maintained, bounded by user-initiated retention;
  automatic compaction is deferred until a real instance proves it necessary.
- Event fidelity is bounded by poll cadence: activity between two polls is
  observed as a single diff, not as separate intermediate events. Bucket
  membership derives from the latest facts, so missed intermediate signals
  never produce wrong buckets.
- The fold is direct code (`internal/attention`); its bucket semantics are
  specified in `docs/Attention_Engine.md` and the event model and storage in
  `docs/Event_Taxonomy_and_Storage.md`.
