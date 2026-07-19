# REST API

Deliberately minimal surface (see docs/Design_Decisions.md §5, §9, §11,
§16). Buckets have no dedicated endpoints — they are client-side filters
over the single attention list.

- `GET /attention` — the single ranked list; states as tags.
  `?state=` is optional sugar, the UI filters client-side.
- `GET /events` — SSE stream (see docs/SSE_Events.md).
- `GET /connections` — list provider connections (§1b).
- `POST /connections` — add a connection (base URL + token).
- `DELETE /connections/{id}` — remove a connection.
- `GET /sync-log` — user-facing sync/poll log (§16);
  `?connection=` filter for banner deep-links.
- `PUT /items/{id}/flag` — set the local "Handle next" flag (§5).
- `DELETE /items/{id}/flag` — clear it.

No manual sync trigger endpoint: polling is automatic (§9).

## Common conventions

- All responses are `application/json`.
- Timestamps are RFC 3339 UTC strings.
- `PUT` and `DELETE` on flags return `204 No Content`.
- `DELETE /connections/{id}` returns `204 No Content`.
- Errors use a uniform shape (see §Errors below).
- Item `id` is an opaque, stable, URL-safe string derived server-side
  from `(connection_id, object_type, native_id)`. Clients treat it as
  a black box and use it only for the `/flag` endpoints.

## `GET /attention`

Returns the full ranked list. Pinned items (`flagged: true`) come first
in flag order; auto-ranked items follow, ordered by state precedence then
newest-signal-first (§6). The `?state=` param filters to a single state
value — it is sugar; the client may filter locally instead.

```json
{
  "items": [
    {
      "id": "a3f8c2d1b4e7f9a0",
      "connection_id": "gh-personal",
      "connection_label": "Personal",
      "connection_type": "github",
      "object_type": "merge_request",
      "native_id": "acme/api#412",
      "title": "Fix flaky sync test",
      "url": "https://github.com/acme/api/pull/412",
      "repo": "acme/api",
      "author": "jdoe",
      "draft": false,
      "states": ["needs_review"],
      "flagged": true,
      "stale": false,
      "updated_at": "2026-07-08T09:14:02Z",
      "signal_counts": {
        "mentioned": 3
      },
      "failing_checks": false
    }
  ]
}
```

Field notes:
- `states` — array of state strings, ordered by precedence (§6):
  `needs_review`, `changes_requested`, `blocked`, `ready_to_merge`,
  `mentioned`, `waiting_on_author`. A WorkItem may carry multiple.
- `flagged` — `true` = pinned in the "Handle next" zone (§5).
- `stale` — `true` = newest event older than the staleness threshold (§6).
- `updated_at` — newest `coalesce(occurred_at, observed_at)` across the
  item's signals, else latest snapshot's `provider_updated_at` (§3 fold).
- `signal_counts` — present only for signal types with count > 1; drives
  the "Mentioned ×3" tag (§6). Types with count = 1 are omitted.
- `failing_checks` — `true` = any red check, gating or not (§4).
  Never drives a state; renders a failure-notification marker.

## `GET /connections`

```json
{
  "connections": [
    {
      "id": "gh-personal",
      "type": "github",
      "base_url": "https://github.com",
      "label": "Personal",
      "identity": "jdoe",
      "health": {
        "status": "ok",
        "last_synced_at": "2026-07-08T09:14:00Z",
        "failure_count": 0,
        "failure_window_minutes": 60
      }
    }
  ]
}
```

- `identity` — resolved handle (§7); `null` while resolution is pending
  or failed.
- `health.status` — `ok` | `degraded` | `failing`.
  `degraded` = some failures in the window; `failing` = all recent cycles
  failed. Drives the §12 health dot colour.
- `health.failure_count` — count of failed cycle rows in the last
  `failure_window_minutes` from `sync_log` (§12). The glanceable teaser.
- Token is never returned.

## `POST /connections`

Request:

```json
{
  "type": "github",
  "base_url": "https://github.com",
  "token": "ghp_...",
  "label": "Personal"
}
```

Response `201 Created` — returns the new connection object (same shape
as one element of `GET /connections`), so the client can display it
immediately without a follow-up GET. `identity` will be `null` until the
first `ResolveIdentity` call completes.

## `GET /sync-log`

`?connection={id}` filters to one connection (used by the §12 banner
deep-link). No pagination — the table is user-bounded (§2/§6 cleanup).

```json
{
  "entries": [
    {
      "id": 42,
      "connection_id": "gh-personal",
      "connection_label": "Personal",
      "ts": "2026-07-08T09:14:00Z",
      "operation": "fast_poll",
      "outcome": "ok",
      "items_changed": 3,
      "rate_remaining": 4850,
      "retries": 0,
      "next_retry": null,
      "error": null,
      "calls": null
    },
    {
      "id": 41,
      "connection_id": "gl-acme",
      "connection_label": "Acme",
      "ts": "2026-07-08T09:13:00Z",
      "operation": "fast_poll",
      "outcome": "rate_limited",
      "items_changed": null,
      "rate_remaining": 0,
      "retries": 1,
      "next_retry": "2026-07-08T09:14:30Z",
      "error": "Rate limited — retry in 90s",
      "calls": [
        {
          "id": 100,
          "ts": "2026-07-08T09:13:00Z",
          "operation": "GET /todos",
          "outcome": "rate_limited",
          "http_status": 429,
          "retries": 1,
          "next_retry": "2026-07-08T09:14:30Z",
          "error": "Rate limited — retry in 90s"
        }
      ]
    }
  ]
}
```

- `operation` — cycle rows: `fast_poll` | `reconcile`;
  call-detail rows: endpoint label (e.g. `"GET /todos"`).
- `outcome` — `ok` | `auth` | `rate_limited` | `network` | `parse`.
- `calls` — `null` on success; array of per-call detail objects on
  failure/retry (§16 progressive disclosure, level 4). Always present
  in the response; the client decides whether to render it expanded.
- `connection_label` is denormalized here so the log is self-contained
  even if the connection is later removed.

## Errors

```json
{
  "error": "not_found",
  "message": "Item not found"
}
```

Error codes: `not_found`, `bad_request`, `conflict` (duplicate
connection), `internal`. HTTP status maps 1:1 (404, 400, 409, 500).

## Decisions

- **`GET /attention` is a flat ordered list.** Flagged items sort first
  (in `flagged_at` order); auto-ranked items follow. The client draws
  the "Handle next" zone separator between the last `flagged: true` and
  first `flagged: false` item.
- **Item `id` is an opaque server-generated hash** of
  `(connection_id, object_type, native_id)` — short, URL-safe, stable.
  Clients treat it as a black box and use it only for the `/flag`
  endpoints.
