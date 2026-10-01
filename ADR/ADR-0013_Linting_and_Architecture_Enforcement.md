# Linting and Architecture Enforcement

## Scope

Implemented (v0.1), extended through v0.1.6 — every gate runs through
`scripts/check.sh`, locally and in CI (`.github/workflows/ci.yml`, one job per
gate or small group of gates). See `docs/Roadmap.md`.

## Context

The codebase has a clear layered structure (ADR-0012): `sdk` is the public
provider contract and a dependency leaf, providers depend only on `sdk`,
`internal/*` packages are the application, and `cmd/devpit` is the sole
composition root. Nothing but tooling stops that structure from eroding over
time, and Go's default `go vet` catches only a narrow class of issues. The repo
is public and handles forge and Jira tokens, and much of it is written by AI
agents (ADR-0022), so anything a machine can decide should be decided by one.

## Decision

`scripts/check.sh` is the single gate runner and the definition of "green"; its
header lists every gate. Contributors run it before a change is done (an
agent's push is gated on it — ADR-0022); CI runs the same script, one job per
gate or small group of gates, so a red check names the failing gate and local
and CI cannot drift — the gate list and the pinned tool versions live only in
the script, not in the workflow. Green is **deterministic**: a gate's result
depends only on the tree, never on the machine, the date, or a third party.

### Go lint and layering

The two gates that make ADR-0012's layered structure executable (see
`.golangci.yml`, `.go-arch-lint.yml`):

1. **golangci-lint (v2)** runs with `default: all` — every bundled linter is
   enabled — minus a curated set of exclusions (below). `depguard` is
   configured to pin the provider plugin boundary: `sdk` may not import
   `internal/*` or `provider/*`, and providers may import neither `internal/*`
   nor any other `provider/*` package. The provider deny is a prefix match, so
   it blocks both cross-provider imports and any shared `provider/*` helper —
   providers duplicate shared-looking code rather than share it (ADR-0003).
2. **go-arch-lint** enforces the full component dependency graph
   (`.go-arch-lint.yml`). `deepScan`
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

A true-but-intended finding is suppressed at its call site with a justified
`//nolint` comment rather than by disabling the linter globally — e.g. `gosec`
G304 on a caller-controlled path, or `errchkjson` on a `json.Marshal` that
cannot fail.

### Frontend

The frontend takes the same stance as `.golangci.yml`: `svelte-check`, eslint
(`typescript-eslint` `recommendedTypeChecked` + `eslint-plugin-svelte`
recommended) with stylistic configs off, and prettier as the frontend's gofmt —
formatting is enforced, style opinions are not. Vitest runs as its test gate,
reusing the Vite/Svelte toolchain (`vitest.config.ts`) rather than a second
build stack. The suite (`frontend/src/lib/*.test.ts`) targets pure logic,
including drift guards asserting the frontend matches Go — the state precedence
(`internal/attention/states.go`) and the wire shape (`internal/api/attention.go`)
— with no component-DOM harness, matching the "smallest thing that works"
stance.

### Coverage floor

`go test` runs with `-race` and a coverage profile, and the gate fails when
total statement coverage, or that of a package with its own floor, drops below
the floor (`scripts/check.sh` holds the numbers). It is a ratchet against
silent regression, not a target to chase: `cmd/devpit` (composition root, no
unit tests by design) and `scripts/demo` (a fixture generator) pull the total
down without needing an exclusion list, and only packages with real logic get a
floor of their own. A floor also fails when it sits more than `COVERAGE_SLACK`
points under what's measured, naming the value to raise it to.

### Repository gates

- **`tidy`** (`go mod tidy -diff`) catches `go.mod`/`go.sum` drift.
- **`shell`** runs the pinned shellcheck over tracked scripts, and runs
  `scripts/*_test.sh`: a shell script that makes decisions (today, the agent
  gate hook — ADR-0022) carries a test held to the same failure-and-boundary
  bar as Go code (`docs/Contributing.md`).
- **`actionlint`** covers workflow YAML, running the pinned shellcheck against
  `run:` blocks too.
- **`links`** (`lychee --offline` over tracked markdown) checks internal links
  only, so it stays deterministic.
- **`docrefs`** fails when a backtick path or package-qualified Go identifier in
  README, `CLAUDE.md`, `docs/`, `ADR/` or the committed skills doesn't resolve
  against tracked files. Its rules (`gate_docrefs`) and allowlist
  (`DOCREF_ALLOW`) live in `scripts/check.sh`.
- **`secrets`** runs gitleaks, pinned, with its **default rules** over the git
  history reachable from `HEAD`. A root `.gitleaks.toml` / `.gitleaksignore`
  that differs from `HEAD` fails the gate, so only a committed one can change
  what it checks.

### Not gates

- **`govulncheck`** runs as a scheduled workflow
  (`.github/workflows/vulncheck.yml`, weekly + `workflow_dispatch`) with its
  own pinned version — the one exception to versions-living-in-`check.sh`,
  because it deliberately isn't a gate. A red run there is a to-do, not a
  broken build.
- **Organisation-specific leak patterns** (internal hostnames, ticket keys) live
  in an optional per-clone config that the agent hook (ADR-0022) applies —
  `docs/Contributing.md` — not in the `secrets` gate.
- **Rejected**, so they aren't "helpfully" re-added: markdownlint and vale
  (prose-style churn, not correctness), yamllint (actionlint already covers the
  YAML that matters), hadolint (one small Dockerfile doesn't justify a
  dedicated linter), nilaway (too false-positive-heavy on this codebase's
  patterns).

## Rationale

"Maximal minus style dogma" keeps the signal-to-noise ratio high: the gate
stays green in steady state, so any red genuinely means something. Encoding the
layering in `depguard` + `go-arch-lint` makes ADR-0012's structure executable
rather than aspirational — a disallowed import fails CI instead of surviving
review.

Each repository gate closes a gap that had let a class of mistake through
unnoticed. `docrefs` is the mechanical half of doc-check's stale-claim check
(ADR-0014) — the half `links` can't do, because lychee skips code spans — so a
renamed file or a deleted symbol fails the gate instead of waiting for a review
to notice it. A committed token is the costliest mistake a contributor to a
public repo can make, and one a machine can decide; the scan is of history, not
the working tree, because a token deleted in a later commit is still published.
A coverage floor that relies on someone remembering to raise it stops
ratcheting, hence the slack check.

Determinism decides what is *not* a gate. A new CVE disclosure can flip
`govulncheck` red with no code change, which would break "green is
deterministic, local == CI". Organisation-specific leak patterns can't be
committed — that would publish the very strings they guard — and CI can't read
a file that isn't committed.

## Consequences

- New opinionated style linters shipped by future golangci-lint versions may
  need adding to the exclusion list. The two pattern-based disables
  (`contextcheck`, `bodyclose`) should stay off as long as the
  detached-log-context and `do()`/`decodeJSON` patterns remain; revisit them
  only if those patterns change.
- New packages must be added to `.go-arch-lint.yml` with their allowed edges, or
  the arch check will flag them as unmapped. The `deepScan` exclusion is pinned
  to `cmd/devpit/main.go`: if the `engine.WithNotifier` wiring moves to another
  file, or a second composition-root file is added, `excludeFiles` must be
  updated in the same change or `deepScan` will resurface the `api -> engine`
  false positive.
- A gate's command, flags, and pinned version live only in `scripts/check.sh` —
  the workflow only invokes it — so bumping a version touches the script alone;
  adding a gate also adds or joins a CI job that invokes it.
- Tools that walk the filesystem rather than tracked files need explicit
  exclusions. `go-arch-lint` excludes local git worktrees under `.claude/` and
  `frontend/node_modules` in `.go-arch-lint.yml` (`exclude:`); gofmt, shell, and
  links check tracked files only for the same reason. A dependency under
  `frontend/node_modules` ships a stray `.go` file, so `frontend/go.mod` declares
  `frontend/` a separate (source-free) Go module, keeping it out of
  `go build`/`vet`/`test ./...` and golangci-lint.
- `secrets` is scoped to `HEAD` rather than gitleaks' default `--all`, so a
  local run can't differ from CI's by scanning other local branches; the CI jobs
  that run it check out with `fetch-depth: 0` for the same reason.
