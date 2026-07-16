# UI Vocabulary — tags, markers, hovers at a glance

The one-page visual reference for everything a row in the attention list can
show. Seed for future user documentation. Decision record:
`ADR/ADR-0016_Presentation_And_Ranking.md`; exact signal conditions and
ranking semantics: `docs/Attention_Engine.md`; wire shapes: `docs/REST_API.md`.

**Status:** all items below are live (v0.1.1–v0.1.5).

## Anatomy of the list

```
┌─ Handle next ─────────────────────────────────────────────────────┐
│ 📌 Fix flaky auth test        [review_requested] [conflict] [stale]│  ← pins: any age,
│    repo · author · 2w ago · pinned 3w ago                          │    flag order
├─ fresh (idle < 7d) ───────────────────────────────────────────────┤
│    Add rate limiter           [review_requested]                   │  ← signal precedence,
│    Retry queue draining       [changes_requested] [checks failing] │    newest first
│    Bump SDK                   [ready to merge · optional checks    │
│                                red]                                │
├─ stale (idle 7–30d) ──────────────────────────────────────────────┤
│    Migrate CI config          [blocked] [rebase] [stale]           │
├─ old (idle > 30d) ────────────────────────────────────────────┤
│    Dark launch flags          [review_submitted] [stale]      │  ← amber row tint
└───────────────────────────────────────────────────────────────────┘
```

Three kinds of tags, three visual weights:

```
[STATE CHIP]   colored, primary   — why the item ranks where it does
[diag badge]   alert styling      — why it can't merge (cosmetic, never ranks)
[age tag]      muted              — how long it has sat (bands the list)
```

Below these, a separate class: **provider labels**. The labels an MR/PR carries
on the provider (GitLab MR labels, GitHub PR labels) render as **plain text
label names, `#`-prefixed and muted** — on a dedicated row below the meta-row.
These are the team's own taxonomy, not a DevPit verdict, so they are
deliberately distinct from the signal chips above. They refresh on reconcile
only and show even on muted rows. See
`ADR/ADR-0016_Presentation_And_Ranking.md`.

## Signals (v0.1.5 — drive ranking, fixed precedence)

Precedence highest → lowest; an item may carry several, the highest ranks it.
The signal vocabulary is one word-set regardless of your role; conditions stay
role-aware where the fact is inherently about a role (see role scope notes).

| chip | role scope | it means | hover |
|---|---|---|---|
| `changes_requested` | author | a reviewer requested changes | {N} |
| `review_requested`  | reviewer / sole approver | your review was requested (or implied — you are the only merge path), not submitted | {N} |
| `blocked`           | author / sole approver | provider merge gate not satisfied | {N} · provider says: {gate_detail} |
| `mentioned`         | anyone | you were @-mentioned (shows ×N if repeated) | {N} · clears when the item closes |
| `ready_to_merge`    | author / sole approver | gate satisfied, mergeable now | {N}; with red checks: · a non-required check is red |
| `auto_merge_armed`  | author / sole approver | provider auto-merge / merge-when-pipeline-succeeds is armed | {N} |
| `checks_running`    | author / sole approver | a pipeline is in progress | {N} |
| `checking`          | any (role-neutral) | gate is `unknown` — no verdict yet; replaces the bare row | {N} |
| `review_submitted`  | reviewer | you already reviewed; ball with author | {N} |

`blocked` defers entirely to the provider's merge gate — DevPit never
re-derives org rules. That is why it is trustworthy.

The `blocked` chip is **suppressed** when a visible diagnostic badge (below)
is the specific reason the gate names — strict match against `gate_detail`,
not "any badge visible" (GitLab's `missing approvals` is near-ubiquitous and
would otherwise erase the chip even when the operative blocker is something no
badge shows, e.g. GitHub's opaque `blocked` gate or a GitLab tier gate). The
hover above (`{N} · provider says: {gate_detail}`) applies whenever the chip
renders.

**Provider parity for best-effort signals:**
- `changes_requested`: a reviewer's changes-requested verdict on both, via the
  GraphQL join — GitHub (`reviewDecision == CHANGES_REQUESTED`) and GitLab
  (`reviewers.nodes.mergeRequestInteraction.reviewState == REQUESTED_CHANGES`).
- `auto_merge_armed`: ships on both GitHub (GraphQL `autoMergeRequest{enabledAt}`,
  non-null ⇒ armed; degrades to false for PATs that cannot read it) and GitLab
  (REST `merge_when_pipeline_succeeds`).
- `checks_running`: **GitLab-only** — GitHub cannot report an in-progress gating
  pipeline (it hides inside `blocked`); `checks_running` is a documented ✗ gap
  on GitHub. We do not reconstruct it from `statusCheckRollup`.

## Diagnostic badges (cosmetic — explain, never move)

| badge | meaning (when `blocked`) | GitHub | GitLab | hover |
|---|---|---|---|---|
| `conflict` | manual conflict resolution needed | ✓ `has_conflicts` (REST) | ✓ `has_conflicts` (REST), refined by `conflicts` (GraphQL) | {N} |
| `rebase` | mechanical rebase unlocks it | ⚠ `behind` — only when repo requires up-to-date branches | ✓ `shouldBeRebased` (GraphQL) | {N} |
| `checks failing` | CI / pipeline red | ⚠ non-gating CI only (`unstable`); gating-CI failures hide inside `blocked` | ✓ `headPipeline` (GraphQL, any pipeline) | {N} |
| `draft` | provider draft; merge gate suspended | draft flag | draft flag | {N} |
| `missing approvals` | required approvals not met | ✓ `reviewDecision` (GraphQL) | ✓ `approved` (GraphQL) | {N} |
| `discussions` | unresolved threads gate the merge | ✗ gate rule unreadable for non-admins | ✓ `blocking_discussions_resolved` (REST) | {N} |
| `policy` | security/org policy denies merge | ✗ no equivalent signal | ⚠ `policies_denied` etc. — masked when a co-present reason wins the verdict field | {N} |

Legend: ✓ full signal · ⚠ partial/conditional · ✗ structurally unavailable.

GitLab shows every applicable reason at once (except the `policy` residual);
GitHub shows what its API discloses. The old "GitLab non-gating CI invisible"
gap is closed by the `headPipeline` join; GitHub's gating-CI opacity remains.

Both providers use a batched GraphQL join (one query per sync cycle, MRs/PRs
via aliases) for the GraphQL-sourced signals; on failure the join degrades
gracefully (badges absent, sync_log entry).

## Age tags (the one exception — they band the list)

Two idle tiers, mutually exclusive. Both render the muted **Stale** tag; the
`old` tier adds a warm amber row tint rather than a separate "Old" label
(`ADR/ADR-0016_Presentation_And_Ranking.md`).

| tier | idle time | effect | shown as | hover |
|---|---|---|---|---|
| `stale` | > 7 days, ≤ 30 | sinks below all fresh items | "Stale" tag | No activity for {N} (threshold: 7 days) |
| `old` | > 30 days | sinks to the very bottom | "Stale" tag + amber row tint | No activity for {N} (threshold: 30 days) |

Bands sort fresh → stale → old; within a band the order is pure recency
(most-recent update first) — signal precedence and the reviewed-done mute do not
reorder it. The pinned zone is exempt — pins never sink, but they still show
their age tag and pin age.

## Row context (tints & meta-row)

Facts a row carries without a tag:

| what | where | means |
|---|---|---|
| blue row tint | whole row | you authored the item (the connection's identity matches the author) — the only mark of authorship; the tag vocabulary is the same whatever your role |
| amber row tint | whole row | the `old` age tier (idle > 30 days) — see Age tags above |
| de-emphasized row | whole row | reviewed-done (`muted`): you are a reviewer — not the author or sole approver — who has submitted your review, so nothing is left for you — the row dims and suppresses its chips (a display cue only; muting does not change its position, which is age band + recency); full opacity on hover |
| "N approved" / "you + N approved" | meta-row (last field) | count of reviewers who approved; informational only (never moves the item), shown when N > 0, hidden on drafts. When you are an approver (`my_review_state == "approved"`) it reads "you approved" or "you + N approved" |

## Hover-text rule

A tooltip must say something the tag label doesn't. Every tag shows how long
its condition has held ("3 days ago" — minutes, then hours past 1h, then days
past 1d), computed from the item's observed history (`since` map in the API);
extra facts are appended only where they exist. No tag restates its own name.

## Why is this item here, in this order?

The list answers "what needs me now?". A row earns its place top-to-bottom by
four rules, in order:

1. **The pinned "Handle next" zone comes first.** Anything you pinned sits at the
   top in the order you pinned it, exempt from all auto-ranking (it still shows
   its age tag and pin age). A pin is a deliberate choice; DevPit never reorders
   it.
2. **Then age bands.** Below the pins the list splits into **fresh** (idle < 7d),
   **stale** (7–30d), and **old** (> 30d), and always sorts fresh → stale → old.
   Fresh work stays on top; rot sinks. This is the one place "how long it sat"
   changes ordering.
3. **Newest first within a band.** Inside a band the most recently active item
   comes first (its newest signal, else the latest provider update).
4. **Signal precedence orders the *chips*, not the items.** When a row carries
   several signals the highest-precedence one leads (see the Signals table
   above); the rest ride along. Precedence picks the headline chip — it does not
   move the row.

Worked example — four open items:

- **A** — you pinned it yesterday; idle 3 weeks.
- **B** — authored, changes requested; idle 2 days.
- **C** — your review requested; idle 1 day.
- **D** — authored, ready to merge; idle 40 days.

Order: **A** first (pinned, exempt). Then the **fresh** band — **C** (idle 1d)
above **B** (idle 2d), newest activity first, *not* B's higher-precedence
"Changes Requested" chip. Then **D** last, alone in the **old** band despite
being ready to merge — age sinks it. B's "Changes Requested" only makes it B's
leading chip; it never lifts B above C.

The exact band thresholds, the precedence list, and the fold conditions are in
`docs/Attention_Engine.md` (the code is authoritative). This is the user-facing
story, not a second copy of the rules.

## Why is something missing?

DevPit shows only open items **you are involved in**, and only what your token
and the provider expose. If you expected an item and don't see it:

- **Involvement.** An item appears only if you authored it, are a
  reviewer/assignee, are the sole account that can merge it, or were @-mentioned
  on it — the sync scopes plus mention signals (`docs/Attention_Engine.md`, list
  membership). A PR you merely watch, or one in a repo you hold no role on, never
  enters the list. Merged/closed items drop out, as do items DevPit can no longer
  see (lost access, repo deleted).
- **Provider asymmetries.** The two forges do not expose the same facts, so a
  signal or badge can be present on one and structurally absent on the other —
  see the parity notes in the Signals and Diagnostic-badges tables above.
  Notably `checks_running` is **GitLab-only** (GitHub hides an in-progress gating
  pipeline inside `blocked`), and GitHub gives no `discussions` or `policy`
  badge. A missing badge means "the provider can't say", never "all clear".
- **Token reach.** On GitHub the token *kind* changes what you see: a
  fine-grained PAT cannot read the notifications feed, so **mentions never
  appear** and other fast signals wait for the 15-minute reconcile
  (`docs/Token_Setup.md`). A missing item can be a token limitation, not a bug.
