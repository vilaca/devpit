#!/usr/bin/env bash
#
# check-tokens.sh — validate that the tokens in a DevPit config actually work
# and carry the scopes DevPit needs, by probing the real forge endpoints.
#
# This is a read-only diagnostic (matching DevPit itself, ADR-0017): it only
# issues GETs plus a harmless GraphQL `viewer`/`currentUser` query against the
# hosts in your config — it never writes. It exists because a dead or
# under-scoped token is not a startup crash; DevPit just shows a red health dot
# (docs/Token_Setup.md, ADR-0018), so this script tells you *why* before you run.
#
# For each connection it reports one line per capability (item discovery, fast
# signals, merge gate) so a partial token — e.g. a GitHub fine-grained PAT that
# can't reach the notifications feed — shows up as a WARN, not a blanket pass.
# The capability→scope mapping is documented in docs/Token_Setup.md; the
# endpoints mirror the provider packages (provider/github, provider/gitlab,
# internal/jira).
#
# Usage:
#   scripts/check-tokens.sh [config.yaml]
#
# With no argument it uses the same default path as the binary
# ($XDG_CONFIG_HOME/devpit/config.yaml, else ~/.config/devpit/config.yaml —
# internal/config DefaultPath).
#
# Requires: yq (v4, github.com/mikefarah/yq) to read the nested YAML, and curl.
# Exit status: non-zero if any REQUIRED capability failed on any connection;
# WARN-only results still exit 0.

set -uo pipefail

UA="devpit/0.1" # match the providers' User-Agent (provider/*/*.go)
FAILS=0

die() {
	echo "error: $*" >&2
	exit 1
}

command -v yq >/dev/null 2>&1 ||
	die "yq is required to read the YAML config — install it (brew install yq, or see github.com/mikefarah/yq)"
command -v curl >/dev/null 2>&1 || die "curl is required but was not found on PATH"

CONFIG=${1:-"${XDG_CONFIG_HOME:-$HOME/.config}/devpit/config.yaml"}
[ -f "$CONFIG" ] || die "config not found: $CONFIG (pass a path as the first argument)"

# Scratch files for the current probe's body and response headers.
BODY=$(mktemp)
HDR=$(mktemp)
trap 'rm -f "$BODY" "$HDR"' EXIT

# yqv reads a scalar at the given path, printing empty (not "null") when absent.
yqv() { yq -r "$1 // \"\"" "$CONFIG"; }

# get performs an authenticated GET, capturing the body and response headers.
# Args: <url> <auth-header-value> [accept]. Prints the HTTP status code (empty
# on a transport error).
get() {
	local url=$1 auth=$2 accept=${3:-application/json}
	curl -sS -m 30 -o "$BODY" -D "$HDR" -w '%{http_code}' \
		-H "Authorization: $auth" -H "Accept: $accept" -H "User-Agent: $UA" \
		"$url" 2>/dev/null
}

# post performs an authenticated POST of a JSON body (for GraphQL probes).
# Args: <url> <auth-header-value> <json>. Prints the HTTP status code.
post() {
	local url=$1 auth=$2 data=$3
	curl -sS -m 30 -o "$BODY" -D /dev/null -w '%{http_code}' \
		-X POST -H "Authorization: $auth" -H "Content-Type: application/json" \
		-H "User-Agent: $UA" --data "$data" "$url" 2>/dev/null
}

# report prints one capability line and, for a REQUIRED capability, records a
# failure. Args: <required:0|1> <label> <status:ok|warn|fail> <detail>.
report() {
	local required=$1 label=$2 status=$3 detail=$4 mark
	case "$status" in
	ok) mark=" ok " ;;
	warn) mark="warn" ;;
	fail)
		mark="FAIL"
		[ "$required" -eq 1 ] && FAILS=$((FAILS + 1))
		;;
	esac
	printf '    [%s] %-22s %s\n' "$mark" "$label" "$detail"
}

# classify turns an HTTP status code into ok/warn/fail for a capability probe.
# A required probe treats "missing" as fail; an optional one downgrades to warn.
# Args: <required:0|1> <label> <code>.
classify() {
	local required=$1 label=$2 code=$3
	if [ -z "$code" ]; then
		report "$required" "$label" fail "connection error (host unreachable?)"
		return
	fi
	case "$code" in
	2*) report "$required" "$label" ok "HTTP $code" ;;
	401) report "$required" "$label" fail "HTTP 401 — token invalid or expired" ;;
	403 | 404)
		if [ "$required" -eq 1 ]; then
			report 1 "$label" fail "HTTP $code — missing scope or no access"
		else
			report 0 "$label" warn "HTTP $code — capability unavailable with this token"
		fi
		;;
	429) report "$required" "$label" warn "HTTP 429 — rate limited, retry later" ;;
	*) report "$required" "$label" fail "HTTP $code" ;;
	esac
}

check_github() {
	local base=$1 token=$2 api graphql b
	b=$(printf '%s' "$base" | sed 's:/*$::') # trim trailing slashes (provider apiBase)
	case "$b" in
	"" | https://github.com | http://github.com) api="https://api.github.com" ;;
	*) api="$b/api/v3" ;;
	esac
	graphql="${api%/v3}/graphql"
	local auth="token $token"

	# Identity — the baseline "is this token alive" probe (provider/github/identity.go).
	local code
	code=$(get "$api/user" "$auth" "application/vnd.github+json")
	classify 1 "identity (GET /user)" "$code"
	if [ "$code" = "401" ]; then return; fi # dead token — the rest is noise

	# Granted scopes, when GitHub exposes them (classic PATs only).
	local scopes
	scopes=$(grep -i '^x-oauth-scopes:' "$HDR" | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')
	if [ -n "$scopes" ]; then
		report 0 "granted scopes" ok "classic PAT: $scopes"
	else
		report 0 "granted scopes" ok "fine-grained PAT (no scope header; probed functionally)"
	fi

	# Item discovery — the 15-minute reconcile (provider/github/reconcile.go).
	code=$(get "$api/search/issues?q=is:pr+review-requested:@me&per_page=1" "$auth" "application/vnd.github+json")
	classify 1 "item discovery (search)" "$code"

	# Fast signals — notifications feed; classic-PAT-only (docs/Token_Setup.md).
	code=$(get "$api/notifications?per_page=1" "$auth" "application/vnd.github+json")
	classify 0 "fast signals (notifications)" "$code"

	# Merge gate — GraphQL (provider/github/graphql.go).
	code=$(post "$graphql" "bearer $token" '{"query":"{viewer{login}}"}')
	if [ "$code" = "200" ] && grep -q '"errors"' "$BODY"; then
		report 1 "merge gate (GraphQL)" warn "HTTP 200 but GraphQL returned errors"
	else
		classify 1 "merge gate (GraphQL)" "$code"
	fi
}

check_gitlab() {
	local base=$1 token=$2 api graphql b
	b=$(printf '%s' "$base" | sed 's:/*$::')
	[ -z "$b" ] && b="https://gitlab.com"
	api="$b/api/v4"
	graphql="$b/api/graphql"
	local auth="Bearer $token"

	# Identity (provider/gitlab/identity.go).
	local code
	code=$(get "$api/user" "$auth")
	classify 1 "identity (GET /user)" "$code"
	if [ "$code" = "401" ]; then return; fi

	# Granted scopes — GitLab lets a token read itself.
	code=$(get "$api/personal_access_tokens/self" "$auth")
	if [ "$code" = "200" ]; then
		local scopes
		scopes=$(yq -r -p json '.scopes | join(",")' "$BODY" 2>/dev/null)
		if printf '%s' "$scopes" | grep -qw 'read_api\|api'; then
			report 0 "granted scopes" ok "$scopes"
		else
			report 0 "granted scopes" warn "read_api not found (scopes: ${scopes:-none})"
		fi
	else
		report 0 "granted scopes" warn "HTTP $code reading token scopes"
	fi

	# Item discovery — assigned MRs (provider/gitlab/reconcile.go).
	code=$(get "$api/merge_requests?scope=assigned_to_me&per_page=1" "$auth")
	classify 1 "item discovery (MRs)" "$code"

	# Fast signals — todos feed (provider/gitlab/fastpoll.go).
	code=$(get "$api/todos?per_page=1" "$auth")
	classify 1 "fast signals (todos)" "$code"

	# Merge gate — GraphQL (provider/gitlab/graphql.go).
	code=$(post "$graphql" "$auth" '{"query":"{currentUser{username}}"}')
	if [ "$code" = "200" ] && grep -q '"errors"' "$BODY"; then
		report 1 "merge gate (GraphQL)" warn "HTTP 200 but GraphQL returned errors"
	else
		classify 1 "merge gate (GraphQL)" "$code"
	fi
}

check_jira() {
	local base=$1 email=$2 token=$3 jbase auth
	jbase=$(printf '%s' "$base" | sed 's:/*$::')
	# Jira Cloud basic auth: base64(email:token) (internal/jira/client.go).
	auth="Basic $(printf '%s' "$email:$token" | base64 | tr -d '\n')"
	local code
	code=$(get "$jbase/rest/api/3/myself" "$auth")
	classify 1 "identity (GET /myself)" "$code"
}

# ---- run over every connection, then the optional jira block ----------------

echo "Checking tokens in $CONFIG"
echo

count=$(yq -r '.connections | length' "$CONFIG")
[ "$count" -gt 0 ] 2>/dev/null || die "no connections found in $CONFIG"

i=0
while [ "$i" -lt "$count" ]; do
	id=$(yqv ".connections[$i].id")
	type=$(yqv ".connections[$i].type")
	token=$(yqv ".connections[$i].token")
	base=$(yqv ".connections[$i].base_url")
	label=$(yqv ".connections[$i].label")
	[ -n "$label" ] || label="$id"

	printf '%s (%s) — %s\n' "$id" "$type" "${base:-default host}"
	if [ -z "$token" ]; then
		report 1 "token present" fail "no token set in config"
	else
		case "$type" in
		github) check_github "$base" "$token" ;;
		gitlab) check_gitlab "$base" "$token" ;;
		*) report 1 "provider" fail "unknown provider type: $type" ;;
		esac
	fi
	echo
	i=$((i + 1))
done

if [ "$(yq -r '.jira // ""' "$CONFIG")" != "" ]; then
	jbase=$(yqv ".jira.base_url")
	jemail=$(yqv ".jira.email")
	jtoken=$(yqv ".jira.api_token")
	printf 'jira — %s\n' "$jbase"
	if [ -z "$jtoken" ] || [ -z "$jemail" ]; then
		report 1 "credentials present" fail "email and api_token are both required"
	else
		check_jira "$jbase" "$jemail" "$jtoken"
	fi
	echo
fi

if [ "$FAILS" -gt 0 ]; then
	echo "$FAILS required check(s) failed — see FAIL lines above."
	exit 1
fi
echo "All required checks passed."
