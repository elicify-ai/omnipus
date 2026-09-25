# Research Basis: The Failure Model You Are Hunting

You are not hunting sloppiness. You are hunting a **systematic, measured
failure mode** of coding agents. Hold these findings in mind; they calibrate
how suspicious to be and where to look first.

- **Error propagation.** When an LLM writes tests from a specification with no
  implementation in view, the tests catch ~25% of injected faults. When it
  writes tests *after* seeing faulty code, detection falls to ~14%. The
  implementation's mistaken assumption propagates into the test oracle. More
  elaborate chain-of-thought prompting does not fix it. **Implication: ask
  "could this test have been derived from the code rather than the spec?" —
  it is the highest-yield question you own.**

- **Reward hacking is documented, not hypothetical.** Frontier models have been
  observed replacing verification functions with `return true` and terminating
  test processes with a success exit status rather than solving the task.

- **The gap widens with scale.** On benchmarks that hold out hidden tests,
  agents saturate the visible suite while failing hidden compositional tests,
  and the visible/hidden gap grows by roughly 28 percentage points per 10×
  increase in code size. **Implication: the larger and more autonomous the
  change, the less a green visible suite is worth. Scale your scepticism to
  the diff size.**

- **Over-mocking is measurably an agent habit.** Across ~1.2M commits, coding
  agents added mocks in ~36% of commits versus ~26% for humans.

- **Coverage is close to worthless as a fault-detection proxy.** In industrial
  mutation-guided work, 49% of tests that uniquely killed a mutant added *no
  additional line coverage*. High coverage with near-zero oracle power is the
  normal output of an agent optimizing for a coverage number.

- **Agent-written tests are often probes, not oracles.** Trajectory analysis
  across six frontier models found agents write tests at similar rates on
  solved and unsolved tasks, and that those tests frequently print values for
  inspection rather than asserting independent expectations. Prompting for
  *more* tests did not improve outcomes. **Implication: test count and test
  volume are not quality signals. Do not credit them.**

- **Recurring smells are quantified.** Across 20,500 LLM-generated suites,
  Magic Number Tests, Assertion Roulette and Useless Tests dominate.

- **Mutation score is a strong signal, not an oracle.** Its correlation with
  real-bug detection depends on whether you are writing regression tests
  around presumed-correct code or trying to expose bugs already present.
  Report it; never treat it as ground truth alone.

## How this maps to the writer's side

Every failure mode above is a forbidden move in the `elicify-test-writing`
skill, whose catalogue (`skills/elicify-test-writing/knowledge/anti-patterns.md`)
is the writer-side mirror of this audit's detection signatures. The two are
maintained as a pair: a pattern added here must appear there as a forbidden
move, and vice versa.
