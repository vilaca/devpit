# Presentation and Ranking

## Scope

Implemented (v0.1.x) — the fold, ranking, and signal model in
`internal/attention`; the presentation (pinned zone, tags, badges, filters) in
`frontend/`. This ADR absorbed the formerly separate signal-design ADR
(ADR-0014's fold-and-renumber convention). See `docs/Roadmap.md` for timing.

## Context

An engineer needs to know what to do next without tuning knobs or reading a
per-repository dashboard. Buckets alone fragment attention; a raw feed buries
it.

Labelling rows with a closed set of viewer-relative "what is *your* move?"
states (Needs Review, Waiting on Author, …) has two flaws. An open item that
matches no state renders as a **bare row** — the reader cannot tell what state
the MR is in. And named states imply a **workflow** — a review lifecycle, an
expected order. Teams configure their forges differently; assuming a sequence
of phases re-derives org process, which `ADR/ADR-0003_Provider_Plugin_Model.md`
sets out to avoid.

## Decision

### One list

- **A single ranked list**, one row per WorkItem, with signals shown as tags.
  Buckets are optional client-side filters (`frontend/src/lib/buckets.ts`), not
  the primary layout. Two filters diverge from one-signal-per-bucket: `mine`
  (items that are yours — the same predicate as the row tint) and `mentioned`,
  which also gathers your review plate (items where you are a reviewer, read
  from the `my_roles` wire field).
- **Every open item you are involved in shows**, even with no signal — it
  renders as a plain row and ranks like any other. Only merged/closed and
  removed items drop out (list membership: `docs/Attention_Engine.md`).
- **A pinned "Handle next" zone** at the top of the unfiltered "All" view:
  user-flagged items in flag order, lifted out of the ranked list (never shown
  twice). Under a bucket filter the zone is hidden and a matching pinned item
  folds into the list at its natural rank — the zone is a whole-list triage aid,
  not a per-bucket one. Pins are exempt from ranking but still show their age
  tags and pin age, so rot cannot hide at the top. The flag is local-only
  (`ADR/ADR-0017_Read_Only_Action_Model.md`).

### Ranking

**Ranking is age band then recency** — no numeric score, no configuration.
Three tiers, top to bottom: fresh, **stale**, **old**. Within a tier, items
order by their ranking timestamp, most recent first (newest signal, else the
latest snapshot's provider-updated time); item ID is the final tiebreak.

- **Signal precedence orders chips, never items.** It only decides which chip
  leads a row (`States[0]`).
- **The age band is the single deliberate exception to "cosmetic markers never
  move items"** — the "stale" and "old" tiers are the anti-rot safety net.
- **Reviewed-done muting is display-only** — a muted item sorts in its tier by
  recency like everything else.

**Rank-only signals** advance the ranking clock but add no chip:

- `signal.approved` / `signal.changes_requested` — review verdicts, stamped with
  the provider's real verdict time (`docs/Provider_API_Analysis.md`; dedupe keys
  in `docs/Event_Taxonomy_and_Storage.md`). Their visual surface stays the
  `changes_requested` chip and the approvals count.
- `signal.ci_failed` — a broken build on an authored MR (GitLab only: GitHub's
  CI notifications aren't PR-scoped). It resurfaces fresh and stale work, but is
  **dropped from the clock once the item is old** on its real activity (every
  signal except `ci_failed`, plus the snapshot): a broken build must not
  resurrect work the user has let go. Staleness is measured without the CI event
  — otherwise a failure would always read as fresh activity and the guard could
  never fire.

### Signals

**A row shows the signals the provider currently reports for the item — neutral
facts, not an inferred state or lifecycle.** There is no closed set of
viewer-relative states and no assumed before/after. This is a read-layer view,
not a storage change: signals map onto `item.observed` facts plus the aimed-at-you
signal stream (`docs/Event_Taxonomy_and_Storage.md`).

The set is fixed, with no configuration. Wire values, precedence, and firing
conditions are direct code — the `State` consts, `precedence`, and `matches` in
`internal/attention/states.go`; labels and plain-language semantics are in
`docs/Attention_Engine.md`. An item carries **every** signal that applies.

- **Role scope (settled decision — D2).** One signal vocabulary whatever your
  role — no author/reviewer labels, no authorship tag (the row tint marks items
  that are yours). The *conditions* stay role-aware where the fact is
  inherently about a role: the gate signals describe an MR that cannot progress
  without you (author or sole approver), review signals are reviewer-relative,
  Mentioned is any-role, and Changes Requested fires both on the author's MR
  verdict and on a reviewer's own verdict.
- **Never bare (settled decision — D3).** `checking` fires purely on gate
  `unknown` — role-neutral, no draft suppression. With the author-guarded gate
  signals, every authored MR carries at least one signal (`ready_to_merge`,
  `blocked`, or `checking`).
- **Sole approver.** When the user is the only account that can merge
  (`sole_approver` role; discovery in
  `ADR/ADR-0004_User_Centric_Synchronization.md`), the item has an authored
  item's urgency: it gets the gate signals, `review_requested` without an
  explicit request, and is never muted. Always-on and self-limiting.
- **Muting.** A reviewer — not the author or sole approver — whose review is
  done has nothing left to do: the row is **muted** (de-emphasized, chips
  suppressed). The one surviving chip is a reviewer-side `changes_requested`, so
  a dim row still says *why* it is dim and that the user is the one blocking it.
- **Provider parity.** Changes Requested, Review Requested, Blocked, Ready to
  Merge, and Checking behave identically on every provider for any user's token;
  so does Mentioned, except that on GitHub it needs a classic PAT
  (`docs/Token_Setup.md`). Auto-merge Armed and Checks Running are best-effort; Checks
  Running is GitLab-only and not reconstructed on GitHub (parity notes in
  `docs/UI_Vocabulary.md`).

### Markers and diagnostic badges

- **Markers carry gate diagnostics; signals never do.** The signal set is driven
  by the provider's merge gate, so Blocked stays trustworthy. Everything that
  explains *why* an item cannot merge is a marker — a provider-normalized
  boolean in the snapshot, like the gate itself (the `WorkItem` marker fields in
  `internal/attention/fold.go`). `failing_checks` means exactly "CI/checks red";
  `merge_conflict` and `needs_rebase` are distinct because they demand different
  author effort. Cosmetic markers never move items. `signal.ci_failed` stays
  within this rule: it carries no chip and no reason, only recency.
- **Parity principle**: a badge ships on a provider only when that provider
  reports a *verdict* readable by any user — never reconstructed from raw facts
  plus org rules DevPit would have to re-derive. Otherwise the badge is a
  documented gap (the parity table in `docs/UI_Vocabulary.md`). Where a provider
  exposes independent per-fact fields, every applicable reason shows at once.
- **The Blocked chip is suppressed when a visible marker is the reason the gate
  names** — a render rule only (signal, bucket, and wire format unchanged), and
  matched **strictly** against `gate_detail`, never "any marker visible".

### Row presentation

- **Hover text adds information beyond the tag label** — never a paraphrase.
  The universal payload is the tag's onset duration, derived from snapshot
  history at fold time; tags append genuinely extra facts where they exist.
- **Repeated same-type signals collapse** to one tag with a count
  ("Mentioned ×3"); the individual signals remain in the event log.
- **Age tiers**: both `stale` and `old` show a "Stale" tag; the `old` tier is
  distinguished by a warm row tint, not a separate label.
- **Context without badges**: a row tint marks items that are yours; the
  meta-row shows the approvals count ("N approved", or "you + N approved" when
  you approved) — a raw count, informational only, with no required-approvals
  denominator.
- **Provider labels** render as plain-text names on their own row, without
  provider colors, and show even on muted rows. The GraphQL open-set refresh
  doesn't carry them; they update when reconcile or a todo/notification
  re-fetches the item.

## Rationale

Age-band-then-recency ordering is trustworthy precisely because it cannot be
tuned into uselessness: fresh work stays on top, rot sinks, and within a tier the
list mirrors what actually just moved — no provider verdict silently reshuffles
it. Ranking by signal precedence would make the order swing on verdicts the engineer
reads off the chips anyway; ordering by recency shows where live activity is
without re-deriving a workflow. For the same reason muting does not demote: an
MR you approved that is still moving surfaces when it moves, dimmed but not
buried. Review verdicts are rank-only signals because a verdict is otherwise a
fact that emits no signal — and GitLab does not bump an MR's `updated_at` on
approval — so an approved MR would sink to the bottom of the fresh band.

A single list keeps the whole picture in one glance and reduces context
switching. Showing observed signals rather than an inferred state keeps DevPit
honest: it reports what the provider says and never assumes a team's workflow —
the same defer-to-the-provider discipline that keeps Blocked trustworthy.
Dropping the "your move" framing removes the bare-row gap for authored MRs and
lets one neutral vocabulary describe an MR whatever your role. One *word-set* is
not role-free *conditions*, though: a role-neutral `changes_requested` would tag
the reviewer who requested the changes with both `changes_requested` and
`review_submitted` — the contradiction the author/reviewer split avoids.

Blocked-chip suppression is strict because GitLab's `needs_approval` is true for
nearly every unapproved MR: loose matching would erase the chip exactly when the
operative blocker is something no marker shows (GitHub's opaque
`mergeable_state: "blocked"`, GitLab tier gates like `jira_association_missing`),
which are the cases where the chip and its `provider says: …` hover earn their
keep.

The approvals count omits a denominator because GitHub's required count is
branch-protection data (admin-only for non-admins) and CODEOWNERS makes raw
counts misleading for gate purposes; the `needs_approval` badge carries the
honest gate verdict. Provider labels are the team's own taxonomy, not DevPit's
attention verdicts, so they stay visually apart from the chips — per-label
provider colors competed with them — and, being stable context rather than an
attention cue, survive the mute. They stay out of the GraphQL open-set refresh
because they change rarely and that query has no complexity headroom to spare.

## Consequences

- The signal set, chip precedence, age thresholds, and ranking are direct code —
  `internal/attention/states.go` and `internal/attention/fold.go` (`sortItems`:
  age band, then recency, then ID). Fold and bucket semantics are specified in
  `docs/Attention_Engine.md`, the wire shape in `docs/REST_API.md`, the visual
  vocabulary in `docs/UI_Vocabulary.md`.
- Buckets a provider cannot feed simply produce no items
  (`ADR/ADR-0003_Provider_Plugin_Model.md`).
- "Never bare" holds for authored MRs only: a non-authored involved item with a
  known gate and no reviewer or mention signal (e.g. a pure assignee on a ready
  MR) renders marker-only, with an empty `states` array (`docs/REST_API.md`).
- GitLab exposes no verdict timestamp, so its provider baselines each MR's
  verdicts on first sight without emitting and stamps only verdicts that appear
  later (from the system note). Verdicts during DevPit downtime or before first
  sight never advance the GitLab ranking clock — a bounded, honest gap rather
  than over-promotion at poll time.
- On GitHub a broken build moves nothing in the ranking; accepted, since a
  non-gating failure still shows the `failing_checks` marker and a gating one
  shows as Blocked.
