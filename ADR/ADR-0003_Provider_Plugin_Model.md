# Provider Plugin Model

## Status

Accepted

## Scope

Implemented (v0.1) for GitHub and GitLab (`provider/github`, `provider/gitlab`,
`sdk`). More forges (Forgejo, Gitea) are **Planned**. See `docs/Roadmap.md`.

## Context

DevPit aggregates work from many source systems, each with its own API,
authentication, and capabilities. The core must stay provider-agnostic.

## Decision

Each source system is a provider plugin implementing a common interface
(`sdk.Provider`). Provider setup is **token-only**: a token (and, for
self-hosted, a base URL) is enough — no webhooks, callback URLs, or
provider-side configuration. Identity is auto-detected via the provider's
`/user` endpoint, with a required manual fallback when the token cannot resolve
a usable handle (bot/deploy tokens). Plugins **declare their capabilities**;
buckets a provider cannot feed simply produce no items, surfaced as a scoped
"unsupported" marker rather than an error.

Providers are kept **independently evolvable**: near-identical helper code (JSON
decoding, time parsing, dedupe-key construction, HTTP status mapping) is
**deliberately duplicated per provider rather than factored into a shared
`provider/*` helper**. The arch rules (ADR-0013) already forbid providers from
importing each other; this decision extends that to shared code they might
otherwise import in common.

## Rationale

A common interface keeps the core provider-agnostic and lets new providers be
added independently. Token-only setup honors the "a token is enough" promise
(`ADR/ADR-0001_Local_First_Web_Application.md`); capability declaration lets a
provider degrade gracefully instead of failing when its token or API version
cannot serve a bucket. Duplication over a shared helper is chosen so a change
driven by one forge's API cannot regress another provider: each plugin evolves
in isolation, and forge APIs diverge enough (merge-gate vocabularies, ID shapes,
rate-limit headers) that a shared abstraction would accrete conditionals anyway.

## Consequences

- The contract is `sdk/provider.go`; its semantics are specified in
  `docs/Provider_SDK.md`, and the per-provider API research (call sets, token
  guidance, merge-gate mappings) lives in `docs/Provider_API_Analysis.md`.
- The capability set is direct code (`sdk.Capabilities`); the engine never asks
  a provider to produce a bucket it declared unavailable.
- Providers may import neither `internal/*` nor each other — enforced in CI
  (`ADR/ADR-0013_Linting_and_Architecture_Enforcement.md`).
- Accepted cost: a fix to genuinely shared logic must be applied to each
  provider by hand, and `dupl`/`goconst` may need local `//nolint` at the
  duplicated sites. Revisit only if a shared concern grows unmanageable across
  four or more providers.
