#!/usr/bin/env bash
#
# db-common.sh — shared helpers for the maintainer-only DB scripts
# (db-trim.sh, db-cleanup.sh, db-reset.sh). Sourced, never run directly.
#
# These scripts operate on a STOPPED DevPit's SQLite database with raw SQL via
# the sqlite3 CLI (ADR-0023: DB maintenance is maintainer-operated, not shipped
# in the binary or the web UI). The two facts that shape this file:
#
#   1. A live DevPit holds a BSD advisory flock(2) on "<db>.lock" for its whole
#      lifetime (internal/storage: acquireLock / flockFile). Mutating the DB
#      underneath it would clobber the running engine, so every script refuses
#      to run while that lock is held. There is no `flock` CLI on macOS, so the
#      probe uses perl (same flock(2) syscall) and falls back to the util-linux
#      `flock` binary.
#   2. handle_next.item_id is NOT the identity triple — it is the item's opaque
#      public handle: hex of the first 8 bytes of
#      SHA-256(connection_id "\0" object_type "\0" native_id)
#      (internal/attention/fold.go itemID). SQLite has no SHA-256, so pins can
#      only be matched to a connection/item by recomputing that hash here.

# die prints an error to stderr and exits non-zero.
die() {
	echo "error: $*" >&2
	exit 1
}

# require_sqlite3 aborts unless the sqlite3 CLI is on PATH.
require_sqlite3() {
	command -v sqlite3 >/dev/null 2>&1 ||
		die "the sqlite3 CLI is required but was not found on PATH"
}

# sql_quote escapes a value for use inside a single-quoted SQL string literal by
# doubling embedded single quotes. Use it for every interpolated shell value.
sql_quote() {
	printf '%s' "$1" | sed "s/'/''/g"
}

# refuse_if_live aborts if a running DevPit holds the advisory lock on the
# database at $1. It mirrors internal/storage acquireLock: a non-blocking
# exclusive flock(2) on the sibling "<db>.lock" file. A held lock means an
# instance is running; a missing lock file means it was never opened (or is
# cleanly gone) and cannot be held.
refuse_if_live() {
	local db=$1 lock="$1.lock"

	# No lock file => no instance can be holding it.
	[ -e "$lock" ] || return 0

	if command -v perl >/dev/null 2>&1; then
		# perl flock() is the same flock(2) syscall the Go code uses. LOCK_NB
		# returns false when another process holds LOCK_EX. Exit 3 == held.
		# Capture the status with `|| rc=$?` so `set -e` doesn't abort here
		# before we can turn a held lock into a friendly message.
		local rc=0
		perl -e '
			use Fcntl qw(:flock);
			open(my $fh, ">>", $ARGV[0]) or exit 0; # unopenable => do not block
			exit(flock($fh, LOCK_EX | LOCK_NB) ? 0 : 3);
		' "$lock" || rc=$?
		[ "$rc" -eq 3 ] && die "database \"$db\" is in use — stop devpit first"
	elif command -v flock >/dev/null 2>&1; then
		# util-linux flock (Linux). Append (9>>) so we never truncate the file.
		if ! (exec 9>>"$lock"; flock -n 9) 2>/dev/null; then
			die "database \"$db\" is in use — stop devpit first"
		fi
	else
		echo "warning: cannot verify devpit is stopped (no perl or flock CLI);" \
			"make sure it is not running before continuing" >&2
	fi
}

# cutoff_rfc3339 prints the RFC 3339 UTC timestamp $1 days before now, matching
# the storage timeFormat (…Z, no sub-seconds). observed_at / ts columns are
# stored in this exact format, so a lexicographic "< cutoff" comparison is a
# correct chronological one.
cutoff_rfc3339() {
	perl -e '
		use POSIX qw(strftime);
		print strftime("%Y-%m-%dT%H:%M:%SZ", gmtime(time - $ARGV[0] * 86400));
	' "$1"
}

# hashes_from_triples reads "connection_id<TAB>object_type<TAB>native_id" lines
# on stdin and prints each item's 16-char hex handle (see the header note on
# handle_next.item_id). Digest::SHA is a core perl module.
hashes_from_triples() {
	perl -MDigest::SHA=sha256_hex -ne '
		chomp;
		next if $_ eq "";
		my ($c, $ot, $nid) = split /\t/, $_, 3;
		print substr(sha256_hex($c . "\0" . $ot . "\0" . $nid), 0, 16), "\n";
	'
}

# build_in_list reads one value per line on stdin and prints a SQL IN() body:
# 'a','b','c'. Empty stdin prints nothing (caller must skip the DELETE). Values
# are item handles (16 hex chars), so no escaping is needed.
build_in_list() {
	sed -e '/^$/d' -e "s/^/'/" -e "s/\$/'/" | paste -sd, -
}

# pin_in_list runs a triple-producing query against the DB and returns the SQL
# IN() body of the matching handle_next handles (empty if none). $1 = db path,
# $2 = a SELECT yielding connection_id, object_type, native_id rows.
pin_in_list() {
	local db=$1 query=$2
	sqlite3 -noheader -separator "$(printf '\t')" "$db" "$query" |
		hashes_from_triples | build_in_list
}

# count_scalar prints the integer result of a single-value SELECT.
count_scalar() {
	local db=$1 query=$2
	sqlite3 -noheader "$db" "$query"
}
