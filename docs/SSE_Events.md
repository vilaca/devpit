# SSE Events

Live updates are delivered over a Server-Sent Events (SSE) stream at
`GET /events` (one-directional, server -> client). Client actions use
the REST API.

The event set is deliberately coarse: events tell the client *that*
something changed and it re-fetches, rather than patching state from
event payloads.

Event types:

- `attention.changed` — the ranked list changed; client re-fetches
  `/attention`.
- `sync.completed` — a poll cycle finished for a connection; feeds the
  health indicator and live sync-log view (§12, §16).
- `sync.failed` — a poll cycle failed; drives the non-blocking failure
  banner (§12).

Fine-grained domain events (review.requested, mention.created, ...) may
be added later for notification/toast features.
