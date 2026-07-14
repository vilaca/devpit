# Web Frontend Architecture

## Scope

**Implemented (v0.1)** — the SPA is fully built: Vite + Svelte 5 build
tooling, the REST/SSE data layer, `go:embed` serving from the binary, and the
full presentation UI (pinned zone, state tags, bucket filters, sync-log view,
failure banner, health dots, keyboard shortcuts, URL state) are all in
`frontend/` and `internal/web`. See `docs/Roadmap.md`.

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

- Accepted cost: a JS build step lives in the repo (`frontend/`, Vite). It
  outputs into `internal/web/dist`, which `go:embed` bundles into the binary; a
  committed placeholder `index.html` keeps `go build` working before the SPA is
  built.
- The binary serves the SPA and the API from one origin: `internal/api` mounts
  the embedded static handler (`internal/web`) as a catch-all behind the more
  specific API routes, with history-API fallback so a browser refresh on any
  route serves `index.html`.
- In dev, Vite's server proxies the API paths through to a running backend
  (`vite.config.ts`), so the browser still sees a single origin.
- Live updates come from an open SSE stream that invalidates a store slice,
  which re-fetches over REST; a cold load runs the same fetch path, so a live
  update and a refresh converge on identical state (the fold is on read).
