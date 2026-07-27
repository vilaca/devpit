# API Design

## Scope

**Implemented** — the REST surface and SSE stream are built in `internal/api`
and wired up in `cmd/devpit`. See `docs/Roadmap.md`.

## Context

Clients need the attention list and live notification that it changed. The
question is the transport for live updates.

## Decision

Expose a **REST API** with a **Server-Sent Events (SSE)** stream for live
updates. Live updates are one-directional (the server pushes "your attention
list changed"); client actions use REST. The event set is deliberately
coarse — events say *that* something changed and the client re-fetches, rather
than patching state from event payloads.

## Rationale

REST is simple to implement and consume. Because updates are one-directional,
SSE fits better than WebSocket: it rides plain HTTP, auto-reconnects, and
avoids bidirectional-channel complexity.

## Consequences

- Supersedes the earlier WebSocket choice.
- The REST surface and SSE event set are specified in `docs/REST_API.md`
  (which includes the SSE stream); both are **Implemented (v0.1)** in
  `internal/api`.
- The SSE events feed the health indicators and sync-log view
  (`ADR/ADR-0018_Sync_Observability.md`) and the live attention list
  (`ADR/ADR-0016_Presentation_And_Ranking.md`).
