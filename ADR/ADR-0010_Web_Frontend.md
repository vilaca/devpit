# Web Frontend Architecture

## Status

Proposed

## Context

This ADR documents a foundational implementation decision for DevPit.

## Decision

The primary user interface will be a browser-based application built
separately from the backend and embedded into release binaries.

## Rationale

A web UI offers the richest user experience for dashboards while
allowing the backend API to support future CLI, TUI, IDE, and mobile
clients.

## Consequences

This decision establishes a consistent implementation direction while
remaining open to future refinement if requirements change.
