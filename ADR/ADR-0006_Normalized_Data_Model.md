# Normalized Data Model

## Status

Proposed

## Context

This ADR records a foundational architectural decision for DevPit.

## Decision

Use provider-neutral entities such as WorkItem, Review, Mention,
Pipeline, Notification, Release, Repository and User.

Storage is events-first: WorkItem is a *derived* grouping folded from
the event log at read time, not a stored primary entity (see
docs/Design_Decisions.md §2–§3).

## Rationale

This avoids provider-specific logic leaking into the application.

## Consequences

Provides a consistent foundation for future implementation and
contributor discussions.
