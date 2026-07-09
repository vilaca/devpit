# Implementation Language (Go)

## Status

Accepted

## Scope

Implemented (v0.1). See `docs/Roadmap.md`.

## Context

This ADR documents a foundational implementation decision for DevPit.

## Decision

DevPit will be implemented primarily in Go.

## Rationale

Go provides a single static binary, excellent concurrency for background
synchronization, simple cross-compilation, low resource usage, and a
mature ecosystem for HTTP services, databases, and OAuth.
These characteristics align well with a self-hosted, local-first
application.

## Consequences

This decision establishes a consistent implementation direction while
remaining open to future refinement if requirements change.
