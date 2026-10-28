Status: Approved

`ADR: [ADR-20261028 — Group chat reuses the normal chat](../architecture/ADR-20261028-group-chat-reuses-the-normal-chat.md)` (accepted after its one correction round; section 11 of that ADR wins over its earlier text)

# Feature Specification: Group chat reuses the normal chat (#1449)

**Created**: 2026-10-28
**Input**: founder requirements and decisions log D1-D28 at `docs/internal/specs/1449-group-chat-requirements.md` (interview-me output, in the requirements form the founder interview produced; there is no separate `.interview.json`); ADR above; approved wireframe `docs/internal/design/1449/index.html` (screenshot `wireframe-full.png`, frames 1-4).
**Status note**: `Approved` (2026-10-28). The five founder questions of the first draft were answered as D21-D25 (2026-10-28) and are applied throughout. The three later questions were answered on 2026-10-28 and are applied throughout: Q-R2-1 as D26 (one Library deletion removes both copies of a chat file, normal and group alike), F-6 as D27 (groups follow `session_messaging.steer_body`, clamped to at most 65,536 bytes) and F-7 as D28 (a step answering two groups keeps its card in the agent's own chat; both groups show the attention cue). **D21 (no Stop in the group chat) supersedes the Stop parts of D4 and D16 and the ADR's Stop decisions (A6, C-I5, UAT-8, UAT-9, UAT-22); where this spec and the ADR differ on those points, the founder decision wins.** D23 changes the ADR's origin-precedence text (A5.2) the same way. **Grill round 1 (report `/workspace/logs/1449/1449-spec-review.md`) and the final round 2 (report `/workspace/logs/1449/1449-spec-review-round2.md`) are applied: see "Grill Round 1 Dispositions" and "Grill Round 2 Dispositions".** No founder question is open. The technical items that held the spec in review (A-1, A-2, A-13 decided by the architect; A-4/A-9 and A-6/A-7 set as owner-refinable defaults) are recorded under "Technical Decisions"; nothing is left undecided.

---

## Overview

Group chats (the team room, squads and ad-hoc groups) are live today but use their own screen: plain-text transcript, own message box, a header listing every member, a full-width layout, their own store and paging. Every chat feature and fix has to be built twice, and the group lacks most of what the normal chat shows (markdown, diagrams, images, files).

This feature makes the group chat **the normal chat with more than one agent in it**. The group opens on the same message list, the same message box (without the model picker, Auto-approve, token counter and skills), the same centred column, the same history behaviour (paging, "Return to latest", the loading cover). Participants are no longer listed in a header: the header shows a small avatar stack with a count, and a side panel opens on demand with status, links, mute/remove, add, cost and the invitation context. Group messages and agent answers can carry images and files in both directions; they are stored like normal chat uploads, and one explicit deletion in the Library removes a chat file everywhere, in normal chats and groups alike (D26). Approval and question cards show up in the group only when they belong to **this** group's work. There is no Stop in the group: stopping happens in each agent's own chat (D21). "Add participant" in an agent's main chat starts a **new** group in a new session; the agent's own chat stays one-to-one. The group storage on the server (`pkg/rooms`) is unchanged in kind; a thin adapter lets the shared chat stack read it (decision D1; option C, a full server merge, is rejected).

The header of normal and helper chats loses its grey bar and borders (D13). That part is **already built** on branch `feature/1449-minimal-header` (commits `1d7886ecc`, `ede852eb2`: the `ChatHeaderSlot` at the right end of the workspace header line). This spec covers only how the group plugs into that slot and what must not regress there.

**Out of scope** (founder, requirements section 6): #1068 messaging scope; #1461 presence awareness; live writing of answers in groups (a later feature); a server merge of groups into sessions; stronger avoidance of passwords in the invitation summary (#1467, accepted risk D18); any Stop control or stop endpoint in the group (D21).

**Dropped by the founder (D21):** the ADR's two unproven Stop items (turn-scoped stop, per-request question cancel) disappear together with the group Stop; no agent-engine stop code is part of this feature. The remaining unproven ADR items are listed under "Items still to be proven in RED/CHECK".

## Existing Codebase Context

> GitNexus MCP tools were not available in this session. Every row below was found with Read/Grep (the fallback `.claude/CLAUDE.md` allows) and is labelled **Verified** (read in this task) or **From ADR** (stated and verified by the ADR author; not re-read here). No impact-analysis run is claimed. Impact rows are **Inferred** from caller greps and must be re-run with `impact` (upstream) by the implementing lead before each edit (shared rule 9).

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `src/components/chat/ChatScreen.tsx::ChatScreen` / `OmnipusComposer` / `VirtualUserMessageRow` / `VirtualAssistantMessageRow` | modified (composer extracted; rows gated on source) | 2,961 lines, 39 under the 3,000 fail limit (`wc -l`, Verified). Composer and rows live here. |
| `src/components/chat/ChatMessageList.tsx`, `HistoryBoundary.tsx`, `useHistoryViewport.ts`, `useOpenReveal.ts`, `useChatHistory.tsx` | modified (read a `ConversationSource` instead of the session stores) | From ADR A1.2 |
| `src/components/chat/ChatHeaderSlot.tsx` (branch `feature/1449-minimal-header`) | extended (group Participants control) | Verified by `git show feature/1449-minimal-header:...`; absent on this spec branch until that branch lands |
| `src/components/rooms/RoomScreen.tsx`, `RoomHeader.tsx`, `RoomTranscript.tsx`, `RoomComposer.tsx` | replaced / deleted at the end | From ADR A11, FE-4 |
| `src/store/rooms.ts::useRoomStore` | extended (limit, attempt, viewMode, viewGeneration); stays its own store (FR-199) | From ADR A1.3 |
| `src/hooks/useCancelState.ts` | session path unchanged; room path installs no global listener | From ADR C-I4 |
| `src/components/panel-shell/registry.tsx`, `types.ts` | extended (`participants` panel) | Verified (files exist) |
| `pkg/rooms/*` (`journal.go`, `answers.go`, `fanout.go`, `admin.go`, `messages.go`, `attach.go`) | extended | `pkg/rooms/journal.go` decodes with plain `json.Unmarshal` (From ADR; additive journal change) |
| `pkg/agent/room_request.go`, `participant.go`, `stop_session.go`, `request_reminder.go`, `ask_user_wire.go` | extended (origin rule per D23, question provenance) / unchanged | From ADR A5, C-I6 |
| `pkg/tools/answer_request.go::AnswerRequestTool` and `pkg/tools/ask_user_question.go` | extended (`media`; provenance at creation) | From ADR C-I1, C-I6 |
| `pkg/addressing/addressing.go::Capture` | extended (`Media`) | From ADR C-I3 |
| `pkg/gateway/rest_uploads.go::HandleUpload`, `rest.go` media routes, `ws_ask_user.go`, `rooms_runtime.go`, `policy_approver.go`, `approvals.go` | extended / unchanged | From ADR C-I1, C-I6, C-I5 |
| `pkg/media/store.go::resolveWorkspaceRef`, `pkg/media/library/library.go::Library.Upload` | reused unchanged | From ADR C-I1 |
| `pkg/media/library/library.go::Library.IncrementRefcount`, `OrphanGC` (hourly, default age 30 days) and `pkg/agent/media_present.go::sessionRefcounter` | extended use (room retention ownership, FR-076) | Verified by reading: a fresh upload has refcount 0 and is collectable; only consuming sessions raise the count |
| `pkg/gateway/rest_uploads.go::finishWorkspaceUpload`, `stageWorkspaceUploadCopy`; `pkg/gateway/rest_library_write.go::handleLibraryEntryDelete`; `pkg/library/transfer.go::Root.Delete` | the staging step becomes shared with agent-returned files (FR-077); the Library delete handler is **extended** to remove the associated chat copy too (D26, FR-086) | Verified by reading: a normal upload is written twice (chat media copy, and a Library file under the workspace's `.library` folder); the Library panel deletes only the Library file |
| `pkg/media/library/library.go::Library.Upload` / `uploadInternal` and `stageWorkspaceUploadCopy` (publication with a caller-preallocated identity, create-or-adopt) | extended (not a wire change): needed so an answer's publication is repeat-safe (FR-078) | Verified by reading: today each call mints a fresh UUID and collision-suffixed file name |
| `pkg/agent/upload_workpath.go::uploadWorkPathRegistry` / `RecordUploadWorkPath` | superseded as the source of the Library-file association by a durable record (FR-086); may stay as a cache | Verified by reading: in-memory map with FIFO eviction, lost on restart, so it cannot back a deletion |
| `src/components/library/LibraryExplorer.tsx::deleteMutation` and its confirmation dialog | extended (confirmation names the chat copy; error with Retry, FR-087) | Verified by reading: calls `deleteLibraryEntry` (Library file only today) |
| `pkg/config/session_messaging.go::EffectiveSteerBodyBytes`, `pkg/gateway/rooms_runtime.go::gatewayRoomDeps.Limits` | reused; the group limit becomes `min(EffectiveSteerBodyBytes(), 65536)` (D27, FR-074) | Verified by reading: default `DefaultSMSteerBodyBytes = 65536`; any value of at least 1 is used as is today |
| `pkg/config/defaults.go` (`"answer_request": "allow"`), `pkg/coreagent/seed.go`, `pkg/coreagent/role_policies_adr090.go` | unchanged (tool already registered and policy-entered) | Verified by grep |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|----------------|------------|----------------|----------------|
| `ChatMessageList` / `HistoryBoundary` / `useHistoryViewport` / `useOpenReveal` (switch to `ConversationSource`) | **HIGH (Inferred)** - the #1266/#1452 history stack, about 30 `ChatScreen.history-*.test.tsx` files plus `src/store/chat.history-*.test.ts` | `ChatScreen`, `useChatHistory`, the session chat | every opened chat |
| `OmnipusComposer` extraction | **HIGH (Inferred)** - 1,000-line move out of a 2,961-line file; every `ChatScreen.*composer*` test | `ChatScreen` | every chat send path |
| `Capture` (`pkg/addressing`) and `commitRoomRequest` | **HIGH (Inferred)** - durable request ledger shared by admission, rewake and held delivery | `pkg/rooms/fanout.go`, `pkg/agent/room_request.go` | all group requests |
| origin merge rule `pkg/agent/participant.go::mergeApprovalOrigin` and `pkg/gateway/policy_approver.go` (D23: room origin only when the step answers only the group) | **MEDIUM (Inferred)** - agent engine; hands-on UAT required before landing (root `CLAUDE.md`, "Hands-on check before any agent-engine landing") | every approval card of every chat (origin label) | all approval cards |
| `AskUserQuestionTool.Execute` / `PendingSet` / `askUserResumeDispatcher` | MEDIUM (Inferred) | question cards, resume | `AskUserQuestionCard` consumers |
| `buildAdmission` (invitation prefix, read-only) | MEDIUM (Inferred) - runs under the room lock | first-request admission | all group sends |
| answer-intent staging, publication and recovery (`pkg/rooms`, `pkg/tools/answer_request.go`, `pkg/media/library`) | **HIGH (Inferred)** - new durable multi-step transaction in the room service | `DeliverAnswer`, `recover`, `AnswerRequestTool` | every group answer with files |
| media retention (orphan collector) and Library publication of group files | **HIGH (Inferred)** - data loss if wrong; shared with normal chat uploads | normal chat upload path, `OrphanGC` | Library panel, member chats |
| `src/components/chat/AttachmentCard.tsx` and the shared message rows (file rows gain download and error states) | **MEDIUM (Inferred)** - shared with every chat | `VirtualUserMessageRow`, `VirtualAssistantMessageRow` | all chats with files |
| Library deletion removing the associated chat copy (`handleLibraryEntryDelete`, the media manifest association, `LibraryExplorer` dialog) | **HIGH (Inferred)** - irreversible data removal shared with every normal chat upload (D26) | Library panel delete, `moveLibraryEntry` / rename (association must follow the file) | every chat that shows the deleted file (shows it as unavailable) |
| `FeedKindLabel`, `MainChatAddParticipant` | already changed on `feature/1449-minimal-header` | - | - |

**HIGH rows need sign-off before GREEN starts** (spec rule). The HIGH rows are the reason for the ordering in the ADR (§11 C-M1): the session-source and composer changes ship neutral first, with the existing suites unchanged as the gate.

---

## Contract Changes (contract-first - Hard Constraint #8)

Order of work: BE-0 edits these before any Go or TypeScript (5-step procedure, root `CLAUDE.md`, "Contract regeneration"). Both copies of every room schema (`contracts/components/schemas/*.yaml` **and** the inline copy in `contracts/asyncapi.yaml`, which say "keep both copies in sync by hand") are edited in the same commit as the regenerated `pkg/api/generated/` and `src/lib/api/generated/`. Frontend-only helper types (`ConversationSource`, `ComposerConversation`, `ComposerProfile`) are not wire types and carry `// not-wire-format`.

| Schema / operation | New / Changed | File | Notes |
|---|---|---|---|
| `RoomEntry` | changed | `contracts/components/schemas/RoomEntry.yaml` + inline in `asyncapi.yaml` | + optional `attachments`: array of `MediaPart`, `maxItems: 16`. Absent = none. Journal change is additive; no version field. **D24/D27:** `text` states the one group message size limit with the same wording as the `RoomMessageRequest` row (at most 65,536 UTF-8 bytes; a smaller operator setting `session_messaging.steer_body` lowers it); today the schema says 5 MiB while the server enforces the operator setting. |
| `RoomMessageRequest` | changed | `RoomMessageRequest.yaml` + inline copy | + optional `media`: array of strings, `maxItems: 16`, each `media://workspace/<workspace id>/<id>`. `text` becomes `minLength: 0`. **D24/D27: one size limit.** The contract text (5 MiB today) is corrected to: "at most 65,536 UTF-8 bytes; an operator can lower this limit with `session_messaging.steer_body`, never raise it" (counted in bytes, not characters). The server's effective limit is `min(steer_body, 65,536)`: a larger operator value is clamped to 65,536, a smaller one is honoured, the default is 65,536. The same effective limit applies to an agent's answer text (`answer_request`, entry `text`). Rule enforced by the handler with inbound validation on and off: text not blank **or** media non-empty. |
| `uploadFiles` (`POST /upload`) | changed | `contracts/openapi.yaml` | Closes existing drift: `workspace_id` (query or form field) already crosses the boundary undocumented. Document it and the conflict rule (both given: `workspace_id` routes to the library, `session_id` ignored). Add a `503` response for "workspace library unavailable". |
| `AskUserQuestionCard` | changed | `AskUserQuestionCard.yaml` + inline copy in `asyncapi.yaml` | + optional `room_ref` (`RoomRef`) and `origin_label` (string 1-200), same shapes and limits as on `ToolApprovalRequiredFrame`. This one card is embedded in `AskUserQuestionFrame` (live) and `SessionStateFrame.pending_asks` (reconnect snapshot); the frame envelope gets no separate copy. |
| `Room` | changed | `Room.yaml` | + optional `invitation_context`: `RoomInvitationContext` and `invitation_context_status` (`generating` / `ready` / `unavailable`). `invitation_context.text` is returned by `getRoom` only. |
| `RoomInvitationContext` | new | `contracts/components/schemas/RoomInvitationContext.yaml` | `{ status, text? (max 4096), author_agent_id, generated_at?, cost_micro_usd?, cost_unpriced? }`. |
| `RoomMetaFrame` | changed | `RoomMetaFrame.yaml` + inline copy | + `invitation_context_status`. The text itself never travels on a frame. |
| `LibraryEntry` | changed (**D26, contract change for backend-lead**) | `contracts/components/schemas/LibraryEntry.yaml` | + optional, read-only `chat_copy`: boolean, `true` when the file has an associated chat copy that a deletion of this entry also removes (FR-086). Absent or `false` = no associated chat copy. Lets the delete confirmation say so before anything is removed (FR-087). |
| `MediaLibraryEntry` | changed (**D26, contract change for backend-lead**) | `contracts/components/schemas/MediaLibraryEntry.yaml` | + optional, read-only `library_path`: string, the workspace-relative path of the entry's Library file (for example `.library/shot.png`), same path rules as `LibraryEntry.path`. This is the durable association between the two copies (the in-memory `uploadWorkPathRegistry` is lost on restart and cannot back a deletion). Written by the shared publication path (FR-077), updated by a same-workspace move or rename, cleared when the Library file is deleted. |
| `deleteLibraryEntry` (`DELETE /library/{workspace_id}/entries`) | changed (**D26, contract change for backend-lead**) | `contracts/openapi.yaml` | Description states: deleting a file that has an associated chat copy also deletes that chat copy (bytes and manifest entry); deleting a directory does so for every associated file inside it; retention owners (session reference counts, group retention, FR-076) never block an explicit deletion. Order: chat copy first, Library file last, so a failure leaves the entry listed and a repeat completes the work; a chat copy that is already gone counts as removed. Responses: `204` both removed; `500` `ErrorResponse` "could not delete <name> completely" when either removal failed (the Library file is then still present); existing `400`/`401`/`403`/`404` unchanged. No new request field: the behaviour is not optional (D26). |
| `moveLibraryEntry`, `renameLibraryEntry`, `copyLibraryEntry` | changed (description only, **D26**) | `contracts/openapi.yaml` | Rename and a move inside the same workspace keep the association (the media entry's `library_path` follows the file). A copy is a new file with no association. A move to another workspace drops the association: the moved file becomes an ordinary file there and the chat copy stays in the original workspace (stated as a known limit in DOC-008). |
| `deleteWorkspaceMedia` | unchanged | `contracts/openapi.yaml` | Has no UI caller; not part of D26 (D26 is about the Library's deletion). |
| `answer_request` tool parameters | changed | `pkg/tools/answer_request.go` (tool schema, not a REST/WS wire type) | + optional `media` (array of strings, max 16). Listed here because agents read it; it is not SPA-facing. |

**Must not collide with**: `RoomRef` (reused as is); `MediaPart` (reused as is - the one media shape the SPA already renders); `RoomEntryFrame`/`RoomSnapshotFrame` (carry `RoomEntry`, so they inherit `attachments`).

**Contract-related decisions** are in "Ambiguity Warnings" and "Technical Decisions" (A-3, A-4, A-9, A-12, A-14).

## API and Data

- **Reads**: `getRoomHistory` (`before_seq`, `limit` 1-1000, unchanged contract) feeds the shared list. The room store gains a `limit` argument so the shared fill rule can request pages of 20-100.
- **Summary detail**: the summary text is read only through `getRoom`. The panel refetches `getRoom` when a `room_meta` frame reports `ready`, and again after every reconnect or re-attach while the panel is open and the last known status was `generating` or the detail was never loaded. No text-bearing frame exists.
- **Writes**: `postRoomMessage` (now with `media`), `uploadFiles` (library branch, no session), `answer_request` tool (now with `media`), `deleteLibraryEntry` (now also removes the associated chat copy, D26).
- **Stored**: attachments live in the entry journal as `MediaPart` references. Each group file exists in the two representations a normal chat upload has: the chat media copy (`media://workspace/<workspace>/<id>`, under `workspaces/<workspace>/media/`, with a reference count and the orphan collector) and the Library file (under the workspace's `.library` folder, shown and deleted in the Library panel). The room record gains the invitation-context record. Server-internal (not wire) durable records are added for: **retention ownership** of each accepted attachment (FR-076), the **answer intent** of an agent answer with files (a state machine with immutable staged source bytes, preallocated publication identities, text and mentions; FR-078), and the **invitation reservation** per request with its transitions (FR-080). Deleting a group removes only the group's own journal directory (D19); the retention ownership is not released by deletion. Each media manifest entry durably records the path of its Library file (`library_path`); one explicit Library deletion removes both copies (D26), for normal chat uploads and group files alike.
- **Streams to the browser** (unchanged routing, `attach_room`): `room_entry`, `room_snapshot`, `room_member_state`, `room_meta` (now carrying the invitation status).

---
## User Stories & Acceptance Criteria

Decisions that shape every story (founder's, not reopened): D1 one chat screen for both kinds; D2 and D3 full display and files both ways; D4 composer; D5 "is working" line; D6 participants; D7 width; D8 and D15 approvals; D9 and D10 starting a group; D11 history; D13 minimal chrome; D17 add participant in main chats only; D18-D20 summary risk, files, unavailable summary; D21 no Stop in the group; D22 attention cue accepted; D23 cards only for work that answers the group; D24 one message size limit; D25 group files exactly like normal chat uploads; D26 one Library deletion removes both copies of a chat file; D27 the size limit follows the operator setting but never above 65,536 bytes; D28 a step answering two groups keeps its card in the agent's own chat.

### User Story 1 - Group messages look like normal chat messages (Priority: P0)

A person reading a group wants every agent answer to show markdown, tables, code with a copy button, formulas, diagrams, images and files exactly as in a normal chat, each answer under its author's name and avatar (R1, R2, D2). Today the group shows plain text.

**Why this priority**: it is the first complaint in the founder's requirements and the reason groups feel unfinished.

**Independent Test**: open a group whose agents answered with a table, a code block, a formula, a diagram and an image; compare with the same content in a normal chat.

**Acceptance Scenarios**:

1. **Given** an agent answered with a table and a code block, **When** the group is open, **Then** the table and code render as in a normal chat and the code block has a Copy control.
2. **Given** an answer contains a diagram and a formula, **When** it is shown, **Then** both render as in a normal chat; invalid diagram source shows the normal chat's diagram error state while the text stays readable.
3. **Given** an entry has images and files, **When** it is shown, **Then** images open in the lightbox and other files show as download cards; a file that can no longer be fetched shows the normal chat's broken state with Retry. A file that is not an image (for example a PDF) offers a keyboard-operable download control in both the person's and the agent's rows, and shows an inline error with Retry when it cannot be fetched (shared row behaviour; the normal chat gains it too).
4. **Given** two different agents answered, **When** the list is shown, **Then** each answer carries its author's name and avatar, and the person's own messages sit right-aligned without a label.
5. **Given** an answer was posted after a pause, **When** it is shown, **Then** it keeps its muted "after pause" mark.
6. **Given** another chat is streaming in the background or was visited last, **When** a group is opened, **Then** no tool calls or status of that other chat appear on any group row.

### User Story 2 - The group message box is the normal message box (Priority: P0)

A person wants to type, attach and send in the group exactly as in a normal chat, but without controls that make no sense for several agents (R3, D4).

**Why this priority**: the message box is the main interaction surface; the second complaint.

**Independent Test**: place a normal chat and a group side by side and compare the message boxes.

**Acceptance Scenarios**:

1. **Given** a group is open, **When** the person looks at the message box, **Then** it has the same card, attach button and send button as the normal one.
2. **Given** a group is open, **When** the person looks at the message box, **Then** there is no model picker, no Auto-approve control, no token counter and no skills control.
3. **Given** a message longer than the group's size limit (65,536 bytes, or the operator's smaller `session_messaging.steer_body`; a larger setting is clamped to 65,536, D27), **When** the person sends it, **Then** it is refused with a message that names the limit in force, and nothing is posted. The box has no Stop button (D21).
4. **Given** the person types "/", **When** the menu opens, **Then** it lists only the six group commands (pause, resume, mute, unmute, members, help); another command is refused with a message and links to the members' own chats.
5. **Given** the person types "@", **When** the menu opens, **Then** it offers "all" and every current member, muted ones included; a picked member stays addressed after a rename; deleting the picked name removes only that explicit addressing, after which the ordinary rules apply (a message with no @-name goes to every member that is not muted).
6. **Given** the group is read-only, reconnecting, or has not yet received its first live view after opening, **When** the person looks at the box, **Then** sending is disabled and the reason is shown in the box's notice area.
7. **Given** a message has no text (or only blank text) and no attachment, **When** the person sends, **Then** it is refused and nothing is posted.
8. **Given** the group is open and the box is empty, **When** the person looks at it, **Then** its placeholder reads "Message the group - type @ to address someone".

### User Story 3 - Send images and files into the group (Priority: P1)

A person wants to attach screenshots and files to a group message so the addressed agents see them (D3).

**Why this priority**: required by D3; group entries are text-only today.

**Independent Test**: attach a screenshot, address one agent, send, then check the group and that agent's own chat.

**Acceptance Scenarios**:

1. **Given** a group with Jarvis, **When** the person attaches a screenshot and sends it to @Jarvis, **Then** the image shows in the person's message with the lightbox, Jarvis's own chat shows the request with the image, and Jarvis can describe it; the file also appears in the workspace Library.
2. **Given** an attachment and no text, **When** the person sends, **Then** the message is accepted and shown.
3. **Given** the file library cannot be reached, **When** the person attaches a file, **Then** the attachment chip shows an explicit error and no chat session is created or used.
4. **Given** 16 attachments are on a message, **When** the person adds a 17th, **Then** it is refused.
5. **Given** a message names a file that belongs to another workspace, a missing file, or something that is not a workspace file, **When** it is sent, **Then** the whole message is refused and nothing is posted.
6. **Given** a request with an image sat waiting for a stopped agent, **When** that agent is started again later, **Then** it still receives the image.

### User Story 4 - Agents return images, files and diagrams in their group answers (Priority: P1)

A person asks an agent in the group for a file or a chart and wants the result in the group, and the same answer in that agent's own chat (D3).

**Why this priority**: required by D3, the other half of Story 3.

**Independent Test**: ask an agent in the group to send back a diagram image and a file.

**Acceptance Scenarios**:

1. **Given** an agent was asked for a diagram image, **When** it answers with the image, **Then** the group shows it with the lightbox and the agent's own chat shows the same answer with the file.
2. **Given** the agent's file is within reach of its own chat but not yet in the workspace, **When** it answers with it, **Then** the file is stored in the workspace first (it also appears in the workspace Library) and the answer shows that stored file.
3. **Given** storing or checking an attached file fails, **When** the agent answers, **Then** the agent receives an error and nothing is posted.
4. **Given** two answers with identical text but different files, **When** a repair after an interruption runs, **Then** they are never treated as the same answer; an agent that repeats the same call (same text, same original files) while its answer is not yet fully completed gets that same answer completed, not a second one.
5. **Given** an answer names a file of another workspace, **When** the agent submits it, **Then** it is refused and nothing is posted.
6. **Given** an agent answer text longer than the group's size limit (the same effective limit as person messages, D27), **When** the agent submits it, **Then** it is refused, and nothing is posted, copied or passed on.
7. **Given** an agent answer with files was accepted and the server stops before it is posted, **When** the server restarts, **Then** the answer is completed once from what was recorded (the original bytes, the text, the mentions) without asking the agent again; an answer the agent was told was refused is never posted later; and if the group, the agent's membership or the request no longer allows it, it is not posted.

### User Story 5 - Calm live view while several agents work (Priority: P1)

A person who addressed two agents wants to see which of them is working and then each finished answer, without streaming text racing across the screen (D5).

**Why this priority**: it is the only live feedback in the group.

**Independent Test**: address two agents and watch the list until both answered.

**Acceptance Scenarios**:

1. **Given** two agents are addressed, **When** they work, **Then** the list shows one "<Agent> is working..." line per agent.
2. **Given** an agent finishes, **When** its answer arrives, **Then** its line is replaced by the answer under its name and avatar, and the other line stays.
3. **Given** an agent's work is waiting for the person in the agent's own chat (for example an approval from a helper), **When** the line is shown, **Then** the agent's avatar shows the attention animation and a link "Open <Agent>'s main chat".
4. **Given** an agent is working, **When** time passes, **Then** no partial answer text is shown before the finished answer.
5. **Given** a request ended as failed, interrupted, refused or unavailable, **When** it is shown, **Then** the line shows that state's label instead of "working".

### User Story 6 - Same column and same history behaviour (Priority: P0)

A person wants the group in the same centred column as a normal chat and wants to scroll back through a long group the same way (R5, D7, D11).

**Why this priority**: removes the full-width complaint and gives groups the hard-won history behaviour.

**Independent Test**: open a long group, scroll up through several pages, use Return to latest, reopen it.

**Acceptance Scenarios**:

1. **Given** a group is open, **When** the window is wide, **Then** messages sit in the same centred column as in a normal chat and the message box in the same narrower centred column, with or without the Participants panel open.
2. **Given** a long group, **When** the person scrolls far up, **Then** earlier messages page in without the view jumping.
3. **Given** the person is reading earlier messages, **When** they choose Return to latest, **Then** the newest message is shown again.
4. **Given** a group is reopened, **When** it loads, **Then** the loading cover is shown and the view lands at the newest message.
5. **Given** loading an earlier page fails, **When** the person chooses Retry, **Then** the same page is requested again and the messages already shown stay.
6. **Given** an earlier page is loading, **When** the person types and sends, **Then** sending works.
7. **Given** a new group has no messages, **When** it opens, **Then** it shows an empty list and a ready message box and posts nothing.
8. **Given** a phone-width window, **When** a group is open, **Then** the header strip collapses as it does for a normal chat and the Participants button stays reachable.

### User Story 7 - Participants on demand (Priority: P0)

A person wants the participant list out of the way until needed (R4, D6): a small stack of avatars with a count opens a side panel.

**Why this priority**: the header listing every member was called "way too much".

**Independent Test**: open the panel in a group with four members and use each control.

**Acceptance Scenarios**:

1. **Given** a group with N members, **When** it is open, **Then** the header's right end shows up to three avatars plus "+(N-3)" and the member count as one button labelled "Participants".
2. **Given** the button, **When** the person activates it (mouse, touch or keyboard), **Then** the panel opens beside the chat and the button reports itself as pressed; activating it again closes the panel.
3. **Given** the panel is open, **When** the person reads it, **Then** it shows per member the status, a link to the member's own chat and a menu with mute/unmute and remove, plus Add participant, "Cost so far" and "Invitation context".
4. **Given** a member's request was interrupted, **When** the person chooses Ask again, **Then** the message box is filled with that member addressed.
5. **Given** a squad or ad-hoc group, **When** the person uses the panel, **Then** they can delete the group; in the team room Add participant is disabled with its explanation.
6. **Given** costs include an unpriced model call, **When** the panel shows the cost, **Then** it says the cost is incomplete.
7. **Given** the panel is open, **When** the person leaves the group, **Then** the panel closes.

### User Story 8 - Start a group from a chat, with an invisible summary (Priority: P1)

A person in an agent's main chat wants to add another agent; this starts a new group in a new session, and the invited agent gets a short background summary that is never posted in the chat (R6, D9, D10, D18, D20).

**Why this priority**: it is the only way users start ad-hoc groups.

**Independent Test**: use Add participant in a main chat, pick an agent, open the panel, send the first message.

**Acceptance Scenarios**:

1. **Given** the person is in an agent's main chat, **When** they look at the header's right end, **Then** an icon-only "Add participant" control is shown; it is not shown in helper chats or other chats.
2. **Given** the person picks an agent, **When** the group is created, **Then** a new empty group opens, the original chat is unchanged and no message was posted in either.
3. **Given** the group just opened, **When** the person opens the panel, **Then** "Invitation context" shows "generating" and then the summary text, without a page reload, also when the connection dropped while it was being made; if the text cannot be fetched the panel shows an error with Retry.
4. **Given** the summary is ready, **When** the person sends the first message to the invited agent, **Then** the agent knows the background and nothing about the summary appears in the group.
5. **Given** the model provider is down, **When** a group is started, **Then** the group opens, the first message goes out at once and the panel says "Invitation context: unavailable".
6. **Given** the origin chat contains a recognised key or credential, **When** the summary is made, **Then** the recognised value is not in the summary.
7. **Given** a summary was made, **When** the panel shows "Cost so far", **Then** it includes the summary's cost once.
8. **Given** the summary is still being made, **When** the person sends the first message, **Then** the message is never held; the invited agent simply goes without the summary.
9. **Given** the first request to a member could not be stored and is retried, or another request reaches the member meanwhile, **When** the requests are delivered, **Then** the member receives the summary exactly once and a request that went out without it never gets it later.

### User Story 9 - Approvals and questions appear in the group only when they belong to it (Priority: P1)

A person approving an agent's tool call during group work wants the card in the group, and does not want cards about unrelated work there (D8, D15).

**Why this priority**: otherwise the person must leave the group to unblock the work.

**Independent Test**: have an agent ask for approval during group work, and separately have it need an unrelated approval.

**Acceptance Scenarios**:

1. **Given** an agent needs a tool approval for this group's work, **When** the card appears, **Then** it shows in the group and in the agent's own chat.
2. **Given** the person approves in the group, **When** the decision is made, **Then** the agent's own chat shows it resolved without a reload.
3. **Given** the agent has an unrelated approval in its own chat, **When** the group is open, **Then** no card for it appears in the group.
4. **Given** an agent asks a question during group work, **When** it is parked, **Then** the question card shows in the group and in the agent's chat; answering in the group updates both.
5. **Given** the group is reloaded while a question is pending, **When** it reopens, **Then** the card is still shown in both places.
6. **Given** a helper the agent started for the group needs an approval, **When** it asks, **Then** the card shows only in the agent's own chat; in the group the agent's avatar shows the attention animation with "Open <Agent>'s main chat", which stops once the approval is resolved.
7. **Given** a secret is requested during group work, **When** it is asked, **Then** no secret card appears in the group; a link to the agent's own chat is shown.
8. **Given** the agent's running step also handles other input (a message typed in its own chat, a messaging-channel message, or a task), **When** an approval arises, **Then** the card stays in the agent's own chat only (D23: a card appears in the group only for work that answers the group).
9. **Given** two browsers, **When** one approves and the other denies at the same moment, **Then** exactly one decision takes effect and both show the same final state.
10. **Given** the agent's running step answers requests from two different groups, **When** an approval or question arises, **Then** the card stays in the agent's own chat only, and both groups show the agent's attention animation with "Open <Agent>'s main chat" until it is resolved (D28).

### User Story 10 - (retired by D21: no Stop in the group chat)

The founder decided that the group chat has no Stop control; stopping happens in each agent's own chat (D21, supersedes the Stop parts of D4 and D16). The story, its scenarios, requirements, tests and UAT rows were removed; the id is not reused.

### User Story 11 - Minimal header, group included (Priority: P0)

A person wants no header bars or borders in any chat; the existing workspace line stays on the left; chat controls sit at the right end (R8, R9, D13).

**Why this priority**: founder-approved wireframe; the normal/helper part already exists, the group part completes it.

**Independent Test**: look at a normal chat, a helper chat and a group chat.

**Acceptance Scenarios**:

1. **Given** a group is open, **When** the person looks at the header line, **Then** the left side is unchanged and the right end holds the Participants button; there is no bar, no border and no surface fill.
2. **Given** a normal chat or a helper chat, **When** the person looks at the header line, **Then** it still shows only "Add participant" (main chat) or the muted breadcrumb "<Parent> / <Helper>" with the parent as a link (helper chat), with no bar or border.
3. **Given** a group, **When** the person hovers or focuses the Participants button, **Then** a tooltip names it; it is operable by keyboard and has a touch-sized target.
4. **Given** a group page, **When** the header line is shown, **Then** the "Open browser" control of the last visited chat is not shown.
5. **Given** a screen reader user, **When** a group is open, **Then** the group's name is announced as the chat's name.
6. **Given** a narrow window, **When** the avatar stack with a count is the widest control, **Then** the workspace strip keeps its own modes and nothing overlaps.

### User Story 12 - Agent text is display-only (Priority: P0)

A person must be able to trust that anything an agent writes in a group is only displayed, never executed (the earlier system-agent spec's FR-168).

**Why this priority**: the group now renders rich content from agents; the safety promise must hold.

**Independent Test**: have an agent post a pause command, a script tag, a script-scheme link and a remote image.

**Acceptance Scenarios**:

1. **Given** an agent posted literal HTML, **When** it is shown, **Then** the HTML appears as text and nothing runs.
2. **Given** an agent posted a link with a script scheme, **When** it is shown, **Then** it is struck through and not clickable.
3. **Given** an agent posted a remote image address, **When** it is shown, **Then** no request to that remote address is made and the image does not load.
4. **Given** an agent posted "/pause" as text, **When** it is shown, **Then** it is visible text and nothing pauses.

### User Story 13 - The group message box never touches another chat (Priority: P0)

A person who opens a group right after a streaming chat, or a chat with an open question, must not stop, send to, or be blocked by that other chat.

**Why this priority**: the shared message box reads chat-wide state today; this is the main regression risk.

**Independent Test**: stream a long answer in a normal chat, switch to a group, send and press Escape.

**Acceptance Scenarios**:

1. **Given** no chat was opened in this tab, **When** a group is opened and a message sent, **Then** the message is posted to the group and no chat is created.
2. **Given** another chat is streaming, replaying, or has an open question, **When** the group is opened, **Then** the group's message box is usable and sending posts only to the group.
3. **Given** another chat is streaming, **When** the person presses Escape in the group, **Then** the other chat keeps streaming.
4. **Given** another chat is open on a question, **When** the group is open, **Then** the group's message box is not locked by it.

### User Story 14 - Files outlive the group (Priority: P2)

A person who deletes a group must not lose the files uploaded into it or produced by it (D19); a person who deletes a file in the Library must have it gone everywhere (D26).

**Why this priority**: data-loss protection.

**Independent Test**: delete a group that had an image; open the image from the agent's own chat.

**Acceptance Scenarios**:

1. **Given** a group with an image is deleted, **When** the person opens the agent's own chat or the Library, **Then** the image still opens.
2. **Given** a group file is deleted in the Library panel, **When** an existing group (or a member's main chat that mirrored it) shows the entry, **Then** both copies are gone (D26) and the entry shows the file as unavailable (the broken-file state with Retry, which keeps failing); nothing recreates the file.
3. **Given** the chat copy of a file is gone, **When** an existing group shows the entry, **Then** the entry shows the broken-file state with Retry, not a silent gap.
4. **Given** a file attached to a message in a paused group, or to a request held for a stopped agent, or to a group that was then deleted, **When** more than 30 days pass and the automatic orphan cleanup runs (also after a restart), **Then** the file still opens.
5. **Given** a file uploaded in a normal chat, **When** the person deletes it in the Library panel, **Then** both copies are gone and the normal chat shows it as unavailable, exactly as a group does (D26 applies to all chat files).
6. **Given** the person chooses Delete on a Library file that has a chat copy, **When** the confirmation opens, **Then** it says that the copy used in chats is deleted too and that chats will show the file as unavailable; **and when** the deletion fails part-way, **Then** the panel shows an error naming the file, the file stays listed, and repeating Delete completes it.

### User Story 15 - What already works keeps working (Priority: P1)

The sidebar's group section, the six commands, mentions, pause and the audit behaviour keep working, and the normal chat's behaviour does not change (D12, D1).

**Why this priority**: the refactor touches the most sensitive chat code.

**Independent Test**: run the existing chat history and composer suites unchanged; use the sidebar and commands.

**Acceptance Scenarios**:

1. **Given** a workspace with groups, **When** the sidebar is shown, **Then** the existing group section under the workspace lists them and opens them.
2. **Given** `/pause` and `/resume` are used, **When** messages are sent while paused, **Then** they behave as documented today (saved, not delivered).
3. **Given** a group is open, **When** its updates arrive, **Then** no normal chat's stored transcript is created or replaced.
4. **Given** the shared stack serves a normal chat, **When** the existing chat history and composer tests run with no edits, **Then** they pass.

## Edge Cases

- **Group with 2, 3, 4 and 10 members**: the stack shows 2, 3, 3 plus "+1", 3 plus "+7"; the count is always the full number.
- **Message with only an attachment**: accepted. **Text with 16 attachments**: accepted. **17th**: refused in the box and, if forced through the API, with 400.
- **Attachment deleted from the Library after posting**: both copies are removed (D26); the group entry, the mirrored main-chat entries and any normal chat that used it show the file as unavailable (broken-file state with Retry). The chat copy being gone always shows that state.
- **Library directory deleted** (for example the `.library` folder): every file inside with an associated chat copy loses that copy too (D26).
- **Library file renamed or moved inside the workspace, then deleted**: the association follows the file; both copies are removed. **Copied**: the copy has no association; deleting it removes only the copy. **Moved to another workspace**: the association is dropped; the chat copy stays in the original workspace (known limit, documented).
- **Upload made before this feature** (its association was only in memory): a one-time repair on start associates a `.library` file with the media entry of the same workspace that has the same SHA-256 and the same file name (ignoring a " (N)" duplicate suffix), only when exactly one entry matches; otherwise the file shows no chat copy and its deletion removes only the Library file (FR-086).
- **Library deletion fails part-way** (chat copy removed, Library file not, or the reverse attempt failed): 500, the file stays listed, a repeat completes it; a chat copy already gone counts as removed.
- **Explicit Library deletion of a file a pending answer intent or a retention owner still references**: deleted anyway; the intent never recreates it (FR-078 (4)); the group entry shows it as unavailable.
- **Upload succeeds, post never sent**: an unattached library file stays, as with an abandoned normal-chat upload; no entry is written.
- **Agent answer whose files cannot be copied**: tool error to the agent; nothing appended, mirrored or forwarded.
- **A stopped member's held request with an image, revived later**: the model gets the image.
- **Invitation summary arrives after the 45-second limit**: discarded. **Gateway restarts while generating**: shown as unavailable after restart. **Group deleted or origin agent removed during generation**: result discarded.
- **Prose password in the origin chat** (not a registered secret, no known credential shape): can pass into the summary. Accepted by the founder (D18, #1467), stated in the documentation; no test asserts it is removed.
- **A step that handles a group request and other input** (own-chat message, channel, task): the card stays in the agent's own chat only (D23). **A step that handles requests of two different groups**: the card stays in the agent's own chat only; both groups show the attention animation and the link (D28).
- **Message of exactly 65,536 bytes**: accepted; **65,537 bytes**: refused. **Multi-byte text**: counted in bytes, not characters (21,845 three-byte characters are accepted, 21,846 are refused).
- **Operator setting `steer_body`** (D27): 131,072 → the group limit stays 65,536; 1,000 → the limit is 1,000 (1,000 bytes accepted, 1,001 refused with a message naming 1,000); unset or 0 → the default 65,536. A changed setting applies to the next message; entries already stored are never re-checked.
- **Opening a group right after a streaming chat**: no foreign tool calls on the group's last row.
- **Reconnect during paging**: stale page responses (after leaving the group, after a truncating snapshot) are ignored.
- **Very long group (more than 10,000 entries)**: the opening snapshot holds the newest 10,000; older pages load on demand.
- **Remote images, script links, raw HTML, invalid diagram source in an agent answer**: handled as in Stories 12 and 1.

---

## UI Screens and States

Frames refer to the approved wireframe (`docs/internal/design/1449/`): 1 normal chat, 2 helper chat, 3 group chat, 4 group chat with the Participants panel.

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Group page (shared message list), frame 3 | The shared open cover until the group's first live view; "catching up" state as in the normal chat | New group: empty list, ready message box, nothing posted | Group read failed: the shared error state with Retry; no such group: "no such group chat" | Earlier messages not shown: the shared "Return to latest" and earlier-history behaviour; an earlier-page failure keeps rows and offers Retry on the same page | Messages in the centred column; author name and avatar per agent answer |
| "... is working" lines | - | none shown | request states failed / interrupted / refused / unavailable show their label | several members: one line each; finished ones replaced by answers | working lines disappear as answers arrive |
| Attention avatar + "Open <Agent>'s main chat" | - | - | link target unknown: line without link (as the existing "Open main chat" rule: shown when Omnipus can find that main chat) | - | animation while waiting; stops when resolved |
| Group message box (shared composer, group profile) | disabled with reason "waiting for the group" until the first live view | placeholder "Message the group - type @ to address someone" | blocked reasons (reconnecting, read-only, snapshot pending) in the notice area; upload error on the chip; nothing-to-send refusal | attachments chips with some failed: failed chips marked, others kept | message posted, box cleared |
| Header Participants button, frame 3 | rendered once the group is known | n/a (a group has at least two agents; the team room may be hidden below two) | - | members unknown: button hidden | avatar stack (3 + "+N") and count; pressed state while the panel is open |
| Participants panel, frame 4 | skeleton rows while the group record loads | - | group record failed: error line with Retry | cost unpriced: "incomplete" marker; invitation context "generating..." | status per member, links, menu, Add participant, cost, invitation context |
| Invitation context disclosure | "Invitation context: generating..." | n/a | "Invitation context: unavailable" | - | summary text rendered through the shared markdown pipeline |
| Approval / question cards in the group | as in the normal chat | none | as in the normal chat (expired, cancelled) | several pending: shown one at a time with "+N more" as in the normal chat | card shown with "for group <name>" origin; resolved card closes |
| Library panel delete confirmation (existing panel, D26) | "Deleting..." on the confirm button, as today | - | partial failure: error naming the file, the file stays listed, Delete can be repeated | - | file without a chat copy: today's dialog; file with a chat copy: the dialog adds that the copy used in chats is deleted too and chats will show the file as unavailable |
| Header slot, frames 1 and 2 (already built) | - | - | roster failed: id fallback in the breadcrumb (existing honest states) | - | Add participant icon (main chat) / muted breadcrumb (helper chat) |

## User Journey

1. The person opens a workspace; the sidebar shows the existing group section (S-114); they open a group. The shared open cover appears and the view lands at the newest message (US 6).
2. They read answers with full formatting, images and files; each under its author (US 1).
3. They type "@" and pick members, optionally attach a screenshot, and send (US 2, US 3). Working lines appear per member (US 5).
4. A card appears (tool approval or question) and they answer it in the group or in the agent's chat; both update (US 9). For helper work they see the attention avatar and follow the link.
5. If they want to stop an agent they go to that agent's own chat (the member link in the panel or the attention link); the group itself has no Stop (D21). Ask again for an interrupted request is in the panel.
6. They open the Participants panel to check status, mute or remove, add someone, read cost and the invitation context (US 7).
7. To start a group they go to an agent's main chat, press Add participant at the right end of the header line, pick an agent; a new empty group opens (US 8).

## Accessibility and Keyboard

- Participants button: a real button (design-system `Button`/`IconButton`), reachable by Tab in header order, `aria-pressed` reflects the panel, accessible name "Participants" plus the count; Enter and Space toggle. Focus ring comes from the global rule only (design-system rule 7; no per-component focus utilities).
- Panel: opens without stealing the message box's text. Focus on closing follows the established shared behaviour per action: closing with the panel's close button returns focus to the message box; closing with Escape returns focus to the Participants button (the invoking trigger). Participants does not change the shared panel shell. The per-member menu is operable by keyboard (existing `dropdown-menu`).
- Group name announced as the chat's name through a visually hidden status node (same pattern as the main chat's title, per D13/ADR A9.2).
- "... is working" lines are polite live-region updates (same as the normal chat's status indicator); the attention link is a real link.
- Touch: icon controls adjacent to the strip's toggles use the design system's touch-minimum target (rule 12). Zoom and narrow behaviour per `docs/internal/design/design-system-definition.md`.
- Escape in the group message box cancels nothing outside the group (US 13).

## Design-System Components

Catalog checked first (`design-system/catalog.json`). Entries found: `avatar`, `icon-button`, `tooltip`, `sheet`, `badge`, `dropdown-menu`, `popover`, `skeleton`, `alert-dialog`, `job-status`. Decisions:

| UI element | Component | Note |
|---|---|---|
| Participants button | `Button` (ghost) with `Tooltip` | no `UsersThree` icon (would read as the Team toggle) |
| Avatar stack | **new catalogued composite `AvatarStack`** (built on `avatar`) | used by header, panel and `ActivityBar` (the inline copy there is switched to it): third use, so rule 14 requires publishing it through the four-part contract (rule 9). Classification: composite (`src/components/ui/CLAUDE.md`). |
| Attention animation | the sidebar's existing attention halo (`SidebarAgentIcon`, `sidebar-attention-halo`) shared, not copied | rule 14 |
| Panel | existing registered side panel mechanism (`registry.tsx`, `SidePanelShell`) | new `participants` entry, not a strip entry |
| Member menu, delete | `dropdown-menu`, `alert-dialog` | as `RoomHeader` uses today |
| Cost, status text | existing `formatAllocatedCost`, `deliveryLabel` | unchanged helpers |
| Message list, rows, composer, history boundary, cover | existing chat components | not re-implemented |
| Header slot | `ChatHeaderSlot` (already on `feature/1449-minimal-header`) | group control plugs in; no bar, no border, tokens only (rules 1, 2) |

No raw `<button>`, no `title=` tooltips, no hard-coded colours or spacing (rules 1, 2).

## Security and User Promises

- **Untrusted agent text**: group text renders through the same pipeline as normal-chat answers (no raw HTML, safe-link allow-list, remote images blocked by the page's content security policy, strict diagrams). Display-only; commands are parsed only from the person's posts on the server (FR-168). Flag: **security-lead on-demand review** of the room-rendering switch and media read posture.
- **Files**: group files use the workspace media library and the same read posture as every chat image (unguessable id; optional-auth routes; the media route builds the caller workspace from its own URL). #1449 neither widens nor tightens it and adds no new authorization to uploads or media reads. The only new cross-workspace check is at **room ingestion**: each message media reference is compared with the room's workspace (422). **Reported to security-lead** (ADR 11.4): all chat media is served by optional-auth routes, pre-existing and installation-wide; any wider hardening is a separate request to security-lead.
- **Approvals** (`docs/security.md`, "Approve a request"): a card in the group is the same decision as in the agent's chat, with the same grant scope ("Always Allow" lasts for the agent chat's own scope); one decision wins. Cards from helpers and secrets stay in the agent's chat. Hard Constraint #6 is unaffected: no new tool, no new policy value, no Auto-approve change. `docs/security.md` must say a card appears in a group only for work that answers the group (D23) (DOC-004).
- **Tool surface** (`docs/tools.md`): `answer_request` keeps `allow` in the ceiling and in role policies; its new `media` parameter is covered by the same policy and by the same file-access checks the agent's own chat uses. No new tool.
- **Invitation summary** (privacy): same workspace only; excludes secret-card entries, secret-tool results and vault content; input and output pass the registered-secret scrubber and the credential-pattern redactor; output filtered before storage. **Known gap, accepted by the founder (D18, #1467)**: a password written in plain prose can pass. Documentation says so plainly.
- **Explicit deletion** (D26): a Library deletion now also removes the chat copy that every chat of the workspace may show. Authorization is unchanged (the existing Library delete route and its workspace scoping); the association is resolved only inside the same workspace, so a deletion can never remove another workspace's chat copy. The confirmation states the consequence before anything is removed.
- **Focus-area review**: touches gateway upload, media and Library delete routes, approvals and the agent engine - `security-lead` on-demand review (not part of implementation).
- **Agent-engine landing**: hands-on UAT by `uat-tester` plus independent `uat-validator` on the exact candidate (root `CLAUDE.md`, "Hands-on check before any agent-engine landing") because `pkg/agent` (origin rule, question provenance) and gateway approval handling change.

---

## Behavioral Contract

Primary flows:
- When a group opens, the system shows the shared message list in the centred column with the loading cover until the group's first live view arrives, then lands at the newest message.
- When an agent answers, the system shows the finished answer under the agent's name and avatar with full formatting, files and images; it never shows partial text.
- When the person sends text and/or attachments, the system posts one message, shows it right-aligned, and delivers it to the addressed members' own chats with the same files.
- When the person presses Participants, the system toggles the side panel; the chat column does not move.
- When the person presses Add participant in a main chat and picks an agent, the system opens a new empty group; the original chat is unchanged; a background summary is prepared without posting anything.
- When a card belongs to this group's work, the system shows it in the group and in the agent's own chat; one decision resolves both. When the step also handles other input, the card stays in the agent's own chat only.
- When a message is longer than the group limit (65,536 bytes, or the operator's smaller `steer_body`, D27), the system refuses it (422), names the limit in force and posts nothing.
- When the person deletes a file in the Library that has a chat copy, the system removes both copies after a confirmation that says so; every chat that shows the file then shows it as unavailable (D26).

Error flows:
- When a message has nothing to send, the system refuses it (422) and posts nothing.
- When any attachment of a message is not a file of this group's workspace, is missing, or is stranded, the system refuses the whole message (422) and posts nothing.
- When the file library is unavailable, the system answers an upload with an explicit 503-class error shown on the attachment chip; it never falls back to a chat session.
- When an agent's answer files cannot be stored or checked, the agent gets a tool error and nothing is posted.
- When the summary cannot be made (provider error, quota, 45-second limit, restart, deletion), the group works without it and the panel says "Invitation context: unavailable".
- When an earlier-page load fails, the system keeps the shown messages and offers Retry on the same page.
- When a Library deletion removes only one of the two copies, the system answers 500, keeps the Library file listed and lets a repeat finish the deletion.

Boundary conditions:
- When a message carries 16 attachments, it is accepted; with 17 it is refused (400).
- When the summary arrives at or before 45 seconds, it is used; after, it is discarded.
- When the history fill needs pages, the system asks for pages of 20 to 100 entries, each strictly older than the last.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not post the invitation summary, or any trace of it, into the group or into either chat, because D10 makes it invisible background (viewable only in the panel).
- The system must not hold a person's first group message while the summary is being made, because D20 says it is never held.
- The system must not show approval, question or secret cards in the group unless they carry this group's reference; helper cards stay in the agent's own chat only (D15), because unrelated cards "do not make sense here" (founder, D8).
- The system must not read or write any normal chat's stored transcript when a group is open (FR-199).
- The system must not create a chat session for a group upload or message.
- The system must not let agent text run a command, approve, or answer a card (FR-168).
- The system must not delete files with a group (D19).
- The system must not leave the chat copy of a file behind after an explicit Library deletion, and must not recreate a file the person deleted explicitly through any retention, repair or recovery step (D26, FR-078).
- The system must not let the operator setting raise the group message limit above 65,536 bytes (D27).
- The system must not show in a group a card from a step that answers two different groups (D28).
- The system must not add anonymous read access beyond the normal chat's (ADR C-I1: group files share the normal chat's read posture; no tighter and no looser).
- The system must not claim anywhere (UI, documentation, tests) that passwords written in plain prose are removed from the summary (D18).
- The system must not add live writing of answers, presence, or other human participants (out of scope), and must not add a Stop control or stop endpoint to the group (D21).
- The system must not reintroduce `FeedKindLabel`'s bar, `MainChatAddParticipant`'s row, `RoomHeader`'s member list, or `ChatControls` on group pages (retired by D13, ADR A9).
- The system must not add "Add participant" to helper or non-main chats (D17).
- The system must not introduce hand-written wire types (Hard Constraint #8).

### Machine-Verifiable Constraints

**Error Codes / Messages** (all error bodies use the existing `ErrorResponse` schema):

| Situation | Status | Message / shape |
|---|---|---|
| `postRoomMessage`: no non-blank text and no media | 422 | `ErrorResponse`; nothing appended |
| `postRoomMessage`: more than 16 media, or other schema violation | 400 | existing `400BadRequest` |
| `postRoomMessage`: media not `media://workspace/<this room's workspace>/<id>`, other workspace, missing, or stranded | 422 | `ErrorResponse`; nothing appended; whole message refused |
| `postRoomMessage`: text longer than the effective limit (`min(steer_body, 65,536)` UTF-8 bytes) | 422 | `ErrorResponse` naming the size and the limit in force; nothing appended; same with inbound validation on and off (A-12) |
| `answer_request`: text longer than the effective limit | tool error | names the limit in force; nothing posted, copied or passed on |
| `deleteLibraryEntry`: file (or directory) with associated chat copies, all removed | 204 | both copies gone |
| `deleteLibraryEntry`: removal of a chat copy or of the Library file failed | 500 | `ErrorResponse` "could not delete <name> completely"; the Library file is still listed; a repeat completes it |
| `postRoomMessage`: success | 202 | unchanged |
| `uploadFiles` with `workspace_id`, library unavailable | 503 | `ErrorResponse`; never falls through to the session path |
| `answer_request` with unusable media | tool error | nothing appended, mirrored or forwarded |

**Limits**: attachments per message at most 16; message and agent-answer text at most 65,536 UTF-8 bytes, lowered by a smaller operator `session_messaging.steer_body` and never raised by a larger one (D24, D27); invitation text at most 4,096 characters, summary of about 200 words; summary input at most 40 entries and 16 KiB; summary time limit 45 seconds; history pages of 20..100 entries (server maximum 1000); `getRoomHistory` contract unchanged. Per-part upload size limit 100 MB, as the existing `uploadFiles` contract says.

**Performance Bounds**: opening a group must not request history when the opening snapshot already covers the fill goal (zero `getRoomHistory` calls) - Scenario S-43; the list must not re-render existing rows when only a member's delivery state changes (row identity stable) - test T-30.

**Scope Boundaries**: the system MUST NOT add any Stop control or endpoint to the group (D21), MUST NOT extend the invitation summary to a different workspace, and MUST NOT accept `media://` refs outside the room's workspace.

## Integration Boundaries

### Shared chat stack (display, history, message box)

- **Data in / out**: the room source projects room entries to chat messages and exposes the history state the list reads; the message box talks to the group through one room-scoped conversation object.
- **Contract**: frontend-only types (`// not-wire-format`); the wire types are the generated room types.
- **On failure**: history errors surface through the shared boundary (Retry on the same page); send errors in the box's notice area.
- **Development**: real components, real room store; the real server is used for the end-to-end tests; no mock of the shared stack.

### Workspace file library

- **Data in / out**: group uploads and agent answer files go in; both representations (chat media copy, Library file) are written by one shared publication path; `media://workspace/...` references come out and are stored in entries and mirrored; retention ownership keeps referenced files out of the orphan collector.
- **Contract**: `uploadFiles` (`workspace_id`), `MediaPart`.
- **On failure**: 503 for unavailable; 422 at room ingestion for a reference that is not the room's workspace file; nothing appended. Upload and media-read authorization are unchanged.
- **Deletion** (D26): `deleteLibraryEntry` removes the Library file and its associated chat copy (found through the media entry's durable `library_path`); chat copy first, Library file last; 500 on a partial failure with the file still listed. Deleting a group never deletes files (D19).
- **Development**: real library, test workspace.

### Agent engine (questions, requests)

- **Data in / out**: question provenance at creation; request capture with media; the origin rule of D23.
- **Contract**: `AskUserQuestionCard`, `answer_request` tool.
- **On failure**: a refused answer (size, files) returns a tool error and appends nothing.
- **Development**: real engine in Go integration tests with a stub model; hands-on UAT on the exact candidate before landing (root `CLAUDE.md`).

### Model provider (invitation summary)

- **Data in / out**: bounded, filtered chat tail in; summary text out.
- **Contract**: internal; text exposed only through `getRoom`.
- **On failure**: `unavailable` (provider error, quota, 45 s, restart, deletion); never blocks a message.
- **Development**: stub model for tests; the real provider only in UAT (UAT provider/model per root `CLAUDE.md`).

### Approval and question hub

- **Data in / out**: cards with the group reference to the group view; decisions by card id.
- **Contract**: `ToolApprovalRequiredFrame` (unchanged), `AskUserQuestionCard` (+ reference).
- **On failure**: unreachable hub shows the existing reconnect states; one decision per card (first valid wins).
- **Development**: real hub in integration; two-browser UAT for the race.

---

## Items still to be proven in RED/CHECK

The ADR's two unproven Stop items (UNPROVEN-1 turn-scoped stop, UNPROVEN-2 per-request question cancel) were **dropped by D21** together with the group Stop; no spike, no escalation path and no agent-engine stop code remain in this feature. Still **[U]/[I]** in the ADR and therefore proven in RED/CHECK, not assumed (each has a pre-designed fallback): the nested assistant-ui runtime scoping (FE-0 spike; fallback `ComposerTransport`, T-26) and approval frames reaching a client on a group page (T-40/S-81; risk R8).

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|------------------|------------------------|---------------------|
| A-1 | Whether a summary output longer than 4,096 characters is truncated or counts as `unavailable` | truncate silently | **Decided (architect, 2026-10-28, TD-1):** after filtering, an output longer than 4,096 characters is cut at the last word boundary at or before 4,096 characters (a hard cut at 4,096 when no word boundary exists), status `ready`, cost counted once as usual |
| A-2 | Whether an origin chat with nothing to summarise (0 usable entries after exclusions) makes a summary or goes straight to `unavailable` | call the model with empty input | **Decided (architect, 2026-10-28, TD-2):** no model call, status `unavailable` at once, no cost line |
| A-3 | D24/D27 require one size limit, in bytes; OpenAPI `maxLength` counts characters, and the server counts UTF-8 bytes; the operator can lower the limit | set `maxLength` to 65536 and call it done | Contract text states "at most 65,536 UTF-8 bytes; a smaller operator `session_messaging.steer_body` lowers it"; the server stays the byte authority and applies `min(steer_body, 65,536)`; the over-limit status is 422 whichever layer catches it (A-12) |
| A-4 | Duplicate media refs in one message | dedupe silently | **Set (TD-4), default, owner may refine during RED with evidence (backend-lead):** accepted as listed and shown once per listed reference (no silent de-duplication); retention is acquired once per (entry, reference) as FR-076 already says. Asserted by DS-1 row 32 |
| A-5 | Exact English wording of states ("Invitation context: unavailable"/"generating") | invent wording | Wording quoted from founder decision D20 and ADR C-I8 is the oracle; other labels reuse today's `deliveryLabel` set |
| A-6 | Whether the tooltip of the Participants button says "Participants" | any | **Set (TD-6), default, owner may refine during RED with evidence (frontend-lead):** "Participants" (same as the accessible name), checked against the design-system skill |
| A-7 | Behaviour of the Participants panel on narrow widths | custom | **Set (TD-7), default, owner may refine during RED with evidence (frontend-lead):** same as the other registered side panels (they already collapse/overlay on narrow widths); no new behaviour |
| A-9 | Whether `answer_request.media` may point to the same file more than once | n/a | **Set (TD-4), default, owner may refine during RED with evidence (backend-lead):** same as A-4: accepted as listed, one staged and published file per listed entry; asserted by DS-12 row 16 |
| A-12 | Whether the schema-level maximum (a 400 under inbound validation) or the server byte check (422) answers an over-limit message | return whichever fires first | Spec value: 422 with the size message in both modes; backend-lead reconciles the schema and the handler (for example by documenting the byte limit in the schema text and leaving the byte check to the server) |
| A-13 | Bounds of the private staging for an answer's original bytes | none | **Decided (architect, 2026-10-28, TD-3):** at most 16 files per answer and 100 MB per file (the `uploadFiles` per-part limit); a larger file is a definitive rejection ("nothing posted") before acceptance; staging lives under the room service's own data, never in an agent-reachable tree; staged bytes are removed after the commit or after a recorded rejection; while an intent stays accepted, staging is kept (also across restarts and the orphan cleanup) |
| A-14 | How a Library file is matched to its chat copy for uploads made before this feature (the association was only in memory) | match by file name only | Spec value (architect, applying D26): a one-time repair on start matches a `.library` file to the media entry of the same workspace with the same SHA-256 and the same file name ignoring a " (N)" duplicate suffix, only when exactly one entry matches; no match or several matches leave the file without an association (its deletion removes only the Library file, and the dialog says nothing about chats). Never matched across workspaces |
| A-11 | Oracles for empty-file, oversize-file and unsafe-filename uploads in DS-10 are "as in the normal chat" | copy values from the implementation | qa-lead cites the normal chat's documented behaviour (`docs/using-omnipus-ui.md`) or the `uploadFiles` contract for each value; if neither states it, ask the dispatcher |

---
## BDD Scenarios

Expected values come from this spec, the cited contract schemas and the ADR; never from reading an implementation. Scenario ids (S-nn) are the stable keys used by the datasets, tests, UAT rows and the traceability matrix.

### Feature: Group chat reuses the normal chat

#### Background

- **Given** a workspace "Acme" with agents Jarvis, Mia and Petra, each with its own main chat
- **And** a group "Launch" in Acme with members Jarvis, Mia and Petra, open in the SPA

### Story 1 - Display

#### Scenario: S-01 Agent answer with a table and a code block renders as in a normal chat
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path
- **Given** Jarvis answered with a markdown table and a fenced code block
- **When** the group list shows that answer
- **Then** the table renders as a table, the code block renders with syntax formatting, and no raw markdown markers are visible

#### Scenario: S-02 Code block in a group answer can be copied
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path
- **Given** Jarvis's answer contains a code block with the text `echo hi`
- **When** the person activates the code block's Copy control
- **Then** the clipboard holds exactly `echo hi` and the control confirms the copy

#### Scenario: S-03 Diagram and formula in a group answer render
**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path
- **Given** Mia's answer contains a valid diagram block and a formula
- **When** the group list shows that answer
- **Then** the diagram is drawn and the formula is typeset, as in a normal chat

#### Scenario: S-04 Invalid diagram source shows the diagram error state and keeps the text
**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Error Path
- **Given** Mia's answer contains a diagram block with invalid source and a sentence of text
- **When** the group list shows that answer
- **Then** the normal chat's diagram error state is shown for the block and the sentence stays readable

#### Scenario: S-05 Images and files in an entry render with lightbox and download card
**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path
- **Given** an entry carries one PNG image and one PDF file
- **When** the group list shows the entry
- **Then** the image is shown and opens in the lightbox on activation, and the PDF shows as a download card with its filename

#### Scenario: S-06 A file that can no longer be fetched shows the broken state with Retry
**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error Path
- **Given** an entry references a library image that has been deleted
- **When** the group list shows the entry
- **Then** the normal chat's broken-image state is shown with a Retry control, and the entry's other content stays visible

#### Scenario: S-07 Answers from two agents carry their own author and the person's message is unlabelled
**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Happy Path
- **Given** the person asked a question, Jarvis answered, then Mia answered
- **When** the group list shows the three entries
- **Then** Jarvis's answer shows the name "Jarvis" with Jarvis's avatar, Mia's shows "Mia" with Mia's avatar, and the person's message is right-aligned with no author label

#### Scenario: S-08 An answer posted after a pause keeps its "after pause" mark
**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Alternate Path
- **Given** Jarvis's work started before `/pause` and its answer is posted after it
- **When** the group list shows that answer
- **Then** the answer's footer shows the muted "after pause" suffix

#### Scenario: S-09 A group opened right after a streaming chat shows no foreign tool calls
**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Edge Case
- **Given** a normal chat with Petra is streaming an answer with three tool calls and the person then opens the group
- **When** the group list shows Jarvis's last answer
- **Then** none of the three tool calls or the streaming status appear on any group row

#### Scenario: S-129 A person's PDF in a group offers a keyboard-operable download
**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path
- **Given** the person posted "report.pdf" in the group
- **When** the person focuses the file card with the keyboard and presses Enter
- **Then** the file downloads with its name, in the person's row and in an agent's row alike

#### Scenario: S-130 A non-image file that cannot be fetched shows an inline error with Retry
**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error Path
- **Given** an agent's entry references "data.csv" whose chat copy is missing
- **When** the person activates its download
- **Then** an inline error is shown on the file card with a Retry control, and Retry repeats the same fetch

### Story 2 - Message box

#### Scenario: S-10 Group message box has the normal card and attach/send but not the session-only controls
**Traces to**: User Story 2, Acceptance Scenario 1 and 2
**Category**: Happy Path
- **Given** the group is open
- **When** the person inspects the message box
- **Then** the card, attach button and send button are present, and no model picker, Auto-approve control, token counter or skills control is present

#### Scenario: S-12 Slash menu lists only the six group commands
**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Happy Path
- **Given** the group is open
- **When** the person types "/"
- **Then** the menu lists exactly pause, resume, mute, unmute, members and help

#### Scenario: S-13 A steering command is refused with links to the members' chats
**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Error Path
- **Given** the group is open
- **When** the person sends "/model gpt @Mia"
- **Then** the message is refused with "Rooms don't steer agents - use Mia's main chat" and a link to Mia's main chat, and nothing is posted

#### Scenario: S-14 "@" offers all and every current member including muted ones
**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Happy Path
- **Given** Petra is muted
- **When** the person types "@"
- **Then** the menu offers "all", Jarvis, Mia and Petra

#### Scenario: S-15 A picked member survives a rename; deleting the pick removes only the explicit addressing
**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case
- **Given** the person picked Mia from the menu, then Mia was renamed to "Mia R", and Petra is muted
- **When** the person sends the message
- **Then** the explicit mentions list holds Mia and only Mia receives it; and when instead the person deletes the picked name and types an ordinary note with no @-name, the explicit mentions list is empty and the server addresses every member that is not muted (Jarvis and Mia, not Petra)

#### Scenario Outline: S-16 Typed names resolve only by the exact-name rule
**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case
- **Given** members named "Mia" and "Mia R" and no picker use
- **When** the person sends the text `<text>`
- **Then** the addressed members are `<addressed>`

**Examples**:

| text | addressed |
|---|---|
| @Mia hello | Mia |
| @Mia R hello | Mia R |
| @Mi hello | nobody (short name stays plain text) |
| @MIA hello | Mia (case-insensitive) |

#### Scenario Outline: S-17 Blocked states disable sending and show the reason
**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Error Path
- **Given** the group is in state `<state>`
- **When** the person looks at the message box
- **Then** sending is disabled and the notice area shows `<reason>`

**Examples**:

| state | reason |
|---|---|
| connection down, reconnecting | the reconnecting reason |
| group read-only | the group's read-only reason |
| first live view not yet received | the waiting-for-the-group reason |

#### Scenario: S-18 A message with neither text nor attachment is refused
**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Error Path
- **Given** the message box is empty, or holds only spaces and a line break
- **When** the person sends
- **Then** nothing is posted and the server (if reached) answers 422

#### Scenario: S-19 The empty message box shows the group placeholder
**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Happy Path
- **Given** the group is open and the box is empty
- **When** the person looks at the box
- **Then** the placeholder reads "Message the group - type @ to address someone"

#### Scenario Outline: S-118 Message size limit counts UTF-8 bytes
**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Edge Case
- **Given** the person composed a message of `<size>`
- **When** the person sends it
- **Then** the outcome is `<outcome>`

**Examples**:

| size | outcome |
|---|---|
| 1 character | accepted (202) |
| 65,536 ASCII characters (65,536 bytes) | accepted (202) |
| 65,537 ASCII characters (65,537 bytes) | refused (422) with a message naming the size and the 65,536-byte limit; nothing posted |
| 21,845 three-byte characters (65,535 bytes) | accepted (202) |
| 21,846 three-byte characters (65,538 bytes) | refused (422); nothing posted |

#### Scenario Outline: S-150 The operator setting lowers the group limit but never raises it
**Traces to**: User Story 2, Acceptance Scenario 3; User Story 4, Acceptance Scenario 6
**Category**: Edge Case
- **Given** the operator set `session_messaging.steer_body` to `<setting>`
- **When** the person sends a group message of `<size>` (and, separately, an agent submits an answer of the same size)
- **Then** the outcome is `<outcome>`

**Examples**:

| setting | size | outcome |
|---|---|---|
| unset | 65,536 bytes | accepted (default limit 65,536) |
| 131,072 | 65,536 bytes | accepted |
| 131,072 | 65,537 bytes | refused (422 / tool error) naming the 65,536-byte limit; nothing posted (clamped) |
| 1,000 | 1,000 bytes | accepted |
| 1,000 | 1,001 bytes | refused (422 / tool error) naming the 1,000-byte limit; nothing posted |

### Story 3 - Files into the group

#### Scenario: S-20 An image sent to @Jarvis shows in the message and reaches Jarvis's own chat
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path
- **Given** the person attached a screenshot and typed "@Jarvis what is this?"
- **When** the person sends
- **Then** the group shows the message with the image and lightbox, Jarvis's own chat shows the request with the same image, and Jarvis's answer describes the image

#### Scenario: S-21 An attachment-only message is accepted
**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** the person attached one image and typed nothing
- **When** the person sends
- **Then** the message is accepted (202) and shown with the image

#### Scenario: S-22 Library unavailable gives an explicit chip error
**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Error Path
- **Given** the workspace library cannot be reached
- **When** the person attaches a file
- **Then** the upload answers 503, the chip shows the error, and the file is not sent with any message

#### Scenario: S-23 Attaching in a group creates no chat session
**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Edge Case
- **Given** the person's session list holds N chats
- **When** the person attaches and uploads a file in the group
- **Then** the session list still holds N chats and the upload carries the workspace and no session

#### Scenario Outline: S-24 Attachment count limit
**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Edge Case
- **Given** a message with `<count>` attachments
- **When** the person sends it
- **Then** the outcome is `<outcome>`

**Examples**:

| count | outcome |
|---|---|
| 1 | accepted |
| 16 | accepted |
| 17 | refused in the box; 400 if forced through the API |

#### Scenario: S-25 A file of another workspace is refused and nothing is posted
**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path
- **Given** a message names `media://workspace/<other workspace>/<id>`
- **When** it is posted through the API
- **Then** the answer is 422, no entry is appended and no request is created

#### Scenario Outline: S-26 Invalid media references refuse the whole message
**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path
- **Given** a message with one valid file and one reference `<ref>`
- **When** it is posted
- **Then** the answer is 422 and neither file appears in the group

**Examples**:

| ref |
|---|
| a session file reference (not a workspace file) |
| a workspace reference that does not exist |
| a stranded library entry |
| a plain web address |
| an empty string |

#### Scenario: S-27 A held request with an image still hands the image to the revived agent
**Traces to**: User Story 3, Acceptance Scenario 6
**Category**: Edge Case
- **Given** Jarvis was stopped, the person sent "@Jarvis look" with an image, and the request is held
- **When** Jarvis is started again and picks up the request
- **Then** Jarvis receives the image as input, as on first delivery

#### Scenario: S-28 An upload that is never posted leaves no entry
**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Edge Case
- **Given** the person uploaded a file in the group and closed the page without sending
- **When** the group is read
- **Then** it contains no entry for the file; the chat media copy has no owner and is collectable by the orphan cleanup, while the Library file stays listed in the Library panel (the cleanup never removes it)

#### Scenario Outline: S-120 Attachments never consume the text budget
**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Edge Case
- **Given** a message whose text is `<text>` with one valid 5 MB attachment
- **When** the person sends it
- **Then** the outcome is `<outcome>`

**Examples**:

| text | outcome |
|---|---|
| 65,536 ASCII bytes | accepted (202) |
| 65,537 ASCII bytes | refused (422), whatever the attachment size |

#### Scenario: S-121 Earlier attachments are quoted by filename only
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case
- **Given** an earlier group message carried the file "plan.pdf" and Mia has not seen it, and the person now sends Mia a new message with the file "new.png"
- **When** Mia's request is built
- **Then** the quoted group context names "plan.pdf" as text only (no file content is resent), and only "new.png" is handed to the model as input

#### Scenario: S-128 A person's upload appears in the Library panel
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path
- **Given** the person attached "shot.png" to a group message and sent it
- **When** the person opens the workspace Library panel
- **Then** "shot.png" is listed there and can be downloaded, exactly as a normal chat upload

#### Scenario: S-135 The resolver refuses another workspace's reference when the caller's workspace is supplied independently
**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path
- **Given** a media reference of workspace B and a caller workspace A supplied independently of the reference
- **When** the reference is resolved
- **Then** it is refused as a cross-workspace reference; this is a resolver-level check only and adds no new upload or media-read authorization

### Story 4 - Files from agents

#### Scenario: S-29 An agent answer with an image appears in the group and in the agent's chat
**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path
- **Given** Jarvis was asked for a diagram image and submits an answer with that image
- **When** the answer is posted
- **Then** the group shows it with the lightbox and Jarvis's own chat shows the same answer with the file

#### Scenario: S-30 A file from the agent's own reach is stored in the workspace first
**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** Jarvis names a file that only its own chat can send
- **When** the answer is submitted
- **Then** the file is stored in the workspace first in both representations (chat media copy and Library file), and the posted answer references the stored chat copy

#### Scenario: S-31 A failed copy or check posts nothing and tells the agent
**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Error Path
- **Given** storing Jarvis's file fails
- **When** Jarvis submits the answer
- **Then** Jarvis receives a definitive "nothing posted" tool error only after the intent is durably recorded as rejected, and no entry, mirror or forwarded mention exists, now or after any restart

#### Scenario: S-32 Two answers with the same text and different files stay distinct
**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case
- **Given** two answers with the text "done" and files F1 and F2
- **When** the mirror repair runs
- **Then** both answers are kept, neither is treated as the other, and no duplicate is mirrored

#### Scenario Outline: S-33 An interruption between steps of an answer leaves no loss and no duplicate
**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case
- **Given** the server stops after step `<step>` of: record answer intent, copy, append, mirror, mention admission
- **When** the server restarts and repairs (from the recorded intent when no entry exists yet)
- **Then** the answer exists exactly once in the group and once in the agent's chat, with its files, mentions were admitted once, and the files are still retained

**Examples**:

| step |
|---|
| record answer intent |
| copy |
| append |
| mirror |
| mention admission |

#### Scenario: S-34 An answer naming another workspace's file is refused
**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Error Path
- **Given** Jarvis names `media://workspace/<other workspace>/<id>`
- **When** Jarvis submits the answer
- **Then** Jarvis receives a tool error and nothing is posted

#### Scenario Outline: S-119 An agent answer over the size limit is refused whole
**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Error Path
- **Given** Jarvis submits an answer whose text is `<size>` with one attached file
- **When** the answer is submitted
- **Then** the outcome is `<outcome>`

**Examples**:

| size | outcome |
|---|---|
| 65,536 bytes | posted, mirrored and forwarded |
| 65,537 bytes | tool error; nothing posted, copied (including the file) or passed on |

#### Scenario: S-133 Retrying an answer with the identical original file arguments repairs one answer
**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case
- **Given** Jarvis submitted an answer with the text "done" and a file at a path only its chat can reach, the group append succeeded, and mirroring failed
- **When** Jarvis submits the same text with the same original file argument again
- **Then** no second copy of the file is made, the existing answer is mirrored once, exactly one answer exists in the group, and a different original file with the same text is treated as a separate answer

#### Scenario: S-127 An agent-returned file appears in the Library panel
**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Happy Path
- **Given** Jarvis answered with a file that only its chat could reach
- **When** the person opens the workspace Library panel
- **Then** the file is listed there with its name and can be downloaded from it, and the group entry opens the same bytes

#### Scenario: S-137 An equal repeat after the answer fully completed is a new answer, as for text-only answers
**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case
- **Given** Jarvis's answer "done" with file F was posted, mirrored and its mentions admitted, and its tool receipt was lost
- **When** Jarvis submits the same text with the same original file again
- **Then** a new answer is posted (the same rule as for text-only answers today), with its own entry and its own publication; while the first was still incomplete, the repeat completes the first instead (S-133)

#### Scenario: S-140 An answer refused with "nothing posted" never posts later
**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Error Path
- **Given** an answer's file could not be read, Jarvis received the definitive "nothing posted" error, and the server then restarts
- **When** the server recovers
- **Then** no entry, mirror or forwarded mention appears for that answer, and its staged bytes and unreferenced published files are removed

#### Scenario: S-141 A source file that changes or disappears after acceptance does not change the answer
**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Edge Case
- **Given** an answer with file F was accepted (its bytes were staged), then F was edited (or deleted) before the answer was posted, and the server restarts
- **When** the answer is completed
- **Then** the posted file has the bytes F had at acceptance, and exactly one answer, one mirror and one Library artifact exist

#### Scenario Outline: S-142 A crash inside publication creates no duplicate artifact
**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Edge Case
- **Given** the server stops `<cut>` for one file of an accepted answer
- **When** the answer is completed after restart
- **Then** the file exists once as a chat media copy and once as a Library file, with the identities allocated when the answer was accepted

**Examples**:

| cut |
|---|
| after the chat media copy was committed and before its result was recorded |
| after the Library file was written and before its result was recorded |
| after both were written and before the entry was appended |

#### Scenario: S-143 An append failure keeps a pending answer recoverable through the collector and a restart
**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Edge Case
- **Given** appending an accepted answer failed for a reason that can be retried, a controlled clock then moves 31 days ahead, the orphan cleanup runs and the server restarts
- **When** the answer is completed
- **Then** its original bytes are still available and the answer posts once; if the failure was definitive the files are released and removed only after the rejection was recorded

#### Scenario Outline: S-144 Recovery rechecks the room before posting
**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Error Path
- **Given** an accepted answer is pending and, before recovery, `<change>`
- **When** the server recovers
- **Then** the answer is not posted, mentions are not admitted, the intent is recorded as rejected, and no model work is restarted

**Examples**:

| change |
|---|
| the group was deleted |
| the agent was removed from the group |
| the request was cancelled |

### Story 5 - Live view

#### Scenario: S-35 Two working lines, then each answer replaces its line
**Traces to**: User Story 5, Acceptance Scenario 1 and 2
**Category**: Happy Path
- **Given** the person addressed Jarvis and Mia
- **When** both are working
- **Then** the list shows "Jarvis is working..." and "Mia is working..."; and when Jarvis answers, his line is replaced by his answer while Mia's line stays

#### Scenario: S-36 A member waiting on the person shows the attention avatar and the link
**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path
- **Given** Mia's group request is waiting on the person and its card is in Mia's own chat only
- **When** the group list shows Mia's line
- **Then** Mia's avatar shows the attention animation and the link "Open Mia's main chat" is present, and the same animation shows on Mia in the Participants button

#### Scenario: S-37 No partial answer text appears while an agent works
**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Edge Case
- **Given** Jarvis is working and its own chat streams partial text
- **When** the group list is watched for 10 seconds
- **Then** no partial text of Jarvis's answer appears in the group before the finished answer

#### Scenario Outline: S-38 Terminal request states show their label
**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Alternate Path
- **Given** a member's request ended as `<state>`
- **When** the list shows the member's line
- **Then** the line shows that state's label (the existing delivery label set) and no "working" text

**Examples**:

| state |
|---|
| failed |
| interrupted |
| refused |
| unavailable |

### Story 6 - Column and history

#### Scenario Outline: S-39 Messages and message box sit in the normal chat's centred columns
**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a wide window and the Participants panel `<panel>`
- **When** the group is open
- **Then** the message column and the message-box column have the same widths as in a normal chat, both centred in the chat area

**Examples**:

| panel |
|---|
| closed |
| open |

#### Scenario: S-40 Scrolling far up pages earlier messages in without jumps
**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Happy Path
- **Given** a real group of 10,005 entries (above the 10,000-entry opening snapshot, so 5 entries are not loaded) opened at the newest entry
- **When** the person scrolls to the top of the loaded entries
- **Then** exactly one earlier-page request is made with an exclusive cursor equal to the oldest loaded sequence number, the 5 older entries appear above, and the entry the person was reading stays at the same screen position; a component test with a deliberately limited snapshot may be used only if it says so

#### Scenario: S-41 Return to latest brings back the newest message
**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path
- **Given** the person scrolled far up and "Return to latest" is shown
- **When** the person chooses it
- **Then** the newest message is shown and the control disappears

#### Scenario: S-42 Reopening shows the loading cover and lands at the newest message
**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Happy Path
- **Given** the person left the group and returns to it
- **When** it opens
- **Then** the loading cover shows until the first live view arrives, then the view is at the newest message

#### Scenario: S-43 No history is requested when the opening view already fills the screen
**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Edge Case
- **Given** the opening view holds entries that fill the screen, or the server says there are no older entries
- **When** the group opens
- **Then** zero `getRoomHistory` calls are made

#### Scenario: S-44 Short opening view fills with bounded contiguous pages
**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** the opening view does not fill the screen and the server says older entries exist
- **When** the group opens
- **Then** pages are requested one at a time, each with `before_seq` strictly smaller than the previous, each with `limit` between 20 and 100, until the screen is filled or the server says no older entries exist

#### Scenario: S-45 A failed earlier page keeps the rows and retries the same page
**Traces to**: User Story 6, Acceptance Scenario 5
**Category**: Error Path
- **Given** the request for the page before `before_seq` = 120 fails
- **When** the person chooses Retry
- **Then** the same request (`before_seq` = 120) is made again and the messages shown stay throughout

#### Scenario: S-46 Sending works while an earlier page loads
**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Alternate Path
- **Given** an earlier page request is in flight
- **When** the person sends a message
- **Then** the message is posted and shown

#### Scenario: S-47 A new group shows an empty list and a ready message box
**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Edge Case
- **Given** a group was just created
- **When** it opens
- **Then** the list is empty, the message box is ready, and no entry exists

#### Scenario: S-48 A page response that arrives after leaving the group is ignored
**Traces to**: User Story 6, Acceptance Scenario 5
**Category**: Edge Case
- **Given** an earlier page request for group "Launch" is in flight and the person opens another group
- **When** the response for "Launch" arrives
- **Then** the other group's list is unchanged

#### Scenario: S-49 At phone width the header collapses and the Participants button stays reachable
**Traces to**: User Story 6, Acceptance Scenario 8
**Category**: Alternate Path
- **Given** a 375-pixel-wide window
- **When** the group is open
- **Then** the workspace strip uses its narrow mode and the Participants button is visible and operable

### Story 7 - Participants

#### Scenario Outline: S-50 Avatar stack and count follow the member count
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Edge Case
- **Given** a group with `<members>` members
- **When** the header is shown
- **Then** `<avatars>` avatars are shown, the overflow marker is `<overflow>`, and the count reads `<members>`

**Examples**:

| members | avatars | overflow |
|---|---|---|
| 2 | 2 | none |
| 3 | 3 | none |
| 4 | 3 | +1 |
| 10 | 3 | +7 |

#### Scenario: S-51 The Participants button opens and closes the panel
**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Happy Path
- **Given** the panel is closed
- **When** the person activates the Participants button with the keyboard (Enter)
- **Then** the panel opens, the button reports pressed, and the chat column keeps its position; activating it again closes the panel and reports not pressed

#### Scenario: S-52 The panel shows status, links, menu, add, cost and invitation context
**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Happy Path
- **Given** Jarvis answered, Mia is waiting for the person, Petra is idle
- **When** the panel is open
- **Then** each member row shows its status and a link to the member's own chat with a menu, and the panel shows Add participant, "Cost so far" and "Invitation context"

#### Scenario: S-53 Ask again fills the message box with that member addressed
**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** Mia's request is `interrupted`
- **When** the person chooses Ask again on Mia's row
- **Then** the message box holds an addressed mention of Mia and nothing is sent

#### Scenario Outline: S-54 Mute, unmute and remove from the member menu
**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Alternate Path
- **Given** a group of three members
- **When** the person chooses `<action>` for Petra from the menu
- **Then** the outcome is `<outcome>`

**Examples**:

| action | outcome |
|---|---|
| mute | Petra shows muted and no longer receives default or "all" messages |
| unmute | Petra receives them again |
| remove (group of 3) | Petra leaves; the group has 2 members |
| remove (group of 2) | refused with the existing two-agent-minimum message; membership unchanged |

#### Scenario: S-55 In the team room Add participant is disabled with its explanation
**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Error Path
- **Given** the team room is open
- **When** the person looks at Add participant in the panel
- **Then** it is disabled and shows "Team room members are the workspace's agents - add the agent to the workspace"

#### Scenario: S-56 A squad or ad-hoc group can be deleted from the panel
**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Alternate Path
- **Given** an ad-hoc group is open
- **When** the person confirms Delete in the panel
- **Then** the group is deleted and the person is taken away from it

#### Scenario: S-57 An unpriced model call marks the cost as incomplete
**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Edge Case
- **Given** the group's allocated cost is flagged unpriced
- **When** the panel shows "Cost so far"
- **Then** it shows the amount and an "incomplete" marker

#### Scenario: S-58 The panel closes when the person leaves the group
**Traces to**: User Story 7, Acceptance Scenario 7
**Category**: Edge Case
- **Given** the panel is open
- **When** the person opens a normal chat
- **Then** the panel is closed and the Participants button is gone

#### Scenario Outline: S-134 Focus after closing the panel follows the closing action
**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** the Participants panel is open and the message box holds an unsent draft
- **When** the person closes it with `<action>`
- **Then** focus is on `<focus>` and the draft is unchanged

**Examples**:

| action | focus |
|---|---|
| the panel's close button | the message box |
| Escape | the Participants button |

### Story 8 - Starting a group and the invitation context

#### Scenario Outline: S-59 Add participant appears only in main chats
**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Alternate Path
- **Given** the person is in `<chat>`
- **When** the header's right end is shown
- **Then** the icon-only "Add participant" control is `<shown>`

**Examples**:

| chat | shown |
|---|---|
| an agent's main chat | shown |
| a helper chat | not shown |
| a task transcript | not shown |
| Omnipus's own chat | not shown |
| a group chat | not shown (the panel has its own Add participant) |

#### Scenario: S-60 Picking an agent opens a new empty group and leaves the original chat alone
**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Happy Path
- **Given** the person is in Jarvis's main chat
- **When** the person presses Add participant and picks Mia
- **Then** a new group with Jarvis and Mia opens empty, Jarvis's main chat is unchanged, and no message was posted anywhere

#### Scenario: S-61 Invitation context goes from generating to ready in the panel
**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Happy Path
- **Given** the group was just created and the summary job is running
- **When** the person opens the panel and the job completes
- **Then** "Invitation context" shows "generating" first; on the live status change to `ready` the panel refetches the group record and shows the summary text, without a page reload

#### Scenario: S-62 The invited agent knows the background and nothing is posted
**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Happy Path
- **Given** the summary is ready and Mia has not yet received a request in this group
- **When** the person sends "@Mia continue" as the first message
- **Then** Mia's first request includes the background as quoted context, Mia's answer uses it, and the group shows no entry containing the summary

#### Scenario: S-63 Provider down: group opens, first message goes out, panel says unavailable
**Traces to**: User Story 8, Acceptance Scenario 5
**Category**: Error Path
- **Given** the model provider rejects every call
- **When** the person starts a group and sends the first message at once
- **Then** the group opens, the message is delivered without delay, and the panel shows "Invitation context: unavailable"

#### Scenario Outline: S-64 The 45-second limit decides whether a result is used
**Traces to**: User Story 8, Acceptance Scenario 5
**Category**: Edge Case
- **Given** the summary job returns after `<seconds>` seconds
- **When** the result arrives
- **Then** the status becomes `<status>` and a late result is `<late>`

**Examples**:

| seconds | status | late |
|---|---|---|
| 44 | ready | not applicable |
| 45 | ready | not applicable |
| 46 | unavailable | discarded |

#### Scenario: S-65 A restart while the summary is generating shows unavailable
**Traces to**: User Story 8, Acceptance Scenario 5
**Category**: Edge Case
- **Given** the group's summary status is `generating` and the server restarts
- **When** the group is loaded again
- **Then** the status is `unavailable` and a result that later arrives is discarded

#### Scenario: S-66 Deleting the group or removing the origin agent during generation discards the result
**Traces to**: User Story 8, Acceptance Scenario 5
**Category**: Edge Case
- **Given** the summary job is running
- **When** the group is deleted (or the origin agent removed) and the job then returns
- **Then** nothing is stored and no cost line is added to a missing group

#### Scenario Outline: S-67 The summary input and output exclude recognised secrets
**Traces to**: User Story 8, Acceptance Scenario 6
**Category**: Error Path
- **Given** the origin chat contains `<content>`
- **When** the summary is made
- **Then** `<expected>`

**Examples**:

| content | expected |
|---|---|
| a registered secret value | the value is absent from the stored summary |
| a known credential pattern (for example an `sk-` style API key) | the pattern is absent from the stored summary |
| a secret-request card entry | the entry is not part of the model's input |
| a result of a secret tool | the result is not part of the model's input |

#### Scenario: S-68 The summary's cost is counted once
**Traces to**: User Story 8, Acceptance Scenario 7
**Category**: Edge Case
- **Given** the summary call cost 0.02 dollars
- **When** the panel shows "Cost so far", also after a reload and after a repeated cost-line write
- **Then** the group's cost includes 0.02 once

#### Scenario: S-69 The first message sent while the summary is generating is never held
**Traces to**: User Story 8, Acceptance Scenario 8
**Category**: Edge Case
- **Given** the summary status is `generating`
- **When** the person sends "@Mia hi" as the first message
- **Then** the message is delivered at once without the summary, and Mia never receives the summary later

#### Scenario: S-70 The summary never appears in entries, frames or mirrored chats
**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Edge Case
- **Given** the summary is ready
- **When** the group's entries, live updates and both agents' main chats are inspected
- **Then** none contains the summary text; only the group record read by the panel does

#### Scenario: S-122 A disconnect while the summary is generating still ends with the text in the open panel
**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Edge Case
- **Given** the panel is open showing "generating", the connection drops, the summary becomes ready meanwhile, and the connection returns
- **When** the group re-attaches
- **Then** the panel refetches the group record and shows the summary text, without a page reload

#### Scenario: S-123 A failed summary fetch shows an error with Retry
**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Error Path
- **Given** the status is `ready` and fetching the group record fails
- **When** the panel tries to show the summary
- **Then** it shows an error line with Retry, and Retry fetches again and shows the text on success

#### Scenario: S-131 A failed receiver write does not lose the summary on retry of the same request
**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Edge Case
- **Given** the summary is `ready`, Mia's first request R1 reserved it, and writing Mia's request capture failed
- **When** R1 is retried
- **Then** R1 carries the summary exactly once and Mia receives it once; if R1 ends cancelled without a capture, the reservation is released

#### Scenario: S-132 Two first requests to the same member at once: exactly one carries the summary
**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Edge Case
- **Given** the summary is `ready` and two requests R1 and R2 for Mia are admitted concurrently
- **When** both are built
- **Then** exactly one of them carries the summary and the other is recorded as skipped, with no duplicate and no loss, also across a restart

#### Scenario: S-138 Failure, a second request, then a retry still delivers the summary once
**Traces to**: User Story 8, Acceptance Scenario 9
**Category**: Edge Case
- **Given** R1 reserved the summary for Mia and its capture write failed (R1 became unavailable), then R2 arrived for Mia
- **When** R1 is retried
- **Then** R1 carries the summary once, R2 went without it and never gets it later, and Mia received the summary exactly once

#### Scenario Outline: S-139 Reservation transitions
**Traces to**: User Story 8, Acceptance Scenario 9
**Category**: Edge Case
- **Given** R1 reserved the summary for Mia and `<event>`
- **When** the next request R3 for Mia is admitted later
- **Then** the reservation is `<state>` and R3 `<r3>`

**Examples**:

| event | state | r3 |
|---|---|---|
| the receiver write failed (unavailable, unknown or storage error), no capture exists | kept for R1 | goes without |
| the capture is stored but the acknowledgement to the room failed | delivered (receiver facts reconciled) | goes without |
| the receiver's facts could not be read | kept for R1 (a read error is not proof of absence) | goes without |
| R1 was cancelled and the receiver's facts show no capture | released | may carry it |
| cancellation raced the commit and the capture exists | delivered | goes without |
| R2, skipped while R1 held the reservation, and R1 was then cancelled | released; R2 stays without it | may carry it |

### Story 9 - Approvals and questions

#### Scenario: S-71 A tool approval for this group's work shows in the group and in the agent's chat
**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path
- **Given** Jarvis's group work needs approval to run a tool
- **When** the card is raised
- **Then** the card is shown in the group labelled "for group Launch" and in Jarvis's own chat

#### Scenario: S-72 Approving in the group resolves the agent's chat without a reload
**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Happy Path
- **Given** the card is shown in the group and in Jarvis's chat
- **When** the person approves it in the group
- **Then** both views show it resolved and the tool runs once

#### Scenario: S-73 An unrelated approval stays out of the group
**Traces to**: User Story 9, Acceptance Scenario 3
**Category**: Edge Case
- **Given** Jarvis has an approval unrelated to the group in its own chat
- **When** the group is open
- **Then** no card for that approval appears in the group

#### Scenario: S-74 A question card from group work shows in both places and answering updates both
**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Happy Path
- **Given** Mia asked a question during group work
- **When** the person answers it in the group
- **Then** the card is answered in both the group and Mia's chat, once

#### Scenario: S-75 A pending question survives a reload of the group
**Traces to**: User Story 9, Acceptance Scenario 5
**Category**: Edge Case
- **Given** a group question card is pending
- **When** the person reloads the page and reopens the group
- **Then** the card is shown in the group and in Mia's chat

#### Scenario: S-76 After a group question is answered, the resumed work's next approval appears in the group
**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** the person answered Mia's group question and the work resumes
- **When** the resumed work needs a tool approval
- **Then** that card shows in the group and in Mia's chat

#### Scenario: S-77 A helper's approval stays in the agent's chat and the group shows the attention avatar
**Traces to**: User Story 9, Acceptance Scenario 6
**Category**: Happy Path
- **Given** a group request made Jarvis start a helper that needs an approval
- **When** the card is raised
- **Then** the card shows only in Jarvis's own chat, Jarvis's avatar in the group shows the attention animation with "Open Jarvis's main chat", and after the person approves there the animation stops

#### Scenario: S-78 A secret request during group work shows no card in the group
**Traces to**: User Story 9, Acceptance Scenario 7
**Category**: Error Path
- **Given** group work makes Mia request a secret
- **When** the request is raised
- **Then** no secret card appears in the group and a link to Mia's own chat is shown

#### Scenario Outline: S-79 A card from a step that also handles other input stays in the agent's chat
**Traces to**: User Story 9, Acceptance Scenario 8
**Category**: Edge Case
- **Given** Jarvis's running step consumed this group's request and also `<other>`
- **When** an approval is raised
- **Then** the card appears in Jarvis's own chat only, and not in the group

**Examples**:

| other |
|---|
| a message typed in Jarvis's own chat |
| a messaging-channel message |
| a task |

#### Scenario: S-80 Approve and deny at the same moment produce exactly one decision
**Traces to**: User Story 9, Acceptance Scenario 9
**Category**: Edge Case
- **Given** the same card is open in the group in browser A and in the agent's chat in browser B
- **When** A approves and B denies at the same moment
- **Then** exactly one decision takes effect and both browsers show the same final state

#### Scenario: S-81 Approval frames reach a browser that is viewing a group page
**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Edge Case
- **Given** the person's browser is on the group page with no chat attached
- **When** an approval for this group's work is raised
- **Then** the card is shown in the group within the normal live-update time

#### Scenario Outline: S-82 A card flips only its own request's state
**Traces to**: User Story 9, Acceptance Scenario 6
**Category**: Edge Case
- **Given** Jarvis has two working requests R1 (this group) and R2 (another group) and a card `<card>`
- **When** the card is pending
- **Then** `<flipped>` shows "waiting for you"

**Examples**:

| card | flipped |
|---|---|
| carries this group's reference for R1 | only R1 |
| carries no group reference (helper or unrelated) | both R1 and R2 (existing per-chat behaviour) |

#### Scenario Outline: S-83 A card appears in the group only for work that answers the group
**Traces to**: User Story 9, Acceptance Scenario 8
**Category**: Edge Case
- **Given** a step that consumed `<inputs>`
- **When** it raises a tool approval or a question
- **Then** the card appears `<where>`

**Examples**:

| inputs | where |
|---|---|
| this group's request only | group and agent's chat |
| this group's request and a message typed in the agent's own chat | agent's chat only (D23) |
| this group's request and a channel message | agent's chat only (D23) |
| this group's request and a task | agent's chat only (D23) |
| this group's request and another agent's message | agent's chat only (D23: other input) |
| this group's request and another group's request | agent's chat only; both groups show the attention animation and link (D28) |
| a web message only | agent's chat only |

#### Scenario: S-151 A step that answers two groups keeps its card in the agent's chat and lights up both groups
**Traces to**: User Story 9, Acceptance Scenario 10
**Category**: Edge Case
- **Given** Jarvis's running step consumed request R1 of group "Launch" and request R2 of group "Ops"
- **When** the step raises a tool approval (or a question)
- **Then** the card appears only in Jarvis's own chat; neither group shows a card; both "Launch" and "Ops" show Jarvis's attention animation with "Open Jarvis's main chat"; once the card is resolved in Jarvis's chat, the animation stops in both groups

### Story 11 - Header

#### Scenario: S-98 The group puts the Participants button in the header slot with no bar or border
**Traces to**: User Story 11, Acceptance Scenario 1
**Category**: Happy Path
- **Given** the group is open
- **When** the header line is inspected
- **Then** the left side (workspace name and toggles) is unchanged, the right end holds the Participants button, and no bar, border or surface fill is present

#### Scenario: S-99 Normal and helper chat headers keep their minimal form
**Traces to**: User Story 11, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** a normal chat, then a helper chat of Jarvis called "Research helper"
- **When** the header lines are inspected
- **Then** the normal chat shows only "Add participant"; the helper chat shows the muted breadcrumb "Jarvis / Research helper" with Jarvis as a link; neither shows a bar or border

#### Scenario: S-100 The Participants button has a tooltip, keyboard operation and a touch target
**Traces to**: User Story 11, Acceptance Scenario 3
**Category**: Happy Path
- **Given** the group is open
- **When** the button receives keyboard focus and then a coarse pointer is used
- **Then** a tooltip names it and its target is at least the design system's touch minimum

#### Scenario: S-101 "Open browser" is not shown on a group page
**Traces to**: User Story 11, Acceptance Scenario 4
**Category**: Edge Case
- **Given** the person's last visited chat supports Open browser
- **When** the group page is open
- **Then** the Open browser control is not shown

#### Scenario: S-102 The group's name is announced as the chat's name
**Traces to**: User Story 11, Acceptance Scenario 5
**Category**: Happy Path
- **Given** a screen reader is active
- **When** the group is opened
- **Then** the group's name "Launch" is available as the chat's status text

#### Scenario: S-103 The widest cluster fits the header line at every width mode
**Traces to**: User Story 11, Acceptance Scenario 6
**Category**: Edge Case
- **Given** a group with 10 members (stack plus "10")
- **When** the window passes through the strip's full, icons and narrow modes
- **Then** nothing overlaps and the workspace strip keeps its own modes

### Story 12 - Safety

#### Scenario: S-104 Literal HTML in an agent answer is shown as text
**Traces to**: User Story 12, Acceptance Scenario 1
**Category**: Error Path
- **Given** an answer contains `<script>alert(1)</script>`
- **When** it is shown
- **Then** the text `<script>alert(1)</script>` appears literally and nothing executes

#### Scenario Outline: S-105 Unsafe link schemes are struck through
**Traces to**: User Story 12, Acceptance Scenario 2
**Category**: Error Path
- **Given** an answer contains a link with address `<href>`
- **When** it is shown
- **Then** the link is `<result>`

**Examples**:

| href | result |
|---|---|
| javascript:alert(1) | struck through, not clickable |
| data:text/html,x | struck through, not clickable |
| https://example.com | clickable, opens in a new tab without referrer access |

#### Scenario: S-106 A remote image address in an agent answer is not loaded
**Traces to**: User Story 12, Acceptance Scenario 3
**Category**: Error Path
- **Given** an answer contains an image pointing at `https://tracker.example/p.png`
- **When** it is shown
- **Then** no network contact reaches that address: in a real browser against the embedded SPA, with a controlled remote endpoint, a positive control (an allowed request to the same instrument is observed) and the page's policy violation or failure recorded for the blocked image

#### Scenario: S-107 An agent's "/pause" text is only text
**Traces to**: User Story 12, Acceptance Scenario 4
**Category**: Error Path
- **Given** Jarvis's answer text is "/pause"
- **When** it is shown
- **Then** it is visible text and the group is not paused

### Story 13 - Isolation

#### Scenario: S-108 With no chat open anywhere, a group message posts only to the group
**Traces to**: User Story 13, Acceptance Scenario 1
**Category**: Happy Path
- **Given** no chat session is active in this tab
- **When** the person sends "@Jarvis hi" in the group
- **Then** the message is posted to the group and no chat session is created

#### Scenario: S-109 An unrelated streaming chat neither blocks nor receives the group message
**Traces to**: User Story 13, Acceptance Scenario 2
**Category**: Edge Case
- **Given** Petra's normal chat was streaming when the person switched to the group
- **When** the person sends a group message
- **Then** the message box is enabled, the message goes to the group only, and Petra's chat receives nothing

#### Scenario: S-110 Escape in the group does not stop another chat
**Traces to**: User Story 13, Acceptance Scenario 3
**Category**: Edge Case
- **Given** Petra's chat is streaming in the background
- **When** the person presses Escape in the group
- **Then** Petra's chat keeps streaming

#### Scenario: S-111 An unrelated open question elsewhere does not lock the group box
**Traces to**: User Story 13, Acceptance Scenario 4
**Category**: Edge Case
- **Given** Petra's chat has an unanswered question card
- **When** the group is open
- **Then** the group's message box is enabled

### Story 14 - Files outlive the group

#### Scenario: S-112 Deleting a group keeps its files
**Traces to**: User Story 14, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a group with an image also mirrored into Jarvis's main chat
- **When** the group is deleted
- **Then** the image still opens from Jarvis's main chat and the Library

#### Scenario: S-113 A group file whose chat copy is gone shows the broken state
**Traces to**: User Story 14, Acceptance Scenario 3
**Category**: Error Path
- **Given** a group entry's image whose chat media copy no longer exists
- **When** the group shows the entry
- **Then** the normal chat's broken-image state with Retry is shown

#### Scenario: S-136 One Library deletion removes both copies of a group file (D26)
**Traces to**: User Story 14, Acceptance Scenario 2
**Category**: Happy Path
- **Given** a group image "shot.png" listed in the Library panel, shown in the group and mirrored into Jarvis's main chat
- **When** the person deletes it in the Library panel and confirms
- **Then** the Library no longer lists it, its chat media copy no longer exists, the group entry and the mirrored entry show the file as unavailable (broken-file state; Retry keeps failing), and no repair, retention or recovery step recreates either copy, also after a restart and an orphan-cleanup run

#### Scenario: S-145 (retired by D26)

The founder chose Option A (D26); the Option B scenario is removed and its id is not reused.

#### Scenario: S-146 A normal chat upload deleted in the Library shows as unavailable in the normal chat
**Traces to**: User Story 14, Acceptance Scenario 5
**Category**: Happy Path
- **Given** the person uploaded "notes.pdf" in Jarvis's main chat (no group involved)
- **When** the person deletes "notes.pdf" in the Library panel and confirms
- **Then** both copies are gone and Jarvis's chat shows the file card in its unavailable state with Retry, exactly as a group entry does

#### Scenario: S-147 The delete confirmation names the chat copy
**Traces to**: User Story 14, Acceptance Scenario 6
**Category**: Happy Path
- **Given** two Library files: "shot.png" with a chat copy and "draft.md" without one
- **When** the person chooses Delete on each
- **Then** the confirmation for "shot.png" says that the copy used in chats is deleted too and chats will show the file as unavailable; the confirmation for "draft.md" is today's dialog with no mention of chats

#### Scenario: S-148 A deletion that fails part-way keeps the file listed and can be repeated
**Traces to**: User Story 14, Acceptance Scenario 6
**Category**: Error Path
- **Given** "shot.png" has a chat copy and removing its Library file fails after the chat copy was removed
- **When** the person confirms Delete
- **Then** the server answers 500 "could not delete shot.png completely", the panel shows that error, "shot.png" stays listed, and a repeated Delete answers 204 and removes it (the missing chat copy counts as already removed)

#### Scenario Outline: S-149 The association follows the file
**Traces to**: User Story 14, Acceptance Scenario 2
**Category**: Edge Case
- **Given** the group image "shot.png" (chat copy C) and the change `<change>`
- **When** the person deletes `<target>` in the Library panel
- **Then** `<outcome>`

**Examples**:

| change | target | outcome |
|---|---|---|
| renamed to "final.png" | "final.png" | both "final.png" and C are removed |
| moved to "designs/shot.png" in the same workspace | "designs/shot.png" | both removed |
| copied to "copy.png" | "copy.png" | only "copy.png" is removed; "shot.png" and C stay |
| none | the `.library` folder | every file inside and every associated chat copy are removed |
| moved to another workspace | the moved file | only the moved file is removed; C stays in the original workspace (association dropped by the move) |
| uploaded before this feature, exactly one media entry with the same SHA-256 and name | "shot.png" | both removed (one-time repair matched them, A-14) |
| uploaded before this feature, two media entries with the same SHA-256 and name | "shot.png" | only the Library file is removed; the dialog does not mention chats |

#### Scenario: S-124 A paused group's attachment survives the orphan cleanup
**Traces to**: User Story 14, Acceptance Scenario 4
**Category**: Edge Case
- **Given** the group is paused, the person posted a message with an image, and a controlled clock moves 31 days ahead
- **When** the real orphan cleanup runs
- **Then** the image still opens from the group

#### Scenario: S-125 A held request's attachment survives the orphan cleanup and a restart
**Traces to**: User Story 14, Acceptance Scenario 4
**Category**: Edge Case
- **Given** a request with an image is held for a stopped agent, the server restarts, and a controlled clock moves 31 days ahead
- **When** the real orphan cleanup runs and the agent is started
- **Then** the agent still receives the image

#### Scenario: S-126 A deleted group's files survive the orphan cleanup
**Traces to**: User Story 14, Acceptance Scenario 4
**Category**: Edge Case
- **Given** a group with an image was deleted and a controlled clock moves 31 days ahead
- **When** the real orphan cleanup runs
- **Then** the original media URL still opens (fixture: no consuming session owns the file, so only the group's retention owner can protect it); the same file also opens from the agent's main chat in a second fixture; an upload that was never posted (S-28) has its chat media copy collected by the same run while its Library file stays listed

### Story 15 - Preserved behaviour

#### Scenario: S-114 The sidebar's group section keeps listing and opening groups
**Traces to**: User Story 15, Acceptance Scenario 1
**Category**: Happy Path
- **Given** Acme has the team room and one ad-hoc group
- **When** the sidebar is shown and the person selects the ad-hoc group
- **Then** both are listed under Acme and the group opens

#### Scenario: S-115 /pause and /resume behave as documented today
**Traces to**: User Story 15, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** the person sent `/pause`, then a message, then `/resume`
- **When** the message is inspected
- **Then** it is saved in the group, was not sent to agents while paused, and is not sent by `/resume`

#### Scenario: S-116 Group updates never create or replace a normal chat's transcript
**Traces to**: User Story 15, Acceptance Scenario 3
**Category**: Edge Case
- **Given** Jarvis's normal chat is cached with 10 messages
- **When** group updates for "Launch" arrive
- **Then** Jarvis's cached chat still has the same 10 messages

#### Scenario: S-117 The existing chat history and composer suites pass unchanged on the shared stack
**Traces to**: User Story 15, Acceptance Scenario 4
**Category**: Happy Path
- **Given** the shared stack serves a normal chat
- **When** the existing chat history and composer tests run with no test edits
- **Then** all pass

---
## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit (Go) | `pkg/rooms`, `pkg/addressing`, `pkg/agent`, `pkg/tools`, `pkg/askuser`, `pkg/gateway` handlers | media validation and ordering, invitation state machine, provenance, status flip |
| Unit (vitest) | room source projection, composer seam, profile, slash/mention, working lines, participants panel, header slot | shared-stack behaviour with a room fixture |
| Integration | real hub and room service with a stub model; two-browser approval race | frames reach a group page, one-decision rule, mirror and repair |
| Neutrality gate | the entire existing chat/history/composer suites | proves the shared stack is unchanged for sessions (FE-1 and FE-0 gates) |
| E2E (Playwright) | real server, stub model | paging, rich content, attachments, approvals, start-a-group |
| Hands-on UAT | exact candidate, tester + independent validator | rows below |

Execution (not only writing): RED packs QA-R1 to QA-R4 are shown failing on a tests-only commit in CI; GREEN by the implementing leads; CHECK by a second instance of qa-lead (test integrity) and a second instance of each lead role; Go results come from CI (root `CLAUDE.md`: never run the full Go suite locally); frontend runs are targeted `npx vitest run <file>` through the slot runner. Every new vitest file must match a CI group pattern (`scripts/check-vitest-coverage.mjs`, T-53).

### Hands-on UAT rows (written at spec time)

Procedure and independence rules: root `CLAUDE.md` ("Hands-on pipeline"), `.claude/skills/omnipus-planning-orchestration/knowledge/landing-trains.md`. Rows UAT-1 to UAT-28 are carried from the ADR (§10 and §11.6; UAT-11 is replaced by UAT-11b). **UAT-8, UAT-9 and UAT-22 (the Stop rows) are removed by D21; ids are not reused.** UAT-29 to UAT-33 are added by this spec; UAT-34 to UAT-36 apply the founder decisions D26 to D28. Provider/model for any agent-driven row: `openrouter` + `deepseek/deepseek-v4.1-flash` (root `CLAUDE.md`). **Rows marked AE touch the agent engine (approvals, questions, requests) and require exact-commit hands-on UAT by `uat-tester` plus independent `uat-validator` before landing.**

| Row | Acceptance criterion | Steps | Expected result | Affected area | Validator critical path | Automation candidate |
|---|---|---|---|---|---|---|
| UAT-1 | US-1 AS 1.1, 1.2; S-01..S-03 | Open a group; an agent answers with a table, a Mermaid block, a formula and a code block | All render as in a normal chat; code has Copy; Mermaid draws | display | Re-drive with a fresh agent answer; compare with the same content in a normal chat | Stable: e2e T-48 |
| UAT-2 | US-3 AS 3.1; S-20 | Attach a screenshot in the group composer and send to @Jarvis | The image shows in your message with the lightbox; Jarvis's main chat shows the request with the image; Jarvis can describe it | files-in, composer | Open Jarvis's main chat independently and check the image and Jarvis's description | Stable: e2e T-48 (stub model) |
| UAT-3 | US-4 AS 4.1; S-29 | Ask an agent in the group to send back a file or diagram image | It appears in the agent's group answer with download/lightbox; the agent's main chat shows the same answer with the file | files-out, mirror | Check both places and the file download | Not yet: needs a real model |
| UAT-4 | US-2 AS 2.1, 2.2; S-10 | Compare the group composer with a normal one | Same card and attach button; no model picker, Auto, token counter or skills; no Stop button | composer | Side-by-side screenshot of both | Stable: T-27 |
| UAT-5 | US-2 AS 2.5; S-14, S-15 | Type "@" in the group | `@all` and the current members (muted included); a picked member stays addressed after a rename; deleting the token removes only that explicit addressing and an ordinary note then goes to every member that is not muted | composer | Rename a member and send; delete the token, send an ordinary note, check who receives it | Stable: T-28 |
| UAT-6 | US-2 AS 2.4; S-12, S-115 | Type "/" in the group | Only the six group commands; `/pause` then a message: not delivered; `/resume` resumes | composer, commands | Check the paused message is saved but absent from the agent's chat | Stable: T-28, T-46 |
| UAT-7 | US-5 AS 5.1, 5.2; S-35, S-07 | Address two agents; watch | One "<Agent> is working..." line per agent, then each finished answer under its author's name and avatar | live view | Watch both lines until both answers; confirm no partial text | Partly: T-33; timing by hand |
| UAT-10 (AE) | US-9 AS 9.1, 9.2; S-71, S-72 | Ask an agent in the group to run a tool that needs approval | The card shows in the group and in the agent's chat; approve in the group; the agent's chat shows it resolved without reload | approvals | Open the agent's chat in a second browser; confirm no reload is needed | Stable: e2e T-49 (stub model) |
| UAT-11b (AE) | US-9 AS 9.6; S-77 (replaces UAT-11, D15) | A group request makes the agent start a helper that needs an approval | The card appears only in the agent's main chat; in the group the agent's avatar shows the attention animation with "Open <Agent>'s main chat"; after approving there, the animation stops | approvals, helper, live view | Confirm the card is absent from the group; follow the link; approve; confirm the animation stops | Not yet: needs a real helper |
| UAT-12 (AE) | US-9 AS 9.3; S-73 | The agent has an unrelated approval in its own chat | No card in the group; the member line says "waiting for you in <Agent>'s main chat" only if that agent's group work is blocked | approvals | Confirm no card in the group; the attention animation may light up for unrelated waiting (accepted, D22) | Not yet |
| UAT-13 (AE) | US-9 AS 9.4; S-74 | An agent asks a question (question card) during group work | The question card shows in the group; answering it there updates the agent's chat | questions | Answer in the group; check the agent's chat shows it answered | Not yet |
| UAT-14 (AE) | US-9 AS 9.7; S-78 | A secret is requested during group work | No secret card in the group; a link to the agent's chat | secrets | Confirm no secret card or value in the group | Not yet |
| UAT-15 | US-8 AS 8.2, 8.3; S-60, S-61 | In Jarvis's main chat press Add participant (top right), pick Mia | A new empty group opens; Jarvis's chat unchanged; the panel shows Invitation context (generating then ready, without a reload); recognised registered keys and key-shaped strings are absent (a password written in plain prose is not part of this row: accepted gap #1467) | start group, invitation | Verify Jarvis's chat has no new message; read the summary | Stable: e2e T-50 (stub model) |
| UAT-16 | US-8 AS 8.4; S-62, S-70 | Send the first group message to @Mia | Mia's answer shows it knew the background; nothing about the summary was posted in the group | invitation delivery | Inspect the group and both chats for the summary text | Partly: T-20 |
| UAT-17 | US-7 AS 7.3; S-52, S-68 | Open the Participants panel | Status per member, open-chat links, mute/remove menu, Add participant, cost so far (including the summary cost), invitation context; the chat stays centred | panel | Operate each control once | Stable: T-36 |
| UAT-18 | US-6 AS 6.2-6.4; S-40..S-42 | Scroll far up in a long group | Earlier messages page in without jumps; Return to latest appears and works; reopening the group shows the loading cover, then lands at the newest message | history | Use a real group of 10,005 entries (above the 10,000 snapshot cap); scroll, observe the earlier-page request, return, reopen | Stable: e2e T-47 |
| UAT-19 | US-11 AS 11.1-11.3; S-98..S-100 | Look at a normal chat, a helper chat and a group chat header | No bar, no borders. Left side unchanged. Right end: Add participant icon / "Jarvis / Research helper" (Jarvis links back) / avatar stack + count. Tooltips on hover; usable by keyboard and touch | header | Tab through the header; use touch emulation | Stable: T-37 |
| UAT-20 | US-12; S-105, S-106, S-107 | An agent posts `/pause`, a link to `javascript:` and an image from a remote address in the group | It shows as text; nothing pauses; the link is struck through and not clickable; the remote image does not load and nothing reaches the remote address | safety | Click the struck link; confirm the group is not paused; watch the remote endpoint's log | Stable: T-32, T-56 |
| UAT-21 | US-6 AS 6.8; S-49 | Narrow window (phone width) on a group | The header strip collapses as today; the Participants button stays reachable | header, layout | Emulate 375 px and open the panel | Stable: T-35, T-38 |
| UAT-23 (AE) | US-9 AS 9.9; S-80 | Approve in the group and deny in the agent's chat at the same moment (two browsers) | Exactly one decision takes effect; both views show the same final state | approvals race | Two browsers, simultaneous clicks, compare final state | Partly: T-13 |
| UAT-24 (AE) | US-9 AS 9.5; S-75 | Reload a group while a question card from group work is pending | The card is still shown in the group and in the agent's chat | questions, reload | Reload both places | Partly: T-15 |
| UAT-25 | US-8 AS 8.5; S-63 | Start a group while the model provider is down | The group opens; the first message goes out at once; the panel shows "Invitation context: unavailable" | invitation failure | Block the provider; time the first message | Stable: T-18/T-50 (stub) |
| UAT-26 | US-14 AS 14.1; S-112 | Delete a group that had an image | The image still opens from the agent's main chat (and from the Library) | retention | Open the image from both places after deletion | Stable: T-09 |
| UAT-27 (AE) | US-13; S-108..S-111 | Open a group right after a chat that is streaming or has an open question | The group's message box works; the other chat is not stopped or sent to; Escape in the group does not stop the other chat | isolation | Stream in chat A, switch to the group, send, press Escape, return to A | Stable: T-26 |
| UAT-28 | US-3 AS 3.5; S-25 | Attach to a group message a file reference from another workspace (API) | Refused; nothing is posted | media security | Replay the API call with a reference of workspace B into a group of workspace A | Stable: T-03 |
| UAT-29 (added by this spec) | US-3 AS 3.2; S-21 | Attach only an image (no text) and send | The message is accepted and shown with the image; the addressed agent receives it | files-in | Check the entry and the agent's chat | Stable: e2e T-48 |
| UAT-30 (added by this spec) | US-8 AS 8.6; S-67 | Put a registered secret value and a key-shaped string in the origin chat, start a group | The summary in the panel contains neither; (a password written in plain prose is not part of this row: accepted gap #1467) | invitation security | Read the stored summary text in the panel | Partly: T-19 |
| UAT-31 (added by this spec) | US-2 AS 2.3; S-118 | Paste a message of 70,000 ASCII characters in the group box and send | It is refused with a visible message that names the size and the 65,536-byte limit; nothing is posted; a message of 65,536 ASCII characters is accepted | message size | Try both sizes; compare the error text with the limit | Stable: T-54 |
| UAT-32 (AE, added by this spec) | US-9 AS 9.8; S-79 | Address an agent in the group and, while it works, send it a Telegram message (or a message in its own chat); then make the work need a tool approval | The card is shown only in the agent's own chat, not in the group | approvals, origin rule | Confirm the group shows no card; approve in the agent's chat | Not yet: needs a channel |
| UAT-33 (added by this spec) | US-1 AS 1.3, US-3 AS 3.1, US-4 AS 4.2; S-127, S-128, S-129 | Send a PDF in the group; ask an agent to return a file; open the Library panel; download the PDF from the group with the keyboard | Both files are listed in the Library and download; the group downloads the PDF with Enter | files, Library | Open the Library in a second browser; download both | Stable: T-60, T-61 |
| UAT-34 (D26) | US-14 AS 14.2, 14.5, 14.6; S-136, S-146, S-147 | Send an image in a group to @Jarvis; upload a PDF in Jarvis's main chat; open the Library panel and delete both files, reading each confirmation | Each confirmation says the copy used in chats is deleted too; after confirming, neither file is listed; the group entry, Jarvis's mirrored entry and the normal-chat PDF all show the file as unavailable; a file without a chat copy shows today's dialog | Library deletion | In a second browser, reload the group and Jarvis's chat and fetch both original media URLs (expect not found); restart the server and confirm nothing reappears | Stable: T-60, T-64 |
| UAT-35 (D27) | US-2 AS 2.3, US-4 AS 4.6; S-150 | Set `session_messaging.steer_body` to 1,000 and send a group message of 1,001 characters; then set it to 131,072 and send 70,000 characters | First: refused naming the 1,000-byte limit; then refused naming the 65,536-byte limit; a 1,000-character message is accepted at the first setting; nothing refused is posted | message size, operator setting | Change the setting, send both sizes, compare each error text with the limit in force | Stable: T-65 |
| UAT-36 (AE, D28) | US-9 AS 9.10; S-151 | Address Jarvis in two groups at once so one step answers both requests, with work that needs a tool approval | The card is shown only in Jarvis's own chat; both groups show Jarvis's attention animation with "Open Jarvis's main chat"; after approving there, both animations stop | approvals, origin rule, live view | Open both groups in a second browser; confirm no card in either; follow one link; approve; confirm both animations stop | Not yet: needs a real model that batches both requests |

### Test Implementation Order

Names are placeholders; qa-lead names the real tests in RED. Order follows the ADR's corrected order (BE-0, QA-R1, FE-0, FE-C, FE-1, QA-R3, BE-1..BE-4, QA-R4, FE-2, FE-3, QA-M).

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| T-24 (QA-R1) | `session_source_neutrality_gate` | Neutrality | S-117 | Whole existing chat/history suite green with no edits once the session source exists (FE-1 gate) |
| T-25 (QA-R1) | `composer_seam_neutrality` | Neutrality | S-117 | Existing composer tests green after extraction and the composer-conversation seam (FE-0 gate) |
| T-26 (QA-R1) | `room_composer_isolation` | Unit + spike | S-108, S-109, S-110, S-111, S-23 | Real composer on a group page: no active session; unrelated streaming, replaying, question-blocked session; assert no send/create/cancel/navigation and Escape scoped. Decides nested runtime vs transport seam |
| T-27 | `composer_profile_controls` | Unit | S-10, S-19 | Controls present/absent (no Stop), placeholder |
| T-28 | `composer_slash_and_mentions` | Unit | S-12, S-13, S-14, S-15, S-16 | Moved from the old room composer tests; S-15 asserts the empty explicit mentions list and, separately, the server's resulting recipients |
| T-29 | `composer_blocked_and_empty` | Unit | S-17, S-18, S-118 | Blocked reasons; nothing to send; over-limit refusal shown in the notice area |
| T-37 | `chat_header_slot_group` | Unit | S-98, S-99, S-100, S-101, S-102 | Group control plugs into the slot; normal/helper unchanged; Open browser hidden; sr-only name |
| T-38 | `workspace_tab_bar_fit_widest_cluster` | Unit | S-103 | Avatar stack + count at all strip modes |
| T-39 | `avatar_stack_catalogue_contract` | Unit | (FR-067) | Four-part contract; `ActivityBar` uses it |
| T-51 | `contracts_verify_and_schema_limits` | CI gate | (SC-003) | `make verify-contracts`; schema limit tests for new fields |
| T-01 (QA-R3) | `contract_limits_attachments_media` | Unit (Go) | S-24, S-21 | 16/17 limits on entry and request |
| T-02 | `post_message_nothing_to_send` | Unit (Go) | S-18, S-21 | Blank/whitespace/media-only rule with inbound validation on and off |
| T-03 | `media_ref_resolution_matrix` | Unit (Go) | S-21, S-25, S-26, S-135 | Room ingestion: the same valid reference listed twice is accepted as listed with retention counted once (TD-4); workspace ok; non-workspace; another workspace; missing; stranded; whole message refused (422). Resolver level: another workspace's reference refused with an independently supplied caller workspace |
| T-04 | `upload_workspace_branch` | Unit (Go) | S-22, S-23, S-28, S-128 | `workspace_id` without `session_id`; 503 when the library is unavailable; no session created; parity with the existing normal-chat upload for any other input (no new authorization); both representations written |
| T-05 | `request_capture_carries_media` | Unit (Go) | S-27, S-20 | Initial admission, rewake and held delivery all hand the model the same files |
| T-06 | `answer_request_media` | Unit (Go) | S-29, S-30, S-31, S-34 | Workspace ref; own-chat copy; copy/check failure; other workspace; the same file listed twice (TD-4); 16 files accepted, 17 files and a file over 100 MB rejected definitively before acceptance (TD-3) |
| T-07 | `answer_identity_and_repair` | Unit (Go) | S-32, S-133, S-137 | Retry with identical original file arguments (not pre-normalized stored ids); equal repeat while incomplete completes the first; equal repeat after full completion is a new answer; text-only behaviour unchanged |
| T-08 | `mirror_attachments_to_main_chat` | Integration | S-20, S-29 | Request and answer mirrored with attachments; main-chat group-traffic entry expands to show them |
| T-09 | `group_deletion_keeps_library_files` | Integration | S-112 | Journal directory removed; library entries remain |
| T-13 | `approval_single_decision` | Unit (Go) | S-80 | Approve and deny at the same moment: one transition wins |
| T-15 | `question_provenance_durable` | Unit (Go) | S-74, S-75, S-76 | Origin captured at creation; present in live card and reconnect snapshot; resume carries it |
| T-16 | `card_origin_only_for_group_work` | Unit (Go) | S-77, S-79, S-83, S-151 | Room origin only when the step answers only one group (D23); any other input, including a second group's request (D28), keeps the card in the agent's chat with no group reference; the helper-has-no-reference test unchanged |
| T-17 | `card_flips_only_own_request` | Unit (Go) | S-82, S-151 | With and without a reference; a two-group step's card (no reference) flips both groups' requests to waiting (D28) |
| T-18 | `invitation_state_machine` | Unit (Go) | S-61, S-63, S-64, S-65, S-66 | generating to ready/unavailable; 44/45/46 s; restart; deletion; late discard; output over 4,096 characters cut at a word boundary or hard-cut, still `ready` (TD-1); zero usable entries gives `unavailable` with no model call (TD-2) |
| T-19 | `invitation_filters_and_exclusions` | Unit (Go) | S-67 | Registered value and pattern removed; secret entries/tool results excluded; prose password NOT asserted (#1467) |
| T-20 | `invitation_delivery_rules` | Unit (Go) | S-62, S-69, S-131, S-132, S-138, S-139 | First request only; skipped when not ready; never held; origin never receives; reservation transitions; failed write then retry; composed R1 fails / R2 arrives / R1 retries; stored capture with failed acknowledgement; facts-read error; cancellation racing the commit; concurrent first requests; restart |
| T-21 | `invitation_cost_once` | Unit (Go) | S-68 | One cost line; unpriced flag; dedupe; no cost line when no model call was made (TD-2) |
| T-22 | `invitation_invisible_and_group_starts_empty` | Integration | S-60, S-70 | Amended add-participant test: record exists, no entry, origin unchanged; summary absent from entries/frames/mirror |
| T-23 | `approval_frames_reach_group_page` | Integration (real hub) | S-81 | Cards for this group reach a client with no chat attached |
| T-30 (QA-R4) | `room_source_projection` | Unit | S-07, S-08, S-09, S-116 | Row identity stable under delivery frames; no foreign tool calls; after-pause mark; no session bucket touched |
| T-31 | `shared_rendering_rich_content` | Unit | S-01, S-02, S-03, S-04, S-05, S-06, S-113 | Markdown, code copy, diagram, math, images, files, broken state through the shared pipeline |
| T-32 | `agent_text_is_display_only` | Unit | S-104, S-105, S-107 | Raw HTML, script/data links, "/pause" text (the remote-image guarantee is T-56) |
| T-33 | `working_lines_and_attention` | Unit | S-35, S-36, S-37, S-38, S-151 | One line per member; replaced by answers; attention + link; no partial text; terminal labels; attention in each group whose request waits on a card held in the agent's chat (D28) |
| T-34 | `room_history_oracles` | Unit | S-40, S-41, S-42, S-43, S-44, S-45, S-46, S-47, S-48 | Assertion map of the ADR (C-I9): retained, amended and re-expressed |
| T-35 | `layout_widths_and_narrow` | Unit | S-39, S-49 | Column widths with/without panel; phone width |
| T-36 | `participants_button_and_panel` | Unit | S-50, S-51, S-52, S-53, S-54, S-55, S-56, S-57, S-58, S-134 | Stack, toggle, content, Ask again, menu, team-room disable, delete, unpriced cost, close on leave, focus per close action with the real trigger registration and a preserved draft |
| T-40 | `group_cards_filtering_and_answer_once` | Unit | S-71, S-72, S-73, S-74, S-77, S-78, S-79, S-151 | Filter by group reference; helper/unrelated/secret absent; one decision |
| T-42 | `room_store_additions` | Unit | S-44, S-45, S-48 | `limit`, attempt counter, view mode, view generation; stale-response fencing retained |
| T-43 | `room_attachment_adapter` | Unit | S-20, S-22, S-23, S-24 | Upload with workspace and no session; chip errors; 16/17 |
| T-44 | `add_participant_gating_and_new_group` | Unit | S-59, S-60 | Main chats only; opens a new group |
| T-45 | `sidebar_group_section` | Unit | S-114 | Unchanged listing and open |
| T-46 | `pause_resume_mute_regression` | Unit | S-115, S-54 | Existing semantics |
| T-47 (QA-M) | `e2e_room_history_paging` | E2E | S-40, S-41, S-42 | Existing spec re-expressed on a real group above the snapshot cap (the existing 10,005-entry fixture): assert the earlier-page request and its exclusive cursor, the anchor, then Return to latest |
| T-48 | `e2e_rich_content_and_attachments` | E2E | S-01, S-05, S-20, S-21, S-29 | Real server, stub model |
| T-49 | `e2e_approvals` | E2E | S-71, S-72 | Real server, stub model |
| T-50 | `e2e_start_group_and_invitation` | E2E | S-60, S-61, S-62, S-63 | Real server, stub model; provider-down variant |
| T-52 | `docs_verifier_audit` | Audit | (DOC-001..DOC-008) | docs-verifier audits the drafts against actual behaviour, including the normal-chat file-card download and Retry text in DOC-005 |
| T-53 | `vitest_coverage_group_guard` | CI gate | (process) | Every new vitest file matches a CI group pattern |
| T-54 | `message_size_limit` | Unit (Go) | S-118, S-119 | 65,536 / 65,537 bytes at the default setting; multi-byte boundary; agent answer over the limit posts nothing and copies no file; same result with inbound validation on and off |
| T-55 | `contract_text_limit_agrees_with_server` | CI gate | S-118, S-150 | The 65,536-byte maximum stated in the contract schemas (both copies, request and entry) equals the server's clamp constant; fails if either drifts (D24, D27) |
| T-56 | `e2e_remote_image_blocked_in_browser` | E2E (real browser, embedded SPA) | S-106 | Controlled remote image endpoint, a positive control proving the instrument sees an allowed request, and the blocked image's policy violation or failure recorded |
| T-57 | `quoted_context_attachments_by_filename` | Unit (Go) | S-120, S-121 | Text at 65,536/65,537 bytes with 5 MB media; quoted history names earlier files as text only; only the current request's files reach the model |
| T-58 | `invitation_detail_refresh_transport` | Unit + integration (frontend transport) | S-61, S-122, S-123 | Status-only frame, then the detail fetch, then the rendered text; disconnect during generation; fetch failure with Retry |
| T-59 | `attachment_retention_collector` | Unit (Go, controlled clock) | S-124, S-125, S-126, S-28 | The real orphan collector 31 days ahead, before and after restart. Asserts the chat media copy and the Library file separately: referenced chat copies survive; an unowned never-posted chat copy is collected while its Library file stays; a deleted paused group's original media URL still opens in a fixture with no consuming-session owner (so a missing group owner is detected), and separately from the agent's chat; ownership acquired once and released when the append fails |
| T-60 | `library_publication_and_deletion` | Integration + E2E | S-127, S-128, S-136, S-146 | Person uploads and agent-returned files listed and downloadable through the real Library surface, each with its durable `library_path`; one Library deletion (D26) removes both copies, asserted on the Library listing, the chat media manifest, the group URL, the mirrored entry and a normal-chat upload; nothing reappears after a restart and an orphan-cleanup run |
| T-61 | `shared_file_rows_download_and_error` | Unit | S-129, S-130 | PDF download by keyboard in user and agent rows; missing non-image file shows inline error and Retry; existing normal-chat row tests unchanged |
| T-62 | `answer_intent_lifecycle` | Unit (Go) | S-33, S-140, S-141, S-142, S-143, S-144 | The full lifecycle: accepted, publishing, committed, rejected; changed or missing original sources; every cut inside each publication; append failure with collector and restart; restart after a returned definitive failure; room, membership and cancellation rechecks; asserts original bytes, one answer, one mirror, no duplicate Library artifact and no model restart |
| T-63 | `library_delete_removes_chat_copy` | Unit (Go) | S-136, S-148, S-149 | Handler and association: chat copy first, Library file last; 204; partial failure 500 with the file still present and a repeat completing it; directory deletion; rename and same-workspace move keep the association; copy and cross-workspace move do not; retention owners and a pending answer intent do not block and do not recreate; the one-time legacy repair (exactly one SHA-256 and name match; zero or several leave no association); never across workspaces |
| T-64 | `library_delete_confirmation_and_error` | Unit (vitest) | S-147, S-148 | Dialog text with and without `chat_copy`; 500 shows the error naming the file and keeps the row; a repeat succeeds; vault-note "Move to trash" unchanged |
| T-65 | `group_size_limit_operator_setting` | Unit (Go) | S-150 | Effective limit `min(steer_body, 65,536)`: unset, 131,072 (clamped), 1,000 (honoured) for person messages and agent answers; error text names the limit in force; a live setting change applies to the next message |

### Test Datasets

Construction reference: `.claude/skills/plan-spec/knowledge/test-dataset-template.md`. Every expected value is from this spec, the cited contract schemas or the ADR.

#### Dataset DS-1: Posting a group message (`postRoomMessage`)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | text `""`, media `[]` | Empty | 422, nothing appended | BDD Scenario: S-18 | zero-length |
| 2 | text `"   \n\t"`, media `[]` | Whitespace only | 422, nothing appended | BDD Scenario: S-18 | invisible but non-empty |
| 3 | text `"a"`, media `[]` | Min text | 202 | BDD Scenario: S-18 | one character |
| 4 | text `""`, media `[1 valid ref]` | Media only | 202 | BDD Scenario: S-21 | attachment-only |
| 5 | text `"hi"`, media `[16 valid refs]` | Max | 202 | BDD Scenario: S-24 | upper bound |
| 6 | text `"hi"`, media `[17 valid refs]` | Max + 1 | 400 | BDD Scenario: S-24 | schema violation |
| 7 | media `[ref of a session file]` | Wrong scope | 422, nothing appended | BDD Scenario: S-26 | not a workspace file |
| 8 | media `[ref of workspace B]` in a group of workspace A | Cross-workspace | 422, nothing appended | BDD Scenario: S-25 | whole message refused |
| 9 | media `[missing ref]` | Missing | 422 | BDD Scenario: S-26 | |
| 10 | media `[stranded library entry]` | Stranded | 422 | BDD Scenario: S-26 | |
| 11 | media `[valid ref, bad ref]` | Mixed | 422 and the valid file is not shown | BDD Scenario: S-26 | all-or-nothing |
| 12 | media `[""]` | Empty string | 422 | BDD Scenario: S-26 | |
| 13 | media `["https://example.com/x.png"]` | Web address | 422 | BDD Scenario: S-26 | |
| 14 | mentions: 256 ids | Max | 202 | BDD Scenario: S-14 | existing contract limit |
| 15 | mentions: 257 ids | Max + 1 | 400 | BDD Scenario: S-14 | existing contract limit |
| 16 | `client_message_id` `""` | Empty | 400 | BDD Scenario: S-18 | existing contract min length |
| 17 | `client_message_id` of 129 characters | Max + 1 | 400 | BDD Scenario: S-18 | existing contract max length |
| 18 | text of 65,536 ASCII bytes | Max | 202 | BDD Scenario: S-118 | D24 |
| 19 | text of 65,537 ASCII bytes | Max + 1 | 422 naming size and limit; nothing appended | BDD Scenario: S-118 | with inbound validation on and off (A-12) |
| 20 | 21,845 three-byte characters (65,535 bytes) | Multi-byte under | 202 | BDD Scenario: S-118 | bytes, not characters |
| 21 | 21,846 three-byte characters (65,538 bytes) | Multi-byte over | 422 | BDD Scenario: S-118 | |
| 22 | agent answer text of 65,536 bytes with a file | Max | posted | BDD Scenario: S-119 | |
| 23 | agent answer text of 65,537 bytes with a file | Max + 1 | tool error; nothing posted, copied or passed on | BDD Scenario: S-119 | |
| 24 | text 65,536 ASCII bytes with 1 valid 5 MB media | Max with media | 202 | BDD Scenario: S-120 | media never consumes text bytes |
| 25 | text 65,537 ASCII bytes with 1 tiny media | Max + 1 with media | 422 | BDD Scenario: S-120 | independent of media size |
| 26 | `steer_body` 131,072; text of 65,536 bytes | Clamp, at limit | 202 | BDD Scenario: S-150 | D27 |
| 27 | `steer_body` 131,072; text of 65,537 bytes | Clamp, over | 422 naming 65,536 | BDD Scenario: S-150 | a larger setting never raises the limit |
| 28 | `steer_body` 1,000; text of 1,000 bytes | Lowered, at limit | 202 | BDD Scenario: S-150 | D27 |
| 29 | `steer_body` 1,000; text of 1,001 bytes | Lowered, over | 422 naming 1,000 | BDD Scenario: S-150 | a smaller setting is honoured |
| 30 | `steer_body` 1,000; agent answer of 1,001 bytes | Lowered, answer | tool error naming 1,000; nothing posted | BDD Scenario: S-150 | same limit for answers |
| 31 | `steer_body` unset or 0 | Default | limit 65,536 | BDD Scenario: S-150 | `EffectiveSteerBodyBytes` default |
| 32 | media `[ref X, ref X]` (same valid ref twice) | Duplicate | 202; entry lists both; shown twice; retention counted once | BDD Scenario: S-21 | TD-4 (owner-refinable default) |

The text-length rows above follow D24 and D27 (one limit, at most 65,536 UTF-8 bytes, lowered by a smaller operator setting).

#### Datasets DS-2 and DS-3

Retired by D21 (stop request and stop outcome datasets); ids are not reused.

#### Dataset DS-4: Invitation summary lifecycle

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | model returns at 44 s | Just under limit | `ready` | BDD Scenario: S-64 | |
| 2 | model returns at 45 s | At limit | `ready` | BDD Scenario: S-64 | |
| 3 | model returns at 46 s | Just over limit | `unavailable`; late result discarded | BDD Scenario: S-64 | |
| 4 | provider error or quota error | Error | `unavailable`; first message not held | BDD Scenario: S-63 | |
| 5 | restart while `generating` | State | `unavailable` on load; late result discarded | BDD Scenario: S-65 | |
| 6 | group deleted while generating | Deleted | result discarded, no cost line | BDD Scenario: S-66 | |
| 7 | origin agent removed while generating | Removed | result discarded | BDD Scenario: S-66 | |
| 8 | first message while `generating` | Concurrency | delivered at once, no summary, never added later | BDD Scenario: S-69 | |
| 9 | first message after `ready`, to a non-origin member | Happy | request carries the summary once | BDD Scenario: S-62 | |
| 10 | second message to the same member | Repeat | no summary prefix | BDD Scenario: S-62 | delivered once |
| 11 | message to the origin agent | Origin | never receives the summary | BDD Scenario: S-62 | |
| 12 | summary text 4096 characters | Max | `ready`, stored | BDD Scenario: S-61 | contract maximum |
| 13 | summary text 4097 characters | Max + 1 | `ready`; text cut at the last word boundary at or before 4,096 characters | BDD Scenario: S-61 | TD-1 |
| 13b | summary text 4,200 characters with no space | Max + 1, no boundary | `ready`; hard cut at exactly 4,096 characters | BDD Scenario: S-61 | TD-1 |
| 14 | cost 0.02 dollars written twice | Duplicate | counted once | BDD Scenario: S-68 | |
| 15 | receiver capture write fails, same request retried | Failure | summary carried once | BDD Scenario: S-131 | |
| 16 | two first requests at once | Concurrency | exactly one carries it | BDD Scenario: S-132 | |
| 17 | request cancelled, receiver facts show no capture | Release | reservation released; a later new request may carry it | BDD Scenario: S-139 | |
| 18 | request unavailable after a storage failure, then R2 arrives, then R1 retried | Retryable | R1 carries it once; R2 never | BDD Scenario: S-138 | |
| 19 | capture stored, acknowledgement failed | Reconcile | delivered; no second delivery | BDD Scenario: S-139 | |
| 20 | facts read fails | Read error | reservation kept | BDD Scenario: S-139 | |
| 21 | cancellation races the commit, capture exists | Race | delivered | BDD Scenario: S-139 | |

#### Dataset DS-5: Summary input and filters

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | origin tail of 40 entries | Max | all 40 used | BDD Scenario: S-61 | spec ceiling |
| 2 | origin tail of 41 entries | Max + 1 | newest 40 used | BDD Scenario: S-61 | |
| 3 | origin tail of 16,384 bytes | Max bytes | used whole | BDD Scenario: S-61 | 16 KiB |
| 4 | origin tail of 16,385 bytes | Max + 1 | cut to 16,384 bytes at an entry boundary | BDD Scenario: S-61 | |
| 5 | registered secret value `sk-REG-123` in the tail | Secret | absent from stored summary | BDD Scenario: S-67 | |
| 6 | credential-shaped string (API key pattern) | Secret | absent from stored summary | BDD Scenario: S-67 | |
| 7 | secret-request card entry | Excluded | not part of the model's input | BDD Scenario: S-67 | |
| 8 | result of a secret tool / vault content | Excluded | not part of the model's input | BDD Scenario: S-67 | |
| 9 | "the password is hunter2" in plain prose, unregistered | Known gap | **not asserted** (accepted, #1467) | BDD Scenario: S-67 | recorded so it cannot become a false green |
| 10 | origin tail with 0 usable entries | Empty | no model call; `unavailable`; no cost line | BDD Scenario: S-63 | TD-2 |

#### Dataset DS-6: Rendering and safety of agent text

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | markdown table with 100 rows | Large | renders as a table; list stays responsive | BDD Scenario: S-01 | |
| 2 | code block of 10 KB | Large | renders; Copy copies all of it | BDD Scenario: S-02 | |
| 3 | valid diagram | Happy | drawn | BDD Scenario: S-03 | |
| 4 | invalid diagram source | Error | diagram error state; text kept | BDD Scenario: S-04 | |
| 5 | `<script>alert(1)</script>` | Injection | literal text | BDD Scenario: S-104 | |
| 6 | link `javascript:alert(1)` | Unsafe scheme | struck through | BDD Scenario: S-105 | |
| 7 | link `data:text/html,x` | Unsafe scheme | struck through | BDD Scenario: S-105 | |
| 8 | link `https://example.com` | Safe | clickable, new tab, no opener access | BDD Scenario: S-105 | |
| 9 | image `https://tracker.example/p.png` | Remote | no request made | BDD Scenario: S-106 | |
| 10 | answer text `/pause` | Command-like | visible text; group not paused | BDD Scenario: S-107 | |
| 11 | answer with right-to-left text and a Unicode name | Unicode | rendered; author label intact | BDD Scenario: S-07 | |
| 12 | image that is 0 bytes | Empty file | broken-image state with Retry | BDD Scenario: S-06 | |

#### Dataset DS-7: Message box state matrix

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | no active chat session anywhere | State | box enabled; send posts to the group | BDD Scenario: S-108 | |
| 2 | other chat streaming | State | box enabled; nothing sent to the other chat | BDD Scenario: S-109 | |
| 3 | other chat replaying history | State | box enabled | BDD Scenario: S-109 | |
| 4 | other chat has an open question | State | box enabled | BDD Scenario: S-111 | |
| 5 | Escape pressed in the group | Key | other chat not cancelled | BDD Scenario: S-110 | |
| 6 | group read-only | Blocked | disabled with reason | BDD Scenario: S-17 | |
| 7 | connection down | Blocked | disabled with reconnecting reason | BDD Scenario: S-17 | |
| 8 | first live view not received | Blocked | disabled with waiting reason | BDD Scenario: S-17 | |
| 9 | uploading a file | Busy | send waits for the upload as in a normal chat | BDD Scenario: S-22 | |
| 10 | typed `/model` | Refused command | refusal with member links | BDD Scenario: S-13 | |

#### Dataset DS-8: Where cards appear

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | tool approval, turn consumed only this group's request | Happy | group + agent's chat | BDD Scenario: S-71 | |
| 2 | same, step also handled a message typed in the agent's own chat | Mixed | agent's chat only | BDD Scenario: S-83 | D23 |
| 3 | same, step also handled a channel message | Mixed | agent's chat only | BDD Scenario: S-79 | D23 |
| 4 | same, step also handled another agent's message | Mixed | agent's chat only | BDD Scenario: S-83 | D23 |
| 5 | approval of unrelated work (no reference) | Unrelated | agent's chat only | BDD Scenario: S-73 | |
| 6 | helper's approval | Helper | agent's chat only + attention in group | BDD Scenario: S-77 | |
| 7 | secret request | Secret | agent's chat only + link | BDD Scenario: S-78 | |
| 8 | question during group work | Question | group + agent's chat | BDD Scenario: S-74 | |
| 9 | question card after page reload | Reload | still in both | BDD Scenario: S-75 | |
| 10 | approve in A, deny in B simultaneously | Race | exactly one decision | BDD Scenario: S-80 | |
| 11 | same as 1, step also handled a task | Mixed | agent's chat only | BDD Scenario: S-79 | D23 |
| 12 | step handled requests of two different groups | Mixed | agent's chat only; attention animation and link in both groups | BDD Scenario: S-151 | D28 |

#### Dataset DS-9: History opening and paging

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | opening view fills the screen, older entries exist | Covered | zero history calls | BDD Scenario: S-43 | |
| 2 | server says no older entries | Terminal | zero history calls; no hint | BDD Scenario: S-43 | |
| 3 | opening view too short, older exist | Fill | contiguous pages, `before_seq` strictly decreasing, `limit` within 20..100 | BDD Scenario: S-44 | |
| 4 | page limit 20 | Min | accepted | BDD Scenario: S-44 | |
| 5 | page limit 100 | Max | accepted | BDD Scenario: S-44 | |
| 6 | page request fails | Error | rows kept; Retry repeats the same `before_seq` | BDD Scenario: S-45 | |
| 7 | response arrives after leaving the group | Stale | ignored | BDD Scenario: S-48 | |
| 8 | response arrives after a truncating snapshot | Stale | ignored; view generation advanced | BDD Scenario: S-48 | |
| 9 | page contains entries of another group, not older than the request, or out of order | Invalid | rejected through the error and Retry path | BDD Scenario: S-45 | existing rule |
| 10 | group with 0 entries | Empty | empty list, ready box | BDD Scenario: S-47 | |
| 11 | real group with 10,005 entries | Above snapshot cap | newest 10,000 loaded on open; the 5 older entries load by one earlier-page request with an exclusive cursor | BDD Scenario: S-40 | the existing e2e fixture size; a 500-entry group cannot exercise paging |
| 12 | group with 10,000 entries exactly | Snapshot cap | all loaded; no earlier page requested | BDD Scenario: S-43 | |

#### Dataset DS-10: Attachment upload

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | one 0-byte file | Empty file | per the normal chat's rule for empty files | BDD Scenario: S-22 | same as normal chat; qa-lead mirrors that oracle |
| 2 | one file of exactly 100 MB | Max | 201 | BDD Scenario: S-22 | existing per-part limit in the `uploadFiles` contract |
| 3 | one file of 100 MB + 1 byte | Max + 1 | refused as in the normal chat | BDD Scenario: S-22 | |
| 4 | filename with spaces and Unicode | Special name | stored; shown with its name | BDD Scenario: S-05 | |
| 5 | filename `../../x` | Path trick | stored under a safe name; never outside the library | BDD Scenario: S-22 | as the normal chat |
| 6 | `workspace_id` of another workspace in an upload | Foreign workspace | the same outcome as the existing normal-chat upload path for the same input (parity; no new authorization) | BDD Scenario: S-23 | an independently supplied caller workspace is needed to assert any 403 (S-135) |
| 7 | `workspace_id` and `session_id` both given | Conflict | `workspace_id` routes to the library; `session_id` ignored | BDD Scenario: S-23 | documented rule |
| 8 | library unavailable | Dependency down | 503; no fallback | BDD Scenario: S-22 | |

#### Dataset DS-11: Retention under the real orphan cleanup (controlled clock)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | attachment in a paused group, 31 days, cleanup runs | Age over limit | file survives | BDD Scenario: S-124 | default orphan age 30 days |
| 2 | held request's attachment, restart, 31 days, cleanup | Age + restart | file survives; agent receives it | BDD Scenario: S-125 | |
| 3 | group deleted, 31 days, cleanup | Deleted | file survives; opens from the agent's chat and Library | BDD Scenario: S-126 | D19 |
| 4 | upload never posted, 31 days, cleanup | Unowned | chat media copy collected; Library file stays listed | BDD Scenario: S-28 | existing behaviour kept, each copy asserted |
| 5 | append failed after ownership acquired | Failure | ownership released; file collectible | BDD Scenario: S-124 | |
| 6 | the same message retried | Duplicate | ownership counted once | BDD Scenario: S-124 | idempotent |
| 8 | upload never posted, 31 days | Two copies | chat media copy collected; Library file stays | BDD Scenario: S-28 | M1: assert each copy |
| 9 | deleted paused group with no consuming session, 31 days | Group owner only | original media URL opens | BDD Scenario: S-126 | detects a missing group owner |
| 7 | file at 29 days | Under limit | survives with or without ownership | BDD Scenario: S-124 | |

#### Dataset DS-12: Answer identity, lifetime and recovery

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | same text, same original file argument, mirror failed earlier | Retry | one answer, one copy, mirrored once | BDD Scenario: S-133 | |
| 2 | same text, different original file | Distinct | separate answer | BDD Scenario: S-133 | |
| 3 | same text, same files in a different order | Order | separate answer (ordered list) | BDD Scenario: S-32 | |
| 4 | crash after intent record | Cut | completed from the intent | BDD Scenario: S-33 | |
| 5 | crash after copy | Cut | completed from the intent; no second copy | BDD Scenario: S-33 | |
| 6 | crash after append / mirror / mention admission | Cut | repaired once | BDD Scenario: S-33 | |
| 7 | text only, no files | Existing | existing repair behaviour unchanged | BDD Scenario: S-32 | |
| 8 | equal call after the answer fully completed | Lifetime | new answer | BDD Scenario: S-137 | same as text-only today |
| 9 | original file changed after acceptance | Source change | staged bytes posted | BDD Scenario: S-141 | |
| 10 | original file deleted after acceptance | Source gone | staged bytes posted | BDD Scenario: S-141 | |
| 11 | cut after chat media copy committed, result not recorded | Publication cut | one chat copy, one Library file | BDD Scenario: S-142 | preallocated identities |
| 12 | cut after Library file written, result not recorded | Publication cut | one chat copy, one Library file | BDD Scenario: S-142 | |
| 13 | definitive rejection returned, then restart | Rejected | never posts; staging removed | BDD Scenario: S-140 | |
| 14 | retryable append failure, 31 days, cleanup, restart | Pending | posts once from staged bytes | BDD Scenario: S-143 | |
| 15 | group deleted / agent removed / request cancelled before recovery | Recheck | rejected; nothing posted; no model work | BDD Scenario: S-144 | |
| 16 | answer lists the same file twice | Duplicate | accepted; two listed attachments | BDD Scenario: S-29 | TD-4 (owner-refinable default) |
| 17 | answer with 16 files | Max | accepted | BDD Scenario: S-31 | TD-3 |
| 18 | answer with 17 files | Max + 1 | definitive rejection; nothing posted | BDD Scenario: S-31 | TD-3 |
| 19 | answer file of 100 MB + 1 byte | Over per-file limit | definitive rejection before acceptance; nothing staged or posted | BDD Scenario: S-31 | TD-3 |

#### Dataset DS-13: Library deletion of a chat file (D26)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | delete a group file with a chat copy | Happy | 204; Library file and chat copy gone; group, mirror show unavailable | BDD Scenario: S-136 | |
| 2 | delete a normal chat upload | Happy | 204; both gone; normal chat shows unavailable | BDD Scenario: S-146 | D26 applies to all chat files |
| 3 | delete a file without a chat copy | No association | 204; only the file removed; today's dialog | BDD Scenario: S-147 | |
| 4 | chat copy removal fails | Failure | 500; Library file still listed | BDD Scenario: S-148 | |
| 5 | Library file removal fails after the chat copy was removed | Partial | 500; file still listed; repeat gives 204 | BDD Scenario: S-148 | missing chat copy counts as removed |
| 6 | chat copy already gone before the deletion | Already removed | 204; Library file removed | BDD Scenario: S-148 | idempotent |
| 7 | chat copy referenced by 3 sessions and a group retention owner | Owned | 204; both gone; no owner blocks or recreates | BDD Scenario: S-136 | explicit deletion wins (FR-076) |
| 8 | pending answer intent references the file | Intent | deleted; the intent never recreates it | BDD Scenario: S-136 | FR-078 (4) |
| 9 | renamed, then deleted | Association moves | both removed | BDD Scenario: S-149 | |
| 10 | moved inside the workspace, then deleted | Association moves | both removed | BDD Scenario: S-149 | |
| 11 | copied, copy deleted | No association | only the copy removed | BDD Scenario: S-149 | |
| 12 | `.library` folder deleted | Directory | every associated chat copy removed | BDD Scenario: S-149 | |
| 13 | moved to another workspace, then deleted there | Cross-workspace | only the moved file removed; chat copy stays | BDD Scenario: S-149 | documented limit |
| 14 | legacy upload, exactly one SHA-256 + name match | Repair | associated; both removed | BDD Scenario: S-149 | A-14 |
| 15 | legacy upload, two matches | Ambiguous | no association; only the Library file removed | BDD Scenario: S-149 | A-14 |
| 16 | legacy upload, same SHA-256 in another workspace only | Cross-workspace | no association | BDD Scenario: S-149 | never across workspaces |
| 17 | after deletion: restart and orphan-cleanup run | Durability | nothing reappears | BDD Scenario: S-136 | |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Session chat history (#1266/#1452) | about 30 `ChatScreen.history-*.test.tsx` and `src/store/chat.history-*.test.ts` | **Yes - T-24 (unchanged files are the gate)** | zero edits allowed in FE-1 |
| Session chat composer | `ChatScreen.*composer*` tests | **Yes - T-25** | unchanged after extraction |
| Room store cursor, dedupe, fencing | `src/store/rooms*.test.ts` (4 files) | Yes - T-42 (additions only) | stay |
| Room opening, history, anchors | `RoomScreen.cached-opening/history`, `RoomTranscript.*` tests | Yes - T-34 | moved or re-expressed per the ADR's assertion map; deleted by qa-lead only after replacements pass in CI with red-before-green evidence |
| Room composer rules (@all, members, rename, ambiguity, agent "/pause" text) | `rooms.composer.test.tsx` | Yes - T-28, T-32 | moved |
| Room view (ordering, replayed id once, after-pause mark, read-only blocks, Error/Retry) | `rooms.view.test.tsx` | Yes - T-30, T-36 | split: rows to shared list, members/cost/states to panel |
| Room history e2e | `tests/e2e/room-history-paging.spec.ts` | Yes - T-47 | re-expressed (Return to latest first); keep the above-cap fixture |
| `FeedKindLabel` and header cluster | `FeedKindLabel.*`, `ChatScreen.kindLabel`, `AddParticipant`, `WorkspaceTabBar.fit`, `ChatHeaderSlot.test.tsx` | Yes - T-37, T-38 | the slot already exists on `feature/1449-minimal-header` |
| FR-199: room frames never enter session buckets | `src/store/rooms.test.ts` "room frames do not replace or create the active agent's main transcript bucket" | Yes - S-116 | unchanged |
| Helper approvals carry no group reference | `TestWU15Rooms_ReceiverHelperApprovalReloadRestoresCompleteApprovableCard` | No change | stays as is (D15) |
| Hard stop interrupts initiating request without replay | `TestRoomsFix5_HardStopInterruptsInitiatingRequestWithoutReplay` | No change | |
| Add participant starts an empty group, origin unchanged | `TestWU15Rooms_AddParticipantStartsEmptyAndLeavesOriginMainUnchanged` | Yes - T-22 | gains: invitation record exists, still no entry |
| Contract-limit tests for rooms | `wu15_rooms_*` | Yes - T-01, T-02 | plus attachments and media |
| REST room routes | `rest_rooms_test.go` | Yes - T-01, T-02, T-54 | plus message size and media |
| `answer_request` has a ceiling policy entry for every agent | `pkg/coreagent/answer_request_policy_test.go` | No change | tool not new |
| Library delete of a plain work-tree file, directory, vault note (trash) | existing `rest_library_write` and `LibraryExplorer` delete tests | Yes - T-63, T-64 | files with no chat copy keep today's behaviour unchanged |
| Session messaging `steer_body` for messages between sessions | existing `session_messaging` tests | No change | D27 clamps only the group limit; session-to-session messages keep the setting as is |

---
## Functional Requirements

**Display**
- **FR-001**: System MUST render group message text through the same renderer as the normal chat's finished answers: markdown, tables, code blocks with a Copy control, math, and Mermaid diagrams (invalid diagram source shows the normal chat's diagram error state).
- **FR-002**: System MUST render entry images with the lightbox, and non-image files as file cards with a keyboard-operable download control in both the person's and the agent's rows; it MUST show the normal chat's broken-image state with Retry for an image that cannot be fetched and an inline error with Retry for a non-image file that cannot be fetched. This is shared file-row behaviour (one implementation, not a group-only fork) that the normal chat's rows also gain; existing normal-chat tests stay unchanged.
- **FR-003**: System MUST show each agent answer under its author's name and avatar and the person's own messages right-aligned without a label.
- **FR-004**: System MUST keep the muted "after pause" mark on answers posted after a pause.
- **FR-005**: System MUST NOT show on any group row the live tool calls or status of any chat session.

**Message box**
- **FR-006**: System MUST render the group message box with the normal composer (same card, attach and send controls) under a group profile.
- **FR-007**: System MUST omit the model picker, Auto-approve control, token counter and skills control from the group message box.
- **FR-008**: System MUST allow up to 16 attachments per group message, show upload errors on the attachment chip, and refuse the 17th.
- **FR-010**: System MUST offer exactly the six group commands (pause, resume, mute, unmute, members, help) in the slash menu and MUST refuse other commands with the existing message and links to the members' own chats.
- **FR-011**: System MUST offer "all" and every current member (muted included) after "@"; picks MUST stay bound to the member through a rename; deleting a picked name MUST remove only the explicit binding (the explicit mentions list becomes empty if it was the last pick), after which the ordinary rules apply (default delivery to every member that is not muted, or the exact-name rule for typed names); typed names MUST resolve only by the exact-name rule.
- **FR-012**: System SHOULD show the placeholder "Message the group - type @ to address someone".
- **FR-013**: System MUST disable sending and show the reason in the notice area when the group is read-only, reconnecting, or has not received its first live view.
- **FR-014**: System MUST refuse (422) a message with no non-blank text and no media, and MUST post nothing.
- **FR-015**: System MUST NOT send to, create, cancel, navigate, or be locked by any chat session from the group message box, and Escape in the group MUST NOT cancel anything outside the group.

**Files and rich content (server)**
- **FR-016**: System MUST store group uploads exactly as normal chat uploads are stored: the chat media copy in the workspace media store (`workspaces/<workspace>/media/`) and the Library file in the workspace's Library folder, written by the existing upload path, using the room's workspace and no session, and MUST NOT create a session for them (D25).
- **FR-017**: System MUST answer 503 when the library is unavailable for a group upload and MUST NOT fall back to the session path.
- **FR-018**: System MUST accept a message's media only as `media://workspace/<this room's workspace>/<id>` references that resolve, MUST resolve every reference before appending anything, and MUST refuse the whole message with 422 if any reference is another workspace's, missing, stranded, or not a workspace file.
- **FR-019**: System MUST keep a message's media in the durable request together with its text so that initial admission, restart-time rewake and held delivery give the model the same files.
- **FR-020**: System MUST let `answer_request` carry up to 16 optional `media` entries (workspace references of the room's workspace, or references or paths the agent's own chat can already send), MUST stage and publish the latter as FR-078 defines before anything is posted, and MUST return a tool error and append nothing on any definitive failure.
- **FR-021**: System MUST order an agent answer as: validate and stage the original bytes and record the accepted intent, publish files (both representations), append the entry, mirror to the agent's chat, then admit mentions; and a person's message as: resolve media, acquire retention, then append deliveries and the entry.
- **FR-022**: System MUST identify an answer attempt by request id, text and the ordered original source identities of its files (a workspace reference, or a path with its content hash taken at acceptance); see FR-084 for how long that identity denotes the same attempt.
- **FR-023**: System MUST mirror a request's and an answer's attachments into the member's main chat and MUST show them in the main chat's collapsed group entry when expanded.
- **FR-024**: System MUST NOT count attachments (their bytes, number or metadata) toward the text size cap: text of 65,536 bytes with valid media is accepted and text of 65,537 bytes is refused whatever the media size (S-120).
- **FR-025**: System MUST NOT delete library files when a group is deleted; both representations stay, and downloadable from members' main chats (D25). One explicit Library deletion removes both representations (D26, FR-086), and a retention or recovery step MUST NOT recreate a file the user explicitly deleted.
- **FR-026**: System MUST NOT add any upload or media-read authorization to group files: their read posture is the normal chat's (unguessable id; the media route derives the caller workspace from its own URL). The only new cross-workspace rule is at room ingestion (FR-018, 422). The resolver's cross-workspace refusal is asserted only with an independently supplied caller workspace (S-135).

**Live view**
- **FR-027**: System MUST show one "<Agent> is working..." line per member whose latest request is `admitted`, `working`, `waiting_on_user` or `awaiting_approval`, replace it with the finished answer, show the state label for terminal non-answer states, and MUST NOT show partial answer text.
- **FR-028**: System MUST show the attention animation on a member's avatar, with the link "Open <Agent>'s main chat", while its group request is `waiting_on_user` and the card is not in the group (including a card held in the agent's chat because the step answers two groups, D28, in every group concerned); the animation MUST stop when the card is resolved.

**Layout and history**
- **FR-029**: System MUST lay a group out in the normal chat's centred columns (messages and message box widths as the normal chat), with or without the Participants panel open.
- **FR-030**: System MUST give groups the normal chat's history behaviour: paging on scroll, "Return to latest", the open loading cover, and Retry on the same page after a failed load.
- **FR-031**: System MUST request group history only when the opening view does not cover the fill goal and older entries exist; requests MUST be contiguous pages with strictly decreasing `before_seq` and `limit` between 20 and 100; stale responses MUST be ignored.
- **FR-032**: System MUST keep group state in its own store and MUST NOT create or replace any chat session's transcript from group updates (FR-199).

**Participants**
- **FR-033**: System MUST show in the header slot one Participants button containing up to three avatars, an overflow marker for more, and the member count, with `aria-pressed` reflecting the panel.
- **FR-034**: System MUST open the Participants panel only on demand, MUST NOT list it as a strip entry, and MUST close it when the person leaves the group.
- **FR-035**: System MUST show in the panel per member the status, a link to its main chat and a mute/unmute/remove menu, plus Add participant (disabled with the existing explanation in the team room), delete for squads and ad-hoc groups, "Cost so far" with an incomplete marker when unpriced, and "Invitation context".
- **FR-036**: System MUST fill the message box with the member addressed when the person chooses Ask again on an interrupted request, and MUST NOT send it.
- **FR-037**: System MUST keep the chat column unchanged when the panel opens.

**Starting a group and invitation context**
- **FR-038**: System MUST show "Add participant" only in an agent's main chat; it MUST create a new empty group, leave the original chat unchanged and post no message.
- **FR-039**: System MUST generate the invitation summary in the gateway after an ad-hoc group with an origin chat is created, using the origin agent's model, from at most 40 entries and 16 KiB of the origin chat, producing about 200 words and at most 4,096 characters (a longer filtered output is cut at the last word boundary at or before 4,096 characters, or hard-cut when none exists, and stays `ready`, TD-1), within 45 seconds, in the same workspace only. When the origin chat has no usable entry after the exclusions of FR-040, it MUST NOT call the model and MUST set `unavailable` with no cost (TD-2).
- **FR-040**: System MUST exclude secret-card entries, secret-tool results and vault content from the summary input, and MUST pass input and output through the registered-secret scrubber and the credential-pattern redactor, filtering the output before storing it.
- **FR-041**: System MUST keep the summary state as `generating`, then `ready` or `unavailable` (provider error, quota, time limit, restart while generating, group deleted, origin agent removed), and MUST discard a result that arrives after `unavailable` or after deletion.
- **FR-042**: System MUST NOT wait for or hold any message because of the summary: delivery only reads the current state, and a member whose first request goes out while the summary is not `ready` goes without it.
- **FR-043**: System MUST deliver a `ready` summary once, as quoted background, on each non-origin member's first request, and MUST NOT give it to the origin agent. The include-or-skip decision is a reservation bound to the request id with the transitions of FR-080.
- **FR-044**: System MUST expose the summary text only through the group read used by the panel, and MUST NOT put it into any entry, live update or mirrored chat.
- **FR-045**: System MUST publish summary status changes (not text) on the group's live metadata, and the open panel MUST refetch the group record on a `ready` change and after every reconnect while its detail may be stale (FR-079), showing "generating", the text, or "Invitation context: unavailable".
- **FR-046**: System MUST count the summary's cost once into the group's allocated cost and the origin agent's usage, only when the call ran.
- **FR-047**: System MUST NOT state in the UI, documentation or tests that passwords written in plain prose are removed from the summary (D18).

**Approvals and questions**
- **FR-048**: System MUST render tool-approval and question cards that carry this group's reference in the group, and the same card MUST also appear in the agent's own chat.
- **FR-049**: System MUST resolve a card once by its id (first valid decision wins) and both views MUST update without a reload.
- **FR-050**: System MUST capture a question's group reference and origin label when the question is created, keep it immutable, carry it in the live card and the reconnect snapshot, and carry it into the resumed work's origin.
- **FR-051**: System MUST NOT show in the group cards that carry no reference of this group: unrelated work, helper cards, secret cards; the group shows the attention animation and link instead.
- **FR-052**: System MUST show a tool-approval or question card in the group only when the step that raised it answers this group; a step that also handles any other input (a message typed in the agent's own chat, a messaging channel, a task, another agent's message) MUST keep its cards in the agent's own chat only (D23). A step that answers requests from two different groups MUST keep its cards in the agent's own chat only, carrying no group reference, and every group concerned shows the attention animation and link (D28, FR-028).
- **FR-053**: System MUST flip to "waiting for you" only the request a reference-carrying card belongs to; a card without a reference keeps the existing per-chat behaviour.
- **FR-054**: System MUST deliver this group's approval cards to a browser that is on the group page with no chat attached.

**Header and chrome**
- **FR-063**: System MUST render the group's Participants button in the existing header slot at the right end of the workspace header line, leaving the left side unchanged.
- **FR-064**: System MUST render no bar, border or surface fill for the group's header controls.
- **FR-065**: System MUST NOT render the "Open browser" control on a group page.
- **FR-066**: System MUST make the Participants button operable by keyboard and touch (touch-minimum target), give it a tooltip and accessible name, expose the group's name as the chat's name to assistive technology, and keep the strip's width modes working with the widest cluster.
- **FR-067**: System MUST publish `AvatarStack` as a catalogued composite, use it in the header, the panel and the activity bar, and share the sidebar's attention halo rather than copy it.
- **FR-068**: System MUST NOT regress the already-built minimal header of normal and helper chats (no bar or border; Add participant in main chats; muted breadcrumb in helper chats).

**Safety**
- **FR-069**: System MUST sanitise group text exactly as the normal chat does: raw HTML escaped, script and data links struck through and not clickable, remote images not loaded, diagrams in strict mode; the remote-image guarantee is verified in a real browser against the embedded page, not only in unit tests.
- **FR-070**: System MUST NOT let agent-authored text run a command, approve, or answer a card; commands are parsed only from the person's posts on the server.

**Preserved behaviour**
- **FR-071**: System MUST keep the sidebar's group section under each workspace.
- **FR-072**: System MUST keep the semantics of `/pause`, `/resume`, `/mute`, `/unmute` and the refusal of other commands.
- **FR-073**: System MUST keep the normal chat's history and composer behaviour unchanged; the existing suites pass with no edits.

**Message size (D24, D27)**
- **FR-074**: System MUST compute the group text limit as `min(session_messaging.steer_body, 65,536)` UTF-8 bytes (the default setting gives 65,536; a larger setting is clamped; a smaller one is honoured), read at submission time. It MUST refuse (422) a group message whose text is longer than that limit, naming the size and the limit in force, and post nothing; the contract text for the message request and the entry states the 65,536-byte maximum and that a smaller operator setting lowers it; the server enforces it with inbound validation on and off. Messages between sessions keep using `steer_body` unchanged.
- **FR-075**: System MUST refuse an agent answer whose text is longer than the same effective limit with a tool error naming the limit in force and MUST NOT post, copy its files, mirror or pass it on.

**Round-1 additions (grill findings B1, I1-I5, I10, M4)**
- **FR-076**: System MUST give every accepted group attachment (a person's message, an agent's answer, requests held or paused, and the mirrored copies) a persistent retention owner so the automatic orphan cleanup never removes a file a group entry or pending request still references. Ownership is acquired idempotently once per (entry or request id, reference) before the append, recorded durably so a restart can repair a missed acquisition, released if the append fails, and not released by deleting the group (D19). Only a user's explicit deletion removes a file.
- **FR-077**: System MUST publish every group file in both representations a normal chat upload has (chat media copy and Library file) through one shared, repeat-safe publication path used for person uploads and agent-returned files. The publication path MUST also record durably, on the chat copy's media entry, the path of its Library file (`library_path`, FR-086).
- **FR-078**: System MUST treat an agent answer with files as a durable answer intent with these states and rules. (1) **Accepted**: validation passed (room open, request owned by the caller, member still a member, request not cancelled, size limits including at most 16 files and 100 MB per file, TD-3) and every original source was read once, hashed and copied into private, immutable staging owned by the intent; the intent also records text, mentions and, for every file, the media identity and Library file name allocated at acceptance. The intent holds retention for its staged bytes and published artifacts from this moment. (2) **Publishing**: each file is published into both representations under its preallocated identities with create-or-adopt semantics, so repeating a step never creates a second artifact; the per-file outcome is recorded durably. (3) **Committed**: the entry is appended (the entry carries the intent id), ownership passes to the entry (acquire before release), then mirror and mention admission complete. (4) **Rejected**: a definitive failure (unreadable source, validation, size, room gone, member removed, request cancelled) is recorded durably as rejected before the agent receives the definitive "nothing posted" error; a rejected intent never posts, staged bytes and unreferenced published files are then removed, and a file the user deleted explicitly is never recreated. (5) A retryable failure leaves the intent accepted and the agent receives an honest "accepted, completing" result, not "nothing posted". (6) After a restart the server completes every accepted intent from the recorded data only: it never asks the agent again, never replays or resumes model work, and rechecks that the room exists, the member still belongs and the request is not cancelled before posting or admitting mentions, otherwise it rejects.
- **FR-079**: System MUST fetch the summary text only through `getRoom`: refetch on a `ready` status change and after reconnect or re-attach while the panel is open and the detail may be stale; a failed fetch shows an error with Retry.
- **FR-080**: System MUST give the summary reservation these transitions: reserved for request R (durable with R, decided once) then either delivered (R's receiver capture is durable) or released. The reservation MUST be kept across every retryable outcome of R (unavailable, unknown, storage error, a failed facts read, a capture-write failure, a failed acknowledgement after a stored capture, where the receiver's durable facts are reconciled first). It is released only when R is irreversibly cancelled and the receiver's facts positively show no capture; a read error is never proof of absence. A request that was sent without the summary (including R2 skipped while R held it) never receives it later; after a release only a later new request may reserve it. Concurrent first requests reserve exactly once. D20 holds: nothing waits.
- **FR-081**: System MUST provide the file-card behaviours of FR-002 in the shared rows used by every chat.
- **FR-082**: System MUST list earlier group attachments in the quoted group context by filename only and MUST hand only the current request's files to the model as input (S-121).
- **FR-083**: System MUST return focus on closing the Participants panel as the shared panel shell does for each action (close button to the message box, Escape to the invoking trigger) and keep an unsent draft.
- **FR-084**: System MUST define the lifetime of an answer-attempt identity as follows. A call with files whose key equals the key of the request's most recent answer returns and completes that answer while it is not fully completed (entry appended, mirrored, mentions admitted) and never creates a second entry; once it is fully completed, an equal call is a new answer (the same rule text-only answers follow today, which this feature does not change).
- **FR-086**: System MUST, on one explicit Library deletion (`deleteLibraryEntry`) of a file that has an associated chat copy, remove both the Library file and that chat copy, for normal chat uploads and group files alike (D26). The association is the media entry's durable `library_path`, kept across rename and same-workspace move, absent for a copy, dropped by a move to another workspace, and resolved only within the same workspace. Deleting a directory applies this to every associated file inside it. Retention owners and pending answer intents MUST NOT block the deletion and MUST NOT recreate either copy. The server MUST remove the chat copy first and the Library file last, answer 204 when both are gone, treat an already-missing chat copy as removed, and answer 500 with the Library file still present when either removal fails, so a repeat completes it. For uploads made before this feature, a one-time repair on start MUST associate a `.library` file with the same-workspace media entry of equal SHA-256 and equal file name (ignoring a " (N)" duplicate suffix) only when exactly one entry matches (A-14). Chats that show a deleted file MUST show it as unavailable (FR-002). Deleting a group still MUST NOT delete files (D19, FR-025).
- **FR-087**: System MUST, in the Library panel, mark a file that has a chat copy (`chat_copy`) and, when the person chooses Delete on it, say in the confirmation that the copy used in chats is deleted too and that chats will show the file as unavailable; on a failed deletion it MUST show an error naming the file, keep the file listed, and allow the deletion to be repeated. Files without a chat copy keep today's dialog.
- **FR-085**: System MUST keep the answer recovery boundary of FR-078 (6): completing an accepted answer persists work the agent already authored; it MUST NOT regenerate an answer, restart or resume an interrupted turn, or bypass cancellation, membership or deletion.

## Success Criteria

- **SC-001**: Every scenario S-01 to S-151 (S-11 and S-84 to S-97 retired by D21; S-145 retired by D26) is covered by at least one test in the Test Implementation Order, and every UAT row UAT-1 to UAT-36 (UAT-8, UAT-9, UAT-22 retired by D21) is executed on the exact candidate commit (AE rows by `uat-tester` plus independent `uat-validator`).
- **SC-002**: The existing chat history and composer suites pass with zero edited test files at the FE-0 and FE-1 gates.
- **SC-003**: `make verify-contracts` and `scripts/check-no-handwritten-wire-types.sh` pass on the candidate; no hand-written wire type exists for the new fields.
- **SC-005**: The summary job causes 0 seconds of deliberate wait for any group message (S-69, S-63).
- **SC-006**: 0 library files are deleted by deleting a group (S-112).
- **SC-007**: `src/components/chat/ChatScreen.tsx` is never longer than 2,961 lines at any commit of the feature.
- **SC-008**: On a group whose opening view fills the screen, the number of `getRoomHistory` calls on open is 0 (S-43).
- **SC-009**: docs-verifier passes DOC-001 to DOC-008 against the actual behaviour.
- **SC-013**: With a controlled clock 31 days ahead, the real orphan cleanup removes 0 referenced group files (paused group, held request, deleted group, after restart) and still removes the chat media copy of an upload that was never posted while its Library file stays listed (S-124, S-125, S-126, S-28).
- **SC-012**: The contract's 65,536-byte maximum for the group message request and entry equals the server's clamp constant, and the server applies `min(steer_body, 65,536)`, checked by T-55 and T-65 (S-118, S-150).
- **SC-014**: After one explicit Library deletion of a chat file, 0 copies of it remain (Library file and chat media copy), also after a restart and an orphan-cleanup run, for group files and normal chat uploads (S-136, S-146, T-60, T-63).
- **SC-010**: The old `RoomTranscript`, `RoomComposer` and `RoomHeader` components are deleted only after the replacement tests pass in CI with red-before-green evidence.

---

## Reachability

> Definition of Done (root `CLAUDE.md`): the feature is done only when a real user or agent can invoke it.

- **Tool registration**: **no new tool.** The one agent-facing change is the new optional `media` parameter of the existing `answer_request` tool. `answer_request` is already registered and has its policy entries: ceiling `"answer_request": "allow"` in `pkg/config/defaults.go` (`sandbox.tool_policies`), seeded for the native agents in `pkg/coreagent/seed.go` and listed in the common work tool set of `pkg/coreagent/role_policies_adr090.go`; the guard test is `pkg/coreagent/answer_request_policy_test.go` (unchanged). Per Hard Constraint #6 no policy value changes and no per-agent override is added. The reachability check `grep -rl '"answer_request"' pkg/coreagent/ pkg/config/ pkg/tools/` returns files (verified), so the tool is callable.
- **Capabilities, who invokes them, and the screen that renders them**:

| Capability | Who can invoke it | Screen / component that renders it | How they get there |
|---|---|---|---|
| Read a group with rich content | any signed-in person | shared message list on the group page | sidebar group section under the workspace, route `/workspaces/$workspaceId/rooms/$roomId` |
| Send text and attachments | person | group message box (shared composer, group profile) | group page |
| Slash commands, @ mentions | person | group message box menus | group page |
| Ask again | person | Participants panel, member row | panel |
| Participants | person | header slot button, then panel (`participants` panel) | group page |
| Mute, remove, delete, add (in a group) | person | panel menu and Add participant | panel |
| Start a group (Add participant) | person | icon-only control in the header slot of an agent's main chat | main chat page |
| Invitation summary generation | the system, automatically, after an ad-hoc group is created from a main chat | none to trigger; read-only "Invitation context" disclosure in the panel | panel |
| Agent answer with files | agent, by calling `answer_request` with `media` while handling a group request | group message list (and the agent's main chat) | agent turn |
| Approvals and questions for this group's work | person | approval/question cards in the group thread area (same components as the normal chat) and in the agent's chat | group page |
| Helper attention signal | person | attention avatar and "Open <Agent>'s main chat" link in the working line and the header stack | group page |
| Group files in the workspace | person | Library panel (existing) and the agent's main chat | existing screens |
| Delete a chat file everywhere (D26) | person | Library panel Delete, confirmation dialog (`LibraryExplorer`) | Library panel |
| Group message size limit (D27) | operator sets `session_messaging.steer_body`; person and agent are refused above the limit in force | group message box notice area; agent tool error | settings / config |

A backend change without a UI path does not exist in this feature: every server change above has a named screen.
- **Test plan execution**: RED packs QA-R1, QA-R2 (chrome), QA-R3 (Go) and QA-R4 (room source, panel, approvals) are shown failing on a tests-only commit in CI before the corresponding GREEN; CHECK is a second qa-lead instance plus a second instance of each lead role; then the single combined 5-reviewer gate on the combined feature branch; then the integration or standalone candidate, one full CI run, and the UAT rows above on the exact candidate. Written is not executed: SC-001 requires execution evidence per row.

## User-facing documentation

Existing pages read before naming destinations: `docs/group-chats.md` (current text says Add participant is "top-left of a chat header", that approvals and questions during group work belong to the main chat and not the group, that group history scrolls "up to 100 at a time", and that deleting a group removes its files), `docs/using-omnipus-ui.md` (sections "The chat area", "Reading earlier messages", "Who each message is from"), `docs/security.md` ("Approve a request"), `docs/library.md`.

| TODO | UI / behaviour change | User-facing page | Matching update |
|---|---|---|---|
| DOC-001 | The group is the normal chat: formatting, images and files both ways, same message box without session-only controls, 16 attachments, files kept after deletion, agent text is display-only | `docs/group-chats.md` (intro, "Who receives your message", "How agents answer", new "Images and files", "Deleting a group", "Limits in this version") | Describe the screen and message box; replace the statement that a deleted group's files are removed with the D19 rule (journal only; library files stay); remove "Secret requests are not handled" or restate it as "a link to the agent's chat" |
| DOC-002 | Participants button and panel; Add participant moved to the right end; starting a group; invitation context | `docs/group-chats.md` ("Add participant", "Member states", new "Participants panel" and "Invitation context") | Replace "top-left"; document panel contents, Ask again, cost; invitation context: what it is, never posted, readable in the panel, shown as generating or "Invitation context: unavailable", **and the plain statement that anything written in the original chat except recognised keys may appear in it (D18, #1467)** |
| DOC-003 | Cards for this group's work appear in the group, only for work that answers the group; helper, unrelated, mixed-input and two-group cards stay in the agent's chat; attention avatar; stopping happens in the agent's own chat | `docs/group-chats.md` ("Approvals and questions", "How agents answer") | Replace "belong to its main chat, not to the group" with the D8/D15/D23/D28 rule (when one step of an agent answers two groups, the card is in the agent's own chat and both groups show the attention cue and link); say there is no Stop in the group and point to the agent's own chat; document Ask again |
| DOC-004 | A card can be answered in a group, only for work that answers the group | `docs/security.md` ("Approve a request") | Same decision and grant scope wherever answered; one decision wins; a step that also handles other input keeps its cards in the agent's chat |
| DOC-005 | Minimal header: no bar or border; controls at the right end; helper breadcrumb; group Participants control; shared history behaviour; **file cards in every chat** | `docs/using-omnipus-ui.md` ("The chat area", "Who each message is from", "Reading earlier messages", "Sessions", and the existing Retry guidance) | Describe the header controls per chat kind; state that groups use the same list, history, Return to latest and message box; **add: a file card (yours or an agent's, any file type) downloads by mouse or keyboard (Enter); if the file cannot be fetched the card shows an error with Retry, which repeats the same file fetch and is not a message resend or a repeat of the agent's work** |
| DOC-006 | Groups use the normal chat's history behaviour | `docs/group-chats.md` ("Opening a group: Earlier messages aren't shown") | Replace the room-specific scrolling text with the shared behaviour and keep the 10,000-entry snapshot statement |
| DOC-007 | Message `media`, upload by workspace, the group size limit (D27) | `docs/group-chats.md` ("API summary", "Who receives your message"); `docs/settings.md` (`steer_body`) | Add `media` on post and the 503/422 outcomes. Replace the size sentence (today: "up to 65,536 bytes long by default. Your operator can change this with `session_messaging.steer_body`") with: a group message, and an agent's group answer, can be up to 65,536 bytes; an operator can lower this with `session_messaging.steer_body` but not raise it above 65,536 bytes for groups; the error names the limit in force. In `docs/settings.md`, add to `steer_body` that group chats use it only up to 65,536 bytes |
| DOC-008 | Group files are stored like normal chat uploads in two representations and outlive the group; one Library deletion removes a chat file everywhere (D26) | `docs/library.md` ("Ordinary deletes are permanent" and the `.library` paragraph); `docs/group-chats.md` ("Deleting a group"); `docs/using-omnipus-ui.md` ("The chat area", file cards) | Say what is true: files appear in the Library as normal chat uploads do; neither copy is deleted with the group; deleting a chat file in the Library also deletes the copy chats use (the confirmation says so), and every chat that showed it, normal or group, then shows it as unavailable; this cannot be undone; a renamed or moved file keeps this link, a copy does not, and a file moved to another workspace leaves its chat copy behind; uploads from before this version are linked only when they can be matched unambiguously (docs-verifier confirms the page's current statements first) |

The implementing lead drafts these; `docs-verifier` audits them against the actual behaviour (T-52) before landing.

## Traceability Matrix

Columns: requirement, user story, BDD scenarios, tests (placeholders, see Test Implementation Order), UAT rows, documentation TODOs.

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | UAT | Documentation TODO(s) |
|---|---|---|---|---|---|
| FR-001 | US-1 | S-01, S-02, S-03, S-04 | T-31, T-48 | UAT-1 | DOC-001 |
| FR-002 | US-1 | S-05, S-06, S-113, S-129, S-130 | T-31, T-48, T-61 | UAT-2, UAT-3, UAT-33 | DOC-001, DOC-005 |
| FR-003 | US-1 | S-07 | T-30 | UAT-7 | DOC-005 |
| FR-004 | US-1 | S-08 | T-30 | - (covered by S-08 in vitest; low-risk unchanged mark) | none - behaviour unchanged |
| FR-005 | US-1, US-13 | S-09, S-116 | T-30 | UAT-27 | none - removes a wrong cross-chat display, nothing documented |
| FR-006 | US-2 | S-10, S-19 | T-27 | UAT-4 | DOC-001 |
| FR-007 | US-2 | S-10 | T-27 | UAT-4 | DOC-001 |
| FR-008 | US-3 | S-20, S-22, S-24 | T-43 | UAT-2 | DOC-001 |
| FR-010 | US-2 | S-12, S-13 | T-28 | UAT-6 | DOC-001 |
| FR-011 | US-2 | S-14, S-15, S-16 | T-28 | UAT-5 | DOC-001 |
| FR-012 | US-2 | S-19 | T-27 | UAT-4 | none - wording only |
| FR-013 | US-2 | S-17 | T-29 | - (covered in vitest; state names need a connection fault) | DOC-001 |
| FR-014 | US-2, US-3 | S-18, S-21 | T-02, T-29 | UAT-29 | DOC-001 |
| FR-015 | US-13 | S-23, S-108, S-109, S-110, S-111 | T-26 | UAT-27 | none - fixes interference; nothing documented |
| FR-016 | US-3 | S-23, S-28, S-128 | T-04, T-60 | UAT-2, UAT-33 | DOC-001, DOC-008 |
| FR-017 | US-3 | S-22 | T-04 | - (needs library fault; covered in Go) | DOC-001 |
| FR-018 | US-3 | S-25, S-26 | T-03 | UAT-28 | DOC-007 |
| FR-019 | US-3 | S-20, S-27 | T-05 | UAT-2 | DOC-001 |
| FR-020 | US-4 | S-29, S-30, S-31, S-34, S-127 | T-06, T-60 | UAT-3, UAT-33 | DOC-001 |
| FR-021 | US-4 | S-29, S-33 | T-07 | UAT-3 | none - internal ordering, no user-visible rule beyond the outcome |
| FR-022 | US-4 | S-32, S-133, S-137 | T-07 | - (needs fault injection; Go only) | none - internal repair identity |
| FR-023 | US-3, US-4 | S-20, S-29 | T-08 | UAT-2, UAT-3 | DOC-001 |
| FR-024 | US-3 | S-24, S-120 | T-01, T-54, T-57 | UAT-31 | DOC-001 |
| FR-025 | US-14 | S-112, S-113, S-126, S-136 | T-09, T-59, T-60 | UAT-26, UAT-34 | DOC-001, DOC-008 |
| FR-026 | US-3 | S-25, S-135 | T-03, T-04 | UAT-28 | DOC-008 |
| FR-027 | US-5 | S-35, S-37, S-38 | T-33 | UAT-7 | DOC-001 |
| FR-028 | US-5, US-9 | S-36, S-77, S-151 | T-33, T-40 | UAT-11b, UAT-36 | DOC-003 |
| FR-029 | US-6 | S-39, S-49 | T-35 | UAT-17, UAT-21 | DOC-005 |
| FR-030 | US-6 | S-40, S-41, S-42, S-45, S-46, S-47 | T-34, T-47 | UAT-18 | DOC-006 |
| FR-031 | US-6 | S-43, S-44, S-48 | T-34, T-42 | UAT-18 | DOC-006 |
| FR-032 | US-6, US-15 | S-47, S-116 | T-30, T-34 | - (internal store isolation) | none - internal, no visible rule |
| FR-033 | US-7 | S-50, S-51, S-100 | T-36 | UAT-17 | DOC-002 |
| FR-034 | US-7 | S-51, S-58 | T-36 | UAT-17 | DOC-002 |
| FR-035 | US-7 | S-52, S-54, S-55, S-56, S-57 | T-36 | UAT-17 | DOC-002 |
| FR-036 | US-7 | S-53 | T-36 | UAT-17 | DOC-002 |
| FR-037 | US-6, US-7 | S-39 | T-35 | UAT-17 | DOC-002 |
| FR-038 | US-8 | S-59, S-60 | T-44, T-22 | UAT-15 | DOC-002 |
| FR-039 | US-8 | S-61, S-63 | T-18, T-50 | UAT-15 | DOC-002 |
| FR-040 | US-8 | S-67 | T-19 | UAT-30 | DOC-002 |
| FR-041 | US-8 | S-61, S-64, S-65, S-66 | T-18 | UAT-25 | DOC-002 |
| FR-042 | US-8 | S-63, S-69, S-132 | T-20 | UAT-25 | DOC-002 |
| FR-043 | US-8 | S-62, S-131, S-132, S-138, S-139 | T-20 | UAT-16 | DOC-002 |
| FR-044 | US-8 | S-70 | T-22 | UAT-16 | DOC-002 |
| FR-045 | US-8 | S-61, S-122, S-123 | T-18, T-36, T-58 | UAT-15 | DOC-002 |
| FR-046 | US-8 | S-68 | T-21 | UAT-17 | DOC-002 |
| FR-047 | US-8 | S-67 | T-19 | UAT-30 | DOC-002 |
| FR-048 | US-9 | S-71, S-74, S-81 | T-23, T-40, T-49 | UAT-10, UAT-13 | DOC-003, DOC-004 |
| FR-049 | US-9 | S-72, S-74, S-80 | T-13, T-40 | UAT-10, UAT-23 | DOC-004 |
| FR-050 | US-9 | S-74, S-75, S-76 | T-15 | UAT-13, UAT-24 | DOC-003 |
| FR-051 | US-9 | S-73, S-77, S-78 | T-16, T-40 | UAT-11b, UAT-12, UAT-14 | DOC-003 |
| FR-052 | US-9 | S-79, S-83, S-151 | T-16, T-17 | UAT-32, UAT-36 | DOC-003, DOC-004 |
| FR-053 | US-9 | S-82 | T-17 | UAT-12 | none - internal status rule |
| FR-054 | US-9 | S-81 | T-23 | UAT-10 | none - internal delivery rule |
| FR-063 | US-11 | S-98 | T-37 | UAT-19 | DOC-005 |
| FR-064 | US-11 | S-98, S-99 | T-37 | UAT-19 | DOC-005 |
| FR-065 | US-11 | S-101 | T-37 | UAT-19 | none - removes a control that acted on the wrong chat |
| FR-066 | US-11 | S-100, S-102, S-103 | T-37, T-38 | UAT-19, UAT-21 | DOC-005 |
| FR-067 | US-11 | S-36 | T-39 | UAT-11b | none - internal component |
| FR-068 | US-11 | S-99 | T-37 | UAT-19 | DOC-005 |
| FR-069 | US-12 | S-104, S-105, S-106 | T-32, T-56 | UAT-20 | DOC-001 |
| FR-070 | US-12 | S-107 | T-32 | UAT-20 | DOC-001 |
| FR-071 | US-15 | S-114 | T-45 | - (unchanged; covered by regression) | none - unchanged |
| FR-072 | US-15 | S-115 | T-46 | UAT-6 | DOC-001 |
| FR-073 | US-15 | S-117 | T-24, T-25 | UAT-18 | none - behaviour unchanged |
| FR-074 | US-2 | S-118, S-150 | T-54, T-55, T-29, T-65 | UAT-31, UAT-35 | DOC-001, DOC-007 |
| FR-075 | US-4 | S-119, S-150 | T-54, T-65 | UAT-31, UAT-35 | DOC-001, DOC-007 |
| FR-076 | US-14 | S-124, S-125, S-126, S-28, S-143 | T-59, T-62 | UAT-26 | DOC-008 |
| FR-077 | US-3, US-4 | S-127, S-128, S-136 | T-60, T-63 | UAT-33, UAT-34 | DOC-008 |
| FR-078 | US-4 | S-33, S-140, S-141, S-142, S-143, S-144 | T-62 | - (needs fault injection; Go only) | none - internal recovery |
| FR-079 | US-8 | S-61, S-122, S-123 | T-58 | UAT-15 | DOC-002 |
| FR-080 | US-8 | S-131, S-132, S-138, S-139 | T-20 | UAT-16 | DOC-002 |
| FR-081 | US-1 | S-129, S-130 | T-61 | UAT-33 | DOC-005 |
| FR-082 | US-3 | S-121 | T-57 | - (needs a quoted-history fixture; Go only) | DOC-001 |
| FR-083 | US-7 | S-134 | T-36 | UAT-17 | DOC-002 |
| FR-084 | US-4 | S-133, S-137 | T-07 | - (needs a lost receipt; Go only) | none - internal retry rule |
| FR-085 | US-4 | S-33, S-140, S-144 | T-62 | - (needs fault injection; Go only) | none - internal recovery boundary |
| FR-086 | US-14 | S-136, S-146, S-148, S-149 | T-60, T-63 | UAT-34 | DOC-008 |
| FR-087 | US-14 | S-147, S-148 | T-64 | UAT-34 | DOC-008 |

**Completeness check**: FR-001 to FR-087 (FR-009 and FR-055 to FR-062 retired by D21; ids are not reused) each have at least one scenario and one test; scenarios S-01 to S-151 (S-11 and S-84 to S-97 retired by D21, S-145 retired by D26) each appear in the matrix and in the Test Implementation Order (verified by script, see the evidence table of the hand-off); DOC-001 to DOC-008 each appear at least once; each UI or behaviour change traces to a DOC TODO or states why none applies. Contract-first (Hard Constraint #8) is tracked by SC-003 and test T-51 rather than an FR because it has no user-visible scenario.

---

## Open Founder Questions

None open. Founder answers applied in this revision: D21 (no Stop in the group), D22 (attention cue accepted), D23 (cards only for work that answers the group), D24 (one message size limit), D25 (group files stored like normal chat uploads), and on 2026-10-28 (Discord #team-b, "q10a, f6 b, f7 a"):

| ID | Question | Answer | Decision | Where applied |
|---|---|---|---|---|
| Q-R2-1 (Q10) | How should explicit deletion of a chat file work, given its two copies? | A: one explicit Library deletion removes both copies; old chat references show the file as unavailable; for normal and group attachments alike; the shared deletion change and its confirmation and error behaviour are authorised | D26 | Contract table (`LibraryEntry.chat_copy`, `MediaLibraryEntry.library_path`, `deleteLibraryEntry`, move/rename/copy), US-14 AS 14.2, 14.5, 14.6, Edge Cases, FR-025, FR-077, FR-086, FR-087, S-136, S-146 to S-149 (S-145 retired), DS-13, T-60, T-63, T-64, UAT-34, SC-014, DOC-008, A-14 |
| F-6 | How does the operator setting `steer_body` interact with the one group limit? | B: groups follow the setting but never above 65,536 bytes; a larger value is clamped, a smaller one honoured; the contract states the maximum and that a smaller setting lowers it | D27 | Contract table (`RoomMessageRequest`, `RoomEntry`), US-2 AS 2.3, US-4 AS 4.6, Edge Cases, FR-074, FR-075, S-150, DS-1 rows 26-31, T-55, T-65, UAT-35, SC-012, DOC-007, A-3 |
| F-7 | Where does the card go when one step answers two different groups? | A: the agent's own chat only; both groups show the attention animation and link | D28 | US-9 AS 9.10, Edge Cases, FR-028, FR-052, S-83, S-151, DS-8 row 12, T-16, T-17, T-33, T-40, UAT-36, DOC-003 |

## Technical Decisions

Recorded 2026-10-28 on the coordinator's instruction; none needs the founder. TD-1 to TD-3 are architect decisions. TD-4 to TD-7 are the spec's recommendations adopted as defaults: **default, owner may refine during RED with evidence** (a refinement is recorded here with its evidence; it never changes a founder decision).

| ID | Item | Decision | Kind | Applied in |
|---|---|---|---|---|
| TD-1 (A-1) | Summary output longer than 4,096 characters | Cut at the last word boundary at or before 4,096 characters (hard cut when none), status `ready`, cost counted once | Architect decision. Rationale: the model ran and produced usable background; `unavailable` would discard it and still cost money | FR-039, DS-4 rows 13 and 13b, T-18 |
| TD-2 (A-2) | Origin chat with nothing to summarise | No model call; `unavailable` at once; no cost line | Architect decision. Rationale: a call on empty input costs money and invites invented content | FR-039, DS-5 row 10, T-18, T-21 |
| TD-3 (A-13) | Bounds of the private staging of an answer's original bytes | At most 16 files, 100 MB per file (the upload limit); over-limit is a definitive rejection before acceptance; staging outside agent-reachable trees; removed after commit or recorded rejection; kept while accepted, across restarts and the orphan cleanup | Architect decision. Rationale: same bounds as a person's upload; no new limit to document | FR-078 (1), DS-12 rows 17-19, T-62 |
| TD-4 (A-4, A-9) | The same file listed twice in a message or answer | Accepted as listed and shown once per listed reference; retention counted once per (entry, reference) | Default, owner may refine during RED with evidence (backend-lead) | DS-1 row 32, DS-12 row 16, T-03, T-06 |
| TD-6 (A-6) | Participants tooltip wording | "Participants" | Default, owner may refine during RED with evidence (frontend-lead) | S-100, T-37 |
| TD-7 (A-7) | Participants panel on narrow widths | Same as the other registered side panels; no new behaviour | Default, owner may refine during RED with evidence (frontend-lead) | S-49, T-35 |

## Grill Round 1 Dispositions

Report: `/workspace/logs/1449/1449-spec-review.md` (verdict BLOCK: 1 Blocking, 10 Important, 4 Minor). Each finding was checked against the code first. All are accepted; none reopens a founder decision (D1-D25 intact).

| Finding | Disposition | Evidence read | Spec fix |
|---|---|---|---|
| B1 attachments have no retention owner | ACCEPTED | `pkg/media/library/library.go::OrphanGC` removes refcount-0 entries older than `DefaultOrphanAge` (30 days); `pkg/gateway/gateway_boot.go::startBackgroundServices` runs it hourly; `pkg/agent/media_present.go::sessionRefcounter` counts sessions only | FR-076, DS-11, S-124 to S-126, T-59, SC-013, API and Data, impact row |
| I1 media store vs Library panel conflated | ACCEPTED | `pkg/gateway/rest_uploads.go::finishWorkspaceUpload` + `stageWorkspaceUploadCopy` (second write under the workspace's `.library`); `handleLibraryEntryDelete` -> `pkg/library/transfer.go::Root.Delete` removes the Library file only | FR-016, FR-025, FR-077, S-30, S-113, S-127, S-128, S-136, US-14 AS 14.2/14.3, DOC-008, T-60, UAT-33 |
| I2 copy changes answer identity; no durable intent | ACCEPTED | `pkg/rooms/answers.go::DeliverAnswer`, `unmirroredAnswer` (matches `Entry.Text == a.Content` on entries only); `Library.uploadInternal` mints a new id per copy | FR-021, FR-022, FR-078, S-33, S-133, DS-12, T-07 |
| I3 REST-only text cannot refresh | ACCEPTED | `contracts/components/schemas/RoomMetaFrame.yaml` has no text; snapshot carries no summary status | Targeted `getRoom` refetch on `ready` and after reconnect; FR-045, FR-079, S-61 reworded, S-122, S-123, T-58, API and Data. No text-bearing frame |
| I4 delivery has no commit boundary | ACCEPTED | `pkg/rooms/fanout.go::prepareAdmission` runs before `pkg/agent/room_request.go::commitRoomRequest` | FR-043, FR-080, S-131, S-132, DS-4 rows 15-17, T-20 (D20 kept: no waiting) |
| I5 file rows lack download/error | ACCEPTED | `src/components/chat/AttachmentCard.tsx::AttachmentCard` takes no download URL or action | FR-002, FR-081, AS 1.3, S-129, S-130, T-61. Scope stated: shared rows, the normal chat gains the behaviour too |
| I6 403 assertions lack a caller baseline | ACCEPTED | `pkg/gateway/rest_uploads.go` media route passes `media.WithCallerWorkspace(workspaceID)` from its own URL (line `a.serveMedia(... WithCallerWorkspace(workspaceID) ...)`) | FR-026, error table row removed, DS-10 row 6, T-03, T-04, S-135, Security text |
| I7 500-entry paging fixture | ACCEPTED | `pkg/rooms/types.go::maxSnapshotEntries = 10000` | S-40, UAT-18, T-47, DS-9 rows 11-12 (10,005 entries) |
| I8 unit test cannot prove remote-image block | ACCEPTED | `pkg/gateway/embed.go::spaBaseContentSecurityPolicy` carries the boundary | S-106, FR-069, T-32 narrowed, T-56 (real browser, positive control), UAT-20 |
| I9 deleting a pick does not exclude the member | ACCEPTED | `pkg/rooms/messages.go::userAddressees`: empty mentions then default to every unmuted member; `docs/group-chats.md` "Who receives your message" | AS 2.5, FR-011, S-15, T-28, UAT-5 |
| I10 FR-024 has no matching test | ACCEPTED | Matrix row pointed at the count scenario and T-01 only | FR-024 rewritten, FR-082, S-120, S-121, T-57, DS-1 rows 24-25 |
| M1 `stops` stream | ACCEPTED | requirements D21 | API and Data "Stored" rewritten; no stop persistence |
| M2 malformed rows and dangling IDs | ACCEPTED | scan of the file | rows moved into DS-1 (rows 18-23); SC-001 states S-11 retired; T-51 cites SC-003; UAT-31 says ASCII |
| M3 UAT-15 password promise | ACCEPTED | D18, FR-047 | UAT-15 follows UAT-30 scope and excludes unregistered prose passwords |
| M4 panel focus return | ACCEPTED | `src/components/panel-shell/SidePanelShell.tsx` uses `requestClose('chat')` for the close button; Escape returns to the trigger | Accessibility text, FR-083, S-134, T-36 |
| Technical ambiguity rows A-1, A-2 | DECIDED (architect, 2026-10-28) | - | TD-1 and TD-2 under "Technical Decisions"; FR-039, DS-4 rows 13/13b and DS-5 row 10 now carry the decided values |

## Grill Round 2 Dispositions

Report: `/workspace/logs/1449/1449-spec-review-round2.md` (final round; verdict BLOCK: 1 Blocking, 4 Important, 1 Minor). This is the last fix round.

| Finding | Disposition | Evidence read | Spec fix |
|---|---|---|---|
| R2-B1 answer intent is not a recoverable publication transaction | ACCEPTED, fixed without a scope change; no founder decision needed (S-33 is kept, not weakened) | `pkg/media/library/library.go::uploadInternal` mints a UUID per call; `pkg/gateway/rest_uploads.go::stageWorkspaceUploadCopy` picks collision-suffixed names; `pkg/agent/upload_workpath.go::uploadWorkPathRegistry` is in-memory; `pkg/rooms/service.go::recover` repairs only appended answers | FR-078 rewritten as a lifecycle (accepted with immutable staged bytes, publishing with preallocated identities and create-or-adopt, committed, rejected before the definitive error, retryable result, restart completion that never replays model work); FR-085; S-31, S-33, S-140 to S-144; DS-12; T-62; A-13 (staging bounds). Cost stated: private staging storage and a create-or-adopt publication API in `pkg/media/library` |
| R2-I1 Library deletion promises conflict | ACCEPTED, resolved by founder decision D26 (Q-R2-1: A, 2026-10-28) | `LibraryExplorer.tsx::deleteMutation` calls `deleteLibraryEntry` (Library file only); `deleteWorkspaceMedia` exists with no UI caller | Q-R2-1 asked and answered (D26): one Library deletion removes both copies, normal and group alike. FR-025, FR-077, new FR-086 and FR-087, AS 14.2, 14.5, 14.6, Edge Cases, DOC-008; S-136 (chosen), S-145 retired, S-146 to S-149; DS-13; T-60, T-63, T-64; UAT-34; SC-014; contract changes for backend-lead (`LibraryEntry.chat_copy`, `MediaLibraryEntry.library_path`, `deleteLibraryEntry`); explicit deletion is never undone by repair |
| R2-I2 identity lifetime | ACCEPTED | `pkg/rooms/answers.go::unmirroredAnswer` matches only while unmirrored | FR-084 (equal call while incomplete completes the first; after full completion it is a new answer, as text-only today), S-137, S-133, DS-12 rows 8-10, T-07 |
| R2-I3 reservation release on retryable terminal states | ACCEPTED | `pkg/rooms/types.go::terminal` includes unavailable; `fanout.go::admittable` re-admits unavailable/unknown | FR-043, FR-080 (explicit transitions; read error is not proof of absence; skipped request never gets it later), S-138, S-139, DS-4 rows 17-21, T-20 |
| R2-I4 missing doc TODO for normal-chat file rows | ACCEPTED | `docs/using-omnipus-ui.md` "The chat area" has no file-card text | DOC-005 extended (download by mouse/keyboard, inline error, Retry repeats the fetch, not a message resend), T-52 audit text, FR-002 row links DOC-005 |
| R2-M1 collector oracle does not name the copy | ACCEPTED | `OrphanGC` acts on media manifest entries; the Library file is a separate workspace file | S-28, S-126, DS-11 rows 4, 8, 9, T-59, SC-013 assert each copy; deleted-paused-group fixture without a consuming-session owner |

## Assumptions

- GitNexus was not available in this session; the codebase context uses Read/Grep and the ADR's verified statements, labelled as such. Implementing leads re-run `impact` before every edit.
- The interview-me state file (`.<spec-name>.interview.json`) does not exist for this feature; the requirements document's decisions log is the interview output (founder interview in the team-lead chat, 2026-10-27/28).
- `ChatHeaderSlot` and the minimal header for normal and helper chats exist only on `feature/1449-minimal-header` (`1d7886ecc`, `ede852eb2`) and are assumed to land before or with this feature; this spec does not redo that work.
- Spec-chosen values (ADR delegated them to the spec): summary time limit 45 s (ADR ceiling 60 s), summary input 40 entries / 16 KiB (ADR ceiling), about 200 words, text maximum 4,096; HTTP codes 422 for "nothing to send", media-reference failures and over-limit text, 400 for schema violations, 503 for library unavailable. The message size limit is at most 65,536 bytes, the existing server default (D24), lowered but never raised by `session_messaging.steer_body` (D27).
- Removed or merged template sections: none removed. "Success Criteria" and "Open Founder Questions" are added. The contract-first requirement is a success criterion (SC-003) rather than an FR.
- Per-OS differences: none expected (browser and Go server behaviour only); the SPA embed stub trap applies to local Go compiles (root `CLAUDE.md`).

## Clarifications

### 2026-10-28

- Q: Does the group need its own header bar or member list? -> A: No; D13/R8/R9 and the approved wireframe (requirements decisions log D13).
- Q: Do helper approvals show in the group? -> A: No, only the attention avatar and link (D15; ADR 11.2 C-I7).
- Q: Does the group have a Stop? -> A: No; stopping happens in each agent's own chat (D21; supersedes the Stop parts of D4 and D16 and the ADR's Stop decisions).
- Q: Is it accepted that the attention cue can light up for unrelated waiting? -> A: Yes (D22).
- Q: When does a card appear in the group? -> A: Only for work that answers the group; a step that also handles other input keeps its cards in the agent's chat (D23; replaces the earlier "group wins over web" assumption).
- Q: What is the group message size limit? -> A: One limit in contract and server (D24): at most 65,536 bytes; the operator setting `session_messaging.steer_body` can lower it, a larger value is clamped (D27).
- Q: Where are group files stored? -> A: Exactly like normal chat uploads, in the workspace media library, visible and deletable in the Library, surviving group deletion (D25).
- Q: Where does Add participant appear? -> A: Main chats only (D17).
- Q: Can passwords in prose reach the invitation summary? -> A: Yes, accepted for #1449 (D18); tracked as #1467.
- Q: Are files deleted with a group? -> A: Never (D19).
- Q: What if the summary cannot be made? -> A: Start without it, panel shows "Invitation context: unavailable", the first message is never held (D20).
- Q: Where do group files live? -> A: In the workspace library, same read posture as the normal chat (ADR 11.2 C-I1).
- Q: What does deleting a chat file in the Library do? -> A: One explicit deletion removes both copies (Library file and chat copy); chats show the file as unavailable; same for normal and group attachments; group deletion still never deletes files (D26).
- Q: Where does a card go when one step answers two different groups? -> A: The agent's own chat only; both groups show the attention animation and link (D28).
