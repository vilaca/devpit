---
name: signal-add
description: >
  Add a new attention signal or diagnostic badge across the DevPit stack:
  provider raw fact -> read-layer fold -> frontend chip/badge + hover text ->
  UI_Vocabulary parity table, with fixture tests. Follows the signal model in
  ADR-0016. Use in the DevPit repo when the user asks to add a signal, chip,
  badge, marker, or state to the attention list, or says "signal-add". Note the
  signal vocabulary is largely settled; confirm the new signal is actually
  warranted before scaffolding.
allowed-tools: Bash, Read, Grep, Glob, Edit, Write
---

# DevPit signal-add

DevPit surfaces the provider's own **neutral facts** as signals — state chips,
diagnostic badges, age tints — not an inferred workflow (`ADR/ADR-0016`,
`docs/Attention_Engine.md`). Adding one touches the whole read path. The
vocabulary is largely settled, so first confirm the signal is warranted and
which existing category it belongs to rather than inventing a new kind.

## 1. Orient (read the current model)

- `ADR/ADR-0016` — presentation/ranking + the signal model and its principles
  (parity, gate-gating, tooltip onset).
- `internal/attention/states.go` and the fold — where signals are defined and
  computed on read.
- `docs/UI_Vocabulary.md` — the provider-parity table; `docs/Attention_Engine.md`
  — the vocabulary.
- The frontend rendering of an existing chip/badge as your template.

## 2. Classify the signal

Decide: state chip vs. diagnostic badge vs. age tag; and whether it is
**gate-gated** (only shown when the merge gate is blocked). Apply the **parity
principle** — a signal ships for a provider only where that provider reports a
user-readable verdict for it; declare it honestly per provider.

## 3. Producer: provider raw fact

In each provider that can honestly report it, set the raw fact (extend the
GraphQL/REST query and normalization). Duplicate per provider — no shared
helper (ADR-0003).

## 4. Read layer: fold

Surface the signal in `internal/attention` (states/fold), including onset
tracking so the UI can show "for 3d" hover text where the model does that.

## 5. Frontend

Render the chip/badge mirroring an existing one, with hover text. Keep the
visual separation of state chips / diagnostic badges / age tags.

## 6. Docs + tests

- Update the `docs/UI_Vocabulary.md` parity table and `docs/Attention_Engine.md`
  vocabulary in the same change (doc-freshness rule).
- Add/extend fold fixtures and tests.
- Run `scripts/check.sh`.
