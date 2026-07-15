# DevPit — agent guide

DevPit is a self-hosted, read-only **attention center**: one ranked list of the
MRs/PRs that need *you*, across GitHub/GitLab, with optional Jira status. Go
backend, single binary that embeds a Svelte SPA, event-sourced over SQLite.

This file is a router, not a knowledge base: it links the canonical docs and
states only the few rules agents most often break. It carries no fact that lives
elsewhere (ADR-0014). Start with [`docs/Why.md`](docs/Why.md),
[`docs/Engineering_Philosophy.md`](docs/Engineering_Philosophy.md),
[`docs/High_Level_Architecture.md`](docs/High_Level_Architecture.md), and
[`docs/Contributing.md`](docs/Contributing.md).

## Before you say a change is done — run the gates

```sh
scripts/check.sh                 # every gate
scripts/check.sh --no-frontend   # backend-only change
```

`check.sh` is the definition of "green" and is what CI runs, so local and CI
can't drift ([`ADR/ADR-0013`](ADR/ADR-0013_Linting_and_Architecture_Enforcement.md)).
The git history is full of after-the-fact `style: gofmt` and `fix: resolve
golangci-lint failures` commits — those mean the gate was skipped. A change
isn't done until `check.sh` is green.

## The rules agents break most

- **Docs drift.** Facts have one home ([`docs/Contributing.md`](docs/Contributing.md)
  → "Where does my fact go?", [`ADR/ADR-0014`](ADR/ADR-0014_Documentation_As_Design_Record.md)).
  When you change a code shape a doc links to, fix that doc's prose *in the same
  change*. Don't restate a code shape or another doc in prose — link it. `/doc-check`
  audits this.
- **Fighting the layering.** `sdk` is the leaf; providers depend on `sdk` only;
  `cmd` is the sole composition root ([`.go-arch-lint.yml`](.go-arch-lint.yml)).
  A needed new cross-component edge is a *design decision* — reconsider or write
  an ADR; don't edit the yml to silence the linter.
- **Extracting a shared provider helper.** Providers **duplicate** helper code on
  purpose so one can't regress another ([`ADR/ADR-0003`](ADR/ADR-0003_Provider_Plugin_Model.md));
  arch-lint rejects a shared `provider/*` package. DRY is deliberately traded for
  isolation here.
- **Over-engineering.** Build the smallest thing that answers "what needs my
  attention now?" ([`docs/Engineering_Philosophy.md`](docs/Engineering_Philosophy.md));
  prefer the stdlib and existing code; delete speculative flexibility.
- **`go run`.** Build a binary (`scripts/start.sh`) — the pure-Go SQLite dep makes
  cold builds slow and `go run` pays it every time. Dev workflow and testing
  expectations: [`docs/Contributing.md`](docs/Contributing.md).

## Repo skills (`.claude/skills/`)

Committed, so every contributor gets them ([`ADR/ADR-0022`](ADR/ADR-0022_Agent_Contributor_Tooling.md)):
`/doc-check` (audit docs vs. code), `/add-provider`, `/new-adr`, `/signal-add`.
Prefer them for those tasks. (Skills *not* committed here are personal — don't
reference them in committed files; other contributors don't have them.)
