# Project Structure

## Status

Proposed

## Context

This ADR documents a foundational implementation decision for DevPit.

## Decision

The repository will separate application entry points, internal
services, provider implementations, shared SDKs, and the frontend.

## Rationale

A modular layout improves maintainability, encourages contributions, and
supports future provider growth without coupling core components.

## Consequences

This decision establishes a consistent implementation direction while
remaining open to future refinement if requirements change.
