# Implementation plan: provider pagination + coverage gaps

Source: housekeeping review, 2026-07-10 (P1 items 4–6). This plan is
self-contained — every reference below was line-checked against the current
tree. Work the steps in order; each compiles and passes tests on its own.
Recommended model: Opus (real code work, not mechanical).

## Invariants — do not violate

- No change to the `sdk.Provider` contract (`sdk/provider.go`) or the event
  taxonomy. This is a correctness + test-coverage pass, nothing else.
- Provider-specific vocabulary stays inside the provider packages; the two
  providers do **not** share code (ADR-0003) — duplicate any pagination helper
  per provider rather than extracting a common one.
- Error classification semantics (which status → which `sdk.Err*`) stay exactly
  as they are today; step 2/3 only *test* them, they do not change them.

## Step 1 — Provider pagination (latent bug: silent truncation)

Neither provider follows pagination, so large result sets are silently
truncated to the first page.

- **GitHub `search()`** — `provider/github/reconcile.go:101`, hits
  `/search/issues?q=...` (`:102`) and reads only page 1. GitHub's Search API
  returns 30 items/page by default. A user with >30 open items in any single
  role scope (review-requested / assignee / author) loses the overflow from the
  reconcile sweep. Fix: follow the `Link:` header `rel="next"` until exhausted,
  or at minimum append `&per_page=100` and log a warning when a full page comes
  back (so truncation is visible, never silent).
- **GitLab `/merge_requests`** — `provider/gitlab/reconcile.go:35` (loops over
  the fixed `scope` values), single request per scope. Fix: follow the
  `X-Next-Page` response header (GitLab's pagination cursor), or `per_page=100`
  + warn.
- **GitLab `/todos`** — `provider/gitlab/fastpoll.go:28`
  (`/todos?state=pending`), same single-request issue, same fix.

Note GitHub `do()` takes an `http.Header` (conditional-request support);
GitLab `do()` (`gitlab.go:61`) does not — keep that asymmetry.

Add a test per provider that serves a paginated fixture (page 1 with a
next-page header, page 2 without) and asserts all items are returned.

## Step 2 — Provider error / rate-limit tests (biggest real coverage gap)

`github_test.go` and `gitlab_test.go` exercise **zero** error branches today;
this is what the 77% / 75% coverage hides, and it's the layer that decides
backoff. Add table-driven tests feeding synthetic responses:

- **Status classification** in `do()`: `provider/github/github.go:72` and
  `provider/gitlab/gitlab.go:61`. Cover 401→`ErrUnauthorized`,
  403/429→`ErrRateLimited`/`RateLimitError`, 5xx→`StatusError`/`ErrServer`,
  other non-2xx→`ErrUnexpected`. Assert the returned sentinel via `errors.Is`
  and the `StatusError.Status` value.
- **`parseRateDelay`** — `provider/github/github.go:111`: reads `Retry-After`
  (seconds) and `X-Ratelimit-Reset` (epoch) and returns the larger. Table:
  only-Retry-After, only-Reset, both (larger wins), neither (zero).
- **`rateRemaining`** — `github.go:137` (`X-Ratelimit-Remaining`) and
  `gitlab.go:108` (`Ratelimit-Remaining` — different header name). Cover
  present/absent/malformed → `*int` vs nil.

## Step 3 — Storage sync-log tests (feed the connection-health UI)

Both are untested and both feed `Server.computeHealth`
(`internal/api/connections.go:68` → `:70`, `:75`), so a regression silently
corrupts the connection-health display.

- **`ReadSyncLogSince`** — `internal/storage/storage.go:312`: seed rows across a
  time boundary, assert only rows at/after `since` return, in the documented
  order. (`TestSyncLogOrdering` covers `ReadSyncLog` at `:290` but not this.)
- **`LastSyncedAt`** — `storage.go:328`: assert the latest successful sync
  timestamp; cover the no-rows case (zero `time.Time`).

## Done when

- `go test ./... -cover` passes; provider and storage coverage rise (target:
  provider error paths and both storage methods exercised).
- `golangci-lint run ./...` and `go-arch-lint check` stay green.
- Pagination verified by a fixture-backed test in each provider.
