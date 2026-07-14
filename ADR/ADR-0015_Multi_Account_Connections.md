# Multi-Account Connections

## Scope

Implemented (v0.1) — `sdk.ConnectionConfig`, `internal/config`, and the
`connection_id` key on every stored row. See `docs/Roadmap.md`.

## Context

A user may hold several accounts on one provider (a personal and an org
github.com login) and accounts across providers. Discovery, identity, rate
budget, and health differ per account.

## Decision

Configuration is a list of independent, named connections. Each has a type
(`github`/`gitlab`/…), base URL, token, auto-detected identity, and a user
label ("Personal", "Acme"), keyed by an opaque, stable connection id. The same
type and even the same host may appear multiple times. Everything —
discovery, identity, rate budget, health — is scoped per connection.

The WorkItem key includes the connection: `(connection-id, object-type,
native-id)` (see `ADR/ADR-0006_Normalized_Data_Model.md`). The same object seen
via two connections yields two rows.

## Rationale

Per-connection scoping keeps provenance clear and lets each account carry its
own rate limit and health. Identity-scoped discovery (a signal targets one
handle → one connection) makes genuine cross-account duplicates rare, so
two-rows-for-one-object is a small accepted edge, not the common case.

## Consequences

- The database stores only opaque connection-id strings; connection details
  (URL, token, resolved identity) live in the config file
  (`ADR/ADR-0019_Secret_Storage.md`).
- Connections are static at runtime — resolved once at startup, no runtime
  add/remove (see `docs/Synchronization_Engine.md`).
- Exact config shape and validation live in `internal/config/config.go`; the
  connection contract is `docs/Provider_SDK.md`.
