# Agent Contributor Tooling

## Scope

Implemented — process decision; applies now to how AI coding agents work in
this repo.

## Context

DevPit is built substantially with AI coding agents. The quality bar was already
written down — `docs/Engineering_Philosophy.md`, `docs/Contributing.md`, the ADRs,
`.golangci.yml`, `.go-arch-lint.yml` — but nothing pointed an agent at it on entry.
The cost showed in the git history: recurring after-the-fact `style: gofmt` and
`fix: resolve golangci-lint failures` commits (gates skipped), and `docs: drop
stale claim` commits (the one-home rule of ADR-0014 not applied). The knowledge
existed; the agent's starting point didn't carry it, and multi-step tasks
(add a provider, add a signal, write an ADR, audit docs) were re-derived each time.

## Decision

Three committed, repo-specific artifacts guide agents; all are shared, so every
contributor and CI-adjacent agent gets the same behaviour.

- **`CLAUDE.md` (repo root) is the agent entry point.** It is a *router*, not a
  knowledge base: it links the canonical docs and states only the handful of
  rules agents most often break (run `scripts/check.sh` before a change is done;
  the doc-freshness and layering rules; the provider-duplication and
  over-engineering stances). It is subject to the one-home rule like any doc
  (ADR-0014) — it carries no fact of its own, only pointers and imperatives.
  An `AGENTS.md` symlink exposes the same file to agent tools that read that
  name instead.
- **Project skills live committed under `.claude/skills/`.** `doc-check`
  (audit docs vs. code), `semantic-check` (audit code vs. the semantic
  invariants), `add-provider`, `new-adr`, and `signal-add` encode the repo's
  recurring multi-step workflows. Each skill **reads the current source as
  its template** rather than embedding a code shape, so a skill cannot drift out
  of sync with the code it scaffolds against.
- **A committed hook gates an agent's commits and pushes.** `.claude/settings.json`
  registers `scripts/claude-gate.sh` as a `PreToolUse` hook on `git` commands.
  A push is blocked until the tree is clean, `scripts/check.sh` is green, and a
  diff-mode `/doc-check` + `/semantic-check` review of `HEAD` has been recorded;
  commits and pushes are also scanned against optional per-clone leak rules
  (`docs/Contributing.md`). The script's header is the home of its exact
  behaviour.

An individual's **personal** skills are not committed and are not referenced from
committed files — other contributors do not have them. Everything else under
`.claude/` (`settings.local.json`, agent worktrees, harness state) stays
uncommitted.

## Rationale

Encode the required steps where the agent actually starts, so they are taken
rather than remembered. Keep the entry point a router so it cannot become a
competing home for facts (the failure mode ADR-0014 exists to prevent). Commit
the skills so a workflow is shared repo behaviour, not per-machine setup, and
make each skill read live source so the tooling ages with the code instead of
rotting into another stale copy.

Running the gate and the doc/invariant checks were instructions an agent could
skip, and the history shows it did: a commit with non-compiling tests reached
`main`, and doc drift surfaced only in later whole-repo sweeps. The hook makes
them preconditions of the push instead. A hook cannot run a skill, so the review
stays the agent's work — the hook blocks the push with what to run, and
`scripts/claude-gate.sh --reviewed` records that it ran. That record is the
agent's word, not proof; the hook's job is to make skipping deliberate rather
than forgetful.

## Consequences

- `CLAUDE.md` holds no volatile fact; when the rules it points to move, only the
  linked doc changes, not `CLAUDE.md`.
- New project skills go under `.claude/skills/` committed; a skill that embeds a
  code shape instead of reading it is a bug (it will drift) — `doc-check`'s own
  discipline applies to the skills too.
- `.gitignore` allowlists `.claude/` (see its comment): agent-local state can't
  be committed by accident, and a new shared artifact under `.claude/` requires
  an explicit un-ignore.
- The hook matches command text (`.claude/settings.json`, the script header), so
  it guards against an agent forgetting, not against intent: a push from a
  human's terminal, from inside another script, or outside Claude Code is not
  gated. CI still runs every gate on pushes to `main` and on pull requests.
- Every agent push costs a review, and a commit made after `--reviewed` needs a
  fresh one (the record is per `HEAD`). Both skills report their routing first,
  and a diff that pulls in no doc and no invariant anchor leaves them nothing to
  check, so a small change stays cheap.
