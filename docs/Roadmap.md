# Roadmap

The single source of truth for *what lands when*. ADRs and specs carry a coarse
`Scope` tag and link here; they do not restate this timeline. Parenthesized
references point to the owning ADR.

## v0.1 — Personal core (GitHub + GitLab)

The complete single-user product for two providers.

- Single-user instance: own SQLite (WAL), own client, localhost, no auth
  (ADR-0001, ADR-0007).
- Multiple named connections per provider, including two accounts on one host
  (ADR-0015).
- Token-only setup: base URL + token, `/user` identity with manual fallback
  (ADR-0003).
- Event-sourced engine: events synthesized by diffing polls, fold-on-read
  (ADR-0005).
- Buckets: Needs Review, Blocked, Ready to Merge, Mentioned, Changes Requested,
  Waiting on Author (ADR-0016). Needs Backport excluded.
- Single ranked list + pinned "Handle next" zone; precedence + newest-first +
  stale badge (ADR-0016).
- Discovery: notifications/todos change-signal + identity-scoped queries
  (ADR-0004).
- Sync: tiered polling + conditional requests + basic `Retry-After` backoff
  (ADR-0004).
- Read-only: deep-link out; no snooze/dismiss; local "Handle next" flag
  (ADR-0017).
- Graceful failure UX: per-provider health + rolling 60m failure count
  (ADR-0018).
- User-facing sync/poll log with progressive disclosure (ADR-0018).
- Frontend: Svelte SPA over REST + SSE (ADR-0008, ADR-0010).
- Secrets: plaintext config with 0600 + least-privilege scopes (ADR-0019).

**Built so far:** the sync engine, both providers, storage, config, and the
attention fold. **Not yet built:** the REST API, SSE stream, and frontend (see
`docs/High_Level_Architecture.md` for the component status).

## v0.2 — More forges + sync hardening

- Providers: Forgejo, Gitea (with capability declaration/degradation, ADR-0003).
- Needs Backport bucket via a configurable label convention (deferred from
  ADR-0016).
- Adaptive rate-budget scheduler, replacing basic backoff (ADR-0004).
- Snapshot/compaction of the event log, only if a real instance proves it
  necessary (ADR-0005).
- Per-call sync-log detail rows (deferred from ADR-0018).

## v0.3 — Team views

- Own-token observation of watched users/teams in a separate `[Me]/[Team]`
  scope (ADR-0001).
- Focuses on open / in-progress MRs and their events (review state, CI,
  staleness) — the team's shared work state — not a reconstruction of each
  teammate's private notifications.
- Buckets re-interpreted for teammates (stalled / blocked-on-them).
- Federation (push aggregation) remains an uncommitted "future maybe", built
  only if users ask (ADR-0001).

## v1.0 — Plugin SDK & ecosystem

- Stable Provider SDK for third-party providers.
- Broaden beyond code forges: Jira, Slack, CI/CD, Sentry, PagerDuty
  (see `docs/Why.md`).

## Open questions (undecided)

- **Authentication / access control** if DevPit is ever exposed beyond
  localhost or federated — currently none is needed (ADR-0001, ADR-0019). A
  hard prerequisite for federation.
- **Observability depth** — structured logs, metrics, tracing beyond the
  user-facing sync log (`docs/Observability.md`).
