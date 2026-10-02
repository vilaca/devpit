# Provider API Analysis — GitHub & GitLab (v0.1)

External API research behind the two v0.1 providers
(`ADR/ADR-0003_Provider_Plugin_Model.md`). Maps discovery and the attention
buckets to exact GitHub/GitLab API calls, defines the merge-gate field mapping (consumed by the
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
 "notifications as change-signal" tier is **optional** on GitHub.
 Without it the reconcile search still discovers authored,
 review-requested, and assigned work, but there are no mentions or fast
 signals (Change signal below; `docs/Token_Setup.md`).
2. **GitLab's public REST API has no ETag/304 support.** Conditional
 requests are a GitHub-only optimization. GitLab change detection
 uses `updated_after` watermark polling plus the todos feed.
3. **GitHub REST `mergeable_state` is officially undocumented** (OpenAPI
 type: free-form string). GraphQL `mergeStateStatus` is GA, enum-typed,
 and fetchable in bulk, which avoids an N+1 REST call per PR for
 merge-gate state. DevPit reads REST `mergeable_state` from the single-PR
 GET only; its REST search rows and GraphQL join carry no merge-gate
 state (Discovery and enrichment below).
4. **GitLab returns `detailed_merge_status` in list responses** — no N+1
 for the merge gate; REST is sufficient for the GitLab plugin.
5. **GitLab "request changes" only blocks the merge gate on
 Premium/Ultimate.** On Free it exists but is informational, so the
 Changes Requested bucket needs the per-MR reviewers endpoint there
 (small bounded N+1 over authored open MRs).

## GitHub

### Identity

`GET /user` (REST; GraphQL `viewer { login databaseId }` is equivalent).
Works for both PAT types. DevPit resolves the login once and uses it in its
search qualifiers (`@me` would also work).

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
against the rate limit; GitHub sends an `X-Poll-Interval` header (default
60s), which DevPit does not read — the fast cadence is the fixed
`defaultFastEvery`. Relevant `reason` values: `review_requested`, `mention`,
`team_mention`, `assign`, `author`, `state_change`. (`ci_activity`, an
Actions run you triggered finishing, is about a check suite rather than a
pull request, so it gives the PR-scoped fast poll nothing to attach a signal
to — GitHub emits no `signal.ci_failed`.)

Each PR notification on the first unread page — open, merged, or closed, and
including watched-only repos — triggers a single-PR
`GET /repos/{owner}/{repo}/pulls/{number}`, the only source of
`mergeable_state` (Merge-gate mapping below). An open PR with no role and no
signal is then dropped (`ADR/ADR-0004_User_Centric_Synchronization.md`); the
rest go through the GraphQL join. Only the `mention` reason yields
`signal.mentioned`, and non-PR notifications (issues) are skipped.

Without a classic PAT there is no working fast tier. A GraphQL search-poll
fallback and token-driven capability degradation were designed but are **not
implemented**: `Capabilities` declares `FastSignal` unconditionally, so
`FastPoll` still calls `/notifications` every cycle and fails — a 403 without a
rate signal is reported as an auth failure — and GitHub mention signals arrive
only from it. A fine-grained PAT therefore gets its discovery from the
reconcile sweep alone, and no mentions. Until the fallback exists, the "recommended
default" framing in Token guidance is aspirational; the practical trade-off is
in `docs/Token_Setup.md`.

### Discovery and enrichment

Reconcile runs one REST search per involvement scope —
`GET /search/issues?q=is:pr is:open <qualifier>:<login>` for `review-requested`,
`assignee`, and `author`, plus `user:` for sole-approver candidates (Sole-approver
discovery below) — following `Link: rel="next"` so a result set is not
truncated to page one (the Search API itself caps a query at 1,000 results;
`provider/github/reconcile.go`). Roles accumulate per PR across scopes into one
`item.observed`, and every member of the review-requested set emits a
`signal.review_requested` keyed on the PR's `updated_at`, so a PR updated while
you are still requested fires it again (dedupe keys:
`docs/Event_Taxonomy_and_Storage.md`). Search rows carry no `mergeable_state`,
so a sweep snapshot reports the gate as `unknown`; only the fast tier's
single-PR GET supplies a real gate.

Known gaps in the sweep's scopes:

- **Mentions** arrive only through the notifications feed; no scope searches
  them.
- **Reviewed PRs.** GitHub stops matching `review-requested:` once you submit a
  review, and no scope searches `reviewed-by:`, so a PR you reviewed (and
  don't author or hold another role on) leaves the sweep.
- **Team review requests.** `review-requested:` also matches requests to a team
  you belong to (`user-review-requested:` would match direct requests only),
  while the fast tier's single-PR GET derives the reviewer role from user
  reviewers alone (`provider/github/normalize.go`), so the two tiers can
  disagree on a team-requested PR.

Every observed PR, from either tier, is then enriched by a GraphQL join:
aliased `repository(owner:, name:) { isArchived pullRequest(number:) { … } }`
lookups in batches (`batchSize` in `runGHBatches`; query `prQueryFmt`,
`provider/github/graphql.go`). It supplies `reviewDecision`, the head/base
branch names, auto-merge state, and `latestReviews` — the latest non-dismissed
review per reviewer, which yields the approvals count, your own review state,
and the rank-only `signal.approved` / `signal.changes_requested` verdict
signals, with `submittedAt` as their real `OccurredAt`
(`docs/Event_Taxonomy_and_Storage.md`, `ADR/ADR-0016`). A PR on an archived
repo is dropped from the sweep (`ADR/ADR-0024_Reconcile_Item_Reaping.md`).

Search notes: since 2025-09-04 all issue searches use "advanced search"
semantics — multiple `repo:`/`org:`/`user:` qualifiers AND together
(previously OR). No `advanced_search` param needed anymore.

**Snapshot cache.** Like GitLab, the provider keeps an in-memory
`openSnapshots` cache of each open PR's last full post-join payload
(`provider/github/github.go`): when a GraphQL batch degrades, the join carries
enrichment forward from it, a draft getting only part of it (the split is in
`carryForwardEnrichment`, `provider/github/graphql.go`). Unlike GitLab's it is
not a refresh baseline — GitHub has no open-set refresh. An entry leaves when
its PR is observed non-open or its repo archived; absence-based eviction happens
only on a complete Reconcile (`pruneClosedSnapshots`,
`provider/github/reconcile.go`), so FastPoll's partial slice never evicts a PR it
merely did not hear about.

### Merge-gate mapping (`mergeable_state`)

REST `mergeable_state`, from the single-PR GET, mapped by `mergeGate`
(`provider/github/normalize.go`); GraphQL's `mergeStateStatus` is believed to
be the same enum, upper-cased (Verify at implementation, item 2).

| Value | Meaning | DevPit gate (+ marker) |
|-------------|---------------------------------------|------------------------------------------------------|
| `clean` | Mergeable, checks passing | `ready` |
| `has_hooks` | Mergeable, passing + pre-receive hooks | `ready` |
| `unstable` | Mergeable with non-passing status | `ready` + `failing_checks` — **not** Blocked |
| `blocked` | Merge blocked (protection rules) | `blocked` |
| `dirty` | Merge commit can't be created (conflict) | `blocked` + `merge_conflict` |
| `behind` | Head out of date (strict checks) | `blocked` + `needs_rebase` |
| `unknown` | Being computed | `unknown` |
| `draft` | deprecated | `unknown`; the PR's `draft` flag says draft |
| anything else, or empty | undocumented / absent | `unknown` |

A `blocked` gate also gets `needs_approval` from the GraphQL join when
`reviewDecision` is `REVIEW_REQUIRED` (non-drafts only).

Caveat **[verify]**: the merge state is actor-agnostic — it reports
`blocked` even for users whose bypass rights would let them merge
(community-sourced, not official docs).

### Rate budget

- REST core: 5,000 req/hour; authorized conditional 304s are free, so an
  unchanged `/notifications` poll costs nothing. A changed (200) poll re-fetches
  every PR on the first unread page (up to 50), not just new ones — the
  request sets no `since`. Collaborator probes for sole-approver candidates
  also count here, cached per repo (`approverTTL`).
- REST search: 30 requests/min. A reconcile issues one paginated search per
  scope (four) every `defaultReconEvery` (`internal/engine/engine.go`) —
  about 4% of the search limit at the 3-minute cadence.
- GraphQL: 5,000 points/hour, ≤2,000 points/min; the join is one query (about a
  point) per batch of PRs it enriches.
- Secondary limits: ≤100 concurrent; on 429 / `retry-after`, honor the
  header, else wait ≥60s with exponential backoff (basic backoff).

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

**Open-set refresh.** Todos miss pipeline transitions, so each fast cycle also
re-queries, in batched GraphQL alias queries, every known-open MR no todo
covered this cycle (ADR-0004). The baseline is the provider's in-memory
`openSnapshots` cache of full post-join payloads, written by both Reconcile and
the fast tier's todo path, so a todo-fresh snapshot is never reverted to an
older sweep's. The join **overrides** every GraphQL-derived field and keeps the
REST-derived ones — never ORing with the cached value, which would pin a stale
`true` forever. `merge_conflict` is GraphQL-derived (conflict note below), so the
refresh clears it too. No-change cycles dedupe away (`observedDedupeKey`); a
GraphQL failure is logged, the batch skipped, and the cycle still succeeds.

### Bucket → call mapping

Global list endpoint, `state=opened`, response includes
`detailed_merge_status`, `draft`, `updated_at`:

| Bucket | Call | Post-filter |
|---------------------------|----------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------|
| Review Requested | `GET /merge_requests?scope=reviews_for_me&state=opened` | my reviewer state ∈ {unreviewed, review_started} via reviewers endpoint (below) |
| Changes Requested | `GET /merge_requests?scope=created_by_me&state=opened` | `detailed_merge_status == requested_changes` (Premium) **or** any reviewer state `requested_changes` via reviewers endpoint (Free) |
| Blocked / Ready to Merge | same authored query | merge-gate mapping below, non-draft |
| Mentioned | `GET /todos?state=pending&action=mentioned` + `directly_addressed` | — |
| Review Submitted | `scope=reviews_for_me` result | my reviewer state ∈ {reviewed, requested_changes} |

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
in `ADR/ADR-0016` and `docs/Event_Taxonomy_and_Storage.md`. The join also selects
the enclosing `project.archived`; an archived project's MRs are dropped from the
sweep so the engine reaps them (`ADR/ADR-0024`), mirroring GitHub's `isArchived`
on the `repository` node.

### Merge-gate mapping (`detailed_merge_status`)

| Class | Values | DevPit state |
|--------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------|
| Ready | `mergeable` | Ready to Merge |
| Transient | `unchecked`, `checking`, `preparing`, `approvals_syncing`, `ci_still_running` | keep previous state; re-poll (see staleness note) |
| Draft | `draft_status` | never Blocked/RTM (drafts) |
| Gate-blocked | `conflict`, `need_rebase`, `not_approved`, `requested_changes`, `ci_must_pass`, `discussions_not_resolved`, `merge_request_blocked`, `status_checks_must_pass`, `commits_status`, `not_open`, plus tier-specific (`jira_association_missing`, `security_policy_*`, `locked_paths`, `locked_lfs_files`, `title_regex`, `merge_time`) | Blocked |

Conflict note: `has_conflicts` (REST) and `conflicts` (GraphQL) are **not** a
conflict test — both are defined as `merge_status != can_be_merged`, so they read
`true` while mergeability is merely uncomputed (`checking`, `unchecked`) and on
every branch a fast-forward-only project cannot merge until it is rebased. The
`conflict` value of `detailed_merge_status` is what GitLab shows the user as the
operative blocker, so DevPit's `merge_conflict` marker reads that instead — and
still drops it when `shouldBeRebased` says the offered action is a rebase, which
in a fast-forward/semi-linear project GitLab's conflict verdict cannot be
separated from. Consequence: because `detailed_merge_status` names only the
*first* failing check, an MR blocked on approvals or discussions reports those
and the conflict stays hidden until they clear.

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
| Fast (change signal) | notifications w/ `If-Modified-Since` (classic PAT only) | `/todos?state=pending` + `updated_after` watermark; + batched GraphQL refresh of volatile booleans for all known-open items | `defaultFastEvery` |
| Detail fetch | single-PR GET per notified PR (merge gate); GraphQL join for reviews, branches, auto-merge | reviewers endpoint for changed MRs; single-MR GET for stuck-transient gate | on change only |
| Reconciliation sweep | four scoped REST searches, no watermark; GraphQL join | full `scope=` list set, no `updated_after`; populates open-set snapshot cache | `defaultReconEvery` |

Cadences are engine constants (`internal/engine/engine.go`, ADR-0004); the reconciliation
sweep also self-heals anything the fast tier missed (deleted todos,
watermark gaps, GitHub search lag).

## Sole-approver discovery (both providers)

Reconcile's sole-approver scope (ADR-0004) finds open PRs/MRs on repos where the
user is the only merge-capable account, skipping drafts and self-authored items:

- **GitHub** — candidates from `is:pr is:open user:<handle>` (repos the user
  owns); sole iff `GET /repos/{owner}/{repo}/collaborators?affiliation=all` lists
  exactly one account with `push`, `maintain`, or `admin`, and it is the user.
  `all`, not `direct`, so team/org merge rights count.
- **GitLab** — candidates are the open MRs of
  `GET /projects?membership=true&min_access_level=40` (Maintainer+); sole iff
  `GET /projects/:path/members/all?min_access_level=40` lists exactly one member,
  and it is the user.

Each provider caches the verdict per repo in memory for 15 min (`approverTTL`).
On GitHub, a join that sees approvals beyond the user's own marks the repo
not-sole at once, without waiting for the next probe. Providers cannot reach
`internal/storage`, so this path never writes the `repo_approvers` table — only
an explicit `UpsertRepoApprover` call does, and none exists outside tests.

## Capability declarations

| Capability | GitHub | GitLab |
|---------------------------------------|----------------------------------------|---------------------------------------|
| Notifications fast-signal | classic PAT only | always (todos) |
| Merge gate (Blocked / Ready to Merge) | always (GraphQL) | ≥ 15.6 |
| Changes Requested | always (`reviewDecision`) | ≥ 16.11; merge-gate variant Premium+ |
| Conditional requests (free 304s) | notifications + REST | none |
| Read-only least-privilege token | fine-grained PAT (minus notifications) | `read_api` (full) |

## Verify at implementation

1. GitHub merge-state actor-agnosticism (community-sourced).
2. REST `mergeable_state` value set — undocumented, and it is what DevPit
 reads (`mergeGate`, `provider/github/normalize.go`); confirm it tracks
 GraphQL's `mergeStateStatus` enum.
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
- The capability table above is provider-API research — what each forge *can*
  do. Only the "Notifications fast-signal" row maps to a declared
  `sdk.Capabilities` field today (`FastSignal`); the other rows are not gated
  capabilities, since capability-gated bucket production is deferred
  (`ADR/ADR-0003_Provider_Plugin_Model.md`, `docs/Provider_SDK.md`).
