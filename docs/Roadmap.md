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

**Built so far:** the sync engine, both providers, storage, config, the
attention fold, the REST API + SSE stream, and the full Svelte SPA — build
tooling, REST/SSE data layer, `go:embed` binary embedding, pinned zone, state
tags, bucket filters, sync-log drawer, failure banner, health dots, keyboard
navigation, and URL state (`frontend/`, `internal/web`). **v0.1 is complete.**
See `docs/High_Level_Architecture.md` for the component status.

## v0.1.1 — Marker vocabulary + age bands ✓ Built

Decided 2026-07-10 (ADR-0016); implementation plan in
`docs/plans/2026-07-10_marker_vocabulary_and_age_bands.md`.

- Markers: `failing_checks` narrowed to CI-only; new `merge_conflict` and
  `needs_rebase`; GitLab starts setting `failing_checks` (`ci_must_pass`).
- Age tiers: `stale` 7–30 days, `old` >30 days (exclusive); list sorts
  in age bands (fresh / stale / old) before state precedence; pinned
  zone exempt.
- UX: combined "ready to merge · optional checks red" rendering; raw
  `gate_detail` as blocked-tooltip; visual separation of state chips /
  diagnostic badges / age tags; pin zone shows age tags + pin age
  (`flagged_at` added to the API).
- Hover text on every tag: onset duration ("for 3d") from snapshot history
  (new `since` map in the API), plus extra facts where they exist
  (ADR-0016 tooltip principle).

## v0.1.2 — Blocked diagnostic badges ✓ Built

Decided 2026-07-10 (ADR-0016). Implementation plan in
`docs/plans/2026-07-10_blocked_diagnostic_badges.md`.

- Three new cosmetic diagnostic badges: `missing approvals`, `discussions`, `policy`.
- Parity principle: badge ships only where the provider reports a user-readable verdict.
- Both providers use a batched GraphQL join (one query per sync cycle).
- GitLab shows all blocked reasons at once (moved off single-valued `detailed_merge_status`).
- GitLab `checks failing` now covers any pipeline (closes the non-gating CI gap).
- Provider parity table in `docs/UI_Vocabulary.md`.

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

## v1.0 — Plugin SDK & ecosystem

- Stable Provider SDK for third-party providers.
- Broaden beyond code forges: Jira, Slack, CI/CD, Sentry, PagerDuty
  (see `docs/Why.md`).

## Unversioned ideas

Noted, not committed to any release.

- Changelog / "what's new since last visit": surface a per-item activity
  feed showing what changed since the user last opened DevPit —
  new reviews, comments, CI results, state transitions. The storage
  infrastructure is already designed for this: the `events` table's
  autoincrement `id` gives insertion order, and `app_state` with a
  `last_seen_event_id` watermark is explicitly deferred in
  `docs/Event_Taxonomy_and_Storage.md` pending this feature. The API
  shape needs a new endpoint (or a cursor param on the existing list)
  that returns events since the watermark, grouped by item. The main
  design question is presentation: inline diff indicators on list rows
  vs. a dedicated "new" section vs. a per-item detail panel.

- Activity-based decay for the Mentioned state: clear the mention once the
  provider observes the user's own reply/review after it. Requires a new
  own-activity signal from providers; preferred over time decay or a local
  dismiss, which are quieter but less honest.
- Homebrew distribution: publish a tap (`homebrew-devpit`) so users can install
  with `brew install vilaca/devpit/devpit`. Requires a release binary (GitHub
  Releases via goreleaser or similar), a formula with the correct SHA256, and
  a `brew test` block. Single-binary nature makes this straightforward once the
  CI release pipeline is in place.

- Night mode (dark theme), remembered so it is set once. Frontend-only:
  persist the choice in `localStorage` (single-user instance, no server
  round-trip needed); default to the OS `prefers-color-scheme` until the
  user picks explicitly.
- Issues as first-class attention items (GitHub issues / GitLab issues).
  Issues already appear in the data model (`object_type = issue`) and in
  the Mentioned bucket (`mentions:@me` search includes issues by design,
  GitLab todos cover `mentioned` and `directly_addressed` for issues
  too). The `signal.assigned` type exists in the taxonomy. So the
  infrastructure is partially there; what is missing is issue-specific
  attention states and their fold rules.

  The "owner or write access" framing is a red herring. The right frame
  is **actionability** — the same principle that governs the PR buckets.
  Issues do not have a review lifecycle, but two actionable states are
  clear regardless of repo access level:

  - **Assigned** — an issue assigned to you. No ownership required; the
    `assignee:@me` GitHub search and GitLab's `scope=assigned_to_me`
    already exist in the provider analysis for PR-assigned discovery.
    Token permissions already cover this (Issues: read on fine-grained
    GitHub PATs; `read_api` on GitLab).
  - **Needs Response** — an issue you opened (or are actively involved
    in) where the most recent activity is a mention or reply directed at
    you and you have not yet responded. This is the issue analogue of
    Changes Requested: the ball is explicitly back in your court. It
    requires detecting "your turn" from mention signals on authored
    items, which the existing `signal.mentioned` + authored-item filter
    can express, but the fold rule and the bucket definition are new.

  What is **not** actionable (and therefore out of scope for the
  personal attention model): issues you are merely subscribed to or
  watching, all issues in a repo you maintain, or any issue with new
  activity where you are not party to it. That territory belongs to a
  repo-management or triage tool, not to DevPit's "what demands your
  action today" framing.

  Open questions before implementation:
  - Whether Assigned and Needs Response are enough, or if there is a
    third state worth naming (e.g., an issue you commented on where
    someone specifically mentioned you back, distinct from one you merely
    opened).
  - Precedence of issue states relative to PR states in the ranked list
    (Assigned issues are probably lower precedence than Needs Review /
    Changes Requested, but higher than Waiting on Author).
  - Whether issues and PRs should be visually distinguished in the list
    (they currently share the same row shape).

- Multiple configs via `--config`: support launching the service with an
  explicit config file path (`devpit --config ~/work.yaml`, `devpit --config
  ~/personal.yaml`), allowing the user to maintain separate connection sets
  and switch between them by restarting with a different flag rather than
  editing a single shared file. Each config is fully independent — its own
  connections, its own DB, its own port if needed. No merging of configs;
  the loaded file is the entire world for that instance. The main open
  question is whether a single default config path (e.g. `~/.config/devpit/
  config.yaml`) is still honoured when `--config` is absent, or whether the
  flag becomes mandatory.

- Connection filter: a UI control (toggle or multi-select) to temporarily
  focus on one or more connections, hiding items from the others without
  touching config. Useful when switching context between work and personal
  accounts, or across multiple GitLab instances. The filter is ephemeral —
  session-only or persisted in `localStorage` — and never modifies the
  underlying connection config. The ranked list, bucket counts, and health
  dots should all reflect the active filter. The main design question is
  placement: a persistent header control vs. a collapsible sidebar vs. a
  keyboard-driven picker (e.g., `f` to open a connection filter overlay).

- Label-based tracking: surface items by label subscription rather than (or in
  addition to) user identity. Instead of only tracking "items assigned to me"
  or "items mentioning me", a user could say "show me everything labelled
  `needs-triage` or `p0`". This would cover team-owned queues and on-call
  rotations where the actionable signal is the label, not the mention.

  The main design questions:
  - Whether label subscriptions are configured per-connection or globally.
  - How label-matched items sit in the bucket/precedence model (they do not
    map cleanly to Needs Review / Changes Requested / Assigned — a new bucket
    or a separate "Watching" tier may be needed).
  - Whether label tracking and user tracking are additive (union) or
    configurable per-subscription.

