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
- `discussions` badge is gate-gated: `blocking_discussions_resolved` is a raw "threads
  exist" fact — it returns false even when the project allows merging with open threads.
  Badge is only set when `gate = blocked`; ready MRs may have unresolved threads.

## v0.1.3 — GraphQL badge freshness ✓ Built

Decided 2026-07-10 (ADR-0004); implementation plan in
`docs/plans/2026-07-10_fast_poll_pipeline_freshness.md`.

- Fast_poll now refreshes the three volatile GraphQL-derived booleans
  (`failing_checks`, `needs_approval`, `needs_rebase`) for **all open items**
  on every ~60 s cycle — not just todo-bearing ones.
- Mechanism: Reconcile populates an in-memory `openSnapshots` cache (full
  REST+GraphQL payloads keyed by native ID). Fast_poll's open-set refresh
  issues a batched GraphQL query for uncovered items, merges only the three
  booleans onto the cached payload, and emits `item.observed` events.
  REST-derived fields (state, title, `merge_conflict`, etc.) are never
  clobbered. Dedup absorbs no-change cycles.
- Graceful degradation: GraphQL failure logs to sync_log and skips the
  open-set refresh; the cycle still succeeds.
- Fixes the live bug where an MR showed `failing_checks: true` while GitLab
  reported `headPipeline: RUNNING` until the next reconcile.

## v0.1.4 — Show all involved open items ✓ Built

Decided 2026-07-10 (ADR-0016).

- The fold no longer drops an open item that matches no attention state. Every
  item in the log is one the user is involved in (assigned/authored sync scopes
  plus mention signals), so authored MRs waiting on review and MRs with an
  `unknown` merge gate stay visible instead of silently disappearing.
- Stateless items render as a plain row (authored blue tint + `draft` marker
  carry the context) and sort below every stated item within their age band.
- Fixes the live bug where ~16 of a user's 26 open authored MRs were hidden:
  11 had gate `unknown` (CI running / gate not yet computed) and 5 were drafts.
- Wire effect: `states` may be an empty array (`docs/REST_API.md`).

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

- ~~Night mode (dark theme), remembered so it is set once.~~ ✓ Built — sun/moon
  toggle in TopBar; `localStorage("theme")` persists the choice; falls back to
  OS `prefers-color-scheme`; inline script in `index.html` prevents paint flash.
  See ADR-0020.

- App menu + quieter SSE status: replace the always-on live-stream dot in the
  top bar with a small app menu after the "DevPit" brand (desktop-app style),
  housing things like `Help`, `Check for updates`, `About`, and a live-updates
  status line. Rationale: SSE liveness ≠ data correctness — if the stream drops,
  the next poll/reconcile still brings everything in and the list is never
  wrong; the per-connection health dot already covers data currency. So the SSE
  indicator should be **quiet by default**: neutral/invisible while live, no
  toast on every blip (auto-reconnect usually wins in seconds), surfacing only
  after the stream has been down past a threshold (e.g. >30–60s of failed
  reconnects). Open question: whether a *prolonged* outage (say >2 min, meaning
  we're relying purely on polling) should escalate from the quiet menu line to
  something more noticeable, or stay quiet always. Not a priority.
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

- ~~Number of reviewers~~: ✓ Built (2026-07-10, ADR-0016). Shows "N approved"
  in the meta-row (between author and timestamp) when at least one reviewer
  has approved. Raw approved-reviewer count — not a gate verdict, never moves
  items. GitLab: `approvedBy { count }` via GraphQL join; GitHub: APPROVED
  count from `latestReviews`. Required-approvals denominator omitted:
  branch-protection data is admin-only on GitHub and CODEOWNERS makes raw
  counts misleading for gate purposes — the `needs_approval` badge carries
  the honest gate verdict.

- Surface rebase need earlier: today the `rebase` diagnostic badge is
  driven purely by GitLab's `shouldBeRebased` (GraphQL), which only turns
  true once a rebase is the *operative* blocker — while approvals or CI
  are outstanding GitLab reports those instead and the badge stays absent
  even when the branch is behind. To show "this will also need a rebase"
  alongside the other gates, DevPit would have to derive it from
  `diverged_commits_count` plus the project's merge method rather than
  trusting the provider's verdict.

  This is in direct tension with ADR-0016's "defer to the provider, never
  re-derive org rules" principle, so it is deliberately deferred, not
  planned. The main questions if ever revisited:
  - Whether `diverged_commits_count > 0` + a fast-forward/semi-linear
    merge method is a safe enough derivation, or still a rumor (it does
    not account for whether the rebase would conflict).
  - Whether it earns a distinct treatment (e.g. a muted "behind" hint)
    versus reusing the `rebase` badge, to keep the derived signal visually
    honest about being weaker than the provider verdict.
  - The GitHub equivalent (`behind` mergeable state) already has this
    shape — only meaningful when the repo requires up-to-date branches —
    so any derivation should be specified for both providers together.

- Sharper `failing_checks` label. "Failing Checks" is broad; a
  "tests failing" (or a per-category) reading would be more actionable.
  GitLab's `headPipeline.status` is a single rollup — it cannot say
  *what* failed — so specificity needs either the job/stage breakdown
  (`headPipeline.jobs` / `stages`, another GraphQL cost) or the merge
  widget's per-check list. Open question: is a coarse "tests failing"
  honest if we cannot tell tests from lint/build, or does specificity
  require the job breakdown? The provider-readable, cheap signal today
  is only the rollup. (Freshness was fixed in v0.1.3.)

