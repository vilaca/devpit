# DevPit

**What requires my attention right now?**

DevPit is a self-hosted attention center for software engineers. Instead of
triaging notifications across GitHub, GitLab, and other forges, you get one
ranked list of actionable work — scoped to you, not to repositories.

The shift: repositories become context. You are the center of the workflow.

## Status

v0.1.1 complete. Storage, provider SDK (GitHub + GitLab), sync engine, attention
fold, REST + SSE API, and the Svelte SPA are all built and embedded into a
single binary. v0.1.1 adds diagnostic markers (`failing_checks`, `merge_conflict`,
`needs_rebase`), age bands (`stale` / `old`), onset hover text, and pin age.

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
| **Needs Review** | A review was requested from you |
| **Changes Requested** | Your PR; a reviewer asked for changes |
| **Blocked** | Your PR; merge gate reports not mergeable |
| **Ready to Merge** | Your PR; provider merge gate reports mergeable |
| **Mentioned** | You were mentioned |
| **Waiting on Author** | You reviewed; ball is back with the author |

Precedence (highest first): Needs Review → Changes Requested → Blocked →
Ready to Merge → Mentioned → Waiting on Author.

## Configuration

A single YAML file with a list of named connections (type, base URL, token).
See [`ADR/ADR-0015_Multi_Account_Connections.md`](ADR/ADR-0015_Multi_Account_Connections.md) and [`internal/config/config.go`](internal/config/config.go).

## Running

Build the SPA once (it embeds into the binary via `go:embed`), then build and
run the binary:

```sh
npm --prefix frontend ci && npm --prefix frontend run build
go build -o devpit ./cmd/devpit
./devpit --config ~/.config/devpit/config.yaml
```

The dashboard and the API are served together at `http://localhost:7474`. The
Go build works without the frontend step (a committed placeholder page is
embedded), but you get the real UI only after `npm run build`. For UI
development, `npm --prefix frontend run dev` runs Vite on `:5173` and proxies
the API through to a running `devpit`.

Prefer this over `go run ./cmd/devpit`. DevPit depends on `modernc.org/sqlite`,
a pure-Go SQLite that is large and slow to compile; a cold build takes ~15–20 s
and pegs all cores (enough to spin up laptop fans). `go run` recompiles whenever
the build cache is cold — e.g. after a dependency upgrade — so a start/stop loop
pays that cost repeatedly. The running binary itself is idle (~0 % CPU between
poll cycles).

## Contributing

See [`docs/Contributing.md`](docs/Contributing.md).

## License

[MIT](LICENSE)
