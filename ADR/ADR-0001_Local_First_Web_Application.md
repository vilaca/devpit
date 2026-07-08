# Local-first Web Application

## Status

Proposed

## Context

This ADR records a foundational architectural decision for DevPit.

## Decision

DevPit will be delivered as a self-hosted web application, runnable as a
single binary or Docker container.

## Rationale

A browser offers the richest UX while keeping deployment simple. A web
backend also enables additional clients (CLI, TUI, IDE, mobile) without
duplicating business logic.

## Consequences

Provides a consistent foundation for future implementation and
contributor discussions.
