# Feature Specification: Chat Steps and Agent Checklist (#991 + #1072)

**Created:** 2026-09-29 (UTC)

**Status:** Draft — requirements interview waived for this draft only; founder approval, the specification grill, and the decisions in §Open questions are still pending. **Not authorised for implementation.**

**Input:** GitHub issues #991, “Chat: group tool calls into collapsible Steps”; #1072, “Todo list UI: surface the agent's active checklist in the chat and the side panel.”

**Size:** Feature (two related chat surfaces with live/replay and side-panel integration).

## Bottom line and release gate

One conversation can show a compact **Step** for a run of tool calls and a **separate active checklist** for the agent's working items. The checklist is not a set of Step badges. This draft describes observable behavior and RED-first checks but does **not** resolve the conflict between #1072's “matching backend state” and both issues' unchanged-wire boundary when someone edits a checklist on the task board. The founder must choose the scope in Q1 before this is implementation-ready. The other choices in §Open questions are likewise not silent approvals.

**Code correct and tested:** Not implemented; test plan only, no tests executed for this draft.

**Reachable by a user/agent:** Not implemented; route and entry-point checks are acceptance gates, not delivery claims.

## Sources and neighboring scope

| Authority | Requirement and boundary |
|---|---|
| GitHub issue #991 | In non-verbose chat, group consecutive tool calls between assistant-text pieces into one Step with preceding-text first-sentence objective (or “Working…”), live plain-language status, final count/duration/failure summary, disclosure of existing details, visible failures, verbose bypass, live/reload parity; **no persisted-transcript or wire change**. |
| GitHub issue #1072 | Show the agent's active `set_todos` checklist in chat and the side panel, preserve tri-state progress, full-list replacement on a new outcome, compact presentation, reload parity; **not** #991's badge grouping and **no persisted-transcript or wire change**. |
| GitHub issue #1021 | Separate plan/task running pill, link from start tools, side-panel presence and drill-down; this draft must not redefine these. |
| GitHub issue #1049 | Separate spinner and token counter for in-process tasks, modeled on the chat composer; this draft must not claim checklist-item token counts. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/side-panels/docs/internal/specs/side-panel-shell-spec.md::FR-022` / `::SP-41` | Wave 3 **already** requires one catalogued chat-style spinner-plus-token-count indicator on **every running task** in Tasks Board, List, Graph and the Plans band. This is the written side-panel/task-surface requirement, not a checklist requirement. Maintain one shared indicator, not a second local copy. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/architecture/ADR-091-steered-sessions-replace-subagents.md::D7` — **A sub-agent is a session steered by another session** | A child's detailed actions belong in the **child's own session**, not a Step copied into the parent; the parent gets its delegation line and side-panel status/open control. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/architecture/ADR-049-planning-goals-system-agents.md::scratchpad` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/architecture/ADR-051-tasks-screen-plans-as-filter.md::tasks` | Scratchpad checklists and executable plan/task records are separate concepts; do not portray checklist items as executable tasks. Cite these decisions by title: **Planning & Goals — Plan entity, evidence-ladder judge, goal loops, System Agents** and **Tasks Screen: Plans-as-Filter over a Combined Task Board**. |

### Available reference patterns

The plan-spec skill's suggested Go reference index is not present in this checkout (file-existence check returned nonzero), and Q1 option A has no Go implementation surface; no reference pattern is reused. No backend storage, migrations, credentials, or new API are proposed in this draft. A founder choice requiring a backend read path would reopen this assessment and the contract-first process before code.

## Existing codebase context and impact

GitNexus was unavailable for this checkout during discovery. The table below records **direct source reads**, not a GitNexus impact report; blast radius is inferred from callers and tests, **not measured by the graph**. Before any symbol is actually changed, the implementing lead must run GitNexus upstream impact or a caller sweep, and flag HIGH/CRITICAL findings.

| Integration seam / source | Directly observed behavior | Inferred change/test surface |
|---|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ChatScreen.tsx::AssistantMessage` and `::VirtualAssistantMessageRow` | Live registered tool UIs render through AssistantUI parts; historical/virtualized/plain-list rows use a separate parts/render pipeline and empty-bubble guard. | Both renderers and their ghost-bubble guards need tests; changing the generic badge alone cannot establish parity. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/lib/messageParts.ts::splitMessageParts` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/lib/omnipus-runtime.ts::pushHistoryParts` / `::buildContentParts` | Finalized calls have sorted/clamped text offsets with stable ties; unknown offsets trail text. The live runtime uses recorded text-at-call-start snapshots and deduplicates call IDs. | Preserve adjacency, text boundaries, tie order, offset-zero and legacy ordering. Do not infer unrecorded text boundaries. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/lib/toolVisibility.ts::shouldRenderToolCall` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/tools/HistoricalToolCallBlock.tsx::renderHistoricalToolCall` | The chat-thread filter deliberately hides some calls; dedicated result UIs (including goal, browser, file and shell) have their own live/replay treatment. A hidden raw `set_goal` badge is not the same as its visible result card. | Reuse, rather than clone, the visibility policy and existing detail presentations. Hidden-call failure accounting and dedicated cards need Q2. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/store/chat/slices/frames.ts::applyToolCallResultFrame`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/store/chat/slices/replay-and-status-frames.ts::handleReplayMessageFrame`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/lib/api/sessions.ts::rawToToolCall` | A result may update a live call or one already baked into a message; replay can coalesce text and bake by owner; REST history maps `parameters` to `params`, retains status/result/duration, but does not fill the internal `error` field. | Success and failure must be derived from **status**, not presence of error text; check live, baked, WebSocket replay and REST fallback. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/pkg/tools/todos.go::SetTodosTool.Execute`, `::parseTodosArg`, `::archiveOtherScratchpadCards` | A successful `set_todos` call replaces the full list; absent item status becomes `pending`, empty list clears; lookup and archival are session-scoped when transcript session ID exists. A failed archival is logged but **creation can still succeed**. Real tasks have an internal scratchpad discriminator. | Deriving a display from successful calls is a plausible **proposal**, not proof of exact board-card state. Failed/incomplete calls must not replace the display. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/contracts/components/schemas/Task.yaml::properties`, `::session_id`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/contracts/components/schemas/Todo.yaml::status`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/contracts/components/schemas/ToolCall.yaml::properties` | Task response has `todos` and a **task-running** `session_id`, but neither the backend's `Scratchpad` nor `OriginSessionID`; its `{text, done}` prose is stale against the actual tri-state Todo schema. ToolCall includes `duration_ms` but no start timestamp. | Do not match a scratchpad card by title/agent or mistake `session_id` for origin session. Duration metric needs Q3. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ActivityBar.tsx::ActivityBar` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ActivityPanel.tsx::ActivityPanel` | Existing Agents/Commands pills lead to flat status rows; no pill mounts if neither has activity. | The checklist-only state needs its **own** reachable side-panel entry without changing Agents/Commands counts; exact entry choice needs Q5. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/ui/disclosure-row.tsx::DisclosureRow` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/design-system/catalog.json::DisclosureRow` | A catalogued disclosure pattern exists. | Use catalogued controls and design tokens; do not hand-build a new button, accordion or indicator. |

**Relevant execution flow (source-inferred):** session tool-start → tool result → live chat store → current chat renderer; persisted transcript → WebSocket replay or REST history → historical/virtualized renderer. A new checklist display reads only the **displayed session**. There is no independent chat-server checklist stream demonstrated here. **Risk:** cross-renderer drift and a false claim of backend parity; graph risk level unknown.

## User stories & acceptance criteria

### US-1 — Read one work Step instead of a wall of badges (P0; #991)

As a reader of normal chat, I want consecutive tool actions presented as one understandable Step so I can follow what the agent is doing, see failure promptly, and inspect the same details if I choose. **Why P0:** this is #991's principal requested behavior. **Independent test:** a single session containing text, two adjacent calls and subsequent text produces one disclosure in its correct place, with verbose chat unchanged.

| Acceptance scenario | Given / When / Then |
|---|---|
| AS-1.1 | Given assistant text, two adjacent calls and another text segment, when the normal chat renders, then exactly one Step sits between the two text segments. A later run after intervening text is a different Step. |
| AS-1.2 | Given a running tool in a Step, when its live state changes, then the Step has the preceding text's first-sentence objective (or “Working…” when absent) and updates a readable current-action status, not a raw tool identifier. |
| AS-1.3 | Given a tool call fails, when the Step settles, then the collapsed header reveals at least the failed count; failure is not portrayed as success. The treatment of deliberately hidden calls is gated by Q2. |
| AS-1.4 | Given a Step with visible tool details, when the reader expands it, then the same detailed presentations and relative order available today appear, including dedicated cards rather than only a generic badge. |
| AS-1.5 | Given verbose chat is enabled, when the same message renders, then the Step wrapper does not replace or reorder existing detailed tool presentations. |
| AS-1.6 | Given an empty or missing preceding text segment or legacy call placement, when chat renders, then it does not invent objective text or place a call earlier than its recorded position. |

### US-2 — Follow the agent's active checklist (P0; #1072)

As the reader of a working session, I want its current `set_todos` outcome and item statuses in chat and in the side panel so I can understand the agent's planned work without switching to the task board. **Why P0:** these are the two missing surfaces named in #1072. **Independent test:** one successful full-list call with pending, in-progress and completed items renders an independent compact checklist in chat and an accessible side-panel section.

| Acceptance scenario | Given / When / Then |
|---|---|
| AS-2.1 | Given a successful checklist update for this session, when the chat and side panel render, then each has a separately labeled compact checklist with the outcome and every item in its submitted order and state. |
| AS-2.2 | Given an active list followed by a successful full replacement with another outcome, when the later result arrives, then the current-list surfaces show only the newer list as active. Exact placement of earlier history is gated by Q4. |
| AS-2.3 | Given a running, denied, cancelled or failed update after a successful one, when it resolves without success, then the previously successful active list remains; the unsuccessful result must not be advertised as the new backend state. |
| AS-2.4 | Given no agent or command pill but a nonempty active checklist, when the reader uses the side-panel entry, then the checklist remains discoverable and the Agents/Commands counts stay unchanged. Exact entry pattern is gated by Q5. |
| AS-2.5 | Given a successful empty-list submission, when the view updates, then there are no old checklist items presented as current; whether an empty outcome shell stays visible is gated by Q4. |
| AS-2.6 | Given a completed session is reloaded, when its history resolves, then the latest **recorded successful** list and its tri-state items match the live presentation. Independent task-board writes require the Q1 scope decision. |

### US-3 — Keep adjacent work distinct and available (P0; #991 + #1072)

As a reader who uses the task panel and child sessions, I need Steps, the active checklist and real running tasks to remain separately intelligible while live and after reload. **Why P0:** a visually plausible duplicate or an unreachable panel is a feature failure. **Independent test:** a parent and child with different tool/checklist activity show only their own data, while the task-running indicators retain their existing separate contract.

| Acceptance scenario | Given / When / Then |
|---|---|
| AS-3.1 | Given `set_todos` occurs during a Step, when the normal chat renders, then checklist items appear in their **own** checklist presentation, never as Step badges or a count of executable tasks. Any raw tool-detail treatment still follows Q2. |
| AS-3.2 | Given a parent delegates to a child with a different checklist, when the parent or child session opens, then each view contains only its own calls and checklist; the parent keeps its delegation status/open affordance rather than copies of the child's Steps. |
| AS-3.3 | Given both replay transport and REST-history fallback can restore a session, when either path loads it, then the Step boundaries, failure state and active recorded checklist agree with the live result. |
| AS-3.4 | Given real tasks run alongside a checklist, when the Tasks Board, List, Graph or Plans band is shown, then the wave-3 spinner and token count remain on running **tasks**, not on checklist items; the chat/side-panel checklist is visually distinct from #1021 plan/task pills and links. |
| AS-3.5 | Given a child's tool call fails while the parent has only successful calls, when the parent conversation renders, then no parent Step reports the child's failure; the child's own session still exposes its failed Step. |

## Behavioral contract, edge cases and safeguards

| Situation | Observable behavior | Source / limitation |
|---|---|---|
| One or more adjacent calls between text pieces | One Step at their original conversation position; an intervening nonempty text piece ends a run. | #991; ordering evidence in §Existing codebase context. |
| No preceding readable sentence | The objective reads exactly “Working…”; never use tool arguments or results as invented intent. | #991. Sentence-boundary treatment for abbreviations/Unicode remains a test choice, Q6. |
| Call still running | A human-readable status changes as current work changes; a completed Step stops presenting a running label. | #991; its existing human-readable action-label precedent. |
| Write was not accepted | The active checklist does not advance on a rejected/unfinished attempt. | #1072; backend acceptance semantics verified in §Existing codebase context. |
| An omitted todo status / empty list | Pending item / cleared current items, respectively, on **successful** calls. | Backend behavior verified in §Existing codebase context. |
| Two concurrent sessions for one agent | Do not copy a child/session B checklist into session A. | #1072; session scoping verified in §Existing codebase context and ADR **A sub-agent is a session steered by another session**. |
| Repeated replay call or live-to-saved transition | No duplicate Step, duplicate checklist update or lost last successful result. | Replay behavior verified in §Existing codebase context. |
| No recorded checklist or insufficient history | Do not show a made-up active list; handle absence explicitly. Whether to use a backend lookup is Q1, not an assumed fallback. | #1072; tool-call and task contracts verified in §Existing codebase context. |

**Explicit non-behaviors:** Do not change persisted transcript entries, tool policy, the agent's `set_todos` behavior, any API/WS schema, generated wire types or backend task storage under a frontend-only choice. Do not create a new executable task for a checklist item. Do not put child session calls in the parent Step. Do not build a second task-running indicator or alter wave-3 task counts. Do not use tool-result text as trusted UI markup or infer session ownership from task title alone. Do not call a summed duration “wall-clock time.” Do not claim an unverified card exactly matches task-board state after an archival failure or independent board edit.

**Machine-checkable invariants:** A two-call run has one Step in normal chat and two pre-existing detail presentations on expansion; verbose has zero Step wrappers; one successful full-list update yields precisely the submitted item count (including zero), never cumulatively appended items; unsuccessful updates leave the active list unchanged; a checklist-only state has an operable entry to its panel; there is no checklist item with a token counter or task-running icon. Text and status are escaped as content, not rendered as HTML. Disclosure is keyboard-operable and reports its expanded state; no nested interactive control inside its trigger. No performance threshold is invented for this draft; large-message and long-list fixtures must detect hangs or missed updates without creating an unapproved truncation limit.

**Conservative type design:** Keep Step/checklist render models internal to the SPA; use only generated contract types for existing boundary data. Introduce no new nominal wire type or unapproved persisted field.

## Prerequisites, runtime and integration boundaries

| Area | Required assumption / behavior |
|---|---|
| Hardware / OS | No new hardware or OS requirement; the product-supported Linux, macOS and Windows remain supported (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/CLAUDE.md::Tech stack and platforms`). |
| Runtimes / services / accounts | Existing embedded SPA, Go gateway and authenticated workspace chat session; no new external service, account or server process. |
| Network | Existing live session connection and history retrieval; a disconnected completed session may use the existing REST history fallback. |
| Development setup | Existing repository SPA workflow; no new setup command, migration or dependency proposed. Use the existing `npm run typecheck` and targeted Vitest commands when implementation begins; CI is authority for full feature gates. |
| Tech stack | Existing React/TypeScript SPA, AssistantUI live renderer, virtual historical renderer, generated REST/WS types, catalogued design-system components (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/CLAUDE.md::Tech stack and platforms`). |
| Deployment / footprint | Render-only addition to the embedded SPA; no new service, telemetry, DB, file write or startup/shutdown step. A task-running indicator shared by wave 3 must stay a single catalogued component. |

| Integration | Data in → data out | Failure / development approach |
|---|---|---|
| Existing chat stream | Assistant text, tool start/result and status → live Step/checklist presentation. | On missing result, show running/unknown rather than success. Test with simulated typed frames; verify real route in end-to-end acceptance. |
| Persisted session / WebSocket replay | Replayed text and tool records → historical Step/checklist. | Preserve recorded order and statuses; test duplicate replay IDs and missing offsets. No new frame. |
| REST history fallback | Existing session history and tool records → the same presentation. | Absent or partial records cannot prove current backend list; do not fake it. Test actual fallback path, not only a seeded component. |
| Task board | Independent checklist edits may exist outside tool-call history. | Existing Task response lacks a safe origin/scratchpad match. Scope decision Q1; no title/agent join. |
| Side panel | Existing Agents/Commands activity plus this session's checklist → separate section/entry. | Preserve those counts and open controls; checklist-only state must not be stranded. |

### Reachability checklist — verify before claiming delivery

| User entry | Existing route or control | Required proof after implementation |
|---|---|---|
| Real workspace conversation | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/routes/_app/workspaces.$workspaceId.chat.tsx::WorkspaceChatRoute` → `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/workspaces/WorkspaceChatTab.tsx::WorkspaceChatTab` → `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ChatScreen.tsx::ChatScreen` | Open a real workspace chat session; the non-verbose Step and independent checklist are visible without a test-only route. |
| Agent's existing checklist tool | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/pkg/agent/loop_wire.go::RegisterReplacing` registers `NewSetTodosTool`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/pkg/config/defaults.go::set_todos` gives it a shipped global ceiling entry (per-agent policy can still tighten it). | Confirm an actual permitted agent can successfully call `set_todos` in a real workspace session, and that the call reaches chat; a registered tool or a unit fixture alone is not proof of user/agent reachability. |
| Non-verbose and verbose | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/settings/ChatSection.tsx::chat-verbose-switch` | Change the real setting and compare both modes on the same session; no Step wrapper in verbose. |
| Checklist-only side-panel entry | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ActivityBar.tsx::ActivityBar` → `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ActivityPanel.tsx::ActivityPanel` | Open the panel when there is no Agent/Command pill; see this session's list and no spurious counts. |
| Reloaded session | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/store/chat/slices/replay-and-status-frames.ts::handleReplayMessageFrame` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/lib/api/sessions.ts::rawToMessage` | Restore the same real session by WebSocket replay and by forced REST fallback; compare Step order and accepted checklist. |

### UX guidance (design guidance, not brand decisions)

**Verdict:** ⚠️ Status visibility and panel reachability need explicit acceptance checks.

| Heuristic | Observable guard |
|---|---|
| H1 — Visibility of system status | Distinguish running from completed and failed Steps; checklist item state is explicit, not inferred from the spinner of a task. |
| H4 — Consistency | Expanded details retain existing dedicated tool UIs; disclosures and indicators use catalogued components. |
| H8 — Minimalist display | One summary Step for adjacent calls and one active checklist instead of a wall of badges or duplicated active lists. |
| H9 — Error recovery | A collapsed failure remains detectable; a failed checklist write does not overwrite the last successful display. |

**Priority actions:** (1) resolve the authority of board edits (Q1); (2) settle hidden-call failure accounting (Q2); (3) guarantee the checklist-only panel entry (Q5). Visual styling and brand specifics remain the design system/frontend lead's decision.

## BDD scenarios

Each ID maps one-to-one to its acceptance scenario. All assertions are observable. Conditional questions remain marked rather than guessed.

#### S-01 — Reader sees one Step between text pieces
**Traces to:** User Story 1, Acceptance Scenario 1.1 · **Category:** Happy Path
- **Given** a message has text A, two consecutive calls, text B, another call and text C.
- **When** the reader opens non-verbose chat.
- **Then** one Step containing the first two calls lies between A and B and a second lies between B and C.

#### S-02 — Reader sees a readable live action
**Traces to:** User Story 1, Acceptance Scenario 1.2 · **Category:** Happy Path
- **Given** a Step has a preceding intent sentence and a call still working.
- **When** a different call in that Step becomes active.
- **Then** its objective stays the first sentence and its status changes in plain words to the current action.

#### S-03 — Reader notices failure without opening details
**Traces to:** User Story 1, Acceptance Scenario 1.3 · **Category:** Error Path
- **Given** a Step with one visible failed call and one successful call.
- **When** the Step settles while collapsed.
- **Then** its header identifies one failure and does not present the run as wholly successful.

#### S-04 — Reader expands existing details
**Traces to:** User Story 1, Acceptance Scenario 1.4 · **Category:** Alternate Path
- **Given** a Step contains a dedicated result view and a normal badge.
- **When** the reader expands its disclosure.
- **Then** both existing detail presentations are available in their original order.

#### S-05 — Reader chooses verbose chat
**Traces to:** User Story 1, Acceptance Scenario 1.5 · **Category:** Alternate Path
- **Given** the same message has text, hidden and visible calls.
- **When** the reader enables verbose chat.
- **Then** each call appears through its existing detailed presentation with no Step wrapper.

#### S-06 — Reader opens a legacy textless run
**Traces to:** User Story 1, Acceptance Scenario 1.6 · **Category:** Edge Case
- **Given** a call has no recorded preceding text or a legacy unknown text position.
- **When** the reader opens non-verbose chat.
- **Then** any Step objective says “Working…” and the call does not appear before text it cannot be placed ahead of.

#### S-07 — Reader sees the session's checklist
**Traces to:** User Story 2, Acceptance Scenario 2.1 · **Category:** Happy Path
- **Given** a successful list has pending, in-progress and completed items in that order.
- **When** the reader opens this session's chat or checklist side-panel section.
- **Then** all three named states, original item order and outcome appear in a separate compact checklist.

#### S-08 — New outcome replaces the active checklist
**Traces to:** User Story 2, Acceptance Scenario 2.2 · **Category:** Happy Path
- **Given** the session's active outcome is A with two items and a later successful submission replaces it with outcome B and one item.
- **When** the later result arrives.
- **Then** the **current** chat and side-panel lists show only B's item as active; historical placement is pending Q4.

#### S-09 — Rejected update leaves prior list intact
**Traces to:** User Story 2, Acceptance Scenario 2.3 · **Category:** Error Path
- **Given** a successful checklist A followed by a refused or failed submission B.
- **When** B receives a non-success result.
- **Then** A remains the active displayed list and B is not presented as accepted progress.

#### S-10 — Checklist-only work still opens the panel
**Traces to:** User Story 2, Acceptance Scenario 2.4 · **Category:** Edge Case
- **Given** the session has one active checklist but no running or failed Agents/Commands entries.
- **When** the reader activates the checklist's panel entry.
- **Then** the checklist section opens and neither Agents nor Commands shows a fabricated count.

#### S-11 — Agent clears a list
**Traces to:** User Story 2, Acceptance Scenario 2.5 · **Category:** Edge Case
- **Given** the session has two active checklist items.
- **When** the agent's empty-list submission succeeds.
- **Then** zero old items remain current in either surface; the empty-shell choice is pending Q4.

#### S-12 — Recorded successful checklist survives reload
**Traces to:** User Story 2, Acceptance Scenario 2.6 · **Category:** Alternate Path
- **Given** the last successful call submits one in-progress item and a later call is cancelled.
- **When** the reader reloads the completed session.
- **Then** the previously accepted item still appears in chat and the side panel; independent board edits are pending Q1.

#### S-13 — Checklist items remain separate from the Step
**Traces to:** User Story 3, Acceptance Scenario 3.1 · **Category:** Happy Path
- **Given** a Step contains a successful `set_todos` invocation with two checklist items.
- **When** the reader opens non-verbose chat.
- **Then** the two items belong to one labeled checklist and neither is a Step badge or executable task.

#### S-14 — Parent and child cannot borrow each other's list
**Traces to:** User Story 3, Acceptance Scenario 3.2 · **Category:** Edge Case
- **Given** a parent and its child submit different lists in separate sessions.
- **When** the reader views the parent session.
- **Then** the parent sees only its own checklist and delegation open control, not the child's Step contents; opening the child shows the child's list.

#### S-15 — Both history paths reconstruct the same result
**Traces to:** User Story 3, Acceptance Scenario 3.3 · **Category:** Edge Case
- **Given** a finished session has interleaved text, a failed call and a successful checklist update.
- **When** the reader restores that session through either supported history path.
- **Then** both paths display the same Step boundaries, collapsed failure summary and current recorded checklist.

#### S-16 — Running-task signal stays on tasks
**Traces to:** User Story 3, Acceptance Scenario 3.4 · **Category:** Alternate Path
- **Given** a running task and an unrelated active agent checklist are visible.
- **When** the reader opens the task views and the chat side panel.
- **Then** the task views retain wave 3's shared spinner-plus-token indicator on the running task, while the checklist has its own identity and no task-running token counter.

#### S-17 — Child failure is not attributed to the parent
**Traces to:** User Story 3, Acceptance Scenario 3.5 · **Category:** Error Path
- **Given** a child session has one failed call and the parent session has only successful calls.
- **When** the reader views the parent's collapsed Step.
- **Then** that Step does not report the child's failure; opening the child's own session reveals it there.

## RED-first test plan (not executed)

**Method:** QA writes tests first and proves RED on the pre-change branch using a tests-only CI commit or the one permitted narrow local run; developer implements, then QA independently CHECKs with mutation probes and CI. A green check must show it actually exercised its named test; read `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/docs/internal/false-green-patterns.md` first. Do not run multiple local suites in parallel, a full Go suite locally, or a bare TypeScript no-op typecheck. Test the negative control: an intentionally unregistered tool still renders through the fallback, a failed call still trips a visible failure assertion, and removing the side-panel entry breaks its test.

| Order | Test name (to write RED first) | Level | BDD | Observable assertion / owner |
|---|---|---|---|---|
| 01 | `groupAdjacentCallsAndTextBoundaries` | Unit | S-01 | Two adjacent calls form one run; nonempty text splits; stable equal offsets, clamp, offset zero and absent offsets preserve order. QA. |
| 02 | `deriveFirstSentenceOrWorkingFallback` | Unit | S-02, S-06 | First sentence and exact fallback, no invented sentence for empty/whitespace/legacy text; Unicode/abbreviations per Q6. QA. |
| 03 | `deriveStepLiveStatusAndSupportedSummary` | Unit | S-02, S-03 | Running/current action updates; failure count and count/duration labels use only available evidence, never fake elapsed time. Q2/Q3 conditional cases. QA. |
| 04 | `projectLatestSuccessfulChecklist` | Unit | S-07, S-08, S-09, S-11, S-12 | Success replaces list; statuses and pending default; non-success retains prior success; empty success clears; replay duplicate does not append. QA. |
| 05 | `isolateChecklistAndStepFailuresBySession` | Unit | S-14, S-17 | Same agent, same outcome, different sessions: no cross-session list or leaked failure count. QA. |
| 06 | `renderStepDetailsWithDedicatedToolUI` | Integration | S-01, S-04, S-13 | Live registration and historical renderer both retain dedicated result views and visible-call order; checklist items outside Step detail. QA. |
| 07 | `renderCollapsedStepFailureAndVerboseBypass` | Integration | S-03, S-05 | Collapsed failure discoverable; verbose uses original presentations and existing per-tool visibility policy. Include all-hidden success and intentionally hidden failed-call controls after Q2. QA. |
| 08 | `renderLiveToBakedStepAndChecklist` | Integration | S-02, S-08, S-09 | Start, success/error, bake and subsequent text produce no duplicate/ghost bubble and update current session only. QA. |
| 09 | `renderHistoricalChecklistAndEmptyState` | Integration | S-06, S-07, S-11, S-12 | Empty/textless call and zero items; virtual and plain-list paths show accurate current list, no fabricated active data. QA. |
| 10 | `openChecklistOnlyActivityPanel` | Integration | S-10, S-13 | Entry opens panel with only a checklist, keyboard disclosure works, Agents/Commands counts unaffected. QA. |
| 11 | `preserveDelegationAndWave3TaskBoundaries` | Integration | S-14, S-16, S-17 | Parent/child isolation including failed calls, status/open link, and one catalogued indicator per running task in every mandated wave-3 surface **once wave 3 lands**. QA + frontend lead. |
| 12 | `restoreStepsAndChecklistByWebSocketReplay` | Integration | S-12, S-15 | Real reducer attach/replay, bake-after-owner, dedup, and failure count; not a pre-seeded component-only test. QA. |
| 13 | `restoreStepsAndChecklistByRestFallback` | Integration | S-12, S-15 | Force REST fallback with its `parameters` mapping and status; compare same data with WS replay/live. QA. |
| 14 | `chatStepsAndChecklistRealRoute` | E2E | S-01, S-07, S-10, S-13 | Open actual workspace chat, non-verbose Step and separate checklist, panel entry, expand details, reload. QA. |
| 15 | `chatStepsVerboseReloadAndSessionIsolation` | E2E | S-05, S-14, S-15, S-17 | Toggle verbose, inspect parent/child failure isolation, reload history through both available paths, restore settings. QA. |
| 16 | `taskIndicatorIndependentOfChecklist` | E2E | S-16 | After wave-3 integration, verify Board/List/Graph/Plans each shows only task running signals and side-panel checklist remains distinct. QA. |

**CI grouping:** new chat-component tests must be matched by the `components-chat` group, store/lib tests by `lib-store`, and end-to-end tests by their existing shard plan; check coverage grouping rather than trusting a job named “green” when it ran zero new tests (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/CLAUDE.md::Tests`). E2E and typecheck gates are written here, **not claimed executed**.

### Test datasets

#### D1 — Step boundaries, visibility, outcome and time

| # | Input / state | Boundary type | Expected | Traces to | Note |
|---|---|---|---|---|---|
| 1 | `A`, call X, call Y, `B`, call Z, `C` | Representative | Two Steps in text order; first contains X/Y | S-01 | Same offset for X/Y preserves order. |
| 2 | Two calls at text offset 0; empty text | Zero/empty | One run; “Working…”; no ghost text | S-01, S-06 | Preserve valid call-only message. |
| 3 | No offset, call after final text | Missing/legacy | Trailing Step; do not claim pre-text intent | S-06 | Existing unknown-position fallback. |
| 4 | Offset below 0 / above content length | Min−1/max+1 | Clamped positions and no crash | S-01 | Preserve existing splitter contract. |
| 5 | `“Read café. Update naïve.”`, first call running then second | Unicode/current action | Objective first sentence; current-action line changes | S-02 | Locale punctuation choice Q6. |
| 6 | Visible success + visible error, one recorded duration missing | Error/partial | Collapsed “1 failed”; no invented total elapsed | S-03 | Summary metric Q3. |
| 7 | `ToolSearch` success, `Skill` error, background `bash` failure | Hidden/visible | Existing detail visibility maintained; collapsed hidden-failure accounting pending Q2 | S-03, S-05 | Negative control for duplicated hide-list. |
| 8 | 1 and 200 adjacent calls, 10 KB first text, repeated IDs | One/large/duplicate | One Step, stable order, no duplicate; no new arbitrary cap | S-01, S-15 | Detect perf/regression without setting threshold. |
| 9 | `set_goal` result card + browser screenshot UI + ordinary call | Dedicated views | All designated visible cards remain usable on expansion or separately under Q2 | S-04 | Do not reduce to generic badges. |
| 10 | Running, pending, parked, denied, cancelled, interrupted statuses | Non-success | No false “done” or fabricated failed count; final classification Q3/Q6 | S-03, S-06 | Persisted domain exceeds success/error. |
| 11 | Only successful `ToolSearch`/`Skill` calls between two text pieces | All-hidden | Wrapper visibility and count **pending Q2**; expansion never reveals a hidden raw badge in normal mode | S-01, S-05 | Test that the existing hide policy still has effect. |

#### D2 — Checklist writes and identity

| # | Input / state | Boundary type | Expected | Traces to | Note |
|---|---|---|---|---|---|
| 1 | A: three items pending/in_progress/completed | Representative | Three states, original order, A in both surfaces | S-07 | Explicit tri-state contract. |
| 2 | A: status omitted on one item | Missing | Item displayed pending after success | S-07 | Backend default. |
| 3 | A then B successful with 1 item | Replacement | Exactly B active, no A item presented current | S-08 | Full list not append. |
| 4 | A then successful `[]` | Empty | Zero current items in chat and panel | S-11 | Empty shell Q4. |
| 5 | A then failed, denied, cancelled, parked, running B | Non-success | A remains last accepted state | S-09 | Never project an attempted write. |
| 6 | Same agent, same outcome in sessions 1 and 2 | Concurrent/identity | Session 1 cannot display session 2's items | S-14 | Title/agent matching is unsafe. |
| 7 | Text `<script>`, bidi text, 500-character item | Special/long | Literal, escaped content; no executable markup or silently discarded item | S-07 | Length is tool-description guidance, not a new UI cap. |
| 8 | Repeated replay result for one call and late result after bake | Duplication/race | One accepted list, no resurrection of superseded list | S-12, S-15 | Include live, baked and replay. |
| 9 | Independent board edit after successful tool call | Conflicting authority | No unsupported parity claim; expected product behavior **pending Q1** | S-12 | Blocking scope choice, not a passing acceptance test yet. |

#### D3 — Route, side-panel and neighboring task surfaces

| # | Input / state | Boundary type | Expected | Traces to | Note |
|---|---|---|---|---|---|
| 1 | Only active checklist, no other activity | Entry boundary | Side-panel checklist accessible; zero false Agents/Commands count | S-10 | Existing bar otherwise returns null. |
| 2 | Parent + child each with different list and own calls | Isolation | No cross-session Step or list; parent keeps Open control | S-14 | ADR **A sub-agent is a session steered by another session**. |
| 3 | Live → saved → WS replay → REST fallback | Transport boundary | Same recorded state, order and failure summary | S-15 | Exercise both reconstruction routes. |
| 4 | Running task in each of Board/List/Graph/Plans plus checklist | Neighboring scope | One shared spinner/token treatment on tasks; separate checklist | S-16 | Wave-3 FR-022/SP-41, not checklist behavior. |
| 5 | Child has an error, parent has only successes | Failure isolation | Parent Step has zero child failures; child Step reports its own | S-17 | Error paths are session-bound. |

### Regression suite and instrument controls

| Existing behavior to preserve | Existing seam / test | New regression and failure probe |
|---|---|---|
| Text/tool ordering and stable ties | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/lib/messageParts.test.ts::splitMessageParts`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ChatScreen.tool-order.test.tsx::ChatScreen — tool-call/text DOM ordering` | Tests 01/06: deliberately swap B and X to show RED; restore. |
| Registered live/dedicated replay UIs, not only fallback badge | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ChatScreen.tool-ui-live-registration.test.tsx::Live tool-UI registration`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ChatScreen.tool-replay-parity.test.tsx::ChatScreen replay parity` | Test 06: fail if dedicated result becomes generic; existing unregistered generic fallback is a negative control. |
| Visibility and verbose filter | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/lib/toolVisibility.test.ts::shouldRenderToolCall` | Test 07: fail if `Skill` success appears as a raw badge, or visible failure disappears. |
| Activity section/count, plain-list parity | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ActivityPanel.test.tsx::ActivityPanel — empty state`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/components/chat/ChatScreen.plain-list-parity.test.tsx::VirtualAssistantMessageRow`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/spec-991-1072/src/hooks/useRunningActivity.test.ts::useRunningActivity` | Tests 09/10: fail if bar disappears when checklist is only content or count increments spuriously. |
| Session reload, no false positives | Existing REST/WS restore seams above; module test inventory is a starting point, not proof of full coverage. | Tests 12–15 must drive actual reducers/route; force fallback and compare; mutation that drops latest successful call must turn them RED. |

## Functional requirements and success criteria

| Requirement | Testable draft obligation |
|---|---|
| **FR-001** (#991) | Non-verbose chat MUST group each consecutive run of tool calls between text pieces without changing their recorded order; whether an all-hidden successful run mounts a Step is **blocked by Q2**. |
| **FR-002** (#991) | Each Step MUST show the first sentence of immediately preceding assistant text or exactly “Working…” when no readable preceding sentence is available. |
| **FR-003** (#991) | A running Step MUST show a changing plain-language current-action line and MUST cease presenting a running line once settled. |
| **FR-004** (#991) | A settled Step MUST show a call count, an evidence-supported duration and a failure summary; the count and duration definitions are **blocked by Q2/Q3** and MUST NOT silently become wall-clock estimates. |
| **FR-005** (#991) | A failed visible tool call MUST be signaled on a collapsed Step, at minimum by a failed count. Whether hidden failures contribute or auto-expand is **blocked by Q2**. |
| **FR-006** (#991) | Expanding a Step MUST preserve the existing visible tool details, dedicated result UIs, order and per-tool visibility policy; not replace all calls with generic badges. |
| **FR-007** (#991) | Verbose chat MUST bypass Steps and preserve the current detailed tool presentation, including calls normally hidden. |
| **FR-008** (#991) | Live, baked, replayed and REST-restored Steps MUST agree on recorded order, boundaries and supported outcomes; no saved-transcript or wire changes. |
| **FR-009** (#1072) | The displayed session's latest **successful recorded** `set_todos` full list MUST produce an independent compact checklist with outcome and pending/in-progress/completed items in submitted order, in both chat and side panel. Board-edit authority is **blocked by Q1**. |
| **FR-010** (#1072) | A later successful call MUST replace the current list, including a zero-item clear; an unfinished/failed/denied/cancelled call MUST NOT supplant the last successful list. Placement and empty shell are **blocked by Q4**. |
| **FR-011** (#1072) | A checklist-only state MUST still have an operable side-panel entry and MUST NOT inflate the Agents/Commands pill counts; exact entry is **blocked by Q5**. |
| **FR-012** (#991 + #1072) | Steps and checklist MUST remain scoped to the displayed session; a child's calls/list MUST stay in its own session, while parent delegation status/open navigation remains. |
| **FR-013** (#991 + #1072) | A checklist MUST remain a separate chat/side-panel surface, not a Step item or real task; #1021 plan/task links/pills and #1049/wave-3 task spinners/token counts MUST remain separate, with the wave-3 indicator shared across Board/List/Graph/Plans. |
| **FR-014** (#1072) | After a completed session reload through either WebSocket replay or REST fallback, both surfaces MUST display the same last **recorded successful** checklist, including empty and tri-state lists, without duplicate results. Q1 governs separate board edits. |
| **FR-015** (both) | The real workspace chat route, non-verbose Step, separate chat checklist and checklist-only side-panel entry MUST be user-reachable; disclosures MUST work by keyboard and expose their state. |

| Criterion | Pass condition |
|---|---|
| **SC-001** | 100% of the D1 run fixtures preserve expected text/call order and have exactly one Step per contiguous run in normal chat; verbose has zero Step wrappers. |
| **SC-002** | 100% of visible failures in D1 can be detected from a collapsed Step; unsupported time is never reported as measured wall-clock time. Final hidden-call oracle awaits Q2. |
| **SC-003** | 100% of D2 successful full-list changes yield the submitted active item count/state in both chat and panel; 100% of non-success changes preserve the prior successful list. Board-edit oracle awaits Q1. |
| **SC-004** | Tests 12–15 verify the actual real chat route and both replay paths against the same session fixtures; checklist-only panel remains reachable and child/parent content is isolated. |
| **SC-005** | Once integrated with side-panel wave 3, Board/List/Graph/Plans each show the single shared task-running indicator for running tasks and never for checklist items. |
| **SC-006** | No diff under persisted-transcript, contract, generated API or backend trees for the frontend-only Q1 option; contract-changing option cannot claim this criterion and needs a revised spec/ADR/contract-first gate. |

### Full requirement-to-test traceability

| Requirement | Story / acceptance | BDD | RED test(s) |
|---|---|---|---|
| FR-001 | US-1 / AS-1.1 | S-01 | 01, 06, 14 |
| FR-002 | US-1 / AS-1.2, AS-1.6 | S-02, S-06 | 02, 09 |
| FR-003 | US-1 / AS-1.2 | S-02 | 03, 08 |
| FR-004 | US-1 / AS-1.2, AS-1.3 | S-02, S-03 | 03, 07 |
| FR-005 | US-1 / AS-1.3 | S-03 | 03, 07, 12 |
| FR-006 | US-1 / AS-1.4 | S-04 | 06 |
| FR-007 | US-1 / AS-1.5 | S-05 | 07, 15 |
| FR-008 | US-1 / AS-1.1, US-3 / AS-3.3 | S-01, S-15 | 01, 08, 12, 13 |
| FR-009 | US-2 / AS-2.1 | S-07 | 04, 09, 14 |
| FR-010 | US-2 / AS-2.2, AS-2.3, AS-2.5 | S-08, S-09, S-11 | 04, 08, 09 |
| FR-011 | US-2 / AS-2.4 | S-10 | 10, 14 |
| FR-012 | US-3 / AS-3.2, AS-3.5 | S-14, S-17 | 05, 11, 15 |
| FR-013 | US-3 / AS-3.1, AS-3.4 | S-13, S-16 | 06, 10, 11, 16 |
| FR-014 | US-2 / AS-2.6, US-3 / AS-3.3 | S-12, S-15 | 04, 09, 12, 13, 15 |
| FR-015 | US-2 / AS-2.4, US-3 / AS-3.1 | S-10, S-13 | 10, 14 |

## Open questions / ambiguity warnings — founder interview required

These choices are **not approved**. Team-lead waived the separate interview only to permit a draft. Do not mark FR-004/005/009/010/011, SC-002/003/006, or the conditional tests green until the relevant decision is recorded and the spec corrected. Questions refer to design, not to this draft's authorship.

| ID / impact | Ambiguity and tempting but unsafe assumption | Options and recommendation to take to founder |
|---|---|---|
| **Q1 — BLOCKING, authority and wire** | #1072 says “matching backend state” yet also “render-only, no wire change.” Latest successful `set_todos` call reproduces agent-written updates but **cannot detect an independent board edit**, and Task exposes neither `Scratchpad` nor `OriginSessionID`; its `session_id` is the execution session, not the originating checklist session. Matching by agent/outcome/title can capture the wrong session or a real task. | **A (recommended for this v1):** promise parity with the last *recorded successful agent call* only, label this scope explicitly and file separate board-edit synchronization work. **B:** require every independent board edit reflected too; this breaks the unchanged-wire boundary and needs an architectural decision, backend-owned contract shape, regenerated types, backend implementation and expanded tests **before** a final spec. Founder decides. |
| **Q2 — BLOCKING, hidden failures / details** | #991 says failures never hidden, but the existing filter intentionally hides `delegate` and background `bash` even on failure, while `set_goal` has a dedicated failure trace. Counting all calls/failures in the new header may reveal hidden agent work; counting only visible ones may under-report failure; even an all-hidden **successful** run could wrongly grow a Step wrapper. Moving dedicated cards inside the Step can conceal them. | **A (recommended):** preserve detail-level visibility and dedicated cards; suppress an all-hidden successful run, but count/report all failures in a Step header without exposing hidden arguments. Decide whether dedicated goal/delegation/card surfaces remain separate from Step disclosure. **B:** count only visible details (explicit exception to issue wording). **C:** auto-expand on any failure (exposes existing hidden presentations only where policy permits). Record inclusion/count and expansion rules in the correction. |
| **Q3 — BLOCKING, duration and lifecycle** | ToolCall has per-call `duration_ms`, not a start timestamp. Concurrent durations summed together do not equal Step elapsed time; some calls are running, denied, parked, interrupted or have no duration. | **A (recommended):** label a sum of *known call durations* as such and show “duration unavailable” if none; never call it elapsed wall-clock. **B:** omit duration if not exact (requires explicit amendment to #991). **C:** true wall-clock elapsed (requires a new source of timing, likely violating the current scope). Choose count basis, final lifecycle labels and missing-duration policy. |
| **Q4 — BLOCKING, one active chat location** | A call might be in an old assistant message, a later call might change the outcome, and `[]` clears items. Showing an updated checklist on every historical call duplicates an apparently active list; removing history entirely can obscure the conversation. Verbose treatment not specified for #1072. | **A (recommended):** one labeled active chat block at the most recent successful call, old blocks retain clearly historical/superseded identity (or render only through verbose detail); empty success shows a labeled cleared state. **B:** a single sticky current block outside the transcript. Specify switch/reset behavior, text-only/virtualized placement and verbose semantics without duplicating Step badges. |
| **Q5 — BLOCKING, entry placement** | The current ActivityBar returns null with no Agents or Commands entries, so adding a checklist section to the sheet alone leaves it unreachable. Calling checklist items running agents/tasks corrupts existing counts. | **A (recommended):** distinct checklist entry/pill that opens the existing panel directly to a separate checklist section, including checklist-only state. **B:** persistent entry elsewhere in chat shell. Ask frontend lead to follow the catalog and avoid visual brand decisions in this spec. |
| **Q6 — status/objective precision** | “First sentence” with abbreviations, newlines, blank intent and Unicode punctuation is undefined; running/status fallback for failed, parked, interrupted and unknown tool names is also undefined. | **A (recommended):** use the first nonempty sentence of immediately preceding text, conservative punctuation splitting, “Working…” only if none; use existing human-readable labels where present, otherwise a generic “Working…” without claiming success. **B:** strict first punctuation terminator even for abbreviations; choose after checking examples. Confirm final-state text for incomplete/cancelled calls. |

**Design-process consequence:** if the interview surfaces an open architectural decision requiring an ADR, architect authors it; one ADR-mode `grill-spec` round, interview on its questions, then one correction. The combined spec gets its own two grill/interview/correction rounds. A blocking finding remaining after the prescribed rounds escalates to the founder, not to an invented default. Backend lead alone edits/regenerates contracts if Q1 chooses B. This draft is neither a completed ADR nor a final approved spec.

## Evaluation scenarios — holdout, not development fixtures

**Reserved for independent post-implementation evaluation.** These are external user-observation prompts, not the RED dataset; do **not** reference them in the implementation test matrix or seed test fixtures from them. Use the actual running instance, screenshots and session reload. Until Q1–Q6 are decided, interpret expectations only where the scenarios do not cross an unresolved boundary.

| Category | Independent evaluator setup and action | Expected observable result |
|---|---|---|
| Happy 1 | In a real session ask an agent to inspect then edit a document while narrating its intent; watch the non-verbose live chat. | One Step for adjacent actions, readable changing status, details accessible and correctly ordered. |
| Happy 2 | Have an agent submit a three-state checklist; open chat and panel with no other background work. | Both places show the same agent outcome and three states; panel entry exists even without an Agents/Commands pill. |
| Happy 3 | Ask an agent to finish one outcome and start a different one; reload the session. | The newer successful outcome alone is marked current; earlier state is not presented as an active duplicate. |
| Error 1 | Cause one readable tool to fail inside a longer run; keep the Step collapsed. | Failure is apparent without guessing from succeeding calls, and details still tell the same story on expansion. |
| Error 2 | After a successful checklist, cause a `set_todos` attempt to be refused, then reload. | The failed attempt is not shown as an accepted new checklist; the last successful one survives. |
| Edge 1 | In two simultaneous sessions of the same agent, use an identically named outcome but different items, then inspect the parent/child views. | No session borrows the other's checklist or detailed tool calls. |
| Edge 2 | Restore an old session containing a textless tool run and a cleared checklist, once with connected replay and once after falling back to history. | No invented intent or resurrected current items; both restoration paths agree. |

## Assumptions and clarification record

| Item | Status |
|---|---|
| The founder's existing #991/#1072 issue bodies, not this draft, authorise the frontend-only unchanged-wire v1 boundary. | Verified issue text; conflicting board-edit meaning still open Q1. |
| Tool-call `status === success` identifies an accepted checklist write, and inputs remain readable in session history. | Inferred from current backend/reducer/REST flow, not yet verified by executed feature tests; if history omits needed inputs, stop and amend rather than parse a formatted result or guess. |
| A successful creation proves exactly one open scratchpad card. | **False:** backend archival failure is nonfatal. This specification makes no such guarantee. |
| A generic UI type or title/agent match can safely identify the current board card. | **False under current Task response:** missing discriminator and origin-session mapping. |
| Founder requirement confirmation, ambiguity decisions, grill rounds and final approval. | **Not obtained.** Team-lead waived interview for this draft only. |

**Clarifications recorded 2026-09-29:** Team-lead asked for one combined draft, not two issue specs; the side-panel wave-3 requirement **FR-022/SP-41 exists in writing** in the amended side-panel specification and is separate from #1072. No founder decision on Q1–Q6 was supplied.
