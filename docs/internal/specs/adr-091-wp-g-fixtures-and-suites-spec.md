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

Verified by reading every one of the 49 files' test functions against the ADR text (not from filenames or grep alone). Two recurring, file-spanning findings drive most of the corrections below and are stated once here rather than repeated in every row:

- **The in-memory ring.** Any test whose fixture constructs `session:` as `&ephemeralSessionStore{}` or `newEphemeralSession(...)` is touching the exact type D1 deletes ("The ephemeral ring is deleted. A steered session has one memory."). Where the ring is merely incidental scaffolding for an unrelated assertion, this is `update` (move the fixture to the real store); where the test's own subject is the ring's behaviour or its isolation from the parent, it is `delete`.
- **`SubTurnConfig.Async` / the `async` argument / `executeSync`.** D4 deletes wait-inline outright ("`delegate` `async:false` and `delegate_run.go::executeSync` are deleted") and D10 deletes the `SubTurnConfig.Async` field itself ("always true once `executeSync` is gone — and every `false` branch"). A test whose own subject is the sync/await dispatch path (its name says "Sync", or its docstring frames it as the `async=false` counterpart of an async sibling) is `delete`. A test that merely passes `Async:`/`"async":` as an incidental driver, or that needs the field removed from a struct literal to keep compiling, is `update`.

Three further single-purpose findings, each affecting several files:

- **`ParentDurableKey`.** D2: "`ParentDurableKey` is removed in the same delivery — no alias period… 15 non-test files, not three" move to the edge, naming `tools/delegate.go::verifyCallerOwnsSession`, `delegate_status.go`, `delegate_park.go`, `delegate_followup.go`, `delegate_run.go` explicitly. Any test that constructs `session.LifecycleRecord{ParentDurableKey: …}` or asserts on that field is `update`: the ancestor-walk/ownership/inbox-keying behaviour it protects is explicitly kept (D5: "any ancestor may act… This matches today's ancestor walk"), but the field moves to `SteeredBy.SteeringSessionID`.
- **Streaming-sourced `status`.** D5/Q22 closes #614 by ruling status must answer "from the record and inbox… never from streaming progress." Tests whose subject is a *live* activity/progress snapshot read from the child's own transcript or from `DelegateProgressReader`/`AgentLoop.ProgressForSession` (not the lifecycle record or inbox) assert exactly the mechanism being deleted — `delete`.
- **Nested rendering of a child's steps in the parent's chat.** D7/D10 delete `replay.go::emitNestedToolCalls` and the `parentSpawnCallID`-driven nesting/shadow-stream suppression ("no child steps in the parent chat… `emitNestedToolCalls`… become dead and are deleted"); a child's own status now lives in the side panel, not folded into the parent's transcript. A test whose subject is that nesting/suppression mechanism is `delete`.

| File | Class | Reason | Replaced by |
|---|---|---|---|
| `pkg/agent/subturn_target_identity_test.go` | **update** | All 9 functions (`TestSpawnSubTurn_TargetIdentity_DispatchesExternalCLIFromTargetConfig`, `_PropagatesFullTargetConfig`, `TestSpawnSubTurn_NativeDispatch_AdoptsFullTargetIdentityIncludingModel`, `_AdoptsTargetToolPolicy`, `_AdoptsTargetWorkspaceForFileTools`, `TestSpawnSubTurn_ProviderPoolCopiedFromParent`, `TestSpawnSubTurn_TargetIdentity_ConcurrentModelSwitchRace`, `_UnresolvedTargetAborts`, `_SelfDelegationNoFallbackWarning`) construct the parent turnState with `session: &ephemeralSessionStore{}` (D1) and call `spawnSubTurn` with `Async: false` (D4/D10) — every one needs its setup moved to the real store and off the deleted sync path. The ADR-032 identity assertions themselves (never inherit agent settings from the parent) are unchanged and explicitly out of scope (§5: "ADR-032 is unchanged") — this was wrongly blanket-marked `retain` for the whole 1387-line file, missing that every function touches both deleted mechanisms | `pkg/agent/CLAUDE.md`'s own regression pointer for this file stands; setup moves to WP-A's persisted-store fixture |
| `pkg/agent/system_turn_tool_output_test.go` | retain, unmodified | #766 containment; touches neither the ring nor `Async` — confirmed by reading, matches the spec's own pre-classification | — |
| `pkg/agent/async_child_publication_test.go` | **update (setup only)** | #783 containment — assertions and controls unchanged; `spawnPublicationTestChild` constructs `&ephemeralSessionStore{}`, which D1/D10 delete, so WP-A moves the setup to the persisted-store fixture | — |
| `pkg/agent/subturn_adr057_test.go` | retain | Tests parent-child edge creation (`ParentSessionID`, `ChildCount`, `Owner` copy, `routingSessionID` inheritance) via `spawnSubTurn`; no ring, no `Async` field set. D2 explicitly keeps these as facts derived from the edge ("`UnifiedMeta.ParentSessionID` = `SteeringSessionID`; `UnifiedMeta.Owner` copied") and D8 keeps `routingSessionID` "as a cache derived from the edge's verified root" | — |
| `pkg/agent/subturn_ask_deny_test.go` | **update** | `askDenyFixture` constructs the parent with `session: &ephemeralSessionStore{}` (D1) and calls `spawnSubTurn` with `Async: false` (D4) to get a synchronous result back directly — that flow is gone; the fixture must dispatch async and observe the ask-tool denial/approval count via the child's completion instead. `AutoDenyAsq` propagation to a headless delegated child is an unrelated, kept concern | — |
| `pkg/agent/subturn_awaited_cancel_test.go` | **delete** | `TestSpawnSubTurn_AwaitedSyncCancel_CascadesAndRecordsDescendants` drives `spawnSubTurn` with the exact `SubTurnConfig{Async: false}` `DelegateTool.executeSync` uses — wait-inline/await delegation, D4 | — |
| `pkg/agent/subturn_cancel_browser_test.go` | retain | Both functions hand-construct `turnState` literals directly (no ring, no `Async`, no `ParentDurableKey`) and assert `routingSessionID`-based reachability for a live in-flight cancel via `al.resolveInterruptAnchors`/`InterruptSessionHard` — D8 explicitly keeps `routingSessionID` "as a cache derived from the edge's verified root on every entry" for exactly this live-turn-reachability purpose | — |
| `pkg/agent/subturn_cancel_status_test.go` | **update** | Both functions construct the parent with `session: newEphemeralSession(nil)` (D1) and `SubTurnConfig{Async: true, Critical: true}` (the `Async` field itself is deleted, D10) while driving a real hard-abort/`RequestCancel` cascade — the Interrupted/Cancelled status-and-reason recording behaviour under test is unrelated to any deletion and stays valid | — |
| `pkg/agent/subturn_delegate_nesting_test.go` | split | `TestNestedDelegate_Await` **delete**: the outer hop AND ray's own nested `delegate` call both use `async:false` as their entire subject — D4 makes the scenario impossible (rejected by argument validation, never reaching the trust-graph gate this file exists to test). `TestNestedDelegate_Background` **update**: `buildParentTurnState` uses `session: &ephemeralSessionStore{}` (D1) and its own outer hop is *also* driven via `SubTurnConfig{Async: false}` to get a synchronous return — restructuring to the async-only dispatch model means polling for the first hop too, not just the nested one; the underlying nested-delegation-permission fix (D5 keeps nested delegation) stays valid. `TestNestedDelegate_TrustGraphDenialDoesNotPoisonSubsequentCalls` **update**: both of ray's inner `delegate` calls (to "outsider" and to "planner") use `"async": false`, which D4 now rejects at argument validation *before* reaching the trust-graph gate this test exists to prove doesn't leak — the scripted arguments must become `"async": true` and the ADR-058 quarantine-boundary concern re-verified against that path | — |
| `pkg/agent/subturn_external_cancel_test.go` | retain | All 4 functions (the file has a 4th, `TestExternalCLISubTurn_RequestCancelEndToEnd_ExternalCLIOnlySession`, that the draft omitted entirely). None constructs the ring, `Async`, or `ParentDurableKey`; all drive `runExternalCLISubTurn` directly or via `RequestCancel`/`InterruptSessionHard`/`Interrupt`. D10 explicitly preserves the runner intact ("The runner stays… preserved intact, with one caller"), and this is a property of that preserved runner's own cancellation wiring, not of the deleted wait-inline delegate mode — `CancelPropagates_Sync`'s docstring frames it as "modeling `delegate(async=false)`" but nothing in its code depends on that mode existing; it is a goroutine directly blocked in the runner, exactly as `CancelPropagates_Async`'s is, just organized to block the caller instead of a detached goroutine | — |
| `pkg/agent/subturn_followup_resume_test.go` | **update** | All 3 functions construct the parent with `session: &ephemeralSessionStore{}` (D1) and drive follow-up/resume via `SubTurnConfig.IsResume`+`DelegateSessionID` directly against `spawnSubTurn` — D2/I-1 keep "a follow-up on a terminal session… bumps the generation first, exactly as today, and then dispatches," but the mechanism moves to `Dispatch`'s generation-aware reservation (I-6); transcript continuity and parent-edge preservation on resume are the kept behavioural intent | — |
| `pkg/agent/subturn_identity_test.go` | retain | Tests `resolveRequestedSkillForChild`/`resolveDelegateSoul`/`composeDelegateInput` directly — no session store, no ring, no `Async`, no edge. ADR-032 (never inherit agent settings) is explicitly unchanged | — |
| `pkg/agent/subturn_key_reuse_race_test.go` | **update** | Both functions directly exercise `al.registerActiveTurn`/`al.clearActiveTurn`/`al.clearActiveTurnStateEntry` — the exact "plain map store" D10 replaces ("`turn.go::registerActiveTurn`'s plain store, replaced by the compare-and-set registration"); the generation-reuse race this file guards against (a stale generation's cleanup erasing a live reused-key generation) is if anything sharpened by D8's generation-aware model, but the primitives under direct test change to `registerTurnIfAbsent` | — |
| `pkg/agent/subturn_liveness_test.go` | split | 6 functions **retain** (`TestIsSubTurnActiveForSpawnCall_LiveMatch_ReturnsTrue`, `_FinishedAndPersisted_ReturnsFalse`, `_FinishedButNotYetPersisted_ReturnsTrue`, `_NoMatch_ReturnsFalse`, `_EmptyID_ReturnsFalse`, `_MultipleTurns_OnlyMatchingOneCounts`): all hand-construct minimal `turnState`s and touch neither the ring nor `Async`. `TestIsSubTurnActiveForSpawnCall_RealRetryWindow_StaysActiveUntilPersisted` **update**: its parent uses `session: &ephemeralSessionStore{}` (D1) and `SubTurnConfig{Async: true}` (field deleted, D10) to drive a real `spawnSubTurn` — the draft blanket-retained the whole file, missing this one real-dispatch function | — |
| `pkg/agent/subturn_rc5b_legacy_store_test.go` | **update** | Constructs the parent with `session: &ephemeralSessionStore{}` (D1); tests that the delegated-task transcript write lands in the store that actually owns the child id (not a stale `parentTS.transcriptStore`) — a store-identity-mismatch risk that stays real, arguably more so, once every steered session gets its identity from the launcher/edge rather than the caller's own field | — |
| `pkg/agent/subturn_rc5b_transcript_task_test.go` | **update** | Same as above: `session: &ephemeralSessionStore{}` (D1) in the parent fixture; the child-transcript-persistence assertion itself is a real-store concern D1 keeps | — |
| `pkg/agent/subturn_rc8_dead_override_test.go` | **update** | `TestSpawnSubTurn_NativeDispatch_SystemPromptComesFromTargetContextBuilder` constructs `session: &ephemeralSessionStore{}` (D1) and calls `spawnSubTurn` with `Async: false` (D4) to get a direct synchronous result — the native persona-resolution-via-ContextBuilder assertion is unrelated to sync/async and stays valid once restructured | — |
| `pkg/agent/subturn_recall_limitation_test.go` | **update** | `TestSubTurn_RecallReadsParentStore_KnownLimitation` constructs `session: &ephemeralSessionStore{}` (D1) and calls `spawnSubTurn` with `Async: false` (D4); the "known limitation" itself is framed entirely around the child having "its own ephemeral session store" distinct from `agent.Sessions` — D1's single real store for every steered session changes the shape of this limitation, so the setup and the expected recall-miss behaviour both need re-deriving against the new storage model, not merely re-pointed | — |
| `pkg/agent/subturn_requested_skill_test.go` | **update** | All 5 functions share `rsSpawn`, which constructs `session: &ephemeralSessionStore{}` (D1) and calls `spawnSubTurn` with `Async: false` (D4). The requested_skill grant/deny/unresolvable/parent-irrelevance assertions (ADR-072) are unrelated to ADR-091 and stay valid once the shared fixture moves to the real store and async-only dispatch | — |
| `pkg/agent/subturn_result_test.go` | split | `TestUpdateToolCallStatusWithRetry_AsyncWaitsForDelayedPlaceholder`, `TestDeliverSubTurnResultNoDeadlock`, `TestDeliverSubTurnResult_RaceWithFinish` **retain** — no ring, no `Async` field, no deleted mechanism; concurrency/orphan-delivery bookkeeping is unaffected. `TestUpdateToolCallStatusWithRetry_FoundOnFirstAttemptSkipsRetry` **update**: its `for _, async := range []bool{true, false}` loop exercises a `false` (sync, no-retry-expected) branch of `updateToolCallStatusWithRetry` that D4 makes unreachable — the loop must drop to the async-only case, the assertion (resolves on first attempt, zero retry-sleeps) stays valid. `TestUpdateToolCallStatusWithRetry_SyncDoesNotWaitForDelayedRecord` **delete**: its entire subject is the synchronous-delegation "found=false on the first attempt is terminal" contract, which no longer exists once every delegation is async (D4) | — |
| `pkg/agent/subturn_soul_test.go` | retain | Neither function touches the ring or `Async`: one calls `resolveDelegateSoul`/`composeDelegateInput` directly, the other drives `runExternalCLISubTurn` (preserved runner, D10) | — |
| `pkg/agent/subturn_sources_test.go` | retain | Not a test file at all — a shared `subturn*.go` production-source-scanning helper (`readSubturnSourcesForTest`) other guard tests import; still needed post-ADR-091 (the corrected reason; the draft's "skill/soul sourcing" reason was wrong, though its `retain` call was right) | — |
| `pkg/agent/subturn_test.go` | split | **delete** (5): `TestSpawnSubTurn_EphemeralSessionIsolation` (the ring's isolation from the parent's store is its literal subject — D1); `TestSpawnSubTurn_ResultDeliverySync` and `TestSyncSubTurn_NoChannelDelivery` (sync/`async=false`-specific delivery behaviour — D4); `TestEphemeralSession_AutoTruncate` (the ring's own 50-message-truncation mechanism — D1); `TestConcurrencySemaphore_Timeout` (asserts `ErrConcurrencyTimeout`, the exact blocking-wait-with-timeout D9 deletes: "Concurrency wait… deleted… a launch at the cap never blocks and is never refused"). **retain** (2): `TestInjectFollowUp`, `TestAPIAliases` (pure `al.steering` queue tests, no session/ring/`Async` dependency). **update** (24, all remaining functions in the file): every one constructs `session: &ephemeralSessionStore{}`/`newEphemeralSession(nil)` (D1) for its turnState fixture, several additionally passing an explicit `Async:` value (field deleted, D10) or exercising `al.HardAbort`'s in-memory `childTurnIDs` cascade / `initialHistoryLength` ring-rollback (D8's durable, edge-based cascade supersedes the in-memory-only walk) — the draft blanket-retained all but 2 of this file's 31 functions | — |
| `pkg/agent/subturn_timeout_stops_child_test.go` | split | `TestDelegatedTaskNoticeIdentityBoundsAndFlattensLabel` **retain** (pure string-formatting test of `delegatedTaskNoticeIdentity`, no session dependency). The other 5 (`TestSubTurnTimedOutResult_PublishesIdentifiedNoticeWhenChildAlreadyTimedOut`, `TestSpawnSubTurn_SettledTimeoutPublicationChoosesSingleOwner`, `TestDelegatedTaskLimitNotice_PersistsOnlyToNestedRootTranscript`, `TestSubTurn_TimedOutChild_StartsNoFurtherToolCalls`, `TestSubTurn_MaxToolIterationsPublishesIdentifiedOperatorNotice`) **update**: each asserts a delegate-lifecycle notice (timeout/max-iterations) written *directly to the parent's transcript* plus a live `EventKindError` — exactly the boundary-11 mechanism D3/I-5 replaces ("`subturn_result.go::emitSubTurnIterationLimitNotice`… becomes an `error` (`fatal: false`) inbox entry, never a root-transcript write"); two of the five additionally construct `session: &ephemeralSessionStore{}` (D1) and pass `Async: false` (D4). The draft's blanket `retain` also cited the wrong D-number (D9, which is about limit *configuration* keys, not notice delivery) | — |
| `pkg/agent/subturn_toolcall_hardabort_status_test.go` | **update** | `session: newEphemeralSession(nil)` (D1) and `SubTurnConfig{Async: true, Critical: true}` (field deleted, D10) in the fixture; the hard-abort-during-tool-call → `SubTurnStatusCancelled` (not `Success`) recording behaviour under test is unrelated to either deletion and stays valid | — |
| `pkg/agent/subturn_transcript_nesting_test.go` | **delete** | `TestSpawnSubTurn_MultiStepChild_StampsParentSpawnCallIDOnOwnNarration` exists specifically to prove the child's own narration entries carry `ParentSpawnCallID` so replay can nest/suppress them under the parent's span — exactly "Nested rendering of the child's steps inside the parent's chat (`subagent_start`/`subagent_end`, `parentSpawnCallID`) \| **Removed from the chat**" (§2.2) and `replay.go::emitNestedToolCalls`, named for deletion in D10. The draft's `retain` missed that this is the very mechanism D7 kills, not a generic transcript test | — |
| `pkg/tools/delegate_adr053_test.go` | **update** | All 25 functions construct `session.LifecycleRecord{…, ParentDurableKey: …}` directly (D2 — field removed, migrates to `SteeredBy.SteeringSessionID`); the Cancel-action functions additionally inject `SetCancelHooks(soft, hard)`, the ad-hoc callback pair I-6 replaces with the published `steer.Canceller.CancelSubtree`; the Respond-action functions' redispatch-via-`IsResume` mechanism likewise moves under I-2/I-6's generation-aware `Dispatch`. Every steering action under test (steer/respond/cancel/inbox/inbox_ack/peek/follow_up, cross-owner rejection, external-CLI steer rejection, needs_input park/resume, cancel soft-then-hard, fail-closed-on-Load-error) is explicitly named as kept by D5/D6/D8 — none of this is a `delete`. The draft blanket-retained this entire 1009-line, 25-function file citing only "delegation logic retained," missing that every single function constructs the field D2 removes | — |
| `pkg/tools/delegate_adr057_fix_test.go` | split | `TestKillChildBackgroundShells_ReturnsRealOutcome` **retain** (background-shell-kill outcome reporting via `SessionManager`/`ProcessSession`, no `ParentDurableKey`/ring/`Async`). The other 4 (`TestDelegateCancel_SurfacesBackgroundShellKillFailure`, `TestEvictStaleTasksLocked_CapEnforced_IndependentOfTTL`, `TestListTaskCopies_DoesNotMutateStoredLastStatusRead`, `TestVerifyCallerOwnsSession_LogsIOErrorDistinctFromNotFound`) **update**: the first and last construct `ParentDurableKey`-bearing records and (for Cancel) `SetCancelHooks` (D2, D8/I-6); the middle two exercise `tool.tasks`/`t.sessionIndex` eviction bookkeeping, the in-memory structure superseded by lifecycle-record-based tracking once status moves off streaming (D5/Q22) | — |
| `pkg/tools/delegate_adr057_test.go` | split | `TestDelegate_RefusedWithoutLifecycleStore`, `TestOwnershipWalk_SiblingRejectedAncestorAllowed`, `TestOwnershipWalk_DepthBounded`, `TestOwnershipWalk_AllSixGatedActions`, `TestDelegateTaskMaps_BoundedAfterNCompletions`, `TestDelegateTaskMaps_RetainWithinTTL` **update**: the ownership-walk tests construct `ParentDurableKey`-chained records via `u14SeedChild` (D2) and prove exactly the ancestor-walk D5 keeps ("any ancestor may act… matches today's ancestor walk"); the refusal and task-map tests pass `"async": false` throughout (now rejected at argument validation before reaching their real subject — needs mechanical adjustment) and touch the `tool.tasks` bookkeeping superseded per D5/Q22. `TestDelegateStatus_SyncAndAsyncSnapshotsNonEmpty` **update**: its "async" subtest is fine in concept but reads `tool.tasks`-based status (superseded, D5/Q22); its "sync" subtest tests `executeSync`'s own separate registration bug, which no longer exists as a distinct code path (D4) — needs re-deriving, not a clean delete of the whole function. `TestRecentActivityLines_LogsEmptyPath` **delete**: tests `recentActivityLines`, which reads the child's own persisted transcript by `ParentSpawnCallID` as a live-activity proxy for status — exactly the streaming/transcript-sourced status mechanism #614/D5/Q22 removes | — |
| `pkg/tools/delegate_adr057_unix_test.go` | **update** | `TestDelegateCancel_KillsThatChildsShells` seeds a `ParentDurableKey`-bearing record and wires `SetCancelHooks` — D2 and D8/I-6; the real-PID background-shell-kill-on-cancel behaviour (FR-028) is unrelated to either deletion and stays valid | — |
| `pkg/tools/delegate_completion_identity_test.go` | split | `TestDelegateAsyncCompletion_MaxToolIterationsNamesTask` **update**: exercises `tool.SetSpawner`/`ExecuteAsync` against the old `SubTurnSpawner` shape I-2 replaces with the injected `steer.SessionLauncher`; the attribution content (Label/Task ID/Session naming) reaching the delegator is an unrelated, kept concern. `TestDelegateSync_ThreadsGeneratedTaskIDToSpawner` **delete**: its subject is threading `TaskID` through the sync (`"async": false`) dispatch path specifically — D4 | — |
| `pkg/tools/delegate_detached_timeout_test.go` | split | `TestErrDelegationDetachedImpliesTimedOut` **retain** (pure `errors.Is` wrapping check, no session dependency). `TestDelegateSync_DetachedTimeoutReportsStillUnwinding` **delete**: named and framed entirely around the sync (`"async": false`) dispatch path — D4 | — |
| `pkg/tools/delegate_fixwave_test.go` | **update** | All 13 functions. The 8 follow_up/steer/cancel functions construct `session.LifecycleRecord{…, ParentDurableKey: …}` directly (D2) — one (`TestDelegateTool_Steer_TerminalCheck_RoutesThroughMutate`) has its own reasoning built entirely around "the walk climbs the `ParentDurableKey` chain," needing a real rewrite, not a field rename. `TestDelegateTool_Run_AgentID_Validation` and `TestDelegateTool_Run_TimeoutSeconds_ThreadsIntoSubTurnConfig` pass `"async": false`/`"async": true` pervasively as incidental drivers (the latter also has a "sync path" subtest whose Timeout-threading mechanism moves to `Limits.TimeoutSeconds` on the edge, D9). `TestDelegateTool_ResolvableSessionIDs(_EmptyIndex)` read `t.sessionIndex`, the map that may be restructured alongside `tool.tasks` per D5/Q22. None of these test wait-inline/ring/nesting as their subject, so none is `delete` — the draft's blanket `retain` missed every one of these mechanism touches | — |
| `pkg/tools/delegate_followup_resume_test.go` | **update** | All 3 functions construct `session.LifecycleRecord{…, ParentDurableKey: …}` (D2); the native-resume-sets-IsResume / 3P-cold-respawn / spawn-failure-logged-unconditionally behaviours are kept concerns whose mechanism moves with the field | — |
| `pkg/tools/delegate_grandchild_test.go` | retain | `TestDelegateCannotSpawnGrandchild` tests the `CloneExcept(ExcludedDelegate, ExcludedSwitchAgent)` registry primitive in isolation (its own file header already notes it no longer describes production `spawnSubTurn`, which excludes only `switch_agent`); D2 keeps `switch_agent` exclusion "as a property of the edge" and the primitive itself is untouched by any D-number | — |
| `pkg/tools/delegate_inbox_ack_ancestor_test.go` | **update** | All 3 functions seed `session.LifecycleRecord{…, ParentDurableKey: …}` and assert `executeInboxAck` is keyed by `rec.ParentDurableKey` rather than the caller's own key — precisely the field D2 removes; the security property under test (an ancestor's ack must land in the target's real inbox, a stranger must be denied) is exactly what D3/D5's edge-based model must also uphold | — |
| `pkg/tools/delegate_m1_m3_wiring_test.go` | **update** | `TestDelegateInboxAck_TruthfulCountAndUnknownIDs` and `TestDelegateInboxAck_AllUnknown_ZeroAcknowledged` seed `ParentDurableKey`-bearing records (D2); `TestDelegateTool_ResolvableLabels_ImplementsJobLabelResolver` constructs `tool.tasks`/`tool.sessionIndex` directly, the structure that may be restructured per D5/Q22 — the label-resolution behaviour for `list_jobs` itself is untouched by any D-number | — |
| `pkg/tools/delegate_parent_agent_test.go` | split | `TestDelegateMint_FailsClosedOnEmptyParentAgentID` **retain**: asserts fail-closed refusal and that no record is written; touches neither `ParentDurableKey` nor the ring. `TestDelegateMint_StampsParentAgentID` **update**: directly asserts `rec.ParentDurableKey != transcript` and `rec.ParentAgentID == rec.ParentDurableKey` — D2 removes the field this test reads (D2 explicitly keeps `ParentAgentID` stamping itself: "the launcher now stamps that field for both fronts") | — |
| `pkg/tools/delegate_park_lifecycle_test.go` | split | `TestDelegateTool_ExecuteAsync_ParksTurn_LeavesNeedsInputRecord` and `TestDelegateTool_ExecuteAsync_NonParkedResult_StillTransitionsToCompleted` **update**: exercise the async (kept) dispatch path; the parked/non-parked lifecycle-state distinction they protect is explicitly named as kept by D6 ("Kept; carried on the edge (D5)"), reading status from the lifecycle record itself (already aligned with D5/Q22, not the superseded streaming mechanism). `TestDelegateTool_ExecuteSync_ParksTurn_LeavesNeedsInputRecord` and `TestDelegateTool_ExecuteSync_NonParkedResult_StillTransitionsToCompleted` **delete**: each is explicitly framed around "executeSync's own `default:` branch, guarded by a distinct switch statement" — a second, sync-only code path D4 deletes | — |
| `pkg/tools/delegate_require_parent_test.go` | **update** | All 5 functions pass `"async": false` in every `Execute()` call as a default/habit, unrelated to what each actually tests (the `require_parent_agent_id` kill-switch fail-closed/escape-hatch/live-resolution behaviour, which D-numbers do not touch) — since `async:false` is now rejected by argument validation before reaching the guard under test, every call needs the argument removed or the test would fail for the wrong reason | — |
| `pkg/tools/delegate_signoff14_test.go` | split | `TestSteerRateWindows_EvictsExpiredOtherSessionKeys` **retain** (pure `steerRateWindows` map-eviction bookkeeping, no `ParentDurableKey`/ring/`Async`). The other 5 (`TestDelegateTool_Respond_NativeRedispatchesWithIsResume`, `_EnqueueFailure_LeavesSessionParkedNotWedged`, `TestDelegateTool_Inbox_AuthorizedAncestor_SeesMessagesUnderDirectParentKey`, `TestDelegateTool_Peek_AuthorizedAncestor_SeesSnapshotUnderDirectParentKey`, `TestDelegateTool_Cancel_NothingToCancel_StillSurfacesShellKillWarnings`) **update**: all seed `ParentDurableKey`-bearing records via `u14SeedChild`/`lc.Persist` (D2), the Cancel one also via `SetCancelHooks` (D8/I-6) | — |
| `pkg/tools/delegate_status_reconnect_test.go` | **update** | All 5 functions manipulate `tool.tasks[...] = &DelegateTaskState{…}` directly, scoping status visibility by `OriginChannel`/`OriginChatID`/`SessionID` string matching — D5 replaces this ad-hoc scoping with edge-based ancestor authority (AC-5: "Siblings and unrelated roots are rejected"); the underlying isolation property (a reconnect must not lose or leak a delegation's status) is explicitly reinforced, not removed | — |
| `pkg/tools/delegate_status_snapshot_test.go` | **delete** | All 3 functions (`TestDelegateStatus_RunningNative_IncludesRecentActivity`, `_NoMatchingActivity_OmitsHeader`, `TestDelegateStatus_RunningExternalCLI_NoLiveSnapshot`) exist to prove `action:"status"` surfaces the child's own recent *transcript narration* as a live-activity snapshot — exactly the mechanism D5/Q22 removes closing #614 ("status answers from the record and inbox… never from streaming progress"). The draft's `retain` missed this entirely | — |
| `pkg/tools/delegate_status_test.go` | retain | All 4 functions test `formatToolCallProgressLine`, a pure string-formatting function with no session/store dependency | — |
| `pkg/tools/delegate_test.go` | split | **retain** (5): `TestDelegateTool_Name`, `_Description`, `_Execute_EmptyTask`, `_Execute_InvalidAction`, `TestDelegateTool_KillSwitchGatesSessionMessagingActionsOnly_ArchM2`. **update** (15): `TestDelegateTool_Parameters` (asserts `"async"` IS a schema property — D4 deletes the argument, the assertion must invert); `TestDelegateTool_Execute_NilSpawner` (its "sync mode" sub-check now fails for a different reason — argument validation, not nil-spawner); `TestDelegate_DefaultIsAsync`, `TestDelegate_MainAgentCanDelegate` (dispatch/response shape moves from `ToolResult.Async` to `DispatchResult`, I-2); `TestDelegate_StatusReflectsRealState` and all 9 `TestDelegateStatus_*` functions (`Empty`, `ListAll`, `GetByID`, `GetByID_NotFound`, `TaskID_NonString`, `ResultTruncation`, `ChannelFiltering_ListAll`, `ChannelFiltering_GetByID`, `ChannelFiltering_NoContext`, `DifferentConversation_NotFound`) all manipulate `tool.tasks`/`DelegateTaskState` directly, the mechanism D5/Q22 supersedes for status, though the scoping/truncation/not-found properties they protect stay valid concerns. **delete** (5): `TestDelegateTool_Execute_AsyncWrongType` (type-validates an argument D4 removes outright), `TestDelegate_AsyncFalseBlocks`, `TestDelegate_AsyncFalse_NowRecordsStatusTask`, `TestDelegate_SyncInterrupted_PropagatesToResult`, `TestDelegate_SyncGenuineError_StillUsesGenericShortcut` (each is specifically about the sync/`async=false` dispatch path — D4). The draft blanket-retained this whole 812-line, 25-function file | — |
| `pkg/tools/delegate_timeout_force_cancel_unix_test.go` | split | `TestDelegateSync_TimedOut_ReportsStoppedAndKillsChildShells` **delete**: named and driven entirely via `"async": false` — D4. `TestDelegateAsync_TimedOutAfterParentTurnEnded_ReportsStoppedNotCanceled` **retain**: exercises the kept async path via `ExecuteAsync` directly, no `ParentDurableKey`/ring; the truthful-timeout-report/shell-kill/`timed_out`-state behaviour is unrelated to any deletion | — |
| `pkg/tools/delegate_toolcall_progress_test.go` | **delete** | All 4 functions exist to prove `action:"status"` surfaces a *live, in-memory* tool-call-argument-streaming snapshot via `DelegateProgressReader`/`AgentLoop.ProgressForSession` — exactly "never from streaming progress" (D5/Q22, #614). The draft's `retain` missed this entirely | — |
| `pkg/tools/message_parent_adr057_test.go` | **update** | Both functions seed `session.LifecycleRecord{…, ParentDurableKey: …}` (D2); `TestMessageParent_DrainedByDirectParentAtDepth3` is specifically about the ancestor-vs-direct-parent inbox-keying fix `MessageParentWaker`/`steer.UpwardDeliverer` (I-5) must also get right; the per-child ceiling-independence property in the second function is an unrelated, kept concern | — |
| `pkg/tools/message_parent_test.go` | **update** | All 13 functions share `newMessageParentTestSetup`, which seeds `session.LifecycleRecord{…, ParentDurableKey: …}` (D2) and wires a `fakeWaker` implementing `MessageParentWaker` — the exact interface D3/I-5 replaces ("`steer.UpwardDeliverer`… injected wherever `message_parent`'s `MessageParentWaker` is wired today — it replaces that interface"). Every message kind under test (progress, question/wait, blocker ceiling, 3P rejection, egress filtering on path fields, kill switch) is explicitly named as kept by D3's table. The draft blanket-retained this whole file | — |

Verified by reading every test function (cross-family review of the Haiku draft).

## Definition of done

1. *Code correct and tested:* fixtures, recorder and suites green; classification complete.
2. *Reachable:* the Playwright reachability spec is the product's AC-12 proof, executed and recorded.
