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
# Gates: gofmt build vet test lint arch frontend
#   lint = golangci-lint, arch = go-arch-lint, frontend = svelte-check.
#   gofmt and frontend are included on purpose — both are recurring sources of
#   after-the-fact "style: gofmt" / "fix: svelte-check" churn that the old CI
#   never caught.
set -uo pipefail   # NOT -e: run every requested gate, report all failures at the end.

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

CI_MODE=0
[[ "${1:-}" == "--ci" ]] && { CI_MODE=1; shift; }

# Pinned; the external linters are installed on demand (a fresh clone and CI
# have neither). Bump the version here and it changes for local and CI together.
GOLANGCI_VERSION="v2.12.2"
ARCHLINT_VERSION="v1.16.0"

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
gate_test()  { go test ./...; }
gate_lint()  { ensure_golangci && golangci-lint run; }
gate_arch()  { ensure_tool go-arch-lint "github.com/fe3dback/go-arch-lint@$ARCHLINT_VERSION" && go-arch-lint check; }
gate_frontend() {
  # svelte-check needs deps; install them the way start.sh does when absent/stale.
  if [[ ! -d frontend/node_modules || frontend/package-lock.json -nt frontend/node_modules ]]; then
    npm --prefix frontend ci || return 1
  fi
  npm --prefix frontend run check
}

ALL_GATES=(gofmt build vet test lint arch frontend)

# --- select which gates to run ---------------------------------------------
case "${1:-}" in
  "")            gates=("${ALL_GATES[@]}") ;;
  --no-frontend) gates=(gofmt build vet test lint arch) ;;
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
