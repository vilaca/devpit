# Releasing

How a maintainer cuts a DevPit release. The pipeline itself — goreleaser plus a
tag-triggered GitHub Actions workflow — and every decision behind it are recorded
in [`ADR-0023`](../ADR/ADR-0023_Packaging_Distribution_and_Release_Pipeline.md);
the artifact matrix and release checklist live in
[`docs/Roadmap.md`](Roadmap.md). This page is the procedure.

A release is one `git tag` push. Everything after that is automated by
[`.github/workflows/release.yml`](../.github/workflows/release.yml), which runs
the full gate and then [`.goreleaser.yaml`](../.goreleaser.yaml).

> **Operational prerequisites** (maintainer-held, one-time): the
> `vilaca/homebrew-devpit` tap repo and the `HOMEBREW_TAP_TOKEN` repo secret must
> exist before the first tag (see ADR-0023 → Consequences). Creating them is
> release-execution setup, not part of this recurring procedure.

## Normal path

1. **Pre-flight** — on an up-to-date `main`:
   - `scripts/check.sh` is green (the gate; it is what the release workflow runs
     via `--ci`). A tag on a red commit produces no artifacts — the pipeline
     fails at its `gate` job by design.
   - `/doc-check` is clean.
   - If the UI changed since the last release, the hero screenshot is fresh —
     re-capture it from the demo world (see [Screenshot refresh](#screenshot-refresh)).
   - The [`docs/Roadmap.md`](Roadmap.md) section for this release is accurate.
2. **Tag and push:**

   ```sh
   git tag v0.1.6
   git push origin v0.1.6
   ```

   The version is stamped into the binary from the tag (`-ldflags -X
   main.version`), so the tag *is* the version — there is nothing else to bump.
3. **Watch it.** The push triggers the `Release` workflow (Actions tab → most
   recent `Release` run). Job `gate` runs every gate; job `release` builds the
   five platform archives, the multi-arch image, the checksums, the GitHub
   Release, and pushes the Homebrew formula to the tap.

## Post-release verification

Run through this once the workflow is green:

- **GitHub Release** has five archives (darwin amd64/arm64, linux amd64/arm64,
  windows amd64) plus `checksums.txt`, and a grouped changelog.
- **Homebrew** installs on both Mac architectures:

  ```sh
  brew install vilaca/devpit/devpit
  ```

  (arm64 and the x86 test machine — arch-specific bottles/binaries).
- **Docker** smoke test: `/up` answers `200` and the UI loads.

  ```sh
  docker run --rm -p 127.0.0.1:7474:7474 \
    -v "$PWD/config.yaml:/etc/devpit/config.yaml:ro" \
    ghcr.io/vilaca/devpit:0.1.6   # goreleaser tags without the "v"; or :latest
  # in another shell: curl -fsS http://127.0.0.1:7474/up && open http://127.0.0.1:7474
  ```

  The container config must set `listen: ":7474"` (see the README's Docker
  section).
- **`devpit --version`** prints the released version.
- **Update chip:** point an *older* binary at the real releases feed and confirm
  the quiet "update available" chip appears in the TopBar. The checker polls
  `https://api.github.com/repos/vilaca/devpit/releases/latest`
  ([`internal/update`](../internal/update/update.go)); a build older than the tag
  you just pushed will see it.

## Manual fallback

When Actions is unavailable, cut the release from a maintainer machine. The tag
and a green `scripts/check.sh` remain mandatory — the automation's guarantees are
your responsibility here.

1. Install the **pinned** goreleaser version (must match the pin in
   [`.github/workflows/release.yml`](../.github/workflows/release.yml)):

   ```sh
   go install github.com/goreleaser/goreleaser/v2@v2.9.0
   ```

   > The pin is deliberately held below v2.10, which deprecated formula
   > generation (`brews`) in favour of casks — and `brew services start devpit`
   > is formula-only. Don't bump it without migrating the Homebrew config to a
   > cask and rehoming the macOS service onto launchd (ADR-0023 forward dep).

2. Export the same credentials the workflow injects:

   ```sh
   export GITHUB_TOKEN=…          # a token with contents + packages write
   export HOMEBREW_TAP_TOKEN=…    # pushes the tap formula
   ```

3. With the tag checked out and `scripts/check.sh` green, and Docker running (for
   the image) and logged in to `ghcr.io`:

   ```sh
   goreleaser release --clean
   ```

Use this only for an Actions outage; the tag-triggered workflow is the normal
path.

## Screenshot refresh

For a UI-visible release, re-capture the hero screenshot from the committed demo
world before tagging — never from a real instance. The capture step and the demo
forge are documented in [`scripts/demo/README.md`](../scripts/demo/README.md).

## Snapshot check (no tag, no publish)

Before trusting a pipeline change, dry-run it locally with the pinned goreleaser:

```sh
goreleaser check                       # validate .goreleaser.yaml
goreleaser release --snapshot --clean  # build every artifact, publish nothing
```

Snapshot builds all five platform binaries and, with Docker running, both image
architectures — without touching GitHub, GHCR, or the tap.
