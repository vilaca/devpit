#!/usr/bin/env bash
#
# db-trim.sh — retention pass over a stopped DevPit database.
#
# The read model folds only the LATEST item.observed snapshot per item
# (internal/attention/fold.go; internal/storage AllOpenTicketKeys uses the same
# max(id) group-by), so older snapshots and spent signals are safe to drop —
# that is what makes trimming lossless for what the UI shows. This script
# applies the retention rules from docs/Event_Taxonomy_and_Storage.md:
#
#   1. Per item, delete superseded item.observed snapshots (all but the latest)
#      older than the cutoff — NEVER an item's latest snapshot.
#   2. Delete signal.* events older than the cutoff.
#   3. Purge, entirely, every row of any item whose latest state is
#      merged/closed/removed and whose newest event is older than the cutoff.
#   4. Purge rows of connections no longer in the config (orphans). Requires
#      --config; skipped with a warning otherwise — never guessed.
#   5. Delete sync_log rows older than the cutoff.
#
# "Purge the item" includes its "Handle next" pins, which carry no connection_id
# and must be matched by recomputing each item's opaque handle (db-common.sh).
#
# Usage:
#   scripts/db-trim.sh <db> [--older-than DAYS] [--config PATH] [--dry-run]
#
# Cutoffs compare against observed_at / ts (RFC 3339 UTC), the timestamps the
# engine actually writes.

set -euo pipefail

cd "$(dirname "$0")/.."
# shellcheck source=scripts/db-common.sh
. scripts/db-common.sh

DB=""
DAYS=90
CONFIG=""
DRY_RUN=0
while [ $# -gt 0 ]; do
	case "$1" in
	--older-than)
		[ $# -ge 2 ] || die "--older-than needs a value (days)"
		DAYS=$2
		shift 2
		;;
	--config)
		[ $# -ge 2 ] || die "--config needs a path"
		CONFIG=$2
		shift 2
		;;
	--dry-run)
		DRY_RUN=1
		shift
		;;
	-*) die "unknown flag: $1" ;;
	*)
		[ -z "$DB" ] || die "unexpected extra argument: $1"
		DB=$1
		shift
		;;
	esac
done

[ -n "$DB" ] || die "usage: scripts/db-trim.sh <db> [--older-than DAYS] [--config PATH] [--dry-run]"
[ -f "$DB" ] || die "database not found: $DB"
case "$DAYS" in
'' | *[!0-9]*) die "--older-than must be a whole number of days, got: $DAYS" ;;
esac
require_sqlite3
refuse_if_live "$DB"

CUT=$(cutoff_rfc3339 "$DAYS")
echo "Cutoff: $CUT (older than $DAYS days)"

# DEAD_SELECT yields the identity triples of items to purge wholesale: latest
# state is terminal (merged/closed) or a removal supersedes the last snapshot,
# AND the item's newest event predates the cutoff.
DEAD_SELECT="
WITH latest_obs AS (
  SELECT connection_id, object_type, native_id, oid, state FROM (
    SELECT connection_id, object_type, native_id, id AS oid,
           json_extract(payload,'\$.state') AS state,
           ROW_NUMBER() OVER (PARTITION BY connection_id,object_type,native_id ORDER BY id DESC) AS rn
    FROM events WHERE event_type='item.observed'
  ) WHERE rn=1
),
latest_rem AS (
  SELECT connection_id, object_type, native_id, max(id) AS rid
  FROM events WHERE event_type='item.removed'
  GROUP BY connection_id, object_type, native_id
),
item_last AS (
  SELECT connection_id, object_type, native_id, max(observed_at) AS last_at
  FROM events GROUP BY connection_id, object_type, native_id
)
SELECT il.connection_id, il.object_type, il.native_id
FROM item_last il
LEFT JOIN latest_obs lo USING (connection_id, object_type, native_id)
LEFT JOIN latest_rem lr USING (connection_id, object_type, native_id)
WHERE il.last_at < '$CUT'
  AND ( (lr.rid IS NOT NULL AND (lo.oid IS NULL OR lr.rid > lo.oid))
     OR (lo.oid IS NOT NULL AND lo.state IN ('merged','closed')) )
"

# Superseded snapshots: non-latest item.observed rows older than the cutoff.
SUPERSEDED_WHERE="
event_type='item.observed' AND observed_at < '$CUT'
AND id <> (SELECT max(id) FROM events e2
           WHERE e2.event_type='item.observed'
             AND e2.connection_id=events.connection_id
             AND e2.object_type=events.object_type
             AND e2.native_id=events.native_id)
"

# --- Orphan connections (config-gated) -------------------------------------
KEEP_LIST=""
ORPHAN_ENABLED=0
if [ -n "$CONFIG" ]; then
	if [ ! -f "$CONFIG" ]; then
		echo "warning: --config $CONFIG not found; skipping orphan-connection purge" >&2
	else
		# Simplest thing that works: connection ids are the `id:` keys (the jira
		# block and top-level keys carry none). Strip quotes and surrounding space.
		# Plain read loop (not mapfile) so this runs on stock macOS bash 3.2 too.
		CONF_IDS=()
		while IFS= read -r id; do
			[ -n "$id" ] && CONF_IDS+=("$id")
		done < <(grep -E '^[[:space:]]*-?[[:space:]]*id:[[:space:]]*' "$CONFIG" |
			sed -E "s/^[[:space:]]*-?[[:space:]]*id:[[:space:]]*//; s/[\"']//g; s/[[:space:]]*\$//")
		if [ "${#CONF_IDS[@]}" -eq 0 ]; then
			echo "warning: no connection ids parsed from $CONFIG; skipping orphan-connection purge" >&2
		else
			for id in "${CONF_IDS[@]}"; do
				KEEP_LIST="${KEEP_LIST:+$KEEP_LIST,}'$(sql_quote "$id")'"
			done
			ORPHAN_ENABLED=1
		fi
	fi
fi

# --- Report -----------------------------------------------------------------
echo "Projected trim (independent per-category counts):"
printf '  %-28s %s\n' "dead items to purge" \
	"$(count_scalar "$DB" "SELECT count(*) FROM ($DEAD_SELECT);")"
printf '  %-28s %s\n' "  their event rows" \
	"$(count_scalar "$DB" "SELECT count(*) FROM events WHERE (connection_id,object_type,native_id) IN ($DEAD_SELECT);")"
printf '  %-28s %s\n' "superseded snapshots" \
	"$(count_scalar "$DB" "SELECT count(*) FROM events WHERE $SUPERSEDED_WHERE;")"
printf '  %-28s %s\n' "old signal events" \
	"$(count_scalar "$DB" "SELECT count(*) FROM events WHERE event_type LIKE 'signal.%' AND observed_at < '$CUT';")"
printf '  %-28s %s\n' "old sync_log rows" \
	"$(count_scalar "$DB" "SELECT count(*) FROM sync_log WHERE ts < '$CUT';")"
if [ "$ORPHAN_ENABLED" -eq 1 ]; then
	printf '  %-28s %s\n' "orphan event rows" \
		"$(count_scalar "$DB" "SELECT count(*) FROM events WHERE connection_id NOT IN ($KEEP_LIST);")"
fi

if [ "$DRY_RUN" -eq 1 ]; then
	echo "[dry-run] no changes made"
	exit 0
fi

# Recompute the handle_next handles for the items being purged (dead items, and
# orphan-connection items) before their events are deleted.
DEAD_PINS=$(pin_in_list "$DB" "$DEAD_SELECT")
ORPHAN_PINS=""
if [ "$ORPHAN_ENABLED" -eq 1 ]; then
	ORPHAN_PINS=$(pin_in_list "$DB" \
		"SELECT DISTINCT connection_id, object_type, native_id FROM events WHERE connection_id NOT IN ($KEEP_LIST);")
fi

{
	echo ".bail on"
	echo "BEGIN;"

	if [ "$ORPHAN_ENABLED" -eq 1 ]; then
		echo "DELETE FROM events         WHERE connection_id NOT IN ($KEEP_LIST); SELECT 'orphan events:      ' || changes();"
		echo "DELETE FROM sync_cursors   WHERE connection_id NOT IN ($KEEP_LIST);"
		echo "DELETE FROM sync_log       WHERE connection_id NOT IN ($KEEP_LIST);"
		echo "DELETE FROM repo_approvers WHERE connection_id NOT IN ($KEEP_LIST);"
		[ -n "$ORPHAN_PINS" ] && echo "DELETE FROM handle_next WHERE item_id IN ($ORPHAN_PINS);"
	fi

	echo "DELETE FROM events WHERE (connection_id,object_type,native_id) IN ($DEAD_SELECT); SELECT 'dead-item rows:     ' || changes();"
	[ -n "$DEAD_PINS" ] && echo "DELETE FROM handle_next WHERE item_id IN ($DEAD_PINS);"

	echo "DELETE FROM events WHERE $SUPERSEDED_WHERE; SELECT 'superseded snaps:   ' || changes();"
	echo "DELETE FROM events WHERE event_type LIKE 'signal.%' AND observed_at < '$CUT'; SELECT 'old signals:        ' || changes();"
	echo "DELETE FROM sync_log WHERE ts < '$CUT'; SELECT 'old sync_log rows:  ' || changes();"

	echo "COMMIT;"
	echo "VACUUM;"
} | sqlite3 "$DB"

echo "Done — trim complete."
