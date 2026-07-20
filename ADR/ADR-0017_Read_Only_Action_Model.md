# Read-Only Action Model

## Status

Accepted

## Scope

Storage and fold support **Implemented (v0.1)** (the `handle_next` table and
the fold's pinned zone); the click-through UI is **Planned** — the frontend is
not built. See `docs/Roadmap.md`.

## Context

DevPit aggregates work from source systems; it is not a replacement for them.
The question is how much state DevPit writes and where actions happen.

## Decision

- **Read-only by default.** Clicking an item deep-links to it on the provider;
  all real actions happen there.
- **No snooze, dismiss, or hide.** Items vanish only when the underlying
  condition clears — DevPit never hides the user's truth.
- **The one user-applied state is a local "Handle next" flag** (stored in
  SQLite, never written to the provider), which drives the pinned zone
  (`ADR/ADR-0016_Presentation_And_Ranking.md`).

## Rationale

Hiding items would make the attention list lie; keeping actions in the source
systems avoids re-implementing (and drifting from) each provider's write
semantics, and keeps token scopes read-only
(`ADR/ADR-0019_Secret_Storage.md`).

## Consequences

- Least-privilege read-only tokens are sufficient; a leaked token cannot write.
- The flag lives in `handle_next` (see `internal/storage`); the flag REST
  endpoints are specified in `docs/REST_API.md` and implemented in `internal/api`.
- Provider write actions (approve, comment) are out of scope; if added later
  they become an explicit, opt-in capability, not a default.
