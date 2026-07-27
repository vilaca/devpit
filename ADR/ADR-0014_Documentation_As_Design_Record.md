# Documentation as Design Record

## Scope

Process decision — applies now to all documentation and code comments.

## Context

DevPit accumulated two parallel decision logs — the ADRs and a large
`docs/Design_Decisions.md` that explicitly "refined and superseded" them —
plus spec docs that restated schemas, enums, and wire formats already present
in code. The duplication drifted: the storage DDL in a doc diverged from
`schema.go`, the state precedence order lived in three places, the sync-log
outcome set in three. Copies rot; a fact with two homes eventually disagrees
with itself.

## Decision

Every fact has exactly one home, chosen by a three-way test:

- **Is it the *why* — a decision and its rationale?** → an **ADR**.
  One decision per ADR. ADRs state the decision and do not re-explain the
  detailed design.
- **Is it expressible as *direct code* — a schema, struct, enum, wire format,
  precedence order?** → **code**. Docs link to it; they never copy it.
- **Is it *design detail that is neither* — an invariant, a cross-component
  protocol, edge-case handling, cursor/fold semantics, external-API facts?**
  → a **`docs/` spec**. A spec elaborates; it does not re-decide (that is the
  ADR) and does not restate shapes (that is code).

Link direction is one-way: **ADR → spec → code**. `docs/Design_Decisions.md`
is retired; its *whys* moved into ADRs, its non-code detail into the specs,
its code-shaped bits were verified against code and dropped. Two decision logs
are not allowed.

### Duplication rules

- **Doc ↔ doc**: not allowed — collapse to one home and link.
- **Code ↔ doc**: docs never mirror code shapes — link to the code instead.
- **Doc ↔ ADR**: tolerated. An ADR is a self-contained record; a brief
  restatement of its decision in a spec is acceptable so long as the spec does
  not become a second decision log.
- **Conflicts** (doc and code, or two docs, actually disagree): a human
  decides which is correct; there is no automatic winner.

### Scope

A decision can be *made* without being *built*. Being in the log is what makes
an ADR the accepted decision — there is no `Status` field (an unsettled
decision is not an ADR yet, and a replaced one is folded away, below). What an
ADR does carry is **`Scope`** — reality: `Implemented (vX)`, `Planned`,
`Deferred`, `Uncommitted`. Scope links to `docs/Roadmap.md`, which is the
single source of truth for *timing*; ADRs and specs carry only the coarse tag,
never a restated milestone list.

Specs carry a scope banner (and inline tags for mixed docs) so a reader never
mistakes designed-for-later for exists-today. Where a current decision has a
known future trigger, record it as a **forward-dependency note** in the ADR's
Consequences (e.g. "log kept uncompacted until a real instance proves
compaction necessary — revisit then").

### Amending vs. adding ADRs

**Mutate an ADR in place by default.** An ADR records the *current* decision,
not a frozen snapshot — the git history is the archive of prior versions, so
editing loses nothing. Create a *new* ADR only when there is a reason to: a
genuinely distinct decision, or a shift large enough to deserve its own record
and rationale. When you do, you still **mutate the affected existing ADR(s)** —
move content to its new home, drop what no longer holds, cross-link — rather than
leaving a stale copy behind. Supersession is *update-old-and-add-new*, not
*freeze-old*: an ADR whose decision is **wholly** replaced is folded into its
successor and the file deleted (git history keeps it — as with ADR-0021); one
only *partly* affected is edited down to what still stands. There is no
tombstone status — nothing stale survives to carry one.

### README and Contributing (2026-07-15)

The **README is the public front door**: written for a stranger at release
time — pitch, feature list, quickstart — never a project log. It carries no
volatile facts except one status line ("Pre-release, vX.Y") bumped per
release; everything else is stable prose linking to the live specs (signal
vocabulary → `docs/Attention_Engine.md`, timing → `docs/Roadmap.md`).
Developer-workflow advice (build loop, dev server) lives in
`docs/Contributing.md`, not the README. The hero screenshot, when it lands
(first-public-release gate in `docs/Roadmap.md`), is captured from seeded
demo data only — never from a real instance.

**`docs/Contributing.md` is the contributor-facing digest** of this ADR plus
process facts (dev workflow, coding standards, testing, provider guidelines).
It restates decisions briefly and links to their homes; it is not a second
decision log.

## Rationale

Drift came from copies, so the fix is to forbid copies: one home per fact, at
the altitude that matches what the fact *is*. Code is the only artifact that
cannot lie about the shapes it defines, so shapes live there. Rationale cannot
live in code, so it lives in ADRs. Everything else is spec. Numeric
cross-reference anchors (`§6`, `Q3`) are the most brittle form of coupling and
are not used in code comments — named references to surviving specs are, since
they survive renumbering.

## Consequences

- `Contributing.md` carries the day-to-day "where does my fact go?" test and
  links here.
- Code comments reference surviving specs by name where a pointer earns its
  place, never by section number.
- Adding a doc means first asking the three-way test; a stub that only points
  at an ADR should not exist as a file.
- The one-way link direction means a code change can invalidate a spec link
  but never a spec's correctness claim about code — the spec never claims to
  *be* the code.
