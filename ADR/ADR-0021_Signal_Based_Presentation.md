# Signal-Based Presentation

## Status

Accepted — supersedes the closed attention-state set and the "your move"
framing of `ADR/ADR-0016_Presentation_And_Ranking.md` (the rest of ADR-0016
stands).

## Scope

**Planned** — decided now, not yet built. The current implementation
(`internal/attention`, `frontend/`) still renders the six named attention
states through v0.1.4; this ADR changes what a row shows and how it ranks. See
`docs/Roadmap.md`.

## Context

DevPit shows only what needs your attention. The membership rule — open items
you are involved in (assigned/authored sync scopes plus mention signals) — is
correct and **unchanged** (`ADR/ADR-0016_Presentation_And_Ranking.md`,
`ADR/ADR-0004_User_Centric_Synchronization.md`). What a shown row *says* is the
problem.

ADR-0016 tagged rows with a closed set of six **attention states** (Needs
Review, Changes Requested, Blocked, Ready to Merge, Mentioned, Waiting on
Author) phrased as "what is *your* move?". Two weaknesses emerged:

- Since v0.1.4 the list also shows open items that match no attention state — an
  authored MR awaiting review, or one whose merge gate is `unknown`. These
  render as **bare rows**: the reader cannot tell what state the MR is in.
- The named states imply a **workflow** — a review lifecycle, an expected order.
  Teams configure their forges differently; assuming a sequence of phases
  re-derives org process, which both this decision's predecessor and
  `ADR/ADR-0003_Provider_Plugin_Model.md` set out to avoid.

## Decision

**A row shows the signals the provider currently reports for the item — neutral
facts, not an inferred state or lifecycle.** There is no closed set of
viewer-relative "attention states" and no assumed before/after.

This is a relabeling of the read layer, not a storage change: it maps directly
onto the existing event model (`docs/Event_Taxonomy_and_Storage.md`) —
`item.observed` facts (draft, merge gate, CI, approvals, conflicts) plus the
aimed-at-you **signal stream** (`signal.mentioned`, `signal.review_requested`,
…).

### Signal set (fixed, no configuration)

Highest precedence (top of the list) first:

1. **Changes requested** — a reviewer's change verdict on the MR.
2. **Review requested** — you are a requested reviewer.
3. **Blocked** — the merge gate reports not-mergeable; the *why* rides as
   explanatory badges (conflict, needs rebase, checks failing, missing
   approvals, discussions, policy).
4. **Mentioned** (×N) — mention signals aimed at you.
5. **Ready to merge** — the merge gate reports mergeable.
6. **Auto-merge armed** — the provider's auto-merge / merge-when-pipeline-succeeds
   is set.
7. **Checks running** — a pipeline is in progress.
8. **Checking** — the merge gate is `unknown`: the provider has not yet returned
   a ready/blocked verdict. Replaces the former bare row. Appears **only** when
   no prior verdict exists — a previously known gate is carried forward through
   a transient recompute (`docs/Event_Taxonomy_and_Storage.md`), so it does not
   flap.

- **Review submitted** (by you) — lowest, informational (the former "Waiting on
  Author"): your review is in and the ball is with the author.

An item carries **every** signal that applies; its **highest** (lowest-numbered)
signal sets its rank, the rest ride as additional tags. `draft` and the approval
count remain a marker and a meta-row fact respectively, not ranking signals.

The precedence, the signal set, and the age thresholds are **direct code**
(`internal/attention/states.go`, `internal/attention/fold.go`); this list is the
design behind them.

### Ranking

Unchanged in shape from `ADR/ADR-0016_Presentation_And_Ranking.md`: **age band
first** (fresh < stale < old), then **signal precedence** above, then
**newest-first**. The pinned "Handle next" zone stays exempt. Reviewing another
person's work (Review requested, #2) ranks above your own Blocked MR (#3): a
quick action that unblocks someone else outranks a block you will often clear
via CI or a rebase anyway.

### Authorship

The only distinction between an item you authored and one you merely contribute
to is the subtle **blue background tint** already defined in
`ADR/ADR-0016_Presentation_And_Ranking.md`. There is no authorship tag; the same
signal vocabulary applies regardless of your role.

### Provider parity (goal)

- **Primary state signal — guaranteed parity.** The signal that names the row
  and sets its rank (#1–#5, #8) behaves identically on GitHub and GitLab — same
  tag, same position — for any user's token. A situation that is Blocked on one
  provider is Blocked on the other. (**Auto-merge armed** (#6) and **Checks
  running** (#7) are held to best-effort, not the hard guarantee: auto-merge
  fields are unverified on both, and GitHub cannot report an in-progress *gating*
  pipeline — it hides inside `blocked`. They ship where readable and are
  documented gaps otherwise.)
- **Explanatory badges — best-effort per provider, gaps documented.** The
  why-blocked badges hanging off a Blocked row (discussions, policy, checks
  failing, needs rebase) ship only where that provider reports a user-readable
  verdict, per the parity principle already in
  `ADR/ADR-0016_Presentation_And_Ranking.md`. Where a provider cannot, the badge
  is omitted and recorded in the provider-parity table (`docs/UI_Vocabulary.md`).
  A row stays correct and correctly ranked with less explanation — never a
  guessed or re-derived reason.

Structural gaps this accepts (`docs/Provider_API_Analysis.md`): GitHub does not
expose thread-gating (`discussions`) or org policy (`policy`) for non-admins and
hides gating-CI inside `blocked`; `needs rebase` is conditional on both
providers. GitLab version floors (gate ≥ 15.6, reviewer states ≥ 16.11) degrade
via capability declaration (`ADR/ADR-0003_Provider_Plugin_Model.md`).

## Rationale

Showing observed signals rather than an inferred state keeps DevPit honest: it
reports what the provider says and never assumes a team's workflow — the same
defer-to-the-provider discipline that keeps Blocked trustworthy. Dropping the
"your move" framing removes the bare-row gap (every shown item now carries at
least one signal, if only "Checking") and lets one neutral vocabulary describe
an MR whatever your role, with the blue tint carrying authorship. A fixed
precedence keeps the list trustworthy — it cannot be tuned into uselessness —
and the "attention" judgment now lives entirely in that ordering.

## Consequences

- **Supersedes** the closed attention-state set and the "your move" framing in
  `ADR/ADR-0016_Presentation_And_Ranking.md`. The rest of ADR-0016 stands:
  single ranked list, pinned zone, markers-carry-gate-diagnostics, age bands,
  hover-adds-information rule, authored tint, approval count.
- The signal vocabulary and precedence are direct code
  (`internal/attention/states.go`, `internal/attention/fold.go`). The read-model
  spec (`docs/Attention_Engine.md`), the tag reference (`docs/UI_Vocabulary.md`),
  and the wire shape (`docs/REST_API.md`) are updated **when this is
  implemented** — until then they correctly describe the shipped v0.1.4 model.
- Wire effect: the `states` array becomes the signal list and is **never empty**
  for a shown item (worst case `["checking"]`), removing the empty-array case
  introduced in v0.1.4 (`docs/REST_API.md`).
- New signal **auto-merge armed** requires a provider field not read today
  (GitHub `auto_merge`, GitLab `merge_when_pipeline_succeeds` / auto-merge) —
  **[verify at implementation]**.
- Timing lives in `docs/Roadmap.md`.
