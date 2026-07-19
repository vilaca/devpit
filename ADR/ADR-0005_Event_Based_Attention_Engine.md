# Event-based Attention Engine

## Status

Proposed

## Context

This ADR records a foundational architectural decision for DevPit.

## Decision

Normalize provider activity into events and derive actionable attention
states from the accumulated event stream.

Because DevPit is polling-first (no webhooks), events are *synthesized* by
diffing each polled snapshot against stored state, then appended to a
persisted event log. Attention state is the fold over that log.

## Rationale

The UI focuses on what requires action instead of exposing
provider-specific notifications. Synthesizing events from polled snapshots
keeps the "a token is enough" setup promise — no webhooks, callbacks, or
provider-side configuration are required — while still delivering the
event-derived state, history, and "what's new" signals of an event model.

## Consequences

- A persisted event log is maintained (with a retention window to bound
  growth); see docs/Design_Decisions.md §2.
- Event fidelity is bounded by poll cadence: activity between two polls is
  observed as a single diff, not as separate intermediate events.
- Provides a consistent foundation for future implementation and
  contributor discussions.
