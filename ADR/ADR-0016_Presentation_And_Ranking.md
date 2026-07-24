# Presentation and Ranking

## Status

Accepted — active. Covers the signal model shipped in v0.1.5 (formerly in the
separate ADR-0021, now folded here per ADR-0014's mutate-by-default convention;
git history preserves the original ADR-0021 text).

## Scope

Fold and ranking **Implemented (v0.1)** (`internal/attention`); the user-facing
presentation (pinned zone, tags, filters) is **Implemented (v0.1)** — the full
UI is built in `frontend/`. The marker vocabulary and age bands (decision
2026-07-10, below) are **Implemented (v0.1.1)**. Blocked diagnostic badges
(`needs_approval`, `unresolved_discussions`, `policy_denied`) are **Implemented
(v0.1.2)**. Showing all involved open items regardless of state is **Implemented
(v0.1.4)**. Signal-based presentation (nine neutral provider signals replacing
the former six attention states) is **Implemented (v0.1.5)**. See
`docs/Roadmap.md`.

## Context

An engineer needs to know what to do next without tuning knobs or reading a
per-repository dashboard. Buckets alone fragment attention; a raw feed buries
it.

The original v0.1 decision tagged rows with a closed set of six **attention
states** (Needs Review, Changes Requested, Blocked, Ready to Merge, Mentioned,
Waiting on Author) phrased as "what is *your* move?". Two weaknesses emerged:

- Since v0.1.4 the list also shows open items that match no attention state —
  an authored MR awaiting review, or one whose merge gate is `unknown`. These
  render as **bare rows**: the reader cannot tell what state the MR is in.
- The named states imply a **workflow** — a review lifecycle, an expected order.
  Teams configure their forges differently; assuming a sequence of phases
  re-derives org process, which both this decision's predecessor and
  `ADR/ADR-0003_Provider_Plugin_Model.md` set out to avoid.

## Decision

### Signal-based presentation (v0.1.5)

**A row shows the signals the provider currently reports for the item — neutral
facts, not an inferred state or lifecycle.** There is no closed set of
viewer-relative "attention states" and no assumed before/after.

This is a relabeling of the read layer, not a storage change: it maps directly
onto the existing event model (`docs/Event_Taxonomy_and_Storage.md`) —
`item.observed` facts (draft, merge gate, CI, approvals, conflicts) plus the
aimed-at-you **signal stream** (`signal.mentioned`, `signal.review_requested`,
…).

#### Signal set (fixed, no configuration)

Nine signals in precedence order, highest first (index 0 ranks the item). These
are the `State` constants and their wire strings
(`internal/attention/states.go`):

| # | wire value | label | condition |
|---|---|---|---|
| 1 | `changes_requested` | Changes Requested | `roles[author] && ReviewDecision == "changes_requested"` |
| 2 | `review_requested`  | Review Requested  | `roles[reviewer] && MyReviewState == "requested"` |
| 3 | `blocked`           | Blocked           | `roles[author] && !Draft && Gate == "blocked"` |
| 4 | `mentioned`         | Mentioned         | `hasMention` |
| 5 | `ready_to_merge`    | Ready to Merge    | `roles[author] && !Draft && Gate == "ready"` |
| 6 | `auto_merge_armed`  | Auto-merge Armed  | `roles[author] && !Draft && AutoMergeArmed` |
| 7 | `checks_running`    | Checks Running    | `roles[author] && !Draft && ChecksRunning` |
| 8 | `checking`          | Checking          | `Gate == "unknown"` (role-neutral — the backstop) |
| 9 | `review_submitted`  | Review Submitted  | `roles[reviewer] && reviewIsDone(MyReviewState)` |

An item carries **every** signal that applies; its **highest** (lowest-numbered)
signal sets its rank, the rest ride as additional tags. `draft` and the approval
count remain a marker and a meta-row fact respectively, not ranking signals.

#### Role scope (settled decision — D2)

The signal *vocabulary* is one word-set — no separate author/reviewer labels,
no authorship tag (the blue tint carries authorship). The *conditions* stay
role-aware where the fact is inherently about a role:

- The gate/verdict signals (Changes Requested, Blocked, Ready to Merge,
  Auto-merge Armed, Checks Running) describe an MR **you authored** — they
  keep their `roles[author]` guard.
- Review Requested and Review Submitted are reviewer-relative.
- Mentioned is any-role.
- **Checking (#8) is role-neutral**: it fires on any involved item whose gate
  is `unknown`, including a draft you only review, so it can backstop a row
  that would otherwise be bare.

Rationale: "same signal vocabulary regardless of your role" (ADR-0021) means
one *word-set*, **not** role-free conditions. Making `changes_requested`
role-neutral would tag the reviewer who *requested* the changes with
`changes_requested` (#1) alongside their `review_submitted` (#9) — a
contradiction the author/reviewer split exists to avoid.

#### Never-bare guarantee and the `checking` backstop (settled decision — D3)

`checking` fires purely on `Gate == "unknown"`; it is role-neutral and has no
draft suppression. Drafts report gate `unknown` on both providers
(`mergeGate` in each provider's normalizer: `"draft"→unknown` on GitHub,
`"draft_status"→unknown` on GitLab), so a draft carries exactly `["checking"]`
plus the `Draft` marker. Because the gate/verdict signals (`blocked`,
`ready_to_merge`, `auto_merge_armed`, `checks_running`) all have both `!Draft`
and `roles[author]` guards, authored MRs are never bare:
- gate `ready` → `ready_to_merge`
- gate `blocked` → `blocked`
- gate `unknown` → `checking`

Narrowing: a non-authored involved item with a *known* gate and no reviewer or
mention signal (e.g. a pure assignee on a ready MR) can still render
marker-only (empty `states` array). The v0.1.4 empty-array case narrows rather
than disappears (`docs/REST_API.md`).

`checking` does not flap: transient gate values never reach storage; the
synthesizer carries the last known gate forward. A previously-blocked MR under
transient recompute keeps gate `blocked` and does not drop to `checking`.

#### Provider parity for primary signals

- **Primary signals guaranteed identical (GitHub and GitLab).** Signals #1–#5
  and #8 behave identically across providers for any user's token — same wire
  value, same position, same rank.
- **Best-effort parity for #6 and #7:**
  - `auto_merge_armed` (#6): ships on both GitHub (GraphQL
    `autoMergeRequest{enabledAt}`, non-null ⇒ armed; degrades to false for PATs
    that cannot read it) and GitLab (REST `merge_when_pipeline_succeeds`).
  - `checks_running` (#7): **GitLab-only** (settled decision). GitHub cannot
    report an in-progress *gating* pipeline — it hides inside `blocked` — and
    we do **not** reconstruct it from `statusCheckRollup`. GitHub leaves
    `ChecksRunning` false; this is a documented ✗ gap in the parity table.

### Ranking

**Age band first** (fresh < stale < old), then **signal precedence** above,
then **newest-first**. The pinned "Handle next" zone stays exempt.

Reviewing another person's work (Review Requested, #2) ranks above your own
Blocked MR (#3): a quick action that unblocks someone else outranks a block you
will often clear via CI or a rebase anyway.

### Structural decisions (v0.1 and v0.1.1–v0.1.4, unchanged)

- **A single ranked list**, one row per WorkItem, with signals shown as tags.
  Buckets are optional client-side filters, not the primary layout.
- **A pinned "Handle next" zone** at the top: user-flagged items in flag order,
  lifted out of the auto-ranked list (never shown twice). The flag is
  local-only and never written back to the provider
  (`ADR/ADR-0017_Read_Only_Action_Model.md`).
- **Ranking is fixed signal-precedence + age tiebreak** — no numeric score, no
  configuration. Within a signal, newest-first, with a "stale" badge once an
  item's age exceeds a threshold as the anti-rot safety net.
- **Repeated same-type signals collapse** to one tag with a count
  ("Mentioned ×3"); the individual signals remain in the detail view.
- **Markers carry gate diagnostics; signals never do** (2026-07-10). The signal
  set is driven by the provider's merge gate, so Blocked stays trustworthy.
  Everything that explains *why* an item cannot merge is a marker:
  `failing_checks` means exactly "CI/checks red"; `merge_conflict` and
  `needs_rebase` are distinct because they demand different author effort.
  Markers are provider-normalized booleans in the item snapshot, like the gate
  itself.
- **Hover text must add information beyond the tag label** (2026-07-10) —
  never a paraphrase of the tag name. The universal payload is the tag's onset
  duration ("for 3d"), derived from the item's snapshot history at fold time;
  tags append genuinely extra facts where they exist (the provider's raw gate
  reason on Blocked, the non-required-check note on ready-but-red, the no-decay
  caveat on Mentioned). A tag with nothing beyond its label to say still shows
  its duration.
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
  by age band (fresh, then stale, then old last) before signal precedence,
  keeping fresh actionable work on top. Within a band the ranking above applies
  unchanged. The pinned zone is exempt — a pin is a deliberate user act — but
  pinned items still show their age tags and pin age, so rot cannot hide at the
  top.
- **Age tier presentation** (2026-07-10). Both `stale` and `old` items show
  a "Stale" tag; the `old` tier is additionally distinguished by a warm amber
  row background tint (`color-mix` over `--marker-old` at 7% opacity), so the
  two tiers remain visually distinct without introducing a separate "Old" label.
  The tag tooltip retains the exact threshold wording so the tier boundary is
  still discoverable on hover.
- **Authored-item row tint** (2026-07-10). Items where the authenticated
  identity of the connection matches the item's author field receive a subtle
  blue row background (`color-mix` over `--accent` at 7% opacity). This
  surfaces own-MR context without adding a separate badge or disturbing the
  ranking.
- **Approval count in meta-row** (2026-07-10). When at least one reviewer has
  approved an item, the row's meta-row shows "N approved" between the author
  and the timestamp. Shown only when N > 0; hidden on drafts. The count is a
  raw approved-reviewer count (not a gate verdict), so it is informational
  only and never moves items. GitLab: `approvedBy { count }` from the existing
  GraphQL join. GitHub: count of `APPROVED` entries in `latestReviews` from
  the same join. Wire field: `approvals_count int` (-1 = unknown, 0 = hide).
  The required-approvals denominator is deliberately omitted: GitHub's required
  count is branch-protection data (admin-only for non-admins) and CODEOWNERS
  makes raw counts misleading for gate purposes — the existing `needs_approval`
  badge already carries the honest gate verdict.
- **Open involved items always show, even stateless** (2026-07-10). The fold
  no longer drops an open item that matches no signal. Every item in the log is
  one the user is involved in (sync scopes are assigned/authored, plus mention
  signals), so an open MR waiting on reviewers, or one whose merge gate the
  provider has not yet computed (`unknown`), stays visible instead of silently
  disappearing. Signals still drive tags and ranking; a signal-less item renders
  as a plain row and sorts below every signaled item within its age band. Only
  merged/closed and removed items drop out. Wire effect: `states` may be an
  empty array for non-authored involved items (`docs/REST_API.md`).

## Rationale

A fixed precedence is trustworthy precisely because it cannot be tuned into
uselessness; a single list keeps the whole picture in one glance and reduces
context switching. Showing observed signals rather than an inferred state keeps
DevPit honest: it reports what the provider says and never assumes a team's
workflow — the same defer-to-the-provider discipline that keeps Blocked
trustworthy. Dropping the "your move" framing removes the bare-row gap for
authored MRs and lets one neutral vocabulary describe an MR whatever your role,
with the blue tint carrying authorship.

## Consequences

- The precedence order, the signal set, and the age thresholds are direct
  code — they live in `internal/attention/states.go` and
  `internal/attention/fold.go` (stale: 7 days, old: 30 days), not in prose.
  The fold and bucket semantics are specified in `docs/Attention_Engine.md`;
  the wire shape in `docs/REST_API.md`.
- Buckets a provider cannot feed simply produce no items
  (`ADR/ADR-0003_Provider_Plugin_Model.md`).
- Wire renames from v0.1.4: `needs_review` → `review_requested`;
  `waiting_on_author` → `review_submitted`.
