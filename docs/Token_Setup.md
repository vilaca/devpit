# Token Setup

How to create the access tokens DevPit needs, with the **exact minimal scopes**
and a `config.yaml` snippet to paste for each provider. DevPit is read-only — it
never writes to your forges (`ADR/ADR-0017_Read_Only_Action_Model.md`) — and
stores tokens in plaintext in the config file, so keep it `chmod 600`
(`ADR/ADR-0019_Secret_Storage.md`). Scopes here are derived from
`docs/Provider_API_Analysis.md`; the full connection schema is in
`internal/config/config.go`.

A dead or under-scoped token is not a crash: DevPit surfaces it as a red
per-connection health dot (`ADR/ADR-0018_Sync_Observability.md`).

## GitHub

GitHub is the one provider where the token *kind* changes what you get, because
its Notifications API — DevPit's fast-signal feed — **only accepts a classic
PAT**. Fine-grained PATs cannot call it, and GitHub mention detection currently
comes only from that feed, so the two token kinds are not equivalent:

| | Classic PAT | Fine-grained PAT |
|---|---|---|
| Fast signals (mentions, review requests, CI) | ✓ via the notifications feed | ✗ not available |
| Item discovery (authored / reviewing / assigned) | ✓ | ✓ — 15-minute reconcile only |
| Merge gate + diagnostic badges | ✓ | ✓ |
| Read-only (no write access) | ✓ public-only (`notifications` alone); ✗ private repos need `repo`, which grants write | ✓ always |

Pick by what matters to you: a **fine-grained PAT** for a true read-only,
least-privilege token (at the cost of no mentions and slower signals), or a
**classic PAT** for the full feature set (at the cost of the read-only guarantee
on private repos). See `docs/Provider_API_Analysis.md` for why the notifications
tier is classic-only.

### Fine-grained PAT (read-only, degraded fast tier)

Settings → Developer settings → Personal access tokens → **Fine-grained
tokens** → Generate new token. Scope it to the repositories/orgs you want and
grant these **read-only** permissions:

- **Metadata:** read (required)
- **Pull requests:** read
- **Commit statuses:** read — for CI signals
- **Checks:** read — for CI signals
- **Issues:** read — for issue mentions

### Classic PAT (full feature set)

Settings → Developer settings → Personal access tokens → **Tokens (classic)** →
Generate new token. Select:

- **`notifications`** — the fast feed (mentions, review requests, CI activity).
  This alone also reads *public* PR details, so for public repositories it is
  the only scope you need.
- add **`repo`** *only* for private repositories (to read their PR details).
  There is no read-only classic scope for private repos — `repo` grants *write*,
  trading away DevPit's read-only guarantee, so add it only if you track private
  repos.

### Expiry & config

Set an expiry you will actually rotate (e.g. 90 days). GitHub Enterprise: add
`base_url: https://github.example.com`.

```yaml
connections:
  - id: github-personal
    type: github
    token: ghp_…            # classic — or github_pat_… for a fine-grained PAT
    label: Personal         # optional — shown on each row; defaults to the id
```

## GitLab

GitLab has a clean read-only scope, so setup is simple and least-privilege.

(avatar) → **Edit profile** → **Access tokens** → Add new token. Grant the
single scope:

- **`read_api`** — full read-only API access (user, todos, MRs, pipelines,
  approvals). No write anywhere; this is DevPit's least-privilege ideal.

Version floor: the merge gate needs GitLab ≥ 15.6 and reviewer states ≥ 16.11
(`docs/Provider_API_Analysis.md`); older self-hosted instances degrade their
declared capabilities. Set an expiry you will rotate.

```yaml
connections:
  - id: work-gitlab
    type: gitlab
    base_url: https://gitlab.example.com   # omit for gitlab.com
    token: glpat-…          # read_api scope
    label: Work
```

## Jira

Jira status enrichment is optional (`ADR/ADR-0021_Jira_Ticket_Enrichment.md`):
with a `jira:` block present, items show their ticket's workflow status inline.
It uses Jira Cloud basic auth — your account **email** plus an **API token**
(there is no scope selection; the token carries your own read access, and DevPit
reads only issue status/summary).

Atlassian account → **Security** → **Create and manage API tokens** → Create API
token (at `id.atlassian.com`). Copy it immediately — Atlassian shows it once.

```yaml
jira:                       # optional — omit the whole block to disable
  base_url: https://your-org.atlassian.net
  email: you@example.com
  api_token: …
```

An absent block turns the feature off; a present block requires all three fields
(`ADR/ADR-0021_Jira_Ticket_Enrichment.md`).
