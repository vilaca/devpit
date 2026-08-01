#!/usr/bin/env bash
#
# check.sh — the single gate runner: every quality gate, in one command.
#
# This is the ONE source of truth for what "green" means. CI runs these same
# gates through this script (.github/workflows/ci.yml), one gate per job so a
# failing check names the gate — but the command, flags, and pinned linter
# versions for each gate live *here*, so CI and local can't drift.
#
# Run it before calling a change done. It prints a per-gate summary so you know
# which gate failed, not just that one did.
#
#   scripts/check.sh                 # every gate
#   scripts/check.sh --no-frontend   # every gate except the frontend
#   scripts/check.sh GATE [GATE...]  # only the named gate(s)
#   scripts/check.sh --ci GATE ...   # how CI invokes it, one gate per job:
#                                    # same gates, CI-only install fast paths
#
# Gates: gofmt build vet test lint arch shell frontend tidy actionlint links
#   lint = golangci-lint, arch = go-arch-lint, shell = shellcheck,
#   frontend = svelte-check + eslint + prettier --check, tidy = go mod tidy -diff,
#   actionlint = workflow YAML + embedded shellcheck, links = lychee (offline,
#   internal markdown links only).
#   gofmt, shell, and frontend are included on purpose — all recurring sources of
#   after-the-fact "style: gofmt" / "fix: svelte-check" / broken-script churn
#   that the old CI never caught. govulncheck is deliberately NOT a gate here —
#   see .github/workflows/vulncheck.yml.
set -uo pipefail   # NOT -e: run every requested gate, report all failures at the end.

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO" || exit 1

CI_MODE=0
[[ "${1:-}" == "--ci" ]] && { CI_MODE=1; shift; }

# Pinned; the external linters are installed on demand (a fresh clone and CI
# have neither). Bump the version here and it changes for local and CI together.
GOLANGCI_VERSION="v2.12.2"
ARCHLINT_VERSION="v1.16.0"
SHELLCHECK_VERSION="v0.10.0"
ACTIONLINT_VERSION="v1.7.12"
LYCHEE_VERSION="v0.24.2"

# Linters run from a repo-local dir, keyed by a version stamp: the pinned
# version is the one that runs even if a different build of the same tool is on
# the machine's PATH, and bumping a pin forces a reinstall. (A plain
# `command -v` check would use whatever is installed — the pin would only
# apply on first install, and local could drift from CI.) bin/ is gitignored.
TOOLS="$REPO/bin/tools"
mkdir -p "$TOOLS"
export PATH="$TOOLS:$PATH"

have_tool()  { [[ -x "$TOOLS/$1" && -e "$TOOLS/.$1-$2" ]]; }
stamp_tool() { rm -f "$TOOLS/.$1-"*; touch "$TOOLS/.$1-$2"; }

ensure_tool() { # ensure_tool <binary> <module@version>
  have_tool "$1" "${2##*@}" && return 0
  echo "    installing $1 ($2)…"
  GOBIN="$TOOLS" go install "$2" && stamp_tool "$1" "${2##*@}"
}

# golangci-lint: under --ci fetch the prebuilt release binary — `go install`
# pays a multi-minute source build on every cold CI run. Locally stay with
# `go install` (no curl|sh on dev machines). Both paths pin GOLANGCI_VERSION.
ensure_golangci() {
  if (( CI_MODE )); then
    have_tool golangci-lint "$GOLANGCI_VERSION" && return 0
    echo "    installing golangci-lint ($GOLANGCI_VERSION, release binary)…"
    curl -sSfL "https://raw.githubusercontent.com/golangci/golangci-lint/$GOLANGCI_VERSION/install.sh" \
      | sh -s -- -b "$TOOLS" "$GOLANGCI_VERSION" \
      && stamp_tool golangci-lint "$GOLANGCI_VERSION"
  else
    ensure_tool golangci-lint "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GOLANGCI_VERSION"
  fi
}

# ShellCheck ships prebuilt release binaries only (no `go install`), so local and
# CI both fetch the pinned tarball — the one pin drives both, same as the others.
# (A comment starting with the bare word "shellcheck" is read as a directive.)
ensure_shellcheck() {
  have_tool shellcheck "$SHELLCHECK_VERSION" && return 0
  local os arch
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux)  os=linux ;;
    *) echo "    shellcheck: unsupported OS $(uname -s)" >&2; return 1 ;;
  esac
  case "$(uname -m)" in
    arm64|aarch64) arch=aarch64 ;;
    x86_64|amd64)  arch=x86_64 ;;
    *) echo "    shellcheck: unsupported arch $(uname -m)" >&2; return 1 ;;
  esac
  echo "    installing shellcheck ($SHELLCHECK_VERSION, release binary)…"
  local url tmp rc
  url="https://github.com/koalaman/shellcheck/releases/download/$SHELLCHECK_VERSION/shellcheck-$SHELLCHECK_VERSION.$os.$arch.tar.xz"
  tmp="$(mktemp -d)"
  if curl -sSfL "$url" | tar -xJ -C "$tmp" \
     && mv "$tmp/shellcheck-$SHELLCHECK_VERSION/shellcheck" "$TOOLS/shellcheck"; then
    stamp_tool shellcheck "$SHELLCHECK_VERSION"; rc=0
  else
    rc=1
  fi
  rm -rf "$tmp"
  return $rc
}

# lychee also ships prebuilt release binaries only; same download-and-stamp
# pattern as shellcheck. Its release tags carry a "lychee-" prefix, and each
# tarball unpacks into a target-triple directory rather than a flat binary.
ensure_lychee() {
  have_tool lychee "$LYCHEE_VERSION" && return 0
  local target
  case "$(uname -s)-$(uname -m)" in
    Darwin-arm64|Darwin-aarch64)  target=aarch64-apple-darwin ;;
    Darwin-x86_64|Darwin-amd64)   target=x86_64-apple-darwin ;;
    Linux-arm64|Linux-aarch64)    target=aarch64-unknown-linux-gnu ;;
    Linux-x86_64|Linux-amd64)     target=x86_64-unknown-linux-gnu ;;
    *) echo "    lychee: unsupported OS/arch $(uname -s)/$(uname -m)" >&2; return 1 ;;
  esac
  echo "    installing lychee ($LYCHEE_VERSION, release binary)…"
  local url tmp rc
  url="https://github.com/lycheeverse/lychee/releases/download/lychee-$LYCHEE_VERSION/lychee-$target.tar.gz"
  tmp="$(mktemp -d)"
  if curl -sSfL "$url" | tar -xz -C "$tmp" \
     && mv "$tmp/lychee-$target/lychee" "$TOOLS/lychee"; then
    stamp_tool lychee "$LYCHEE_VERSION"; rc=0
  else
    rc=1
  fi
  rm -rf "$tmp"
  return $rc
}

# --- gate definitions: each returns non-zero on failure --------------------
gate_gofmt() {
  # Tracked files only: `gofmt -l .` recurses into hidden dirs, so it would
  # also check agent worktrees under .claude/worktrees/ (the same reason
  # .claude is excluded in .go-arch-lint.yml).
  local unformatted
  unformatted="$(git ls-files -z -- '*.go' | xargs -0 gofmt -l)" || return 1
  [[ -z "$unformatted" ]] && return 0
  echo "not gofmt'd:"; echo "$unformatted"; return 1
}
gate_build() { go build ./...; }
gate_vet()   { go vet ./...; }
gate_test()  { go test -race ./...; }
gate_lint()  { ensure_golangci && golangci-lint run; }
gate_arch()  { ensure_tool go-arch-lint "github.com/fe3dback/go-arch-lint@$ARCHLINT_VERSION" && go-arch-lint check; }
gate_shell() {
  # Tracked shell scripts only (same rationale as gofmt: skip agent worktrees).
  # severity=warning: catch real bugs (unquoted vars, bad redirects, typos), not
  # style/info nags on deliberate idioms — the naggy tier would only breed churn.
  ensure_shellcheck || return 1
  git ls-files -z -- '*.sh' | xargs -0 shellcheck --severity=warning
}
gate_frontend() {
  # svelte-check needs deps; install them the way start.sh does when absent/stale.
  if [[ ! -d frontend/node_modules || frontend/package-lock.json -nt frontend/node_modules ]]; then
    npm --prefix frontend ci || return 1
  fi
  # Three explicit commands, not one chained script, so a failure names the
  # tool that failed (svelte-check vs. eslint vs. prettier) in the output.
  npm --prefix frontend run check \
    && npm --prefix frontend run lint \
    && npm --prefix frontend run format:check
}
gate_tidy() {
  # -diff: report drift without touching go.mod/go.sum; non-zero exit if any.
  go mod tidy -diff
}
gate_actionlint() {
  # actionlint shells out to shellcheck for `run:` blocks when one is on PATH;
  # ensure the pinned shellcheck is installed first so bin/tools (already
  # prepended to PATH) is what it finds, not whatever's on the machine's PATH.
  ensure_shellcheck || return 1
  ensure_tool actionlint "github.com/rhysd/actionlint/cmd/actionlint@$ACTIONLINT_VERSION" || return 1
  actionlint
}
gate_links() {
  # Tracked markdown only (same worktree rationale as gofmt/shell). --offline
  # checks local file links only — external URLs are skipped, not fetched, so
  # the gate stays deterministic (no flakiness from third-party sites).
  ensure_lychee || return 1
  git ls-files -z -- '*.md' | xargs -0 lychee --offline --no-progress
}

ALL_GATES=(gofmt build vet test lint arch shell frontend tidy actionlint links)

# --- select which gates to run ---------------------------------------------
case "${1:-}" in
  "")            gates=("${ALL_GATES[@]}") ;;
  --no-frontend) gates=(gofmt build vet test lint arch shell tidy actionlint links) ;;
  *)             gates=("$@") ;;
esac

FAILED=()
for g in "${gates[@]}"; do
  if ! declare -F "gate_$g" >/dev/null; then
    echo "unknown gate: $g (valid: ${ALL_GATES[*]})" >&2
    exit 2
  fi
  echo "==> $g"
  if "gate_$g"; then echo "    ok: $g"; else echo "    FAIL: $g"; FAILED+=("$g"); fi
done

echo
if [[ ${#FAILED[@]} -eq 0 ]]; then
  echo "All gates passed."
  exit 0
fi
echo "FAILED: ${FAILED[*]}"
exit 1
