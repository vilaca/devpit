# Secret Storage

## Scope

Implemented (v0.1) — the 0600 permission warning lives in `internal/config`.
See `docs/Roadmap.md`.

## Context

DevPit needs provider tokens to poll. It is a personal, single-user,
self-hosted tool running on a machine the user owns. The question is how much
protection the tokens need at rest.

## Decision

Tokens are stored in plaintext in the config file — no encryption at rest.
Mitigations that add no complexity:

- Restrictive file permissions (0600), with a startup **warning** (not a
  failure) if the file is readable beyond the owner.
- Least-privilege, read-only token scopes so a leaked token cannot write
  (`ADR/ADR-0017_Read_Only_Action_Model.md`).

## Rationale

For a single user who owns the host, encryption at rest buys little — the key
would live on the same machine — while adding real complexity (key management,
prompts). Simplicity wins at this scope.

## Consequences

- **Accepted trade-off**: anyone who can read the config file, a backup, or the
  host has the tokens.
- The config shape and the permission check are direct code
  (`internal/config/config.go`).
