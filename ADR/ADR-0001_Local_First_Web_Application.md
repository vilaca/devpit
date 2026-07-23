# Local-first Web Application

## Status

Accepted

## Scope

Single-user localhost core **Implemented (v0.1)**. Team visibility (own-token
observation) is **Planned**.

## Context

This ADR records a foundational architectural decision for DevPit.

## Decision

DevPit is delivered as a self-hosted web application, runnable as a single
binary or Docker container. The core is **single-user**: each user runs their
own instance with their own SQLite store and their own client, on localhost,
with no authentication and no webhooks.

## Rationale

A browser offers the richest UX while keeping deployment simple, and a web
backend enables additional clients (CLI, TUI, IDE, mobile) without duplicating
business logic. Single-user-on-localhost keeps the setup promise minimal — a
token is enough — and removes auth, callback URLs, and provider-side config
from the v0.1 surface.

## Consequences

- No authentication exists while DevPit is localhost-only.
- Team visibility is delivered later as **own-token observation** of watched
  users/teams in a separate scope (Planned); it sees provider-visible facts
  only, never a teammate's private notifications.
