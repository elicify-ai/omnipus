# ADR-20261028 — Group chat reuses the normal chat

| Header | Value |
|---|---|
| Status | Proposed — awaiting the one grill-spec round (ADR mode) and the founder interview on section 9 |
| Date | 2026-10-28 |
| Author | Architect (Opus 5.5), dispatched by team-lead for #1449 |
| Issue | #1449 "Group chat reuses the normal chat" (related, out of scope: #1068 messaging scope, #1461 presence) |
| Source decisions | D1–D14 of `docs/internal/specs/1449-group-chat-requirements.md` (founder interview 2026-10-27/28). They are founder decisions and are not reopened here. |
| Approved design | `docs/internal/design/1449/index.html`, `docs/internal/design/1449/wireframe-full.png` (frames 1–4) |
| Captured at | branch `spec/1449-group-chat` at `893e9fc45` |
| Amends | omnipus-system-agent spec FR-169 (approvals only in the main chat), FR-171 (members and cost in the header), FR-207 ("top-left", "open it empty"); the Chief ruling of 2026-10-18 "option C" pinned in `pkg/gateway/l1_wu8_rooms_red_test.go::TestWU15Rooms_ReceiverHelperApprovalReloadRestoresCompleteApprovableCard`. Each amendment is stated and justified where it is decided (A5, A7, A9). |

**Naming note.** The dispatch asked for "ADR-0XX, the next free number". `.claude/agents/architect.md` ("ADR naming") says every new ADR is named `ADR-<YYYYMMDD>-<slug>.md` through `scripts/new-adr-id.sh`, and the shared rules rank the agent file above a dispatch prompt. I followed the agent file: `scripts/new-adr-id.sh "Group chat reuses the normal chat"` printed `ADR-20261028-group-chat-reuses-the-normal-chat` (exit 0).

Grounding labels used below: **[V]** = verified by reading the cited `file::symbol` in this task; **[I]** = inferred (reason given); **[U]** = unknown, with what would settle it.

---

## 1. Context

### 1.1 What exists today

**The normal chat** is one stack bound to the active session:

- `src/components/chat/ChatScreen.tsx::ChatScreen` (file is 2,961 lines, 39 below the 3,000-line CI limit, shared rule 10) renders `FeedKindLabel`, `MainChatAddParticipant`, `VirtualizedMessageList`, the thread tails (`AskUserQuestionThreadTail`, `SecretRequestThreadTail`, `ToolApprovalThreadTail`) and `OmnipusComposer` (defined in the same file, about 1,000 lines). [V]
- `src/components/chat/ChatMessageList.tsx` (`PlainMessageList`, `VirtualizedMessageListInner`, `useOpenCover`, `OpenCover`), `HistoryBoundary.tsx` (`HistoryBoundary`, `FloatingReturnToLatest`, `HistoryReaderStatus`), `useHistoryViewport.ts` (`useHistoryViewport`, `runHistoryFill`), `useChatHistory.tsx` (`useChatHistory`, `HistoryOpenState`) and `useOpenReveal.ts::useOpenReveal` all read `useSessionStore.activeSessionId` and `useChatStore` directly (8–11 references per file, counted with grep). [V]
- The message rows `VirtualUserMessageRow` and `VirtualAssistantMessageRow` live in `ChatScreen.tsx`. They already resolve the author **per message** (`message.agentId ?? activeAgentId`, then `AgentReplyLabel`), render `HistoricalMessageMarkdown` (markdown, Mermaid, math, code with copy), and render `message.media` through `ChatImage` (lightbox) and `AttachmentCard`. They also read session-scoped live state (`toolCalls`, `toolCallOrder`, `sessionsById[activeSessionId]`). [V]
- The composer is built on assistant-ui: `src/lib/omnipus-runtime.ts::useOmnipusRuntime` returns `useExternalStoreRuntime` with `onNew` → `sendMessage`, `onCancel` → `cancelStream` and the attachment adapter `src/lib/attachment-adapter.ts`, which **creates a session** before uploading when none exists. The composer's concerns live in `src/hooks/useSlashMenu.ts` (slash menu plus "@" agent mention mode), `useFileUpload.ts` (takes a `ComposerRuntime`) and `useCancelState.ts`. [V]

**The group chat** is a separate stack:

- `src/routes/_app/workspaces.$workspaceId.rooms.$roomId.tsx` → `src/components/rooms/RoomScreen.tsx::RoomScreen` → `RoomHeader` (all members, states, cost, menus), `RoomTranscript` (plain `whitespace-pre-wrap` text, its own paging and anchoring), `SendNotice`, and `RoomComposer` (a `Textarea` with its own @-picker that binds picks to agent ids). [V]
- `src/store/rooms.ts::useRoomStore` keeps per-room `entries`, an integer `seq` reconnect cursor, `hasOlder`/`olderBoundary`, `historyStatus` and `deliveries`, attaches with `attach_room`, and pages with `fetchRoomHistory` (`before_seq`, `limit`). It never touches session buckets (FR-199). [V]
- Server: `pkg/rooms` journals each room under `entities/rooms/<id>/` as `room.json` plus JSONL streams (`pkg/rooms/journal.go::FileJournal`), with no version field. `RoomEntry` is text-only (`contracts/components/schemas/RoomEntry.yaml`, `additionalProperties: false`). [V]
- A member answers only through the `answer_request` tool (`pkg/tools/answer_request.go`, parameters `request_id` and `text`). The answer is appended (`pkg/rooms/answers.go::DeliverAnswer`) and mirrored into the member's main chat (`pkg/agent/room_request.go::MirrorRoomAnswer`). [V]
- The member's room work runs as a turn of **its own main session** (`pkg/agent/room_request.go::AdmitRoomRequest`, `commitRoomRequest`), queued behind that session's current turn. [V]

### 1.2 Forces

- D1/R7: one screen, reused components, `pkg/rooms` storage unchanged "for now", read through an adapter.
- FR-199: a room is not a session. No agent session works inside it, and room frames never enter session buckets (pinned by `src/store/rooms.test.ts` "[FR-199/201; K45] room frames do not replace or create the active agent's main transcript bucket"). [V]
- Hard Constraint #8: every byte across the gateway/SPA boundary is contract-first. D3 needs contract changes.
- Size budgets: `ChatScreen.tsx` cannot grow (rule 10).
- The history stack (#1266/#1452) is hard-won and heavily tested (about 30 `ChatScreen.history-*.test.tsx` files plus `src/store/chat.history-*.test.ts`). Any adapter that touches it must be provably behaviour-neutral for sessions.

---

## 2. Decision summary

| # | Question (requirements §5) | Decision |
|---|---|---|
| A1 | Adapter shape | A **`ConversationSource`** React context, read by the list/history/reveal stack instead of the session stores. It has two implementations: a session source (a thin wrapper, no behaviour change) and a room source (a projection over `useRoomStore`). The stores stay separate. |
| A2 | Composer | **Extract `OmnipusComposer`** from `ChatScreen.tsx` first (mandatory, size budget). Then drive it with a **`ComposerProfile`** (which controls exist) under a **room-scoped assistant-ui runtime** (`useRoomRuntime`). Fallback if nested runtimes misbehave: a `ComposerTransport` seam (A2.4). |
| A3 | Multi-author rows | The room source projects each `RoomEntry` to a `ChatMessage`: user → `role:'user'`, agent → `role:'assistant'` plus `agentId`. The existing per-message author label is reused. Session-only reads in rows are gated on `source.sessionId`. D5 adds per-agent "… is working" tail lines built from deliveries. |
| A4 | Rich content (D3) | Add optional `attachments: MediaPart[]` to `RoomEntry` and optional `media: string[]` (`media://` refs) to `RoomMessageRequest`. Add an optional `media` parameter to `answer_request`. Uploads are room-scoped (no session is created). Attachments are mirrored into the main chat. Journal change is additive; no version bump. |
| A5 | Approvals in the group (D8) | Cards carrying `room_ref.room_id == this room` render in the group as well as in the main chat. Answer once by approval id; both views update. **Question cards gain `room_ref`/`origin_label`.** **Helper approvals: yes — a helper launched by a room-classed turn inherits that turn's origin** (amends the 2026-10-18 option-C ruling). Secret cards stay in the main chat only. |
| A6 | Stop (D4) | New `POST /rooms/{id}/stop`. Not-yet-started requests are cancelled. A working **room-only** turn is stopped turn-scoped, with no session fence and no queue discard. A working **mixed** turn keeps running; this room's request is detached and its late answer refused. |
| A7 | Invitation context (D10) | Generated by the gateway on ad-hoc creation from a main chat, with the origin agent's model, scrubbed for secrets before and after. Stored on the room record and delivered once in each other member's first request as quoted background. Shown in the Participants panel. Cost is counted into the room's allocated cost and the origin agent's usage. |
| A8 | Untrusted text (FR-168) | Render room text through the **same** `HistoricalMessageMarkdown` pipeline as main-chat answers (no raw HTML, `isSafeHref` links, CSP-blocked remote images, Mermaid `strict`). FR-168 is untouched because commands are parsed only server-side from user posts. |
| A9 | Minimal chrome (D13) | Delete the `FeedKindLabel` bar and the `MainChatAddParticipant` row. Add a `ChatHeaderControls` cluster in the existing `workspace-chat-controls` slot at the right end of the workspace header line: Add participant (icon-only), helper breadcrumb (muted text), or Participants (an `AvatarStack` + count). |
| A10 | Participants panel (D6) | A new registered side panel `participants` (not a strip entry) that takes over everything `RoomHeader` did, plus Ask again, member chat links, cost and invitation context. |
| A11 | Tests | Behaviour assertions move to the shared components with a room fixture. Plain-transcript and room-composer DOM tests are retired with their components. Go room tests stay. Five Go tests change their assertions on purpose (A11 table). |

---

## 3. Options considered

### 3.1 Adapter shape (A1)

| Option | Description | For | Against | Verdict |
|---|---|---|---|---|
| **O1: room as a virtual bucket in the chat store** | Reduce room frames into `useChatStore.sessionsById['room:<id>']` and set `activeSessionId` to that key, so every component works untouched | Zero component edits | Breaks FR-199 by construction. Every session-keyed side effect keys off `activeSessionId`: `attach_session` in `OmnipusRuntimeProvider.tsx`, `sendMessage`, `cancelStream`, the `useChatHistory` REST fallback (`fetchSessionMessages`), `ChatControls` (Open browser), `MainChatAddParticipant`, the attachment adapter's `createSession`. The history slice (`src/store/chat/history.ts::createHistorySlice`) speaks string positions and `fetchSessionMessages`, not `seq`/`getRoomHistory`. Each of these would need a "but not for rooms" guard, which is a fork hidden inside the store. | Rejected |
| **O2: `ConversationSource` context (chosen)** | The display stack reads a small interface; a session implementation wraps today's stores, a room implementation projects `useRoomStore` | Components are reused, not forked. Stores stay separate (FR-199 holds). The session path can be proven neutral: same tests, unchanged. The seam is narrow: what the display stack reads today. | One refactor across about 6 files (list, boundary, viewport, open reveal, history open state, rows' session reads). The projection must keep row object identity stable (see A1.3). | **Chosen** |
| O3: parallel room components (today) | Keep `RoomTranscript`/`RoomComposer`, add markdown etc. to them | Smallest immediate diff | Is the double work D1/R7 forbid | Rejected by D1 |
| O4: server merge (rooms become sessions) | Option C of D1 | One model | Founder-rejected (D1) | Out of scope |

### 3.2 Composer (A2)

| Option | For | Against | Verdict |
|---|---|---|---|
| **C1: extract, then a profile plus a room-scoped assistant-ui runtime (chosen)** | `ComposerPrimitive`, `useComposerRuntime`, `useFileUpload`, attachment chips, paste/drag-drop and Stop all keep working because they talk to "the runtime", and the room provides one | Needs nested `AssistantRuntimeProvider` to scope correctly (A2.4 spike) | **Chosen** |
| C2: composer takes callbacks only (`ComposerTransport`) | No dependency on assistant-ui nesting | Rewrites the composer's coupling to `ComposerRuntime` (`useFileUpload(composerRuntime)`, `ComposerPrimitive.*`), so it touches more code | Fallback only (A2.4) |
| C3: keep `RoomComposer`, restyle it | Small | Fails R3 ("almost identical") and R7; attachments would be built twice | Rejected |

### 3.3 Helper approvals (A5)

| Option | For | Against | Verdict |
|---|---|---|---|
| H1: keep option C (helper cards never carry `room_ref`) | No change; test already pins it | The founder's test in D8 is "does the card make sense here". A helper launched by the member's room turn is doing group work. Its card would sit only in the member's main chat while the group says "waiting for you in X's chat". | Rejected |
| **H2: inherit at launch when the launching turn is room-classed (chosen)** | Matches D8's intent; restart-safe if persisted; reuses the existing origin merge rules | Changes `steer.LaunchRequest` and the lifecycle record; amends a ruling and a test | **Chosen** |
| H3: inherit always (any parent origin) | Simple | Would carry channel or peer labels into helpers. Out of scope, and the room part is the only part D8 asks for. | Rejected |

### 3.4 Stop (A6)

| Option | For | Against | Verdict |
|---|---|---|---|
| S1: map group Stop to the normal session Stop of each addressed member | No new server primitive | `pkg/agent/stop_session.go::StopSession` is "the ONE session Stop". It discards queued person messages (`StopRequest.OnInputDiscarded`, FR-024), fences the session as stopped so later room requests are `held` (`pkg/agent/request_reminder.go::receiverStopped`, FR-252), and stops unrelated input that joined the turn. That breaks D8's principle that unrelated work stays. | Rejected |
| **S2: room-scoped stop with turn-scoped cancel for room-only turns (chosen)** | Stops exactly the group's work; leaves the member's main chat usable | Needs a new turn-scoped stop primitive | **Chosen** |
| S3: Stop only cancels not-yet-started requests (a `/pause`-like action) | No agent change | Fails D4 ("stops the work") for running answers, which is the main case | Rejected |

---

## 4. Decisions in detail

### A1 — `ConversationSource`

**A1.1 Interface (frontend-only, not a wire type).** New module `src/components/chat/conversationSource.ts`. It exports `ConversationSourceContext`, `useConversationSource()` and `useSessionConversationSource()`. The room implementation `useRoomConversationSource(roomId)` lives in `src/components/rooms/`. Members:

- `key: string` — the session id, or `room:<roomId>`. This is the list's React `key` (today `VirtualizedMessageList key={activeSessionId}`) and the reveal identity.
- `kind: 'session' | 'room'`; `sessionId: string | null` (always `null` for a room).
- `messages: ChatMessage[]`.
- `history` — exactly the `SessionHistoryState` fields the display stack reads today: `viewMode`, `hasEarlier`, `hasLater`, `requests[kind]` (`status`/`error`/`attempt`), `serverState`, `historyLoaded`, `viewGeneration`, `readerPositions`, `olderBoundary`, `newerBoundary`, `liveControlsReady`. The type is a `Pick<SessionHistoryState, …>`, so the session source passes the real object through.
- `catchingUp: boolean`; `openState: 'pending' | 'ready' | 'failed'` (the `useOpenReveal.ts::openState` result).
- Actions: `loadOlder(size)`, `loadNewer(size)`, `returnToLatest(size)`, `refresh()`, `retry(kind)`, `enterReading()`, `protectPositions(set)`. Optional, session only: `showEarlierClear(position, size)` (the closed-clear path in `HistoryBoundary`).
- `capabilities: { newerWindow: boolean; clearMarkers: boolean; liveToolCalls: boolean }`.

**A1.2 Consumers switched to the context.** `ChatMessageList.tsx` (`useOpenCover`, both lists), `HistoryBoundary.tsx` (all three exports), `useHistoryViewport.ts` (`useHistoryViewport` and `runHistoryFill`, which today calls `useChatStore.getState().loadOlderHistory`), `useOpenReveal.ts`, `useChatHistory.tsx::HistoryOpenState`. `useChatHistory` itself stays session-only (its REST fallback is `fetchSessionMessages`). The room source never mounts it.

**A1.3 Room source semantics** (each maps the room store's existing behaviour, verified in `src/store/rooms.ts`):

| Session concept | Room mapping |
|---|---|
| `historyPosition` (string) | `room:<seq>`; `readerPositions` = the displayed entries' positions |
| `olderBoundary` | `RoomLiveState.olderBoundary ?? entries[0].seq` (as `loadOlder` uses today) |
| `loadOlder(size)` | `useRoomStore.loadOlder(roomId, { limit: size.limit })`. The store gains a `limit` argument; `fetchRoomHistory` already accepts `limit` (`src/lib/api/rooms.ts::fetchRoomHistory`, server max 1000 per `pkg/rooms/history.go::HistoryMaxLimit`). |
| `requests.older` | from `historyStatus`/`historyFailed`, plus an `attempt` counter added to the room store |
| `viewGeneration` | incremented on a truncating snapshot (`has_older === true`), which is exactly when the room store replaces the window today |
| `hasLater`, `loadNewer`, `refresh` | always `false` / no-op. The room window is contiguous to the live tail: `mergeEntries` never evicts, and the snapshot carries at most 10,000 newest entries (`pkg/rooms/attach.go::maxSnapshotEntries`). `capabilities.newerWindow = false`. |
| `viewMode` / `enterReading` / `returnToLatest` | a per-room `viewMode` field in the room store (UI state, like the session's). `returnToLatest` sets it to `'latest'` and lets the viewport's follow-latest pin the bottom. No fetch, because the live tail is always loaded. Return to latest still shows (D11). |
| `openState` | `pending` until this opening's attach snapshot arrives (`snapshotReceived` together with the `attachedRoomId === roomId` guard now in `RoomScreen`); `failed` on a room read error; otherwise `ready`. This keeps the guarantees pinned by `RoomScreen.cached-opening.test.tsx`. |
| `catchingUp` | `!snapshotReceived` after an attach |

**Row identity.** `VirtualAssistantMessageRow` is `React.memo` and relies on unchanged messages keeping their object identity (its doc comment in `ChatScreen.tsx`). The room projection must therefore memoise per entry (keyed by entry id, invalidated only when that entry object changes) and must not rebuild the rows on every delivery frame. Delivery state is a separate projection (A3.3), so `room_member_state` frames never re-render the rows.

**A1.4 What does not change.** The room store stays its own store: no session bucket, its own cursor, `attach_room`, routed by `src/store/chat/slices/room-frames.ts`. FR-199 tests stay green unchanged.

### A2 — Composer

**A2.1 Extract first (mandatory, behaviour-neutral).** Move `OmnipusComposer`, `composerPlaceholder` and `ComposerAttachmentChip` from `ChatScreen.tsx` to `src/components/chat/composer/OmnipusComposer.tsx`. Re-export from `ChatScreen.tsx` only if a test imports it from there. No logic change; every `ChatScreen.*composer*` test stays green. The reason: `ChatScreen.tsx` is at 2,961 of 3,000 lines, so any #1449 addition there fails CI.

Moving the rows (`VirtualUserMessageRow`, `VirtualAssistantMessageRow`, `UserMessage`, `AssistantMessage`) to `MessageRows.tsx` is **recommended but not required**. `ChatMessageList.tsx` already imports them from `./ChatScreen`. Do it only if A3's edits would push `ChatScreen.tsx` over budget.

**A2.2 `ComposerProfile`** (prop on `OmnipusComposer`, default = today's session profile):

| Field | Session | Room (D4) |
|---|---|---|
| `modelPicker`, `autoApprove`, `tokenCounter`, `skills` | on | **off** |
| `attachments` | on | **on** (room upload target, A4) |
| `stop` | session Stop (`useCancelState`) | **room Stop** (A6), visible while any member has live work from this room |
| `slashCommands` | server catalogue | the six room commands only (`/pause /resume /mute /unmute /members /help`, FR-205; `src/components/rooms/roomModel.ts` `ROOM_COMMANDS`), sent as room text |
| `mentions` | agent-mention mode (`useSlashMenu`) | **room members**: `@all` plus current members (muted ones included). Picks are bound to agent ids and sent as `mentions`; deleting a token un-addresses that member. This ports `RoomComposer`'s rules exactly. |
| `placeholder` | "Message <Agent>…" | "Message the group — type @ to address someone" (wireframe frame 3) |
| `prefill` | — | `{ agentId, nonce }` for Ask again (from the panel, A10) |
| `blockedReason` | — | `RoomScreen.tsx::blockedReasonOf` (reconnecting, read-only, snapshot pending), shown in the composer's existing notice slot |

**A2.3 Room runtime.** `src/components/rooms/useRoomRuntime.ts` returns `useExternalStoreRuntime` with:

- `messages` = the room source's messages; `isRunning` = any member has a `working`/`admitted` delivery.
- `onNew` → `useRoomSend` (kept: one request at a time, `client_message_id` minted once, Retry re-posts the identical request, FR-216). It carries `text`, `mentions` and `media` refs.
- `onCancel` → room Stop.
- `adapters.attachments` = a **room attachment adapter** that uploads with `room_id` and never calls `createSession` (FR-199).

`RoomScreen` wraps its subtree in `<AssistantRuntimeProvider runtime={roomRuntime}>`.

**A2.4 Spike and fallback.** Whether a nested `AssistantRuntimeProvider` cleanly scopes `useComposerRuntime`/`ComposerPrimitive` below it, while the app-wide provider in `OmnipusRuntimeProvider.tsx` stays mounted, is **[U]**. The library docs were not reachable in this task (context7 refused the API key) and `node_modules` is not installed in this worktree. Settle it with FE-0 (section 6): a vitest that mounts a nested provider and asserts that `useComposerRuntime()` resolves to the inner runtime and that `onNew` reaches the room transport, not `sendMessage`. If it fails, use the fallback C2: a `ComposerTransport` interface (`send`, `cancel`, `addAttachment`, `isRunning`) passed through the profile.

### A3 — Multi-author rows and live view

**A3.1 Projection.** For each `RoomEntry`:

- `author.kind === 'user'` → `role:'user'`, no `participant`. The row renders right-aligned with no label (F17 rule 1, `participantLabel.ts`).
- `author.kind === 'agent'` → `role:'assistant'`, `agentId: author.agent_id`. `VirtualAssistantMessageRow` already resolves the name and avatar per row (`messageAgentId = message.agentId ?? activeAgentId` → `AgentReplyLabel`). [V]
- Other fields: `id` = entry id, `content` = `text`, `media` = `attachments` mapped to the existing `ChatMessage.media` shape (`url`, `filename`, `contentType`, `type`), `timestamp`, `historyPosition` = `room:<seq>`, `status: 'done'`.
- A new client-only optional field `roomMarks?: { afterPause: boolean }` is rendered as a muted suffix "after pause" beside the existing footer suffix (`getMessageStatusSuffix`). This keeps FR-170/FR-217 and `rooms.view.test.tsx` "after-pause answer keeps that mark".

**A3.2 Session reads in rows.** Rows must not read the foreground session's live state when they render a room. Today `VirtualAssistantMessageRow` reads `useChatStore(s => s.toolCalls)` and `sessionsById[activeSessionId]` and would attach the *last visited session's* live tool calls to the room's last assistant row. Gate every such read on `useConversationSource().sessionId != null` (and `capabilities.liveToolCalls`). In the room, `RowDeliveryStatus` and `UnansweredUserMessageStatus` (session send machinery) are not rendered; the room's send outcome stays with `useRoomSend`/`SendNotice`, moved into the composer's notice slot.

**A3.3 D5 "… is working".** A tail block inside the scroller (where `StreamingDelegation` sits for sessions) renders one line per member whose latest delivery (`latestDeliveryByMember`) is `admitted`/`working`/`waiting_on_user`/`awaiting_approval`. It reuses the existing `AgentStatusIndicator` (phase `working`/`waiting`) with the label from `roomModel.ts::deliveryLabel`. When a card for it is not in the group (A5), the line carries the existing "Open <Agent>'s main chat" link. No streaming text (D5: live writing is later).

### A4 — Rich content (D3), contract-first

**A4.1 Contract shapes** (architect decides the shape; backend-lead edits `contracts/components/schemas/` **and the inline copies in `contracts/asyncapi.yaml`**, whose descriptions say "keep both copies in sync by hand", then regenerates):

| Schema | Change |
|---|---|
| `RoomEntry` | + optional `attachments`: array, `maxItems: 16`, items `$ref: MediaPart` (`type` image/audio/video/file, `url` `/api/v1/media/{ref}`, `filename`, `content_type`, `caption`). Absent = none. This reuses the one media shape the SPA already renders (`MediaFrame`). |
| `RoomMessageRequest` | + optional `media`: array of strings, `maxItems: 16`, each a `media://` ref (the same form as the WS `message` frame's `media`). `text` becomes `minLength: 0`. The handler enforces "text not blank **or** media non-empty" with inbound validation on and off (`pkg/rooms/messages.go::validateMessage`). |
| `uploadFiles` (`POST /upload`) | + `room_id` (query or form field), mutually exclusive with `session_id`. With `room_id`, files are stored under the room's entity directory (`entities/rooms/<id>/media/`, so deleting the room deletes them) and registered in the media store. Access is the room-read access (`getRoomHistory`'s rule). |
| `answer_request` tool parameters (`pkg/tools/answer_request.go::AnswerRequestTool.Parameters`) | + optional `media`: array of strings (≤16) naming files the agent can already send to its own chat (`media://` refs or workspace-relative paths). The server resolves them with **the same access checks the agent's normal outbound media path uses** (exact function **[U]**: backend-lead names it in the spec), copies them into the room media scope, and stores them as `MediaPart`. |

**A4.2 Storage.** `pkg/rooms/types.go::storedEntry` wraps `generated.RoomEntry`, so the new field is carried automatically. Journal lines are decoded with plain `json.Unmarshal` (`pkg/rooms/journal.go`, no `DisallowUnknownFields`) [V]. Old lines therefore read with no attachments, and a rolled-back older binary ignores the unknown field: text stays, attachments disappear from display. No version field is introduced; the change is additive. The body cap stays text-only: attachments do not count toward `EffectiveSteerBodyBytes`.

**A4.3 Mirroring.**

- `commitRoomRequest` sets the request's `TranscriptEntry.Attachments` and the wake's `bus.InboundMessage.Media` (`pkg/bus/types.go::InboundMessage.Media` exists [V]), so the member's model actually receives the images as multimodal input.
- `MirrorRoomAnswer` gains an attachments argument and writes them on the mirrored entry.
- `RoomTrafficEntry` (main chat, FR-202) shows attachments in its expanded state through `ChatImage`/`AttachmentCard`.
- The room context window (`pkg/rooms/fanout.go::roomContext`, 20 entries / 8 KiB) lists earlier attachments by filename only. Only the current request carries media.

**A4.4 Diagrams, math, code, tables** need no server change: they are markdown in `text` and render through A8.

### A5 — Approvals and questions in the group (D8)

**A5.1 Delivery to the group view.** The client store already keeps `roomRef` per pending approval (`src/store/toolApproval.ts::PendingToolApproval.roomRef`, filled from `tool_approval_required.room_ref` and the reconnect snapshot `SessionStatePendingApproval.room_ref`) [V].

- The group renders `ToolApprovalPanel` with a filter `approval.roomRef?.room_id === roomId`. The main chat keeps its root-chat filter (`approvalChatId`). The same card therefore appears in both places.
- Answer once: resolution is by approval id through the existing endpoint, and the resolved frame removes the single store entry, so both views update.
- The room pre-admission card (FR-251, `pkg/agent/room_request.go::requestRoomApproval`) already carries `room_ref` and therefore appears in the group.
- Cards arrive on the user's hub connection regardless of the attached session (they are rendered from the approval store today in any chat). Verifying that the frames reach a client viewing a room page is part of BE/FE CHECK. **[I]**: `broadcastToolApprovalRequired` broadcasts, but I did not trace the per-connection fan-out filter.

**A5.2 Mixed origin.** Keep today's rules unchanged:

- `pkg/agent/participant.go::mergeApprovalOrigin`: precedence channel > room > peer > web, first-of-class keeps the label.
- `pkg/gateway/policy_approver.go::policyApproverAdapter.RequestApproval` sets `roomRef = nil` when `ChannelTriggered()`.

A channel-joined turn's cards therefore stay in the main chat (D8: "channel wins"). Unrelated work has no `room_ref` and stays in the main chat.

**A5.3 Question cards.** `AskUserQuestionFrame` and `AskUserQuestionCard` have no `room_ref`/`origin_label` today [V, schemas listed]. Add both, optional, with the same shapes and limits as `ToolApprovalRequiredFrame`. Fill them from `currentApprovalOrigin()` at emission (`pkg/gateway/ws_ask_user.go::askUserCardSink.EmitCard`). The group renders `AskUserQuestionCard` for its own references. Answer once by question id. FR-168 is unaffected: only the user answers.

**A5.4 Secret cards stay in the main chat only** (D10: never secrets in group surfaces). The group shows "waiting for you in <Agent>'s main chat" with the link.

**A5.5 Helper (delegate) approvals: today they do NOT carry the reference.** [V] The child turn starts from `context.Background()` (`pkg/agent/steer_launcher.go::runDispatchedSteeredTurn`). `pkg/agent/steer_reconstruct.go::reconstructSteeredTurn` builds `processOptions` without `approvalOrigin`. `pkg/steer/types.go::LaunchRequest` has no origin field. The test comment records "Chief ruling 2026-10-18 (option C): room_ref belongs to the room pre-admission card and to the room-received member's OWN turn — never to a helper's card".

**Decision H2:** when a helper is launched, snapshot the launching turn's `currentApprovalOrigin()`. If its class is **room**, persist `{label, room_ref}` on the launch record (new `LaunchRequest`/lifecycle field) and seed the child's `processOptions.approvalOrigin` from it in `reconstructSteeredTurn`, so it is restart-safe. The child's own merge rules still apply: a channel input joining the child relabels it and drops the `room_ref`. Channel or peer origins are not inherited (H3 rejected). This **amends the 2026-10-18 option-C ruling**, because D8 delegated helper approvals to the architect after that ruling was made. The founder may override (section 9, Q1).

**A5.6 Accidental finding, fixed in this feature.** `pkg/gateway/rooms_runtime.go::WSHandler.roomCard` → `rooms.Service.CardPending(mainID, …)` flips **every** working room request under the member's main session to `waiting_on_user` when any card there is pending, including one from unrelated work (`pkg/rooms/answers.go::cardChanged`). With A5.3/A5.5, a card that carries a `room_ref` flips only the delivery with that `request_id`. A card without a `room_ref` keeps today's per-main behaviour, which is conservative because it may block the turn. Documented, not silent.

### A6 — Stop in a group (D4)

**A6.1 Contract.** `POST /api/v1/rooms/{id}/stop` (operation `stopRoomWork`):

- Request `RoomStopRequest { client_request_id: string(1..128); agent_ids?: string[] (≤256) }`. Absent `agent_ids` = every member with live work from this room.
- Response `RoomStopResponse { results: [{ agent_id, request_ids: string[], outcome: 'cancelled' | 'stopping' | 'detached' | 'none' }] }`.
- Idempotent per `client_request_id`. Errors: the existing `ErrorResponse`, 404 "no such group chat".
- Each delivery change is published as the usual `room_member_state` frame. The new delivery reason value is `stopped` (`RoomDeliveryState.reason` is a free string per its schema [V]).

**A6.2 Semantics per delivery of this room to an addressed member:**

| Delivery state | Effect | Outcome |
|---|---|---|
| not consumed (`recorded`, `awaiting_approval`, `admitted`, `held`) | `cancelled`/`stopped` through the existing `cancelDelivery` path; the pre-admission card is withdrawn (`gatewayRoomDeps.CancelApproval`) | `cancelled` |
| consumed by a **room-only** turn (every input the turn consumed is a request of this room) — `working`, `waiting_on_user` | **turn-scoped stop** of that turn id and its helper tree. No session stopped fence (no FR-252 hold) and no discard of queued person messages (unlike `StopSession`). The deliveries settle `interrupted` through the existing path (`settlement.go::ConsumeEnded`). | `stopping` |
| consumed by a **mixed** turn (web, channel or peer input joined, or another room's request) | the turn continues for its other inputs; this room's deliveries become `cancelled`/`stopped`; a later `answer_request` for them is refused ("the group stopped this request") and nothing is appended | `detached` |

The new agent primitive "stop this turn if it is still turn T of session S, tree scope, keep the queue, set no fence" is **new code**. Its feasibility on the existing canceller (`StopRequest.Canceller`/`StopTurnsCanceller`) is **[I]**, because those cancel per session today. backend-lead confirms it in the spec.

**A6.3 UI.** The composer's Stop button (`ComposerProfile.stop`) shows while `isRunning`. It stops everyone with live work, and the "… is working" lines update as states arrive. Per-member Stop is out of scope for the composer. The panel's per-member menu may offer "Stop" later; that is not decided here.

### A7 — Invitation context (D10)

**A7.1 Where it is generated.** In the gateway, right after `pkg/rooms/admin.go::Service.Create` succeeds for `kind: adhoc` with an `origin_session_id`. That id is already required to pass `SessionInWorkspace`, which gives "same workspace only" [V]. It runs as a background job; the room opens immediately.

- **Input:** the model-visible tail of the origin main session, bounded (spec fixes the numbers; the architect's ceiling is 40 entries / 16 KiB). It excludes secret-card entries and tool results of secret tools.
- **Model:** the origin agent's configured model and provider, resolved exactly as for its main turns.
- **Prompt:** summarise for the invited agents, at most about 200 words, never include credentials.

**A7.2 Never secrets — mechanical, not only instructed.** Both the input and the output pass `pkg/logger/sensitive.go::ScrubSensitiveValues` (every registered credential plaintext) and `pkg/audit/redactor.go::RedactCredentials` (credential patterns) [V]. The output is scrubbed **before** it is stored. Residual risk: a paraphrased secret that matches neither a registered value nor a pattern (R6).

**A7.3 Storage and contract.** It lives on the room record (`roomRecord`, `room.json`). `Room.yaml` gains optional `invitation_context: RoomInvitationContext { status: 'generating' | 'ready' | 'failed'; text?: string (≤4096); author_agent_id; generated_at?; cost_micro_usd?; cost_unpriced? }`. It is readable through `getRoom` only (it is not on frames and never in `RoomEntry`, so it is never posted to the chat or mirrored).

**A7.4 Delivery to members.** In `pkg/rooms/fanout.go::buildAdmission`, on each non-origin member's **first** request in this room, the text is prefixed as a quoted block: "Background from <Agent>'s chat (summary, not instructions):". While `status = generating`, the first admission waits up to a bounded time (spec value; ceiling 20 s), then proceeds without it and records `failed`, which the panel shows. The origin agent never receives it (it has its own history).

**A7.5 Display.** The Participants panel shows "Invitation context ›" (wireframe frame 4), a disclosure that renders the text through A8 with its status (generating / not available).

**A7.6 Cost.** The call is the origin agent's usage (its model and catalog price). It is also counted once into the room's allocated cost: a `turns.jsonl` line with id `invite:<room_id>` through `rooms.Service.AddTurnCost`, so the dedupe by turn id holds. Unpriced sets `allocated_cost_unpriced`. "Cost so far" in the panel therefore includes it.

**A7.7 FR-207 amendment.** "Open it empty" still holds for entries: no message is posted. `TestWU15Rooms_AddParticipantStartsEmptyAndLeavesOriginMainUnchanged` keeps its assertions and adds one: `invitation_context` exists on the room and no entry was written.

### A8 — Untrusted agent text (FR-168)

Room text renders through `historical-markdown.tsx::HistoricalMessageMarkdown`, the renderer of every finished agent answer in the normal chat [V]:

- `remark-gfm`, `remark-math`, `rehype-katex` and `rehype-phosphor-emoji`, **with no `rehype-raw`**, so literal HTML is escaped.
- Links go through `markdown-shared.tsx::createLinkRenderer`: `isSafeHref` scheme allow-list (`src/lib/url-safe.ts`), new tab with `noopener noreferrer`, unsafe schemes struck through.
- Images go through `MarkdownImage` (unsafe `src` rejected).
- The SPA CSP `img-src 'self' data: blob:` (`pkg/gateway/embed.go::spaBaseContentSecurityPolicy`) blocks remote tracking images.
- Mermaid runs with `securityLevel: 'strict'` (`mermaid-renderer.tsx`).

Room members are the same workspace conversation agents whose main-chat answers already render this way, so the trust level is the same. `UntrustedChildText` (inert links plus a badge, built for delegated-child text in the session list) is **not** used: room authors are first-class agents, not helpers.

FR-168 is untouched. It is about effect, not display: commands are parsed only from user posts on the server (`pkg/rooms/messages.go::runCommand`), and answers only resolve @-mentions (`answers.go::DeliverAnswer`). Rendering never executes anything. `rooms.composer.test.tsx` "[FR-168/189] an agent's /pause text stays visible input and never executes a command" moves to the shared stack unchanged in meaning.

User rows render like the normal chat's user rows (plain `whitespace-pre-wrap` plus the skill/command chips logic).

### A9 — Minimal chrome (D13), normal, helper and group chats

**A9.1 Mount point.** The workspace header already has a right-end slot: `src/components/workspaces/WorkspaceTabContainer.tsx`'s `<div data-testid="workspace-chat-controls">` after `WorkspaceTabBar`, shown when `resolveActiveSegment(...) === 'chat'` [V]. The room route also resolves to `'chat'` because `rooms` is not a tab segment (`WorkspaceTabBar.tsx::resolveActiveSegment`) [V]. A new `ChatHeaderControls` renders there, before the existing `ChatControls`. The workspace name and toggles on the left stay unchanged (R9). `WorkspaceTabBar` measures the remaining slot (R44), so the added `shrink-0` controls are absorbed by its existing mode fallback. `WorkspaceTabBar.fit.test.tsx` must cover the widest cluster (an avatar stack plus count).

**A9.2 What each chat shows:**

| Chat | Right-end control | Replaces |
|---|---|---|
| main chat (non-Omnipus, `attachedSessionType === 'main'`) | `IconButton` ghost, `UserPlus`, `Tooltip` "Add participant", `aria-label` "Add participant"; opens the existing `AddParticipant` popover (target `main`) | the `MainChatAddParticipant` row at the top of `ChatScreen` |
| helper/task child | small muted caption text "‹ <Parent> / <Kind>". The parent part is the existing `ParentCrumbLink`. Truncated with a `Tooltip` when long. The honest states of `FeedKindLabel` are kept: no fabricated names while the roster loads, an id fallback when it fails. | the `FeedKindLabel` grey bar with `border-b` |
| task chat with no descriptor | the same muted caption "Task: <title>" | the `FeedKindLabel` task bar |
| group chat | `AvatarStack` (up to 3 plus `+N`) and the count as one ghost `Button` labelled "Participants" (`aria-pressed` = panel open). No `UsersThree` icon, so it never reads as the Team toggle (wireframe note). | `RoomHeader` |

The main chat's title line ("<Agent> - Main Session") is **not shown** (D13: no chat header bar). Its text moves to an `sr-only` `role="status"` node inside `ChatHeaderControls`, so assistive technology keeps the chat's name. `FeedKindLabel` becomes that `sr-only` label plus the breadcrumb; the bar markup is deleted.

**A9.3 Design-system rules that apply** (`.claude/skills/omnipus-design-system/SKILL.md`):

- Rule 1: catalogued `IconButton`/`Button`/`Tooltip`/`Avatar` only; no raw `<button>`, no `title=` tooltips.
- Rule 2: spacing and type from tokens (`--space-*`, `--type-caption-size`, `--type-utility-xs-size`); no borders, no surface fills on the cluster.
- Rule 7: no per-component `focus-visible` utilities (the global ring applies).
- Rule 12: `pointer-coarse:h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]` on the icon buttons, because they are adjacent to the strip's toggles.
- Rule 14: the avatar stack already exists inline in `ActivityBar.tsx` (`-space-x-[var(--space-2)]`) [V]. The header and panel would be the second and third instances, so publish **`AvatarStack`** as a catalogued composite through the four-part contract (rule 9) and switch `ActivityBar` to it.
- Height stays `h-chrome-header`.

**A9.4 Accidental finding.** On a room page `ChatControls` (Open browser) renders today and acts on `useSessionStore.activeSessionId`, which is the last visited session, not the group. With A9 it is not rendered on the room route.

### A10 — Participants panel (D6)

A new registered side panel: `PanelId` `'participants'`, policy `{ scope: 'workspace', switchFollows: 'never' }`, and `WorkspacePanelContext.roomId` (`src/components/panel-shell/types.ts`, `registry.tsx`). It opens only from the header button and is **not** a strip entry. It closes when the route leaves the room.

It contains what `RoomHeader` did:

- per-member state via `deliveryLabel`;
- "open chat" links via `memberMainSessionId`;
- Ask again (on `interrupted`, filling the composer through `prefill`);
- mute/unmute/remove in the `⋯` menu (the `useMembership` logic);
- delete (squad/ad-hoc);
- Add participant (target `room`; disabled with `TEAM_ROOM_ADD_EXPLANATION` in the team room, FR-208);
- "Cost so far" (`formatAllocatedCost`, unpriced flag, FR-171 amended: it moves from the header to the panel per D6);
- Invitation context (A7.5).

The chat keeps its centred column while the panel is open (D7: messages `max-w-4xl`, composer `max-w-3xl`, as in `ChatMessageList.tsx` and `ChatScreen.tsx` today).

### A11 — Room tests: what moves

| Today | Fate |
|---|---|
| `src/components/rooms/RoomScreen.cached-opening.test.tsx` (7) | **Move** to the shared stack with a room-source fixture (open reveal, opening jump, waits for this opening's snapshot). The assertions keep their meaning; DOM selectors change from `[data-room-entry-id]` to the list's rows. |
| `RoomScreen.history.test.tsx` (5) | **Move**: no automatic walk on mount, exactly one request at the top with the oldest seq, terminal hides the hint, failed GET keeps rows plus Retry on the same boundary, composer unblocked during a page. |
| `RoomTranscript.anchor.test.tsx` (3), `RoomTranscript.chrome-anchor.test.tsx` (4), `RoomTranscript.opening.test.tsx` (4) | **Re-express** against `useHistoryViewport` with a room source (live tail growth during a GET is not counted as prepended; reader scroll during the GET is preserved; loader/hint/error insertion keeps the anchor; switching rooms lands at the new room). Then **delete** the files with `RoomTranscript.tsx`. |
| `src/store/rooms*.test.ts` (4 files) | **Stay** (the store stays) and add tests for the new `limit`, `attempt`, `viewMode`, `viewGeneration`. |
| `src/test/integration/rooms.composer.test.tsx` (9) | **Move** to the shared composer with the room profile: @all, current members only, muted addressable, picks survive rename, ambiguous typed name refusal, short name kept as text, deleted pick un-addresses, agent /pause text stays text. |
| `src/test/integration/rooms.view.test.tsx` (12) | **Split**: transcript rows → shared list (ordering, replayed id once, after-pause mark, read-only reason blocks send, failed read shows Error/Retry); members, cost and states → Participants panel tests. |
| `tests/e2e/room-history-paging.spec.ts` | **Stay**, with selectors retargeted to the shared list. |
| `tests/l1-room-ui/*` harness | Retarget the selectors (owned by qa-lead). |
| `src/components/chat/FeedKindLabel.*.test.tsx` (4 files) | **Rewrite** for the `sr-only` label and the breadcrumb (honest states stay asserted). The bar assertions are retired. |
| `ChatScreen.kindLabel.test.tsx`, `AddParticipant.test.tsx`, `WorkspaceTabBar.fit.test.tsx` | Update for the header cluster |
| Go `pkg/rooms/*_test.go`, `pkg/gateway/*room*_test.go` | **Stay**. Changed on purpose: `TestWU15Rooms_ReceiverHelperApprovalReloadRestoresCompleteApprovableCard` (now asserts `room_ref` for a helper launched by a room-only turn, plus a negative case for a channel-classed launch); `TestWU15Rooms_AddParticipantStartsEmptyAndLeavesOriginMainUnchanged` (+ invitation context, still no entry); `TestRoomsFix5_HardStopInterruptsInitiatingRequestWithoutReplay` (unchanged; the new room Stop gets its own tests); `rest_rooms_test.go` (+ stop route); `wu15_rooms_*` contract-limit tests (+ attachments, media). |

Every new vitest file must match a CI group pattern (`scripts/check-vitest-coverage.mjs`, `src/components/chat/CLAUDE.md`).

---

## 5. Consequences

**Positive**
- One display, history and composer stack. Fixes land once (D1/R7). Groups get markdown, Mermaid, math, code, tables, images and files (D2).
- FR-199 is preserved structurally: rooms never enter session buckets.
- Stop no longer has to choose between doing nothing and killing the member's whole main chat.
- The approval UX matches the founder's test ("does the card make sense here").
- `ChatScreen.tsx` drops about 1,000 lines (composer extraction), which restores size headroom.

**Negative**
- The `ConversationSource` refactor touches the most sensitive frontend area (#1266/#1452). Mitigation: FE-1 ships the session source alone, behaviour-neutral, with the entire history suite green unchanged before any room code.
- New server primitives: a turn-scoped stop, helper origin persistence, invitation summarisation, room-scoped uploads. Each is a new failure surface.
- Five Go assertions and one ruling are amended on purpose (A11).
- The contract grows: `RoomEntry`, `RoomMessageRequest`, `Room`, `uploadFiles`, ask-user frames, a new stop operation and two new schemas.

**Neutral**
- `pkg/rooms` stays the storage of record (D1). The journal change is additive (A4.2).
- The room window still never evicts (as today); very long rooms hold up to 10,000 snapshot entries plus loaded pages in memory (R4).

---

## 6. Implementation outline (owners, order, dependencies)

One owner per area (root `CLAUDE.md` "Feature ownership"): **frontend-lead** (all `src/` UI and stores), **backend-lead** (contracts and regeneration, `pkg/`), **qa-lead** (test author: red tests first, migration of the room tests), **CHECK** by a different instance of each role than the implementer.

| Step | Owner | Work | Depends on |
|---|---|---|---|
| BE-0 | backend-lead | Contract edits and regeneration (5-step procedure, Hard Constraint #8): A4.1 (`RoomEntry.attachments`, `RoomMessageRequest.media`, `uploadFiles.room_id`), A5.3 (ask-user `room_ref`/`origin_label`), A6.1 (`stopRoomWork`, `RoomStopRequest`, `RoomStopResponse`), A7.3 (`Room.invitation_context`, `RoomInvitationContext`). Both copies of each room schema. | this ADR (post-grill) |
| FE-0 | frontend-lead | Extract `OmnipusComposer` to `composer/OmnipusComposer.tsx` (behaviour-neutral) and the A2.4 nested-runtime spike test | — (parallel with BE-0) |
| FE-C | frontend-lead | D13 chrome (A9): `ChatHeaderControls`, `AvatarStack` publication, `FeedKindLabel` bar removal, `MainChatAddParticipant` row removal, `ChatControls` hidden on rooms | — (independent; can ship first) |
| QA-1 | qa-lead | Red tests: session-source neutrality harness; room-source history/opening/anchor tests (ported from A11); composer room-profile tests; chrome tests; Go red tests for stop, helper origin, attachments, invitation context. Red is shown on a tests-only commit in CI. | BE-0 (types), FE-0 |
| FE-1 | frontend-lead | `ConversationSource` plus the session source; switch the A1.2 consumers. **Gate:** the whole existing chat/history suite is green with no test edits. | FE-0 |
| BE-1 | backend-lead | Rich content: room upload scope, `answer_request.media`, entry storage, mirroring (A4.2–A4.3) | BE-0 |
| BE-2 | backend-lead | Room Stop and the turn-scoped stop primitive (A6) | BE-0 |
| BE-3 | backend-lead | Helper origin inheritance and persistence; ask-user origin fill; per-request `CardPending` keying (A5.3, A5.5, A5.6) | BE-0 |
| BE-4 | backend-lead | Invitation context job, scrubbing, admission prefix, cost line (A7) | BE-0 |
| FE-2 | frontend-lead | Room store additions (A1.3); room source and projection (A3.1–A3.2); working lines (A3.3); `useRoomRuntime` and the room attachment adapter; composer room profile (A2.2); `RoomScreen` rebuilt on `ChatMessageList` plus `OmnipusComposer` plus the thread tails filtered by room (A5.1) | FE-1, BE-0; live paths need BE-1..BE-3 |
| FE-3 | frontend-lead | Participants panel (A10), including the invitation-context disclosure | FE-2, BE-4 |
| FE-4 | frontend-lead | Delete `RoomTranscript.tsx`, `RoomComposer.tsx`, `RoomHeader.tsx` and their DOM-only tests once QA-1's ported tests are green | FE-2, FE-3, QA-1 |
| DOC | frontend-lead (draft), docs-verifier (audit) | `docs/group-chats.md`, `docs/using-omnipus-ui.md` (requirements §7) | FE-3, FE-C |
| CHECK | second instance per role | Cross-check each step's diff against this ADR's decisions; frontend CHECK runs only targeted `npx vitest run <file>` / the one e2e spec through `/usr/local/bin/heavy`; Go results come from CI | each step |

Critical path: BE-0 → FE-1 → FE-2 → FE-3 → FE-4. FE-C and FE-0 run in parallel from day one.

---

## 7. Risks

| # | Risk | Likelihood / impact | Mitigation |
|---|---|---|---|
| R1 | The session-source refactor regresses #1266/#1452 history behaviour | Medium / high | FE-1 is neutral-only, with the full existing history suite unchanged as its gate; room code arrives in FE-2 |
| R2 | A nested assistant-ui runtime does not scope (A2.4 [U]) | Unknown / medium | The FE-0 spike decides before FE-2; fallback C2 is pre-designed |
| R3 | Rows leak the last session's live state into a room | High if not handled / medium | A3.2 gating on `sessionId`, plus a red test: a room open right after a streaming session shows no foreign tool calls |
| R4 | Memory for very long rooms (no eviction) | Low / low | Unchanged from today; a follow-up issue if measured |
| R5 | The turn-scoped stop is harder than inferred (A6.2 [I]) | Medium / medium | backend-lead confirms feasibility in the spec. If not feasible, Stop falls back to "cancel not-started plus detach working" (the mixed-turn behaviour for all turns), which is still never a session Stop. |
| R6 | The invitation summary paraphrases a secret that matches no registered value or pattern | Low / high | Two mechanical scrubs plus the instruction; the input excludes secret cards and secret-tool results; the user can view the text (D10) |
| R7 | Helper origin inheritance labels unrelated helper work "for group" | Low / medium | Inherit only at launch from a room-classed turn; a child channel input overrides; negative test |
| R8 | Approval frames do not reach a client on a room page (A5.1 [I]) | Low / high | An explicit CHECK item plus an integration test with the real hub |
| R9 | Contract and server disagree on the room text limit (5 MiB in `RoomEntry.yaml`/`RoomMessageRequest.yaml` vs 64 KiB enforced through `EffectiveSteerBodyBytes`) | Exists today / low | Accidental finding (section 8); not changed by this ADR |

---

## 8. Accidental findings (reported, not fixed on the side)

1. **Helper-card status flip is too broad.** Any pending card under a member's main flips all its working room requests to `waiting_on_user` (`rooms_runtime.go::WSHandler.roomCard` → `CardPending(mainID, …)`). Fixed within A5.6.
2. **`ChatControls` on room pages** acts on the last visited session (A9.4). Fixed within A9.
3. **The room text limit differs between contract and server** (R9). Not in #1449 scope; for backend-lead to decide (contract `maxLength` vs `EffectiveSteerBodyBytes`).
4. **Security note for security-lead.** Room text in the member's prompt is "quoted, not instructions" as a label only, with no delimiters or escaping (`pkg/agent/room_request.go::composeRoomRequestText`, `pkg/rooms/fanout.go::roomContext`), so text with newlines can imitate `[Name]` lines or the header. Out of #1449 scope; worth its own issue. A7.4's invitation prefix uses the same convention and inherits the same caveat.

---

## 9. Open questions only the founder can answer

1. **Helper approvals in the group (A5.5).** I decided that a card from a helper the agent started *for the group* also appears in the group. This reverses an internal ruling from 2026-10-18 that kept helper cards in the agent's own chat only. Confirm, or keep helper cards out of the group.
2. **Stop when the agent is also doing something else (A6.2).** If the agent's running work mixes your group request with, say, a Telegram message, Stop withdraws the group request and the agent keeps working on the other message. The alternative is to stop everything, including the unrelated work. Confirm "withdraw only".
3. **Add participant in other 1:1 chats (A9.2).** Today it exists only in an agent's main chat (FR-207). Should extra chats (not the main) also offer "Add participant"? The ADR keeps main-only.

---

## 10. UAT rows (for the spec)

| ID | Steps | Expected |
|---|---|---|
| UAT-1 | Open a group; an agent answers with a table, a Mermaid block, a formula and a code block | All render as in a normal chat; code has Copy; Mermaid draws |
| UAT-2 | Attach a screenshot in the group composer and send to @Jarvis | The image shows in your message with the lightbox; Jarvis's main chat shows the request with the image; Jarvis can describe it |
| UAT-3 | Ask an agent in the group to send back a file or diagram image | It appears in the agent's group answer with download/lightbox; the agent's main chat shows the same answer with the file |
| UAT-4 | Compare the group composer with a normal one | Same card and attach button; no model picker, Auto, token counter or skills; Stop appears while someone works |
| UAT-5 | Type "@" in the group | `@all` and the current members (muted included); a picked member stays addressed after a rename; deleting the token un-addresses |
| UAT-6 | Type "/" in the group | Only the six group commands; `/pause` then a message: not delivered; `/resume` resumes |
| UAT-7 | Address two agents; watch | One "<Agent> is working…" line per agent, then each finished answer under its author's name and avatar |
| UAT-8 | While both work, press Stop | Both stop; lines show interrupted; Ask again is available in the panel; each agent's main chat still accepts new messages immediately (not stopped/held) |
| UAT-9 | Press Stop while an agent's run also handles a Telegram message (Q2) | The group request is withdrawn; the Telegram reply still completes in the agent's chat; no group answer appears later |
| UAT-10 | Ask an agent in the group to run a tool that needs approval | The card shows in the group and in the agent's chat; approve in the group; the agent's chat shows it resolved without reload |
| UAT-11 | Same as UAT-10, but the agent's helper needs the approval (Q1) | The card shows in the group and in the agent's chat; answer once |
| UAT-12 | The agent has an unrelated approval in its own chat | No card in the group; the member line says "waiting for you in <Agent>'s main chat" only if that agent's group work is blocked |
| UAT-13 | An agent asks a question (question card) during group work | The question card shows in the group; answering it there updates the agent's chat |
| UAT-14 | A secret is requested during group work | No secret card in the group; a link to the agent's chat |
| UAT-15 | In Jarvis's main chat press Add participant (top right), pick Mia | A new empty group opens; Jarvis's chat unchanged; the panel shows Invitation context (generating → ready); it contains no keys or passwords |
| UAT-16 | Send the first group message to @Mia | Mia's answer shows it knew the background; nothing about the summary was posted in the group |
| UAT-17 | Open the Participants panel | Status per member, open-chat links, ⋯ mute/remove, Add participant, cost so far (including the summary cost), invitation context; the chat stays centred |
| UAT-18 | Scroll far up in a long group | Earlier messages page in without jumps; Return to latest appears and works; reopening the group shows the loading cover, then lands at the newest message |
| UAT-19 | Look at a normal chat, a helper chat and a group chat header | No bar, no borders. Left side unchanged. Right end: Add participant icon / "‹ Jarvis / Research helper" (Jarvis links back) / avatar stack + count. Tooltips on hover; usable by keyboard and touch. |
| UAT-20 | An agent posts `/pause` or a link to `javascript:` in the group | It shows as text; nothing pauses; the link is struck through and not clickable |
| UAT-21 | Narrow window (phone width) on a group | The header strip collapses as today; the Participants button stays reachable |

---

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| `ChatScreen.tsx` is 2,961 lines; `OmnipusComposer` is defined in it | `wc -l src/components/chat/ChatScreen.tsx` → 2961; `ChatScreen.tsx::OmnipusComposer` | Verified |
| List/history stack reads the session stores directly | grep counts: ChatMessageList 10, HistoryBoundary 8, useHistoryViewport 10, useChatHistory 11, useOpenReveal 4 | Verified |
| Rows resolve the author per message and render markdown and media | `ChatScreen.tsx::VirtualAssistantMessageRow` (`messageAgentId`, `HistoricalMessageMarkdown`), `VirtualUserMessageRow` (`ChatImage`, `AttachmentCard`) | Verified |
| Room store: own seq cursor, no eviction, `loadOlder` | `src/store/rooms.ts::useRoomStore`, `mergeEntries`, `reduce` | Verified |
| `fetchRoomHistory` accepts `limit` | `src/lib/api/rooms.ts::fetchRoomHistory` | Verified |
| `RoomEntry`/`RoomMessageRequest` text-only, `additionalProperties: false` | `contracts/components/schemas/RoomEntry.yaml`, `RoomMessageRequest.yaml` | Verified |
| Journal decodes with plain `json.Unmarshal` | `pkg/rooms/journal.go` (grep: `json.Unmarshal`, no `DisallowUnknownFields`) | Verified |
| Snapshot ≤ 10,000; context window 20 entries / 8 KiB | `pkg/rooms/types.go` `maxSnapshotEntries`, `maxContextEntries`, `maxContextBytes` | Verified |
| `answer_request` takes only `request_id`, `text` | `pkg/tools/answer_request.go::AnswerRequestTool.Parameters` (`required: request_id, text`) | Verified |
| `bus.InboundMessage` has `Media` | `pkg/bus/types.go::InboundMessage` (`Media []string`) | Verified |
| Helper approvals carry no `room_ref` today, by a 2026-10-18 ruling | `pkg/gateway/l1_wu8_rooms_red_test.go` comment "Chief ruling 2026-10-18 (option C)"; `steer_launcher.go::runDispatchedSteeredTurn` `context.Background()`; no `approvalOrigin` in `steer_reconstruct.go` (grep empty) | Verified |
| Channel-triggered approvals drop `room_ref` | `pkg/gateway/policy_approver.go` (`ChannelTriggered()` → `roomRef = nil`) | Verified |
| Client keeps `roomRef` per approval | `src/store/toolApproval.ts::PendingToolApproval.roomRef`, `k7Fields` | Verified |
| Ask-user frames lack `room_ref` | `grep -c "room_ref\|origin_label"` on `AskUserQuestionFrame.yaml` and `AskUserQuestionCard.yaml` → 0 and 0 (the same grep finds both on `ToolApprovalRequiredFrame` in `asyncapi.yaml`) | Verified |
| Session Stop discards the queue and fences the session | `pkg/agent/stop_session.go::StopRequest` (`OnInputDiscarded`, FR-024); `request_reminder.go::receiverStopped` exists | Verified |
| No room cancel endpoint exists | `contracts/openapi.yaml` room operations: list/create/get/delete/updateMembers/getRoomHistory/postRoomMessage only | Verified |
| `origin_session_id` exists on room create | `contracts/components/schemas/RoomCreateRequest.yaml` | Verified |
| Credential scrubbers exist | `pkg/logger/sensitive.go::ScrubSensitiveValues`, `pkg/audit/redactor.go::RedactCredentials` | Verified |
| Markdown pipeline has no raw HTML; safe links/images; CSP blocks remote images; Mermaid strict | `historical-markdown.tsx` plugin list; `markdown-shared.tsx::createLinkRenderer`, `MarkdownImage`; `pkg/gateway/embed.go::spaBaseContentSecurityPolicy` (`img-src 'self' data: blob:`); `mermaid-renderer.tsx` `securityLevel: 'strict'` | Verified |
| Right-end header slot exists; room route resolves to 'chat' | `WorkspaceTabContainer.tsx` `workspace-chat-controls`; `WorkspaceTabBar.tsx::resolveActiveSegment` | Verified |
| `FeedKindLabel` bar has a background and `border-b` | `FeedKindLabel.tsx` className `bg-[var(--color-surface-2)] border-b` | Verified |
| An avatar stack already exists inline | `ActivityBar.tsx` `-space-x-[var(--space-2)]` | Verified |
| Nested `AssistantRuntimeProvider` scopes correctly | context7 refused the key; no `node_modules` in the worktree | Unknown (FE-0 spike) |
| The turn-scoped stop is buildable on the existing canceller | reasoning from `StopRequest.Canceller`; not traced | Inferred |
| Approval frames reach a client on a room page | `broadcastToolApprovalRequired` broadcasts; fan-out filter not traced | Inferred |
| GitNexus not used | No GitNexus MCP tools were available in this session; Read/Grep were used as the fallback, per root `CLAUDE.md` | Verified |
| ADR id | `scripts/new-adr-id.sh "Group chat reuses the normal chat"` exit 0 → `ADR-20261028-group-chat-reuses-the-normal-chat` | Verified |
| **Self-check** | Re-read this ADR against the dispatch done-criteria: every §5 question 1–8 answered (A1–A8, A11); D13 chrome with design-system rules (A9); options compared (§3); risks (§7); implementation outline with the four owners, order and dependencies (§6); UAT rows (§10); founder-only questions (§9); no code or contract edited (`git status` shows only this file); no Go tests or heavy commands run | Verified |
