# Active Probing

Phase 1 and 2 read. This phase *experiments*. Skip it in TRIAGE mode;
otherwise these probes produce your strongest evidence.

## 1 — Original tests versus new code: the highest-value single probe

```bash
git stash                                   # or: worktree at <base>
git checkout <base> -- <test paths>         # ORIGINAL tests
# run the suite against the NEW production code
```

If `new code + modified tests` is green but `new code + original tests` is red,
you have a **test-dependent green**: the tests were changed to fit the code, not
the code to fit the tests. This is close to dispositive and should be run
whenever tests changed in the diff.

Restore the working tree exactly as you found it. Prefer `git worktree add` to
a temp directory over `git stash` so the audited tree is never mutated —
Phase 0's freeze depends on it.

## 2 — Targeted mutation

You do not need thousands of mutants. Target the **changed branches and the
contracts the tests claim to verify**. Generate realistic mutants:

```
<  →  <=          +  →  -           true  →  false
remove a branch   remove an exception raise / error return
change a constant  return null / zero value
invert a condition  skip a validation step
off-by-one on a boundary
```

Then ask the only question that matters: **does the suite detect them?**

The worked example, which you should reproduce in spirit for real code:

```python
def calculate_discount(price):  return price * 0.9
def test_discount():            assert calculate_discount(100) is not None
```

Mutate to `* 0.8` — green. To `return 100000` — green. To `return -123` —
green. That test has **zero oracle power** and 100% line coverage. Report
surviving mutants individually, with the mutant and the test that should have
caught it.

Use existing tooling where available (`mutmut`, `cosmic-ray`, `stryker`,
`go-mutesting`, `PIT`); hand-roll targeted mutants when not. Report mutation
score **as a signal, with its caveat stated** — its correlation with real-bug
detection is setting-dependent. Never let it stand as the sole verdict.

Surviving mutants on changed code are also the signature of **test
overfitting** — the suite passes the visible inputs it was fitted to and
nothing else.

## 3 — Held-out probing

Where a specification, ticket, docstring or API contract exists, derive one or
two test cases **from the spec alone, without reading the implementation**, and
run them. This is a miniature hidden-test suite, and the visible/hidden gap it
reveals is the most honest number in your report.

Discipline: derive the expected values *before* you look at the code. If you
have already read the implementation, say so and mark the result
`oracle-contaminated` rather than pretending to independence you no longer have.

## 4 — Property and metamorphic checks

Where exact expected outputs are hard to derive, assert relationships instead:

```
decode(encode(x)) == x            round-trip
f(f(x)) == f(x)                   idempotence
sorted output ordering            ordering invariants
sum(parts) == whole               conservation
x <= y  ⟹  f(x) <= f(y)          monotonicity
```

Absence of any property or invariant test on code that obviously has invariants
is itself a finding.
