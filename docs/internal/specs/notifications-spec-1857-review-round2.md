# Adversarial Review: Notifications — #1857 amendment, final round

**Correction:** the initial round-2 report said 5 round-1 findings were addressed and 6 partial. That was wrong because the specific R1-MAJ-008 nonfatal-run control and R1-MIN-003 baseline corrections are present; C11 introduces separate new defects. Correct is **7 addressed and 4 partial**. Impact: the old findings are closed, the new C11 issues remain under R2-MAJ-005, and the active count stays 6 MAJOR / 1 MINOR.

**Document reviewed:** `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec.md`

**Mode:** SPEC — amendment only

**Round:** Round 2 of 2 (final; no third grill)

**Review date:** 2026-11-15

**Verdict:** **REVISE**

**Reviewed revision:** `5616f4b4ef3c5dc46b48c1615b242bd5af26c718`, branch `landing/notif-1857-spec`

**Correction diff:** `a2e268220..5616f4b4ef`, limited to the specification above; 161 additions, 66 deletions.

**Round-1 report:** `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec-1857-review.md`, at `a2e268220`.

**Production reference:** `/workspace/wt/rel-head-1857`, revision `5d20e539fa63624189d82f71c45418512b219222`. All production-source conclusions below refer to this revision, not the older specification worktree.

## Executive summary

**REVISE: 6 MAJOR and 1 MINOR active findings.** Seven of the eleven round-1 findings are fully addressed in the specification; four are partially addressed. The correction introduces incorrect execution-state claims, still permits one explicitly known cause to render as unknown, leaves several promised outcomes unspecified, and offers a persistence-recovery instruction that can repeat already-completed work.

The parent-surface split, separate resume/workspace explanations, upstream-refusal attribution, real-adapter test plan and accessibility sampling are materially improved. Frontend contracts, live/reload rendering, Settings, parent navigation and recovery wording were reviewed alongside backend callers; this is source-level specification review, not browser or implementation acceptance.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 6 |
| MINOR | 1 |
| OBSERVATION | 0 |
| **Total active findings** | **7** |

### Scope and evidence rules

- The founder-approved base specification is not reopened. Accepted architect rulings R-2, R-3, R-4, R-5's contract shape and R-6 remain constraints, not new questions.
- Existing Q1–Q4 remain unanswered. Their status and consistency were checked; no option was selected and none is re-asked below.
- Author claims were checked against `/workspace/logs/1857/draft-report.md::Grill round 1 fix pass`, the new §13.8.5 matrix and the actual production callers. Finding a role string or an emission in a helper is not evidence that its caller continues.
- GitNexus is unavailable for this commission. Direct reads, targeted `rg` and read-only Git inspection were used. No graph analysis, builds, tests, browser runs, issue changes, commits or pushes were performed.
- **Verified** means the cited specification or source was inspected; **Inferred** identifies a consequence of implementing the specification as written. No runtime reproduction is claimed.
- The counts below do not count an old finding twice: the disposition table links each remaining problem to its round-2 finding.

## Round-1 disposition audit

“Addressed” means corrected in the specification, not implemented or tested.

| Round-1 finding | Disposition | Evidence and remaining issue |
|---|---|---|
| 1857-R1-MAJ-001 — save stages | **Partial** | B7/B11/B19/B20 and W16 now distinguish named stages, but `recordToolCalls` actually stops the turn; entry into finalization does not establish an answer; the late gateway failure also fails the turn. W15's new repeat-work remedy is not a save operation. See R2-MAJ-001 and R2-MAJ-006. |
| 1857-R1-MAJ-002 — resume versus workspace | **Addressed** | W14 and its neutral twin bind separate explanations to `errExternalResumeUnavailable` and `errExternalWorkspaceChanged`; S-141 includes an unchanged-workspace control. The remaining slot-table defect belongs to the matrix-completeness finding, not a reopening of the cause split. |
| 1857-R1-MAJ-003 — parent surfaces | **Addressed** | §13.8.4 now separately names the direct tool result, completion/inbox message and human lifecycle summary. S-141/T-56/U27 cover summary → Open → child error and the completion route without changing the approved summary design. Source checks confirmed these are distinct paths. |
| 1857-R1-MAJ-004 — false-green guard | **Partial** | S-138/T-54 now separate display rejection, payload cleanliness and both mutations; S-139/T-55 add positive cause coverage. However, the new builtin-duplicate exemption explicitly allows a known cause to remain generic. See R2-MAJ-003. |
| 1857-R1-MAJ-005 — missing acceptance oracles | **Partial** | §13.8.5 adds named codes, attributions and WP-6 manifest rows, with a before-test copy rule. It omits B7 and several B16 branches and misstates W14's required slots; incorrect state entries also remain. See R2-MAJ-001/002/004/005. |
| 1857-R1-MAJ-006 — adapter bypass | **Partial; remaining gap is MINOR** | S-143/T-56 now begin at a fake process/output boundary and keep the parser, dispatcher, assembly, persistence and renderer real; U27–U29 cover the two reported journeys. The requested duplicated-argument Settings counterpart is still absent. See R2-MIN-001. |
| 1857-R1-MAJ-007 — upstream-refusal attribution | **Addressed** | C9, the contract paragraph, W13 and OQ-1857-2 consistently say `ambiguous`, matching R-2. This was a transcription correction, not a new attribution decision. |
| 1857-R1-MAJ-008 — nonfatal CLI diagnostics | **Addressed** | W4 is terminal-only and S-142 supplies the successful-run control, correcting the original run-stopped claim. C11 adds separate new defects: an incorrect baseline assertion and an unfinished failed-Settings-result disposition. These are R2-MAJ-005, not a re-raised R1 finding. |
| 1857-R1-MIN-001 — Settings cross-reference | **Addressed** | §13.8.4 and U28 use W2's neutral model-unavailable form, not W13. |
| 1857-R1-MIN-002 — reserved-code duplicate | **Addressed** | §13.7 has one `delegated_task_limit` row, marked reserved; B9/OQ-1857-6 retain the no-new-emitter ruling. Issue filing remains team-lead's task; no filing completion is claimed. |
| 1857-R1-MIN-003 — overstated baseline | **Addressed** | C6 and B18 now qualify transport-wording and dedicated Settings branches; §13.8.1 exempts untyped notices/authored replies, and the original totals are re-qualified. C11 introduces a new false “no line today” assertion, recorded separately in R2-MAJ-005; the old corrections are not re-raised. |

**Disposition totals:** 7 addressed, 4 partial, 0 wholly unaddressed. This does not endorse the author's “all 11 Done” assessment.

## Findings

### CRITICAL findings

None established within this amendment's scope. This is not a security certification.

### MAJOR findings

#### 1857-R2-MAJ-001 — The save-stage matrix still maps stopping failures to continuing or completed work

- **Severity:** MAJOR.
- **Lenses:** Inconsistency with source; UI states; testability; incompleteness.
- **Affected sections:** §13.8.2 B11/B19/B20; §13.8.3 W15/W16 and neutral twins; §13.8.5; S-140; T-55.
- **Relation to round 1:** R1-MAJ-001 remains open; the correction added the incorrect continuing-state oracle.
- **Failure scenario:** A continuation flush fails before another tool round. B19/W16/S-140 require the user to be told that work continues and the test to assert continued tool execution, although the actual caller refuses that tool round and ends the turn. Separately, a tool-limit notice fails to save during finalization; W15 would claim an answer completed even though the turn reached its limit without a final answer.
- **Evidence:** `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn_response.go::recordToolCalls` wraps and reports a failed continuation flush, then returns. `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn_iterations.go::dispatchToolCalls` recognizes that wrapper, sets `TurnEndStatusError` and returns before `executeToolCalls`. `/workspace/wt/rel-head-1857/pkg/agent/wu22_turn_archive_failure_test.go::TestWU22FlushContinuationArchiveFailureStopsToolRound` explicitly asserts no new tool round is admitted; this test was read, not run. In contrast, `/workspace/wt/rel-head-1857/pkg/agent/started_tool_outcome.go::beginStartedTool` returns `true` after receipt-write failure. These are not interchangeable consumers of the shared reporter.
- **Further evidence:** `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn.go::finalizeTurn` handles empty, stopped and iteration-limited outcomes as well as answers. Its failed `recordIterationLimitNotice` branch calls the same reporter without a completed answer. Its direct `Sessions.Save` failure emits separately rather than calling `reportSessionSaveFailure`, so a helper-only fix misses it. `/workspace/wt/rel-head-1857/pkg/gateway/websocket_streamer.go::Finalize` returns the late persistence error, and `::publishDone` sets `TurnFailed`; the turn finalizer also returns an error. B20's “No — post-stream notice” must not be read as a successful/non-failing turn.
- **Recommendation:** Bind dispositions to the exact failed operation and caller outcome, not the error role or “finalize path.” Separate admission refusal, continuing receipt loss, terminal continuation-save failure, actual completed-answer save failure, finalization without an answer, and the late gateway failure. Include the direct `Sessions.Save` emission. Preserve the existing stop-before-next-tool and failed-turn semantics; do not change execution to satisfy the new copy tests. Require S-140/T-55 to drive the real callers and assert both text and execution outcome. Use “answered” only when completed-answer evidence exists.
- **Certainty:** **Verified (high confidence)** for the source/specification contradictions. Misleading UI or execution regression from following the current oracle is **Inferred (high confidence)**; not exercised.

#### 1857-R2-MAJ-002 — Goal-state failures are not uniformly background, nonterminal events

- **Severity:** MAJOR.
- **Lenses:** Inconsistency with source; state completeness; UI states; testability.
- **Affected sections:** §13.8.5 B14; §13.8.2 B14; S-139/T-55.
- **Relation to round 1:** New incorrect state assertion introduced while filling R1-MAJ-005's matrix.
- **Failure scenario:** An active turn cannot read its goal state while preparing a provider request. The matrix treats it as a background event with no running or aborted turn and permits only that state's consequence wording. A helper-level test can pass without checking that the active request actually stopped.
- **Evidence:** The matrix says, “No — a background goal-loop event; no turn is running or aborted (verified: the site logs, emits and continues).” `/workspace/wt/rel-head-1857/pkg/agent/goal_record_error.go::refuseUnreadGoalState` calls the reporter, sets `TurnEndStatusError`, returns an error result and ends request preparation. `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn.go::prepareToolSurface` calls it for failed goal-forcing reads and failed compressed tool-surface construction. Reading only `/workspace/wt/rel-head-1857/pkg/agent/goal_record_error.go::reportGoalReadError` cannot establish the caller's state.
- **Recommendation:** Split background/pre-admission goal notices from active-turn provider-request refusal. Specify the actual outcome and voice for each, preserving the refusal before the provider call. Add a caller-driven fixture through `prepareToolSurface`, alongside the background control, asserting no provider call and the truthful stopped/refused outcome. Correct the author's unsupported non-terminality claim.
- **Certainty:** **Verified (high confidence)** for the active-turn exit and contradictory matrix. Future misrendering is **Inferred (high confidence)**.

#### 1857-R2-MAJ-003 — The rewritten guard still blesses a known cause as “we can't tell why”

- **Severity:** MAJOR.
- **Lenses:** Requirements consistency; false-green risk; truthful attribution.
- **Affected sections:** §13.8.2 B10; §13.8.5 B10 builtin-only row; S-138/S-139; T-54/T-55; FR-092.
- **Relation to round 1:** R1-MAJ-004 is only partially fixed.
- **Failure scenario:** Two builtin tools have the same name. The runtime detects exactly that condition, but the amendment explicitly retains the unknown sentence and makes it a passing negative control. The new guard therefore passes while the user is still told the reason is unknown, contrary to the amendment's central requirement.
- **Evidence:** FR-092 defines a known safe reason as a matched sentinel, structured fact or pinned matcher. B10 itself calls the duplicate-name cause known exactly. `/workspace/wt/rel-head-1857/pkg/agent/loop_policy.go::checkToolDedupInvariant` detects the repeated name and records both source categories; `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn.go::prepareToolSurface` emits the corresponding structured duplicate payload before its current generic error. The new matrix and T-54/T-55 deliberately exempt the builtin-only instance because it lacks a user-configurable remedy. Lack of a configuration remedy is not lack of a known cause.
- **Recommendation:** Remove the builtin-only collision from the residual-unknown allow-list. Give it truthful fixed catalogue/assembled copy with an appropriate per-code attribution and no exposed tool names. The architect can select the minimal variant/code arrangement; do not mislabel it `config` or change attribution per instance. Require positive known-cause coverage for both MCP-involved and builtin-only collisions, and a mutation that restores the generic result. Keep the approved unknown fallback for genuinely unclassified residuals.
- **Certainty:** **Verified (high confidence)** for the direct requirement/oracle contradiction and available source fact. The false-green implementation is **Inferred (high confidence)** from the prescribed assertions.

#### 1857-R2-MAJ-004 — The “every newly promised cause” matrix still lacks executable oracles

- **Severity:** MAJOR.
- **Lenses:** Incompleteness; ambiguity; contract-first; testability.
- **Affected sections:** §13.8.2 B7/B16; §13.8.3 W9/W14; §13.8.5; S-139; T-55/T-56.
- **Relation to round 1:** R1-MAJ-005 remains partially open.
- **Failure scenario:** The RED author reaches message-admission failure or a CLI session-record read/start-marker write failure. S-139/T-55 require the row's exact code, attribution and sentence, but the promised row does not exist. Choosing a nearby config or resume error can either retain generic output or explain the wrong cause, and tests then inherit the implementer's guess.
- **Evidence:** §13.8.5 has 17 cause rows, but no B7 row: W9 supplies copy without selecting its code and attribution. B16's inventory lists missing executor config, empty CLI, session-record reads, `beginExternalRun`, start-marker writes and driver selection, while the matrix supplies only the two resume/workspace sentinels. `/workspace/wt/rel-head-1857/pkg/agent/external_run_session.go::externalRunResumeOnly` and `::markExternalRunStarted` return distinct storage refusals; `/workspace/wt/rel-head-1857/pkg/agent/external_dispatch.go::runExternalCLISubTurn` propagates them before starting the run. They are not evidence of a changed workspace or necessarily a configuration gap. “`agent_not_configured` family” is not a selected variant: `/workspace/wt/rel-head-1857/contracts/components/schemas/LLMError.yaml::x-user-messages.agent_not_configured` currently means missing workspace membership. B16a/B16b also say required slots “none,” although both W14 forms require `{cli}`.
- **Recommendation:** Complete B7 and each distinct B16 failure-class disposition before their RED expectations are authored. Several producers may share one truthful outcome where justified; a new code per call site is not required. State selected code, per-code attribution, both voices or an exact manifest row, action, state, actual surfaces and a test reference. Correct W14's `{cli}` requirement and define its absent-fact fallback. Reconcile the contract list and the positive coverage set with the completed matrix. Retain the approved WP-6 copy process for named rows, but do not call missing rows a complete oracle.
- **Certainty:** **Verified (high confidence)** for the missing rows, slot mismatch and distinct refusal branches. Incorrect future attribution/copy is **Inferred (high confidence)**.

#### 1857-R2-MAJ-005 — C11 mistakes an unchanged run outcome for an unchanged display, and leaves Settings unresolved

- **Severity:** MAJOR.
- **Lenses:** Source accuracy; state completeness; UI error state; testability.
- **Affected sections:** §13.8.1; §13.8.2 C11 and baseline totals; §13.8.5 C11; S-142; T-56(g).
- **Relation to round 1:** New defects in C11, added by the fix. The original terminal-only wording/control and the C6/B18 baseline corrections are addressed; neither old finding is re-raised.
- **Failure scenario:** A malformed CLI line is followed by valid output. The new normal-run rule correctly requires no terminal failure sentence, but an implementer treating C11 as “none exists today/no change” can leave the old transient and persisted generic error. In Settings, following the retained any-error-fails rule still returns a failed result, while the same matrix promises no user-facing sentence and supplies no Settings-specific outcome/copy oracle.
- **Evidence:** `/workspace/wt/rel-head-1857/pkg/agent/runner/driver_opencode.go::parseLine` emits malformed JSON as `Fatal: false`. `/workspace/wt/rel-head-1857/pkg/agent/external_dispatch.go::drainExternalRun` appends sanitized error text and calls `emitExternalCLIErrorEvent` before checking `Fatal`; the check controls `runErr`, not the existing display/persistence side effects. C11 nevertheless says “no terminal-failure line is shown” and “no user-facing sentence exists or is added.” `/workspace/wt/rel-head-1857/pkg/gateway/rest_executor_smoketest.go::drainSmokeTestRun` returns `ok=false` with sanitized text on every error event; `::buildResponse` publishes that failed result. C11 keeps this “until WP-0 says otherwise,” while T-56(g) asserts current behaviour rather than the complete intended Settings result.
- **Recommendation:** Correct the baseline: a nonfatal diagnostic does not fail the normal run, but it currently emits/persists an error line. State explicitly that the amendment removes those user-facing diagnostic emissions on the successful normal-run path. Pin a separate Settings disposition now: retain its stated stricter failed-test behaviour and specify truthful test-level copy and the expected response without claiming the diagnostic proves a failed CLI run. Changing that existing drain rule would be a separate deliberate decision, not an implicit WP-0 implementation choice. Make the normal-run and Settings fixtures assert final status, exact allowed text and absence of unwanted live/persisted lines. Update the baseline labels/totals consistently; do not leave the final behaviour to a contract implementation step. No change to R-2's classifier ownership is needed.
- **Certainty:** **Verified (high confidence)** for emission-before-fatality, the Settings branch and the conflicting/unfinished specification. Future UI behaviour is **Inferred (high confidence)**.

#### 1857-R2-MAJ-006 — W15 tells users to rerun completed work to repair history

- **Severity:** MAJOR.
- **Lenses:** Recovery correctness; UI journey; user promises; testability.
- **Affected sections:** §13.8.3 W15, its neutral twin and recovery note; §13.8.5 B11; S-140/T-55; amendment documentation TODO.
- **Relation to round 1:** New action added to satisfy R1-MAJ-001's request for a supported remedy.
- **Failure scenario:** A turn sends a message or makes another non-repeatable change, then produces its answer, but the final conversation save fails. W15 tells the person to “ask again if you need it saved”; its neutral form says “run it again if it is needed there.” Repeating the original task can send the message or perform the change again. It does not restore the original transcript, and a second save can fail too.
- **Evidence:** W15's note calls re-asking an “evidenced” action because it “recreates” the exchange. `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn_iterations.go::dispatchToolCalls` executes tools before eventual finalization; `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn.go::finalizeTurn` reports later save failures and does not undo those tool effects. The relevant user-promise distinction already appears in `/workspace/wt/rel-head-1857/docs/tools.md::Background results`: a reporting failure is not proof it is safe to rerun the command.
- **Recommendation:** Remove repeat-the-task advice as a remedy for missing history. Distinguish the work's outcome from its saved record, and direct the user to check the existing result before deciding whether anything should be repeated. Any offered record-preservation action must use a verified existing capability; do not invent automatic persistence retries or a new recovery feature. Pin both voice forms and add a completed-side-effect-plus-save-failure fixture that rejects copy suggesting a task rerun solely to save history. Apply the same distinction in the planned user documentation.
- **Certainty:** **Verified (high confidence)** for the new instruction and execution-before-final-save ordering. Duplicate external effects after a person follows it are **Inferred (medium confidence)**; no such action was performed or reproduced.

### MINOR findings

#### 1857-R2-MIN-001 — Duplicated arguments still lack the Settings-path acceptance case

- **Severity:** MINOR.
- **Lenses:** Test coverage; cross-surface consistency; reachability.
- **Affected sections:** S-143; T-56(a)/(b); U27/U28.
- **Relation to round 1:** Narrow residual of R1-MAJ-006 after the substantial adapter-coverage correction.
- **Failure scenario:** The helper path handles flag rejection, and Settings handles nested model-unavailable envelopes, but Settings's process-exit/stderr case remains on the old generic translation. The enumerated tests can all pass: duplicated arguments are checked only in helper chat, and only the nested-envelope case is explicitly sent through Settings.
- **Evidence:** S-143 says only case (b) additionally drives Settings; T-56(a)/(b) repeat that split. U27 exercises delegation and reload; U28 exercises Settings only for model unavailability. `/workspace/wt/rel-head-1857/pkg/gateway/rest_executor_smoketest.go::drainSmokeTestRun` is a separate consumer from `/workspace/wt/rel-head-1857/pkg/agent/external_dispatch.go::drainExternalRun`.
- **Recommendation:** Add the duplicated-argument process-exit fixture to the real Settings handler path and U27's steps. Assert W1's neutral form, failed-test status and absence of flag/value text in the serialized result. Pair it with a corrected-arguments success control. This extends existing rows, not a new test framework or additional campaign.
- **Certainty:** **Verified (high confidence)** for the planned coverage omission and separate consumers. The surviving regression is **Inferred (medium confidence)**.

### Observations

None additional. The active findings above cover the defects established in this final round.

## Structural integrity and twelve-lens accounting

The table assesses amendment readiness only. Existing base-spec structure is not re-approved or reopened.

| Structural check | Result | Amendment assessment |
|---|---|---|
| Status and scope | Present | The amendment and D85 remain pending; no implementation is claimed. |
| Architecture grounding | Present | Accepted R-2/R-3/R-4/R-5 shape/R-6 preserved; source wins over inaccurate new caller claims. |
| Contract-first sequence | Present, incomplete oracle | Enum/catalogue/attribution, closed slots, generation and consumer order are stated. R2-MAJ-004 prevents claiming the complete outcome contract is ready. |
| API, persisted data and surfaces | Present | Chat, direct result, completion, lifecycle summary, Settings and gateway save notice are now separated. No hand-written parallel wire type is proposed. |
| UI states and journeys | Correction required | R2-MAJ-001/002/005/006: continuing versus failed work and the recovery action remain wrong or incomplete. |
| Accessibility and keyboard | Explicit plan; not executed | §16A plus U29 cover focus, disclosures, one-time announcements, reduced motion and 320 px wrapping on the new variants. |
| Design-system reuse | Preserved | No new primitive, palette or layout is introduced by the amendment. Existing carriers remain subject to their action/disclosure contracts. |
| Security/user promises | Explicit boundary; not certified | FR-093 and T-56 name actual serialized and persisted surfaces; no new CLI-text exposure is authorized. |
| BDD/oracle independence | Correction required | The matrix cannot supply the missing B7/B16 expectations; several new state assertions contradict callers. |
| Traceability | Links present | FR-092/093 → S-133–S-143 → T-52–T-56 and DOC-02/08/12 are linked; linked tests with wrong or missing expectations are not evidence of coverage. |
| Reachability | Planned, not demonstrated | Actual helper/parent/Settings paths now named; R2-MIN-001 remains. |
| User-facing documentation | Named TODOs present | DOC-02 and DOC-08 explicitly include Settings and parent Open; correct the save-state/recovery descriptions with the findings. No docs shipping claim at spec stage. |
| Epic/riders | Existing status retained | #1857 remains a child of #1604. Current same-file rider confirmation remains unverified in §1.5; this review does not invent chief confirmation or reopen that inherited base issue. |
| Agent-tool field parity | Not applicable | Reporting changes, not a new agent-managed property or tool. No registration/policy addition is required by this amendment. |

| Lens | Assessment |
|---|---|
| 1. Ambiguity | R2-MAJ-004/005: absent complete outcomes and a deferred Settings decision. |
| 2. Incompleteness | R2-MAJ-001/004 and R2-MIN-001: omitted caller classes and a missing surface test. |
| 3. Inconsistency/source | R2-MAJ-001/002/003/005: execution state, known-cause definition and nonfatal baseline. |
| 4. Infeasibility | Required continuation after `recordToolCalls` failure conflicts with the existing execution contract (R2-MAJ-001). No new deployment/runtime dependency is needed; fake external boundaries are feasible, not yet exercised. |
| 5. Contract-first | R2-MAJ-004. Existing generated error shapes and catalogue ownership were inspected; preserve the approved assembly boundary. |
| 6. Security/user promises | Safe-fact-only CLI output remains specified; T-56 now inspects actual wire/persistence, not only hidden text. No alternative exposure option is accepted here. |
| 7. Reachability | Three parent routes are corrected; Settings integration exists in the plan, with the remaining R2-MIN-001 case. |
| 8. UI states/journeys | R2-MAJ-001/002/005/006. False “continues,” “answered” and rerun advice are user-facing defects, not just backend naming issues. |
| 9. Accessibility/keyboard | §16A/U29 reviewed against the design-system interaction requirements. No additional amendment-specific omission established; runtime focus/announcements remain unknown. |
| 10. Design-system/brand | Catalogue and status/state rules checked. Keep existing components; truthful status/recovery, not a new visual design, is the issue. |
| 11. Testability/false greens | R2-MAJ-001–005 and R2-MIN-001. A clean payload does not prove a known cause was explained, and a test of a helper does not prove its caller continues. |
| 12. Overcomplexity | No new layer is warranted. Correct caller facts, catalogue variants and existing boundary tests; do not add a frontend classifier, arbitrary-text trust path or persistence-retry subsystem. |

## Test-plan assessment

No tests were written or executed. Existing test source was used as evidence of intended invariants, not a passing receipt.

| Test/scenario | What improved | Required correction or limit |
|---|---|---|
| S-138 / T-54 | Three distinct assertions; both new-site and existing-site mutations | Remove known builtin duplicates from the generic exception; retain genuinely unclassified controls. |
| S-139 / T-55 | Positive code, attribution, variant and replay expectations | Complete missing rows and use real caller states. Named future WP-6 copy must exist independently before assertions are authored. |
| S-140 / T-55 | Admission, mid-run, final and gateway producers named | Split continuing receipt failure from terminal flush failure; finalization with no answer; direct session-save failure; gateway failed outcome. Assert execution as well as copy. |
| S-141 / T-56 / U27 | Actual completion route and summary → Open journey | Keep lifecycle text unchanged; preserve the classified cause until neutral copy is assembled. Correct W14's slot/absent-fact fixtures. |
| S-142 / T-56(g) | Malformed line → valid output → successful completion | Assert removal of current live/persisted diagnostic error lines; specify the distinct intended Settings status and message. |
| S-143 / T-56 | Real adapters and serialized/persisted boundaries | Add duplicated-argument Settings coverage; raw stdout and stderr absence must be asserted on bytes/records, not inferred from what the UI hides. |
| U27–U29 | Exact-cut CLI journeys and accessibility sampling | These are execution plans only. Tester and independent validator must provide exact-cut evidence; no browser claim is made here. |
| A4/A5 media preservation | FR-092/T-52 now name both gates' different inputs and promotion ordering | Preserve recovery classification, presentation relabeling and independent incident eligibility. Accepted R-4 is not reopened. |

### Missing test categories and dataset gaps

| Category / dataset | Missing or incorrect boundary | Required case |
|---|---|---|
| Storage failure state | A shared reporter is mistaken for a shared caller outcome | Continuing tool receipt versus terminal continuation flush; finalization with an answer versus iteration-limit/no-answer finalization; late gateway failure. Assert work/outcome as well as text (R2-MAJ-001). |
| Goal read lifecycle | Only the background reporter's behaviour is modelled | Active request preparation refuses before the provider call; background/pre-admission control remains separate (R2-MAJ-002). |
| Known-cause guard | Builtin-only collision is treated as unknowable | Both builtin-only and MCP-involved duplicate fixtures require truthful cause copy; restoring generic copy fails (R2-MAJ-003). |
| Admission and CLI preflight | Missing code/copy oracles and slot absence | B7 and B16 storage/config refusal classes, with known and absent CLI identity where applicable (R2-MAJ-004). |
| Nonfatal CLI diagnostic | Normal-run success and a failed Settings test have different outcomes | Same malformed-line input through both real consumers; assert their distinct status and allowed text (R2-MAJ-005). |
| Recovery copy | Repeating completed work is treated as saving its record | Completed side effect followed by final-save failure; reject advice to rerun solely for history (R2-MAJ-006). |
| Settings process-exit adapter | Only nested model-unavailable input reaches the Settings test | Duplicated-argument failure and corrected-argument success through the real Settings handler (R2-MIN-001). |

Use fault fixtures tied to the exact failed operation. Where a storage fault also prevents the error receipt from being persisted, do not manufacture a replay PASS from a fixture that bypasses that write; state which receipt could actually be saved. Keep the existing CI/targeted-command discipline and no-change-to-execution control tests.

## STRIDE and user-promise review

STRIDE covers spoofing, tampering, repudiation, information disclosure, denial of service and elevation of privilege. “Specified” below is not runtime certification.

| Component / boundary | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| CLI error → generated catalogue/assembly | ok | ok | risk | ok | ok | ok | Driver-keyed closed names, structured kinds and fixed copy are specified. C11's diagnostic/outcome distinction needs correction (R2-MAJ-005). |
| Error assembly → live/replay/Settings/parent | ok | ok | risk | ok | ok | ok | FR-093 prohibits raw stderr and stdout error-envelope text on client/persisted error surfaces. T-56 now proposes real-boundary verification; outcome records remain wrong or incomplete under R2-MAJ-001/002/005. |
| Storage/goal refusal → execution outcome | ok | ok | risk | ok | ok | ok | No new execution authority is authorized. Preserve existing refusals rather than changing execution to satisfy a wording test; R2-MAJ-001/002 concern the state and outcome oracle, not a verified permission bypass. |
| Existing auth, tool authority and resource guards | ok | ok | ok | ok | ok | ok | No new endpoint authority, policy relaxation, unbounded store or retry loop is proposed. Existing unrelated controls were not re-audited. |

**Legend:** S = spoofing; T = tampering; R = repudiation/auditability; I = information disclosure; D = denial of service; E = elevation of privilege. `risk` means the document leaves the identified concern unresolved; `ok` means addressed by a stated boundary or not applicable to the amendment, **not** an implementation/security PASS. Pending founder questions grant no data-exposure exception.

security-relevant side finding, reported to team-lead

## Reachability and delivery claims

| Question | Answer | Evidence |
|---|---|---|
| Is a new tool registration required? | No | §17: no new tool or agent-managed property. |
| Are actual user/agent surfaces named? | Yes, in the plan | §13.8.4; `/workspace/wt/rel-head-1857/src/lib/delegationEventLine.ts::delegationEventShowsOpen`; `/workspace/wt/rel-head-1857/pkg/agent/steer_completion.go::completionMessage`; `/workspace/wt/rel-head-1857/pkg/gateway/rest_executor_smoketest.go::buildResponse`. |
| Does the plan require execution? | Yes | T-56 and U27–U29 on the exact cut; successful execution remains unverified. |

**Code correct and tested:** Not established; this is a specification-only review and no tests ran.

**Reachable by a user/agent:** Existing routes were source-checked; the proposed outcomes have not been implemented or demonstrated by this review.

## Existing decisions and question-state check

This is a status check, not a new interview or recommendation to reconsider accepted decisions.

| Item | Verified specification status |
|---|---|
| R-2 | Separate CLI classifier, six agreed classifier codes, typed cancellation/timeout exceptions and `ambiguous` upstream-refusal attribution retained. |
| R-3 | No offending flag names/values or reconstructed commands; naming Additional CLI arguments remains allowed. Prevention stays outside this amendment. |
| R-4 | Narrow terminal promotion, no pre-send/no-status promotion, quote/no-quote distinction, both media gates and independent incident eligibility retained. |
| R-5 shape | Own limiter's separate config-attributed shape retained; inclusion still depends on Q2. |
| R-6 | Reserved delegated-task code, no new emitter; team-lead owns filing. |
| Q1 / OQ-1857-1 | Open. No answer applied. |
| Q2 / OQ-1857-5 | Open on scope only. No answer applied. |
| Q3 / OQ-1857-7 | Open; security consultation retained. No answer applied. |
| Q4 / OQ-1857-8 | Open on wording pattern; factual corrections remain necessary whichever pattern is chosen. |
| Q1 ↔ Q3 | §30.4 explicitly requires consistent answers; neither authorizes CLI output exposure by default. FR-093 retains the no-CLI-text boundary pending answers. |

## Unasked questions for the author

These are source/contract clarifications required by the findings, not new founder decisions.

1. Which exact caller proves that work continues, stops before its next provider/tool call, or produced an answer? Bind each save/goal row to that evidence rather than the shared reporting helper.
2. What code, attribution, variant and no-fact form covers B7 and each remaining B16 refusal class, and where is its independent RED oracle recorded?
3. What safe test-level sentence accompanies the retained failed Settings result for C11, while a normal run with the same diagnostic continues successfully without a terminal error line?

## Verdict rationale and final-round next action

**Verdict: REVISE.** The six MAJOR findings describe incorrect or incomplete behaviour a literal implementation would ship. The strongest evidence is in callers: a continuation-save failure prevents the next tool round; a goal-read refusal ends an active request; and a nonfatal diagnostic currently emits text before its fatality is checked. The new tests cannot be accepted as independent oracles while they encode the opposite facts or explicitly allow a known cause to remain unknown.

The spec author has one final correction pass for `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec.md`, covering R2-MAJ-001–006 and R2-MIN-001 without changing production code to match erroneous oracles. Team-lead retains the already-open Q1–Q4 decision process; this report neither decides those questions nor creates another grill round.

### Escalation to the founder

The final author correction has not happened yet; this report does not presume its outcome. If any of R2-MAJ-001–006 remains blocking after that correction, team-lead must escalate its exact remaining gap and disposition to the founder rather than silently waive it or commission another grill. No CRITICAL finding was established here, and no new product choice is needed to correct the verified facts. Only after the corrections and existing decision dependencies are resolved should implementation readiness be claimed.

### Recommended next actions

- [ ] Correct caller-state and recovery facts, including positive and negative execution controls (R2-MAJ-001/002/006).
- [ ] Remove the known-cause generic exemption and complete the missing outcomes/slots (R2-MAJ-003/004).
- [ ] Correct C11's baseline and distinct Settings result; extend duplicated-argument coverage to Settings (R2-MAJ-005; R2-MIN-001).

### Next step in the process

Review written to: `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec-1857-review-round2.md`.

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews the founder on “Questions for the founder”, then the spec author fixes round-2 findings. Any CRITICAL finding still open after that fix is listed under “Escalation to the founder” above for the founder to decide — do not run a third grill round. Once resolved, team-lead plans the implementation (RED / GREEN / CHECK, the 5-reviewer gate).

There are **no new interview questions** from this round; the existing pending interview items remain with the founder. Any remaining blocking MAJOR finding after the final correction follows the same escalation rule stated above.

**Final verdict: REVISE.**

## Questions for the founder — NEW only

**None.** The findings require source-grounded specification and acceptance-oracle corrections. Existing Q1–Q4 remain open in §30.4 and are intentionally not repeated as new questions.

Skills: `grill-spec`, `omnipus-shared-rules`, `omnipus-voice`, `ux-heuristics-review`. The local review constitution and report template were applied to this single commissioned final round; result verification was not an additional grill.

## Evidence table

| Requirement / claim | Evidence | Certainty / limitation |
|---|---|---|
| Correct amendment and production revisions | `git rev-parse HEAD`, exit 0: `5616f4b4ef3c5dc46b48c1615b242bd5af26c718`; `git -C /workspace/wt/rel-head-1857 rev-parse HEAD`, exit 0: `5d20e539fa63624189d82f71c45418512b219222`; `git diff --quiet 5616f4b4ef -- /workspace/wt/notif-1857/docs/internal/specs/notifications-spec.md`, exit 0. | Verified, high confidence. |
| Exact fix scope | Read-only diff of `a2e268220..5616f4b4ef` names only the reviewed specification; numstat is 161 additions / 66 deletions. | Verified, high confidence. |
| All 8 MAJOR and 3 MINOR R1 dispositions checked | `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec-1857-review.md`; current specification §13.8.5/§35, S-138–S-143 and T-54–T-56; `/workspace/logs/1857/draft-report.md::Grill round 1 fix pass`; eleven audit rows above. | Verified, high confidence; seven addressed, four partial. R1-MAJ-008 and R1-MIN-003 closed; new C11 defects tracked separately. |
| B19/B20/W16/S-140 source claims checked | `/workspace/wt/rel-head-1857/pkg/agent/started_tool_outcome.go::beginStartedTool`; `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn_response.go::recordToolCalls`; `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn_iterations.go::dispatchToolCalls`; `/workspace/wt/rel-head-1857/pkg/gateway/websocket_streamer.go::Finalize` / `publishDone`. | Verified, high confidence; R2-MAJ-001. |
| B11 finalization and recovery checked | `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn.go::finalizeTurn`; W15; `/workspace/wt/rel-head-1857/docs/tools.md::Background results`. | Source/text verified; repeated external work inferred, not reproduced. |
| B14 caller state checked | `/workspace/wt/rel-head-1857/pkg/agent/goal_record_error.go::refuseUnreadGoalState`; `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn.go::prepareToolSurface`. | Verified, high confidence; R2-MAJ-002. |
| New matrix and rewritten guard checked | §13.8.5, FR-092, S-138/S-139, T-54/T-55; duplicate detector and CLI refusal helpers cited in R2-MAJ-003/004. | Verified, high confidence; 17 cause rows are not complete coverage. |
| C11/S-142/T-56(g) checked | `/workspace/wt/rel-head-1857/pkg/agent/runner/driver_opencode.go::parseLine`; `/workspace/wt/rel-head-1857/pkg/agent/external_dispatch.go::drainExternalRun`; `/workspace/wt/rel-head-1857/pkg/gateway/rest_executor_smoketest.go::drainSmokeTestRun`. | Verified, high confidence; R2-MAJ-005. |
| Parent paths, S-141/S-143 and U27–U29 checked | `/workspace/wt/rel-head-1857/src/lib/delegationEventLine.ts::delegationEventLineText` / `delegationEventShowsOpen`; `/workspace/wt/rel-head-1857/pkg/agent/steer_completion.go::completionMessage`; §13.8.4, T-56 and §24. | Source/specification verified; journeys not driven. |
| Contract/trust references checked | `/workspace/wt/rel-head-1857/contracts/components/schemas/LLMError.yaml::x-user-messages`; `/workspace/wt/rel-head-1857/contracts/components/schemas/SessionMessageError.yaml`; `/workspace/wt/rel-head-1857/contracts/openapi.yaml::postAgentsExecutorSmokeTest`; `/workspace/wt/rel-head-1857/contracts/asyncapi.yaml::LLMError`; `/workspace/wt/rel-head-1857/pkg/agent/translate_error_provider_messages.go::WireLLMError`; `/workspace/wt/rel-head-1857/src/lib/llm-error.ts::getLLMErrorDisplay`. | Verified source shape; no generation or contract test run. |
| Architecture, frontend and user-promise references applied | Relevant sections of `/workspace/wt/rel-head-1857/docs/internal/architecture/AS-IS-architecture.md`; `/workspace/wt/rel-head-1857/docs/internal/architecture/ADR-051-media-handling-and-provider-error-translation.md`; `/workspace/wt/rel-head-1857/design-system/catalog.json`; `/workspace/wt/rel-head-1857/docs/internal/design/design-system-definition.md`; `/workspace/wt/rel-head-1857/docs/internal/brand/brand-guidelines.md`; `/workspace/wt/rel-head-1857/src/components/ui/CLAUDE.md`; `/workspace/wt/rel-head-1857/docs/security.md`; `/workspace/wt/rel-head-1857/docs/tools.md`; relevant user-guide/provider sections. | Read-only reference review, not an audit of unrelated base features. |
| Q1–Q4 pending; accepted rulings not reopened | §30.4, D85 and `/workspace/logs/1857/architect-read.md::Rulings`. | Verified, high confidence; no answers applied. |
| Verification limits honoured | `/workspace/wt/rel-head-1857/docs/internal/false-green-patterns.md` applied; existing regression tests read only; no build, test, browser, commit or push command run. | No implementation PASS claimed. |
| Report-only scope and finding counts | `git status --short`, exit 0: only the untracked round-2 report; `git diff --quiet HEAD`, exit 0: tracked files unchanged; release-worktree status exit 0, empty. Anchored finding-heading searches returned 6 MAJOR and 1 MINOR, both exit 0. `git diff --no-index --check /dev/null /workspace/wt/notif-1857/docs/internal/specs/notifications-spec-1857-review-round2.md` returned 1 with no whitespace diagnostics (a new-file diff, not an empty diff). | Verified. |
| **Self-check** | Re-read the fix-pass sections, author dispositions and decisive caller/consumer source, then checked the report against all eleven R1 findings and the newly commissioned rows/tests. Corrected the disposition count to 7 addressed / 4 partial without re-raising fixed findings. Every active finding has severity, concrete scenario, evidence, recommendation and certainty. Re-ran pinned-source, tracked-scope, count and formatting checks above. No approved base decision was reopened, pending founder answer invented, unrelated security detail published, or file other than this report authored. | **Verified for review scope and report content; runtime behaviour remains untested. Final round, no third grill.** |
