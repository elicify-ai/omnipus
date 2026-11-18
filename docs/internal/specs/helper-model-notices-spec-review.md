# Adversarial Review: Helper model notices

**Document reviewed**: `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec.md`

**Mode**: Spec

**Round**: Round 1 of 2

**Review date**: 2026-11-15

**Reviewed commit**: `ddc5578088006e955248a52ec749d7c6cf83a755`

**Release base**: `f408d81c746b94a30824537628ab7e03016a093b`

**Branch**: `landing/1537-spec`

**Verdict**: **REVISE**

## Executive Summary

**8 MAJOR findings, 6 MINOR findings and 3 observations; no CRITICAL finding.** Implementing only the addressing and browser changes described would leave notices missing after parent completion, restart replayed countdowns, omit timeout retries from the main chat, and leave some successful or finished helpers looking stuck. Frontend contracts, composition, accessibility, narrow-screen behavior and test oracles received the same scrutiny as backend routing and lifecycle recovery.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 8 |
| MINOR | 6 |
| OBSERVATION | 3 |
| **Total** | **17** |

### Scope and evidence limits

- This is a specification review, not an implementation gate. The base-to-reviewed-commit diff contains only the specification. The sole authorized output is this report; the spec and product source are unchanged.
- GitNexus is unavailable on this pod, as stated in the brief. Exploration used `rg` and direct reads; no graph-based impact analysis is claimed.
- The founder's option A is accepted input, not reopened here. Issue #1850 is the commissioned rider; #1851 remains outside this work. Read-only issue retrieval confirmed all three are open.
- The probe reports and recipe describe fake-model observations on `8f5a95ba7`, not acceptance of a changed implementation on this review's commit. Their limited coverage is retained; this review ran no build, test, browser, model call or contract generator.
- **Verified** below means a contradiction or mechanism was checked first-hand in the spec/source. **Inferred** means a prospective failure follows from those mechanisms but was not executed. No speculative failure is presented as a reproduced incident.
- No agent-managed object's editable fields change: these are server-authored, read-only notices. The spec's tool-parity exemption is applicable; no new tool or policy entry is needed.

---

## Findings

### CRITICAL Findings

None established.

### MAJOR Findings

#### [MAJ-001] A parent's completion clears the helper state needed for reload

- **Severity**: MAJOR.
- **Lens**: Inconsistency & ADR/AS-IS contradiction; Incompleteness; Testability & false-green risk.
- **Affected section**: Contract Changes, final paragraph; Integration Boundaries / Gateway to browser; FR-007, FR-008; UAT-H8.
- **Failure scenario**: A helper starts a 30-second retry while the main agent is still replying. The main agent finishes during that wait. A fresh tab then opens the main chat: the helper is still waiting, but its named notice is absent from the snapshot and cannot be recovered from history.
- **Evidence**: The spec says keeping the helper's `turn_id` means the main agent's turn-end frame cannot clear it. `/workspace/wt/1537-spec/pkg/gateway/ws_hub_projection.go::activeTurnProjection.update` handles `hubKindDone` with an unconditional `p.clear()`, not a turn-keyed deletion. `/workspace/wt/1537-spec/pkg/gateway/websocket_streamer.go::wsStreamerFinalize.publishDone` publishes that kind. `/workspace/wt/1537-spec/pkg/gateway/websocket_forward_hub.go::WSHandler.hubProviderRetry` and `/workspace/wt/1537-spec/pkg/gateway/websocket_forward_provider_status.go::WSHandler.hubProviderStatus` retain notices in that projection. FR-011 forbids saving them to transcripts.
- **Recommendation**: Put gateway projection lifetime explicitly in scope. Require main-turn completion to remove main-owned state without removing active helper state, and helper completion to remove only the appropriate helper execution's state. Add a real-hub test for **helper notice → main `done` → fresh snapshot**, plus main Stop/new-turn and another helper ending. The current test hierarchy has no gateway recovery unit/integration row; add one. An already-idle main followed by a notice does not test this ordering.
- **Certainty**: **Verified, high confidence** — the claimed existing turn isolation is contradicted by the unconditional clear. The future UI failure is inferred from reusing it unchanged, not reproduced.

#### [MAJ-002] Catch-up resets the receipt clock and restarts the wait

- **Severity**: MAJOR.
- **Lens**: Infeasibility; Inconsistency & ADR/AS-IS contradiction; Testability & false-green risk.
- **Affected section**: Edge Cases / late attach and reload; Integration Boundaries; FR-008; BDD “Reload during the wait”; UAT-H8.
- **Failure scenario**: A retry scheduled for 30 seconds is replayed to a fresh tab five seconds later. The tab shows approximately 30 seconds remaining, not 25. A late silence notice similarly understates how long the model has been silent.
- **Evidence**: `/workspace/wt/1537-spec/pkg/gateway/ws_hub_projection.go::projectionFrames` returns the saved notice bytes, including the original `sent_at`. `/workspace/wt/1537-spec/pkg/gateway/websocket_replay.go::wsHandlerHandleAttachSession.sendIncremental` also sends byte-identical journal frames. `/workspace/wt/1537-spec/src/store/chat/slices/provider-frames.ts::handleProviderFrame` assigns a new `receivedAt` on receipt. `/workspace/wt/1537-spec/src/components/chat/ProviderRetryIndicator.tsx::remainingRetrySeconds` computes `retry_at - (sent_at + client elapsed since receipt)`; `/workspace/wt/1537-spec/src/components/chat/ProviderWaitingIndicator.tsx::waitingElapsedSeconds` uses the same anchor. The “mid-wait attach” test in `/workspace/wt/1537-spec/src/components/chat/ProviderRetryIndicator.test.tsx::ProviderRetryIndicator — mid-wait attach and skew` supplies the **original** receipt time directly, bypassing the reducer behavior a fresh client actually encounters.
- **Recommendation**: Specify how a fresh attach obtains a current server-time anchor while preserving the original retry deadline and clock-skew tolerance. First evaluate the already-present `session_state.emitted_at`, emitted by `/workspace/wt/1537-spec/pkg/gateway/ws_tool_approval.go::WSHandler.sessionStateBytes`, rather than assuming a new snapshot field is required. Cover snapshot, incremental reconnect and navigation back to a chat, through the real reducer and renderer, for both destinations and both ticking notice types. At simulated second five, assert a bounded value around 25 seconds, then its decrement and zero clamp: **“at most 0:25” also accepts an incorrectly permanent 0:00**. State any permitted replay-time field differences separately from the live-copy equality rule.
- **Certainty**: **Verified, high confidence** — the arithmetic and receipt handling contradict reuse-as-is; no runtime test was run.

#### [MAJ-003] Timeout and empty-stream retries bypass the listed emitter

- **Severity**: MAJOR.
- **Lens**: Reachability; Incompleteness; Inconsistency & ADR/AS-IS contradiction.
- **Affected section**: Symbols Involved; Impact Assessment; FR-001, FR-002; Test Datasets; Regression Test Requirements.
- **Failure scenario**: A helper's provider fails to begin answering, or closes an empty stream. Its retry reaches its own chat but never gets the new named main-chat copy. All planned 429 tests can pass while this ordinary retry path remains unimplemented.
- **Evidence**: The impact table says the #1664 no-response retry emits through `providerRetryObserver`. `/workspace/wt/1537-spec/pkg/agent/loop_run_turn_response.go::agentLoopRunTurnResponseCallLLMWithRetries.retryTimeout` instead emits `EventKindLLMRetry`; `timeoutRetryPayload` already addresses the **transcript session**, not the root. `/workspace/wt/1537-spec/pkg/gateway/context_window_notice.go::WSHandler.hubLLMRetry` forwards timed `timeout` and `empty_reply` events through `hubProviderRetry`. Existing direct wire proofs are `/workspace/wt/1537-spec/pkg/gateway/provider_status_1664_test.go::TestIssue1664_B5_TimeoutRetryBecomesNoResponseRetryFrame` and `TestIssue1664_B5_EmptyReplyRetryBecomesEmptyReplyRetryFrame`.
- **Recommendation**: Correct the emitter inventory and define one two-destination rule for **all eligible provider-retry producers**, not just the fallback-chain observer. Add helper timeout and empty-stream scenarios asserting both session IDs, helper identity and distinct `no_response`/`empty_reply` wording. Keep context-window notices and other deliberately unforwarded loop-retry reasons outside the change. Correct the Overview's blanket current-behavior claim too: the probe establishes the 429/silence paths, not every retry producer.
- **Certainty**: **Verified, high confidence** — both independent emission paths were read.

#### [MAJ-004] The contract edit list omits the schemas consumed by generation

- **Severity**: MAJOR.
- **Lens**: Contract-first gaps; Reachability.
- **Affected section**: Contract Changes; Test Implementation Order; Ambiguity Warnings #1.
- **Failure scenario**: The backend developer follows the table: edits the two standalone schemas and only the AsyncAPI message descriptions. The generated WebSocket types and validators still lack helper identity, so typed consumers cannot use it or the browser rejects the new frame.
- **Evidence**: `/workspace/wt/1537-spec/contracts/asyncapi.yaml::components.schemas.ProviderRetryFrame` and `components.schemas.ProviderStatusFrame` are inline definitions, each saying to keep it synchronized with its standalone schema. `/workspace/wt/1537-spec/scripts/_gen-asyncapi-types.mjs::schemas` reads `doc.components.schemas` from AsyncAPI. The table specifies only **message descriptions** for that file and does not state the regeneration/atomic-artifact step required by `/workspace/wt/1537-spec/CLAUDE.md::Contract regeneration`.
- **Recommendation**: Name both inline schema definitions as change targets, alongside `/workspace/wt/1537-spec/contracts/components/schemas/ProviderRetryFrame.yaml` and `/workspace/wt/1537-spec/contracts/components/schemas/ProviderStatusFrame.yaml`. State the sequence: architect settles shape; backend edits all authoritative representations; runs `/workspace/wt/1537-spec/scripts/gen-contracts.sh`; commits generated artifacts with the contracts; consumers use generated types; contract verification checks drift. Specify the both-present-or-both-absent identifier invariant and malformed-frame behavior. List live-copy differences explicitly: destination `session_id`, helper identifiers and per-session `seq`. If a new type is chosen, update emitters, parser dispatch, state, recovery and all traceability—not merely FR-002 test names as Ambiguity Warning #1 currently suggests.
- **Certainty**: **Verified, high confidence** — generator input and edit-list mismatch checked directly.

#### [MAJ-005] An old tab rejects the supposedly backward-compatible copy

- **Severity**: MAJOR.
- **Lens**: Contract-first gaps; Inconsistency & ADR/AS-IS contradiction; UI states & journey gaps.
- **Affected section**: Edge Cases / “Old browser tab without this change”; Contract Changes; Ambiguity Warnings #1.
- **Failure scenario**: A user leaves the main chat open across a backend update. The old tab reconnects and receives helper notices with the new properties. It drops them rather than showing today's unnamed notice—even while the main agent is streaming—contrary to “no worse.”
- **Evidence**: `/workspace/wt/1537-spec/src/lib/api/generated/ws-schemas.ts::ProviderRetryFrame` and `ProviderStatusFrame` end in `.strict()`. `/workspace/wt/1537-spec/src/lib/ws.ts::_parseServerFrame` drops known-type validation failures. This matches **“additionalProperties Policy (Requests Closed, Responses Per-Case)”**, `/workspace/wt/1537-spec/docs/internal/architecture/ADR-014-additional-properties.md::Decision`. `/workspace/wt/1537-spec/src/hooks/useVersionCheck.ts::useVersionCheck` offers a refresh toast; it does not refresh the code or relax validation.
- **Recommendation**: Remove the false compatibility promise. Have the architect record the supported stale-tab behavior: refresh required, or an explicitly designed compatible delivery path if continuity is required. Include that behavior in acceptance and documentation. A newer validator accepting older frames does **not** prove an older validator accepts newer frames. Do not globally weaken schema validation to mask the discrepancy.
- **Certainty**: **Verified, high confidence** — existing validator and parser behavior are explicit.

#### [MAJ-006] Nested-helper removal lacks a recoverable root-addressed signal

- **Severity**: MAJOR.
- **Lens**: Incompleteness; Reachability; UI states & journey gaps.
- **Affected section**: Edge Cases / helper of a helper; Integration Boundaries; FR-006, FR-008; UAT-H7.
- **Failure scenario**: The root shows a grandchild's retry line. The root socket disconnects briefly. The grandchild finishes or is stopped while its immediate parent continues other work. On incremental reconnect the root can keep showing that grandchild as waiting, although it has ended.
- **Evidence**: `/workspace/wt/1537-spec/pkg/agent/steer_audience.go::deliverOwnerKey` resolves the direct steering session; `SteerUpwardDeliverer.Deliver` passes that owner to the lifecycle emitters. `/workspace/wt/1537-spec/pkg/agent/steer_frames.go::AgentLoop.deliverSubagentEnd` and `deliverSubagentState` address that **parent**, not the cascade root. `/workspace/wt/1537-spec/pkg/gateway/websocket_forward_hub.go::WSHandler.hubTurnEnd` forgets projection items but emits no clearing frame. The alternative helper-event broadcast is also insufficient for missed events: `/workspace/wt/1537-spec/pkg/gateway/helper_event_frame.go::WSHandler.hubHelperEvent` calls `hubBroadcastWithSequencedCopy`, which journals only in the event's destination hub; other tabs get unsequenced live copies. `sendIncremental` replays only the attached root's journal. UAT-H7 tests appearance, not removal or disconnection.
- **Recommendation**: Specify a root-scoped, catch-up-safe clearing/reconciliation rule keyed to the affected helper's current execution. Cover finish, failure and Stop while connected and while disconnected, then resume/follow-up in the same helper session so an old terminal fact cannot clear a new wait. Preserve the agreed “middle chat unchanged” scope. Do not synthesize a root `done`: that would end the wrong chat's UI state. This is the missing lifecycle mechanism, not a request to reopen nested-helper product scope.
- **Certainty**: **Inferred, high confidence** — the routing/replay mechanisms and missing specification rule are verified; the prospective named-line implementation does not yet exist.

#### [MAJ-007] Successful fallback can leave the main chat saying “Retrying now”

- **Severity**: MAJOR.
- **Lens**: Incompleteness; UI states & journey gaps; Testability & false-green risk.
- **Affected section**: FR-005, FR-006; Behavioral Contract; Non-Behaviors / fallback unchanged; Test Datasets.
- **Failure scenario**: A helper's first candidate receives repeated short 429s; its fallback answers on its first attempt and starts a long tool operation. The helper chat clears the retry line on `provider_fallback`, but the main chat receives neither that frame nor a recovered status. Its named line remains “Retrying now…” until the helper eventually finishes, falsely describing healthy work as a model problem.
- **Evidence**: `/workspace/wt/1537-spec/pkg/providers/fallback.go::FallbackChain.Execute` sets `Retries = attemptNo - 1` for the **answering candidate**, so a first-attempt fallback reports zero. `/workspace/wt/1537-spec/pkg/agent/loop_run_turn.go::agentLoopRunTurn.callProviderOnce` queues the fallback note and calls `noteProviderRecovered`; `/workspace/wt/1537-spec/pkg/agent/provider_status.go::agentLoopRunTurn.noteProviderRecovered` emits nothing for zero. With no 30-second silence, the silence watch has no waiting state to clear. `/workspace/wt/1537-spec/pkg/agent/fallback_note.go::turnState.queueProviderFallbackNote` sends only to the helper's transcript session. `/workspace/wt/1537-spec/src/store/chat/slices/provider-frames.ts::handleProviderFrame`, `provider_fallback` branch, clears the retry slot only in that addressed bucket. The spec excludes changes to this fallback path and supplies no equivalent named-line transition.
- **Recommendation**: Define the named notice's transition when a candidate switch succeeds without retrying the answering candidate. Clear the obsolete named retry without copying or persisting the fallback note into the main chat and without inventing a positive retry count. Add a two-candidate test with short waits and a successful tool call that keeps the helper active after provider success. Test both live and reattached root views; completion-only tests conceal the gap.
- **Certainty**: **Inferred, high confidence** — candidate accounting and event/consumer paths verified; composed UI failure not executed.

#### [MAJ-008] Reusing the indicators unchanged conflicts with one named, quiet live region

- **Severity**: MAJOR.
- **Lens**: Accessibility & keyboard; Infeasibility; UI states & journey gaps.
- **Affected section**: User Story 3; Accessibility and Keyboard; Design-System Components; FR-010; UAT-H9.
- **Failure scenario**: The developer prefixes and wraps the existing indicator components in the specified shared status region. Their inner live regions still announce unnamed messages, while the new outer region contains ticking visible text. Two helpers can produce duplicate, unnamed or repeated countdown announcements instead of one named state change.
- **Evidence**: The spec combines “the same two components with a name prefix” with “one `role=status` live region” whose text never changes on ticks. `/workspace/wt/1537-spec/src/components/chat/ProviderRetryIndicator.tsx::ProviderRetryIndicator` and `/workspace/wt/1537-spec/src/components/chat/ProviderWaitingIndicator.tsx::ProviderWaitingIndicator` each already own a hidden polite live region and a separate ticking visible span. A prefix outside the component does not enter its frozen announcement. Adding an outer live region puts those ticking spans back inside a live region. UAT-H9's axe/DOM check alone is not evidence of what a screen reader actually announces.
- **Recommendation**: Specify one owner for announcements. Permit the reused components to render without their own live regions in the named-line composition; keep ticking visible rows outside the one announcement region, whose frozen state-change text includes the name. Preserve standalone own-chat behavior. Test two simultaneous helpers, retry→recovered, silence→responding, overflow promotion and countdown ticks; distinguish DOM/axe assertions from an actual assistive-technology check of “announced once.” Do not claim the latter from the former.
- **Certainty**: **Verified** composition conflict; **Inferred, high confidence** accessibility consequence. Actual screen-reader behavior remains untested.

### MINOR Findings

#### [MIN-001] Overflow can describe recovered helpers as still waiting

- **Severity**: MINOR.
- **Lens**: Ambiguity; UI states & journey gaps.
- **Affected section**: Edge Cases / more than three helpers; UI Screens and States; FR-009.
- **Failure scenario**: Four helpers recover but continue tool work. Their recovered lines remain until work ends, yet the fourth is summarized as “and 1 more helper is waiting on its model.” Also, “three earliest” does not say earliest launch, first notice or latest update, so updates/reload can select different rows.
- **Evidence**: The spec's Success state retains recovered rows until helper end, while its overflow wording classifies every remaining row as waiting. The only overflow BDD dataset contains four retrying helpers.
- **Recommendation**: Define eligible row states, the stable ordering anchor, promotion when a row clears, and truthful mixed-state summary semantics. Add recovered-only and mixed waiting/recovered datasets. Exact wording remains the chief's copy task; do not silently remove the specified recovered lifetime to solve it.
- **Certainty**: **Verified, high confidence** — internal state/copy contradiction.

#### [MIN-002] The feature Epic and confirmed rider ownership are not recorded

- **Severity**: MINOR.
- **Lens**: Incompleteness; structural integrity.
- **Affected section**: Epic and Riders headers.
- **Failure scenario**: The feature advances into implementation with no linked feature container and no recorded owner/architect confirmation of its same-file hardening scope; #1850 can be omitted from the landing scope or treated as a separate patch despite being part of this package.
- **Evidence**: The spec says “Epic: none set” and justifies it with a missing milestone, which is not an Epic. `/workspace/wt/1537-spec/docs/internal/issue-and-board-conventions.md::Feature epics, Stage and Horizon` requires an Epic and chief/architect rider confirmation. Issue #1850's body names Team B, but the spec does not record the confirmation. This does not prove no Epic or confirmation exists elsewhere.
- **Recommendation**: Have the chief supply/link the Epic and record #1850's owner plus the architect-confirmed rider set before finalization. Keep #1851 explicitly excluded. Do not infer tracking completion from a milestone or from issue filing alone.
- **Certainty**: **Verified** documentation gap; external confirmation not supplied is **Unknown**.

#### [MIN-003] The spec exempts security fixes from their required re-review

- **Severity**: MINOR.
- **Lens**: Security; Inconsistency & ADR/AS-IS contradiction.
- **Affected section**: Security and User Promises, last bullet.
- **Failure scenario**: A security reviewer finds a delivery-boundary defect; the implementation is changed, then the team uses “not after fix rounds” to skip checking that correction before landing.
- **Evidence**: The spec prescribes security review “once, on the fresh implementation … not after fix rounds.” `/workspace/wt/1537-spec/CLAUDE.md::Change sizes and the review gate` instead requires re-review by the reviewers whose findings were fixed, scoped to those fixes. The absence of a changed security-focus package does not remove the feature gate's security seat.
- **Recommendation**: Replace the exemption with the current combined-cut gate rule and affected-findings-only re-review. No extra full security gate per author or every unrelated correction is needed.
- **Certainty**: **Verified, high confidence** — process contradiction, not a newly discovered product vulnerability.

#### [MIN-004] Reload during a wait cannot prove notices are absent from history

- **Severity**: MINOR.
- **Lens**: Testability & false-green risk; Incompleteness.
- **Affected section**: FR-011; Traceability Matrix, FR-011 row; Regression Test Requirements.
- **Failure scenario**: An implementation wrongly persists every notice. The planned reload-during-wait test still passes because the saved notice reappears. FR-011 is then reported covered without an oracle that rejects the forbidden persistence.
- **Evidence**: The FR-011 traceability row maps its non-persistence claim to “Reload during the wait” and addressing-unit tests. That scenario checks only that a notice returns, not why. The same row combines channel containment with storage absence without a dedicated BDD negative case.
- **Recommendation**: Add a completed-run history scenario asserting no retry/silence/recovered notice in persisted helper or root history and no notice after a fresh completed-chat load. Include real ordinary transcript entries and an unchanged saved fallback note as positive controls, so empty/unavailable history cannot pass. Give unchanged channel containment an explicit scenario/test mapping, with a root-channel positive control. Do not change channel behavior under #1851.
- **Certainty**: **Verified, high confidence** — the specified oracle cannot distinguish the forbidden implementation.

#### [MIN-005] The execution plan omits authority and required gates, and overstates RED

- **Severity**: MINOR.
- **Lens**: Testability & false-green risk; structural integrity.
- **Affected section**: Test-Driven Development Plan; Reachability / Test plan execution; Success Criteria.
- **Failure scenario**: A team runs only its seven new test rows on an author build and treats that as completion, or weakens already-correct regression assertions to satisfy “orders 1–7, all failing on the release head.” The result says nothing about the combined cut's generated types or browser build.
- **Evidence**: The plan names test authorship/execution, but does not name CI as the Go/build authority, `npm run typecheck`, contract verification or the relevant design-system checks. Its literal all-failing claim includes unchanged own-turn behavior and validation of old frames—positive controls that may already pass. `/workspace/wt/1537-spec/CLAUDE.md::Build, test, and quality gates`, `Definition of Done`, and `Landing trains` distinguish narrow author checks from the exact-cut CI/reviewer/UAT evidence.
- **Recommendation**: Name the applicable gates and their authoritative execution location: targeted author checks under the local limits; full CI and the combined feature gate at the cut; tester/independent validator on that exact commit. Record failing-first evidence for new behavior, while preserving passing controls and unchanged regressions. Do not dispatch reviewer-side local reruns; request any permitted narrow run through team-lead. State that fake-model rows prove deterministic routing/rendering, not real-provider behavior. Include the routing-consumer guard in the affected regression inventory: `/workspace/wt/1537-spec/pkg/agent/routing_session_id_consumer_set_adr057_test.go::TestRoutingSessionID_ConsumerSetIsClosed` explicitly accounts for the current two emitters and may need a deliberate, justified expectation update if their consumers move—not deletion.
- **Certainty**: **Verified, high confidence** — omissions checked against the complete spec and current root rules; no test failure is claimed.

#### [MIN-006] The proposed existing name resolver exposes IDs on a missing agent

- **Severity**: MINOR.
- **Lens**: Ambiguity; Inconsistency & ADR/AS-IS contradiction.
- **Affected section**: Symbols Involved / useRunningActivity; Unknown helper name; FR-005; Design-System Components.
- **Failure scenario**: The implementer reuses the claimed existing resolver for a deleted or not-yet-loaded agent. The main-chat notice starts with the raw agent ID, not “Helper.”
- **Evidence**: The spec describes the existing fallback as “Unknown agent.” `/workspace/wt/1537-spec/src/hooks/useRunningActivity.ts::resolveAgent` returns that only for an absent ID; for a nonempty ID missing from the list it returns the **ID itself**. Separately, the current recovered text is built as a system message in `/workspace/wt/1537-spec/src/store/chat/slices/provider-frames.ts::handleProviderFrame`, not by either reusable indicator component. “Same two components” is not a complete inventory of the three states.
- **Recommendation**: Correct both reuse claims. Explicitly require the named-line composition to map missing/loading/error roster resolution to “Helper,” never through the raw-ID fallback; keep the existing unknown-name negative test. Identify how the recovered sentence is reused without appending an extra unnamed root message. Do not change unrelated Activity naming or own-chat recovery presentation as a side fix.
- **Certainty**: **Verified, high confidence** — resolver and recovery render path read directly.

### Observations

#### [OBS-001] Preserve domain-component reuse; do not invent a new public notification kit

- **Severity**: OBSERVATION — non-gating.
- **Lens**: Design-system reuse & brand.
- **Affected section**: Design-System Components; Accessibility and Keyboard.
- **Failure scenario**: None established for the proposed token-based domain-component reuse. A generic public component or new controls would add requirements absent from this feature.
- **Evidence**: `/workspace/wt/1537-spec/design-system/catalog.json::entries` contains general `Callout`, `RunningIndicator` and `JobStatus` exports; neither provider indicator is a catalog entry. The actual indicators already implement the specific retry/silence grammar. `/workspace/wt/1537-spec/docs/internal/design/design-system-definition.md::Operating model` permits domain components outside the public primitive package. `/workspace/wt/1537-spec/src/components/ui/CLAUDE.md::Publishing a component is a four-part contract` applies if a public component is introduced. `/workspace/wt/1537-spec/docs/internal/brand/brand-guidelines.md::Brand Voice & Tone` favors clear, calm UI wording.
- **Recommendation**: Preserve the existing domain grammar while fixing MAJ-008's announcement ownership. Verify long names plus full notice text at 320 CSS pixels and the existing composer/Stop controls remain usable. No visual redesign, new dismiss control, arbitrary token or public component publication is justified by this spec.
- **Certainty**: **Verified** reuse evidence; future visual quality is **Unknown** until rendered.

#### [OBS-002] Recovery needs a bounded change, not a durable notice subsystem

- **Severity**: OBSERVATION — non-gating.
- **Lens**: Overcomplexity.
- **Affected section**: API and Data; Integration Boundaries; Ambiguity Warnings #2.
- **Failure scenario**: None established for the current no-new-store approach. Solving the recovery findings with transcript persistence, a parallel notification service or extra session subscriptions would introduce avoidable ownership and cleanup paths.
- **Evidence**: FR-008 requires recovery; FR-011 prohibits persistence. `/workspace/wt/1537-spec/pkg/gateway/ws_hub_projection.go::activeTurnProjection`, `/workspace/wt/1537-spec/pkg/gateway/websocket_replay.go::wsHandlerHandleAttachSession.sendIncremental` and `/workspace/wt/1537-spec/pkg/gateway/ws_tool_approval.go::WSHandler.sessionStateBytes` already supply bounded live state, recovery and a server clock. **“UI-independent turns and session-bound webchat streaming”**, `/workspace/wt/1537-spec/docs/internal/architecture/ADR-082-ui-independent-turns-and-session-bound-streaming.md::Decisions`, separates execution lifetime from viewer connections.
- **Recommendation**: Investigate those existing mechanisms first. Do not add a polling architecture, persisted notice log or feature flag merely because replay correctness was omitted from the impact table. A new abstraction must solve a requirement these mechanisms cannot meet.
- **Certainty**: **Inferred, medium confidence** — reuse recommendation, not a completed design or proof that no new field is needed.

#### [OBS-003] Adjacent replay-order hazard deserves a state-transition test

- **Severity**: OBSERVATION — warning, not a separate blocking claim.
- **Lens**: Incompleteness; Testability & false-green risk.
- **Affected section**: Integration Boundaries; FR-006, FR-008; gateway test addition under MAJ-001.
- **Failure scenario**: A turn first publishes a waiting status, later a retry, then a recovered status while continuing tool work. A fresh snapshot can replay recovered **before** the retained retry and reinstate a stale retry line.
- **Evidence**: `/workspace/wt/1537-spec/pkg/gateway/ws_hub_projection.go::activeTurnProjection.item` preserves first-insertion order, while `update` replaces each item's bytes in place. `hubProviderRetry` and `hubProviderStatus` use different per-turn keys. `projectionFrames` walks that retained order, and `/workspace/wt/1537-spec/src/store/chat/slices/provider-frames.ts::handleProviderFrame` applies the frames in receipt order. The exact end-to-end production sequence was not executed in this review.
- **Recommendation**: Add retry/status ordering permutations to the real-hub snapshot tests and require the final projected state to equal the live state. Ask the chief to track this adjacent hardening concern with the same-file rider scope if confirmed; do not silently repair it outside this package.
- **Certainty**: **Inferred, medium confidence** — storage/reduction mechanism verified, full producer sequence not independently reproduced. **UNVERIFIED runtime warning**, not an additional gate block.

---

## Structural Integrity

### Spec mode — plan-spec output

| Check | Result | Notes |
|---|---|---|
| Valid `Status:` field | PASS | `In review`. |
| ADR linked or explicitly not needed | PASS for presence; design remains pending | The header says no ADR, but acknowledges an unresolved wire shape. The separately commissioned architect must close that choice and its recovery consequences before RED. |
| Contract changes stated first, citing contracts | FAIL | Section exists, but the executable schema and generation plan is incomplete: MAJ-004; stale-tab claim false: MAJ-005. |
| API and data section | PASS for presence | No REST change or notice persistence; recovery assumptions fail under MAJ-001/002/006. |
| UI loading, empty, error and partial states | PASS for presence | Mixed recovered/waiting overflow needs MIN-001; successful fallback needs MAJ-007. |
| End-to-end user journey | PASS | Main → helper → recovery → completion is stated. |
| Accessibility and keyboard | FAIL in composition | No new controls or focus changes are proposed, but the one-region rule conflicts with unchanged reuse: MAJ-008. |
| Design-system components, catalogue-first | PASS | Existing domain components; token-only styling; OBS-001. |
| Security and user promises | PASS for product-scope coverage; process correction needed | Named-field construction and channel containment retained; MIN-003 removes the invalid re-review exemption. |
| BDD acceptance scenarios; independent expected values | PASS for presence | Countdown oracle is too weak, MAJ-002; missing paths listed below. |
| Requirement → scenario → test traceability | FAIL | FR-011's mapped scenario does not prove its prohibition: MIN-004. |
| Epic and confirmed, owned riders | FAIL | MIN-002. This review does not claim a chief confirmation that was not supplied. |
| Reachability section | PASS for presence | Names the real chat screen and execution stages; misses producers/recovery paths under MAJ-003/006. |
| Specific user-facing documentation TODOs | PASS for presence | DOC-001–003 name `/workspace/wt/1537-spec/docs/troubleshooting.md` and `/workspace/wt/1537-spec/docs/agents.md`; add decided stale-tab/recovery behavior. |
| Agent tool parity | N/A, stated | No editable managed-object property, tool argument, policy or agent action changes. |

### Twelve-lens coverage

| Lens | Findings / disposition |
|---|---|
| 1. Ambiguity | MIN-001, MIN-006; live-copy exceptions and paired identifiers in MAJ-004. |
| 2. Incompleteness | MAJ-001/003/006/007; MIN-002/004. |
| 3. Inconsistency / ADR / AS-IS | MAJ-001/002/003/005; MIN-003/006. |
| 4. Infeasibility | MAJ-002's reuse claim; MAJ-008's incompatible composition instructions. |
| 5. Contract-first gaps | MAJ-004/005. |
| 6. Security | MIN-003; STRIDE review below. No new demonstrated exploit. |
| 7. Reachability | MAJ-003/006; exact-cut verification gap in MIN-005. |
| 8. UI states and journey | MAJ-005/007/008; MIN-001. |
| 9. Accessibility and keyboard | MAJ-008. Text-only notice rows add no keyboard control. |
| 10. Design-system reuse and brand | OBS-001; no demonstrated token/brand violation in the written proposal. |
| 11. Testability / false greens | MAJ-002/003/006/007/008; MIN-004/005; OBS-003. |
| 12. Overcomplexity | OBS-002; no speculative service or configuration is required. |

### Architecture qualification

The primary walkthrough was read at `/workspace/wt/1537-spec/docs/internal/architecture/AS-IS-architecture.md::Session storage and bounded chat history` and `Contract-first wire-format pipeline`; its dated implementation claims were checked against the current code above. `/workspace/wt/1537-spec/docs/internal/architecture/plugin-extensibility-assessment.md` was read for the channels/tools boundary; this feature adds no extension surface.

**“A sub-agent is a session steered by another session”**, `/workspace/wt/1537-spec/docs/internal/architecture/ADR-091-steered-sessions-replace-subagents.md::D7`, distinguishes a child's own content from the parent's lifecycle presentation. Option A authorizes a narrow, named model-status summary; it is not permission to restore child-token/tool-output routing into the root. Have the architect record that bounded relationship to the existing decision. Older timestamp-cursor language in **“SPA Streaming Resilience”** is not the current catch-up implementation; the present hub journal/projection code is the evidence used here.

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap description | Required addition |
|---|---|---|
| Gateway recovery integration | UI/reducer tests cannot detect a cleared projection | Helper notice → main completion → fresh snapshot; other helper unaffected; snapshot and live-only recovery. |
| Replay clock integration | Existing component test supplies an impossible old receipt time to a fresh tab | Real reducer receipt with a controlled clock; snapshot and incremental paths; both chats; retry and elapsed-silence counters. |
| Retry producer coverage | Only the fallback-chain/429 path is concretely exercised | No-response and empty-stream retries through the loop event path, with original error classes preserved. |
| Lifecycle concurrency | H7 proves only nested appearance | Grandchild finish/failure/Stop missed during disconnect; same-session resume/follow-up; old terminal fact versus new notice. |
| Provider-success transitions | All successful scripts use the same candidate's retry | Primary retry exhausted, fallback's first attempt succeeds, helper continues tools; no stale root line. |
| Accessibility composition | One helper plus axe/text is not announcement proof | Multiple helpers and overflow; no nested live regions; frozen named text; separate actual screen-reader evidence if claiming “once.” |
| Persistence negatives | Reload while active also accepts saved notices | Completed history and fresh load, with saved-message and fallback-note positive controls. |
| Display boundaries | Overflow tests use only retrying helpers | Recovered/waiting mixes, promotion and deterministic ordering; 320px full text with long names. |
| Regression scope | Producer routing guard absent from inventory | Preserve own-notice/fallback/channel tests; deliberately reconcile the closed routing-consumer guard if affected. |
| Delivery authority | Own tests are not exact-cut acceptance | Apply MIN-005; no full local suite or reviewer-side execution is commissioned by this report. |

### Dataset Gaps

| Dataset | Missing boundary or negative | Recommendation |
|---|---|---|
| Helper identifiers | One field present; null; empty; 255/256 characters; unrelated extra field | Generated validators must accept valid paired values and reject the documented malformed shapes; include unchanged valid own-session frames as controls. |
| Countdown | Exact late-receipt value; zero after deadline; fast/slow client clock | Freeze clocks, assert expected remaining seconds and progression—not only an upper bound. Use browser timing tolerance only for the end-to-end visibility claim. |
| Notice composition | Same agent in two chats; same child session in a later execution | Key by helper identity while retaining execution/turn freshness; assert independent removal and no old-state revival. |
| Names | Roster still loading/failing, missing agent, long unbroken name | “Helper” until resolved; never raw IDs; wrapping and announced name checked separately. |
| Recovery | Main done before/after notice; missed terminal event; status/retry ordering | Cover both snapshot and incremental semantics, not just one hard reload in an already-idle root. |
| Provider scripts | Timeout, empty stream, multi-candidate success | Add deterministic cases without changing retry policy or requiring a paid provider for these routing tests. |

Tests named in the spec are proposed tests unless explicitly identified above as existing. No RED, GREEN, mutation, accessibility or CI result is claimed by this review.

---

## STRIDE Threat Summary

S = spoofing; T = tampering; R = repudiation; I = information disclosure; D = denial of service; E = elevation of privilege.

| Component / flow | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| Named-field notice construction | ok | ok | N/A | ok | N/A | N/A | Keep raw provider errors excluded. Add paired-field negative fixtures; IDs are not visible text. |
| Session publication and recovery | ok* | ok* | N/A | ok* | review | ok* | Preserve addressed-session routing/access checks; do not solve nested clearing with a global content broadcast. New cleanup must not retain ended helpers indefinitely. |
| Browser name and notice rendering | ok | ok | N/A | review | review | N/A | Use plain text, no raw-ID fallback; require long-name and row-cap tests. MIN-006 and lifecycle findings concern incorrect status, not a demonstrated exploit. |
| Tool/channel policy | N/A | N/A | N/A | ok | N/A | ok | No access-policy change; channel retry audience remains unchanged. |
| Fix verification process | N/A | N/A | review | review | review | review | MIN-003 is an explicit review exemption that must be removed. |

**Legend**: `ok` = no new gap established in the reviewed proposal; `N/A` = this flow does not introduce that property; `review` = a stated verification/implementation obligation, not a claimed exploit. `ok*` depends on retaining existing access behavior; it is not a fresh authorization audit.

User promises were checked against `/workspace/wt/1537-spec/docs/security.md::What it is`, `Limits and things to watch`, `/workspace/wt/1537-spec/docs/tools.md::Background results`, and the retry/silence sections of `/workspace/wt/1537-spec/docs/troubleshooting.md`. No secret, approval or tool-policy expansion is authorized by this feature. No product security issue was filed or published.

---

## Reachability Check

| Question | Answer | Evidence |
|---|---|---|
| Agent-facing tool: registered and permitted? | N/A | No new callable capability or editable object; spec header and Reachability section. |
| User-facing: named screen/component renders it? | Yes, as a plan | `/workspace/wt/1537-spec/src/components/chat/ChatScreen.tsx::ChatScreen` currently renders the own-session retry/waiting indicators under `isStreaming`; the spec explicitly plans a separate idle-visible named area. |
| Test plan describes execution, not only authorship? | Yes, but incomplete | RED/GREEN/CHECK and H1–H9 are stated; MIN-005 supplies missing authority/gates, and the MAJOR findings add missing paths. |
| Implemented and usable on this reviewed commit? | No such claim | The reviewed change adds only the specification. |

**Code correct and tested:** Not assessed by execution; specification review only.

**Reachable by a user/agent:** Proposed wiring reviewed; no changed implementation or hands-on acceptance verified.

## Unasked Questions

These are for the author and architect, not new founder product choices.

1. Which exact scoped event or current-state reconciliation removes each root notice for a direct child and a grandchild, including terminal events missed offline and a later execution reusing the same helper session?
2. How is the replay clock anchored? Can existing `session_state.emitted_at` satisfy the requirement without a new field, and how is that reconciled with unchanged own-notice behavior?
3. Which transitions clear a notice when the next successful provider is a fallback? Which rows count toward overflow, and what stable order survives updates and fresh snapshots?
4. Which component owns the one named announcement region, and how is recovered wording reused without inserting a duplicate unnamed system message?
5. Where are the closed wire decision, Epic, chief/architect rider confirmation and exact-cut verification plan recorded?

---

## Verdict Rationale

**REVISE**, not BLOCK: the review establishes incorrect status/recovery behavior and build-contract gaps, but no demonstrated data-loss path or new security hole meeting the skill's CRITICAL definition. MAJ-001/002/006 mean “reload is a MUST” is not yet backed by a workable recovery contract; MAJ-003/007 show that 429→same-model success is not the whole reachable provider lifecycle. MAJ-004/005 invalidate the claim that the proposed fields are a small, safely additive contract-only edit. MAJ-008 prevents unchanged component reuse from satisfying the accessibility contract.

The known open points have not merely been repeated: the findings identify the concrete hub clear, clock calculation, strict validators and direct-parent recovery boundaries the architect must resolve. This reviewer does not choose the wire design or reopen option A.

### Recommended Next Actions

- [ ] Architect: resolve notice ownership, current-state/replay lifetime, clock and clearing contracts using MAJ-001/002/005/006/007; record the narrow relationship to existing session-routing decisions.
- [ ] Spec author: correct producer/contract inventories, composition and test oracles; add the missing scenarios and mappings, preserving the unchanged behaviors and channel exclusion.
- [ ] Chief/team-lead: supply the Epic and rider confirmation, route strings for copy review, and record the founder-interview checkpoint before fix round 1.

### Next step in the process

**Verdict: REVISE**

Review written to: `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec-review.md`.

This is grill round 1 of 2 (fixed). Next: team-lead interviews the founder on “Questions for the founder”, then the spec author fixes round-1 findings, then grill-spec runs SPEC MODE ROUND 2 on the corrected spec at `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec.md` — regardless of this round's verdict. There is no third spec grill round.

## Questions for the founder

**No new product-choice question is required by this review.** Option A is already chosen. The wire shape, mandatory reload implementation and nested-helper scope are already routed to the architect in the commission; copy goes to the chief. Do not ask the founder to repeat those instructions or present the implementation defects above as optional product choices. Team-lead should record the interview checkpoint and bring back only a genuine product trade-off exposed by the architect's work before the correction.

skills: grill-spec (worktree skill, review constitution and report template), omnipus-shared-rules, ux-heuristics-review; design-system rulebook read for relevant reuse/token/accessibility guidance.

## Evidence and final self-check

| Claim | Evidence | Certainty |
|---|---|---|
| Correct revision and source-only scope | `git rev-parse HEAD` exit 0: `ddc5578088006e955248a52ec749d7c6cf83a755`; `git rev-parse f408d81c7` exit 0: `f408d81c746b94a30824537628ab7e03016a093b`; `git diff --name-status f408d81c7..HEAD` exit 0: only `A docs/internal/specs/helper-model-notices-spec.md`. | Verified |
| Round 1 is appropriate | `rg --files docs/internal/specs` followed by the review-name filter: no prior round-1/round-2 artifact, filter exit 1; known spec search exit 0. The specification was read in full. | Verified |
| MAJ-001: parent completion clears projected notices | `/workspace/wt/1537-spec/pkg/gateway/ws_hub_projection.go::activeTurnProjection.update`; `/workspace/wt/1537-spec/pkg/gateway/websocket_streamer.go::wsStreamerFinalize.publishDone`. | Verified mechanism; prospective failure inferred |
| MAJ-002: fresh replay loses elapsed time | `/workspace/wt/1537-spec/pkg/gateway/ws_hub_projection.go::projectionFrames`; `/workspace/wt/1537-spec/src/store/chat/slices/provider-frames.ts::handleProviderFrame`; `/workspace/wt/1537-spec/src/components/chat/ProviderRetryIndicator.tsx::remainingRetrySeconds`; existing test's original-receipt setup read. | Verified calculation; not executed |
| MAJ-003: separate timeout/empty-reply emitter | `/workspace/wt/1537-spec/pkg/agent/loop_run_turn_response.go::timeoutRetryPayload`; `/workspace/wt/1537-spec/pkg/gateway/context_window_notice.go::WSHandler.hubLLMRetry`; both named #1664 tests read, not run. | Verified |
| MAJ-004: inline schemas drive generation | `/workspace/wt/1537-spec/contracts/asyncapi.yaml::components.schemas.ProviderRetryFrame` / `ProviderStatusFrame`; `/workspace/wt/1537-spec/scripts/_gen-asyncapi-types.mjs::schemas`; `/workspace/wt/1537-spec/scripts/gen-contracts.sh`. | Verified |
| MAJ-005: old validators reject new properties | `/workspace/wt/1537-spec/src/lib/api/generated/ws-schemas.ts::ProviderRetryFrame` / `ProviderStatusFrame`; `/workspace/wt/1537-spec/src/lib/ws.ts::_parseServerFrame`; `/workspace/wt/1537-spec/src/hooks/useVersionCheck.ts::useVersionCheck`. | Verified |
| MAJ-006: root lacks specified recoverable nested clearing | `/workspace/wt/1537-spec/pkg/agent/steer_audience.go::deliverOwnerKey`; `/workspace/wt/1537-spec/pkg/agent/steer_frames.go::deliverSubagentState`; `/workspace/wt/1537-spec/pkg/gateway/websocket_forward_hub.go::hubTurnEnd` / `hubBroadcastWithSequencedCopy`; `/workspace/wt/1537-spec/pkg/gateway/websocket_replay.go::sendIncremental`. | Inferred from verified routing |
| MAJ-007: fallback success need not emit recovered | `/workspace/wt/1537-spec/pkg/providers/fallback.go::FallbackChain.Execute`; `/workspace/wt/1537-spec/pkg/agent/loop_run_turn.go::callProviderOnce`; `/workspace/wt/1537-spec/pkg/agent/provider_status.go::noteProviderRecovered`; `/workspace/wt/1537-spec/pkg/agent/fallback_note.go::queueProviderFallbackNote`. | Inferred from verified emission rules |
| MAJ-008: unchanged indicators own separate live regions | `/workspace/wt/1537-spec/src/components/chat/ProviderRetryIndicator.tsx::ProviderRetryIndicator`; `/workspace/wt/1537-spec/src/components/chat/ProviderWaitingIndicator.tsx::ProviderWaitingIndicator`; spec FR-010 and Design-System Components. | Verified composition conflict; actual announcements Unknown |
| MIN-001: overflow mislabels recovered rows | `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec.md::UI Screens and States`, `Edge Cases`, FR-009. | Verified |
| MIN-002: Epic/rider confirmation absent from spec | `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec.md::Epic`, `Riders`; `/workspace/wt/1537-spec/docs/internal/issue-and-board-conventions.md::Feature epics, Stage and Horizon`. | Verified document gap; external confirmation Unknown |
| MIN-003: re-review exemption contradicts current process | `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec.md::Security and User Promises`; `/workspace/wt/1537-spec/CLAUDE.md::Change sizes and the review gate`. | Verified |
| MIN-004: active-wait reload cannot prove non-persistence | `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec.md::Traceability Matrix`, FR-011 and “Reload during the wait”. | Verified oracle gap |
| MIN-005: test execution plan omits authority and overstates RED | `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec.md::Reachability`; `/workspace/wt/1537-spec/CLAUDE.md::Build, test, and quality gates`, `Landing trains`. | Verified document gap |
| MIN-006: missing-agent resolver returns raw ID | `/workspace/wt/1537-spec/src/hooks/useRunningActivity.ts::resolveAgent`; `/workspace/wt/1537-spec/src/store/chat/slices/provider-frames.ts::handleProviderFrame`; spec Symbols Involved and Design-System Components. | Verified |
| OBS-001: existing domain components are a suitable starting point | `/workspace/wt/1537-spec/design-system/catalog.json::entries` (all exports enumerated, known `Callout`/`JobStatus` controls present); `/workspace/wt/1537-spec/docs/internal/design/design-system-definition.md::Operating model`; existing indicator components read. | Verified inventory; reuse recommendation Inferred |
| OBS-002: existing bounded recovery mechanisms merit reuse first | `/workspace/wt/1537-spec/pkg/gateway/ws_hub_projection.go::activeTurnProjection`; `/workspace/wt/1537-spec/pkg/gateway/websocket_replay.go::sendIncremental`; `/workspace/wt/1537-spec/pkg/gateway/ws_tool_approval.go::sessionStateBytes`. | Inferred recommendation from verified mechanisms |
| OBS-003: replay-order warning | `/workspace/wt/1537-spec/pkg/gateway/ws_hub_projection.go::activeTurnProjection.item`, `update`, `projectionFrames`; retry/status use separate keys. | Inferred; full runtime sequence UNVERIFIED |
| Issue/rider scope and probe limits checked | Read-only `gh issue view 1537`, `1850`, `1851` exit 0: all `OPEN`; `/workspace/logs/1537/RESULT-1537.md`, `/workspace/logs/1537/tester/report.md`, `/workspace/logs/1537/validator/report.md`, `/workspace/logs/1537/recipe/RECIPE.md` read. | Verified documents; no fresh probe claimed |
| No build/test result or product delivery claimed | Review used reads, searches and read-only Git/GitHub checks; no generator, build, test, browser, commit, push or GitHub mutation was run. | Verified for this review |
| **Self-check** | Rechecked the spec against every finding and all twelve lenses; verified citation symbols, the current generator/validator paths, snapshot versus incremental behavior, and countdown-test positive-control limitation. Re-read this report as a diff and checked final Git scope/HEAD against the commissioned revision; only this authorized report is added, with no tracked-source or staged change. | Verified; no product execution claimed |
