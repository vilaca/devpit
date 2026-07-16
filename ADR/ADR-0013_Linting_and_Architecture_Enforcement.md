# Linting and Architecture Enforcement

## Scope

Implemented — every gate runs through `scripts/check.sh`, locally and in CI
(`.github/workflows/ci.yml`, one job per gate). See `docs/Roadmap.md`.

## Context

The codebase has a clear layered structure (ADR-0012): `sdk` is the public
provider contract and a dependency leaf, providers depend only on `sdk`,
`internal/*` packages are the application, and `cmd/devpit` is the sole
composition root. Nothing but tooling stops that structure from eroding over
time, and Go's default `go vet` catches only a narrow class of issues.

## Decision

`scripts/check.sh` is the single gate runner and the definition of "green": it
runs `gofmt`, `go build`/`vet`/`test`, golangci-lint, go-arch-lint, shellcheck
on the shell scripts, `go mod tidy -diff`, actionlint on the workflow YAML,
lychee on tracked markdown, and the frontend `svelte-check` + eslint +
`prettier --check`. Contributors run it before a change is done; CI runs the
same script, one job per gate, so a red check names the failing gate and local
and CI cannot drift — the gate list and the pinned linter versions live only in
the script, not in the workflow. The two gates that make ADR-0012's layered
structure executable (see `.golangci.yml`, `.go-arch-lint.yml`):

1. **golangci-lint (v2)** runs with `default: all` — every bundled linter is
   enabled — minus a curated set of exclusions (below). `depguard` is
   configured to pin the provider plugin boundary: `sdk` may not import
   `internal/*` or `provider/*`, and providers may import neither `internal/*`
   nor any other `provider/*` package. The provider deny is a prefix match, so
   it blocks both cross-provider imports and any shared `provider/*` helper —
   providers duplicate shared-looking code rather than share it (ADR-0003).
2. **go-arch-lint** enforces the full component dependency graph
   (`.go-arch-lint.yml`): the allowed edges between `sdk`, `cmd`, `config`,
   `engine`, `storage`, `api`, `attention`, and the two providers. `deepScan`
   is **on** — layering is checked at the method-call / dependency-injection
   level, not just imports, so a violation routed through an interface or an
   injected value is still caught. Its one false positive is the composition
   root: `cmd` injects an `api.Server` into `engine.WithNotifier` (engine owns
   the `Notifier` interface; `internal/api` implements it without importing
   engine), which `deepScan` misreads as an `api -> engine` edge. `cmd/devpit/
   main.go` is therefore listed in `excludeFiles`; since `cmd` may depend on
   everything, excluding it forfeits no real enforcement.

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
the arch check will flag them as unmapped. The `deepScan` exclusion is pinned to
`cmd/devpit/main.go`: if the `engine.WithNotifier` wiring moves to another file,
or a second composition-root file is added, `excludeFiles` must be updated in the
same change or `deepScan` will resurface the `api -> engine` false positive.

Adding a gate or bumping a pinned linter version is a change to `scripts/check.sh`
alone — the workflow only invokes it. `go-arch-lint` scans the filesystem, so
local git worktrees under `.claude/` are excluded in `.go-arch-lint.yml`
(`exclude:`); the gofmt gate checks tracked files only for the same reason. A
fresh CI checkout has no worktrees.

## Amendment — v0.1.6: frontend lint parity, tidy/actionlint/links gates, scheduled govulncheck (2026-07-16)

The frontend reaches the same lint stance as `.golangci.yml`: eslint
(`typescript-eslint` `recommendedTypeChecked` + `eslint-plugin-svelte`
recommended) with stylistic configs off, and prettier as the frontend's gofmt
— formatting is enforced, style opinions are not, the same line the Decision
already draws for Go. Both join `gate_frontend` alongside `svelte-check`.

Three new gates close remaining coverage gaps: `tidy` (`go mod tidy -diff` —
`go.mod`/`go.sum` drift was previously uncaught), `actionlint` (workflow YAML
was the one script class no gate covered; it runs the pinned shellcheck
against `run:` blocks too), and `links` (`lychee --offline` over tracked
markdown — an offline, internal-link-only check, so it stays deterministic;
this is the mechanical complement to ADR-0014's link-by-exact-filename
discipline).

`govulncheck` is deliberately **not** a `scripts/check.sh` gate: a new CVE
disclosure can flip it red with no code change, which would break "green is
deterministic, local == CI." It runs instead as a scheduled workflow
(`.github/workflows/vulncheck.yml`, weekly + `workflow_dispatch`) with its own
pinned version — the one exception to versions-living-in-`check.sh`, because
this check deliberately isn't one. A red run there is a to-do, not a broken
build.

Rejected candidates, so they aren't "helpfully" re-added later: markdownlint
and vale (prose-style churn, not correctness), yamllint (actionlint already
covers the YAML that matters), hadolint (one small Dockerfile doesn't
justify a dedicated linter), nilaway (too false-positive-heavy on this
codebase's patterns).

Adding eslint pulled in a transitive dependency (`flat-cache` → `flatted`)
that ships a stray `.go` file inside `frontend/node_modules` — invisible to
`git ls-files`, but `go build`/`vet`/`test ./...`, golangci-lint, and
go-arch-lint all discover files by walking the filesystem or the Go module
graph, not tracked-files-only, so they picked it up as part of this module.
`frontend/go.mod` declares `frontend/` a separate (source-free) Go module,
which is the correct fix for the compiler-driven gates; go-arch-lint still
scans the raw filesystem regardless of module boundaries, so it needed its
own `frontend/node_modules` entry in `.go-arch-lint.yml`'s `exclude:`,
alongside the existing `.claude` worktree exclusion.

## Amendment — frontend test gate (2026-07-16)

`gate_frontend` now also runs `npm run test` (Vitest), so the frontend has an
executable-test gate on par with Go's `go test`, not just type/lint checks.
Vitest reuses the existing Vite/Svelte toolchain (`vitest.config.ts`), so it
adds a test runner without a second build stack. The CI `frontend` job needs no
change — it already invokes the whole gate via `scripts/check.sh --ci frontend`.
The suite deliberately targets pure logic (buckets, relative-time formatting,
the SSE reconnect state machine, `toggleFlag`) plus one drift guard that asserts
the frontend state precedence equals Go's `internal/attention/states.go` — no
component-DOM harness, matching the "smallest thing that works" stance.

## Amendment — Go coverage floor (2026-07-16)

`gate_test` now runs `go test -race -coverprofile=... ./...`, reads the total
statement-coverage percentage off `go tool cover -func`'s last line, and fails
the gate below `COVERAGE_FLOOR` (`scripts/check.sh`, currently 75% — a few
points under the ~81% measured when the floor was added). It is a ratchet
against silent regression, not a target to chase: the floor is the single
number for the whole module, not a per-package minimum, so `cmd/devpit`
(composition root, no unit tests by design) and `scripts/demo` (a fixture
generator, not shipped product code) pull the total down without needing an
exclusion list. The CI `build` job needs no change — it already runs `test`
via `scripts/check.sh --ci build vet test tidy`.
