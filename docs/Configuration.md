# Configuration

DevPit reads one YAML file at startup (path from `--config`, defaulting to
`$XDG_CONFIG_HOME/devpit/config.yaml`). The file is the only runtime
configuration — connections are static (`ADR/ADR-0015_Multi_Account_Connections.md`)
and poll cadences are engine constants, not settings
(`ADR/ADR-0004_User_Centric_Synchronization.md`).

The full key schema of record is `internal/config/config.go`. Connections and
the optional `jira:` block — with per-provider token scopes and paste-ready
snippets — are covered in [`docs/Token_Setup.md`](Token_Setup.md). This page
documents only `listen:`, the one key with a security implication.

## `listen`

Optional TCP address the dashboard API binds to. Defaults to `localhost:7474`.

> **Security.** The API is unauthenticated (`ADR/ADR-0001_Local_First_Web_Application.md`).
> Keep the bind on loopback. Set a non-loopback value (e.g. `:7474`) **only**
> inside a container — where loopback is unreachable from the host — and publish
> it host-side as a loopback port map (`-p 127.0.0.1:7474:7474`), never on a
> routable interface. Rationale:
> `ADR/ADR-0023_Packaging_Distribution_and_Release_Pipeline.md`.
