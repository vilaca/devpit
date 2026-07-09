# Deployment Model

## Status

Accepted

## Scope

Single-binary build **Implemented (v0.1)**; the Docker image is **Planned** —
no Dockerfile exists yet. See `docs/Roadmap.md`.

## Context

This ADR documents a foundational implementation decision for DevPit.

## Decision

DevPit will support two primary deployment methods: a single executable
and a Docker container.

## Rationale

This minimizes installation friction for individuals while providing an
easy deployment path for teams and homelab environments.

## Consequences

This decision establishes a consistent implementation direction while
remaining open to future refinement if requirements change.
