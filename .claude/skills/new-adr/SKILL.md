---
name: new-adr
description: >
  Scaffold a convention-correct ADR in the DevPit repo (ADR/ADR-NNNN_Title.md).
  Picks the next sequential number, matches the existing ADR template, sets a
  Scope tag (never a Status field), links docs/Roadmap.md for timing instead of
  restating it, and adds a forward-dependency note in Consequences when the
  decision has a known future trigger. Use in the DevPit repo when the user says
  "new ADR", "write an ADR", "record this decision", "new-adr", or "add a
  decision record".
allowed-tools: Bash, Read, Grep, Glob, Write
---

# DevPit new-adr

The ADR log is DevPit's **only** decision record (`ADR/ADR-0014`,
`docs/Contributing.md`). Being in the log is what makes a decision accepted —
there is no approval status to track. This skill scaffolds one to the repo's
conventions.

## 1. Read the current conventions and template

- `docs/Contributing.md` → the "ADR process" section is authoritative; if it has
  changed, defer to it over this skill.
- Read the two most recent `ADR/*.md` files to copy the current section
  structure (Context / Decision / Consequences, or whatever they now use).

## 2. Pick the number

List `ADR/`, find the highest `ADR-NNNN`, add one, zero-pad to four digits.
Filename is `ADR-NNNN_Title_With_Underscores.md`. Numbers are sequential with no
gaps — don't reuse or skip.

## 3. Draft with the user

Confirm the title and the single decision. **One decision per ADR** — if the
user is describing two, split them into two ADRs.

## 4. Fill the required fields

- **`Scope`**: one of `Implemented (vX)` / `Planned` / `Deferred` /
  `Uncommitted`. There is **no `Status:` field** — do not add one.
- **Timing**: link `docs/Roadmap.md`; never restate milestone dates or ordering.
- **Code shapes**: reference the source file; don't paste enums/schemas into the
  ADR (one home per fact).
- **Forward dependency**: if the decision has a known future trigger, record it
  as a note in Consequences.

## 5. Cross-links

Link related ADRs and the owning spec. If a `docs/` spec is affected by this
decision, note it — and update that spec's prose in the same change if a code
shape it links to moves.
