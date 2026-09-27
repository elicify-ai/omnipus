# Oracle Power Assessment

Phase 1 asks "was something weakened?" This phase asks the harder question:
**"was there ever any oracle power to weaken?"** A suite can be untouched,
honest, and still worthless.

## Per-test measures

Compute and report:

```
tests with zero assertions                  ← the headline number
assertions per test (median, min)
distinct expected values (magic-number density)
mocks per test
setup lines vs verification lines ratio
duplicate assertion structures (copy-paste suites)
negative-case ratio   (error paths / total)
boundary-case ratio   (edges / total)
branch diversity      (do the inputs reach different branches?)
```

## Named smells

- **Useless Test** — executes code, asserts nothing meaningful. The single most
  common LLM output.
- **Assertion Roulette** — many unlabelled assertions in one test; a failure
  cannot be localized.
- **Magic Number Test** — unexplained literals with no derivation from the spec.
- **Long Test** — a test doing so much that its intent is unrecoverable.
- **Probe, not oracle** — the test prints, logs or inspects rather than asserts.
  This is the specific pathology of agent-written tests. Look for `print(`,
  `console.log(`, `t.Log(` in a test whose assertions are absent or trivial.

## The oracle-independence question

For the changed tests, answer directly and put the answer in the report:

> **Could every expected value in this test have been read off the
> implementation rather than derived from a specification?**

If yes for most assertions, mark **oracle independence: NOT ESTABLISHED**. Cite
the ~25%→~14% error-propagation finding (`knowledge/research-basis.md`). This is
a headline finding, not a footnote — it is the difference between a test suite
and a mirror.

The writer-side counterpart is the law of oracle independence in the
`elicify-test-writing` skill: expected values come from the specification,
never from the implementation. An audit finding of NOT ESTABLISHED means that
law was broken at writing time.
