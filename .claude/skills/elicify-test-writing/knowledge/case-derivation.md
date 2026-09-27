# Case Derivation

Do not brainstorm test cases. **Derive** them. Brainstorming produces six happy
paths; derivation produces the cases that fail.

## Step 1 — Extract the contract

Before any case, write down, from the specification and not from the code:

- **Inputs** — each parameter, its type, and its valid domain (range, length,
  format, nullability).
- **Outputs** — the exact return shape and the values it can take.
- **Errors** — every failure mode, its trigger condition, and its specific
  type/message/code.
- **Side effects** — what is written, sent, logged, or mutated.
- **Invariants** — what must hold before, during and after, always.

If any of these is unknown, that is a **specification gap**. Naming it is worth
more than the test you were about to write. Say so explicitly rather than
guessing and encoding the guess as an expectation.

## Step 2 — Equivalence classes

Partition each input's domain into classes the code treats *differently*. One
representative per class. Additional cases from the same class add runtime, not
risk coverage.

For `discount(price: float, tier: str)`:

```
price :  negative | zero | normal | very large | NaN / Inf
tier  :  "free" | "pro" | "enterprise" | unknown string | empty | None
```

Every class needs a case, including the ones that are supposed to be rejected.

## Step 3 — Boundaries

Off-by-one is the most common real defect and the least commonly tested. For
every bounded input, test **six points**:

```
min - 1   min   min + 1   …   max - 1   max   max + 1
```

Boundaries hide in more places than numeric ranges:

| Kind | Boundary cases |
|---|---|
| Numeric range | `min-1, min, min+1, max-1, max, max+1` |
| Collection size | `empty, one, two, n-1, n, n+1, over-limit` |
| String length | `"", "a", at limit, one over limit` |
| Dates | epoch, DST transition, leap day, year boundary, month end (28/29/30/31) |
| Pagination | page 0, page 1, last page, last+1, page size 0, page size max |
| Money | zero, smallest unit, rounding half-up/half-even, negative, currency max |
| Time windows | just before open, exactly at open, exactly at close, just after |
| Unicode | ASCII, multi-byte, combining marks, emoji, RTL, zero-width |

## Step 4 — Negative and error paths

**Target: at least 30% of cases.** A suite with no negative cases has not been
designed; it has been demonstrated.

For every documented error, assert the **specific** type and the **specific**
message or code — not merely that something was raised:

```
invalid type            wrong type entirely
missing required        absent, null, undefined
out of range            below min, above max
malformed               unparseable format
empty                   "", [], {}, whitespace-only
too large               over size/length limit
duplicate               unique-constraint violation
unauthorized            missing / wrong / expired credentials
conflicting             mutually exclusive options both set
downstream failure      dependency times out, returns 500, returns garbage
```

That last row matters most and is tested least: **what does your code do when
its dependency misbehaves?** Not when it is absent — when it returns a 200 with
a malformed body, or hangs, or returns yesterday's data.

## Step 5 — State and sequence

Where behaviour depends on history, the sequence *is* the input:

```
operation on empty/initial state
operation twice in a row              (idempotent? or double-applied?)
operations out of expected order
operation after failure               (is state consistent, or half-written?)
concurrent operations                 (interleaving invariants)
operation after reaching a limit
undo / rollback / compensation
```

Asserting only the end state misses every defect that lives in the transition.

## Step 6 — Properties and metamorphic relations

When the exact expected output is hard to derive, assert **relationships**.
These often catch more than a dozen example-based tests, and they resist
overfitting because the agent cannot special-case an input it has not seen.

| Property | Form | Applies to |
|---|---|---|
| Round-trip | `decode(encode(x)) == x` | serializers, codecs, parsers, compression |
| Idempotence | `f(f(x)) == f(x)` | normalizers, upserts, migrations, sanitizers |
| Commutativity | `f(a,b) == f(b,a)` | merges, set operations, sums |
| Monotonicity | `x ≤ y ⟹ f(x) ≤ f(y)` | pricing, scoring, ranking, rate limits |
| Conservation | `sum(parts) == whole` | splitting, sharding, allocation, accounting |
| Invariance | `f(shuffle(x)) == f(x)` | aggregations, statistics |
| Inverse | `undo(do(x)) == x` | transactions, edits, state machines |
| Oracle comparison | `f(x) == slow_reference(x)` | optimized implementations |

Fuzz these over generated inputs where a property-testing library exists
(`hypothesis`, `fast-check`, `gopter`, `proptest`, `jqwik`). A property test
with 1,000 generated inputs is a far stronger oracle than three hand-picked
examples — and it cannot be satisfied by memorizing test inputs.

## Step 7 — Reproducing a bug

For a bug fix, the reproducing test comes **first** and must be observed red:

1. Write a test that fails **because of this specific bug**.
2. Confirm the failure message describes the bug, not a setup error.
3. Fix the code.
4. Confirm the test passes.
5. Confirm it still fails if you revert the fix.

Step 5 is the one that gets skipped and the one that proves the test is bound
to the bug rather than passing for an unrelated reason.

## The case table

Every case carries its **source**. This column is what enforces oracle
independence, and a row that cannot fill it is not ready to be written.

| # | Case | Input | Expected | Source |
|---|---|---|---|---|
| 1 | standard pro discount | `(100.0, "pro")` | `90.0` | pricing-spec §3.2 |
| 2 | lower boundary | `(0.0, "pro")` | `0.0` | pricing-spec §3.2 |
| 3 | below range | `(-0.01, "pro")` | `ValueError("price must be ≥ 0")` | pricing-spec §3.5 |
| 4 | unknown tier | `(100.0, "gold")` | `KeyError("unknown tier: gold")` | ENG-4471 |
| 5 | monotonicity | generated | `p1 ≤ p2 ⟹ f(p1) ≤ f(p2)` | derived invariant |

**"The implementation returns it" is not a valid source.** If that is the only
answer available, the row is a characterization test — label it as such, and do
not call it verification.
