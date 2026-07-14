# DevPit — agent guide

DevPit is a self-hosted, read-only **attention center**: one ranked list of the
MRs/PRs that need *you*, across GitHub/GitLab, with optional Jira status. Go
backend, single binary that embeds a Svelte SPA, event-sourced over SQLite.

The *why* and the principles are canonical elsewhere — read, don't restate:
[`docs/Why.md`](docs/Why.md), [`docs/Engineering_Philosophy.md`](docs/Engineering_Philosophy.md),
[`docs/High_Level_Architecture.md`](docs/High_Level_Architecture.md).

## Before you say a change is done — run the gates

```sh
scripts/check.sh          # gofmt, build, vet, test, golangci-lint, go-arch-lint, svelte-check
scripts/check.sh --no-frontend   # backend-only change
```

`check.sh` **is** what CI runs (`.github/workflows/ci.yml` calls the same
script), so local-green and CI-green can't drift. The git history is full of
after-the-fact `style: gofmt` and `fix: resolve golangci-lint failures` commits
— those mean the gate was skipped. Don't add to them: run `check.sh` and get it
green *before* you call the work finished. A change isn't done until every gate
passes.

## Where each fact lives (so docs don't drift or duplicate)

One home per fact — see [`docs/Contributing.md`](docs/Contributing.md) and
[`ADR/ADR-0014_Documentation_As_Design_Record.md`](ADR/ADR-0014_Documentation_As_Design_Record.md):

- **A decision + its rationale** → a numbered **ADR** in `ADR/`. The ADR log is
  the *only* decision record; don't open a second one.
- **A code shape** (schema, struct, enum, wire format, precedence order) →
  **the code**. Docs *link* to the source file; they never copy the shape into
  prose.
- **Design detail that is neither** (invariants, protocols, edge cases) → a
  **`docs/` spec**.

**Doc-freshness rule:** when you change a code shape that a spec links to, update
that spec's prose *in the same change*. Stale prose is a bug. Before adding a
sentence to a doc, check it isn't already stated (and owned) somewhere else.

## Architecture guardrails (enforced, not aspirational)

Layering is enforced by `go-arch-lint` ([`.go-arch-lint.yml`](.go-arch-lint.yml),
[`ADR/ADR-0013`](ADR/ADR-0013_Linting_and_Architecture_Enforcement.md)): `sdk` is
the leaf contract, providers depend on **`sdk` only**, `cmd` is the sole
composition root. If a change needs a new cross-component edge, that's a design
decision — reconsider or write an ADR, don't just edit the yml to make the
linter pass.

Providers **duplicate** helper code (JSON decode, time parse, status mapping) on
purpose so a change to one can't regress another
([`ADR/ADR-0003`](ADR/ADR-0003_Provider_Plugin_Model.md)). Do **not** extract a
shared `provider/*` helper — arch-lint rejects it and it's the wrong instinct
here. This is a case where the codebase deliberately trades DRY for isolation.

## Not over-engineering

Match [`docs/Engineering_Philosophy.md`](docs/Engineering_Philosophy.md): the
smallest thing that answers "what needs my attention now?". Before adding an
abstraction, interface-with-one-impl, or dependency, prefer the stdlib and
existing code. Delete speculative flexibility rather than keep it "just in
case".

## Build & test notes

- Build a **binary** (`scripts/start.sh` or `go build -o bin/devpit ./cmd/devpit`),
  don't `go run` — the pure-Go SQLite dep makes cold builds ~15–20 s, paid every
  `go run`. `start.sh` builds frontend → backend → restart in that load-bearing
  order (the SPA embeds at compile time).
- Config: one YAML at `~/.config/devpit/config.yaml`; schema/validation live in
  [`internal/config/config.go`](internal/config/config.go).
- Tests use recorded fixtures in `testdata/fixtures/`; unit-test the fold,
  engine cycle, storage, and config. Provider changes get fixture-based tests.
- `bin/` and `docs/plans/` are gitignored — `docs/plans/` holds working
  implementation plans, not the committed design record.
