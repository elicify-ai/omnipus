# ADR-091 WP-E — Contracts and SPA: wire what exists, add an open button

- **Decision record:** [ADR-091](../architecture/ADR-091-steered-sessions-replace-subagents.md) D7 (client), D4 (contract), I-4
- **Landing order:** [adr-091-landing-order.md](adr-091-landing-order.md) — consumes I-1, I-4; owns every wire-format change
- **Owner files:** landing order §3, row E
- **Status:** Draft rev 4 (consolidated after three grills; supersedes every earlier sentence)

## Summary

Nothing about how a steered session looks on screen is new. The side panel, the pill, the sidebar nesting, session opening, single-session streaming and replay all exist and stay as they are. The status line the founder wants (#755) was designed in ADR-053 "Unified goal / plan / subagent system" and put into the contracts as `subagent_message` and `subagent_state` — and then never emitted by the server or read by the SPA. This package's whole job on the screen side is: **read those frames into the row the side panel already draws, add an open control to that row, count the open session's running direct children in the pill, and stop filing a child's frames under its parent.** Plus every wire-format change the other packages need, made here and only here (Hard Constraint #8). No steering composer: writing into an open session already steers it (founder decision, round 4).

## Existing codebase context

*Source inspection at `364290cb5`.*

### What exists and is reused unchanged

| Surface | Where | Evidence |
|---|---|---|
| Side panel listing the active session's children | `src/components/chat/ActivityPanel.tsx`, `src/hooks/useRunningActivity.ts` | aggregates `subagent_start` / `subagent_end` spans and background bash into one list with status dot, elapsed, `RECENTLY_FINISHED_CAP` |
| Pill with running count | existing tray grammar shared with `src/components/chat/GoalPillTray.tsx` | grammar unchanged; the count's source changes (below) |
| Sidebar nesting and opening a child | `src/components/sessions/SessionTree.tsx` | nests children via `GET /sessions?parent_session_id=`; `Session.parent_session_id` already on the wire |
| Watching a child live | single-session attach (`AttachSessionFrame.yaml`) and per-session replay | a task session is opened this way today |
| Steering by typing | inbound message to a session with a live turn becomes a steering message | no UI needed |
| Approval and question handling | `src/store/toolApproval.ts`, `ToolApprovalModal.tsx`, `CrossWorkspaceApprovalBanner.tsx`; `tool_approval_required` and `ask_user_question` are **session-scoped** frames (`runtime-state.ts::SESSION_SCOPED_FRAME_TYPES`) with their own identity handling | unchanged |
| Status-line contract | `contracts/components/schemas/SubagentMessageFrame.yaml` (`kind`, `text`, `pct`, `span_id`), `SubagentStateFrame.yaml` (`state` ∈ the eight lifecycle states) | **generated types exist in Go and TS; zero references in production code** (the only non-generated reference in `src/` is the FR-047 guard test that forbids them) — designed, never wired |

### Contract changes (this package owns every one)

| File | Change | Why |
|---|---|---|
| `SubagentStartFrame.yaml` | add optional `child_session_id`; `parent_call_id` documented as the originating `delegate` **or `create_task`** call id | the open control needs a target; task-origin children have a span too (I-4) |
| `SubagentEndFrame.yaml`, `ToolResultProjectionFrame.yaml`, `ToolApprovalRequiredFrame.yaml`, `GoalStatusFrame.yaml`, `LoopStatusFrame.yaml`, `TaskStatusChangedFrame.yaml`, and the inline copies in `asyncapi.yaml` | **delete `producing_session_id`** — the complete inventory of seven files | it was the workaround for relabelled frames; every frame now carries its own `session_id` (I-4; closes #658's split) |
| `DelegateRunAction.yaml` | remove `wait` and `allow_blocking_question` (the contract's names — the Go tool parameter is `async`, in `pkg/tools/delegate.go::Parameters`, owned by WP-C); add `goal` in `create_task`'s shape | D4, D6 |
| `DelegateSessionResponse.yaml` (run result) | add `state` (`queued` / `running`), `queue_position`, `generation` — populated from `DispatchResult`, never from `Launch` | I-2 |
| `WorkspaceDelegationEdge.yaml` | description text: no await | D4 |
| `SessionLifecycleRecord.yaml` | add record-level `origin` `{kind, call_id, task_id}`, `steered_by` (steering session, root, authorization, limits, tool exclusions), `stop` (`{at, generation, by}`); `generation` already exists on the wire; **delete `parent_durable_key`** | I-1 |
| `SessionMessageGoalStatus.yaml` | **extended minimally** (founder decision, round 10): `direction` gains `session_to_parent`; `condition` gains `not_met`; new optional `evidence[]{criterion, met, note}` — the existing kind, no new kind | I-5 |
| `SubagentMessageFrame.yaml` | the `kind` enum gains `goal_status` (it excludes it today) — an enum value on an existing frame, not a new frame type | I-4 |
| Stop response (`websocket_cancel.go` frame; REST cancel body) | add `reached`, `unreachable[]{id, reason}`, `skipped_newer_generation`, `skipped_terminal`, `partial` | I-6 |
| `WsFrameType.yaml`, `asyncapi.yaml` inline copies | kept in sync by hand (ADR-084's copies rule); **no new frame type** | — |
| `SubagentStateFrame.yaml`, `AttachSessionFrame.yaml`, `Session.yaml`, the `Task` wire shape | **unchanged** — the task's new `OriginCallID` is disk-only like `OriginSessionID` and MUST NOT be added to any schema | — |

### SPA changes (all small)

| File | Today | Change |
|---|---|---|
| `src/store/chat/slices/frames.ts::handleFrame` | buckets by the frame's `session_id`; test-mode fallback to the active session (`FALLBACK_SID`); buffers child steps by parent call (`pendingByParentCallId`, `span.steps`) | keep bucketing for session-scoped frames; **delete the fallback** and **delete the step buffering** (no child steps arrive in a parent's bucket any more); reduce `subagent_message` / `subagent_state` into the span record |
| `src/lib/__adr057__noSubagentMessageOrStateReferences.test.ts` | ADR-057 FR-047 guard: asserts zero references to the two frames (the earlier, emitter-less surface was deleted in `2337e864b`) | **deleted** in the same commit that adds the consumer; ADR-091 D7 supersedes FR-047 |
| `src/hooks/useRunningActivity.ts` | span item: label, agent, status, elapsed, interrupt reason; `runningCount` combines agent spans **and background shell jobs** | item gains `statusLine` (last `subagent_message.text`), `lifecycleState` (last `subagent_state.state`), `childSessionId`; a **separate** `runningChildren` selector counts agent spans in `running` only — the pill reads that (founder decision, round 8) |
| `src/components/chat/ActivityPanel.tsx` | row: dot + label + elapsed | row shows the one status line ("last update N s ago") and an **open** control navigating to `childSessionId`; `queued` rows show "queued"; while `src/store/toolApproval.ts` holds a pending approval for the child's session id, the line reads "awaiting approval: <tool>" (#658; the modal keeps working as today) |
| `src/lib/subagentStatus.ts` | interrupt-reason formatting | maps `lifecycleState` to the existing dot vocabulary |
| `src/components/chat/SubagentBlock.tsx`, `ChatScreen.tsx`, `JudgeVerdictThreadCard.tsx`, `src/lib/toolVisibility.ts::shouldRenderSubagentSpan` | render nested child steps inside the parent's chat from relabelled frames | dead once frames stop arriving in the parent's bucket — delete the nested-step rendering; the `delegate` tool-call line and the tool-call filter stay |
| `src/lib/api/generated/*` | generated | regenerated, never hand-edited |

### Impact assessment (manual)

| Area | Risk | Dependents |
|---|---|---|
| Deleting `producing_session_id` from seven schemas | MEDIUM — Go readers (WP-B) and TS readers must move in the same commit | `websocket_forward.go`, `frames.ts` |
| Removing the frame fallback | MEDIUM — store tests that relied on it | `frames.ts` tests |
| Deleting nested-step rendering | LOW — code path receives no frames after WP-B | `SubagentBlock` tests |
| Side panel row and pill selector | LOW | `ActivityPanel` tests, pill tests |

## User stories and acceptance criteria

### US-1 — Find, open and watch a steered session (P0)

1. **Given** a running steered session, **When** the side panel renders for its parent, **Then** the existing row shows title, state, elapsed, one status line and an open control.
2. **Given** the operator activates the open control (or picks the child in the sidebar), **When** the chat loads, **Then** its history replays and live frames stream, exactly as for a task session today.
3. **Given** the operator writes into it while its turn is live, **When** the message is sent, **Then** it is treated as steering — no new UI.
4. **Given** a finished steered session, **When** opened, **Then** its full history shows, including tool errors.
5. **Given** a child launched at the cap, **When** the side panel renders, **Then** its row reads "queued" until it starts.

### US-2 — The parent chat stays clean; the side panel carries the status (P0)

1. **Given** a parent that delegated, **When** the child runs, **Then** the parent's chat shows only the `delegate` tool-call line.
2. **Given** the child calls `message_parent` with progress, **When** the `subagent_message` frame arrives, **Then** the row's status line shows its `text` within 1 s.
3. **Given** the child parks (`needs_input`), completes or fails, **When** the `subagent_state` frame arrives, **Then** the row's state dot changes accordingly.
4. **Given** a grandchild, **When** it emits status, **Then** it appears in *its own parent's* side panel — not the root's.
5. **Given** the open session has N direct children in `running` and M background shell jobs, **When** the pill renders, **Then** it shows N (founder decision, round 8).
6. **Given** the child is waiting for a tool approval, **When** the approval frame arrives (session-scoped; broadcast to every browser of the account today), **Then** the modal behaves exactly as today and the child's row reads "awaiting approval: <tool>" until it is resolved.
7. **Given** a page reload with a running child, **When** the parent's replay arrives, **Then** the row shows the last status line and state from the replayed `subagent_*` events in the parent's transcript (WP-B persists them; founder decision, round 7).

### US-3 — Contracts change first, all of them here (P0)

1. **Given** any wire change in this delivery, **When** it lands, **Then** the schema change, the regenerated Go and TS, and the consumer land in one atomic commit and `make verify-contracts` is green.
2. **Given** `DelegateRunAction`, **When** validated, **Then** `wait` and `allow_blocking_question` are rejected and `goal` is accepted in `create_task`'s shape.
3. **Given** the seven schemas that carry `producing_session_id` today, **When** the delivery lands, **Then** none does, and `grep -rn producing_session_id contracts/` returns 0.

### Edge cases

| Case | Expected |
|---|---|
| A session-scoped frame (`tool_call_start`, `subagent_message`, `tool_approval_required`, …) arrives with no `session_id` | dropped with a diagnostic; never filed under the active session |
| A genuinely global frame (`task_updated`, `plan_updated`, `library_changed`) arrives | handled as today — it is not in `SESSION_SCOPED_FRAME_TYPES` |
| A `subagent_message` whose `span_id` matches no open span (replay gap) | stored against the span id; rendered when the span appears |
| A transcript written before this delivery replays a `subagent_start` without `child_session_id` (data written by the old code; history-only per §6) | the row renders without the open control |
| A child completes while its own view is open | its view shows completion; the parent's row moves to recently-finished |
| More than `RECENTLY_FINISHED_CAP` finished children | oldest drop off, as today |

## Behavioral contract

- When a steered session exists, the sidebar and the parent's side panel show it as they show a task session today, and the row has a status line and an open control.
- When a session is opened, the chat streams and replays that session by its own id.
- When a child reports progress or changes state, its row updates; the parent's chat does not.
- When the pill renders, it counts the open session's running direct agent children.
- When a wire shape changes, the contract changes first, here, and the clients are regenerated.

## Explicit non-behaviors and safeguards

### Qualitative prohibitions

- The SPA must not file a frame under a session other than its `session_id`, because re-labelling is what hid children.
- The SPA must not render any child frame inside a parent's chat, because the founder decision is that child work lives in the side panel and the child's own session.
- The SPA must not fall back to the active session for a session-scoped frame without an id, because that fallback lets weak fixtures pass.
- The SPA must not subscribe to a session nobody has opened.
- The SPA must not introduce a new frame type or a new panel for this; the ADR-053 frames and the existing panel are the design.
- The SPA must not build a steering composer.
- The SPA must not count shell jobs in the pill, because the pill answers "how many sub-agents are running".
- No wire type may be hand-written (`scripts/check-no-handwritten-wire-types.sh`).

### Machine-verifiable constraints

Conventions: landing order §5 item 6.

| Constraint | Exact check |
|---|---|
| Contract-first | `make verify-contracts` green; generated diff committed with the schema |
| Field inventory | `grep -rln producing_session_id contracts/ --include='*.yaml'` → 0; `grep -rln ProducingSessionID src/lib/api/generated/` → 0 |
| Bucketing | a session-scoped frame with `session_id: C` lands in bucket C in 100% of store tests |
| Parent chat clean | with C running under B, B's message list gains exactly one entry (the `delegate` tool call) |
| Side panel row | for C: label non-empty; `lifecycleState` equals the last `subagent_state.state`; `statusLine` equals the last `subagent_message.text`; open control navigates to `child_session_id`; `queued` renders "queued" |
| Status latency | row updates within 1 s of the frame (fake timers) |
| Pill | with 2 running children, 1 completed child and 1 running shell job, `runningChildren` is 2 |
| No fallback | `handleFrame` with a session-scoped frame missing `session_id` → dropped, diagnostic logged; `task_updated` without one → handled as today; `FALLBACK_SID` absent from `runtime-state.ts` |
| Single attach | at most one `attach_session` per tab at any time |
| Removed args | Zod schema for `DelegateRunAction` rejects `wait` and `allow_blocking_question` |
| Approval line | with a pending approval for C in `useToolApprovalStore`, C's row reads "awaiting approval: <tool>"; cleared on `tool_approval_resolved` |
| Guard retired | `test ! -e src/lib/__adr057__noSubagentMessageOrStateReferences.test.ts` |
| No new frame type | the set of `WsFrameType` values is unchanged by this package |
| Replay rebuilds the row | with an empty store and a replayed transcript holding the AS-7 events, the row shows the last line and state |

## Integration boundaries

| System | Contract | On failure |
|---|---|---|
| Gateway (WP-B) | I-4: own-id frames; `subagent_start` (+`child_session_id`) / `state` / `message` / `end` persisted in the parent's transcript and replayed | reconnect replays the open session |
| Generated clients | `scripts/gen-contracts.sh` | CI `verify-contracts` |
| Session REST | `parent_session_id` (existing) | n/a |

## BDD scenarios

```gherkin
Feature: Steered sessions on screen

  # Happy Path — Traces to: US-1 / AS-1, AS-2
  Scenario: Open a running steered session from the side panel
    Given worker session C is running under Jim's session B
    When the operator views B
    Then the side panel row for C shows "running" and an open control
    When the operator activates the open control
    Then C's history replays and new frames render in C's view

  # Alternate Path — Traces to: US-1 / AS-5
  Scenario: A queued child shows as queued
    Given C was launched at the cap
    When the operator views B
    Then C's row reads "queued"
    When C starts
    Then C's row reads "running"

  # Happy Path — Traces to: US-2 / AS-1, AS-2
  Scenario: The parent chat stays clean while the row updates
    Given B is open and C is running
    When B's stream receives a subagent_message with text "checking the checkout page"
    Then B's chat still shows only the delegate tool-call line
    And C's row shows "checking the checkout page"

  # Happy Path — Traces to: US-1 / AS-3
  Scenario: Writing into an open steered session steers it
    Given C is open and its turn is live
    When the operator sends a message
    Then C's steering queue receives it

  # Alternate Path — Traces to: US-2 / AS-4, AS-5
  Scenario: Grandchild in its own parent's panel; pill counts direct children only
    Given R -> B -> C with B and C running, and a background shell job in B
    When the operator views R
    Then R's side panel lists B and R's pill shows 1
    When the operator views B
    Then B's side panel lists C and B's pill shows 1

  # Alternate Path — Traces to: US-2 / AS-7
  Scenario: Reload rebuilds the row from the replayed transcript
    Given the browser store is empty and B's transcript holds C's lifecycle events
    When B's replay arrives
    Then C's row shows the last status line and its state

  # Edge Case — Traces to: edge cases
  Scenario: An old transcript without child_session_id
    When a replayed subagent_start arrives without child_session_id
    Then the row renders without the open control

  # Error Path — Traces to: edge cases
  Scenario: A session-scoped frame without a session id is dropped; a global frame is not
    When a tool_call_start frame arrives with no session_id
    Then it is not rendered anywhere and a diagnostic is logged
    When a task_updated frame arrives
    Then it is handled as today

  # Happy Path — Traces to: US-2 / AS-6
  Scenario: A child's pending approval shows on its row
    Given C is running under B and a tool_approval_required frame for C has arrived
    When the operator views B
    Then C's row reads "awaiting approval: bash"
    And the approval modal behaves exactly as today
    When the approval is resolved
    Then C's row returns to its status line

  # Happy Path — Traces to: US-3 / AS-3, FR-E-008, FR-E-010
  Scenario: Nothing new is added; the retired field is gone everywhere
    When the delivery is inspected
    Then WsFrameType has gained no value
    And no schema under contracts/ contains producing_session_id
    And no new panel or composer exists in the DOM
    And the FR-047 guard test file no longer exists

  # Error Path — Traces to: US-3 / AS-2
  Scenario: Removed delegate arguments fail validation
    When a DelegateRunAction with wait=true is validated
    Then validation fails naming "wait"
```

## TDD plan

Implementers load the `test-driven-development` skill first. Vitest groups per `src/components/chat/CLAUDE.md` (`components-chat`; store tests in `lib-store`); `scripts/check-vitest-coverage.mjs` is the tripwire.

| Order | Test | Level | Traces to |
|---|---|---|---|
| 1 | contract: `verify-contracts`; Zod rejection of `wait` / `allow_blocking_question`; `child_session_id` optional on `SubagentStartFrame`; `producing_session_id` absent from all seven schemas; **explicit assertions** that the generated validators accept `SessionLifecycleRecord` with `origin` + `steered_by` + `stop` and reject `parent_durable_key`, accept a Stop response with `reached` / `unreachable` / `skipped_newer_generation` / `skipped_terminal` / `partial`, accept `DelegateSessionResponse` with `state` / `queue_position` / `generation`, accept a `goal_status` with `session_to_parent` + `not_met` + `evidence` and a `subagent_message` of kind `goal_status`; the FR-047 guard file is gone | Unit | US-3 |
| 2 | `frames.bucketsByProducer.test.ts` | Unit | US-1/AS-2 |
| 3 | `frames.dropsMissingSessionId_keepsGlobal.test.ts` | Unit | edge |
| 4 | `frames.reducesSubagentMessageAndState.test.ts` | Unit | US-2/AS-2,3 |
| 5 | `frames.reloadRebuildsRow.test.ts` | Unit | US-2/AS-7 |
| 6 | `chat.parentReceivesNoChildFrames.test.ts` | Unit | US-2/AS-1 |
| 7 | `useRunningActivity.statusLineChildIdQueued.test.ts` | Unit | US-1/AS-1,5 |
| 8 | `useRunningActivity.grandchildUnderOwnParent.test.ts` | Unit | US-2/AS-4 |
| 9 | `useRunningActivity.runningChildrenExcludesShell.test.ts` | Unit | US-2/AS-5 |
| 10 | `pill.countsRunningChildren.test.tsx` | Unit | US-2/AS-5 |
| 11 | `ActivityPanel.rowWithStatusAndOpen.test.tsx` | Unit | US-1/AS-1, edge (no child id) |
| 12 | `ActivityPanel.awaitingApprovalLine.test.tsx` | Unit | US-2/AS-6 |
| 13 | `ChatScreen.opensSteeredSession.test.tsx` | Unit | US-1/AS-2 |
| 14 | Playwright: side panel → open worker → live stream → typed steer → parent chat unchanged → reload → row restored | E2E | US-1, US-2 |

### Test datasets

| ID | Input | Expected | Traces to |
|---|---|---|---|
| E-1 | frames with `session_id` ∈ {C, B, missing}; types ∈ {tool_call_start, task_updated} | bucket C / bucket B / dropped (session-scoped) / handled (global) | US-1, edge |
| E-2 | in B's transcript: start(C), state(queued), state(running), message(progress ×3), state(needs_input), state(completed), end | row: line = last text; state per frame, in order; "queued" first | US-2 |
| E-3 | tree R → B → C, B and C running, one shell job under B; view R / view B | R's panel lists B, pill 1; B's panel lists C, pill 1 | US-2/AS-4,5 |
| E-4 | `DelegateRunAction` with `wait` / valid `goal` / invalid `goal` | reject / accept / reject with `create_task`'s message | US-3 |
| E-5 | `subagent_start` with / without `child_session_id` | open control present / absent | edge |
| E-6 | finished children 0 / cap / cap+1 | rows / rows / oldest dropped | edge |
| E-7 | approval queue: none / pending for C / resolved | plain line / "awaiting approval: bash" / plain line | US-2/AS-6 |
| E-8 | replay from an empty store with E-2's events | row rebuilt | US-2/AS-7 |

### Regression requirements

Preserved unchanged: `toolVisibility.ts` tool-call filter; `RECENTLY_FINISHED_CAP` and elapsed-time behaviour in `useRunningActivity.ts`; verbose-chat behaviour for the parent's own tool calls; `GoalPillTray` / `GoalIndicator` rendering; the pill's tray grammar; `SessionTree` nesting; approval and question-card identity handling. Updated: tests keyed on `parentCallId` spans; `ActivityPanel` tests; the pill test (new selector). Deleted: `SubagentBlock` nested-step tests, the test-mode active-session fallback and its tests, the FR-047 guard.

## Functional requirements

| ID | Requirement |
|---|---|
| FR-E-001 | Every wire change in this delivery MUST be made in this package, follow the five-step contract-first process and land atomically with regenerated clients. |
| FR-E-002 | The SPA MUST bucket every session-scoped frame (`SESSION_SCOPED_FRAME_TYPES`, which includes approvals and ask-user cards) by its `session_id`, MUST drop a session-scoped frame without one, and MUST handle genuinely global frames (`task_updated`, `plan_updated`, `library_changed`) as today; the test-mode active-session fallback is deleted. |
| FR-E-003 | The parent's chat MUST show no child frame; the `delegate` tool-call line remains. |
| FR-E-004 | The existing side panel row MUST show the last `subagent_message.text` as its status line, the last `subagent_state.state` as its state ("queued" included), and an open control targeting `child_session_id`. |
| FR-E-005 | The pill MUST show the number of the open session's direct agent children in `running`, via a selector that excludes background shell jobs; a grandchild counts in its own parent's pill. |
| FR-E-006 | The SPA MUST attach to at most one session per tab and MUST NOT subscribe to unopened sessions. |
| FR-E-007 | `DelegateRunAction` MUST reject `wait` and `allow_blocking_question` and accept `goal` in `create_task`'s shape. |
| FR-E-008 | This package MUST NOT add a frame type, a panel, or a steering composer. |
| FR-E-009 | The side panel row MUST show "awaiting approval: <tool>" while the browser's approval queue holds a pending approval for that child, read from `src/store/toolApproval.ts` by session id — no new frame (#658). |
| FR-E-010 | The ADR-057 FR-047 guard test MUST be deleted in the same commit that adds the `subagent_message` / `subagent_state` consumer. |
| FR-E-011 | `producing_session_id` MUST be deleted from all seven schemas and inline copies that carry it, in one commit with the Go readers (WP-B) and the TS readers. |
| FR-E-012 | `SessionLifecycleRecord.yaml`, `DelegateSessionResponse.yaml` and the Stop response MUST carry the I-1, I-2 and I-6 fields respectively, each proven by a generated-validator assertion in test 1; `parent_durable_key` MUST be deleted. |
| FR-E-013 | `SessionMessageGoalStatus.yaml` MUST gain direction `session_to_parent`, condition `not_met` and `evidence[]`, and `SubagentMessageFrame.yaml`'s `kind` enum MUST gain `goal_status`; no new kind and no new frame type (founder decision, round 10). |

## Success criteria

| ID | Criterion |
|---|---|
| SC-E-1 | `make verify-contracts` green on every commit of this package. |
| SC-E-2 | 0 frames filed under a non-matching bucket across the store test suite; 0 child frames in a parent bucket. |
| SC-E-3 | Playwright: the operator opens a running worker from the side panel and sees it react to a typed message within 5 s, while the parent chat gains nothing; after a reload the row is back. |
| SC-E-4 | 0 occurrences of `producing_session_id` under `contracts/` and `src/`. |
| SC-E-5 | Net SPA line count for this package is negative (deleted nested-step rendering exceeds the row additions). |

## Traceability

| Requirement | Story | Scenarios | Tests |
|---|---|---|---|
| FR-E-001 | US-3 | contracts first | 1 |
| FR-E-002 | US-1 | dropped vs global | 2, 3 |
| FR-E-003 | US-2 | chat stays clean | 6, 14 |
| FR-E-004 | US-1, US-2 | open; queued; row updates; reload | 4, 5, 7, 11 |
| FR-E-005 | US-2 | grandchild and pill | 8, 9, 10 |
| FR-E-006 | US-1 | open from side panel | 13, 14 |
| FR-E-007 | US-3 | removed arguments | 1 |
| FR-E-008 | US-3 | nothing new | 14 (DOM assertion); constraint "No new frame type" |
| FR-E-009 | US-2 | pending approval | 12 |
| FR-E-010 | US-3 | nothing new | 1 |
| FR-E-011 | US-3 | retired field gone | 1 |
| FR-E-012 | US-3 | contracts first | 1 (explicit assertions) |
| FR-E-013 | US-3 | contracts first | 1 |

ADR ACs covered: AC-7 (client half), AC-12, AC-4 (contract half).

## Ambiguity warnings

| What was ambiguous | Resolution |
|---|---|
| Status line length | truncated at 120 characters with an ellipsis — accepted default |
| Which `subagent_message` kinds feed the line | all kinds with `text` (progress, checkpoint, blocker, question, error, handback); `steer` / `respond` show as "steered" without text — accepted default |
| What the pill counts | **Founder decision (round 8): direct children of the open session**, agent sessions only |

## Holdout evaluation scenarios (not for development)

1. Delegate from Jim; the worker appears in Jim's side panel with a readable name and "running".
2. The worker reports progress; the row's line changes within a second.
3. Click open; watch the worker live in its own session.
4. Type into it; it changes course. Jim's chat has not changed.
5. Reload the page; the side panel rows and the open worker come back correct.
6. Open a finished worker from yesterday; full history including a failed tool call.
7. Try `delegate` with `async=false` from an agent; the tool rejects it.
8. Delegate at the cap; the row says "queued", then "running".

## Definition of done

1. *Code correct and tested:* vitest groups green; Playwright 14 green; `verify-contracts` green.
2. *Reachable:* holdout 1–5 and 8 executed in the real UI by the operator.
