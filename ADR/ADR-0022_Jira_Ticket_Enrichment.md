# Jira Ticket Enrichment

## Scope

Implemented (v0.1.5) — `sdk.ExtractTicketKeys` (both providers), the
`internal/jira` enricher (Client + Refresher, 15-minute sweep), the
`jira_tickets` cache table, and read-time decoration in `internal/api`. See
`docs/Roadmap.md`.

## Context

Most merge requests in ticket-driven teams reference a Jira issue
(`PROJ-123`) in the title, source branch, or description. The MR list is where
the user already looks; the ticket's workflow status (In Review, In Acceptance
Testing, …) is context they currently fetch by hand from Jira. The roadmap
lists Jira under "broaden beyond code forges".

Jira does not fit the provider contract (ADR-0003): a provider is an
identity-scoped source that *produces* work items via FastPoll/Reconcile and
declares forge capabilities (merge gate, review states). Jira produces no
items here — it *decorates* items that forge connections already found. Field
studies on the user's real MRs (27 open, 17 with keys) show the key appears in
the title or branch in every referencing MR, so extraction from snapshot
fields is sufficient; commit messages are deliberately out of scope (they
would cost one extra API call per MR for negligible recall).

## Decision

**Ticket keys are provider facts; ticket data is a separately synced,
persisted cache; presentation is a status prefix on the item title.**

- **Extraction at normalize time.** Each provider extracts ticket keys from
  the sources it already holds — title, then source branch, then description;
  the first source containing any key wins (`sdk.ExtractTicketKeys`). Keys are
  stored on the `item.observed` payload as `ticket_keys`. The pattern is the
  Jira default key shape (`[A-Z][A-Z0-9]+-\d+`, word-bounded, case-sensitive).
  All providers (GitLab, GitHub) call the shared helper.
- **A Jira enricher, not a Jira provider.** A single `internal/jira` component
  (one per process, config-gated) periodically collects the distinct
  `ticket_keys` across open items, fetches issue status/summary/assignee from
  Jira Cloud REST (`/rest/api/3/issue/{key}`), and upserts into a new
  `jira_tickets` table. It never emits events; the event log remains
  provider facts only (ADR-0006). Cadence mirrors the reconcile tier
  (15 min, engine constant per ADR-0004). Per-key failures record
  `fetch_error` and keep the last good data.
- **Persisted cache.** `jira_tickets` lives in SQLite (migration), so ticket
  context survives restarts and renders offline, consistent with local-first
  (ADR-0001). `fetched_at` drives refresh; rows for keys no longer referenced
  by any open item are pruned on the same sweep.
- **Read-time decoration.** The fold carries `ticket_keys` through to the
  WorkItem; the API layer joins against `jira_tickets` (reader pool) and adds
  an optional `jira {key, status, url}` object to the attention item. The
  frontend renders `[<status>] ` as a prefix on the title, linking to the
  ticket — a link out, per the read-only action model (ADR-0017).
- **Config.** An optional top-level `jira:` block in `config.yaml`
  (`base_url`, `email`, `api_token` — Jira Cloud basic auth). Absent block =
  feature off. Present block = all three fields required. The plaintext-token
  posture and file-permission warning follow ADR-0019 unchanged.

## Consequences

- The `sdk.ItemObservedPayload` gains `ticket_keys`; because the dedupe key
  hashes the payload, each open item re-emits one `item.observed` on first
  run after upgrade (harmless, self-deduping thereafter).
- Jira outages degrade gracefully: items render without a prefix (no cached
  row) or with the last cached status (stale row); sync of forge data is
  unaffected.
- Enrichment failures are logged to the process log, not `sync_log`;
  integrating the enricher into sync observability (ADR-0018) is deferred
  until the pattern proves itself.
- A second enricher (e.g. Linear) would generalize this shape; we deliberately
  do not build the abstraction now (one concrete case first).
