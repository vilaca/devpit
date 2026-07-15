# Local-first Web Application

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
- The `listen:` config key (`ADR/ADR-0023_Packaging_Distribution_and_Release_Pipeline.md`)
  may bind beyond loopback **inside a container**, but this localhost-only host
  exposure is preserved by publishing the port as `-p 127.0.0.1:7474:7474`.
  Publishing the host port on `0.0.0.0` would reintroduce the no-auth exposure
  this ADR forbids.
- Team visibility is delivered later as **own-token observation** of watched
  users/teams in a separate scope (Planned); it sees provider-visible facts
  only, never a teammate's private notifications.
