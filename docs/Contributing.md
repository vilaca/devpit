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

- One decision per ADR; number sequentially (`ADR-NNNN_Title.md`).
- Every ADR carries `Status` (`Accepted` / `Proposed` / `Superseded by …`) and
  `Scope` (`Implemented (vX)` / `Planned` / `Deferred` / `Uncommitted`).
- `docs/Roadmap.md` is the single source of truth for *timing*; link to it
  rather than restating milestones.
- When a decision has a known future trigger, record a forward-dependency note
  in Consequences.

## Coding standards

- Go, standard `gofmt`. Linting and architecture enforcement are defined in
  `ADR/ADR-0013_Linting_and_Architecture_Enforcement.md` and enforced in CI.
- Code comments reference specs by name where useful, never by section number.

## Testing expectations

- Unit-test the fold, engine cycle, storage, and config against fakes/fixtures
  (see `testdata/fixtures/`); providers are tested against recorded fixtures.
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
