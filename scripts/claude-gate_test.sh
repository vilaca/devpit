#!/usr/bin/env bash
#
# claude-gate_test.sh — behaviour tests for scripts/claude-gate.sh, run by the
# `shell` gate in scripts/check.sh.
#
# Each case group builds a throwaway repo (with a bare "origin") carrying the
# real hook plus two stubs: a scripts/check.sh that counts its runs and goes red
# when STUB_RED is set, and a bin/tools/gitleaks that flags the token LEAKME in
# whatever the real one would scan for the same arguments. The stubs pin the
# hook's wiring — what it scans, when it blocks, what it caches — not gitleaks
# or check.sh themselves.
set -uo pipefail

HOOK="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/claude-gate.sh"
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1   # the machine's git config can't leak in
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAILS=0

new_repo() { # new_repo <name> — fresh repo, one commit pushed to its origin; cd into it
  local d="$TMP/$1"
  git init -q --bare "$d.origin.git"
  git init -q "$d" && cd "$d" || exit 1
  git checkout -q -b main
  mkdir -p scripts bin/tools
  cp "$HOOK" scripts/claude-gate.sh
  cat >scripts/check.sh <<'EOF'
#!/usr/bin/env bash
echo run >>"$(git rev-parse --git-dir)/check-runs"
[[ -z ${STUB_RED:-} ]] || { echo "FAILED: lint"; exit 1; }
echo "All gates passed."
EOF
  cat >bin/tools/gitleaks <<'EOF'
#!/usr/bin/env bash
# Stub: exit 1 iff LEAKME is in what the real gitleaks would scan for these args.
mode="" staged=0 precommit=0 logopts=""
for a in "$@"; do
  case $a in
    git|stdin) mode=$a ;;
    --staged) staged=1 ;;
    --pre-commit) precommit=1 ;;
    --log-opts=*) logopts=${a#--log-opts=} ;;
  esac
done
case $mode in
  stdin) text="$(cat)" ;;
  git) if (( precommit && staged )); then text="$(git diff --cached)"
       elif (( precommit )); then text="$(git diff)"
       else text="$(git log -p --format= "$logopts")"; fi ;;   # patches only, no messages
esac
[[ $text != *LEAKME* ]]
EOF
  chmod +x scripts/*.sh bin/tools/gitleaks
  echo /bin/ >.gitignore
  echo hello >notes
  git add -A && git commit -qm init
  git remote add origin "$d.origin.git" && git push -qu origin main
}

run_hook() { # run_hook <command> — feed the hook a Bash tool call; sets OUT and RC
  local json
  json="$(printf '{"tool_name":"Bash","cwd":"%s","tool_input":{"command":"%s"}}' "$PWD" "${1//\"/\\\"}")"
  OUT="$(scripts/claude-gate.sh <<<"$json" 2>&1)"
  RC=$?
}
expect() { # expect <label> <exit code> [<text the output must contain>]
  if [[ $RC == "$2" && ( -z ${3:-} || $OUT == *"$3"* ) ]]; then
    echo "ok   $1"
  else
    echo "FAIL $1 — exit $RC, want $2${3:+ with output containing \"$3\"}"
    sed 's/^/     | /' <<<"$OUT"
    FAILS=$((FAILS + 1))
  fi
}
runs() { # how many times the stub check.sh has run in this repo
  local f; f="$(git rev-parse --git-dir)/check-runs"
  if [[ -f $f ]]; then wc -l <"$f" | tr -d ' '; else echo 0; fi
}
expect_runs() { # expect_runs <label> <count>
  local n; n="$(runs)"
  if [[ $n == "$2" ]]; then
    echo "ok   $1"
  else
    echo "FAIL $1 — check.sh ran $n time(s), want $2"
    FAILS=$((FAILS + 1))
  fi
}

echo "== push"
new_repo push
run_hook "ls -la";                  expect "a non-git command passes" 0
run_hook "git status";              expect "another git command passes" 0
run_hook "git push";                expect "nothing new to push passes" 0
git commit -q --allow-empty -m one
run_hook "git push origin main";    expect "a push without a review is blocked" 2 "no recorded review"
expect_runs "check.sh ran for the new HEAD" 1
run_hook "git push";                expect "a re-push is still blocked" 2 "no recorded review"
expect_runs "a green check.sh is cached per HEAD" 1
scripts/claude-gate.sh --reviewed >/dev/null
run_hook "cd . && git push";        expect "a push after --reviewed passes" 0
git commit -q --allow-empty -m two
run_hook "git -C . push";           expect "a new commit needs a new review (git -C form)" 2 "no recorded review"
expect_runs "a new HEAD re-runs check.sh" 2
echo x >dirty
run_hook "git push";                expect "a dirty tree is blocked" 2 "uncommitted changes"
OUT="$(scripts/claude-gate.sh --reviewed 2>&1)"; RC=$?
expect "--reviewed refuses a dirty tree" 1 "commit the review's fixes first"
rm dirty
git commit -q --allow-empty -m red
STUB_RED=1 run_hook "git push";     expect "a red check.sh blocks with its summary" 2 "FAILED: lint"
run_hook "git commit -m x && git push"
expect "a push chained after a commit is still gated" 2 "no recorded review"
echo x >dirty && git add dirty
run_hook "git commit -m x && git push"
expect "a chained commit+push with changes says to split it" 2 "run the commit, then the push"
git rm -q --cached dirty && rm dirty
run_hook "gh pr create --fill";     expect "gh pr create (which can push) is gated" 2 "no recorded review"
run_hook "gh pr create --head x --fill"
expect "gh pr create --head (which doesn't push) passes" 0
run_hook "gh pr list";              expect "other gh commands pass" 0

echo "== leak rules"
new_repo leaks
rules="$(git rev-parse --git-common-dir)/info/gitleaks-local.toml"
run_hook 'git commit -m "LEAKME"';  expect "without a rules file, leak steps are skipped" 0
touch "$rules"
run_hook 'git commit -m "fix: see LEAKME"'
expect "a leak in the commit message is blocked" 2 "leak rule"
echo LEAKME >f && git add f
run_hook 'git commit -m "fix: clean"'
expect "a leak in the staged change is blocked" 2 "leak rule"
git rm -q --cached f && rm f
echo LEAKME >>notes
run_hook 'git commit -am "fix: clean"'
expect "a leak in an unstaged change (commit -a) is blocked" 2 "leak rule"
git checkout -q notes
run_hook 'git commit -m "fix: clean"'
expect "a clean commit passes" 0
echo LEAKME >>notes && git commit -qam "fix: clean message"
scripts/claude-gate.sh --reviewed >/dev/null
run_hook "git push";                expect "a leak in the pushed history is blocked" 2 "history reachable from HEAD"
git reset -q --hard origin/main
git commit -q --allow-empty -m "chore: LEAKME in a message"
scripts/claude-gate.sh --reviewed >/dev/null
run_hook "git push";                expect "a leak in a pushed commit message is blocked" 2 "history reachable from HEAD"

echo
if (( FAILS )); then echo "claude-gate_test: $FAILS failed"; exit 1; fi
echo "claude-gate_test: all passed"
