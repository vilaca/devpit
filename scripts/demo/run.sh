#!/usr/bin/env bash
#
# run.sh — start the DevPit demo world: the mock forge + a devpit instance
# pointed at it, seeded from the fixtures under scripts/demo/fixtures.
#
# One command. It builds the SPA + devpit (via the canonical scripts/start.sh)
# and the mock forge, wipes the scratch DB for a fresh world, starts both
# processes, pins the "Handle next" item once the first sync lands, and prints
# the URL. Ctrl-C tears both down.
#
#   scripts/demo/run.sh
#
# The list fills within ~60s (reconcile is immediate; GitHub notification
# signals arrive on the first fast-poll tick). This is the source of the hero
# screenshot (docs/assets/hero.png) and every UI demo — never a real instance.
# Extend the world by editing fixtures only; see scripts/demo/README.md.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEMO="$REPO/scripts/demo"
FORGE_ADDR="localhost:9099"
APP_ADDR="localhost:7474"
SCRATCH="$DEMO/.scratch"

# The item pinned into "Handle next". Pins are local DB state (ADR-0016), wiped
# with the scratch DB, so run.sh re-applies it every run. The item id is
# derived exactly as the read layer does: first 8 bytes of
# sha256(connection_id \0 object_type \0 native_id), hex.
PIN_CONN="github-cloud"
PIN_NATIVE="acme/checkout-api#482"

sha256hex() { if command -v shasum >/dev/null 2>&1; then shasum -a 256; else sha256sum; fi; }
pin_id() { printf '%s\000merge_request\000%s' "$PIN_CONN" "$PIN_NATIVE" | sha256hex | cut -c1-16; }

# pin_when_ready waits for the target item to appear in /attention, then flags
# it. Runs in the background so it doesn't block the foreground processes.
pin_when_ready() {
  local id; id="$(pin_id)"
  for _ in $(seq 1 120); do
    if curl -sf "http://$APP_ADDR/attention" 2>/dev/null | grep -q "\"$id\""; then
      curl -sf -X PUT "http://$APP_ADDR/items/$id/flag" >/dev/null 2>&1 \
        && echo "==> Pinned \"Handle next\" item ($PIN_NATIVE)"
      return
    fi
    sleep 1
  done
  echo "==> warn: pin target $PIN_NATIVE never appeared; skipping pin"
}

cd "$REPO"

# Build the SPA + devpit binary the canonical way, then the mock forge.
echo "==> Building devpit (frontend + backend)"
scripts/start.sh --no-start
echo "==> Building demo-forge"
go build -o "$REPO/bin/demo-forge" ./scripts/demo

# Fresh world every run: drop the scratch DB so pins/history don't accumulate.
rm -rf "$SCRATCH"
mkdir -p "$SCRATCH"

pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT INT TERM

# Free the app port if a stale devpit is holding it (and the DB lock).
existing="$(lsof -nP -iTCP:"${APP_ADDR##*:}" -sTCP:LISTEN -t 2>/dev/null || true)"
# shellcheck disable=SC2086
[[ -n "$existing" ]] && { kill $existing 2>/dev/null || true; sleep 0.5; }

echo "==> Starting demo-forge on http://$FORGE_ADDR"
"$REPO/bin/demo-forge" -addr "$FORGE_ADDR" -fixtures "$DEMO/fixtures" &
pids+=($!)

# Wait for the forge to accept connections before devpit's first sync fires.
for _ in $(seq 1 50); do
  curl -sf "http://$FORGE_ADDR/gh/api/v3/user" >/dev/null 2>&1 && break
  sleep 0.1
done

echo "==> Starting devpit on http://$APP_ADDR"
"$REPO/bin/devpit" --config "$DEMO/config.yaml" &
pids+=($!)

pin_when_ready &

echo
echo "  demo-forge  http://$FORGE_ADDR   (mock GitHub + GitLab)"
echo "  DevPit      http://$APP_ADDR   (open this; Ctrl-C to stop both)"
echo
wait
