# DevPit

**What requires my attention right now?**

DevPit is a self-hosted attention center for software engineers. Instead of
triaging notifications across GitHub, GitLab, and other forges, you get one
ranked list of actionable work — scoped to you, not to repositories.

The shift: repositories become context. You are the center of the workflow.

## Status

Early development — not yet usable. Core storage and provider SDK done;
engine and UI in progress.

## How it works

- **Polling-first, no webhooks.** Uses your own token; no server-side setup,
  no callback URLs required.
- **Event log + fold-on-read.** Each poll diff appends events to a durable
  SQLite log. Attention states and item facts are computed at read time
  (pure event sourcing — no separate materialized state table).
- **Attention states as tags.** Each work item carries one or more state
  tags; items are ranked by state precedence then recency.
- **SSE live updates.** The local web UI subscribes to a Server-Sent Events
  stream and re-fetches on `attention.changed` — no page refresh needed.
- **Single binary, single SQLite file.** Runs on your machine; WAL mode
  ensures reads never block while the sync writer appends.

## Attention states

Six states in v0.1, shown as tags on each item:

| State | Meaning |
|---|---|
| **Ready to Merge** | Your PR; provider merge gate reports mergeable |
| **Needs Review** | A review was requested from you |
| **Changes Requested** | Your PR; a reviewer asked for changes |
| **Blocked** | Your PR; merge gate reports not mergeable |
| **Mentioned** | You were mentioned |
| **Waiting on Author** | You reviewed; ball is back with the author |

Precedence (highest first): Ready to Merge → Needs Review → Changes
Requested → Blocked → Mentioned → Waiting on Author.

## Configuration

A single YAML file with a list of named connections (type, base URL, token).
See [`docs/Configuration.md`](docs/Configuration.md).

## Contributing

See [`docs/Contributing.md`](docs/Contributing.md).

## License

[MIT](LICENSE)
