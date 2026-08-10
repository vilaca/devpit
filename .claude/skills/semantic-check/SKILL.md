---
name: semantic-check
description: >
  Audit DevPit against its semantic invariants — intent from the ADRs that no
  linter can enforce (read-only, provider isolation, sdk neutrality/honesty,
  signals-vs-markers, one ranked list, no workflow inference, user-centric sync,
  monotonic live client state).
  Two modes: default routes a diff to the invariants its files touch; `audit`
  scans the whole tree, one invariant at a time. Each finding is adversarial —
  construct a concrete scenario where the claim breaks — then verified by a
  skeptic pass, and resolves to fix / write-an-ADR / amend-the-invariant. Use in
  the DevPit repo when the user says "semantic-check", "check invariants",
  "audit invariants", "does this change break intent", or before a release.
  Reports findings; only edits when asked.
allowed-tools: Bash, Read, Grep, Glob, Task
---

# DevPit semantic-check

`.go-arch-lint.yml` and `.golangci.yml` enforce what a machine can. This skill
covers what they can't: the **meaning** a change can violate while every
deterministic gate stays green. The invariants live in
`docs/Semantic_Invariants.md` — each with a **Claim**, a **Home** ADR, code
**Anchors**, and a **Hunt** procedure. This skill executes those Hunts
adversarially.

An AI judge is nondeterministic, so this is **advisory-with-teeth**, never a
`check.sh` gate (`feedback-explicit-ci-divergence`): a VIOLATED finding needs a
human "accepted, here's why" or an ADR, not a silent pass. Read
`docs/Semantic_Invariants.md` first — it is the authority for the invariant set,
the verdict vocabulary (HOLDS / VIOLATED / WEAKENED), and the escalation
semantics (fix / write-an-ADR / amend-the-invariant). If it has changed, defer
to it over this skill.

## The rule that makes this "about meaning"

Do **not** ask "is this consistent with the docs" — that degenerates into
doc-checking. Ask, for each invariant in scope: **construct a concrete input or
diff under the current code where the claim fails.** A finding is the scenario,
not the vibe. No scenario ⇒ HOLDS.

## Test adversaries

For every concrete failure scenario, identify the automated test that could make
it happen. When the scoped diff changes behavior or fixes a bug, inspect the
changed or neighboring tests for a regression that forces the triggering
failure/boundary condition — especially stale or out-of-order asynchronous
responses, retries, reconnects, and cancellation where ordering can change the
result. A missing test is a **coverage gap**, not an invariant violation; report
it separately with the scenario and the test seam.

## Mode selection

- **`/semantic-check`** (no args, or a ref/path list) — **diff mode**. Scope is
  the working diff (`git diff` + staged), or `git diff <base>...HEAD` if a base
  is named. Route only the *touched* invariants.
- **`/semantic-check audit`** — **audit mode**. Scope is the whole tree; run
  every invariant. This is also the calibration run (see Calibration).

## Procedure

### 1. Determine scope and route (deterministic)

- Diff mode: `git diff --name-only` (plus `--cached`, plus `<base>...HEAD` when a
  base is given) → the changed file set.
- Read `docs/Semantic_Invariants.md`. For each invariant, match its **Anchors**
  globs against the changed set. An invariant is *in scope* if any anchor
  matches. In audit mode every invariant is in scope.
- Report the routing up front: which invariants are in scope and why (which
  changed file hit which anchor). If a changed file matches *no* invariant's
  anchors, say so — that is either fine (deterministic-only change) or a gap in
  the anchor coverage worth noting.

### 2. Hunt, one subagent per in-scope invariant (fan out)

Spawn one subagent (Task tool) per in-scope invariant, in parallel. Give each:

- the invariant's full entry (Claim, Home, Anchors, Hunt),
- the scope (the diff, or "whole tree" for audit),
- this instruction: *Execute the Hunt over the anchored files. For anything
  suspicious, construct the concrete failure scenario. Return each candidate as
  {verdict: VIOLATED|WEAKENED|HOLDS, file:line, scenario, suggested action}. Read
  the `Home` ADR to ground the claim's intent. Prefer false silence over false
  alarm — only report what you can tie to a real `file:line`.*

Each subagent reads live source (never a remembered shape) and returns
structured candidates.

### 3. Verify — adversarial skeptic pass

A semantic audit's failure mode is **plausible-but-wrong findings**; one
hallucinated violation destroys trust in the whole gate.

**Scope the skeptic pass to claims about code.** A finding that asserts a defect
in the *tree* (VIOLATED, or WEAKENED-because-a-code-barrier-eroded) gets a
skeptic subagent (Task tool) whose only job is to **refute** it: read the cited
code and argue the claim actually holds. A finding that is only about the
*invariant's own text* or a *doc/code comment* (e.g. "this Claim omits an axis",
"this comment names a forge concept") needs no adversarial code-refutation —
resolve it directly by amending the wording; sending it to a skeptic wastes a
round.

For each code claim, the skeptic must:
- read the cited `file:line` and try to find a producer/consumer/guard the
  hunter missed that makes the claim false;
- for a VIOLATED, **corroborate via `git blame`/`git log`** of the introducing
  commit, and check the surrounding code for a **self-contradiction** (a nearby
  block that deliberately does the opposite) — the strongest confirmations the
  first audit produced came from exactly these two moves;
- default to **refuted** when uncertain.

A finding survives only if the skeptic cannot refute it against real code. Drop
refuted candidates; keep survivors with the skeptic's reasoning attached.

### 4. Report

Group by invariant, most severe first (VIOLATED before WEAKENED). Each surviving
finding:

- `INV-N` · `file:line`
- **verdict** (VIOLATED / WEAKENED)
- the concrete scenario that breaks (or erodes) the claim
- the one resolving action: **fix**, **write/amend an ADR**, or **amend the
  invariant**

End with: invariants in scope, candidates found, survivors after verification,
and any invariant that produced *no findings and no near-misses* across its whole
anchored surface — flag it as possibly unfalsifiable (a claim that can't fail is
dead weight; recommend cutting or sharpening its Claim/Hunt).

Do **not** edit code, ADRs, or the invariants file unless the user asks. If they
ask you to act on a finding, follow its resolving action and re-run the affected
invariant.

## Calibration

The first audit doubles as the acceptance test for the invariant set itself. Run
the runnable regression corpus (`corpus/cases.yaml`) whenever this skill's prompt
or the model changes, so judge drift (an upgrade silently making the audit
lenient) is visible.

The audit has found three VIOLATED findings — a dead / one-sided `sdk` surface
(INV-4), a `needs_rebase` computed from raw branch divergence (INV-7), and an
older dashboard hydration overwriting newer state (INV-9). **All are now fixed
in-tree** and pinned as synthetic `diff` fixtures
(`inv4_reintroduce_dead_field.diff`, `inv7_reintroduce_diverged_or.diff`,
`inv9_reintroduce_hydration_race.diff`) that the audit must flag VIOLATED —
guarding against the exact regressions. The tree has no known live violation.

The corpus also includes **restraint** cases that must stay HOLDS; if the audit
flags one, the skeptic pass is too eager:
- the identical `decodeJSON` / `parseTime` / `observedDedupeKey` trio across
  providers is the **designed** duplicate state (ADR-0003), HOLDS not WEAKENED;
- a benign hover-text change touches no invariant;
- (implicit) the age band moving items in the ranking is the sanctioned
  INV-5/INV-6 exception.

If the audit misses a VIOLATED fixture, the Hunts are too vague — sharpen them.
If it flags a restraint case, the skeptic pass in step 3 is too lenient — tighten
it.

## Relationship to other tooling

- `/doc-check` audits docs vs. code for *drift*; this audits code vs. *intent*.
  They are complementary — neither subsumes the other.
- When a finding resolves to "write an ADR", use `/new-adr`; amend the
  invariant's `Claim` in the same change so the two stay in step.
