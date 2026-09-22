VERDICT: BLOCK
CRITICAL: 3  MAJOR: 19  MINOR: 2

Reviewed the nine documents as one delivery against `364290cb51cef7b8c900ac1c03cef68860799d20`, including the rev-2 report and all three prior audits. No files changed.

### [CRITICAL] F01 — The old message permission remains an explicit instruction
- **Where:** ADR D3, Q18/Q26; WP-B FR-B-009 and “Ambiguity warnings”; Appendix C C1.
- **Problem:** The new own-session restriction is correct, but the same specification still instructs implementers to retain the permissive existing behavior.
- **Evidence:** WP-B’s resolution says: “same rule as a task session today — `denyUnownedTarget`, unchanged. No new rule.” [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/message.go::denyUnownedTarget](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/message.go) allows shared, unbound webchat targets. Q18 also remains unreconciled with Q26.
- **Fix:** Replace the obsolete resolution and mark Q18’s earlier answer explicitly superseded. Retain FR-B-009 and its real-ownership positive and negative controls.

### [CRITICAL] F02 — Stop still permits stale work and root-session resurrection
- **Where:** ADR D2/D8; I-1/I-3/I-6; FR-D-002/004/009.
- **Problem:** The corrected cancellation model remains contradicted and incomplete. D2 increments generation on every re-entry; D8 later reserves against a root record it says does not exist. Queued work carries no expected generation: Stop generation `g`, revive as `g+1`, then dispatch an old wake—the current-record check permits it. A delayed cascade can likewise stop newly revived work.
- **Evidence:** I-3’s `WakeInput` and `Dispatch(ctx, sessionID)` lack generation identity. I-5 always wakes for interrupted children, but root chats have no durable Stop marker. [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/async_notifier.go::Notify](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/async_notifier.go) can re-enter those roots. Contrary to I-6’s “existing” duplicate rejection, [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/turn.go::registerActiveTurn](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/turn.go) performs a map `Store`.
- **Fix:** Publish one cancellation protocol covering expected generations, launch/cascade/revival ordering, registration ownership and durable root-wake suppression. Locate Stop storage for ordinary task records with nil `SteeredBy`. Test stale wakes after revival, revival during cascade, launch during cascade, and interruption notifications entering a stopped root.

### [CRITICAL] F03 — Losing a lifecycle record can restore a child’s user audience
- **Where:** I-8; I-5; FR-A-004, FR-B-001, FR-D-007.
- **Problem:** Classification assumes that absence of a lifecycle record proves an ordinary root. A child whose lifecycle record disappears while its session and transcript remain can consequently receive normal audience behavior.
- **Evidence:** I-8 states: “no lifecycle record at all (a root chat session)” → `ordinary_root`, with trusted inputs restricted to “the lifecycle store only.” That input cannot distinguish a genuine root, a damaged child, or a nonexistent ancestor.
- **Fix:** Verify session existence/type together with lifecycle presence. Missing lifecycle data required by a child must refuse execution and publication. Add controls for a genuine root, deleted child record, legacy child without a record, and nonexistent ancestor.

### [MAJOR] F04 — The upward-delivery interface still cannot be consumed as published
- **Where:** I-5; CP-0; WP-B/WP-C integration boundaries.
- **Problem:** I-2 fixes the launcher’s package dependency, but I-5 recreates it. Tools must call an operation published only in `pkg/agent`. Its return signature is also contradictory, and its audience input cannot carry classification errors.
- **Evidence:** I-5 declares `DeliverUpward(ctx, UpwardEvent) error`, then says it returns `Delivery`. The existing [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/message_parent.go::MessageParentWaker](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/message_parent.go) resides on the tools side specifically to avoid importing `pkg/agent`.
- **Fix:** Publish the injected interface, concrete payload/result/error types, classification input, nil semantics and wiring owner. Define how authenticated human identity enters the authority check; `{Kind, ID}` alone is not authentication. CP-0 must compile actual tool consumers.

### [MAJOR] F05 — The proposed upward event is not the existing inbox entry
- **Where:** I-5; FR-B-002/006/010; WP-E contract manifest.
- **Problem:** New outcome kinds do not match the reused inbox contract. Existing reporting kinds are missing from I-5. Independent implementations cannot agree on serialization, wake eligibility or status rendering.
- **Evidence:** [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/message_inbox.go::Append](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/message_inbox.go) accepts `generated.SessionMessage`. Its contract has progress/checkpoint/artifact/blocker/question/error/handback and other variants—not I-5’s completed/parked/interrupted/timed-out/goal-verdict vocabulary.
- **Fix:** Publish an exhaustive mapping into existing message variants, including payload limits and oversized completion handling. If schema additions are necessary, assign them explicitly to WP-E. Do not introduce a second overlapping message vocabulary without its mapping.

### [MAJOR] F06 — Required edits still fall outside ownership and interfaces
- **Where:** Landing order §3, CP-0; FR-B-010/011; FR-D-008.
- **Problem:** The seven-agent model still requires undocumented coordination and edits outside assigned files.
- **Evidence:** Required changes include:
  - Inbox admission in [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/message_inbox.go::Append](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/message_inbox.go), which has no owner.
  - Live child-ID forwarding and deleted-field readers in [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/websocket_forward.go::onSubTurnSpawn](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/websocket_forward.go), also unassigned.
  - WP-D’s unreadable-index behavior in A-owned lifecycle files.
  - WP-D reservation and A-owned registration sharing an unspecified lock.
- **Fix:** Assign these files and publish the index-error, lifecycle-notification and reservation/registration interfaces. Name the owner of each integration test and the replacement check for permissive CP-0 stubs.

### [MAJOR] F07 — Delayed tasks still lose the creating session
- **Where:** ADR D1; I-2; FR-A-010; FR-C-005.
- **Problem:** The task specification states the desired parentage but does not preserve the information required to implement it. It also leaves creation timing inconsistent: creation-time launcher in D1 versus start-time launcher in FR-A-010.
- **Evidence:** [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/task.go::taskCreateToolExecute.buildTask](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/task.go) does not persist the creating session. The existing task `OriginSessionID` is documented for scratchpad cards. FR-A-010 says `Launch` when empty and `Dispatch` otherwise, without explicitly dispatching the newly launched branch.
- **Fix:** Specify one creation/start sequence, durable creator-session provenance, workspace/ownership inputs for ordinary-root tasks, and behavior for recurring or already-completed task sessions. Assign the required model changes and test delayed start after restart.

### [MAJOR] F08 — Return-before-start has no enforceable ordering interface
- **Where:** Q20; I-2/I-3; FR-C-001; test `TestDelegateRun_ReturnsBeforeChildStarts`.
- **Problem:** C must call launch and dispatch, yet the child must not start until C’s tool call has returned. Neither interface communicates that return boundary. An asynchronously scheduled dispatch can run before the caller returns.
- **Evidence:** I-2 exposes only `Launch` and `Dispatch`; I-3 also admits queued sessions whenever capacity frees. Neither contains a readiness acknowledgement tied to the tool result.
- **Fix:** Publish the result-commit/start ordering mechanism and its owner. Queued sessions must also remain ineligible until that boundary. The named test must observe real tool execution and registration, not a mock dispatch held behind a test-only barrier.

### [MAJOR] F09 — A consumed message ID does not establish the claimed crash guarantee
- **Where:** ADR D3/§7; I-3/I-5; FR-B-002; B-4.
- **Problem:** “Consumed” remains undefined relative to parent execution. Recording consumption first can lose work after a crash; recording it afterward can repeat work. Retrying `DeliverUpward` also lacks a specified stable identity source.
- **Evidence:** I-3 skips a turn if its ID is already recorded, but provides no pending/processing/completed boundary. Inbox deduplication only recognizes an already supplied identical ID. Recovery between persisted child outcome and inbox append is asserted as “re-runs the child’s finish,” without a recovery rule. The dedupe description also centers on steered recipients, although a direct parent is commonly an ordinary root.
- **Fix:** Define stable event identity, durable processing states and the precise guarantee. Cover ordinary-root recipients and crashes around outcome persistence, inbox append, consumption marking and parent execution. Do not promise exactly-once arbitrary tool effects through transcript deduplication.

### [MAJOR] F10 — Completion can remain suppressed until an unrelated restart
- **Where:** ADR D3; I-5; FR-B-010; WP-B test 10d, B-5 and ambiguity resolutions.
- **Problem:** Terminal events “always wake,” but suppression by debounce/hourly limits is explicitly retained with retry only at boot. Progress simultaneously must never wake and must produce a coalesced wake.
- **Evidence:** B-5 requires “1 coalesced wake”; the ambiguity table still accepts 500 ms progress coalescing. [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/async_notifier.go::allowWake](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/async_notifier.go) enforces existing suppression without a live retry mechanism.
- **Fix:** Make terminal events bypass suppression or guarantee live draining/retry. Specify delivery while the parent already has a turn. Remove progress-wake requirements and test closely spaced sibling completions and hourly-cap overflow without restarting.

### [MAJOR] F11 — Completion and concurrency can deadlock each other
- **Where:** D6/D9; I-5 outcome table; FR-A-011; FR-C-008; WP-C completion scenario.
- **Problem:** A waiting parent remains `running`, but no rule releases its execution capacity. At cap one, A queues B and waits for B while retaining the only slot. Treating queued descendants as quiet instead reports completion prematurely. Parked descendants are similarly unspecified.
- **Evidence:** I-5 keeps an answered parent `running` until its subtree is quiet. D9 counts steered sessions and tasks together. WP-C’s BDD additionally expects literal states `empty`, `parked`, and `answered_children_running`, contradicting the newly published persisted-state mapping.
- **Fix:** Define subtree quietness and execution-slot lifetime separately. Specify queued/parked descendants, last-child completion and restart behavior. Rewrite the BDD state vocabulary and test nested delegation at cap one through real persistence.

### [MAJOR] F12 — Status replay lacks a durable association for both launch fronts
- **Where:** I-1/I-2/I-4; ADR D7; FR-B-006/011; FR-E-004.
- **Problem:** Status frames require a span identity, but launch interfaces carry neither the originating call/span ID nor a durable lookup contract. Task-created children have no specified start-span mechanism. Adding the child ID to a delegate result fixes only one direction of one launch path.
- **Evidence:** [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/replay.go::buildSubagentStart](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/replay.go) derives the span from a tool-call ID; surrounding replay logic recognizes `spawn`/`delegate`, not `create_task`. Start/end frames are reconstructed from tool calls today, contrary to D7’s claim that they are already persisted as equivalent frame events.
- **Fix:** Publish correlation, persistence format and start/state/end ordering for delegate and task origins, queued launches, revival and restart. Test actual task creation through status display, reload and opening the child.

### [MAJOR] F13 — The pill still has two incompatible counts
- **Where:** WP-E US-2, BDD “pill counts all,” machine constraints, FR-E-005/E-3.
- **Problem:** Viewing B in R → B → C requires both two running sessions and one direct child. The claimed unchanged selector also counts shell jobs, which FR-E-005 excludes.
- **Evidence:** The BDD says “the pill shows 2 running sessions.” [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/src/hooks/useRunningActivity.ts::useRunningActivity](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/src/hooks/useRunningActivity.ts) combines agent and shell activity before calculating `runningCount`.
- **Fix:** Use running direct agent children consistently throughout requirements and scenarios. Explicitly change the selector and test one running child alongside one running shell job.

### [MAJOR] F14 — The frame-field deletion manifest explicitly preserves obsolete fields
- **Where:** D7/I-4; WP-E contract changes.
- **Problem:** The delivery retires `producing_session_id`, but the manifest names only part of its contract surface and marks another containing schema unchanged.
- **Evidence:** [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/contracts/components/schemas/SubagentEndFrame.yaml](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/contracts/components/schemas/SubagentEndFrame.yaml) contains the field but is explicitly “unchanged” in WP-E. Other frame schemas and inline AsyncAPI copies also contain it.
- **Fix:** Inventory every obsolete field and handwritten reader. Explicitly delete them with their generated counterparts and inline copies. Add an absence guard scoped to the retired field.

### [MAJOR] F15 — Shared test fixtures remain unusable by existing tools-package tests
- **Where:** I-7; WP-G G-1/G-2 and acceptance criterion 1.
- **Problem:** The launcher injection removes one import cycle but creates another for in-package tools tests. The published fixture also lacks the runtime hooks its re-entry, crash and sink-control helpers require.
- **Evidence:** I-7 places a fixture accepting `tools.SessionLauncher` in agent test utilities. Existing delegate/message-parent tests declare `package tools`; importing that fixture creates `tools(test) → testutil → tools`. WP-G still advertises `DelegationTree(t, depth)`, omitting I-7’s launcher argument.
- **Fix:** Specify external-package integration tests or a neutral shared interface package. Publish runtime hooks for re-entry/reboot and recording all sinks. Make each package’s boundary controls usable without importing another package’s internals.

### [MAJOR] F16 — Zero-valued limits have contradictory meanings
- **Where:** D9; FR-A-008; dataset A-9.
- **Problem:** The timeout dataset rejects a value the decision explicitly accepts. The depth description also misstates current precedence when the global value is unset.
- **Evidence:** D9 says `Limits.TimeoutSeconds: 0 = default`; A-9 says zero is rejected. [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/delegation_depth.go::resolveEffectiveDelegationDepth](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/delegation_depth.go) returns an explicit edge depth when global depth is zero; it does not first substitute a global default and take the minimum.
- **Fix:** Publish one boundary table covering absent, zero, negative and positive values, with explicit per-edge precedence. Distinguish carried-over behavior from an intentional change.

### [MAJOR] F17 — The job collector is still both unchanged and changed
- **Where:** ADR D2; WP-C symbol table; FR-C-010; Appendix C M11.
- **Problem:** The correct task-origin exclusion is now required, but two older instructions directly contradict it.
- **Evidence:** ADR D2 says the collector “needs no change.” WP-C calls it “unchanged,” then separately says it “reads the edge.” [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/list_jobs_sources.go::collectSubagentRows](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/list_jobs_sources.go) currently lacks the required task-origin exclusion.
- **Fix:** Retain FR-C-010 and its before-limit duplicate test. Replace both conflicting rows with the precise filtering and durable-actionability changes.

### [MAJOR] F18 — A retained-unchanged test still constructs the deleted ring
- **Where:** WP-B regression requirements versus its Definition of Done; WP-G classification.
- **Problem:** The setup-only migration is correctly specified in two places but remains prohibited by another.
- **Evidence:** WP-B still says both containment files are “Preserved unchanged.” [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/async_child_publication_test.go::spawnPublicationTestChild](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/async_child_publication_test.go) constructs `ephemeralSessionStore`.
- **Fix:** Make every retention instruction preserve assertions and controls while assigning setup migration to WP-A. Do not preserve the production ring to satisfy the contradictory clause.

### [MAJOR] F19 — Several “exact checks” are neither executable nor valid oracles
- **Where:** WP-A/C/F/G machine constraints; FR-F-006.
- **Problem:** The main audit exclusions were repaired, but the delivery still contains checks that count legitimate negative tests, inspect live installation paths instead of fixtures, or require prose interpretation.
- **Evidence:** WP-F’s address check excludes tests only in a parenthetical; its containment command searches `ADR-091` followed by “marked temporary” prose. WP-A/C retain unrestricted banned-name searches. WP-F says every nonzero count fails although its runner check requires one match.
- **Fix:** Publish runnable checks with explicit scopes, per-check expected counts, fixture-provided paths and separate execution-error handling. Prove guards fail on the forbidden behavior; deleting a comment must not satisfy containment removal.

### [MAJOR] F20 — The post-merge closure check becomes a pre-merge CI dependency
- **Where:** WP-F US-3/FR-F-005; proposed issue-closure script.
- **Problem:** The proposed filename automatically enrolls a post-merge condition in pre-merge CI. It also lacks the required guard self-test.
- **Evidence:** [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/scripts/guards.sh](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/scripts/guards.sh) discovers every `check-*.sh`. WP-F requires issues closed only after the delivery merges, while this script requires them already closed.
- **Fix:** Name the operational verifier outside the guard-discovery pattern and assign its post-merge invocation. Keep fixture-tested validation separate from live tracker operations.

### [MAJOR] F21 — The closure verifier accepts unrelated comments
- **Where:** AC-13; WP-F closure script/FR-F-005.
- **Problem:** The script can pass without a citation to this delivery or the required explanation for an open issue.
- **Evidence:** Closed issues accept any historical comment matching `#[0-9]+|[0-9a-f]{7,40}`. Open issues require only `ADR-091`, not a section or reason. Comment retrieval is piped without preserving the API command’s exit status.
- **Fix:** Require the actual merging PR/commit and ADR section in the closure evidence. Require an explicit exclusion reason for #784/#803. Fail independently on API errors; test unrelated issue references, arbitrary hex strings and incomplete comments as negative cases.

### [MAJOR] F22 — Required acceptance claims can still pass without being exercised
- **Where:** AC-1/3/6/11/13; work-package traceability and TDD plans.
- **Problem:** Several important additions are only assertions or table entries, not tests that establish the claimed behavior.
- **Evidence:** AC-1 requires verification across restart, but no named launch test explicitly reopens stores. Completion maps mainly to a unit disposition table, which cannot establish persisted-state validation or last-descendant completion. Twelve boundary tests are now named, while AC-3, BDD examples and success criteria still specify nine or ten. D11 lacks an explicit mapping proving all three containments at depth three.
- **Fix:** Complete the traceability gaps below with real persistence, restart and boundary controls. Reconcile the boundary inventory once, and reference that inventory everywhere.

### [MINOR] F23 — Broadcast frames are incorrectly described as lacking session scope
- **Where:** WP-E FR-E-002, edge cases and “No fallback” constraint.
- **Problem:** The corrected scoped-frame rule still misclassifies approvals/questions and retains a blanket missing-ID rejection.
- **Evidence:** [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/src/store/chat/runtime-state.ts::SESSION_SCOPED_FRAME_TYPES](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/src/store/chat/runtime-state.ts) includes `tool_approval_required` and `ask_user_question`. Broadcasting to browsers does not remove their session identity.
- **Fix:** Preserve the existing approval/question identity handling, including nested question-card identity. Use genuinely global frames as missing-ID controls and qualify every rejection rule consistently.

### [MINOR] F24 — D11 describes proposed containment as already present
- **Where:** ADR D11 and evidence baseline.
- **Problem:** “Three live defects are contained now” is not true at the stated release tip.
- **Evidence:** [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop_run_turn_tools.go::deliverToolOutput](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop_run_turn_tools.go) still sends media based on its presence. [/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop_inbound.go::processSystemMessage](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/loop_inbound.go) still constructs `SendResponse: true` without the proposed root restoration.
- **Fix:** Label these as proposed interim changes, or cite their actual landing commits and update the baseline. Keep their already-assigned deletion owners.

## Rev-2 dispositions not actually applied

All 20 rows were checked. **Seventeen remain incomplete; M16, m1 and m2 are resolved.**

| Appendix C row | Exact remaining gap |
|---|---|
| C1 | Own-chat restriction contradicted by WP-B’s unchanged-permission resolution and Q18. F01. |
| C2 | Old generation/root-record rules survive; stale wakes and root recovery remain unprotected. F02. |
| M1 | I-5 dependency/signature unresolved; I-7 still creates a tools-test cycle and has inconsistent signatures. F04/F15. |
| M2 | Task creator-session persistence and creation/start sequencing remain unspecified. F07. |
| M3 | Lifecycle mapping exists, but C’s scenarios still require different states; inbox mapping and descendant outcomes remain undefined. F05/F11. |
| M4 | Consumption timing and stable retry identity remain undefined. F09. |
| M5 | Terminal suppression survives; progress-wake requirements remain. F10. |
| M6 | Transcript persistence is assigned, but durable span correlation and task-origin replay are missing. F12. |
| M7 | Direct-child rule conflicts with the two-session BDD and unchanged shell-inclusive count. F13. |
| M8 | Blanket missing-ID check and incorrect global-frame examples remain. F23. |
| M9 | Missing lifecycle records automatically become roots; older blanket no-edge migration clauses remain in A/D. F03. |
| M10 | Inbox/forwarding ownership and cross-owner index/registration interfaces remain missing. F06. |
| M11 | “Unchanged,” “reads the edge” and “needs no change” remain despite the new exclusion requirement. F17. |
| M12 | Zero timeout and unset-depth precedence remain contradictory. F16. |
| M13 | WP-B still requires the ring-instantiating file unchanged. F18. |
| M14 | Main grep fixes hold; remaining checks still include prose exclusions, invalid scopes and live-path placeholders. F19. |
| M15 | Several useful tests were added, but restart/persistence coverage and closure verification remain incomplete. F21/F22. |

## Cross-spec consistency matrix

Requirement references below use their `FR-` identifiers.

| Decision | Implementing specs | Consistent? |
|---|---|---|
| D1 — shared launcher, one memory | A-001/002/003/009/010; C-001 | **No:** task sequencing, provenance and unchanged-test conflict. |
| D2 — canonical edge and identity | A-004/005/006/012; C-003/007; I-8 | **No:** generation and missing-record classification. |
| D3 — audience and upward delivery | B-001–004/008–011; I-5 | **No:** contradictory permissions, missing message mapping and delivery guarantees. |
| D4 — remove wait-inline | C-002; E-007; F-001/003 | **Yes in decision coverage:** deletion checks still need repair. |
| D5 — steering authority/actions | C-003–007/011 | **No:** incomplete principal/interface and task-parentage contracts. |
| D6 — completion and optional goal | A-009; C-008/009/013 | **No:** outcome scenarios, quiet-subtree semantics and capacity release. |
| D7 — existing surfaces, own-session stream | B-005–008/011; E-002–010 | **No:** span correlation, pill, field deletions and frame classification. |
| D8 — durable Stop | A-007; D-001–010; I-6 | **No:** stale generations, root wakes and shared ordering. |
| D9 — one limit surface | A-008/011; C-012 | **No:** zero/default semantics and nested queue behavior. |
| D10 — named deletions | A; C-002/010; E-003/010; F-001–003/006 | **No:** incomplete field inventory, conflicting retention and audit checks. |
| D11 — contain, then remove | A-004; B-001; D-001/003; D10 manifest | **No:** current-state claim is wrong; depth-three acceptance mapping remains incomplete. |

## Traceability gaps

Every D1–D11 and AC-1–AC-13 has some nominal coverage. The remaining problem is incomplete or ineffective coverage, plus requirements without actual scenarios.

| Requirement | Missing proof or mapping |
|---|---|
| AC-1; Q15 | Explicit reopened-store/restart readback; named empty-label-and-task validation case. |
| AC-3/D3 | One consistent twelve-boundary inventory, including question-card handling and positive controls for each boundary. |
| AC-5/Q3 | Published trusted-human identity path; authority testing must not construct an arbitrary `Kind: human` and call that authentication. |
| AC-6 | Real persisted/generated-state validation; queued/parked descendants; completion following the final descendant. |
| AC-8/Q17 | Stale wake after revival, cascade versus revival/launch, and child notifications into a stopped root. |
| AC-11/D11 | Explicit mapping of all three containment properties at depth three, including their replacement behavior. |
| AC-13 | Delivery-specific citation verification and a post-merge execution phase. |
| FR-A-006/009/010/011/012 | Traceability references pruning, goal, origin and queue scenarios that are not corresponding WP-A BDD scenarios. A-010 also appears twice with different mappings. |
| FR-B-004/009/010/011 | Deleted-parent, own-chat permission, admission/suppression and persisted replay scenarios are incomplete or absent from BDD. |
| FR-C-004/006 | No corresponding arrival-order or external-limit BDD scenario despite named tests. |
| FR-D-002/006/008/009/010 | Missing BDD scenarios for reservation races, parked recovery, unreadable index, root without record and originating-channel notice. |
| WP-F/G | No complete requirement → acceptance → scenario → named-test mapping. F-005/006 are absent from its ordered test table. |

Issue closure is **planned, not yet earned**:

| Issue | Decision → acceptance → named test | Remaining blocker |
|---|---|---|
| #658 | D7 → E-009 → `ActivityPanel.awaitingApprovalLine.test.tsx` | Preserve actual broadcast/session handling; complete forwarding and field migration. |
| #614 | D5 → C-011 → `TestDelegateStatus_FromRecordNotStreaming` | Mapping is present, including no-message-yet behavior. |
| #670 | D8 → AC-8 → `TestCascade_ReachesReenteredChild`, queued-wake tests | F02 cancellation protocol. |
| #755 | D7 → B-011/E-004 → `TestStatusFrames_PersistedInParentTranscript_Replay` | F12 durable correlation and replay coverage. |
| #763 | D1/D7 → AC-1/7/12 → launcher, open-control and reachability tests | Task/launcher integration and live-frame ownership. |
| #764/#765 | D3 → AC-3 → boundary tests and three-level no-leak suite | F01/F03 audience defects and complete boundary controls. |
| #784 | §12 exclusion → AC-13/F-005 | Correctly remains open: parent-authored content quoting is separate. |
| #803 | §12 exclusion → AC-13/F-005 | Correctly remains open: context-limit/resumption policy is not decided here. |

## Claims I verified as correct

- The checkout matches the stated release tip. **122 unique extracted `file::symbol` pairs resolve textually**; this establishes existence, not every behavioral claim.
- The corrected inventories reproduce exactly: **24** Go files, **58** contract files, **18** narrow SPA files, **96** broad SPA files, **47** mechanism test files/**17,130** lines, and **15** production files referencing `ParentDurableKey`.
- **Appendix C M16 is complete:** the sibling-wait option is removed; deletion and its audit are explicit.
- **Appendix C m1 is complete:** 49-file classification arithmetic, eight-round header and #807’s decided-but-not-landed status are corrected.
- **Appendix C m2 is complete:** D8 now explains Stop marker, generation, reservation and pre-arm latch. I found no additional register defect warranting a separate finding.
- Task session creation and lifecycle minting are real reusable mechanisms. Both external-worker paths already use the same runner; preserving it is correct.
- Inbox append precedes wake; message-ID deduplication and `inbox_ack` already exist.
- Sidebar nesting, the side panel, single-session attach/replay and workspace-scoped approval presentation already exist.
- The status-frame contracts exist. FR-047’s prohibition guard exists, and its deletion now has an explicit owner and landing condition.
- The `wait` contract field versus Go tool `async` distinction is corrected.
- Parent-goal isolation now has an actual-input test; retention has an actual-pruning test; Q20 has an ordered-event test; Q21 has an originating-channel test.
- Classification at CP-0 follows the founder’s decision. The unfinished classification appendix is **not itself a new finding**.
- #614’s status requirement and #784/#803’s exclusions are explicit. The founder’s chosen explicit-progress status mechanism does not require reintroducing child prose streaming.

## Claims I could not verify

- No implementation or acceptance tests were executed; the proposed behavior is not delivered by these documents.
- The historical “80 of 80” live-session measurement, current GitHub issue states and historical founder approvals were not independently re-established.
- Atomic launch, durable cancellation, replay ordering and exactly-once logical consumption remain unproven implementation claims.
- GitNexus tools were unavailable; verification used direct source inspection.
- Several audit commands are not runnable as specified, so no green result is claimed for them.