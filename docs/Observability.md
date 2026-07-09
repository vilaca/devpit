# Observability

Operational observability — structured logs, metrics, tracing, health
endpoints, sync statistics — is largely **future scope** (`docs/Roadmap.md`);
v0.1 uses standard structured logging.

Distinct from operational logs, DevPit exposes a **user-facing** sync/poll log
in the UI (per-cycle summaries, per-call detail on failure) so users can see
polling working and understand failures. That is a product feature, specified
in `docs/Synchronization_Engine.md` and decided in
`ADR/ADR-0018_Sync_Observability.md`.
