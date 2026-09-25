# BDD Scenario Reference

Rules and format for writing Behaviour-Driven Development scenarios in
Given-When-Then notation. Follow these rules when producing BDD scenarios
for a spec written by plan-spec.

## Scenario Format

```
#### Scenario: [Descriptive, action-oriented title]

**Traces to**: User Story [N], Acceptance Scenario [M]
**Category**: [Happy Path | Alternate Path | Error Path | Edge Case]

- **Given** [a precondition that establishes the starting state]
- **And** [an additional precondition, if needed]
- **When** [a single action performed by the actor]
- **Then** [an observable, verifiable outcome]
- **And** [an additional assertion, if needed]
- **But** [something that should NOT happen, if relevant]
```

## Mandatory Rules

1. **Traceability is non-negotiable.** Every scenario MUST have a `Traces to:`
   line that references its parent User Story number AND the specific
   Acceptance Scenario number it elaborates.

2. **One action per When.** The When step describes exactly one action. If a
   workflow involves multiple steps, either split into separate scenarios or
   use And steps under When only for tightly coupled sub-actions that are
   meaningless in isolation.

3. **Category every scenario.** Assign exactly one category:
   - **Happy Path**: The main success flow. The user does the expected thing
     and gets the expected result.
   - **Alternate Path**: A valid but non-default path through the feature.
     Still succeeds, but through a different route.
   - **Error Path**: The user or system encounters an error. The scenario
     verifies correct error handling, messaging, and recovery.
   - **Edge Case**: Boundary conditions, unusual inputs, race conditions,
     or rare-but-possible situations.

4. **Assertions must be observable.** Then steps describe externally visible
   outcomes (UI messages, API responses, state changes, file outputs).
   Never assert on internal implementation details.

5. **Preconditions must be concrete.** Given steps should describe a specific,
   reproducible state — not vague conditions like "the system is ready."

6. **Expected values come from the spec, not from any implementation.** The
   same oracle-independence law `elicify-test-writing` applies to code-level
   tests applies here: if a Then value can only be produced by running
   existing code and reading its output, it has no oracle power. Derive it
   from this spec's own Behavioral Contract, the cited contract schema, or
   the ADR — never from source you have not cited.

## Parameterised Scenarios (Scenario Outlines)

When the same logic applies to multiple input values, use a Scenario Outline
with an Examples table:

```
#### Scenario Outline: [Title describing the parameterised behaviour]

**Traces to**: User Story [N], Acceptance Scenario [M]
**Category**: [Category]

- **Given** [precondition with `<placeholder>`]
- **When** [action with `<placeholder>`]
- **Then** [outcome with `<placeholder>`]

**Examples**:

| placeholder_1 | placeholder_2 | expected_outcome |
|---------------|---------------|------------------|
| value_a       | value_b       | result_a         |
| value_c       | value_d       | result_b         |
```

Use Scenario Outlines when:
- Testing the same rule with different valid inputs
- Verifying multiple boundary values
- Covering a matrix of input combinations

Each row in the Examples table becomes an independent test case.

## Background (Shared Preconditions)

When multiple scenarios within the same feature share identical Given steps,
extract them into a Background block:

```
#### Background

- **Given** [shared precondition for all scenarios in this feature]
- **And** [another shared precondition]
```

Place the Background before the first scenario. It runs before each scenario
in the feature. Use sparingly — if only some scenarios share the precondition,
keep it in those individual scenarios.

## Anti-Patterns to Avoid

1. **Implementation leakage**: "Given the database has a users table with
   columns id, name, email" — this couples the scenario to a specific schema.
   Instead: "Given a registered user with email alice@example.com." A
   contract-shaped precondition ("Given a request matching the
   `AgentCreateRequestMain` schema") is fine — that is the spec's own cited
   contract, not an implementation detail.

2. **Missing assertions**: A scenario with When but no Then is incomplete.
   Every scenario must assert at least one outcome.

3. **Overly broad scenarios**: "Given a user, When they use the system,
   Then it works" — too vague to test. Be specific about inputs and outputs.

4. **Testing implementation, not behaviour**: "Then the function returns
   an array of length 3" — this tests internal structure. Instead: "Then
   3 results are displayed."

5. **Compound When steps**: "When the user logs in and navigates to settings
   and changes their password" — this is three actions. Split into three
   scenarios or use a focused scenario for the specific behaviour.

6. **Orphan scenarios**: A scenario without `Traces to:` is disconnected
   from requirements. Every scenario must trace to a user story.

## Naming Conventions

- Scenario titles should be descriptive and action-oriented.
- Use the actor's perspective: "User resets password with valid token"
  not "Password reset token validation."
- Include the key differentiator: "User submits form with empty required field"
  not just "Form validation."
- For error paths, name the error: "User receives rate limit error after
  10 requests in 1 minute."

## Coverage Checklist

For each user story, verify:

- [ ] At least one Happy Path scenario
- [ ] At least one Error Path scenario
- [ ] Alternate paths for any conditional logic
- [ ] Edge Case scenarios for each boundary condition listed in the spec
- [ ] All acceptance scenarios are covered by at least one BDD scenario
- [ ] Frontend-facing scenarios cover loading, empty, error, and partial states,
      not only the happy-path render
