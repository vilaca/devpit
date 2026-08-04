#!/usr/bin/env bash
#
# uninstall.sh — dev convenience: undo a local systemd (user) install of devpit.
#
# Reverses the Linux install from the README (binary in ~/.local/bin, unit in
# the systemd user dir): stop + disable the service, remove the binary and unit,
# reload systemd. A dev/source-tree helper, not shipped with releases — brew
# users run `brew uninstall devpit`, Docker users `docker rm`.
#
# Local state is KEPT by default — the config holds your tokens and the DB holds
# "Handle next" pins and hover history (the only non-rebuildable state, ADR-0023).
# Pass --purge to also remove them.
#
# Usage: scripts/uninstall.sh [--purge]
set -euo pipefail

PURGE=0
case "${1:-}" in
  "")        ;;
  --purge)   PURGE=1 ;;
  -h|--help) sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
  *)         echo "unknown argument: $1 (use --purge or --help)" >&2; exit 2 ;;
esac

CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}"
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
BIN="$HOME/.local/bin/devpit"
UNIT="$CONFIG_HOME/systemd/user/devpit.service"

# Stop and disable first (harmless if it was never enabled or systemd is absent).
if command -v systemctl >/dev/null 2>&1; then
  systemctl --user disable --now devpit 2>/dev/null || true
fi

rm -f "$BIN" "$UNIT"
command -v systemctl >/dev/null 2>&1 && systemctl --user daemon-reload 2>/dev/null || true
echo "Removed the devpit binary and systemd user unit."

if (( PURGE )); then
  rm -rf "$CONFIG_HOME/devpit" "$DATA_HOME/devpit"
  echo "Purged $CONFIG_HOME/devpit and $DATA_HOME/devpit."
  echo "If your config set a custom db_path outside there, remove it by hand."
else
  echo "Kept local state (pass --purge to remove):"
  echo "  config: $CONFIG_HOME/devpit/"
  echo "  data:   your configured db_path (default example: $DATA_HOME/devpit/)"
fi
