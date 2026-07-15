#!/usr/bin/env bash
#
# db-reset.sh — factory-reset a stopped DevPit database by EMPTYING it.
#
# Every user table is truncated; schema_version is kept so the next start
# migrates cleanly from the current schema version rather than re-running every
# migration. The file itself is never deleted: a bind-mounted DB file must keep
# its inode (a Docker/compose volume mount would otherwise break), and
# storage.Open migrates unconditionally on an empty schema anyway.
#
# The table list is read from sqlite_master, so a table added in a future
# migration is emptied automatically — nothing to forget here.
#
# Usage:
#   scripts/db-reset.sh <db> [--dry-run]
#
# The event store is a rebuildable cache (ADR-0023): after a reset the next
# poll cycle re-syncs everything from the forges. The only real loss is
# local-only state — "Handle next" pins, onset-duration history, and the sync
# log — none of which lives on any forge.

set -euo pipefail

cd "$(dirname "$0")/.."
# shellcheck source=scripts/db-common.sh
. scripts/db-common.sh

DB=""
DRY_RUN=0
for arg in "$@"; do
	case "$arg" in
	--dry-run) DRY_RUN=1 ;;
	-*) die "unknown flag: $arg" ;;
	*)
		[ -z "$DB" ] || die "unexpected extra argument: $arg"
		DB=$arg
		;;
	esac
done

[ -n "$DB" ] || die "usage: scripts/db-reset.sh <db> [--dry-run]"
[ -f "$DB" ] || die "database not found: $DB"
require_sqlite3
refuse_if_live "$DB"

# User tables to empty: everything except schema_version and SQLite internals.
# Plain read loop (not mapfile) so this runs on stock macOS bash 3.2 too.
TABLES=()
while IFS= read -r t; do
	[ -n "$t" ] && TABLES+=("$t")
done < <(sqlite3 -noheader "$DB" \
	"SELECT name FROM sqlite_master
	 WHERE type='table' AND name != 'schema_version' AND name NOT LIKE 'sqlite_%'
	 ORDER BY name;")

[ "${#TABLES[@]}" -gt 0 ] || die "no user tables found — is this a DevPit database?"

echo "Tables to empty (schema_version kept):"
for t in "${TABLES[@]}"; do
	printf '  %-16s %s rows\n' "$t" "$(count_scalar "$DB" "SELECT count(*) FROM \"$t\";")"
done

if [ "$DRY_RUN" -eq 1 ]; then
	echo "[dry-run] no changes made"
	exit 0
fi

{
	echo ".bail on"
	echo "BEGIN;"
	for t in "${TABLES[@]}"; do
		echo "DELETE FROM \"$t\";"
	done
	echo "COMMIT;"
	echo "VACUUM;"
} | sqlite3 "$DB"

echo "Done — database emptied (schema_version kept):"
for t in "${TABLES[@]}"; do
	printf '  %-16s %s rows\n' "$t" "$(count_scalar "$DB" "SELECT count(*) FROM \"$t\";")"
done
echo
echo 'Note: "Handle next" pins are gone; the next start re-syncs every connection from scratch.'
