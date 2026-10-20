# Feature Specification: Chat history paging

Status: Draft
Created: 2026-10-21
Issue: https://github.com/elicify-ai/omnipus/issues/1266
Size: Feature
Author: architect
Repository/worktree: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266
Source revision: `5c2753da7fbf4caa728e66e3c7a6a89c3712690d`
Branch: `feature/1266-chat-history-paging`

## Discovery, authority and scope

Open a long conversation at its recent end, not by replaying its lifetime. Load approximately four chat screens initially. While the person scrolls upward, fetch approximately two more screens before they reach the loaded beginning, without moving what they are reading. Earlier history remains saved and reachable, including history before `/clear` and a directly addressed old message. This is a display/read change, not a change to the agent's model context or execution.

The issue's **Founder's model**, **Turning a screen into a number of messages**, **Pieces**, and **Done when** are the confirmed written interview for this Draft. Its 2026-10-11 scheduling note says after session-core; writing this specification does not authorize an earlier implementation or landing. No additional interview is fabricated. Product choices not settled there are Q1–Q4 in the final section. Their recommendations are **proposals, not founder decisions**; resolve them before approving this spec or commissioning implementation. This dispatch produces only this file, not a separate architecture decision record, contracts, implementation, tests or runtime acceptance.

| Dimension | Scope |
|---|---|
| Actors | A signed-in person reading main, existing extra, channel, scheduled, task or helper conversations; the existing gateway serving those conversations; an authorized API client reading a page. |
| Problem | A months-long permanent conversation makes opening, transferring and reconstructing history proportional to its lifetime. Rendering fewer elements alone does not reduce the server read or replay. |
| In scope | Recent-page open, upward prefetch, stable scroll position, explicit crossing of clear dividers, old-target neighborhood reads, shared REST/snapshot paging, unchanged live catch-up, error/Retry, cancellation of obsolete reads, bounded opening work and real long-session UI verification. |
| Out of scope | Changing model context/recall/retention, rewriting accepted transcript bytes, new chat creation, a room-paging redesign, new agent tools, new permissions, a new history content store or external service. Full-text message search is not silently included: Q2 settles its producer/reachability scope. |
| Priority | P1-high, matching the issue. Normal reading should not depend on the age of a standing conversation. |
| Human acceptance | Open a genuine long session in the shipped UI, read upward while it receives live activity, cross a clear divider, follow an old-target link, interrupt a history request and recover; retain screenshots, geometry and network evidence on the exact tested commit. |

## Available Reference Patterns

The optional Go implementation reference overview at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/reference/go-implementation/00-overview.md` is **absent at the source revision**. A controlled inventory found the existing `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/reference/built-in-tools.md`, but no Go-pattern directory. Do not invent a reference implementation. Reuse the actual archive and Train 3 patterns below.

| Reference | Pattern and adaptation |
|---|---|
| C2–C5 below | One append-only archive, stable internal addresses, merge-on-read corrections and content-free seek indexes. Extend those owners; the existing model-slot ordinal is not a chat-row sequence. |
| T1–T3 below, read from `origin/work/train3-approvals-rooms` at `573c3674462650ce0f7af8e3e02de0fca50ae295` | Ascending page contents selected backward, exclusive cursor, truthful continuation, one request per boundary, stale-response fences, current data winning overlaps and anchored prepend. Reuse semantics, not the room's in-memory whole-history reader or its 24-pixel trigger. |
| T2/T3, including `b3b437a354fb5588925bc20f228573aed7514e0a` | Anchor the same entry through **every** loading/hint/Retry layout, include negative height deltas and disable native scroll anchoring. Adapt to a virtualized list and late image measurement rather than copying a non-virtualized component. |

## Existing Codebase Context

### Source register — read in this task

Citations below refer to this register. Paths are absolute; a symbol after `::` identifies what was read, not a line number. Statements marked **target** describe new work rather than existing behavior.

| Key | Evidence (`file::symbol` or contract section) | Verified current behavior |
|---|---|---|
| C1 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_view.go`::ReadTranscriptView / sinceLastClear / IsClearMarker / TranscriptView.HasEarlier; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/input_disposition.go`::ReadTranscriptWithDispositions / InputDispositions | Reads the full merged transcript and all input dispositions, then cuts at the last exact clear marker. HasEarlier currently describes that cut, not ordinary paging. |
| C2 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_effects.go`::readTranscriptMerged / applyToolCallEffects / transcriptPartitions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/unified_write.go`::ReadTranscript / deriveCancelTruncation | Reads whole partitions; later settle/projection/retract effects change earlier tool calls on read. Cancellation labels the nearest preceding applicable assistant entry. Returning raw recent lines is not equivalent. |
| C3 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/archive_day_store.go`::Append / ReadAtRaw / partitionForAppendLocked / partitionPathForKeyLocked; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/archive_record.go`::ArchiveAddress / ArchiveRecord / ToolCallEffect | Physical addresses are partition key + encoded-byte offset + checked entry ID, disk-only. Rollover uses server append day, not the entry timestamp. ReadAtRaw seeks one bounded record. Append admits an encoded envelope up to 8 MiB. |
| C4 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/ordinal_index.go`::ordinalRow / OrdinalCount / ordinalCountLocked; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_indexed.go`::appendTranscriptLocked | The existing ordinal index covers **model placements**, excluding chat-only records until consumption. It cannot number chat history. The chat appender mints missing IDs on new writes. |
| C5 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_partition.go`::transcriptPartitionPaths; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/daypartition.go`::PartitionStore.ReadMessages / TranscriptEntry / ToolCall; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/unified_conv_transcript.go`::convConvergeSessionTranscript / convNormalizeChatObject | UnifiedStore has a partition lister and boot conversion. The older PartitionStore reader is not the gateway's current history reader. Legacy membership normalization belongs to conversion, not a new paging-time legacy store. |
| C6 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/rest_sessions.go`::getSessionMessages / getSession / writeSessionDetail / withWireJudgeVerdict / jsonSessionStorageError | Messages returns a bare array plus X-Has-Earlier-Messages. Detail includes messages and optional has_earlier_messages. Both read C1. Messages projects judge verdicts specially. Storage errors are visible and redact internal causes. |
| C7 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/websocket_replay.go`::handleAttachSessionWithAck / attachReadTranscript / sendSnapshot / sendIncremental / replayTranscript / recordEmittedIDs / failCatchUp / resumeLiveSession | Bind/hold first; read history before sending the wipe; replay, projection, catch_up_complete, release live hold, goal rehydrate, attention ack. Incremental uses journal frames, not the transcript reader. Failed rebuild must not acknowledge catch-up. |
| C8 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/replay.go`::streamReplay / emitFlatToolCall / emitSpawnParentToolCall / dispatchSpecialEntry / dispatchPersistedSubagentStart / dispatchPersistedSubagentFrames; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/ws_hub_projection.go`::activeTurnProjection / projectionFrames | One persisted tool call emits its start and applicable result together. Still-running delegates withhold a result; persisted lifecycle entries determine real state. Replay handles special rows, cancellation, questions and a single replay done. Projection excludes already-emitted IDs and does not reconstruct orphan results. |
| C9 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/ws_session_hub.go`::sessionHub / hubRegistry / journalEntry / retention constants | Live seq is transport order; the journal is limited to 2,048 frames / 1 MiB per hub, with a 32 MiB global journal budget. Projection has a separate 2 MiB target with documented current-item/running-start exceptions (C8). These are not hard limits on total snapshot bytes. |
| C10 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.tsx`::ChatScreen / VirtualizedMessageListInner / VirtualizerAutoFollow / PlainMessageList; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ClearContextDivider.tsx`::ClearContextDivider / EarlierMessagesNote | Measured historical outer rows, estimate 80px, overscan 5; separate streaming tail and plain-list fallback. Tool cards can live **inside** one outer row. REST and replay compete under overwrite guards. Earlier-clear text is a note, not a pager. |
| C11 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/lib/api/sessions.ts`::fetchSessionMessages / fetchSessionDetail / parseWireMessageList / rawToMessage / sessionMessagesHasEarlier; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatThread.tsx`::ChatThread | Adapter expects a bare array, degrades malformed items visibly, and moves the header through a side channel. There is another messages consumer besides ChatScreen. |
| C12 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/session.ts`::applyMessageArray / bakeToolCallsByOwner / stampToolCallOffset; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/messages.ts`::MAX_MESSAGES_PER_SESSION / getMessages / evictMessageFromBucket | Current 500-message ring drops the oldest entries. Blindly prepending into it will discard fetched history. Existing duplicate-message projection keeps the last occurrence; owned tool calls carry stable text offsets. |
| C13 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/slices/catchup-frames.ts`::handleSessionSnapshotFrame / handleCatchUpCompleteFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/cursor.ts`::applySnapshotHistoryWipe / gateFrameBySeq / insertHistoryMessageId | Snapshot wipes history, not pending input/control state. catch_up_complete is the definitive successful cursor commit. History positions must not replace this cursor. |
| C14 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/clear-refetch.ts`::runClearRefetch / armClearRefresh / flushHeldClearProjection; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/has-earlier.ts`::recordSessionHasEarlier | Clear refresh is owned by a particular send/operation, defers application while busy, and retains its operation through read failure. Paging must not treat any observed old marker as confirmation of a newer clear. |
| C15 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/main_session.go`::gateMainRead / mainSessionAllowed / resolveMainSessionForRead; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/rest.go`::withAuth / jsonErr; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/gateway_boot.go` (session-route registrations); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/auth.go`::checkBearerAuth | Session REST routes use withAuth; main eligibility is enforced separately, including hidden/removed pairs. No separate per-user session-read ownership predicate was found in these read paths. Do not describe the main gate as such a predicate or invent one. |
| C16 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/search/SearchModal.tsx`::SearchModal; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/useSelectSession.ts`::useSelectSession; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/session.ts`::attachToSession | Existing Sessions search selects sessions by title/workspace/agent, not transcript message hits. No target-message navigation parameter is consumed on the inspected chat/session routes. Message search is not an established producer of a message ID. Q2 is a real scope question. |
| C17 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/openapi.yaml`::getSession / getSessionMessages; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionDetail.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/Message.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ReplayMessageFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ToolCallStartFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ToolCallResultFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/asyncapi.yaml`::AttachSessionFrame / SessionSnapshotFrame / CatchUpCompleteFrame | Current contracts have no chat-page request/envelope. Snapshot describes a full replay. Shared WS component files have hand-synchronized inline mirrors. Tool-start/result frames lack an owning message ID, so cropped replay cannot safely rely on preceding-message heuristics alone. |
| T1 | **Git object, not landed:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/RoomHistoryResponse.yaml`::RoomHistoryResponse and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/rooms/history.go`::History / historyPage at Train 3 | entries ascending; has_older iff another eligible entry; next_before_seq iff has_older, equal to first entry seq; exclusive before_seq. Default 100, server range 1..1000. Reader slices room memory, not disk. |
| T2 | **Git object:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/rooms/RoomTranscript.tsx`::RoomTranscript / captureAnchor / contentOffsetOf and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/rooms.ts`::loadOlder / pageContradictsContinuation at Train 3 | Current-reader anchoring, one request, invalidated stale responses, visible failure/Retry and continuation validation. |
| T3 | `b3b437a354fb5588925bc20f228573aed7514e0a`; **Git object:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/rooms/RoomTranscript.chrome-anchor.test.tsx` | Regression fixture accounts for loader/hint/Retry heights and negative deltas; commit records a 35.46875px browser drift. Those receipts/tests were read, not rerun or independently certified here. |

### Architecture constraints and evidence quality

The as-is walkthrough and extension assessment were read at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/architecture/AS-IS-architecture.md` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/architecture/plugin-extensibility-assessment.md`. They are dated context; current C1–C17 win. The 180-file ADR title catalog was inspected to identify applicable decisions, not treated as proof of runtime behavior.

| Existing decision, cited by title | Paging constraint |
|---|---|
| **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::D2 / D8 / U2-ARCHSHAPE | One canonical archive; private model payload, source and physical marks never on the public wire; clear advances display/context without deleting history; saved chats go through the existing cutover. |
| **UI-independent turns and session-bound webchat streaming**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/architecture/ADR-082-ui-independent-turns-and-session-bound-streaming.md`::D1–D5 | Switching, aborting a history request or losing a viewer stops no agent work. Preserve session-bound live updates and actual active-turn state. Its older timestamp-cursor wording is superseded by current C7/C13, not restored. |
| Root rules, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/CLAUDE.md`::Hard Constraints / Definition of Done; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/CLAUDE.md`::Two independent 64-shard lock pools | Pure Go/file storage, embedded SPA, generated contracts first, no new runtime dependency, existing lock order and exact-commit UAT. |

GitNexus MCP discovery returned **No matching deferred tools found**. No graph query, impact result or detect_changes result is claimed. Direct caller searches found C1's three gateway callers (REST messages, detail and snapshot), C11's ChatScreen/ChatThread/clear-refresh consumers and C7's snapshot/incremental split. Search controls found known ReadTranscriptView calls before absence conclusions. Jev tools were also unavailable; no external judgment is claimed.

### Impact assessment — inferred from caller sweeps, not a graph result

| Planned modification | Risk and direct dependents | Indirect dependents / required checks |
|---|---|---|
| Add bounded page selection beside C1/C2; extend archive-derived indexing | High, inferred: C3/C4 append/effect/disposition readers and clear/cancel projection. Do not redefine full ReadTranscript as a page. | Recall/model readers remain full or independently windowed; archive bytes unchanged; rollover, restart, corrupt-storage and lock tests. |
| C6 messages and detail response shape | High, inferred: C11 adapters, ChatScreen, ChatThread and clear refresh; generated Go/TS schemas. | Workspace/session entry, read-only/removed-agent screens, verdict/special-row parity, CLI/API consumers documented together. |
| C7 attachReadTranscript / sendSnapshot / replayTranscript | High, inferred: C8 replay analysis, C13 replay reducer, emitted-ID projection filter, active-turn/control state. | Existing incremental journal invariants, attention acknowledgement and exact-commit browser reconnect UAT. |
| C10 virtualized/plain lists and C12 bounded state | High, inferred: measured keys, auto-follow, live/historical tool ownership, pending tails. | Late images, manual scrolling while loading, no-observer fallback, session switches, below/above cap traversal. Extract paging/anchor logic rather than expanding the 3,267-line ChatScreen file. |

Manual execution flows: session selection → attach/bind → snapshot page or incremental journal → live stream; history threshold → REST page → anchored prepend; clear send → operation-owned refresh → fresh page; target navigation → neighborhood page → bidirectional browsing. These span session storage, gateway contracts/replay and chat UI/state. They are manual maps, **not GitNexus process or cluster names**. No production symbol is edited by this Draft.

## User Stories & Acceptance Criteria

### User Story 1 — Open a long conversation quickly (Priority: P1)

A reader opens a standing conversation and sees its newest conversation promptly, including truthful running tools and controls, without first loading months of messages.

**Why this priority:** it removes the issue's primary opening cost. **Independent Test:** open the same recent suffix with a short and a long retained prefix and compare opening work and the visible suffix.

1. **Given** a long conversation, **When** it is opened, **Then** approximately four screens of newest conversation appear at the bottom and earlier history is available without replaying the whole archive.
2. **Given** an empty, short or previously cleared conversation, **When** it is opened, **Then** it truthfully shows the available recent content, stops at the latest clear boundary and does not imply missing content is deleted.
3. **Given** conversation storage cannot be read, **When** opening is attempted, **Then** the existing view is retained and an actionable error offers a retry without falsely completing the open.
4. **Given** a running conversation and an interrupted browser connection, **When** it reconnects, **Then** already displayed content is not duplicated and current tools, approvals, questions and live progress remain correct.

### User Story 2 — Read upward without waiting or losing place (Priority: P1)

A reader scrolls toward older work. More history arrives before the loaded beginning, while the same visible message stays at the same position even if an image expands or live output continues below.

**Why this priority:** a fast first page alone does not deliver the founder's endless-reading model. **Independent Test:** read upward through a deterministic variable-height conversation with delayed pages and simultaneous live appends.

1. **Given** earlier history exists, **When** the reader comes within two visible chat heights of the loaded beginning, **Then** one background page of about two screens is requested, sized from measured drawn rows within the 20..100 request bounds.
2. **Given** a pending earlier page, **When** it arrives, **Then** the reader's current message and position are preserved and overlapping messages/tools appear only once.
3. **Given** an earlier page fails, **When** the failure is received, **Then** current history and reading position remain, an inline error and Retry are available, and sending is not disabled by the history failure.
4. **Given** a history request in progress, **When** the person changes conversations, **Then** its late response or failure cannot change the new conversation.
5. **Given** the loaded window reaches its selected memory policy boundary, **When** reading continues, **Then** older content remains reachable and the reader is never silently bounced back to the newest messages. The lifetime policy is Q1, not an unstated assumption.

### User Story 3 — Read across a clear or jump to an old target (Priority: P1)

A person deliberately opens history before a clear divider or follows a known old message. The requested neighborhood is reachable directly, without loading every intervening page or changing what the agent remembers.

**Why this priority:** both are explicit issue requirements and prevent hidden history from becoming unreachable. **Independent Test:** start after two clears; open earlier history and independently open a target near the beginning.

1. **Given** earlier history is behind a clear divider, **When** the person chooses Show earlier messages, **Then** the preceding page is added through the normal paging mechanism with the divider and scroll position preserved.
2. **Given** an authorized old target, **When** its message link is opened, **Then** a neighborhood containing it and available rows on both sides appears and the target is highlighted without first walking back from the tail.
3. **Given** a missing, expired or inaccessible target, **When** its link is opened, **Then** no wrong conversation or substitute message is shown and the person gets a truthful recovery choice.
4. **Given** all retained earlier history is loaded, **When** reading reaches the beginning, **Then** no additional fetch loop, empty gap or active earlier-history control remains.

### User Story 4 — Trust the history boundary and its evidence (Priority: P1)

An operator or client can read pages only through existing authorized conversation access, distinguish failures from empty history, and verify resource use on a real long conversation.

**Why this priority:** cropped history is incorrect if it leaks another conversation, hides errors or merely moves lifetime work to a different request. **Independent Test:** compare REST and live-open history, hostile inputs, resource counts and independent UI evidence.

1. **Given** authorized conversation access, **When** a page is requested, **Then** the same ordered, corrected history is returned through each history carrier and opening work does not grow with an unrelated retained prefix.
2. **Given** malformed paging inputs or a position from another conversation, **When** a page is requested, **Then** a documented refusal returns no unauthorized history and does not silently fall back to the newest page.
3. **Given** a hidden, deleted or unauthorized conversation, **When** any paging direction or target lookup is requested, **Then** existing access refusal applies before looking up its history.
4. **Given** a real long-session acceptance run, **When** the evidence is evaluated, **Then** opening cost, replay size, anchor drift, request failures and memory behavior are measurable against the approved targets, not inferred from green unit tests.

## Behavioral Contract

| When | Then |
|---|---|
| A conversation opens normally | Show the recent end, approximately four chat screens, then live updates. |
| The reader approaches the loaded beginning | Load ahead using distance in pixels and row-height-based sizing, not a fixed message-index trigger. |
| Older content arrives or content above the reader changes height | Preserve the same current reading position; do not force a bottom jump. |
| A clear boundary is reached for the first time | Keep it closed until the reader deliberately asks to see earlier messages. Opening old display history does not restore model context. |
| A history request fails or contradicts its continuation | Keep displayed content; explain the failure and offer Retry of that boundary. |
| The session changes or a clear establishes a new view | Old reads cannot change the new view, its flags or its error state. |
| A known old message is opened | Load its neighborhood directly; provide a route back to the latest messages. |
| No more retained messages exist | Stop fetching; an empty session remains a valid empty conversation. |

## Edge Cases

| Boundary | Expected behavior |
|---|---|
| 0, 1, 19, 20, 21, 99, 100 or 101 ordinary rows | Terminal flags reflect actual earlier/later entries, never page fullness alone. Fewer than 20 may return at a clear, archive end or complete-group/source-group boundary. |
| Consecutive clears, clear as first/last entry, clear during fetch | Exact divider identity; explicit crossing; old requests fenced by view generation. |
| Late/out-of-order timestamps or non-sortable IDs | Preserve archive acceptance order, not timestamp/lexical ID order. |
| Midnight, restart, derived-index rebuild or retention | Existing retained positions survive rollover/restart/rebuild; expired positions visibly fail instead of pointing at different rows. |
| Tool-only entry, many calls in one entry, late settle, retract, cancellation | Whole owning groups, current merged state, no orphan result, no fabricated completion. Q3 settles the oversized-group limit conflict. |
| Same tool/message ID on multiple legacy records | Position identifies the occurrence; existing last-wins presentation rules and a warning are preserved, not guessed by text equality. |
| Tall image, diagram, table, collapsed/expanded tool or font/width change | Remeasure; anchored reader stays put; estimate updates without a fetch storm. |
| Tiny/hidden/zero-height viewport; missing observation support | Do not divide by zero or walk history while hidden. Offer an explicit earlier action when automatic geometry is unavailable. |
| Network timeout, invalid response metadata, storage/index failure | No successful empty-page substitution; visible Retry; existing rows and continuation retained. |
| Main, existing extra, legacy-converted, root, child, native/external worker | Same page semantics under the addressed session's existing access and capability rules; no parent-history graft or newly permitted clear. |

## Explicit Non-Behaviors & Safeguards

### Qualitative prohibitions

The system must not fetch the lifetime transcript and slice it, replay history through the live sequence gate, use a timestamp or transport seq as a history cursor, renumber records after append/clear, or cache every standing session's lifetime contents. These would preserve the original cost or corrupt ordering (C1–C4/C9).

The system must not silently drop tool-only rows, make a completed historical tool look running, complete a genuinely running call, join assistant text across a steer, clear an approval, acknowledge attention because of background history reading, restore model context, stop work on navigation, automatically cross a fresh clear boundary, or replace the whole chat with an older-page failure (C7–C15).

No new tool registration is required: this is existing chat/API reachability, not a newly callable agent capability. No room contract or room limit is changed. No source/route/model payload, raw address, partition name or byte offset is exposed in a public cursor/message/error or the new history diagnostics. Existing privileged storage-error cause logging (C6) is not silently redesigned by this feature.

### Machine-verifiable constraints

| Area | Observable constraint |
|---|---|
| Requests | History limit is an integer 20..100, default 40. Each explicitly supplied paging/target field has one nonempty value; unknown/null-like values never mean omission. Clamp the browser's estimate, **reject** bad external input. |
| Continuation | Page order is ascending acceptance order, cursors exclusive. has_earlier iff another eligible retained group exists before the returned range; next_before iff has_earlier. Symmetric has_later/next_after for forward/around reads. Empty page has no continuation cursor. |
| Failures | HTTP 400 for invalid syntax/range/mode combination; HTTP 404 with `session not found` for an inaccessible/missing session; HTTP 404 with `history position not found` for an otherwise authorized but unknown/foreign/expired position; HTTP 404 with `message not found` for an authorized missing target. See technical validation precedence below. |
| Retry | An earlier-page error retains exactly the old content, continuation and live cursor; no automatic retry storm. Retry repeats the failed direction/boundary, recalculating only the bounded row count if geometry changed. |
| Geometry | Absent user movement, the chosen visible anchor's offset differs by at most 1 CSS pixel after a committed prepend/control transition/settled remeasurement in the reference browser run. |
| Isolation | One in-flight history request per active view and direction; same-boundary triggers coalesce. No stale response writes after switch, clear replacement, target replacement or snapshot generation change. |
| Bounded opening | No full-transcript content scan in a healthy indexed page read. Replay only selected groups plus bounded relevant context and existing active state; never the omitted prefix. Q3 qualifies the exceptional atomic-group row count. |
| Lifetime memory | Q1 must select the policy; a 500-entry oldest-drop ring must not immediately discard prepended rows. A DOM-node count is not a heap-memory claim. |
| Performance | Q4 proposes an explicit acceptance envelope. Numerical targets below are not measured results or silent founder-approved service guarantees. |

### Conservative type design

Use generated wire types and existing domain ArchiveAddress/ToolCallID where their invariants apply. A public history position is an opaque string, not an alias of the model ordinal or WS cursor. In-memory anchors/request generations are local state, clearly not wire formats. Do not create nominal wrappers with no additional invariant.

## Prerequisites

| Topic | Requirement |
|---|---|
| Hardware/OS | Existing supported Linux, macOS or Windows installation; no new kernel or hardware prerequisite. Reference performance machine/browser is recorded in the acceptance receipt (Q4). |
| Runtime | Existing standalone pure-Go binary and its embedded web app. Development baseline is Go 1.26.9; React 19, existing query/virtual-list libraries and Vitest 4.1.11 are recorded in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/go.mod` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/package.json`. No new runtime dependency. |
| Services/accounts | Existing configured gateway and authorized account. Paging itself needs no model call, network service, database server or extra credential. Agent-driven UAT uses the root-mandated provider/model if a live turn is part of the lane. |
| Network | Browser access to the existing authenticated gateway REST/WS origin. A failed or offline history request remains recoverable, not empty. |

## Development Setup

This spec-writing dispatch runs **no local heavy commands**, no contract generation and no application/UAT process. No setup command is claimed executed.

The later implementation follows the existing setup in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/CLAUDE.md`::Running it locally / SPA embed and root `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/CLAUDE.md`::Build, test, and quality gates. In particular, use the gateway with the freshly embedded SPA, not an unrelated Vite server or stale embedded assets. Team-lead provisions an isolated data root and port for UAT; it must not point at or alter a person's production long session. Clone/anonymize that session with permission while retaining its real structural/height distribution.

Expected first opening: newest bounded page, pending/control state, then live tail. Known setup failures: absent SPA embed is a build/setup problem; missing build tags are not paging failures; a bare project-root TypeScript invocation is not a typecheck. Test and UAT execution belongs to the later RED/GREEN/CHECK plan and remote CI authority, not this Draft.

## Tech Stack

| Category | Choice / pin | Source |
|---|---|---|
| Backend | Existing Go 1.26.9, pure Go | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/go.mod`; root constraints |
| Frontend | React 19; existing virtual list (^3.14.6) and query (^5.101.2) packages | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/package.json`; C10 |
| Datastore | Canonical day-partitioned archive; private, derived content-free paging metadata only | C2–C5; no SQLite or second content database for this feature |
| Contracts | Existing OpenAPI/AsyncAPI source and generated Go/TS/runtime validators | C17; contract-first plan below |
| Tests | Existing Go tests, Vitest 4.1.11, browser end-to-end tests and independent UAT | Existing test sources in Regression Requirements; root UAT rules |
| External APIs | None required by paging | Reading retained chat is local gateway functionality |

## Deployment / Runtime

Runs inside the existing gateway and embedded SPA on all supported platforms. Deploy backend/contracts/SPA together; this deliberately changes the messages response from array to envelope. Reload old browser tabs on upgrade. Do not add a permanent dual-array compatibility path or return an unbounded legacy response on missing parameters. User-facing protocol documentation must disclose the response change.

Derived paging metadata is disposable and private. Boot/cutover prepares old sessions before ordinary history serving; a healthy restart reuses validated metadata rather than recounting content. Missing/corrupt derived metadata uses a serialized, bounded-memory rebuild with a visible not-ready/storage outcome, **never** a full-read-on-every-request fallback. Data repair/cutover time is recorded separately from ordinary cold-process opens. Canonical accepted bytes, model window, retention and existing lock order remain authoritative (C2–C5).

Opening/paging cancellation cancels only read work and releases resources. It must not call Stop, clear a context or unsubscribe another session's work. Logs are local operational diagnostics, not telemetry. No lifetime-content cache or persistent browser history database is introduced.

## Integration Boundaries

| Boundary | Data / contract | Failure / development approach |
|---|---|---|
| Archive → page reader | Public chat projection, referenced corrections/dispositions/cancel state, stable private addresses. Model-only/source bytes excluded. | Real temporary archive fixtures and injected read failures; fail visibly on unavailable required content/index, never partial-success-as-empty. |
| Gateway REST → SPA | Generated paged messages envelope and metadata, request bounds/directions/target selectors. | Real handler + generated schema tests; mock only the network in component tests. Keep per-item visible placeholders while validating page metadata strictly. |
| Gateway attach → SPA | Same selected page and public identities, existing replay frames, projection, successful terminal catch-up marker and held live tail. | Real hub/replay fixtures with deterministic bind/persist barriers. No fake hub in tests claiming catch-up ordering. |
| Existing navigation/search → target loader | Authorized session plus known message ID or history position. | Q2 settles the missing message-hit producer. Direct target navigation is real UI reachability, not a console-only store method. |
| Train 3 / interim replay branch | Shared semantic invariants and eventual landed code, not a new external runtime dependency. | Pin/re-read landed revisions and rerun cross-feature regressions before implementation/landing. |

## Technical Decisions, Contract-First Plan and Affected Components

These implementation-design sections are intentionally separate from the behavioral stories above. Except Q1–Q4's explicitly conditional choices, they define the proposed technical shape for review; they do not assert that it exists today.

### D1 — Shared newest-first page reader, not a suffix of a full read

**Context:** C1 reads all content before cutting; C2 must resolve later effects, input dispositions and cancel-derived truncation. C3 already supports exact addressed reads. C4's ordinal counts a different view. Therefore a newest-first early-stop **chat projection reader is feasible**, but not by reversing C1's completed slice or calling the old PartitionStore reader.

**Decision:** add one bounded chat-page selection/projection path owned by UnifiedStore/ArchiveDayStore. REST messages, REST detail and WS snapshot must use it with identical options. Keep ReadTranscript and its full-read semantics for consumers that intentionally need them. Reuse exact marker recognition and the existing merge rules; do not rewrite them as a less-correct second projection.

Extend the existing file-based, content-free indexing approach with chat occurrence ordering, ID/location lookup, previous/next eligible chat references, latest-clear reference and relevant projection/dependency references. This is a derived index of the **same archive**, not another content history. Store physical ArchiveAddress only internally. Include effect targets/retractions, applicable cancellation targets and disposition locations in incremental index maintenance; scanning the lifetime disposition sidecar or effect suffix on every page is also prohibited.

| Required invariant | Implementation obligation / evidence |
|---|---|
| Bounded selection | Traverse eligible chat-group metadata newest-first. Take nearest whole groups until adding another would exceed the requested row target; if the nearest indivisible group alone exceeds that target, include it subject to Q3's maximum/exception rule. Stop also at 100 source groups or a clear/end boundary, with one actual eligible lookahead from metadata. Materialize only selected content and relevant dependencies, then return ascending acceptance order. Zero-visible-height groups cannot defeat the source-group cap. No lifetime content read/sort to determine a page. |
| Correct corrections | Resolve the current applicable settle, unretracted projections, cancel truncation and input-disposition labels for selected entries. A late effect may target a day outside the page; use indexed references, not a scan of all intervening content. Guarded settles/retraction order must match C2's semantics. |
| Crash consistency | Canonical archive append remains authority. Index publication records its covered tail; a crash between append and index publication recovers the uncovered tail before declaring a page current. Never serve a success with silently missing accepted chat/effects. Derived-index failure must not turn a durable append into a claimed unsaved message; mark the index stale and repair before the next history success. |
| Cold/legacy behavior | Existing converted sessions use this same reader. Build/rebuild streams once into disk metadata with bounded scratch, does not rewrite retained content, publishes atomically and retains deterministic positions. No hot map proportional to every session's lifetime. Interrupted rebuild is retryable. |
| Locks | Preserve session-shard → archive/index state order and session-shard → cacheMu; no reverse nesting, global cache lock over I/O, hub lock while taking a session shard, or simultaneous parent/child shard hold. Capture the page cut under the addressed session's synchronization. |
| Errors/damage | Distinguish normal absent optional files from required unreadable/corrupt state. Match current malformed-line/diagnostic rules, count/report skipped malformed records, and never call an unconverted row valid. A structurally contradictory/corrupt index is a repair/error path, not terminal history. |

**Consequences:** positive—page content work is tied to selected rows/references rather than retained prefix; REST and replay share semantics. Negative—chat/effect/disposition indexing and crash repair are real backend work, not a query-parameter patch. Neutral—full archive/model readers and accepted bytes stay unchanged. Exact index layout is backend-owned internal design, but the bounded-work, recovery and no-content-copy invariants are mandatory and tested.

### D2 — Stable public occurrence positions; no overloaded sequence

A paging position names one accepted **chat occurrence** in one session. Use a versioned opaque token, not entry ID alone (legacy duplicate IDs exist), entry timestamp, array index, WS seq, model archive ordinal or a client-supplied filename/offset.

**Chosen shape:** `hp1_` plus the unpadded base64url SHA-256 digest of a length-delimited tuple of canonical session ID, stable internal partition key, encoded record-start offset and recorded entry ID. This is a one-way occurrence fingerprint; raw/reversibly encoded physical marks never leave the store. The derived same-session index resolves it to the checked address. A token is a selector, **not authorization**, and adding another session's token never grants access. The digest remains deterministic on rebuild and rollover because the canonical bytes/addresses do not change (C3). Hash collision or duplicate index binding is a visible integrity error, never last-wins address substitution.

Positions order by the index's private archive acceptance order, **not by sorting their opaque strings**. Repeating a request under concurrent append yields the same exclusive range, apart from legitimate newer projection values/expiry. Clear appends another occurrence; it neither renumbers nor invalidates retained old positions. Retention deletes a target only by normal policy; an unknown/expired position returns the same generic authorized-position 404. No boot ID is part of the history position.

Public Message gains read-only `history_position` on paged REST entries. Original recorded `id` stays original. A position distinguishes duplicate occurrences for selection while existing duplicate-message/tool presentation remains last-wins with diagnostics (C8/C12); never concatenate content twice because the same occurrence came through two carriers. Imported rows lacking valid public identity use the existing visible malformed-row treatment with stable position-based placeholder keys; do not mint new persisted ULIDs or infer missing provenance/turn/goal facts during paging.

### D3 — Page and target semantics

| Mode | Request | Selection / response |
|---|---|---|
| Latest | Omit before/after/around selectors; optional limit | Newest eligible groups since latest clear, ascending in response. Stop at the marker, including it if reached. If four screens do not reach the marker, return only the newest page; do not add a distant divider across an omitted gap. |
| Older | `before=<history_position>` | Exclusive predecessors. Return the nearest preceding page, ascending. The first encountered clear divider is included as the oldest row and stops that request. Repeating before **that marker** explicitly crosses into the preceding segment. |
| Newer | `after=<history_position>` | Exclusive successors, nearest first in ascending response order. Used after a jump and, if Q1 selects eviction, to reload an evicted newer end. Clear dividers are ordinary visible boundaries in this explicitly addressed direction; forward traversal never restores model context. |
| Target neighborhood | Exactly one `around_message_id` or `around=<history_position>` | Include target plus approximately half the budget before and half after. For ordinary single-row groups with limit L, reserve floor((L−1)/2) before and the rest after; use available capacity on the other side at an end. Around-message-ID selects the latest retained canonical occurrence for duplicate IDs; an exact position selects that occurrence. May cross clear dividers explicitly, showing all encountered dividers. |

Do not require the client to enumerate every older page to find an ID. The index supplies target lookup and both directions. `before`, `after`, `around`, `around_message_id` are mutually exclusive. An addressed request is an explicit history read; default opening and automatic prefetch never cross an unopened clear boundary. One click on Show earlier at a marker permits the next segment; it does not silently authorize a walk across every older clear.

Page envelope target: generated **SessionHistoryResponse**, with required `session_id`, `entries` (public Message array, ascending, never null), `has_earlier`, `has_later`, and `page_info`. Optional `next_before` / `next_after` exist iff the corresponding flag is true and equal the first/last returned occurrence position. `page_info` includes effective `limit`, `row_count`, optional `stopped_at_clear` (position when applicable), optional `latest_clear` (the latest marker's recorded entry ID and position, whether or not it is inside this page), and the bounded replay manifest/context described in D4. Empty entries means both flags false and no cursors or stopped_at_clear. A full page at the real beginning still says has_earlier=false. A clear boundary with older retained entries says has_earlier=true, with next_before at its marker. Never return an empty nonterminal page.

`has_later` describes retained successors at that read's cut; it is **not** an instruction to merge an unrelated live tail into a target-centered window. The newest normal page has has_later=false at its cut even if live messages append immediately afterward. Cursor values remain separate from session state and transport progress.

### D4 — Tool grouping and cross-carrier projection

**Atomic selection unit:** one owning chat entry with its tool calls and their merged state; start/result of a finished call are never separate pagination units. A pending call has a start and truthful pending status, not a made-up result. There is no requirement to include an entire turn, the child's full transcript or every entry between a delegate start and end. These would make an ordinary long-running turn an unbounded page.

Tool-only entries are first-class groups; do not drop them using ChatScreen's current role filter. A page carries enough public ownership to create a non-prose historical tool group even if narration lies outside the page. Target **SessionHistoryEntryRef** for each selected source group contains its `history_position`, recorded `entry_id` (when valid), and ordered `tool_call_ids`; this manifest contains IDs/structural metadata only, not copied text/results. Put the same ordered source-group manifest in REST page_info and in the WS snapshot page_info. It maps existing tool-start/result frames to their true source group rather than the reducer's current "last assistant" heuristic. Existing generated event payloads remain the event formats; no handwritten frame family.

For a WS snapshot, prepare the bounded replay plan before emitting the wipe. Each manifest ref additionally carries `replay_start` (zero-based index) and `replay_count` for that source group's generated historical content frames; page_info carries `replay_frame_count`. These ranges are ordered, nonoverlapping and cover the selected-entry content frames. They allow the client to associate special frames lacking the archive entry ID as well as tool frames; it must not infer an owner from whichever bubble precedes a cropped page. A source group suppressed by existing presentation rules has replay_count=0. The terminal replay done, session_state, projection and catch_up_complete are not content-frame ordinals. replay_frame_count counts every generated replay frame before that replay done, including session-level terminal-card/replay-warning frames; those are explicitly identified as `replay_context_ranges` in page_info, never miscounted as a selected message. Each such range has start, count and kind (`terminal_card` or `warning`). Manifest ranges and context-only ranges together cover exactly [0,replay_frame_count), with no gap or overlap. REST refs omit replay ranges/counts because their public Message payloads directly carry source-group identity. The same shared projector/context produces both paths; do not build a second independent replay implementation. The client validates frame totals/ranges before committing the historical batch; truncated or contradictory replay is a visible failed attach, not partial successful history.

Supply bounded **SessionHistoryContext** for dependencies of selected groups: actual relevant persisted span start/latest state/end, cancel/disposition projection references resolved to public values, and alias/ownership associations needed to render selected rows. Its generated shape contains only public IDs and existing public generated payloads. It must not replay support-only dependencies as extra chronological message rows, invent terminal state, or load all spans in the session. Historical hydration is read-only: no notifications, new approval requests, active-turn changes, attention acknowledgement or new pending question from an old card. Current pending state still comes from session_state. Global latest goal status is live state, never guessed from the last selected history row.

| Cross-carrier rule | Required result |
|---|---|
| Same session/options/read cut | REST messages, detail's messages and snapshot manifest select the same occurrence range; compare IDs, tool ownership and current projected fields, not unlike raw frame counts. |
| Replay grouping | A finished selected tool emits start then its result before the page replay done; no limit applied to flattened start/result frames. Active tools may have only their start. Track coverage of starts, terminal results and text separately: an emitted start is not proof that a projection's later result was replayed. Keep existing emitted-ID deduplication, but never use mere call-ID presence to suppress a missing result. |
| Selected span lacks start or end in range | Read relevant dependency facts through the index and hydrate keyed context without pretending the omitted event happened at the page edge. Never interpret "not in this page" as "still running". |
| Steering/terminal outcome | Preserve user/steer boundaries, turn/agent/goal association, terminal_outcome and text offsets. Page prepend is historical hydration, not feeding old assistant text through live-tail append heuristics. Recompute only affected joining boundaries from source identities, keeping aliases to old targets. |
| Duplicate occurrences/calls | Track occurrence positions separately from display aliases. Preserve existing last-canonical-occurrence semantics across pages and later live data; a stale historical call cannot undo a newer result. Warn/count genuine stored duplicate IDs. Do not dedupe unrelated messages on text/timestamp similarity. |
| Special content | Preserve participant/reply labels, model, fallback notes, helper events, goal outcomes, handover notices, diagnostic visibility, verdict cards, truncation, attachments and safe result references through the same historical hydration path. Per-item malformed data retains a visible placeholder, not a changed cursor. |

**Row-budget qualification (Q3):** a single archive entry may contain multiple drawn tool rows. A request targets 20..100 **drawn-row units**, not 20..100 WS frames, and group weight is narration/system row plus its tool rows (one call's start/result is one tool row). Hidden/metadata-only items are not measured visible rows. The server uses the same documented canonical group weights; browser measurements use its rendered row model, including tool children. They are estimates of visible coverage, not permission to drop hidden archive entries. Invisible metadata travels with selected groups/context and does not drive an automatic history walk. Q3 must settle an atomic group heavier than 100; the recommendation is a visible/logged atomic-group exception, not an unannounced violation of the founder's cap. Until settled, tests for that exception are conditional and no hard replay-byte guarantee is claimed. Existing 8 MiB record admission and result-reference/preview machinery must not be weakened.

### D5 — Initial page ownership and pixel math

The server owns validation and the default **40 row units** (four 800px screens at the 80px initial estimate). The client owns visible chat geometry, not the window height or an assumed monitor size.

`AttachSessionFrame` gains optional `history_limit` (integer 20..100; default 40) for the snapshot branch. If a committed visible viewport height is already available **before** the one initial attach is sent, request clamp(ceil(4 × H / A), 20, 100). Otherwise omit it and use server 40. No temporary measuring attach, speculative full replay or second attach just to correct the size. Snapshot returns the effective page metadata. Once measured, top up **older** content through the normal page reader until roughly four screens are covered, a clear/end is reached, or the policy/cap prevents more; re-evaluate geometry after each response rather than walking to archive start. The 100 cap applies per request, not lifetime retention.

| Quantity / rule | Definition |
|---|---|
| H | Current chat scroller's clientHeight in CSS pixels, excluding composer/header. H≤0 or hidden document disables automatic loads. |
| A initially | 80 CSS pixels per drawn content/tool row until actual measurements exist. |
| Running average | Maintain the latest 100 distinct measured drawn-row samples for this view/width/presentation generation. A=max(1, sum(heights)/sample count). Repeated observation of the same row replaces its sample, not an extra vote. Exclude loader/hint/Retry, empty hidden rows, zero/non-finite heights and the changing live bubble. Reset samples on session/major width/Verbose presentation change, then seed 80. |
| Tool rows | Measure/count the row model actually drawn, including inline tool rows; the current outer-message measurement alone (C10) does not prove that every tool event already counts separately. Avoid counting a parent wrapper's height **and** all its children as independent heights. Include inter-row space consistently. |
| Dtop | Pixel distance from the viewport top to the earliest loaded content boundary within this history segment, accounting for real prefix controls/spacers. Do not use first-visible array index or a count of 20 remaining messages. |
| Automatic trigger | Once opening/target hydration is committed, if Dtop ≤ 2H, has_earlier, no unopened clear barrier, no direction error and no in-flight request, fetch the earlier page. |
| Requested count | clamp(ceil(2H/A), 20, 100). Example H=800/A=80 →20; H=800/A=40 →40; H=800/A=400 →20; H=1600/A=10 →100. |
| Underfilled viewport | Re-evaluate after commit, but stop at terminal/clear/error. The explicit earlier control remains usable without a scrollbar. A page that adds no positive drawn content height does not automatically chain another request: keep its advancing cursor for an explicit next action instead of walking invisible lifetime metadata. Do not treat observation of an unchanged cursor as permission to loop. |

Fixed 40 is bootstrap fallback only, not the permanent viewport model. A background earlier request does not set isReplaying/isStreaming or disable the composer. Forward continuation after a target/eviction uses the symmetric pixel-distance rule near the loaded newer edge, never interprets live seq as an after position.

### D6 — Ordering, request state and clear races

State belongs to `(session_id, view_generation, direction, boundary)`; at most one request per direction and coalesce repeated same-boundary triggers. Top-up and manual earlier use the same request path. Each request captures the generation and boundary, uses AbortController at the network edge and checks the generation again before committing **success or failure**. Abort on switch/leave, new full snapshot, target replacement and applied clear refresh. Server read work checks request cancellation and releases file resources.

Merge a successful page into **current**, not request-start, state. Keep ascending server occurrence order; preserve current/live values on identity collisions and all newer tool results/streaming objects. Pages never mutate the transport cursor, session totals, model context, pending inputs, active turn, goal state or approvals. Reject wrong session, repeated/nonadvancing cursor, impossible flag/cursor relation or overlapping contradictory order as a visible protocol failure. Do not compare the lexical order of opaque tokens; validate identity/continuation and established adjacency, with server order the authority.

An independent REST latest result must never overwrite a newer WS page/window. WS attach remains the authoritative live-open path; avoid concurrently fetching two full initial histories. REST supplies initial fallback when WS history is unavailable, supplemental page-specific special-row hydration if needed, and older/newer/target reads. Replace the global header side channel with metadata committed atomically with its page. All C11 callers, including ChatThread, must migrate; clear refresh consumes the page envelope without losing its operation ownership.

Successful clear refresh applies the fresh bounded latest page plus the new marker identity in page_info, even if the marker itself lies before the selected recent range. Only **that operation's** unknown-at-arm marker can settle C14; a random marker in an older page cannot. A successful clear invalidates older browsing reads and closes the new earlier-clear barrier; a refused/held clear does not invent a view reset. When another tab's clear is discovered through fresh latest/snapshot metadata, establish a new view generation; an already issued old-generation page cannot reopen it. A retained history token itself stays valid for an explicit future read.

### D7 — Snapshot/incremental lifecycle and interim replay fix composition

```
client attach (optional history_limit, independent since_seq/boot_id)
    → existing access checks
    → bind hub and hold live frames; capture W/projection
    → if incremental is servable:
          session_state → journal tail (since_seq, W] → catch_up_complete(W)
      else:
          read ONE shared latest history page before telling client to wipe
          session_snapshot(W, page_info/manifest/context)
          → session_state (current controls/active turn)
          → selected history replay → one replay done
          → projection not covered by emitted IDs
          → catch_up_complete(W)
    → release held live frames in existing sequence order
    → existing goal rehydrate / successful foreground attention handling
```

Do not apply history_limit to incremental journal catch-up. Cutting that tail would acknowledge missed events. Its existing journal count/byte retention decides whether it is servable; an unservable cursor falls back to the bounded latest snapshot. Older-page fetches are unsequenced historical hydration and never advance since_seq. Pending approvals/questions/secrets, active-turn projection, steering/control receipts, goal status and unsent/failed optimistic input are not limited to the recent-history slice. Preserve read-before-wipe, failure without catch_up_complete, hold release order, and actual active-turn/goal rehydration (C7–C9/C13).

**Interim overlap:** `fix/chat-replay-render-once` is a dispatch-described frontend-only change: buffer WS attach replay until done, publish once and settle opening at the bottom. It was not available as a local or remote ref at research time (`git ls-remote` returned Train 3 only), so its actual implementation is **Unknown**, not cited as read code. **Integration decision: rebase this feature onto its landed destination commit once landed; re-read it before editing the replay path.** Do not duplicate its buffer or open-scroll fix. Paging supplies a bounded input to that buffer and supersedes full-history loading, not the render-once guarantee.

The existing protocol distinguishes replay done from definitive catch_up_complete: projection frames can occur between them. Preserve the interim's one historical commit while ensuring the opening scroll settles only after the complete snapshot/projection commit; a live done is not an attach terminator, and an incremental attach must not be mistaken for a fresh bottom-jumping history replay. No bottom scroll on every prefetched page, image measurement, live append while reading up, or routine incremental reconnect. A failed replay discards incomplete staged history and retains the prior visible view/cursor.

### D8 — Scroll anchoring and visible recovery

Use stable occurrence/display-group keys, not the current index-key default, in both virtualized and plain paths. Track the current first visible content anchor by identity and intra-row pixel offset. Capture/refresh it at committed layouts and genuine reader movement, not just at request start. During a prepend/control-layout change, adjust by the **signed change in that anchor's content offset** against the current scroll position. Do not use total scrollHeight delta: that includes simultaneous live growth below the reader. With virtualization, use the keyed measurement model and a measured visible element to correct estimated sizes before paint, then re-correct as ResizeObserver/image measurements settle. Respect user movement during the request; do not restore a stale request-start scrollTop.

Reuse T2/T3's signed-delta/every-layout discipline and `overflow-anchor: none` ownership. It must cover loading status insertion, hint removal, Retry replacement, empty terminal-page shrink, width/font change, image/diagram decoding and live-to-historical handoff. Avoid double compensation by browser-native anchoring, virtualizer adjustment and assistant-ui auto-follow. One paging anchor coordinator owns corrections; existing auto-follow remains bottom-only outside historical-read mode. A lost anchor from an explicit window/session replacement invalidates it rather than guessing a pixel position in another view.

| UX requirement | Behavior |
|---|---|
| Visibility of status / recovery | Reuse catalogued Button/ErrorState patterns, not a second one-off error banner. Show “Loading earlier messages…” at the history boundary; on failure show “Could not load earlier messages.” with Retry while preserving content. Symmetric newer/target error text names that action. |
| Clear boundary | Preserve the quiet divider and explanation. Replace its interim noninteractive-only treatment with accessible **Show earlier messages** when that exact unopened marker has retained predecessors. Opened older markers do not all acquire the same global earlier flag. |
| No history left | Remove/disable the earlier action truthfully; do not keep polling a terminal page. No large blank loading spacer. |
| Accessibility | Keyboard-operable actions, polite page-load status, no focus theft on prepend, no “new response” announcement for historical rows, reduced-motion-safe anchor correction. No focus ring/color/spacing inventions. |
| Plain fallback | Same page/error/generation semantics. Measure actual elements on scroll/resize when available; otherwise manual controls with 80px estimate. Never fall back to fetching or rendering the full server lifetime. |
| Fast scroll / network failure | “No visible gap” is tested inside Q4's normal-use envelope, not promised under infinite latency. Outside it, show status/error truthfully, never silent blank history. |

### D9 — Memory and target-centered browsing (Q1/Q2)

**Mandatory now:** bounded first page/replay/staging; no lifetime preload, inactive-request leak or all-session history cache. The current oldest-drop ring cannot accept prepends unchanged. Whole owning tool groups are retained/evicted together, with their indexes/measurement samples cleaned.

**Recommended Q1=A design, not approved:** retain at most 500 source groups in a contiguous reader-centered window, preserving current anchor and at least two screens of guard content where available. Evict whole far-edge groups opposite traversal, not newly fetched groups; cursors make either evicted end reloadable. Keep current live execution/control state separate from the historical read window and a bounded newest-tail cache of at most 100 complete groups. New live messages cannot create a hidden gap inside the displayed old window or evict the anchor; show a Return to latest action instead of attaching a far-away tail below it. Pending user input and active tool starts keep existing explicit bounds/protections and are not discarded to meet a history cap. Old-frame updates still reach keyed active control state; absent historical rows can be reloaded from the archive. Q3 must also handle an individual oversized group. Measure and record heap use, not only counts.

A target load replaces the displayed history window atomically only after validation. Hold the previous view on failure. Fetch directly around target; highlight/center after measurements settle and preserve its identity if multiple source entries coalesce into one bubble. Keep before/after continuation and real clear dividers. Maintain live connection/control state while reading old history; opening historical rows must not revive old approvals, change the current goal or issue Stop. Return to latest loads a **new latest page**, not every missing intervening page. Sending from old history remains addressed to the current conversation and deliberately returns display to its live end; it never sends the historical window as model context.

**Q1=B alternative:** retain all intentionally loaded history during one visit and release it on leave; resource guarantee is on open only. This avoids bidirectional eviction but may grow with browsing/live use and changes the existing 500-message policy. Do not silently choose B or advertise lifetime-bounded memory if it is selected.

**Q2 recommendation:** implement the neighborhood reader and a validated target-message deep link as the reachable producer now; do not build an unrequested full-text search engine. The existing Sessions search stays title/workspace/agent search. Add message-hit search only if the founder explicitly includes it in this issue.

### D10 — Contract-first change inventory (backend-lead only)

All following paths are under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266`. No contract file is edited by this spec. New schema filenames/types below are **proposed artifacts**, not claims of existing definitions.

| Contract artifact | Required change |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/openapi.yaml`::getSessionMessages | Replace bare array with generated SessionHistoryResponse. Define before/after/around/around_message_id and limit parameters, exclusive semantics, validation/error responses, and continuation invariants. Remove X-Has-Earlier-Messages as authority; document the atomic envelope change, not two competing paging channels. |
| Same OpenAPI::getSession; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionDetail.yaml` | Detail's messages is the same bounded latest page (default 40), never a hidden full-transcript path. Keep existing metadata/agent_removed fields; add the same page_info and continuation fields. Explicitly replace has_earlier_messages's clear-only meaning with canonical has_earlier; migrate every consumer atomically rather than keeping conflicting meanings. Detail needs no older-page selector; messages endpoint owns paging. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionHistoryResponse.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionHistoryPageInfo.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionHistoryEntryRef.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionHistoryContext.yaml` | Add shared envelope/metadata/manifest/public context per D2–D4. Required non-null arrays and booleans; cursor presence iff continuation; optional fields omitted, not null. Limit/array weight descriptions explicitly reflect the founder's Q3 answer. Context reuses existing generated public payloads, excludes private archive records. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/Message.yaml` | Add optional readOnly history_position, required by the page-producer invariant for each returned source occurrence. Add readOnly optional terminal_outcome with the same persisted meaning as ReplayMessageFrame.terminal_outcome so REST pages do not lose the existing distinct terminal-message boundary. Preserve other existing public message fields and private-data exclusions. Record the currently observed provider_fallback discriminator gap (C8 handles it, while C17's Message enum omits it) for team-lead's scope triage: page parity needs an explicit contract/adaptation disposition, not a silent placeholder or an unreviewed side fix. No new untyped alias of TranscriptEntry. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/AttachSessionFrame.yaml` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/asyncapi.yaml`::AttachSessionFrame / attach operation | Optional history_limit 20..100/default 40, snapshot-only; since_seq/boot_id and ack semantics unchanged. Document invalid-limit refusal before bind/wipe in both validation modes. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionSnapshotFrame.yaml` and same AsyncAPI::SessionSnapshotFrame / snapshot operation | Replace “full replay” with recent-page replay. Include canonical has_earlier/next_before/has_later/next_after and generated page_info/manifest/context. Remove clear-only has_earlier_messages meaning; retain unrelated secret-request/state fields and snapshot seq/reason semantics. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/CatchUpCompleteFrame.yaml` and same AsyncAPI::CatchUpCompleteFrame | Clarify terminal completion of selected-page replay **plus projection**, not lifetime history. No history position/limit on incremental journal frames. |
| Shared replay/tool schemas in C17 and their AsyncAPI mirrors | Retain frame shapes unless the reviewed implementation needs an explicit ownership carrier beyond D4's manifest. The manifest is the chosen ownership carrier; no implicit uncontracted Go/TS message_id field may be added. Describe its relation to replay start/result and special-frame IDs. Any additional carrier requires architect-shape review before backend-lead regeneration. |

The new shared shapes have the following field contract; these are design tables, not handwritten replacement wire types:

| Shape | Required fields | Optional fields / semantic constraints |
|---|---|---|
| SessionHistoryResponse | session_id (string, 1..255), entries (Message array, non-null), has_earlier / has_later (booleans), page_info (SessionHistoryPageInfo) | next_before / next_after (47-character position strings); iff rules in D3. entries and manifest each have at most 100 source groups; an oversized atomic group can weigh more drawn rows only if Q3=A. |
| SessionHistoryPageInfo | limit (integer20..100), row_count (nonnegative integer), manifest (SessionHistoryEntryRef array), context (SessionHistoryContext) | stopped_at_clear (position reached at this page's old edge); latest_clear (object with entry_id and history_position) is the latest marker at the read cut, even outside the selected range. Omit absent markers, never null. Latest-clear metadata is not a rendered row or permission to cross it. WS snapshot additionally requires replay_frame_count≥0 and replay_context_ranges (array of {start≥0,count≥1,kind=terminal_card/warning}); REST omits these replay-only fields. |
| SessionHistoryEntryRef | history_position (47-character string), tool_call_ids (ordered unique nonempty-string array, empty when none) | entry_id (nonempty recorded ID when valid). Manifest order equals selected source-group order, with one ref for each entry, including visible-malformed placeholders. Its tool IDs are canonical calls to replay for that occurrence, not copies of another occurrence's calls. WS snapshot additionally requires replay_start≥0/replay_count≥0 as paired integer fields, with D4's complete nonoverlapping coverage; REST omits them. |
| SessionHistoryContext | spans (array), duplicate_ids (array); both always present/non-null | Each spans item has span_id plus the actual applicable start/state/message/end payloads when recorded, reusing generated SubagentStartFrame/SubagentStateFrame/SubagentMessageFrame/SubagentEndFrame; include has_run only when proven by accepted lifecycle history. Each duplicate_ids item has kind (message/tool), id, canonical_position and occurrence_count≥2. These lists are restricted to selected-group IDs and required active/ownership dependencies, never all session history. Disposition/cancel values already projected onto selected Message/replay fields are not duplicated as a second authoritative context field. |

For REST detail the same required page metadata/continuation invariants accompany its existing session/messages fields; for SessionSnapshotFrame they accompany its existing snapshot metadata and replay sequence (no second entries content array). All new objects are closed, reference existing public payload schemas, omit absent optional values and carry no internal addresses. Backend-lead must confirm generated validators enforce the declared structure and add explicit runtime checks for cross-field/mode invariants. No size limit may silently discard a required dependency; Q3 governs the compound-group exception.

Order: (1) backend-lead adds shared schemas, (2) references them from OpenAPI/AsyncAPI including manual inline mirrors, (3) runs `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/scripts/gen-contracts.sh`, (4) commits canonical specs and generated Go/TS/Zod/inbound mirrors atomically, (5) consumers use generated types only. Verify with `make verify-contracts` from the stated worktree in the approved remote gate. No handwritten paging wire structs/interfaces or handwritten replacement response schemas. The adapter may transform generated types into local display state; it must not bypass generated validation to accept an old array. Conditional continuation/same-session/order assertions require semantic tests as well as structural validation (T1/T2).

### D11 — Access, input validation and error behavior

Reuse the **actual** read authority (C15): existing REST authentication wrapper, validated session ID, main eligibility, same session-store resolution and existing WS authentication/main gating. New positions/target IDs are resolved only inside the already authorized addressed session. Do not introduce a parent/root fallback or infer access because a caller knows a position. Existing child sessions remain independent archives; opening a child pages **that child**, not its routing root. These requirements are access parity, not a claim that an unobserved per-user read gate exists.

Precedence: authenticate → validate path/session-ID syntax → existing addressed-session eligibility/existence/access → validate paging syntax/combinations/limit → same-session target/position lookup → read/projection. Consequently an unauthenticated request is 401 even with bad limit, and an inaccessible/missing session returns the existing refusal without proving whether a supplied position exists. An authorized session with a malformed selector is 400. Do not add a new multi-user ownership policy; if the integration baseline adds a session-read gate before implementation, call that same gate in every new mode.

| Input/outcome after session access | Exact response / behavior |
|---|---|
| limit omitted | Use 40, not unlimited. |
| limit not one digits-only decimal integer in 20..100; duplicate values; malformed percent encoding | 400 ErrorResponse.error = `limit must be an integer from 20 to 100`. Leading decimal zeroes permitted; signs, whitespace, fractions, exponent, overflow, empty and literal null refused. |
| More than one direction/target selector | 400 `choose only one of before, after, around or around_message_id`. |
| before/after/around token malformed, duplicate, empty, null-like, over 47 characters, invalid hp1_ digest alphabet/length or malformed URL encoding | 400 `invalid history position`. A valid hp1_ token is exactly 47 ASCII characters; do not trim or silently omit it. |
| around_message_id empty, duplicate, invalid URL encoding, controls or over 255 UTF-8 bytes | 400 `invalid target message id`. Unicode non-control IDs within the byte bound are not rejected merely for being non-ASCII; use encoded query values. |
| Well-formed unknown/other-session/expired position | 404 `history position not found`; no foreign session lookup/body, no reset-to-newest. |
| Well-formed absent/expired target | 404 `message not found`; UI retains prior view and offers Return to latest. |
| Required history/index/projection unavailable | 500 through the existing redacted session-storage error path; if a bounded rebuild is actively preparing rather than failing, 503 ErrorResponse.error = `Session history is being prepared. Retry shortly.` No completed page or cursor. No new work-lifetime timeout. |
| Bad attach history_limit | Existing session-scoped error frame with `history_limit must be an integer from 20 to 100`; reject before hub binding/snapshot. No catch_up_complete and no history wipe. |

Validate RawQuery without the silent malformed-field omission trap; reuse Train 3's parseHistoryQuery/parseHistoryInt once landed where compatible, adjusting bounds/messages, not weakening handling with validation disabled. Cancellation is not successful empty history. Ordinary malformed per-item public payloads retain C11's visible placeholder and diagnostic behavior, with a position-stable key; broken page-level metadata is a failed page.

### D12 — Train 3 base decision and delivery order

**Decision: wait for Train 3's integration landing, then rebase onto the landing destination; do not cherry-pick its unlanded history/anchor commits.** This Draft can be committed/pushed now on the assigned feature branch; writing it does not require or perform a rebase. The feature's implementation cannot be declared integration-ready until the dependency is landed and the rebase is reconciled. Recommendation rationale: Train 3's branch contains broad approvals/room changes and generated contracts; cherry-picking a correction without its room schema/state baseline risks a partial dependency and duplicate implementations. Waiting preserves its reviewed lineage, then reuse/extract only appropriately shared history utilities. Do not import unrelated room UI or change its founder-selected limits/trigger.

Deliberate differences: chat `before`/`next_before` are opaque occurrence positions, not room entry seq; `has_earlier` follows the issue's name versus room `has_older`; ascending/exclusive/actual-lookahead semantics are identical. Chat's 20..100 measured target/default 40 differs from room's 1..1000/default 100. Chat uses a two-screen pixel trigger instead of room's 24px edge trigger, a virtualized/image-aware anchor instead of a plain DOM list, a disk early-stop projection instead of room's in-memory slice, and explicit clear/around/forward semantics. Room's live subscription cursor remains untouched exactly as chat's since_seq remains untouched.

| Stage | Owner / dependency |
|---|---|
| Founder decisions and spec review | Team-lead interviews Q1–Q4; apply the required two spec grill-and-fix rounds with founder interview before each fix. This author does not mark this Draft approved or start another ADR/grill cycle. |
| Landed bases | Team-lead identifies Train 3 and interim replay-fix landing commits; rebase the feature onto that shared destination, re-read changed seams, resolve existing same-file conflicts consciously. No force push is authorized by this spec. |
| Contract source + generated artifacts | Backend-lead, before consumers; merge/paging limits depend on settled Q1/Q3. |
| RED | QA writes independent backend and frontend tests from this spec; tests-only CI proves intended failures. Never weaken existing tests to fit an envelope or hide a regression. |
| GREEN | Backend-lead: indexing/shared reader, projection, REST/snapshot, access/validation/logging. Frontend-lead: generated adapters, paging state, grouping, measured fetch/anchor, clear/target/recovery, matching public docs. |
| CHECK and release gate | Separate QA integrity audit, five-reviewer feature gate, docs-verifier, exact-commit UAT + independent validator; same whole-integration gate before main. Founder approval remains required to land. |

### Alternatives considered and consequences

| Alternative | Why not selected / consequence |
|---|---|
| Full ReadTranscriptView then slice; cache the lifetime transcript | Rejected: retains lifetime decode/heap cost and cold-open problem. D1's index adds recovery work but enables genuinely bounded selected-content reads. |
| Entry ID, timestamp, transport seq or model ordinal as cursor | Rejected: duplicate IDs, delayed timestamps and different ordering/lifetimes are evidenced in C2–C4/C9/C12. D2 adds an occurrence selector without public physical marks. |
| Base64-encode ArchiveAddress into the public token | Rejected: reversible exposure contradicts the existing disk-only address boundary. D2's one-way token needs an index lookup, not a new credential/key lifecycle. |
| Directly reverse file lines without effect/disposition/dependency indexing | Rejected: stale settled tools, wrong cancellation/disposition and false running child state; gathering all later effects defeats early stop. D1/D4 preserve one projection authority. |
| Replay only text or cut after N start/result frames | Rejected: loses tools/special rows or orphans results. Select whole owning source groups, then replay them with explicit manifest/context. Q3 settles exceptional group weight. |
| Copy room reader/component wholesale | Rejected: room memory/seq/nonvirtual layout do not match session archive/effects/clear/image behavior. Reuse its continuation/error/anchor invariants and measured correction. |
| Keep current oldest-drop ring while prepending | Rejected: fetched old rows immediately disappear. Q1 chooses an actual reader-centered eviction policy or explicitly opening-only memory guarantee. |
| Cherry-pick unlanded Train 3 or duplicate interim buffer | Rejected: partially imported contracts/dependencies or competing same-path ownership. Rebase on their reviewed landing commits and preserve their guarantees. |

Affected owners are session archive/projection/indexes (backend-lead), gateway/contracts/replay (backend-lead with architect-owned wire shape), chat/navigation/adapters/state/scroll (frontend-lead), tests/acceptance plan (qa-lead), auth/privacy audit (security-lead) and public documentation (implementing leads with docs-verifier). There is no provider, scheduler, tool-policy, room-engine or model-context behavior change. The principal negative costs are derived-index recovery, a deliberately changed public envelope and bidirectional reader state if Q1=A; these require the feature-size cross-cutting/security gate, not an unreviewed frontend optimization.

## Performance, Observability and Measurement

### Proposed Q4 acceptance envelope — not an achieved benchmark

| Measure | Proposed target / method |
|---|---|
| Reference data | 100,000 chat source groups over 90 day partitions, at least 100 MiB encoded total, mixed tools/images/system/clear entries. Also a permissioned anonymized genuine long session; record its actual counts/bytes/shape, never call the synthetic fixture real UAT. |
| Server healthy-index page | Warm p95 ≤300ms and cold-process p95 ≤750ms over 30 distinct opens/pages at limit 40 and 100, on a recorded reference host. No model latency included. Cold process does not mean deleting/rebuilding a healthy persisted index before every read. |
| UI ready | Newest page visible, opening scroll settled and composer/control state ready p95 ≤1.5s over 30 long-session opens on the same browser/host with gateway round-trip ≤100ms. Measure from session-selection action, including actual first REST/detail/WS work. |
| Normal reading | Upward reading at ≤1 viewport/second, effective page response ≤500ms, no packet loss: 30 consecutive page boundaries with zero visible blank/history wait at the viewport and ≤1px settled anchor drift. Fast flicks/offline/slow storage instead require truthful status/error. |
| Prefix independence | Compare equal selected suffixes on 1,000 versus 100,000 prefix groups. At a healthy index, decoded archive records/bytes are selected groups + relevant dependency records, not the prefix. Server page allocations and replay content work vary by ≤10% for an otherwise identical suffix/dependency set; record raw counts, not wall time alone. |
| Memory/replay | Opening retains only chosen page, staged replay, relevant metadata and current state—not a lifetime intermediate. Record server allocated/peak bytes, browser retained heap after opening/GC, and total wire bytes/content-frame counts. For Q1=A, inspect 100-page traversal for 500 reader/100 tail group caps and release of page/tool/image caches; do not mislabel those counts as a hard total-byte cap. |
| Repair/upgrade | Separately time boot/cold missing-index rebuild, peak scratch and recovered accepted tails. No hidden lifetime read in ordinary page timings; no memory proportional to total archive content during rebuild. |

A no-gap guarantee for arbitrary scroll speed/latency and a fixed absolute byte cap for arbitrary admitted 8 MiB records/tool groups are **not established**. Q3/Q4 settle those trade-offs; retain their caveats in documentation and acceptance.

### Observability

Extend existing local structured replay/catch-up logs (C7/C8) rather than adding telemetry or a monitoring service. Log a content-free `history_page` outcome with session ID, direction, effective/requested limit, selected group/drawn-row counts, has_earlier/has_later, stop reason (row target/source-group cap/clear/end/atomic exception), duration_ms, archive records/bytes read, relevant dependency count, index hit/rebuild state and redacted failure class. No message bodies, parameters, result contents, participant secrets, raw cursor/address tokens or arbitrary query strings. Counters must distinguish request abort, protocol rejection, storage failure and terminal success.

Browser development diagnostics record request generation/direction, timing, measured H/A, requested rows, stale-response suppression, anchor correction/drift and loaded-group/cache counts without text/results. Reuse existing diagnostic channel; no UI error is log-only. Observe total attach replay frames/bytes and projection context separately so reducing entry count cannot disguise oversized frames.

### Measurement instrument checks

A baseline full-history open must produce lifetime read/replay counts; the candidate must not. Geometry tests must fail if compensation is disabled, if loader/hint heights are omitted, if total scrollHeight replaces anchor offset, or if index keys replace stable keys. Prefetch tests must fail if fixed messages replace pixel threshold/height math. Read instrumentation must detect a planted full-read fallback and an all-dispositions scan. Session fences must fail when generation validation is removed. Store tests must catch the existing oldest-drop ring discarding prepends. Count discrete reads/frames, use actual browser rectangles for geometry, and bind every result to commit + data fixture + instrument configuration. No green is trusted without those controls.

## Reachability

| Entry point | End-to-end path and acceptance |
|---|---|
| Normal chat | Existing agent/main navigation and existing session selection reach ChatScreen → initial attach/page → bottom; no hidden default full-history REST/detail request. |
| Scroll history | The actual scroller triggers measured prefetch; older rows can be read, not merely fetched into an unused cache. Explicit earlier control works in short/plain-list layouts. |
| Clear divider | Show earlier messages on the exact retained marker calls the same older-page action and shows the pre-clear segment, while the model window is unchanged. |
| Old target | A validated session/message deep link is a real browser navigation route under Q2=A; it loads around the target and highlights it. Current Sessions search is not falsely advertised as transcript search. Q2=B additionally needs the founder-scoped message-hit search entry. |
| Newer / latest | Scroll forward after a target (and eviction under Q1=A) loads the missing newer range; Return to latest gets the newest page without hidden gap. |
| API | Authorized callers can use the documented messages modes with generated contracts. Detail does not hide an unbounded alternative. |
| Tools | No new agent tool; no builtin-policy/registration work. Existing recall remains available and unchanged. |
| Verification | Real long-session UI campaign plus independent uat-validator on exact landing commit. Unit/schema/geometry receipts do not substitute for this. |

Delivery must be reported in two separate lines: **code correct and tested** and **reachable by a user/agent**. For this spec-only commit neither implementation claim is complete.

## User-Facing Documentation TODO

Implementing leads draft these updates **in the behavior change**; docs-verifier audits the actual merged behavior. No public-doc change is required for this Draft-only commit because no UI/API behavior is shipped here.

| ID | Affected existing page (outside internal docs) | Specific TODO |
|---|---|---|
| DOC-1 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/using-omnipus-ui.md`::The chat area / Clear a chat's context / Sessions / Limits and things to watch | Explain recent-first opening, automatic older loading, stable position, manual earlier/Retry, clear barriers, selected lifetime-memory behavior, target links/Return to latest and no context restoration. Replace the sentence saying reaching pre-clear messages is only planned. Do not call existing Sessions search full-text search unless Q2 authorizes and implementation delivers it. |
| DOC-2 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/operations/observability.md`::Session transcript / Replay | Explain full retained archive versus bounded UI/REST pages; document page limits/cursors/flags and content-free diagnostics, rebuild behavior and how to measure real opening/replay cost. A script that needs all history must explicitly page, not assume a detail response is complete. |
| DOC-3 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/protocol/websocket-protocol.md`::Connection Lifecycle / Server to Client Frames | Document attach history_limit, snapshot page metadata/manifest, replay done versus catch_up_complete, live hold ordering, unchanged incremental cursor and protocol-breaking array→envelope migration/reload requirement. |

## BDD Scenarios

Scenarios use dataset IDs defined below. Every When is one action; concurrent activity is a Given condition controlled by the fixture. **Conditional** scenarios specify a founder choice without treating its recommendation as approved.

### B01 — Open the recent end of a long session
Traces to: User Story 1, Acceptance Scenario 1.
Category: Happy Path.
**Given** D-A1 with an 800px chat viewport and no measured rows, **When** the person opens it, **Then** the selected recent page appears at the bottom, the default request budget is 40, omitted history has a valid earlier continuation, and lifetime prefix content is not read or replayed.

### B02 — Scenario Outline: Empty, short and exactly full terminal pages
Traces to: User Story 1, Acceptance Scenario 2; User Story 3, Acceptance Scenario 4.
Category: Edge Case.
**Given** D-A2 with `<count>` retained ordinary rows and requested budget `<limit>`, **When** its newest page is requested, **Then** `<returned>` newest rows appear in order with earlier flag `<earlier>`, empty is a successful empty array, and continuation is present iff earlier is true.

| count | limit | returned | earlier |
|---|---|---|---|
| 0 | 20 | 0 | false |
| 1 | 20 | 1 | false |
| 19 | 20 | 19 | false |
| 20 | 20 | 20 | false |
| 21 | 20 | 20 | true |
| 39 | 40 | 39 | false |
| 40 | 40 | 40 | false |
| 41 | 40 | 40 | true |
| 99 | 100 | 99 | false |
| 100 | 100 | 100 | false |
| 101 | 100 | 100 | true |

### B03 — Open after the latest clear, without crossing it
Traces to: User Story 1, Acceptance Scenario 2.
Category: Alternate Path.
**Given** D-A3 with two clear markers, **When** the conversation is opened, **Then** only the latest eligible segment/page appears; its reached marker is a divider, pre-clear history remains closed, and correct earlier metadata remains available even if the marker lies outside the recent page.

### B04 — Failed opening retains the prior view
Traces to: User Story 1, Acceptance Scenario 3.
Category: Error Path.
**Given** D-E1 and a previously displayed page, **When** a snapshot history read fails, **Then** the person sees a retryable error, no history wipe/successful catch-up is committed, and that page/cursor remain intact.

### B05 — Snapshot during tool/steer activity has no gap or orphan
Traces to: User Story 1, Acceptance Scenario 4.
Category: Happy Path.
**Given** D-R1 with a persist/publish at the controlled bind barrier, a pending tool, steering input, approval/question, active goal and a child with a start outside the selected page, **When** a new viewer attaches, **Then** it sees the selected corrected history and actual current control/turn state exactly once, completed calls have their starts/results, pending calls are not marked complete, and held live output follows successful catch-up.

### B06 — Incremental reconnect remains a complete journal catch-up
Traces to: User Story 1, Acceptance Scenario 4.
Category: Alternate Path.
**Given** D-R2 with a servable live cursor and an independently loaded older page, **When** the viewer reconnects, **Then** every retained journal event after its cursor is applied once, no history-page limit cuts the journal, older history is not duplicated, and the reader is not jumped to the bottom. An unservable cursor uses B01's bounded snapshot instead.

### B07 — Scenario Outline: Trigger and requested count are based on pixels and drawn rows
Traces to: User Story 2, Acceptance Scenario 1.
Category: Happy Path.
**Given** D-G1's viewport `<H>`px, measured drawn-row average `<A>`px, earlier history and distance `<distance>`px, **When** the reader reaches that distance from the loaded beginning, **Then** `<requests>` request is started with `<limit>` row units if triggered, tool rows participate and repeated events for that boundary do not create another request. D-G1b/c additionally cover sample replacement/reset, zero/hidden viewports and the 80px seed.

| H | A | distance | requests | limit |
|---|---|---|---|---|
| 800 | 80 | 1601 | 0 | not requested |
| 800 | 80 | 1600 | 1 | 20 |
| 800 | 80 | 1599 | 1 | 20 |
| 800 | 40 | 1600 | 1 | 40 |
| 800 | 400 | 1600 | 1 | 20 |
| 1600 | 10 | 3200 | 1 | 100 |

### B08 — Variable-height prepend preserves the moving reader
Traces to: User Story 2, Acceptance Scenario 2.
Category: Happy Path.
**Given** D-G2 with the reader moving during a page request and live rows growing below, **When** the earlier page commits, **Then** the current visible anchor retains its offset within 1 CSS pixel, new rows are above it, duplicate rows/tools are absent, and no forced bottom scroll occurs.

### B09 — Layout changes, including negative changes, keep the anchor
Traces to: User Story 2, Acceptance Scenario 2.
Category: Edge Case.
**Given** D-G3's nonzero hint/loader/Retry heights and late image/diagram growth, **When** the specified layout change commits, **Then** signed correction retains the anchor within 1 CSS pixel, even for empty terminal pages and unchanged first entry, with no native/virtualizer double compensation.

### B10 — History failure and explicit Retry preserve live use
Traces to: User Story 2, Acceptance Scenario 3.
Category: Error Path.
**Given** D-E2's failed earlier request, **When** the person selects Retry, **Then** the same failed boundary is requested once, old rows/continuation/live cursor remain until success, errors remain visible if it fails again, and ordinary send/Stop remain reachable throughout.

### B11 — Obsolete page results cannot affect another view
Traces to: User Story 2, Acceptance Scenario 4.
Category: Edge Case.
**Given** D-R3 with an old request deliberately completing after a switch/clear/snapshot/target generation change, **When** that old result arrives, **Then** neither its rows nor its continuation/error writes into the current view and the obsolete network read is aborted when possible; no agent work stops.

### B12 — Window limit does not make older history unreachable
Traces to: User Story 2, Acceptance Scenario 5.
Category: Edge Case.
**Given** D-M1 and the Q1-selected policy, **When** the reader crosses the existing 500-entry boundary, **Then** fetched rows remain readable without losing the anchor. For Q1=A, far-edge whole groups are evicted/reloadable and live cache stays within 100 groups; for Q1=B, no within-visit eviction occurs and no lifetime-memory-bound claim is made.

### B13 — Explicitly cross one clear divider
Traces to: User Story 3, Acceptance Scenario 1.
Category: Happy Path.
**Given** D-A3 at its latest closed divider, **When** the person selects Show earlier messages, **Then** the preceding segment's page appears above the same divider, the normal anchor/error path is used, no further clear is silently opened, and the model context/session identity remain unchanged.

### B14 — Around-target read provides both sides without a tail walk
Traces to: User Story 3, Acceptance Scenario 2.
Category: Happy Path.
**Given** D-J1's old target near the beginning of the long session, **When** its validated target link is opened under the Q2-selected producer, **Then** the target neighborhood and available context before/after appear, the target is highlighted, and no chain of tail-to-target page reads occurs.

### B15 — Continue from a target or return to latest
Traces to: User Story 3, Acceptance Scenario 2.
Category: Alternate Path.
**Given** D-J2's target-centered view with retained successors and ongoing live activity, **When** the reader selects its specified continuation action, **Then** a forward page joins contiguously or a new latest page replaces the window as specified, no hidden middle gap is displayed, and active controls remain current.

### B16 — Unknown/expired target gives recovery, not a substitute
Traces to: User Story 3, Acceptance Scenario 3.
Category: Error Path.
**Given** D-J3's missing/expired/foreign target, **When** it is opened, **Then** the documented error is shown, the prior display is preserved, no other session content appears and Return to latest is available for an otherwise readable session.

### B17 — Lookahead ends paging truthfully
Traces to: User Story 3, Acceptance Scenario 4.
Category: Edge Case.
**Given** D-A4 at the actual beginning, **When** its final earlier page commits, **Then** has_earlier is false and next_before is absent, the earlier action stops requesting, and removal of controls does not jump the view.

### B18 — Same corrected projection on REST/detail/snapshot and old sessions
Traces to: User Story 4, Acceptance Scenario 1.
Category: Happy Path.
**Given** D-P1's effects/dispositions/cancellation/duplicate/special-row fixture in each supported session kind at the same read cut, **When** the history carriers are compared, **Then** their selected occurrence identities, order, ownership and public projected fields agree, private fields are absent, child content stays in its child and no metadata-only row becomes a fabricated prose reply.

### B19 — Malformed page requests are refused, never reset to latest
Traces to: User Story 4, Acceptance Scenario 2.
Category: Error Path.
**Given** D-V1's authorized session and query variant, **When** it is requested, **Then** the exact documented status/error results, no bad limit is clamped by the server, no malformed cursor is treated as omission and no history response is emitted on rejection.

### B20 — Authentication and addressed-session access apply in every mode
Traces to: User Story 4, Acceptance Scenario 3.
Category: Error Path.
**Given** D-V2's missing auth/hidden main/missing child/foreign position state, **When** its requested history mode is used, **Then** existing read refusal and validation precedence apply, no unauthorized content/target existence is revealed and no parent/root fallback occurs.

### B21 — Bounded read/replay is independent of retained prefix
Traces to: User Story 4, Acceptance Scenarios 1 and 4.
Category: Happy Path.
**Given** D-A1's identical suffixes with small and large prefixes and a healthy persisted index, **When** their newest pages are opened, **Then** the approved record/byte/allocation/replay targets hold with measured read counters; a planted full-read fallback fails the check.

### B22 — Index recovery and retained positions are stable
Traces to: User Story 4, Acceptance Scenario 1.
Category: Edge Case.
**Given** D-R4's append/index crash cut, rollover/restart/rebuild/retention variant, **When** the recorded position is read, **Then** it identifies the same retained occurrence with complete accepted corrections, or returns its documented expired/not-ready/error outcome; it never names a different row or an invented empty history.

### B23 — Invalid page metadata is a visible failed page
Traces to: User Story 2, Acceptance Scenario 3; User Story 4, Acceptance Scenario 2.
Category: Error Path.
**Given** D-E3's contradictory session/cursor/order metadata, **When** the response is consumed, **Then** no partial page/flags are committed, the same boundary offers Retry, live state remains intact and diagnostics count the failure.

### B24 — Extreme compound tool groups respect the founder's selected cap rule
Traces to: User Story 1, Acceptance Scenario 1; User Story 4, Acceptance Scenario 1.
Category: Edge Case.
**Given** D-P2's group at or beyond the 100-row target, **When** the boundary page is requested, **Then** the Q3-approved rule is followed, no start/result/owner is orphaned, continuation progresses and any exceptional weight is observable; no hard row/byte bound is asserted contrary to the selected rule.

### B25 — Real long-session UI acceptance includes measurement and recovery
Traces to: User Story 4, Acceptance Scenario 4.
Category: Happy Path.
**Given** D-U1 on the exact candidate commit with approved Q4 targets, **When** the independent long-session campaign is evaluated, **Then** its saved screenshots/traces/geometry/network/heap evidence proves fast recent-first open, normal-use no-gap reading, bounded opening/replay, clear/target reachability, visible failure recovery and the Q1-selected lifetime policy; synthetic-only evidence cannot pass this scenario.

## Test-Driven Development Plan

Tests below are **planned**, not written or executed. Test names are proposed. QA owns RED and an independent CHECK instance; implementing leads do not change production behavior merely to satisfy a weak test. Order units → integration → end-to-end, with deterministic barriers rather than sleeps for concurrency.

| Order / test | Level | BDD | Independent oracle / scope |
|---|---|---|---|
| T01 TestChatHistoryPage_SelectsNewestAndActualLookahead | Unit | B01, B02, B17 | Frozen acceptance-order fixtures; exact selected IDs/positions/flags at empty/exact-full/off-by-one. |
| T02 TestChatHistoryPosition_StableOccurrenceAndSessionScope | Unit | B19, B20, B22 | Duplicate IDs/equal timestamps, distinct sessions, rollover/rebuild and expiry; fingerprint ambiguity never silently accepted. |
| T03 TestChatHistoryPage_ClearStopsAndExplicitCrossing | Unit | B03, B13 | Last exact marker, segment-local stopping, one explicit crossing, no model/history-byte changes. |
| T04 TestChatHistoryPage_MergedProjectionParity | Unit | B18, B24 | Hand-derived settle guard/project/retract/cancel/disposition/canonical-duplicate expectations across pages; do not derive expected values by calling production projection. |
| T05 TestChatHistoryPage_TargetAndForwardNeighbors | Unit | B14, B15, B16 | Exact around allocation/edge fill and exclusive after; ID lookup versus exact occurrence. |
| T06 TestChatHistoryIndex_RecoveryAndBoundedReads | Unit | B21, B22 | Real file seams and counted addressed reads; crash cuts/incomplete tail, content-free index repair, no whole read or disposition scan on healthy path. |
| T07 chatHistorySizing.measuredRowsAndPixelThreshold | Unit | B07 | Independent formula examples including measured tool rows, sample replacement, clamps and H=0/hidden. |
| T08 chatHistoryState.orderedMergeAndGenerationFences | Unit | B06, B08, B11, B23 | Deferred network promises + live update between request/response; exact state/cursor/control equality and rejected metadata. |
| T09 chatHistoryWindow.readerAndTailPolicy | Unit | B12, B15 | Q1-selected policy, 499/500/501 groups, no fetched-row oldest-drop, whole tool-group/cache cleanup and reloadable opposite edge if A. |
| T10 chatHistoryAnchor.signedEveryLayoutCorrection | Unit | B08, B09, B17 | T3-derived independent geometry fixture incl loader/hint/Retry/gaps; live below plus moving reader; mutation checks disable each compensation. |
| T11 TestGetSessionHistory_ValidationAndAccess | Integration | B19, B20 | Real registered auth/read routes with generated error schemas; table-driven query variants both inbound-validation settings. |
| T12 TestSessionHistory_RESTDetailSnapshotSelectSamePage | Integration | B01, B02, B03, B18, B24 | Real store/handlers/hub and generated response validation; compare same-cut public identities/fields, no duplicate initial lifetime read. |
| T13 TestAttach_PagedSnapshotPreservesLiveAndControls | Integration | B04, B05, B06 | Existing attach fixture/barriers; page manifest ownership, history done/projection/complete ordering, failed read no wipe/complete, complete incremental tail. |
| T14 chatHistoryClear.operationOwnedPageRefresh | Integration | B03, B11, B13 | Real clear-intent/reducer path with operation generations; marker outside recent page still confirms only its own clear; old page cannot confirm it. |
| T15 chatHistoryComponents.retryPlainAndTargetPaths | Integration | B02, B10, B11, B14, B15, B16, B23 | Real chat/store/adapters + generated fixture wire responses; composer not removed on older-page failure, plain fallback/manual actions and accessible target route. |
| T16 chat-history-paging.open-scroll-anchor | E2E | B01, B07, B08, B09, B12, B13, B17 | Built gateway/embedded SPA in a real browser, real stylesheet/images, delayed pages and live below; CSS rectangles/frame traces, not mocked heights alone. |
| T17 chat-history-paging.target-reconnect-retry | E2E | B04, B05, B06, B10, B11, B14, B15, B16, B20, B23 | Real navigation, target route, network failures/session switch, pending approval and current streaming tool across reconnect. |
| T18 chat-history-paging.prefix-and-real-session-evidence | E2E | B21, B22, B25 | Approved reference envelope; synthetic prefix-scaling instrument controls plus genuine long session and independent validator. |

Backend test locations: extend/add narrowly scoped files alongside `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_view_test.go`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_effects_test.go`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/clear_view_test.go` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/ws_attach_catchup_test.go`. Frontend/state tests belong beside their owning modules, not a new test-only facade. Proposed browser file: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/tests/e2e/chat-history-paging.spec.ts`; verify CI discovery rather than assuming it executes.

### Test Datasets

All counts below are source groups unless a row explicitly says drawn rows/frames. These are reproducible fixtures, not production measurements.

#### Dataset D-A — Archive and page boundaries

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-A1 | Identical final 200 ordinary rows after prefixes of 1,000 and 100,000 groups; 90 days; ≥100 MiB long case; fixed read cut | Large/happy/scaling | Same newest 40/100 groups and lookahead; content work independent of prefix | B01, B21 |
| D-A2 | count 0, 1, 19, 20, 21, 39, 40, 41, 99, 100, 101; limits 20/40/100 | Empty/min/min−1/max/max+1 | Newest min(count,limit); flag iff count>limit; empty [] never null | B02 |
| D-A3 | 250 old groups; marker C1; 60 groups; C2; 0/1/19/20/100 post-clear groups; marker-like ordinary text controls | Clear boundaries | Latest segment only; exact marker rules; explicit before C2 crosses one segment; markers never reset positions | B03, B13 |
| D-A4 | final predecessor counts 0, 19, 20 with request20; same first entry on empty terminal shrink | Terminal/exact full | Terminal flags and absent cursors, no automatic loop | B17 |

#### Dataset D-G — Geometry and sizing

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-G1 | H=800,A=80,Dtop=1601/1600/1599px | Trigger±1 | No/yes/yes; requested20; one request | B07 |
| D-G1b | H/A 800/40, 800/400, 1600/10; average sample empty→80; sample 0/NaN/negative ignored | Estimate min/max/non-finite | Requests40/20/100; stable nonzero average, no overflow | B07 |
| D-G1c | H=0, hidden document, H=1; same row observed repeatedly; 101 distinct samples; width/Verbose change | Zero/hidden/bounded history | No automatic hidden/zero reads; clamp20 otherwise; replace samples/reset and keep last100 | B07 |
| D-G2 | Heights 24/80/240/1200px; inline tool rows; 400px reader movement during request; 10 live appends below | Variable/concurrent | Current anchor, not request-start position, survives; latest tool results win overlap | B08 |
| D-G3 | T3-like hint32px, loader19.5px, Retry72px, gaps16px; terminal controls removed; image grows 80→640px; table width changes | Signed/late layout | ≤1px settled anchor drift including negative delta and unchanged first row; no double compensation | B09 |

#### Dataset D-P — Projection and grouping

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-P1 | Base pending call; guarded settle success; mismatching second settle; projections P1/P2 then retract P2/P1; far-day target; input discarded; cancel matching/nonmatching turn | Effects/cross-page | Hand-derived current state matches each carrier; cancel touches only applicable assistant; bytes unchanged | B18 |
| D-P1b | Tool-only entry; duplicate message/call IDs across pages; no turn/model fields on legacy rows; converted session; parent and separate native/external child sessions | Legacy/duplicate/ownership | Stable occurrence cursor, last-wins diagnostics, no invented identity facts, child isolated | B18, B22 |
| D-P1c | Populated participant/reply/model/fallback/helper/goal/handover/diagnostic/verdict/media rows plus raw private model/source payloads | Full public-field parity/private negative control | Same public rendering/typed state, zero private data leakage; metadata-only no fake prose | B05, B18 |
| D-P2 | Atomic groups of 1, 19, 20, 99, 100, 101 tool/drawn rows; huge result near existing admission bound; pending call with no result | Limit±1/group/result | Q3-selected atomicity rule, no orphan or fabricated result; existing result references/previews preserved | B24 |

#### Dataset D-R — Concurrency, reconnect and recovery

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-R1 | Append/effect before bind, between bind/read, between read/complete and after release; steer, pending asks/approvals and active goal | Concurrent snapshot | Each selected/current live item once, truthful control state, no split start/result, correct hold order | B05 |
| D-R2 | same-boot servable cursor; ahead cursor; evicted journal; boot mismatch; active unfinished answer | Reconnect/boundary | Full incremental tail or bounded snapshot as existing servability dictates, no older-page cursor mutation | B06 |
| D-R3 | Deferred success/error after A→B→A; applied clear; refused clear; snapshot reset; two target navigations | Stale/reordered | Generation fence, not session-ID-only test; no obsolete content/flag/error | B11 |
| D-R4 | Crash after archive fsync/before index publish; missing/corrupt index; rollover at UTC midnight; restart/rebuild; target partition retained/expired; unreadable directory | Persistence/damage | Complete tail repair or visible 503/500; same retained token; expired404, never rebinding | B22 |

#### Dataset D-J — Old targets and continuation

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-J1 | target1/2/middle/last−1/last; limit20/40/100; target before two clears; exact position versus duplicate message ID | Around edges/happy | Include target, balanced available neighbors with end fill, correct two-way cursors, no tail walk | B14 |
| D-J2 | Around-target window; actions scroll-newer / Return to latest / send; live tail now 1,000 groups newer | Detached tail/alternate | Contiguous after page or fresh latest window, no hidden gap or context restoration | B15 |
| D-J3 | Unknown ID, expired ID, other session ID, foreign position and valid target in hidden main | Error/security | Correct generic404/refusal, no substitute; prior view and recovery preserved | B16, B20 |

#### Dataset D-V — Input validation and access

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-V1 | limit omitted/20/40/100/020; then 19/101/0/−1/1.5/+20/whitespace/empty/null/NaN/Infinity/2^63/duplicate/malformed encoding | Min−1/min/max/max+1/type | Valid default/range; invalid400 exact limit text, never clamp/omit | B19 |
| D-V1b | valid47-char position; 46/48-char, wrong version, bad base64url, blank/null/controls, duplicate, `%ZZ`; valid token bound to other session | String/format/scope | Malformed400; authorized unknown/foreign404 with no content | B19, B20 |
| D-V1c | before+after, around+around_message_id, every selector omitted, target ID empty/255/256 UTF-8 bytes/Unicode/controls/duplicate | Mode/string/bytes | Only one selector accepted; target bound is bytes not runes; documented400 | B19 |
| D-V2 | No/invalid auth; hidden main pair; missing/deleted session; legitimate child/unowned legacy session; each latest/before/after/around and bad-limit variant | Authentication/access/precedence | Existing gate outcomes before target lookup; no invented user-owner restriction, no parent fallback | B20 |

#### Dataset D-E — Failure surfaces

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-E1 | Unreadable partition/index, canceled snapshot read, failed required correction lookup | Storage/open failure | No wipe/complete, prior view/cursor retained, visible recovery | B04 |
| D-E2 | Offline/timeout/500 first older request; Retry fails then succeeds; plain-list fallback | Dependency/recovery | One request at a time, error persists, same boundary, current chat still usable | B10 |
| D-E3 | Wrong session, has_earlier without next_before, cursor with false flag, empty nonterminal, cursor repeats, duplicate contradictory ordering; valid metadata with one malformed message | Protocol corruption/per-item | Reject whole contradictory page; valid page retains visible placeholder for bad item only | B23 |

#### Dataset D-M / D-U — Memory and real acceptance

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-M1 | 499/500/501 groups; 100 backward/forward page traversals; active call owned near eviction edge; delayed images; live tail100/101 | Memory/cap±1 | Q1-selected behavior; no discarded prepend, anchor retained, whole-group cache release if A | B12 |
| D-U1 | Permissioned anonymized genuine long session, recorded host/OS/browser/viewport/commit, 30 opens and 30 normal-speed boundaries; network/reconnect/clear/target lanes | Real-session external acceptance | Q4-approved metrics plus screenshots/traces and independent validation; no synthetic-only PASS | B25 |

### Regression Test Requirements

Existing functionality is modified. Preserve all unrelated assertions; only explicit full-history/envelope/earlier-note expectations change with their replacement tests, never by skipping/deleting coverage. No pre-existing failure is exempt from release responsibility.

| Existing behavior / read test source | Must remain / replacement |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_view_test.go`::TestReadTranscriptView_StartsAtTheLastClearMarker / TestReadTranscriptView_UsesTheLastOfSeveralMarkers / TestReadTranscriptView_OnlyTheExactMarkerCuts | Full view helper retains its contract if retained; bounded-page tests separately assert marker/end semantics. Do not globally change full ReadTranscript. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_effects_test.go`::TestToolCallEffects_AppendOnlyAndMergeRules / TestProjectToolCalls_SurvivesDayRollover | Existing full-reader merge/durability assertions unchanged; T04/T06 add bounded-reader parity and indexed late effects. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/input_disposition_test.go`::TestInputDisposition_LabelsOnlyTheDiscardedEntryAndLeavesArchiveBytes; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/cancel_truncation_derive_test.go`::TestReadTranscript_CancelTruncatesOnlyItsTurnsLastAssistantEntry | Old projection semantics unchanged; add cross-page/cross-day equivalents. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/ws_attach_catchup_test.go`::TestAttach_H4_IncrementalWhileStreaming_NoGapNoDuplicate / TestAttach_H6_PersistedBetweenBindAndRead_NoDuplicateNoGap / TestAttach_N4_UnreadableHistory_NeverWipesTheClient / TestAttach_FailedRebuild_NoCatchUpComplete | Ordering/failure assertions remain; fixture hook may accept page options, not weaken the invariant. T13 adds cropped-page ownership/context and controls. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/clear_view_test.go`::TestGetSessionMessages_StartsAtTheLastClearMarker / TestAttach_SnapshotReplaysSinceTheLastClearMarker | Migrate array/header to envelope/page metadata, retain exact old/new-content/marker assertions. Add explicit before-marker reachability. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.virtualization.test.tsx`::VirtualizedMessageList; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/cursor.test.ts`::gateFrameBySeq / applySnapshotHistoryWipe | Virtualization, live streaming and plain fallback remain; transport cursor/wipe semantics remain. DOM-node cap alone is not a memory/performance proof. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.clear-marker.test.tsx`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.earlier-note.test.tsx` | Divider/unknown-row/refusal semantics remain. Interim note-only assertions deliberately replaced with actionable earlier-history tests, not merely removed. |
| Train 3 T3 anchor regressions | Continue unchanged after rebase; chat tests adapt their measured-layout failure modes rather than altering room behavior. |
| Interim replay-render-once fix | Its tests are not yet read/known. After landing, enumerate and retain them; demonstrate one historical snapshot commit with bounded paging and no bottom jump on older-page commits. |

Regression dataset: D-P1b/C/D-R1/D-R2/D-R3 preserve old identity, special rows, tool statuses, pending controls, steering and clear operation ownership; D-G3 protects the measured Train 3 failure; D-E1 protects failed-read/no-wipe. These run red/green as relevant on tests-only/candidate CI commits, with named tests and saved logs/exit codes. This author ran none of them.

## Functional Requirements

| ID | Normative requirement |
|---|---|
| FR-001 | MUST open on a bounded recent page approximating four screens using D5's bootstrap/measured sizing, never lifetime read/replay. |
| FR-002 | MUST select newest-first with early stop/lookahead and return ascending acceptance order from one shared reader for messages/detail/snapshot. |
| FR-003 | MUST use same-session immutable occurrence positions, exclusive before/after and actual continuation; transport/model cursors remain distinct. |
| FR-004 | MUST preserve latest-clear default view, explicit one-barrier earlier crossing and operation-owned clear refresh without restoring model context. |
| FR-005 | MUST keep complete tool owner/start/result groups and all current projection/dependency semantics, including late effects, cancellation, disposition and real child lifecycle state. Q3 qualifies oversized groups. |
| FR-006 | MUST preserve incremental journal completeness, bind/hold/live order, active projection, current controls/goals/attention semantics and failed-snapshot/no-complete behavior. |
| FR-007 | MUST use pixel-distance prefetch, measured running average including drawn tool rows, 80px seed and client request clamp20..100; server validates, default40. |
| FR-008 | MUST preserve current reader anchor across prepend/control changes/late height measurement/live growth using stable keys and signed corrections; plain fallback remains usable. |
| FR-009 | MUST coalesce in-flight same-boundary reads and fence/abort obsolete results after session/view generation changes; historical loads never alter live cursor/control state. |
| FR-010 | MUST expose visible per-direction error/Retry, reject contradictory pages, preserve current rows and allow ordinary live use through an older-page failure; terminal history stops fetches. |
| FR-011 | MUST load an authorized target neighborhood directly, include available before/after context, highlight it, support contiguous newer reads/Return to latest and refuse missing targets truthfully. Q2 settles its producer scope. |
| FR-012 | MUST implement Q1's explicitly selected lifetime-window policy, preserve bounded opening/replay in either case and never let oldest-drop silently discard a fetched prepend. |
| FR-013 | MUST apply actual existing auth/main/session access on every mode and validate limits/selector syntax/same-session positions with D11's precedence and documented outcomes. |
| FR-014 | MUST preserve saved-session/child isolation/public-field parity, degrade invalid individual rows visibly, expose no private archive payload/marks and use generated contract types/validators only. |
| FR-015 | MUST instrument bounded reads/replay/anchor/request/memory behavior without message content and meet Q4's approved real-session performance/normal-use acceptance envelope. |
| FR-016 | MUST rebase onto landed Train 3 and interim replay fix before integration delivery, reuse their applicable behavior without duplication, update DOC-1..3 and complete real reachability/UAT plus independent validation. |

## Success Criteria

| ID | Measurable completion condition |
|---|---|
| SC-001 | Healthy indexed opening decodes no omitted prefix content and replays only selected complete groups plus documented relevant context/current state; test controls detect a full-read fallback. |
| SC-002 | Every ordinary page in fixture traversal is ascending/exclusive and continuation correct, with zero duplicated displayed occurrences/tools, orphan finished results or stale-response writes. |
| SC-003 | Real-browser anchor drift ≤1 CSS pixel at settled page/layout transitions in the approved envelope, with zero viewport history gaps over 30 normal-use boundaries. |
| SC-004 | Q4-approved p95 server/UI targets pass with saved host/fixture/commit/raw samples and prefix-scaling counters; no substitute stopwatch-only proof. |
| SC-005 | Opening replay and retained state are independent of total retained prefix; Q1-selected policy passes its 100-page/memory/cache test, with no lifetime-bound claim under B. |
| SC-006 | All four user stories are reachable through actual UI/API entry points; a genuine long-session campaign passes independent validation on the exact landing commit. |
| SC-007 | Generated contract verification and required backend/frontend/regression/security/docs gates are clean or founder-explicitly deferred with tracked issues; none claimed run by this Draft. |
| SC-008 | Q1–Q4 are answered and reflected in contracts/tests/docs before approval; Status remains Draft until the team's actual approval process says otherwise. |

## Traceability Matrix

| Requirement | User Story / Acceptance | BDD | Tests |
|---|---|---|---|
| FR-001 | US1.1–2 | B01, B02, B03, B24 | T01, T07, T12, T16 |
| FR-002 | US4.1, US1.1 | B01, B18, B21, B22 | T01, T04, T06, T12, T18 |
| FR-003 | US3.2/4, US4.1–2 | B02, B14, B15, B17, B19, B22 | T01, T02, T05, T11 |
| FR-004 | US1.2, US3.1 | B03, B11, B13 | T03, T14, T16 |
| FR-005 | US1.4, US4.1 | B05, B18, B24 | T04, T12, T13 |
| FR-006 | US1.3–4 | B04, B05, B06 | T08, T13, T17 |
| FR-007 | US2.1 | B01, B07 | T07, T16 |
| FR-008 | US2.2, US3.4 | B08, B09, B17 | T10, T16 |
| FR-009 | US2.1/2/4 | B06, B07, B08, B11, B23 | T08, T14, T15, T17 |
| FR-010 | US1.3, US2.3, US3.3–4 | B02, B04, B10, B16, B17, B23 | T01, T11, T13, T15, T17 |
| FR-011 | US3.2–3 | B14, B15, B16 | T05, T15, T17 |
| FR-012 | US2.5 | B12, B15, B25 | T09, T16, T18 |
| FR-013 | US4.2–3 | B16, B19, B20, B23 | T02, T11, T15, T17 |
| FR-014 | US1.2/4, US4.1/3 | B05, B18, B20, B22, B23 | T04, T11, T12, T15 |
| FR-015 | US4.4 | B07, B09, B21, B22, B25 | T06, T07, T10, T18 |
| FR-016 | US1–4 | B09, B13, B14, B25 | T10, T14, T16, T17, T18 + docs audit / dependency checks |

All 25 BDD scenarios have at least one FR and test above; test cases trace back through BDD to the numbered acceptance scenarios. Conditional Q1/Q2/Q3/Q4 cases must be made single-valued after the interview, not silently selected by an implementing agent.

## Ambiguity Warnings

| ID | Unclear point / tempting agent assumption | Resolution boundary |
|---|---|---|
| Q1 | “Bounded memory” might mean only opening (the issue's exact Done when) or a whole long browsing visit. Existing 500-message ring is insufficient for prepend. | Founder chooses lifetime eviction scope; D9 defines recommendations, B12/T09/D-M1 record both, no silent choice. |
| Q2 | “From search” could commission transcript search even though current Sessions search has no message hits. | Founder selects reachable direct-target loading now versus additional message search. |
| Q3 | A source entry can contain more than 100 drawn tool rows; strict maximum and atomic groups can conflict. | Founder selects explicit atomic overshoot or strict-cap fragment design; no false max100 assertion. |
| Q4 | “Fast” and “normal use” give no host/latency/scroll envelope or p95 threshold. | Founder approves/modifies the proposed numeric envelope before it becomes a release gate. |

No other product choice is filled in from memory. Technical defaults (40 bootstrap, 100 measured samples, opaque occurrence position, shared reader and exclusive semantics) are this Draft's architect proposals grounded in C1–C17/issue/T1–T3, subject to the normal spec review. No author adjudication of a later dispute over this design: escalate to the founder under recusal.

## Evaluation Scenarios (Holdout)

These are post-implementation evaluator prompts, not development tests; deliberately excluded from TDD/traceability. Because this is a single shared spec, they are **not secret holdouts**. Team-lead gives the independent evaluator a separate unrevealed genuine session/target/failure schedule and does not pretend publishing these descriptions hides them from implementers.

| ID / category | External setup and action | Expected observation |
|---|---|---|
| H1 / Happy Path | Open an evaluator-selected old genuine session on a normal device. | Newest messages/control state appear within the approved envelope without lifetime transfer; evidence names the commit/device/data. |
| H2 / Happy Path | Read old work naturally through several screenfuls while another viewer/agent continues the conversation. | No normal-use gap or forced bottom jump; old and new tool states remain truthful. |
| H3 / Happy Path | Follow an evaluator-selected old-target link, then return to latest. | Correct target and nearby context without a tail walk; latest is actually current. |
| H4 / Error | Lose network connectivity just before an earlier-page response. | Current rows and position remain; failure is visible and explicit Retry recovers when connectivity returns. |
| H5 / Error | Follow a real expired/foreign target link while signed in to the legitimate session. | No unrelated content or guessed replacement; truthful refusal and recovery. |
| H6 / Edge Case | Cross a real clear divider while images and a tool row expand above the reader. | Divider remains chronological, model context unchanged and reading position stable. |
| H7 / Edge Case | Switch away/back rapidly during old requests and cross the selected memory-window threshold. | No cross-view rows/errors; selected eviction policy remains reloadable and actually bounded if A. |

## Assumptions and Clarifications

| Item | State |
|---|---|
| Interview gate | The dispatch explicitly accepts the founder's written issue model as the interview and asks for a Draft with up to four open questions. It does not approve unresolved recommendations. |
| Architecture feasibility | Indexed newest-first projection is inferred feasible from addressed archive reads, not proven by a spike or benchmark in this authoring task. |
| Live reconnect protocol | Current C7/C13 code, not dated comments about timestamps/full history, governs invariants to preserve. |
| Dependency availability | Train 3 object/reuse code was read. Interim replay branch behavior is dispatch-supplied intent only; landed code must be verified. No cherry-pick/rebase occurred in this spec task. |
| Test status | All new test names/data are plans. Existing test source was inspected; no test, build, typecheck, browser run, CI campaign or UAT was executed by this author. |
| Delivery date | No deadline, worker cap or runtime work timeout is introduced. |

## Open questions for the founder

### Q1 — Does the memory bound apply throughout reading, or only on open?

The issue explicitly bounds memory/replay **on open**. The current browser drops the oldest entry above 500, which would discard newly prepended history. Keeping all intentionally fetched rows is simpler but lets a long visit grow; a movable bounded window adds forward reload behavior but preserves a genuine lifetime bound.

| Option | Outcome |
|---|---|
| **A — Recommended** | Reader-centered 500 complete-group window plus bounded 100-group latest cache, reload either evicted edge, never evict the visible anchor or current pending/active controls. D9/T09 apply. |
| B | No history eviction during this visit; release on leave. Guarantee bounded opening only and document growth while reading. Remove the oldest-drop behavior for browsed history rather than pretending 500 still bounds it. |

One-line answer: **Q1 A / B**.

### Q2 — What should produce the old-message jump in this issue?

The current Sessions search finds conversations, not messages (C16). The neighborhood loader is required either way, but inventing a transcript search engine would expand this feature and its access/performance work.

| Option | Outcome |
|---|---|
| **A — Recommended** | Ship the around-target reader and a validated browser deep link for a known session/message ID now. Keep existing Sessions search unchanged; connect a future message-search result to this same route. |
| B | Include real message-content search and its result→target UI here. A separate founder scope for search coverage/indexing/privacy is required before this spec can be approved; do not guess those requirements. |

One-line answer: **Q2 A / B**.

### Q3 — Is 100 a strict returned-row cap even for one compound tool entry?

One saved entry may contain narration plus many tool rows. The issue says count drawn rows and clamp fetches at 20..100, while a page must not split a tool from its owner/result. Treating WS frames as rows or silently cutting a compound entry would be wrong.

| Option | Outcome |
|---|---|
| **A — Recommended** | Clamp the requested target to20..100, stop at whole owning groups and permit an explicitly documented/logged exceptional overshoot for one indivisible group. Keep existing encoded-record/result protections; do not call 100 a hard returned-row/byte bound. |
| B | Require a strict returned-row cap100. Commission a contract-defined fragment/continuation representation for oversized owning entries, preserving per-call start/result/owner identity. That additional contract design must be resolved before implementation, not replaced by truncation or silent omission. |

One-line answer: **Q3 A / B**.

### Q4 — Approve or replace the performance and normal-reading envelope?

The model specifies screens and ahead-loading, but not a measurable meaning of “fast” or network conditions for “no gap.” Without a stated envelope a test could pass on a tiny session or fail under arbitrary latency. The performance section proposes a 100,000-group/100 MiB fixture plus a genuine long session, recorded reference machine/browser, p95 server 300ms warm/750ms cold-process and UI1.5s, and no gaps at ≤1 screen/second with page responses ≤500ms.

| Option | Outcome |
|---|---|
| **A — Recommended** | Use those provisional targets, record the reference host/browser and actual genuine-session size, and require the measured evidence and instrument controls before completion. |
| B | Replace the targets/envelope with your specified numbers. Do not silently weaken them after a failed run; interview before spec approval. |

One-line answer: **Q4 A / B + replacement targets**.
