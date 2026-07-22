# Implementation plan: docs + lint cleanup (housekeeping P0/P2)

Source: housekeeping review, 2026-07-10 (items 1–3, 7–12). All refs line-checked
against the current tree. Mechanical, well-specified work — recommended model
**Sonnet 4.6, high effort**. Each item is independent; do them in any order.
Run `golangci-lint run ./...`, `go-arch-lint check`, and `go test ./...` at the
end (all green today).

## 1. Pin CI tool versions (drift/reproducibility — highest value)

`.github/workflows/ci.yml` — `latest` lets a tool release silently change what
passes CI. Pin to explicit tags and bump deliberately.

- `:31` `version: latest` → `version: v2.12.2` (current golangci-lint).
- `:43` `go install github.com/fe3dback/go-arch-lint@latest` →
  `...@v1.16.0` (current go-arch-lint).

## 2. README precedence is inverted (user-facing, factually wrong)

`README.md` states Ready to Merge *first*; code
(`internal/attention/states.go:25`) ranks it *fourth*, behind the
action-demanding states — the central design principle (ADR-0016).

- `:45` sentence → `Needs Review → Changes Requested → Blocked → Ready to Merge
  → Mentioned → Waiting on Author`.
- `:38-43` the states table also leads with Ready to Merge; reorder its rows to
  match the precedence above, or add a note that the table is not in precedence
  order.

## 3. README dead link

`README.md:51` links `docs/Configuration.md`, which does not exist. Repoint to
`ADR/ADR-0015_Multi_Account_Connections.md` + `internal/config/config.go`
(the real config contract), or create the file. Simplest: repoint.

## 7. ADR-0012 stale "placeholder"

`ADR/ADR-0012_Project_Structure.md:10` calls `frontend/` a "placeholder"; the
SPA is fully built and four other ADRs say "Implemented (v0.1)". Drop the word
"placeholder" — just list `frontend/`.

## 8. Event_Taxonomy retention described as built

`docs/Event_Taxonomy_and_Storage.md:133-135` ("## Retention") describes
user-initiated "clear history older than X" in present tense, but no such
storage method or endpoint exists (it's a v0.2 Roadmap item). Tag it
**Deferred**, matching the styling of the app_state paragraph earlier in the
same file.

## 9. Delete dead field `Engine.conns`

`internal/engine/engine.go:39` declares `conns []*conn`; `:130` appends to it;
it is **never read** anywhere. Delete the field and the append line.

## 10. Delete unused `errCodeNotFound`

`internal/api/errors.go:10` — defined, never used (only `errCodeBadRequest` and
`errCodeInternal` are). Delete it (or wire a 404 handler if one is planned —
but nothing needs it today, so delete).

## 11. Raw driver error leaked into user banner

`internal/engine/cycle.go:243` — `"local storage error — "+err.Error()` pushes a
raw SQLite message to the SSE banner, inconsistent with the plain-language
`causeText` used for provider errors. Replace with a fixed phrase (e.g.
`"local storage error"`), dropping the appended `err.Error()`. Low-risk,
single-user local app.

## 12. Why.md event vocabulary drift (soft)

`docs/Why.md:99-103` invents CamelCase event names (`ReviewRequested`,
`Mentioned`, `PipelineFailed`, `ConflictDetected`, `BackportNeeded`) that do not
match the real taxonomy (`sdk/provider.go`: `item.observed`, `signal.mentioned`,
…). Why.md is a vision doc — replace the concrete list with a prose sentence
("normalized into common events — reviews requested, mentions, CI failures —
then folded into attention states") so it can't drift. `BackportNeeded` is a
future/vision item; keep it only as prose, not as a current event type.

## Done when

- `.github/workflows/ci.yml` pins both tool versions.
- README precedence + table + link all correct.
- ADR-0012, Event_Taxonomy, Why.md updated.
- `Engine.conns`, `errCodeNotFound`, and the raw-error banner string removed.
- All three gates + tests green.
