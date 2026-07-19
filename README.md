# DevPit

**What requires my attention right now?**

DevPit is a self-hosted attention center for software engineers. It aggregates
actionable work from GitHub, GitLab, and other code forges into a single
ranked list — so you spend less time triaging notifications and more time
shipping.

## Status

**Design phase.** No code yet. See [`docs/`](docs/) for architecture decisions
and [`WHY_DEVPIT.md`](WHY_DEVPIT.md) for motivation.

## How it works

- Polls your configured providers using your own tokens (no webhooks, no
  server-side setup).
- Synthesizes events by diffing each snapshot against stored state.
- Folds the event log into attention states: **Needs Review**, **Blocked**,
  **Ready to Merge**, **Changes Requested**, **Mentioned**, **Waiting on Author**.
- Presents a single ranked list in a local web UI, updated via SSE.

Single binary, single SQLite file, runs on your machine.

## Architecture

| Path | Purpose |
|------|---------|
| `cmd/devpit/` | Main entry point |
| `sdk/` | Provider interface and shared types |
| `internal/engine/` | Sync / polling engine |
| `internal/attention/` | Attention fold logic |
| `internal/storage/` | SQLite layer |
| `internal/api/` | REST handlers + SSE |
| `internal/config/` | Config file parsing |
| `provider/github/` | GitHub provider |
| `provider/gitlab/` | GitLab provider |
| `frontend/` | Svelte SPA (embedded via `go:embed`) |

Key documents:

- [`docs/Design_Decisions.md`](docs/Design_Decisions.md) — authoritative decision log
- [`docs/Provider_SDK.md`](docs/Provider_SDK.md) — provider interface
- [`docs/REST_API.md`](docs/REST_API.md) — API shapes
- [`docs/Event_Taxonomy_and_Storage.md`](docs/Event_Taxonomy_and_Storage.md) — event schema
- [`docs/Provider_API_Analysis.md`](docs/Provider_API_Analysis.md) — GitHub/GitLab API facts

## Configuration

A single YAML file with a list of named connections (type, base URL, token).
See [`docs/Configuration.md`](docs/Configuration.md).

## Provider tests

Provider tests use recorded HTTP fixtures (`testdata/fixtures/`) — no live
tokens required in CI. To record new fixtures locally, set the provider token
via environment variable and run with `-record` flag (see provider test files).

## License

[MIT](LICENSE)
