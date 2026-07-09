# Documentation as Design Record

## Status

Accepted

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

### Status vs. Scope

A decision can be *made* without being *built*. Keep the axes separate:

- **`Status`** — the decision's standing: `Accepted`, `Proposed`,
  `Superseded by ADR-XXXX`.
- **`Scope`** — reality: `Implemented (vX)`, `Planned`, `Deferred`,
  `Uncommitted`. Links to `docs/Roadmap.md`, which is the single source of
  truth for *timing*; ADRs and specs carry only the coarse tag, never a
  restated milestone list.

Specs carry a status banner (and inline tags for mixed docs) so a reader never
mistakes designed-for-later for exists-today. Where a current decision has a
known future trigger, record it as a **forward-dependency note** in the ADR's
Consequences (e.g. "no auth while localhost-only; federation forces it —
revisit then").

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
