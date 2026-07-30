# Reconcile Item Reaping

## Scope

Planned — fixes a live correctness bug (merged/closed items never leave the
list). Lands with the reconcile-reaping work; see `docs/Roadmap.md` for timing.

## Context

An item leaves the attention list only when its latest snapshot is non-open
(`merged`/`closed`) or an `item.removed` fact supersedes it — the fold drops
those and shows every other open item (`internal/attention/fold.go`,
`docs/Event_Taxonomy_and_Storage.md`). The event taxonomy specifies
`item.removed` as *"the reconciliation sweep no longer sees the item"*, but **no
provider ever emitted it** — the fact was consumed by the fold and never
produced. Combined with reconcile being hard-scoped to open items
(`provider/github/reconcile.go`, `provider/gitlab/reconcile.go`), a PR/MR that
merges or closes — or that the user is un-roled from — drops out of every sweep
with its last snapshot still `open`, so it becomes a permanent "ghost" row.
It renders under whatever its last open gate was, typically `checking` (gate
`unknown`), dated to when it entered that state (`internal/attention/states.go`).
FastPoll can correct this only when a fresh notification/todo forces a re-fetch,
which a self-merge usually does not produce.

Two properties make this the engine's job, not the provider's. First, the store
already holds each item's latest state per `native_id`
(`internal/storage/storage.go`) and the read layer already computes which items
are open per connection (`internal/attention/fold.go`) — the diff needs store
access the providers (the `sdk` leaf, `.go-arch-lint.yml`) do not have. Second,
startup already runs a full cursor-less reconcile
(`internal/engine/cycle.go` passes `nil` state), so an engine-side diff reaps
items that went terminal while the app was down "for free" on the next start.

## Decision

**Reconcile becomes a full authoritative sweep and the engine reaps.**

- **Full sweep, no incremental cursor.** Every reconcile enumerates *all* open
  roled items, dropping the incremental `updated_after` / `updated:>` filter and
  its per-scope cursor bookkeeping. The absolute cost is the startup sweep
  repeated each cycle (`docs/Roadmap.md` cadence); enrichment batching is
  unchanged (`provider/gitlab/graphql.go`, `provider/github/graphql.go`) and the
  `item.observed` dedupe-hash makes an unchanged re-sweep a write/notify no-op
  (`docs/Event_Taxonomy_and_Storage.md`).
- **`Complete` on `PollResult`.** A new boolean (mirroring `Degraded`,
  `sdk/provider.go`) is true only when every role-scope's REST identity
  enumeration succeeded — **including** sole-approver discovery, whose
  silent-degrade paths (`provider/gitlab/reconcile.go`,
  `provider/github/reconcile.go`) must clear it. It is **independent of
  `Degraded`**: the reap set is the REST identity set, so a GraphQL
  enrichment failure never justifies suppressing a removal.
- **Engine-side reap.** After a `Complete` reconcile the engine
  (`internal/engine/cycle.go`) diffs the **swept set** — the `native_id`s of the
  `item.observed` events in the reconcile's `PollResult` — against the store's
  currently-open items **that carry a role** for that connection, and appends an
  `item.removed` fact for each item present in the store but absent from the
  sweep. Reconcile's scopes cover every role, so a roled open item missing from
  a complete sweep is genuinely gone (merged, closed, or access/role lost).
  Deriving the swept set from the result's events is sound only because both
  providers' GraphQL joins return the original events unchanged on enrichment
  failure (`provider/github/graphql.go`, `provider/gitlab/graphql.go`) — that
  never-drop behaviour becomes a stated invariant of the join. The diff needs
  one new store read; the `engine → storage` edge already exists
  (`.go-arch-lint.yml`).
- **Mention-only items are exempt.** An item surfaced purely by a FastPoll
  mention carries no role, is outside reconcile's authority, and is never
  reaped by this path.
- **Per-episode removal, idempotent while gone.** The engine reaps only items
  whose latest stored event is an observed-open (an already-removed item is
  skipped, so a still-gone item is not re-removed every cycle), and keys each
  removal to the superseded observed event so a reopen→re-merge produces a
  fresh, higher-id removal. This supersedes the "a constant [key] — at most one
  live removal" rule; `docs/Event_Taxonomy_and_Storage.md` is reworded in the
  same change to own the mechanics (and to note the emitter and the un-role
  case).
- **Salted resurrection.** Reappearance after a removal cannot rely on the
  snapshot dedupe key alone: an item re-observed with an *identical* fact set
  hashes to its pre-removal key, `INSERT OR IGNORE` drops it, and the removal
  would stay the latest event forever. So when a swept item's latest stored
  event is `item.removed`, the engine salts that observed event's dedupe key
  with the removal's event id before writing, guaranteeing a fresh, higher-id
  snapshot that supersedes the removal. This is what makes a *false* reap
  (below) self-healing rather than permanent.

## Rationale

The fold's contract — "show every open item the user is involved in" — is only
trustworthy if items reliably *leave* when involvement ends; the missing
`item.removed` producer was the gap. Putting the diff in the engine keeps the
one place with both store access and the full cross-tier picture, avoids the
per-provider duplication ADR-0003 would otherwise force, and reuses the
already-accepted startup full-sweep to clean ghosts that accrued while the app
was off. Making every reconcile a full sweep trades a bounded, already-paid
enrichment cost for a simpler provider (no cursor state) and prompt reaping —
the smallest thing that makes the list self-correcting
(`docs/Engineering_Philosophy.md`). Gating reaping on REST-completeness rather
than `!Degraded` keeps ghosts getting cleaned on accounts that chronically hit
the GraphQL complexity ceiling, where `Degraded` is common
(`provider/gitlab/graphql.go`).

## Consequences

- **Reconcile is stateless on cursors.** The per-scope `updated_after` cursors
  and the not-degraded cursor-advance guard are deleted; `PollState` for
  reconcile shrinks. FastPoll's own watermark and GitLab's in-memory
  `openSnapshots` refresh cache are a separate concern and unchanged.
- **A new store read per complete reconcile** (open roled items for the
  connection) and synthesized `item.removed` writes on the existing durable
  events-then-cursors path (`internal/engine/cycle.go`).
- **Reaping is duplicated in neither provider** — providers only compute the
  `Complete` flag; the diff lives once in the engine. This is deliberately *not*
  a shared provider helper (ADR-0003 does not apply to the engine layer).
- **Un-roling removes the item from your list**, matching the taxonomy's
  "no longer sees the item" case; if a role returns, the salted-resurrection
  path re-inserts a superseding snapshot even when the fact set is unchanged.
- **A false reap is transient, not permanent.** GitHub's search API is
  eventually consistent and can transiently omit a live PR without an error;
  that miss reaps the item, and the next sweep resurrects it (salted), so the
  worst case is the item flickering out for one reconcile interval. Accepted —
  a two-strike miss rule remains a possible follow-up if flicker is observed in
  practice, and is deliberately not built now.
- **Mention-only merged items remain a known residual gap.** Role-less items
  are outside reconcile's authority and are never reaped; nothing else clears
  them once merged. On GitHub the same change fixes the cheap half: the
  FastPoll role-less drop (`provider/github/fastpoll.go`) keeps *non-open*
  snapshots, since a merged snapshot makes the fold drop the item and can never
  render as a bare row. GitLab mention-only ghosts (todo-driven, no
  post-merge todo) are recorded here as a forward-dependency for the sync
  hardening milestone (`docs/Roadmap.md`).
- **A partial sweep never reaps** — any REST scope or sole-approver enumeration
  failure clears `Complete`, so a transient outage cannot mass-remove live
  items.
- Related: `ADR/ADR-0005_Event_Based_Attention_Engine.md` (event store),
  `ADR/ADR-0016_Presentation_And_Ranking.md` (the fold shows every open involved
  item — the invariant this restores on the exit side),
  `ADR/ADR-0003_Provider_Plugin_Model.md` (why the diff is not a provider
  helper). The event semantics live in `docs/Event_Taxonomy_and_Storage.md`;
  this ADR does not restate the schema.
