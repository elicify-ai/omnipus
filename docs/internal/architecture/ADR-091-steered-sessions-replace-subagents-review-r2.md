VERDICT: BLOCK
CRITICAL: 2  MAJOR: 16  MINOR: 2

Reviewed against `364290cb51cef7b8c900ac1c03cef68860799d20`. No files changed. Findings below distinguish incomplete prior fixes from newly identified defects.

### [CRITICAL] The unchanged message tool bypasses the audience rule
- **Where:** ADR D3/Q18; WP-B FR-B-001; I-5.
- **Problem:** Giving a child its own address does not prevent it from explicitly sending to the root chat or an external user. The specification’s asserted protection is false.
- **Evidence:** [message.go::denyUnownedTarget](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/message.go) permits unbound channels, explicitly including shared `webchat`. It also permits any recipient on a channel owned by the acting agent. A child can therefore request `send_message(channel="webchat", chat_id=<root>)`. D3 nevertheless says this rule “contains it — no new rule.”
- **Fix:** Reconcile Q18 with D3 explicitly: preserve channel-ownership policy while applying the session-audience restriction at the common publication boundary, or explicitly narrow the no-publication mandate. Test with real ownership resolution; a nil ownership mock would reject the request and conceal this bypass. **The rev-1 audience-boundary disposition remains incomplete.**

### [CRITICAL] The cancellation interface cannot enforce its promised scope
- **Where:** I-1/I-6; ADR D8; WP-A US-3; WP-D FR-D-002/004.
- **Problem:** `ReserveDispatch(rootSessionID, generation)` cannot distinguish duplicate starts of B from independent starts of siblings B and C. It also cannot distinguish stopping middle node B from stopping ultimate root R. Incrementing generation on every automatic re-entry creates another escape from a generation-scoped Stop.
- **Evidence:** WP-D requires duplicate reservations for one session to admit exactly one, while allowing independent sessions. It also requires “Stop on a middle node B” to leave A and R untouched. I-1 increments generation on **every re-entry**. Existing [lifecycle.go::LifecycleRecord](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/lifecycle.go) has a session-local generation, not a shared cancellation generation.
- **Fix:** Publish target-session identity, cancellation-generation semantics, ancestor Stop checks, reservation-to-registration ordering and release behavior. Identify where Stop persists for ordinary chat roots, which need not have lifecycle records. Test concurrent siblings, duplicate starts, middle-node Stop, mismatched parent/child generations and automatic wakes after Stop. **The rev-1 pre-arm disposition is incomplete.**

### [MAJOR] Published interfaces cannot be consumed without unpublished dependencies
- **Where:** I-2/I-3/I-5/I-6/I-7; CP-0; WP-C FR-C-001; WP-G G-1.
- **Problem:** Tool consumers cannot directly import the proposed launcher from `pkg/agent`; `dispatchSteered` is also unexported. The shared fixture has the equivalent dependency problem. Required types and injection points remain unspecified.
- **Evidence:** [delegate.go::SubTurnSpawner](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/delegate.go) explicitly avoids an agent↔tools import cycle. In-package agent tests already import `testutil`, so making that package import the agent launcher creates another cycle. [gateway_harness.go::RegisterGatewayRunner](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/testutil/gateway_harness.go) demonstrates the existing injection solution. I-2 also omits label input despite label-only launch semantics; `LaunchResult`, `Payload` and `Principal` lack concrete definitions.
- **Fix:** Publish compiled shared types and injected interfaces before dependent work starts. Define title, authorization/depth, authenticated human identity, incoming wake content, errors and nil semantics. Give I-7 explicit launcher/runtime hooks.

### [MAJOR] Task adoption has no coherent creation-and-start sequence
- **Where:** ADR D1; I-2; WP-A FR-A-010; WP-C FR-C-005.
- **Problem:** “`create_task` = launcher + task record” conflicts with keeping `StartTaskNow` unchanged. Scheduled and human-created tasks also lack the mandatory steering-session input.
- **Evidence:** [task_executor.go::StartTaskNow](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_executor.go) returns without dispatch when `Task.SessionID` is already populated. Its creation block is separate from `createTaskSessionSync`. [task.go::Task](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/task/task.go) records creator-agent and parent-task identities, not the creating session required by I-2. FR-A-010 only says the task front **SHOULD** use the launcher.
- **Fix:** Specify when task sessions are created and how an already-created session starts. Define parentage for delayed, recurring, plan-owned and human-created tasks. Make reuse mandatory and test these actual executor paths. **The rev-1 launcher/`StartTaskNow` fix resolves delegation’s misuse but leaves the task side inconsistent.**

### [MAJOR] Completion outcomes do not fit the persistence and delivery contracts
- **Where:** WP-C US-4/FR-C-008; I-5; WP-E contract table.
- **Problem:** `empty`, `parked` and `answered_children_running` are required as distinct states without a mapping to the persisted lifecycle. I-5 cannot report the last outcome at all.
- **Evidence:** [lifecycle.go::LifecycleState](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/lifecycle.go) and [SubagentStateFrame.yaml](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/contracts/components/schemas/SubagentStateFrame.yaml) allow only the existing eight states. Persistence rejects other values. I-5’s exhaustive event-kind list omits `answered_children_running`.
- **Fix:** Publish the mapping from completion outcome to persisted state, terminality, upward message and displayed status. Define what happens when remaining descendants subsequently finish, including parked and queued descendants. Test real persistence and generated-contract validation, not only a disposition function.

### [MAJOR] Inbox deduplication does not establish exactly-once wake delivery
- **Where:** I-5; WP-B FR-B-002, B-4, SC-B-2; ADR §7.
- **Problem:** The specified mechanism cannot guarantee “never 2” wakes after a crash between recipient processing and acknowledgement.
- **Evidence:** [message_inbox.go::Append](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/message_inbox.go) deduplicates stored messages. [async_notifier.go::Notify](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/async_notifier.go) passes no message identifier into the inbound turn. I-5’s event has neither a stable delivery ID nor generation. An unacknowledged entry does not reveal whether its wake was already consumed.
- **Fix:** Define stable delivery identity and durable recipient-consumption semantics. Distinguish one logical consumption from one physical wake. Test a crash **after consumption, before acknowledgement**; ack-then-retry is insufficient. **The rev-1 restart disposition remains incomplete.**

### [MAJOR] Existing inbox and wake limits can suppress completion
- **Where:** I-5; WP-B FR-B-002/003; progress-storm case; Appendix B round 5.
- **Problem:** Progress can exhaust the capacity needed to persist completion. Existing wake suppression also contradicts per-child notification.
- **Evidence:** [message_inbox.go::Append](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/session/message_inbox.go) applies the default 200-unacknowledged-entry cap and 10-message/minute limit before persistence, regardless of kind. [async_notifier.go::WakeParent](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/async_notifier.go) rejects progress and suppresses wakes within 15 seconds or above its hourly cap, returning success for suppression.
- **Fix:** Specify reliable terminal-event admission or durable retry, and distinguish suppressed from delivered wakes. Define the 500-ms progress coalescing behavior. Test completion after full/rate-limited inboxes and multiple children completing close together.

### [MAJOR] Persistent status lines have no server replay contract
- **Where:** ADR D7/Q24; WP-B server-side table; WP-E US-2 AS-7; I-4.
- **Problem:** The new persistence decision is not implemented by the replay path the spec explicitly leaves unchanged.
- **Evidence:** [websocket_replay.go::loadReplay](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/websocket_replay.go) reads the session transcript, not the inbox or lifecycle store. [replay.go::buildSubagentStart](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/replay.go) derives span identity from a tool-call ID; I-1 supplies no corresponding association. Acknowledged inbox messages can also be compacted away.
- **Fix:** Assign reconstruction of status frames from the existing stores, including child-to-span association, retention, cursor and ordering rules. Test restart and reload from an empty browser store after acknowledgement and compaction. **This is an incomplete fix to review-3’s #755 persistence finding; a reducer test with fabricated replay frames does not close it.**

### [MAJOR] The unchanged pill cannot count running grandchildren
- **Where:** ADR D7/AC-7; WP-E FR-E-005, E-3.
- **Problem:** Direct-parent events plus single-session attachment do not give the existing pill the required tree-wide count.
- **Evidence:** [useRunningActivity.ts::useRunningActivity](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/src/hooks/useRunningActivity.ts) counts active-session spans and background shell activity. E-3 requires two running sessions for R→B→C while viewing R, but C’s status is delivered only to B.
- **Fix:** Define the count’s scope and its authoritative existing session-query source. Distinguish agent sessions from shell jobs. Test from an empty store with the grandchild never opened; do not introduce multi-session subscriptions.

### [MAJOR] The missing-session-ID rule would discard legitimate global frames
- **Where:** WP-E FR-E-002; missing-ID scenario and constraints.
- **Problem:** WP-E broadens I-4’s rule from session-scoped frames to **every frame**.
- **Evidence:** [runtime-state.ts::SESSION_SCOPED_FRAME_TYPES](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/src/store/chat/runtime-state.ts) intentionally excludes global events. [frames.ts::createFrameSlice](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/src/store/chat/slices/frames.ts) handles legitimate events without a session ID, including global task/plan updates.
- **Fix:** Restrict bucketing and missing-ID rejection to session-scoped frames. Include valid global events as preservation controls.

### [MAJOR] Missing edges ambiguously mean either ordinary session or forbidden legacy child
- **Where:** I-1/I-3/I-5; ADR §6; WP-B edge cases; WP-D FR-D-007.
- **Problem:** WP-B grants normal audience behavior when the edge is nil; migration forbids old delegated sessions with nil edges from running. WP-D’s blanket no-edge rule can also encompass ordinary task or root records.
- **Evidence:** WP-B says “edge nil … it is not steered; normal audience rules.” WP-D’s machine check says “record without edge → state failed.” I-5 accepts only a lifecycle-record pointer and defines no classification of ordinary, missing, legacy or corrupt records.
- **Fix:** Publish an exhaustive classification table and its trusted inputs. Cover ordinary roots without lifecycle records, valid children, legacy delegates, ordinary task/plan records, unreadable records and invalid ancestors. Specify operator reporting when no valid edge exists for upward delivery.

### [MAJOR] Ownership and checkpoints still require coordination outside the published interfaces
- **Where:** Landing order §3/CP-0..CP-4; WP-A/B/D/E/F.
- **Problem:** Several required edits have no adequate owner/interface, and dependent packages start before their complete contracts are published.
- **Evidence:** WP-D requires changes to the production boot hook, but [gateway_boot.go](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/gateway/gateway_boot.go) is absent from landing ownership. WP-B retires payload fields in [events.go](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/events.go), also absent. WP-F’s one-caller consolidation requires the task-side wrapper owned nowhere in its row. CP-1 requires B/C/D migrations before CP-2/CP-3 publish I-5/I-6. Only one test mentions an I-6 stub.
- **Fix:** Assign these files and cross-owner lifecycle hooks explicitly. Publish I-3/I-5/I-6 and necessary schemas before dependent implementation. Specify integration order without an alias period. WP-E must enumerate the lifecycle-edge and partial-Stop wire changes, not just the listed “small” contract changes. **The rev-1 ownership disposition is incomplete.**

### [MAJOR] Keeping the job collector unchanged creates duplicate task rows
- **Where:** ADR D10; WP-A parent-agent stamping; WP-C US-5/FR-C-010.
- **Problem:** Stamping `ParentAgentID` on task-backed sessions makes them eligible for both collectors. The claimed single row does not follow.
- **Evidence:** [list_jobs_sources.go::collectTaskRows / collectSubagentRows](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/list_jobs_sources.go) produce separate task and subagent rows. [list_jobs.go::ListJobsTool.collect](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/tools/list_jobs.go) concatenates them without cross-kind deduplication. WP-C also retains conflicting “unchanged” and “reads the edge” instructions.
- **Fix:** Define task-origin exclusion or canonical task/session deduplication, including filtering before result limits. Remove the contradictory row and test one actionable row after restart. **The fix to reuse-review finding 2 is incomplete.**

### [MAJOR] The surviving configuration remains unnamed
- **Where:** ADR D9/D10; WP-A FR-A-008; Appendix A.
- **Problem:** Only concurrency has an actual replacement configuration path. Depth, default timeout and concurrency-wait paths, defaults and boundary semantics remain undefined.
- **Evidence:** [subturn.go::getSubTurnConfig](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/subturn.go) consumes fields in the block being deleted. D9 replaces these with “one global ceiling,” “one config key” and “the single surface.” [delegation_depth.go::resolveEffectiveDelegationDepth](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/delegation_depth.go) currently gives zero a specific unset meaning.
- **Fix:** Name each replacement path, type, unit, default, zero/unset interpretation and rejection boundary. Test both launch fronts. **The rev-1 D9 disposition is not complete.**

### [MAJOR] A test required to remain unchanged instantiates the deleted ring
- **Where:** WP-B regression requirements/Definition of Done; WP-G classification; ADR D1/D10.
- **Problem:** These requirements cannot all hold simultaneously.
- **Evidence:** [async_child_publication_test.go::spawnPublicationTestChild](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/async_child_publication_test.go) constructs `&ephemeralSessionStore{}`. WP-B and WP-G require the file retained **unmodified**, while D1/D10 delete that production type.
- **Fix:** Preserve the assertions and controls while explicitly updating the setup to the persisted-store fixture. Name the test owner. Do not retain the ring to satisfy the unchanged-file requirement.

### [MAJOR] The specified audit commands reject legitimate enforcement tests
- **Where:** WP-F machine constraints; `TestExternalRunner_SingleCaller`; FR-F-002.
- **Problem:** The “exact” one-caller check counts test calls, so correct production consolidation cannot make it pass.
- **Evidence:** Its command excludes only the runner-definition file. At the baseline it finds **25 calls: two production and 23 test calls**, including [external_dispatch_test.go](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/external_dispatch_test.go) and [runner_egress_test.go](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/runner_egress_test.go). Other zero-match checks include negative fixtures and their own guard literals.
- **Fix:** Count production call expressions or exclude test files. Define expected counts per check, distinguish grep’s no-match exit from execution failure, and keep negative-test/guard fixtures outside the production scan. Replace live home-directory placeholders with fixture paths in executable tests.

### [MAJOR] Mandatory decisions can remain false while the mapped tests pass
- **Where:** ADR AC-1/3/6/8/13; WP-A/C/D/E/F/G traceability.
- **Problem:** Several requirements lack implementing requirements or tests that exercise the claimed behavior.
- **Evidence:** Parent-goal noninheritance appears in D6/AC-6 but nowhere in the seven implementing specs. FR-A-006 maps ancestor retention to `TestReconstruct_RefusesInvalidEdge`, which never needs to invoke pruning. Q20 requires return **before child start**, but C’s scenario checks only return **before child finishes**. Boundary coverage alternately counts nine, ten and eleven cases, while D3 lists twelve.
- **Fix:** Add the missing mappings listed below. Require actual invocation of every publication boundary with corresponding controls; one tool call cannot prove all boundaries were exercised. Test actual model input for goal isolation, actual pruning for retention and an ordered start/return event sequence for Q20. **The rev-1 false-green disposition remains incomplete.**

### [MAJOR] Per-child wake is weakened by an unrequested sibling-wait option
- **Where:** ADR D3; WP-B FR-B-003; Appendix B round 2.
- **Problem:** D3 retains the replaced all-siblings mechanism as “a task-board option, off by default.” This contradicts unconditional per-child wake and introduces configuration without an interface, owner or test.
- **Evidence:** The existing mechanism is [task_executor_judge.go::notifyParentIfAllSiblingsDone](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/task_executor_judge.go). The founder decision says wake per child; the delivery mandate says delete replaced mechanisms rather than retain them behind options.
- **Fix:** Remove this option and make per-child delivery unconditional. Any distinct future board-summary behavior requires a separate decision.

### [MINOR] Corrected inventories and baseline facts still contain contradictions
- **Where:** WP-G appendix; ADR D9/status header; WP-B edge cases/holdouts.
- **Problem:** Several factual and editorial remnants undermine the claimed completed reconciliation.
- **Evidence:** The 47-file mechanism set excludes two prefilled containment-test rows; their union has 49 files, so “remaining 44” is wrong. D9 says #807 “removed” the absolute cap, but [midturn_budget.go::absoluteShareTokens](/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2-wt-judge-godmode/pkg/agent/midturn_budget.go) still implements it at the stated baseline. The header says three decision rounds; Appendix B has seven. WP-B still references “Jim’s nested view.”
- **Fix:** Correct membership arithmetic and distinguish an adjacent decision from a landed baseline change. Remove obsolete nested-view wording. Q25 legitimately defers classification to CP-0; completing it before this review is **not** requested.

### [MINOR] Core explanations still require specialist vocabulary
- **Where:** I-3/I-6; ADR D8; WP-B/G.
- **Problem:** Terms central to founder decisions remain unexplained: “pre-arm latch,” “reservation,” “generation,” “shadow stream,” “ack/dedupe” and “vacuity control.”
- **Evidence:** “Ordering … is atomic on the root’s record” does not explain which action wins or what the user will observe.
- **Fix:** Define each term briefly on first use. For example: “Stop records a durable cancellation before any queued child can start; only a newer instruction permits it to run again.” Keep implementation names in supporting tables.

## Rev-1 dispositions not actually applied

| Appendix A row | Remaining defect |
|---|---|
| C3: upward wakes establish parent identity | Identity correction is stated, but delivery guarantees lack stable identity, receipt semantics and limit handling. |
| D5 cannot call `StartTaskNow` | Delegate-specific misuse is resolved; task-side create/start sequencing remains inconsistent. |
| Audience lacks identity at boundaries | Direct-message protection is factually false; the publication-origin interface remains incomplete. |
| Derived root preserves pre-arm latch | I-6 cannot express session/subtree scope or safe generation handling. |
| Durable cascade integrity | Retention has no effective test; ordinary-root cancellation storage and missing-edge classification remain undefined. |
| D6 completion prerequisites | Required outcomes do not map to persisted states and delivery kinds. |
| Restart can strand the parent | Exactly-once wake, terminal admission and legacy/no-edge reporting remain incomplete. |
| D9 surviving limits | Replacement keys and complete validation semantics are still absent. |
| `list_jobs` old-index dependency | Conflicting instructions remain; task-backed rows duplicate without an explicit rule. |
| ACs permit false greens | Missing and ineffective mappings remain, listed below. |
| Work packages overlap/omit files | Required files, cross-owner hooks and interface publication checkpoints remain incomplete. |
| Blast-radius figures | Main mechanism count is corrected; WP-G’s classification denominator is inconsistent. |

The other rev-1 corrections are materially reflected in the text, including self-target permission, ancestor authority, accepted operator visibility, external-worker limits, walked-root restoration and preservation of the shared external runner.

## Cross-spec consistency matrix

| Decision | Implementing requirements | Consistent? |
|---|---|---|
| D1 — launcher/one memory | A-001/002/003/009/010; C-001 | **No:** task adoption and unchanged-test contradiction. |
| D2 — canonical edge/identity | A-004/005/006; C-003/007 | **No:** generation, missing-edge and retention gaps. |
| D3 — audience/upward delivery | B-001/002/003/004/008; I-5 | **No:** direct-message bypass, delivery and limit defects. |
| D4 — remove wait-inline | C-002; E-007; A internal-Async deletion | **Yes in decision coverage:** audit implementation needs correction. |
| D5 — steering authority/actions | C-003/004/005/006/011 | **No:** principal/interface and task-adoption details missing. |
| D6 — completion/optional goal | A-009; C-008/009 | **No:** outcome mapping and parent-goal isolation absent. |
| D7 — existing UI/own-session stream | B-005/006/007; E-002–010 | **No:** replay, pill and global-frame contradictions. |
| D8 — durable Stop | A-007; D-001–008 | **No:** reservation scope and durable-generation model incomplete. |
| D9 — one limit surface | A-008 | **No:** surviving configuration undefined. |
| D10 — deletions | A ring/config removal; C-002/010; E-003/010; F-001–003 | **No:** retained option, test contradiction and incorrect audit checks. |
| D11 — containment then removal | A reconstruction; B-001; D-001/003; D10 deletion list | **No:** removal is assigned, but unchanged tests conflict and no distinct FR proves all three depth-three containments. |

## Traceability gaps

These distinguish missing coverage from merely missing table entries.

| Requirement/decision | Gap |
|---|---|
| AC-1 | Named launcher tests do not explicitly establish readback after restarting/reopening stores. |
| AC-3 / D3 | Twelve boundary rows versus nine/ten/eleven test inventories; question-card relay/rejection is not represented in the containment outline. |
| AC-6 / Q13 | No work-package FR, story, scenario or named test for absence of inherited parent goal **and goal context**. |
| AC-8 / Q17 | `TestBoot_StoppedStaysStoppedUnlessNewerInstruction` is named only in ambiguity prose; absent from the ordered TDD plan and FR mapping. |
| Q20 | “Before worker finishes” does not prove “before worker starts.” |
| Q21 | Originating-channel partial-Stop notice has no story, FR, scenario or named test; D8 still mentions only logs/UI. |
| AC-13 | WP-F US-3 and CP-6 assign closure, but no FR or named verification checks issue state and citation comments. |
| FR-A-006 | Invalid-edge reconstruction is not a retention test. |
| FR-A-009/010 | Traceability names goal/origin scenarios that are not actual BDD scenarios. |
| FR-B-004 | Deleted-parent handling has a dataset reference but no corresponding BDD scenario. |
| FR-C-004 | Arrival-order proof is assigned to the authority-matrix test without a defined ordering scenario. |
| FR-C-011 | Story and named test exist; no BDD scenario. |
| FR-E-008 | No story; only contract-diff review, which does not prove absence of a new panel/composer. |
| FR-E-009/010 | Tests exist, but both requirements are omitted from the traceability table and lack BDD scenarios. |
| WP-F/G | No complete requirement→story/acceptance→scenario→named-test mapping. |
| Q25 | Classification timing is stated correctly in WP-G, but the landing-order CP-0 gate omits classification explicitly. |

Issue-closure trace:

| Issue | Decision → acceptance → named test | Closure assessment |
|---|---|---|
| #658 | D7 → E US-2 AS-6/FR-E-009 → `ActivityPanel.awaitingApprovalLine.test.tsx`; B frame-identity test | Coverage now exists; preserve the verified workspace-wide approval route. |
| #614 | D5 → C US-6/FR-C-011 → `TestDelegateStatus_FromRecordNotStreaming` | Trace exists; define the no-message-yet case instead of fabricating a timestamp. |
| #670 | D8 → AC-8 → `TestCascade_ReachesReenteredChild`, queued-wake/registration tests | Blocked by I-6’s scope/generation defects. |
| #755 | D7 → E US-2 AS-7 → `frames.reloadRebuildsRow.test.ts` | Backend persistence/replay proof missing. |
| #763 | D1/D7 → AC-1/7/12 → launcher/open/reachability tests | Trace exists; launcher and UI integration defects remain. |
| #764/#765 | D3 → AC-3 → boundary and three-level no-leak suites | Blocked by direct-message bypass and incomplete boundary controls. |
| #784/#803 | Explicit exclusions in §12; CP-6/F US-3 leave-open comments | Correctly remain open. |

## Claims I verified as correct

- The checkout matches the stated release-tip hash.
- All **114 unique extracted `file::symbol` citations** resolved textually to existing files/symbols. This is an existence check, not blanket behavioral endorsement.
- Task creation already supplies reusable session creation, metadata and lifecycle code.
- Both external-worker paths call the same runner; deleting that runner would be wrong.
- Inbox append precedes wake, and append-level message-ID deduplication exists.
- The existing ownership walk permits ancestors.
- Sidebar nesting and single-session attach/replay exist.
- The approval broadcast and workspace-scoped modal do not require opening the child chat.
- The two status-frame schemas exist; their missing runtime integration is real.
- FR-047 retirement, step-buffer deletion, the forged-target reader migration and D11 containment removals now have explicit assignments.
- #614 now has a decision, requirement and named test. #784/#803 exclusions remain explicit.

## Claims I could not verify

- No implementation or acceptance tests were executed; this was a read-only specification review.
- The live-instance “80 of 80” measurement, current GitHub issue states and claimed historical founder approvals were not independently re-established.
- Atomic launch, durable Stop, replay reconstruction and exactly-once logical consumption remain proposed behavior, not verified implementation.
- GitNexus tools were unavailable; verification used direct source inspection.
- Several founder decisions are present in the ADR but not enforceably carried into the implementing specifications, as listed above.