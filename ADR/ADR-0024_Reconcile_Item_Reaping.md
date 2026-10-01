# Reconcile Item Reaping

## Scope

Implemented (v0.1.6) — on a complete reconcile the engine reaps merged, closed,
and un-roled items, and mention-only leftovers once `Provider.ResolveOpen`
confirms them gone (`internal/engine/cycle.go`). See `docs/Roadmap.md`.

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

**Reconcile is a full authoritative sweep, and the engine reaps.**

- **Full sweep, no incremental cursor.** Every reconcile enumerates *all* open
  roled items — no incremental `updated_after` / `updated:>` filter, no per-scope
  cursor. The absolute cost is the startup sweep repeated each cycle (cadence: `defaultReconEvery` in `internal/engine/engine.go`,
  rationale in ADR-0004); enrichment batching is
  unchanged (`provider/gitlab/graphql.go`, `provider/github/graphql.go`) and the
  `item.observed` dedupe-hash makes an unchanged re-sweep a write/notify no-op
  (`docs/Event_Taxonomy_and_Storage.md`).
- **`Complete` on `PollResult`.** A boolean (mirroring `Degraded`,
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
  *failure* (`provider/github/graphql.go`, `provider/gitlab/graphql.go`) — that
  never-drop-on-failure behaviour is a stated invariant of the join. The one
  sanctioned drop is an **archived-repo item** (see the archived-repo bullet in
  Consequences): the join drops it deliberately so it leaves the swept set and is
  reaped, which is safe because it is keyed on a definitive forge fact
  (`isArchived` / `project.archived`), never a transient failure. The diff needs
  one store read, over the existing `engine → storage` edge
  (`.go-arch-lint.yml`).
- **Mention-only leftovers are confirmed first.** An item surfaced purely by a
  FastPoll mention carries no role, so a complete sweep missing it proves
  nothing. The engine asks `Provider.ResolveOpen` (`sdk/provider.go`) which of
  these leftovers are still open and reaps the rest; a still-open mention is
  kept, and a lookup error skips leftover reaping that cycle (fail closed)
  without blocking the roled path.
- **Per-episode removal, idempotent while gone.** The engine reaps only items
  whose latest stored event is an observed-open (an already-removed item is
  skipped, so a still-gone item is not re-removed every cycle), and keys each
  removal to the superseded observed event so a reopen→re-merge produces a
  fresh, higher-id removal. The mechanics live in
  `docs/Event_Taxonomy_and_Storage.md`.
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

- **Reconcile carries no cursor state** — no per-scope `updated_after` cursors,
  no cursor-advance guard. FastPoll's own watermark and each provider's
  in-memory `openSnapshots` cache are a separate concern.
- **One store read per complete reconcile** (the latest fact for every item on
  the connection, which the leftover and resurrection paths also use) and synthesized `item.removed` writes on the existing durable
  events-then-cursors path (`internal/engine/cycle.go`).
- **Reaping is duplicated in neither provider** — providers only compute the
  `Complete` flag and answer `ResolveOpen`; the diff lives once in the engine. This is deliberately *not*
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
- **Mention-only leftovers cost one provider call** per complete reconcile that
  has any. GitHub's FastPoll role-less drop (`provider/github/fastpoll.go`) also
  keeps *non-open* snapshots, since a merged snapshot makes the fold drop the
  item and can never render as a bare row.
- **A partial sweep never reaps** — any REST scope or sole-approver enumeration
  failure clears `Complete`, so a transient outage cannot mass-remove live
  items.
- **Archived-repo items are reaped.** A PR/MR on a repo the forge reports
  archived needs no attention (an archived repo is read-only), so both providers'
  GraphQL joins drop its `item.observed` — and any sibling signal sharing the
  native ID — keyed on GitHub's `isArchived` / GitLab's `project.archived`
  (`provider/github/graphql.go`, `provider/gitlab/graphql.go`). GitLab also evicts
  the item from its `openSnapshots` cache so FastPoll's open-set refresh cannot
  resurrect it. The dropped item is absent from the sweep, so the engine reaps a
  previously-open one exactly as it reaps a merge/close. This is the *only* drop
  the join's never-drop-on-failure invariant permits; because it is gated on the
  definitive archived fact and skipped whenever the enrichment batch degrades, a
  transient GraphQL failure never triggers it (no false reap). It also narrows
  discovery to involvement that can still act — the intent of
  `ADR/ADR-0004_User_Centric_Synchronization.md`.
- Related: `ADR/ADR-0005_Event_Based_Attention_Engine.md` (event store),
  `ADR/ADR-0016_Presentation_And_Ranking.md` (the fold shows every open involved
  item — the invariant this restores on the exit side),
  `ADR/ADR-0003_Provider_Plugin_Model.md` (why the diff is not a provider
  helper). The event semantics live in `docs/Event_Taxonomy_and_Storage.md`;
  this ADR does not restate the schema.
