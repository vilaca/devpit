# doc-check regression corpus

Pinned cases the diff-mode `/doc-check` must reproduce. This is the acceptance
test for the *judge* — run it (`/doc-check corpus`) whenever the skill prompt or
the model changes, so judge drift (an upgrade silently making the check lenient
or noisy) is visible instead of silent.

`cases.yaml` lists every case. Each is a **`diff`** — a synthetic change (the
`.diff` file) fed to the judge as if it were the working diff. These are *judge
inputs*, not necessarily `git apply`-clean: enough context to reason over, no
more. A case pins `expect: FINDING` with the `check` (the SKILL.md section
number) and the `target` doc the finding must name, or `expect: CLEAN`.

The `CLEAN` case is a **restraint** test — the run fails if the judge flags it,
which is how we catch a check that drowns real findings in false alarms.

A mismatch means a check's instructions drifted — fix the skill, not the corpus.
When the tree changes under a case (a cited doc line or symbol moves), refresh
the diff's context so it still describes the current tree.
