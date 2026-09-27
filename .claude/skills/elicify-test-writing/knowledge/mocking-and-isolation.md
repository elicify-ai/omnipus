# Mocking and Isolation

Coding agents add mocks in roughly **36%** of their commits, against ~26% for
humans. The excess is not neutral: it produces tests that look thorough and
verify nothing, because the test ends up asserting that a mock returned what
the mock was configured to return.

## The decisive rule

> **Never mock the unit under test, and never mock at a boundary inside it.**
> Mock at the process edge. Leave everything the test claims to verify real.

## Where the boundary belongs

```
        ┌─────────── your system: REAL ───────────┐
                                                   │
  test → controller → service → domain → repo ─────┼──→ database      MOCK or
                                                   │                   real-in-container
                                          → http ──┼──→ payment API    MOCK
                                          → clock ─┼──→ wall time      MOCK (inject)
                                          → rand ──┼──→ entropy        MOCK (seed)
                                          → fs ────┼──→ disk           MOCK or tmpdir
```

**Mock at the edge** — things outside your process, that are slow, costly,
nondeterministic, or not yours:

- network calls to third parties (especially paid or rate-limited)
- wall-clock time and timezones
- randomness and UUID generation
- the filesystem, where a real temp directory is impractical
- email, SMS, push, payment capture — anything with a real-world side effect

**Do not mock** — things inside the behaviour under test:

- the class, function or module named in the test's own filename
- domain logic, validation, calculation, mapping, serialization
- your own service layer, when testing a controller that must exercise it
- the database, when a container or in-memory equivalent is available — an
  ORM query that is mocked has never been shown to be valid SQL

## Boundary movement is the failure to detect

```
Before:  controller → real service → real domain → mocked payment API   ✓
After:   controller → mocked service                                    ✗
```

The second version still passes. It has stopped testing the service, the
domain, the wiring between them, and every mapping in between. Nothing in the
test's name or shape reveals this.

**The question is never "are there mocks". It is "where is the boundary, and
did it move inward?"**

## Failure shapes to recognize

### Testing the mock

```python
mock_service.calculate.return_value = 90.0
result = controller.get_price(100)
assert result == 90.0        # asserts the mock returned its configured value
```

The discount logic is never executed. This test passes if `calculate` is
deleted entirely.

### Mocking the unit under test

```python
@patch("pricing.calculate_discount")     # ← the function being tested
def test_calculate_discount(mock_calc):
    ...
```

Any `patch` whose target lives in the module the test is named for is a defect.
Grep for it.

### Partial mocks and spies on the subject

Patching one method of the class under test means the test verifies a chimera —
half real object, half fixture — that exists in no production path.

### Mocks that encode the expected behaviour

```python
mock_repo.find.side_effect = lambda id: User(id=id, tier="pro")
```

The fixture now *contains* the business rule. If the real repository maps tiers
differently, every test still passes.

### Over-specified mocks

Asserting an exact sequence of internal calls tests the implementation's shape,
not its behaviour. The test then breaks on every legitimate refactor and
catches no bugs — the worst possible trade. Assert observable outcomes; assert
call arguments only at true edges, where the call *is* the outcome (a payment
was captured, an email was sent).

### Under-specified mocks

```java
verify(repo).save(any());
```

Proves a save happened. Does not prove the right thing was saved — which is
usually the whole behaviour. Use `eq(expectedEntity)` or an argument captor.

## Test double taxonomy

Precision here prevents most of the confusion:

| Double | What it is | Use when |
|---|---|---|
| **Dummy** | Filler, never used | A required parameter is irrelevant to the case |
| **Stub** | Returns canned answers | You need the collaborator to return something |
| **Fake** | Working lightweight implementation | In-memory repo, tmpdir FS — **usually the best choice** |
| **Spy** | Records calls, real behaviour | You must assert an interaction at an edge |
| **Mock** | Pre-programmed with expectations | The interaction itself is the behaviour under test |

**Prefer fakes.** An in-memory repository that genuinely stores and retrieves
exercises your real query-and-mapping code paths. A mocked repository does not.
Most over-mocking is a fake that was never written.

## Integration-level rules

- Use a real database in a container (Testcontainers or equivalent) for
  repository tests. Mocked SQL has never been shown to parse.
- Use a local HTTP stub server (WireMock, `responses`, `nock`, `httptest`) for
  external APIs, and assert the request you sent, not just the response you
  configured.
- Where you mock a third-party API, **pin the contract**: keep a recorded real
  response as a fixture and a periodic contract test against the live service.
  A mock that has drifted from reality is a test asserting a fiction.

## Isolation hygiene

- Tests pass in any order, and pass when run alone.
- No shared mutable state between tests; no reliance on a previous test's
  writes.
- Every mock, patch, env var and temp file is torn down — leakage makes
  failures land in an unrelated test and destroys triage.
- No test depends on the live network, real wall-clock time, or a developer's
  local files.
- Seed every RNG explicitly and assert the exact resulting sequence.

## The self-check

For each test with a mock, answer in one sentence:

> **If the real implementation of everything I mocked were replaced with
> something completely wrong, would this test still pass?**

If yes, and the mocked thing was inside the behaviour under test, the test has
no oracle power. Move the boundary outward or replace the mock with a fake.
