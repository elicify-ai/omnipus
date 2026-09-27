# Assertion Strength

## The lattice

Every assertion occupies a rung. Information carried falls as you descend.
**Write at the highest rung the behaviour genuinely supports.**

```
exact equality              assert x == 42
      ↓                     knows the value
bounded range               assert 41.9 < x < 42.1
      ↓                     knows the magnitude
type check                  assert isinstance(x, int)
      ↓                     knows the shape
non-null                    assert x is not None
      ↓                     knows something happened
truthiness                  assert x
      ↓                     knows it is not 0/""/[]
no assertion                result = f()
                            knows nothing — this is not a test
```

Moving down a rung to make a test pass is **assertion weakening** and is
forbidden. Moving down because the behaviour is genuinely nondeterministic is
legitimate — and must carry a comment saying which source of nondeterminism
forced it.

Two rungs deserve naming as their own hazard because they look like tests:

- **truthiness** — `assert x` passes for `1`, `"error"`, `[None]`, `-999` and
  any object. It distinguishes almost nothing.
- **non-null** — passes for every wrong answer that is not `None`. The
  canonical LLM assertion, and the canonical worthless one.

## Strong vs weakened, by framework

### Python — pytest

| Strong | Weakened |
|---|---|
| `assert result == 42` | `assert result` / `assert result is not None` |
| `assert items == [a, b, c]` | `assert a in items` / `assert len(items) > 0` |
| `assert len(items) == 4` | `assert items` |
| `assert d == {"k": 1, "j": 2}` | `assert "k" in d` |
| `pytest.raises(ValueError, match=r"rate must be in \[0,1\]")` | `pytest.raises(Exception)` |
| `assert abs(x - y) < 1e-9` | `assert abs(x - y) < 10` |
| `assert all(p.active for p in ps)` | `assert any(p.active for p in ps)` |
| `assert s == "expected exact"` | `assert "expect" in s` |
| `assert re.fullmatch(r"^\d{4}-\d{2}$", s)` | `assert "-" in s` |

### Python — unittest / mock

| Strong | Weakened |
|---|---|
| `assertEqual(x, expected)` | `assertIsNotNone(x)` / `assertTrue(x)` |
| `assertListEqual(a, b)` | `assertIn(item, a)` |
| `assertDictEqual(a, b)` | `assertIsInstance(a, dict)` |
| `assertRaisesRegex(ValueError, "...")` | `assertRaises(Exception)` |
| `mock.assert_called_once_with(a, b=2)` | `mock.assert_called()` |
| `assertEqual(mock.call_count, 1)` | `assertTrue(mock.called)` |
| `assertAlmostEqual(x, y, places=9)` | `assertAlmostEqual(x, y, places=1)` |

### JavaScript / TypeScript — Jest, Vitest

| Strong | Weakened |
|---|---|
| `expect(v).toEqual(expected)` | `expect(v).toBeTruthy()` / `toBeDefined()` |
| `expect(v).toStrictEqual(expected)` | `expect(v).toEqual(expect.anything())` |
| `expect(arr).toEqual([1, 2, 3])` | `expect(arr).toContain(1)` |
| `expect(arr).toHaveLength(3)` | `expect(arr.length).toBeGreaterThan(0)` |
| `expect(fn).toThrow(new RangeError("rate out of bounds"))` | `expect(fn).toThrow()` |
| `expect(spy).toHaveBeenCalledWith(a, b)` | `expect(spy).toHaveBeenCalled()` |
| `expect(spy).toHaveBeenCalledTimes(1)` | `expect(spy).toHaveBeenCalled()` |
| `expect(s).toBe("exact")` | `expect(s).toMatch(/exa/)` |
| `expect(n).toBeCloseTo(1.005, 5)` | `expect(n).toBeCloseTo(1.005, 0)` |

Inline snapshots are acceptable **only** when the value was derived from a spec
and reviewed. An auto-written or auto-updated snapshot is oracle adaptation
with a friendly name.

### Go

| Strong | Weakened |
|---|---|
| `if got != want { t.Errorf(...) }` | `if got == nil { ... }` |
| `reflect.DeepEqual(got, want)` | `len(got) > 0` |
| `errors.Is(err, ErrRateOutOfRange)` | `err != nil` |
| `assert.Equal(t, want, got)` | `assert.NotNil(t, got)` |
| `assert.EqualError(t, err, "exact message")` | `assert.Error(t, err)` |
| `t.Fatalf` on a precondition | ignoring the precondition |

Go-specific: an error test that only checks `err != nil` cannot distinguish the
error you meant from an unrelated failure earlier in the call. Use
`errors.Is` / `errors.As` with a sentinel or typed error.

### Java — JUnit 5 / AssertJ

| Strong | Weakened |
|---|---|
| `assertEquals(expected, actual)` | `assertNotNull(actual)` |
| `assertThat(list).containsExactly(a, b, c)` | `assertThat(list).isNotEmpty()` |
| `assertThrows(IllegalArgumentException.class, ...)` then assert message | `assertThrows(Exception.class, ...)` |
| `verify(dep).save(eq(expectedEntity))` | `verify(dep).save(any())` |
| `verify(dep, times(1))` | `verify(dep, atLeastOnce())` |

### Ruby — RSpec

| Strong | Weakened |
|---|---|
| `expect(x).to eq(42)` | `expect(x).to be_truthy` |
| `expect(a).to eq([1,2,3])` | `expect(a).to include(1)` |
| `expect { }.to raise_error(ArgError, /exact/)` | `expect { }.to raise_error` |
| `expect(dep).to have_received(:save).with(entity)` | `expect(dep).to have_received(:save)` |

### Rust

| Strong | Weakened |
|---|---|
| `assert_eq!(got, want)` | `assert!(got.is_some())` |
| `assert!(matches!(e, Error::OutOfRange { .. }))` | `assert!(result.is_err())` |
| `assert_eq!(v, vec![1,2,3])` | `assert!(!v.is_empty())` |

## Argument matchers are an assertion, and they have a lattice too

```
eq(exact value)  →  matcher with constraints  →  any()  →  no verification
```

`verify(repo).save(any())` proves a call happened. It does not prove the right
thing was saved — which is usually the entire behaviour under test.

## Tolerances

A tolerance is a claim about numerics, not a convenience. Write the tightest
one the arithmetic supports and state why:

```python
# IEEE-754 double accumulation over ≤1e4 terms: ~1e-12 worst case
assert abs(total - 1234.56) < 1e-9
```

Widening a tolerance by more than 2× to make a test pass is on the forbidden
list. If the value genuinely moved, the expectation changed — which is a spec
question, not a numerics question.

## Time, randomness and ordering

These are the honest reasons to sit below exact equality — and each has a
strengthening move that gets you back up the lattice:

| Nondeterminism | Weak habit | Strengthening move |
|---|---|---|
| Wall-clock | `assert elapsed < 10` | Inject a fake clock; assert the exact timestamp |
| Randomness | `assert 0 <= x <= 1` | Seed the RNG; assert the exact sequence |
| Map/set ordering | `assert set(a) == set(b)` | Legitimate — but assert the full set, not membership |
| Concurrency interleaving | `assert result is not None` | Assert the invariant that must hold under every interleaving |
| Generated IDs | `assert id` | Inject the generator; or assert the format exactly with `fullmatch` |

The rule: **make the system deterministic, then assert exactly.** Do not accept
nondeterminism you could have injected away.
