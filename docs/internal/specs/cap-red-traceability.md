# Cap visibility RED traceability (#1547)

Specification: `/workspace/omnipus-coordination/designs-20261029/cap-visibility-spec-r6.md` (final r6, exclusively). Base: `1d3989df060887c9afe37c367562ce6975550724`. Test-author worktree: `/workspace/wt-cap`, branch `landing/cap`.

## Test plan (before assertions)

**Behaviour:** observations are dated and do not claim blockage; refusals report the decision's own numbers, cause and time; memory only lowers configured capacity; queued work is granted fairly against current memory without preemption; live execution evidence comes only from a full-claim reservation plus its entry marker.

**Oracle:** exact messages M1–M4, M7a–M7rs, M10a–M10d, M12a–M26; scenarios S1–S60 (including suffixes); datasets D1–D11 and PS1–PS14; requirements CAP-A1–A13 and CAP-B1–B12; 21 interleavings in §8.4. Expected values are derived from these tables before executing the implementation. No oracle comes from observed output.

**Boundary:** real tools, admission gates, executor, plan engine and file stores. Inject only memory readings (through `config.SetMemoryProviderForTest`), clocks, model/network edge, and filesystem failures. Tests must not substitute a fake gate, queue, resolver, notice implementation or collector. Where a required API is absent, any compatibility adapter must call the actual old production path and label omitted evidence, or fail explicitly; no adapter may implement the missing policy. Signature adapters are in separately marked `_test.go` files. The only non-test addition is `/workspace/wt-cap/pkg/agent/cap_red_scaffolding.go`: two explicitly compile-only placeholders, `foldMemory` and `ScheduledLaneLimit`, under the dispatch's minimal compile-stub exception. They return zero evidence, implement no admission policy, and MUST be deleted/replaced by GREEN.

**Case derivation:**

| Area | Independent expectations | Boundaries / negative cases | Planned discriminating faults |
|---|---|---|---|
| execute_plan | M1/M2, idempotent M3/M4, exact capacity object | active 0/16/17; configured max 4; age 0/4/40/70/89/90/91 s; nil/unreliable/zero time | unconditional queued note; zero omitted; stale observation accepted; repeated approval write |
| descriptions/guidance | M16/M17/M21, original preview line unchanged | forbidden generic slot/limit/cap text | copied guidance; retired generic cap sentence |
| run_task/delegate | exact per-cause messages, complete JSON shape, unchanged closed delegate schema | admitted/queued/session_busy; repeat queued/starting/running/unreadable | lost dispatch evidence; repeated dispatch; lifecycle record guessed as execution |
| inbound | M12a/M12b/M13 from real gate decision | occupancy over lowered limit; follow-up with lease | post-decision re-read; unverifiable mislabeled pressure |
| memory / task semaphore | D1–D11, strict >0.85, configured-only stored cap | configured 0/1/2/8/2000; threshold 0.84/0.85/0.86; two held slots and recovery | frozen boot fold; raises configured 1; double release; stale generation restore |
| steered queue | current-limit grants, older eligible first, full-claim identity | pressure 8→2; recovery 2→6/8; busy owner; cancel middle; batch failure | unconditional slot transfer; single-entry release; newcomer overtakes |
| plan snapshot | own fresh count and observation, latest publication wins | goals/loops, failed counter, concurrent admission/tick | shared pass count; unreliable publication; mixed/older tuple |
| roster waiting | gate-specific live predicates, bounded/scoped lists | next vs in_progress; cancelled/deleted/blocked/stopped; 3-list/512-map bounds | stale projection; task-origin omission; cross-workspace leak |
| scheduled lane | real exported ScheduledLaneLimit; no fixed healthy bound; one wait line | twelve healthy dispatches; cancellation; FIFO; 8→2→unlimited | canned seam; cached limit; transfer at lowered limit; second queue |
| related-work notices | M18, parent only, progress/no wake, full deterministic episode id | same-parent conveyed episode; two cause cycles; failed append/inbox/no edge | broadcast; origin-wide suppression; retry storm; missing inherited bookkeeping |
| entry/resolver/config | starting until entry boundary, error propagation, live-derived publication | stamp before reservation; same-generation replacement; worker before caller returns | lifecycle-only running; swallowed read error; stale caller config publication |

**Execution:** targeted named tests only, in foreground through `/usr/local/bin/heavy`; `CGO_ENABLED=0`, tags `goolm,stdjson`, `-p 1`, `TMPDIR=/workspace/.cache/tmp`. Logs and scratch: `/workspace/logs/cap-red/`. Direct exit status saved beside every log. Existing targeted tests run before and after the additions. No full suite, implementation, push, hosted CI, live-model UAT or independent CHECK is claimed by this RED author.

**Impact:** GitNexus tools are not connected in this session (tool discovery returned no GitNexus tool). Read/Grep fallback is used. Only new test symbols, this document, and the two compile-only symbols just listed change; no existing production implementation is edited. Impact is inferred, not graph-verified.

## Ownership, dependencies and conflicts

- Base is exactly the spec's pinned release SHA: no base drift detected.
- `/workspace/omnipus/CLAUDE.md` was read as requested; that separate shared checkout carries older instructions. Work is governed by the supplied current instructions and `/workspace/wt-cap/CLAUDE.md`; no edits outside the assigned worktree.
- 6b comparison reference at start: `origin/landing/t6b` = `2e8c1d237877c0b84580fe7f9d8a5aa7b3af16d5`. Inventory saved at `/workspace/logs/cap-red/t6b-overlap-inventory.txt`.
- Expected implementation rebase conflicts: `/workspace/wt-cap/pkg/cron/service.go`, `/workspace/wt-cap/pkg/tools/task.go`, `/workspace/wt-cap/pkg/agent/task_executor_run.go`, `/workspace/wt-cap/pkg/agent/task_run_loop.go`, `/workspace/wt-cap/pkg/agent/task_trigger.go`, `/workspace/wt-cap/pkg/agent/test_helpers_test.go`, `/workspace/wt-cap/pkg/gateway/gateway_boot.go`, `/workspace/wt-cap/pkg/gateway/rest_tasks.go`, and `/workspace/wt-cap/docs/settings.md`. New `cap_red_*_test.go` filenames avoid direct test-file overlap; semantic fixture drift is still possible.
- T8/T10 dependencies and unresolved testability gaps will be listed per row below; no synthetic PASS is permitted.

## Results and scenario traceability

**Status: PARTIAL / BLOCKED RED handoff, not complete and not GREEN.** There are 62 new top-level Go test declarations (agent 53, tools 4, config 2, cron 3), but the final tree is NOT compile-verified or test-verified. Earlier named RED runs and initial regression controls exist; final runs received no test output before the harness stopped them. Missing-interface RED, behavioural RED, passing existing invariants, partial cases and gaps are distinguished below. No setup/compile/deadline failure is counted as a behavioural proof.

**Dependency contract:** T10 r6 FR-07/T10-Q8 removes the fixed lane/setting; T8 r17 TK-30 owns pending-occurrence protection and reload-surviving scheduler semantics. Those backends are not at this pinned base. Scheduled service-unit cases below target cap-r6's contract, not the absent merged T10 engine. Integrated scheduler/pending-occurrence acceptance **needs T8/T10 backend**. No T8/T10 production stub was introduced.

The scenario table includes only IDs live in r6 §6; S28, S39–S41, S42a and S45 are not invented. Each row states what its test does NOT establish where coverage is partial.

| Scenario | Test | File | Execution / completeness | Required failure or evidence; remaining gap |
|---|---|---|---|---|
| S1 | `TestCapRED_PlanExecute_ObservationDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | Exact M1/M2 and full JSON, independent PS table. Missing real PlanExecute snapshot/clock injection and old unconditional queued note are explicit RED. |
| S2 | `TestCapRED_PlanExecute_ObservationDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | Exact M1/M2 and full JSON, independent PS table. Missing real PlanExecute snapshot/clock injection and old unconditional queued note are explicit RED. |
| S3 | `TestCapRED_PlanExecute_ObservationDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED; PARTIAL | Historical 70 s / stopped-40 s supplied snapshot is asserted. Actual intervening promotion/engine-stop side effects are NOT exercised. |
| S4 | `TestCapRED_PlanExecute_ObservationDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED; PARTIAL | Historical 70 s / stopped-40 s supplied snapshot is asserted. Actual intervening promotion/engine-stop side effects are NOT exercised. |
| S5 | `TestCapRED_PlanExecute_ObservationDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | Exact M1/M2 and full JSON, independent PS table. Missing real PlanExecute snapshot/clock injection and old unconditional queued note are explicit RED. |
| S6 | `TestCapRED_PlanExecute_ObservationDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | Exact M1/M2 and full JSON, independent PS table. Missing real PlanExecute snapshot/clock injection and old unconditional queued note are explicit RED. |
| S7 | `TestCapRED_PlanExecute_ObservationDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | Exact M1/M2 and full JSON, independent PS table. Missing real PlanExecute snapshot/clock injection and old unconditional queued note are explicit RED. |
| S8 | `TestCapRED_PlanExecute_ObservationDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | Exact M1/M2 and full JSON, independent PS table. Missing real PlanExecute snapshot/clock injection and old unconditional queued note are explicit RED. |
| S9 | `TestCapRED_PlanExecute_ObservationDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | Exact M1/M2 and full JSON, independent PS table. Missing real PlanExecute snapshot/clock injection and old unconditional queued note are explicit RED. |
| S10 | `TestCapRED_PlanExecute_AlreadyApproved_Observation` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | Exact M3/M4 and complete approved record unchanged; source absence is not converted to made-up zero capacity. |
| S11 | `TestCapRED_PlanExecute_AlreadyApproved_Observation` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | Exact M3/M4 and complete approved record unchanged; source absence is not converted to made-up zero capacity. |
| S12 | `TestCapRED_RunTask_QueuedDisclosure` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Actual registered run_task + real launcher; exact full M7c/M7a/M7b result and durable in_progress. No fake start callback. |
| S12a | `TestCapRED_RunTask_QueuedDisclosure` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Actual registered run_task + real launcher; exact full M7c/M7a/M7b result and durable in_progress. No fake start callback. |
| S12b | `TestCapRED_RunTask_QueuedDisclosure` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Actual registered run_task + real launcher; exact full M7c/M7a/M7b result and durable in_progress. No fake start callback. |
| S12c | `TestCapRED_RunTask_FreeAndExecutingRepeat` | `/workspace/wt-cap/pkg/agent/cap_red_live_test.go` | UNEXECUTED<br>Final tree UNVERIFIED | Real launched task reaches parked model edge; unqueued full shape has no cap fields, repeat exact running note, same session and no second body. |
| S12d | `TestCapRED_RunTask_RepeatQueuedDoesNotDispatch` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Real queued launch then repeated real tool call. M7r, unchanged queue/session; old redispatch/duplicate-admission error is RED. |
| S12e | `TestCapRED_RunTask_FreeAndExecutingRepeat` | `/workspace/wt-cap/pkg/agent/cap_red_live_test.go` | UNEXECUTED<br>Final tree UNVERIFIED | Real launched task reaches parked model edge; unqueued full shape has no cap fields, repeat exact running note, same session and no second body. |
| S12f | `TestCapRED_RunTask_UnreadableLifecycleIsAnError` | `/workspace/wt-cap/pkg/agent/cap_red_live_test.go` | UNEXECUTED<br>Final tree UNVERIFIED | Real lifecycle JSONL replaced with malformed bytes after a real first launch; original bytes restored. Specific unreadable-state error and no dispatch, never guessed running. |
| S12g | `TestCapRED_RunTask_ReservationWithoutSessionReportsStarting` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED; PARTIAL | Real task run-slot held without durable session: exact M23/no invented session/admission. Concurrent failing-first-launch and third retry are NOT exercised. |
| S12h | — | — | GAP — no test | GAP: no test pauses queued lifecycle publication at T0. TaskStartOutcome is absent; no fake outcome or timing hook was added. |
| S13 | `TestCapRED_Descriptions_NoGenericCapWording` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | M16/M17/M21 verbatim; forbidden generic cap words, shared guidance byte identity, original preview line preserved. |
| S14 | `TestCapRED_SessionWorker_RefusalNamesCause` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Real gate and outbound bus; exact M12a/M12b/M13, including occupancy 8 above lowered 2. Existing leases retained. |
| S15 | `TestCapRED_SessionWorker_RefusalNamesCause` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Real gate and outbound bus; exact M12a/M12b/M13, including occupancy 8 above lowered 2. Existing leases retained. |
| S16 | `TestCapRED_SessionWorker_RefusalNamesCause` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Real gate and outbound bus; exact M12a/M12b/M13, including occupancy 8 above lowered 2. Existing leases retained. |
| S16a | `TestCapRED_Inbound_FollowupLeaseSurvivesLowerLimit` | `/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | PASS in red-agent-first<br>Final tree UNVERIFIED; PARTIAL | Real existing-scope admission stays admitted under lowered limit. No additional worker refusal/log is explicitly inspected. |
| S17 | `TestCapRED_ScheduledLane_HealthyTwelveThroughRealSeam` | `/workspace/wt-cap/pkg/agent/cap_red_scheduled_test.go` | UNEXECUTED<br>Final tree UNVERIFIED | Actual cron service, exported real ScheduledLaneLimit function (compile-only placeholder until GREEN); twelve independently held handlers on dispatch/RunNow, no wait lines. Missing constructor seam is explicit RED. |
| S18 | `TestCapRED_ScheduledLane_PressureWaitsBothEntryPaths` | `/workspace/wt-cap/pkg/agent/cap_red_scheduled_test.go` | UNEXECUTED<br>Final tree UNVERIFIED | Actual service + exported seam, both entry paths. Exactly one full M14a decision/log and third starts after handler return. Old constructor/fixed lane and compile-only seam fail. |
| S19 | `TestCapRED_ScheduledLane_PressureWaitsBothEntryPaths` | `/workspace/wt-cap/pkg/agent/cap_red_scheduled_test.go` | UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Full M14b on both paths. Cancellation ordering uses separate cron fairness case with measured cause; unverifiable-cancellation combination is NOT separately exercised. |
| S20 | `TestCapRED_ListJobs_CapPairDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | PASS in red-tools-first<br>Final tree UNVERIFIED | Existing real list_jobs renderer with its external snapshot source: exact cap quartet/absence and stopped-engine flag. Real engine publication is tested separately. |
| S21 | `TestCapRED_ListJobs_CapPairDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | PASS in red-tools-first<br>Final tree UNVERIFIED | Existing real list_jobs renderer with its external snapshot source: exact cap quartet/absence and stopped-engine flag. Real engine publication is tested separately. |
| S22 | `TestCapRED_ListJobs_CapPairDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | PASS in red-tools-first<br>Final tree UNVERIFIED | Existing real list_jobs renderer with its external snapshot source: exact cap quartet/absence and stopped-engine flag. Real engine publication is tested separately. |
| S22a | `TestCapRED_ListJobs_CapPairDatasets` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | PASS in red-tools-first<br>Final tree UNVERIFIED | Existing real list_jobs renderer with its external snapshot source: exact cap quartet/absence and stopped-engine flag. Real engine publication is tested separately. |
| S22b | `TestCapRED_TaskWaitingProjection_RealCollector` | `/workspace/wt-cap/pkg/agent/cap_red_projection_test.go` | FAIL in red-agent-second<br>Final tree UNVERIFIED; PARTIAL | Actual ExecuteTask + registered real collector, next task memory refusal then in_progress invalidation. Retry/grant clearing is not separately driven. |
| S22c | `TestCapRED_PlanWaitingMembers_BoundedRealCollector` | `/workspace/wt-cap/pkg/agent/cap_red_projection_test.go` | FAIL in red-agent-second<br>Final tree UNVERIFIED; PARTIAL | Four real refused plan members; count4, exactly3 samples, ids/numbers only, no task rows or foreign-workspace visibility, terminal-plan invalidation. Successful member grant clearing is NOT driven. |
| S22d | `TestCapRED_SteeredTaskWait_VisibleWhileInProgress` | `/workspace/wt-cap/pkg/agent/cap_red_projection_test.go` | FAIL in red-agent-second; fixture leg excluded<br>Final tree UNVERIFIED; PARTIAL | Real main-owned launch, task durably in_progress, real collector task waiting / plan waiting_runs with position; no member task/subagent row. Deliverer intentionally unwired, not a fake successful notice. A real forced Append failure and grant removal are NOT driven in this row. |
| S22e | `TestCapRED_SteeredTaskWait_VisibleWhileInProgress` | `/workspace/wt-cap/pkg/agent/cap_red_projection_test.go` | FAIL in red-agent-second; fixture leg excluded<br>Final tree UNVERIFIED; PARTIAL | Real main-owned launch, task durably in_progress, real collector task waiting / plan waiting_runs with position; no member task/subagent row. Deliverer intentionally unwired, not a fake successful notice. A real forced Append failure and grant removal are NOT driven in this row. |
| S22f | — | — | GAP — no test | GAP: no real task-refusal publication-delay barrier exists in this pack; TryAcquire evidence is tested, but no T0-versus-publish equality is claimed. |
| S23 | `TestCapRED_Descriptions_NoGenericCapWording` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | M16/M17/M21 verbatim; forbidden generic cap words, shared guidance byte identity, original preview line preserved. |
| S24 | `TestCapRED_Delegate_QueuedSentenceAndClosedJSON` | `/workspace/wt-cap/pkg/agent/cap_red_notice_test.go` | UNEXECUTED<br>Final tree UNVERIFIED | Real delegate tool/launcher; exact M10c/M10a configured/unset/one/explicit-pressure, full closed JSON with no concurrency_limit, same-parent launch episode not duplicated. |
| S25 | `TestCapRED_Delegate_QueuedSentenceAndClosedJSON` | `/workspace/wt-cap/pkg/agent/cap_red_notice_test.go` | UNEXECUTED<br>Final tree UNVERIFIED | Real delegate tool/launcher; exact M10c/M10a configured/unset/one/explicit-pressure, full closed JSON with no concurrency_limit, same-parent launch episode not duplicated. |
| S26 | `TestCapRED_Delegate_QueuedSentenceAndClosedJSON` | `/workspace/wt-cap/pkg/agent/cap_red_notice_test.go` | UNEXECUTED<br>Final tree UNVERIFIED | Real delegate tool/launcher; exact M10c/M10a configured/unset/one/explicit-pressure, full closed JSON with no concurrency_limit, same-parent launch episode not duplicated. |
| S27 | `TestCapRED_Delegate_QueuedSentenceAndClosedJSON` | `/workspace/wt-cap/pkg/agent/cap_red_notice_test.go` | UNEXECUTED<br>Final tree UNVERIFIED | Real delegate tool/launcher; exact M10c/M10a configured/unset/one/explicit-pressure, full closed JSON with no concurrency_limit, same-parent launch episode not duplicated. |
| S28a | `TestCapRED_ActualGates_MemoryDatasets` | `/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Configured 1/2/8/unset and unverifiable real gate table; M7b/M12b/M10b/M15u/M14b/M25b are in corresponding surface tests. No host /proc read. |
| S28b | `TestCapRED_ThresholdAndCauseTransitions_NoState` | `/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED; PARTIAL | Injected real strict >.85 threshold, .84/.85/.86 and pressure/unverifiable/healthy transitions, independent expected tuples. D9 fixed occupancy 3 is not established by this pure-fold test. |
| S29 | `TestCapRED_ExecuteTask_RefusalNamesCause` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED; PARTIAL | Actual ExecuteTask, exact typed M15m/M15u/M15 error, errors.Is retained, refused next state. Roster numbers use separate collector case. All application logs are not scanned for retired sentinel text. |
| S30 | `TestCapRED_ExecuteTask_RefusalNamesCause` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED; PARTIAL | Actual ExecuteTask, exact typed M15m/M15u/M15 error, errors.Is retained, refused next state. Roster numbers use separate collector case. All application logs are not scanned for retired sentinel text. |
| S31 | `TestCapRED_SteerQueue_8to2_NoPromotionAboveLimit` | `/workspace/wt-cap/pkg/agent/cap_red_queue_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Actual gate with eight holders/five waiters: first six completions no grant, seventh oldest grant, sustained floor and original full identity. No preemption. |
| S32 | `TestCapRED_SteerQueue_NewcomerDispatchesOlderRealBodies` | `/workspace/wt-cap/pkg/agent/cap_red_live_test.go` | UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Real launcher/newcomer dispatch must drain both old entries and all three actual bodies must reach model edge. Initial limit2 is configured, not obtained by pressure on configured8; pressure-recovery numeric batch is tested separately. |
| S32a | `TestCapRED_SteerPromotion_BatchInstallsAllReservations` | `/workspace/wt-cap/pkg/agent/cap_red_queue_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Real admission/release batch, four FIFO promotions on rise2->6, all reservations installed under gate before batch returns. |
| S32b | `TestCapRED_SteerQueue_SkipsBusyOwnerAndCancelsMiddle` | `/workspace/wt-cap/pkg/agent/cap_red_queue_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Real queue retains same-session busy entry while later eligible entries progress; canceled middle consumes no reservation. |
| S33 | `TestCapRED_SteerQueue_8to2_NoPromotionAboveLimit` | `/workspace/wt-cap/pkg/agent/cap_red_queue_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Actual gate with eight holders/five waiters: first six completions no grant, seventh oldest grant, sustained floor and original full identity. No preemption. |
| S34 | `TestCapRED_SteerQueue_QueuedCancelRecomputesPositions` | `/workspace/wt-cap/pkg/agent/cap_red_queue_test.go` | PASS in red-agent-first<br>Final tree UNVERIFIED; PARTIAL | Middle removal and oldest promotion are asserted. Live AdmissionFor numerical positions are NOT queried. |
| S35 | `TestCapRED_DelegateStatus_SessionBusyNumberFree` | `/workspace/wt-cap/pkg/agent/cap_red_live_test.go` | UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Actual full-claim gate placement and complete real status M24s, no notice/extra reservation. M7d/M10d launch texts are NOT separately driven. |
| S36 | `TestCapRED_CapNotice_ProgressDeliveredNotWoken` | `/workspace/wt-cap/pkg/agent/cap_red_notice_test.go` | UNEXECUTED<br>Final tree UNVERIFIED | Actual engine main-owned plan member/Deliver/inbox. Exact M18, full deterministic identity, current generation, parent-only, no wake/turn. Missing notice is behavioural RED; subsequent envelope assertions stop on absence. |
| S37 | `TestCapRED_CapNotice_ConveyedEpisodeDoesNotSuppressLater` | `/workspace/wt-cap/pkg/agent/cap_red_notice_failure_test.go` | UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Same-parent delegate: first result-conveyed episode suppressed, later unverifiable episode delivered. Caller-A/assignee-B run_task and stopped-parent cases are NOT exercised. |
| S38 | `TestCapRED_CapNotice_ProgressDeliveredNotWoken` | `/workspace/wt-cap/pkg/agent/cap_red_notice_test.go` | UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Different-workspace inbox empty; real collector foreign-workspace denial in projection file. Stopped parent storage/no-wake is NOT exercised. |
| S42 | `TestCapRED_DispatchSema_ConfiguredCapacityGeneration`<br>`TestCapRED_DispatchSema_GenerationRandomizedInterleavings` | `/workspace/wt-cap/pkg/agent/cap_red_publication_test.go` | FAIL in red-agent-first<br>FAIL in red-agent-first<br>Final tree UNVERIFIED; PARTIAL | Deterministic generation G+1 then stale/equal G and 24 fixed-seed concurrent applications. Missing ConfigSnapshot/ApplyConfiguredCapacity is interface RED, NOT an exercised race. Real gateway Settings PUT is GAP. |
| S42b | `TestCapRED_FastUpsertDerivedFromLiveConfig`<br>`TestCapRED_FastUpsertRejectsMissingLiveConfig` | `/workspace/wt-cap/pkg/agent/cap_red_publication_test.go` | FAIL in red-agent-first<br>FAIL in red-agent-first<br>Final tree UNVERIFIED; PARTIAL | Real stale-before-first-sample UpsertAgentFast: live capacities/unrelated port retained, requested agent applied, generation unchanged; nil live config fails. Gateway PUT/full-reload fallback and validation-failure leg are NOT exercised. |
| S43 | `TestCapRED_ThresholdAndCauseTransitions_NoState` | `/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED; PARTIAL | Injected real strict >.85 threshold, .84/.85/.86 and pressure/unverifiable/healthy transitions, independent expected tuples. D9 fixed occupancy 3 is not established by this pure-fold test. |
| S43a | `TestCapRED_SteerQueue_AlternatingReadings_OrderPreserved` | `/workspace/wt-cap/pkg/agent/cap_red_queue_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Real queue across pressure/healthy decisions; oldest-first drain, no revoke, pressure blocks at occupancy>=2. |
| S44 | `TestCapRED_Descriptions_NoGenericCapWording` | `/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED | M16/M17/M21 verbatim; forbidden generic cap words, shared guidance byte identity, original preview line preserved. |
| S46 | `TestCapRED_RetiredSchedulesKeyIgnoredAndDropped`<br>`TestCapRED_SchedulesConfigHasNoFixedLaneCapacity` | `/workspace/wt-cap/pkg/config/cap_red_retired_schedule_test.go` | UNEXECUTED<br>UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Real LoadConfig/SaveConfig negative/zero/3/8/12 table, no retired field/default. Full gateway boot and T10 consolidated-engine wiring: needs T8/T10 backend. |
| S47 | `TestCapRED_DispatchSema_RecoversWithoutResize_HeldSlots` | `/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | D10 exactly two held, pressure refusal, healthy admit2->3, owned release3->2, pressure refuses again. Stored configured8 never resized. |
| S47a | `TestCapRED_Fold_MemoryAndAbsentBound_Table` | `/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | D11 full independent 3x4 matrix. foldMemory is explicitly compile-only RED scaffolding, not implemented policy. |
| S48 | `TestCapRED_PlanEngine_TickFreshCountPerDecision` | `/workspace/wt-cap/pkg/agent/cap_red_snapshot_test.go` | FAIL in red-agent-second<br>Final tree UNVERIFIED | Real engine/goals14/three approved plans, exactly two running and four own counts including pass-end, final16 not17, distinct times, exact M19. Absent CapSnapshot blocks later assertions explicitly. |
| S49 | `TestCapRED_AdmissionView_InitialEntryBoundary` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-second<br>Final tree UNVERIFIED; PARTIAL | Real ordinary initial pre-commit hook reads starting then successful body handoff running. Task-origin/promoted failure/terminal cleanup and repeat-start are NOT exercised. |
| S49a | — | — | GAP — no test | GAP: no deterministic real producer/resolver barriers for wake consume failure, replacement-between-reads, task worker-before-return, or resolver second-load failure/second identity change. No fake resolver or guessed state was substituted. |
| S49b | — | — | GAP — no test | GAP: no deterministic real producer/resolver barriers for wake consume failure, replacement-between-reads, task worker-before-return, or resolver second-load failure/second identity change. No fake resolver or guessed state was substituted. |
| S49c | `TestCapRED_DelegateStatus_RecordIsNotExecution` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-second<br>Final tree UNVERIFIED; PARTIAL | Actual nonterminal lifecycle with newly stamped identity and no reservation; full status must carry M26/no Admission clause. Static discriminating window only: paused concurrent repeat-start/preparation is NOT exercised. |
| S49d | — | — | GAP — no test | GAP: no deterministic real producer/resolver barriers for wake consume failure, replacement-between-reads, task worker-before-return, or resolver second-load failure/second identity change. No fake resolver or guessed state was substituted. |
| S49e | — | — | GAP — no test | GAP: no deterministic real producer/resolver barriers for wake consume failure, replacement-between-reads, task worker-before-return, or resolver second-load failure/second identity change. No fake resolver or guessed state was substituted. |
| S50 | `TestCapRED_DelegateStatus_SessionBusyNumberFree` | `/workspace/wt-cap/pkg/agent/cap_red_live_test.go` | UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Real M24s and no serialization notice; M18 over-lowered limit wording in S36. Later-turn wake and M7d/M10d not separately driven. |
| S51 | `TestCapRED_CapNotice_TwoCauseCyclesKeepEpisodeIdentity` | `/workspace/wt-cap/pkg/agent/cap_red_notice_test.go` | UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Actual production release consumer, same-cause dedupe then two cause cycles to five episodes, ids/unchanged placement/body. Initial missing notice blocks later stages. Requeue/revival identity and same-parent run_task cycle are GAP; delegate later suppression is separately tested. |
| S52 | `TestCapRED_CapNotice_AppendFailureIsVisibleAndBounded` | `/workspace/wt-cap/pkg/agent/cap_red_notice_failure_test.go` | UNEXECUTED<br>Final tree UNVERIFIED | Real malformed inbox directory, real cap-full prefill, and no-edge; unchanged queue, one WARN/episode, no wake; inherited NotDelivered + transcript frame; real status fallback. No fabricated Deliver result. |
| S53 | `TestCapRED_TaskWaitingProjection_RealCollector`<br>`TestCapRED_PlanWaitingMembers_BoundedRealCollector` | `/workspace/wt-cap/pkg/agent/cap_red_projection_test.go` | FAIL in red-agent-second<br>FAIL in red-agent-second<br>Final tree UNVERIFIED; PARTIAL | in_progress task predicate and plan-stop cleanup only. Cancellation/deletion/blocking/grant matrix and 512-entry create-refuse-delete bound are GAP. |
| S54 | `TestCapRED_SessionWorker_RefusalNamesCause` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED | Real gate and outbound bus; exact M12a/M12b/M13, including occupancy 8 above lowered 2. Existing leases retained. |
| S55 | `TestCapRED_ExecuteTask_RefusalNamesCause` | `/workspace/wt-cap/pkg/agent/cap_red_surfaces_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED; PARTIAL | Actual ExecuteTask, exact typed M15m/M15u/M15 error, errors.Is retained, refused next state. Roster numbers use separate collector case. All application logs are not scanned for retired sentinel text. |
| S56 | `TestCapRED_ScheduledLane_RealExportedSeam`<br>`TestCapRED_ScheduledLane_HealthyTwelveThroughRealSeam`<br>`TestCapRED_ScheduledLane_PressureWaitsBothEntryPaths` | `/workspace/wt-cap/pkg/agent/cap_red_scheduled_test.go` | UNEXECUTED<br>UNEXECUTED<br>UNEXECUTED<br>Final tree UNVERIFIED | Real exported symbol and shared memory injection: healthy0, pressure2, unverifiable2, strict .85, actual cron entry sites. Symbol is compile-only scaffolding until GREEN; constructor wiring absent. |
| S57 | `TestCapRED_Cron_LaneFairness_OlderWaitersBothEntryPaths` | `/workspace/wt-cap/pkg/cron/cap_red_lane_test.go` | UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Both real dispatch and RunNow with ordered wait logs as barriers; four releases, newcomer FIFO, cancelled middle/no starvation, max2. Pending constructor/wait-log support blocks downstream assertions. Cancellation exactly DURING release is not separately held. |
| S57a | `TestCapRED_Cron_LaneReleaseRechecksLowerLimitAndDrainsRecovery` | `/workspace/wt-cap/pkg/cron/cap_red_lane_test.go` | UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Both real paths8->2->unlimited; six releases none, seventh one, recovery release drains FIFO including newcomer. Limit-reader events + same lane mutex completion barrier; needs T8/T10 backend integration and reconciliation of removed laneWindow.sem fixture. |
| S58 | `TestCapRED_SteerPromotion_BatchInstallsAllReservations` | `/workspace/wt-cap/pkg/agent/cap_red_queue_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED; PARTIAL | Real batch reservation plus separate semaphore double-release invariant. All three production release consumers and one failing promotion with successful siblings are GAP. |
| S59 | `TestCapRED_DelegateStatus_DatedAfterRecoveryNoEvent` | `/workspace/wt-cap/pkg/agent/cap_red_notice_test.go` | UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Complete dated M24 at +120 s from real decision ObservedAt, recover/config change without event, no memory measurement/admission. Missing decision time is interface RED; repeated run_task M7r/M7rs after time jump is GAP. |
| S60 | `TestCapRED_PlanJudge_LaneMemoryGated`<br>`TestCapRED_PlanJudge_ReleaseOnPhaseUpdateFailure`<br>`TestCapRED_PlanJudge_ReleaseOnRegistryFailure`<br>`TestCapRED_PlanJudge_HealthyConfiguredFullKeepsDebug` | `/workspace/wt-cap/pkg/agent/cap_red_snapshot_test.go` | FAIL in red-agent-second<br>PASS in red-agent-second<br>UNEXECUTED<br>UNEXECUTED<br>Final tree UNVERIFIED; PARTIAL | Real shared semaphore consumer; two held -> pressure/unverifiable exact M25; healthy held4 preserves full Debug; phase-update and real CAS-registration failure release ownership. Four active real model judge bodies not simultaneously driven. |

### Dataset traceability

| Dataset | Test / file | Evidence and limitations |
|---|---|---|
| D1 | `TestCapRED_ActualGates_MemoryDatasets`<br>`/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED. Expectations from r6 §8.1; memory provider injected. |
| D2 | `TestCapRED_ActualGates_MemoryDatasets`<br>`/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED. Expectations from r6 §8.1; memory provider injected. |
| D3 | `TestCapRED_ActualGates_MemoryDatasets`<br>`/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED. Expectations from r6 §8.1; memory provider injected. |
| D4 | `TestCapRED_ActualGates_MemoryDatasets`<br>`/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED; D4 full eight-to-two reduction additionally exercised by queue test. Expectations from r6 §8.1; memory provider injected. |
| D5 | `TestCapRED_ActualGates_MemoryDatasets`<br>`/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED. Expectations from r6 §8.1; memory provider injected. |
| D6 | `TestCapRED_ActualGates_MemoryDatasets`<br>`/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED. Expectations from r6 §8.1; memory provider injected. |
| D7 | `TestCapRED_ActualGates_MemoryDatasets`<br>`/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED. Expectations from r6 §8.1; memory provider injected. |
| D8 | `TestCapRED_ThresholdAndCauseTransitions_NoState`<br>`/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED. Expectations from r6 §8.1; memory provider injected. |
| D9 | `TestCapRED_ThresholdAndCauseTransitions_NoState`<br>`/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED; D9 fixed occupancy3 is NOT exercised (pure-fold alternation only). Expectations from r6 §8.1; memory provider injected. |
| D10 | `TestCapRED_DispatchSema_RecoversWithoutResize_HeldSlots`<br>`/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED. Expectations from r6 §8.1; memory provider injected. |
| D11 | `TestCapRED_Fold_MemoryAndAbsentBound_Table`<br>`/workspace/wt-cap/pkg/agent/cap_red_memory_test.go` | FAIL in red-agent-first<br>Final tree UNVERIFIED. Expectations from r6 §8.1; memory provider injected. |
| PS1 | `TestCapRED_PlanExecute_ObservationDatasets`<br>`/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED; Independent exact snapshot/JSON oracle from §8.2. |
| PS2 | `TestCapRED_PlanExecute_ObservationDatasets`<br>`/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED; Independent exact snapshot/JSON oracle from §8.2. |
| PS3 | `TestCapRED_PlanExecute_ObservationDatasets`<br>`/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED; Independent exact snapshot/JSON oracle from §8.2. |
| PS4 | `TestCapRED_PlanExecute_ObservationDatasets`<br>`/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED; Independent exact snapshot/JSON oracle from §8.2. |
| PS5 | `TestCapRED_PlanExecute_ObservationDatasets`<br>`/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED; Independent exact snapshot/JSON oracle from §8.2. |
| PS6 | `TestCapRED_PlanExecute_ObservationDatasets`<br>`/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED; Independent exact snapshot/JSON oracle from §8.2. |
| PS7 | `TestCapRED_PlanExecute_ObservationDatasets`<br>`/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED; Independent exact snapshot/JSON oracle from §8.2. |
| PS8 | `TestCapRED_PlanExecute_ObservationDatasets`<br>`/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED; Independent exact snapshot/JSON oracle from §8.2. |
| PS9 | `TestCapRED_PlanExecute_ObservationDatasets`<br>`/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | FAIL in red-tools-first<br>Final tree UNVERIFIED; Independent exact snapshot/JSON oracle from §8.2. |
| PS10 | `TestCapRED_PlanEngine_PublishesOwnSummedObservation`<br>`/workspace/wt-cap/pkg/agent/cap_red_snapshot_test.go` | FAIL in red-agent-second<br>Final tree UNVERIFIED; Independent exact snapshot/JSON oracle from §8.2. |
| PS11 | `TestCapRED_PlanEngine_FailedReadPublishesNothing`<br>`/workspace/wt-cap/pkg/agent/cap_red_snapshot_test.go` | FAIL in red-agent-second<br>Final tree UNVERIFIED; Missing CapSnapshot prevents exercising failed-read preservation at this base. |
| PS12 | `TestCapRED_PlanEngine_OlderObservationCannotReplaceNewer`<br>`/workspace/wt-cap/pkg/agent/cap_red_snapshot_test.go` | FAIL in red-agent-second<br>Final tree UNVERIFIED; Backward injected observation clock asserts monotonic selection, but a genuinely delayed older pass after a newer admission is GAP. |
| PS13 | — | GAP; GAP: concurrent admission/Tick publication and tuple-coherence ordering is not implemented in this test pack. |
| PS14 | `TestCapRED_ListJobs_CapPairDatasets`<br>`/workspace/wt-cap/pkg/tools/cap_red_observation_test.go` | PASS in red-tools-first<br>Final tree UNVERIFIED; Renderer/source control only; a real engine Stop operation is not exercised. |

### Requirement rows (r6 §10)

| Requirement | Scenarios | Disposition |
|---|---|---|
| CAP-A1 | S1–S9 | Observation table; S3/S4 real engine transitions partial |
| CAP-A2 | S10/S11 | Exact idempotent M3/M4 |
| CAP-A3 | S44 | Description/preview tests |
| CAP-A4 | S13 | Exact M16 / forbidden wording |
| CAP-A5 | S12–S12h/S49/S59 | Partial: delayed commit, failing concurrent launch, several resolver paths and dated run_task remain gaps |
| CAP-A6 | S14–S16a/S54 | Exact inbound text and lease invariant; no post-decision-publication race barrier |
| CAP-A7 | S17–S19/S46/S56/S57/S57a | Real seam/service unit cases; integrated consolidated scheduler needs T8/T10 backend |
| CAP-A8 | S20–S22f/S48/S53 | Partial: renderer/real collector + snapshots; delay/512-bound/full invalidation gaps |
| CAP-A9 | S23 | Guidance byte identity |
| CAP-A10 | S13/S23 | M17 guidance checked |
| CAP-A11 | S55 | Partial: exact sentinel/refusal tested; full code-comment review is not an automated test |
| CAP-A12 | U-1–U-11/U-6a | NOT EXECUTED: implementation-stage user-doc drafting, docs-verifier and real-app UAT are separate. No user behaviour implemented here. |
| CAP-A13 | S36–S38/S51/S52 | Partial: real notice path/failure/cause cycles; caller≠parent run_task/stopped parent/revival gaps |
| CAP-B1 | S24–S28b/S31–S35/S49/S58 | Real gate/signature/closed delegate surfaces; multiple release-consumer/failure and entry-boundary gaps |
| CAP-B2 | S22f/S29/S30/S42/S47/S47a | Real semaphore, immutable tuple, configured-only cap/retired WaitAndAcquire. Delayed publication is GAP. |
| CAP-B3 | S26/D5/D11 | Configured1/2 never raised or mislabeled |
| CAP-B4 | S31/S33/S43a | Gate counts and identities/no revoke asserted; no separate stop/pause observer |
| CAP-B5 | S29/S30/S55 | Exact typed refusal and errors.Is; no retired phrase in error |
| CAP-B6 | S12–S19/S24–S38/S50–S52/S54/S55/S59/S60 | Partial: actual tools/outbound/engine/log/inbox; not every follow-up path |
| CAP-B7 | S31–S34/S43a/S58 | Real current-limit/batch gate and newcomer body dispatch; all three release consumers GAP |
| CAP-B8 | S20–S22a/S48/PS10–PS14 | Missing real publication API explicit RED; PS13 and true delayed PS12 ordering GAP |
| CAP-B9 | S17–S19/S46/S56/S57/S57a | Real exported function is explicitly compile-only RED placeholder; scheduler constructor absent. Needs T8/T10 backend integration. |
| CAP-B10 | S12d–S12h/S49–S49e/S51/S59 | Partial real status/repeat/start; no fake resolver; four producer/resolver schedules GAP |
| CAP-B11 | S60 | Pressure/unverifiable + healthy-full + both real failure releases |
| CAP-B12 | S42/S42b | Missing generation APIs explicit RED; real upsert stale publication exposed. Full publisher inventory/Settings PUT/fallback GAP |

### All 21 required deterministic orderings (r6 §8.4)

| Ordering | Scenario / concrete test evidence | Coverage limit |
|---|---|---|
| I1 | S42 (see named test/file in scenario row) | Partial: missing generation interfaces block acquire ordering; no Settings PUT integration |
| I2 | S42 (see named test/file in scenario row) | Deterministic G+1/stale-G test; interface RED, not yet executed race semantics |
| I3 | S43a (see named test/file in scenario row) | Partial: memory limit decisions covered; no paused pressure-read/settings-write combination |
| I4 | S49/S49b (see named test/file in scenario row) | Partial ordinary pre-commit starting read; pending-task handoff/failure and replacement gaps |
| I5 | S12g (see named test/file in scenario row) | Reservation-only starting test; concurrent failing first launch/third attempt GAP |
| I6 | S31 (see named test/file in scenario row) | Real two-acquires/one-release synchronized semaphore test; targeted -race receipt separately |
| I7 | S32/S57 (see named test/file in scenario row) | Real newcomer body-dispatch test; scheduled FIFO logs/barriers; truly simultaneous race GAP |
| I8 | S58 (see named test/file in scenario row) | Batch reservation test only; failed one-of-three dispatch and all release consumers GAP |
| I9 | PS12 (see named test/file in scenario row) | Backward observation clock probe only; actual delayed older pass GAP |
| I10 | S51 (see named test/file in scenario row) | Sequential real release-driven episode transitions; concurrent delivery/cause race GAP |
| I11 | S59 (see named test/file in scenario row) | Real status clock jump; absent ObservedAt blocks historical-read assertions; dated run_task GAP |
| I12 | S57/S60 (see named test/file in scenario row) | Canceled scheduler waiter before release and real judge registration/phase failure; cancellation DURING release GAP |
| I13 | S57a (see named test/file in scenario row) | Actual service release sequence both entry paths; blocked by absent constructor/wait log at base |
| I14 | S49b (see named test/file in scenario row) | GAP: no real resolver two-read interleaving barrier |
| I15 | S49a (see named test/file in scenario row) | GAP: no admitted system-wake producer entry/consume-failure schedule |
| I16 | S42b (see named test/file in scenario row) | Real UpsertAgentFast old input AFTER published replacement; no API-only simulation |
| I17 | S49c (see named test/file in scenario row) | Static full status discriminator with nonterminal record/no gate; concurrent preparation/repeat GAP |
| I18 | S49d (see named test/file in scenario row) | GAP: task body-before-caller-return barrier |
| I19 | S49e (see named test/file in scenario row) | GAP: resolver second load failure/second identity change after initial success |
| I20 | S22f (see named test/file in scenario row) | GAP: refused evidence publication-delay barrier |
| I21 | S22d/S22e (see named test/file in scenario row) | Real main-owned in_progress task/plan collectors; notice unwired rather than real Append fault; grant removal GAP |

### Approval-condition inventory (r6 §8.5)

These are not silently marked satisfied by a test name. Each inherits the exact partial/gap disposition of its scenario rows above.

| Condition | Scenario evidence / outstanding work |
|---|---|
| AC1 | S42; see scenario row(s), including any interface-blocked or GAP leg |
| AC2 | S49; see scenario row(s), including any interface-blocked or GAP leg |
| AC3 | S17/S56; see scenario row(s), including any interface-blocked or GAP leg |
| AC4 | S58; see scenario row(s), including any interface-blocked or GAP leg |
| AC5 | S60; see scenario row(s), including any interface-blocked or GAP leg |
| AC6 | S51; see scenario row(s), including any interface-blocked or GAP leg |
| AC7 | S59; see scenario row(s), including any interface-blocked or GAP leg |
| AC8 | S35/S50; see scenario row(s), including any interface-blocked or GAP leg |
| AC9 | S37; see scenario row(s), including any interface-blocked or GAP leg |
| AC10 | S57; see scenario row(s), including any interface-blocked or GAP leg |
| AC11 | S47/S47a; see scenario row(s), including any interface-blocked or GAP leg |
| AC12 | S57a; see scenario row(s), including any interface-blocked or GAP leg |
| AC13 | S49b; see scenario row(s), including any interface-blocked or GAP leg |
| AC14 | S49a; see scenario row(s), including any interface-blocked or GAP leg |
| AC15 | S12h; see scenario row(s), including any interface-blocked or GAP leg |
| AC16 | S59; see scenario row(s), including any interface-blocked or GAP leg |
| AC17 | S32; see scenario row(s), including any interface-blocked or GAP leg |
| AC18 | S22d/S22e; see scenario row(s), including any interface-blocked or GAP leg |
| AC19 | S37/S51; see scenario row(s), including any interface-blocked or GAP leg |
| AC20 | S42b; see scenario row(s), including any interface-blocked or GAP leg |
| AC21 | S42b; see scenario row(s), including any interface-blocked or GAP leg |
| AC22 | S49c; see scenario row(s), including any interface-blocked or GAP leg |
| AC23 | S49d; see scenario row(s), including any interface-blocked or GAP leg |
| AC24 | S49e; see scenario row(s), including any interface-blocked or GAP leg |
| AC25 | S22f; see scenario row(s), including any interface-blocked or GAP leg |
| AC26 | S22b/S22d/S22e; see scenario row(s), including any interface-blocked or GAP leg |

### Hands-on rows

U-1…U-11, U-6a, U-13…U-17: **NOT EXECUTED**. These remain the existing spec-time exact-cut tester/independent-validator campaign, not RED-author unit results. U-12 is T10-owned and is not duplicated. The in-product Test Lab follows landing, not this authoring step.

## Gate receipts

All paths below are outside the worktree. Each claimed command has a saved stdout/stderr log and its directly captured exit. A killed harness has a status receipt, never an invented test exit. No hosted CI, full Go suite, Fly cluster, live-model UAT, independent CHECK, GREEN implementation or mutation pass is claimed.

| Receipt | Raw outcome | Scope |
|---|---|---|
| `/workspace/logs/cap-red/baseline-tools.log` + `/workspace/logs/cap-red/baseline-tools.exit` | 0 | Four actual named top-level tools tests (not an unmatched run_task name); initial base |
| `/workspace/logs/cap-red/baseline-agent.log` + `/workspace/logs/cap-red/baseline-agent.exit` | 0 | Five actual admission/semaphore/dispatch baseline tests; initial base |
| `/workspace/logs/cap-red/red-tools-first.log` + `/workspace/logs/cap-red/red-tools-first.exit` | 1 | 21 leaves: 17 intended failures, 4 existing cap-quartet controls; early tree |
| `/workspace/logs/cap-red/red-agent-first.log` + `/workspace/logs/cap-red/red-agent-first.exit` | 1 | Early memory/queue/publication/surfaces; TWO invalid parent-launch fixture failures excluded from behavioural RED |
| `/workspace/logs/cap-red/red-agent-second.log` + `/workspace/logs/cap-red/red-agent-second.exit` | 1 | New snapshots/projections and corrected launch; member plan-store fixture failure excluded, subsequently corrected |
| `/workspace/logs/cap-red/red-final.log` + `/workspace/logs/cap-red/red-final.exit` | NOT RUN / no command-exit receipt | Final scoped CapRED tests in four packages; see assertion/compile distinctions |
| `/workspace/logs/cap-red/race-final.log` + `/workspace/logs/cap-red/race-final.exit` | NOT RUN / no command-exit receipt | Targeted synchronized semaphore/generation/identity tests with -race, not a full suite |
| `/workspace/logs/cap-red/regression-final.log` + `/workspace/logs/cap-red/regression-final.exit` | NOT RUN / no command-exit receipt | Named existing tests in each touched package on final tree |
| `/workspace/logs/cap-red/verify-contracts.log` + `/workspace/logs/cap-red/verify-contracts.exit` | NOT RUN / no command-exit receipt | make verify-contracts, local because it needs git diff; contracts/generated/source trees must remain unchanged |
| `/workspace/logs/cap-red/lint-final.log` + `/workspace/logs/cap-red/lint-final.exit` | 127 | Initial attempt: PATH linter absent (127), not a lint verdict |
| `/workspace/logs/cap-red/lint-pinned.log` + `/workspace/logs/cap-red/lint-pinned.exit` | NOT RUN / no command-exit receipt | Exact-version existing binaries found; pinned-linter job was canceled while awaiting admission; no lint or compile verdict |
| `/workspace/logs/cap-red/static-final.log` + `/workspace/logs/cap-red/static-final.exit` | NOT RUN / no command-exit receipt | Formatting, whitespace, allowed-path and test-size inventory |

The combined final-run attempt was stopped by the tool harness without any `CAP_STAGE_BEGIN`; `/workspace/logs/cap-red/combined-final.log` contains the runner queue and `elicify-runner gate: one job at a time`, not a test result. The direct retry `/workspace/logs/cap-red/red-final.log` contains queue output and a dispatcher finish HTTP 409, then was also stopped by the harness without named test output or a directly captured exit. Both are execution UNKNOWN, not behavioural RED. The local contracts and pinned-linter requests were canceled while still waiting for admission (empty logs). Status receipts are `/workspace/logs/cap-red/combined-final.status.json`, `/workspace/logs/cap-red/red-final.status.json`, `/workspace/logs/cap-red/verify-contracts.status.json`, `/workspace/logs/cap-red/lint-pinned.status.json`. No retry was silently moved off the prescribed runner or given invented success.

## Premises, known conflicts and handoff

- No base-SHA drift: this work started on the exact r6 pin. No fetch/rebase/merge was performed. The existing shared `/workspace/omnipus/CLAUDE.md` is older than this worktree; current task/worktree instructions were followed.
- No obsolete test was weakened, skipped, deleted or silently made to agree with the old implementation. Existing `TestPlanExecuteTool_AlreadyApproved_Idempotent` and older queued/refusal assertions still pin the OLD text. GREEN must replace them with exact r6 expectations in the implementation round, alongside these stronger tests; they are intentionally not production-regression acceptance for the new wording.
- The named snapshot injection interfaces on PlanExecuteTool follow the existing list_jobs source/time seam, but r6 does not prescribe their setter names. GREEN may wire the real observation source differently; adapt only injection setup, retaining the exact independent oracle. Missing setters are explicitly interface RED, not complete publication proof.
- The cron unit fixture accesses the pinned `laneWindow`/`laneSem` fields only to invoke the REAL private dispatch path. R6 removes the fixed channel; update this fixture to the merged engine's real run-window snapshot, never keep a retired channel to appease a test. The release completion barrier currently assumes the lane limit comparison uses `cs.mu`; if the production owner uses a separate lane mutex, supply a real event barrier there rather than a sleep.
- 6b overlap: the new cap_red files avoid direct filename conflicts, but `/workspace/wt-cap/pkg/cron/service.go`, `/workspace/wt-cap/pkg/tools/task.go`, `/workspace/wt-cap/pkg/agent/task_executor_run.go`, `/workspace/wt-cap/pkg/agent/task_run_loop.go`, `/workspace/wt-cap/pkg/agent/task_trigger.go`, `/workspace/wt-cap/pkg/agent/test_helpers_test.go`, `/workspace/wt-cap/pkg/gateway/gateway_boot.go`, `/workspace/wt-cap/pkg/gateway/rest_tasks.go`, `/workspace/wt-cap/docs/settings.md` are likely semantic rebase points (inventory `/workspace/logs/cap-red/t6b-overlap-inventory.txt`). Do not edit shared fixtures merely to hide a new contract.
- Remaining missing cases are NOT declared impossible product requirements: they need additional real producer/resolver/publication test barriers or the T8/T10 integration. No fake gate, fake resolver, fake collector or fake notice implementation was added to manufacture those proofs.
- GREEN work must remove `/workspace/wt-cap/pkg/agent/cap_red_scaffolding.go`, implement real shared policy and generation/live-view APIs, complete all GAP/partial legs, run docs-reference/user-doc audit, and reconcile old message tests. CHECK/mutation proof belongs to an independent instance after real implementation exists.

## Self-check / delivery claims

| Check | Evidence / honest disposition |
|---|---|
| Oracle independence | Every numeric/message expectation derives from final r6, not observed actual output. Setters/hooks are test seams only, never replacement policy. |
| Real boundary | Real registered tools, gates, executor, plan engine, lifecycle/inbox/task stores, collectors and shared Deliver path; only memory/time/model/filesystem edges injected. |
| Scope | New tests + ledger + two compile-only symbols; no existing production implementation changed; no contracts/generated/src edits are authorized. |
| Execution | Foreground heavy invocations; long invocations may be automatically detached/stopped by the harness. No turn is ended with a pending job, and no stopped job is counted as a test result. |
| RED proof | Named assertion failures only; missing APIs distinguished; fixture failures and deadline/compile errors excluded. See per-scenario/receipt table. |
| Completeness | NOT COMPLETE: all live scenarios/datasets/requirements/21 orderings have a ledger row, but GAP/partial rows are not acceptance passes. |
| GREEN / mutation / CHECK | NOT RUN; RED author does not implement production to obtain GREEN or author its own independent CHECK. |
| Commits / push | Commit SHAs are in the handoff report; human author/committer only, no AI trailer. No push, PR, landing, merge or external publication. |
| Code correct and tested | RED test artifact only, with per-command receipts. Production feature correctness is NOT delivered or claimed. |
| Reachable by a user/agent | Existing real entry points exercised where named. New cap-visibility/memory-admission behaviour remains unimplemented and is NOT claimed reachable. |
| Self-check | No skipped/deleted/weakened tests; known gaps visible; only observed runs count; all background jobs joined/finished before final handoff. |

skills: omnipus-shared-rules, elicify-test-writing
