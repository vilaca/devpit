# Provider Plugin Model

## Status

Proposed

## Context

This ADR records a foundational architectural decision for DevPit.

## Decision

Each source system is implemented as a provider plugin exposing a common
interface.

## Rationale

The core remains provider-agnostic while new providers can be added
independently.

## Consequences

Provides a consistent foundation for future implementation and
contributor discussions.
