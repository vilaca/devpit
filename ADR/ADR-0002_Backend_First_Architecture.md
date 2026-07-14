# Backend-first Architecture

## Scope

Implemented (v0.1) — the backend owns synchronization, storage, business logic,
providers, and the fold (`cmd/devpit`, `internal/*`, `provider/*`). See
`docs/Roadmap.md`.

## Context

This ADR records a foundational architectural decision for DevPit.

## Decision

The backend owns synchronization, storage, business logic, provider
integrations, and the Attention Engine.

## Rationale

Keeping the backend authoritative allows multiple frontends to share the
same API and minimizes coupling.

## Consequences

Provides a consistent foundation for future implementation and
contributor discussions.
