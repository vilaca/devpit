# Presentation and Ranking

## Status

Accepted

## Scope

Fold and ranking **Implemented (v0.1)** (`internal/attention`); the user-facing
presentation (pinned zone, tags, filters) is **Implemented (v0.1)** — the
full UI is built in `frontend/`. The marker vocabulary and age bands
(decision 2026-07-10, below) are **Implemented (v0.1.1)**. Blocked diagnostic
badges (`needs_approval`, `unresolved_discussions`, `policy_denied`) are
**Implemented (v0.1.2)**. See `docs/Roadmap.md`.

## Context

An engineer needs to know what to do next without tuning knobs or reading a
per-repository dashboard. Buckets alone fragment attention; a raw feed buries
it.

## Decision

- **A single ranked list**, one row per WorkItem, with attention states shown
  as tags. Buckets are optional client-side filters, not the primary layout.
- **A pinned "Handle next" zone** at the top: user-flagged items in flag order,
  lifted out of the auto-ranked list (never shown twice). The flag is
  local-only and never written back to the provider
  (`ADR/ADR-0017_Read_Only_Action_Model.md`).
- **Ranking is fixed state-precedence + age tiebreak** — no numeric score, no
  configuration. Action-demanding states rank above Ready to Merge. Within a
  state, newest-first, with a "stale" badge once an item's age exceeds a
  threshold as the anti-rot safety net.
- **Repeated same-type signals collapse** to one tag with a count
  ("Mentioned ×3"); the individual signals remain in the detail view.
- **Markers carry gate diagnostics; states never do** (2026-07-10). Attention
  states remain a closed set driven by the provider's merge gate, so Blocked
  stays trustworthy. Everything that explains *why* an item cannot merge is a
  marker: `failing_checks` means exactly "CI/checks red" (no longer merge
  conflicts); `merge_conflict` and `needs_rebase` are distinct because they
  demand different author effort (manual resolution vs a mechanical rebase).
  Markers are provider-normalized booleans in the item snapshot, like the gate
  itself.
- **Hover text must add information beyond the tag label** (2026-07-10) —
  never a paraphrase of the tag name. The universal payload is the tag's
  onset duration ("for 3d"), derived from the item's snapshot history at fold
  time; tags append genuinely extra facts where they exist (the provider's
  raw gate reason on Blocked, the non-required-check note on ready-but-red,
  the no-decay caveat on Mentioned). A tag with nothing beyond its label to
  say still shows its duration.
- **Parity principle for diagnostic badges** (2026-07-10): a badge ships on a
  provider only when that provider reports a *verdict* readable by any user —
  never reconstructed from raw facts plus org rules DevPit would have to
  re-derive. Otherwise the badge is a documented gap for that provider (see the
  provider-parity table in `docs/UI_Vocabulary.md`).
- **Three new diagnostic badges** explain *why* an item is `blocked`:
  `missing approvals`, `discussions`, `policy`. Like `conflict`, `rebase`, and
  `checks failing` they are cosmetic — cosmetic markers never move items — and
  are provider-normalized booleans in the `item.observed` payload.
  `missing approvals` ships on both providers. `discussions` and `policy` are
  GitLab-only (see parity table).
- **GitLab shows all applicable reasons simultaneously.** GitLab's
  `detailed_merge_status` is single-valued, so badges move off it onto
  independent per-fact signals: `has_conflicts` and
  `blocking_discussions_resolved` (REST fields, free) plus a batched GraphQL
  join (`approved`/`approvalsLeft`, `shouldBeRebased`,
  `headPipeline.status`). Only `policy` stays on `detailed_merge_status`
  (no independent field exists) and can be masked by a co-present reason —
  accepted residual. GitHub gets the same batched GraphQL join shape
  (`reviewDecision`).
- **GitLab `checks failing` extended** (2026-07-10): moves from
  `ci_must_pass` (gating only) to `headPipeline.status` red (any pipeline
  via GraphQL join), closing the documented "GitLab non-gating CI invisible"
  gap.
- **Age tiers band the list** (2026-07-10). `stale` (idle 7–30 days) and
  `old` (idle >30 days) are mutually exclusive tiers, and they are the
  *single deliberate exception* to "markers never move items": the list sorts
  by age band (fresh, then stale, then old last) before state
  precedence, keeping fresh actionable work on top. Within a band the ranking
  above applies unchanged. The pinned zone is exempt — a pin is a deliberate
  user act — but pinned items still show their age tags and pin age, so rot
  cannot hide at the top.
- **Age tier presentation** (2026-07-10). Both `stale` and `old` items show
  a "Stale" tag; the `old` tier is additionally distinguished by a warm amber
  row background tint (`color-mix` over `--marker-old` at 7% opacity), so the
  two tiers remain visually distinct without introducing a separate "Old" label.
  The tag tooltip retains the exact threshold wording so the tier boundary is
  still discoverable on hover.
- **Authored-item row tint** (2026-07-10). Items where the authenticated
  identity of the connection matches the item's author field receive a subtle
  blue row background (`color-mix` over `--accent` at 7% opacity). This
  surfaces own-MR context — typically `waiting_on_author` or
  `changes_requested` state — without adding a separate badge or disturbing
  the ranking.

## Rationale

A fixed precedence is trustworthy precisely because it cannot be tuned into
uselessness; a single list keeps the whole picture in one glance and reduces
context switching.

## Consequences

- The precedence order, the state set, and the age thresholds are direct
  code — they live in `internal/attention/states.go` and
  `internal/attention/fold.go` (stale: 7 days, old: 30 days), not in
  prose. The fold and bucket semantics are specified in `docs/Attention_Engine.md`;
  the wire shape in `docs/REST_API.md`.
- Buckets a provider cannot feed simply produce no items
  (`ADR/ADR-0003_Provider_Plugin_Model.md`).
