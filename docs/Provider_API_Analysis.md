# Provider API Analysis — GitHub & GitLab (v0.1)

External API research behind the two v0.1 providers
(`ADR/ADR-0003_Provider_Plugin_Model.md`). Maps each attention bucket to exact
GitHub/GitLab API calls, defines the merge-gate field mapping (consumed by the
fold in `docs/Attention_Engine.md`), sets token guidance, and budgets a poll
cycle (`docs/Synchronization_Engine.md`).

All facts verified against official docs / live API in July 2026 unless
flagged. Items marked **[verify]** must be re-checked during
implementation.

## Design-impacting findings (read this first)

1. **GitHub notifications require a classic PAT.** The Notifications API
 "only support[s] authentication using a personal access token
 (classic)" with `notifications` or `repo` scope. Fine-grained PATs
 cannot call it. Classic `repo` grants *write* to private repos — there
 is no read-only classic scope for private repos. Consequence: the
 "notifications as change-signal" tier is **optional** on GitHub and
 the plugin must work without it (search-based polling covers all
 buckets). This is a capability declaration, driven by token type.
2. **GitLab's public REST API has no ETag/304 support.** Conditional
 requests are a GitHub-only optimization. GitLab change detection
 uses `updated_after` watermark polling plus the todos feed.
3. **GitHub REST `mergeable_state` is officially undocumented** (OpenAPI
 type: free-form string). GraphQL `mergeStateStatus` is GA, enum-typed,
 and fetchable in bulk via `search()` — the GitHub plugin should use
 **GraphQL** for its identity-scoped queries, avoiding an N+1 REST call
 per PR for merge-gate state.
4. **GitLab returns `detailed_merge_status` in list responses** — no N+1
 for the merge gate; REST is sufficient for the GitLab plugin.
5. **GitLab "request changes" only blocks the merge gate on
 Premium/Ultimate.** On Free it exists but is informational, so the
 Changes Requested bucket needs the per-MR reviewers endpoint there
 (small bounded N+1 over authored open MRs).

## GitHub

### Identity

`GET /user` (REST) or `viewer { login databaseId }` (GraphQL). Works for
both PAT types. Search supports `@me`, so most queries don't need the
resolved login, but store it for provenance and team-view use.

### Token guidance

| Token | Gets | Loses |
|-----------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------|
| Fine-grained PAT (recommended default) | Search, GraphQL (since 2023-04), PR details, true read-only least privilege. Permissions: Metadata: read, Pull requests: read; add Commit statuses: read + Checks: read for CI, Issues: read for issue mentions. | Notifications API |
| Classic PAT, `notifications` scope only | Notifications feed | Private-repo PR details |
| Classic PAT, `repo` (+`notifications`) | Everything | Read-only guarantee — `repo` grants write to private repos (mitigation broken) |

Setup UI should present fine-grained as the default path and explain the
trade-off if the user wants the notifications fast-signal on private
repos. `GET /search/issues` works with fine-grained PATs and requires no
permissions (results scoped to token visibility).

### Change signal (fast tier, optional)

`GET /notifications` (classic PAT only): optimized for polling with
`Last-Modified` / `If-Modified-Since`; 304 responses **do not count**
against the rate limit; honor the `X-Poll-Interval` header (default
60s). Relevant `reason` values: `review_requested`, `mention`,
`team_mention`, `assign`, `author`, `state_change`, `ci_activity`.

Without a classic PAT the fast tier is the GraphQL search poll below,
run at the same cadence — costs a few points per cycle, which the budget
absorbs easily.

> **Implementation note (as of v0.1.6):** this search-poll fallback and the
> token-driven capability degradation are **not yet implemented**. `FastPoll`
> polls `/notifications` unconditionally and GitHub mention signals arrive only
> from it, so a fine-grained PAT loses the fast tier and mentions entirely (only
> the 3-minute reconcile runs). The practical token trade-off users face is in
> `docs/Token_Setup.md`; the "recommended default" and "optional tier" framing
> here is aspirational until the fallback lands.

### Bucket → call mapping

One GraphQL request per cycle, aliasing multiple `search()` calls
(`type: ISSUE`), each selecting on PullRequest:
`number, title, url, updatedAt, isDraft, mergeStateStatus,
reviewDecision, latestReviews { nodes { state submittedAt author { login } } },
repository { nameWithOwner }`. The `latestReviews` nodes (latest non-dismissed
review per reviewer) power the rank-only `signal.approved` /
`signal.changes_requested` verdict signals; `submittedAt` is the real provider
timestamp used as `OccurredAt` (`docs/Event_Taxonomy_and_Storage.md`,
`ADR/ADR-0016`).

| Bucket | Search query | Post-filter |
|--------------------|----------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------|
| Needs Review | `is:open is:pr review-requested:@me archived:false` | — (`user-review-requested:@me` additionally distinguishes direct from team requests) |
| Changes Requested | `is:open is:pr author:@me archived:false` | `reviewDecision == CHANGES_REQUESTED` |
| Blocked | same authored query | merge-gate mapping below, non-draft |
| Ready to Merge | same authored query | merge-gate mapping below, non-draft |
| Mentioned | `is:open mentions:@me archived:false` | includes issues by design |
| Waiting on Author | `is:open is:pr reviewed-by:@me -review-requested:@me -author:@me archived:false` | — |

Assigned work (discovery per ADR-0004): `is:open assignee:@me`.

Search notes: since 2025-09-04 all issue searches use "advanced search"
semantics — multiple `repo:`/`org:`/`user:` qualifiers AND together
(previously OR). No `advanced_search` param needed anymore.

### Merge-gate mapping (`mergeStateStatus`)

| Value | Meaning | DevPit state |
|-------------|---------------------------------------|------------------------------------------------------|
| `CLEAN` | Mergeable, checks passing | Ready to Merge |
| `HAS_HOOKS` | Mergeable, passing + pre-receive hooks | Ready to Merge |
| `UNSTABLE` | Mergeable with non-passing status | failure notification, **not** Blocked |
| `BLOCKED` | Merge blocked (protection rules) | Blocked |
| `DIRTY` | Merge commit can't be created (conflict) | Blocked |
| `BEHIND` | Head out of date (strict checks) | Blocked |
| `UNKNOWN` | Being computed | transient — keep previous state, re-poll |
| `DRAFT` | deprecated | use `isDraft` instead (drafts never Blocked/RTM) |

Caveat **[verify]**: `mergeStateStatus` is actor-agnostic — it reports
`BLOCKED` even for users whose bypass rights would let them merge
(community-sourced, not official docs).

### Rate budget

- GraphQL: 5,000 points/hour; a multi-search query costs ~1 point per
 100 nodes requested per connection (min 1). A 4-search × 50-node cycle
 is ~2–4 points → polling every 60s ≈ 150–250 points/hour. **~5% of
 budget.**
- REST search (if used instead): 30 requests/min — 4 queries/min fits,
 but GraphQL is preferred anyway.
- REST core: 5,000 req/hour; authorized conditional 304s are free.
- Secondary limits: ≤100 concurrent; ≤2,000 GraphQL points/min; on 429 /
 `retry-after`, honor the header, else wait ≥60s with exponential
 backoff (basic backoff).

## GitLab

### Identity

`GET /user` returns the token owner. Project/group access tokens return
their internal bot user **[verify]** and deploy/CI tokens can't call
`/user` at all — these hit the manual-fallback path. Store the
resolved `id` and `username`; list filters take either, and
`scope=`-style params avoid needing them in most calls.

### Token guidance

PAT with **`read_api`** scope: full read-only API access (user, todos,
MRs, pipelines, approvals). This is the least-privilege ideal — GitLab
is strictly better than GitHub here. No write anywhere (DevPit needs
none; it can't mark todos done, which is fine — read-only).

### Change signal (fast tier)

Two cheap calls per cycle:

- `GET /todos?state=pending` — pending todos for me. Event-typed
 (`action_name`): `review_requested`, `mentioned`,
 `directly_addressed`, `assigned`, `build_failed`, `unmergeable`,
 `review_submitted`, `approval_required`... **Doc/source mismatch
 [verify]:** the docs' filter list omits `review_requested` /
 `review_submitted`, but the source (`app/models/todo.rb`,
 `lib/api/todos.rb`) accepts and emits them — trust the source.
 Caveats: no duplicate pending todo is created while one is pending,
 and many changes create no todo (new commits, pipeline success) — a
 supplementary signal, not a complete feed.
- `GET /merge_requests?scope=all&state=opened&order_by=updated_at&sort=desc&updated_after=<watermark>`
 per relevant scope (see below) — the watermark poll that catches what
 todos miss. Keep a small clock-skew overlap (re-query from
 `watermark − 1min`) and dedupe on `(id, updated_at)`. No ETag/304
 exists on the public API — don't build on it.

### Bucket → call mapping

Global list endpoint, `state=opened`, response includes
`detailed_merge_status`, `draft`, `updated_at`:

| Bucket | Call | Post-filter |
|---------------------------|----------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------|
| Needs Review | `GET /merge_requests?scope=reviews_for_me&state=opened` | my reviewer state ∈ {unreviewed, review_started} via reviewers endpoint (below) |
| Changes Requested | `GET /merge_requests?scope=created_by_me&state=opened` | `detailed_merge_status == requested_changes` (Premium) **or** any reviewer state `requested_changes` via reviewers endpoint (Free) |
| Blocked / Ready to Merge | same authored query | merge-gate mapping below, non-draft |
| Mentioned | `GET /todos?state=pending&action=mentioned` + `directly_addressed` | — |
| Waiting on Author | `scope=reviews_for_me` result | my reviewer state ∈ {reviewed, requested_changes} |

Assigned: `scope=assigned_to_me&state=opened`.

**Reviewer state** comes from
`GET /projects/:id/merge_requests/:iid/reviewers` → `{user, state}`
with states `unreviewed | reviewed | requested_changes | approved |
unapproved | review_started`. Note the `reviewers[]` array embedded in
MR list responses does **not** carry this — its `state` is the user's
*account* state. This is a bounded N+1: only over open MRs where I'm a
reviewer or author, and only when the MR's `updated_at` moved.
The GraphQL join selects `reviewers.nodes.mergeRequestInteraction.reviewState`
and uses it two ways: the `REQUESTED_CHANGES` verdict on any reviewer drives the
author's `changes_requested` signal, and the *viewer's own* node sets
`my_review_state` (`changes_requested` / `reviewed` / `approved`), overridden to
`approved` when they also appear in `approvedBy`. This makes the per-MR reviewers
endpoint above unnecessary. Pending verdicts (`UNREVIEWED`, `REVIEW_STARTED`)
leave `my_review_state` empty so the item stays `review_requested`. The same
`approvedBy` nodes and `REQUESTED_CHANGES` reviewers also drive the rank-only
`signal.approved` / `signal.changes_requested` verdict signals. GitLab has no
verdict timestamp on `approvedBy`/`reviewState`, so on each new or changed
verdict the provider makes one extra REST call — `GET /projects/:id/merge_requests/:iid/notes?order_by=created_at&sort=desc&per_page=100` — to find the
system note's `created_at` (the true verdict time). See the baseline-diff logic
in `ADR/ADR-0016` and `docs/Event_Taxonomy_and_Storage.md`.

### Merge-gate mapping (`detailed_merge_status`)

| Class | Values | DevPit state |
|--------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------|
| Ready | `mergeable` | Ready to Merge |
| Transient | `unchecked`, `checking`, `preparing`, `approvals_syncing`, `ci_still_running` | keep previous state; re-poll (see staleness note) |
| Draft | `draft_status` | never Blocked/RTM (drafts) |
| Gate-blocked | `conflict`, `need_rebase`, `not_approved`, `requested_changes`, `ci_must_pass`, `discussions_not_resolved`, `merge_request_blocked`, `status_checks_must_pass`, `commits_status`, `not_open`, plus tier-specific (`jira_association_missing`, `security_policy_*`, `locked_paths`, `locked_lfs_files`, `title_regex`, `merge_time`) | Blocked |

Staleness note: list endpoints "might not proactively update"
merge status — `unchecked` is common on lists. For items stuck
transient, do a targeted single-MR GET; use
`with_merge_status_recheck=true` sparingly (async, not guaranteed,
can be restricted by a feature flag for sub-Developer roles).
`merge_status` (the old field) is deprecated since 15.6 — never read it.

Version floor: `detailed_merge_status` needs GitLab ≥ 15.6; reviewer
states need ≥ 16.11 (default-on since 17.2). The plugin should degrade its
declared capabilities on older self-hosted instances **[verify at implementation:
minimum supported GitLab version]**.

### Rate budget

- gitlab.com: 2,000 authenticated API requests/min per user. A poll
 cycle is ~4–6 requests + bounded reviewer-state fetches → even 30s
 polling uses **<1%** of budget. Per-endpoint caps exist (e.g.
 `GET /users/:id` 300/10min — avoid; we don't need it) but none on
 `/todos` or `/merge_requests` lists. Heavy use of the `search` param
 on the global MR list can 429 — we don't use it.
- 429 handling: honor `Retry-After`; also read `RateLimit-Remaining` /
 `RateLimit-Reset` headers for the sync log's `rate_remaining`.
- Self-hosted: all limits are admin-configurable — treat 429 +
 `Retry-After` generically, never hardcode gitlab.com numbers.

## Poll cycle sketch (both providers, per the tiered poll)

| Tier | GitHub | GitLab | Default cadence |
|----------------------|--------------------------------------------------------------------------------|-------------------------------------------------------------------------------|----------------------------------------|
| Fast (change signal) | notifications w/ `If-Modified-Since` (classic PAT) **or** GraphQL search poll | `/todos?state=pending` + `updated_after` watermark; + batched GraphQL refresh of volatile booleans for all known-open items (v0.1.3) | 60s (obey `X-Poll-Interval` on GitHub) |
| Detail fetch | included in GraphQL responses | reviewers endpoint for changed MRs; single-MR GET for stuck-transient gate | on change only |
| Reconciliation sweep | full bucket query set, no watermark | full `scope=` list set, no `updated_after`; populates open-set snapshot cache | 3 min |

Cadences are proposed defaults (fixed in v0.1); the reconciliation
sweep also self-heals anything the fast tier missed (deleted todos,
watermark gaps, GitHub search lag).

## Capability declarations

| Capability | GitHub | GitLab |
|---------------------------------------|----------------------------------------|---------------------------------------|
| Notifications fast-signal | classic PAT only | always (todos) |
| Merge gate (Blocked / Ready to Merge) | always (GraphQL) | ≥ 15.6 |
| Changes Requested | always (`reviewDecision`) | ≥ 16.11; merge-gate variant Premium+ |
| Conditional requests (free 304s) | notifications + REST | none |
| Read-only least-privilege token | fine-grained PAT (minus notifications) | `read_api` (full) |

## Verify at implementation

1. GitHub `mergeStateStatus` actor-agnosticism (community-sourced).
2. REST `mergeable_state` value set — undocumented; irrelevant if the
 plugin stays on GraphQL, revisit only if REST fallback is added.
3. GitLab `/todos` accepting `action=review_requested` (source says yes,
 docs omit it).
4. `GET /user` behavior for GitLab project/group access tokens
 (bot-user response is inferred).
5. GitLab "no duplicate pending todo" dedup behavior.
6. Minimum supported GitLab version and the degradation matrix for
 older instances.
7. GitHub fine-grained PAT: confirm org-owned repos honor the token when
 the org restricts fine-grained PAT access (org opt-in policies).

## Cross-references

- Discovery on GitHub: the notifications feed is conditional on a classic PAT;
  search polling is the baseline (`ADR/ADR-0004_User_Centric_Synchronization.md`).
- Conditional ETag requests apply to GitHub only; GitLab uses `updated_after`
  watermarks (`docs/Synchronization_Engine.md`).
- The capability table above is the source for each provider's declared
  `sdk.Capabilities` (`docs/Provider_SDK.md`).
