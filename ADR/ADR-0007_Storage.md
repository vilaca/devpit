# Storage

## Status

Accepted

## Scope

SQLite in WAL with split read/write pools and a single-instance file lock
**Implemented (v0.1)** (`internal/storage`). See `docs/Roadmap.md`.

## Context

The polling writer must never block GUI reads: the UI must refresh at any time,
even mid-sync. The store must also be safe against a second process opening the
same database.

## Decision

Use **SQLite in WAL (Write-Ahead Logging) mode**. A single writer (the sync
engine) and many readers (the REST handlers) run on **two separate connection
pools** over the same database: a write pool capped at one connection and a
read-only pool. Writes use short, batched transactions so the committed
snapshot advances promptly. A **single-instance advisory file lock** is taken
at open time — two DevPit processes writing one database would clobber each
other's cursors and sync log every cycle.

## Rationale

WAL lets readers proceed against the last committed snapshot while the writer
appends, delivering "reads never wait" without torn-row anomalies. Splitting
the pools ensures a long reconcile write never serializes API reads behind it —
the reason WAL was chosen in the first place. SQLite keeps personal deployments
a single file with no server to run.

## Consequences

- The two-pool split, WAL pragmas, and the file lock are direct code
  (`internal/storage`); the concurrency model is specified in
  `docs/Synchronization_Engine.md`.
