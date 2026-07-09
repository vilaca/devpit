# Normalized Data Model

## Status

Accepted

## Scope

Implemented (v0.1) for two object types — `merge_request` (covers GitHub PRs)
and `issue`. Broader entity coverage is **Planned/future**. See
`docs/Roadmap.md`.

## Context

Provider objects differ in shape and vocabulary. The application must reason
over them uniformly without provider-specific logic leaking into the core.

## Decision

Use a provider-neutral, **events-first** model. A WorkItem is a *derived*
grouping folded from the event log at read time — not a stored primary
entity — keyed by `(connection-id, object-type, native-id)`
(`ADR/ADR-0015_Multi_Account_Connections.md`). v0.1 realizes two object types,
`merge_request` and `issue`, as periodic snapshot facts (`item.observed`) plus
discrete signal events.

## Rationale

Events-first storage gives history and "what's new" for free
(`ADR/ADR-0005_Event_Based_Attention_Engine.md`) and avoids a materialized
state table that would need its own reconciliation. A provider-neutral fact set
keeps provider quirks in the plugins.

## Consequences

- There is no materialized current-state table in v0.1; object facts and
  attention states are folded from events on read.
- The event schema, the normalized fact set, and the event taxonomy are direct
  code (`internal/storage/schema.go`, `sdk/provider.go`) plus the spec
  `docs/Event_Taxonomy_and_Storage.md`. Additional provider-neutral entities
  (reviews, pipelines, releases) are introduced only as the taxonomy grows —
  they are not pre-modeled.
