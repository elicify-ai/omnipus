# ADR-091 WP-G — Shared fixtures, test classification, cross-package suites

- **Decision record:** [ADR-091](../architecture/ADR-091-steered-sessions-replace-subagents.md) §10, §2.4
- **Landing order:** [adr-091-landing-order.md](adr-091-landing-order.md) — publishes I-7 first; runs alongside all packages
- **Owner files:** landing order §3, row G
- **Status:** Draft rev 2 (consolidated after three grills; supersedes every earlier sentence of rev 1)
- **Founder decision (round 4):** implementers write their own unit tests with the `test-driven-development` skill. This package owns only what is shared.
- **Founder decision (round 7):** the classification table is this package's first deliverable at CP-0, not a pre-grill task.

## Summary

Every package needs the same two things to prove its acceptance criteria: a real, store-backed delegation tree three levels deep, and a way to record everything that tries to reach a user so a test can assert "nothing did" — and prove the boundary was actually exercised. This package builds both first, decides what happens to the 49 existing test files that pin today's sub-agent behaviour, and writes the suites that only make sense across packages — the end-to-end reachability check above all.

## Existing codebase context

*Source inspection at `364290cb5`.*

| Item | Today | Change |
|---|---|---|
| Test helpers | `pkg/agent/testutil/` (scenario provider, `gateway_harness.go::RegisterGatewayRunner` injection pattern) | gains `DelegationTree`, `RecordingOutbound` |
| Existing mechanism tests | 47 files in the glob `pkg/agent/subturn*_test.go`, `pkg/tools/delegate*_test.go`, `pkg/tools/message_parent*_test.go`, plus the two containment tests below = **49** | classified retain / update / delete |
| Containment tests | `pkg/agent/system_turn_tool_output_test.go` (#766), `pkg/agent/async_child_publication_test.go` (#783) | assertions and controls **retained unchanged**; the second's setup constructs the deleted ring (`spawnPublicationTestChild` → `&ephemeralSessionStore{}`) and is moved to the persisted-store fixture by WP-A |
| In-package tools tests | `pkg/tools/delegate*_test.go` are `package tools` | those that need I-7 become **external-package tests** (`package tools_test`), because a `package tools` test importing `pkg/agent/testutil` recreates the import cycle |
| Cross-package suites | `tests/` | gains the ADR-091 E2E suite |
| CI vitest groups | `.github/workflows/pr.yml` | new SPA tests must match an existing group pattern (`scripts/check-vitest-coverage.mjs` is the tripwire) |

## Deliverables

### G-1 — `DelegationTree(t, deps steer.Deps, depth int) Tree` (I-7)

Builds R → A → B (→ C) with real sessions **through the injected I-2 launcher** (until WP-A publishes I-2 at CP-0, through the store directly with the same record shape), real lifecycle records with edges — including R's own `ordinary_root` record (round 9) — each session under a distinct agent profile, distinct workspaces available on request. The fixture holds only `pkg/steer` interfaces (`steer.Deps{Launcher, Canceller, Deliverer, Classifier, LifecycleStore, SessionStore, BootHook}`); every hook maps to a published operation: `Reenter(B)` → `Deliverer.Deliver` of a `handback` from B's child; `QueueWake(B, gen)` → `Deliver` with B's turn held; `Stop(node)` → `Canceller.CancelSubtree`; `Revive(node)` → `Canceller.Revive`; `CorruptRecord(node, invariant)` / `DeleteRecord(node)` / `DeleteEdge(node)` → the lifecycle store; `Crash()` → close every store without flushing; `Reboot()` → reopen and run `BootHook` (the same function `gateway_boot.go` runs).

### G-2 — `RecordingOutbound(t)` (I-7)

Implements `steer.BoundaryObserver` — every boundary calls `Observe(boundary, session, audience)` before acting on the audience decision, so "exercised and blocked" is distinguishable from "never exercised" — and wraps every sink in the boundary inventory (landing order §6): the bus (`PublishOutbound`, `PublishOutboundMedia`), `SendMedia`, the external-channel stream adapters' `Write`, the WS `send`, the `message` tool's send, the task result notification and the question-card broadcast. Provides `AssertNothingTo(address)`, `AssertReceived(session, kind)` (the control) and `AssertBoundaryInvoked(name)`.

### G-3 — Classification of existing tests

Every one of the 49 files gets a row: **retain** (protects behaviour ADR-091 keeps — target identity, ancestor authority, parked/respond transitions, cancellation of live descendants), **update** (asserts an identity source that changes — inherited address → edge; `ParentDurableKey` → edge; ring setup → real store), or **delete** (asserts wait-inline, the ring, per-site booleans, the sibling notifier). The row names the replacing test where one exists. The classification is the appendix of this spec, filled in by this package at CP-0 before any other package deletes a test.

### G-4 — Cross-package suites

| Suite | Asserts |
|---|---|
| `TestE2E_ThreeLevelDelegation_NoLeak` | AC-3 across all twelve boundaries with A, B and C, first run and re-entry; every boundary invoked; controls present |
| `TestE2E_StopReachesReenteredChild` | AC-8 through the real Stop surface |
| `TestE2E_StopSurvivesRestart` | AC-8, Q17: Stop, `Reboot()`, nothing runs; a newer instruction revives |
| `TestE2E_CompletionWakesPerChild` | D3 through the real notifier; `handback` per child |
| `TestE2E_Restart_ParentToldOnce` | §7: crash between persist and wake → one turn |
| `TestE2E_TaskChildInSidePanel` | a `create_task` child appears in the parent's transcript events and, via replay, in the row |
| Playwright `steered-session-reachability.spec.ts` | AC-12: side panel row with status → open → live stream → typed steer → parent chat gains nothing beyond the `delegate` line → reload → row restored |

## Acceptance criteria

1. **Given** any package's test, **When** it needs a tree or a recorder, **Then** I-7 provides it without that package building its own, and a `pkg/tools` test uses it from `package tools_test`.
2. **Given** the 49 files, **When** WP-A/B/C delete or update tests, **Then** each change cites a classification row.
3. **Given** the E2E suite, **When** run against the merged branch, **Then** AC-3, AC-8, AC-12 and §7 pass through real surfaces, not mocks.
4. **Given** a mocked provider that returns a verdict without tool calls, **When** the leak suite runs, **Then** the control assertion fails — proving the suite cannot pass vacuously.
5. **Given** the leak suite, **When** any one boundary is not exercised, **Then** `AssertBoundaryInvoked` fails for it.

## Machine-verifiable constraints

| Check | Expected |
|---|---|
| Fixture realism | `DelegationTree` sessions exist on disk under the test data dir with valid edges, and R has an `ordinary_root` record |
| Recorder coverage | every sink in the boundary inventory passes through the recorder in test builds; `AssertBoundaryInvoked` has one name per boundary |
| No import cycle | `go vet ./pkg/tools/...` and `go vet ./pkg/agent/testutil/...` pass with the fixture present |
| Classification complete | 49 rows, each retain/update/delete with a reason |
| Vacuity guard | the control assertion fails when the child made 0 tool calls; `AssertBoundaryInvoked` fails for a skipped boundary |
| CI grouping | every new vitest file matches a `pr.yml` group |

## BDD scenarios

```gherkin
Feature: Shared fixtures and cross-package suites

  # Happy Path — Traces to: AC 1
  Scenario: A tools test uses the tree from an external package
    Given a test in package tools_test
    When it builds DelegationTree(t, launcher, 3)
    Then R, A, B and C exist on disk with valid edges and R has an ordinary_root record

  # Error Path — Traces to: AC 4, 5
  Scenario: The leak suite cannot pass vacuously
    Given a provider stub that returns a verdict without tool calls
    When the leak suite runs
    Then the control assertion fails
    And AssertBoundaryInvoked fails for every boundary the child never reached

  # Happy Path — Traces to: AC 3
  Scenario: Stop survives a restart end to end
    Given a three-level tree and Stop pressed on R
    When Reboot() runs
    Then no session under R starts
    When a newer instruction is written into B
    Then B runs as a new generation

  # Error Path — Traces to: FR-G-004
  Scenario: A new SPA test outside every CI group is caught
    Given a new vitest file whose path matches no group in pr.yml
    When scripts/check-vitest-coverage.mjs runs
    Then it fails naming the file
```

## TDD plan

| Order | Test | Level | Traces to |
|---|---|---|---|
| 1 | `TestDelegationTree_BuildsValidEdges_AndRootRecord` | Unit | AC 1 |
| 2 | `TestDelegationTree_UsableFromToolsTest` (in `package tools_test`) | Unit | AC 1 |
| 3 | `TestRecordingOutbound_CapturesAllSinks` | Unit | AC 1 |
| 4 | `TestRecordingOutbound_ControlFailsWhenChildIdle` | Unit | AC 4 |
| 5 | `TestRecordingOutbound_BoundaryInvokedFailsWhenSkipped` | Unit | AC 5 |
| 6 | the six cross-package Go suites | E2E | AC 3 |
| 7 | Playwright reachability | E2E | AC 3 |

## Functional requirements

| ID | Requirement |
|---|---|
| FR-G-001 | I-7 MUST be published before any package asserts AC-3 or AC-8, and MUST be usable from `package tools_test` without an import cycle. |
| FR-G-002 | Every one of the 49 existing mechanism test files MUST be classified before any is deleted or updated. |
| FR-G-003 | Cross-package suites MUST exercise real surfaces, MUST include a vacuity control, and MUST assert every boundary was invoked. |
| FR-G-004 | New SPA tests MUST land in an existing CI vitest group. |

## Success criteria

| ID | Criterion |
|---|---|
| SC-G-1 | 49 / 49 rows classified. |
| SC-G-2 | E2E suites green on the merged branch; Playwright reachability recorded with a screenshot. |

## Traceability

| Requirement | Acceptance | Scenarios | Tests |
|---|---|---|---|
| FR-G-001 | AC 1 | tools test uses the tree | 1, 2, 3 |
| FR-G-002 | AC 2 | — (a table, checked by review at CP-0) | appendix |
| FR-G-003 | AC 3, 4, 5 | cannot pass vacuously; Stop survives restart | 4, 5, 6, 7 |
| FR-G-004 | *as a maintainer, I want every new SPA test to run in CI* | new test outside every group is caught | the `check-vitest-coverage.mjs` tripwire, exercised by scenario |

## Appendix — classification table (completed at CP-0)

| File | Class | Reason | Replaced by |
|---|---|---|---|
| `pkg/agent/subturn_target_identity_test.go` | retain | ADR-032 — child never inherits agent settings | — |
| `pkg/agent/system_turn_tool_output_test.go` | retain, unmodified | #766 containment | — |
| `pkg/agent/async_child_publication_test.go` | **update (setup only)** | #783 containment — assertions and controls unchanged; `spawnPublicationTestChild` constructs `&ephemeralSessionStore{}`, which D1/D10 delete, so WP-A moves the setup to the persisted-store fixture | — |
| *(remaining 46 rows — 47 glob files plus these two containment tests make 49 — filled by this package at CP-0)* | | | |

## Definition of done

1. *Code correct and tested:* fixtures, recorder and suites green; classification complete.
2. *Reachable:* the Playwright reachability spec is the product's AC-12 proof, executed and recorded.
