# Packaging, Distribution & Release Pipeline

## Scope

Planned — ships as DevPit's first public release. Implemented across the v0.1.6
release work (runtime update check + `listen:` key, maintenance scripts, demo
forge, release pipeline). See `docs/Roadmap.md` for timing and the release
checklist.

## Context

DevPit has been built privately through v0.1.5, run from source. The first
public release turns it into something a stranger can install, run, and keep
current without a Go toolchain. That is one decision set — how the binary is
built, cut into artifacts, distributed (Homebrew, Docker, GitHub Releases),
launched as a service, kept up to date, and operated — bound by a single
rationale (a trustworthy, low-friction first release) and by DevPit's
local-first, read-only posture (`ADR/ADR-0001_Local_First_Web_Application.md`,
`ADR/ADR-0017_Read_Only_Action_Model.md`). Recorded separately these would
cross-reference on every point, so they live here as one ADR.

## Decision

### Release pipeline

- goreleaser (pinned version), triggered by a `v*` tag push, in
  `.github/workflows/release.yml`.
- The full `scripts/check.sh --ci` gate runs **before** goreleaser — a tag on a
  red commit must not ship (gate-before-release). The gate is the definition of
  green (`ADR/ADR-0013_Linting_and_Architecture_Enforcement.md`).
- The frontend is built by a goreleaser `before` hook (`npm ci && npm run
  build`); the binary embeds the SPA via `go:embed`
  (`ADR/ADR-0010_Web_Frontend.md`).
- The release version is stamped into the binary via `-ldflags -X
  main.version={{.Version}}`; an untagged build reports `dev`.

### Artifact matrix

- **Supported:** darwin/amd64, darwin/arm64, linux/amd64, linux/arm64.
- **Best-effort:** windows/amd64 (`.exe`), built because `modernc.org/sqlite` is
  pure Go and cross-compilation is therefore free
  (`ADR/ADR-0009_Implementation_Language.md`). No Windows start script — a README
  note only, untested.
- **Docker image** `ghcr.io/vilaca/devpit` (linux amd64+arm64), tags `latest`
  plus the version. This realizes the container half of
  `ADR/ADR-0011_Deployment_Model.md`, which left it Planned with no Dockerfile.

### Homebrew

- Tap repo `vilaca/homebrew-devpit`; goreleaser generates the formula with a
  `service` block (`brew services start devpit`) and a `test` block running
  `devpit --version`. A `HOMEBREW_TAP_TOKEN` CI secret is a maintainer-held
  operational prerequisite.

### Service integration (the roadmap's "start scripts", reinterpreted)

- No bespoke launch scripts. Instead: the brew `service` block (macOS), a
  committed systemd user unit under `packaging/` (Linux), a `compose.yaml`
  example (Docker), and a README one-liner (Windows). `scripts/start.sh` remains
  a dev-only tool (`docs/Contributing.md`).

### Update check

- The running app polls `GET /repos/vilaca/devpit/releases/latest` at startup
  and every 24 h, comparing the stamped version. Version `dev` skips the check;
  failures log quietly and never surface as errors (the graceful-failure posture
  of `ADR/ADR-0018_Sync_Observability.md`).
- UI: a quiet "update available" chip in the TopBar. Hover shows the upgrade
  command — `brew upgrade vilaca/devpit/devpit`, or `docker pull
  ghcr.io/vilaca/devpit` when `/.dockerenv` is present — and clicking deep-links
  to the release page (a link out, `ADR/ADR-0017_Read_Only_Action_Model.md`).
- **No opt-out, no auto-download, no version-skip memory** — deliberate. The
  check is a single unauthenticated public GitHub call; a self-hosted attention
  tool should tell you when it is stale without being asked, and it still never
  acts on its own.

### `listen:` config key

- A new optional top-level `listen:` key, default `localhost:7474` (behaviour
  unchanged when absent). It exists because a loopback bind inside a container is
  unreachable from the host: a container sets `listen: :7474`. The key and its
  validation are direct code (`internal/config`).
- The host-side loopback-only stance of `ADR/ADR-0001_Local_First_Web_Application.md`
  is preserved by publishing the port as `-p 127.0.0.1:7474:7474` in every
  example, so the app is reachable only from the host's loopback even though the
  in-container bind is on all interfaces. ADR-0001 carries a pointer to this.

### Docker DB is disposable

- The event store is a rebuildable cache
  (`ADR/ADR-0005_Event_Based_Attention_Engine.md`): a fresh DB re-syncs within
  one cycle. The only non-rebuildable state is local-only — "Handle next" pins
  (`ADR/ADR-0017_Read_Only_Action_Model.md`), onset-duration history, and the
  sync log (`ADR/ADR-0018_Sync_Observability.md`). The `compose.yaml` example
  uses a named volume anyway; the README states the volume is optional and what
  skipping it costs. The config mount is always required.

### DB maintenance is maintainer-only

- Retention in this release is three maintainer-operated raw-SQL scripts under
  `scripts/`, run with the instance stopped and requiring the `sqlite3` CLI:
  `db-trim.sh` (retention pass), `db-cleanup.sh <connection-id>` (purge one
  source), and `db-reset.sh` (empties every table — enumerated from
  `sqlite_master`, so schema-agnostic — rather than deleting the file, so a
  bind-mounted DB keeps its inode; `storage.Open` migrates unconditionally
  either way). The retention semantics live in
  `docs/Event_Taxonomy_and_Storage.md`; end users on brew/Docker have no trim
  story this release.

### Demo mock forge

- A committed, reusable mock of both forges under `scripts/demo/` serves fixture
  JSON. The hero screenshot is always captured from it, never from a real
  instance, and re-captured on UI-visible releases.

### History

- No squash or rewrite before release (verified clean; no secrets in any commit).

## Rationale

Single-binary plus pure-Go SQLite (`ADR/ADR-0009_Implementation_Language.md`)
makes cross-compilation and a static image free and Homebrew straightforward, so
a broad artifact matrix costs almost nothing. Gate-before-release keeps
"`check.sh` is the definition of green" (`ADR/ADR-0013_Linting_and_Architecture_Enforcement.md`)
true for published artifacts, not just CI. The update check and the `listen:`
key are both shaped by the local-first, read-only posture: surface staleness but
never self-update (ADR-0017), and widen the bind only inside the container while
keeping host exposure on loopback (ADR-0001). Maintainer-only scripts answer the
one real operational need (DB growth) with the smallest thing that works,
deferring a user-facing feature until a real instance proves it necessary
(`docs/Engineering_Philosophy.md`).

## Consequences

- **Gate-before-release**: a tag pushed onto a red commit produces no
  artifacts — the pipeline fails at the gate.
- **Operational prerequisites** (maintainer-held): the `vilaca/homebrew-devpit`
  tap repo and the `HOMEBREW_TAP_TOKEN` secret must exist before the first tag.
- **The update check has no opt-out** and makes one unauthenticated public
  GitHub request per day; accepted deliberately.
- **`listen:` interacts with ADR-0001**: binding beyond loopback is safe only
  because the published host port stays `127.0.0.1`. Publishing on `0.0.0.0`
  would reintroduce the no-auth exposure ADR-0001 forbids.
- **Docker DB disposability** is an accepted trade-off: losing the volume loses
  pins, onset history, and the sync log — never forge data.
- **Forward dependency**: binary-shipped, user-facing retention ("clear history
  older than X") is deferred to v0.2 (`docs/Roadmap.md`); until then brew/Docker
  users rely on the maintainer scripts or the disposable Docker DB.
- Provider tokens and their exact scopes are documented in `docs/Token_Setup.md`;
  this ADR does not restate them.
