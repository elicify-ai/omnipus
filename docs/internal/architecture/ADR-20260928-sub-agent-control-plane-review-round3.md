# Independent review — The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume

**Status:** BLOCK — not ready to use as the build specification

**Date:** 2026-09-30

**Mode:** ADR review; one grill invocation, followed by first-hand verification of its findings

**Reviewed revision:** `9a4380295ca28d0270c208683bc93628f717a415`

**Release source inspected:** `origin/release/v0.1.1` at `4f24f1e462fc1105848f2730a86d137ee330eb4b`

## Executive summary

**BLOCK.** The revised ADR resolves several important founder choices, but its completion-versus-Stop rule does not account for an already-published result. Following the stated lifecycle winner rule while retaining today's publication and deduplication path can suppress a later result from the resumed session. Four further major gaps prevent an unambiguous build: the founder-rejected held-claim mechanism is restored, authenticated owner-message provenance has no durable writer specified, plan Stop still has a separate task-goal ending path, and delivered steers never become final under the ledger's literal rules.

| Severity | Count | Effect on this verdict |
|---|---:|---|
| CRITICAL | 1 | Blocks build readiness |
| MAJOR | 4 | Must be resolved before using the ADR as the build specification |
| MINOR | 2 | Source/test-instrument corrections; do not independently block |
| OBSERVATION | 1 | Suggested test data, not a new requirement |
| **Total** | **8** | **BLOCK** |

**Certainty:** the cited source facts and conflicting requirements are **Verified (high confidence)**. Runtime consequences are **Inferred (high confidence)** from those facts, not reproduced incidents. No implementation test, build, CI run or browser acceptance is claimed.

**Frontend coverage was not as deep as backend coverage.** The written Stop/Esc, receipt, question, five-state display and plan/task requirements were reviewed, together with the relevant wire schemas. This pass did not inspect every SPA consumer or execute the UI. Frontend implementation readiness is **UNVERIFIED — WARNING**, not an additional blocking finding. There is no visual-design verdict.

## Scope and evidence boundaries

| Item | Basis |
|---|---|
| Artifact under review | [The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume][adr], including D1–D10, T1–T26, four build units, the current Impact table and all 22 round-2 dispositions |
| Build-spec authority | [Founder plan][plan], Q26=A and the train-3 rulings: this corrected ADR serves as the specification; this is not a review of a future, unwritten spec |
| Binding decisions | The founder-plan section “2026-09-29 Founder rulings — session states, stop model” through the end, plus the earlier explicit Q2=B simplification that the later train-3 ruling incorporates |
| Prior findings | [Independent round-2 review][round2]; all 22 entries compared with the revised decisions, not merely counted |
| Architectural grounding | Relevant sections of [AS-IS architecture][as-is], [Plugin Extensibility — Evidence-Based Assessment][plugins], [A sub-agent is a session steered by another session][steered] and [An open conversation must keep the ability to delegate][open-conversation]; pinned code wins over historical descriptions |
| Revision discipline | Code inspected with `git show` at the release SHA above. Disputed boot-hook and Stop-test assertions were also checked at the ADR author's stated baseline, `e4d9a126c7e72a27238cd9f6de7ab55792139cc5` |
| Unmerged work | Simplified Q2=B branch inspected at `82a1e351a1ed5b28cd507970ca8abe35cd563195`; older deferred-claim branch at `4c693d186d4e94c892b9fca08de8e8097c0f6e3e`. Neither is silently treated as released code |
| Source navigation | GitNexus tools/resources were unavailable. Targeted Git source reads/searches were used; no graph impact or `detect_changes` result is claimed |
| Change boundary | This review report only. The reviewed ADR, production code, contracts, tests and founder plan are unchanged by this review |

The old numbered review IDs below refer to the **round-2 findings**, not to this report's findings. The historical appendix is not current build authority. Source keys in the final evidence table identify the exact files and symbols supporting the findings.

## Blocking findings

### CRIT-001 — The lifecycle race winner can disagree with an already-published final result

| Field | Finding |
|---|---|
| Affected sections | D2 completion/Stop precedence; D8.1, D8.9; T11; Impact's retained final-report repair |
| Severity | **CRITICAL** — a resumed result can be silently suppressed |
| Certainty | **Verified (high confidence)** publication order, final-message identity and contradictory recovery instructions; **Inferred (high confidence)** resulting failure sequence |
| Evidence | **E-PUBLISH**, **E-BOOT**, and ADR D2/D8/T11 |

**Failure scenario.** A child publishes its final handback, and the parent consumes it. Stop then commits its fence before the child's terminal lifecycle mutation. D2 says Stop wins and the child becomes stopped. RESUME continues that same generation. Today's final-message identity is `<child>:<generation>:final`; an acknowledged message with that identity makes subsequent completion delivery short-circuit. The resumed child's genuinely new final result can therefore be discarded as an already-delivered result.

This is a verified source-order gap, not an assertion that an arbitrary Stop always loses data. `deliverSteeredCompletion` calls the upward deliverer before its lifecycle mutation. The deliverer appends the deterministic final message and emits upward frames/wake. `completionFinalAlreadyStored` and the deliverer both recognize acknowledged final identities. D2 defines the winner at the later lifecycle write without specifying how the earlier publication is fenced or invalidated.

The crash interval is also unresolved: a stored final may exist while the child is still queued/running. D8 says to stop interrupted working records, while Impact preserves `finishFromFinal`, which repairs a non-terminal record to completed/failed from that stored message. A build specification must say which durable event has committed the outcome.

**Required correction.** Define one recoverable commit boundary joining the winning lifecycle outcome and permission to publish it. One possible design is to persist an unpublished result, commit the winning lifecycle decision, then publish/retry only the committed result. The exact design belongs to the architect; the required property is that a losing completion cannot consume the final identity needed by a same-generation RESUME. Reconcile final-report boot repair with that boundary.

**Required proof.** Extend T11 beyond final state and stop receipts: assert parent inbox content, frames, acknowledgment, a subsequent same-generation resumed result, and recovery at each publication/commit crash interval. Exercise both Stop-first and completion-first orders through the real paths.

### MAJ-001 — The ADR reinstates the held-claim mechanism the founder explicitly removed

| Field | Finding |
|---|---|
| Affected sections | D6 deferred settlement; D8.5; T7, T12, T15, T16; build unit 3; Alternatives; round-2 CRIT-002 disposition |
| Severity | **MAJOR** — wrong product behavior and an unapproved state machine |
| Certainty | **Verified (high confidence)** contradiction with binding founder instructions |
| Evidence | **E-RULINGS**, **E-Q2B**, ADR D6/D8 and the named tests/build unit |

**Failure scenario.** The team integrates the approved immediate-rejection implementation, then follows the ADR and its RED tests by adding held-claim reconstruction from `Goal.LatestClaim`, descendant-settlement rechecks, durable deduplication and fresh-claim scheduling. That implements the machinery the founder dropped, rather than the approved simpler behavior.

The founder ruling is explicit: a goal `met` claim is rejected while the parent's own helpers are still running; “Held claim / launch fence / re-check / speed index dropped. Rare claim-and-launch race accepted.” The later train-3 scope explicitly names the **Q2=B simplification**. The inspected simplified branch rejects the claim before accepting claim state. The older `resumeDeferredGoalAfterDescendantTerminal` branch is not authority to undo that decision.

**Required correction.** Keep immediate claim rejection, including the required waiting-child block. Keep the independent durable stopped-child notice: the parent must still be told and decide. Remove the held-claim reconstruction/recheck requirements and rewrite tests whose setup or expected result says “claim held” or “deferred met.” Mark round-2 CRIT-002's obsolete mechanism as superseded, not as a requirement to restore it.

This does **not** remove the separate criteria-free quiet-subtree completion rule. That rule can still use the required waiting/stopped traversal behavior. Distinguish it from accepting and holding a goal claim; do not reintroduce the founder-accepted claim/launch race fence.

### MAJ-002 — Strict owner answers lack a specified durable authenticated-message source

| Field | Finding |
|---|---|
| Affected sections | D1.4–D1.5; D4 persistence/wire inventory; T1–T3, T14 |
| Severity | **MAJOR** — the promised authorized answer path is not fully buildable |
| Certainty | **Verified (high confidence)** persistence mismatch; **Inferred (high confidence)** restart consequence; no demonstrated authorization exploit |
| Evidence | **E-OWNER**, ADR Who/When/Which/Once rules |

**Failure scenario.** The signed-in owner sends coded answer `m7`. The process restarts before the root agent cites that persisted message in `respond`. The current transcript retains its server message ID and text, but not that message's authenticated `GatewayUserID`. D1 requires validation of the earlier message's identity. An implementer must either reject a genuine answer or invent an unsafe substitute such as the session owner, the current caller, or an agent-supplied identity.

The gateway places authenticated `GatewayUserID` on the transient inbound message. Its persisted user `TranscriptEntry` does not carry authenticated author provenance. A session's Owner is not proof of who authored each message in that session. The separate client message ID is not an authenticated identity either.

**Required correction.** Specify a server-written durable provenance record linking the server message ID, session, authenticated principal, exact answer text and ordering relative to the shown-question entry. Name the authenticated intake writer and lookup path. Do not trust an agent assertion or a client-selected message identifier as the provenance. Any new bytes exposed across the gateway/SPA boundary remain contract-first; backend-lead owns those edits/regeneration.

**Required proof.** Add a positive owner-answer-after-restart case, alongside non-owner, forged-reference, channel/missing-provenance and stale/shown-anchor negatives. Retain Once across direct and relayed answers. This does not reopen the accepted parent-withdrawal risk or the accepted web-sign-in requirement.

### MAJ-003 — Plan Stop clears task-owned goals through a separate, unlisted writer

| Field | Finding |
|---|---|
| Affected sections | D6 goal independence; D8.10; T17, T20; Impact and unit-3 dependencies |
| Severity | **MAJOR** — plan restart can retain the session but lose its active goal |
| Certainty | **Verified (high confidence)** current writer chain and ownership distinction; **Inferred (high confidence)** incomplete-implementation consequence |
| Evidence | **E-PLAN-GOAL**, ADR's unchanged Plan/Task wire pairs and preserved-goal requirement |

**Failure scenario.** The implementation removes the listed session-owned terminal hooks and changes plan restart to reuse the original member sessions. `StopPlan` still marks an in-progress task `failed(stopped_by_user)` and calls the independent task-goal ending path. That path maps this pair to `GoalStateCleared`. Restart reuses the member's history, but its task-owned goal has already been cleared.

The actual chain is `StopPlan` → `cancelMemberLocked` → `terminateTaskGoalRecord` → `TerminateTaskGoalRecord` → `GoalStateForTerminalTask`. It resolves `GoalOwnerKindTask`. The session-owned hook explicitly ignores non-session ownership, so removing that hook cannot fix this path.

**Required correction.** Explicitly amend task-terminal-to-goal-terminal coupling for resumable plan Stop while retaining the founder-approved Plan/Task wire pairs. Add the task-goal writer and affected callers to Impact and the plan integration dependency. Do not broadly remove independent successful or exhausted goal adjudication.

**Required proof.** T17 must use a real task-owned active goal, stop the plan through the real stop path, restart it, and prove the same goal remains active with the same member session/history. A fixture using only a session-owned goal would miss this writer entirely.

### MAJ-004 — Delivered steers remain pending forever under the literal ledger rules

| Field | Finding |
|---|---|
| Affected sections | D4 receipt states and Retention; D5 pending definition and byte cap; T22 |
| Severity | **MAJOR** — ordinary successful use can exhaust admission and prevent compaction |
| Certainty | **Verified (high confidence)** incompatible definitions; **Inferred (high confidence)** literal-implementation consequence |
| Evidence | **E-LEDGER** |

**Failure scenario.** A child receives sixteen 16 KiB steers, all successfully delivered and present in its transcript. D4 says a steer never reaches `applied`. D5 defines pending as neither applied nor superseded, and D4 explicitly treats delivered controls as pending for compaction. The 256 KiB pending-text budget can therefore remain full even though there is no undelivered text. A later steer is refused; completion to done does not itself supply a retirement transition for those receipts.

A later Stop/redirect might supersede controls, but requiring that unrelated action is not a defined successful-delivery lifecycle. Nor can an implementer mark a steer applied: the ADR correctly refuses to claim model compliance.

**Required correction.** Define finality by control verb. For a steer, durable delivery should be runtime-final without claiming compliance; only undelivered pending text should consume its queue byte allowance. Retain delivered receipt summaries and replay-prevention identities during compaction. Make the recovery exception for missing transcript injection explicit in that rule.

**Required proof.** Add normal delivered-steer cases to T22: budget is released, a later steer is accepted, a quiescent done/stopped ledger can compact, and restart does not inject delivered text again.

## Non-blocking findings

### MIN-001 — Several “Verified” Impact claims contradict the inspected source

| Field | Finding |
|---|---|
| Severity | **MINOR** — inaccurate implementation map, not a separate duplication of the behavioral blockers |
| Certainty | **Verified (high confidence)** for the pinned sources below; historical timing of the unpinned local-branch claim is **Unknown** |
| Failure scenario | An implementer follows a claimed absence, skips a real goal-ending hook, or writes RED work for behavior already present. Broad normative requirements may catch the mistake, but the supposedly verified map cannot safely guide the work |
| Evidence | **E-BOOT**, **E-STOP-TEST**, **E-Q2B**, **E-SYMBOL** |

| ADR assertion | Source result and required correction |
|---|---|
| Only one boot `EndSessionGoal` call exists; `finishFromFinal` has none; the second sweep has no goal-end hook | Both `SteerBootRecovery.finishFromFinal` and `failInterrupted` invoke `EndSessionGoal`. `PlanEngine.sweepToFailedInterrupted` invokes `steeredGoalEndHook`; the gateway installs both bindings. Verified at both the author's stated baseline and the inspected release revision. List all paths explicitly |
| `assertStopLandedOn` rejects every terminal state other than cancelled | At both baselines it already accepts cancelled **or completed** through `legalTerminal`. The new stopped/non-terminal behavior still needs testing, but “teach it to accept completed” is not the missing change |
| The Q2=B direct-child branch has no `LifecycleNeedsInput` case | The branch inspected at `82a1e351a` already has that case. This may be later branch work, not a false historical read; no exact earlier SHA was supplied for the four-commit snapshot. Pin the implementation revision, retain T12, and do not dispatch a duplicate fix on the current branch |
| The #1026 goal-claim entry points to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/tools/goal_claim.go::executeSetGoal` | That symbol is absent from the inspected file; `GoalClaimTool.Execute` is the actual entry point. Correct the citation and separately locate any set-goal/schema work required by #1026 |

The false boot and Stop-test claims are **not explained by release drift**: they are also false at the baseline the ADR cites. Update the Impact and Evidence sections; do not preserve the inaccurate “code correction” as authority.

### MIN-002 — T11's claimed existing test seam does not control the real completion writer

| Field | Finding |
|---|---|
| Affected sections | T11; round-2 OBS-003 disposition |
| Severity | **MINOR** — test-instrument specification is inaccurate |
| Certainty | **Verified (high confidence)** concrete-store versus interface wiring |
| Failure scenario | A test wraps the `LifecycleMutator` accepted by `TransitionSession`, gets a deterministic green, but never interleaves the real completion writer. The race the test claims to cover remains uncontrolled |
| Evidence | **E-INSTRUMENT**, **E-PUBLISH** |

`TransitionSession` accepts `LifecycleMutator`; the real `deliverSteeredCompletion` path obtains a concrete `*session.LifecycleStore` through `GetSessionLifecycleStore` and mutates it directly. An interface having the same method is not proof that the actual caller can be substituted. The current completion path also contains a production-declared `completeStateWriteTestHook`, whereas T11 promises a test-owned wrapper at an existing seam without a production test hook.

**Correction.** Identify a real controllable boundary for both actors, or explicitly include a normal dependency-seam refactor that the real completion path uses. Then force both commit orders and the publication intervals from CRIT-001. Do not assert that deterministic testing is impossible; the defect is the claim that this particular existing seam already suffices.

### OBS-001 — Make boundary and partial-write cases explicit in the test data

**Severity:** OBSERVATION. **Certainty:** Inferred (medium confidence), test-planning recommendation grounded in D1/D4/D5, not an observed defect.

T1–T26 would be easier to audit with datasets at 200/201 queued items, the 16 KiB body limit, the 256 KiB pending-text limit, the sixth/seventh rate-limited call and exact question expiry. Include competing direct/relayed answers and durable-write failures between answer consumption, relay closure and dispatch. Assert visible refusal or recoverable state, not only a return code. These are refinements of existing rules, not permission to add new product limits or retry policies.

## Disposition of all 22 round-2 findings

“Resolved” below means **resolved in the written design**, not implemented or tested. Partial resolutions remain explicitly qualified.

| Round-2 ID | Independent disposition |
|---|---|
| CRIT-001 | **Original collision resolved:** the live Stop fence and lasting stop note are distinct, with atomic landing/resume rules. New publication/identity gap remains: this report's CRIT-001 |
| CRIT-002 | **Not resolved consistently with founder policy:** stopped-child notice is required, but the held-claim repair must be superseded by immediate rejection; MAJ-001 |
| MAJ-001 | **Resolved:** Q19 timeout, Q20/Q21 single-target clear and the false founder quotation are corrected |
| MAJ-002 | **Resolved:** omitted/session scope is self-only at the server, including root; tree scope is explicit |
| MAJ-003 | **Normative rule resolved; evidence not corrected:** all session outcomes are independent of goal ending, but real boot hooks are falsely denied; MIN-001 |
| MAJ-004 | **Resolved:** lifetime-timeout RESUME gets a fresh budget; question expiry remains separate |
| MAJ-005 | **Accepted risk correctly recorded:** founder chose parent withdrawal without owner approval. This review does not reopen it |
| MAJ-006 | **Resolved in design:** Stop/Stop all preserve the pending question, relay, shown anchor and original deadline. MAJ-002 identifies an additional durable answer-source prerequisite |
| MAJ-007 | **State/frontier rule resolved; claim behavior still wrong:** waiting blocks, stopped prunes, but held-claim tests conflict with immediate rejection; MAJ-001 |
| MAJ-008 | **Session reuse choice resolved; integration incomplete:** same step session/history is required, but the task-owned goal writer is missing; MAJ-003 |
| MAJ-009 | **Resolved:** direct-parent storage/wake rules, deduplication, eager restart notices, exact state and `resume` vocabulary are specified |
| MAJ-010 | **Resolved:** pre-crash controls remain pending and require a new explicit RESUME; boot does not dispatch them |
| MIN-001 | **Resolved:** current action and receipt vocabulary is aligned; historical names are labelled as such |
| MIN-002 | **Resolved:** prior alternatives, affected components and dispositions are isolated in a superseded appendix |
| MIN-003 | **Baseline separation improved:** release and local Q2=B work are distinguished. Pin/update the local branch evidence; the inspected branch already blocks needs-input; MIN-001 |
| MIN-004 | **Partial:** stopped ledgers are eligible to compact, but delivered steers never become final under the current definition; MAJ-004 |
| MIN-005 | **Resolved:** question expiry and session lifetime timeout have distinct outcomes |
| MIN-006 | **Wire choice resolved:** Plan/Task reason pairs remain; GoalOutcome stop wording is explicit. The retained task pair still needs the MAJ-003 writer change |
| MIN-007 | **Resolved in written UI behavior:** Esc respects focus and higher-priority editor/dialog handlers; no browser validation claimed |
| OBS-001 | **Resolved:** two-pass boot recovery replaces an unnecessary edge-order rewrite |
| OBS-002 | **Resolved:** single-user posture is stated without deleting retained owner checks |
| OBS-003 | **Partial:** both interleavings are specified, but the claimed existing test instrument does not intercept real completion; MIN-002 |

## RED test assessment: T1–T26

The inventory contains all 26 IDs. That is not proof of adequacy or execution. **As a set, these scenarios are not yet sufficient for a build gate** because some require rejected behavior and others miss the actual durable writers/boundaries.

| Test | Assessment and required interpretation |
|---|---|
| T1 | Multiple-question ambiguity is testable. Use authenticated persisted messages, not an agent-authored owner flag |
| T2 | Message reuse refusal is testable. Persist consumption and include restart/direct-versus-relayed reuse |
| T3 | Who/When negatives are testable once MAJ-002 defines real provenance; include a valid-owner positive control |
| T4 | Stopped-note persistence and child non-resumption are specified and testable |
| T5 | Redirect/steer ordering is specified; synchronize actual enqueue/injection boundaries rather than sleep-based timing |
| T6 | Two-pass boot, per-direct-parent notices, stopped-parent storage and repeated-sweep deduplication are specified |
| T7 | **Rewrite:** “claim held” contradicts immediate rejection. Separately test the legitimate recursive completion frontier |
| T8 | Original 24-hour expiry and visible fatal handback are specified; use a controlled clock and the real expiry path |
| T9 | History plus resume-note input is testable. It proves no replay of the original instruction, not that a model can never choose to repeat an external side effect |
| T10 | Prove a late steer drains without an external nudge/restart. Check ledger wake deduplication, without claiming the separate #1027 compaction issue is fixed |
| T11 | **Expand/correct:** publication, acknowledgment, same-generation resumed result and crash intervals; use a real-path barrier, not a helper-only imitation |
| T12 | **Rewrite expected result to refusal:** waiting direct child blocks claim admission. The inspected simplified branch already includes needs-input; keep an integration regression |
| T13 | Own-target goal clear, distinct durable notice and no automatic descendant clear/Stop are specified |
| T14 | Retained question and answer-driven helper resume are specified; add positive authenticated-message persistence across restart |
| T15 | **Rewrite obsolete held-claim setup:** keep one durable stopped-child notice and parent choice without reconstructing rejected claim state |
| T16 | **Rewrite obsolete held-claim setup:** retain stopped-node traversal cut and restored blocking after resume; distinguish direct claim admission from recursive completion |
| T17 | Same session/history and recoverable dispatch are specified. Must include the real task-owned goal writer and verify active goal identity across Stop/restart |
| T18 | Preserved intent, no boot dispatch, fresh RESUME release order and repeated-crash behavior are specified |
| T19 | Fresh lifetime budget versus unchanged question deadline is specified; prove an actual eligible model dispatch, not only a timestamp |
| T20 | Goal/session independence is specified. Cover both boot hooks/bindings and relevant ownership kinds; do not let a session-goal-only fixture stand in for plan tasks |
| T21 | Server-scope cases and real focused UI interactions are specified. Include omitted scope and higher-priority Esc handling, not only a frontend helper |
| T22 | **Expand:** normal delivered steers must release admission budget and permit compaction while remaining replay-safe; MAJ-004 |
| T23 | Genuine failure settles promptly, descendant stops preserve goals, and incomplete fan-out is visible/recoverable. Assert no restored wait-for-descendants hang |
| T24 | #1026 scope is present, but use actual tool/schema/read/chat surfaces: owner positive, delegated/grandchild negatives, empty arrays, criterion over 1,000 characters, and visible “Judge: …” feedback with the issue's existing secondary styling |
| T25 | Accepted withdrawal risk is represented without fabricating an authenticated answer; stale-owner-answer refusal and audit are testable |
| T26 | Exact versus coarse session state, retained Plan/Task wire pairs and visible “Stopped” labels are specified; test generated validation and actual readers, not just type declarations |

## Build units and Impact corrections

**The contracts-first order and one-train integration rule are sound. The current dependency details are not ready to dispatch unchanged.**

| Unit | Required correction / dependency |
|---|---|
| 1 — contracts and RED | Close durable owner-message provenance and per-verb receipt finality before consumers/tests hard-code assumptions. Rewrite held-claim tests and identify T11's real instrument. Backend-lead still owns contract edits/regeneration |
| 2 — lifecycle, ledger, questions and recovery | Settle outcome/publication commitment before implementing same-generation Stop/RESUME and boot repair. Include the authenticated intake writer, all real boot goal hooks and delivered-steer retirement. Keep shared writer integration coordinated |
| 3 — #1020, Q2=B, #1053 and plans | Integrate **simplified immediate rejection**, not deferred settlement. Preserve task-owned goals before claiming plan session reuse is correct. Keep #1053 prompt failure settlement separate from the quiet-subtree success gate |
| 4 — train gate and reachability | Keep one train-3 integration/PR/CI flow under founder rules; component branches do not gain separate CI runs from this report. CHECK must verify saved RED/GREEN receipts and real tool/UI invocation. No landing approval is supplied here |

Frontend Stop/Esc/receipt work and independent #1026 work can still proceed in separate worktrees after their shared generated contracts are settled. Shared lifecycle/completion edits need integration coordination; this is not a reason to serialize unrelated work.

| Impact area | Audit result |
|---|---|
| Stop fence/note, cascade entry points, lifecycle mapping, delegate action enum, gateway CancelFrame routing | Named source surfaces were found. The proposed outcomes are future work, not present behavior. Publication semantics need CRIT-001 before the completion row is actionable |
| Boot goal-ending inventory | Incorrect at both inspected baselines; see MIN-001. Include both recovery calls, the plan sweep hook and both gateway bindings |
| Plan Stop/restart | Top-level entry points are real; omission of the separate task-goal writer is substantive, MAJ-003 |
| Q2=B | Correctly separated from release, but an obsolete deferred branch must not dictate the new design. The inspected simplified branch already has the waiting-child case |
| Owner-answer storage | Question/relay storage is specified; authenticated message intake/provenance is not. Add that writer/reader pair, MAJ-002 |
| #1026 symbol | Correct `GoalClaimTool.Execute`; do not dispatch against nonexistent `executeSetGoal` in the cited file |
| Existing Stop regression test | Already accepts completed; add new stopped-state/race assertions rather than claiming completed is currently rejected |
| Frontend and user documentation | Impact explicitly leaves detailed frontend inspection and the exact user-doc destination to their owners. That remains a stated verification gap, not proof of coverage |

Issue scopes were read directly: [#1020](https://github.com/elicify-ai/omnipus/issues/1020), [#1026](https://github.com/elicify-ai/omnipus/issues/1026), [#1053](https://github.com/elicify-ai/omnipus/issues/1053), and the historical [#984](https://github.com/elicify-ai/omnipus/pull/984). Historical issue/PR test claims are not this review's test evidence. No #1027 fix, #1083 display/hide work or later unrelated backlog item is silently added to train 3.

## Cross-cutting and user-interaction assessment

| Concern | Result |
|---|---|
| Ownership and coupling | Session, task and goal ownership must remain distinct; MAJ-003 exposes a real cross-owner writer, not a style preference |
| Concurrency and recovery | CRIT-001 is the primary unresolved commit boundary. D1 consumption, D4 receipts and plan dispatch require durable recovery tests, not only state-shape assertions |
| Identity, tampering and audit | Strict owner checks are appropriate, but durable provenance is missing (MAJ-002). The accepted parent-withdrawal risk remains accepted; no new multi-user permission model is requested |
| Availability and footprint | Visible refusals and non-refused essential messages are explicit. Delivered-steer finality undermines the ledger bound (MAJ-004). Actual RAM/disk behavior remains unmeasured; no footprint pass claimed |
| Degradation and ecosystem | The ADR retains native versus external-child distinctions and file-based control storage; this review does not propose a new executable/plugin runtime. Cross-platform behavior is untested |
| H1/H2 — visible state and plain naming | Five visible session states, separate receipt delivery/enforcement, and “Stopped” plan/task labels are specified; T26 must verify rendered results |
| H3/H5 — user control and error prevention | Self-only first Stop, explicit Stop all confirmation, retained questions and accepted withdrawal notices are coherent written decisions. Server scope and focused Esc are necessary T21 assertions |
| H9 — recovery from failure | Pending restart controls, named member-resume failures and visible question expiry are specified. The missing provenance/publication boundaries must not become silent fallback behavior |

## Questions for the founder

Existing founder rulings already settle the product-policy choices identified here. They do not need to be asked again. The remaining question is how to proceed after the authorized correction round has been used; this review does not authorize another correction or grill by itself.

**Context and impact.** The ADR is the train-3 build specification, but the verified gaps above remain after its correction. Building the conflicting text would make the implementation team choose policy or durability semantics on its own.

**Q1 — How should the remaining blockers be handled?** Answer shape: `Q1 A` or `Q1 B`.

| Option | Meaning |
|---|---|
| **A — Recommended** | Authorize a bounded exception for the architect to correct only these verified gaps under the existing founder rulings. Team-lead verifies the resulting evidence; do not automatically schedule another grill |
| B | Hold build-spec approval for a founder discussion/disposition of these findings before any further ADR edit |

Until that disposition, **escalate the remaining blocking findings to the founder**. Do not silently run another correction/review loop, treat the stale held-claim mechanism as approved, or edit the ADR as part of this report-only task.

## Delivery status

**Code correct and tested:** Not established — specification/source review only; T1–T26 have not been executed by this reviewer.

**Reachable by a user/agent:** Not established — no runtime or browser acceptance was performed.

Skills used: `grill-spec` (one invocation), `omnipus-shared-rules`, `gitnexus-exploring` (source-read fallback), `ux-heuristics-review`, `commit-messages`.

[adr]: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md
[round2]: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/docs/internal/architecture/ADR-20260928-sub-agent-control-plane-review-round2.md
[plan]: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/PLAN-2026-09-28.md
[as-is]: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/docs/internal/architecture/AS-IS-architecture.md
[plugins]: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/docs/internal/architecture/plugin-extensibility-assessment.md
[steered]: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/docs/internal/architecture/ADR-091-steered-sessions-replace-subagents.md
[open-conversation]: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/docs/internal/architecture/ADR-093-open-conversation-must-keep-delegation.md

## Evidence table

Filesystem paths below locate the inspected files in the worktree. Release-source claims refer to their **Git objects at the pinned release SHA**, not to an assumption that the worktree contains that revision. All source reads used for findings completed successfully; no static search is offered as a behavioral green.

| Claim | Evidence | Certainty |
|---|---|---|
| Reviewed artifact and release are pinned | `git rev-parse HEAD origin/release/v0.1.1`, exit 0: `9a4380295ca28d0270c208683bc93628f717a415`, `4f24f1e462fc1105848f2730a86d137ee330eb4b`; requested ADR bytes compared with `git show` of the first revision, exit 0, unchanged | Verified — high |
| **E-RULINGS:** founder rejected held claims and fixed Stop/resume/plan scope | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/PLAN-2026-09-28.md` :: “Founder model split 2026-09-29” Q2=B simplification; “Founder rulings — session states, stop model”; “landing trains”; Q26=A. Read through the requested range's end | Verified — high |
| **E-PUBLISH:** publication precedes the lifecycle commit and uses a generation-final identity | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/agent/steer_completion.go::AgentLoop.deliverSteeredCompletion`, `::AgentLoop.completionFinalAlreadyStored`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/agent/steer_audience.go::SteerUpwardDeliverer.Deliver`, `::deliverEntryIsAcked`. Pinned `git show` reads, exit 0: Deliver before Mutate; `<child>:<generation>:final`; acknowledged-ID short circuit | Verified source — high; resumed-result loss is Inferred — high, not reproduced |
| **E-BOOT:** final repair and additional goal-ending hooks exist | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered`, `::SteerBootRecovery.finishFromFinal`, `::SteerBootRecovery.failInterrupted`, `::PlanEngine.sweepToFailedInterrupted`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/gateway/gateway_boot.go` :: `EndSessionGoal` binding and `SetSteeredGoalEndHook` binding. `git show` reads at both stated baselines, exit 0: two recovery callbacks plus plan sweep hook | Verified — high |
| **E-Q2B:** simplified admission rejects immediately; current local branch includes waiting children | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/tools/goal_claim.go::GoalClaimTool.Execute`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/agent/goal_record_wiring.go::agentLoopGoalRecordAccess.DirectNonTerminalChildren` at `82a1e351a1ed5b28cd507970ca8abe35cd563195`, `git show` exit 0: rejection before claim acceptance and explicit `LifecycleNeedsInput` case. Older deferred function read at `4c693d186d4e94c892b9fca08de8e8097c0f6e3e`, exit 0 | Verified at pinned branches — high; author's unpinned earlier local snapshot Unknown |
| **E-OWNER:** persisted user transcript lacks the required authenticated author field | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/gateway/websocket_chat.go::wsHandlerHandleChatMessage.persistUserMessage`, `::wsHandlerHandleChatMessage.buildInboundMessage`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/session/daypartition.go::TranscriptEntry`. Full type covered in bounded reads: ID/text persist; GatewayUserID is on transient inbound, not that entry | Verified source — high; restart validation consequence Inferred — high |
| **E-PLAN-GOAL:** plan Stop has a separate task-owned goal-ending path | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/agent/plan_engine.go::PlanEngine.StopPlan`, `::PlanEngine.cancelMemberLocked`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/agent/task_goal_terminal.go::terminateTaskGoalRecord`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/tools/task.go::TerminateTaskGoalRecord`, `::GoalStateForTerminalTask`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/agent/goal_child_completion.go::AgentLoop.endSessionOwnedGoalOnTerminal`. Read chain maps failed + stopped_by_user to cleared for task ownership; session hook skips it | Verified source — high; partial-fix consequence Inferred — high |
| **E-LEDGER:** delivered steers have no finality under the literal rules | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md` :: D4 states/Retention and D5 pending/limits: no steer applied; pending excludes only applied/superseded; delivered blocks compaction; 256 KiB pending-text cap | Verified contradiction — high; cap exhaustion under literal implementation Inferred — high |
| **E-STOP-TEST:** completed is already a legal terminal Stop-race outcome | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/tests/adr091/steered_sessions_test.go::assertStopLandedOn`, `::TestE2E_StopSurvivesRestart`. `git show` at `e4d9a126c7e72a27238cd9f6de7ab55792139cc5` and `4f24f1e462fc1105848f2730a86d137ee330eb4b`, exit 0: `legalTerminal` accepts `LifecycleCancelled` or `LifecycleCompleted` | Verified — high; test not executed |
| **E-INSTRUMENT:** the interface seam does not wrap the real completion writer today | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/session/lifecycle_bridge.go::LifecycleMutator`, `::TransitionSession`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/agent/session_messaging_wire.go::AgentLoop.GetSessionLifecycleStore`; E-PUBLISH. Getter returns concrete `*session.LifecycleStore`; completion uses it directly | Verified — high |
| **E-SYMBOL:** disputed missing symbols were checked with positive controls | Pinned-source subprocess checks, exit 0: “positive Execute present; executeSetGoal absent”; “positive activeGoalForSession present; DirectNonTerminalChildren absent.” Files: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/tools/goal_claim.go` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/pkg/agent/goal_record_wiring.go` at the release SHA | Verified symbol inventory — high, not behavior |
| Issue scope and wire baseline were read | `gh issue view` for #1020/#1026/#1053/#984 with JSON, each exit 0; issue links in the Impact section. Schema reads: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/contracts/components/schemas/Session.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/contracts/components/schemas/CancelFrame.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/contracts/components/schemas/Plan.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/contracts/components/schemas/Task.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/contracts/components/schemas/GoalOutcome.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/contracts/asyncapi.yaml` | Verified source/scope — high; generated/runtime behavior untested |
| All acceptance/disposition IDs are present; inventory check can detect omissions | Static Python check, exit 0: “requested ADR bytes unchanged; 26/26 tests and 22/22 disposition IDs; missing-ID controls detected; no runtime test claim.” Removed T1 and CRIT-001 in memory and confirmed the inventory check rejected both; no source mutation | Verified inventory only — high |
| Runtime, frontend completeness and release certification remain unverified | No local suites, CI gates or browser acceptance executed by this reviewer. Frontend assessment limited to written interaction requirements and wire shapes. GitNexus unavailable; no graph check claimed | Unknown runtime result; explicitly not a green |
| **Self-check** | Reread the entire report in bounded sections against the requested verdict, four-part findings, all 26 tests, all 22 dispositions, build/Impact review, maximum four founder questions and evidence requirements. Static report checker exit 0: “8 findings; 26 test assessments; 22 dispositions; one founder question; tables and absolute links valid; negative controls detected; reviewed ADR unchanged.” `git diff --cached --name-status`, exit 0: only the requested review added. `git diff --cached --check`: first exit 2 caught Markdown trailing spaces; corrected run exit 0 with no output. No runtime test or reachability claim | Verified — high; document/source checks only |
