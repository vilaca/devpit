#!/usr/bin/env bash
#
# db-cleanup.sh — purge one connection (source) completely from a stopped
# DevPit database.
#
# Deletes every row that belongs to the connection across the tables that carry
# a connection_id — events, sync_cursors, sync_log, repo_approvers — plus its
# "Handle next" pins. Pins can only be matched by recomputing each item's
# opaque handle (see db-common.sh header): handle_next has no connection_id.
# jira_tickets is keyed by ticket key and shared across connections, so it is
# left alone (the running app prunes it against the live open set).
#
# Usage:
#   scripts/db-cleanup.sh <db> <connection-id> [--dry-run]
#
# If the connection still exists in the config, DevPit will simply repopulate
# it on the next sync — allowed, but the script warns so a typo'd id or a
# still-live source is not purged by accident.

set -euo pipefail

cd "$(dirname "$0")/.."
# shellcheck source=scripts/db-common.sh
. scripts/db-common.sh

DB=""
CONN=""
DRY_RUN=0
for arg in "$@"; do
	case "$arg" in
	--dry-run) DRY_RUN=1 ;;
	-*) die "unknown flag: $arg" ;;
	*)
		if [ -z "$DB" ]; then
			DB=$arg
		elif [ -z "$CONN" ]; then
			CONN=$arg
		else
			die "unexpected extra argument: $arg"
		fi
		;;
	esac
done

[ -n "$DB" ] && [ -n "$CONN" ] ||
	die "usage: scripts/db-cleanup.sh <db> <connection-id> [--dry-run]"
[ -f "$DB" ] || die "database not found: $DB"
require_sqlite3
refuse_if_live "$DB"

Q=$(sql_quote "$CONN")

# Warn if the connection is still configured (its rows would come back).
config_path="${XDG_CONFIG_HOME:-$HOME/.config}/devpit/config.yaml"
if [ -f "$config_path" ] && grep -Eq "^[[:space:]]*-?[[:space:]]*id:[[:space:]]*[\"']?${CONN}[\"']?[[:space:]]*$" "$config_path"; then
	echo "warning: connection \"$CONN\" is still present in $config_path;" \
		"it will be repopulated on the next sync" >&2
fi

# Rows per table for this connection (also the dry-run report).
echo "Rows for connection \"$CONN\":"
for t in events sync_cursors sync_log repo_approvers; do
	printf '  %-16s %s\n' "$t" \
		"$(count_scalar "$DB" "SELECT count(*) FROM $t WHERE connection_id='$Q';")"
done

# handle_next pins for this connection's items (recomputed handles).
PIN_LIST=$(pin_in_list "$DB" \
	"SELECT DISTINCT connection_id, object_type, native_id FROM events WHERE connection_id='$Q';")
PIN_COUNT=0
if [ -n "$PIN_LIST" ]; then
	PIN_COUNT=$(count_scalar "$DB" "SELECT count(*) FROM handle_next WHERE item_id IN ($PIN_LIST);")
fi
printf '  %-16s %s\n' "handle_next" "$PIN_COUNT"

if [ "$DRY_RUN" -eq 1 ]; then
	echo "[dry-run] no changes made"
	exit 0
fi

{
	echo ".bail on"
	echo "BEGIN;"
	echo "DELETE FROM events         WHERE connection_id='$Q';"
	echo "DELETE FROM sync_cursors   WHERE connection_id='$Q';"
	echo "DELETE FROM sync_log       WHERE connection_id='$Q';"
	echo "DELETE FROM repo_approvers WHERE connection_id='$Q';"
	[ -n "$PIN_LIST" ] && echo "DELETE FROM handle_next WHERE item_id IN ($PIN_LIST);"
	echo "COMMIT;"
	echo "VACUUM;"
} | sqlite3 "$DB"

echo "Done — purged connection \"$CONN\"."
