# Backend-first Architecture

## Status

Proposed

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
