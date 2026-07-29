# Contributing

## Where does my fact go?

DevPit keeps one home per fact (see `ADR/ADR-0014_Documentation_As_Design_Record.md`).
Before writing anything down, apply the test:

- **The *why*** (a decision + its rationale) → an **ADR** in `ADR/`.
- **Direct code** (schema, struct, enum, wire format, precedence order) →
  **code**. Reference it from docs; never copy it into prose.
- **Design detail that is neither** (invariants, protocols, edge cases,
  cursor/fold semantics, external-API facts) → a **`docs/` spec**.

Never restate a code shape in a doc — link to the source file. Never open a
second decision log — the ADRs are it.

## ADR process

- One decision per ADR; number sequentially (`ADR-NNNN_Title.md`), no gaps —
  when an ADR is folded into another (mutate-by-default, `ADR/ADR-0014`),
  renumber the later ADRs and update references in the same change.
- Every ADR carries `Scope` (`Implemented (vX)` / `Planned` / `Deferred` /
  `Uncommitted`); there is no `Status` field — being in the log is what makes
  it the accepted decision.
- `docs/Roadmap.md` is the single source of truth for *timing*; link to it
  rather than restating milestones.
- When a decision has a known future trigger, record a forward-dependency note
  in Consequences.

## Dev workflow

- `scripts/start.sh` is the rebuild-and-restart loop (frontend + backend +
  instance swap); its header documents the flags (`--no-frontend`,
  `--no-start`, pass-through args after `--`).
- Build a binary rather than `go run ./cmd/devpit`. DevPit depends on
  `modernc.org/sqlite`, a pure-Go SQLite that is large and slow to compile; a
  cold build takes ~15–20 s and pegs all cores. `go run` recompiles whenever
  the build cache is cold — e.g. after a dependency upgrade — so a start/stop
  loop pays that cost repeatedly. The running binary itself is idle (~0 % CPU
  between poll cycles).
- For UI development, `npm --prefix frontend run dev` runs Vite on `:5173` and
  proxies the API through to a running `devpit`. The Go build works without
  the frontend build (a committed placeholder page is embedded); you get the
  real UI only after `npm --prefix frontend run build`.
- Commit messages use conventional prefixes (`feat:`, `fix:`, `docs:`,
  `build:`, `style:`, `refactor:`, `chore:`, `test:`), imperative mood.
- Working implementation plans and agent handoffs live in the gitignored
  `docs/plans/` — they are not part of the committed design record.

## Coding standards

- Go, standard `gofmt`. Linting and architecture enforcement are defined in
  `ADR/ADR-0013_Linting_and_Architecture_Enforcement.md` and enforced in CI.
- Code comments reference specs by name where useful, never by section number.

## Testing expectations

- Unit-test the fold, engine cycle, storage, and config against fakes/fixtures
  (see `testdata/fixtures/`); providers are tested against recorded fixtures.
  Provider tests replay go-vcr cassettes (`ModeReplayOnly`); to re-record one,
  flip the recorder mode in the provider's test helper, run against a real
  token, then restore the mode before committing.
- A change to a code shape that a spec links to should be reflected in that
  spec's prose in the same change.

## Provider guidelines

- Implement the `sdk.Provider` contract (`sdk/provider.go`); see
  `docs/Provider_SDK.md` for the contract's semantics and
  `docs/Provider_API_Analysis.md` for the per-provider API research.
- Declare capabilities honestly; the engine never asks a provider to produce a
  bucket it declared unavailable.
- Prefer duplicating helper code (JSON decode, time parse, status mapping) over
  a shared `provider/*` helper — providers evolve independently by design
  (`ADR/ADR-0003_Provider_Plugin_Model.md`).

## Releasing

Cutting a release is a maintainer task with its own procedure — pre-flight,
tagging, verification, and the manual fallback — in
[`docs/Releasing.md`](Releasing.md). The pipeline's design is
[`ADR-0023`](../ADR/ADR-0023_Packaging_Distribution_and_Release_Pipeline.md).
