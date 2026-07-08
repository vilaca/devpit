# Storage

## Status

Proposed

## Context

This ADR records a foundational architectural decision for DevPit.

## Decision

Use SQLite by default and PostgreSQL for larger deployments.

## Rationale

SQLite keeps personal deployments simple while PostgreSQL supports
team-scale workloads.

SQLite runs in WAL (Write-Ahead Logging) mode so the polling writer never
blocks GUI reads: readers see the last committed snapshot while sync
appends events. See docs/Design_Decisions.md §14.

## Consequences

- WAL mode assumed; single writer (sync) + many readers (REST handlers),
  with short batched write transactions.
- Provides a consistent foundation for future implementation and
  contributor discussions.
