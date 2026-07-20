# Web Frontend Architecture

## Status

Accepted

## Scope

**Planned** — `frontend/` is a placeholder; no SPA is built yet. See
`docs/Roadmap.md`.

## Context

The primary interface is a dashboard that must update live and be fast to
operate, while the backend API stays reusable by other clients.

## Decision

The primary user interface is a browser-based **lightweight SPA (Svelte)**,
built separately from the backend and embedded into release binaries as static
assets via `go:embed`. It subscribes to the SSE stream and patches the DOM in
place (no page refresh); keyboard shortcuts are handled client-side.

## Rationale

A web UI offers the richest experience for dashboards while the backend API
(`ADR/ADR-0008_API_Design.md`) remains available to future CLI, TUI, IDE, and
mobile clients. Embedding via `go:embed` keeps the single-binary deployment
(`ADR/ADR-0011_Deployment_Model.md`).

## Consequences

- Accepted cost: a JS build step lives in the repo.
- Frontend and its SSE/REST integration are **Planned**, not built (the last
  remaining v0.1 piece); the API surface it consumes is built (`internal/api`)
  and specified in `docs/REST_API.md`.
