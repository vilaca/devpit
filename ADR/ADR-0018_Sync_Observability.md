# Sync Observability

## Status

Accepted

## Scope

Sync-log persistence and per-cycle rows **Implemented (v0.1)**
(`internal/storage`, `internal/engine`). Per-call detail rows **Deferred**. The
user-facing health indicators and sync-log view are **Implemented (v0.1)** —
connection health dots, the failure banner, and the sync-log drawer are built
in `frontend/`. See `docs/Roadmap.md`.

## Context

Polling is invisible. A user must be able to trust that an empty attention list
means "nothing to do" rather than "sync is broken", and to see how and when a
provider last failed.

## Decision

- **Per-provider health**: a health dot, "last synced X ago", and a rolling
  failure count over a fixed recent window (60 minutes), derived from the sync
  log. The window is a fixed constant, not user-configurable.
- **Graceful degradation**: on failure keep showing the last good data marked
  *stale*, plus a non-blocking banner naming the provider and cause. One
  provider failing never blanks the others.
- **Never conflate empty with failed**: an empty list says so explicitly
  ("All clear — synced 1m ago"); a stale/failed provider is always visibly
  distinct.
- **A persisted, bounded sync log**: one human-readable row per poll cycle per
  connection; on failure the individual calls, statuses, retries, and
  next-retry are captured and shown on expand. Bounded by user-initiated
  cleanup plus an optional cap.
- **Progressive disclosure**: health dot → rolling failure count → per-cycle
  log → expanded per-call detail.

## Rationale

A visible, healthy sync log is what makes an empty attention list believable;
it serves debugging and trust at once, without adding setup steps.

## Consequences

- The `sync_log` schema and the outcome set are direct code
  (`internal/storage/schema.go`, `internal/engine/cycle.go`) — an open `TEXT` outcome
  column so the set extends without migration. The log's semantics and the
  outcome taxonomy are specified in `docs/Synchronization_Engine.md`; the
  response shapes in `docs/REST_API.md`, implemented in `internal/api`.
- Per-call detail rows are Deferred: the v0.1 schema records cycle summaries
  only.
- Health and the sync-log view live behind the same SSE events as the rest of
  the UI (`ADR/ADR-0008_API_Design.md`).
