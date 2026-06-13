# Sprint H — Subagent Collapsed-Block UI (BACKLOG #7)

**Status:** Revised · 2026-04-20 (after grill-spec review, review file: `sprint-h-subagent-block-spec-review.md`)
**Branch target:** `sprint-h-subagent-block`
**Depends on:** none — adds new schema field (no backward compat), new frame types, new payload fields.
**Related:** Sprint I (historical replay fidelity) consumes the same schema; I1 reads Sprint H's invariants.

## Prior Decisions Reversed In This Sprint

- **Plan 3 §1 / temporal-puzzling-melody.md §4 Axis-1** ("subagent grandchildren: allowed, unlimited depth, budget-only caps") is **formally reversed** in this sprint. Rationale: owner decision (2026-04-20) — "unlimited grandchildren is not an option; one level only for general subagents; we will improve that in the future." The existing test `pkg/tools/spawn_grandchild_test.go::TestSubagentCanSpawnGrandchild` asserts the reversed behavior; as part of H1's scope, it is inverted in the same PR that lands this sprint, to assert the NEW contract (unknown-tool error when a subagent attempts `spawn`). The prior ADR reference is preserved in the commit message as "reversed".

## Prior Decisions Confirmed

- **No backward compatibility for historical sessions.** Owner decision (2026-04-20) — existing `$OMNIPUS_HOME/sessions/*` may be wiped at install. Sprint H ships forward-only; there is no scenario, no test, and no code path for pre-Sprint-H persisted sessions.

## Context

Today when an agent uses the `spawn` tool to delegate a sub-task, the result comes back as a single `tool_call_result` frame with a large text blob. The SPA renders it with `ToolCallBadge` — one badge per spawn call, collapsed Result pane holding the entire sub-turn transcript as unstructured text. Users cannot tell which tools ran inside the sub-turn, with what params, or how long each took.

The existing Playwright spec `tests/e2e/handoff.spec.ts (b)` probes for `[data-testid="subagent-collapsed"]` and `[data-testid="subagent-expanded"]`. These elements do not exist. The spec times out.

This sprint:

1. Introduces a **subagent span** at the wire layer (`subagent_start` / `subagent_end` WS frames) tied to agent-loop events `EventKindSubTurnSpawn` / `EventKindSubTurnEnd`, so the frontend can reconstruct the nested sub-turn.
2. Adds `ParentToolCallID` to `TranscriptEntry.ToolCall` so nested tool calls are grouped under their parent spawn on disk.
3. Adds `ParentSpawnCallID` to the agent-loop events `ToolExecStartPayload` and `ToolExecEndPayload`, populated from sub-turn state, so the WebSocket forwarder can tag each outbound frame with its parent.
4. **Forbids grandchildren** at the tool-registry level: a subagent's tool registry does not contain `spawn` (and does not contain `handoff`) — they cannot even see those tools.
5. Ships a new React component `SubagentBlock` with the ToolCallBadge visual grammar — collapsed = live one-line status ("Subagent · label · working · 3 steps"), expanded = mini-transcript (nested ToolCallBadges + final result).
6. Adds Playwright E2E tests against a live gateway.

**Success looks like:** a user sees `Subagent · research repo structure · 7 steps · 4.2s`, clicks to expand, and sees the exact sequence of tool calls the subagent made, identical in fidelity to what they see for the main agent's own tool calls. A subagent attempting to call `spawn` or `handoff` gets an "unknown tool" error from the LLM because those tools aren't registered for it.

## Glossary (pinned for this sprint)

- **sub-turn** — the agent-loop concept; one nested run of `pkg/agent/subturn.go::spawnSubTurn`.
- **spawn tool** — `pkg/tools/spawn.go::SpawnTool`, the LLM-exposed API that triggers a sub-turn.
- **span** — UI/wire concept; one `subagent_start`…`subagent_end` pair bracketing the frames for a single sub-turn.
- **span_id** — `"span_" + <spawn tool call's ToolCall.ID>`. Deterministic. Derivable from persisted data. Enables Sprint I's replay-time reconstruction.

## Hard Constraints

- **One invariant chain for correlation:** `ToolCall.ID` (persisted) == WS `call_id` (wire) == `ToolExecStartPayload.ToolCallID` (event). This existing invariant is the anchor for all new correlation work. `ToolCall.ParentToolCallID` on a nested call references the parent spawn's `ToolCall.ID` via this chain.
- **Span lifecycle is tied to sub-turn events, not tool return.** `subagent_start` emits on `EventKindSubTurnSpawn`; `subagent_end` emits on `EventKindSubTurnEnd` (or `…Orphan`). It does NOT emit on the spawn tool's `Execute`/`ExecuteAsync` return (which completes in ms while the sub-turn may run for minutes).
- **Tool-registry filter is centralized.** The ONLY place a sub-turn's `ToolRegistry` is constructed is `pkg/agent/subturn.go`; that construction uses a new `ToolRegistry.CloneExcept(names ...string)` method to omit `spawn` and `handoff`.
- **Forward-only schema.** Additive fields only (`ParentToolCallID` on `ToolCall`, `ParentSpawnCallID` on event payloads). No migrations. No backward-compat logic for pre-Sprint-H sessions (per owner decision).

## Available Reference Patterns

| File | Pattern | Maps to |
|---|---|---|
| `src/components/chat/ToolCallBadge.tsx` | Collapsible bordered block with header + status pill + caret + expanded detail pane. | `SubagentBlock.tsx` reuses the shell structure and the same status set (`running` / `success` / `error` / `cancelled`). |
| `src/components/chat/AgentLabel.tsx` | Small labeled tag with `data-testid`, compact typography. | Reference for tag styling on nested messages inside the span. |
| `pkg/tools/handoff.go` (Sprint G, 1-level enforcement via `isHandoffActive` callback) | Callback-based refusal at Execute time. | Precedent for scoped tool constraints. Sprint H uses the stronger registry-filter approach instead (tool absent, not refused at execute). |
| `pkg/gateway/websocket.go::eventForwarder` | How the backend streams live frames to the SPA. | Extended to read `ParentSpawnCallID` from event payloads and tag outbound `tool_call_*` frames with `parent_call_id`. |

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|---|---|---|
| `pkg/session/daypartition.go::ToolCall` | extends | Add `ParentToolCallID string \`json:"parent_tool_call_id,omitempty"\``. |
| `pkg/agent/events.go::ToolExecStartPayload` / `ToolExecEndPayload` | extends | Add `ParentSpawnCallID string` field. |
| `pkg/agent/loop.go` tool-exec emit sites (~3685, ~3815, ~4074) | modifies | Populate `ParentSpawnCallID` from `turnState.parentSpawnCallID`. |
| `pkg/agent/subturn.go::spawnSubTurn` | modifies | Sets child `turnState.parentSpawnCallID` = parent spawn's `ToolCall.ID`; uses `ToolRegistry.CloneExcept("spawn", "handoff")` when constructing child registry. |
| `pkg/tools/registry.go::ToolRegistry` | extends | Add method `CloneExcept(names ...string) *ToolRegistry`. |
| `pkg/agent/events.go` | extends | Add `EventKindSubTurnSpawn` emission on sub-turn start and ensure `EventKindSubTurnEnd` emission on sub-turn end — both carry `SpanID`, `ParentSpawnCallID`, `TaskLabel`, `AgentID`, `Status`, `DurationMS`. |
| `pkg/gateway/websocket.go::eventForwarder` | modifies | New frame types `subagent_start` / `subagent_end`. Propagates `parent_call_id` on `tool_call_*` frames from the payload's `ParentSpawnCallID`. |
| `pkg/tools/spawn_grandchild_test.go` | inverts | Asserts the NEW contract: unknown-tool error when a subagent calls `spawn`. Header comment cites reversal of Plan 3 §1. |
| `src/lib/ws.ts` frame types | extends | `WsSubagentStartFrame`, `WsSubagentEndFrame`. `parent_call_id` optional field on `WsToolCallStartFrame` / `WsToolCallResultFrame`. |
| `src/store/chat.ts` | extends | Groups frames by `parent_call_id` into a span record. Tolerates out-of-order arrival (buffer until `subagent_start` arrives; drop to flat after 10s). |
| `src/components/chat/SubagentBlock.tsx` | new | UI component. |
| `src/components/chat/ChatScreen.tsx::AssistantMessage` | modifies | Routes to `SubagentBlock` for spans; `ToolCallBadge` for un-grouped calls. |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents | Indirect Dependents |
|---|---|---|---|
| `ToolCall` struct (add `ParentToolCallID`) | **LOW** | JSONL reader/writer (additive field); transcript tests | Sprint I's replay |
| `ToolExecStartPayload` / `ToolExecEndPayload` (add `ParentSpawnCallID`) | **MEDIUM** | 3 emit sites in `pkg/agent/loop.go`, all tests using these payloads | `eventForwarder` in `websocket.go` |
| `ToolRegistry.CloneExcept` | **LOW** | `pkg/agent/subturn.go`, unit tests | — |
| `pkg/agent/subturn.go::spawnSubTurn` | **MEDIUM** | Spawn + subagent tool paths | Every existing subagent/spawn test |
| `pkg/agent/events.go` (subturn events) | **MEDIUM** | Agent-loop lifecycle | WS forwarder |
| Chat-store frame routing | **MEDIUM** | All WS frame handlers | Live streaming regression |
| `spawn_grandchild_test.go` (INVERSION) | **HIGH** | That single test file | The test's presence is a contract — inverting it declares the reversal of Plan 3 §1 |

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| `SpawnTool.Execute[Async]` → `SubagentManager` / `SubTurnSpawner.SpawnSubTurn` → agent-loop sub-turn → `EventKindSubTurnSpawn`/`…End` | Bracket with `subagent_start`/`_end`. Child tool calls fire `ToolExecStartPayload` with `ParentSpawnCallID` set. |
| WS send path: `wc.sendCh <- data` | Unchanged. New frame types use the same channel. |
| Frontend receive path: `WsConnection.onFrame` → `chat.ts` reducer | Extend reducer with `subagent_*` cases and `parent_call_id`-aware `tool_call_*` grouping. |

## Behavioral Contract

- When the parent agent invokes `spawn`, the chat renders a `SubagentBlock` in `running` state as soon as the `subagent_start` frame arrives (which fires on `EventKindSubTurnSpawn`).
- While the sub-turn runs, the collapsed header's step count updates **+1 on every `tool_call_start` frame carrying matching `parent_call_id`**. The count does not decrement or re-count.
- When the user clicks the collapsed header, the block expands to reveal the mini-transcript: each nested tool call as a `ToolCallBadge`, plus the sub-turn's final result string at the bottom (distinguishable from tool calls).
- When the sub-turn finishes, the block's header updates to the terminal status (`success` / `error` / `cancelled`) on `subagent_end`. The block stays collapsed if the user never expanded it.
- When a subagent tries to call `spawn` OR `handoff`, its LLM receives the normal "unknown tool" error response because those tools are absent from its registry. No nested span is created.
- The parent chat stream contains **exactly one** SubagentBlock per sub-turn, regardless of nested tool call count.
- If a sub-turn orphans (parent turn finishes first), the block resolves to terminal state within a 5-second grace period, status `interrupted`. A structured log entry is emitted.

## Explicit Non-Behaviors

- **No nested SubagentBlocks.** Grandchildren are impossible by construction; encountering a span inside a span is a bug.
- **No retroactive subagent spans.** Pre-Sprint-H sessions get wiped (owner decision); no migration.
- **No streaming of subagent intermediate assistant text.** Only the sub-turn's **final result string** is forwarded to the frontend (as part of `subagent_end` or via the parent's existing mechanism for `ExecuteAsync` result delivery). Intermediate token streams from the sub-turn are NOT routed to the frontend in this sprint. Documented deferral.
- **No raw provider messages or system prompts** leak to the frontend.
- **No blocking of the parent turn** on sub-turn completion. `ExecuteAsync` semantics preserved — the parent turn proceeds; the span resolves when `EventKindSubTurnEnd` arrives, even if the parent's own `done` already fired.
- **No >100 step edge case handling.** Sub-turns are capped at `maxIterations = 10` in the existing code path; large counts aren't realistic. YAGNI.

## Integration Boundaries

- **LLM provider API.** No change. Subagents receive their own Messages slice; only the `tools` array differs (no `spawn`, no `handoff`).
- **Transcript JSONL files.** Additive field `parent_tool_call_id`. Readers that don't know about it ignore it.
- **WebSocket protocol.** Two new frame types (`subagent_start`, `subagent_end`) + one optional field (`parent_call_id`) on existing `tool_call_*` frames.

---

## User Stories

### US-1 · P0 · Live subagent visibility

**Narrative:** When Max delegates "research repo structure" to a subagent, the user sees a single collapsed block in the chat — `Subagent · research repo structure · working · 0 steps` — that updates as the sub-turn progresses.

**Why this priority:** The entire purpose of the feature.

**Independent test:** Trigger a spawn via a scripted scenario prompt; assert `[data-testid="subagent-collapsed"]` appears within 5s, shows step-count text, updates at least once, resolves to a terminal status within timeout.

**Acceptance Scenarios:**

1. **Given** the agent calls `spawn(task:"list *.go files", label:"audit go files")`, **When** the user looks at the chat, **Then** a block with `data-testid="subagent-collapsed"` appears containing the task label.
2. **Given** the sub-turn has fired 3 `tool_call_start` events with matching `parent_call_id`, **When** the user reads the collapsed header, **Then** the step counter shows `3 steps`.
3. **Given** the sub-turn has finished successfully, **When** the header renders, **Then** it shows a success icon and duration (e.g., `4.2s`).
4. **Given** the sub-turn failed, **When** the header renders, **Then** it shows an error icon; the block still expands on click.
5. **Given** the parent turn finished before the sub-turn, **When** `subagent_end` eventually arrives (or the 5s grace elapses), **Then** the block resolves to terminal state.

### US-2 · P0 · Subagent expansion reveals the mini-transcript

**Narrative:** Users drill into a sub-turn to understand exactly what it did — which tools, with what parameters, returning what.

**Why this priority:** Opacity was the original complaint (#7).

**Independent test:** Click a `subagent-collapsed` block → assert `[data-testid="subagent-expanded"]` appears with ≥1 `ToolCallBadge` inside.

**Acceptance Scenarios:**

1. **Given** a collapsed block with 2 nested tool calls, **When** the user clicks the header, **Then** `[data-testid="subagent-expanded"]` appears with 2 `tool-call-badge` elements.
2. **Given** the expanded region, **When** the user inspects it, **Then** each badge shows its tool name, status, and duration.
3. **Given** the sub-turn produced a final result string, **When** the region expands, **Then** the result appears at the bottom of the expanded area, visually distinguishable from the tool-call badges.

### US-3 · P0 · Grandchildren forbidden at the registry

**Narrative:** A subagent cannot spawn a grandchild — `spawn` (and `handoff`) are absent from its tool registry. Attempting to use them returns an "unknown tool" error from the tool dispatcher.

**Why this priority:** Owner decision (2026-04-20). Reverses Plan 3 §1.

**Independent test:** Unit — construct sub-turn registry; `registry.Get("spawn") == nil`, `registry.Get("handoff") == nil`. Integration — sub-turn with a scenario provider scripted to call `spawn`; assert unknown-tool error result; assert no new `subagent_start` frame.

**Acceptance Scenarios:**

1. **Given** a sub-turn's tool registry is constructed via `CloneExcept("spawn","handoff")`, **When** queried, **Then** both are absent.
2. **Given** the sub-turn's LLM emits a tool call for `spawn`, **When** the dispatcher processes it, **Then** it returns an unknown-tool error to the LLM and no grandchild span is created.
3. **Given** the chain Parent → Subagent → (attempted grandchild), **When** the run ends, **Then** the parent's transcript contains exactly one spawn entry and zero subagent-nested `ParentToolCallID` chains below depth 1.

### US-4 · P1 · Live step counter without expansion

**Narrative:** A user watching the chat during a slow sub-turn sees the step counter increment visibly without needing to expand.

**Why this priority:** Partially delivers the "chat goes quiet" UX complaint.

**Independent test:** Observe the collapsed header's step count changing at least twice during a multi-step sub-turn.

**Acceptance Scenarios:**

1. **Given** a sub-turn that fires 5 `tool_call_start` frames with matching `parent_call_id`, **When** the run progresses, **Then** the collapsed header's step count increments +1 per `tool_call_start`.
2. **Given** the run is ongoing, **When** the header spinner is visible, **Then** the user has not yet expanded the block.

### US-5 · P2 · Keyboard + a11y baseline

**Narrative:** `SubagentBlock` header is a focusable button with `aria-expanded`, Enter/Space toggles expansion, screen readers announce "Subagent, <task label>, <N steps>".

**Why this priority:** P2 baseline; inherited from ToolCallBadge precedent.

**Independent test:** Run axe (WCAG 2.1 AA) against a page containing both collapsed and expanded blocks; assert zero new violations relative to `main`'s axe baseline.

**Acceptance Scenarios:**

1. **Given** a `SubagentBlock` is rendered, **When** the user tabs to it, **Then** focus lands on the header button.
2. **Given** focus is on a collapsed block, **When** the user presses Enter or Space, **Then** `aria-expanded="true"`.
3. **Given** expansion completed, **When** axe runs at WCAG 2.1 AA, **Then** zero new violations against elements matching `[data-testid^="subagent-"]`.

## Edge Cases

- **Subagent returns empty result.** Expanded body shows the step list but no final-result section.
- **Sub-turn cancelled mid-run.** `subagent_end{status:"cancelled"}` arrives; block shows cancelled icon; expanded body shows partial steps, no final result.
- **Network disconnect during live sub-turn.** On WS reconnect there is no dedicated span replay; the block may remain in `running` state until the session's next live event or until the 5-second orphan grace resolves it. Documented MVP behavior.
- **Orphan sub-turn (parent done first).** 5-second grace then `subagent_end{status:"interrupted"}` emitted by the gateway's timeout watchdog. Structured log entry.
- **Two sibling spawns in one parent message.** Each renders as its own `SubagentBlock` in the order their `subagent_start` frames arrived.
- **Out-of-order frames.** `tool_call_start{parent_call_id:"c1"}` arriving BEFORE `subagent_start{parent_call_id:"c1"}` is buffered in the reducer for up to 10s. If `subagent_start` still hasn't arrived, the buffered tool call is released as a flat `ToolCallBadge` and the reducer emits a dev-console warning.
- **Label truncation.** Collapsed header shows `label` if the spawn's `Parameters.label` is set; else `task.slice(0, 60) + (task.length > 60 ? "…" : "")`. Character count, not byte count. Graceme-safe via `Array.from()` for emoji/CJK before truncating.

## BDD Scenarios

### Scenario 1 — Collapsed block appears on sub-turn spawn (Happy Path)

```gherkin
Traces to: US-1 / AS-1
Given the chat view is mounted on a live session
And the assistant issues a spawn tool call with call_id="c1" and label="audit go files"
When the backend fires EventKindSubTurnSpawn and the gateway emits
    subagent_start{span_id:"span_c1", parent_call_id:"c1", task_label:"audit go files", agent_id:"max"}
Then the message view renders an element [data-testid="subagent-collapsed"]
And the header text contains "audit go files"
And the header shows a running spinner
```

### Scenario 2 — Step counter increments on tool_call_start (Happy Path)

```gherkin
Traces to: US-1 / AS-2, US-4 / AS-1
Given a SubagentBlock in running state with step counter "0 steps"
When the backend emits tool_call_start{call_id:"t1", parent_call_id:"c1"}
Then the header step counter shows "1 step"
When the backend emits tool_call_start{call_id:"t2", parent_call_id:"c1"}
Then the header step counter shows "2 steps"
```

### Scenario 3 — Terminal success status (Happy Path)

```gherkin
Traces to: US-1 / AS-3
Given a SubagentBlock with 3 recorded steps
When the backend emits subagent_end{span_id:"span_c1", status:"success", duration_ms:4210}
Then the header shows a success icon
And the header shows "4.2s"
```

### Scenario 4 — Expansion reveals nested tool calls (Happy Path)

```gherkin
Traces to: US-2 / AS-1, US-2 / AS-2
Given a collapsed SubagentBlock with span_id="span_c1" and 2 nested tool calls
When the user clicks the collapsed header
Then an element [data-testid="subagent-expanded"] is rendered
And the expanded region contains 2 elements with data-testid="tool-call-badge"
And each ToolCallBadge displays its tool name
```

### Scenario 5 — Final result rendered at bottom of expanded body (Happy Path)

```gherkin
Traces to: US-2 / AS-3
Given a SubagentBlock whose sub-turn finished with final_result="Found 12 Go files"
When the user expands it
Then the expanded region contains the 2 nested tool-call-badges
And below them, a section labeled "Final result" containing "Found 12 Go files"
```

### Scenario 6 — Sub-turn failure (Error Path)

```gherkin
Traces to: US-1 / AS-4
Given a SubagentBlock in running state
When the backend emits subagent_end{span_id:"span_c1", status:"error", duration_ms:1200}
Then the header shows an error icon
And the header still responds to click and expands to show accumulated steps
```

### Scenario 7 — Orphan sub-turn resolves via grace period (Error Path)

```gherkin
Traces to: US-1 / AS-5, Edge (orphan)
Given a SubagentBlock in running state
And the parent's `done` frame has arrived
When 5 seconds elapse with no subagent_end
Then the gateway's orphan watchdog emits subagent_end{span_id:"span_c1", status:"interrupted"}
And the block header shows an interrupted/cancelled-style icon
And a structured log entry is emitted at slog.Warn with span_id, parent_call_id
```

### Scenario 8 — Out-of-order frames (Edge)

```gherkin
Traces to: Edge (out-of-order)
Given a tool_call_start{parent_call_id:"c1"} arrives before subagent_start{parent_call_id:"c1"}
When 500ms later the subagent_start arrives
Then the previously-buffered tool call is attached to the span
And the SubagentBlock shows "1 step" when rendered
```

### Scenario 9 — Grandchildren forbidden at registry (Error Path)

```gherkin
Traces to: US-3 / AS-1
Given a sub-turn ToolRegistry is constructed via parent.CloneExcept("spawn","handoff")
When the test asserts registry.Get("spawn") and registry.Get("handoff")
Then both are nil
And the subagent's LLM prompt's tools[] array contains neither
```

### Scenario 10 — Grandchild attempt returns unknown-tool (Error Path)

```gherkin
Traces to: US-3 / AS-2, US-3 / AS-3
Given a subagent sub-turn is running
When the sub-turn's scenario provider emits a tool call with name="spawn"
Then the tool dispatcher returns an unknown-tool error to the LLM
And no subagent_start frame with a grandchild parent_call_id is emitted
And the parent's transcript ToolCalls slice contains exactly one spawn entry
```

### Scenario 11 — a11y axe-clean (Happy Path)

```gherkin
Traces to: US-5 / AS-3
Given a chat screen rendered with both an expanded and a collapsed SubagentBlock
When axe runs at WCAG 2.1 AA
Then zero new violations are reported on elements matching [data-testid^="subagent-"]
```

### Scenario 12 — Keyboard expansion (Happy Path)

```gherkin
Traces to: US-5 / AS-1, US-5 / AS-2
Given focus is on a collapsed SubagentBlock's header button
When the user presses Enter
Then aria-expanded is "true"
And [data-testid="subagent-expanded"] is visible
```

### Scenario 13 — Two sibling spawns (Edge)

```gherkin
Traces to: Edge (sibling spawns)
Given the assistant emits two spawn frames with call_ids c1 then c2
When the chat renders the message
Then two distinct SubagentBlock elements appear, in the order (c1, c2)
And each expands independently without affecting the other
```

### Scenario 14 — Label truncation boundary (Edge)

```gherkin
Traces to: Edge (label truncation)
Given spawn has no Parameters.label and task="<exactly-60-char-string>"
When the SubagentBlock header renders
Then the header text contains the full 60-char task with no ellipsis

Given spawn has no Parameters.label and task="<61-char-string>"
When the SubagentBlock header renders
Then the header text contains the first 60 chars followed by "…"
```

## TDD Plan

| Order | Test Name | Level | Traces to BDD | Description |
|---|---|---|---|---|
| 1 | `TestToolCall_JSONRoundtrip_WithParentToolCallID` | Unit (Go) | schema invariant | Round-trip `ToolCall{ParentToolCallID:"c1"}` through JSON — field preserved, omitempty when empty. |
| 2 | `TestToolRegistry_CloneExcept_OmitsNamed` | Unit (Go) | Scenario 9 | `CloneExcept("spawn","handoff")` produces a registry without those tools but with all other siblings. |
| 3 | `TestSubTurn_ChildRegistry_OmitsSpawnAndHandoff` | Unit (Go) | Scenario 9 | `spawnSubTurn` calls `CloneExcept("spawn","handoff")`; resulting registry verified. |
| 4 | `TestToolExecStartPayload_CarriesParentSpawnCallID` | Unit (Go) | Scenarios 2, 4 | When a sub-turn's tool call fires, the `ToolExecStartPayload.ParentSpawnCallID` equals parent spawn's `ToolCall.ID`. |
| 5 | `TestSpawn_SubTurnStart_EmitsSubagentStart` | Unit (Go) | Scenario 1 | `EventKindSubTurnSpawn` causes the gateway to emit `subagent_start{span_id:"span_"+parent_call_id, parent_call_id, task_label, agent_id}`. |
| 6 | `TestSpawn_SubTurnEnd_EmitsSubagentEnd` | Unit (Go) | Scenarios 3, 6 | `EventKindSubTurnEnd` causes emission of `subagent_end{span_id, status, duration_ms}`. Status reflects sub-turn outcome. |
| 7 | `TestSpawn_OrphanSubTurn_EmitsInterruptedAfter5s` | Integration (Go) | Scenario 7 | Parent `done` fires; no `EventKindSubTurnEnd` arrives; gateway watchdog emits `subagent_end{status:"interrupted"}` after 5s and logs at Warn. |
| 8 | `TestSubTurn_GrandchildAttempt_ReturnsUnknownTool` | Integration (Go) | Scenario 10 | Sub-turn with a scenario provider scripted to call `spawn` → unknown-tool error; no new `subagent_start` emitted. |
| 9 | `TestSpawn_PersistsParentToolCallIDOnChildren` | Integration (Go) | schema invariant | After a spawn run, `ReadTranscript` returns entries where nested tool calls have `ParentToolCallID == <parent spawn's ToolCall.ID>`. |
| 10 | `TestSpawn_GrandchildTest_Inverted` | Unit (Go) | Plan 3 §1 reversal | The rewritten `spawn_grandchild_test.go` asserts unknown-tool error when a sub-turn calls `spawn`, NOT that grandchildren execute. Header comment documents the reversal. |
| 11 | `ChatStore_GroupsFramesBySpan` | Unit (TS) | Scenarios 2, 4, 5, 8 | Feed subagent_start + nested tool_call_start/result (both in-order AND out-of-order) + subagent_end into the reducer; assert span is populated correctly in both cases. |
| 12 | `ChatStore_OrphanFrame_FallsBackFlat` | Unit (TS) | Edge (out-of-order) | `tool_call_start{parent_call_id:"x"}` with no matching `subagent_start` within 10s → rendered flat; dev-console warning emitted. |
| 13 | `SubagentBlock_Collapsed_LiveStepCounter` | Component (TS, RTL) | Scenarios 1, 2 | Props update step count → DOM updates; spinner visible while `status==="running"`. |
| 14 | `SubagentBlock_Expanded_NestedToolCallsInOrder` | Component (TS, RTL) | Scenario 4 | 2 nested tool calls → 2 ToolCallBadges in stored order. |
| 15 | `SubagentBlock_Expanded_FinalResult` | Component (TS, RTL) | Scenario 5 | Final result renders in a dedicated section at the bottom. |
| 16 | `SubagentBlock_TerminalStatuses` | Component (TS, RTL) | Scenarios 3, 6, 7 | success/error/interrupted each render the correct icon and keep the block expandable. |
| 17 | `SubagentBlock_a11y_WCAG21_AA` | Component (TS, RTL + axe) | Scenario 11 | axe against both states — zero new violations on `[data-testid^="subagent-"]`. |
| 18 | `SubagentBlock_Keyboard_EnterAndSpace` | Component (TS, RTL) | Scenario 12 | Enter AND Space on header toggle `aria-expanded`. |
| 19 | `SubagentBlock_LabelTruncation` | Component (TS, RTL) | Scenario 14 | 60-char → full, 61-char → first 60 + "…". Emoji/CJK uses graceme count (`Array.from`). |
| 20 | `e2e: handoff.spec.ts (b)` — UNSKIP | E2E (Playwright, scenario provider) | Scenarios 1, 4 | Deterministic scenario provider triggers spawn; assert `subagent-collapsed` → click → `subagent-expanded` → ≥1 nested `tool-call-badge`. |
| 21 | `e2e: subagent.spec.ts (a) · grandchild refused` | E2E (Playwright, scenario provider) | Scenarios 9, 10 | Scenario provider scripted to make the subagent attempt `spawn`; assert unknown-tool error in transcript; zero nested `subagent-collapsed`. |
| 22 | `e2e: subagent.spec.ts (b) · sibling spawns` | E2E (Playwright, scenario provider) | Scenario 13 | Exactly 2 `subagent-collapsed`; each expands independently. |
| 23 | `e2e: subagent.spec.ts (c) · live step counter` | E2E (Playwright, scenario provider) | Scenario 2, US-4 | Step count visibly increments during the run. |
| 24 | `e2e: subagent.spec.ts (d) · real-LLM smoke` | E2E (Playwright, OpenRouter CI) | US-1 | Best-effort smoke — uses a real LLM; does NOT gate merge; only verifies no JS errors on a real spawn. |

### Test Datasets

| ID | Input shape | Expected outcome | Traces to |
|---|---|---|---|
| D1 | `ToolCall{ID:"t1", ParentToolCallID:"c1", Tool:"fs.list", Status:"success"}` | Rendered as nested ToolCallBadge inside span `span_c1` | Scenario 4 |
| D2 | `ToolCall{ID:"t2", ParentToolCallID:"c1", Tool:"shell", Status:"error"}` | Error-styled nested badge; span continues | Scenario 6 |
| D3 | `ToolCall{ID:"tx", ParentToolCallID:"c1", Tool:"spawn"}` (hypothetical — should never persist) | Cannot be produced; child registry omits spawn | Scenarios 9, 10 |
| D4 | `task="<60-char>"`, no label | Collapsed header shows all 60 chars | Scenario 14 |
| D5 | `task="<61-char>"`, no label | Collapsed header shows first 60 + "…" | Scenario 14 |
| D6 | `task="🎉".repeat(50)` (100-char visually, 200-byte) | Truncates at graceme 60, not byte 60 | Scenario 14 |
| D7 | `label="X"`, `task="<100-char>"` | Collapsed header shows "X" (label wins) | Scenario 14 |
| D8 | `ToolExecStartPayload{ToolCallID:"t3", ParentSpawnCallID:"c1"}` | Outbound WS frame has `parent_call_id:"c1"` | Scenario 2 |
| D9 | Parent turn done; sub-turn still running 5.5s later | Orphan watchdog fires `subagent_end{status:"interrupted"}`; slog.Warn emitted | Scenario 7 |

## Regression

Existing behaviors that MUST be preserved:

1. `pkg/tools/spawn_test.go` (excluding `spawn_grandchild_test.go` which is inverted) — remaining tests pass; spawn tool still works.
2. `pkg/tools/handoff_test.go` — untouched; re-run to confirm.
3. Live tool-call rendering in `AssistantMessage` — a non-spawn tool call with empty `parent_call_id` renders as a flat `ToolCallBadge`.
4. WS reconnection (existing logic) — unchanged.

New regression tests:

- `TestChatRouter_NonSpawnCall_NoSpan` — flat `tool_call_start` (no `parent_call_id`) is NOT grouped into any span.
- `TestChatRouter_MixedStream` — a span interleaved with flat tool calls in the same assistant message renders both layers correctly.

## Functional Requirements

- **FR-H-001**: `pkg/session/daypartition.go::ToolCall` MUST carry `ParentToolCallID string \`json:"parent_tool_call_id,omitempty"\``.
- **FR-H-002**: `pkg/agent/events.go::ToolExecStartPayload` and `ToolExecEndPayload` MUST carry `ParentSpawnCallID string`. All emit sites (`pkg/agent/loop.go` at tool-exec emit points) MUST populate it from `turnState.parentSpawnCallID`.
- **FR-H-003**: `pkg/agent/subturn.go::spawnSubTurn` MUST set the child `turnState.parentSpawnCallID` to the parent spawn's `ToolCall.ID` at sub-turn construction.
- **FR-H-004**: The gateway MUST emit `subagent_start` on `EventKindSubTurnSpawn` and `subagent_end` on `EventKindSubTurnEnd` (or a synthesized `interrupted` emission after the 5s orphan grace). `subagent_start` MUST carry `span_id = "span_" + parent_call_id`, `parent_call_id`, `task_label`, `agent_id`. `subagent_end` MUST carry `span_id`, `status`, `duration_ms`.
- **FR-H-005**: Live `tool_call_start` and `tool_call_result` WS frames fired inside a sub-turn MUST carry `parent_call_id` equal to the parent spawn's `ToolCall.ID` (sourced from `ParentSpawnCallID` on the event payload).
- **FR-H-006**: `pkg/tools/registry.go::ToolRegistry` MUST expose `CloneExcept(tools ...ExcludedTool) *ToolRegistry`. `spawnSubTurn` MUST use it with `ExcludedSpawn`, `ExcludedSubagent`, and `ExcludedHandoff` — all three delegation tools are excluded so grandchild spawning and agent switching are impossible from within a sub-turn.
- **FR-H-007**: `pkg/tools/spawn_grandchild_test.go` MUST be inverted in the same PR that lands this sprint: asserts unknown-tool error on subagent `spawn` call. Header comment cites the Plan 3 §1 reversal.
- **FR-H-008**: The SPA MUST render a `SubagentBlock` for every `subagent_start`…`subagent_end` bracket received from the wire. Collapsed header has `data-testid="subagent-collapsed"`; expanded body has `data-testid="subagent-expanded"`.
- **FR-H-009**: The SPA reducer MUST tolerate out-of-order frame arrival: `tool_call_start{parent_call_id:"x"}` arriving before `subagent_start{parent_call_id:"x"}` is buffered up to 10s; if the matching `subagent_start` arrives, attach to span; if the 10s elapses, release as flat and emit a dev-console warning.
- **FR-H-010**: Step count MUST increment by +1 on every `tool_call_start` frame matching `parent_call_id`, regardless of outcome. No decrement.
- **FR-H-011**: The backend MUST emit `slog.Debug` on `subagent_start` and `subagent_end` with keys `span_id`, `parent_call_id`, `agent_id`; on orphan interruption, MUST emit `slog.Warn` with the same keys.
- **FR-H-012**: The SPA MUST NOT render nested `SubagentBlock` inside a span's expanded body (impossible by construction; reducer should assert in dev mode).

## Success Criteria

- **SC-H-001**: `tests/e2e/handoff.spec.ts (b)` passes against a live gateway using the deterministic scenario-provider path.
- **SC-H-002 (deterministic)**: The new `subagent.spec.ts` suite (rows 21-23) passes in CI against the scenario provider.
- **SC-H-003 (best-effort smoke)**: `subagent.spec.ts (d)` runs against real LLM via OpenRouter CI; failures do NOT gate merge.
- **SC-H-004**: `CGO_ENABLED=0 go test -race ./pkg/agent/... ./pkg/tools/... ./pkg/session/...` green, including inverted `spawn_grandchild_test.go`.
- **SC-H-005**: `npx tsc --noEmit && npm run build` exit 0.
- **SC-H-006**: axe at WCAG 2.1 AA — zero new violations on `[data-testid^="subagent-"]` elements, measured against `main` baseline.

## Traceability Matrix

| FR | User Story | BDD Scenarios | Tests |
|---|---|---|---|
| FR-H-001 | US-1, US-2 (enabling) | 4 (via dataset) | 1, 9 |
| FR-H-002 | US-1, US-2 (enabling) | 2, 4 | 4 |
| FR-H-003 | US-1, US-2 (enabling) | 2 | 4 |
| FR-H-004 | US-1 | 1, 3, 6, 7 | 5, 6, 7 |
| FR-H-005 | US-2, US-4 | 2, 4 | 4, 20 |
| FR-H-006 | US-3 | 9 | 2, 3 |
| FR-H-007 | US-3 | 10 | 10 |
| FR-H-008 | US-1, US-2 | 1, 4 | 13, 14, 20 |
| FR-H-009 | US-1 (robustness) | 8 | 11, 12 |
| FR-H-010 | US-1, US-4 | 2 | 13, 23 |
| FR-H-011 | Observability | 7 | 7 |
| FR-H-012 | US-3 | (impossible state) | dev assert (not formally tested) |

## Ambiguity Warnings — RESOLVED

| Original ambiguity | Resolution |
|---|---|
| `span_id` format | `"span_" + parent spawn's ToolCall.ID`. Deterministic. Enables Sprint I reconstruction. |
| Expanded body scroll container | Bounded at `max-height: 400px; overflow-y: auto`. |
| Media rendering inside the span | Reuse existing `MediaPart` rendering from chat message path. No new pipeline. |
| Task label truncation | `label` if present; else `Array.from(task).slice(0, 60).join('') + (task length > 60 ? "…" : "")`. Graceme-safe. |
| Subagent intermediate assistant text streaming | Deferred. Only final result flows. Documented in Explicit Non-Behaviors. |
| Orphan sub-turn terminal state | 5s grace after parent done, then `status:"interrupted"`. |
| Step definition | +1 per `tool_call_start` matching parent_call_id. |

## Holdout Evaluation Scenarios (POST-IMPLEMENTATION — NOT IN TRACEABILITY)

- **H-E1:** New session, ask "spawn a subagent to list the 3 most recent commits". Expect: one `subagent-collapsed` with task label; expand → see a `shell` or `git` ToolCallBadge with the real command.
- **H-E2:** Adversarial — "have the subagent itself spawn another subagent". Expect: single SubagentBlock; expanded → no nested block; subagent's text contains a tool-error response.
- **H-E3:** Slow multi-step run. Expect: step count visibly increments during the run without clicking.
- **H-E4 (error):** Kill the gateway mid-sub-turn. Reload SPA. Expect: partial SubagentBlock visible; status `interrupted` (within ~5s of reload due to the orphan watchdog).
- **H-E5 (error):** Scenario provider returns malformed spawn params. Expect: main chat shows an error; no SubagentBlock.
- **H-E6 (edge):** Parent issues two spawn calls back-to-back. Expect: two sibling SubagentBlocks, each independently expandable.
- **H-E7 (edge):** Expand a block, then reload the page. Expect: reload re-renders from replay (Sprint I), block restored with same step count.

---

## Sprint Execution Plan

Three parallel agents on a single feature branch.

### H1 · backend-lead (Go)

Scope: `pkg/session/daypartition.go`, `pkg/agent/events.go`, `pkg/agent/loop.go`, `pkg/agent/subturn.go`, `pkg/tools/registry.go`, `pkg/tools/spawn.go`, `pkg/tools/spawn_grandchild_test.go` (invert), `pkg/gateway/websocket.go`, `pkg/gateway/replay.go` (extract orphan watchdog into helper).

1. Add `ParentToolCallID` field on `ToolCall`; round-trip test.
2. Add `ParentSpawnCallID` on `ToolExecStartPayload` and `ToolExecEndPayload`; update all 3 emit sites in `loop.go` to populate from `turnState.parentSpawnCallID`; unit test.
3. Add `ToolRegistry.CloneExcept(names ...string) *ToolRegistry`; unit test.
4. In `subturn.go::spawnSubTurn`: set `childTurnState.parentSpawnCallID = parent spawn's ToolCall.ID`; build child registry via `parent.Tools.CloneExcept("spawn","handoff")`; unit test child registry.
5. Emit `EventKindSubTurnSpawn` on sub-turn start and `EventKindSubTurnEnd` on sub-turn end with span payload (span_id, parent_call_id, task_label, agent_id, status, duration_ms).
6. `websocket.go::eventForwarder`: on `EventKindSubTurnSpawn` → `subagent_start` frame; on `EventKindSubTurnEnd` → `subagent_end` frame; on `ToolExecStart/EndPayload.ParentSpawnCallID != ""` → tag outbound `tool_call_*` frames with `parent_call_id`.
7. Orphan watchdog: when parent turn's `done` fires, set a 5s timer per open span; if `EventKindSubTurnEnd` hasn't arrived, synthesize `subagent_end{status:"interrupted"}`; `slog.Warn` entry.
8. **Invert `spawn_grandchild_test.go`**: rename to `TestSubagentCannotSpawnGrandchild`; assert unknown-tool error; header comment cites reversal of Plan 3 §1.
9. Structured logging per FR-H-011.

### H2 · frontend-lead (TS/React)

Scope: `src/lib/ws.ts`, `src/store/chat.ts`, `src/components/chat/SubagentBlock.tsx` (new), `src/components/chat/ChatScreen.tsx::AssistantMessage`.

1. Frame types: `WsSubagentStartFrame`, `WsSubagentEndFrame`; optional `parent_call_id` on `WsToolCallStartFrame`/`WsToolCallResultFrame`.
2. `ChatMessage` gains optional `spans: SubagentSpan[]` where each span has `{spanId, parentCallId, taskLabel, status, durationMs, steps: Array<ToolCall>, finalResult?: string}`.
3. Reducer extensions:
   - `subagent_start` → push a new span onto the current streaming assistant message.
   - `tool_call_start` with matching `parent_call_id` → attach to that span's `steps`; if no matching span yet, buffer in a transient `pendingByParentCallId: Record<string, BufferedCall[]>` with a 10s TTL.
   - `subagent_end` → finalize span's status/duration/finalResult; flush any buffered calls matching its `parent_call_id`.
   - Tool calls with empty `parent_call_id` → unchanged live behavior.
4. `SubagentBlock.tsx` (new): collapsed header (label + step count + status pill + caret, `data-testid="subagent-collapsed"`) + expanded body (`data-testid="subagent-expanded"`) with nested `ToolCallBadge`s + final result section; `max-height:400px; overflow-y:auto`.
5. `AssistantMessage` routing: render one `SubagentBlock` per span + one `ToolCallBadge` per un-grouped call.
6. Component + a11y tests (rows 11-19 of TDD).

### H3 · qa-lead (Playwright + integration)

Scope: `tests/e2e/handoff.spec.ts` (unskip test b), `tests/e2e/subagent.spec.ts` (new), a11y baseline integration.

1. Un-skip `handoff.spec.ts (b)` and rewrite against the new `data-testid`s using the scenario-provider path (deterministic).
2. New `subagent.spec.ts`:
   - `(a) grandchild refused` — scenario provider makes the sub-turn call `spawn`; assert unknown-tool error + zero nested blocks.
   - `(b) sibling spawns` — parent emits two spawn calls; assert exactly 2 SubagentBlocks.
   - `(c) live step counter` — observe counter incrementing during the run.
   - `(d) real-LLM smoke` — runs against OpenRouter CI; tolerant of LLM decisions; logs but does NOT fail if no tool calls occur.
3. axe integration: assert zero new violations on `[data-testid^="subagent-"]` against the `main` baseline.

### Sequencing & Gates

- H1 defines the wire contract (subagent frames + `parent_call_id` on tool-call frames). **H1 lands first.** H2 and H3 stack on it.
- Merge gates: `go build + vet + race test ./pkg/agent/... ./pkg/tools/... ./pkg/session/... ./pkg/gateway/...` + `npx tsc --noEmit && npm run build` + `handoff.spec.ts (b)` + `subagent.spec.ts` (a/b/c) + axe clean.
- BACKLOG: #7 moves to ✅ FIXED with SHA on merge.
