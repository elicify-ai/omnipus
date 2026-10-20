# Adversarial Review: Parent wake and visibility for helper events

**Verdict: BLOCK — 1 CRITICAL, 13 MAJOR, 2 MINOR, 1 OBSERVATION.** This is spec-mode grill round 1 of 2, not an implementation or release verdict.

**Correction: said the existing all-socket goal-outcome broadcast was a blocking cross-account disclosure, wrong because _A sub-agent is a session steered by another session_ explicitly accepts any authenticated operator viewing any session; correct is that broad viewing is an accepted deployment assumption, not an authorization defect established by this review. Impact: that provisional finding is withdrawn and is not counted below.** Authority: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/architecture/ADR-091-steered-sessions-replace-subagents.md`::D7 — Viewing authority / Risks R7. The narrower cached-message retry isolation test does not repeal that viewing decision.

**Document reviewed**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/specs/parent-wake-visibility-spec.md`

**Mode**: Spec

**Round**: Round 1 of 2

**Review date**: 2026-10-15

**Reviewer**: architect; independent of the spec's authorship

**Source baseline**: `feat/parent-wake-20261015`, `bc7dc46ea9ae1486efdd89eeb1458fc23baf3e25`

**Spec SHA-256**: `0350c76b1afb934ed646e154f29b167798b857d478606ec78a0fae2553e152fc`

## Executive Summary

The publish-then-ack sequence can lose the promised verdict-plus-hand-back input across a crash: a successful in-memory publish is not durable delivery. The specification also needs corrections to Stop handling, event identities, outcome deduplication, restart continuation, parked question cards, and the frontend acceptance contract before implementation. All twelve lenses were applied; contract, UI-state, accessibility and catalogue checks received the same source-based scrutiny as runtime behavior.

| Severity | Count |
|---|---:|
| CRITICAL | 1 |
| MAJOR | 13 |
| MINOR | 2 |
| OBSERVATION | 1 |
| **Total** | **17** |

### Authority and verification limits

| Item | Treatment in this review |
|---|---|
| Corrected Stop decision, Source: Interview 2026-10-15, delivered by team-lead during this review | **A helper STOP, whether issued by its parent agent or a person, never wakes its parent and never auto-resumes the helper.** A stopped row may remain. `Q-STOP` is answered; do not re-interview it. |
| Allowed helper wake reasons | Finished/hand-back, failed, historical timeout, gateway-restart cut-off, Judge verdict, question/blocker. `creator_notice` remains only the explicitly commissioned shared-entry integration seam, not another helper event. |
| Restart | Native helper auto-resume and one parent wake remain required. Completed historical tool invocations must not be replayed. External workers are not replaced or automatically resumed. |
| Scope | Goal idle window and no-goal idle checker remain out of scope. No new work timeout, debounce on real endings, tool-policy layer or UI-disconnect watchdog is proposed. |
| Source of current behavior | Direct reads at the pinned HEAD. GitNexus MCP is not connected in this reviewer harness; no graph query, impact analysis or `detect_changes` result is claimed. |
| Historical observations | The supplied investigations and issue were read. The two R7 screenshots were inspected: the apparent passing verdict is in General Purpose's helper chat; the failing screenshot is Jim's parent chat. Historical live delivery and the forensic incident totals were not independently reproduced. |
| Verification type | Source/document review only. No Go, frontend, browser, CI or UAT suite was run. Runtime failure scenarios below are reasoned consequences, not claimed reproductions. No live instance or live data directory was accessed. |

## Findings

### CRITICAL Findings

#### [CRIT-001] Publishing a wake does not durably save the verdict input

- **Severity**: CRITICAL — loss of required parent input.
- **Certainty**: **Verified (high confidence)** document/code ordering; crash consequence **Inferred (high confidence)**, not executed.
- **Lens**: Infeasibility; Incompleteness; Testability & false-green risk.
- **Affected section**: §9.5 steps 2–4, §9.9; FR-005, FR-021; Test 6 and Test 21.
- **Failure scenario**: The combined body is built and published, then the verdict is acknowledged. The process dies before the receiving turn archives that body. Alternatively, it dies after the receiver's consumed marker but before model-input archival. Recovery can now suppress the wake or find an acknowledged verdict without the promised combined input. The durable ordinary hand-back alone does not contain the newly combined verdict text.
- **Evidence**: FR-005 requires the body to be durable before acknowledgement, but §9.5 specifies only “Build”, “Publish”, then “Mark ... read”. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/async_notifier.go`::asyncNotifierImpl.Notify calls PublishInbound; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/bus/bus.go`::publish / MessageBus.PublishInbound enqueue into an in-process channel. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/loop_inbound.go`::AgentLoop.processSteeredSystemWake writes the consumed marker and inbox acknowledgement before runSteeredTurnBody. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/loop_run_turn.go`::agentLoopRunTurnPrepare.selectTurnProvider saves the user input later, inside the turn.
- **Recommendation**: Name the authoritative durable carrier of the **exact combined input**, its identity, and its recovery reader. Persist it before acknowledging the verdict; order the consumed claim against durable model placement, not bus publication or turn registration. Reuse the committed-final and addressed-archive mechanisms rather than adding a second content authority. Fault-inject and reopen real stores after publication, after verdict acknowledgement, and after the consumed marker but before model placement; assert exact verdict and hand-back input, not merely notifier-call order.

### MAJOR Findings

#### [MAJ-001] The corrected no-Stop-wake rule is not applied to parent-issued Stops

- **Severity**: MAJOR — unwanted parent compute after an intentional Stop.
- **Certainty**: **Verified (high confidence)** specification contradiction with the supplied founder clarification; execution consequence **Inferred (high confidence)**.
- **Lens**: Inconsistency & ADR/AS-IS contradiction; Security.
- **Affected section**: PW-D4, PW-D7; Machine-Verifiable Constraints / Wake reasons; §9.6; FR-010; Tests 10, 16, 28; Wake reason dataset row 7; DOC-1.
- **Failure scenario**: The parent agent stops its helper. An implementation excludes only a person's Stop, as the table and tests say, accepts reason `stopped`, or replays a generic `stopped_child:` notice as wake-eligible. The stopped helper does not resume, but its parent nevertheless starts another billed turn, contrary to the clarified rule.
- **Evidence**: The closed reason list still includes `stopped`; PW-D7 and §9.6 describe the exception as an explicit **user/person** Stop. Test 28 explicitly says a `stopped_child:` notice “still wakes”. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/message_inbox.go`::classifyEnvelope / isSteeringLifecycleNoticeText currently makes that prefix wake-eligible. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/stopped_notice.go`::stoppedChildNoticeMessage represents all landed stop causes using that generic nonfatal error envelope. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/lifecycle_edge.go`::StopCauseStop explicitly covers both human and agent stops.
- **Recommendation**: Remove `stopped` as a wake reason. Apply the exclusion to **all intentional helper Stops**, independently of actor, in first delivery, live-turn injection and boot retry. Distinguish restart and historical timeout using the authoritative landed transition, not the generic text prefix. Preserve a genuine tool-limit blocker separately. Add parent-issued Stop and tree-Stop cases, including unacknowledged-notice replay; each asserts zero wake/join and zero automatic resume. Keep the optional visible stopped row and close `Q-STOP` in the document and DOC-1.

#### [MAJ-002] A transient Stop marker is mistaken for the lasting stopped state

- **Severity**: MAJOR — conflicting admission rules and false successful-wake reporting.
- **Certainty**: **Verified (high confidence)** state representations and contradictory table; downstream consequence **Inferred (high confidence)**.
- **Lens**: Incompleteness; Inconsistency & ADR/AS-IS contradiction; Security.
- **Affected section**: §1.1 last Stop claim; §9.4 recipient-state table; §9.6 last row; FR-010, FR-017; Test 12.
- **Failure scenario**: A person's Stop has fully landed on the parent. Its marker has been cleared, so §9.4 classifies it as “no live turn, no Stop marker” and promises one idle wake. The existing receiver refuses it because it is landed stopped. The caller can report publication as a wake that will never run; changing the receiver to obey the table instead would resurrect intentionally stopped work. An accepted Stop whose fence write has not landed is another omitted state.
- **Evidence**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_completion_commit.go`::landSteeredStopLocked sets LifecycleStopped and clears the current marker. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/lifecycle_edge.go`::LifecycleRecord.Stopped recognizes both landed state and in-flight marker. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/loop_inbound.go`::AgentLoop.processSteeredSystemWake refuses stopped ordinary roots independently of the marker. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/boot_sweep.go`::SteerBootRecovery.recoverSteered also checks accepted-but-unlanded Stop intent before creating a restart stop.
- **Recommendation**: Replace the marker-only rule with an explicit state/authority table covering in-flight Stop, accepted-but-unlanded Stop and landed intentional Stop. Retain reports unconsumed for legitimate continuation. Specify matching producer and receiver decisions; do not relax the existing stopped-root guard merely to satisfy a producer-side test. Preserve self-only Stop versus tree Stop: stopping the parent does not imply that its independently continuing children were cancelled. Handle the restart-stopped-parent policy separately in the founder interview below.

#### [MAJ-003] Deterministic row IDs collide for distinct real events

- **Severity**: MAJOR — later required notices disappear.
- **Certainty**: **Verified (high confidence)** proposed key and same-generation resume; collision consequence **Inferred (high confidence)**.
- **Lens**: Incorrect behavior under Incompleteness; Testability & false-green risk.
- **Affected section**: §9.3 message ID; FR-022; Test 4; restart and verdict datasets.
- **Failure scenario**: A helper restarts, resumes on the same generation, then is interrupted by another restart. Both `restarted` rows receive the same proposed ID, so the second is a no-op. Likewise, two different goals on the same helper can each produce `verdict_unmet` round 1; omitting goal identity collapses the second notice.
- **Evidence**: §9.3 proposes `helper-event-<transcript session id>-<helper session id>-<kind>-<generation or verdict round>`. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_cancel.go`::SteerCanceller.reviveWithExecution resumes a stopped helper on the **same generation**. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/stopped_notice.go`::stoppedChildNoticeID already includes the real landed stop sequence to distinguish such events; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_frames.go`::goalVerdictUpwardMessageID includes goal ID and round.
- **Recommendation**: Derive each row identity from its authoritative source event: landed stop sequence/identity for stop-family notices, goal/verdict identity for rulings, and the selected conclusion identity for not-verified. Keep destination transcript identity separate. Test both duplicate delivery of **one** event and **two distinct events** sharing a generation or round number.

#### [MAJ-004] Normal parent completion is excluded from the live-to-idle fallback

- **Severity**: MAJOR — an outcome arriving at turn closure can remain unwoken.
- **Certainty**: **Verified (high confidence)** existing and proposed guard; race consequence **Inferred (high confidence)**, not reproduced.
- **Lens**: Incompleteness; Inconsistency & ADR/AS-IS contradiction.
- **Affected section**: §9.4 live-turn row; US-6 AS-3; FR-017; Test 20.
- **Failure scenario**: The producer observes a live parent. The parent's normal turn finishes and commits `completed` before EnqueueSteeringWake checks it. Enqueue is refused, but the specified fallback requires the reloaded record to be **not terminal**. A completed ordinary root is terminal and is nevertheless an eligible recipient of a new outcome turn, so the legitimate fallback is lost.
- **Evidence**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_audience.go`::SteerUpwardDeliverer.wakeOwnerOrStore uses the same `!current.Terminal()` restriction the spec repeats. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steering.go`::AgentLoop.enqueueSteeringItemWithStatus refuses terminal recipients. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/loop_inbound.go`::AgentLoop.admitOrdinaryRootWake explicitly admits a finished ordinary root's next round.
- **Recommendation**: Permit fallback for an eligible completed ordinary root while revalidating identity, current Stop authority and recipient eligibility. Do not generally reopen arbitrary terminal helpers or replace a changed execution. Add a deterministic end-between-live-check-and-enqueue test and assert one eventual input delivery, not just a publication count.

#### [MAJ-005] “One outcome” has no shared delivery identity or wake owner

- **Severity**: MAJOR — duplicate or wrongly suppressed parent turns.
- **Certainty**: **Verified (high confidence)** different producer identities and ambiguous fallback; double-wake consequence **Inferred (high confidence)**.
- **Lens**: Ambiguity; Incompleteness.
- **Affected section**: §9.4 idempotency, §9.5 fallback and task-owned branch; FR-013, FR-021; Test 6 and Test 22.
- **Failure scenario**: The parent polls the final first. Delivery correctly reports no new hand-back wake because that final was already consumed. “If step 3 does not wake” can then fall back under the different verdict identity and start the forbidden second turn. Task verdict and task-completion notices similarly have separate producer paths, while the spec merely says that “one of” them owns wake.
- **Evidence**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_audience.go`::SteerUpwardDeliverer.Deliver stamps `<child>:<generation>:final`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_frames.go`::goalVerdictUpwardMessageID uses `<goal>-verdict-<round>`. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steering.go`::AgentLoop.recordPolledFinalConsumed consumes the former. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_completion_commit.go`::AgentLoop.publishCommittedFinal explicitly distinguishes an observed acknowledgement from wake publication. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/goal_child_completion.go`::AgentLoop.wakeMetVerdictEntry can deliver the latter; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/task_executor_judge.go`::TaskExecutor.deliverTaskCompletionUpward is a separate task producer.
- **Recommendation**: Specify the canonical outcome identity, its correlation to the verdict/final/task run, and one wake-owning delivery for each supported path. Distinguish already consumed, joined live, published, deliberately held/suppressed and failed. A fallback must preserve the outcome's shared consumption identity and must not turn intentional suppression into another wake. Include task-owned met plus completion, poll-before-delivery, retry and boot cases.

#### [MAJ-006] Promised question, failure and plain-finish rows have no defined carrier

- **Severity**: MAJOR — acceptance can pass model-input delivery while required parent-chat rows are absent.
- **Certainty**: **Verified (high confidence)** specification omission and existing carrier behavior; missing UI consequence **Inferred (high confidence)**.
- **Lens**: Contract-first gaps; Reachability; Incompleteness.
- **Affected section**: Behavioral Contract; “Questions, blockers, and failures wake once”; US-7 AS-7; §9.3; FR-025; Test 27.
- **Failure scenario**: A goal-less helper finishes, asks, blocks or fails, and the parent model does not repeat the event. The inbox/model input and Activity panel can update while the lasting parent-chat row promised by these scenarios has no specified writer or wire representation.
- **Evidence**: The proposed HelperEvent kinds are only `verdict_unmet`, `not_verified`, `stopped`, `restarted`, `timed_out`; GoalOutcome is for an ended goal, not a question or goal-less finish. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_audience.go`::SteerUpwardDeliverer.publishUpward writes subagent status/end/message frames, not these independent chat rows. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/contracts/components/schemas/Message.yaml`::system_subtype / goal_outcome and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/contracts/asyncapi.yaml`::GoalOutcomeFrame define the current specific carriers.
- **Recommendation**: Name the stored entry, live frame, replay conversion and insertion path for **each** promised event. Either extend HelperEvent or explicitly reuse a suitable existing carrier; do not label a question or plain finish as a goal ending. State generated Go/TypeScript consumers and the five-step schema/reference/regenerate/atomic-commit/consumer order. Include cold REST adaptation and both chat-rendering paths, not just the live reducer. Tests must use a parent model that does not echo the event.

#### [MAJ-007] Tool-call IDs are treated as unique for the whole transcript

- **Severity**: MAJOR — legitimate new tool decisions can be silently suppressed after resume.
- **Certainty**: **Verified (high confidence)** occurrence-based archive support and repeated-ID fixture; execution consequence **Inferred (high confidence)**.
- **Lens**: Inconsistency & ADR/AS-IS contradiction; Testability & false-green risk.
- **Affected section**: Resume machine constraints; §9.7; FR-014; Test 5; SC-005.
- **Failure scenario**: An earlier assistant occurrence successfully used `call_0`. After restart the model makes a genuinely new decision with that same provider-generated ID. The transcript-wide “already has a successful result” guard suppresses the new invocation, although it is not replay of the old tool.
- **Evidence**: §9.7 permits a later decision only as “a new call with a new id”. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/window_runtime.go`::turnState.recordCallIssuers binds a call to its issuing assistant archive address. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/ordinal_window_test.go`::TestOrdinals_RepeatedCallIDNamesItsOwnIssuer explicitly creates two assistant occurrences using `call_0`, each with its own result. **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::D2 / U2-ARCHSHAPE, specifies occurrence-scoped tool-result references.
- **Recommendation**: Identify a historical invocation by issuing assistant occurrence **plus** call ID. Resume at the next model decision over faithful saved history; do not replay an already completed historical invocation or globally blacklist its provider ID. Add the positive case where a resumed model legitimately reuses `call_0`, alongside independent executor-count evidence for the historical invocation.

#### [MAJ-008] A deterministic ID does not cause a failed row write to retry

- **Severity**: MAJOR — required history remains permanently incomplete after a transient write failure.
- **Certainty**: **Verified (high confidence)** current return/short-circuit behavior and unspecified retry driver; consequence **Inferred (high confidence)**.
- **Lens**: Incompleteness; UI states & journey gaps.
- **Affected section**: US-3 AS-6; §9.5 step 1; §9.9 row-write failure; FR-004, FR-008, FR-022; Test 30.
- **Failure scenario**: The helper row succeeds, the parent row fails, and the wake is consumed. The goal ends or the stop notice is acknowledged. “A later retry” never repairs the missing parent row because no producer, pending record or trigger for that retry is specified.
- **Evidence**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/goal_outcome.go`::AgentLoop.recordGoalOutcome logs/counts failure and returns no result to its caller; clearGoalWithOutcome can already have ended the goal. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/stopped_notice.go`::AgentLoop.deliverLandedStopNotice returns early for an already-stored, acknowledged notice. §9.9 says only “Retry may write the row once (deterministic id).” Test 30 manually attempting a second write would not prove a production retry exists.
- **Recommendation**: Define who repairs each destination, what retained authoritative event makes it discoverable after acknowledgement, and when repair runs. Make write success/failure observable to callers; retain an observable pending failure until repair succeeds. Repair the helper and parent transcripts independently of wake consumption, with no duplicate turn or rewrite of later execution state. Fault-inject one destination, consume the outcome, reopen the stores and exercise the actual repair driver.

#### [MAJ-009] A parked question card is not a live turn that can be joined

- **Severity**: MAJOR — helper outcomes can bypass or strand a question waiting for the person.
- **Certainty**: **Verified (high confidence)** actual park behavior and false state assumption; integration consequence **Inferred (high confidence)**.
- **Lens**: Infeasibility; Inconsistency & ADR/AS-IS contradiction; UI states & journey gaps.
- **Affected section**: US-7 AS-4; Edge Cases / approval or question card; §9.4 first row; FR-020; Test 20.
- **Failure scenario**: The parent has an AskUserQuestion card pending. Unlike a blocking tool approval, that question already ended its execution. A helper outcome therefore finds no live turn to join and can start an unrelated new turn while the card still awaits an answer, or be queued to a dead scope. An approval-only fixture will not expose it.
- **Evidence**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/tools/ask_user_question.go`::AskUserQuestionTool.Execute returns ParksTurn; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/loop_run_turn_tools.go`::agentLoopRunTurnToolsExecute.finishCall sets TurnPhaseParked / TurnEndStatusParked and returns from the turn. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/ask_user_wire_test.go`::TestRequestCancel_CancelsPendingAskWithoutActiveTurn expressly tests a pending card with no active turn. The spec combines both states into “Live turn, including ... a question”.
- **Recommendation**: Split approval waits from parked root clarification cards. Define the durable destination and consumption point for an outcome received while a card is pending, preserve the card and its answer correlation, and state whether/when a new execution is allowed. Do not synthesize an answer or reintroduce the retired helper person-question pause. Add separate real-registry question-card and approval cases; count actual executions as well as visible rows.

#### [MAJ-010] Frontend behavior omits loading, empty, error and partial-state contracts

- **Severity**: MAJOR — users cannot distinguish a pending event, a failed write, and a helper that actually resumed.
- **Certainty**: **Verified (high confidence)** missing required specification sections/states; UX consequence **Inferred (medium confidence)**, not browser-tested.
- **Lens**: UI states & journey gaps; Incompleteness.
- **Affected section**: §10, §11; US-3 AS-6; US-5 AS-3/4; Test 24–26; DOC-1–3.
- **Failure scenario**: Resume is admitted but queued, or only the helper transcript is saved, or the connection is catching up. The proposed UI table describes the eventual lasting line but not how the person tells these states apart. A “continuing” row can be presented as running before a slot exists, or a missing parent row can remain indistinguishable from no event. A narrow-screen reader may only see truncated text with no specified usable expansion.
- **Evidence**: §10 lists populated surfaces only. There is no UI-state breakdown or end-to-end journey section covering these exceptions. §9.9 describes write failure as logged/counted, not its user-visible recovery state. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/GoalOutcomeRow.tsx`::GoalOutcomeRow truncates collapsed text and expands it; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/ChatScreen.tsx`::SystemMessage / VirtualSystemMessageRow are distinct render paths that must retain parity.
- **Recommendation**: Add the required screen/state and journey sections. Explicitly inherit existing chat loading/empty/reconnect behavior where no new UI is needed; define partial row-save failure, resume queued versus running versus refused, and recovery/error visibility without claiming an unsaved row was shown. Describe the journey from delegation through idle completion or restart, Activity/Open, parent notice, and reload. State narrow/touch expansion behavior and add component tests for the named states. This is not a request for a new screen or visual redesign.

#### [MAJ-011] Dynamic notice and expansion accessibility is unspecified

- **Severity**: MAJOR — a screen-reader or keyboard-only user can miss the event this feature exists to expose.
- **Certainty**: **Verified (high confidence)** missing accessibility contract; user impact **Inferred (medium confidence)**, not an executed accessibility failure.
- **Lens**: Accessibility & keyboard.
- **Affected section**: §9.3 expandable line; §10 approval-card behavior; §12 frontend/UAT plan.
- **Failure scenario**: The new restart or verdict line appears visually while focus stays in the composer or an approval card. No announcement behavior is specified, so a screen-reader user may hear nothing; replay could also announce old notices repeatedly. An independently built expander has no specified keyboard operation or focus preservation.
- **Evidence**: The spec contains no accessibility/keyboard section or corresponding tests. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/GoalOutcomeRow.tsx`::GoalOutcomeRow uses native details/summary and a keyboard-focusable summary; copying its visual appearance alone is not an announcement or focus contract for the new event.
- **Recommendation**: Specify keyboard expansion/collapse, accessible names, composer/card focus preservation, and one appropriate announcement for a newly arriving event without re-announcing the replayed history. Use the existing native/catalogue control semantics rather than a clickable decorative row. Include keyboard, focus and accessibility assertions for the new notice in both rendering paths; distinguish this specification gap from a claim that the existing row has failed such tests.

#### [MAJ-012] Traceability names checks that do not exercise the promised behavior

- **Severity**: MAJOR — false-green acceptance of missing live delivery, duplicates or repeated tool side effects.
- **Certainty**: **Verified (high confidence)** plan/matrix mismatches; future false-green consequence **Inferred (high confidence)**.
- **Lens**: Testability & false-green risk; Reachability.
- **Affected section**: §11 reachability table; §12 test hierarchy/table/UAT; Traceability Matrix; SC-001, SC-005, SC-008.
- **Failure scenario**: All 30 planned tests pass while a second browser receives no tokens, `creator_notice` never calls the real entry, an over-cap progress append is falsely reported successful, task verdict plus completion starts two turns, or a tool repeats its side effect but leaves only one retained success row.
- **Evidence**:
  - The 37 BDD blocks and 26 requirement rows exist, but the test table contains no explicit two-browser test or `creator_notice` invocation. FR-003 maps the two-browser scenario to a one-browser stream test; FR-017 maps the creator notice to channel/never-turned/live-join tests.
  - FR-012's cap-refusal scenario maps to Test 1, a **classifier** test, not real inbox admission/refusal and sender-visible error.
  - Task-owned met deduplication appears in FR-013 but not its listed session-goal-only test.
  - Test 26 and SC-001 accept a token before **reload**, not before **completion**. A final-only reply could satisfy that.
  - UAT-4 uses “transcript shows one success” to prove an executor ran once; those are different observations.
  - §11 assigns timeout reachability to UAT-3/4, which exercise human Stop and restart, not a retained timeout fixture.
  - FR-024 intentionally uses docs review rather than BDD; SC-008 nevertheless claims every FR has BDD and a planned test. The frontend plan names no typecheck/design-system execution gates and includes no row-state, keyboard or cold-load component tests.
- **Recommendation**: Correct the matrix rather than counting labels as coverage. Add actual two-client/session fan-out, explicit `creator_notice`, real cap/rate refusals, task-owned dedupe, corrected parent-issued Stop, and historical-timeout visibility cases. Hold a controlled stream open after the first partial token and before releasing completion. Count executor invocations or a durable external side effect independently of transcript deduplication. Add cold REST/live/replay parity and both renderer tests, the UI/accessibility cases above, `npm run typecheck` and the applicable design-system gates. Separate documentation/review constraints from behavioral BDD coverage instead of manufacturing a documentation BDD scenario.

#### [MAJ-013] The spec silently replaces governing decisions without an amendment map

- **Severity**: MAJOR — implementers and reviewers have contradictory normative restart/wake instructions.
- **Certainty**: **Verified (high confidence)** source contradiction and missing amendment/rationale; maintenance consequence **Inferred (high confidence)**.
- **Lens**: Inconsistency & ADR/AS-IS contradiction; Incompleteness.
- **Affected section**: Opening declaration; Decisions log; §9.4–9.7; §13; missing ADR/security-and-user-promises sections.
- **Failure scenario**: One implementer follows the new automatic restart dispatch; another retains the existing “boot never starts a turn” or “every accepted report wakes” rule because the spec neither links nor explicitly supersedes those decisions. The runtime or regression gate can then preserve precisely the behavior this feature must replace, or broaden the restart exception to intentional Stops.
- **Evidence**:
  - **The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md`::D6 / D8, requires stopped-state retention and no boot dispatch.
  - **Steering commands: no person question**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/architecture/ADR-20261004-steering-commands-no-person-question.md`::C1 / C5, separates new instructions from old report replay.
  - **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::D3, makes every accepted report eligible to wake; this feature deliberately removes progress/checkpoint/artifact wakes.
  - The source agrees with those existing behaviors: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/boot_sweep.go`::SteerBootRecovery.recoverSteered and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/message_inbox.go`::classifyEnvelope. The spec says “not an ADR” but supplies neither a linked governing ADR nor an explicit no-new-ADR rationale/amendment map.
- **Recommendation**: Cite the affected decisions **by title**, record the 2026-10-15 founder supersession narrowly, and arrange the dated corrections to the existing ADRs through the architect owner. Explicitly say whether no new ADR is needed because the founder has already settled the policy; do not commission an unnecessary extra ADR/grill loop. Preserve occurrence-scoped history, selected-execution Stop protection, parent-edge ownership, single ordinary admission/settlement, no external-worker replacement and unchanged tool policies. Add the required security/user-promises section tied to the existing public docs. Do not infer a new account-privacy policy from cached-message retry isolation.

### MINOR Findings

#### [MIN-001] Cross-references and the stale-comment correction point to the wrong facts

- **Severity**: MINOR.
- **Certainty**: **Verified (high confidence)** from complete spec and source reads.
- **Lens**: Ambiguity; Inconsistency & ADR/AS-IS contradiction.
- **Affected section**: Decisions-log introduction; §8; Ambiguity Warnings row 1; §9.8.
- **Failure scenario**: The author or implementer looks for a nonexistent §16/§6, attributes a Stop decision to the streaming decision PW-D1, or changes a comment that never claimed messages were not stored.
- **Evidence**: The log and PW-D7 point to §16, which is absent; §8 points to the accepted list in §6, which is not a numbered section; the warning says PW-D1 is a Stop decision although it is live streaming. §9.8 says to correct a comment about “not stored”; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/tools/message_parent.go`::outcomeForKind instead says artifact/checkpoint are not **wake-eligible**.
- **Recommendation**: Repair the heading references and decision IDs. Replace the answered Stop question with the supplied clarification. Describe the actual stale-comment mismatch; after the classifier fix, its non-wake eligibility statement becomes correct.

#### [MIN-002] “Use the design system” is not a catalogue-first reuse decision

- **Severity**: MINOR — structural design-system gap, not an invented visual defect.
- **Certainty**: **Verified (high confidence)** catalogue entries and omitted mapping; maintenance impact **Inferred (medium confidence)**.
- **Lens**: Design-system reuse & brand.
- **Affected section**: §9.3 final presentation paragraph; §10; missing design-system components section.
- **Failure scenario**: The frontend author independently copies the existing row's expander markup rather than making an explicit reuse decision, leaving two implementations of the same recurring UI job with diverging behavior. “Same pattern” provides no named ownership or verification contract.
- **Evidence**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/design-system/catalog.json`::entries includes Accordion, EmptyState, QueryErrorState and Skeleton. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/GoalOutcomeRow.tsx`::GoalOutcomeRow is an existing domain row, not a public catalogue entry. The spec gives no catalogue mapping or explicit shared-row composition decision.
- **Recommendation**: Add the required component/reuse table: identify the existing domain row behavior to share or the catalogued expansion primitive to use, and inherited state components where applicable. Keep current tokens, icon family, dark/chat-first presentation and no emoji. If a genuinely new public component is necessary, name its export, catalogue entry, stylesheet source registration and verification manifest; otherwise explicitly state that no public component is being added. The review makes no new brand or visual-design decision.

### Observations

#### [OBS-001] The wake entry's row-written flag has no defined responsibility

- **Severity**: OBSERVATION — not a gating defect.
- **Certainty**: **Verified (high confidence)** unspecified parameter behavior; simplification **Inferred (medium confidence)**.
- **Lens**: Overcomplexity.
- **Affected section**: §9.4 inputs.
- **Suggestion**: `WakeSession` accepts “whether a parent-chat row was already written”, but no branch consumes that flag. A caller's Boolean is not proof of a durable row and couples reusable wake delivery to presentation. Keep row persistence/repair with its owner and omit the flag unless the revised failure policy gives it a concrete, necessary meaning. This does not remove the founder-required row or the shared wake entry.

## Structural Integrity

These are document checks, not passing product tests.

| Check | Result | Notes |
|---|---|---|
| Status field present and valid | PASS | Draft. |
| ADR linked, or explicit no-new-ADR rationale | FAIL | “Not an ADR” is not that rationale; existing decisions requiring narrow amendments are not linked. MAJ-013. |
| Contract changes stated first, citing the contract sources | PARTIAL | §9 has the shape and §13 puts generation before consumers, but the shape follows the earlier implementation/impact detail. Move the contract-first design ahead of that detail when addressing MAJ-006/013; keep backend-lead ownership and atomic generated artifacts. |
| API and data section | PASS with substantive gaps | §9 exists; omitted row carriers and identity semantics are MAJ-003/005/006. |
| UI screens and loading/empty/error/partial states | FAIL | Populated surface table only. MAJ-010. |
| End-to-end user journey section | FAIL | Stories describe individual outcomes; no complete exception/recovery journey is specified. MAJ-010. |
| Accessibility and keyboard section | FAIL | MAJ-011. |
| Design-system components, catalogue-first | FAIL | Generic reuse direction only. MIN-002. |
| Security and user-promises section | FAIL | Sensitive-data filtering is named, but there is no explicit section mapping the control/admission and documentation promises. MAJ-002/013. Accepted broad operator viewing must not be silently replaced. |
| BDD acceptance scenarios with spec-derived expectations | PASS | Seven stories, 40 acceptance scenarios, 37 BDD blocks; all 40 acceptance references are represented. Runtime tests are still only planned. |
| Every BDD block has a back-reference | PASS | All 37 contain `Traces to`. Positive control: US-1 AS-1 is found by the same audit. |
| Requirement → scenario → test matrix | FAIL substantively | All 26 FRs are listed, but several mapped instruments do not exercise their scenarios; FR-024 is deliberately not behavioral BDD. MAJ-012. |
| Reachability section | PASS with gaps | §11 names open parent chat/reload and exact-commit tester/validator receipts; its fan-out/timeout/stream measurements need MAJ-012. |
| Specific public documentation TODOs | PASS with corrections required | DOC-1–3 name actual public pages. Apply the corrected actor-independent Stop rule and final restart/card policies. |

## Twelve-Lens Coverage

| Lens | Finding/disposition |
|---|---|
| 1 — Ambiguity | MAJ-005; MIN-001; OBS-001. |
| 2 — Incompleteness | CRIT-001; MAJ-002/003/008. |
| 3 — Inconsistency and ADR/AS-IS contradiction | MAJ-001/002/004/007/009/013. |
| 4 — Infeasibility | CRIT-001's publish-only durability guarantee; MAJ-009's join into a turn that has ended. No new runtime, CGo dependency or unsupported-platform requirement is proposed. |
| 5 — Contract-first gaps | MAJ-006; §9's existing schema/reference direction is retained, not replaced by hand-written wire types. |
| 6 — Security | MAJ-001/002/013: Stop/admission authority, not a new policy layer. Broad authenticated-operator viewing is explicitly accepted; the provisional privacy finding was withdrawn. |
| 7 — Reachability | MAJ-006/012; shared entry and existing chat surface are named, but source wiring alone is not runtime acceptance. |
| 8 — UI states and journey gaps | MAJ-008/009/010. |
| 9 — Accessibility and keyboard | MAJ-011. |
| 10 — Design-system reuse and brand | MIN-002. No unsupported color, typography or brand change is alleged. |
| 11 — Testability and false-green risk | MAJ-003/007/012 and CRIT-001. |
| 12 — Overcomplexity | OBS-001. One shared wake entry is justified by the stated callers; this review does not propose a new messaging service or general scheduler. |

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap description | Required addition / affected scenarios |
|---|---|---|
| Crash integration | Publication and a consumed marker are tested as if they proved model-input durability. | Reopen real stores at each CRIT-001 cut and prove exact combined input is recoverable/placed once. |
| Real-event identity | Repeated same-ID writes do not test distinct events colliding. | Two same-generation restart stops; two different goals at the same round; repeated delivery of each as a paired positive control. |
| Stop and concurrency | Tests use a current marker and a person's helper Stop only. | Landed intentional parent Stop without marker; accepted-but-unlanded Stop; parent-agent helper Stop; self/tree scope and boot retry; normal completion at the enqueue boundary. |
| Outcome integration | One notifier call is not end-to-end once-only consumption. | Session and task outcomes, live joins, fallback, poll-before-wake, deliberately held results, and boot all share the defined outcome identity. |
| Parked questions | Approval wait is treated as equivalent to a pending question card. | Real pending AskUserQuestion registry with no active turn, independent approval wait, correlated answer and outcome races. |
| Frontend states and adapters | Reducer-only tests omit the cold adapter and actual row components. | REST conversion, live/replay sequence handling, SystemMessage and VirtualSystemMessageRow parity, empty/reconnect/error/partial states and long-text expansion. |
| Accessibility | No keyboard/focus/announcement assertions. | MAJ-011's contract, including replay not repeatedly announcing old events. |
| Streaming and fan-out | One token before reload and one browser do not establish incremental delivery to all viewers. | Two browsers on the same parent; controlled partial token before completion; no browser-triggered duplicate turn; different-workspace bucket stays unchanged. |
| Resume safety | A successful transcript row is used as an executor-count oracle. | Historical invocation counter/side-effect evidence; new assistant occurrence reusing the same call ID remains executable. |
| Row repair | Manual second write can conceal the absence of a retry driver. | Independent destination failure, outcome consumption, reopened stores and actual repair pass; no second wake. |
| Public docs and gates | SC-008 conflates behavioral tests with documentation review; frontend gates are not named. | Separate docs-verifier evidence; explicit typecheck/design-system checks and exact-commit tester/validator receipts. Full Go/build authority remains CI, with no reviewer-side suite run. |

### Dataset Gaps

| Dataset | Missing boundary | Recommendation |
|---|---|---|
| Wake reason | Parent-issued Stop, all stop-family first-delivery/replay paths, deliberate hold versus actual failure | Never wake for Stop regardless of actor; distinguish authenticated restart/timeout transitions from a generic error prefix. |
| Recipient state | Landed intentional Stop, pending accepted Stop, root completed during enqueue, restart-stopped recipient | Add the first three; record the founder's restart-parent disposition before fixing the last. |
| Event identity | Repeated stop sequence within one generation; different goals sharing round 1 | Separate source-event identity from deduplicated redelivery. |
| Resume history | Reused provider ID in a new assistant occurrence; independent side-effect count | Keep occurrence-aware history and a separately observable executor count. |
| Inbox admission | Actual cap and rate boundaries/refusal result | Exercise Append and the sender-visible result, not only CapExempt/WakeEligible flags. |
| Wire/replay | Missing/empty session ID, unknown helper kind, cold/live/replay orderings | Pair valid-frame controls with generated-validator rejection and correct session bucketing. Do not turn these into a new account-privacy boundary. |
| User-visible state | Very long text, queued admission, partial row save and reconnect | Assert actual row rendering and clear current state on narrow/touch and keyboard paths. |

## STRIDE Threat Summary

STRIDE means spoofing, tampering, repudiation, information disclosure, denial of service and elevation of privilege. “ok” means addressed by the stated existing boundary or not newly introduced; it is not a penetration-test result.

| Component / flow | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| Wake admission / Stop authority | risk | risk | risk | ok | risk | risk | Marker-only/actor-specific rules must not authorize intentional stopped work. MAJ-001/002. Preserve selected-execution identity and existing admission. |
| Outcome identity and consumption | ok | risk | risk | ok | risk | ok | Incorrect correlation can lose input or charge duplicate turns. CRIT-001; MAJ-005. |
| Transcript rows and repair | ok | risk | risk | ok | ok | ok | Colliding IDs hide distinct history; repair must not depend on unacknowledged wake state. MAJ-003/008. |
| Helper-event live / REST / replay | ok | risk | risk | accepted | ok | ok | Use generated, subtype-discriminated carriers and existing sensitive-data filtering. Any authenticated operator viewing sessions is an accepted assumption in _A sub-agent is a session steered by another session_::D7/R7, not a newly discovered confidentiality promise. |
| Automatic native-helper resume | ok | risk | risk | ok | risk | risk | No historical tool replay, no Stop resurrection, no external-worker replacement or privilege widening. MAJ-002/007/013. |
| Outcome-triggered model spend | ok | ok | risk | ok | accepted | ok | No hourly cap on real endings is deliberate. Enforce identity and once-only consumption; do not reintroduce the forbidden outcome debounce. Progress/checkpoint/artifact remain capped at inbox admission and no longer wake. |

## Reachability Check

| Question | Answer | Evidence |
|---|---|---|
| New agent-facing tool requiring catalogue and policy registration? | N/A | WakeSession is an internal entry, not a new tool. Existing delegate/message_parent paths remain the public doors. No new tool policy or fallback is justified. |
| User-facing named screen/component? | Yes, existing chat; new row wiring remains specified incompletely | Open parent chat is the stated surface. Current row precedents are `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/ChatScreen.tsx`::SystemMessage / VirtualSystemMessageRow and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/GoalOutcomeRow.tsx`::GoalOutcomeRow. MAJ-006/010/012 cover missing carrier/state/test detail. |
| Current session-bound stream mechanism? | Verified by source, not re-run | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/gateway/websocket_streamer.go`::WSHandler.GetStreamer refuses non-webchat or empty session IDs and returns a streamer even with no browser. The parent reconstruction still needs the proposed channel fix. |
| Cold-load path identified? | Existing goal-outcome path verified; helper-event adaptation must be named | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/lib/api/sessions.ts`::rawToMessage forwards goal_outcome; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/store/chat/slices/replay-and-status-frames.ts`::handleReplayAndStatusFrame handles the live/replayed goal_outcome. A new reducer alone does not update the cold adapter. |
| Plan requires execution rather than test authorship alone? | Yes in principle; measurement gaps remain | §11/12 require CI and exact-commit UAT tester plus independent validator. Correct MAJ-012; none of that execution occurred in this review. |
| Required public docs identified? | Yes | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/agents.md`::Workers and delegation; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/goals.md`::Limits and things to watch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/troubleshooting.md`. DOC-1 must include parent-issued Stop, and final restart/card policies must agree with the guide. |

### UX heuristic assessment

**Several issues to address.** This is a written-flow review, not a visual screenshot acceptance of the proposed feature.

| Relevant heuristic | Result |
|---|---|
| H1 — Visibility of system status | MAJ-006/008/010: each promised event needs a lasting row, repair ownership, and truthful queued/running/refused state. |
| H3 — User control and freedom | MAJ-001/002/009: Stop must stay stopped and a question card must keep its correlation and focus. |
| H4 — Consistency and standards | MIN-002: make an explicit shared-row/catalogue choice rather than another look-alike expander. |
| H9 — Recognize and recover from errors | MAJ-008/010: a logged write failure is not an explained user-visible recovery state. |
| H10 — Help and documentation | DOC-1–3 are valid destinations; update them to the corrected Stop and final restart/question behavior. |

**Priority Actions**: (1) preserve Stop/card control; (2) make row durability and recovery truthful; (3) prove live and accessible parent-chat visibility independently of the model repeating an event.

## Current-Behavior Claim Audit

Code wins over dated documents. These rows cover the runtime assertions the review relies on; they do not certify the supplied investigations' historical runtime data.

| Spec claim group | Source check at the pinned HEAD | Result |
|---|---|---|
| §1.1 / §1.2 completion can wake completed, but main-channel reconstruction prevents live web streaming | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_audience.go`::wakeOwnerOrStore / wakeStoredMessage; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_reconstruct.go`::reconstructSteeredTurn; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/gateway/websocket_streamer.go`::GetStreamer | Verified in source, with the separate stopped-root receiver guard and closure race noted above. Not a replayed UAT result. |
| Wake bus channel versus reporting target | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/async_notifier.go`::Notify; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_launcher.go`::reportingTargetFor | Verified: bus channel is system; no-peer reporting target is webchat/session; reconstructed ordinary turn uses metadata independently. Issue #1317's symptom is supported, not its shorthand causal path. |
| Goal-outcome versus Judge transcript, helper-only current writer, met suppression and early acknowledgement | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/goal_outcome.go`::recordGoalOutcome; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/goal_loop.go`::writeGoalVerdictTranscript; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/goal_triggers.go`::agentLoopRunGoalAdjudication.finishMetGoal; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/goal_child_completion.go`::ackMetVerdictEntry / wakeMetVerdictEntry; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_frames.go`::deliverGoalVerdictUpward | Verified, with canonical-identity/durability gaps CRIT-001 and MAJ-005. Task-owned verdict eligibility is not proof of one turn for verdict plus task completion. |
| Stop-notice working-only gate and missing-parent discrepancy | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/stopped_notice.go`::parentWorkingForStoppedNotice / deliverLandedStopNotice | Verified: running/queued or missing record is treated as working; completed is not. Intentional Stop policy changes are MAJ-001/002, not a reason to copy the old gate. |
| Progress/checkpoint/artifact wake and remain admission-capped; legacy debounce is not the production Deliver gate | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/message_inbox.go`::classifyEnvelope / MessageInboxStore.appendMessage; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/async_notifier.go`::WakeParent / WakeParentAlways; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_audience.go`::publishUpward / wakeParentStoredMessage | Verified. The legacy WakeParent call remains in the session-messaging bus consumer; do not mistake that for the direct Deliver path. |
| Restart currently stops rather than resumes; timeout is historical | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/boot_sweep.go`::recoverSteered / failInterrupted; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/lifecycle_edge.go`::StopCauseTimeout | Verified. Boot preserves accepted intentional Stop and existing stopped state. The new auto-resume is a deliberate change, not an existing capability. |
| Existing GoalOutcome rendering/copy, stored subtype, and side-panel distinction | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/lib/goalOutcome.ts`::describeGoalOutcome / buildGoalOutcomeInsertion; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/store/chat/slices/replay-and-status-frames.ts`::handleReplayAndStatusFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/ChatScreen.tsx`::SystemMessage / VirtualSystemMessageRow | GoalOutcome copy and per-ID insertion verified. A subagent status frame is not the independent goal-outcome row. New helper-event cold/render wiring is not already implemented. |
| Not verified preserves active goal and carries the work result | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/judge_conclusion.go`::notVerifiedConclusion; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/goal_child_completion.go`::redriveGoalAdjudication / concludeInteractiveGoalNotVerified | Verified in source. The existing conclusion and its 8,000-byte result cap are not a goal-ending GoalOutcome. |
| Poll suppresses later final and ordinary missing-root initialization exists | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steering.go`::recordPolledFinalConsumed; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/ordinary_root_record.go`::ensureOrdinaryRootRecord | Verified. The latter requires its owning admission lock and refuses a missing helper lifecycle; reuse does not waive visible-main eligibility checks. |
| All parent Stop protection is a current marker; question card implies live turn; call ID is transcript-unique | Source symbols in MAJ-002/007/009 | Rejected as broad assumptions. Landed stopped state, parked question and occurrence-scoped call identity must be represented explicitly. |
| Any other workspace/account socket receiving a goal-outcome frame is necessarily unauthorized | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/gateway/websocket_forward_hub.go`::hubGoalOutcome / hubBroadcastWithSequencedCopy; **A sub-agent is a session steered by another session**::D7/R7, absolute ADR path in the correction above | Rejected. Broad operator viewing is accepted. Session bucketing/rendering isolation remains the feature's promise; this review does not widen it into uncommissioned per-account confidentiality. |

## Unasked Questions

These are author/implementer resolution prompts, not additional founder policy questions.

| ID | Prompt |
|---|---|
| A1 (A / B) | CRIT-001 requires recoverable exact input, not publication alone. **A — recommended:** persist it in an existing committed/addressed carrier before acknowledgement. **B:** delay acknowledgement until the exact input is durably placed. Name the authoritative write, recovery reader and consumed-marker order for the chosen option. |
| A2 (A / B) | Held/already-consumed must not become a fallback wake. **A — recommended:** one canonical outcome key and wake owner. **B:** separate producer keys with an explicit durable correlation and ownership rule. Specify the result dispositions for the chosen option. |
| A3 (A / B) | Acknowledging wake must not strand a missing row. **A — recommended:** repair each transcript destination independently from a retained authoritative event. **B:** retain delivery pending until all required row writes succeed, without republishing a consumed turn. Name the actual production repair driver, not a manual test retry. |
| A4 (A / B) | §15 names no exact companion for scheduled-task `FQ-4` / §5.7. **A — recommended:** provide its absolute path and revision. **B:** make the shared-entry seam self-contained and remove the untraced dependency claim. The existing scheduled-autonomy document at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/specs/schedules-autonomy-spec.md` is not evidence for that newer seam. Its exact companion requirements remain **Unknown — UNVERIFIED WARNING**, not a blocking claim that the other work does not exist. |

## Questions for the founder

These are genuinely open policy boundaries, not requests to revisit the answered no-Stop-wake rule. Team-lead should ask them before the fix round.

### Q1 — Parent interrupted by the same gateway restart (A / B)

A gateway restart can cut off both the helper and its parent. The spec promises a helper-restart parent wake, but also excludes automatic continuation of the person's own main chat. At the checked HEAD, a restart-stopped ordinary parent refuses a helper hand-back wake. This determines whether the person hears from that parent without sending another message.

**A — Recommended:** allow exactly the fresh helper-restart outcome to wake that restart-stopped parent, with the helper event as new input; do not replay the parent's interrupted instruction, and never override an intentional Stop. **B:** retain the row/report but wait for a person or the next authorized ordinary trigger to continue that parent; explicitly narrow the restart-wake promise for this state.

Answer: **Q1 A** or **Q1 B**.

### Q2 — Helper outcome while the parent awaits a question-card answer (A / B)

An approval waits inside a live execution, but a structured question card parks and ends that execution. The spec says both should “join the live turn”; that is not the runtime state for a pending question. The choice is whether the parent handles a helper result immediately while still waiting for your answer, or waits and handles both together.

**A — Recommended:** show the helper row immediately, retain its report durably, and deliver it with the person's correlated answer when the parked parent continues; the existing card stays valid and no extra outcome-only turn starts while it is unanswered. **B:** let the outcome start a separate parent execution immediately while preserving the unanswered card and its later answer; the spec must define the ordering and turn-count exception explicitly.

Answer: **Q2 A** or **Q2 B**.

## Verdict Rationale

**BLOCK** follows from CRIT-001 alone: the concrete publish/ack/consumption ordering does not meet the stated durable-input guarantee. MAJ-001–013 independently require revision; in particular, the corrected actor-independent Stop policy must reach all delivery/replay paths, and a green classifier/reducer test cannot certify the crash, side-effect or live-rendering promises. The former broad-broadcast privacy finding is withdrawn, not deferred.

### Recommended Next Actions

| Order | Action |
|---|---|
| 1 | Team-lead interviews the founder on Q1/Q2. Record the already supplied Stop clarification as settled, not another question. |
| 2 | Spec author fixes CRIT-001 and MAJ-001–009: exact-input durability, Stop state/actor coverage, distinct event and shared outcome identities, closure fallback, row carriers/repair, occurrence-based resume and parked-card handling. |
| 3 | Complete the frontend, accessibility, catalogue, governing-decision/security sections and truthful test matrix (MAJ-010–013; MIN-001/002). Retain DOC-1–3 and correct their Stop/restart wording. |
| 4 | Run the fixed second spec grill on the corrected document. Implementation then follows the agreed RED/GREEN/CHECK, five-reviewer feature gate, CI, exact-commit hands-on tester/validator and founder landing approval; this report does not perform or waive them. |

### Next step in the process

Verdict: **BLOCK**

Review written to: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/specs/parent-wake-visibility-spec-review.md`

This is grill round 1 of 2 (fixed). Next: team-lead interviews the founder on "Questions for the founder", then the spec author fixes round-1 findings, then grill-spec runs SPEC MODE ROUND 2 on the corrected spec at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/specs/parent-wake-visibility-spec.md` — regardless of this round's verdict.

**Code correct and tested:** Not established; no implementation or product test execution in this review.

**Reachable by a user/agent:** Not established for the proposed behavior; current source entry/render paths were inspected, not accepted through UAT.

skills: omnipus-shared-rules, grill-spec, gitnexus-exploring, ux-heuristics-review, omnipus-design-system, github-cli, jev-use:jev-use, commit-messages

## Evidence Table

| Claim | Evidence | Certainty |
|---|---|---|
| Exact review baseline and unchanged production relative to the author's cited base | `git rev-parse HEAD`, exit 0: `bc7dc46ea9ae1486efdd89eeb1458fc23baf3e25`; `git diff --name-status a1eb083a4842886699740f1f2c5b93c6ba6b6974 bc7dc46ea9ae1486efdd89eeb1458fc23baf3e25`, exit 0: only the spec addition. Initial `git status --short`, exit 0: empty. | Verified — high confidence |
| Round 1 and complete structural census | Read-only Python document audit, exit 0: `stories=7`, `bdd_blocks=37`, `functional_requirements=26`, `traceability_rows=26`, `planned_test_rows=30`; both review filenames absent before writing. Second audit, exit 0: `acceptance_count=40`, `bdd_trace_pairs=40`, `uncovered_acceptance=[]`. | Verified — high confidence |
| Empty structural/test searches had positive controls | Same audit, exit 0: known Functional Requirements/Test implementation order and US-1 AS-1 found; `bdd_without_backreference=[]`, two-browser/creator_notice absent from the actual test table, required UI/accessibility/security/catalogue headings absent. Complete spec was also read. | Verified — high confidence |
| Corrected Stop authority applies to parent-agent and human Stops | Team-lead steer in this review, Source: Interview 2026-10-15: “a STOP of a helper (by the parent agent or by a person) is never a wake reason ... never wakes the parent and never auto-resumes the helper.” Compared against PW-D4/PW-D7, closed list and Test 28. | Verified supplied authority — high confidence |
| CRIT-001 ordering defect | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/bus/bus.go`::publish; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/async_notifier.go`::Notify; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/loop_inbound.go`::processSteeredSystemWake; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/loop_run_turn.go`::agentLoopRunTurnPrepare.selectTurnProvider. | Verified code/order; crash loss Inferred — high confidence |
| MAJ-001/002/003 Stop policy, lasting state and event-ID gaps | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/message_inbox.go`::classifyEnvelope; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/stopped_notice.go`::stoppedChildNoticeID / deliverLandedStopNotice; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_completion_commit.go`::landSteeredStopLocked; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_cancel.go`::reviveWithExecution. | Verified code/spec mismatch; consequences Inferred — high confidence |
| MAJ-004/005 closure and outcome-consumption gaps | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_audience.go`::wakeOwnerOrStore / Deliver; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steering.go`::enqueueSteeringItemWithStatus / recordPolledFinalConsumed; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/goal_child_completion.go`::wakeMetVerdictEntry; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/task_executor_judge.go`::deliverTaskCompletionUpward. | Verified paths and unspecified shared identity; races Inferred — high confidence |
| MAJ-006/008 missing row carrier/repair guarantees | Reviewed spec::FR-025 / §9.3 / §9.9; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/goal_outcome.go`::recordGoalOutcome; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/stopped_notice.go`::deliverLandedStopNotice; existing Message and GoalOutcomeFrame schemas read. | Verified specification and source gaps; outcome Inferred — high confidence |
| MAJ-007 call ID is not invocation identity | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/window_runtime.go`::recordCallIssuers; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/ordinal_window_test.go`::TestOrdinals_RepeatedCallIDNamesItsOwnIssuer; session-core ADR::D2 / U2-ARCHSHAPE read. The test was read, not run. | Verified — high confidence |
| MAJ-009 pending question need not have a live turn | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/tools/ask_user_question.go`::AskUserQuestionTool.Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/loop_run_turn_tools.go`::agentLoopRunTurnToolsExecute.finishCall; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/ask_user_wire_test.go`::TestRequestCancel_CancelsPendingAskWithoutActiveTurn. | Verified code/fixture, not re-run — high confidence |
| MAJ-010/011/012 and MIN-002 frontend/state/test gaps | Complete spec::§10–12 / SC-001,005,008 / matrix; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/ChatScreen.tsx`::SystemMessage / VirtualSystemMessageRow; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/GoalOutcomeRow.tsx`::GoalOutcomeRow; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/design-system/catalog.json`::entries; read-only catalogue inspection found Accordion/EmptyState/QueryErrorState/Skeleton. | Verified omissions/current patterns; UX effects Inferred — medium/high confidence |
| MAJ-013 governing decisions require a narrow supersession map | **The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume**::D6/D8; **Steering commands: no person question**::C1/C5; **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**::D2/D3/D6; absolute ADR citations in MAJ-013. AS-IS walkthrough and plugin assessment read as dated context, not current code authority. | Verified document contradictions — high confidence |
| Broad operator viewing is accepted; provisional privacy finding removed | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/architecture/ADR-091-steered-sessions-replace-subagents.md`::D7 — Viewing authority / Risks R7; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/gateway/websocket_forward_hub.go`::hubGoalOutcome / hubBroadcastWithSequencedCopy; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/gateway/websocket_first_message_security_test.go`::TestExistingUserMessage_RetryIsIsolatedByPrincipal has the narrower retry-cache scope. | Verified correction — high confidence |
| Public-doc destinations and current promises were checked | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/agents.md`::Workers and delegation; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/goals.md`::Limits and things to watch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/tools.md`::Limits and things to watch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/security.md`::Scheduled and unattended runs; troubleshooting headings inspected. | Verified source review, not a docs-implementation audit — high confidence |
| R7 screenshots are different chats, not contradictory proof of parent visibility | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/qa/l1-uat-t1-r2/R7-2-goal-met-judge.png`: composer “Message General Purpose”; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/qa/l1-uat-t2-r2/r7-no-visible-judge-verdict.png`: Jim main-session title/composer. Both images read directly. | Verified images only — high confidence |
| No runtime PASS, graph impact or live-instance claim | Performed operations: source/document/image reads, read-only Git/GitHub inspection and this review artifact. No product suites, gateway requests, live-instance operations or production edits. Graph unavailable; documentation-only blast radius is Inferred from scope/diff, not a graph run. | Verified operation scope; feature readiness Unknown |
| Report artifact checks | Read-only `python3 -I` final-document audit, exit 0: `counts=1/13/2/1; lenses=12; questions=2; absolute_paths=53; missing=0`; seeded missing-failure and missing-lens copies rejected; input spec hash unchanged. Initial whitespace check caught seven Markdown hard-break suffixes (exit 3); after replacing them with blank lines, `git diff --cached --check`, exit 0. `git diff --cached --name-status`, exit 0: only this review added. | Verified artifact checks, not product tests — high confidence |
| Severity classification cross-check | One batched `jev-use judge` call fed directly from the report through read-only Python, exit 0: `escalated: false`; classifications agree on 1 critical, 13 major, 2 minor and 1 observation. This is a classification check, not independent source verification or a test run. | Verified tool verdict only — high confidence |
| **Self-check** | Reconciled every counted finding with its cited spec heading and first-hand source; checked all twelve lenses, structural/test/STRIDE/reachability sections, corrected Stop authority, separate founder questions and fixed round-two next step. Rechecked the 26-FR/40-acceptance/37-BDD/30-test census with positive controls; no product-green claim or withdrawn privacy finding is counted. Final staged-diff/scope checks accompany the report commit. | Verified document/source self-check; product execution not performed |
