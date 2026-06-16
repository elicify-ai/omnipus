# Sprint I — Historical Chat Replay Fidelity

**Status:** Revised · 2026-04-20 (after grill-spec review, review file: `sprint-i-historical-replay-fidelity-spec-review.md`)
**Branch target:** `sprint-i-replay-fidelity`
**Depends on:** Sprint H invariants (see "Sprint H Dependencies" below).
**Related:** Sprint H (live subagent UI).

## Prior Decisions Confirmed

- **No backward compatibility.** Owner decision (2026-04-20) — existing `$OMNIPUS_HOME/sessions/*` can be wiped at install. Sprint I ships against the Sprint-H-era schema only. Scenarios about "pre-Sprint-H legacy sessions" are deleted.
- **Replay target is `UnifiedStore.ReadTranscript`** (single `transcript.jsonl` per session). `PartitionStore.ReadMessages` is a separate store not wired to the gateway; multi-day partition replay is out of scope.

## Sprint H Dependencies (specific invariants relied on)

1. `TranscriptEntry.ToolCall` carries `ParentToolCallID string` (Sprint H FR-H-001).
2. Invariant: `ToolCall.ID` is the canonical call identifier; the wire `call_id` and the payload `ToolCallID` all equal this string (pre-existing, Sprint H documents it).
3. `ParentToolCallID` holds the parent spawn's `ToolCall.ID` (Sprint H FR-H-001).
4. `span_id = "span_" + parent spawn's ToolCall.ID` (Sprint H pinned glossary).
5. WS frame types `subagent_start` / `subagent_end` + `parent_call_id` field on `tool_call_*` (Sprint H FR-H-004, FR-H-005).

If Sprint H has not landed when I1 starts, I1 can stub these (mirror the struct field locally) and rebase when H merges.

## Context

Historical sessions currently render with **lower fidelity than live sessions.** The smoking gun: `pkg/gateway/websocket.go:662-727` (`handleAttachSession` replay loop):

```go
for _, tc := range entry.ToolCalls {
    pendingToolCalls = append(pendingToolCalls, fmt.Sprintf("**%s** — %s%s", tc.Tool, status, dur))
}
// ... joined with \n, shipped as one replay_message{role:"assistant", content:"..."}
```

Every persisted tool call — with `Parameters` and `Result` intact on disk — is flattened into a markdown string and concatenated into a plain text `replay_message` frame. The SPA's `ToolCallBadge` draws from `tool_call_start` and `tool_call_result` frames only, so on replay it receives nothing to draw.

The data is fine — `pkg/session/daypartition.go::ToolCall` persists `Parameters map[string]any` and `Result map[string]any` in full. The loss is entirely at the wire layer.

**Success looks like:** open any (post-Sprint-H) session; the tool-call badges, subagent blocks, text messages, and agent labels render identically to how the session looked live.

## Hard Constraints

- **One reducer path.** The SPA handles the same frame types during live streaming and during replay. No "replay mode" branch — any divergence is a reducer bug.
- **Ordering preserved.** Events within a transcript entry emit in the same relative order they occurred live.
- **No multi-partition concerns.** `UnifiedStore.ReadTranscript` reads a single `transcript.jsonl`. Partition support, if needed, is a separate sprint.
- **No raw backward-compat handling.** Existing sessions may be wiped; no migration, no legacy-shape fallback in code.
- **Frame-size bounded.** Replay MUST NOT emit a WS frame larger than 1 MiB. Oversize results truncate with an explicit marker.
- **Register for live events BEFORE replay starts,** buffering any incoming events during replay and flushing after `done`. Prevents the "attach-during-active-turn event loss" race.

## Available Reference Patterns

| File | Pattern | Maps to |
|---|---|---|
| `pkg/gateway/websocket.go` live tool-call forwarder | How live turns emit `tool_call_start` + `tool_call_result` framed around assistant text. | Replay mirrors this sequence using persisted fields as the source. |
| `pkg/gateway/websocket.go` existing `replay_message` handling | Today's wire frame for historical text. Carries `agent_id`. | Kept as-is for text; augmented with per-tool-call frames. |
| `src/store/chat.ts` reducer cases for `tool_call_start` / `tool_call_result` | The live reducer path we share. | Verified in I2 to work correctly for replay; minor text-interleave fix (see FR-I-010). |

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|---|---|---|
| `pkg/gateway/websocket.go::handleAttachSession` replay loop (lines ~640-730) | rewrites | Extracted to `pkg/gateway/replay.go::streamReplay(ctx, entries, emitFrame)` — testable. |
| `pkg/session/unified.go::UnifiedStore.ReadTranscript` | reads | Single-file reader. Input source for replay. |
| `pkg/session/daypartition.go::TranscriptEntry`, `ToolCall` | reads | Source of truth. `ToolCall.ParentToolCallID` from Sprint H used for span reconstruction. |
| `pkg/gateway/websocket.go::wsServerFrame` | extends | `AgentID` field already present; verify it populates on tool-call frames too. |
| `pkg/gateway/websocket.go::handleAttachSession` live-event subscription | modifies | Register for live events before starting replay; buffer during replay; flush after `done`. |
| `src/lib/ws.ts` frame types | verifies | `tool_call_start/result` + `subagent_start/end` + `parent_call_id` all defined by Sprint H. No change. |
| `src/store/chat.ts` reducer | modifies | Small tweak for text-at-tool-call-start positioning during replay (FR-I-010). |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents | Indirect Dependents |
|---|---|---|---|
| `handleAttachSession` replay loop | **HIGH** | Every session reopen | `handoff.spec.ts (a)`, `message-history.test.tsx`, `chat-streaming.test.tsx`, `media.spec.ts`, `session_milestone2_test.go` |
| `chat.ts` reducer textAtToolCallStart semantics | **LOW** | Every WS frame handler | Live streaming |
| New `pkg/gateway/replay.go` extraction | **LOW** | `handleAttachSession` only | — |
| Live-event buffering during replay | **MEDIUM** | Attach-mid-active-turn scenarios | Orphan handling |

**CRITICAL path:** every session reopen goes through this loop. A regression breaks every reopen.

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| User clicks a session → WS `attach_session` → backend `handleAttachSession` → replay frames → SPA reducer builds chat view | The subject of this sprint. |
| REST `GET /api/v1/sessions/{id}/messages` (`fetchSessionMessages` / ChatThread prefetch) | Orthogonal — returns raw `TranscriptEntry[]`. Consumers have the full data. Not changed by this sprint. |

## Behavioral Contract

- On `attach_session`, the gateway registers the connection for live events immediately, then starts streaming replay frames. Any live events arriving during replay are buffered and flushed after `done`.
- For each `TranscriptEntry`:
  - `role=user`/`system` → `replay_message{role, content, agent_id}`.
  - `role=assistant` → `replay_message{role:"assistant", content, agent_id}` if content non-empty, then for each `tc in entry.ToolCalls`: `tool_call_start{tool, call_id:tc.ID, params:tc.Parameters, parent_call_id:tc.ParentToolCallID, agent_id:entry.AgentID}` then `tool_call_result{...}` with status, result, duration, agent_id, parent_call_id.
  - Spawn entries — when the replay detects a tool call whose children (later in the entry slice or later entries) carry matching `ParentToolCallID`, the replay wraps the nested frames with `subagent_start` before and `subagent_end` after.
- After all entries: one `done` frame.
- After `done`: any buffered live events flush.

## Explicit Non-Behaviors

- **No token frames.** `token` implies streaming-in-progress (isStreaming=true, cursor visible). Replaying completed messages as tokens would leave stale cursors.
- **No synthetic subagent spans from pre-Sprint-H data.** Sessions without `ParentToolCallID` chains replay as flat tool calls (which, under the no-backward-compat rule, don't exist anyway — but the code still handles a spawn call whose nested children are absent correctly).
- **No transcript mutation.** Replay is read-only.
- **No partition traversal.** Single-file `transcript.jsonl` only.
- **No frame emission during replay for entries of type `compaction`.**

## Integration Boundaries

- **WebSocket protocol.** No new frame types (Sprint H added them). Change is in which types replay emits.
- **JSONL transcript files.** Read-only.
- **REST `/sessions/{id}/messages`.** Unchanged; still returns raw `TranscriptEntry[]`.

---

## User Stories

### US-1 · P0 · Historical tool calls render as badges

**Narrative:** Reopening a past session shows the same tool-call badges, expandable, with real params and results — not a stringified markdown list.

**Why this priority:** Core regression fix.

**Independent test:** Create a session with a known tool call, reopen, assert `tool-call-badge` visible with real params/result on expand.

**Acceptance Scenarios:**

1. **Given** a persisted entry with `ToolCall{ID:"t1", Tool:"shell", Parameters:{cmd:"echo hi"}, Result:{stdout:"hi\n"}, Status:"success", DurationMS:42}`, **When** the user opens the session, **Then** a `tool-call-badge` is rendered with tool `shell` and status `success`.
2. **Given** the user expands the badge, **When** the expanded pane renders, **Then** it contains `echo hi` in Parameters and `hi\n` in Result.
3. **Given** the entry has N tool calls, **When** replayed, **Then** N badges appear in stored order.

### US-2 · P0 · Historical text and agent labels preserved

**Narrative:** Assistant text, user text, agent labels, attachments, system messages — all render identically to live.

**Why this priority:** The rewrite touches every session reopen. A subtle break is invisible in tests but obvious in production.

**Independent test:** Representative session with mixed entries; reopen; assert all layers intact.

**Acceptance Scenarios:**

1. **Given** an assistant entry with `AgentID="ray"`, **When** replayed, **Then** the rendered message has `AgentLabel` reading "ray".
2. **Given** a user entry, **When** replayed, **Then** a user message with the right content is shown.
3. **Given** a message with an attachment, **When** replayed, **Then** the media part renders.

### US-3 · P0 · Historical subagent spans render as SubagentBlocks

**Narrative:** Sessions containing Sprint-H-recorded subagent runs replay as SubagentBlocks, not flat lists.

**Why this priority:** Closes the loop with Sprint H. Without this, any subagent run older than the current session feels broken.

**Independent test:** Run a spawn live, close the session, reopen; assert `subagent-collapsed` appears with same step count as live.

**Acceptance Scenarios:**

1. **Given** an assistant entry includes a spawn `ToolCall{ID:"c1"}` and follow-up entries carry `ToolCall{ParentToolCallID:"c1"}`, **When** replayed, **Then** the SPA renders a SubagentBlock with the nested badges inside it.
2. **Given** a session with no spawn calls, **When** replayed, **Then** no SubagentBlock appears — all calls flat.
3. **Given** a spawn whose nested children had `ParentToolCallID` but the spawn entry itself never exists (orphan), **When** replayed, **Then** the orphan children render as flat badges and a warning is logged.

### US-4 · P1 · Ordering is faithful

**Narrative:** Order matches what the user saw live. Interleaved text + tool calls appear in the right positions.

**Why this priority:** Ordering bugs are invisible until a specific session looks wrong.

**Independent test:** Fixture with deterministic ordering (user → assistant text → tool X → tool Y → user → assistant); reopen; assert exact sequence.

**Acceptance Scenarios:**

1. **Given** an assistant entry with text "A" and tool calls [X, Y], **When** replayed, **Then** rendered order is: text "A", badge X, badge Y.
2. **Given** two sibling spawns in one entry (ids c1 then c2), **When** replayed, **Then** SubagentBlock for c1 appears before SubagentBlock for c2.

### US-5 · P1 · Clean seam between replay and live

**Narrative:** After replay completes, the user sends a new message and live streaming picks up without duplicated messages, and no live events from in-flight active turns are lost during the replay window.

**Why this priority:** Attach-during-active-turn is a realistic case (user opens the SPA while an agent is mid-turn). Events lost during the replay gap would appear as corruption.

**Independent test:** Start an active-turn session on the backend; attach a new client mid-turn; verify no events are lost.

**Acceptance Scenarios:**

1. **Given** replay finished, **When** the user sends a new message, **Then** the streamed response appears appended to the replayed transcript.
2. **Given** replay is in progress and the session is actively producing live events, **When** `done` arrives, **Then** the buffered live events flush in arrival order immediately after.
3. **Given** replay is in progress, **When** the user types in the input, **Then** the send button is disabled until `done` arrives (prevents out-of-order sends).

### US-6 · P2 · Large tool-call results don't break replay

**Narrative:** A session with a tool that returned a 10 MB blob still opens correctly — the badge appears, the result is truncated with a clear marker, the rest of the session is intact.

**Why this priority:** P2 — edge case but WS frame-size limits would cause hard failures if unhandled.

**Independent test:** Fixture tool call with 2 MB result; reopen; badge visible; result shows truncation marker; next entry still renders.

**Acceptance Scenarios:**

1. **Given** a `ToolCall.Result` that JSON-encodes above 1 MiB, **When** replayed, **Then** the emitted `tool_call_result` frame carries a truncated result: `{"_truncated": true, "original_size_bytes": <N>, "preview": "<first 10 KB>"}`.
2. **Given** a truncated result arrives at the SPA, **When** the badge expands, **Then** the Result pane shows the preview and an explicit "truncated — N bytes" notice.

## Edge Cases

- **Empty transcript.** Replay emits only `done`. Empty chat view.
- **Only user messages, no assistant.** Only `replay_message{role:"user"}` frames + done.
- **Very large transcript (thousands of entries, tens of thousands of tool calls).** Frames stream through `sendCh` with backpressure respected; no batching in this sprint.
- **Single tool result >1 MiB.** Truncated per FR-I-011 (below).
- **Orphan `ParentToolCallID`.** Parent spawn missing from transcript. Flat-render the child; `slog.Warn` with `parent_tool_call_id` key.
- **System role entries.** Emit as `replay_message{role:"system"}`. (The SPA either renders or hides based on existing rules.)
- **Compaction entries.** Skip (emit zero frames). Confirmed against `daypartition.go:118` — compaction entries use `Summary`, not user-facing `Content`; they are agent-loop metadata, not chat artifacts.
- **Entry with `agent_id=""`.** Emit `replay_message` without the `agent_id` field (omitempty). SPA falls back to session's active agent — same as today.
- **Attach-during-active-turn.** Live events arriving during replay are buffered and flushed after `done` (FR-I-009).
- **Duplicate `ToolCall.ID` in one transcript (writer bug).** Replay dedupes: only the LATEST entry with a given ID is emitted (FR-I-012).
- **Concurrent attach from two browsers.** Each gets its own replay snapshot; minor lag window for in-flight writes is acceptable.

## BDD Scenarios

### Scenario 1 — Historical tool call renders as badge (Happy Path)

```gherkin
Traces to: US-1 / AS-1, AS-2
Given a session persisted with an assistant entry containing ToolCall{ID:"t1", Tool:"shell", Parameters:{cmd:"echo hi"}, Result:{stdout:"hi"}, Status:"success", DurationMS:42}
When the user opens the session
Then the chat view contains an element [data-testid="tool-call-badge"] with tool name "shell"
And clicking the badge expands it
And the expanded pane shows "echo hi" under Parameters
And the expanded pane shows "hi" under Result
```

### Scenario 2 — Multiple historical tool calls in stored order (Happy Path)

```gherkin
Traces to: US-1 / AS-3, US-4 / AS-1
Given an assistant entry with content="working" and tool_calls=[{Tool:"fs.list"},{Tool:"shell"}]
When replayed
Then the rendered sequence is: text "working", badge "fs.list", badge "shell"
```

### Scenario 3 — Agent label survives replay (Happy Path)

```gherkin
Traces to: US-2 / AS-1
Given an assistant entry with agent_id="ray" and content="hello there"
When replayed
Then the rendered message contains [data-testid="agent-label"] with text "ray"
And the rendered message shows "hello there"
```

### Scenario 4 — User and system messages replay (Happy Path)

```gherkin
Traces to: US-2 / AS-2, Edge (system)
Given entries: user "what's 2+2?" and system "agent switched"
When replayed
Then a user-role message "what's 2+2?" is rendered
And a system-role message "agent switched" is handled per existing SPA rules
```

### Scenario 5 — Historical subagent span (Happy Path)

```gherkin
Traces to: US-3 / AS-1
Given an assistant entry has a spawn tool call with ToolCall.ID="c1"
And a subsequent entry has ToolCall{ParentToolCallID:"c1"} (one nested call)
When the session is replayed
Then frames emit in order: replay_message, tool_call_start{call_id:"c1", tool:"spawn"}, subagent_start{span_id:"span_c1", parent_call_id:"c1"}, tool_call_start{parent_call_id:"c1"}, tool_call_result{parent_call_id:"c1"}, subagent_end{span_id:"span_c1"}, tool_call_result{call_id:"c1"}, done
And the SPA renders one [data-testid="subagent-collapsed"] block
And expanding it reveals the one nested tool-call-badge
```

### Scenario 6 — No subagent spans when no ParentToolCallID present (Happy Path)

```gherkin
Traces to: US-3 / AS-2
Given a session whose ToolCall entries all have ParentToolCallID=""
When replayed
Then no subagent_start frames are emitted
And no SubagentBlocks are rendered
And all tool calls render as flat ToolCallBadges
```

### Scenario 7 — Orphan ParentToolCallID (Error Path)

```gherkin
Traces to: US-3 / AS-3
Given a ToolCall entry with ParentToolCallID="missing-c99"
And no ToolCall with ID="missing-c99" is present in the transcript
When replayed
Then the tool call emits as a flat tool_call_start/result pair (no subagent_start)
And slog.Warn is emitted with keys {event:"replay_orphan", session_id, parent_tool_call_id:"missing-c99"}
```

### Scenario 8 — Replay followed by live continuation (Happy Path)

```gherkin
Traces to: US-5 / AS-1
Given a session replayed to completion (done received)
When the user sends a new message
Then the new assistant response streams in below the replayed transcript
And no replayed messages are duplicated
```

### Scenario 9 — Attach during active turn: events buffered (Edge)

```gherkin
Traces to: US-5 / AS-2
Given the session is actively producing live token frames
When a new browser attaches and replay begins
Then the gateway registers the new connection for live events immediately
And live events arriving during replay are buffered
When replay emits the done frame
Then buffered events flush in arrival order
```

### Scenario 10 — Input disabled during replay (Happy Path)

```gherkin
Traces to: US-5 / AS-3
Given replay is in progress
When the user types in the message input
Then the send button is disabled
When replay's done frame arrives
Then the send button becomes enabled
```

### Scenario 11 — Large tool result truncation (Edge)

```gherkin
Traces to: US-6 / AS-1, AS-2
Given an assistant entry with a ToolCall whose JSON-encoded Result is 2 MiB
When replayed
Then the emitted tool_call_result frame contains result={"_truncated": true, "original_size_bytes": <2_097_152>, "preview": "<first 10240 bytes>"}
And the WS frame itself is below 1 MiB
And the SPA badge's Result pane shows the preview and a "truncated" notice
```

### Scenario 12 — Empty transcript (Edge)

```gherkin
Traces to: Edge (empty)
Given a session with zero transcript entries
When the user opens it
Then a done frame is emitted with no preceding replay frames
```

### Scenario 13 — Duplicate ToolCall.ID dedup (Edge)

```gherkin
Traces to: Edge (duplicate id)
Given a transcript contains two ToolCall entries both with ID="t1"
When replayed
Then only the latest-occurring entry emits its tool_call_start/result pair
And slog.Warn is emitted with keys {event:"replay_duplicate_tool_call_id", tool_call_id:"t1"}
```

### Scenario 14 — Compaction entry is not visible (Alternate Path)

```gherkin
Traces to: Edge (compaction)
Given a transcript contains an entry with Type=EntryTypeCompaction
When replayed
Then zero frames are emitted for that entry
```

### Scenario 15 — Agent ID parity between live and replay (Happy Path)

```gherkin
Traces to: US-2 generalized, FR-I-008
Given a tool call fires in a live turn with agent_id="ray"
When that tool call is persisted and the session is later replayed
Then the replay's tool_call_start frame carries agent_id="ray"
And the replay's tool_call_result frame carries agent_id="ray"
```

## TDD Plan

| Order | Test Name | Level | Traces to BDD | Description |
|---|---|---|---|---|
| 1 | `TestStreamReplay_Extracted_TestableSignature` | Unit (Go) | n/a | `streamReplay(ctx, entries, func(wsServerFrame) error) error` can be called with a slice-backed emitter for assertion. |
| 2 | `TestReplay_SingleToolCall_EmitsStartAndResult` | Unit (Go) | Scenario 1 | Single assistant entry with one tool call → `[replay_message, tool_call_start, tool_call_result, done]`. |
| 3 | `TestReplay_MultipleToolCalls_PreservesOrder` | Unit (Go) | Scenario 2 | Entry with [X, Y] → X's frames before Y's. |
| 4 | `TestReplay_Params_And_Result_Fidelity` | Unit (Go) | Scenario 1 | Emitted frame's params/result match disk bit-for-bit. |
| 5 | `TestReplay_UserEntry_EmitsReplayMessage` | Unit (Go) | Scenario 4 | User entry → exactly one `replay_message{role:"user"}`. |
| 6 | `TestReplay_AssistantWithAgentID` | Unit (Go) | Scenario 3, 15 | `agent_id` flows through. |
| 7 | `TestReplay_ToolCall_CarriesAgentID` | Unit (Go) | Scenario 15 | Tool-call frames also carry `agent_id`. |
| 8 | `TestReplay_SpawnSpan_Synthesizes_StartEnd` | Unit (Go) | Scenario 5 | Spawn with nested ParentToolCallID → `subagent_start` … `subagent_end` wrapping nested frames. |
| 9 | `TestReplay_NoSpawnSpans_WhenNoChildren` | Unit (Go) | Scenario 6 | No `ParentToolCallID` anywhere → no `subagent_*` frames. |
| 10 | `TestReplay_OrphanParentToolCallID_Warns` | Unit (Go) | Scenario 7 | Orphan renders flat + `slog.Warn`. |
| 11 | `TestReplay_DuplicateCallID_EmitsLatestOnly` | Unit (Go) | Scenario 13 | Dedup on `ToolCall.ID`; `slog.Warn`. |
| 12 | `TestReplay_CompactionEntry_Skipped` | Unit (Go) | Scenario 14 | Zero frames for compaction. |
| 13 | `TestReplay_EmptyTranscript_JustDone` | Unit (Go) | Scenario 12 | Empty → just `done`. |
| 14 | `TestReplay_OversizedResult_Truncates` | Unit (Go) | Scenario 11 | 2 MiB result → frame has `{_truncated:true, original_size_bytes, preview}`, frame < 1 MiB. |
| 15 | `TestReplay_CtxCancelled_StopsCleanly` | Integration (Go) | non-functional | Ctx cancel mid-replay — no leak, no panic. |
| 16 | `TestAttach_RegistersLiveEventsBeforeReplay` | Integration (Go) | Scenario 9 | Sequence: register → stream replay with buffering → done → flush buffered. |
| 17 | `TestAttach_StartLogged` / `TestAttach_EndLogged` | Unit (Go) | FR-I-013 | `slog.Info` at start with `{session_id, entry_count_loaded, tool_call_count_loaded, span_count_detected}`; at end with `{frames_emitted, duration_ms}`. |
| 18 | `ChatStore_ReplaySequence_MatchesLiveSequence` | Unit (TS) | Scenario 1, 5, 15 | Run the same logical sequence through the reducer once as live frames, once as replay frames — resulting state equal (modulo `isStreaming`). |
| 19 | `ChatStore_ReplayMessageThenToolCall_InterleavesCorrectly` | Unit (TS) | Scenario 2, FR-I-010 | Assistant text with tool call in the middle — badge position matches live interleave. |
| 20 | `ChatScreen_Replay_RendersToolCallBadge` | Integration (TS, RTL + MSW) | Scenario 1 | Mock WS replay of one tool call → DOM has a ToolCallBadge. |
| 21 | `ChatScreen_Replay_RendersSubagentBlock` | Integration (TS, RTL) | Scenario 5 | Mock replay of a spawn span → DOM has `subagent-collapsed`; expand → nested badges. |
| 22 | `ChatScreen_Replay_SendDisabled` | Integration (TS, RTL) | Scenario 10 | Send button disabled until `done`. |
| 23 | `e2e: replay-fidelity.spec.ts (a) tool-call fidelity` | E2E (Playwright) | Scenarios 1, 2, 3 | Live gateway + scenario provider: run a session with known tool calls, close, reopen, assert badges match live. |
| 24 | `e2e: replay-fidelity.spec.ts (b) subagent span` | E2E (Playwright) | Scenario 5 | Requires Sprint H merged. Round-trip a spawn through persist-and-replay; SubagentBlock visible with correct step count. |
| 25 | `e2e: replay-fidelity.spec.ts (c) attach-during-active` | E2E (Playwright) | Scenario 9 | Backend is producing live events when a new browser attaches — no events lost across the replay boundary. |
| 26 | `e2e: replay-fidelity.spec.ts (d) live continuation` | E2E (Playwright) | Scenario 8 | Reopen a session, wait for replay done, send a message, streaming works. |
| 27 | `e2e: replay-fidelity.spec.ts (e) send disabled during replay` | E2E (Playwright) | Scenario 10 | Input send button disabled while replay streams. |

### Test Datasets

| ID | Shape | Expected frames (sequence) | Traces to |
|---|---|---|---|
| D1 | `assistant, content="ok", tool_calls=[{ID:"t1", Tool:"t1"}]` | `replay_message`, `tool_call_start{t1}`, `tool_call_result{t1}` | Scenarios 1, 2 |
| D2 | Spawn call `ID:"c1"` + nested `{ID:"t2", ParentToolCallID:"c1"}` | `replay_message`, `tool_call_start{c1, spawn}`, `subagent_start{span_c1, parent_call_id:c1}`, `tool_call_start{t2, parent_call_id:c1}`, `tool_call_result{t2}`, `subagent_end{span_c1}`, `tool_call_result{c1}` | Scenario 5 |
| D3 | `user, content="hi"` | `replay_message{role:user, content:hi}` | Scenario 4 |
| D4 | `assistant, agent_id="ray", content="hello"` | `replay_message{role:assistant, agent_id:ray, content:hello}` | Scenario 3 |
| D5 | `{ID:"t9", ParentToolCallID:"ghost"}` (orphan) | `tool_call_start{t9}`, `tool_call_result{t9}` (flat); slog.Warn | Scenario 7 |
| D6 | Two entries both `ID:"t1"` | Latest only emits; slog.Warn | Scenario 13 |
| D7 | Compaction entry with `Summary` only | No frames | Scenario 14 |
| D8 | Result at exactly 1 MiB JSON-encoded | No truncation, frame just below limit | Scenario 11 boundary |
| D9 | Result at 1 MiB + 1 byte | Truncated frame emitted | Scenario 11 |
| D10 | `assistant, agent_id=""` | `replay_message{role:assistant, content}` — `agent_id` field omitted (`,omitempty`) | Edge (empty agent_id) |

## Regression

Existing behaviors that MUST be preserved:

1. `tests/e2e/handoff.spec.ts (a)` — agent label renders on assistant replies in a live session.
2. `src/test/integration/message-history.test.tsx` — passes (with reducer minor tweak if needed).
3. `tests/e2e/media.spec.ts` — attachment replay still works.
4. `pkg/gateway/session_milestone2_test.go` — existing session attach tests pass.
5. ChatThread prefetch via `fetchSessionMessages` — unchanged REST path.

New regression tests:

- `TestStreamReplay_Live_And_Replay_FrameCount_Match` — a fixture session run live produces N tool-call frames; the same session replayed produces N matching tool-call frames (same tool names in same order, same statuses).

## Functional Requirements

- **FR-I-001**: The backend MUST emit `tool_call_start` and `tool_call_result` WS frame pairs during replay for every persisted `ToolCall`. Each frame carries `call_id` (==`ToolCall.ID`), `tool`, `params`, `result`, `status`, `duration_ms`, and (if non-empty) `parent_call_id` (==`ToolCall.ParentToolCallID`).
- **FR-I-002**: The backend MUST emit a `replay_message` frame for every non-empty assistant/user/system entry content, preserving `role` and `agent_id` (the `agent_id` field is omitted from the JSON when the entry's `AgentID==""`).
- **FR-I-003**: The backend MUST emit `subagent_start` immediately before and `subagent_end` immediately after the nested tool-call frames of a spawn entry whose children have matching `ParentToolCallID`.
- **FR-I-004**: The replay MUST terminate with exactly one `done` frame.
- **FR-I-005**: The replay MUST honor context cancellation between every frame.
- **FR-I-006**: The replay MUST NOT emit frames for entries of type `compaction`.
- **FR-I-007**: A tool call whose `ParentToolCallID` does not match any spawn's `ToolCall.ID` in the transcript MUST emit as flat `tool_call_*` frames (no `subagent_start`); backend logs `slog.Warn` with the orphan key.
- **FR-I-008**: Both replay AND live `tool_call_start` / `tool_call_result` frames MUST carry `agent_id` (from `TranscriptEntry.AgentID` on replay; from the emitting agent on live). The live `eventForwarder` is updated in the same PR for parity.
- **FR-I-009**: `handleAttachSession` MUST register the connection for live events BEFORE starting the replay stream. Live events arriving during replay MUST be buffered (capped at 1000 events or session memory budget) and flushed in arrival order immediately after the `done` frame.
- **FR-I-010**: The SPA reducer's `textAtToolCallStart` capture MUST operate correctly when the assistant message was delivered via a single `replay_message` rather than progressive `token` frames. Any existing live-streaming-specific state (e.g., streamCursor) MUST NOT leak into replay.
- **FR-I-011**: The backend MUST truncate a `tool_call_result` frame whose JSON-encoded result exceeds 1 MiB, replacing `result` with `{"_truncated": true, "original_size_bytes": <N>, "preview": "<first 10 KiB>"}` and emitting `slog.Warn` with `{event:"replay_result_truncated", tool_call_id, original_size_bytes}`.
- **FR-I-012**: Replay MUST deduplicate duplicate `ToolCall.ID` within one transcript — only the latest occurrence emits; `slog.Warn` logged.
- **FR-I-013**: The backend MUST emit `slog.Info` at replay start with `{session_id, entry_count_loaded, tool_call_count_loaded, span_count_detected}` and at replay end with `{session_id, frames_emitted, duration_ms}`.
- **FR-I-014**: The SPA MUST disable the send button while replay is streaming; it enables only when `done` arrives.

## Success Criteria

- **SC-I-001**: `replay-fidelity.spec.ts` rows (a), (c), (d), (e) pass in CI against a scenario-provider gateway (deterministic). Row (b) passes once Sprint H is merged.
- **SC-I-002**: `tests/e2e/handoff.spec.ts (a)` passes both for live sessions and for reopened sessions.
- **SC-I-003**: Regression suite green: `message-history.test.tsx`, `chat-streaming.test.tsx`, `media.spec.ts`, `session_milestone2_test.go`.
- **SC-I-004 (narrow)**: For a fixture session, the LIVE-captured DOM and the REPLAY-captured DOM agree on:
  (i) same count of `[data-testid="tool-call-badge"]`;
  (ii) same set of `tool` attribute values;
  (iii) same set of status icons per badge;
  (iv) same order of user/assistant/system message roles.
  Explicit out-of-scope for parity: streaming cursors, timestamps, key props, memoization-induced DOM differences.
- **SC-I-005**: `CGO_ENABLED=0 go test -race ./pkg/gateway/... ./pkg/session/...` exits 0.
- **SC-I-006**: axe at WCAG 2.1 AA on a replay-rendered page — zero new violations vs. live-rendered page (using the Sprint H baseline).

## Traceability Matrix

| FR | User Story | BDD Scenarios | Tests |
|---|---|---|---|
| FR-I-001 | US-1 | 1, 2 | 2, 3, 4, 20, 23 |
| FR-I-002 | US-2 | 3, 4 | 5, 6, 18 |
| FR-I-003 | US-3 | 5 | 8, 21, 24 |
| FR-I-004 | US-5 | 8, 12 | 13, 26 |
| FR-I-005 | non-functional | — | 15 |
| FR-I-006 | Edge (compaction) | 14 | 12 |
| FR-I-007 | US-3 | 7 | 10 |
| FR-I-008 | US-2 generalized | 15 | 7 |
| FR-I-009 | US-5 | 9 | 16, 25 |
| FR-I-010 | US-4 | 2 | 19 |
| FR-I-011 | US-6 | 11 | 14 |
| FR-I-012 | Edge (duplicate id) | 13 | 11 |
| FR-I-013 | Observability | — | 17 |
| FR-I-014 | US-5 | 10 | 22, 27 |

## Ambiguity Warnings — RESOLVED

| Original ambiguity | Resolution |
|---|---|
| Stream completed text as `token` frames to simulate live? | No. Only `replay_message`. Rationale: `token` implies streaming-in-progress semantics (`isStreaming=true`, cursor) that mis-handle completed content. |
| Should REST `/sessions/{id}/messages` get enriched? | Not in this sprint. Struct-tag JSON already emits `parent_tool_call_id` post-Sprint-H; additional REST work not needed. |
| User-message-mid-replay behavior | Input is disabled during replay; enables on `done`. FR-I-014. |
| Duplicate `ToolCall.ID` dedup | Replay emits latest only; `slog.Warn`. FR-I-012. |
| Large tool result handling | Truncate at 1 MiB; preview + metadata. FR-I-011. |
| Attach-during-active-turn races | Register-before-replay + buffer-during + flush-after-done. FR-I-009. |
| Visual parity test flakiness | Narrow testable checks (badge count, tool names, status icons, message-role order). SC-I-004. |

## Holdout Evaluation Scenarios (POST-IMPLEMENTATION — NOT IN TRACEABILITY)

- **I-E1:** Open a session that had a shell command. Visible tool-call display matches what was visible live (modulo cursors).
- **I-E2:** (Requires Sprint H) Live session: trigger a spawn with ≥3 nested tool calls. Close. Reopen. SubagentBlock appears with the same step count as live.
- **I-E3 (error):** Corrupt one JSONL line. Reopen. Session opens; warning logged; other entries replayed; no SPA crash.
- **I-E4 (large):** Fixture session with a 2 MiB tool result. Reopen — badge expands; preview + truncation notice visible; no WS frame errors.
- **I-E5 (performance):** 500-entry session. Time from clicking the session to the send button becoming enabled: ≤3 seconds on a reference laptop (Apple Silicon Mac, Chrome stable).
- **I-E6 (attach mid-turn):** Start an agent that will take 10s to respond. Open a new browser tab attached to the same session 3 seconds in. The tab shows replay of the pre-attach state then seamlessly picks up the in-flight response. No missed tokens.
- **I-E7 (duplicate write bug):** Manually insert a duplicate ToolCall.ID into a session file. Reopen — one badge visible; slog.Warn entry present.

---

## Sprint Execution Plan

Three parallel agents on a feature branch.

### I1 · backend-lead (Go)

Scope: `pkg/gateway/websocket.go::handleAttachSession`, new `pkg/gateway/replay.go`, targeted tests in `pkg/gateway/`.

1. Extract replay into `pkg/gateway/replay.go::streamReplay(ctx, entries, func(wsServerFrame) error) error` — testable via slice-backed sink.
2. Rewrite helper per the behavioral contract: text entries emit `replay_message`; assistant entries emit `replay_message` then per-tool-call frame pairs; spawn spans bracketed with `subagent_start` / `subagent_end`; compaction skipped; duplicates deduped; orphans flagged.
3. Replace `handleAttachSession`'s inline loop with: register live-event forwarder → start buffer → call `streamReplay(...)` → emit `done` → flush buffer → steady-state.
4. Implement 1 MiB truncation for oversize tool results.
5. Also update live-path `eventForwarder` to populate `agent_id` on `tool_call_*` frames (FR-I-008 parity).
6. Structured logging per FR-I-013.
7. Dense unit tests for TDD rows 1-15.

### I2 · frontend-lead (TS/React)

Scope: `src/store/chat.ts`, `src/components/chat/ChatScreen.tsx` (input-disable during replay), targeted test utilities.

1. **Verify, don't rewrite.** Audit the reducer's `tool_call_start` / `tool_call_result` / `replay_message` / `subagent_start` / `subagent_end` handling — confirm live and replay use the same code paths. Write the parity test (TDD row 18) BEFORE touching code.
2. Fix `textAtToolCallStart` semantics for replay (FR-I-010): when `tool_call_start` arrives after a `replay_message` (not progressive tokens), ensure the captured text reflects the replay_message's content at that moment. Most likely a small assertion tweak.
3. Add `isReplaying` boolean to the chat store; set true on `attach_session` send, false on `done`; disable send button while true (FR-I-014).
4. Component/integration tests (rows 18-22, 27).

### I3 · qa-lead (Playwright + integration)

Scope: `tests/e2e/replay-fidelity.spec.ts` (new), fixture sessions.

1. Build new spec with 5 tests (a-e) per TDD rows 23-27.
2. Visual parity check per SC-I-004 narrow criteria: run the same scripted prompt twice, once capture live DOM badge+role sequence, then close + reopen and capture replay DOM sequence; assert equality on the four narrow dimensions.
3. Attach-during-active-turn (e) — uses a scenario provider that streams slowly; a second browser attaches mid-stream; assert no lost events.

### Sequencing & Gates

- I1's `streamReplay` extraction is the load-bearing change.
- I1 can merge independently as PR-1. I2 + I3 target PR-2 stacked on PR-1.
- Sprint H dependency: TDD row 24 (subagent-span E2E) requires Sprint H merged.
- Merge gates: `go build + vet + race test ./pkg/gateway/... ./pkg/session/...` + `npx tsc --noEmit && npm run build` + `replay-fidelity.spec.ts` rows a/c/d/e (and b once H is in) + regression suite + axe clean.
- BACKLOG: file a new ✅ FIXED line for "historical session fidelity" with this sprint's SHA.
