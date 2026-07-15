---
name: add-provider
description: >
  Scaffold a new forge provider in the DevPit repo end-to-end: a provider/<name>/
  package implementing the sdk.Provider contract with duplicated (not shared)
  helpers per ADR-0003, init() registration, the .go-arch-lint.yml component +
  dependency mapping, composition-root wiring in cmd, a fixture-based test
  skeleton, honest capability declaration, and a docs/Provider_API_Analysis.md
  section. Use in the DevPit repo when the user asks to add a provider / forge /
  integration (e.g. Forgejo, Codeberg, Gitea, Bitbucket) or says "add-provider".
allowed-tools: Bash, Read, Grep, Glob, Edit, Write
---

# DevPit add-provider

Providers are peers integrated as plugins (`ADR/ADR-0003`). The load-bearing
rule: **providers duplicate helper code and depend on `sdk` only** — never on
each other and never on a shared `provider/*` package, so a change to one can't
regress another. `go-arch-lint` enforces this. This skill scaffolds a new one by
mirroring an existing provider, not by inventing structure.

## 1. Orient (read the current shapes — don't assume)

- `sdk/provider.go` — the `Provider` contract to implement, and the capability
  type. This is the spec of record; `docs/Provider_SDK.md` explains its
  semantics.
- `provider/github/` and `provider/gitlab/` — the two working templates. List
  their files and pick the closer analogue (GraphQL vs REST). Note how each file
  is organised (client, identity, normalize, types, urls, fastpoll, reconcile,
  doc) — you will mirror this set.
- How providers **register**: find the `init()` registration in an existing
  provider and the registry it calls.
- How `cmd/devpit/main.go` constructs providers (the composition root).
- `docs/Provider_API_Analysis.md` — the per-provider API research format.

## 2. Confirm inputs with the user

Provider name (package + config `type`), host/base-URL model, API style
(GraphQL/REST), auth (token scopes), and which capabilities the forge can
honestly support. Declare capabilities honestly — the engine never asks a
provider for a bucket it declared unavailable.

## 3. Scaffold provider/<name>/

Create the package mirroring the chosen template's file set. **Duplicate** the
helpers you need (JSON decode, time parse, status mapping) into this package —
do not import another provider or introduce a shared helper. Implement every
method of `sdk.Provider`. Register via `init()` the same way the templates do.

## 4. Wire the boundaries

- `.go-arch-lint.yml`: add a `providerX: { in: provider/<name> }` component with
  `mayDependOn: [sdk]`, and add it to `cmd`'s `mayDependOn` list.
- `cmd/devpit/main.go`: construct/register the provider as the others are.

## 5. Tests (fixture-based)

Add a `<name>_test.go` mirroring an existing provider test, driven by recorded
fixtures under `testdata/fixtures/`. Cover identity resolution and
normalization of the provider's payloads into the sdk shapes.

## 6. Docs

- Add a `docs/Provider_API_Analysis.md` section for the forge.
- If the provider is on the roadmap (e.g. Forgejo/Codeberg for v0.2), update
  `docs/Roadmap.md` / README status only where they already track providers —
  don't restate timing that lives in the Roadmap.

## 7. Verify

Run `scripts/check.sh` and get every gate green — `arch` proves the layering,
`test` proves the fixtures, `lint`/`vet`/`gofmt` the rest.
