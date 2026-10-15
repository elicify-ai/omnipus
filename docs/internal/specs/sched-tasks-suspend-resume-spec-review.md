# Adversarial Review: Agent scheduled tasks and Suspend / Resume

**Verdict: REVISE.** Round 1 identifies seven MAJOR specification gaps, two MINOR hand-off gaps and one optional observation. The contract and UI review has the same weight as the scheduler review: stale Calendar writes and saved-but-unavailable Resume are unresolved user-facing cases, not backend-only concerns.

| Review header | Value |
|---|---|
| Document reviewed | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec.md` |
| Input design | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/design/sched-tasks-suspend-resume-design-20261015.md` |
| Mode / round | **SPEC / Round 1 of 2 (fixed)** |
| Date | 2026-10-15 (UTC) |
| Reviewed commit | `08af07b45a8b558a5915ea53a5545f1d72572470` on `feat/sched-tasks-20261015` |
| Independence | Fresh reviewer context; this reviewer did not author the specification or design. No spec corrections are made in this round. |
| Method | Complete spec/design reads, relevant architecture/ADR and public-document reads, direct code/contract inspection, controlled searches and an independent document-link audit. GitNexus MCP is unavailable; the checkout-specific CLI reports no index. No graph blast-radius score is claimed. |
| Policy authority | Preserve founder **Q1 B / Q2 YES** as recorded in the spec/design. The L1 candidate `ad853522d1a9bbcbf800ea59b76c337cbada3feb` is inspected as a separate Git object, not treated as code merged into this checkout. |
| Execution limits | No application build, unit/integration/browser suite or user acceptance testing (UAT) was run. Source inspection establishes premises, not a reproduced defect in the future implementation. |

## Executive summary

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 7 |
| MINOR | 2 |
| OBSERVATION | 1 |
| **Total** | **10** |

The specification states the intended safeguards, but several do not yet have a complete state transition, wire contract or discriminating test. In particular, checking an agent when it edits timing does not identify whose authority a future fire will use; delaying notification does not hide a partially created task from recovery; filtering timed tasks from the queue must not also strand an admitted retry.

**Certainty convention:** findings explicitly distinguish **Verified (high confidence)** document/source premises from **Inferred** failure consequences. They are design/test-plan findings, not claims that an unimplemented feature has failed a runtime test. Evidence keys resolve to first-hand reads or saved command receipts in the final table.

## Findings

### MAJOR findings

#### MAJ-001 — Define whose authority each future scheduling commitment retains

| Field | Review |
|---|---|
| Severity / lens | **MAJOR — Ambiguity; Security** |
| Affected sections | §5.2 existing-task `set`/`resume`; §5.3; FR-014/FR-021; BDD-25; T22/T24 |
| Failure scenario | Mia schedules an existing human-created task assigned to Mia, or a task created by another agent. The scheduling check succeeds now. Before the next fire, the relevant delegation permission is removed. If the implementation merely preserves the existing task initiator, a human-created task still has nil authority and the automatic path never checks Mia as the scheduling actor; a different creator can instead cause a valid new commitment to be refused under the wrong identity. Reusing the same field also lets a later explicit run/retry replace the standing schedule authority. |
| Evidence | §5.3 requires current caller authorization and says “Run-time delegation removal still prevents the next automatic run.” It does not define a persisted authority transition for existing-task arming. Current `automaticInitiator` uses `Task.Initiator`; `executeTask` invokes agent delegation authorization only when the resolved initiator is non-nil. `consumeTaskAttempt` can replace `Task.Initiator` with failed-run authority. **E1**. |
| Recommendation | Define the standing authority for create, set and resume, including human operations, and its lifetime across reassignment and explicit runs. Specify how it remains distinct from authority already captured by an admitted attempt. Future admission must use the chosen authority without treating unknown/agent-origin work as a human start. After the founder resolves **FQ-1**, add human-created, different-creator, revoked-authority and concurrent admitted-retry fixtures. Do not choose a new persisted entity merely by default. |
| Certainty | **Verified (high):** missing transition and present resolver/writer behaviour. **Inferred (high):** wrong-actor authorization if existing-task arming preserves that field without another defined authority source; not exploit-tested. |

#### MAJ-002 — Notification ordering is not a complete task/goal publication boundary

| Field | Review |
|---|---|
| Severity / lens | **MAJOR — Incompleteness; Testability & false-green risk** |
| Affected sections | §4.1 paired task-goal pattern; §5.1 creation; §7 storage boundary; FR-001; BDD-07; T16/T18; DS-5 |
| Failure scenario | A recurring scheduled task file is saved, then its paired goal write fails and deletion fails, or the process crashes between those writes. Even if no creation notification is emitted, a recovery scan can discover and arm the incomplete task. BDD-07's “no activation/publication” promise is then false. A test spying only on the creation callback would miss it. |
| Evidence | The prescribed reuse is task/goal persistence followed by notification and existing cleanup. Current `persistAndRespond` calls `CreateByAgent` before `SyncTaskGoalRecord`; `Store.Create` publishes the final task file. `ListWithUnreadable` and `RunRecoverySweep` enumerate task files independently and have no paired-goal-completeness predicate. **E2**. |
| Recommendation | State the publication invariant and the actual cut point that makes creation eligible for scheduler discovery/admission. One possible design is validated/preallocated identity, goal first, then publication of the schedulable task with defined orphan-goal cleanup; another is an explicit completeness fence observed by every discovery/admission path. Require recovery between writes, restart at each cut point and failed-rollback tests against real stores. Do not weaken the already-stated no-publication requirement to “no notification.” |
| Certainty | **Verified (high):** publication and independent discovery paths. **Inferred (high):** incomplete creation can become schedulable if the reused ordering is left unchanged; no crash experiment run. |

#### MAJ-003 — Give queued admitted retries a positive admission rule and regression test

| Field | Review |
|---|---|
| Severity / lens | **MAJOR — Inconsistency with current execution flow; Testability & false-green risk** |
| Affected sections | §5.1 alternate-entry closure; FR-009; BDD-18/BDD-54; T20/T28; DS-6; §9.3 |
| Failure scenario | A timed run is admitted, then its schedule is suspended. An attempt fails; immediate redispatch cannot get capacity and leaves the task at `next`. A queue prefilter based solely on “has a timed trigger” strands this permitted retry, although suspension must not freeze admitted work. |
| Evidence | §5.1 says both “prefilter the queue” and “retries within a run already admitted keep their existing paths.” BDD-18 proves rejection using a manual-task positive control; BDD-54 covers explicit-run context, not the timed-retry/capacity-refusal distinction. `consumeTaskAttempt` writes `next`; `runTask` attempts immediate redispatch; `CheckQueuedTasks` is an established later pickup route. The existing `TestAutoInitiator_F5_RetryAuthoritySurvivesQueuePickup` explicitly protects queue pickup under the failed run's authority, but is not named in §9.3. **E3**. |
| Recommendation | Define the trusted distinction between an unadmitted timed task and a continuation of admitted work at both the queue prefilter and shared admission boundary. Reuse valid existing execution evidence where possible; nil `occurrence_ms` or run kind alone cannot classify this. Preserve the existing queue-pickup regression and add a suspended timed retry with forced capacity refusal followed by successful queue pickup, retaining its authority/budget. Pair it with an indistinguishable-looking but never-admitted timed task that must not run. |
| Certainty | **Verified (high):** current retry/queue route and missing discriminating case. **Inferred (high):** a broad timed-task filter would strand work. This is not a claim that the spec intentionally forbids retries. |

#### MAJ-004 — Clear must not turn waiting timed work into an automatic manual-queue start

| Field | Review |
|---|---|
| Severity / lens | **MAJOR — Ambiguity; Incompleteness** |
| Affected sections | US-1.AC2; §3.1 Clear; §5.1 operation table; BDD-03; T17; DS-5 |
| Failure scenario | An existing `next` task is given a time trigger and waits correctly under the new timed-task queue exclusion. Clear then replaces only its trigger with manual. On the next queue drain, it is now an ordinary assigned manual `next` task and starts automatically. The operation removed timing but caused exactly the execution the no-start promise rules out. |
| Evidence | Clear is defined as canonical manual/no suspension, without defining a transition or admission fence for an idle `next` task. BDD-03 checks “no immediate run,” which can pass if the later drain has not run. Current `CheckQueuedTasks` dispatches assigned `next` tasks with satisfied dependencies. The proposed timed-task exclusion ceases to apply once timing is cleared. **E4**. |
| Recommendation | Define Clear's locked transition for not-yet-admitted ready and dependency-waiting tasks so removing timing alone cannot make them newly dispatchable. Specify the observable resulting state and keep already-admitted attempts/retries intact. Test a real later queue drain and dependency completion, not only the Clear handler's call stack. The existing no-start decision is settled; it does not need another founder vote. |
| Certainty | **Verified (high):** undefined Clear transition and existing queue predicate. **Inferred (high):** automatic start after a trigger-only clear. |

#### MAJ-005 — Calendar's promised stale-rule protection has no REST precondition

| Field | Review |
|---|---|
| Severity / lens | **MAJOR — Contract-first gaps; Infeasibility; UI states & journey gaps** |
| Affected sections | US-6.AC3; §5.5 TaskUpdateRequest delta; §6.2; FR-031; BDD-45; T11/T31 |
| Failure scenario | Calendar A reads a series. Another Calendar or an agent saves a newer rule. A later genuine recurrence edit from A arrives after that save and overwrites it. Omitting the trigger for title-only saves fixes a different case; a refetch before save cannot close the interval between that read and the write. |
| Evidence | The tool gets `expected_schedule_hash`, while §5.5 gives PATCH only an input-only `type/config` trigger change. No REST conditional header/body token or failure contract is specified. Current `TaskUpdateRequest`/`updateTask` define no such precondition, and `taskPatch.applyUpdate` uses an unconditional locked update. §6.2 nevertheless requires handling a stale-rule conflict without writing the old form over the current schedule. **E5**. |
| Recommendation | Define the generated contract-first conditional-write mechanism for trigger-changing PATCHes, the baseline supplied to Calendar, the comparison under the task lock and the exact conflict response. A schedule token or documented conditional header can satisfy this; a separate generic entity-version redesign is unnecessary. State how both views preserve the user's draft and offer read/review/reapply after conflict. Add a barrier-controlled stale-save test through the real HTTP path. |
| Certainty | **Verified (high):** requested guarantee and missing prescribed wire mechanism. **Inferred (high):** lost update without a server-enforced precondition. |

#### MAJ-006 — Define the HTTP and UI outcome when Resume saves but cannot arm

| Field | Review |
|---|---|
| Severity / lens | **MAJOR — Contract-first gaps; UI states & journey gaps** |
| Affected sections | US-1.AC4/US-6.AC2; §3.4 HTTP matrix; §5.5 suspend/resume endpoints; §6.2; BDD-08/BDD-44; T26/T31 |
| Failure scenario | Resume persists `suspended=false`, then cron registration fails. A 200 containing only Task makes the UI look resumed and operational without any activation warning. A generic failure makes the opposite mistake if it keeps displaying persisted Suspended. A later Task read confirms the flag but still cannot report whether activation is available using the specified response delta. |
| Evidence | §5.2 explicitly separates saved state from `scheduler_state=unavailable` in the tool result. The state POSTs instead specify 200 Task and 500 only for storage failure; Task has no activation outcome field, and the contract delta adds none to it. BDD-44 does not name persistence-success/registration-failure. Current `OnTaskUpserted` logs registration errors and returns void; the spec correctly requires an observable outcome, but does not complete that requirement for these endpoints. **E6**. |
| Recommendation | Specify the generated HTTP outcome, status/body and read-back behaviour for saved-but-unavailable activation, including exhausted/no-future-fire versus activation failure. Define the partial state in both controls: the flag is active but automatic starts are unavailable, with actionable recovery and no false “nothing saved” claim. Either a typed outcome accompanying Task or another documented typed response can work; the architect/author should select the contract, not defer a wire-shape choice to frontend implementation. Test the real endpoint and both controls with registration failing after a successful write. |
| Certainty | **Verified (high):** tool/HTTP contract asymmetry and missing UI case. **Inferred (high):** misleading activation or suspension feedback with the prescribed endpoint shape. |

#### MAJ-007 — Bound occurrence work and output across the whole request

| Field | Review |
|---|---|
| Severity / lens | **MAJOR — Security (resource exhaustion); Incompleteness** |
| Affected sections | §3.4 occurrence constraint; §5.6; FR-025; BDD-36; T09/T27; DS-4 |
| Failure scenario | A permitted agent owns thousands of series and asks for a large valid range without `task_id`. Each series respects its 500-instant/10,000-iteration limit, yet the combined request can allocate millions of positions or day buckets and return an oversized tool result. The 100-row task-list cap does not constrain the specified occurrence view. |
| Evidence | Limits in §3.4 and DS-4 are per task; §5.6 permits all authorized series with optional task_id and prescribes no aggregate output/work budget. The reused `buildOccurrenceSets` loops over every supplied task; its constants are explicitly per task. `maxTaskListRows` is the default task-list limit, not an occurrence-request limit. **E7**. |
| Recommendation | State a finite request-wide expansion/output budget and deterministic behaviour at its boundary, shared by REST and tools. An actionable refusal asking for a narrower task/range is sufficient; pagination is not automatically required. Do not silently return a partial calendar as complete. Include many authorized series and exact budget/+1 cases, not only one series at each per-task cap. Preserve filtering before expansion. |
| Certainty | **Verified (high):** per-task-only specified bounds and reused loop. **Inferred (medium):** practical memory/context exhaustion depends on workload; not load-tested. |

### MINOR findings

#### MIN-001 — Add schedule-mutation audit acceptance to the existing audit path

| Field | Review |
|---|---|
| Severity / lens | **MINOR — Security (repudiation)** |
| Affected sections | §5.1/§5.3; §7; §9; FR-002/FR-007 |
| Failure scenario | A person later needs to determine who changed availability or cleared a rule. Generic tool-call records may show a call, but the new endpoint/domain path is not required to record the actual committed schedule transition, so the saved change cannot reliably be reconstructed. |
| Evidence | The specification has no mutation-specific audit requirement/test. Existing `auditTask` and `auditTriggerChange` are explicit handler calls, not a store-wide automatic guarantee; `auditTriggerChange` deliberately excludes manual/once destinations and identical triggers. Generic audit coverage is **not** asserted absent. **E8**. |
| Recommendation | Require existing-audit integration for create/set/clear/suspend/resume, with actor, workspace/task, action, before/after schedule identity and outcome; specify no-op and audit-write-failure behaviour consistent with existing conventions. Test UI and tool entry points. Do not add a suspension-history ledger or duplicate full task instructions in audit records. |
| Certainty | **Verified (high):** acceptance omission and narrow existing hook. **Inferred (medium):** insufficient semantic reconstruction without explicit integration. |

#### MIN-002 — Complete the required design hand-off scaffolding

| Field | Review |
|---|---|
| Severity / lens | **MINOR — Incompleteness; Contract-first gaps; Design-system reuse & brand** |
| Affected sections | §1 authority; §5 normative backend/contract design; §6.5 interaction acceptance |
| Failure scenario | Implementation starts from detailed mechanisms without an explicit ADR-applicability ruling or named shared UI dependencies; the contract inventory is encountered only after the mechanism sections. Reviewers cannot confirm those prerequisites from the intended hand-off sections and must reconstruct them, adding avoidable cross-tree rework. This is a hand-off defect, not evidence that the planned runtime or UI already violates a rule. |
| Evidence | The full spec cites previous ADRs but never explicitly says whether this feature needs a new ADR and why. §5.1–§5.4 precede §5.5's exact contract inventory. §6.5 says “Use catalogued controls, badges, inline error and loading primitives” without a catalogue-first component map. The grill's structural checklist requires all three. The actual catalog already includes Button, Badge, Sheet, FormError and QueryErrorState. **E9**. |
| Recommendation | Record the ADR/no-ADR determination after the required founder interview, without manufacturing an ADR for a resolved choice. Put a concise contract inventory before mechanism detail, preserving the already-correct generation-before-consumers order. Add a small catalog-backed component/reuse map for controls, pending/error feedback and approval containment; state that no new public component family is commissioned. No visual redesign is requested. |
| Certainty | **Verified (high):** document structure and catalog entries; no runtime failure inferred from section order alone. |

### Observation

#### OBS-001 — Keep the automated browser policy matrix thin

| Field | Review |
|---|---|
| Severity / lens | **OBSERVATION — Overcomplexity** |
| Affected sections | T35 versus T06/T07/T22 and UAT-07/UAT-08 |
| Suggestion | Keep the full role/default/upgrade/operator combinations in unit/integration tests. Use a smaller browser test to prove real discovery, approval and refusal wiring. Preserve all mandated hands-on UAT rows and their independent validation; this is an optional cost reduction, not a release blocker. |
| Evidence / certainty | §9.1 and §12 repeat these dimensions at several levels. **Verified (high)** plan overlap; actual runtime/cost saving is **Unknown** because these tests are not implemented. **E10**. |

## Structural integrity

The independent **document-link** audit found **7 stories, 28 acceptance criteria, 54 BDD scenarios, 35 planned tests and 34 functional requirements**. It checks identifiers and links, not behavioural completeness. Three deliberately broken in-memory documents were detected: removed scenario/test links, a removed acceptance back-reference and an unknown matrix requirement. No repository source was mutated for those controls. **E11**.

| Required check | Result | Evidence / qualification |
|---|---|---|
| Status field | PASS | Draft, with explicit review/testing limitations. |
| ADR linked, or explicit no-ADR determination | PARTIAL — MIN-002 | Relevant existing decisions cited; feature-specific determination absent. |
| Contract inventory before implementation detail | PARTIAL — MIN-002 | Exact delta exists in §5.5, after §5.1–§5.4. Generation before consumers is separately and correctly required. |
| API/data and integration boundaries | PRESENT; REVISE | §5/§7; MAJ-001/002/005/006/007. |
| UI loading/empty/error/partial states | PRESENT; REVISE | §6.2–§6.4 covers saving, invalid/missing preview and empty/error reads; saved-but-unavailable activation is missing (MAJ-006). |
| End-to-end journeys | PASS | §2 stories, §6 controls/approval flows and §12 actual invocation/acceptance journeys. |
| Accessibility and keyboard | PASS at specification level | §6.5 and T15/T31 specify keyboard action, focus, announcements, narrow/reflow/forced-colour acceptance. No rendered accessibility result claimed. |
| Catalog-first shared component map | PARTIAL — MIN-002 | Shared-kit intent is explicit; named dependency mapping is not. |
| Security/user promises | PRESENT; REVISE | §5.3/§5.4/§7/§13; future authority, aggregate bounds and audit acceptance need revision. |
| BDD values/oracle independence | PASS, with coverage gaps below | §8/§9 fixed fixtures and independent expected values; no implementation-derived oracle is prescribed. |
| Story → acceptance → BDD links | PASS | E11: all stories/acceptance criteria covered; each scenario has a valid back-reference. |
| Requirement → scenario → planned-test links | PASS | E11: all 34 FR and 54 BDD appear; linked tests cover each row's scenarios. A valid link is not proof the planned assertion distinguishes the failure. |
| Reachability | PASS as a plan, not delivery | §5.4/§11 name real registration/policy paths and both existing detail surfaces; §12 requires actual execution. |
| User-facing documentation TODOs | PASS as a plan | DOC-01–DOC-05 name affected existing pages, concrete updates and docs-verifier ownership. This review-only change needs no public behaviour-doc update because it changes no product behaviour. E13. |
| Regression impact and execution gates | PRESENT; REVISE | §9.3/§9.4 name tests, CI, CHECK and exact-commit UAT. Add the admitted-retry queue regression in MAJ-003. |

## Twelve-lens coverage

An explicit no-additional-defect assessment is not counted as an invented finding. The following records all twelve lenses; accessibility and reachability were checked against real sources, not skipped because this is a written spec.

| Lens | Disposition |
|---|---|
| 1 — Ambiguity | MAJ-001/004: standing future authority and Clear's ready-state transition. |
| 2 — Incompleteness | MAJ-002/004/007, MIN-002: publication, lifecycle transitions, aggregate bounds and hand-off prerequisites. |
| 3 — Inconsistency with ADRs / as-is | MAJ-003: preserve current retry queue path. The spec correctly separates timing from execution status and flags removed legacy-timing claims; no additional retired-surface restoration established. E14. |
| 4 — Infeasibility | MAJ-005: a lock and client refetch cannot reject an unversioned stale PATCH. No new prohibited runtime dependency is proposed. |
| 5 — Contract-first gaps | MAJ-005/006 and MIN-002. Generated-only input/response/WS contracts and backend-owned regeneration are otherwise explicit in §5.5. |
| 6 — Security | MAJ-001/007 and MIN-001. Exact prepared approval, foreign filtering, current policy and unattended refusal have explicit negative cases; no additional defect established from the inspected grant/approval paths. E1/E7/E8/E12. |
| 7 — Reachability | No additional defect established. §5.4 names static and live paths, reload/late binding, role/ceiling/Auto classification; §11/§12 require real invocation. Current absence is a known baseline gap, not a claim of delivered reachability. E12. |
| 8 — UI states/journeys | MAJ-005/006. Both existing surfaces and readable live/reconnect approvals receive concrete rendering/error coverage. Calendar routes one-time/due chips to existing task detail and recurring chips to its series editor; no new Schedules screen is assumed. E5/E6/E12. |
| 9 — Accessibility/keyboard | No additional defect established: §6.5 explicitly covers semantic controls, Enter/Space, focus containment/return, announcements and narrow-screen full-payload access. Actual conformance still requires T15/T31/UAT; no screenshot or browser assertion is claimed. E9/E10. |
| 10 — Design-system reuse/brand | MIN-002. Spec retains the existing brand, no raw scheduling-syntax controls or colour-only meaning, and does not commission new visual tokens. Catalog-backed dependencies remain to be named. E9. |
| 11 — Testability/false-green risk | MAJ-002/003/004/005/006/007: add discriminating integration tests. Reference completeness passes E11 but cannot prove runtime safety; prescribed CI/independent CHECK/UAT are not substituted by this audit. |
| 12 — Overcomplexity | OBS-001. The shared query extraction has two actual consumers (REST/tools); existing task/cron/policy/run layers remain. No evidence warrants proposing another scheduler, history ledger, database or policy fallback. |

### UX assessment and priority actions

**Several issues to address.** This assesses the written flow, not rendered appearance.

| Relevant heuristic | Concrete review consequence |
|---|---|
| H1 — Visibility of system status | MAJ-006: separate “saved active” from “automatic firing unavailable.” |
| H3 — User control | MAJ-004: removing timing must not cause an unrequested automatic start. |
| H5 — Error prevention | MAJ-005: stale Calendar writes need a server-enforced precondition. |
| H9 — Recovery | MAJ-005/006: preserve drafts and provide truthful read-back/retry for conflicts and partial activation. |

| Priority | Action |
|---|---|
| 1 | Resolve future-run authority and completion/admission publication (MAJ-001/002). |
| 2 | Complete stale-save and saved-but-unavailable HTTP/UI contracts (MAJ-005/006). |
| 3 | Prove the queue distinguishes admitted retries and Clear from new automatic work (MAJ-003/004). |

## Test coverage assessment

| Missing discriminating case | Required level / dataset change | Finding |
|---|---|---|
| Scheduling actor differs from task creator/human origin; later authority revoked; explicit run/retry cannot rewrite standing commitment | Real authorization + stores; extend T22/T24 and DS-3 | MAJ-001 |
| Recovery reads creation between task/goal writes; rollback deletion fails; process restarts at publication cut points | Store/scheduler integration; T16/T18, DS-5 | MAJ-002 |
| Suspended admitted timed attempt hits capacity refusal, then queue pickup succeeds with original authority; never-admitted twin refuses | Executor/queue integration plus named regression; T20/T28, DS-6 | MAJ-003 |
| Clear from idle `next` and dependency-waiting state followed by actual drain/unblock | Domain/executor integration; T17, DS-5 | MAJ-004 |
| Genuine recurrence saves from two views arrive out of date order; conflict preserves draft | HTTP barriers plus component/browser recovery; T11/T31, DS-5 | MAJ-005 |
| State persistence succeeds, registration fails, both controls display partial truth | Real endpoint fault plus mounted controls; T26/T31, BDD-44/DS-5 | MAJ-006 |
| Many authorized series cross request-wide work/output limit; exact boundary/+1, deterministic error/partial handling | Shared-query + REST/tool parity; T09/T27, DS-4 | MAJ-007 |
| Tool and UI scheduling changes produce committed-transition audit metadata, including no-op/failure treatment | Existing audit integration; extend §9 | MIN-001 |

The plan already requires unit → integration → end-to-end execution, fixed calendar/DST fixtures, generated-validator positive controls, `npm run typecheck`, CI for heavy Go/race gates, independent CHECK and exact-commit tester/validator UAT. These are requirements read in §9/§12, **not executed checks**. The gaps above are where a passing planned test could still miss the stated failure.

## STRIDE threat summary

STRIDE groups threats as **S**poofing identity, **T**ampering, **R**epudiation, **I**nformation disclosure, **D**enial of service and **E**levation of privilege. “Specified” below means the document has a mitigation, not that runtime protection passed.

| Component / data flow | S | T | R | I | D | E | Review |
|---|---|---|---|---|---|---|---|
| Tool identity / ownership / future delegation | Specified | MAJ-001 | MIN-001 | Scoped | Not new | MAJ-001 | Persist the future commitment's actual authority; do not inherit a nil human bypass. |
| Prepared proposal / grants / reconnect | Specified | Specified | Existing mechanisms | Escaped/scoped | Existing expiry | Specified | Fixed args, state recheck and invalid-preview refusal are required; no additional defect verified. |
| Task/goal publication / scheduler discovery | Internal | MAJ-002 | MIN-001 | No new disclosure found | Recovery eligibility | Authority above | Independent discovery must observe a complete publication. |
| Queue / dependency / follow-up / Clear | Internal | MAJ-003/004 | Existing run truth | No new disclosure found | Stranded retry risk | MAJ-004 | New-run fences and admitted continuations must differ. |
| REST / Calendar state controls | Existing auth | MAJ-005 | MIN-001 | Generated payload | MAJ-006 recovery | Existing auth | Conditional writes and partial activation need complete wire/UI definitions. |
| Occurrence / history reads | Scoped | Server authority | Recorded history | Filter before expansion | MAJ-007 | Scoped | Per-task limits do not bound a whole request. |
| Restart / older-binary downgrade | Not changed | Explicit warning | Existing records | Not changed | Unsafe rollback disclosed | Not changed | Spec explicitly warns that an older binary may ignore suspension; no extra deployment defect established. |

## Reachability check

| Question | Result | Evidence |
|---|---|---|
| Does this commit implement the new tool? | **No — expected baseline gap.** Controlled literal searches find `create_task` in each required tree and no `schedule_task` in them (search exit 1 each). | E12 controlled search receipt. This is a static absence check, not a live invocation test. |
| Does the spec cover both metadata/policy and live implementation? | **Yes, planned.** §5.4/§11/T23 require all of them together and actual ToolSearch/execution. | Existing live wiring and two-layer resolver read, E12. |
| Are both human controls attached to real entry paths? | **Yes, named and source-traced.** Calendar recurring chips use the series editor; one-time/due chips use existing task detail. | E12. Controls still need implementation/UAT. |
| Does the plan require execution rather than just written tests? | **Yes.** §9.4/§12 distinguish RED/GREEN/CHECK, UI invocation, exact-commit UAT and independent validation. | Spec §9.4/§11/§12, E10. |

**Code correct and tested:** Not established for the future feature; no implementation tests were run in this review.

**Reachable by a user/agent:** Not delivered at the reviewed baseline; reachability is specified and must later be demonstrated on the implementation commit.

## Unasked questions for the specification author

These are technical corrections/clarifications for the author and implementing leads, not extra founder interviews.

| Topic | Required clarification |
|---|---|
| Publication | Which complete-publication boundary is observed by boot, recovery and due admission, including failed cleanup? |
| Queue and Clear | What trusted evidence distinguishes an admitted retry, and what locked Clear transition prevents newly eligible idle work? The existing no-start meaning remains binding. |
| Calendar contracts | Which conditional-write and partial-activation shapes will backend-lead define/regenerate before consumers? HTTP schema selection is architect-owned work. |
| Query limits / audit / hand-off | What aggregate budget, semantic audit acceptance and named catalog/ADR prerequisites complete the existing requirements? |

## Questions for the founder

Only the standing **future-run authority** choice is genuinely open. Q1 B (no existing-worker migration/backfill) and Q2 YES (Omnipus may schedule through inherited global Ask/AutoAsks) remain settled. Clear must not cause a start, suspension must not stop admitted work, and missed runs must not replay; none is reopened here.

### FQ-1 — Whose permission should future fires use after an agent sets or resumes another origin's task? (A / B)

**Context and impact:** a task's creator, the agent making its latest future scheduling commitment and the starter of its latest explicit run can be different actors. For a human-created task, the current automatic initiator is nil. The choice determines whose later permission removal stops future scheduled execution and what the approval is authorizing. It also determines what must be persisted separately from an already-admitted retry.

| Option | Meaning / consequence |
|---|---|
| **A — Retain the original standing task authority** | Set/Resume checks the caller at mutation time, but later fires remain under the task's previous creator/assigner authority, including human origin. Document explicitly that removing the new scheduling caller's permission does not revoke that standing commitment. The author must reconcile BDD-25's caller-based wording. |
| **B — Bind to the actor making the latest arming commitment — recommended** | Set/Resume establishes that actor's continuing execution authority; future fires recheck it. Human and agent commitments remain distinguishable. Preserve authority already captured by admitted runs/retries; an unrelated explicit Run now must not replace the standing commitment. Define same-actor no-op and human-control behaviour explicitly. |

**Recommendation: B.** It best matches the spec's promise to re-authorize the scheduling caller before future execution and prevents agent scheduling from borrowing human-origin authority. It requires explicit state ownership, not another tool-policy layer. Team-lead should capture the founder answer before the author chooses the persistence shape. Answer in one line: **“FQ-1 A”** or **“FQ-1 B.”**

## Verdict rationale and next step

**REVISE**, not PASS. MAJ-001/002 leave future execution authority and complete publication undefined; MAJ-003/004 leave automatic-entry transitions insufficiently specified/tested; MAJ-005/006 leave user-facing guarantees without a complete server contract; MAJ-007 leaves aggregate query work unbounded. No CRITICAL finding is asserted as a proven incident/security hole: these are verified spec gaps with explicitly inferred future consequences, not reproduced implementation defects. The typed triage helper escalated its close REVISE/BLOCK choice; the reviewer took that decision back and selected REVISE on this basis. **E15**.

This is **grill round 1 of 2 (fixed)**. Next: team-lead interviews the founder on **Questions for the founder**, then the spec author fixes round-1 findings in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec.md`. Run **SPEC MODE ROUND 2** on that corrected spec regardless of this round's verdict. No fixes, additional grill round or implementation landing are performed by this review.

skills: grill-spec, omnipus-shared-rules, gitnexus-exploring, ux-heuristics-review, omnipus-design-system, jev-use:jev-use, github-cli, commit-messages

## Evidence table

All source evidence below was inspected first-hand in this review at the baseline above. The table does not adopt the spec author's evidence claims as execution receipts.

| Claim | Evidence | Certainty |
|---|---|---|
| E0 — Correct baseline and unused round-1 slot | `git branch --show-current` exit 0: `feat/sched-tasks-20261015`; `git rev-parse HEAD` exit 0: `08af07b45a8b558a5915ea53a5545f1d72572470`; initial `git status --short` exit 0: empty. Initial document audit records both review paths absent with the spec present in `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-grill1-evidence-20261015.wM8fHu/spec-links-initial.json`. | **Verified (high)** |
| E1 — Standing automatic authority differs from mutation caller and retry authority | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_executor.go`::automaticInitiator / executeTask; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_run_loop.go`::consumeTaskAttempt / retryAuthority; reviewed spec::§5.2/§5.3/BDD-25. | **Verified source (high)**; MAJ-001 consequence **Inferred (high)** |
| E2 — Task-first publication and independent recovery | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/task.go`::taskCreateToolExecute.persistAndRespond; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/task/store.go`::Store.Create / CreateByAgent / ListWithUnreadable; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_trigger.go`::RunRecoverySweep; reviewed spec::§4.1/§5.1/§7/BDD-07. | **Verified source (high)**; MAJ-002 consequence **Inferred (high)** |
| E3 — Retry may need later queue pickup | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_executor_run.go`::TaskExecutor.runTask; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_run_loop.go`::consumeTaskAttempt; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/sessioncore_initiator_auto_test.go`::TestAutoInitiator_F5_RetryAuthoritySurvivesQueuePickup; reviewed spec::BDD-18/BDD-54/§9.3. Test read, **not run**. | **Verified source (high)**; MAJ-003 consequence **Inferred (high)** |
| E4 — Manual Next tasks can drain after trigger-only Clear | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_executor.go`::CheckQueuedTasks; reviewed spec::§5.1 Clear / BDD-03 / BDD-18. | **Verified source (high)**; MAJ-004 consequence **Inferred (high)** |
| E5 — PATCH lacks a scheduling precondition | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskUpdateRequest.yaml`::TaskUpdateRequest; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/openapi.yaml`::updateTask; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/rest_tasks.go`::taskPatch.validate / buildTimingPatch / applyUpdate; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/calendar/CalendarEventSlideOver.tsx`::buildTriggerForSave / handleSave; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/lib/api/tasks.ts`::updateTask; reviewed spec::§5.5/§6.2. | **Verified source (high)**; MAJ-005 consequence **Inferred (high)** |
| E6 — Saved state and activation need different HTTP/UI facts | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/Task.yaml`::Task; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_trigger.go`::OnTaskUpserted / replaceJobLocked; reviewed spec::§3.4/§5.2/§5.5/§6.2/BDD-08/BDD-44. | **Verified source (high)**; MAJ-006 consequence **Inferred (high)** |
| E7 — Limits are per task, not aggregate occurrence-request limits | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/task_occurrences.go`::buildOccurrenceSets / buildOneOccurrenceSet / perTaskInstantCap / perTaskIterationBudget; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/task_query.go`::maxTaskListRows; reviewed spec::§3.4/§5.6/DS-4. | **Verified source (high)**; MAJ-007 workload consequence **Inferred (medium)** |
| E8 — Existing semantic audit hook is narrow; new audit acceptance absent | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/rest_tasks.go`::auditTask / auditTriggerChange; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/security.md`::What it is / When you would use it; full reviewed spec::§5/§7/§9. | **Verified source (high)**; reconstruction risk **Inferred (medium)** |
| E9 — Required scaffolding and available shared UI components | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/.claude/skills/grill-spec/SKILL.md`::Phase 1 / lenses 5, 9, 10; reviewed spec::§1/§5/§6.5; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/design-system/catalog.json`::entries (Button / Badge / FormError / QueryErrorState / Sheet); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/design/design-system-definition.md`::Operating model / The rules in plain language; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/brand/brand-guidelines.md`::Brand Voice & Tone; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/ui/CLAUDE.md`::No hand-built controls. Catalog extraction exit 0: `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-grill1-evidence-20261015.wM8fHu/catalog-components.json`. | **Verified (high)** hand-off requirements/content; not rendered UI evidence |
| E10 — Test-level overlap, observable acceptance and mandatory UAT | Reviewed spec::§9.1 T06/T07/T22/T35 / §9.4 / §12 UAT-07/UAT-08; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/CLAUDE.md`::Definition of Done / Spec-Driven Workflow; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/false-green-patterns.md`::Checklist: before you claim something passes. | **Verified (high)** planned obligations; execution/cost **Unknown** |
| E11 — Reference consistency and instrument negative controls | `python3 -I /Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-grill1-evidence-20261015.wM8fHu/check_spec_links.py` exit 0: `stories=7`, `acceptance_criteria=28`, `bdd_scenarios=54`, `planned_tests=35`, `functional_requirements=34`, `errors=[]`; three broken-document controls detected. Initial receipt: `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-grill1-evidence-20261015.wM8fHu/spec-links-initial.json`; final rerun/scope receipts: `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-grill1-evidence-20261015.wM8fHu/final-check.log`. | **Verified document check (high)**; not behavioural test coverage |
| E12 — Controlled reachability, real detail entry paths and policy/approval seams | Controlled `rg -l -F` searches: create_task exit 0 in each of coreagent/config/tools, schedule_task exit 1 in each; `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-grill1-evidence-20261015.wM8fHu/reachability.json`. Source: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/loop_wire.go`::registerTaskAndPlanTools; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/compositor.go`::resolveEffectivePolicyWith; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/config/validate.go`::ReconcileToolPolicyCeiling; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/loop_run_turn_tools.go`::resolveAskPolicy / requestAskApproval; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/ws_tool_approval.go`::broadcastToolApprovalRequired; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/asyncapi.yaml`::ToolApprovalRequiredFrame / SessionStatePendingApproval; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/agents/approvalPreviews/registry.ts`::TOOL_APPROVAL_PREVIEWS; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/screens/CalendarScreen.tsx`::handleEventClick / CalendarScreen; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/workspaces/TaskDetailPanel.tsx`::TaskDetailPanel. | **Verified source/search (high)**; no live reachability result |
| E13 — Specific matching public-doc plan, not missing documentation work | Reviewed spec::DOC-01–DOC-05; read `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/calendar.md`::How to edit or stop a series / Limits and things to watch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/tasks.md`::The task detail panel; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/tools.md`::Allow, ask and deny; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/reference/built-in-tools.md`::Built-in tools; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/operations/tools-configuration.md`::Cron Tool. | **Verified (high)** destinations/affected guidance; future update accuracy not certified |
| E14 — Architectural decisions and source precedence checked | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/AS-IS-architecture.md`::Tools System / Contract-first wire-format pipeline; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/plugin-extensibility-assessment.md`::Tools — not a plugin system today. Read ADRs by title: **RRULE (RFC 5545) as the Recurrence Model for Task Triggers**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/ADR-049-rrule-recurrence-model.md`::Decision / Input-bounds policy / DST policy; **Per-Task Run History (an additive execution-record layer)**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/ADR-050-task-run-history-model.md`::RD2/RD6/RD7/RD8; **Two-layer tool-policy model: the reconciled global ceiling IS the default; remove the fail-closed per-agent backfill**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/ADR-077-two-layer-tool-policy-remove-fail-closed-backfill.md`::D1/D3/D6; **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::D5/D11/D12; **The Omnipus system agent: one technical partner, workflows as skills, a narrow ask list and a secure secret field**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md`::OSA-1/OSA-3. Older architecture/recurrence passages are not used to override current code or the spec's explicit founder amendment. | **Verified source (high)**; scope comparison, not implementation approval |
| E15 — Decision support is advisory; close verdict escalated back | `jev-use judge` exit 3, indicating escalation, not a runtime test failure: overall REVISE confidence 0.40, `escalate=true`; other triage choices retained MAJOR gaps, MINOR audit/scaffolding, one genuine founder question and optional browser simplification. Reviewer independently selected REVISE as explained above. Receipt: `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-grill1-evidence-20261015.wM8fHu/triage-result.json`. | **Verified receipt (high)**; final verdict is reviewer judgment |
| E16 — Graph limitation and pinned candidate, not a fabricated impact run | `gitnexus status` exit 0: `Repository not indexed`; checkout-specific query exit 1: repository not found, receipt `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-grill1-evidence-20261015.wM8fHu/gitnexus-query.log`. Candidate `git show` reads exit 0 for seed and role inventory: `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-grill1-evidence-20261015.wM8fHu/l1-seed.go`::RestrictForWorkerTier and `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-grill1-evidence-20261015.wM8fHu/l1-role-policies.go`::omnipusSupportedTools / omnipusSeedPolicies. No production symbol edited. | **Verified limitation/candidate (high)**; transitive impact **Unknown**, not needed for review-only edit |
| **Self-check** | Final artifact re-read through its staged diff; all twelve lens dispositions, each finding's scenario/evidence/severity/certainty, the one genuine founder question, fixed round-2 next step, absolute paths, counts and skills acknowledgement checked. Document-link/instrument controls and diff whitespace/scope checks rerun; only this review artifact is added and the reviewed spec/design/source/contracts/tests remain unchanged. Receipts: `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-grill1-evidence-20261015.wM8fHu/final-check.log`. No implementation-test, UAT, merge or feature-completion claim. | **Verified review self-check (high)**; implementation acceptance **Unknown** |
