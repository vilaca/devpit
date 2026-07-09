# High-Level Architecture

The orientation map — start here, then follow the links to the ADR (the *why*),
the spec (the *design detail*), or the code (the *truth*).

## Components

| Component | What it does | Code | Spec | Decision | Status |
|---|---|---|---|---|---|
| **Provider plugins** | Talk to GitHub/GitLab; discover the user's work; normalize it into events | `provider/github`, `provider/gitlab`, `sdk` | `docs/Provider_SDK.md`, `docs/Provider_API_Analysis.md` | ADR-0003 | **Built** (GitHub, GitLab) |
| **Sync Engine** | One goroutine per connection; tiered polling; diffs snapshots into events; writes the log | `internal/engine` | `docs/Synchronization_Engine.md` | ADR-0004, ADR-0005 | **Built** |
| **Storage** | SQLite (WAL), split read/write pools, single-instance lock; the event log | `internal/storage` | `docs/Event_Taxonomy_and_Storage.md` | ADR-0007 | **Built** |
| **Attention Engine** | Folds the event log into the ranked, tagged attention list at read time | `internal/attention` | `docs/Attention_Engine.md` | ADR-0005, ADR-0016 | **Built** |
| **Config** | Loads the static, named connection list; 0600 warning | `internal/config` | — | ADR-0015, ADR-0019 | **Built** |
| **REST API + SSE** | Serves the attention list, connections, sync log; pushes coarse change events | `internal/api` | `docs/REST_API.md` | ADR-0008 | **Built** |
| **Web UI** | Svelte SPA; live dashboard; keyboard-first | `frontend/` | — | ADR-0010 | **Planned** |

## How it hangs together

The **sync engine** is the sole writer: per connection it polls the provider,
diffs each snapshot against stored state, and appends events to **storage**.
The **attention engine** and **REST API** are readers: on request they fold the
event log into the ranked list. Live updates flow one way — the engine notifies
the API, which pushes a coarse SSE event, and the client re-fetches over REST.
Reads never block the writer and vice versa (split pools, WAL — ADR-0007).

## Deployment

Single binary (built) or Docker (planned) — `ADR/ADR-0011_Deployment_Model.md`.
Single-user, localhost, no auth (`ADR/ADR-0001_Local_First_Web_Application.md`).

## Timeline

`docs/Roadmap.md` is the single source of truth for what lands when.
