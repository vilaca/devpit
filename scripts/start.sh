#!/usr/bin/env bash
#
# start.sh — rebuild the DevPit frontend + backend and (re)start the binary.
#
# DevPit is a single binary that embeds the built SPA (internal/web/dist) at
# compile time and serves both the API and the UI from localhost:7474 while
# holding an exclusive lock on the SQLite DB. So the order is load-bearing:
#
#   1. build the frontend   → vite writes internal/web/dist
#   2. build the backend     → go embeds that fresh dist into the binary
#   3. stop the old instance → it holds the port + DB lock; a clash is fatal
#   4. start the new instance
#
# Steps are "as needed": a missing running instance is not an error, and the
# frontend build is skipped with --no-frontend when only backend changed.
#
# Usage:
#   scripts/start.sh                 # full rebuild + restart
#   scripts/start.sh --no-frontend   # skip the vite build (backend-only change)
#   scripts/start.sh --no-start      # build only, don't (re)start
#   scripts/start.sh -- --config X   # pass args after -- through to devpit
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO/bin/devpit"
ADDR="localhost:7474"
PORT="${ADDR##*:}"

build_frontend=1
do_start=1
devpit_args=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-frontend) build_frontend=0 ;;
    --no-start)    do_start=0 ;;
    --)            shift; devpit_args=("$@"); break ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

cd "$REPO"
mkdir -p "$REPO/bin"

if [[ "$build_frontend" == 1 ]]; then
  # node_modules missing (fresh clone) or older than the lockfile (dep change).
  if [[ ! -d frontend/node_modules || frontend/package-lock.json -nt frontend/node_modules ]]; then
    echo "==> Installing frontend deps (npm ci)"
    npm --prefix frontend ci
  fi
  echo "==> Building frontend (vite → internal/web/dist)"
  npm --prefix frontend run build
else
  echo "==> Skipping frontend build (--no-frontend)"
fi

echo "==> Building backend (embeds internal/web/dist)"
go build -o "$BIN" ./cmd/devpit

# PIDs listening on the port. Filtered to LISTEN so we match the server, never
# a client (e.g. a browser tab connected to the dashboard also has a socket on
# this port — killing that would be wrong).
listener_pids() {
  lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -t 2>/dev/null || true
}

# Stop whatever is listening on the port — that is the process owning the DB
# lock, regardless of how it was launched (go run, an old bin, etc.). We wait
# for the process to fully exit, not just for the port to free: devpit frees
# the listener before it closes the DB, so the fresh instance would otherwise
# race the old one on the SQLite lock and die on startup.
stop_running() {
  local pids p alive
  pids="$(listener_pids)"
  [[ -z "$pids" ]] && { echo "==> Nothing listening on $ADDR"; return; }
  echo "==> Stopping running instance on $ADDR (pid: $pids)"
  # shellcheck disable=SC2086
  kill -TERM $pids 2>/dev/null || true
  for _ in $(seq 1 100); do
    alive=""
    for p in $pids; do kill -0 "$p" 2>/dev/null && alive=1; done
    [[ -z "$alive" ]] && break
    sleep 0.1
  done
  # Force any straggler that ignored SIGTERM, then confirm the port is free.
  for p in $pids; do kill -0 "$p" 2>/dev/null && kill -KILL "$p" 2>/dev/null || true; done
  for _ in $(seq 1 50); do
    [[ -z "$(listener_pids)" ]] && break
    sleep 0.1
  done
}

if [[ "$do_start" == 0 ]]; then
  echo "==> Build complete (--no-start); binary at $BIN"
  exit 0
fi

stop_running

# Run devpit in the foreground: this script becomes the service, so it keeps
# running with logs on the terminal until you stop it with Ctrl-C (SIGINT,
# which devpit drains gracefully). exec replaces the shell so there is no extra
# process and signals go straight to devpit.
echo "==> Starting devpit on http://$ADDR (Ctrl-C to stop)"
exec "$BIN" "${devpit_args[@]}"
