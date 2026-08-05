# semantic-check regression corpus

Pinned cases the `/semantic-check audit` must reproduce. This is the acceptance
test for the *judge* — run it whenever the skill prompt or the model changes, so
judge drift (a model upgrade silently making the audit lenient) is visible
instead of silent.

`cases.yaml` lists every case. Two kinds:

- **`live`** — a finding that exists on the current tree. The audit must keep
  reporting it until the code is fixed or the finding is ADR-accepted; when that
  happens, delete the case (and add a `diff` case if the shape is worth pinning).
- **`diff`** — a synthetic change (the `.diff` file) fed to the judge as the
  scope. These are *judge inputs*, not necessarily `git apply`-clean: enough
  context to reason over, no more.

A case pins an `expect` verdict. The two `HOLDS` cases are **restraint** tests —
the audit fails if it flags them, which is how we catch an over-eager skeptic
pass (step 3) that would drown real findings in false alarms.

To run: for each case, give the invariant's entry plus the scope (the live tree,
or the `.diff`) to a hunter as in the skill's step 2–3, and assert the returned
verdict equals `expect`. A mismatch means the Hunt or the skeptic pass drifted —
fix the skill, not the corpus.
