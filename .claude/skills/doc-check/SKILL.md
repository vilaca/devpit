---
name: doc-check
description: >
  Audit the DevPit repo's docs and ADRs for drift, redundancy, and convention
  breaks against the actual implementation. Enforces "one home per fact"
  (docs/Contributing.md, ADR-0014): finds prose that restates a code shape
  instead of linking it, doc claims whose referenced code has changed, stale
  version/Scope tags, duplicated facts, dead internal links, ADR convention
  violations, and stale agent instructions (CLAUDE.md and the committed
  skills). Use in the DevPit repo when the user says "check the docs",
  "are the docs stale", "doc-check", "audit ADRs", "docs consistency", or before
  a release. Reports findings; only edits when asked.
allowed-tools: Bash, Read, Grep, Glob, Edit
---

# DevPit doc-check

DevPit keeps **one home per fact** (`docs/Contributing.md`, `ADR/ADR-0014`):
the *why* lives in an ADR, a *code shape* lives in code (docs link to it, never
copy it), and *design detail* lives in a `docs/` spec. Docs are ~a quarter of
this repo's commit history and the top source of churn — most doc bugs are
drift (prose the code outgrew), redundancy (a fact stated twice), or ADR
convention slips. This skill finds them.

Report findings grouped by category, each as `file:line — what's wrong — the
fix`. Do **not** edit unless the user asks; if they do, fix and re-run.

## 0. Scope

Targets: `README.md`, `docs/*.md` (including `docs/Contributing.md` itself),
`ADR/*.md`, and the agent instructions — `CLAUDE.md` (and its `AGENTS.md`
symlink) plus `.claude/skills/*/SKILL.md`. Ignore `docs/plans/` (gitignored
working notes, not the design record). Read `docs/Contributing.md` first — it
is the authority for the rules below; if it has changed, defer to it over this
skill — but its own claims are audited like any doc's.

## 1. Prose that restates a code shape (should link instead)

The rule: schema, struct fields, enums, wire formats, config keys, precedence
orders, HTTP routes live in **code**; docs reference the source file, never copy
the shape into prose. Find violations:

- Signal/bucket/state vocabularies → compare doc lists against the real enums
  (`internal/attention/states.go`, the fold, providers). A doc that enumerates
  states or badges is suspect — it should point at the source.
- Config keys → `internal/config/config.go` is the schema of record (README
  already links it; check other docs don't re-list keys).
- REST routes/response fields → the `internal/api` handlers.
- Any prose list that mirrors a Go `const`/`type` block is a candidate.

For each, confirm the code shape, then flag the prose with the file to link to.

## 2. Stale claims (code the prose describes has changed or gone)

Extract every concrete code reference from the docs — symbol names, field names,
route paths, flags, signal/bucket names, `vX.Y.Z` version tags, file paths in
backticks — and verify each still exists and still matches:

- `grep`/`Glob` each named symbol, file, and route in the codebase.
- Version claims (e.g. "v0.1.5") → cross-check `docs/Roadmap.md` and the current
  state; the Roadmap is the single source of truth for timing.
- Backtick file paths → confirm the file exists.

Flag anything named in prose that no longer resolves or no longer matches.

## 3. ADR conventions (docs/Contributing.md "ADR process")

Read a couple of recent ADRs to learn the current template, then check every
`ADR/*.md`:

- Carries a `Scope` (`Implemented (vX)` / `Planned` / `Deferred` /
  `Uncommitted`). There is **no `Status:` field** — flag any that has one.
- Filenames are `ADR-NNNN_Title.md`, sequentially numbered with no gaps (a
  fold renumbers the later ADRs — `docs/Contributing.md`); flag gaps, dupes,
  and references to a number that has since been renumbered away.
- Timing is **linked to `docs/Roadmap.md`, not restated**; flag milestone dates
  copied into an ADR.
- One decision per ADR; a decision with a known future trigger has a
  forward-dependency note in Consequences.

## 4. Redundancy / second decision log

Flag the same fact asserted in two places (e.g. a principle restated instead of
linking `docs/Engineering_Philosophy.md`, or a decision recorded outside the ADR
log). The ADRs are the only decision log.

## 5. Dead internal links

Extract every relative markdown link (`](docs/…`, `](ADR/…`, `](internal/…`) and
confirm the target exists.

## 6. Agent instructions (CLAUDE.md, AGENTS.md, skills)

- `CLAUDE.md` is a router (`ADR/ADR-0022`): flag any fact it *carries* whose
  home is elsewhere, and any pointer whose target moved (linked docs, skill
  names, script names and flags).
- `AGENTS.md` must remain a symlink to `CLAUDE.md`.
- Each `.claude/skills/*/SKILL.md` names files, symbols, scripts, and
  conventions — verify every one still resolves and matches (the skills promise
  to read live source; a stale pointer breaks that), and check each rule they
  state against its home (`docs/Contributing.md`, the ADRs).

## 7. Report

Group by the sections above. Lead with drift and dead links (correctness), then
redundancy and convention. End with a one-line count. If asked to fix, prefer
"replace restated shape with a link to the source" over rewording, update stale
claims to match code, and re-run `scripts/check.sh` if any code files changed.
