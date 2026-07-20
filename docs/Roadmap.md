# Roadmap

Scope reflects the decisions in docs/Design_Decisions.md (section refs in
parentheses). The single-user core and attention engine land first;
providers arrive incrementally.

## v0.1 — Personal core (GitHub + GitLab)

The complete single-user product for two providers.

- Single-user instance: own SQLite (WAL), own client, localhost, no auth
  (§1, §14).
- Multiple named connections per provider, including two accounts on one
  host (§1b).
- Token-only setup: base URL + token, `/user` identity with manual fallback
  (§7).
- Event-sourced engine: events synthesized by diffing polls, fold-on-read
  (§2, ADR-0005).
- Buckets: Needs Review, Blocked, Ready to Merge, Mentioned, Changes
  Requested, Waiting on Author (§4). Needs Backport excluded.
- Single ranked list + pinned "Handle next" zone; precedence + newest-first
  + stale badge (§5, §6).
- Discovery: notifications/todos change-signal + identity-scoped queries
  (§8).
- Sync: tiered polling + conditional requests + basic `Retry-After` backoff
  (§9).
- Read-only: deep-link out; no snooze/dismiss; local "Handle next" flag
  (§11).
- Graceful failure UX: per-provider health + rolling 60m failure count
  (§12).
- User-facing sync/poll log with progressive disclosure (§16).
- Frontend: Svelte SPA over REST + SSE (§13).
- Secrets: plaintext config with 0600 + least-privilege scopes (§15).

## v0.2 — More forges + sync hardening

- Providers: Forgejo, Gitea (with capability declaration/degradation, §10).
- Needs Backport bucket via a configurable label convention (§4, deferred
  item).
- Adaptive rate-budget scheduler, replacing basic backoff (§9).
- Snapshot/compaction of the event log, only if a real instance proves it
  necessary (§2).

## v0.3 — Team views

- Own-token observation of watched users/teams in a separate `[Me]/[Team]`
  scope (§1).
- Focuses on **open / in-progress MRs and their events** (review state, CI,
  staleness) — the team's shared work state — not a reconstruction of each
  teammate's private notifications (§1).
- Buckets re-interpreted for teammates (stalled / blocked-on-them).
- Federation (push aggregation) remains an uncommitted "future maybe" — built
  only if users ask (§1).

## v1.0 — Plugin SDK & ecosystem

- Stable Provider SDK for third-party providers.
- Broaden beyond code forges: Jira, Slack, CI/CD, Sentry, PagerDuty
  (per docs/Why.md).
