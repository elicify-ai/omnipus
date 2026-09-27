# Adversarial Review Report Template

This is a template asset inside the `grill-spec` skill directory -- the
findings-report structure the skill fills in when it grills a spec or ADR.
It is not a summary of work done in this session; it is source content the
skill consumes at runtime.

Use this template when assembling the findings report in Phase 4 of
`SKILL.md`.

Last reviewed: 2026-09-25

---

```markdown
# Adversarial Review: [Spec or ADR name]

**Document reviewed**: [path/to/document.md]
**Mode**: [Spec | ADR]
**Round**: [Round 1 of 2 | Round 2 of 2 (final) | ADR grill (fixed, one round)]
**Review date**: [YYYY-MM-DD]
**Verdict**: [BLOCK | REVISE | PASS]

## Executive Summary

[2-3 sentences. State the total findings by severity and the overall verdict.
Be direct -- no softening language. State plainly whether frontend coverage
(lenses 5, 8, 9, 10) was as thorough as backend coverage, or name the gap.]

| Severity | Count |
|----------|-------|
| CRITICAL | N |
| MAJOR | N |
| MINOR | N |
| OBSERVATION | N |
| **Total** | **N** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] [Short title]

- **Lens**: [Ambiguity | Incompleteness | Inconsistency & ADR/AS-IS contradiction |
  Infeasibility | Contract-first gaps | Security | Reachability |
  UI states & journey gaps | Accessibility & keyboard |
  Design-system reuse & brand | Testability & false-green risk | Overcomplexity]
- **Affected section**: [Specific heading, requirement ID, or scenario name -- never a line number]
- **Failure scenario**: [The concrete way this goes wrong if shipped as-is --
  not an abstract risk. Name the trigger and the observable consequence.]
- **Evidence**: [The exact text from the document, or the repo fact that
  contradicts it. Code claims cite `file::symbol`, never `file:line`.]
- **Recommendation**: [Exactly what to change. Provide rewritten text if possible.]

---

#### [CRIT-002] [Short title]

[Same structure as above]

---

### MAJOR Findings

#### [MAJ-001] [Short title]

- **Lens**: [Lens name]
- **Affected section**: [Specific reference]
- **Failure scenario**: [Concrete consequence]
- **Evidence**: [Text or repo fact]
- **Recommendation**: [Specific fix]

---

### MINOR Findings

#### [MIN-001] [Short title]

- **Lens**: [Lens name]
- **Affected section**: [Specific reference]
- **Failure scenario**: [What quality issue results]
- **Recommendation**: [Specific fix]

---

### Observations

#### [OBS-001] [Short title]

- **Lens**: [Lens name]
- **Affected section**: [Specific reference]
- **Suggestion**: [Improvement idea]

---

## Structural Integrity

<!-- Choose the variant matching the mode. -->

### Variant A: Spec mode (plan-spec output)

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` field present, valid value | PASS/FAIL | |
| ADR linked (or explicitly stated not needed) | PASS/FAIL | |
| Contract changes stated first, citing `contracts/` | PASS/FAIL | |
| API and data section | PASS/FAIL | |
| UI screens and states (loading/empty/error/partial) | PASS/FAIL | |
| User journey section | PASS/FAIL | |
| Accessibility and keyboard section | PASS/FAIL | |
| Design-system components, catalogue-first | PASS/FAIL | |
| Security and user promises section (when touched) | PASS/FAIL | |
| BDD acceptance scenarios, oracle from spec | PASS/FAIL | |
| Traceability table: requirement -> scenario -> test | PASS/FAIL | |
| Reachability section (tool policy / screen wiring) | PASS/FAIL | |

### Variant B: ADR mode

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` / `Date:` / `Deciders:` header | PASS/FAIL | |
| `## Context` | PASS/FAIL | |
| `## Decision` | PASS/FAIL | |
| `## Consequences` | PASS/FAIL | |
| Related ADRs cited by title | PASS/FAIL | |
| States the open design decision it closes | PASS/FAIL | |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| [e.g., Concurrency] | [What's missing] | [Which scenarios need it] |
| [e.g., Frontend component states] | [What's missing] | [Which screens need it] |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| [e.g., Email Inputs] | [e.g., Unicode local-part] | [Specific test case to add] |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| [Component 1] | [risk/ok] | [risk/ok] | [risk/ok] | [risk/ok] | [risk/ok] | [risk/ok] | [Key concern] |
| [Component 2] | [risk/ok] | [risk/ok] | [risk/ok] | [risk/ok] | [risk/ok] | [risk/ok] | [Key concern] |

**Legend**: risk = identified threat not mitigated in the document, ok = adequately addressed or not applicable

---

## Reachability Check

| Question | Answer | Evidence |
|----------|--------|----------|
| Agent-facing tool: registered + policy entry for every agent? | Yes/No/N-A | `grep -rl '"<tool_name>"' pkg/coreagent/ pkg/config/ pkg/tools/` result |
| User-facing: named screen/component renders it? | Yes/No/N-A | Component path, or "none named" |
| Test plan describes execution, not just authorship? | Yes/No | |

---

## Unasked Questions

<!-- Genuine gaps the document should have answered. Distinct from
     "Questions for the founder" below: these are prompts for the document's
     author; the founder list is for team-lead's interview. -->

1. [Question about missing requirement or undecided design choice]
2. [Question about unclear failure handling]
3. [Question about missing integration concern]
4. [...]

---

## Questions for the founder

<!-- Separate from findings and from "Unasked Questions" above. Points that
     are unclear or need a founder decision, for team-lead to turn into an
     interview BEFORE the fix round (founder decision #4). Never merge this
     list into the findings table. -->

1. [Open point needing a founder decision]
2. [...]

---

## Verdict Rationale

[1-2 paragraphs explaining the verdict. Reference the most impactful findings
by ID. State clearly what must change before implementation -- or the next
grill round -- can proceed.]

### Escalation to the founder

<!-- ONLY present in: spec mode round 2, or the ADR mode correction round --
     i.e. after the fixed rounds are exhausted. Every remaining
     CRITICAL/blocking finding after the fix round goes here for the
     founder's decision. This never triggers another grill round. Omit this
     section entirely in spec mode round 1 and in the ADR's single grill
     round. -->

| Finding ID | Why it's still open | Founder decision needed |
|---|---|---|
| [CRIT-00N] | [What the fix round didn't resolve, or couldn't] | [The specific choice the founder must make] |

### Recommended Next Actions

- [ ] [Specific action item -- reference finding ID]
- [ ] [Specific action item -- reference finding ID]
- [ ] [Specific action item -- reference finding ID]

### Next step in the process

<!-- Use the exact wording from SKILL.md's "Output" section for the
     matching mode/round -- never recommend /taskify. -->
```
