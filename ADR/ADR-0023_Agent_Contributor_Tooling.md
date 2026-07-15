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

Two committed, repo-specific artifacts guide agents; both are shared, so every
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
  (audit docs vs. code), `add-provider`, `new-adr`, and `signal-add` encode the
  repo's recurring multi-step workflows. Each skill **reads the current source as
  its template** rather than embedding a code shape, so a skill cannot drift out
  of sync with the code it scaffolds against.

An individual's **personal** skills are not committed and are not referenced from
committed files — other contributors do not have them. Everything else under
`.claude/` (local settings, agent worktrees, harness state) stays uncommitted.

## Rationale

Encode the required steps where the agent actually starts, so they are taken
rather than remembered. Keep the entry point a router so it cannot become a
competing home for facts (the failure mode ADR-0014 exists to prevent). Commit
the skills so a workflow is shared repo behaviour, not per-machine setup, and
make each skill read live source so the tooling ages with the code instead of
rotting into another stale copy.

## Consequences

- `CLAUDE.md` holds no volatile fact; when the rules it points to move, only the
  linked doc changes, not `CLAUDE.md`.
- New project skills go under `.claude/skills/` committed; a skill that embeds a
  code shape instead of reading it is a bug (it will drift) — `doc-check`'s own
  discipline applies to the skills too.
- `.gitignore` allowlists `.claude/`: everything under it is ignored except
  `.claude/skills/`, so agent-local state can't be committed by accident and a
  new shared artifact under `.claude/` requires an explicit un-ignore.
