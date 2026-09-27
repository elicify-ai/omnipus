# Spec Output Template

Structural template for `docs/internal/specs/<name>-spec.md`. Fill in every section.
Replace all `[bracketed placeholders]` with actual content. A section marked
"remove if not applicable" may be dropped, but only with an explicit one-line reason
in its place in the spec's own Assumptions section — never a silent deletion. Remove
this header block itself from the final output.

The section order below is fixed by `plan-spec/SKILL.md` and the S1 lane brief:
contract changes first, then backend and frontend covered equally, BDD scenarios with
oracles from this spec, a traceability table, and Reachability.

---

`Status: Draft`

`ADR: [ADR-NNN — Title](../architecture/ADR-NNN-title.md)` — or, if none:
`ADR: none — no open design decision`

# Feature Specification: [Feature Name]

**Created**: [YYYY-MM-DD]
**Input**: interview-me output at `[path]`; ADR at `[path]` (if any)

---

## Overview

[What this feature is, the problem it solves, who it is for — one to three
paragraphs, plain English. This is the section a non-engineer founder reads first.]

## Existing Codebase Context

> Populated by GitNexus (`query`, `context`, `impact`, `trace`). Remove this section
> only if the index is genuinely stale/empty for the relevant area — say so, do not
> just omit it.

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| [name] | [calls / modifies / extends] | [summary from `context`] |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|----------------|------------|----------------|----------------|
| [name] | [LOW/MEDIUM/HIGH/CRITICAL] | [list] | [list] |

Flag any HIGH/CRITICAL row prominently — it needs sign-off before GREEN starts, not a
silent note.

---

## Contract Changes (contract-first — Hard Constraint #8)

> Every byte crossing the gateway/SPA boundary starts here. Remove this section only
> for a feature that adds no wire-format surface (rare) — say so explicitly.

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| [TypeName] | [new/changed] | `contracts/components/schemas/[TypeName].yaml` | [what it carries, referenced from openapi.yaml/asyncapi.yaml] |

State explicitly: which existing schemas this feature extends, and which it must not
collide with. The generated types (`pkg/api/generated/`, `src/lib/api/generated/`) are
the only legal cross-boundary types once these are regenerated — this spec proposes the
schema, it does not hand-write the generated code.

## API and Data

[Endpoints/events touched or added — described by contract reference, not by
hand-written shapes; data model changes at the behavioural level: what is stored, not
how (no `CREATE TABLE`, no struct definitions — see the phrasing guardrail in
`plan-spec/SKILL.md`).]

---

## User Stories & Acceptance Criteria

### User Story 1 — [Title] (Priority: P[0-4])

[Narrative: a [role/actor] wants to [action] so that [benefit]. Current pain point and
how this story addresses it.]

**Why this priority**: [justification relative to other stories]

**Independent Test**: [how this story is verified in isolation]

**Acceptance Scenarios**:

1. **Given** [precondition], **When** [action], **Then** [expected outcome].
2. **Given** [precondition], **When** [action], **Then** [expected outcome].

### User Story 2 — [Title] (Priority: P[0-4])

[Repeat per story. Backend and frontend stories are both first-class — do not group
frontend work as an afterthought under a single backend story.]

---

## UI Screens and States

> Remove only for a feature with no UI surface — say so explicitly.

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| [screen name] | [what renders] | [what renders] | [what renders] | [what renders] | [what renders] |

## User Journey

[Step-by-step walkthrough from the user's perspective: entry point, each screen/action
in order, exit/completion. Cross-reference the acceptance scenarios above by number
rather than restating them.]

## Accessibility and Keyboard

[Keyboard operability (tab order, focus-visible ownership — `omnipus-design-system`
skill), screen-reader labels/roles for any new control, touch-target sizing on narrow
screens, and any zoom behaviour per the design-system definition's touch/zoom rules
(`docs/internal/design/design-system-definition.md`).]

## Design-System Components

[For each new UI element: which catalogued component (`design-system/catalog.json`)
it uses, checked BEFORE proposing anything new. If a genuinely new component is
needed, say so and name the classification it would need (primitive/composite) per
`src/components/ui/CLAUDE.md` — this spec proposes it, it does not build it.]

## Security and User Promises

[Anything touching tool access, approval flows, or shell rules against
`docs/security.md` / `docs/tools.md`; anything touching a focus-area package
(`pkg/auth`, `pkg/credentials`, `pkg/fspolicy`, `pkg/identity`, `pkg/pairing`,
`pkg/pathsafe`, `pkg/shellrule`, `pkg/security`, `pkg/sandbox`, `pkg/audit`,
`pkg/policy`, gateway rate limiting, gateway auth) flagged for security-lead's
on-demand review regardless of change size.]

---

## Behavioral Contract

Primary flows:
- When [condition], the system [behavior].

Error flows:
- When [error condition], the system [behavior].

Boundary conditions:
- When [boundary condition], the system [behavior].

## Edge Cases

- [What happens when [unusual condition]? Expected: [behaviour].]
- [What happens when [boundary condition]? Expected: [behaviour].]

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not [behavior] because [reason].
- [Behaviors an agent might "helpfully" add beyond scope.]
- [Scope boundaries; security/safety boundaries.]

### Machine-Verifiable Constraints

> Include only categories relevant to this feature.

**Error Codes / Messages**: When [boundary violation], the system MUST return
[status/shape — cite the contract schema] with `[exact message or format]`.

**Performance Bounds**: [Metric] MUST be [operator] [threshold] [unit] at [condition].

**Scope Boundaries**: The system MUST NOT [extend to / accept / process] [boundary]
because [reason].

## Integration Boundaries

### [External System or Internal Boundary Name]

- **Data in / out**: [what flows each way]
- **Contract**: [cite the schema file / protocol / auth]
- **On failure**: [behavior when unavailable or erroring]
- **Development**: [real service | mock/simulated twin] — [reason]

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|------------------|------------------------|---------------------|
| 1 | [gap]    | [what an agent would do]  | [question] |

---

## BDD Scenarios

### Feature: [Feature Name]

#### Scenario: [Descriptive Scenario Title]

**Traces to**: User Story [N], Acceptance Scenario [M]
**Category**: [Happy Path | Alternate Path | Error Path | Edge Case]

- **Given** [precondition]
- **When** [action]
- **Then** [expected outcome]

[Repeat for all scenarios, grouped by user story. Format/rules: `bdd-template.md`.]

---

## Test-Driven Development Plan

### Test Hierarchy

| Level       | Scope                         | Purpose                                     |
|-------------|--------------------------------|----------------------------------------------|
| Unit        | [functions/methods]           | [logic in isolation]                        |
| Integration | [module interactions]         | [components work together]                  |
| E2E         | [full user workflows]         | [complete feature from user view]           |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|-------------------------|--------------|
| 1     | [name — placeholder, qa-lead names the real test in RED] | Unit | Scenario: [title] | [what it verifies] |

### Test Datasets

Construction reference: `test-dataset-template.md`.

#### Dataset: [Context]

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | [value] | [type] | [expected] | BDD Scenario: [title] | [note] |

### Regression Test Requirements

**If modifying existing functionality:**

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|----------------|------------------------------|-------|
| [behaviour]        | [test name]    | [Yes/No — name if yes]      | [why] |

**If new functionality:** No regression impact — new capability. Integration seams
protected by: [existing tests covering the boundary, if any].

---

## Functional Requirements

- **FR-001**: System MUST [requirement].
- **FR-002**: System SHOULD [requirement].
- **FR-003**: System MAY [requirement].

## Success Criteria

- **SC-001**: [measurable outcome, numeric threshold or clear pass/fail].
- **SC-002**: [observable pass/fail condition].

---

## Reachability

> Definition of Done (root `CLAUDE.md`): a feature is not done until a real user or
> agent can invoke it. State this concretely, not "TBD".

- **Tool registration**: [new tool name, or "none — no new tool"]. Policy entry lives
  in: [exact config file(s) — Hard Constraint #6 requires an explicit policy entry per
  agent].
- **Screen**: [which screen/component renders this feature, or "none — backend-only;
  justified because…"].
- **Test plan execution**: [where in RED/GREEN/CHECK this gets run, not merely written].

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s)          | Test Name(s)            |
|-------------|-----------|---------------------------|--------------------------|
| FR-001      | US-1      | Scenario: [title]         | [test_name]              |

**Completeness check**: every FR-xxx has at least one BDD scenario and one test; every
BDD scenario appears at least once.

---

## Assumptions

- [Assumption about environment, dependencies, user behaviour, or infrastructure.]
- [Any section removed above, and why.]

## Clarifications

### [YYYY-MM-DD]

- Q: [question raised while writing this spec] -> A: [answer/decision, and its source
  — interview-me's Decisions Log ID, the ADR, or a direct founder answer].
