# Roadmap

The single source of truth for *what lands when*. ADRs and specs carry a coarse
`Scope` tag and link here; they do not restate this timeline. Parenthesized
references point to the owning ADR.

## v0.1 — Personal core (GitHub + GitLab)

The single-user product for two providers, **complete**: local single-user
instance (ADR-0001, ADR-0007), multi-account connections (ADR-0015),
token-only setup (ADR-0003), event-sourced engine (ADR-0005), user-centric
tiered sync (ADR-0004), one ranked list + pinned zone (ADR-0016), read-only
actions (ADR-0017), sync health + log (ADR-0018), Svelte SPA over REST + SSE
(ADR-0008, ADR-0010), plaintext-config secrets (ADR-0019). Component status:
`docs/High_Level_Architecture.md`.

## v0.1.1 — Marker vocabulary + age bands ✓ Built

CI-only `failing_checks` plus the `merge_conflict` / `needs_rebase` markers, age
bands in the ranking, and onset hover text on every tag (ADR-0016).

## v0.1.2 — Blocked diagnostic badges ✓ Built

The `missing approvals`, `discussions`, and `policy` badges, shipped only where
the provider reports the verdict (ADR-0016; parity table in
`docs/UI_Vocabulary.md`).

## v0.1.3 — GraphQL badge freshness ✓ Built

GitLab's FastPoll open-set refresh keeps the GraphQL-derived badges fresh for every
open item, not only todo-bearing ones (ADR-0004; merge semantics revised in
v0.1.6).

## v0.1.4 — Show all involved open items ✓ Built

The fold keeps every involved open item, including one that carries no signal
(ADR-0016; wire effect in `docs/REST_API.md`).

## v0.1.5 — Signal-based presentation ✓ Built

Rows show neutral provider signals in a fixed precedence instead of
viewer-relative attention states (ADR-0016). The `blocked` chip's suppression
when a marker already names the reason followed in v0.1.6 (ADR-0016).

## v0.1.5 — Jira ticket enrichment ✓ Built

Jira keys extracted at normalize time and ticket status shown on the title from
a persisted cache; off unless configured (ADR-0021; refresh cadence revised in
v0.1.6).

## v0.1.6 — First public release (beta) — packaging & distribution

DevPit's first public release (ADR-0023). The former version-agnostic
release-readiness gate is now this release's checklist; every item below ships
in v0.1.6.

- Homebrew-installable: publish a tap (`homebrew-devpit`) so users can
  `brew install vilaca/devpit/devpit`, with a goreleaser-generated formula
  carrying a `service` block and a `test` block (ADR-0023).
- Self-update awareness: the running app polls the GitHub Releases feed and
  surfaces a quiet "update available" chip; it never self-updates, only links
  out (ADR-0023, ADR-0017).
- Service integration for all platforms: a brew `service` block (macOS), a
  committed systemd user unit under `packaging/` (Linux), and a `compose.yaml`
  example (Docker); Windows is a README note only ("use Docker or run
  `devpit.exe --config …`; best-effort, untested") (ADR-0023).
- `listen:` config key: an optional bind address (default `localhost:7474`) so a
  container can bind `:7474` while published examples keep host exposure on
  loopback (ADR-0023, ADR-0001).
- Revised logging: review and tighten all log output before public exposure —
  remove debug noise, ensure errors are actionable, and confirm nothing
  sensitive (tokens, URLs with credentials) leaks into the log stream.
- Docker image: publish `ghcr.io/vilaca/devpit` (amd64+arm64) so users can run
  without a local Go toolchain. Config is mounted; the SQLite DB volume is
  optional because the store is a disposable cache (ADR-0023).
- `/up` health endpoint: a lightweight HTTP endpoint that returns `200 OK` when
  the service is ready. Used by Docker health-checks and process supervisors
  (ADR-0023).
- README updated: install (brew), Docker run, service integration, and update
  instructions documented; the README reflects the actual first-public-release
  surface. Includes the hero screenshot — captured from the committed demo forge,
  never from a real instance (ADR-0023).

## v0.2 — More forges + sync hardening

- Providers: Forgejo, Gitea, Codeberg (with capability declaration/degradation, ADR-0003). Codeberg is a hosted Forgejo instance; its provider type shares the Forgejo implementation with `base_url` defaulting to `https://codeberg.org`.
- Needs Backport bucket via a configurable label convention (deferred from v0.1,
  ADR-0003).
- Adaptive rate-budget scheduler, replacing basic backoff (ADR-0004).
- Snapshot/compaction of the event log, only if a real instance proves it
  necessary (ADR-0005).
- Per-call sync-log detail rows (deferred from ADR-0018).
- Binary-shipped retention ("clear history older than X") for brew/Docker users
  (`docs/Event_Taxonomy_and_Storage.md`, ADR-0023).

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
  (see `docs/Why.md`). Jira here means a full work-item provider —
  ticket-status enrichment already shipped in v0.1.5
  (`ADR/ADR-0021_Jira_Ticket_Enrichment.md`).

## Unversioned ideas

Noted, not committed to any release.

- Federation: a possible tier beyond single-user localhost and Planned team
  visibility (own-token observation). **Needs refinement** — the term is not
  yet defined. Candidate readings, mutually exclusive and each with different
  consequences: independent user-owned instances sharing attention data
  (peer-to-peer or via a hub); a hosted multi-user deployment behind accounts
  and login (which is what would force authentication, absent today per
  ADR-0001); or cross-provider identity linking of a single user's own
  handles. Before this becomes Planned it needs a chosen definition and its
  own ADR — in particular it is the trigger that would end the "localhost-only,
  no auth" stance of `ADR/ADR-0001_Local_First_Web_Application.md`.

- User-created areas (drag-to-organize): today the only manual grouping is
  pinning an item to the **Handle next** zone
  (`frontend/src/components/PinnedZone.svelte`; the flag/"star" action,
  `PUT`/`DELETE /items/{id}/flag`) — "Filtered" is not a user area, just the
  ranked list's heading under an active filter. Let the user define their own
  named areas and **drag** MRs/PRs into them instead of only being able to star,
  generalizing the single pin zone into arbitrary buckets ("Handle next" becomes
  one built-in area). Area membership is local-only organizational state that
  never acts on the forge (`ADR/ADR-0017_Read_Only_Action_Model.md`) and would
  extend the same local store as pins. Open design questions: ordered lanes
  (Kanban-style) vs. labels/tags; one area per item vs. many; whether an area
  suspends auto-ranking and age bands the way the pinned zone does
  (`docs/UI_Vocabulary.md`); the drag interaction plus a keyboard equivalent
  (DevPit is keyboard-first); and whether areas join the deferred local-state
  export path.

- User-configurable timing: expose polling and other timed activities in the
  config file. Today these are **deliberately constants, not config** — poll
  intervals and the staleness/old thresholds are engine constants (ADR-0004;
  `internal/config/config.go` states this explicitly, and config is static per
  `ADR/ADR-0015_Multi_Account_Connections.md`), and the other cadences are
  hardcoded too: the update check (24h, `internal/update`), the Jira refresh
  sweep (5m, `internal/jira`), and the sole-approver cache TTL (15m). Letting
  users tune them needs sane bounds and validation — too-frequent polling burns
  the forge rate budget, so this interacts with the v0.2 adaptive rate-budget
  scheduler — and revisits the constants-not-config stance of ADR-0004/ADR-0015,
  so it needs its own ADR before becoming Planned.

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

- Actor attribution & activity timeline (backed by already-captured data): the
  event log already records *who* did each thing — `Event.Actor`, plus the
  `Reviewer` / `Approver` / `Assigner` fields on the review, approval, and
  assignment signal payloads (`sdk/provider.go`) — and persists it, but the read
  layer does not yet surface it. Two near-term features fall out with **no new
  provider work**: (a) a **"who approved / who requested changes" tooltip** on the
  approval meta-row and review chips, reading the stored actor instead of only the
  count; and (b) an **all-events-by-timestamp activity view**, the global
  chronological complement to the item-scoped changelog idea above. These fields
  are deliberately retained as pre-wiring for this, not dead surface — see the
  pre-wiring carve-out in `docs/Semantic_Invariants.md` (INV-4).

- Show the resolved account display name (backed by already-captured data):
  `ResolveIdentity` already returns each connection's real name via
  `sdk.Identity.DisplayName` (`sdk/provider.go`) — e.g. "Ada Lovelace" rather
  than the `alovelace` handle — populated by both providers but currently
  discarded by the engine, which keeps only the handle. A near-term feature
  surfaces it to label connections by real name (sidebar / account chip / handle
  tooltip), with **no new provider work**. The field is deliberately retained as
  pre-wiring for this, not dead surface — see the pre-wiring carve-out in
  `docs/Semantic_Invariants.md` (INV-4).

- Activity-based decay for the Mentioned state: clear the mention once the
  provider observes the user's own reply/review after it. Requires a new
  own-activity signal from providers; preferred over time decay or a local
  dismiss, which are quieter but less honest.
- ~~Night mode (dark theme), remembered so it is set once.~~ ✓ Built (ADR-0020).

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
    (Assigned issues are probably lower precedence than Review Requested /
    Changes Requested, but higher than Review Submitted).
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
    map cleanly to Review Requested / Changes Requested / Assigned — a new bucket
    or a separate "Watching" tier may be needed).
  - Whether label tracking and user tracking are additive (union) or
    configurable per-subscription.

- ~~Number of reviewers~~ ✓ Built — the "N approved" meta-row count (ADR-0016).

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

