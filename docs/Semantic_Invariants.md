# Semantic Invariants

> **Status:** the invariants below are **implemented** expectations about the
> current tree. This file is the operational home of the *falsifiable form* of
> each claim (claim, code anchors, hunt procedure); the **why** stays in the
> ADR named as each invariant's `Home` and is linked, never restated
> (`ADR/ADR-0014_Documentation_As_Design_Record.md`). Audited by the
> `/semantic-check` skill (`.claude/skills/semantic-check/`).

Deterministic rules already have homes that a machine enforces:
`.go-arch-lint.yml` (layering), `.golangci.yml` (lint), `scripts/check.sh` (the
gate). **This file is only for intent that no linter can see** — the meaning a
change can violate while every deterministic gate stays green.

Each invariant is a claim an audit can try to *break*, not a description to
check for consistency. The unit of work is: route a diff (or the whole tree)
to the invariants its files touch, then for each, *construct a concrete
scenario under the current code where the claim fails*.

## Verdict vocabulary

Per invariant, per finding:

- **HOLDS** — no counterexample found; state the reasoning that makes it hold.
- **VIOLATED** — a concrete counterexample exists in the tree today. Cite
  `file:line` and the scenario that breaks the claim.
- **WEAKENED** — the claim still technically holds, but a barrier that used to
  protect it is gone, so a future change violates it cheaply (e.g. an `sdk`
  field only one provider can populate; dead capability surface; duplicated
  code that has drifted toward "let's just unify it"). Cite the eroded barrier.

## Escalation semantics

A **VIOLATED** or **WEAKENED** finding resolves to exactly one action, never a
silent edit of the invariant to make the audit pass:

- **fix** the code, or
- **write/amend an ADR** — the violation is really a conscious decision; record
  it and amend the invariant's `Claim` to match, or
- **amend the invariant** — the claim itself was wrong or unfalsifiable.

This mirrors the arch-lint stance (`CLAUDE.md`): a needed new cross-component
edge is a design decision, not a lint to silence. Same here.

## Writing a good invariant

A claim nobody can imagine failing ("the code is maintainable") is dead weight.
The filter: **can you describe a plausible diff that would violate it?** If not,
cut it. Each entry carries four fields:

- **Claim** — the falsifiable contract, one sentence.
- **Home** — the ADR/doc that owns the *why* (linked).
- **Anchors** — the code seams the claim constrains; routes a diff to this
  invariant and bounds an audit's scan.
- **Hunt** — grep seeds and questions: a falsification procedure a reader (or
  subagent) executes without re-reading the whole tree.

## Out of scope

These invariants cover *intent* — meaning a change can violate while every
deterministic gate stays green. They are **not** a catch-all quality net:

- **Plain duplication / DRY** (e.g. two identical `internal/storage` helpers)
  is owned by code review, `golangci-lint` (`dupl`/`goconst`), and `/simplify`
  — not an invariant. The exception is INV-2, and only because there
  duplication-drift is the *precursor to a provider-isolation breach*, not
  because duplication is itself a violation. Do not widen an invariant's anchors
  to make it police ordinary refactors.
- **Anything a linter already enforces** — layering (`.go-arch-lint.yml`),
  formatting, unused *imports*. If a machine can decide it, it belongs in
  `scripts/check.sh`, not here. Add an invariant only for judgment a linter
  cannot make.

A finding that reduces to "this could be tidier" with no invariant behind it is
out of scope; route it to `/simplify` or code review.

---

## INV-1 — Read-only

**Claim:** No code path issues a state-changing call to a forge or to Jira; the
only user-applied state DevPit persists is the local `handle_next` flag in
SQLite, and it is never written back to any provider.

**Home:** `ADR/ADR-0017_Read_Only_Action_Model.md` (also `docs/Why.md`
"Read-only by Default").

**Anchors:** `provider/github/`, `provider/gitlab/`, `internal/jira/`,
`internal/api/`, `internal/storage/`, `sdk/`.

**Hunt:**
- (a) Any outbound HTTP verb other than `GET`/`HEAD` in provider or jira code —
  `http.MethodPost|Put|Patch|Delete`, `http.NewRequest(` with a non-GET method,
  a request body on a forge call.
- (b) Any GraphQL `mutation` document, or a REST path that names a write action
  (approve, merge, comment, note, label, close).
- (c) A write to a provider that is dressed up as a read — a "refresh" that
  POSTs, a token scope requested beyond read.
- (d) Any DevPit-persisted user state beyond `handle_next` that could imply a
  writeback path (a dismiss/snooze/hide store — explicitly forbidden by the ADR).

---

## INV-2 — Provider isolation

**Claim:** A bug in one provider cannot change another provider's behavior:
provider packages neither import each other nor a shared `provider/*` helper,
and share no cross-provider mutable state.

**Home:** `ADR/ADR-0003_Provider_Plugin_Model.md`.

**Anchors:** `provider/github/`, `provider/gitlab/`, `sdk/`.

**Hunt:**
- (a) An import edge between `provider/github` and `provider/gitlab`, or a new
  `provider/<shared>` package either imports (arch-lint catches the import; the
  *design smell* of introducing shared provider code is what to flag).
- (b) Package-level mutable state (`var` at package scope, a singleton, a
  process-wide cache/registry) that more than one provider reads or writes.
- (c) Duplicated helpers (JSON decode, time parse, dedupe-key, HTTP status
  mapping) whose copies have **drifted** far enough that the next fix will be
  "unify them" — the WEAKENED precursor to a shared-helper violation.
- (d) Shared state routed through `sdk` or `internal/*` that couples the two
  providers' runtime behavior.

---

## INV-3 — SDK is a forge-neutral leaf

**Claim:** No GitHub- or GitLab-specific concept leaks into `sdk`: its exported
types name provider-neutral facts only, and it depends on nothing but the
standard library.

**Home:** `ADR/ADR-0006_Normalized_Data_Model.md` (provider-neutral model);
`ADR/ADR-0003_Provider_Plugin_Model.md` (sdk is the common contract).

**Anchors:** `sdk/`.

**Hunt:**
- (a) A forge-specific identifier in an `sdk` type or field name or doc comment
  — `mergeable_state`, `detailed_merge_status`, `graphql`, `pipeline` phrased as
  a GitLab concept, `check_run`, anything that reads as one forge's vocabulary.
- (b) A field whose *only* honest meaning is one provider's API shape (a neutral
  name hiding a forge-specific enum).
- (c) An `sdk` import of anything outside the standard library (leaf violation;
  arch-lint covers the internal/provider edges, this catches third-party creep).

---

## INV-4 — No dead or one-sided SDK surface

**Claim:** Every field, flag, and capability in the `sdk` contract is both
*produced* by at least one provider and *consumed* by the read layer — no
write-only surface (set, never read), no read-never surface (declared, never
populated), no capability flag no provider honors.

**Accepted exception — pre-wiring for a Roadmap feature.** A field a provider
populates but the read layer does not yet consume is *not* dead surface **iff**
it is deliberately retained as pre-wiring for a feature in `docs/Roadmap.md` and
links there. Two shapes qualify: (1) retained event-log history — the
events-first model (`ADR/ADR-0006_Normalized_Data_Model.md`) keeps richer history
than the current fold reads on purpose; and (2) a connection fact that is
resolved but not yet surfaced, awaiting its UI consumer. This carve-out is
falsifiable, not a loophole: a populated-but-unread field with **no** Roadmap
link is still a finding. (Current holders: `Event.Actor` and the
review/approval/assignment payload actor fields — see the "Actor attribution &
activity timeline" roadmap entry; and `Identity.DisplayName` — see the "Show the
resolved account display name" roadmap entry.)

**Home:** `ADR/ADR-0003_Provider_Plugin_Model.md` (capabilities are direct code,
"the engine never asks a provider to produce a bucket it declared unavailable");
`ADR/ADR-0006_Normalized_Data_Model.md` (facts are not pre-modeled).

**Anchors:** `sdk/`, `provider/github/`, `provider/gitlab/`, `internal/attention/`,
`internal/api/`.

**Hunt:**
- (a) An `sdk` struct field written by providers but never read downstream (fold,
  api), or read downstream but never populated by any provider.
- (b) A `Capabilities` flag (or equivalent declared capability) that no provider
  sets, or that nothing branches on.
- (c) A `PollResult`/event field that is carried through the pipeline but changes
  no output.
- (d) A field one provider populates and the other structurally cannot — a
  WEAKENED signal that the fact is really forge-specific (compare against INV-3).

---

## INV-5 — Signals mean attention, markers mean diagnosis

**Claim:** A signal answers "does this need *me* now"; a marker/diagnostic badge
explains data state (why an item is blocked, how stale it is). No signal encodes
a gate diagnostic, and no marker moves an item in the ranking — the single
exception being the deliberate age-band tiering.

**Home:** `ADR/ADR-0016_Presentation_And_Ranking.md` ("Markers carry gate
diagnostics; signals never do"; "cosmetic markers never move items").

**Anchors:** `internal/attention/`, `frontend/src/lib/`.

**Hunt:**
- (a) A marker/diagnostic value read inside the ranking sort (`sortItems` in
  `fold.go`) or feeding an item's rank timestamp — other than the age band.
- (b) A signal's firing condition or payload carrying a gate *reason* (conflict,
  missing-approval, policy) that belongs on a marker.
- (c) A new sort key, tiebreak, or promotion/demotion driven by a cosmetic fact.
- (d) A diagnostic badge that fires without a provider-reported verdict —
  reconstructed from raw facts plus org rules (the parity principle).

---

## INV-6 — One ranked list, one ordering

**Claim:** There is a single ranked list with one ordering rule (age band, then
recency, then item ID); no feature introduces a second competing prioritization,
a numeric score, or a user-tunable ranking knob.

**Home:** `ADR/ADR-0016_Presentation_And_Ranking.md`;
`docs/Engineering_Philosophy.md` ("attention over information", one list).

**Anchors:** `internal/attention/fold.go`, `frontend/src/lib/`.

**Hunt:**
- (a) A second sort path or an alternate ordering computed alongside the canonical
  one (a "priority", "score", "weight" field that reorders).
- (b) A config key or UI control that changes ranking (buckets are *filters*, not
  a reordering — a filter that secretly reorders is a violation).
- (c) Signal precedence leaking back into item ranking (precedence orders chips
  within a row only; it must not rank items — the 2026-07-13 revision).

---

## INV-7 — Report provider facts, never infer workflow

**Claim:** DevPit reports the facts a provider currently reports; it never
re-derives a team's process — no assumed review lifecycle, phase sequence, or
org policy reconstructed from raw facts.

**Home:** `ADR/ADR-0016_Presentation_And_Ranking.md` (signals are "neutral facts,
not an inferred state or lifecycle"); `ADR/ADR-0003_Provider_Plugin_Model.md`
(stay provider-agnostic).

**Anchors:** `internal/attention/`, `provider/github/normalize.go`,
`provider/gitlab/normalize.go`.

**Hunt:**
- (a) Logic that infers a required-approval count, a review order, or a "next
  phase" from raw facts rather than reading a provider verdict.
- (b) A normalizer that hardcodes one forge's workflow assumption (e.g. "a PR
  with N approvals is ready") instead of deferring to the reported merge gate.
- (c) A signal or badge that fires on a *derived* lifecycle position rather than
  a currently-observed provider fact.

---

## INV-8 — User-centric sync scope

**Claim:** Synchronization discovers work only from the user's own involvement —
review requests, mentions, assigned, authored items, and repos where the user is
the **sole merge-capable approver** (ADR-0004's v0.1.5 discovery scope); it never
mirrors whole repositories or retains work the user has no stake in. A
repo/project-wide *query* is permitted only when its results are filtered down to
one of these involvement axes.

**Home:** `docs/Why.md` ("User-centric Synchronization");
`ADR/ADR-0004_User_Centric_Synchronization.md`.

**Anchors:** `internal/engine/`, `provider/github/reconcile.go`,
`provider/gitlab/reconcile.go`, `provider/github/fastpoll.go`,
`provider/gitlab/fastpoll.go`.

**Hunt:**
- (a) A provider query that lists a repository's items wholesale (all open PRs in
  a repo, org-wide enumeration) rather than filtering to the authenticated
  user's involvement.
- (b) A sync path that fetches repository detail eagerly instead of on demand.
- (c) A membership rule that admits items the user is not involved in (no
  review-request, mention, assignment, authorship, or sole-approver tie). A
  repo/project-wide listing (e.g. GitHub `user:<handle>`, GitLab per-project
  `state=opened`) is only acceptable if every retained item is filtered to a
  sole-approver (or other involvement) stake — an unfiltered wholesale listing
  is the violation.
