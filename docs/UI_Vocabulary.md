# UI Vocabulary — tags, markers, hovers at a glance

The one-page visual reference for everything a row in the attention list can
show. Seed for future user documentation. Decision record:
`ADR/ADR-0016_Presentation_And_Ranking.md`; wire shapes: `docs/REST_API.md`.

**Status:** describes the v0.1.1 design
(`docs/plans/2026-07-10_marker_vocabulary_and_age_bands.md`). Items marked ⏳
are not built yet; unmarked behavior is live in v0.1.

## Anatomy of the list

```
┌─ Handle next ─────────────────────────────────────────────────────┐
│ 📌 Fix flaky auth test        [needs_review] [conflict] [stale]   │  ← pins: any age,
│    repo · author · 2w ago · pinned 3w ago ⏳                       │    flag order
├─ fresh (idle < 7d) ───────────────────────────────────────────────┤
│    Add rate limiter           [needs_review]                      │  ← state precedence,
│    Retry queue draining       [changes_requested] [checks failing]│    newest first
│    Bump SDK                   [ready to merge · optional checks   │
│                                red ⏳]                             │
├─ stale (idle 7–30d) ⏳ ───────────────────────────────────────────┤
│    Migrate CI config          [blocked] [rebase] [stale]          │
├─ abandoned (idle > 30d) ⏳ ────────────────────────────────────────┤
│    Dark launch flags          [waiting_on_author] [abandoned]     │
└───────────────────────────────────────────────────────────────────┘
```

Three kinds of tags, three visual weights ⏳:

```
[STATE CHIP]   colored, primary   — why the item ranks where it does
[diag badge]   alert styling      — why it can't merge (cosmetic, never ranks)
[age tag]      muted              — how long it has sat (bands the list ⏳)
```

## Attention states (drive ranking — closed set, fixed precedence)

Precedence highest → lowest; an item may carry several, the first ranks it.

| chip | you are | it means | hover ⏳ |
|---|---|---|---|
| `needs_review` | reviewer | your review was requested, not submitted | for {N} |
| `changes_requested` | author | a reviewer requested changes | for {N} |
| `blocked` | author | provider merge gate not satisfied | for {N} · provider says: {gate_detail} |
| `ready_to_merge` | author | gate satisfied, mergeable now | for {N}; with red checks: · a non-required check is red |
| `mentioned` | anyone | you were @-mentioned (shows ×N if repeated) | for {N} · clears when the item closes |
| `waiting_on_author` | reviewer | you already reviewed; ball with author | for {N} |

`blocked` defers entirely to the provider's merge gate — DevPit never
re-derives org rules. That is why it is trustworthy.

## Diagnostic badges (cosmetic — explain, never move)

| badge | it means | GitHub | GitLab | hover ⏳ |
|---|---|---|---|---|
| `conflict` ⏳ | manual conflict resolution needed | `dirty` | `conflict` | for {N} |
| `rebase` ⏳ | mechanical rebase / update-branch | `behind` | `need_rebase` | for {N} |
| `checks failing` | CI / pipeline red (CI-only in v0.1.1 ⏳) | `unstable` | `ci_must_pass` ⏳ | for {N} |
| `draft` | provider draft; merge gate suspended | draft flag | draft flag | for {N} |

Honest gaps: GitHub gating-CI failures hide inside its opaque `blocked`
status (no badge — the item still ranks blocked); GitLab non-gating CI
failures are invisible (no pipeline fetch); GitHub only reports `behind`
when the target branch requires up-to-date branches — no `rebase` badge is
not proof of freshness.

## Age tags (the one exception — they band the list ⏳)

| tag | idle time | effect ⏳ | hover |
|---|---|---|---|
| `stale` | > 7 days, ≤ 30 | sinks below all fresh items | No activity for {N} (threshold: 7 days) |
| `abandoned` ⏳ | > 30 days | sinks to the very bottom | No activity for {N} (threshold: 30 days) |

Mutually exclusive. Bands sort fresh → stale → abandoned; within a band the
normal order (state precedence, newest first) applies. The pinned zone is
exempt — pins never sink, but they still show their age tags and pin age.

## Hover-text rule ⏳

A tooltip must say something the tag label doesn't. Every tag shows how long
its condition has held ("for 3d" — minutes, then hours past 1h, then days
past 1d), computed from the item's observed history; extra facts are
appended only where they exist. No tag restates its own name.
