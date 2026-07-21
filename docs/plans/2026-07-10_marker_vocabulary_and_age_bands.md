# Implementation plan: marker vocabulary + age bands (v0.1.1)

Decision record: `ADR/ADR-0016_Presentation_And_Ranking.md` (sections dated
2026-07-10). This plan is self-contained — everything needed to implement is
here or in the referenced code. Work through the steps in order; each step
compiles and passes tests on its own.

## Invariants — do not violate

- **Attention states and their precedence do not change.** No edits to
  `internal/attention/states.go` semantics. `blocked` keeps deferring entirely
  to the provider merge gate.
- **Markers never affect ranking**, with exactly one exception: the age bands
  in step 4. `merge_conflict`, `needs_rebase`, `failing_checks`, `draft` are
  purely cosmetic.
- Provider-specific vocabulary (`mergeable_state`, `detailed_merge_status`
  values) stays inside the provider packages. Core (`internal/`) only sees the
  normalized booleans and the opaque `gate_detail` string.

## Semantics being implemented

| marker | meaning | GitHub (`mergeable_state`) | GitLab (`detailed_merge_status`) |
|---|---|---|---|
| `failing_checks` | CI/checks red | `unstable` **only** (today it also includes `dirty` — remove that) | `ci_must_pass` (new — GitLab sets it for the first time) |
| `merge_conflict` (new) | manual conflict resolution needed | `dirty` | `conflict` |
| `needs_rebase` (new) | mechanical rebase / update-branch | `behind` | `need_rebase` |

Gate mapping (`mergeGate` in both providers) is **unchanged** — `dirty`,
`behind`, `conflict`, `need_rebase`, `ci_must_pass` still produce gate
`blocked`; `unstable` still produces `ready`. The markers are additional
fields, not a re-derivation.

Age tiers (fold-side, from the item's ranking timestamp `updated_at`):

- `stale` = idle more than 7 days and at most 30 days.
- `abandoned` (new) = idle more than 30 days.
- Mutually exclusive: never both true.
- Thresholds follow the existing pattern: exported defaults, overridable via
  arguments, non-positive disables that tier.

Age **bands** (the one ranking change): the auto-ranked list sorts by band
first — fresh (0), stale (1), abandoned (2) — then the existing key (state
precedence, newest-first, item ID). The pinned zone is exempt: pins stay on
top in flag order regardless of age.

Known gaps — document, do not try to fix:

- GitHub gating-CI failures are hidden inside `mergeable_state: "blocked"` and
  cannot be distinguished; they simply rank `blocked` with no CI marker.
- GitLab non-gating CI failures are invisible (no pipeline fetch in v0.1).
- GitHub only reports `behind` when branch protection requires up-to-date
  branches; absence of `needs_rebase` is not proof of freshness.

## Step 1 — SDK payload

`sdk/provider.go` (~line 156, next to `Gate`/`GateDetail`/`FailingChecks`):
add to `ItemObservedPayload`:

```go
MergeConflict bool `json:"merge_conflict"`
NeedsRebase   bool `json:"needs_rebase"`
```

Old stored events lack these fields (unmarshal to false). The fold always
reads the latest snapshot, so items pick the markers up on the next poll
cycle — **no migration**.

## Step 2 — Providers

`provider/github/normalize.go` (`observedFromPull`, ~line 94):

```go
FailingChecks: pr.MergeableState == "unstable",
MergeConflict: pr.MergeableState == "dirty",
NeedsRebase:   pr.MergeableState == "behind",
```

`provider/gitlab/normalize.go` (~line 101, next to `Gate:`):

```go
FailingChecks: mr.DetailedMergeStatus == "ci_must_pass",
MergeConflict: mr.DetailedMergeStatus == "conflict",
NeedsRebase:   mr.DetailedMergeStatus == "need_rebase",
```

Update/extend the providers' cassette-based tests (`testdata/fixtures/`) to
assert the new fields for the relevant statuses.

## Step 3 — Fold: fields + age tiers

`internal/attention/fold.go`:

- Add `DefaultAbandonedThreshold = 30 * 24 * time.Hour` next to
  `DefaultStaleThreshold` (line 29).
- `WorkItem` gains:
  - `MergeConflict bool` and `NeedsRebase bool` (copied from facts, like
    `FailingChecks` at line 179),
  - `GateDetail string` (`json:"gate_detail,omitempty"`, copied verbatim from
    `facts.GateDetail` — powers the blocked-tag tooltip),
  - `Abandoned bool`,
  - `FlaggedAt *time.Time` (`json:"flagged_at,omitempty"`, set only on pinned
    items — see step 5).
- `Fold` and `List` take a second threshold argument
  (`staleThreshold, abandonedThreshold time.Duration`). Tier logic in
  `foldItem` (replacing line 176):

```go
idle := now.Sub(updatedAt)
abandoned := abandonedThreshold > 0 && idle > abandonedThreshold
stale := !abandoned && staleThreshold > 0 && idle > staleThreshold
```

  (If the abandoned tier is disabled, items older than 30 days are simply
  `stale` — the `!abandoned` guard handles this automatically.)

## Step 3b — Fold: onset timestamps (`since` map)

Tooltip principle (ADR-0016): hover text must add information beyond the tag
label; the universal payload is how long the tag's condition has held.

In `foldItem`, compute an onset timestamp per active state and per active
diagnostic marker (`merge_conflict`, `needs_rebase`, `failing_checks`,
`draft`). Onset = the start of the **latest contiguous run** of snapshots in
which the condition holds, walking the item's `item.observed` events
newest → oldest (unmarshal each snapshot's payload; stop extending a run at
the first snapshot where the condition is false). Use each snapshot's
`provider_updated_at` when parseable, else its `observed_at`, as the run-start
time. Exception: `mentioned` is signal-driven — its onset is the earliest
mention signal's `coalesce(occurred_at, observed_at)` (the state never clears
while the item is open).

Accepted accuracy bounds (document, don't fight): onset is clamped to when
DevPit first observed the item, and resolution is the poll cadence.

`WorkItem` gains `Since map[string]time.Time` (`json:"since,omitempty"`),
keyed by the wire names of states and markers (`"needs_review"`,
`"merge_conflict"`, …). Only currently-active tags appear in the map.

## Step 4 — Fold: age bands in sorting

`sortItems` (`internal/attention/fold.go:231`): compare age band before state
precedence. Band: 0 fresh, 1 stale, 2 abandoned (derive from the two flags).
Keep the rest of the comparator unchanged. `pin()` is untouched — banding
applies only to the auto-ranked remainder.

## Step 5 — Pin age

`internal/storage/storage.go:476`: `ListHandleNext` returns only item IDs.
Change it (or add a sibling) to also return `flagged_at` (stored as RFC3339
TEXT in the `handle_next` table, `schema.go:44`). Thread the timestamps
through `attention.List` → `pin()` so pinned WorkItems carry `FlaggedAt`.

## Step 6 — API

`internal/api/attention.go` (DTO ~line 34, mapping ~line 83): add
`merge_conflict`, `needs_rebase`, `abandoned`, `gate_detail` (omitempty),
`flagged_at` (omitempty), `since` (omitempty map, step 3b). Update call sites of `attention.List`
(`attention.go:40`) and `api.New` (`server.go`, `api_test.go:33`,
`cmd/devpit/main.go:78`) for the second threshold argument — pass
`attention.DefaultAbandonedThreshold`.

## Step 7 — Frontend

Files: `frontend/src/lib/types.ts` (add the new WorkItem fields),
`frontend/src/components/StateTags.svelte`, `WorkItemRow.svelte`,
`PinnedZone.svelte`, `frontend/src/app.css`.

- **Three visual tiers of tags**: state chips (primary, as today) >
  diagnostic badges (`merge_conflict` → "conflict", `needs_rebase` →
  "rebase", `failing_checks` → "failing checks", alert styling) > age tags
  (`stale`, `abandoned`, muted styling).
- **Hover text — one uniform rule** (rewrite `titleFor` and the marker
  titles in `StateTags.svelte`): every state chip and diagnostic badge gets
  `title` = `for {relativeTime(since[tag])}` (the `since` map from the API;
  omit the tooltip if the key is absent). Formatting comes from the existing
  `relativeTime` helper (minutes, hours past 1h, days past 1d). Appended
  extras:
  - `blocked`: `for {N} · provider says: {gate_detail}` (omit the suffix when
    `gate_detail` is empty; raw provider vocabulary is fine in a tooltip).
  - `ready_to_merge` when `failing_checks` is also set: `for {N} · a
    non-required check is red`.
  - `mentioned`: `for {N} · clears when the item closes`.
  - `stale` / `abandoned`: keep the existing dynamic form — `No activity for
    {relativeTime(updated_at)} (threshold: 7 days)` / `(threshold: 30 days)`
    — these two carry their duration in the text already and get no `since`
    entry lookup.
  Never restate the tag label in the tooltip; the duration and extras are the
  whole content.
- **Ready-but-red rendering**: when states include `ready_to_merge` AND
  `failing_checks` is true, render the pair as one combined phrase — "ready
  to merge · optional checks red" — so the pairing looks intentional, not
  contradictory.
- **Pinned zone**: render age tags on pinned items (do not suppress them;
  consider slightly louder styling) and show pin age from `flagged_at`
  ("pinned 3w ago" — a relative-time helper likely exists in
  `frontend/src/lib/format.ts`).
- Note the list order from the API already reflects the age bands; the
  frontend does not re-sort.

## Step 8 — Docs

- `docs/Attention_Engine.md`: marker section — new markers, narrowed
  `failing_checks`, age tiers, the banding exception, and the three known
  gaps listed above (including the GitHub `behind` caveat).
- `docs/REST_API.md`: new fields on the `GET /attention` item shape.
- `docs/Event_Taxonomy_and_Storage.md`: new `item.observed` payload fields.
- `ADR/ADR-0016_Presentation_And_Ranking.md`: flip the Scope line for this
  work from **Planned** to **Implemented (v0.1.1)**; update the Consequences
  bullet that says "threshold: 7 days" to mention both thresholds.
- `docs/Roadmap.md`: mark the v0.1.1 section as built.
- `docs/UI_Vocabulary.md`: the visual one-page reference for all tags,
  badges, age tiers, and hover texts — remove its ⏳ (not-built) marks as
  each piece lands, so it always reflects the shipped UI.

## Tests (extend `internal/attention/fold_test.go` and provider/API tests)

- Marker mapping per provider status (table-driven, both providers).
- `dirty` PR: `blocked` state + `merge_conflict` true + `failing_checks`
  false (the narrowing).
- Tier exclusivity: 10-day item → stale only; 40-day item → abandoned only;
  disabled thresholds.
- Banding: a 10-day-old `needs_review` sorts **below** a fresh
  `waiting_on_author`; abandoned sorts last; within a band the old order
  holds; pinned items ignore bands.
- Pin age surfaces `flagged_at`; API DTO carries all new fields.
- Onset (`since`): state true across 3 snapshots → onset = oldest of the run;
  state false in the middle snapshot → onset = latest run only; mentioned
  onset = earliest mention signal; markers get onsets too; absent for
  inactive tags.

## Out of scope (deliberately)

- Mentioned-state decay (recorded under "Unversioned ideas" in
  `docs/Roadmap.md`).
- GitLab pipeline fetching, GitHub check-run details.
- Any change to states, precedence, buckets, or the gate mapping.
