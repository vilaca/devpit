# Attention Engine

> **Status:** the fold (read-time computation of buckets and ranking) is
> **implemented** in `internal/attention`. The user-facing presentation
> (pinned zone, tags, filters, marker vocabulary, age bands, blocked
> diagnostic badges) is **implemented** in `frontend/` through v0.1.5.
> Decision: `ADR/ADR-0005_Event_Based_Attention_Engine.md` and
> `ADR/ADR-0016_Presentation_And_Ranking.md`.

The read side of DevPit. It folds the event log (`docs/Event_Taxonomy_and_Storage.md`)
into a single ranked list of WorkItems, each tagged with the attention states
it currently satisfies. It computes nothing on the write path — buckets are
derived at read time from the latest facts and signals.

**List membership.** Every open item you are involved in appears — the sync
scopes (assigned/authored) plus mention signals define involvement, so an open
item in the log is one you have a stake in. States drive tags and ranking, but
an item that matches *no* state still appears as a plain row (an authored one
carries the blue "mine" tint; a draft carries the Draft marker). Only
merged/closed items and items removed after their last snapshot drop out. This
is deliberate: an authored MR quietly waiting on reviewers, or one whose merge
gate the provider has not yet computed (`unknown`), should not vanish.

The bucket predicates and the precedence order are **direct code**
(`internal/attention/states.go`, `internal/attention/fold.go`); this spec is
the design behind them. Where the two ever disagree, the code is authoritative
for the exact conditions.

## Signals (v0.1.5)

Nine signals replacing the former six attention states. A WorkItem may carry
several at once; they render as chips. The signal *vocabulary* is one word-set —
no separate author/reviewer labels, no authorship tag (the blue tint carries
authorship). The *conditions* stay role-aware where the fact is inherently about
a role (see Role scope below).

The nine signal wire values and their precedence order are direct code — the
`State` consts and `precedence` var in
[`internal/attention/states.go`](../internal/attention/states.go) (index 0 is
the leading chip; precedence orders chips, not item ranking — see Ranking).
Their human labels, highest precedence first:

| wire value | label |
|---|---|
| `changes_requested` | Changes Requested |
| `review_requested`  | Review Requested  |
| `blocked`           | Blocked           |
| `mentioned`         | Mentioned         |
| `ready_to_merge`    | Ready to Merge    |
| `auto_merge_armed`  | Auto-merge Armed  |
| `checks_running`    | Checks Running    |
| `checking`          | Checking          |
| `review_submitted`  | Review Submitted  |

The exact firing condition for each signal is **direct code** — the `matches`
switch in `internal/attention/states.go`; the plain-language meaning and role
scope are below. An item carries **every** signal that applies; its
highest-precedence signal leads, the rest ride as additional tags.

### Role scope

The gate signals (Blocked, Ready to Merge, Auto-merge Armed, Checks Running)
describe an MR that **cannot progress without you** — you authored it, or you
are its `sole_approver` (the only account that can merge; always-on, never
muted — `ADR/ADR-0016_Presentation_And_Ranking.md`). Changes Requested fires
both author-side (a reviewer's verdict on your MR) and reviewer-side (your own
"changes requested" verdict on someone else's MR); the shared chip's row
brightness conveys whose court the ball is in. Review Requested is
reviewer-relative and also fires for a sole
approver whose review isn't done — an implicit review obligation, no explicit
request needed. Review Submitted is reviewer-relative. Mentioned is any-role.
**Checking is role-neutral**: it
fires on any involved item whose gate is `unknown`, including a draft you only
review, so it can backstop a row that would otherwise be bare.

### Edge definitions

- **Blocked defers to the provider's merge gate.** DevPit does not re-derive an
  organization's rules (required checks, approvals, conflicts) — it reports what
  the gate reports. "No approvals" is Blocked only where the org *requires*
  approvals; a just-opened PR awaiting review is not Blocked by default. The
  merge-gate value mappings per provider live in `docs/Provider_API_Analysis.md`.
- **Non-gating check failures are a failure *notification*, not Blocked.** Any
  red check is surfaced (the `failing_checks` marker on the item) so nothing is
  missed, but only merge-gating failures put a PR in the Blocked signal —
  keeping Blocked trustworthy.
- **Drafts carry Checking, not Blocked or Ready to Merge.** Both providers
  report gate `unknown` for drafts, so a draft's gate/verdict signals are
  suppressed (they keep their `!Draft` guard). An authored draft carries
  `["checking"]` plus the Draft marker; it still picks up Mentioned and
  review signals where they apply. Normal author rules resume once marked ready.
- **Authored MRs are never bare** — gate `ready` → `ready_to_merge`, gate
  `blocked` → `blocked`, gate `unknown` → `checking`. A non-authored involved
  item with a known gate and no reviewer or mention signal (e.g. a pure assignee
  on a ready MR) may still render marker-only (empty `states` array).
- **Changes Requested (author) vs Review Submitted (reviewer)** are the two
  sides of a review round-trip. Changes Requested is highest-precedence (your
  turn); Review Submitted is lowest-precedence (informational — the stale badge is the
  safety net for round-trips the author has forgotten). Where the org's merge
  gate enforces approvals, Changes Requested co-occurs with Blocked; the item
  carries both tags and ranks by Changes Requested.
- **Checking does not flap.** Transient gate values never reach storage; the
  synthesizer carries the last known gate forward. A previously-blocked MR under
  transient recompute keeps gate `blocked` and does not drop to `checking`.

## Markers (v0.1.1–v0.1.2)

Markers are diagnostic booleans on the item snapshot, normalized per provider.
They explain *why* an item is in its state but never change the state itself —
with one deliberate exception: age bands (see Ranking below). Since v0.1.2 the
GitLab markers no longer read a single `detailed_merge_status` value — they come
from independent REST fields plus a batched GraphQL join, so every applicable
reason shows at once (`docs/UI_Vocabulary.md` has the provider-parity table).

The marker set is the diagnostic boolean fields on `WorkItem`
(`internal/attention/fold.go`) — `failing_checks` (CI/checks red),
`merge_conflict` (manual conflict resolution needed), `needs_rebase` (mechanical
rebase/update-branch needed), `needs_approval` (required approvals not met),
`unresolved_discussions` (unresolved threads gate the merge), `policy_denied`
(security/org policy denies merge), and `draft`. The per-provider derivation of
each — which GitHub/GitLab field feeds it and where a provider is structurally
blind — is the parity table in `docs/UI_Vocabulary.md` (raw API facts in
`docs/Provider_API_Analysis.md`), not restated here.

The gate mapping (`mergeGate` in each provider) is **unchanged** — `dirty`,
`behind`, `conflict`, `need_rebase`, and `ci_must_pass` still produce gate
`blocked`; `unstable` still produces `ready`. Markers are additional fields,
not a re-derivation of the gate.

### Onset timestamps (`since` map)

Each active marker and state tag carries an onset timestamp: the start of the
**latest contiguous run** of snapshots in which the condition has held,
computed by walking the item's `item.observed` events newest → oldest. The
`since` map is keyed by the wire name of each active tag (e.g.
`"review_requested"`, `"merge_conflict"`). Exception: `mentioned` onset is the
earliest mention signal's `coalesce(occurred_at, observed_at)` — the state
never clears while the item is open.

Accuracy is bounded by when DevPit first observed the item and by the poll
cadence. Onset is not shown for `stale` and `old` (their duration is
already in the hover text).

### Known gaps

- **GitHub gating-CI failures** are hidden inside `mergeable_state: "blocked"` and
  cannot be distinguished from other block causes; they simply rank `blocked`
  with no CI marker.
- **GitHub `behind`** is reported only when branch protection *requires*
  up-to-date branches; absence of `needs_rebase` is not proof of freshness.

(The earlier "GitLab non-gating CI invisible" gap was closed in v0.1.2 by the
`headPipeline` GraphQL join — GitLab now surfaces any red pipeline.)

## Ranking

Age bands plus recency — **no numeric score, no configuration** (revised
2026-07-13; signal precedence no longer ranks items). The principle is that fresh
work stays on top and, within a tier, the list mirrors what actually just moved.

- **Age bands sort the list first** (the single deliberate marker exception):
  fresh, then stale, then old last. Fresh work stays on top; rot sinks.
- **Within a band: most-recent-update-first**, where an item's timestamp is its
  newest signal (falling back to the latest snapshot's provider-updated time).
  Review verdicts count as recency here: `signal.approved` and
  `signal.changes_requested` are rank-only signals that advance this timestamp
  without adding a chip (`ADR/ADR-0016_Presentation_And_Ranking.md`,
  `docs/Event_Taxonomy_and_Storage.md`). Item ID is the stable final tiebreak.
  Neither signal precedence nor the reviewed-done mute reorders a band — a
  stateless or muted item sorts by recency like any other; signals survive only
  as chips (precedence orders the chips).
- Two age tiers, both constants in `fold.go`, mutually exclusive
  (`!old && stale` guards the stale tier): `stale` once an item's age exceeds
  7 days, `old` once idle more than 30 days. Both render the muted **"Stale"**
  tag; the `old` tier is distinguished by a warm amber row tint, not a separate
  "Old" label (`ADR/ADR-0016_Presentation_And_Ranking.md`).
- **Pinned zone is exempt** from band sorting — a pin is a deliberate user act.
  Pinned items still display their age tags and pin age so rot cannot hide at
  the top.
- **Repeated same-type signals collapse** to one tag with a count
  ("Mentioned ×3"); the individual signals remain only in the stored event
  log — the UI shows the count, with hover adding the onset.

## Presentation (Implemented, v0.1.1–v0.1.4)

- A **single ranked list**, one row per item, states as tags; buckets are
  optional client-side filters, not the primary layout. Two filters diverge from
  the one-signal-per-bucket mapping (`ADR/ADR-0016_Presentation_And_Ranking.md`):
  a `mine` filter (first chip) narrows to items you authored,
  and `mentioned` also gathers items where you are a reviewer (via the `my_roles`
  wire field). The fold is unchanged — `my_roles` is a projection of an existing
  fact.
- A **pinned "Handle next" zone** at the top holds user-flagged items in flag
  order, lifted out of the auto-ranked list (never shown twice). The flag is
  local-only (`ADR/ADR-0017_Read_Only_Action_Model.md`).
- **Three visual tiers of tags**: state chips (primary) > diagnostic badges
  (conflict, rebase, failing checks, missing approvals, discussions, policy;
  alert styling) > the muted **Stale** age tag. The `old` tier carries no
  separate label — it is shown by a warm amber row tint.
- **Hover text** adds information beyond the tag label: duration ("for 3d")
  from the `since` map, plus tag-specific extras (blocked shows the provider's
  raw gate detail; ready_to_merge with failing_checks notes the non-required
  check; mentioned notes it never clears while open).
- **Pin age**: pinned items show "pinned N ago" from `flagged_at`.
- **Row tints** carry context without a badge: a blue tint on items authored by
  the connection's identity, a warm amber tint on the `old` tier.
- **Approved count**: the meta-row shows "N approved" when at least one reviewer
  has approved — a raw count, informational only (never moves the item), hidden
  on drafts.

The wire shape of the ranked list is specified in `docs/REST_API.md`.
