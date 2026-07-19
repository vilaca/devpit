# Security

PAT-first, optional OAuth, read-only by default, least privilege.

Tokens are stored in plaintext in the config file (no encryption at rest) —
a deliberate simplicity choice for a personal, single-user, self-hosted
deployment. Mitigations: restrictive file permissions (0600) with a startup
warning if the file is more permissive, and least-privilege / read-only
token scopes so a leaked token cannot write. See docs/Design_Decisions.md
§15.
