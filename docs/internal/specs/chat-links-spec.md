# Feature Specification: Chat links, show_to_user, and path display

**Created**: 2026-10-15
**Status**: Approved-draft (drift-synced 2026-10-29 to b7d4483c9)
**Input**: Approved design note `coordination ledger `designs/chat-links/` design-note.md`, binding decisions `coordination ledger `designs/chat-links/` decisions.md`, grill round 1 `coordination ledger `designs/chat-links/` spec-review.md`, grill round 2 `coordination ledger `designs/chat-links/` spec-review-round2.md` (every round-2 MAJ/MIN answered in "Review response (round 2)"), and the drift check `(maintainer-local path)` (applied in "Drift sync 2026-10-29").
**Code baseline**: `origin/release/v0.1.1` at `b7d4483c9` (2026-10-29). The design note and both grill rounds read an older checkout at `b45fd6f16` (2026-10-06); every `file::symbol` below was re-checked against `b7d4483c9` by `git show` / `git grep` on 2026-10-29.
**Size**: Feature (cross-tree, wire contract change, touches `pkg/agent`, `pkg/tools`, gateway WebSocket). Needs the 5-reviewer gate and hands-on UAT on the exact landing commit.
**ADR**: No product ADR. The tool-to-hub seam, the live-only hub method, and the presence mechanism are closed in this spec. A one-page ADR with that text is filed when this spec is committed into the repo (`docs/internal/architecture/`). This design lane does not edit the repo, so the ADR file is a landing step, not an open design choice. Q1-panel is a founder decision (2026-10-15) and is no longer a working assumption.

Certainty labels: **V** verified in the tree at `b7d4483c9` (drift sync 2026-10-29), **I** inferred, **U** unknown (settled by the named unit).

## Binding decisions (not re-opened)

Who decided each row is in `decisions.md`. A row not marked Founder is a team-lead or architect decision. Do not describe those as founder-final.

| ID | Who | Decision |
|---|---|---|
| D-Q1 | Founder | `show_to_user`: global ceiling `allow`, own workspace only, rate-limited at 5 calls per minute per chat. |
| D-Q1-panel | Founder (2026-10-15, replaces assumption A) | **Auto-open only when this chat currently has focus: the browser tab is visible and this chat is the view on screen.** Otherwise do not open. Place a clickable "Open in Library" notice in the chat instead. See "Watching". |
| D-Q2 | Team-lead | Links: `omnipus://file/...` scheme plus bare workspace-relative links (rules below). |
| D-Q3 | Team-lead | New live-only `open_in_library` WebSocket frame (contract-first) plus optional `library_ref` on `MediaPart`. |
| D-Q4 | Founder | Linking to **saved knowledge-base views is out of scope**. Notes and `.base` files stay linkable as ordinary files; a view gets no special link, no guidance, no documentation. U11 records the follow-up at planning. |
| D-Q5 | Founder | Auto-open is limited to the turn's own workspace. |
| D-Q6 | Team-lead | Ready-made `link:` line only on `write_file`/`edit_file`/`append_file`, `send_file`, `show_to_user`, `knowledge_read`. |
| D-A | Founder | **`link:` lines are produced on web chat only**; other channels get the file name only. |
| D-R3 | Founder | Absolute path plus a link only when the user asks where the file is. |
| D-R4 | Founder | A file outside every Library is the full path, with no link. |
| D-R5 | Founder | On web chat, a `send_file` card for a workspace file also shows "Open in Library". |
| D-B | Team-lead, narrowed by Q1-panel | **Every attached tab receives the frame and decides on its own.** A watching tab opens. A tab that is not watching shows the notice. A disconnected tab does not open later. |
| D-C | Founder number; architect mechanics | **5 calls per minute per chat** (founder, with Q1). Sliding window, process-wide counter, injectable clock (architect, D-limit). |
| D-D | Architect | **"Never on reload" holds for transcript replay and incremental reconnect**, guaranteed on the server by non-journaled, unsequenced delivery, plus a client guard keyed on `request_id` (see Replay guarantee). |
| D-seam | Architect | The open target is a typed optional field on the tool result and on `ToolExecEndPayload`. See "Tool-to-hub seam". |
| D-watch | Founder (2026-10-15) | The agent is told whether the user is watching **this** chat. See "Watching". |
| D-presence | Architect | `chat_presence` client frame, 15s refresh, 45s expiry, exact context line. See "Watching". |
| D-takeover | Architect, superseded | Phone width is not a gate. `historyPushed` and `panelRowWidth` are not inputs. |
| D-F1 | Founder (2026-10-29) | **Chat links win over base-instruction rule 27.** Default display is the name or relative path plus a link; the absolute path only when the user asks (R3) or when the file is outside every Library (R4). Rule 27 of `pkg/agent/context.go` `<operating_rules>` ("After reading back saved artifacts, provide their verified full absolute paths.") and the matching sentence in `docs/agents.md` are rewritten to match, as a requirement of this feature (FR-027). |
| D-F2 | Founder (2026-10-29) | **Omnipus is not granted `show_to_user`.** It keeps its fixed tool list (`pkg/coreagent/role_policies_adr090.go::omnipusSupportedTools`, omnipus-system-agent spec section 6.5). Files in its chat still get passive links. Replaces the architect row D-admin (Admin no longer exists). |
| D-omnipus-links | Architect (drift sync 2026-10-29) | Omnipus's chat has no workspace for bare links; `omnipus://` links stay clickable; for an Omnipus turn the resolver also tries every other workspace (see Resolver Rules). |
| D-grant | Architect (drift sync 2026-10-29) | Grant through `seed.go::NewCustomAgentToolsCfg` (allow) and `workerTierDeniedTools` (deny); no per-role grant. |

Founder requirements: R1 clickable links (file to Library at file; note/base to Library; website to new tab); R2 default display name or workspace-relative path, never absolute; R3 absolute path plus link only when the user asks; R4 files outside Library show the full path, no dead link; R5 `send_file` web card also shows "Open in Library" for workspace files (outside channels use name plus `send_file`); R6 `show_to_user(ref)` live-only auto-open respecting the leave gate; R7 one base-instruction change covering links and path display, plus one sentence each in the `send_file` and `show_to_user` `Description()`. Since 2026-10-21 (`ed39230ab`) the base instructions have no "Rule 2": they are one continuously numbered list inside `<operating_rules>` in `pkg/agent/context.go`, and the old artifacts-over-chat content is rules 25 to 27 under "### Files and artifacts". R7 therefore rewrites that group (D-F1).

---

## Contract (Hard Constraint 8) - stated first

Order per root `CLAUDE.md` ("Contract regeneration"): schema, reference, `scripts/gen-contracts.sh`, one atomic commit, then consume generated types only. backend-lead edits the specs; shapes are architect decisions.

**Two-copy trap (V):** `contracts/asyncapi.yaml` carries its OWN inline `components/schemas/MediaPart` (`title: MediaPart`, `additionalProperties: false`) and the generated Go `generated.MediaPart` (`pkg/api/generated/asyncapi_types.gen.go`) and the SPA's Zod `MediaPart` (`src/lib/api/generated/_asyncapi-zod-schemas.generated.ts`) come from that inline copy, not from `contracts/components/schemas/MediaPart.yaml`. The file itself still says to keep inline copies "in sync by hand" (its `RoomRef` note). The inline copy is also what the paged-history preparation checks: `pkg/gateway/history_page_prepare.go::validateHistorySourceFrame` decodes a prepared `media` frame into the generated `generated.MediaFrame` ("MediaFrame is an inline AsyncAPI component rather than a standalone inbound-schema file"). Therefore **both copies are edited in the same atomic commit** for every item below. (The round-1 drift example, the inline `AgentSwitchedFrame`, no longer exists: that frame was removed on 2026-10-10, `b62539418`.)

| Item | Exact name | Change (both copies unless stated) |
|---|---|---|
| New named schema | `contracts/components/schemas/LibraryRef.yaml` AND a named `LibraryRef` in the inline `components/schemas` of `contracts/asyncapi.yaml` | `title: LibraryRef`; required `workspace_id` (string, minLength 1), `path` (string); `additionalProperties: false`. The inline copy is required because the inline `MediaPart` resolves `$ref: '#/components/schemas/LibraryRef'` inside `asyncapi.yaml`, not against the file. No anonymous inline object, so Go gets a named type. |
| `MediaPart` | `contracts/components/schemas/MediaPart.yaml` AND the inline `MediaPart` in `contracts/asyncapi.yaml` | Add optional `library_ref` (`$ref` LibraryRef). Required list unchanged. |
| New frame schema | `contracts/components/schemas/OpenInLibraryFrame.yaml` AND the inline copy in `asyncapi.yaml` `components/schemas` | `title: OpenInLibraryFrame`; `additionalProperties: false`; required `type` (const `open_in_library`), `session_id` (minLength 1), `call_id` (minLength 1), `request_id` (string, minLength 32, maxLength 32, pattern `^[0-9a-f]{32}$`), `workspace_id` (minLength 1), `path` (string; Library path, mount name first for mounts). `call_id` correlates the frame with the tool card only; it is not unique. `request_id` is the server-minted identity (D-dedupe). **No `seq` property** (the frame is unsequenced by design, see Replay guarantee). |
| `asyncapi.yaml` wiring | message `OpenInLibraryFrame` (title `open_in_library`) | Add to `channels/chat/messages`, `components/messages`, and a server-to-client operation `receiveOpenInLibrary` modelled on the existing `receiveMedia` operation; extend the inline frame-type enum in `asyncapi.yaml` together with `WsFrameType.yaml`. |
| `WsFrameType.yaml` | enum value `open_in_library` | Add. |
| SPA registration | `SESSION_SCOPED_FRAME_TYPES` in `src/store/chat/runtime-state.ts`; Zod registration of the generated `OpenInLibraryFrame`; dispatch entry in the frame handler | Add `open_in_library` only. Dispatch is a single line in `handleFrame` calling a new handler file (budget rule in U7). `chat_presence` is client→server and is NOT added to `SESSION_SCOPED_FRAME_TYPES`. |
| New client frame | `contracts/components/schemas/ChatPresenceFrame.yaml` AND the inline copy in `asyncapi.yaml` `components/schemas` | `title: ChatPresenceFrame`; `additionalProperties: false`; required `type` (const `chat_presence`), `session_id` (string, minLength 1, maxLength 128), `watching` (boolean). No `seq`. Client→server, wired like `PingFrame` (`sendChatPresence` on `channels/chat`). `WsFrameType` gains `chat_presence`. Not journaled, not replayed, not persisted. Server-side inbound validation: `scripts/gen-contracts.sh` step 5 copies `contracts/components/schemas/*.yaml` into `pkg/gateway/inboundschemas/` automatically, but `pkg/gateway/websocket.go::wsFrameSchemaName` needs a new `chat_presence` → `ChatPresenceFrame` case (not automatic). Precedent for a connection-bound client presence frame: `mail_panel_observer` (`pkg/gateway/mail_presence.go`, `src/components/workspaces/mail/mailPanelPresence.ts`). |
| Not changed | `tool_call_result` shape; no `openapi.yaml` route; no resolver endpoint; `contracts/components/schemas/Attachment.yaml` and the REST `SessionHistoryResponse` (sent-file cards do not travel there; see "REST history path") | |

**Regeneration CHECK items (qa-lead, fail-able):** after `scripts/gen-contracts.sh`:

| Item | What must be true |
|---|---|
| (a) | `library_ref` / `LibraryRef` appears in all five generated artifacts: `pkg/api/generated/asyncapi_types.gen.go`, `src/lib/api/generated/_asyncapi-zod-schemas.generated.ts`, `src/lib/api/generated/ws-schemas.ts`, `src/lib/api/generated/asyncapi-types.ts`, `src/lib/api/generated/schemas.ts`. |
| (b) | `OpenInLibraryFrame` and `request_id` appear in those same five, and `WsFrameType` in `ws-schemas.ts` includes `open_in_library`. `ChatPresenceFrame` and `chat_presence` appear in those same five too. |
| (c) | A vitest drives the real runtime parser (`src/lib/ws.ts`, which imports `WsFrame` / `WsFrameType` from `src/lib/api/generated/ws-schemas.ts`; `src/workers/ws-parser.worker.ts` imports the same module). It parses a `media` frame with `library_ref`, a `media` frame without it, and an `open_in_library` frame with a 32-hex `request_id`. `getDroppedFrameCount()` and `getUnknownFrameTypeCount()` stay 0. A frame whose `request_id` is missing or not 32 lowercase hex is rejected. Grep of the fragment file alone is not this check: the SPA does not validate frames from `_asyncapi-zod-schemas.generated.ts`. |
| (d) | `make verify-contracts` and `scripts/check-no-handwritten-wire-types.sh` pass. |
| (e) | `pkg/gateway/inboundschemas/ChatPresenceFrame.yaml` exists after regeneration, and a gateway test sends a `chat_presence` frame missing `watching` and sees it rejected through `wsFrameSchemaName` (instrument: the same test with a valid frame is accepted). |

The negative control is edit-one-copy-only: check (a) must fail. `ws-schemas.ts`'s own header says runtime validation MUST import from there.

---

## Replay guarantee (the "never on reload" mechanism)

Facts (V at `b7d4483c9`): every frame published through `hubPublishMetaAlsoTo` -> `sessionHub.publishMeta` is numbered, journaled, and re-sent from the journal tail by `pkg/gateway/websocket_replay.go::sendIncremental`, "byte-identical to what every live tab received". The snapshot path is now the paged chat history: `wsHandlerHandleAttachSession.sendSnapshot` -> `pkg/gateway/history_page_websocket.go::sendPreparedHistorySnapshot`, which sends a `session_snapshot` frame and then `prepared.ReplayFrames`. Those frames are rebuilt from the saved transcript by `pkg/gateway/history_page_prepare.go::prepareGroup` through `pkg/gateway/history_replay_entry.go::streamReplayState.replayEntry`. The REST page `pkg/gateway/history_page_http.go::serveSessionHistory` shares the same preparation. (The pre-paging `replayTranscript` no longer exists.) Whether `isReplaying` is raised on the incremental path is still not settled for the SPA (U). So a journaled `open_in_library` frame would be redelivered to a tab that reconnects incrementally and, with no client flag guaranteed, could open a Library late.

Decision (D-D): the guarantee is **on the server**.
1. The gateway delivers `open_in_library` through `deliverLive` (Tool-to-hub seam): enqueue to the connections currently bound to the session, without numbering or journaling (V `sessionHub.conns`, `hubConn.enqueue`). The frame therefore has no `seq`, is never in `sendIncremental`'s tail, never in the projection, and never in a snapshot. A precedent for unsequenced delivery is `sendRawFrameBytes` / `hubBroadcastWithSequencedCopy` (V); `deliverLive` does not call them.
2. History replay cannot produce it: the frame is not persisted, so neither the WebSocket history snapshot nor the REST history page can rebuild it, and the replay emitter (`streamReplayState.replayEntry`, `pkg/gateway/replay.go`) gets no twin. `validateHistorySourceFrame` refuses any frame type it does not list, so an accidental twin would fail history preparation loudly instead of slipping through.
3. **Client guard (defence in depth, applies to both attach paths):** the SPA acts only on a frame that has no `seq`, arrives while the bucket is not replaying and not awaiting catch-up, belongs to the session on screen, and whose `request_id` is not already in a bounded handled-set. The set stores the last 256 keys of `session_id` + `request_id` for this page load. `call_id` is not a key: two frames that share a `call_id` and differ in `request_id` both act. A repeated `request_id` still in the set is dropped with nothing visible. When a 257th distinct key arrives, the oldest key is forgotten and a later frame with that forgotten key acts again.
4. A tab that is disconnected when the call happens never opens anything later. This is intended. A tab that is mid-attach (hold mode) is bound, so it does receive the frame, after catch-up; see "Tool-to-hub seam".

Consequence for claims: SEC-8/SC-003 state **zero Library navigations** on every reload and reconnect path, and **zero `open_in_library` frames in any replayed or journaled output**.

### REST history path (resolved 2026-10-29)

Question from the drift check: does the REST history page carry sent-file cards, so that `library_ref` would also be needed there? **Answer: no (V).**

| Fact | Evidence at `b7d4483c9` |
|---|---|
| A REST history page is `SessionHistoryResponse.entries[]`, each a `Message`; it has no frame list and no `MediaPart` | `contracts/components/schemas/SessionHistoryResponse.yaml` (`entries.items: Message.yaml`); `history_page_http.go::serveSessionHistory` writes `prepared.encodedResponse` (the encoded `Message` entries only) |
| `Message.attachments` (`Attachment.yaml`: `type`, `path`, `size`, `mime_type`) records files the **user uploaded**, not files the agent sent | Only writer: `pkg/gateway/websocket_chat.go` sets `Attachments: buildTranscriptAttachments(..., hcm.acceptedMedia, ...)` on the saved user entry. `send_file` media stay in the tool call result (`tc.Result["media"]`), read by `replay.go::buildMediaFrame` |
| The SPA does not read `attachments` from REST history | `src/lib/api/sessions.ts::rawToMessage` forwards no `attachments` and no media |
| Assistant file cards come only from `media` frames | `src/store/chat/slices/replay-and-status-frames.ts` (`case 'media'`) is the only writer of `ChatMessage.media` besides the user's own outbound upload (`outbound-lifecycle.ts`) |
| On reload, `media` frames arrive through the WebSocket history snapshot | `sendPreparedHistorySnapshot` sends `prepared.ReplayFrames`; `prepareGroup` collects `generated.MediaFrame` from `replayEntry` -> `emitToolCallFrames` -> `buildMediaFrame` |

Consequence: `library_ref` is added only to `MediaPart` (both copies). `Attachment.yaml` and the REST history schema are not changed. FR-021's reload half is wired into the history-page preparation (`prepareGroup` / `streamReplayState`), which serves the WebSocket snapshot. A known limit, not changed by this feature: earlier pages fetched over REST (`src/store/chat/history.ts`, `fetchSessionMessages` with `before`) show no assistant file card at all today (I, from the facts above), so they show no "Open in Library" either.

## Tool-to-hub seam (D-seam, D-hub) — closed

`agent.ToolExecEndPayload` (`pkg/agent/events.go`) has one result field, `Result string` (the ForLLM text). Since the design note it also carries `Cancelled bool` (a confirmed terminal cancellation, distinct from an error), and `ToolCallID` is typed `session.ToolCallID`. `pkg/gateway/websocket_forward_hub.go::hubToolExecEnd` forwards that string and reports status `cancelled` separately from `error`. The frame needs `workspace_id`, `path` and `request_id`, which are not in that string, and must not be scraped out of it: `pkg/agent/loop_run_turn_tools.go` appends circuit-breaker and repeat notices onto `ForLLM` after the tool returns, and `pkg/tools/result.go`'s own `Deferred` comment forbids parsing ForLLM for engine decisions.

**Decision.** A typed optional pointer, the same shape as `ToolResult.Deferred` (`json:"-"`, never a wire field, never persisted):

| Carrier | Field | Rule |
|---|---|---|
| `pkg/tools/result.go::ToolResult` | `OpenInLibrary *LibraryOpenTarget` with `json:"-"` | Set only by `show_to_user` on success, together with the request-worded ForLLM text. Nil on every other tool and on every error. |
| `pkg/tools` (same file) | `LibraryOpenTarget` | Three strings: `RequestID`, `WorkspaceID`, `Path`. Not a generated wire type. |
| `pkg/agent/events.go::ToolExecEndPayload` | `OpenInLibrary *tools.LibraryOpenTarget` | Copied in `pkg/agent/tool_result_events.go::emitAdmittedToolResult` only when `!IsError`. The other two payload literals (`loop_run_turn_tools.go`'s async-error emit, `goal_record_wiring.go`'s goal anchor) omit it, so it stays nil. `loop.go` is not edited (its line count is pinned). |

`RequestID` is 16 bytes from `crypto/rand`, hex-encoded to 32 lowercase characters, minted inside the tool before it returns. If `crypto/rand` fails, the tool returns an error, sets no pointer, publishes no frame, and the audit reason is `request_id_unavailable`. No fallback to `call_id`.

`hubToolExecEnd` publishes the frame only when `Tool == "show_to_user"` AND `!IsError` AND `!Cancelled` AND `OpenInLibrary != nil`. It builds the generated `OpenInLibraryFrame` from the pointer, never from `Result`; `call_id` is `string(p.ToolCallID)`, the same conversion the `tool_call_result` frame uses. A hook that replaces the `ToolResult` drops the pointer and therefore drops the frame; that is correct, because the success sentence and the pointer are set on the same value.

**`deliverLive(sessionID, frameBytes)`** is a new method on `sessionHub`, in a sibling file of `pkg/gateway/ws_session_hub.go` (do not grow `publishMeta`). It is not `publishMeta` and it is not `hubPublishMetaAlsoTo`.

| Step | Behaviour |
|---|---|
| 1. Lookup | `hubRegistry.lookup(sessionID)`. Nil means no hub: return. Do not call `getOrCreate`. A delivery must not create a hub (BS-6.11: a later attach opens nothing). |
| 2. Evicted | Take `h.mu`. If `h.evicted` is set, unlock and return. Do not follow `publishMeta`'s re-route through `getOrCreate`. An evicted hub has already lost its connections (`evictIdle` only removes an idle hub), and a hub created later is a new attach that must not receive this frame. |
| 3. Enqueue | For each bound `hubConn`, call `enqueue` with the raw bytes. Do not call `spliceSeq`. The JSON has no `seq` key. |
| 4. Overflow | `enqueue` returns false when the connection is dead or was just closed for falling behind (`wsConn.enqueue`, which then calls `closeCatchUp`). Collect those connections, `delete` them from `h.conns` while `h.mu` is held, unlock, then call `h.onOverflow` for each, outside the lock. Same order as `publishMeta`. |
| 5. Untouched | Do not increment `head`, do not call `appendJournalLocked`, do not call `proj.update`, do not add to `publishedTotal`, do not call `enforceGlobalBudget`, do not call `maybeSweepIdle`, do not change `lastActive`. |
| 6. Hold mode | `enqueue` appends to `held` while `holding` is true (`ws_conn_queue.go`). `releaseHold` moves `held` onto the live queue after `catch_up_complete`. The client therefore sees the frame only once catch-up has finished, so the "not awaiting catch-up" guard does not drop it. That tab was already attaching, which is "attached" under D-B. It is not the disconnected-tab case. |

Tests 14 and 15 cover this table, including one connection in hold mode and one whose `enqueue` returns false.

## Watching (Q1-panel, D-watch, D-presence) — closed

Founder, 2026-10-15, replacing working assumption A. Two parts: when the Library may open by itself, and a fact the agent can read.

### What "watching" means

A browser tab is watching session S only when both are true:

| Test | Rule |
|---|---|
| Tab visible | `document.visibilityState === 'visible'`. A background tab, a minimised window, or a hidden document is not watching. This is the founder's "visible tab". It is not `document.hasFocus()`: clicking a side panel in the same window must not look like the user left. |
| Chat view active | `useSessionStore.activeSessionId` (`src/store/session.ts`) is S, and a chat screen for S is mounted. The three mount points are `src/routes/_app/sessions.$sessionId.tsx` (it sets `activeSessionId` through `setActiveSession` / `attachToSession`, and redirects into the workspace chat when the session has an active workspace), `src/components/workspaces/WorkspaceChatTab.tsx::WorkspaceChatTab` (rendered by `workspaces.$workspaceId.chat.tsx`, renders `ChatScreen`), and `src/components/chat/OmnipusChatScreen.tsx::OmnipusChatScreen` (route `/omnipus`, renders `ChatScreen` once Omnipus's main session is the active session). The former `admin.chat.tsx` was deleted with Admin (`88c63303e`). Settings, the full-page Library (`library.tsx`), the board, the calendar route and group-chat rooms (`workspaces.$workspaceId.rooms.$roomId.tsx`) are not the chat view. A side panel beside the chat does not unmount it, so the tab stays watching. |

The session is watching when any attached tab is watching (founder: any tab focused = watching). No tab, an expired claim, or a failed lookup means not watching. Never default to yes.

### How the SPA reports it

A new client→server frame `chat_presence` (contract table). Not a field on `attach_session`: attach runs only when the socket attaches, and watching changes without a re-attach (the user switches tabs or routes). The existing `ping` frame stays a connection heartbeat; it has no session and `additionalProperties: false`, so presence is not added to it.

One hook, `useChatPresence(sessionId)`, mounted from `sessions.$sessionId.tsx`, `WorkspaceChatTab` and `OmnipusChatScreen` only (Omnipus never calls `show_to_user` under D-F2, but the presence line must still be truthful for its chat), not inside `ChatScreen.tsx` (2,956 lines, over the 2,000-line warn). The Mail panel's `mailPanelPresence.ts` is the in-tree model for mount/unmount lifecycle and best-effort sends. It sends `watching: true` when the tab becomes watching, `watching: false` on hide, on session change, and on unmount, and it re-sends `watching: true` every 15 seconds while it remains watching. It does not send for any other session.

The server accepts the frame only from a connection allowed to attach to that session (the same authorisation as `attach_session`). Any other session id is ignored. The frame is not journaled, not replayed, and not written to the transcript.

### How a claim expires

In-memory only, owned by the gateway, one set of claims per session id. Each claim is the connection plus the time it was last refreshed.

| Event | Effect |
|---|---|
| `watching: true` | Upsert this connection's claim, refresh the time. |
| `watching: false`, hook unmount, or the connection unbinding | Delete this connection's claim at once. |
| No refresh for 45 seconds | The claim is expired and does not count. 45 seconds is three missed 15-second refreshes. The number is an architect choice (D-presence), not a founder one. |
| At least one unexpired claim | The session is watching. |

`pkg/agent` and `pkg/tools` must not import `pkg/gateway`. The gateway injects `func(sessionID string) bool`. A nil func means not watching.

### What the agent sees

In the dynamic, uncached turn context (`pkg/agent/context.go::ContextBuilder.buildDynamicContext`), inside `## Current Session`, one exact line for a web chat turn. `buildDynamicContext(delegationDepth int, workspaceID, channel, chatID, senderID, senderDisplayName string)` receives no session id today, so the line needs either a per-turn injector that is handed the turn's transcript session id (precedent: `WithModeNoteInjector` and `WithWorkingDirInjector`, wired in `pkg/agent/loop_env.go`) or a new parameter. Choosing between them is a U3 implementation choice; the output below is fixed. `context.go` is 2,024 lines (over the 2,000-line warn), so keep the addition small or put the injector in a sibling file:

`User watching: yes`

or

`User watching: no`

The line is about that turn's own transcript session only. A parent turn does not report a child's bit, and a child turn does not report the parent's. A non-web channel omits the line (`show_to_user` already refuses there). The line is not stored in the transcript. The model-facing text does not include a tab count, a user id, or a timestamp.

`show_to_user` does not refuse when the user is away. The founder asked for a notice in that case, not an error. The result text says which branch happened, so the model is not told the Library opened:

| At call time | Result text |
|---|---|
| Watching | `Asked the Library to open <name>.` plus the `link:` line |
| Not watching | `The user is not watching this chat. Left an "Open in Library" notice; the Library was not opened.` plus the `link:` line on web chat |

The base-instruction section and the `show_to_user` description each add one sentence: call it when the line says yes; when it says no, tell the user where the file is and do not expect the Library to open.

### What the screen does

`deliverLive` still sends the frame to every bound connection (D-B). Each tab decides once, when the frame arrives, in `decideLibraryOpen`. It does not decide again when the tab later becomes visible. A late return uses the notice; it does not auto-open after the fact.

| Step | Condition | Result |
|---|---|---|
| 1 | This tab is watching, `guardPending` is false, and the Library leave gate would pass (no unsaved Library edits) | `open` |
| 2 | Anything else, including not watching, a pending discard dialog, or a dirty Library | `notice` |

There is no `drop` for "user not watching": the notice is the founder's otherwise-branch. `guardPending` still stops the notice's Open action from stacking a second dialog.

Consequences of replacing assumption A, recorded so they are not silent:

| Situation | Result |
|---|---|
| User is watching, Calendar or Mail is the side panel, and it is not dirty | The Library opens and replaces that panel. |
| User is watching, the Library has unsaved edits | Notice. Clicking it runs the leave gate (R6). Nothing is discarded by itself. |
| User is watching on a phone | The Library opens, even though it covers the chat. Phone width is not a gate. |
| User is watching in one tab and a second tab of the same chat is in the background | The session's context line is yes. The visible tab opens. The background tab shows the notice. |
| No tab is watching | Context line is no. Every attached tab shows the notice. Nothing opens. |

---

## Existing Codebase Context

GitNexus tools were not connected in the authoring session or in the 2026-10-29 drift sync; no graph impact was run. All rows are grep/read based on `b7d4483c9` (Inferred where marked). Step 0 of U1/U3/U5/U6/U7: run `impact` on the symbols below and record caller counts before editing (shared rule 9). If GitNexus is still unavailable then, do a Grep sweep and label the row Inferred.

| Symbol | Role | Fact |
|---|---|---|
| `src/components/chat/markdown-shared.tsx::createLinkRenderer` | modifies | `a: createLinkRenderer(null)` is module-level in both `markdown-text.tsx` (live) and `historical-markdown.tsx` (finalized); no workspace input (V) |
| `src/lib/url-safe.ts::isSafeHref` | unchanged | http, https, mailto, tel only (V) |
| `src/components/library/LibraryPanel.tsx` | modifies | docked explorer receives `initialWorkspaceId` only (V; file unchanged since `b45fd6f16`). `asLibraryAddress` / `asWorkspaceContext` ignore a panel context that carries `sessionId` (a field `src/store/ui.ts` added since the design note), so chat-link opens pass a workspace context without `sessionId` |
| `src/components/workspaces/mail/MailPanel.tsx`, `MailAttachmentViewer.tsx` | regression consumer | already call `openPanel('library', { workspaceId, path })` for "Open in Library" after saving an attachment; the docked panel ignores the `path` today (I), so U6 also fixes them |
| `src/routes/_app/library.tsx` | reused | standalone `/library` route with `workspace` and `path` search params (V) |
| `src/components/panel-shell/leaveGate.ts::leaveGateThen`; `useUiStore` `activePanel`, `guardPending`, `openPanel` | calls | one registered guard today: the Library (V). `historyPushed` is NOT an input (D-takeover) |
| `src/components/panel-shell/SidePanelShell.tsx` (`rowWidth`, `takeover`); `panelWidth.ts::isPhoneTakeover` | unchanged | Not an input. U7 does not edit the shell (V symbols, superseded decision) |
| `src/lib/toolVisibility.ts::shouldRenderToolCall` | modifies | default branch returns true, so an unlisted tool shows a card; U7 adds an explicit `show_to_user` case (V; the old `switch_agent` case is gone) |
| `src/components/chat/ChatScreen.tsx` | modifies (shrinks) | two inline assistant `<a ... download={m.filename}>` blocks (V grep: two sites; the review counted three, U8 locates the third if any); file is 2,956 lines vs grandfathered 3,607 in `scripts/budgets/files.txt` (V) |
| `src/store/chat/slices/replay-and-status-frames.ts` (`case 'media'`, `handleReplayAndStatusFrame` pinned at 580), `types.ts::MediaAttachment` (`type`, `url`, `filename`, `contentType`, `caption?`) | modifies | media part mapping; the only writer of assistant `ChatMessage.media` (V) |
| `src/components/chat/OmnipusChatScreen.tsx::OmnipusChatScreen` | modifies (hook mount) | third chat view (route `/omnipus`), renders `ChatScreen` (V) |
| `pkg/gateway/websocket_forward_hub.go::hubToolExecEnd` | modifies | HIGH risk (every tool end passes through). Addition: when the new pointer is set, call `deliverLive`. Do not parse `Result` |
| `pkg/agent/events.go::ToolExecEndPayload`; `pkg/tools/result.go::ToolResult` | modifies | one optional pointer each (D-seam). `Result` stays a string; the payload now also has `Cancelled` and a typed `ToolCallID` (V) |
| `pkg/agent/tool_result_events.go::emitAdmittedToolResult` | modifies | the only copy site (file unchanged since `b45fd6f16`). `loop.go` is not edited (still pinned, `pkg/agent/CLAUDE.md`) |
| `pkg/gateway/ws_session_hub.go::publishMeta`, `lookup`, `getOrCreate`; `ws_conn_queue.go::enqueue`, `releaseHold` | models | `deliverLive` is a sibling, not a change to `publishMeta` (V; both files byte-unchanged since `b45fd6f16`) |
| `pkg/gateway/history_page_websocket.go::sendPreparedHistorySnapshot`; `history_page_prepare.go::prepareGroup`, `validateHistorySourceFrame`; `history_replay_entry.go::replayEntry`; `history_page_http.go::serveSessionHistory` | calls / modifies (U4) | reload path. Media frames are rebuilt per page and memoised (`history_page_media.go::historyPreparedMedia`) (V) |
| `pkg/gateway/websocket.go::wsFrameSchemaName` | modifies | inbound frame-type → schema map; gains `chat_presence` (V) |
| `pkg/gateway/mail_presence.go`; `src/components/workspaces/mail/mailPanelPresence.ts` | model only | connection-bound presence with teardown on disconnect (V) |
| `pkg/library/root.go::CleanRelPath` | calls | `path.Clean`, C0-only control check, rejects backslash, leading `/`, `..`-prefixed elements (V) |
| `pkg/workspace/instructions.go::safeID` | calls | workspace-id rule: non-empty, no `/`, `\`, `..` (V) |
| `pkg/tools/base.go::ToolChannel`, `ToolWorkspaceID`, `TurnWorkspaceDir`, `ToolRootChatSessionID`; `pkg/tools/ask_user_question.go::webChannelName` ("webchat") | calls | all exist (V) |
| `pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory` (and `adr090SparseRolePolicies`, `omnipusSupportedTools`) | unchanged (reads the core) | Omnipus gets exactly `omnipusSupportedTools`, everything else denied. Mia, Jim, Planner, Researcher and Worker also receive every non-deny entry of `NewCustomAgentToolsCfg()`; Planner, Researcher and Worker after `RestrictForWorkerTier` (V). No per-role `show_to_user` grant is needed |
| `pkg/coreagent/seed.go::NewCustomAgentToolsCfg`, `workerTierDeniedTools`, `RestrictForWorkerTier` | modifies | founder default tool table of 2026-10-22: Main-agent creation defaults and the Worker-only denies (V) |
| `pkg/coreagent/core.go` agent IDs | reads | `omnipus`, `mia`, `jim`, `worker` (General Purpose), `planner`, `researcher`, `judge`, `plansupervisor`; no Ava, no Admin (V; guard `scripts/check-no-ava-admin-agent-ids.sh`) |
| `pkg/agent/context.go` `<operating_rules>` "### Files and artifacts" rules 25 to 27; `context_env_test.go` `{"Rule 2", "Save code, HTML, scripts, CSS, JSON, YAML, configuration,"}` | modifies | Old "Rule 2 Artifacts over chat" was replaced on 2026-10-21 (`ed39230ab`). Rule 27 reads "After reading back saved artifacts, provide their verified full absolute paths." (V) |
| `pkg/agent/loop_env.go::wireModeNoteInjectors`, `wireWorkingDirInjectors` | model only | per-turn injector precedent for the `User watching` line (V) |
| `pkg/knowledge/tools.go::checkRetrievalRate` | model only | keys on agent id. The new limiter copies the shape (an `Allow` decision) and NOT the key (D-limit, V) |
| `pkg/agent/steer_reconstruct.go::reconstructSteeredTurn` | calls | a steered child is stamped channel `"webchat"` (V). The channel gate cannot refuse a worker |
| `pkg/tools/base.go::ToolRootChatSessionID`, `ToolTranscriptSessionID`; `pkg/agent/loop_run_turn.go` → `pkg/agent/browser_deferral.go::withBrowserRootChatSessionID` | calls | the worker refusal compares these two (V) |
| `pkg/steer/types.go::Boundaries` | unchanged | closed inventory. No new boundary (D-boundary, V) |
| `pkg/gateway/webchat_channel.go::SendMedia`; `pkg/gateway/replay.go::buildMediaFrame` (reached via `streamReplayState.emitFlatToolCall` / `emitSpawnParentToolCall`); `pkg/bus/types.go::OutboundMediaMessage` | modifies | live message already carries `WorkspaceID`; the replay builder does not (V). See FR-021 |
| `pkg/session/unified.go::UnifiedStore.GetMeta` → `UnifiedMeta.WorkspaceID` | calls | replay's workspace id (V; `UnifiedMeta` embeds `SessionMeta`) |
| `src/lib/api/sessions.ts::rawToMessage`; `contracts/components/schemas/SessionHistoryResponse.yaml`, `Attachment.yaml` | unchanged | REST history carries no sent-file card (see "REST history path", V) |
| `src/components/rooms/RoomTranscript.tsx` | unchanged | group chats show agent text as plain text; out of scope (V) |
| `show_to_user`, `open_in_library`, `chat_presence`, `omnipus://`, `library_ref`, `deliverLive`, `LinkLine` | new | 0 occurrences in `pkg cmd internal src contracts docs tests scripts` at `b7d4483c9` (V; control search for `send_file` finds 61 files) |

Impact (Inferred, to be measured): `hubToolExecEnd` HIGH (all hub forwarder tests: `sprint_h_forwarder_test.go`, `toolexecend_live_error_parity_test.go`); `LibraryPanel` docked branch MEDIUM-HIGH (`LibraryPanel.test.tsx`, pop-out via `registerExpandContext`, plus the two Mail callers); `createLinkRenderer` MEDIUM; `MediaPart` MEDIUM (generated Go/TS, history preparation decodes it); `history_page_prepare.go` MEDIUM (shared by WS snapshot and REST page); `seed.go::NewCustomAgentToolsCfg` MEDIUM (every custom agent and every built-in's core); `context.go` rule text MEDIUM. HIGH risk on `hubToolExecEnd` is flagged to the founder.

---

## User Stories & Acceptance Criteria

### User Story 1 - Open a workspace file from a chat link (P0)

A user reads an agent reply that mentions a file it wrote. The user clicks the file name and the Library shows that file. Today file names are plain text and the docked Library ignores any path it is given.

**Why this priority**: R1 core promise; everything else builds on the click.
**Independent Test**: one workspace, one written file; click the link in the reply; the Library shows that file.

**Acceptance Scenarios**:
1. **Given** an agent wrote `notes/plan.md` in workspace W, **When** the reply links it and the user clicks, **Then** the Library opens at that file in W.
2. **Given** a bare relative link in a chat that has a known workspace, **When** clicked, **Then** it opens against that workspace.
3. **Given** the Library is open on file A, **When** the user clicks a link to file B, **Then** the Library moves to B.
4. **Given** the Library holds unsaved edits, **When** the user clicks a link, **Then** the unsaved-edits prompt appears and nothing changes until the user decides.
5. **Given** the link names a missing file, **When** clicked, **Then** the Library shows its existing "file not there" landing.
6. **Given** a reply is still streaming, **When** it contains a link, **Then** the link is already clickable and identical to the finished message.
7. **Given** a chat with no known workspace, **When** a bare relative link appears, **Then** it is plain text.
8. **Given** the visible link text differs from the real target, **When** the link is shown, **Then** the real target is visible without hovering.

### User Story 2 - Open knowledge notes and bases from chat (P1)

A user asks about a note or a base; the agent answers with a link that opens it in the Library with the knowledge rail. Saved views are out of scope (D-Q4).

**Why this priority**: R1 second clause; reuses US-1.
**Independent Test**: `knowledge_read` on a note returns a link line; clicking opens the note.

**Acceptance Scenarios**:
1. **Given** a note in a knowledge collection, **When** the agent calls `knowledge_read` on web chat, **Then** the result includes a `link:` line to the note's Library location.
2. **Given** a `.base` file, **When** its link is clicked, **Then** the Library shows the base preview.
3. **Given** a collection under a mounted folder, **When** a note link is produced, **Then** it uses the mount name as first segment and opens correctly.

### User Story 3 - Websites still open in a new tab (P1)

**Why this priority**: R1 third clause; regression guard on the changed renderer.
**Independent Test**: an https link opens a new tab.

**Acceptance Scenarios**:
1. **Given** `https://example.com`, **When** clicked, **Then** a new tab opens with no opener access.
2. **Given** an existing mail deep link or `mailto:`, **When** clicked, **Then** behaviour is unchanged.
3. **Given** `![x](omnipus://file/...)` as an image, **Then** it stays blocked.
4. **Given** any non-chat-link URL with an unsafe scheme, **Then** the URL transform itself still blanks it on both render paths.

### User Story 4 - Sensible path display (P0)

By default the user sees file names or workspace-relative paths. If the user asks where a file is, the agent gives the absolute path and a link. A file the Library cannot open is shown with its full path and no link.

**Why this priority**: R2, R3, R4.
**Independent Test**: one file in the workspace, one in the agent's private folder; compare replies.

**Acceptance Scenarios**:
1. **Given** a workspace file, **When** the agent mentions it, **Then** the text is the name or a relative path, never absolute.
2. **Given** the user asks "where is it?", **Then** the agent gives the absolute path and a link.
3. **Given** a file outside every Library, **Then** the agent shows the full path as code with no link.
4. **Given** a write tool result for such a file, **Then** it carries no `link:` line.
5. **Given** a write on a non-web channel, **Then** the result carries no `link:` line, for Library files too (D-A).

### User Story 5 - "Open in Library" on sent files (P1)

**Why this priority**: R5.
**Independent Test**: `send_file` a workspace file in web chat; the card shows two actions.

**Acceptance Scenarios**:
1. **Given** a workspace (non-image) file sent in web chat, **Then** the card shows download and "Open in Library".
2. **Given** a screenshot or non-Library file, **Then** only download shows.
3. **Given** the file is sent over Telegram or email, **Then** the recipient gets the file and no Omnipus link, and the agent is told to refer to it by name.
4. **Given** a page reload, **Then** the card still shows "Open in Library".
5. **Given** a workspace image sent with `send_file`, **Then** it keeps its existing image toolbar and gains no new action in this feature (Assumption A7).

### User Story 6 - Agent opens the Library for the user, live only (P0)

While a reply is running, the agent calls `show_to_user`. If the screen is free or a clean Library is open, the Library moves to the file; otherwise the chat shows a quiet "Open in Library" notice. It never happens on reload or reconnect, for a session not on screen, or from a delegated worker.

**Why this priority**: R6; riskiest behaviour, most security scenarios.
**Independent Test**: live run opens the panel; reloading the same session does not.

**Acceptance Scenarios**:
1. **Given** a live turn and this chat is the visible tab's chat view, **When** the agent calls `show_to_user` on an own-workspace file, **Then** the Library opens on it without taking keyboard focus, including on a phone and including when Calendar is the side panel.
2. **Given** the tab is hidden, the chat is not the view on screen, or the Library has unsaved edits, **Then** a notice "Open in Library" appears and nothing opens.
3. **Given** the user clicks the notice, **Then** the leave gate runs, then the Library opens.
4. **Given** reload or incremental reconnect, **Then** nothing opens and no notice appears.
5. **Given** a target outside any Library, in another workspace, unreadable, missing, a turn with no workspace, a delegated worker session, or a non-web channel, **Then** the tool returns an error and nothing opens.
6. **Given** more than five calls in a minute in one chat, **Then** the extra calls return an error.

### User Story 7 - One clear rule for agents (P1)

**Why this priority**: R7; without guidance agents invent links or copy absolute paths.
**Independent Test**: assembled base instructions contain the rewritten "### Files and artifacts" group and no longer tell agents to report full absolute paths after every save.

**Acceptance Scenarios**:
1. **Given** an agent's assembled base instructions, **Then** the "### Files and artifacts" group of `<operating_rules>` covers links, path display and artifacts-over-chat, keeps continuous rule numbering, and the old rule 27 text ("provide their verified full absolute paths") is gone (D-F1).
2. **Given** the `send_file` and `show_to_user` descriptions, **Then** each carries one added sentence.
3. **Given** `docs/agents.md`, **Then** it no longer says agents report full absolute paths after saving; it states the name-plus-link default and the absolute path on request (D-F1).

---

## User Journeys

| Journey | Steps (user view) | Ends with |
|---|---|---|
| J1 Click a link | Reads reply; sees a file name styled as a link; presses Enter or clicks; Library appears beside chat on that file; chat scroll and composer untouched | File visible; if the Library had unsaved edits, the discard prompt first |
| J2 Click a sent-file card action | Sees file card with Download and "Open in Library <name>"; activates the second; same as J1 | File visible |
| J3 Agent opens the panel | Asks "show me the plan" while looking at the chat; agent replies and the Library moves to the file while typing focus stays in the composer; screen reader hears "Opened plan.md in Library" | File visible. If the user is not looking at this chat, the "Open in Library" notice instead |
| J4 Notice instead | Is working in Calendar; agent shows a file; a quiet notice "The agent wants to show you plan.md" with an Open action appears in the chat; user ignores it or clicks it | No interruption unless the user clicks |
| J5 File outside Library | Asks for a private agent file; sees the full path as code text; nothing clickable | Understands where it is |
| J6 After reload | Reloads the page | Chat history and cards return; no panel flips, no notice |

## UI States

| Surface | State | Behaviour |
|---|---|---|
| Chat link (internal) | resolvable | Anchor with real `href`, visible target hint if text differs |
| | unparseable / unsafe / no workspace for bare link | The same node `MarkdownLink` already renders for an unsafe href: a struck-through span with `title="Link removed: unsafe URL scheme"` (`markdown-shared.tsx`). Not a raw text node. Tests assert that treatment |
| | modified click (Ctrl/Cmd/middle/shift) | Browser default: opens the standalone `/library?workspace=&path=` page in a new tab |
| Docked Library on a deep address | loading | Library's existing loading state |
| | file missing | Library's existing "file not there" landing |
| | workspace not accessible to this user | Library's existing denied/not-found state; no file content shown (test asserts) |
| Card action | button present / absent (no `library_ref`) | per US-5 |
| | gate pending | Button does nothing further until the discard decision |
| `open_in_library` notice | shown | One notice per session (a newer one replaces the older); never persisted in the transcript; gone after reload. Two actions: Open (`Button`) and Dismiss (`IconButton`, `aria-label="Dismiss"`) |
| | Open clicked | Notice removed; the leave gate runs, then the Library opens |
| | Dismiss clicked, or Escape while focus is inside the notice | Notice removed; nothing opens. Escape while focus is in the composer does not dismiss it (that key already closes panels) |
| Auto-open | opened | Announced politely; focus unchanged |
| | dropped (replaying, other session, duplicate `request_id`, `guardPending`) | Nothing visible |

## Accessibility and Keyboard

- Internal chat links are real anchors: Tab reaches them, Enter activates, visible focus ring from the design system.
- "Open in Library" has an accessible name that includes the file: `aria-label="Open <filename> in Library"`.
- Auto-open never moves keyboard focus; it announces through an `aria-live="polite"` region ("Opened <name> in Library"). The notice is announced the same way and its action is reachable by Tab.
- Spoofing defence is not hover-only: when the visible text differs from the decoded target (compared to the full path and to the base name), a muted suffix with the target ("W / path") is shown after the text. The suffix element has an id; the anchor sets `aria-describedby` to that id, and `title` to the same words. Do not set `aria-description` (it is not ARIA 1.2). No suffix means no `aria-describedby`.
- The notice's Dismiss control is a catalogued `IconButton` with `aria-label="Dismiss"`, reached by Tab after Open. Escape dismisses only while focus is inside the notice.
- A phone the user is watching follows the same open rule as a wide window. `decideLibraryOpen` does not read `historyPushed` or `panelRowWidth`.
- No emoji in UI text or stored data (root `CLAUDE.md`).

## Design-System Reuse

Load the `omnipus-design-system` skill before touching `src/components/` (root `CLAUDE.md`). Catalogue check, done against `design-system/catalog.json` (62 entries at `b7d4483c9`; the six added since round 2 are `src/components/ui/agent-icon.tsx`, `filter-menu.tsx`, `view-switch.tsx`, `word-boundary-text.tsx`, `src/design-system/agent-identity.ts` and `src/lib/agentIconArt.ts`, none of them a notice):

| Catalogued piece | Use |
|---|---|
| `src/components/ui/button.tsx` (`Button`) | "Open in Library" on the card and on the notice |
| `src/components/ui/icon-button.tsx` (`IconButton`) | Dismiss on the notice |
| `src/components/ui/toast-container.tsx` | Not used. A toast is a timed global stack. This notice stays until the user acts, is one per chat, and is replaced rather than piled up. |
| `src/components/ui/alert-dialog.tsx` | Not used. The away-path is a notice, not a dialog. The existing unsaved-edits dialog still runs when the user clicks through a dirty Library. |
| `src/components/ui/disclosure-row.tsx` | Not used. This is not a disclosure. |

The notice shell and the extracted media item are local components for that reason. Tokens only, Phosphor icons only, no raw controls. The design-system gate stays green.

---

## Behavioral Contract

Primary flows:
- When a reply contains a link to a workspace file, the system renders a clickable link that opens the Library at that file.
- When a web-chat write/edit/append, `send_file`, `show_to_user` or `knowledge_read` succeeds on a Library file, the result gives the agent the link to use.
- When `show_to_user` succeeds in a live turn and this tab is watching, the Library opens on the file. When this tab is not watching, the chat shows the notice and nothing opens.
- When `send_file` delivers a workspace file in web chat, the card offers "Open in Library".

Error flows:
- When a link is malformed, uses an unknown scheme or fails the path rules, it renders as plain text.
- When `show_to_user` cannot open the target (outside, other workspace, unreadable or missing, no workspace in the turn, delegated worker, no UI), it returns an error and opens nothing.
- When a file is missing or forbidden, the agent gets identical error text.

Boundary conditions:
- Names with brackets, parentheses, spaces or non-ASCII characters round-trip to the exact file.
- Names containing DEL, C1, bidirectional-control or zero-width characters get no link anywhere.
- The frame is ignored while replaying, awaiting catch-up, for another session, on a duplicate `request_id`, or while a discard prompt is pending. A repeated `call_id` with a new `request_id` is not ignored.
- A sixth call in one minute in one chat returns an error.

---

## Edge Cases

- Two tabs on one chat: the session is watching if either tab is; only a watching tab auto-opens (D-B, Q1-panel).
- A tab disconnected during the call never opens later (Replay guarantee).
- `show_to_user` with no connected tab: the call succeeds as a request; the result text says it opens only if the user is watching (U6 test).
- Write target that does not exist yet: resolver resolves the deepest existing parent.
- Case-insensitive filesystem: folding via the Library's existing helper (`pkg/library/casefold.go`).
- `send_file` re-registering an already registered path: button still appears.
- Mount name equal to a real top-level folder name: mount wins only if declared (see Resolver).

---

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not auto-open on replay, reload, reconnect or a session that is not on screen, because a surprise panel flip on reload is what R6 forbids.
- The system must not auto-open when this tab is not watching, must not discard unsaved Library edits on its own, and must not take keyboard focus on auto-open (Q1-panel, R6). A side panel with no unsaved edits may be replaced when the user is watching.
- The system must not auto-open another workspace's content (D-Q5), because an action is sharper than a passive link.
- The system must not render a link for a path outside every Library (R4).
- The system must not copy agent-supplied link text into an `href`; the SPA rebuilds the target from parsed fields.
- The system must not widen `isSafeHref` or allow `omnipus://` in images.
- The system must not add a REST endpoint for the resolver.
- The system must not give `show_to_user` to Planner, Researcher, General Purpose (`worker`), any custom worker, or Omnipus (D-F2: Omnipus keeps its fixed tool list).
- The system must not render chat links in group-chat rooms in this feature (`RoomTranscript.tsx` shows agent text as plain text); rooms are out of scope.
- The system must not add `link:` lines to other tools (D-Q6) or to any non-web channel (D-A), because external recipients would get dead links disclosing the workspace id.
- The system must not offer any saved-view link, view guidance or view documentation (D-Q4); an `omnipus://kb/...` href is plain text.
- The system must not reintroduce a fail-closed per-agent policy backfill or hardcoded policy fallback (Hard Constraint 6; guard `scripts/check-no-fail-closed-backfill.sh`).
- The system must not make the file row in the write confirmation clickable in this feature (Follow-ups).
- The system must not add telemetry, new storage or any runtime dependency (Hard Constraints 1, 3).
- Agent text must not echo absolute paths from the environment block into replies by default (R2), and no base-instruction rule may tell agents to report full absolute paths after every save (D-F1).

### Machine-Verifiable Constraints

**Link grammar** (SPA `parseChatLink`; Go encoder):
- Accepted: `omnipus://file/<ws>/<seg>[/<seg>...]` where `<ws>` matches `^[A-Za-z0-9_.-]+$`, is not `.`/`..`-only, and satisfies the workspace rule `pkg/workspace/instructions.go::safeID` (non-empty, no `/`, `\`, `..`); plus bare relative hrefs under the Bare-link rule.
- Each `<seg>` is percent-encoded UTF-8; the encoder also escapes `(`, `)`, `[`, `]`, space, `<`, `>`.
- **The SPA check is a strict subset of `CleanRelPath`.** Parity test: every SPA-accepted path is accepted by `CleanRelPath` and round-trips. SPA-only rejections (explicit list): empty segments (`a//b`), `.` segments (`a/./b`), leading `./`, DEL, C1 controls, bidirectional controls (U+202A-202E, U+2066-2069, U+200E/F), zero-width characters (U+200B-200D, U+2060, U+FEFF), and path or workspace segments over 255 characters.
- Rejected forms (plain text): any other host (`kb` included), userinfo, port, uppercase scheme, `omnipus:file/...`, fragment, query, empty workspace.
- The Go encoder emits no link for a name containing any SPA-only-rejected character, and no link when the workspace id fails `^[A-Za-z0-9_.-]+$` or `safeID` (the same rule the SPA applies to `<ws>`, not only to path segments).

**Bare-link rule** (an `href` without scheme): accepted only if (1) the chat's workspace is known (below), (2) it does not start with `/`, `./`, `../`, `#`, `?`, `//`, (3) the first segment has no `:`, (4) the path passes the strict check above, and (5) the final segment looks like a file: it contains a `.` not in first position followed by 1-16 alphanumerics. Folders need the explicit `omnipus://file/` form. Everything else (`README`, `docs/`, `#section`, `?q=1`, `//host/x`, `a:b`) stays plain text, as today.

**Workspace for bare links**: `MarkdownLink` reads the chat's workspace from the chat session's recorded workspace via a store selector (U5 names it). A historical message uses the workspace recorded on its own session, not the one currently viewed. No known workspace (a `/sessions/$sessionId` chat without one) means bare links are plain text. **Omnipus's chat is treated as having no workspace for bare links**: its session is the Omnipus main in the default workspace (`src/lib/omnipus.ts`), but Omnipus works on every workspace from its own agent home (`pkg/agent/workspace_reroot.go`, Omnipus branch; Omnipus role prompt in `pkg/coreagent/prompts_adr090.go`), so resolving a bare link against the default workspace would usually point at the wrong file. The selector returns no workspace when the chat's agent is `omnipus` (`src/lib/omnipus.ts::isOmnipusAgentId`). `omnipus://` links always carry their own workspace and stay clickable there (D-F2: passive links).

**URL transform**: `urlTransform = (url) => isChatLink(url) ? url : defaultUrlTransform(url)` on both render paths. Test: `javascript:` is blanked by the transform itself on both paths.

**`open_in_library` frame**: server-to-client; unsequenced; non-journaled; not persisted; no replay twin.

**`link:` line**: one exported helper, `pkg/tools.LinkLine`, used by all six producers (`write_file`, `edit_file`, `append_file`, `send_file`, `show_to_user`, `knowledge_read`). `webChannelName` is unexported and `knowledge_read` lives in `pkg/knowledge`, which already imports `pkg/tools`, so six private copies are not allowed. The helper appends a line only when `ToolChannel(ctx)` equals `"webchat"` and the resolver returns workspace or mount. Never for outside paths. It does NOT also require "this session is the root chat": a steered child's channel is stamped `"webchat"` (`reconstructSteeredTurn`), and the parent model is who needs the link. The root-session check belongs only to `show_to_user`'s auto-open.

**`show_to_user` result text**: the two success sentences in "Watching" (exact). Not-found and not-permitted: byte-identical text. Other texts fixed by U3 per refusal row; none names the raw rejected path. Not watching is not an error.

**Rate limit** (D-limit): 5 per minute per chat, sliding window. The counter is one process-wide object, keyed by the root chat session id (`ToolRootChatSessionID`). The knowledge retrieval limiter (`pkg/knowledge/tools.go::RetrievalRateLimiter`, held on `ToolDeps`) is the shape to copy — an `Allow` decision — and not the key: that one is per agent. Every `show_to_user` instance receives the same limiter object. Constructing a new one inside `NewShowToUserTool` would split the budget, because tools are built per agent (`pkg/agent/loop_wire.go`, loops over `registry.ListAgentIDs()`), and BS-6.13's two-instance case would fail. It is not a field whose state lives on the tool instance. The clock is a `func() time.Time` on the limiter; tests pass a fake clock and do not sleep. Only calls that pass validation consume the budget. The counter is in memory and resets on process restart. Calls over the limit return an error and are audited. Two tool instances from two different agents' registries that see the same root chat session id share the one budget (BS-6.13). (The round-2 wording used `switch_agent`; that tool was removed in U8/DEL-07 and no longer exists.)

**Audit**: every `show_to_user` call, success or refusal, is audited with agent id, outcome and reason code (outside, other workspace, denied-or-missing, no workspace, delegated session, wrong channel, rate limit); success adds resolved workspace and path; refusals never log the raw rejected path in the user-visible error.

**Thread card**: `shouldRenderToolCall` returns true for an unknown tool name (`default`). U7 adds an explicit `show_to_user` case: a success is hidden (the Library move or the notice is the surface); an error is shown (`isError`), so a refusal is visible in the thread. Verbose chat still shows every call, because that function returns true before the switch. The case does not hide the notice or the auto-open.

**Performance and footprint**: parse is synchronous with no network call; resolver adds at most one `EvalSymlinks` per tool result (I); extra memory is the process-wide per-chat counter and the SPA handled-set (256 keys).

### Conservative Type Design

No new nominal types beyond the generated frame, `LibraryRef`, and the resolver's `HostRef`, which carries kind/workspace/rel semantics a bare string cannot.

---

## Resolver Rules (`pkg/library/resolve.go`)

Result kinds: workspace, mount, outside. Algorithm from the design note (EvalSymlinks with deepest-existing-parent for new files; turn workspace first; compare to `workspace.SafeWorkDir` then mount host paths from `workspace.LoadMounts`; case folding via the Library helper). Symlink rule (MIN-002): a symlink is "inside" only if it is a declared mount of that workspace and the final real path lies under that mount's host path; any other symlink whose real target leaves the work tree is outside. A write that arrives through the in-tree mount path (`work/docs/c.md`) resolves to the same mount result as via the host path. No REST endpoint, no SPA twin. Import-cycle check (`pkg/tools` to `pkg/library`) is U1 step 0; fallback location `pkg/workspace`.

**Omnipus (D-F2, passive links).** Omnipus's file tools root at its own agent home and may write into any workspace's work directory or mounts through its setup authority (`pkg/tools/resolvepath.go::withSetupAuthority`, built from `workspace.ListWorkspaceIDs`, `workspace.SafeWorkDir` and `workspace.AllowedMountRoots`). When the turn's agent is `omnipus` and the path is not in the turn workspace, the resolver also tries every other workspace in that same list, so a file Omnipus writes into workspace W2 gets `omnipus://file/W2/...` (a passive link the user opens with their own Library permissions) instead of being classified as outside. Files in Omnipus's own agent home stay outside (full path, no link, R4). Other agents keep the turn-workspace-only rule; their file policy cannot reach another workspace anyway. This is an architect choice made in the drift sync to give D-F2's "files in its chat still get passive links" a concrete meaning. `show_to_user` is unaffected (Omnipus does not hold it).

## Integration Boundaries

| System | Data in/out | Contract | Failure behaviour | Dev approach |
|---|---|---|---|---|
| Chat WebSocket | New unsequenced `open_in_library`; `media` gains optional `library_ref` | Contract section | SPA ignores per client guard; absent field means no button | Real hub, real frame builders |
| Library REST (`pkg/library`) | Existing reads; unchanged | Existing | Missing file: landing; denied workspace: existing denied state | Real |
| react-markdown v10 | `urlTransform` on both renderers | Library docs (V per note) | If the live primitive does not forward the prop, RED test 6 fails and U5 reports; architect decides a hook (U) | Real |
| Filesystem policy (`ResolveTurnFSPolicy`, `resolveAutoCheckedPath`) | Read check for `show_to_user` | Existing | Denied reported as not found | Real |
| Outside channels | `send_file` bytes only | Existing | No Omnipus link ever sent | Real |
| Audit | Call audited with reason code | Existing audit path (event name U) | n/a | Real |

---

## BDD Scenarios

IDs: `BS-<story>.<n>`; `SEC-n`. Categories: HP happy, AP alternate, EP error, EC edge. Layer: BE, FE, X (both).

### Feature: Links from chat to the Library

#### BS-1.1 Click a written file's link (HP, FE)
**Traces to**: US-1, AS-1
- **Given** W contains `notes/plan.md` and the message links `omnipus://file/W/notes/plan.md`
- **When** the user clicks the link
- **Then** the docked Library shows `notes/plan.md` in W

#### BS-1.2 Bare relative link, chat has a workspace (AP, FE)
**Traces to**: US-1, AS-2
- **Given** the chat's session records workspace W and the message links `notes/plan.md`
- **When** clicked
- **Then** the Library opens it in W

#### BS-1.3 Re-address an already open docked Library (EC, FE)
**Traces to**: US-1, AS-3
- **Given** the docked Library shows A in W
- **When** the user clicks a link to B in W
- **Then** the Library shows B

#### BS-1.4 Dirty Library holds navigation (EC, FE)
**Traces to**: US-1, AS-4
- **Given** the Library has unsaved edits
- **When** the user clicks a link to another file
- **Then** the discard prompt appears and the edited file stays shown; Cancel leaves all untouched; Discard opens the linked file

#### BS-1.5 Link to a missing file (EP, FE)
**Traces to**: US-1, AS-5
- **When** a link to `gone.md` is clicked
- **Then** the Library shows its "file not there" landing

#### BS-1.6 Live and finalized messages render identically (HP, FE)
**Traces to**: US-1, AS-6
- **Given** the same markdown with an `omnipus://` link and a bare link
- **When** rendered by the live and the finalized path
- **Then** both give the same clickable anchor

#### BS-1.7 Write tools hand the agent a link on web chat (HP, BE)
**Traces to**: US-1, AS-1
- **Given** an agent in W on the web channel
- **When** `write_file`, `edit_file` or `append_file` succeeds on `notes/plan.md`
- **Then** the result contains `link: omnipus://file/W/notes/plan.md`

#### BS-1.7b No link line on other channels (EP, BE)
**Traces to**: US-4, AS-5
- **Given** the turn's channel is Telegram (and again email)
- **When** a write tool or `knowledge_read` succeeds on a Library file
- **Then** the result has no `link:` line

#### BS-1.8 Link round trip for awkward names (EC, X) - outline
**Traces to**: US-1, AS-1
- **Given** a file named `<name>`
- **When** the Go encoder builds its link and the SPA decodes it
- **Then** workspace and path equal the originals and the markdown link does not end early

| name |
|---|
| `report (final).md` |
| `a[1].md` |
| `100% done #1?.md` |
| `uber/ü ñ.txt` |
| `a b.md` |
| a name equal to a mount name |
| a name starting `..x` (rejected both sides) |
| a 255-char segment accepted; a 256-char segment rejected |

#### BS-1.9 Mounted folder file (AP, X)
**Traces to**: US-1, AS-1
- **Given** W has mount `docs`; the agent writes via the host path and again via the in-tree path
- **Then** both links are `omnipus://file/W/docs/a.md` and open

#### BS-1.10 Fullscreen Library unchanged (HP, FE)
**Traces to**: US-1, AS-3
- **Then** fullscreen addressing and the pop-out URL behave as before

#### BS-1.11 Bare link with no known workspace (EP, FE)
**Traces to**: US-1, AS-7
- **Given** Omnipus's chat (`/omnipus`; treated as having no workspace for bare links), and separately a `/sessions/$sessionId` chat whose session records no workspace
- **Then** `[x](notes/plan.md)` renders as plain text
- **And** in Omnipus's chat an `omnipus://file/W/notes/plan.md` link is still clickable and opens W (D-F2)

#### BS-1.12 Historical message uses its own session's workspace (EC, FE)
**Traces to**: US-1, AS-2
- **Given** a message written in W1 is shown while viewing W2
- **When** its bare link is clicked
- **Then** it opens in W1; if the session records none, it is plain text

#### BS-1.13 Workspace the user cannot open (EP, FE)
**Traces to**: US-1, AS-5
- **Given** a link naming a workspace this user cannot open
- **When** clicked
- **Then** the Library's existing denied state shows and no file content is shown

#### BS-1.14 Spoofed text is visible without hover (EC, FE)
**Traces to**: US-1, AS-8
- **Given** `[Q3 report.xlsx](omnipus://file/W/secrets/payroll.xlsx)`
- **Then** a muted suffix with "W / secrets/payroll.xlsx" follows the text, `title` carries it, and `aria-describedby` points at the suffix. `aria-description` is absent
- **And** a link whose text equals the target path or base name gets no suffix

#### BS-1.15 Modified click and keyboard (AP, FE)
**Traces to**: US-1, AS-1
- **Then** Ctrl/Cmd/middle click opens the standalone `/library?workspace=&path=` page in a new tab; plain click uses the panel; Tab focuses and Enter activates; no `target=_blank` on the internal anchor

#### BS-1.16 Ordinary relative links stay plain (EC, FE) - outline
**Traces to**: US-1, AS-7
- **Given** `<href>` such as `README`, `docs/`, `./a.md`, `#sec`, `?q=1`, `//host/x`, `a:b`, `a//b.md`, `a/./b.md`
- **Then** each renders as plain text

#### BS-1.17 Mail "Open in Library" lands on the saved file (EC, FE)
**Traces to**: US-1, AS-3 (regression consumer of the U6 fix)
- **Given** a mail attachment saved to the Library at `inbox/report.pdf` in W
- **When** the user activates the Mail panel's "Open in Library" (`MailPanel.tsx` / `MailAttachmentViewer.tsx`, `openPanel('library', { workspaceId, path })`)
- **Then** the docked Library shows `inbox/report.pdf`, not the workspace root

### Feature: Knowledge links (notes and bases only)

#### BS-2.1 knowledge_read result carries a link (HP, BE)
**Traces to**: US-2, AS-1
- **Given** a collection whose root maps to Library path L, web channel
- **Then** `knowledge_read` of `x/y.md` includes `link: omnipus://file/<ws>/<L>/x/y.md`

#### BS-2.2 Note and base links open (HP, FE)
**Traces to**: US-2, AS-1, AS-2
- **Then** a note opens with the knowledge rail and a `.base` file opens as a base preview

#### BS-2.3 Collection under a mount (AP, BE)
**Traces to**: US-2, AS-3
- **Then** the first path segment is the mount name

#### BS-2.4 find/list stay link-free (EC, BE)
**Traces to**: US-2, AS-1
- **Then** `knowledge_find`/`knowledge_list` add no `link:` line

### Feature: Websites and unchanged links

#### BS-3.1 https link (HP, FE)
**Traces to**: US-3, AS-1
- **Then** opens in a new tab with `rel="noopener noreferrer"`

#### BS-3.2 Mail deep link and mailto unchanged (AP, FE)
**Traces to**: US-3, AS-2

#### BS-3.3 omnipus image stays blocked (EP, FE)
**Traces to**: US-3, AS-3

#### BS-3.4 Transform blanks unsafe schemes itself (EP, FE)
**Traces to**: US-3, AS-4
- **Then** `javascript:` is blanked by `urlTransform` on both paths, and valid chat links pass

### Feature: Path display and files outside the Library

#### BS-4.1 Default shows name or relative path (HP, X)
**Traces to**: US-4, AS-1
- Model behaviour; verified by UAT. The BS-7.1 test checks the instruction wording only, not agent behaviour; a green there is not a claim about the agent.

#### BS-4.2 User asks for the path (AP, X)
**Traces to**: US-4, AS-2 (UAT)

#### BS-4.3 File outside every Library (EP, BE)
**Traces to**: US-4, AS-3, AS-4
- **Given** writes to `<home>/agents/<id>/x.txt`, `$OMNIPUS_HOME/other.txt`, `/tmp/x.txt`
- **Then** no `link:` line

#### BS-4.4 Resolver classification (EC, BE) - outline
**Traces to**: US-4, AS-3

| input | kind | rel |
|---|---|---|
| `<W work>/a/b.md` | workspace | `a/b.md` |
| `<W work>/new/not-yet.md` (absent) | workspace | `new/not-yet.md` |
| `<mount host>/c.md` for mount `docs` | mount | `docs/c.md` |
| in-tree `work/docs/c.md` through the mount | mount | `docs/c.md` |
| `<home>/agents/x/mem.md` | outside | - |
| relative `a.md` | outside | - |
| NUL or CR in path | outside | - |
| undeclared symlink in W to outside | outside | - |
| rogue symlink beside a mount pointing into the mount's sibling | outside | - |
| mount named like a real top-level folder | declared mount wins | per mount |
| symlink outside pointing into W | workspace | real target rel |
| case-differing path on case-insensitive FS | workspace | folded rel |
| Windows drive and UNC forms | per OS, derived in test | - |
| turn agent `omnipus`, `<W2 work>/setup/x.md` | workspace (W2) | `setup/x.md` |
| turn agent `omnipus`, `<omnipus agent home>/notes.md` | outside | - |
| turn agent `mia` in W1, `<W2 work>/x.md` | outside | - |

### Feature: send_file card

#### BS-5.1 Card shows Open in Library (HP, X)
**Traces to**: US-5, AS-1
- **Then** the `media` part carries `library_ref` and the extracted media item shows download plus "Open in Library" with the accessible name

#### BS-5.2 No button for non-Library files (EP, X)
**Traces to**: US-5, AS-2

#### BS-5.3 Outside channels (AP, BE)
**Traces to**: US-5, AS-3
- **Then** file delivered, no Omnipus link, result text says refer to it by name

#### BS-5.4 Survives reload and re-registration (EC, X)
**Traces to**: US-5, AS-4
- **Given** a `send_file` card for a workspace file, **When** the page reloads and the WebSocket history snapshot (`sendPreparedHistorySnapshot`) replays the `media` frame, **Then** its `MediaPart` carries `library_ref` and the card still shows "Open in Library"
- **And** the REST history page (`SessionHistoryResponse`) is not a path for this card (see "REST history path"); no `Attachment.yaml` change is asserted

#### BS-5.5 Button honours the leave gate (EC, FE)
**Traces to**: US-5, AS-1

#### BS-5.6 Workspace image keeps its toolbar (EC, FE)
**Traces to**: US-5, AS-5
- **Then** `ChatImage` renders as today; no new action

### Feature: show_to_user

#### BS-6.1 Auto-open while this tab is watching (HP, X)
**Traces to**: US-6, AS-1
- **Given** a live turn, this tab visible, this chat the mounted chat route, own-workspace file
- **When** `show_to_user` succeeds
- **Then** exactly one `open_in_library` frame reaches the bound connections and this tab's Library opens on the file
- **And** focus stays where it was and the polite announcement fires
- **And** the result text is `Asked the Library to open <name>.`

#### BS-6.2 Clean Library open, tab watching: moves (AP, FE)
**Traces to**: US-6, AS-1
- **Then** the Library navigates to the file without any prompt

#### BS-6.3 Another panel open, tab watching: opens (AP, FE)
**Traces to**: US-6, AS-1
- **Given** Calendar, Mail or the live browser is the side panel, and it has no unsaved edits, and this tab is watching
- **Then** the Library replaces that panel

#### BS-6.4 Dirty Library: notice only (EC, FE)
**Traces to**: US-6, AS-2
- **Given** this tab is watching and the Library has unsaved edits
- **Then** no discard prompt opens on its own; the notice appears; clicking it runs the leave gate

#### BS-6.5 Not watching: notice only (EC, FE)
**Traces to**: US-6, AS-2
- **Given** `<case>` (tab hidden; another route mounted, such as the full-page Library or Settings; this session is not `activeSessionId`; a phone-width tab that is hidden)
- **When** a live `open_in_library` frame arrives
- **Then** the notice appears and nothing opens. `historyPushed` and `panelRowWidth` are not read
- **And** a phone-width tab that IS watching opens, same as a wide tab

#### BS-6.6 Notice click goes through the gate (HP, FE)
**Traces to**: US-6, AS-3
- **Then** clean outgoing panel: opens at once; dirty Library: discard prompt first; a pending `guardPending` drops a second notice-triggered open

#### BS-6.7 Reload and incremental reconnect never open (EC, X)
**Traces to**: US-6, AS-4
- **Given** a transcript with a prior successful call, and separately a call made while the tab was disconnected
- **When** the tab re-attaches (snapshot path, then incremental path)
- **Then** zero `open_in_library` frames are delivered, zero navigations, zero notices

#### BS-6.8 Frame guard cases (EC, FE) - outline
**Traces to**: US-6, AS-4
- **Given** `<case>` (replaying; awaiting catch-up; frame carrying a `seq`; other session on screen; duplicate `request_id`; missing or non-hex `request_id`; 257th distinct `request_id` evicts the oldest)
- **Then** nothing opens and no notice appears for the dropped frame
- **And** two frames that share a `call_id` and have different `request_id`s both act
- **And** after the 257th key evicts the oldest, a later frame with that forgotten `request_id` acts again

#### BS-6.9 Refusals (EP, BE) - outline
**Traces to**: US-6, AS-5
- **Given** `<case>`, **When** `show_to_user` runs, **Then** it returns the listed error, publishes no frame, and is audited with the reason code

| case | result |
|---|---|
| ref resolves outside every Library | "not in a Library, give the user the full path in text" |
| ref in another workspace | other-workspace error |
| ref missing | not-found/not-permitted text |
| ref denied by the agent's read policy | identical bytes to missing |
| ref is a secret carve-out path | identical bytes to missing |
| turn has no workspace (empty `ToolWorkspaceID`) | no-workspace error |
| turn is a delegated or steered worker session (`ToolTranscriptSessionID` ≠ `ToolRootChatSessionID`; the child's channel is `"webchat"`, so the channel gate does not catch it) | "only the chat the user is in can open the Library" |
| channel is not web chat | "no UI attached" |
| sixth call within a minute in one chat | rate-limit error |

#### BS-6.10 Success text matches the watching bit (HP, BE)
**Traces to**: US-6, AS-1, AS-2
- **Then** watching uses `Asked the Library to open ...` and not-watching uses `The user is not watching this chat. Left an "Open in Library" notice...`
- **And** neither sentence is an error, and neither claims the Library opened when the user was away

#### BS-6.11 No connected tab (EC, BE)
**Traces to**: US-6, AS-1
- **Given** the turn runs with no tab attached
- **Then** the call succeeds as a request; no frame is journaled; a tab attaching later opens nothing

#### BS-6.12 Several tabs (AP, X)
**Traces to**: US-6, AS-1
- **Then** each of two bound connections receives the frame, an unbound connection does not, the watching tab opens, and the other tab shows the notice (D-B, Q1-panel)

#### BS-6.13 Rate limit semantics (EC, BE)
**Traces to**: US-6, AS-6
- **Then** sliding window, 5 per minute per root chat session; a fake clock (no sleep) shows call 4 allowed, call 5 allowed, call 6 refused; calls refused by validation do not consume budget; concurrent calls cannot exceed 5 (race test under `-race`); two `show_to_user` instances built for two different agents (Mia's and Jim's registries) and called with the same root chat session id share one budget (3 + 3 calls: the sixth is refused); a process restart resets the counter

#### BS-6.14 Registered and granted by policy (HP, BE)
**Traces to**: US-6, AS-1
- **Given** a fresh install, and an old install whose saved ceiling lacks the tool, upgraded through Reconcile
- **Then** the ceiling is `allow` in both, and Reconcile never overwrites an operator value
- **And** `pkg/coreagent/seed.go::NewCustomAgentToolsCfg` lists `"show_to_user": allow`, and `workerTierDeniedTools` lists `show_to_user` (one new row in the founder's 2026-10-22 default tool table: Main allow, Worker deny). `ADR090RolePolicyInventory` gets no per-role grant: Mia and Jim receive `allow` through the shared Main core; Planner, Researcher and General Purpose (`worker`) receive `deny` through `RestrictForWorkerTier`
- **And** Omnipus resolves to `deny` (it is not in `omnipusSupportedTools`, so `omnipusSeedPolicies` persists an explicit deny; D-F2). Judge and PlanSupervisor stay explicit deny. A fresh custom Main agent resolves to `allow`; a fresh custom worker (`Subagent`, `subagent_3p`) resolves to `deny`
- **And** `show_to_user` is not in `fullManifestToolNames` and not in `PreviewedLazyToolNames` (`TestADR090_GlobalVisibility37` keeps its 36-name literal and its `serve_web` preview). `ToolManifestTier` returns `ManifestLazy`. U9's role prompts for `mia` and `jim` name `tool:show_to_user` so the model knows to load it through ToolSearch
- **And** the effective policy for each of those agents equals this table (behavioural, not a grep)

#### BS-6.15 Refusals and successes are audited (HP, BE)
**Traces to**: US-6, AS-5

#### BS-6.16 Presence marker (HP, X)
**Traces to**: US-6, D-watch
- **Given** the tab is visible on the chat route for S, **When** the hook sends `chat_presence`, **Then** a web turn for S renders `User watching: yes` and a turn for any other session renders `User watching: no`
- **And** a second hidden tab of S does not make the line no, while S's only tab going hidden or unmounting makes the next turn `User watching: no`
- **And** a claim with no refresh for 45 seconds does not count, and a refresh at 15 seconds does
- **And** a frame for a session this connection cannot attach to changes nothing
- **And** the line is absent from the transcript and from replay
- **And** a steered child's turn reports the child session's own bit, never the parent's
- **And** a visible tab on `/omnipus` makes Omnipus's next web turn render `User watching: yes` (the hook is mounted from `OmnipusChatScreen` too)

### Feature: Agent guidance

#### BS-7.1 The "Files and artifacts" group is rewritten (HP, BE)
**Traces to**: US-7, AS-1
- **Then** the "### Files and artifacts" group of `<operating_rules>` in `pkg/agent/context.go` contains, as short continuously numbered rules: link rules, relative-path default, absolute path only on request, outside files as full path, never invent an `omnipus://` URL, do not copy absolute paths from the environment block into replies, artifacts-over-chat retained (today's rules 25 and 26), links only from `link:` lines or relative paths, call `show_to_user` when the context line says `User watching: yes` and do not expect the Library to open when it says no
- **And** today's rule 27 text "After reading back saved artifacts, provide their verified full absolute paths." is gone (D-F1)
- **And** the rules after the group are renumbered so the list stays continuous, and `context_env_test.go`'s rule-text table (today `{"Rule 2", "Save code, HTML, scripts, CSS, JSON, YAML, configuration,"}`) is updated to the new text

#### BS-7.2 Tool descriptions (HP, BE)
**Traces to**: US-7, AS-2

#### BS-7.3 User documentation matches the rewritten rule (HP, docs)
**Traces to**: US-7, AS-3
- **Given** `docs/agents.md`
- **Then** the sentence "with their contents checked and full absolute paths reported" is replaced by the name-plus-link default with the absolute path on request, and `docs-verifier` rules it TRUE against the new rule text

### Feature: Security

| ID | Scenario | Layer | Cat | Traces |
|---|---|---|---|---|
| SEC-1 | `omnipus://file/W/../../etc/passwd`, `..%2f`, backslash, leading `/`, NUL/CR: plain text; server `CleanRelPath` rejects the same | X | EP | US-1 AS-1 |
| SEC-2 | Undeclared symlink out of the workspace: no link, `show_to_user` refuses with the not-found text | BE | EP | US-6 AS-5 |
| SEC-3 | Secret carve-out path (master key, credentials): `show_to_user` refuses with not-found bytes | BE | EP | US-6 AS-5 |
| SEC-4 | `javascript:`, `data:`, `vbscript:`, `file:`, `blob:`, `ftp:`: none becomes an anchor; the transform blanks them | FE | EP | US-3 AS-4 |
| SEC-5 | `OMNIPUS://file/...`, `omnipus:file/W/a`, `omnipus://evil/W/a`, `omnipus://file//a`, userinfo, port, fragment, query, empty workspace, `omnipus://kb/...`: plain text | FE | EP | US-1 AS-1 |
| SEC-6 | Link-text spoofing: target shown without hover (suffix plus `aria-describedby` on that suffix; no `aria-description`) | FE | EC | US-1 AS-8 |
| SEC-7 | Prompt-injected `show_to_user`: only own-workspace, read-permitted files; 5/min; no auto-open when the tab is not watching; never discards unsaved Library edits (notice instead); focus untouched; audited | X | EP | US-6 AS-2, AS-5 |
| SEC-8 | Reload, snapshot attach and incremental reconnect: zero navigations, zero frames in any replayed or journaled output | X | EC | US-6 AS-4 |
| SEC-9 | Missing and forbidden give byte-identical errors | BE | EP | US-6 AS-5 |
| SEC-10 | Rendered `href` is rebuilt from parsed fields, never the raw input | FE | EC | US-1 AS-1 |
| SEC-11 | `show_to_user` ref naming another workspace: error, no frame | BE | EP | US-6 AS-5 |
| SEC-12 | `send_file` of a symlink to outside: no `library_ref` | BE | EP | US-5 AS-2 |
| SEC-13 | Names with DEL, C1, bidi or zero-width characters: no link emitted, SPA renders plain text | X | EC | US-1 AS-1 |
| SEC-14 | Delegated worker session calling `show_to_user`: refused, nothing delivered to the user's session | BE | EP | US-6 AS-5 |
| SEC-16 | Omnipus has no `show_to_user` (effective policy `deny`); a prompt-injected Omnipus cannot auto-open the Library | BE | EP | US-6 AS-5 |
| SEC-15 | Non-web channel: no `link:` line, so no internal workspace id leaks to external recipients | BE | EP | US-4 AS-5 |

---

## Test-Driven Development Plan

Local runs: one narrow process at a time; CI is the authority for Go (root `CLAUDE.md`). RED first (qa-lead). `make lint-budgets` stays green throughout.

| Order | Test | Level | Traces to | Description |
|---|---|---|---|---|
| 1 | `TestEncodeLibraryLink_GoldenVectors` (Go) + `chat-link.golden.test.ts` (same fixture) | Unit | BS-1.8, SEC-13 | Vectors incl. `)`, `[`, space, `%`, unicode, 255/256-char segments, rejected characters |
| 2 | `TestSPAStrictSubsetOfCleanRelPath` (fixture-driven) + `parseChatLink` table | Unit | SEC-1, SEC-5, BS-1.16 | Every SPA-accepted path accepted by `CleanRelPath` and round-trips; explicit SPA-only rejection list; D2 |
| 3 | `TestResolveHostPath_*` | Unit | BS-4.4, SEC-2 | Table above, `t.TempDir`, per-OS derivation, mount-symlink rules, the three agent-specific rows (Omnipus into W2, Omnipus home, Mia into W2) |
| 4 | `MarkdownLink.chatLink.test.tsx` | Unit | BS-1.1, 1.2, 1.11, 1.12, 1.14, 1.15, 1.16, SEC-6, 10 | Anchor, suffix, `aria-describedby`, struck-through unsafe treatment, modified click, keyboard |
| 5 | `LibraryPanel.docked.test.tsx` (rewrite) | Unit | BS-1.1, 1.3, 1.5, 1.10, 1.13, 1.17 | Docked explorer selects path; re-addresses; missing-file landing; denied state; a context carrying `sessionId` is ignored as today; the Mail "Open in Library" path lands on the file |
| 6 | `MarkdownText.urlTransform.test.tsx` | Unit | BS-1.6, 3.4, SEC-4 | Live and finalized parity; default transform still blanks `javascript:`; settles the prop-forwarding unknown |
| 7 | `MarkdownImage.omnipus.test.tsx` | Unit | BS-3.3 | |
| 8 | `TestWriteTools_LinkLine` | Unit | BS-1.7, 1.7b, 1.9, 4.3, SEC-15 | web inside / mount / outside / Telegram / email |
| 9 | `TestKnowledgeRead_LinkLine`, `TestKnowledgeFindList_NoLink` | Unit | BS-2.1, 2.3, 2.4, 1.7b | |
| 10 | `TestShowToUser_Refusals` | Unit | BS-6.9, SEC-2, 3, 9, 11 | All rows except the worker proof; byte-equality missing vs denied; no-workspace row. Context values are built by hand, so this test is not the SEC-14 proof |
| 10b | `TestShowToUser_SteeredChildRefused` | Integration | BS-6.9 worker row, SEC-14 | Through `pkg/agent/testutil.DelegationTree`: a steered child calls `show_to_user`; the result is the worker refusal; the root hub receives zero `open_in_library` frames. No new `pkg/steer.Boundary` and no bump of the pinned `Boundaries` inventory |
| 11 | `TestShowToUser_RateLimit` (`-race`) | Unit | BS-6.13 | Fake clock, no sleep: calls 4, 5 and 6; concurrent callers; validation refusals do not consume; two instances for two agents with one root chat session id share the budget |
| 12 | `TestShowToUser_PolicySeed` (model: `ask_user_question_seed_test.go`) + the three count bumps + Reconcile add-without-overwrite | Unit | BS-6.14, SEC-16 | Effective policy: Mia, Jim allow; Omnipus, Planner, Researcher, General Purpose (`worker`), Judge, PlanSupervisor deny; fresh custom Main allow; fresh custom worker deny; fresh and upgraded config. Bump `catalogSizeToday` 119 → 120 and `postMergeAdditions` 18 → 19 (with its comment) in `pkg/coreagent/catalog_count_test.go`, and the 119-entry assertion plus its comment in `pkg/tools/auto_approve_test.go::TestAutoApprove_ClassTableMatchesFounderFile` to 120. Assert `show_to_user` is absent from `FullManifestToolNames()`. Negative controls: remove the `NewCustomAgentToolsCfg` entry (Mia/Jim must go red); remove the `workerTierDeniedTools` entry (Planner/Researcher/worker must go red) |
| 13 | `TestShowToUser_Audit` | Unit | BS-6.15, SEC-7 | Success and each refusal reason code |
| 14 | `TestHubToolExecEnd_ShowToUser_DeliversLiveOnly` | Integration | BS-6.1, 6.10, 6.11, 6.12 | Two bound connections both receive the unsequenced frame; one unbound connection does not; the frame is absent from the journal and the projection; a nil `OpenInLibrary` publishes nothing even when the tool name is `show_to_user`; one connection in hold mode receives the frame only after `releaseHold` and not in the journal tail; one connection whose `enqueue` returns false is removed and `onOverflow` runs; `lookup` nil and an evicted hub both deliver nothing and neither calls `getOrCreate` |
| 15 | `TestOpenInLibrary_NeverReplayedOrJournaled` | Integration | BS-6.7, SEC-8 | (a) snapshot attach (`sendPreparedHistorySnapshot`) with transcript containing the call, and the REST page (`serveSessionHistory`) for the same session; (b) publish during a simulated disconnect then incremental attach; assert zero `open_in_library` frames and byte-compare with a control fixture proving the same transcript's `send_file` `media` frame IS in `prepared.ReplayFrames` and a journaled frame IS in the incremental tail (instrument check; `agent_switched` no longer exists) |
| 16 | `TestSendFile_MediaPartLibraryRef` | Integration | BS-5.1, 5.2, 5.4, SEC-12 | Re-registered ref, outside, symlink; reload through `history_page_prepare.go::prepareGroup` (the media frame in `ReplayFrames` carries `library_ref`, and `validateHistorySourceFrame` still accepts it); a memoised `historyPreparedMedia` subset keeps `library_ref` |
| 17 | `TestSendFile_ChannelNoLink` | Unit | BS-5.3 | |
| 18 | `open-in-library.handler.test.ts` | Unit | BS-6.1-6.8, 6.12 | `decideLibraryOpen` with an injected `tabWatching` boolean (do not mount the shell); focus unchanged; announcement; notice Open and Dismiss, including Escape only while the notice is focused; dirty Library and `guardPending` produce a notice even when watching; bounded `request_id` set; same `call_id` with two `request_id`s both act; `shouldRenderToolCall('show_to_user', …)` is false on success and true on error, and true when verbose chat is on; a frame is not re-decided when `tabWatching` flips later |
| 19 | `AssistantMediaItem.openInLibrary.test.tsx` (name set in U8) | Unit | BS-5.1, 5.2, 5.5, 5.6 | Button, accessible name, gate, image untouched |
| 20 | Contract CHECK tests | Unit | Contract section | Items (a) to (e), including the runtime parser vitest through `src/lib/ws.ts` and the `wsFrameSchemaName` inbound check |
| 21 | `TestBaseInstructions_LinkSection`, `TestToolDescriptions_OneSentence`, `TestDynamicContext_UserWatching` | Unit | BS-7.1, 7.2, 6.16 | Wording of the rewritten "### Files and artifacts" group; absence of the old rule 27 sentence; continuous numbering (the numbers parse as 1..N without gaps); the exact `User watching: yes` / `User watching: no` line and its absence for a non-web channel |
| 22 | `chat-links.spec.ts` (Playwright) | E2E | BS-1.1-1.4, 2.2, 5.1, 6.1-6.7 | Real gateway; stubbed model output via `tests/e2e/fixtures/terminal-outcome-provider.ts`. The file MUST be assigned a shard in `tests/e2e/shards.json` (`scripts/e2e-shards.sh check` fails otherwise) |
| 23 | UAT campaign (uat-tester + uat-validator) | E2E | BS-4.1, 4.2, 6.1, 6.3, 6.7, SEC-7 | `openrouter` + `deepseek/deepseek-v4.1-flash`, exact landing commit |

### Test Datasets

| ID | Dataset | Rows | Traces to |
|---|---|---|---|
| D1 Link names | BS-1.8 names; empty; 255/256-char; 4096-char path with expected reject | boundary, unicode | BS-1.8 |
| D2 Reject set | `..`, `%2e%2e`, backslash, leading `/`, NUL, CR/LF, DEL, C1, RLO, zero-width, over-length ws segment, `..x`, `./a`, `a/./b`, `a//b`, `#x`, `?q`, `//host/x`, `a:b`, SEC-4/5 forms, `omnipus://kb/...` | error | SEC-1, 4, 5, 13, BS-1.16 |
| D3 Resolver | BS-4.4 rows | edge, error | BS-4.4 |
| D4 show_to_user | valid; other workspace; outside; missing; denied; secret; empty workspace; worker session; non-web; call 5, 6 (boundary 4/5/6); symlink inside a mount | boundary, error | BS-6.9, 6.13 |
| D5 Frame handling | watching; hidden tab; other route; dirty Library; Calendar open while watching; replaying; awaiting catch-up; hold mode; has `seq`; other session; duplicate `request_id`; same `call_id` two `request_id`s; 257th key; presence expiry at 45s; two tabs | edge | BS-6.1-6.8, 6.16 |
| D6 Media | workspace file, mount file, screenshot, symlink-out, re-registered, workspace image, file in another workspace | edge, error | BS-5.x |

### Regression Test Requirements

Preserved and must pass unchanged unless named: https and mail deep link (`markdown-shared.test.tsx`, `historical-markdown.test.tsx`, `markdown-text.test.tsx`); `isSafeHref`/image gate tests; fullscreen Library; hub forwarder parity (`sprint_h_forwarder_test.go`, `toolexecend_live_error_parity_test.go`); paged-history snapshot and REST page tests (the `media` frame still replays; `validateHistorySourceFrame` unchanged except accepting the new optional field through the generated type); `send_file` delivery and Auto-approve; Mail "Open in Library" tests (`MailPanel.attachmentHandoff.test.tsx`) still pass; tool-policy guards (`check-no-fail-closed-backfill.sh`, `check-no-ava-admin-agent-ids.sh`, `ValidateToolPolicyCoverage`, the three counts in test 12 updated and no others); Omnipus's tool set unchanged (`omnipusSupportedTools` untouched); `TestADR090_GlobalVisibility37`'s 36-name literal unchanged; `context_env_test.go` rule-text table (the one intentional change, D-F1); `LibraryPanel.test.tsx` is rewritten, not weakened; size budgets (`make lint-budgets`) incl. `ChatScreen.tsx` shrinking, `loop.go` not edited, `handleFrame` growing by at most one dispatch line, and `handleReplayAndStatusFrame` (pinned at 580 in `scripts/budgets/functions.txt`) not growing: U8 moves the `case 'media'` body into a sibling function and leaves one call behind. Shared golden-vector fixtures for tests 1 and 2 are authored by qa-lead in RED; U1 and U5 consume them and neither re-authors them.

---

## Functional Requirements

| ID | Requirement |
|---|---|
| FR-001 | The SPA MUST render `omnipus://file/<ws>/<path>` and bare relative hrefs (per the Bare-link rule) as clickable links opening the Library at that address (R1). |
| FR-002 | Live and finalized markdown MUST render those links identically; both MUST use `urlTransform` that preserves valid chat links and delegates everything else to the default transform. |
| FR-003 | `parseChatLink` MUST accept only the strict grammar (a strict subset of `CleanRelPath`) and return no match for every other form including the `kb` host. |
| FR-004 | The rendered `href` MUST be rebuilt from parsed fields; when the visible text differs from the decoded target, the target MUST be shown as a visible suffix, set as `title`, and referenced by `aria-describedby`. `aria-description` MUST NOT be set. |
| FR-005 | `isSafeHref` and image sources MUST NOT accept `omnipus://`. |
| FR-006 | https links MUST keep opening a new tab with `noopener noreferrer` (R1). |
| FR-007 | The docked Library MUST open at a supplied path and re-address on every new supplied context. |
| FR-008 | Link clicks, the card action and the notice action MUST go through the panel leave gate; a pending discard decision MUST drop a further open. |
| FR-009 | `pkg/library` MUST provide a resolver mapping an absolute host path to workspace, mount or outside under the Resolver Rules. |
| FR-010 | One Go encoder MUST build links; the SPA MUST decode them; both MUST pass shared golden vectors; the encoder MUST emit no link for names with SPA-rejected characters. |
| FR-011 | `write_file`, `edit_file`, `append_file`, `send_file`, `show_to_user`, `knowledge_read` MUST append a `link:` line for Library files on the web channel only, and MUST NOT for outside files or other channels (D-Q6, D-A, R4). |
| FR-012 | Other tools MUST NOT gain link lines. |
| FR-013 | Notes and `.base` files MUST link as ordinary Library files; no view-addressed link, guidance or documentation exists (D-Q4). |
| FR-014 | `show_to_user(ref)` MUST validate in order: channel, root-chat session (`ToolTranscriptSessionID` equals `ToolRootChatSessionID`, both non-empty), workspace present, parse, resolve, own-workspace, read policy and carve-out, rate limit; on success set `OpenInLibrary` (with a new `request_id`) and deliver one `open_in_library` frame. The channel check alone MUST NOT be treated as the worker check. |
| FR-015 | Not-found and not-permitted MUST produce identical error text; the success text MUST be a request, not a claim. |
| FR-016 | The frame MUST be delivered by `deliverLive` (Tool-to-hub seam): unsequenced, unjournaled, to currently bound connections only, never persisted, never rebuilt by replay. Overflow unbinds like `publishMeta`. An evicted or missing hub drops the frame and MUST NOT call `getOrCreate`. A connection in hold mode receives the frame after `releaseHold`. |
| FR-017 | The SPA MUST act on the frame only if it has no `seq`, the bucket is not replaying or awaiting catch-up, the session is on screen, and `request_id` is not in the bounded handled-set (last 256 `session_id`+`request_id` keys). `call_id` MUST NOT be the key. |
| FR-018 | Auto-open MUST follow `decideLibraryOpen` (Watching): `open` only when this tab is watching, no discard dialog is pending, and the Library leave gate would pass; every other arrival shows one non-modal notice with Open and Dismiss. The decision MUST NOT read `panelRowWidth` or `historyPushed`, and MUST NOT be repeated when the tab later becomes visible. Auto-open MUST NOT move focus and MUST announce politely. A successful call MUST NOT also show the raw tool card unless verbose chat is on. |
| FR-019 | `show_to_user` MUST be registered with ceiling `allow`, `AutoRuns`, catalog, and seed entries per BS-6.14: `"show_to_user": allow` in `seed.go::NewCustomAgentToolsCfg` and an entry in `workerTierDeniedTools`, so Mia and Jim allow through the Main core; Omnipus, Planner, Researcher, General Purpose (`worker`), Judge, PlanSupervisor deny; custom Main allow, custom worker deny. `omnipusSupportedTools` MUST NOT change (D-F2). Reconcile adds the ceiling to old installs without overwriting operator values. It MUST NOT be added to `fullManifestToolNames`. The three pinned counts in BS-6.14's test (119 → 120, 18 → 19, 119 → 120) MUST be bumped in the same change. |
| FR-020 | `show_to_user` MUST be limited to 5 calls/minute/chat (sliding), and every call, success or refusal, MUST be audited with a reason code. |
| FR-021 | `MediaPart` (both contract copies, plus the inline `LibraryRef`) MUST gain optional `library_ref`. One function `libraryRefFor(localPath, workspaceID)` fills it for workspace and mount files of every media type, including images. Live: `workspaceID` is `bus.OutboundMediaMessage.WorkspaceID` inside `webchat_channel.go::SendMedia`. Reload: the history-page request (`history_page_prepare.go`) reads `UnifiedStore.GetMeta(sessionID).WorkspaceID` once per page and hands it to the `streamReplayState` it builds in `prepareGroup`, so `buildMediaFrame` (reached via `emitFlatToolCall` / `emitSpawnParentToolCall`) can call `libraryRefFor`. The memoised `historyPreparedMedia` subsets keep the field. The persisted descriptor stays `ref, filename, content_type, type` (the ref is not stored). Empty or failed meta means the field is omitted. The REST history page is not a path for this card and is not changed (see "REST history path"). The SPA stores it on `MediaAttachment` and shows the button only on the non-image file card; `ChatImage` ignores it (A7). |
| FR-022 | The assistant file item MUST show "Open in Library" (accessible name with the file name) when `library_ref` is present, web chat only; images keep their toolbar. |
| FR-026 | The SPA MUST report watching with `chat_presence` under the Watching rules, from all three chat views (`sessions.$sessionId.tsx`, `WorkspaceChatTab`, `OmnipusChatScreen`), and a web turn's dynamic context MUST contain exactly `User watching: yes` or `User watching: no` for that turn's own session. Expired, missing, and unauthorised claims MUST count as no. The gateway MUST validate the inbound frame through `wsFrameSchemaName`. |
| FR-023 | The "### Files and artifacts" group of `<operating_rules>` in `pkg/agent/context.go` MUST be rewritten to cover links, path display and artifacts-over-chat, including when to call `show_to_user` from the watching line, with continuous numbering; `send_file` and `show_to_user` descriptions one added sentence each (R7, D-watch). |
| FR-027 | Base-instruction rule 27 ("After reading back saved artifacts, provide their verified full absolute paths.") MUST be replaced by the D-F1 rule (name or relative path plus link by default; absolute path on request or for files outside every Library), and `docs/agents.md` MUST say the same in the same change (founder D-F1, 2026-10-29). |
| FR-024 | The user-facing documentation pages below MUST be updated in the same change. |
| FR-025 | No new runtime dependency, no resolver endpoint, no hand-written wire types (Hard Constraints 1, 8); the design-system and size-budget gates stay green. |

## Success Criteria

| ID | Criterion |
|---|---|
| SC-001 | 100 percent of golden vectors round-trip in Go and the SPA, and the parity test passes. |
| SC-002 | Every SEC-1..SEC-16 test fails when its mitigation is removed (mutation probe in qa-lead CHECK) and passes with it. |
| SC-003 | On every reload and reconnect path (snapshot and incremental) there are 0 Library navigations, 0 notices and 0 `open_in_library` frames in replayed or journaled output. |
| SC-004 | Effective-policy test (BS-6.14) passes for every listed agent on fresh and Reconcile-upgraded config and fails when the inventory grant is removed. `grep -rl '"show_to_user"' pkg/coreagent/ pkg/config/ pkg/tools/` is kept only as a smoke check. |
| SC-005 | `make verify-contracts` and `scripts/check-no-handwritten-wire-types.sh` pass; the contract CHECK items (a)-(e) pass. |
| SC-006 | `GOOS=windows go vet` and the macOS and Linux CI legs pass for the resolver. |
| SC-007 | UAT on the landing commit: BS-1.1, 5.1, 6.1, 6.3, 6.7 and the outside-file row PASS with screenshot evidence and an independent validator ruling. |
| SC-008 | `make lint-budgets`, design-system gate, `npm run typecheck` pass; existing hub, markdown, Library and policy tests pass unchanged except those named. |

## Traceability Matrix

| Requirement | Story | Scenarios | Tests (order no.) |
|---|---|---|---|
| FR-001 | US-1 | BS-1.1, 1.2, 1.5, 1.11, 1.12, 1.16 | 4, 22 |
| FR-002 | US-1, US-3 | BS-1.6, 3.4 | 6 |
| FR-003 | US-1 | SEC-1, SEC-5, SEC-13, BS-1.16 | 2 |
| FR-004 | US-1 | BS-1.14, 1.15, SEC-6, SEC-10 | 4 |
| FR-005 | US-3 | BS-3.3, SEC-4 | 7 |
| FR-006 | US-3 | BS-3.1, 3.2 | 4, regression |
| FR-007 | US-1 | BS-1.1, 1.3, 1.10, 1.13, 1.17 | 5 |
| FR-008 | US-1, 5, 6 | BS-1.4, 5.5, 6.4, 6.6 | 4, 18, 19 |
| FR-009 | US-4 | BS-4.4, SEC-2 | 3 |
| FR-010 | US-1 | BS-1.8, 1.9, SEC-13 | 1 |
| FR-011 | US-1, 2, 4 | BS-1.7, 1.7b, 2.1, 4.3, SEC-15 | 8, 9 |
| FR-012 | US-2 | BS-2.4 | 9 |
| FR-013 | US-2 | BS-2.1, 2.2, 2.3 | 9, 22 |
| FR-014 | US-6 | BS-6.1, 6.9, SEC-3, 11, 14 | 10, 10b |
| FR-015 | US-6 | BS-6.9, 6.10, SEC-9 | 10, 14 |
| FR-016 | US-6 | BS-6.7, 6.11, SEC-8 | 14, 15 |
| FR-017 | US-6 | BS-6.7, 6.8 | 15, 18 |
| FR-018 | US-6 | BS-6.1-6.5, 6.12, SEC-7 | 18, 22 |
| FR-026 | US-6 | BS-6.16 | 20, 21 |
| FR-019 | US-6 | BS-6.14, SEC-16 | 12 |
| FR-020 | US-6 | BS-6.13, 6.15, SEC-7 | 11, 13 |
| FR-021 | US-5 | BS-5.1, 5.2, 5.4, SEC-12 | 16, 20 |
| FR-022 | US-5 | BS-5.1, 5.3, 5.5, 5.6 | 17, 19 |
| FR-023 | US-7 | BS-7.1, 7.2 | 21 |
| FR-027 | US-7 | BS-7.1, 7.3 | 21, docs-verifier report |
| FR-024 | all | docs audit | docs-verifier report |
| FR-025 | all | guards | CI guards, SC-005, SC-008 |

Every BS/SEC scenario appears in at least one FR row (BS-4.1 and 4.2 map to FR-023 and UAT).

## Reachability (Definition of Done)

Reported as two lines, never merged: "code correct and tested" and "reachable by a user or agent".

| Check | How shown |
|---|---|
| Tool registered with an explicit policy entry for every agent | Edit list: `pkg/config/defaults.go` ceiling `allow`; `pkg/coreagent/seed.go::NewCustomAgentToolsCfg` `"show_to_user": allow` (Main-agent creation defaults, founder table 2026-10-22) and `workerTierDeniedTools` entry. `ADR090RolePolicyInventory` is not edited: Mia and Jim receive the Main core's `allow`; Planner, Researcher and General Purpose (`worker`) receive `deny` through `RestrictForWorkerTier` (without the core entry every built-in persists an explicit deny and the tool is unreachable; V mechanism). Omnipus stays at its explicit deny (`omnipusSupportedTools` unchanged, D-F2). Judge and PlanSupervisor stay explicit deny. `auto_approve.go` (`AutoRuns`); `general_builtin_catalog.go::GeneralBuiltinMetadata`; `pkg/agent/loop_wire.go`. `pkg/tools/manifest.go` is NOT edited (D-manifest). Three pinned counts bumped (test 12). Gate: the effective-policy test (SC-004), not the grep. |
| Screen or component renders it | `MarkdownLink` (chat links); docked `LibraryPanel` at address; extracted assistant media item (button); chat notice component; store handler for `open_in_library`; `useChatPresence` mounted on all three chat views |
| Prompt text names the tool | `pkg/coreagent/prompts_adr090.go` names `tool:show_to_user` in the `mia` and `jim` role prompts only (U9); the `omnipus` prompt does not (D-F2) |
| Agents are told the new path rule | `pkg/agent/context.go` `<operating_rules>` "### Files and artifacts" rewritten; old rule 27 gone (FR-027) |
| Test plan executed | Red-then-green recorded per test; CI; UAT evidence with validator rulings |
| Hands-on UAT on the exact commit | Required: touches `pkg/agent`, `pkg/tools`, gateway WebSocket handlers |

## User Documentation TODO (outside `docs/internal/`)

| Page | Update | Owner |
|---|---|---|
| `docs/library.md` | New "Links from chat" and "Open in Library on sent files" sections; the agent may open the Library while you are looking at that chat, or leave an "Open in Library" notice when you are not | frontend-lead |
| `docs/using-omnipus-ui.md` | Chat area and panels: clickable file and note links, the Library opening while you are looking at the chat, the "Open in Library" notice when you are not, unsaved-edit prompt applies, Ctrl/Cmd-click opens the full page | frontend-lead |
| `docs/knowledge.md` | Notes and bases open from chat (no mention of views) | frontend-lead |
| `docs/reference/built-in-tools.md` | New `show_to_user` row; amend `send_file` row | backend-lead |
| `docs/tools.md` | One row in "Default tool sets for main agents and workers": `show_to_user` Main Allow, Worker Off; and one sentence near the `send_file` Auto-approve text: `show_to_user` is allowed by default and Omnipus does not have it | backend-lead |
| `docs/security.md` | Links and `show_to_user` open a viewer only, read nothing you could not already open, and open by themselves only while you are looking at that chat. Unsaved Library edits are not discarded. 5 calls a minute. The agent is told only whether you are watching that chat, not which device. Auto-open shows the Library's existing preview of that file. `security-lead` confirms the preview's own policy in the feature gate; this spec does not change it | backend-lead |
| `docs/agents.md` | Replace "with their contents checked and full absolute paths reported" (line 68 at `b7d4483c9`) with the D-F1 rule: agents name files (or give a relative path) with a link, and give the absolute path when you ask or when the file is outside every Library. The page does not list per-agent default tools today (V), so no tool row is added there; say in one sentence that Mia and Jim can open the Library for you and Omnipus cannot (D-F2) | backend-lead |
| `docs/protocol/websocket-protocol.md` | One row for `open_in_library`: server to client, live only, no `seq`, not replayed, not in the journal. Do not expand this into a repair of other missing rows (`library_changed` is already absent) | backend-lead |

`docs-verifier` audits each page before landing; a missing update is an Important finding.

## Work Breakdown

| Unit | Owner | Scope | Depends |
|---|---|---|---|
| U0 Contract | backend-lead | Contract section incl. both copies, `LibraryRef`, CHECK items | none |
| U1 Resolver + encoder | backend-lead | BS-4.4, 1.8, SEC-2, 13; step 0 import-cycle check (U); `impact` runs | none |
| U2 Tool-result links | backend-lead | BS-1.7, 1.7b, 1.9, 2.1, 2.3, 2.4, 4.3, SEC-15. One `pkg/tools.LinkLine` for all six producers | U1 |
| U3 show_to_user | backend-lead | BS-6.1, 6.7, 6.9-6.16, SEC-3, 7, 8, 9, 11, 14, 16. `deliverLive`, the typed `OpenInLibrary` field, and the in-memory presence store (injected func; nil means not watching; model the connection binding and disconnect teardown on `mail_presence.go`). `chat_presence` handler plus the `wsFrameSchemaName` case. Policy: `NewCustomAgentToolsCfg` allow + `workerTierDeniedTools` entry; no per-role grant; `omnipusSupportedTools` untouched. No `manifest.go` edit. No `loop.go` edit. The context line goes through a per-turn injector or a new `buildDynamicContext` parameter that carries the turn's session id (keep `context.go` growth minimal; it is over the 2,000-line warn). Process-wide limiter with an injectable clock. Publish only when `!IsError && !Cancelled` | U0, U1 |
| U4 Media part target | backend-lead | BS-5.1-5.4, 5.6, SEC-12. `libraryRefFor` from `SendMedia` (live, `OutboundMediaMessage.WorkspaceID`) and from the history-page preparation (`history_page_prepare.go::prepareGroup` → `streamReplayState` → `buildMediaFrame`; workspace id from `UnifiedStore.GetMeta` once per page). No `Attachment.yaml` or REST history change | U0, U1 |
| U5 SPA parser + renderer | frontend-lead | BS-1.1, 1.2, 1.5, 1.6, 1.11, 1.12, 1.14-1.16, 3.x, SEC-1, 4, 5, 6, 10, 13; names the workspace selector (returns no workspace for Omnipus's chat). Link opens pass a workspace panel context without `sessionId` | none |
| U6 Docked Library fix | frontend-lead | BS-1.3, 1.10, 1.13, 1.17 (also fixes Mail's existing "Open in Library") | none; sequence against L1 (L1 status not re-checked in the drift sync, U) |
| U7 `open_in_library` handler + notice + presence hook | frontend-lead | BS-6.1-6.8, 6.12, 6.16. New handler file (`decideLibraryOpen`) plus one dispatch line in `handleFrame`. `useChatPresence` on `sessions.$sessionId.tsx`, `WorkspaceChatTab` and `OmnipusChatScreen` only (model: `mailPanelPresence.ts`). Do not edit `SidePanelShell` or `ChatScreen.tsx`. Notice uses `Button` and `IconButton`. `shouldRenderToolCall` gains the `show_to_user` case | U0, U5, U6 |
| U8 Assistant media item | frontend-lead | First **extract** the assistant media rendering (the two inline `download` blocks in `ChatScreen.tsx`, plus any third site found; net shrink of `ChatScreen.tsx`) into one component, then add the action. Also: `MediaAttachment` gains optional `libraryRef`, and the `case 'media'` body of `handleReplayAndStatusFrame` moves to a sibling so that pinned function does not grow. Images: field stored, button not shown. BS-5.1, 5.2, 5.5, 5.6 | U0, U5, U6 |
| U9 Prompt text | prometheus-prompt-engineer | BS-7.1, 7.2, FR-027. Rewrite the "### Files and artifacts" group of `<operating_rules>` (rules 25 to 27 today), remove the rule 27 absolute-path sentence, keep numbering continuous. Role prompts for `mia` and `jim` name `tool:show_to_user`; the `omnipus` prompt does not. The tool is `ManifestLazy`, so the prompt must tell the model to load it; do not ask for a full-manifest edit | U2, U3 names fixed |
| U10 Docs | implementing leads + docs-verifier | Docs table, including the `docs/agents.md` absolute-path sentence (BS-7.3) | all |
| U11 Follow-up record | team-lead | Record the Follow-ups list as tracker issues with no release label, during planning. This spec does not create the issues (the design lane does not write to the tracker). The record is not conditional on another founder ask; Q4 already required the views follow-up | none |

Waves: 1 = U0, U1, U5, U6; 2 = U2, U3, U4, U7, U8; 3 = U9, U10, CHECK, 5-reviewer gate, UAT. `LibraryPanel.tsx`, `ChatScreen.tsx`, panel-shell and `markdown-shared.tsx` land before or after L1 (not read, U), never concurrently. `pkg/agent/context.go` gains the rule-text edit and the one `User watching` line in `buildDynamicContext`. `SidePanelShell` is not edited.

## Ambiguity Warnings

| What is ambiguous | Likely agent assumption | Resolved by |
|---|---|---|
| Whether the live primitive forwards `urlTransform` | Yes | Test 6. Still Unknown until that test runs (`@assistant-ui/react-markdown` is not installed in the design checkout) |
| `pkg/tools` → `pkg/library` import cycle | No cycle | U1 step 0. If there is a cycle, the resolver moves to `pkg/workspace`. Not a product decision |
| Third assistant file render site | Only two exist | U8 greps and extracts all (two found again at `b7d4483c9`) |
| Injector or parameter for the `User watching` line | Either works | U3 picks; output text is fixed |

Closed, do not re-open: hub method and payload seam (Tool-to-hub seam), worker channel and root-session comparison (BS-6.9, test 10b), the REST history path (resolved: not a path for sent-file cards), Omnipus's grant (D-F2) and the absolute-path rule (D-F1). The phone-width gate from round 2 was superseded by the founder watching decision; do not implement it.

## Open Questions for the Founder

None. Q1-panel and the watching marker are founder decisions (2026-10-15) and are specified under Watching. The two drift-check questions were answered by the founder on 2026-10-29: F1 = A (chat links win over rule 27; D-F1) and F2 = A (Omnipus not granted; D-F2).

Recorded so they can be vetoed, not left open:

| Consequence | Why it is not a new question |
|---|---|
| A side panel such as Calendar is replaced when the user is watching | Q1-panel replaced the old "only when no panel is open" assumption. Unsaved Library edits still block auto-open, because R6 already requires the leave gate. |
| A phone the user is watching auto-opens and covers the chat | The founder removed the phone special case by replacing that assumption. Width is not a gate. |
| "Focus" means a visible tab on a chat route, not "this operating-system window is the foreground app" | The founder defined it as "visible tab and the chat view active". |
| A claim expires after 45 seconds | The founder required an expiry and left the number to the spec. |

## Follow-ups (not in this feature; no issues created here)

1. Links to saved knowledge-base views (needs new Library UI for a view address).
2. Clickable file row in the write-confirmation block (`FileWriteConfirm.tsx::FileOpBlock`).
3. "Open in Library" for workspace images sent with `send_file`.
4. `tel:` entry in `isSafeHref` is dead for markdown links under the default transform (low value).
Each is tracked without a release-version label (root `CLAUDE.md`, release strategy). U11 records them at planning. That record does not wait on another founder ask.

## Evaluation Scenarios (Holdout)

Not referenced in the TDD plan or traceability matrix.
- **H1**: Ask an agent to write a note and tell you where it is; click the link; the Library shows the note.
- **H2**: Ask it to send you a workspace file; the card has Download and Open in Library, both work.
- **H3**: Ask it to "show me" a file while idle; the Library opens; refresh; it does not reopen.
- **H4**: Ask it to show a file from its private memory folder; you get the full path as text; nothing clickable, nothing opens.
- **H5**: Switch to another app so the chat tab is hidden, ask it to show a file; nothing opens, and coming back shows an "Open in Library" notice. Ask again while looking at the chat; the Library opens, even if Calendar was the side panel.
- **H6**: With unsaved edits in the Library, ask it to show another file; no prompt interrupts you; the notice appears; clicking it asks before discarding.
- **H7**: Drop your connection (airplane mode on the tab), let the agent show a file, reconnect; nothing opens.
- **H8**: A file named `report (final) [v2].md` opens exactly.

## Assumptions

- A1. Mount files belong to the turn's workspace for the own-workspace rule.
- A2. Superseded 2026-10-29: custom-agent defaults are one explicit table, `seed.go::NewCustomAgentToolsCfg` (founder default tool table, 2026-10-22), with Worker denies in `workerTierDeniedTools`; `show_to_user` is one new row there (Main allow, Worker deny).
- A3. The rewritten "### Files and artifacts" group keeps the artifacts-over-chat content of today's rules 25 and 26 (structured deliverables and diagrams over 15 lines go to files).
- A9. Omnipus's turn workspace (`tools.ToolWorkspaceID`) is the default workspace, taken from its session meta (`pkg/agent/loop_process_message.go`); I, not traced end to end. The resolver rule for Omnipus does not depend on it, because it also tries every other workspace.
- A4. A tab that missed the frame never opens late (intended).
- A5. User-facing documentation updates are needed; none waived.
- A6. A focus-neutral auto-open with a polite announcement is sufficient for screen-reader users (UAT checks with the accessibility tree).
- A7. Images sent with `send_file` keep their toolbar and gain no new action; R5's "card" is read as the file card.
- A8. Superseded. Phone width does not choose notice versus open. Watching does.

## Clarifications

### 2026-10-15
- Round-1 fix applied from `spec-review.md` and `decisions.md`.
- Round-2 fix applied from `spec-review-round2.md`. Provenance of every binding row is in `decisions.md`.
- Founder decision the same day replaced Q1-panel assumption A with the watching rule and required a presence marker. Specced under Watching. No further founder question.

### 2026-10-29
- Drift check against `b7d4483c9` (`DRIFT-REPORT.md`). Founder answered F1 = A and F2 = A (recorded as D-F1, D-F2 here and in `decisions.md`). All drift edits applied; see "Drift sync 2026-10-29".

## Review response (round 1)

| Finding | Response |
|---|---|
| MAJ-001 views | Accepted. Views removed from stories, scenarios, guidance, docs; Q4 stated as out of scope; Follow-ups added; "Phase" wording removed. |
| MAJ-002 replay | Accepted, option 1. Verified `publishMeta` journals and `sendIncremental` re-sends. Server-side unsequenced/unjournaled delivery (Replay guarantee), `seq` dropped from the schema, client guard rejects any frame with a `seq`, test 15 covers snapshot and incremental paths with an instrument control. Whether `isReplaying` is raised on incremental attach stays Unknown; the design no longer depends on it. |
| MAJ-003 contract | Accepted. Verified the inline `MediaPart` and the already-drifted inline `AgentSwitchedFrame`; both copies edited, `LibraryRef` named, regeneration CHECK items, contract moved to the top. |
| MAJ-004 panel policy | Accepted via D-Q1; UI states, accessibility, focus, announcement, phone rule (A8) added. |
| MAJ-005 child/no workspace/no tab | Accepted; refusal rows, request-worded success, BS-6.11. Round-1 left the child channel Unknown. Round 2 closed it (R2-MIN-03): the stamp is `"webchat"`, and the refusal compares the two session ids. |
| MAJ-006 Hard Constraint 6 | Accepted. Verified the inventory and its deny default; role file added to the edit list; SC-004 replaced by an effective-policy test with a negative control. |
| MAJ-007 channels | Accepted via D-A; BS-1.7b, SEC-15. |
| MAJ-008 FileCard | Partly disagree on count: grep finds exactly two inline assistant `download={m.filename}` blocks in `ChatScreen.tsx` (plus the user-bubble `AttachmentCard`), not three; U8 greps for a third. Rest accepted: extract first, images decided (A7), budgets stated. |
| MAJ-009 bare links | Accepted; workspace source, no-workspace rule, historical rule, file-like rule, reject rows, denied-workspace scenario. Selector name left to U5. |
| MAJ-010 UI sections | Accepted; States, Journeys, Accessibility, Design-System sections added; non-hover spoof defence. |
| MIN-001 | Accepted; strict-subset wording, parity test direction, SPA-only rejection list, workspace rule bound to `safeID`. This also decides the old bidi question as "no link, plain text" (changes my earlier recommendation of escaping in the hover). |
| MIN-002 | Accepted; mount-only symlink rule and rows. |
| MIN-003 | Accepted; transform delegates to the default. |
| MIN-004 | Accepted; refusals audited with reason code. |
| MIN-005 | Accepted; sliding window, per chat, validation refusals not counted, restart resets, 256-id bound. |
| MIN-006 | Accepted; ADR sentence added; `impact` runs are step 0 of each unit. GitNexus was not available here, so no caller counts are claimed. |
| OBS-1/2/3 | OBS-1 done (no open questions); OBS-2 noted at BS-4.1; OBS-3 added to BS-7.1 wording. |

## Review response (round 2)

| Finding | Response |
|---|---|
| R2-MAJ-01 | Accepted. `historyPushed` is no longer an input. `SidePanelShell` publishes measured `rowWidth` as `panelRowWidth`. `decideLibraryOpen` uses `isPhoneTakeover` (`PANEL_TAKEOVER_PX` 680). Width 0 (unmeasured) takes the notice path, which is stricter than the shell's own `rowWidth > 0` guard, so a phone does not auto-open before the first measurement. BS-6.5 has rows at 0, 679 and 680, including "no panel open". |
| R2-MAJ-02 | Accepted, the new-field option. `request_id` is 16 bytes from `crypto/rand`, 32 lowercase hex, required on the frame. The handled-set keys on `session_id` + `request_id`. `call_id` stays for the tool card only. BS-6.8 adds "same `call_id`, two `request_id`s, both act". |
| R2-MAJ-03 | Accepted, option (b). Typed `OpenInLibrary` pointer on `ToolResult` (`json:"-"`) and on `ToolExecEndPayload`, copied only in `emitAdmittedToolResult`. `deliverLive` is specified for lookup-not-create, overflow unbind, evicted drop, and hold mode. The "nothing is open" claim is corrected: the seam is closed; Q1-panel remains a working assumption. The ADR file is a landing step, not an open choice. |
| R2-MIN-01 | Accepted. CHECK items (a) and (b) name `ws-schemas.ts`, `asyncapi-types.ts` and `schemas.ts`. Item (c) is a vitest through `src/lib/ws.ts` asserting the two drop counters stay 0. Inline `LibraryRef` is required in `asyncapi.yaml`. |
| R2-MIN-02 | Accepted, with these choices: three pins bumped (`catalogSizeToday`, `postMergeAdditions`, the auto-approve 114). `manifest.go` is not edited; the tool stays `ManifestLazy` and U9 names it. Admin is not granted (inventory deny), and is removed from the allow column of BS-6.14. |
| R2-MIN-03 | Accepted. The unknown is closed: `reconstructSteeredTurn` stamps `"webchat"`. Refusal compares `ToolTranscriptSessionID` with `ToolRootChatSessionID`. Test 10b is the integration proof through `DelegationTree`. No new ADR-091 boundary: the child never builds a frame, so there is no new publication for `Boundaries` to name. |
| R2-MIN-04 | Accepted. One `libraryRefFor`, live from `OutboundMediaMessage.WorkspaceID`, replay from `UnifiedStore.GetMeta`. Not persisted. U8 owns `MediaAttachment` and extracts the `media` case so the pinned function does not grow. Images: field filled, `ChatImage` ignores it. |
| R2-MIN-05 | Accepted. Process-wide limiter keyed by root chat session id, injectable clock, fake-clock boundaries 4/5/6, one agent-switch case. |
| R2-MIN-06 | Accepted. BS-1.5 is on test 5; BS-2.2 is on test 22; test 14 names two bound connections and one unbound. `decisions.md` now separates founder, working assumption, team-lead and architect. U11 records follow-ups at planning, not "when the founder asks". |
| R2-MIN-07 | Accepted. Dismiss is a catalogued `IconButton` (`aria-label="Dismiss"`, Tab, Escape only while focused). Catalogue result is recorded (Button and IconButton used; toast, alert-dialog and disclosure-row rejected, with why). Thread card: success hidden, error shown, verbose still shows it. `aria-describedby` replaces `aria-description`. |
| OBS-2 | Folded into test 14 (hold mode). |
| OBS-3 | Folded: one `pkg/tools.LinkLine`. |
| OBS-4 | Folded: one protocol-doc row for `open_in_library` only. |
| OBS-5 | Folded: unsafe links keep today's struck-through span, and tests assert that. |
| OBS-6 | Folded: the encoder emits no link when the workspace id fails the SPA pattern. |
| OBS-7 | Folded: `chat-links.spec.ts` must be assigned in `tests/e2e/shards.json`. |
| OBS-1 | Docs sentence only: auto-open shows the Library's existing preview. Preview policy is unchanged; `security-lead` confirms it in the gate. |

Status history: Draft 2026-10-15; Draft (round-1 fixed) 2026-10-15; Approved-draft (round-2 fixed) 2026-10-15; Approved-draft updated the same day for the founder watching decision; Approved-draft (drift-synced 2026-10-29 to b7d4483c9). No third grill round. No founder question left open.

The round-2 response row R2-MAJ-01 specified a phone-width gate. The founder decision supersedes that gate. Do not implement `panelRowWidth`.

The two "Review response" tables above are history: they mention Admin, `AgentSwitchedFrame` and the count 114 as they stood when written. The current requirements are the sections above them, as amended by the drift sync below.

## Drift sync 2026-10-29

Source: `(maintainer-local path)`. Code baseline moved from `b45fd6f16` (2026-10-06; what the design note and both grills actually read) to `b7d4483c9`. Backup of the pre-sync text: `spec.md.bak-20261029`. No founder-final decision was changed except by adding D-F1 and D-F2.

| # | Drift item | Sections edited |
|---|---|---|
| S1 | Header: status, drift-report input, code baseline, certainty-label baseline | Header |
| S2 | Founder rows D-F1 (rule 27 vs R2/R3; chat links win) and D-F2 (Omnipus not granted); R7 restated for the grouped `<operating_rules>` | Binding decisions |
| E1 | Stale numbers and paths re-grounded: `ChatScreen.tsx` 2,956 lines, catalogue 62 entries (six new sources named), `handleReplayAndStatusFrame` 580, `withBrowserRootChatSessionID` in `browser_deferral.go`; GitNexus note | Existing Codebase Context, Design-System Reuse |
| E2 | `AgentSwitchedFrame` example dropped (frame removed `b62539418`); `receiveOpenInLibrary` modelled on `receiveMedia`; inline enum plus `WsFrameType.yaml`; `wsFrameSchemaName` case for `chat_presence`; `inboundschemas` auto-sync; `mail_panel_observer` precedent; new CHECK item (e) | Contract, Codebase Context, TDD test 20, SC-005 |
| E3 | Snapshot path is `sendSnapshot -> sendPreparedHistorySnapshot` (`replayTranscript` gone), REST page shares the preparation; test 15 instrument control uses the `send_file` `media` frame; `agent_switched` regression removed | Replay guarantee, TDD test 15, Regression requirements |
| E4 | Reload half of FR-021 wired through `history_page_prepare.go::prepareGroup` / `streamReplayState`; memoised media keep the field. **REST history question resolved: REST pages do not carry sent-file cards** (new subsection), so `Attachment.yaml` is unchanged | Replay guarantee (new "REST history path"), FR-021, U4, BS-5.4, test 16, Contract "Not changed" |
| E5 | Ava and Admin removed; grant through `NewCustomAgentToolsCfg` + `workerTierDeniedTools`; no per-role grant; Omnipus denied (D-F2); SEC-16 added | BS-6.14, FR-019, Reachability, U3, U9, Explicit Non-Behaviors, Security table, SC-002, test 12, traceability |
| E6 | Pinned counts corrected to 119 → 120, 18 → 19, 119 → 120 (the old 114/13/114 were wrong at writing) | Test 12, FR-019 |
| E7 | `switch_agent` case replaced by "two agents' instances, one root chat session" | Rate limit, BS-6.13, test 11 |
| E8 | No "Rule 2": "### Files and artifacts" group (rules 25 to 27) rewritten; rule 27 removed (FR-027, D-F1); `docs/agents.md` sentence replaced; BS-7.3 and US-7 AS-3 added; `context_env_test.go` table entry named | US-7, BS-7.1, BS-7.3, FR-023, FR-027, test 21, Reachability, Docs TODO, U9, U10, A3, Regression requirements |
| E9 | `User watching` line: `buildDynamicContext` has no session id, so injector or parameter; `context.go` over the 2,000-line warn | Watching ("What the agent sees"), U3, Ambiguity warnings |
| E10 | Third chat view `/omnipus` (`OmnipusChatScreen`); `admin.chat.tsx` deleted; hook mounted on all three views; Omnipus presence scenario | Watching, U7, FR-026, BS-6.16, Reachability |
| E11 | Omnipus chat has no workspace for bare links; `omnipus://` links stay clickable there; resolver tries other workspaces for Omnipus (architect choice giving D-F2's "passive links" a meaning); three resolver rows | Workspace for bare links, BS-1.11, Resolver Rules, BS-4.4, test 3, A9 |
| E12 | Seam: payload `Cancelled` and typed `ToolCallID`; publish only when `!IsError && !Cancelled`; `call_id` conversion | Tool-to-hub seam, U3 |
| E13 | Link opens pass a workspace panel context without `sessionId` (`LibraryPanel.tsx::asWorkspaceContext`) | Codebase Context, U5, test 5 |
| E14 | U6 also fixes Mail's "Open in Library"; BS-1.17 added; Mail handoff tests kept green | BS-1.17, U6, test 5, FR-007 traceability, Codebase Context, Regression requirements |
| E15 | Group-chat rooms named out of scope (plain text) | Explicit Non-Behaviors, Watching, Codebase Context |
| S3 | `docs/tools.md` gains a row in "Default tool sets for main agents and workers" (Main Allow, Worker Off) and the Omnipus note | Docs TODO |
| S4 | Founder questions answered; ambiguity list updated; A2 superseded; A9 added; Clarifications 2026-10-29; status history | Open Questions, Ambiguity Warnings, Assumptions, Clarifications, Status history |
