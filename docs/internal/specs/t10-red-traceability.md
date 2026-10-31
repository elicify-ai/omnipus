# T10 RED traceability — final specification r6

**Bottom line: partial RED pack, not complete scenario coverage and not ready for implementation sign-off.** The pack compiles and exposes real missing behavior. Missing T8 behavior and unbound deterministic hooks remain loud failing tests, not skips. A declaration that says `BLOCKED` is not counted as a completed behavioral test. The tables below explicitly retain the uncovered portions.

## Authority, base and scope

- Behavior: `/workspace/omnipus-coordination/designs-20261029/schedules-t10-spec-r6.md`, final r6; answer C includes **all B-only content plus step 0**. No further design/grill round was started.
- Inheritance: `/workspace/omnipus-coordination/designs-20261029/tool-parity-spec-r17.md` and `/workspace/omnipus-coordination/designs-20261029/t8-rebase-note-6b-replay.md`.
- Worktree: `/workspace/wt-t10`; branch `landing/t10`; fetched base `origin/release/v0.1.1` at `1d3989df060887c9afe37c367562ce6975550724`. No push, merge, issue closure or production implementation.
- Interrupted-started once exception is preserved: a delivered one-time fire whose run started and never finished is replayed once at boot under T8 6b F1. Never interpret the skipped-once rule as overriding this case. This pack declares that inherited backend dependency; it does not fake its replay.
- The module actually requires Go 1.27.3. All Go tests used `-tags goolm,stdjson -p 1`, plain `/usr/local/bin/heavy`, named test selection, foreground execution, one heavy job at a time. Full suites, race sweep, full typecheck/build and live acceptance were not run.

## Verified premise drift and instrument corrections

1. The spec pin `da7c1d941` is older than the requested base. #1490's three-copy shutdown stop ordering and task-trigger stop-before-sweep are already present. Step 0 is **passing characterization**, not manufactured RED. Saved comparison: `/workspace/logs/t10-red/spec-pin-drift.diff`.
2. The canonical unknown-API JSON 404 fallback already exists in `/workspace/wt-t10/pkg/gateway/gateway_boot.go` (`prepareListener`). The production-mux unknown-path/SPA/CSRF controls pass. The schedules and Automations routes themselves still exist and fail removal tests.
3. #1243 was **not reproduced** by the new held-worker backend fixture: full Calendar-shaped saves with After N, On date and Does not repeat all succeed while a real run is open and the unchanged definition remains frozen. This is a passing characterization of that exact fixture, not proof that every reported browser race is fixed. The six UI save-request controls pass. Browser execution is still blocked on the isolated held-worker fixture and a built app.
4. `MessageInboxStore.Drain` reads unacknowledged messages; it does not consume them. The first creator test wrongly expected a second read to be empty. That oracle was corrected to compare complete before/after snapshots across all recipients. The corrected run finds **no duplicate notices** on the tested completion retries. The missing creator copy/loss mechanism remains real RED.
5. The original run-now helper filtered out every pre-T10 `agent_turn` job and misleadingly said no registration existed. Discovery now uses the actual task identity; a dedicated real-producer test still asserts the final `task` kind. The first Judge fixture also omitted Definition-of-Done criterion IDs. It now reads the real paired goal's criteria plus DoD; real runs settle and the manual control passes. A dormant past job with null next-run is distinguished from an armed occurrence; tests do not require deleting an unarmed bookkeeping object.
6. Initial UI transport lacked the synthetic CSRF cookie; the corrected test supplies it. The stale-action check now accepts the real typed client's plain conflict message and fails specifically on the absent occurrence re-read. The earlier CSRF/setup failures are retained as test-instrument receipts, not product findings.

## Test report and evidence

### Final named runs

| Area | Authored Go test functions | Passing | Failing | Targeted package statement coverage |
|---|---:|---:|---:|---:|
| Config | 2 | 0 | 2 | 2.8% |
| Cron engine | 7 | 1 | 6 | 57.2% |
| Agent loops/tasks/notices/rider | 15 | 3 | 12 | 13.8% |
| Gateway wiring/routes/run-now/projection | 22 | 6 | 16 | 4.4% |
| Real run_task tool | 1 | 0 | 1 | 0.3% |
| **Authored Go total** | **47** | **10** | **37** | **7.8% weighted across the five selected packages** |

- Final Go selection: **60 top-level functions: 23 pass, 37 fail**, including **13 existing regression functions, all passing**. Expanded leaf results: **58 pass, 55 fail without a BLOCKED declaration, 71 fail with a BLOCKED declaration; zero skipped**. The 71 blocked leaves include prerequisite failures and dependency declarations, not 71 independent BDD scenarios. Some also expose an earlier real assertion (for example initial loop delay); they must not be counted twice. Authored leaves alone: 35 pass, 55 ordinary fail, 71 blocked. Parent and child counts are not added together.
- Final UI selection: **89 cases: 81 pass, 8 fail, zero pending**. Authored UI cases: **19: 11 pass, 8 fail**. Existing UI regressions: **70, all pass** (47 Calendar editor, 2 recurring guard, 21 run-status field).
- One Playwright #1243 row is authored in the already assigned Calendar spec. **Collection only: 1 row found. It was NOT executed**; no browser, redirect or screenshot success is claimed. Required isolated fixture variables are `T10_INFLIGHT_TASK_ID` and `T10_INFLIGHT_WORKSPACE_ID`; absence fails loudly, never skips.
- Both final test runs intentionally exit 1. Passing controls prove specific outputs/state: real terminal run results and session IDs, inbox contents, exact retry/retention/timing, real mux envelopes, and real save requests. They do not merely assert “no error”.

| Evidence | Exit | Meaning |
|---|---:|---|
| `/workspace/logs/t10-red/step0-1450.log` | 1 | Missing SPA embed input; infrastructure, not behavioral RED. |
| `/workspace/logs/t10-red/step0-1450-with-embed-fixture.log` | 75 | Test command passed but runner copy-back failed; not green. |
| `/workspace/logs/t10-red/step0-1450-rerun.log` | 0 | Original named #1450 repro repeated 150 times with cpu 1. |
| `/workspace/logs/t10-red/step0-shutdown.log` | 0 | Real old timer stores/drain controls and bounded shutdown harness. |
| `/workspace/logs/t10-red/step1-core-red-rerun.log` | 1 | Executable config/cron RED; earlier output-directory failure superseded. |
| `/workspace/logs/t10-red/step1-2-agent-red.log` | 1 | Real loop/task failures plus declared unbound recovery/T8 hooks. |
| `/workspace/logs/t10-red/step1-3-gateway-red-rerun.log` | 1 | Production boot/mux/removal RED, after test compile corrections. |
| `/workspace/logs/t10-red/step1-2-existing-targeted.log` | 0 | 12 named base regression functions. |
| `/workspace/logs/t10-red/step4-notice-baseline.log` | 0 | Passing real stored-run/CAS/inbox baseline before creator delta. |
| `/workspace/logs/t10-red/step4-corrected-entries-notices.log` | 1 | Corrected creator copy/tool/run-now failures, not the old Drain/Judge artifacts. |
| `/workspace/logs/t10-red/step4-rider-status-controls.log` | 1 | Three-sink #712 RED, real live run-now control pass, both status-route failures. |
| `/workspace/logs/t10-red/final-targeted-go-v3.jsonl` | 1 | Authoritative final Go selection including all 47 authored functions and 13 regressions. |
| `/workspace/logs/t10-red/final-go-v3-function-coverage.log` | 0 | `go tool cover -func` over final profile. |
| `/workspace/logs/t10-red/final-targeted-ui-v2.log` | 1 | Authoritative five-file UI selection and V8 coverage on expected failures. |
| `/workspace/logs/t10-red/step4-playwright-collection.log` | 0 | Browser row collection only; not execution evidence. |

Every listed run has the corresponding `.exit` receipt beside it. Machine results: `/workspace/logs/t10-red/go-test-counts.json`, `/workspace/logs/t10-red/go-file-coverage.json`, `/workspace/logs/t10-red/final-targeted-ui-v2-results.json`. Final profiles: `/workspace/logs/t10-red/final-targeted-go-v3.cover` and `/workspace/logs/t10-red/final-targeted-ui-v2-coverage/coverage-summary.json`. Earlier attempts are retained; v3 supersedes earlier Go snapshots. The final UI runner also reports failure copying its temporary `node_modules/.vite-temp` directory back; the JSON test report and coverage summary did arrive and their failing results are quoted here. This is not a green wrapper/copy-back claim.

### Feature-file coverage (targeted only)

| Production file | Statement coverage |
|---|---:|
| `/workspace/wt-t10/pkg/cron/service.go` | 59.2% |
| `/workspace/wt-t10/pkg/agent/loop_scheduler.go` | 57.0% |
| `/workspace/wt-t10/pkg/agent/task_trigger.go` | 53.3% |
| `/workspace/wt-t10/pkg/agent/task_run_loop.go` | 6.7% |
| `/workspace/wt-t10/pkg/agent/task_executor.go` | 14.9% |
| `/workspace/wt-t10/pkg/gateway/gateway_boot.go` | 11.2% |
| `/workspace/wt-t10/pkg/gateway/rest_tasks.go` | 30.7% |
| `/workspace/wt-t10/pkg/gateway/shutdown.go` | 71.7% |
| `/workspace/wt-t10/src/components/calendar/CalendarEventSlideOver.tsx` | 89.34% |
| `/workspace/wt-t10/src/components/calendar/RecurrenceEditor.tsx` | 84.21% |
| `/workspace/wt-t10/src/components/workspaces/TaskDetailPanel.tsx` | 35.03% |
| `/workspace/wt-t10/src/components/workspaces/TaskRunStatusField.tsx` | 100% |
| `/workspace/wt-t10/src/lib/calendar/useOccurrences.ts` | 63.33% |

The five explicitly included UI production files total **60.13% statements, 62.54% lines**. No whole-repository coverage percentage or all-scenario coverage percentage is claimed. High control coverage does not repair absent integration hooks or make a blocked scenario covered.

## Shortcut detection and verified product failures

| Real implementation / surface | Test that exposes it | Verified result |
|---|---|---|
| `CronService`, config and all producers | `TestJobSpecKind_RequiredAndKnown`, `TestT10_NoFixedLaneLimitRemains`, `TestT10_SchedulesCapacityKeyIsIgnoredAndNotSeeded`, `TestT10_AllProducersCarryKindOnCreatingSpec`, `TestT10_RealTaskCreatingRegistrationKind` | Required kind-creation API absent; retired jobs retained; fixed fields/defaults/semaphore remain; actual producer persists agent_turn. |
| Gateway scheduler wiring / auxiliary stores | `TestT10_GatewayBootUsesExactlyOneEngineAndSharedStore`, `TestT10_AuxiliarySchedulerFilesAreNeverOpened` | Private engines remain; Linux inotify observes forbidden auxiliary opens/replacements, not just byte differences. |
| Task and heartbeat reconciliation | `TestT10_TaskReconcilePreservesForeignKindsByteForByte`, `TestT10_HeartbeatReconcilePreservesForeignKindsByteForByte` | Real reconcilers prune foreign jobs, including a loop with a deceptive heartbeat-style name. |
| Loop ownership / G5 | `TestT10_LoopNonSelectedJobRunsNoTurnAndRemovesItself`, `TestT10_LoopStopAndReplaceRemoveEverySessionJob`, `TestLoopRunner_G5Protections` | Stale job executes and mutates state; session cleanup leaves a duplicate; worker owner is not refused. Initial self-paced creation fails its persisted-delay assertion too. |
| Retired API/contracts | `TestSchedulesRoutes_Removed_AllGeneric404`, `TestT10_NoScheduleWireTypesOrConsumers` | Routes and generated Schedule types/consumers remain. Generic unknown-API handling itself passes. |
| Tool/REST run-now | `TestT10_RunTaskRoutesScheduledBeforeStatusAndDoneGate`, `TestT10_RESTRunNowConsumesArmedRegistration`, `TestT10_RunNowExplicitEligibilityAndDearmedRegistration` | Tool pre-writes status, refuses recurring done too early; REST lacks consumption marker/context, leaves original armed; future de-armed task dispatches instead of refusing. Manual/past controls and non-armed-future explicit refusal pass. |
| Both REST status routes | `TestT10_ScheduledTaskRejectsBothStatusActivationRoutesBeforeWrite` | Real next and in_progress PATCHes mutate scheduled task files; in_progress opens a real run. |
| Creator delta | `TestT10_CreatorNoticeFanOutAtRealRunCompletion` | Same-workspace success/failure/authoritative Stop copies absent; unresolvable creator notice loss absent. Dedup/person/foreign/no-nonterminal-copy controls pass. No duplicate-delivery defect is claimed. |
| Dispatch diagnostics (#712) | `TestT10_NotDispatchedDiagnosticsStayOutOfEveryUserSink` | Raw causes, including synthetic registered-secret plaintext, enter actual task result, stored goal and chat. Gateway diagnostic is retained and registered secret scrubbed there. Only planted test credentials were used; no live secrets. |
| HTTP projection and real query-driven UI | `TestT10_OccurrencesHTTPProjectsLiveArmedTwinAndDearmedTwin`, authored Calendar/task-panel UI tests | Generated schema drops armed field; HTTP lacks live armed instant; E1/bucket actions absent; stale refusal does not refresh; schedule-bearing selector retains activation choices; one-shot panel still offers PATCH Start. |

No production function was replaced with a test implementation and no hardcoded/no-op implementation was supplied. Missing registration, memory-admission, recovery and T8 seams fail explicitly. The assertions on differentiated IDs/inputs, persisted full content, actual file writes, real notifications and real run histories would catch empty/constant implementations on exercised paths; unbound rows cannot make that claim and stay listed as gaps.

## Remaining gaps — completion must not be inferred

- **CRITICAL, needs T8 backend:** inherited ownership/delivery/waiting/boot/replay suite, actual caller revocation while queued, E0 automatic queued delivery, skipped and held creator lifecycle, consumed marker/restart/identical-save/edit/final-COUNT, plan gates and auto-deny provenance on unified run-now.
- **CRITICAL, unbound hooks:** real kind dispatcher, injected shared memory provider (25 mixed, pressure/unmeasurable floor, once-per-wait memory log, asynchronous task handler lifetime, no preemption/config matrix); exact pre-activation ordering; loop recovery identity and single-write/sustained persistence faults; exact post-ProcessScheduled interleave and no-frame oracle; real child cleanup and audit job identity.
- **Unwritten behavior assertions:** S-D.5 boot-only retry reload/physical restart; full same-process handleConfigReload proof; first-save write observation (AST and final disk content are not sufficient); loop nil-next ordering, same-boot interval/self-paced twin and exact limit-minus-one execution; symmetric loop reconciler foreign-kind protection; whole merged-engine all-kind shutdown.
- **UI / acceptance:** real CalendarScreen one-shot and aggregated-chip clicks and full retained edit/result/chat/day-history surfaces are not driven by the Vitest panel tests. They start at the real panel entry, not the real chip. Automations redirect is not browser-verified. The browser #1243 row is collected, not run, and its fixture needs to be bound by the acceptance lane. The status tests cover the selectors, not ordinary Board drain execution.
- `make verify-contracts` was **not run**: it invokes generation into production trees and a whole-project typecheck, outside this tests-only/targeted run. Contract tests expose deletion/additive-field requirements, not a successful regeneration gate. Full cut CI, race tests, eight independent holdout/UAT rows and docs-at-cut verification remain unexecuted.
- Compile scaffold: `/workspace/wt-t10/pkg/gateway/spa/index.html`, copied from `/workspace/wt-t10/pkg/gateway/testdata/t10-red-spa/index.html`, is explicitly marked RED embed-only HTML. It was force-tracked so the clean runner can compile. It is not a built SPA and must be replaced/removed before release. No production Go or TypeScript changed; do not describe this as zero non-test artifacts.
- Existing config characterization still asserts the old schedules defaults on the base. When capacity is removed, its obsolete assertions require an explicit spec-aligned update; they must not be silently weakened as an unrelated regression.

## Local commits (no push)

| Commit | Purpose |
|---|---|
| `046ec7360` | Step 0 shutdown characterization |
| `aa6500be9` | Kinds, admission and loop RED contracts |
| `0de599f13` | Task ownership and auxiliary no-access |
| `358f730b1` | Production-mux retired route checks |
| `32e7e8ffd` | Passing real run-completion notice baseline |
| `0c0a745bd0ac2b41adbcf02a5ec0a3b0fe527ebe` | Real run-now/creator/Calendar/#712/#1243 tests and browser row |

All commits use Daniel Piatkowski's authorized GitHub identity, with no AI co-author trailer. The final accounting/schema-boundary-check commit is reported in the handoff rather than recursively embedded in itself.

## BDD traceability — every r6 scenario

Status vocabulary: **RED** = executable behavior/structural defect observed; **PASS** = stated characterization actually passed; **PARTIAL** = evidence exists but important clauses are not proved; **BLOCKED** = failing declaration/prerequisite, not completed behavior coverage; **GAP** = not converted into a behavioral test. Every row below is taken from the r6 headings; no new scenario is invented. Final evidence defaults to the final Go/UI receipts above unless a row names the step-0/browser receipt.

| Scenario (r6 line) | Test / entry | Status and evidence limits |
|---|---|---|
| S-A.1 — Fresh install creates exactly one engine and one store (580) | `TestT10_GatewayBootUsesExactlyOneEngineAndSharedStore; TestT10_AuxiliarySchedulerFilesAreNeverOpened` | **RED / PARTIAL** — Real boot retains private engines and auxiliary paths; full constructor-count instrumentation is absent. |
| S-A.2 — Dispatcher routes each kind to its own handler (587) | `TestDispatcher_RoutesByKind_Exclusively` | **BLOCKED** — Kind registration adapter is unbound; no successful per-kind production dispatch proof. |
| S-A.3 — Non-registered kinds fail closed (and are dropped at load) (594) | `TestJobSpecKind_RequiredAndKnown; TestT10_NonRegisteredKindsDroppedOnceAtLoad; TestT10_LoadDropLoggingNamesEveryJobExactlyOnce` | **RED + BLOCKED** — Required creating Kind absent; real persisted residue is retained and drop logs absent. Valid-create rows stop at missing Kind. |
| S-A.4 — Engine lock stays a leaf under the dispatcher (601) | `TestDispatcher_RoutesByKind_Exclusively` | **BLOCKED** — Reentrant callback oracle written but not reached until real dispatcher binding exists; concurrent lock-order stress absent. |
| S-A.5 — Reconcilers never touch foreign kinds (608) | `TestT10_TaskReconcilePreservesForeignKindsByteForByte; TestT10_HeartbeatReconcilePreservesForeignKindsByteForByte` | **RED / PARTIAL** — Task and heartbeat foreign-kind deletion observed; symmetric real loop reconciliation still uncovered. |
| S-B.1 — Task trigger and heartbeat survive a restart exactly once (617) | `TestT10_InheritedT8OwnershipAndWaiting; TestT10_HeartbeatsReDerivedAndFireWithoutSchedulesAPI` | **BLOCKED / PARTIAL** — Real heartbeat derivation/timing passes; exact task/heartbeat physical-restart table and once-only task dispatch not rebound. |
| S-B.2 — Overdue once at boot stays skipped, never replayed (624) | `TestT10_InheritedT8OwnershipAndWaiting` | **BLOCKED** — Needs T8 boot/delivery backend. Waiting state, skipped marker and the interrupted-started 6b exception are declared, not behaviorally proved. |
| S-B.3 — Unreadable store aborts boot before any reconciliation or collection (631) | `TestT10_GatewayBootCheckedLoadPrecedesAllWrites` | **PASS** — Actual boot caller receives original UnmarshalTypeError, shared file byte-identical and hosted schedulers not started. |
| S-B.4 — Reconciliations run before the engine can collect (638) | `TestT10_BootReconciliationsFinishBeforeFirstCollection` | **BLOCKED** — Exact production pre-activation/first-collection barriers unbound; checked-load failure alone cannot prove success order. |
| S-B.5 — Loop recovery re-arms an overdue self-paced one-shot exactly once (645) | `TestLoopBootReconcile_ReArmsOverdueOneShot` | **RED / BLOCKED** — Real /loop creation lacks persisted initial 1000 ms delay; boot-recovery hook absent, later timing/chaining oracle not reached. |
| S-B.6 — A crash that left two loop jobs yields exactly one owned next step (652) | `TestLoopBootReconcile_OwnershipAndCrashWindows; TestT10_LoopStopAndReplaceRemoveEverySessionJob` | **BLOCKED / PARTIAL** — Recovery/crash windows blocked; real non-recovery stop/replacement leaves duplicate jobs, proving session cleanup defect only. |
| S-B.7 — A loop at its saved run limit is not re-armed (661) | `TestLoopBootReconcile_OwnershipAndCrashWindows` | **BLOCKED** — Saved limit and limit-minus-one fixtures written; recovery absent. Exact one-more execution oracle is still a gap. |
| S-B.8 — A revoked loop callback mutates nothing (R4-M01) (668) | `TestT10_LoopRevokedCallbackHasNoPostTurnEffects; TestT10_LoopNonSelectedJobRunsNoTurnAndRemovesItself` | **BLOCKED / PARTIAL** — Exact post-return interleave/frame test unbound. Entry-time stale callback actually runs a turn and mutates state (RED). |
| S-B.9 — A recovery that cannot persist its outcome does not activate the engine (R4-M02) (677) | `TestT10_LoopRecoveryPersistenceFailures` | **BLOCKED** — Single-write, sustained/repeated failure and next-boot retry hooks absent. No persistence/activation outcome is claimed. |
| S-B.10 — A missing-job interval loop is reconstructed as an interval job (R5-M02) (685) | `TestLoopBootReconcile_OwnershipAndCrashWindows` | **BLOCKED** — Interval missing-job fixture has every/interval/two-run expectations but stops at recovery binding; same-boot self-paced twin not implemented. |
| S-C.1 — No timer work starts inside the drain (695) | `TestShutdown_EngineStopsBeforeDrain` | **PASS base / PARTIAL merged** — Step-0 real three-copy stores/drain and stop-order harness pass. All three kinds on the future merged engine not yet demonstrated. |
| S-C.2 — In-flight run cancels within the existing budget (702) | `TestShutdown_EngineStopsBeforeDrain` | **PASS harness / PARTIAL** — Scaled existing-budget cancellation and close pass; not an actual merged all-kind in-flight shutdown run. |
| S-C.3 — A config reload leaves the engine — and every job lifecycle — untouched (708) | `TestReload_DoesNotRestartEngineOrStrandRunning` | **BLOCKED** — Actual reload phases selected, but missing Kind blocks held-run execution. Full handleConfigReload and boot-backoff legs remain gaps. |
| S-D.1 — Creating write already carries the final kind (717) | `TestT10_AllProducersCarryKindOnCreatingSpec; TestT10_RealTaskCreatingRegistrationKind; TestJobSpecKind_RequiredAndKnown` | **RED / PARTIAL** — All four producer AST checks and actual task creating/final persistence fail kind requirement; first creating-write observation/count still absent. |
| S-D.2 — The removed capacity setting has no effect; admission is memory-governed (724) | `TestT10_SchedulesCapacityKeyIsIgnoredAndNotSeeded; TestT10_NoSchedulesCapacityDefaultConstant; TestT10_NoFixedLaneLimitRemains; TestScheduledAdmission_MemoryGoverned` | **RED / BLOCKED** — Config/fields/default/semaphore failures are real; healthy 25 mixed and per-config real dispatch cases await memory binding. |
| S-D.3 — The memory floor caps dispatches across kinds (not whole runs) (731) | `TestScheduledAdmission_MemoryGoverned` | **BLOCKED** — Shared cap-visibility gate unbound; no async task handler-lifetime/no-preemption/once-per-wait log proof. |
| S-D.4 — Burst of one kind queues other dispatches (accepted) (739) | `TestScheduledAdmission_MemoryGoverned` | **BLOCKED** — Burst row is a binding declaration, not verified fair/eventual mixed-kind admission. |
| S-D.5 — A reload-time retry-backoff change keeps the boot value (and no capacity exists to change) (746) | `TestReload_DoesNotRestartEngineOrStrandRunning (related only)` | **GAP** — No real reload-backoff-change and physical-restart test written. Capacity structure tests do not cover boot-only backoff. |
| S-E.1 — Schedule-runner-kind transient failure retries on existing backoff (755) | `TestPerKindRules_Preserved` | **PASS characterization** — Direct real engine heartbeat fixture: configured 60/120/300 s retry and reset after real success. Merged kind-dispatch binding remains separate. |
| S-E.2 — Task occurrence failure never backs off (761) | `TestT10_TaskOccurrenceFailureUsesNextBoundaryNotBackoff` | **PASS handler / PARTIAL** — Real task scheduler advances failed daily fire to exact next boundary with zero retry counter; full real async run/history retention not proved by this refusing dispatch seam. |
| S-E.3 — Loop failure records and carries on (768) | `TestT10_LoopFailureRecordsThenCarriesOnAtNextInterval` | **PASS characterization** — Real /loop, ProcessScheduled and engine: provider failure recorded; next exact interval runs successfully; count/history advance twice, no retry backoff. |
| S-E.4 — Retention rules unchanged (774) | `TestPerKindRules_Preserved; TestTaskTerminalNotice_CharacterizationBeforeCreatorDelta` | **PASS inline / PARTIAL** — Heartbeat/loop exact last-20 retention passes; real task runs persist. Task retention limits/cleanup and merged dispatch entirety not exercised. |
| S-E.5 — Elapsed runtime alone never cancels work (780) | `TestPerKindRules_Preserved` | **PASS characterization** — All three kind fixtures: real context has no deadline, fake elapsed 301 s does not cancel, nonempty successful result and duration recorded. |
| S-F.1 — Unregistered loop owner is refused (790) | `TestLoopRunner_G5Protections/missing-owner` | **PASS** — Real loop runner returns owner-specific error without provider turn. |
| S-F.2 — Worker-owned loop is refused (the schedule runner's rule, universalized) (796) | `TestLoopRunner_G5Protections/worker-owner` | **RED** — Real worker-owned loop reaches the provider instead of refusing; tested independently of memory admission. |
| S-F.3 — Loop dispatch is subject to the same memory-governed admission (803) | `TestScheduledAdmission_MemoryGoverned` | **BLOCKED** — Shared memory gate binding absent; ordinary worker refusal is not treated as admission proof. |
| S-F.4 — Loop-spawned child processes are cleaned up (809) | `TestLoopRunner_G5Protections/S-F.4-child-cleanup-success; TestLoopRunner_G5Protections/S-F.4-child-cleanup-error` | **BLOCKED** — Real spawned-child/process tracker harness not bound; no child-termination oracle executed. |
| S-F.5 — Auto-denied tool in a loop run carries the job identity in audit (815) | `TestLoopRunner_G5Protections/S-F.5-audit-job-identity` | **BLOCKED** — Real auto-deny audit harness not bound; no audit record asserted. |
| S-G.1 — Every removed route answers the generic JSON 404 envelope (Outline) (823) | `TestSchedulesRoutes_Removed_AllGeneric404` | **RED** — All seven route shapes through actual production mux, authenticated and CSRF-valid. Retired handlers remain. |
| S-G.2 — Heartbeats keep working without the API (841) | `TestT10_HeartbeatsReDerivedAndFireWithoutSchedulesAPI` | **PASS internal / PARTIAL** — Two workspace schedules fire exact content/timing without schedules REST; recording runner does not prove actual ProcessScheduled/main-session resolution. |
| S-G.3 — Notifications REST is untouched (848) | `TestT10_TimerFailureStillReachesNotificationsREST` | **PASS callback-to-REST / PARTIAL** — Actual failure callback and authenticated notifications REST return two distinct diagnostics; not an actual failed timer turn end-to-end. |
| S-G.4 — Contract tree contains no Schedule wire types (854) | `TestT10_NoScheduleWireTypesOrConsumers` | **RED / GAP gate** — Generated contracts still contain Schedule types; generation/drift gate not run. |
| S-G.5 — Generic unknown-API handling with SPA control (860) | `TestUnknownAPI_JSON404_SPAControl` | **PASS** — Actual production mux canonical JSON 404 for invented API paths; ordinary route HTML and CSRF rejection controls pass. HTML is compile scaffold, not browser app. |
| S-G.6 — Automations endpoint is gone with the Schedule types; the redirect stays (T10-Q7 A) (867) | `TestSchedulesRoutes_Removed_AllGeneric404; TestT10_NoScheduleWireTypesOrConsumers` | **RED / PARTIAL** — Automations endpoint/types still present. Real SPA /automations redirect not browser-tested. |
| S-H.1 — Run-now consumes an armed once occurrence (875) | `TestT10_RESTRunNowConsumesArmedRegistration; TestT10_RunTaskRoutesScheduledBeforeStatusAndDoneGate` | **RED / PARTIAL** — Real REST run completes but marker/occurrence ownership missing and original once remains armed; tool route ordering fails. Full Calendar one-shot chip path not driven. |
| S-H.2 — Run-now on a recurring occurrence continues the series with the marker committed at admission (882) | `TestT10_RESTRunNowConsumesArmedRegistration` | **RED / PARTIAL** — Real recurring run lacks marker/occurrence context/progression; explicit armed future is rejected by old guard. Admission-commit barrier not instrumented. |
| S-H.3 — Manual task run-now is unchanged (control) (889) | `TestT10_RESTRunNowConsumesArmedRegistration/manual-explicit=false; TestT10_RunTaskRoutesScheduledBeforeStatusAndDoneGate/manual-inbox-control` | **PASS** — Real ordinary REST run completes Done with result/session and no marker; tool retains existing pre-status mutation in refusing-dispatch control. |
| S-H.4 — Consumed or past-skipped once runs ordinarily, arms nothing (895) | `TestT10_RunNowExplicitEligibilityAndDearmedRegistration/past-once-ordinary-control; TestT10_RunNowMarkerProgressionExactInstants` | **PASS past / BLOCKED consumed** — Past unarmed once runs ordinarily with no marker or armed next instant. Consumed-once twin needs T8/consumption backend. |
| S-H.5 — De-armed future trigger refuses run-now truthfully (902) | `TestT10_RunNowExplicitEligibilityAndDearmedRegistration/dearmed-future-refused` | **RED** — Actual scheduler de-arms live registration while task stays future; no-occurrence REST wrongly accepts and completes a real run. |
| S-H.6 — Run-now during a trigger change acts on the current registration only (909) | `TestT10_RunNowAdmissionAndT8Dependencies/D6-6-trigger-edit-race` | **BLOCKED** — Reservation/edit/current-registration interleave not implemented; no synthetic identity-resolution substitute. |
| S-H.7 — Restart before the original instant never resurrects a consumed occurrence (916) | `TestT10_RunNowMarkerProgressionExactInstants` | **BLOCKED** — Exact Monday consumption/Tuesday marker/Wednesday next assertions are declaration text, not executing restart/edit/COUNT behavior. |
| S-H.8 — Explicit occurrence_ms: armed consumes, past re-runs as today, mismatch refuses (923) | `TestT10_RESTRunNowConsumesArmedRegistration/recurring-explicit=true; TestT10_RunNowExplicitEligibilityAndDearmedRegistration` | **RED + PASS controls / PARTIAL** — Armed future is wrongly rejected; past historical rerun and other-future refusal controls pass, registration unchanged. Overdue-owned E1 and E0 binding incomplete. |
| S-H.9 — The actual caller is carried and validated before admission (929) | `TestT10_RunNowAdmissionAndT8Dependencies/D6-11-actual-starter-revoked; TestT10_RunNowAdmissionAndT8Dependencies/D6-12-person-provenance` | **BLOCKED** — Needs T8 actual caller/provenance and real queued gate; no claim from passing person-shaped fixture alone. |
| S-H.10 — Pre-admission failures leave the armed occurrence intact (936) | `TestT10_RunNowAdmissionAndT8Dependencies; TestT10_CancelledCallerPreAdmissionBinding` | **BLOCKED** — Refusal/cancel/shutdown/crash/marker-save boundaries not bound; no no-consumption or later automatic-fire proof. |
| S-H.11 — A recurring series whose last run is `done` advances via the contract (943) | `TestT10_RunTaskRoutesScheduledBeforeStatusAndDoneGate/recurring-done-advances-series; TestT10_RunTaskRoutesScheduledBeforeStatusAndDoneGate/ordinary-done-still-refused` | **RED tool / PARTIAL** — Real tool rejects recurring done before trigger route. Ordinary done refusal passes; real done-recurring consumed run not executed. |
| S-H.12 — Calendar reads the authoritative armed occurrence **[B-only]** (950) | `TestT10_OccurrencesHTTPProjectsLiveArmedTwinAndDearmedTwin; CalendarEventSlideOver.t10-red UI cases` | **RED / PARTIAL** — Real HTTP live/dearmed twins have equal recurrence/history but armed projection missing; generated Zod drops field, E1 absent, stale refusal no re-read. Full Go/TS codegen and consumed-restart twin not covered. |
| S-H.13 — The one-shot Calendar chip runs through the unified operation **[B-only]** (959) | `TaskDetailPanel.t10-red UI cases; TestT10_ScheduledTaskRejectsBothStatusActivationRoutesBeforeWrite` | **RED panel / GAP chip** — Actual once panel retains PATCH Start. Title/instruction observed; chip click, successful REST run-now and complete result/chat/history preservation not driven. |
| S-H.14 — An aggregated day bucket offers Run next armed occurrence **[B-only]** (967) | `CalendarEventSlideOver.t10-red bucket cases` | **RED panel / GAP chip** — Actual bucket panel lacks exactly-one Run next armed occurrence action. Outside-day absence passes. Chip click/day-scoped history retention not fully asserted. |
| S-H.15 — Run-now refuses while the collected automatic delivery is queued **[B-only]** (976) | `TestT10_RunNowAdmissionAndT8Dependencies/D6-16-E0-explicit; TestT10_RunNowAdmissionAndT8Dependencies/D6-17-E0-task-level` | **BLOCKED** — Needs real collected-queued automatic delivery, T8 ownership and pressure gate. E2/E4 ordinary controls cannot prove E0. |
| S-H.16 — Neither status route starts a schedule-bearing task **[B-only]** (985) | `TestT10_ScheduledTaskRejectsBothStatusActivationRoutesBeforeWrite; TaskDetailPanel.t10-red status cases` | **RED / PARTIAL** — Both actual REST PATCHes mutate scheduled files; in_progress starts a run; selector retains forbidden options. Manual selector control passes; ordinary Board drain not driven. |
| S-I.1 — Same-workspace creator receives the assignee's notice once (994) | `TestT10_CreatorNoticeFanOutAtRealRunCompletion/D7-1-same-workspace-success` | **RED** — Real stored scheduled run/CAS/inbox: captured notices full, eligible creator copy absent. |
| S-I.2 — Creator equals assignee sends no duplicate (1001) | `TestT10_CreatorNoticeFanOutAtRealRunCompletion/D7-2-creator-is-assignee` | **PASS** — Exactly one full inbox notice, recipients unchanged/max two, repeated completion adds no message. |
| S-I.3 — Creator equals starter dedupes against captured recipients (1007) | `TestT10_CreatorNoticeFanOutAtRealRunCompletion/D7-3-creator-is-starter` | **PASS** — Captured-recipient dedupe and full content survive actual completion retry. |
| S-I.4 — Person-created task sends no creator notice (1013) | `TestT10_CreatorNoticeFanOutAtRealRunCompletion/D7-4-person-username-collides-with-agent` | **PASS** — Actual human creation with colliding username produces no creator notice; captured recipients still receive full notice. |
| S-I.5 — Foreign-workspace creator receives no completion notice (boundary preserved) (1019) | `TestT10_CreatorNoticeFanOutAtRealRunCompletion/D7-5-foreign-creator-no-push; TestT10_InheritedT8OwnershipAndWaiting` | **PASS no push / BLOCKED pull** — Real foreign-workspace creator receives no completion/result push; inherited status-only pull still needs T8. |
| S-I.6 — Failed occurrence notifies the eligible creator too (1026) | `TestT10_CreatorNoticeFanOutAtRealRunCompletion/D7-6-failed-run` | **RED** — Real failed run stores exact failure and sends captured copies; owed creator copy absent. |
| S-I.7 — Skipped occurrence notifies no one (1032) | `TestT10_CreatorNoticeScheduledLifecycleDependencies/D7-7-skipped-no-run` | **BLOCKED** — Actual skipped/no-run path needs T8; no nil-run terminal callback used as substitute. |
| S-I.8 — Delivered-but-held occurrence notifies on actual dispatch (1038) | `TestT10_CreatorNoticeScheduledLifecycleDependencies/D7-8-delivered-held-then-dispatched` | **BLOCKED** — Delivered-held/release lifecycle not implemented in fixture; no notice timing proof. |
| S-I.9 — A CAS-rejected stale outcome notifies no one; the authoritative terminal outcome notifies once (1044) | `TestTaskTerminalNotice_CharacterizationBeforeCreatorDelta; TestT10_CreatorNoticeFanOutAtRealRunCompletion/D7-9-authoritative-stopped-not-late-success; TestT10_CreatorNoticeFanOutAtRealRunCompletion/D7-10-nonterminal-conflict` | **RED delta + PASS baseline** — Real Stop wins CAS: captured authoritative stopped notice once, rejected late success absent; nonterminal sends none. Creator Stop copy missing. |
| S-I.10 — An unresolvable creator main records a notice loss (1051) | `TestT10_CreatorNoticeFanOutAtRealRunCompletion/D7-12-unresolvable-creator-main` | **RED** — Real resolver failure does not increment notice-loss counter or write visible loss line; captured notices still intact. |
| S-J.1 — Dispatch-failure reason is plain in every user sink; credentials absent everywhere (1059) | `TestT10_NotDispatchedDiagnosticsStayOutOfEveryUserSink` | **RED** — All five D-8 rows executed through real executor; task, paired goal and chat render raw diagnostics. Registered value scrubbed in actual operator log. |
| S-J.2 — Recurring save with a run in flight stops blaming the view (1066) | `TestT10_RecurringSaveWithRealRunInFlightNamesActualBlocker; CalendarEventSlideOver.t10-red save cases; T10 #1243 Playwright row` | **PASS fixture / BLOCKED browser** — Real held-worker After N full save succeeds; six real-editor request controls pass. Browser row collected only, no live fixture/build, original reported race not claimed fixed. |
| S-J.3 — No-run-in-flight controls unchanged (1072) | `TestT10_RecurringSaveWithRealRunInFlightNamesActualBlocker; CalendarEventSlideOver.t10-red save cases` | **PASS controls / PARTIAL** — On date and once-conversion saves succeed while real run held; six UI running/nonrunning request controls pass. No-run backend race/full browser matrix not exercised. |
| S-K.1 — Persisted `agent_turn` residue is dropped at load, loudly, once (1080) | `TestT10_NonRegisteredKindsDroppedOnceAtLoad; TestT10_LoadDropLoggingNamesEveryJobExactlyOnce` | **RED** — Actual current-format future agent_turn residue retained/missing once-per-job warning; future no-dispatch check fails or remains under failing cleanup, not a migration marker. |
| S-K.2 — Auxiliary files are never opened (no-access invariant) (1087) | `TestT10_AuxiliarySchedulerFilesAreNeverOpened` | **RED** — Linux inotify observes forbidden reads/writes/replacement for both seeded auxiliary paths; post-observation bytes also compared. |
| S-K.3 — Heartbeats are unaffected (1094) | `TestT10_HeartbeatsReDerivedAndFireWithoutSchedulesAPI; TestT10_InheritedT8OwnershipAndWaiting` | **PASS internal / PARTIAL** — Settings-derived exact heartbeat content/timing pass; real physical restart and task-series re-derivation/once count remain gaps. |

## Dataset row accounting

Each dataset row is accounted for below. “Blocked” means its asserted behavior is not executed, even if a named table row exists. Range rows from the spec are expanded where necessary.

| Dataset row | Evidence | Status / unproved portion |
|---|---|---|
| D-1.1 | `TestPerKindRules_Preserved` | PASS engine backoff/inline retention; actual heartbeat ProcessScheduled/main delivery remains partial |
| D-1.2 | `TestT10_TaskOccurrenceFailureUsesNextBoundaryNotBackoff` | PASS handler progression with real recurrence, not full async task history/retention |
| D-1.3 | `TestT10_LoopFailureRecordsThenCarriesOnAtNextInterval; TestPerKindRules_Preserved` | PASS real loop error/next interval and inline retention |
| D-1.4 | `TestT10_NonRegisteredKindsDroppedOnceAtLoad; TestT10_LoadDropLoggingNamesEveryJobExactlyOnce` | RED actual load/drop/log failures; kind validation also blocked by missing creating field |
| D-1.5 | `TestT10_NonRegisteredKindsDroppedOnceAtLoad; TestT10_LoadDropLoggingNamesEveryJobExactlyOnce` | RED actual load/drop/log failures; kind validation also blocked by missing creating field |
| D-1.6 | `TestT10_RESTRunNowConsumesArmedRegistration; existing TestTriggerOnce_FiresExactlyOnce` | PARTIAL: original once controls and run-now still armed failure; no isolated merged-engine DeleteAfterRun lifecycle test |
| D-1.7 | `TestScheduledAdmission_MemoryGoverned` | BLOCKED shared admission and async handler lifetime |
| D-1.8 | `TestPerKindRules_Preserved` | PASS all kind fixtures, no deadline, 301 s and nonempty recorded completion |
| D-2.1 | `TestScheduledAdmission_MemoryGoverned` | BLOCKED memory seam; unknown-kind mixed floor, async-return boundary, no-preemption and once-per-wait log assertions incomplete |
| D-2.2 | `TestT10_SchedulesCapacityKeyIsIgnoredAndNotSeeded; TestT10_NoFixedLaneLimitRemains` | RED unset/0/negative/8 parse/marshal/field/default checks; actual mixed admission for each config remains BLOCKED |
| D-2.3 | `TestScheduledAdmission_MemoryGoverned` | BLOCKED memory seam; unknown-kind mixed floor, async-return boundary, no-preemption and once-per-wait log assertions incomplete |
| D-2.4 | `TestScheduledAdmission_MemoryGoverned` | BLOCKED memory seam; unknown-kind mixed floor, async-return boundary, no-preemption and once-per-wait log assertions incomplete |
| D-2.5 | `TestScheduledAdmission_MemoryGoverned` | BLOCKED memory seam; unknown-kind mixed floor, async-return boundary, no-preemption and once-per-wait log assertions incomplete |
| D-3.1 | `TestT10_InheritedT8OwnershipAndWaiting` | BLOCKED needs T8, exact T0 task boot/fire/skipped table not executed; 6b started-interrupted once exception remains required |
| D-3.2 | `TestT10_InheritedT8OwnershipAndWaiting` | BLOCKED needs T8, exact T0 task boot/fire/skipped table not executed; 6b started-interrupted once exception remains required |
| D-3.3 | `TestT10_InheritedT8OwnershipAndWaiting` | BLOCKED needs T8, exact T0 task boot/fire/skipped table not executed; 6b started-interrupted once exception remains required |
| D-3.4 | `TestT10_RunNowMarkerProgressionExactInstants` | BLOCKED exact Tue marker/Wed first boundary only in declaration, not behavior |
| D-3.5 | `TestLoopBootReconcile_ReArmsOverdueOneShot; TestLoopBootReconcile_OwnershipAndCrashWindows` | BLOCKED recovery; real self-paced initial delay assertion RED, not exact T0 restart proof |
| D-3.6 | `TestLoopBootReconcile_ReArmsOverdueOneShot; TestLoopBootReconcile_OwnershipAndCrashWindows` | BLOCKED recovery; real self-paced initial delay assertion RED, not exact T0 restart proof |
| D-3.7 | `TestT10_HeartbeatsReDerivedAndFireWithoutSchedulesAPI` | PASS exact internal derivation/timing, PARTIAL physical restart |
| D-3.8 | `TestT10_NonRegisteredKindsDroppedOnceAtLoad; TestT10_LoadDropLoggingNamesEveryJobExactlyOnce` | RED real persisted residue/drop/log; T0 fixed boot-table variant not separately driven |
| D-3.9 | `TestT10_GatewayBootCheckedLoadPrecedesAllWrites` | PASS original typed error, byte-identical file, no hosted scheduler starts |
| D-3b.1 | `TestLoopBootReconcile_OwnershipAndCrashWindows` | BLOCKED recovery hook; limit-minus-one exact execution, nil-next ordering and same-boot interval/self-paced twin incomplete |
| D-3b.2 | `TestLoopBootReconcile_OwnershipAndCrashWindows` | BLOCKED recovery hook; limit-minus-one exact execution, nil-next ordering and same-boot interval/self-paced twin incomplete |
| D-3b.3 | `TestLoopBootReconcile_OwnershipAndCrashWindows` | BLOCKED recovery hook; limit-minus-one exact execution, nil-next ordering and same-boot interval/self-paced twin incomplete |
| D-3b.4 | `TestLoopBootReconcile_OwnershipAndCrashWindows` | BLOCKED recovery hook; limit-minus-one exact execution, nil-next ordering and same-boot interval/self-paced twin incomplete |
| D-3b.5 | `TestT10_LoopStopAndReplaceRemoveEverySessionJob; TestLoopBootReconcile_OwnershipAndCrashWindows` | RED actual session cleanup, PARTIAL after-recovery precursor because recovery unavailable |
| D-3b.6 | `TestT10_LoopStopAndReplaceRemoveEverySessionJob; TestLoopBootReconcile_OwnershipAndCrashWindows` | RED actual session cleanup, PARTIAL after-recovery precursor because recovery unavailable |
| D-3b.7 | `TestT10_LoopRecoveryPersistenceFailures` | BLOCKED single/sustained/repeat/next-boot write injection not bound; separate failure types not simulated |
| D-3b.8 | `TestLoopBootReconcile_OwnershipAndCrashWindows` | BLOCKED recovery hook; limit-minus-one exact execution, nil-next ordering and same-boot interval/self-paced twin incomplete |
| D-3b.9 | `TestLoopBootReconcile_OwnershipAndCrashWindows` | BLOCKED recovery hook; limit-minus-one exact execution, nil-next ordering and same-boot interval/self-paced twin incomplete |
| D-3b.10 | `TestLoopBootReconcile_OwnershipAndCrashWindows` | BLOCKED recovery hook; limit-minus-one exact execution, nil-next ordering and same-boot interval/self-paced twin incomplete |
| D-3b.11 | `TestT10_LoopRecoveryPersistenceFailures` | BLOCKED single/sustained/repeat/next-boot write injection not bound; separate failure types not simulated |
| D-3b.12 | `TestT10_LoopRecoveryPersistenceFailures` | BLOCKED single/sustained/repeat/next-boot write injection not bound; separate failure types not simulated |
| D-3b.13 | `TestLoopBootReconcile_OwnershipAndCrashWindows` | BLOCKED recovery hook; limit-minus-one exact execution, nil-next ordering and same-boot interval/self-paced twin incomplete |
| D-3b.14 | `TestT10_LoopRevokedCallbackHasNoPostTurnEffects` | BLOCKED exact post-return race and state/frame oracle not implemented |
| D-3b.15 | `TestT10_LoopRevokedCallbackHasNoPostTurnEffects` | BLOCKED exact post-return race and state/frame oracle not implemented |
| D-3b.16 | `TestLoopBootReconcile_OwnershipAndCrashWindows` | BLOCKED recovery hook; limit-minus-one exact execution, nil-next ordering and same-boot interval/self-paced twin incomplete |
| D-3b.17 | `TestT10_LoopRecoveryPersistenceFailures` | BLOCKED single/sustained/repeat/next-boot write injection not bound; separate failure types not simulated |
| D-4.1 | `TestT10_AllProducersCarryKindOnCreatingSpec` | RED creating literal omits Kind; GAP real first-write count/content observation |
| D-4.2 | `TestT10_AllProducersCarryKindOnCreatingSpec` | RED creating literal omits Kind; GAP real first-write count/content observation |
| D-4.3 | `TestT10_AllProducersCarryKindOnCreatingSpec` | RED creating literal omits Kind; GAP real first-write count/content observation |
| D-4.4 | `TestT10_AllProducersCarryKindOnCreatingSpec; TestT10_RealTaskCreatingRegistrationKind` | RED AST plus actual task-notification final disk Kind; first-write-only observer still GAP |
| D-4.5 | `TestJobSpecKind_RequiredAndKnown` | BLOCKED missing JobSpec.Kind; no pretend create-then-stamp implementation |
| D-5.1 | `TestSchedulesRoutes_Removed_AllGeneric404` | RED seven actual production-mux route shapes |
| D-5.2 | `TestSchedulesRoutes_Removed_AllGeneric404` | RED seven actual production-mux route shapes |
| D-5.3 | `TestSchedulesRoutes_Removed_AllGeneric404` | RED seven actual production-mux route shapes |
| D-5.4 | `TestSchedulesRoutes_Removed_AllGeneric404` | RED seven actual production-mux route shapes |
| D-5.5 | `TestSchedulesRoutes_Removed_AllGeneric404` | RED seven actual production-mux route shapes |
| D-5.6 | `TestSchedulesRoutes_Removed_AllGeneric404` | RED seven actual production-mux route shapes |
| D-5.7 | `TestSchedulesRoutes_Removed_AllGeneric404` | RED seven actual production-mux route shapes |
| D-5.8 | `TestUnknownAPI_JSON404_SPAControl` | PASS canonical JSON404, HTML and CSRF controls |
| D-5.9 | `TestSchedulesRoutes_Removed_AllGeneric404; TestT10_NoScheduleWireTypesOrConsumers` | RED endpoint/types; GAP browser redirect |
| D-5.10 | `TestT10_HeartbeatsReDerivedAndFireWithoutSchedulesAPI` | PASS internal exact heartbeat timing/content; actual owner/main runner binding partial |
| D-5.11 | `TestT10_TimerFailureStillReachesNotificationsREST` | PASS distinct real notifications REST content; source is actual callback, not real failed timer turn |
| D-5.12 | `TestT10_NoScheduleWireTypesOrConsumers` | RED generated Schedule tree; verify-contracts NOT RUN |
| D-6.1 | `TestT10_RESTRunNowConsumesArmedRegistration; TestT10_RunTaskRoutesScheduledBeforeStatusAndDoneGate` | PASS real manual completion and tool ordering control; full UI ordinary paths not all driven |
| D-6.2 | `TestT10_RESTRunNowConsumesArmedRegistration; TestT10_RunTaskRoutesScheduledBeforeStatusAndDoneGate` | RED real REST marker/context/armed-state and tool ordering; admission-barrier/whole UI consume proof incomplete |
| D-6.3 | `TestT10_RESTRunNowConsumesArmedRegistration; TestT10_RunTaskRoutesScheduledBeforeStatusAndDoneGate` | RED real REST marker/context/armed-state and tool ordering; admission-barrier/whole UI consume proof incomplete |
| D-6.4 | `TestT10_RunNowExplicitEligibilityAndDearmedRegistration` | PASS past unarmed once; consumed-once twin BLOCKED |
| D-6.5 | `TestT10_RunNowExplicitEligibilityAndDearmedRegistration` | RED actual future dearmed entry dispatches |
| D-6.6 | `TestT10_RunNowAdmissionAndT8Dependencies; TestT10_CancelledCallerPreAdmissionBinding` | BLOCKED T8/admission/current-identity/plan/auth/provenance/reservation/E0 hooks, no real race proof |
| D-6.7 | `TestT10_RunNowAdmissionAndT8Dependencies; TestT10_CancelledCallerPreAdmissionBinding` | BLOCKED T8/admission/current-identity/plan/auth/provenance/reservation/E0 hooks, no real race proof |
| D-6.8 | `TestT10_RESTRunNowConsumesArmedRegistration/recurring-explicit=true` | RED old future guard refuses armed equality; overdue-owned twin not driven |
| D-6.9 | `TestT10_RunNowExplicitEligibilityAndDearmedRegistration` | PASS historical real run and future-other refusal, no marker/registration mutation |
| D-6.10 | `TestT10_RunNowExplicitEligibilityAndDearmedRegistration` | PASS historical real run and future-other refusal, no marker/registration mutation |
| D-6.11 | `TestT10_RunNowAdmissionAndT8Dependencies; TestT10_CancelledCallerPreAdmissionBinding` | BLOCKED T8/admission/current-identity/plan/auth/provenance/reservation/E0 hooks, no real race proof |
| D-6.12 | `TestT10_RunNowAdmissionAndT8Dependencies; TestT10_CancelledCallerPreAdmissionBinding` | BLOCKED T8/admission/current-identity/plan/auth/provenance/reservation/E0 hooks, no real race proof |
| D-6.13 | `TestT10_RunNowAdmissionAndT8Dependencies; TestT10_CancelledCallerPreAdmissionBinding` | BLOCKED T8/admission/current-identity/plan/auth/provenance/reservation/E0 hooks, no real race proof |
| D-6.14 | `TestT10_RESTRunNowConsumesArmedRegistration; TestT10_RunTaskRoutesScheduledBeforeStatusAndDoneGate` | RED real REST marker/context/armed-state and tool ordering; admission-barrier/whole UI consume proof incomplete |
| D-6.15 | `TaskDetailPanel.t10-red UI cases; TestT10_ScheduledTaskRejectsBothStatusActivationRoutesBeforeWrite` | RED panel Start/status routes; GAP real fire-chip click and successful panel run-now request |
| D-6.16 | `TestT10_RunNowAdmissionAndT8Dependencies; TestT10_CancelledCallerPreAdmissionBinding` | BLOCKED T8/admission/current-identity/plan/auth/provenance/reservation/E0 hooks, no real race proof |
| D-6.17 | `TestT10_RunNowAdmissionAndT8Dependencies; TestT10_CancelledCallerPreAdmissionBinding` | BLOCKED T8/admission/current-identity/plan/auth/provenance/reservation/E0 hooks, no real race proof |
| D-7.1 | `TestT10_CreatorNoticeFanOutAtRealRunCompletion` | RED real creator delta; captured authoritative notices/store/readback intact |
| D-7.2 | `TestT10_CreatorNoticeFanOutAtRealRunCompletion` | PASS real dedupe/person/nonterminal controls and no added notice on completion retry |
| D-7.3 | `TestT10_CreatorNoticeFanOutAtRealRunCompletion` | PASS real dedupe/person/nonterminal controls and no added notice on completion retry |
| D-7.4 | `TestT10_CreatorNoticeFanOutAtRealRunCompletion` | PASS real dedupe/person/nonterminal controls and no added notice on completion retry |
| D-7.5 | `TestT10_CreatorNoticeFanOutAtRealRunCompletion` | PASS real foreign no-push; status-only pull BLOCKED needs T8 |
| D-7.6 | `TestT10_CreatorNoticeFanOutAtRealRunCompletion` | RED real creator delta; captured authoritative notices/store/readback intact |
| D-7.7 | `TestT10_CreatorNoticeScheduledLifecycleDependencies` | BLOCKED actual skipped/held/run-now lifecycle, not nil-run shortcut |
| D-7.8 | `TestT10_CreatorNoticeScheduledLifecycleDependencies` | BLOCKED actual skipped/held/run-now lifecycle, not nil-run shortcut |
| D-7.9 | `TestT10_CreatorNoticeFanOutAtRealRunCompletion` | RED real creator delta; captured authoritative notices/store/readback intact |
| D-7.10 | `TestT10_CreatorNoticeFanOutAtRealRunCompletion` | PASS real dedupe/person/nonterminal controls and no added notice on completion retry |
| D-7.11 | `TestT10_CreatorNoticeScheduledLifecycleDependencies` | BLOCKED actual skipped/held/run-now lifecycle, not nil-run shortcut |
| D-7.12 | `TestT10_CreatorNoticeFanOutAtRealRunCompletion` | RED real creator delta; captured authoritative notices/store/readback intact |
| D-8.1 | `TestT10_NotDispatchedDiagnosticsStayOutOfEveryUserSink` | RED actual task/goal/chat raw error wrapper; registered/unknown/env/hook/plain rows all executed; operator cause retained and registered credential scrubbed |
| D-8.2 | `TestT10_NotDispatchedDiagnosticsStayOutOfEveryUserSink` | RED actual task/goal/chat raw error wrapper; registered/unknown/env/hook/plain rows all executed; operator cause retained and registered credential scrubbed |
| D-8.3 | `TestT10_NotDispatchedDiagnosticsStayOutOfEveryUserSink` | RED actual task/goal/chat raw error wrapper; registered/unknown/env/hook/plain rows all executed; operator cause retained and registered credential scrubbed |
| D-8.4 | `TestT10_NotDispatchedDiagnosticsStayOutOfEveryUserSink` | RED actual task/goal/chat raw error wrapper; registered/unknown/env/hook/plain rows all executed; operator cause retained and registered credential scrubbed |
| D-8.5 | `TestT10_NotDispatchedDiagnosticsStayOutOfEveryUserSink` | RED actual task/goal/chat raw error wrapper; registered/unknown/env/hook/plain rows all executed; operator cause retained and registered credential scrubbed |
| D-9.1 | `TestT10_RecurringSaveWithRealRunInFlightNamesActualBlocker; CalendarEventSlideOver.t10-red save cases; T10 #1243 Playwright row` | PASS real held-worker full save and UI request; browser only COLLECTED, not run; does not reproduce every reported live race |
| D-9.2 | `CalendarEventSlideOver.t10-red save cases` | PASS UI After-N no-run request/control; GAP real no-run backend paired matrix |
| D-9.3 | `TestT10_RecurringSaveWithRealRunInFlightNamesActualBlocker; CalendarEventSlideOver.t10-red save cases` | PASS real held-worker On-date/once-conversion and running/no-running UI transport controls; full browser matrix not executed |
| D-9.4 | `TestT10_RecurringSaveWithRealRunInFlightNamesActualBlocker; CalendarEventSlideOver.t10-red save cases` | PASS real held-worker On-date/once-conversion and running/no-running UI transport controls; full browser matrix not executed |
| D-10.1 | `TestT10_NonRegisteredKindsDroppedOnceAtLoad; TestT10_LoadDropLoggingNamesEveryJobExactlyOnce` | RED actual current-format residue; no migration/version-marker substitute |
| D-10.2 | `TestT10_AuxiliarySchedulerFilesAreNeverOpened` | RED actual inotify no-read/no-write invariant and byte comparison |
| D-10.3 | `TestT10_AuxiliarySchedulerFilesAreNeverOpened` | RED actual inotify no-read/no-write invariant and byte comparison |
| D-10.4 | `TestT10_HeartbeatsReDerivedAndFireWithoutSchedulesAPI` | PASS internal timing/content; full restart partial |
| D-10.5 | `TestT10_InheritedT8OwnershipAndWaiting` | BLOCKED recurring boot rederive + exact single fire on merged engine |
| D-11.1 | `TestT10_OccurrencesHTTPProjectsLiveArmedTwinAndDearmedTwin; CalendarEventSlideOver.t10-red E1/twin cases` | RED real HTTP and real query/schema/control; consumed-restart twin absent |
| D-11.2 | `CalendarEventSlideOver.t10-red bucket cases` | RED actual panel action missing; outside-day absence PASS; GAP full aggregated-chip click and day-history assertion |
| D-11.3 | `TestT10_RunNowMarkerProgressionExactInstants` | BLOCKED consumed-before-launch crash/restart projection/render |
| D-11.4 | `CalendarEventSlideOver.t10-red E2 case` | PASS actual editor control issues real-client POST for past no-run occurrence |
| D-11.5 | `CalendarEventSlideOver.t10-red stale-click case` | RED refusal observed through actual client; occurrence HTTP query not refreshed; no real backend trigger-edit race |
| D-11.6 | `CalendarEventSlideOver.t10-red generated-schema case; TestT10_NoScheduleWireTypesOrConsumers` | RED generated Zod strips live/null field; GAP additive Go/TS type checks and verify-contracts |
| D-11.7 | `TaskDetailPanel.t10-red one-shot case; TestT10_ScheduledTaskRejectsBothStatusActivationRoutesBeforeWrite` | RED retained PATCH Start and actual HTTP mutations; GAP fire-chip-through-run-now click and full preserved surfaces |
| D-11.8 | `CalendarEventSlideOver.t10-red E1/E2/E3 cases; TestT10_RunNowExplicitEligibilityAndDearmedRegistration` | RED armed future control absent, PASS E2 real-client request and E3 hidden/refused; real server equality covered by separate failing REST test |
| D-11.9 | `TaskDetailPanel.t10-red selectors; TestT10_ScheduledTaskRejectsBothStatusActivationRoutesBeforeWrite` | RED scheduled selectors and both HTTP writes; PASS manual selector; GAP ordinary Board drain |

## Functional requirement traceability

| Requirement (r6 line) | Scenario evidence | Status / gate |
|---|---|---|
| T10-FR-01 (1493) | S-A.1, S-A.2, S-A.4 | S-A.1: RED / PARTIAL; S-A.2: BLOCKED; S-A.4: BLOCKED |
| T10-FR-02 (1494) | S-A.3, S-D.1 | S-A.3: RED + BLOCKED; S-D.1: RED / PARTIAL |
| T10-FR-03 (1495) | S-B.1, S-B.2, S-B.3, S-B.4 | S-B.1: BLOCKED / PARTIAL; S-B.2: BLOCKED; S-B.3: PASS; S-B.4: BLOCKED |
| T10-FR-04 (1496) | S-A.1, S-K.2 | S-A.1: RED / PARTIAL; S-K.2: RED |
| T10-FR-05 (1497) | S-B.5, S-B.6, S-B.7, S-B.8, S-B.9, S-B.10 | S-B.5: RED / BLOCKED; S-B.6: BLOCKED / PARTIAL; S-B.7: BLOCKED; S-B.8: BLOCKED / PARTIAL; S-B.9: BLOCKED; S-B.10: BLOCKED |
| T10-FR-06 (1498) | S-C.1, S-C.2 | S-C.1: PASS base / PARTIAL merged; S-C.2: PASS harness / PARTIAL |
| T10-FR-07 (1499) | S-D.2, S-D.3, S-D.4, S-D.5 | S-D.2: RED / BLOCKED; S-D.3: BLOCKED; S-D.4: BLOCKED; S-D.5: GAP |
| T10-FR-08 (1500) | S-E.1, S-E.2, S-E.3, S-E.5 | S-E.1: PASS characterization; S-E.2: PASS handler / PARTIAL; S-E.3: PASS characterization; S-E.5: PASS characterization |
| T10-FR-09 (1501) | S-E.4 + D-1 rows | S-E.4: PASS inline / PARTIAL |
| T10-FR-10 (1502) | S-F.1–S-F.5 | S-F.1: PASS; S-F.2: RED; S-F.3: BLOCKED; S-F.4: BLOCKED; S-F.5: BLOCKED |
| T10-FR-11 (1503) | D-1 rows 1, 3 | PARTIAL — D-1.1/3 inline retention/timing, loop real failure passes; actual heartbeat owner/main delivery incomplete |
| T10-FR-12 (1504) | S-G.1, S-G.2, S-G.3, S-G.5, S-G.6 | S-G.1: RED; S-G.2: PASS internal / PARTIAL; S-G.3: PASS callback-to-REST / PARTIAL; S-G.5: PASS; S-G.6: RED / PARTIAL |
| T10-FR-13 (1505) | S-G.4; **[B-only]** S-H.12 (the additive field) | S-G.4: RED / GAP gate; S-H.12: RED / PARTIAL |
| T10-FR-14 (1506) | S-K.1–S-K.3 | S-K.1: RED; S-K.2: RED; S-K.3: PASS internal / PARTIAL |
| T10-FR-15 (1507) | S-B.2 (+ D-3 rows) | S-B.2: BLOCKED |
| T10-FR-16 (1508) | S-H.1–S-H.5, S-H.7, S-H.8, S-H.11–S-H.16 | S-H.1: RED / PARTIAL; S-H.2: RED / PARTIAL; S-H.3: PASS; S-H.4: PASS past / BLOCKED consumed; S-H.5: RED; S-H.7: BLOCKED; S-H.8: RED + PASS controls / PARTIAL; S-H.11: RED tool / PARTIAL; S-H.12: RED / PARTIAL; S-H.13: RED panel / GAP chip; S-H.14: RED panel / GAP chip; S-H.15: BLOCKED; S-H.16: RED / PARTIAL |
| T10-FR-17 (1509) | S-H.6, S-H.9, S-H.10, S-H.15 | S-H.6: BLOCKED; S-H.9: BLOCKED; S-H.10: BLOCKED; S-H.15: BLOCKED |
| T10-FR-18 (1510) | S-I.1, S-I.5, S-I.6, S-I.8, S-I.9, S-I.10 | S-I.1: RED; S-I.5: PASS no push / BLOCKED pull; S-I.6: RED; S-I.8: BLOCKED; S-I.9: RED delta + PASS baseline; S-I.10: RED |
| T10-FR-19 (1511) | S-I.2–S-I.4, S-I.7 | S-I.2: PASS; S-I.3: PASS; S-I.4: PASS; S-I.7: BLOCKED |
| T10-FR-20 (1512) | S-J.1 | S-J.1: RED |
| T10-FR-21 (1513) | S-J.2, S-J.3 | S-J.2: PASS fixture / BLOCKED browser; S-J.3: PASS controls / PARTIAL |
| T10-FR-22 (1514) | S-A.4 | S-A.4: BLOCKED |
| T10-FR-23 (1515) | T8 18.30/18.34/18.37/18.39/18.40 equivalents | BLOCKED — T8 12b–12f and 18.30/18.34/18.37/18.39/18.40 declarations only, not a rerun of missing backend |
| T10-FR-24 (1516) | — | NOT RUN — release docs/SC-15 check at the cut; not a test-author production edit |
| T10-FR-25 (1517) | S-A.5, S-A.3, S-K.1 | S-A.5: RED / PARTIAL; S-A.3: RED + BLOCKED; S-K.1: RED |
| T10-FR-26 (1518) | S-C.3, S-D.5 | S-C.3: BLOCKED; S-D.5: GAP |

## Test file inventory

| File | Authored top-level Go functions / UI role |
|---|---|
| `/workspace/wt-t10/pkg/agent/t10_creator_notice_red_test.go` | `TestT10_CreatorNoticeFanOutAtRealRunCompletion`; `TestT10_CreatorNoticeScheduledLifecycleDependencies` |
| `/workspace/wt-t10/pkg/agent/t10_dispatch_redaction_test.go` | `TestT10_NotDispatchedDiagnosticsStayOutOfEveryUserSink` |
| `/workspace/wt-t10/pkg/agent/t10_loop_failure_test.go` | `TestT10_LoopFailureRecordsThenCarriesOnAtNextInterval` |
| `/workspace/wt-t10/pkg/agent/t10_loop_red_test.go` | `TestLoopBootReconcile_ReArmsOverdueOneShot`; `TestLoopBootReconcile_OwnershipAndCrashWindows`; `TestT10_LoopRevokedCallbackHasNoPostTurnEffects`; `TestT10_LoopNonSelectedJobRunsNoTurnAndRemovesItself`; `TestT10_LoopRecoveryPersistenceFailures`; `TestLoopRunner_G5Protections`; `TestT10_LoopStopAndReplaceRemoveEverySessionJob` |
| `/workspace/wt-t10/pkg/agent/t10_notice_baseline_test.go` | `TestTaskTerminalNotice_CharacterizationBeforeCreatorDelta` |
| `/workspace/wt-t10/pkg/agent/t10_red_scaffolding_test.go` | RED binding/helper scaffolding only; not a test count |
| `/workspace/wt-t10/pkg/agent/t10_task_merge_red_test.go` | `TestT10_TaskReconcilePreservesForeignKindsByteForByte`; `TestT10_TaskOccurrenceFailureUsesNextBoundaryNotBackoff`; `TestT10_InheritedT8OwnershipAndWaiting` |
| `/workspace/wt-t10/pkg/config/t10_schedules_red_test.go` | `TestT10_SchedulesCapacityKeyIsIgnoredAndNotSeeded`; `TestT10_NoSchedulesCapacityDefaultConstant` |
| `/workspace/wt-t10/pkg/cron/t10_engine_red_test.go` | `TestJobSpecKind_RequiredAndKnown`; `TestT10_NonRegisteredKindsDroppedOnceAtLoad`; `TestT10_LoadDropLoggingNamesEveryJobExactlyOnce`; `TestDispatcher_RoutesByKind_Exclusively`; `TestScheduledAdmission_MemoryGoverned`; `TestT10_NoFixedLaneLimitRemains`; `TestPerKindRules_Preserved` |
| `/workspace/wt-t10/pkg/cron/t10_red_scaffolding_test.go` | RED binding/helper scaffolding only; not a test count |
| `/workspace/wt-t10/pkg/gateway/t10_auxiliary_noaccess_linux_test.go` | `TestT10_AuxiliarySchedulerFilesAreNeverOpened` |
| `/workspace/wt-t10/pkg/gateway/t10_calendar_rider_red_test.go` | `TestT10_OccurrencesHTTPProjectsLiveArmedTwinAndDearmedTwin`; `TestT10_RecurringSaveWithRealRunInFlightNamesActualBlocker` |
| `/workspace/wt-t10/pkg/gateway/t10_engine_boot_red_test.go` | `TestT10_GatewayBootUsesExactlyOneEngineAndSharedStore`; `TestT10_GatewayBootCheckedLoadPrecedesAllWrites`; `TestReload_DoesNotRestartEngineOrStrandRunning`; `TestT10_BootReconciliationsFinishBeforeFirstCollection` |
| `/workspace/wt-t10/pkg/gateway/t10_heartbeat_red_test.go` | `TestT10_HeartbeatsReDerivedAndFireWithoutSchedulesAPI`; `TestT10_HeartbeatReconcilePreservesForeignKindsByteForByte` |
| `/workspace/wt-t10/pkg/gateway/t10_producers_red_test.go` | `TestT10_AllProducersCarryKindOnCreatingSpec` |
| `/workspace/wt-t10/pkg/gateway/t10_routes_red_test.go` | `TestSchedulesRoutes_Removed_AllGeneric404`; `TestUnknownAPI_JSON404_SPAControl`; `TestT10_TimerFailureStillReachesNotificationsREST`; `TestT10_NoScheduleWireTypesOrConsumers` |
| `/workspace/wt-t10/pkg/gateway/t10_run_now_red_test.go` | `TestT10_RealTaskCreatingRegistrationKind`; `TestT10_RESTRunNowConsumesArmedRegistration`; `TestT10_ScheduledTaskRejectsBothStatusActivationRoutesBeforeWrite`; `TestT10_RunNowExplicitEligibilityAndDearmedRegistration`; `TestT10_RunNowAdmissionAndT8Dependencies`; `TestT10_RunNowMarkerProgressionExactInstants`; `TestT10_CancelledCallerPreAdmissionBinding` |
| `/workspace/wt-t10/pkg/gateway/t10_shutdown_test.go` | `TestShutdown_EngineStopsBeforeDrain` |
| `/workspace/wt-t10/pkg/tools/t10_run_task_red_test.go` | `TestT10_RunTaskRoutesScheduledBeforeStatusAndDoneGate` |
| `/workspace/wt-t10/src/components/calendar/CalendarEventSlideOver.t10-red.test.tsx` | 15 UI cases: authoritative HTTP query/Zod/control (live and null checked independently) and six rider request controls |
| `/workspace/wt-t10/src/components/workspaces/TaskDetailPanel.t10-red.test.tsx` | 4 UI cases: real panel status/one-shot controls |
| `/workspace/wt-t10/tests/e2e/calendar-recurrence.spec.ts` | 1 new #1243 Playwright row; existing rows unchanged; collection only |

## Holdout / acceptance and self-check

The eight evaluation scenarios in r6 section 20 and the independent UAT plan are **not executed**. The #1243 row is only collected. No live-instance check, secret link, Automations redirect screenshot, manual browser success or full cut CI is claimed. The independent checker is a different instance/model and has not been run by this author.

| Self-check | Evidence / result |
|---|---|
| Every scenario/dataset/FR accounted for | 71 BDD rows, 108 dataset rows, 26 functional-requirement rows below/above; accounting is not full behavioral coverage. |
| No false green | Final Go/UI tests exit 1; 71 blocked leaves remain failing; gaps are explicit. |
| Meaningful positive controls | Real run completion/content/session, actual stored goal/CAS/inbox, retry/retention/timing, real mux, real UI request bodies and real held-worker saves; 13 Go and 70 UI existing regressions pass. |
| No-op/constant implementation challenge | Exercised behaviors assert differentiated content, persistent state, real dispatch/callback counts and writes. Unbound behavior cannot satisfy this check and is not called covered. |
| No production implementation | Only tests, this report and the explicitly marked HTML compile fixture; no non-test Go/TypeScript, contract or dependency lock changes. |
| No skipped/removed/weakened existing tests | No Skip added; existing tests unchanged apart from appending the browser row. New instrument defects were corrected visibly; kind invariant moved to a dedicated real-producer test, not removed. |
| Traceability | Each authored test/scenario table has a spec reference; no inferred product scenario was introduced. |
| Build discipline | Foreground plain heavy commands, positive named selections, Go -p 1, one heavy job at a time. No pending worker/job. |
| Delivery limit | Partial tests-only handoff; CRITICAL coverage/dependency gaps require completion. No push, merge or feature-delivery claim. |
