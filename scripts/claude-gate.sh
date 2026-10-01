#!/usr/bin/env bash
#
# claude-gate.sh — Claude Code PreToolUse hook on Bash (.claude/settings.json,
# ADR-0022): gates an agent's `git commit` / `git push` in this repo.
#
#   git commit → the per-clone leak rules (below) over the change and message
#   git push   → (and `gh pr create` without --head, which can push)
#                a clean tree; a green scripts/check.sh; the per-clone leak
#                rules over the history and commit messages reachable from HEAD;
#                and a recorded /doc-check + /semantic-check review of HEAD
#
# Every other command passes straight through. Exit 2 blocks the tool call and
# feeds stderr back to the agent. The hook's `if` filters (`Bash(git *)`,
# `Bash(gh pr *)`) match parsed subcommands; the regexes below re-check the text
# (and are the only filter on Claude Code versions without `if`). A guard
# against accidents, not a lock: a push hidden inside another script isn't seen.
#
# A hook can't run a skill, so the review is the agent's job: the push is
# blocked with instructions until `scripts/claude-gate.sh --reviewed` records
# the reviewed HEAD. A green check.sh is cached per HEAD the same way — on a
# clean tree its gates are deterministic, so a re-push needn't re-run them.
#
# Per-clone leak rules: an optional gitleaks config at
# .git/info/gitleaks-local.toml, never committed (docs/Contributing.md). With
# no such file, the leak steps are skipped.
set -uo pipefail

block() { echo "claude-gate: $*" >&2; exit 2; }
stamp() { # stamp <name> — per-worktree file holding the HEAD it was recorded for
  local dir; dir="$(git rev-parse --git-path claude-gate)"
  mkdir -p "$dir" && echo "$dir/$1"
}

if [[ ${1:-} == --reviewed ]]; then
  cd "$(git rev-parse --show-toplevel)" || exit 1
  [[ -z "$(git status --porcelain)" ]] || { echo "commit the review's fixes first" >&2; exit 1; }
  git rev-parse HEAD >"$(stamp reviewed)" && echo "claude-gate: review recorded for $(git rev-parse --short HEAD)"
  exit
fi

input="$(cat)"
if command -v jq >/dev/null; then
  cmd="$(jq -r '.tool_input.command // empty' <<<"$input")"
  cwd="$(jq -r '.cwd // empty' <<<"$input")"
else
  cmd="$input" cwd=""   # raw JSON still contains the command text
fi

# `git [-C dir] push|commit` anywhere in the line (chains, `cd x && git push`).
git_re='(^|[[:space:];&|("])git([[:space:]]+-C[[:space:]]+[^[:space:]]+)?[[:space:]]+'
is_push=0 is_commit=0
[[ $cmd =~ ${git_re}push([[:space:]]|$) ]] && is_push=1
[[ $cmd =~ ${git_re}commit([[:space:]]|$) ]] && is_commit=1
# `gh pr create` pushes an unpushed branch unless --head names one (gh help).
[[ $cmd =~ (^|[[:space:]\;\&\|\(\"])gh[[:space:]]+pr[[:space:]]+create([[:space:]]|$) \
   && ! $cmd =~ [[:space:]](--head|-H)([[:space:]=]|$) ]] && is_push=1
(( is_push || is_commit )) || exit 0

cd "${cwd:-.}" 2>/dev/null && root="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
cd "$root" || exit 0

leak_rules="$(git rev-parse --git-common-dir)/info/gitleaks-local.toml"
gitleaks_local() { # gitleaks_local <gitleaks args…> — scan with the per-clone rules
  [[ -x bin/tools/gitleaks ]] || scripts/check.sh secrets >/dev/null 2>&1
  bin/tools/gitleaks --no-banner --redact --config "$leak_rules" "$@"
}

if (( is_commit )) && [[ -f $leak_rules ]]; then
  # Staged and unstaged (for `commit -a`), plus the command text, which holds
  # the -m message.
  gitleaks_local git --pre-commit --staged . >&2 \
    && gitleaks_local git --pre-commit . >&2 \
    && printf '%s\n' "$cmd" | gitleaks_local stdin >&2 \
    || block "per-clone leak rule matched the commit (see above)"
fi

if (( is_push )); then
  if [[ -n "$(git status --porcelain)" ]]; then
    (( is_commit )) && block "uncommitted changes — this hook runs before the command, so a push chained after a commit sees the tree as it is now; run the commit, then the push"
    block "uncommitted changes — check.sh tests the working tree, not what you push; commit or stash first"
  fi

  head="$(git rev-parse HEAD)"
  base="$(git rev-parse --abbrev-ref '@{upstream}' 2>/dev/null || echo origin/main)"
  [[ "$(git rev-list --count "$base..HEAD" 2>/dev/null)" == 0 ]] && exit 0  # no new commits

  if [[ "$(cat "$(stamp green)" 2>/dev/null)" != "$head" ]]; then
    log="$(mktemp "${TMPDIR:-/tmp}/claude-gate.XXXXXX")"
    if ! scripts/check.sh >"$log" 2>&1; then
      tail -n 30 "$log" >&2
      block "scripts/check.sh is red — fix it before pushing (full log: $log)"
    fi
    rm -f "$log"
    echo "$head" >"$(stamp green)"
  fi

  if [[ -f $leak_rules ]]; then
    gitleaks_local git --log-opts=HEAD . >&2 \
      && git log --format=%B HEAD | gitleaks_local stdin >&2 \
      || block "per-clone leak rule matched the history reachable from HEAD (see above)"
  fi

  [[ "$(cat "$(stamp reviewed)" 2>/dev/null)" == "$head" ]] || block "check.sh is green, but HEAD ${head:0:7} has no recorded review.
Review what this push adds ($base...HEAD) before pushing:
  1. Run /doc-check $base in a subagent, and at the same time run
     /semantic-check $base yourself (it fans out its own subagents).
  2. Fix what they find, or surface it to the user; commit any fixes.
  3. Run scripts/claude-gate.sh --reviewed, then retry the push."
fi
exit 0
