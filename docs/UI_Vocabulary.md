# UI Vocabulary — tags, markers, hovers at a glance

The one-page visual reference for everything a row in the attention list can
show. Seed for future user documentation. Decision record:
`ADR/ADR-0016_Presentation_And_Ranking.md`; wire shapes: `docs/REST_API.md`.

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

## Signals (v0.1.5 — drive ranking, fixed precedence)

Precedence highest → lowest; an item may carry several, the highest ranks it.
The signal vocabulary is one word-set regardless of your role; conditions stay
role-aware where the fact is inherently about a role (see role scope notes).

| chip | role scope | it means | hover |
|---|---|---|---|
| `changes_requested` | author | a reviewer requested changes | for {N} |
| `review_requested`  | reviewer | your review was requested, not submitted | for {N} |
| `blocked`           | author | provider merge gate not satisfied | for {N} · provider says: {gate_detail} |
| `mentioned`         | anyone | you were @-mentioned (shows ×N if repeated) | for {N} · clears when the item closes |
| `ready_to_merge`    | author | gate satisfied, mergeable now | for {N}; with red checks: · a non-required check is red |
| `auto_merge_armed`  | author | provider auto-merge / merge-when-pipeline-succeeds is armed | for {N} |
| `checks_running`    | author | a pipeline is in progress | for {N} |
| `checking`          | any (role-neutral) | gate is `unknown` — no verdict yet; replaces the bare row | for {N} |
| `review_submitted`  | reviewer | you already reviewed; ball with author | for {N} |

`blocked` defers entirely to the provider's merge gate — DevPit never
re-derives org rules. That is why it is trustworthy.

**Provider parity for best-effort signals:**
- `auto_merge_armed`: ships on both GitHub (GraphQL `autoMergeRequest{enabledAt}`,
  non-null ⇒ armed; degrades to false for PATs that cannot read it) and GitLab
  (REST `merge_when_pipeline_succeeds`).
- `checks_running`: **GitLab-only** — GitHub cannot report an in-progress gating
  pipeline (it hides inside `blocked`); `checks_running` is a documented ✗ gap
  on GitHub. We do not reconstruct it from `statusCheckRollup`.

## Diagnostic badges (cosmetic — explain, never move)

| badge | meaning (when `blocked`) | GitHub | GitLab | hover |
|---|---|---|---|---|
| `conflict` | manual conflict resolution needed | ✓ `has_conflicts` (REST) | ✓ `has_conflicts` (REST) | for {N} |
| `rebase` | mechanical rebase unlocks it | ⚠ `behind` — only when repo requires up-to-date branches | ✓ `shouldBeRebased` (GraphQL) | for {N} |
| `checks failing` | CI / pipeline red | ⚠ non-gating CI only (`unstable`); gating-CI failures hide inside `blocked` | ✓ `headPipeline` (GraphQL, any pipeline) | for {N} |
| `draft` | provider draft; merge gate suspended | draft flag | draft flag | for {N} |
| `missing approvals` | required approvals not met | ✓ `reviewDecision` (GraphQL) | ✓ `approved` (GraphQL) | for {N} |
| `discussions` | unresolved threads gate the merge | ✗ gate rule unreadable for non-admins | ✓ `blocking_discussions_resolved` (REST) | for {N} |
| `policy` | security/org policy denies merge | ✗ no equivalent signal | ⚠ `policies_denied` etc. — masked when a co-present reason wins the verdict field | for {N} |

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

Bands sort fresh → stale → old; within a band the normal order (state
precedence, newest first) applies. The pinned zone is exempt — pins never sink,
but they still show their age tag and pin age.

## Row context (tints & meta-row)

Facts a row carries without a tag:

| what | where | means |
|---|---|---|
| blue row tint | whole row | you authored the item (the connection's identity matches the author) — the only mark of authorship; the tag vocabulary is the same whatever your role |
| amber row tint | whole row | the `old` age tier (idle > 30 days) — see Age tags above |
| "N approved" | meta-row, between author and timestamp | raw count of reviewers who approved; informational only (never moves the item), shown when N > 0, hidden on drafts |

## Hover-text rule

A tooltip must say something the tag label doesn't. Every tag shows how long
its condition has held ("for 3d" — minutes, then hours past 1h, then days
past 1d), computed from the item's observed history (`since` map in the API);
extra facts are appended only where they exist. No tag restates its own name.
