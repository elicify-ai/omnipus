---
name: elicify-test-writing
metadata:
  display_name: Test Writing
description: >-
  Plan and write tests that can actually fail. Enforces oracle independence
  (expected values derived from the specification, never read off the
  implementation), maximum assertion strength, deliberate boundary/negative/
  property case derivation, and a mock boundary that leaves the unit under test
  real. Ends with a mandatory self-verification gate: mutate your own code and
  confirm the test dies. Use before writing any test, when adding tests to
  existing code, when a bug needs a reproducing test, when asked to improve test
  coverage or test quality, and whenever a suite is green but not trusted. Load
  this BEFORE writing the first assertion, not after.
argument-hint: "[spec file, feature description, or path to code needing tests]"
allowed-tools: Read, Grep, Glob, Bash, Write, Edit
---

# Test Plan & Write

## The one thing

**A test is worth exactly what it would have caught.**

Not what it covers. Not how many there are. Not whether it is green. A test
that cannot fail is not a weak test — it is a *false* one, because it emits the
signal "verified" while verifying nothing. Green tests that cannot fail are
worse than no tests, because they consume the trust that no-tests would have
correctly withheld.

Everything in this skill exists to make your tests capable of failing for the
right reason.

## When to use

Load this **before writing the first assertion** — when implementing new
behaviour, fixing a bug, adding tests to untested code, being asked to raise
coverage, or reviewing your own test output before declaring work done.

Do not load it for pure configuration, documentation, or static-content changes
with no behavioural surface.

---

## The law: oracle independence

> **The expected value in a test must come from the specification, never from
> the implementation.**

This is not style guidance. It is the difference between a test and a mirror.

When an LLM writes tests from a specification with no implementation in view,
those tests catch about **25%** of injected faults. When it writes tests *after*
reading faulty code, detection collapses to about **14%**. The implementation's
mistaken assumption propagates straight into the test oracle — and more
elaborate reasoning does not repair it. The isolation does.

Concretely, this forbids the workflow you will naturally reach for:

```
✗  write code → run it → observe output → write assertions matching the output
```

That certifies whatever the code does, including its bugs, and it produces a
suite that is green by construction. Do this instead:

```
✓  read spec → derive expected values → write failing test → write code → green
```

**The operational test, applied to every assertion you write:**

> If the implementation were subtly wrong, would this expected value still be
> what I wrote?

If the honest answer is no — if you got the number by running the code — the
assertion has no oracle power. Go back to the specification, the ticket, the
API contract, the docstring, the standard, or first-principles arithmetic, and
derive it. If no independent source exists, **that is a specification gap, and
naming it is more valuable than the test would have been.** Say so.

### When the code already exists

You will often be asked to test code that is already written. You cannot unsee
it. Handle it honestly:

1. Derive expectations from the spec, ticket, docstring or contract **first**,
   writing them down before you re-read the implementation.
2. Where the code and your derivation disagree, **do not** adjust your
   derivation. That disagreement is either a bug or a spec gap, and it is the
   single most valuable thing you will find. Report it.
3. Where no independent source exists at all, mark those tests
   `characterization test` in a comment — they pin current behaviour, they do
   not verify correctness, and they must never be described as verification.

---

## Workflow

### 1 — PLAN before writing

Produce a short written plan first. Skipping straight to code is how you end up
with six tests of the happy path and none of the edges.

Discover the stack before planning: read `package.json`, `pyproject.toml`,
`go.mod`, `pom.xml`, `Cargo.toml`, `Makefile`, existing test files. Match the
repository's framework, naming, layout and assertion style. Prefer checked-in
wrappers (`./gradlew`, `make test`) over global tools.

Then fill in the plan — the template is in `knowledge/test-plan-template.md`:

- **Behaviour under test** — one sentence, in terms of observable outcome.
- **Specification source** — the file, ticket, contract or standard each
  expected value derives from. "The implementation" is not a valid entry.
- **The unit boundary** — what is real, what is mocked, and why.
- **Case table** — every case with its input, expected output, and the *source*
  of that expectation.
- **What would break this** — the mutations you will use in step 4.
- **Known gaps** — what you are deliberately not covering, and why.

### 2 — DERIVE cases systematically

Do not brainstorm cases. Derive them. Full catalogue in
`knowledge/case-derivation.md`; the minimum bar:

- **Happy path** — at least one, with an exact expected value.
- **Boundaries** — for every numeric or sized input: `min-1, min, min+1,
  max-1, max, max+1`. Off-by-one is the most common real defect and the most
  commonly untested.
- **Equivalence classes** — one representative per class of input that the code
  treats differently. More cases from the same class add runtime, not coverage
  of risk.
- **Negative and error paths** — invalid input, missing input, wrong type,
  empty, null, zero, negative, oversized, malformed. **Assert the specific
  error type and the specific message or code.** A suite with no negative cases
  is a suite that has not been designed.
- **State and ordering** — where behaviour depends on sequence, test the
  sequence, not just the end state.
- **Properties and invariants** — where exact expected outputs are hard to
  derive, assert relationships instead: round-trip (`decode(encode(x)) == x`),
  idempotence, monotonicity, conservation, ordering. These often catch more
  than a dozen example-based tests.

Target a **negative-case ratio of at least 30%**. If every test you wrote is a
happy path, you have not tested — you have demonstrated.

### 3 — WRITE at maximum assertion strength

Every assertion sits somewhere on this lattice. **Always write the highest rung
the behaviour genuinely supports.**

```
exact equality          ← write here by default
      ↓
bounded range           ← only for genuine float/timing tolerance
      ↓
type check
      ↓
non-null
      ↓
truthiness
      ↓
no assertion            ← this is not a test
```

Full per-framework strong/weak tables in `knowledge/assertion-strength.md`.
The rules that matter most:

- **Assert the value, not its existence.** `assert result == 42`, never
  `assert result is not None`.
- **Assert the whole shape** where the whole shape is specified — the full
  list, not `x in list`; the exact dict, not one key.
- **Assert specific exceptions with specific messages** — `raises(ValueError,
  match="rate must be between 0 and 1")`, never `raises(Exception)`.
- **Assert call arguments, not call occurrence** — `assert_called_once_with(...)`,
  never `assert_called()`.
- **One behaviour per test, named for the behaviour.** `test_discount_rejects_
  negative_price`, not `test_discount_2`. A test name that does not state the
  expected outcome cannot be triaged when it fails.
- **Give every assertion a distinguishable failure.** If a test has five bare
  assertions, a failure tells you nothing about which. Add messages or split.
- **No magic numbers.** Every literal either derives visibly from the spec or
  carries a comment saying where it came from.
- **Seed the real stored shape.** A replay, reload or persistence test seeds
  its fixture with the exact shape the system actually stores, copied from a
  real artifact (a saved transcript, a stored record) — never a simplified
  stand-in. A fixture of plain strings where the store holds `{text: ...}`
  objects passed while the real reload path showed "0 lines".

Mocking is where good-looking tests go to die — full rules in
`knowledge/mocking-and-isolation.md`. The single decisive rule:

> **Never mock the unit under test, and never mock at a boundary inside it.**
> Mock at the process edge — network, clock, filesystem, randomness, paid
> third-party APIs — and leave everything the test claims to verify real.

Coding agents add mocks in ~36% of commits versus ~26% for humans, and the
failure is always the same shape: the test ends up asserting that a mock
returned what the mock was configured to return.

### 4 — PROVE the test can fail

**This step is mandatory and is what separates this skill from an intention.**
A test you have not seen fail is not a test you have verified; it is a test you
have hoped for.

1. **See it red for the right reason.** Run the test before the implementation
   exists, or against the unfixed bug. Read the failure message: does it name
   the actual expected-versus-actual? A test that fails with `ImportError` or
   `NameError` has not demonstrated anything.
2. **See it green.** Implement, run, confirm.
3. **Mutate the implementation and confirm the test dies.** Apply at least
   three mutations to the code you just wrote and re-run:

   ```
   change a constant            true → false          < → <=
   remove a branch              remove an error raise  invert a condition
   return null / zero value     off-by-one on a boundary
   ```

   **Any mutation that survives is a hole in your test, not a quirk.** Fix the
   test, not the mutation. Revert every mutation afterwards and confirm green.

Why this step and not coverage: in industrial measurement, **49% of tests that
uniquely killed a mutant added no additional line coverage at all.** Coverage
tells you a line executed. Only mutation tells you an assertion was watching.

---

## The gate

**You may not describe tests as written, done, passing or verified until every
line below is true.** Work through it explicitly; do not assert it from memory.

### Oracle independence
- [ ] Every expected value traces to a spec, ticket, contract, standard or
      derivation — **not** to observed output of the code under test.
- [ ] No assertion was written by running the code and copying the result.
- [ ] Any test that pins existing behaviour without an independent source is
      labelled `characterization test` and is not called verification.
- [ ] Disagreements between my derivation and the implementation are reported,
      not silently resolved in the implementation's favour.

### Case coverage
- [ ] At least one exact-value happy path.
- [ ] Boundaries tested at `min-1, min, min+1, max-1, max, max+1` for every
      bounded input.
- [ ] Negative and error cases present, asserting **specific** error type and
      message or code. Negative-case ratio ≥ 30%.
- [ ] Empty / null / zero / malformed inputs covered where reachable.
- [ ] Invariants or properties asserted where exact outputs are hard to derive.
- [ ] Deliberate gaps written down in the plan, not left implicit.

### Assertion strength
- [ ] Every assertion is at the highest lattice rung the behaviour supports.
- [ ] Zero tests with no assertion. Zero tests whose only assertion is
      `is not None`, `toBeTruthy`, `assertTrue(x)` or equivalent.
- [ ] Exceptions asserted by specific type **and** message/code.
- [ ] Mock assertions check arguments, not merely that a call happened.
- [ ] Tolerances are the tightest the numerics genuinely allow, with the reason
      stated.
- [ ] No magic numbers without a stated origin.
- [ ] Every test name states the expected outcome.

### Isolation
- [ ] The unit under test is real — not mocked, partially mocked, or spied.
- [ ] Mocks sit at process edges only (network, clock, filesystem, randomness,
      external paid APIs).
- [ ] No test asserts only that a mock returned its configured value.
- [ ] Tests pass in any order and in isolation; no shared mutable state.

### Proof of failability
- [ ] Every new test was **observed red** before being made green, with a
      failure message naming expected vs actual.
- [ ] At least three mutations applied to the implementation; **every one was
      caught**. Survivors were fixed by strengthening the test.
- [ ] All mutations reverted; suite green afterwards.

### Execution integrity
- [ ] The **full** suite ran — collected count matches expected count.
- [ ] Zero collection errors.
- [ ] Skip / xfail counts unchanged from baseline.
- [ ] Zero new warnings, suppressions or lint disables introduced.
- [ ] Suite ran against the **final** state of the code — nothing edited after
      the run. Re-run if anything changed.
- [ ] Reported result is the raw exit code, not a summary of it.

---

## Forbidden moves

If a test fails, the failure is information. **Never make a test pass by
weakening the test.** These are prohibited regardless of pressure, deadline or
how confident you are that the failure is spurious:

- deleting or skipping a failing test (`skip`, `xfail`, `@Disabled`, `.only(`,
  `t.Skip`, `NotImplementedError` stubs, environment-gated early returns)
- weakening or removing an assertion
- changing an expected value to match observed output
- widening a tolerance, timeout or match pattern to swallow the difference
- mocking the thing that is failing
- catching and swallowing the exception
- demoting an error to a warning, adding `# noqa` / `@ts-ignore` /
  `eslint-disable` / `TreatWarningsAsErrors=false`
- lowering a coverage threshold or adding a coverage exclusion
- `|| true`, `exit 0`, `continue-on-error`, or any harness change that converts
  a red run into a green one

**When a test fails and you cannot fix the cause, report the red.** A red test
with an honest explanation is a contribution. A green test that hides it is a
liability that someone will pay for later, with interest.

Every item above is independently detectable, and the `test-integrity-auditor`
agent is built to find all of them. Assume it will.

---

## Report honestly

When you hand work back, never lead with `Tests: GREEN`. Lead with what was
actually established:

```
tests added        : 14   (9 positive, 5 negative)
observed red first : 14 / 14
mutations applied  : 6    caught: 6    survived: 0
full suite         : 1,428 collected / 1,428 passed / 0 skipped / 0 errors
expectations from  : pricing-spec.md §3.2, ticket ENG-4471
not covered        : concurrent-write path — needs an integration harness
```

If you could not establish something, say `UNVERIFIED`. Never let a gap default
to favourable.

---

## References

| File | Use it for |
|---|---|
| `knowledge/assertion-strength.md` | The lattice, and strong/weak tables for pytest, unittest, Jest/Vitest, Go, JUnit, RSpec, Rust |
| `knowledge/case-derivation.md` | Deriving boundary, equivalence-class, negative, state, property and metamorphic cases from a spec |
| `knowledge/mocking-and-isolation.md` | Where the mock boundary belongs, test-double taxonomy, and the over-mocking failure shapes |
| `knowledge/anti-patterns.md` | The full catalogue of green-manufacturing patterns, with detection signatures |
| `knowledge/test-plan-template.md` | The plan to fill in during step 1 |
