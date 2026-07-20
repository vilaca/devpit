# Attention Engine

> **Status:** the fold (read-time computation of buckets and ranking) is
> **implemented** in `internal/attention`. The user-facing presentation
> (pinned zone, tags, filters) is **Planned** — the frontend is not built.
> Decision: `ADR/ADR-0005_Event_Based_Attention_Engine.md` and
> `ADR/ADR-0016_Presentation_And_Ranking.md`.

The read side of DevPit. It folds the event log (`docs/Event_Taxonomy_and_Storage.md`)
into a single ranked list of WorkItems, each tagged with the attention states
it currently satisfies. It computes nothing on the write path — buckets are
derived at read time from the latest facts and signals.

The bucket predicates and the precedence order are **direct code**
(`internal/attention/states.go`, `internal/attention/fold.go`); this spec is
the design behind them. Where the two ever disagree, the code is authoritative
for the exact conditions.

## Attention states

Six states in v0.1. A WorkItem may carry several at once; they render as tags.

- **Needs Review** — you are a requested reviewer and have not yet reviewed.
- **Changes Requested** — a PR you authored where a reviewer requested changes;
  the ball is in your court.
- **Blocked** — a non-draft PR you authored that the provider's merge gate
  reports as not mergeable.
- **Ready to Merge** — a non-draft PR you authored that the merge gate reports
  as mergeable. Symmetric with Blocked.
- **Mentioned** — an open item with at least one mention signal aimed at you.
- **Waiting on Author** — a PR you are reviewing where your review is done and
  the ball is back with the author.

### Edge definitions

- **Blocked defers to the provider's merge gate.** DevPit does not re-derive an
  organization's rules (required checks, approvals, conflicts) — it reports what
  the gate reports. "No approvals" is Blocked only where the org *requires*
  approvals; a just-opened PR awaiting review is not Blocked by default. The
  merge-gate value mappings per provider live in `docs/Provider_API_Analysis.md`.
- **Non-gating check failures are a failure *notification*, not Blocked.** Any
  red check is surfaced (a marker on the item) so nothing is missed, but only
  merge-gating failures put a PR in the Blocked bucket — keeping Blocked
  trustworthy.
- **Drafts are never Blocked and never Ready to Merge** (their unmergeable
  state is expected); they still surface for Mentioned and explicit review
  requests. Normal rules resume once marked ready.
- **Changes Requested (author) vs Waiting on Author (reviewer)** are the two
  sides of a review round-trip, split because they have opposite actionability.
  Changes Requested is high-precedence (your turn); Waiting on Author is
  informational and lowest precedence (not your turn) — the stale badge is the
  safety net for round-trips the author has forgotten. Where the org's merge
  gate enforces approvals, Changes Requested co-occurs with Blocked; the item
  carries both tags and ranks by the more actionable Changes Requested.

## Ranking

Fixed state-precedence plus an age tiebreak — **no numeric score, no
configuration**. Action-demanding states rank above Ready to Merge (a quick
win, but nothing is stuck there). The exact precedence order is code
(`states.go`); the principle is that what demands your action outranks what is
merely done.

- **Within a state: newest-first**, where an item's timestamp is its newest
  signal (falling back to the latest snapshot's provider-updated time).
- **Stale badge** once an item's age exceeds a threshold (7 days, a constant in
  `fold.go`) — the anti-rot safety net.
- **Repeated same-type signals collapse** to one tag with a count
  ("Mentioned ×3"); the individual signals remain in the detail view.

## Presentation (Planned)

- A **single ranked list**, one row per item, states as tags; buckets are
  optional client-side filters, not the primary layout.
- A **pinned "Handle next" zone** at the top holds user-flagged items in flag
  order, lifted out of the auto-ranked list (never shown twice). The flag is
  local-only (`ADR/ADR-0017_Read_Only_Action_Model.md`).

The wire shape of the ranked list is specified in `docs/REST_API.md`.
