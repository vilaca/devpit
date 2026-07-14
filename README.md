# DevPit

**What requires my attention right now?**

DevPit is a self-hosted attention center for software engineers. Instead of
triaging notifications across GitHub, GitLab, and other forges, you get one
ranked list of actionable work — scoped to you, not to repositories.

The shift: repositories become context. You are the center of the workflow.

<!-- Hero screenshot goes here: capture from seeded demo data at the
     first-public-release gate (docs/Roadmap.md) — never from a real instance. -->

## What you get

- **One ranked list across forges.** Every open MR/PR you're involved in —
  authored, reviewing, assigned, mentioned — in a single list, freshest
  actionable work first.
- **Neutral signals, not an inferred workflow.** Rows carry the provider's own
  facts as chips — Changes Requested, Review Requested, Blocked, Ready to
  Merge, … — plus diagnostic badges (failing checks, merge conflict, unresolved
  discussions) and age tints. The current vocabulary lives in
  [`docs/Attention_Engine.md`](docs/Attention_Engine.md).
- **Knows when only you can merge.** Repos where you are the sole
  merge-capable account get an always-on attention axis: those MRs surface as
  blocked on you even without an explicit review request.
- **Sees the ticket behind the MR.** With a Jira connection configured, items
  link to their Jira issue and show its workflow status inline.
- **A "Handle next" zone.** Pin the items you've decided to act on; pins are
  local-only and exempt from auto-ranking.
- **Private and read-only.** Runs on your machine, with your tokens, and talks
  only to your forges. DevPit never acts on your behalf — every action is a
  deep link out to the provider
  ([`ADR/ADR-0017_Read_Only_Action_Model.md`](ADR/ADR-0017_Read_Only_Action_Model.md)).

## Status

Pre-release, v0.1.5 — GitHub + GitLab, single-user, localhost. Milestones and
timing: [`docs/Roadmap.md`](docs/Roadmap.md).

## Under the hood

- **Polling-first, no webhooks.** Uses your own token; no server-side setup,
  no callback URLs required.
- **Event log + fold-on-read.** Each poll diff appends events to a durable
  SQLite log; item facts and signals are computed at read time (pure event
  sourcing — no materialized state table).
- **SSE live updates.** The web UI subscribes to a Server-Sent Events stream
  and re-fetches on `attention.changed` — no page refresh.
- **Single binary, single SQLite file.** The SPA embeds via `go:embed`; WAL
  mode keeps reads unblocked while the sync writer appends.

## Quickstart

```sh
npm --prefix frontend ci   # once, after cloning
scripts/start.sh
```

`scripts/start.sh` builds the SPA (it embeds into the binary), builds the
binary, stops any running instance, and starts the new one. Or do the same by
hand:

```sh
npm --prefix frontend run build
go build -o devpit ./cmd/devpit
./devpit   # --config <path> to override the default below
```

Either way the dashboard and the API are served together at
`http://localhost:7474`.

Configuration is one YAML file (default `~/.config/devpit/config.yaml`;
`chmod 600` it — it holds plaintext tokens):

```yaml
db_path: /path/to/devpit.db
connections:
  - id: github-personal
    type: github
    token: ghp_…            # read scopes suffice
    label: Personal         # optional — shown on each row; defaults to the id
  - id: work-gitlab
    type: gitlab
    base_url: https://gitlab.example.com
    token: glpat-…          # read_api scope suffices
    label: Work             # optional — defaults to the id
jira:                       # optional — Jira status enrichment
  base_url: https://example.atlassian.net
  email: you@example.com
  api_token: …
```

Per connection only `id`, `type`, and `token` are required; the full schema and
validation rules live in [`internal/config/config.go`](internal/config/config.go).

## Design docs

`docs/` holds the specs; `ADR/` holds every decision with its rationale
([`ADR/ADR-0014_Documentation_As_Design_Record.md`](ADR/ADR-0014_Documentation_As_Design_Record.md)).
Start with [`docs/Why.md`](docs/Why.md) and
[`docs/High_Level_Architecture.md`](docs/High_Level_Architecture.md).

## Contributing

See [`docs/Contributing.md`](docs/Contributing.md).

## License

[MIT](LICENSE)
