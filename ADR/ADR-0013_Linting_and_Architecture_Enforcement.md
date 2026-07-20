# Linting and Architecture Enforcement

## Status

Accepted

## Scope

Implemented — enforced in CI (`.github/workflows/ci.yml`). See `docs/Roadmap.md`.

## Context

The codebase has a clear layered structure (ADR-0012): `sdk` is the public
provider contract and a dependency leaf, providers depend only on `sdk`,
`internal/*` packages are the application, and `cmd/devpit` is the sole
composition root. Nothing but tooling stops that structure from eroding over
time, and Go's default `go vet` catches only a narrow class of issues.

## Decision

CI enforces two complementary gates (see `.golangci.yml`, `.go-arch-lint.yml`,
and `.github/workflows/ci.yml`):

1. **golangci-lint (v2)** runs with `default: all` — every bundled linter is
   enabled — minus a curated set of exclusions (below). `depguard` is
   configured to pin the provider plugin boundary: `sdk` may not import
   `internal/*` or `provider/*`, and providers may import neither `internal/*`
   nor each other.
2. **go-arch-lint** enforces the full component dependency graph
   (`.go-arch-lint.yml`): the allowed edges between `sdk`, `cmd`, `config`,
   `engine`, `storage`, `api`, `attention`, and the two providers.

### Disabled linters

*Opinionated style/formatting* (kept off so a lint failure always signals a
real correctness or quality issue, never a style preference): `wsl`, `wsl_v5`,
`nlreturn`, `varnamelen`, `exhaustruct`, `paralleltest`, `tagliatelle`,
`err113`, `wrapcheck`, `nonamedreturns`, `gochecknoglobals`, `gochecknoinits`,
`mnd`, `testpackage`, `noinlineerr`, `cyclop`, `funlen`. (`gomodguard` is off
as a deprecated alias; `gomodguard_v2` stays enabled.)

*False positives against deliberate patterns* — these are the ones most likely
to be "helpfully" re-enabled by a future contributor, so the reasoning is
recorded here:

- **`contextcheck`** — the engine records each cycle's outcome on a *detached*
  context (`internal/engine/cycle.go` `writeLog`: `context.WithTimeout(
  context.Background(), ...)`). This is intentional: a shutdown that cancels
  the request context mid-cycle must still be able to write the final
  `sync_log` row. Passing the (cancelled) request context — which is what
  `contextcheck` demands — would defeat that guarantee.
- **`bodyclose`** — providers close HTTP response bodies inside the
  `do()` / `decodeJSON` helper pair (`provider/*/*.go`), which the linter
  cannot trace across the helper boundary, so it false-positives at every call
  site.

Two remaining true-but-intended findings are suppressed locally with justified
`//nolint` comments rather than disabling the linter globally: `gosec` G304 on
the config and lock-file opens (caller-controlled paths), and `errchkjson` on
the dedupe-key `json.Marshal` (JSON-safe scalar payload that cannot error).

## Rationale

"Maximal minus style dogma" keeps the signal-to-noise ratio high: the gate
stays green in steady state, so any red genuinely means something. Encoding the
layering in `depguard` + `go-arch-lint` makes ADR-0012's structure executable
rather than aspirational — a disallowed import fails CI instead of surviving
review.

## Consequences

New opinionated style linters shipped by future golangci-lint versions may need
adding to the exclusion list. The two pattern-based disables (`contextcheck`,
`bodyclose`) should stay off as long as the detached-log-context and
`do()`/`decodeJSON` patterns remain; revisit them only if those patterns change.
New packages must be added to `.go-arch-lint.yml` with their allowed edges, or
the arch check will flag them as unmapped.
