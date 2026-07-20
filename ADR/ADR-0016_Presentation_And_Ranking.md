# Presentation and Ranking

## Status

Accepted

## Scope

Fold and ranking **Implemented (v0.1)** (`internal/attention`); the user-facing
presentation (pinned zone, tags, filters) is **Planned** — the frontend is not
built. See `docs/Roadmap.md`.

## Context

An engineer needs to know what to do next without tuning knobs or reading a
per-repository dashboard. Buckets alone fragment attention; a raw feed buries
it.

## Decision

- **A single ranked list**, one row per WorkItem, with attention states shown
  as tags. Buckets are optional client-side filters, not the primary layout.
- **A pinned "Handle next" zone** at the top: user-flagged items in flag order,
  lifted out of the auto-ranked list (never shown twice). The flag is
  local-only and never written back to the provider
  (`ADR/ADR-0017_Read_Only_Action_Model.md`).
- **Ranking is fixed state-precedence + age tiebreak** — no numeric score, no
  configuration. Action-demanding states rank above Ready to Merge. Within a
  state, newest-first, with a "stale" badge once an item's age exceeds a
  threshold as the anti-rot safety net.
- **Repeated same-type signals collapse** to one tag with a count
  ("Mentioned ×3"); the individual signals remain in the detail view.

## Rationale

A fixed precedence is trustworthy precisely because it cannot be tuned into
uselessness; a single list keeps the whole picture in one glance and reduces
context switching.

## Consequences

- The precedence order, the state set, and the staleness threshold are direct
  code — they live in `internal/attention/states.go` and
  `internal/attention/fold.go` (threshold: 7 days), not in prose. The fold and
  bucket semantics are specified in `docs/Attention_Engine.md`; the wire shape
  in `docs/REST_API.md`.
- Buckets a provider cannot feed simply produce no items
  (`ADR/ADR-0003_Provider_Plugin_Model.md`).
