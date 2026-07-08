# API Design

## Status

Proposed

## Context

This ADR records a foundational architectural decision for DevPit.

## Decision

Expose a REST API with Server-Sent Events (SSE) for live updates.

## Rationale

REST is simple to implement and consume. Live updates in DevPit are
one-directional (server pushes "your attention list changed" to the
client; client actions use REST), so SSE is a better fit than WebSocket:
it rides plain HTTP, auto-reconnects, and avoids bidirectional-channel
complexity. See docs/Design_Decisions.md §13.

## Consequences

Provides a consistent foundation for future implementation and
contributor discussions. (Supersedes the earlier WebSocket choice;
docs/SSE_Events.md lists the event stream.)
