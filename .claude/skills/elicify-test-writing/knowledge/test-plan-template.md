# Test Plan Template

Fill this in **before** writing the first assertion. It takes a few minutes and
it is the step that prevents six happy-path tests and no edges.

---

## Behaviour under test

<One sentence, in terms of an observable outcome. Not "test the discount
function" — "a pro-tier customer is charged 90% of list price, and any price
below zero is rejected.">

## Specification sources

| Expectation | Source |
|---|---|
| discount rate for pro tier | `pricing-spec.md` §3.2 |
| rejection of negative price | `pricing-spec.md` §3.5 |
| unknown-tier error message | ticket ENG-4471 |

**"The implementation returns it" is not a valid source.** Any expectation
without an independent source produces a *characterization test*, which pins
current behaviour and must never be reported as verification.

**Specification gaps found:** <list them — this is often the most valuable
output of the whole exercise>

## Unit boundary

```
REAL  : <what the test genuinely exercises>
MOCK  : <what is faked, and why — must be process-edge only>
```

Self-check: *if everything I mocked were replaced with something completely
wrong, would this test still pass?* If yes, the boundary is too tight.

## Case table

| # | Case | Class | Input | Expected | Source |
|---|---|---|---|---|---|
| 1 | | happy | | | |
| 2 | | boundary | | | |
| 3 | | boundary | | | |
| 4 | | negative | | | |
| 5 | | negative | | | |
| 6 | | error path | | | |
| 7 | | property | | | derived invariant |

Coverage of classes — confirm each is present or deliberately excluded:

- [ ] happy path with an **exact** expected value
- [ ] every equivalence class represented once
- [ ] boundaries: `min-1, min, min+1, max-1, max, max+1`
- [ ] empty / null / zero / malformed
- [ ] each documented error, asserted by **specific** type and message/code
- [ ] downstream-dependency misbehaviour (timeout, 500, malformed body)
- [ ] state and sequence, where behaviour depends on history
- [ ] at least one property or invariant
- [ ] **negative-case ratio ≥ 30%**

## Mutations I will use to prove the tests can fail

At least three. These get applied to the implementation in step 4, and every
one must be caught.

| # | Mutation | Test that must catch it |
|---|---|---|
| 1 | `* 0.9` → `* 0.8` | |
| 2 | remove the negative-price guard | |
| 3 | `<` → `<=` at the tier boundary | |

## Deliberate gaps

<What you are not covering, and why. An honest gap is a contribution; a silent
one is a liability.>

Example: *concurrent-write path not covered — requires an integration harness
that does not exist yet. Tracked in ENG-4488.*

---

## Completion record

Fill in when done. This is what you report — never `Tests: GREEN`.

```
tests added        : <n>   (<positive> positive, <negative> negative)
observed red first : <n> / <n>
mutations applied  : <n>   caught: <n>   survived: <n>
full suite         : <collected> collected / <passed> passed / <skipped> skipped / <errors> errors
expectations from  : <sources>
not covered        : <gaps>
unverified         : <anything you could not establish — never default to favourable>
```
