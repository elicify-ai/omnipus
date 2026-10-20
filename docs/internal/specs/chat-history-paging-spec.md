# Feature Specification: Chat history paging

Status: Draft
Created: 2026-10-21
Amended: 2026-10-21 — fix round 1, after the founder's review interview
Issue: https://github.com/elicify-ai/omnipus/issues/1266
Size: Feature
Author: architect
Repository/worktree: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266
Code grounding revision: `5c2753da7fbf4caa728e66e3c7a6a89c3712690d`
Round-1 input: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/specs/chat-history-paging-spec-review.md`, commit `7b6cc0884`
Branch: `feature/1266-chat-history-paging`

Correction: the first Draft required waiting for unlanded dependencies, proposed an unreachable message-target route, and mixed automatic index rebuild designs. Those clauses are superseded by the founder's 2026-10-21 answers: build now, gate only landing order, remove message-target navigation entirely, and ship a separate offline operator script rather than a product upgrade path. The founder selected the 500-group reader window plus 100-group tail cache and subsequently amended Q4 in this same round to add a soft page-byte budget subordinate to a two-screen content floor and whole-tool-group atomicity. This correction does not claim implementation, independent review approval or runtime acceptance.

## Decisions log

The following answers are recorded verbatim in substance from the founder's round-1 dispatch, dated **2026-10-21**. **R1-Q** labels refer to the review interview; they must not be confused with the first Draft's differently numbered questions.

| Decision | Founder answer / authority | Normative effect |
|---|---|---|
| R1-Q1 = B | Build now on the current release/v0.1.1 base; catch up with Train 3 and the chief's interim replay fix, branch `fix/chat-replay-render-once`, as each lands. **Only the landing ORDER is gated.** The chat query parser is written in this feature; do not depend on parseHistoryQuery in the room handler. The interim fix is re-read before RED. | D11/D12: no dependency-landing gate on spec approval, RED or GREEN; no cherry-pick of the unlanded train. Team-lead owns the pre-RED source handoff and subsequent catch-up/landing coordination. |
| R1-Q2 — removed requirement | Founder: **“to jump to a specific message is no requirement.”** | No message-neighborhood loader, message-target route parameter, search-hit integration or copy-link action. The old-target stories/scenarios/requirements/tests/datasets/documentation/reachability work is removed, not renamed. Show earlier messages across a `/clear` divider remains required. |
| R1-Q3 = greenfield | **NO upgrade/migration path and NO preparing-history state in the product.** New sessions get their index as they are written. Existing data on the founder's live instance gets a **SEPARATE one-off migration script**, run by team-lead with the instance stopped after a full backup. The script ships with the feature, is tested, idempotent, has dry-run output and checks the backup precondition. A missing product index is a clear error with Retry, never silent empty/partial history. | D1/D13: no boot-time backfill, background/lazy rebuild, automatic 503/retry cadence or progress UI. The offline script is its own deliverable; it does not add an application upgrade service. |
| R1-Q4 — initial interview answer, later amended below | Keep the **window plus the 100-group tail cache**, initially **row cap only, NO byte budget**; the review concerns were accepted on that basis. Keep the oversized-group and performance-envelope answers as drafted. | Historical decision record only for the no-byte-budget clause: the same-day amendment below supersedes it. The 500+100 group policy, atomic-group exception and numeric performance envelope remain selected. MAJ-008 remains founder-accepted. |
| R1-Q4 amendment — 2026-10-21, same fix round | Keep the drafted window + 100-group tail cache + row cap and **ADD a page size limit**: **(1)** soft byte budget per page; **(2)** it must never be so small that the user has less than two screens to scroll—the budget never cuts below content needed for at least two screens using the measured/estimated row-height model; **(3)** never split a tool group, always at least one whole group where content exists. Pick a comfortably sized default and test huge-output/normal/open/prefetch cases. | D4a selects **4 MiB soft projected-page budget**, two-screen floor takes precedence, whole groups take precedence; row request cap remains20..100. Actual height is verified/topped up at the client. MAJ-002 is **fixed by founder-amended Q4**, not accepted as-is; MAJ-008 stays accepted. B37–B39/T27–T28/FR-021 cover the amendment. Do not reopen either decision. |

The initial written issue model remains the interview for recent-first opening, two-screen pixel prefetch, drawn-row sizing, seamless prepend, shared early-stop reader and real long-session UI acceptance, **except** that R1-Q2 removes its old-message jump sentence. This is one correction of the spec after grill round 1, not a new ADR or a second review verdict. Status remains Draft for the required round-2 grill/interview/fix and later approval.

## Discovery, authority and scope

Open a long conversation at its recent end without replaying its lifetime. Load approximately four chat screens initially. As the person scrolls upward, fetch approximately two more screens before the loaded beginning, preserving the reader's position. Earlier history remains saved and reachable by scrolling, including history before `/clear`. Display paging does not restore the agent's old model context or change execution.

| Dimension | Scope |
|---|---|
| Actors | A signed-in person reading main, existing extra, channel, scheduled, task or helper conversations; the existing gateway; an authorized paged API client; team-lead performing the separate offline data preparation. |
| Problem | Months-long standing conversations make opening, transferring and reconstructing history proportional to their lifetime. Virtualizing the display alone does not reduce server reads or replay. |
| In scope | Recent-page open, upward prefetch, stable scroll/focus, explicit clear-divider crossing, reader-centered bounded history and forward reload after eviction, Return to latest, shared REST/snapshot paging, reconnect while reading history, retention-aware continuation, visible errors/Retry, obsolete-read cancellation, observability and real long-session acceptance. The separate backed-up/offline script ships with the feature. |
| Priority | P1-high, matching the issue. Work can begin on the current release base; only integration landing order is gated by the overlapping branches. |
| Human acceptance | Open a genuine long session in the built UI; read upward during live activity; cross a clear divider; reconnect after sleep/restart while reading older history; use Retry/Return to latest with keyboard and assistive-technology checks. Retain geometry, screenshots, network and memory evidence on the exact candidate commit. |

### Non-goals

| Excluded work | Safeguard |
|---|---|
| Jump to a specific message, message-content search, a loader around a message, message-target route parameter and copy-link/permalink action | Entirely out of this issue by R1-Q2. Ordinary cursor paging and refetching an already loaded window are not an ID lookup or a new navigation destination. |
| Automatic upgrade, boot-time/history-open rebuild, background migration and product preparing-history state | No runtime fallback that scans lifetime content to manufacture a page. The separate offline script is the only preparation of pre-feature data commissioned here. |
| New model-context/recall/retention policy, rewritten accepted archive bytes or new chat creation | Preserve existing context, execution and deletion authority; adapt only paging's response to actual retention. |
| Room-paging redesign, room parser refactor, new agent tools/permissions, a second content store or external service | Reuse applicable semantics; do not import the whole room implementation or invent a runtime dependency. |
| Hard byte truncation below the two-screen floor or removal of the founder-selected tail cache | Amended R1-Q4 selects a soft budget, not a hard cut through required visible content/tools. Do not split/truncate groups or substitute a single-window policy. |

## Available Reference Patterns

The optional overview `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/reference/go-implementation/00-overview.md` was absent in the initial controlled inventory; the known existing control `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/reference/built-in-tools.md` was found. No Go reference implementation is invented. Use actual archive/index/read patterns and Train 3's semantic precedents below.

| Reference | Reuse / deliberate adaptation |
|---|---|
| C1–C5 | One append-only archive, stable internal addresses, content-free indexes and merge-on-read projection; the model-slot ordinal is not a chat-row sequence. |
| T1/T2 at `573c3674462650ce0f7af8e3e02de0fca50ae295` | Ascending contents selected backward; exclusive cursor; actual-entry lookahead; one request per boundary; stale-response fence; current live values winning overlap. Do not reuse a room-memory slice as a disk reader. |
| T2/T3, including `b3b437a354fb5588925bc20f228573aed7514e0a` | Same entry through every hint/loader/Retry layout, signed deltas including shrinkage, native anchoring disabled. Adapt to virtual measurements/images rather than copying a plain list. |
| C18/C19 | Existing expected-expiry versus I/O-failure distinction and catalogued UI controls. Do not report a swept partition as corrupt storage or a Skeleton as an announcement. |

## Existing Codebase Context

### Source register — evidence, not target implementation

Paths are absolute; citations use `file::symbol`, never line numbers. Initial-source claims below were read in this authoring session; round-1 additions/rechecks are C18–C21 and the frame/Train 3 seams. Proposed types/files elsewhere are explicitly deliverables, not claims that those definitions already exist.

| Key | Evidence | Verified current seam |
|---|---|---|
| C1 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_view.go`::ReadTranscriptView / sinceLastClear / IsClearMarker; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/input_disposition.go`::ReadTranscriptWithDispositions / InputDispositions | Full merged transcript/disposition read, then last exact clear-marker cut. HasEarlier currently describes that cut. |
| C2 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_effects.go`::readTranscriptMerged / applyToolCallEffects / transcriptPartitions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/unified_write.go`::ReadTranscript / deriveCancelTruncation | Later settle/projection/retract effects and cancellation change earlier public rows on read. Reversing raw lines is not equivalent. |
| C3 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/archive_day_store.go`::Append / ReadAtRaw / partitionForAppendLocked / partitionPathForKeyLocked / partitionGone; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/archive_record.go`::ArchiveAddress / ArchiveRecord | Stable disk-only partition/encoded-byte/checked-ID addresses, rollover by append day, 8 MiB record admission. **ReadAtRaw currently holds the archive mutex across content I/O**; D1 needs a snapshot-read seam rather than claiming this existing method is nonblocking. |
| C4 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/ordinal_index.go`::ordinalRow / OrdinalCount; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_indexed.go`::appendTranscriptLocked | Existing ordinal index covers model placements, not all chat occurrences; new chat writes mint missing IDs. |
| C5 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_partition.go`::transcriptPartitionPaths; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/daypartition.go`::TranscriptEntry / ToolCall / PartitionStore.ReadMessages; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/unified_conv_transcript.go`::convConvergeSessionTranscript / convRewriteChatLines | UnifiedStore is the gateway reader, not the older PartitionStore reader. The existing historical cutover can rewrite partitions; token stability can be promised only after final canonical bytes are fixed. No new such runtime path is added here. |
| C6 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/rest_sessions.go`::getSessionMessages / getSession / writeSessionDetail / withWireJudgeVerdict / jsonSessionStorageError | Bare messages array + X-Has-Earlier-Messages; detail messages/has_earlier_messages; both C1. Special verdict projection and redacted storage-error responses must be preserved. |
| C7 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/websocket_replay.go`::resolveSession / handleAttachSessionWithAck / sendSnapshot / sendIncremental / replayTranscript / failCatchUp / resumeLiveSession | Resolve access, bind/hold, read before wipe, replay then projection then complete, release held live frames, goal rehydrate/attention handling. Incremental uses journal frames. Failed replay must not acknowledge successful catch-up. |
| C8 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/replay.go`::streamReplay / emitFlatToolCall / emitSpawnParentToolCall / dispatchSpecialEntry / dispatchPersistedSubagentFrames / buildEntryMessage / buildReplayFallbackNote; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/ws_hub_projection.go`::projectionFrames / activeTurnProjection | Per-call start/applicable result; still-running delegate state; multiple special frame types and replay done. ID-only coverage needs care when a start was replayed but a terminal result was not. |
| C9 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/ws_session_hub.go`::sessionHub / hubRegistry / journalEntry | Live seq is transport order; journal budgets are 2,048 frames / 1 MiB per hub and 32 MiB global. Projection's 2 MiB target has existing exceptions; these are not page byte limits. |
| C10 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.tsx`::ChatScreen / VirtualizedMessageListInner / VirtualizerAutoFollow / PlainMessageList; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ClearContextDivider.tsx`::ClearContextDivider / EarlierMessagesNote | Outer-row measurement, fixed estimate80/overscan5, separate live tail/plain fallback. Tools can be inside an outer row. Earlier-clear text is not currently a pager. |
| C11 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/lib/api/sessions.ts`::fetchSessionMessages / fetchSessionDetail / parseWireMessageList / rawToMessage; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatThread.tsx`::ChatThread | Array adapter, visible malformed-item placeholder, header side channel and another messages consumer. |
| C12 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/session.ts`::applyMessageArray / bakeToolCallsByOwner / stampToolCallOffset; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/messages.ts`::MAX_MESSAGES_PER_SESSION / getMessages / evictMessageFromBucket | Oldest-drop 500-message ring would discard a prepend. Duplicate display IDs keep the last occurrence; tool ownership/text offsets already matter. |
| C13 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/slices/catchup-frames.ts`::handleSessionSnapshotFrame / handleCatchUpCompleteFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/cursor.ts`::applySnapshotHistoryWipe / gateFrameBySeq | Current snapshot wipes history; complete commits the live cursor. Preserving the historical reader is a target change, not existing behavior. |
| C14 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/clear-refetch.ts`::runClearRefetch / armClearRefresh / flushHeldClearProjection; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/has-earlier.ts`::recordSessionHasEarlier | A particular clear send owns its refresh; busy application is deferred; old markers cannot confirm newer operations. |
| C15 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/main_session.go`::gateMainRead / mainSessionAllowed / resolveMainSessionForRead; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/rest.go`::withAuth / jsonErr; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/auth.go`::checkBearerAuth | Actual authenticated session read and main-eligibility gates. No separate per-user read-ownership predicate is claimed. |
| C16 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/search/SearchModal.tsx`::SearchModal; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/useSelectSession.ts`::useSelectSession; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/session.ts`::attachToSession | Existing search opens sessions, not message hits. It remains unchanged; no new target route is commissioned. |
| C17 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/openapi.yaml`::getSession / getSessionMessages; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/asyncapi.yaml`::AttachSessionFrame / SessionSnapshotFrame / CatchUpCompleteFrame / MediaFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/Message.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ToolCallStartFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ToolCallResultFrame.yaml` | No chat-page envelope yet; snapshot describes a full replay. Tool frames lack persisted-source ownership. Shared WS schemas and inline mirrors must change together. |
| C18 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/retention_sweep.go`::RetentionSweep / expireArchiveSessions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/archive_expiry.go`::SweepExpired / sweepRolledPartitionsLocked / expireSession / advancePastExpiredLocked / rowResolvable | Round-1 source confirms rolled-partition expiry is called from retention; partial delete success is real; absent partition differs from unreadable/corrupt content. Main identity is protected while old rolled content expires. |
| C19 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/design-system/catalog.json`::Button / ErrorState / Skeleton entries; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/ui/button.tsx`::Button / ActionAnnouncement; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/ui/error-state.tsx`::ErrorState; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/ui/skeleton.tsx`::Skeleton | ErrorState supplies role=alert/Retry; Skeleton is aria-hidden and reduced-motion aware; Button supports pending feedback but pending disables the native control. D8 avoids unintended focus loss and duplicate announcements. |
| C20 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/store.ts`::withBucket / syncForeground / sessionsById | Buckets are keyed by session. No verified global inactive-reader eviction policy is assumed; D9 explicitly releases this feature's inactive history/tail contents. |
| C21 | Controlled `git ls-remote --heads origin fix/chat-replay-render-once work/train3-approvals-rooms`, round1 | Train 3 resolves to `573c3674462650ce0f7af8e3e02de0fca50ae295`; the interim branch is absent. A second exact-origin probe also returned no interim ref. Its implementation remains Unknown; PRE-RED-1 obtains it from the chief rather than inventing facts. |
| T1 | **Git object:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/RoomHistoryResponse.yaml`::RoomHistoryResponse; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/rooms/history.go`::History / historyPage at Train 3 | entries ascending, has_older/next_before_seq iff actual continuation, exclusive cursor, default100/server1..1000. |
| T2/T3 | **Git objects:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/rooms/RoomTranscript.tsx`::RoomTranscript / captureAnchor / contentOffsetOf; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/rooms.ts`::loadOlder; commit `b3b437a354fb5588925bc20f228573aed7514e0a` | Every-layout anchor correction, negative deltas, failure/Retry and native anchoring disabled. Existing measured drift receipt was read, not independently rerun. |
| T4 | **Git objects:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/daypartition.go`::TranscriptEntry.RoomRef; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/replay.go`::buildEntryMessage at Train 3 | RoomRef is server-stamped and projected as room_ref. Chat pages must preserve it after catch-up; it grants no room membership or execution authority. |

### Architecture constraints, manual impact and unavailable tools

The primary walkthrough `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/architecture/AS-IS-architecture.md` and extension assessment `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/architecture/plugin-extensibility-assessment.md` were read in this session. Their dated descriptions do not override C1–C21. The ADR title inventory was used to find applicable decisions, not to certify runtime behavior.

| Existing decision / rule | Constraint |
|---|---|
| **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::D2 / D8 / U2-ARCHSHAPE | One archive, public/private projection boundary, same-session clear and no restored model context. R1-Q3 specifically selects greenfield paging metadata; do not design another automatic cutover here. |
| **UI-independent turns and session-bound webchat streaming**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/architecture/ADR-082-ui-independent-turns-and-session-bound-streaming.md`::D1–D5 | Losing a viewer/aborting a page stops no execution. Keep session-bound progress/current turn state; current sequence-based C7/C13 code wins over old timestamp wording. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/CLAUDE.md`::Hard Constraints / Definition of Done; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/CLAUDE.md`::lock order | Pure Go backend/file storage, embedded SPA, contract-first generated types, no runtime service/dependency, existing shard order and exact-commit UAT. |

GitNexus MCP discovery again returned **No matching deferred tools found**. No query/impact/detect_changes result is claimed; direct searches found the known three C1 gateway callers and C18's production sweep chain before relying on absence searches. Jev tools were also unavailable. No production symbol is edited in this correction.

| Planned seam | Manual blast radius — **Inferred**, not graph output |
|---|---|
| Shared page/index projection | REST messages/detail, attach snapshot, chat/effect/disposition writers and retention; preserve full/model readers. High cross-tree scope. |
| New snapshot-read cut | Session shard + archive mutex capture versus slow content I/O, append/cancel/retention races. Existing ReadAtRaw cannot simply be wrapped in the shard for the whole page. |
| Per-frame historical origin | Existing replay/special/media emitters, generated schemas, reducer ownership and current/live coverage. No frame-range side protocol. |
| Chat state and geometry | Virtual/plain list, cursor/wipe, pending input, clear intent, controls, far-edge eviction and inactive-bucket cleanup. Extract from the 3,267-line ChatScreen rather than adding another large conductor. |
| Offline script | Operator-only canonical-file inventory, backup verification and derived metadata; no application constructor with implicit conversion or model/credential/network behavior. |

Manual flows: session selection → attach → bounded snapshot/incremental journal → live; scroll → page → anchored prepend; snapshot during reading → tail/control refresh + bounded existing-window revalidation; clear → operation-owned latest page; retention → truthful retained boundaries; stopped instance + verified backup → dry-run/apply script. These are not invented GitNexus processes/clusters.

## User Stories & Acceptance Criteria

### User Story 1 — Open a long conversation quickly (Priority: P1)

A reader opens a standing conversation and sees recent messages and truthful current controls without transferring months of history.

**Why this priority:** primary issue cost. **Independent Test:** compare equal recent suffixes with short/long prefixes.

1. **Given** a long conversation, **When** it opens, **Then** approximately four screens appear at the recent end without a lifetime content read/replay.
2. **Given** an empty, short or cleared conversation, **When** it opens, **Then** only actual available content appears, respecting the newest clear boundary.
3. **Given** unavailable storage or required paging metadata, **When** opening is attempted, **Then** a clear error and Retry preserve any existing view; neither empty success nor automatic data preparation is invented.
4. **Given** live tool/steer activity and a lost browser connection, **When** the viewer reconnects, **Then** current controls/progress and historical tool ownership remain correct without duplicate or orphan rows.
5. **Given** normal or huge-output history, **When** a page is selected under the soft size budget, **Then** bytes limit unnecessary extra content only after the modeled two-screen floor; whole groups remain and normal content is not needlessly cut.

### User Story 2 — Read continuously, including across reconnect (Priority: P1)

A reader scrolls upward, continues toward newer history after eviction, or deliberately returns to the live end. Variable heights, failures and reconnects do not take away their place or keyboard focus.

**Why this priority:** seamless reading is the founder's model, not an optional optimization. **Independent Test:** variable-height reading with delayed pages, live traffic, forced snapshot reconnect and keyboard-only recovery.

1. **Given** earlier history exists, **When** the reader comes within two visible chat heights of its loaded beginning, **Then** one background page targets two screens using measured drawn rows and request bounds20..100.
2. **Given** a pending page, **When** it arrives, **Then** current anchor/offset and live values are preserved without duplicates.
3. **Given** a page request fails, **When** its failure arrives, **Then** the current view stays with a visible error/Retry and ordinary sending/Stop remain usable.
4. **Given** a page in flight, **When** the person changes conversations or an intentional clear replaces this view, **Then** stale success/error cannot affect the replacement.
5. **Given** the 500-group reader boundary, **When** browsing continues, **Then** whole far-edge groups are evicted/reloadable, not the new prepend or visible anchor; the latest cache stays at100 groups.
6. **Given** a person reading old history, **When** reconnect requires a fresh snapshot, **Then** their window/anchor stay, current controls/tail refresh, and the already loaded range is revalidated without walking from the new tail.
7. **Given** detached historical reading, **When** Return to latest or a newer-page action is invoked, **Then** its own loading/error/success state is visible, no hidden middle gap is displayed and pending user input is not resent or lost.
8. **Given** keyboard-only operation, **When** Show earlier, Retry or Return to latest is activated, **Then** actions are accessible and focus follows D8's deterministic policy without a scroll jump.
9. **Given** assistive technology or reduced-motion preferences, **When** a history page changes state, **Then** status/failure is announced once, historical rows are not announced as new answers, and no smooth-scroll/shimmer requirement overrides reduced motion.

### User Story 3 — Read across a clear divider (Priority: P1)

A person deliberately reads older saved conversation without changing the agent's freshly cleared context.

**Why this priority:** explicit founder requirement and real earlier-history reachability. **Independent Test:** two clear segments, including an underfilled viewport with no scrollbar.

1. **Given** a closed clear divider with retained predecessors, **When** Show earlier messages is chosen, **Then** the preceding segment's page is prepended through the ordinary mechanism; the divider and model context remain unchanged.
2. **Given** the earliest retained boundary, **When** reading reaches it, **Then** no more-history control/fetch loop or phantom cursor remains.
3. **Given** a failed explicit clear-crossing request, **When** Retry is chosen, **Then** that boundary is retried without resending `/clear`, losing the current segment or opening a second barrier silently.

### User Story 4 — Trust projection, access, expiry and evidence (Priority: P1)

A reader/client gets corrected authorized history, truthful continuation after expiry and auditable performance/privacy evidence.

**Why this priority:** a small but incorrect page does not solve the problem. **Independent Test:** real store/handler/replay parity, hostile inputs and controlled sweeps.

1. **Given** the same readable session/read cut, **When** the history carriers are compared, **Then** ordered occurrence identities, tool ownership and projected public fields agree.
2. **Given** invalid paging parameters, **When** a request is made, **Then** its documented refusal occurs without clamping bad external input or substituting the latest page.
3. **Given** missing authentication or unreadable session authority, **When** any page mode is requested, **Then** existing refusal wins before paging/position lookup.
4. **Given** a genuine long-session campaign, **When** it is independently evaluated, **Then** actual UI/network/geometry/heap/read evidence meets the adopted envelope, not merely green unit tests.
5. **Given** retained content changes through a sweep, **When** the next page is read, **Then** only remaining eligible groups determine continuation; expected expiry is not an endless storage-error Retry.
6. **Given** a slow history read, **When** a new entry is appended, **Then** history content I/O does not hold the session/append locks hostage, and the page still represents a consistent cut.
7. **Given** successful, failed or aborted history reads containing diagnostic canaries, **When** logs are captured, **Then** required counts/failure classes appear without bodies, parameters, result content, raw positions/addresses or arbitrary query strings.

### User Story 5 — Prepare existing founder data offline, explicitly (Priority: P1)

Team-lead prepares the founder's current canonical data with the instance stopped and a verified full backup, independently of ordinary product operation.

**Why this priority:** greenfield runtime behavior must not strand the founder's existing data or hide a migration in first open. **Independent Test:** real temporary data/backup trees, dry-run, apply, repeat and interruption checks before touching live data.

1. **Given** a stopped instance and verified full backup, **When** dry-run is invoked, **Then** its deterministic plan/counts are shown and nothing is written.
2. **Given** missing/incorrect backup evidence, a running instance or changed source data, **When** preparation is invoked, **Then** it refuses with a nonzero exit and no canonical write.
3. **Given** a completed or interrupted prior preparation, **When** apply is rerun, **Then** completed matching indexes are unchanged, unfinished derived staging is safely resumed/replaced and canonical content/identities remain byte-identical.
4. **Given** a session has no valid index at ordinary product read time, **When** its history is opened, **Then** the product shows a minimal error with Retry, never a backfill/preparing state or a silently partial/empty transcript.

## Behavioral Contract and Edge Cases

| When / boundary | Then |
|---|---|
| Normal open | Recent bounded page at bottom, then live; default40 row units until actual geometry is known. |
| Within2H of loaded beginning | One older request with clamp(ceil(2H/A),20,100); current average also estimates unmeasured virtual rows. |
| Page/control/image height changes | Same current visible anchor and offset, signed correction, no total-scrollHeight compensation. |
| Fresh snapshot while reading old history | Keep reader rows/measurements/focus/anchor; refresh tail and authoritative current state separately; bounded existing-range refresh never returns the reader to latest automatically. |
| Retention removes a boundary or middle partition | Skip expected expired groups; actual lookahead/continuation updates; no guessed result or permanent500 at every old edge. |
| Clear barrier reached | Keep closed until explicit Show earlier; one activation opens one preceding segment, never restores model context. |
| Last retained page, including exactly full | has_earlier=false, no next_before and no poll loop. |
| Empty session, missing index or malformed row | Empty only for proven no eligible content; missing required index is error; malformed individual row has a stable visible placeholder. |
| One atomic group exceeds100 drawn rows or the soft byte budget | Keep it whole. The two-screen estimated content floor can also require an atomic boundary overshoot; report the override. Never split start/result or cut required visible coverage to fit bytes. |
| History error, including reconnect revalidation | Retain already displayed rows; inline action-specific error/Retry; current live controls keep working. |
| Leave/switch | Abort/fence reads and release this feature's inactive reader/tail payloads; no agent cancellation. |

### Explicit safeguards and machine-verifiable constraints

| Area | Constraint |
|---|---|
| Requests | External limit is one digits-only decimal integer20..100, default40; client estimates clamp, invalid external values reject. before/after are exclusive. No message-ID lookup or neighborhood selector is added. |
| Group/order/bytes | Newest-first selection, ascending acceptance-order output, whole groups, request row target20..100/max100 source groups per response except an indivisible group's drawn weight. D4a applies a4 MiB soft budget only once the two-screen floor is satisfied; row-cap-limited responses are not shortened further by bytes and contiguous top-up meets logical screen coverage. No lifetime slice/cache fallback. |
| Continuation | Actual retained eligible lookahead, not fullness or sequence gaps. Known expired positions remain boundary selectors via content-free order tombstones; unknown/foreign tokens are404. |
| Reader memory | Active reader≤500 source groups, latest cache≤100 groups, atomic groups together. A page's soft byte target is not a hard total-heap ceiling. On leave release loaded history/media/results/measurement caches; preserve only existing necessary current/pending control state. |
| Snapshot | Only successful catch_up_complete advances the live cursor. Older-page/range refresh never changes it, acknowledges attention, emits an old ask/approval or runs historical content as a command. |
| Focus/geometry | ≤1 CSS pixel settled anchor drift in the adopted reference envelope; preserve actual focus except the explicitly defined removed-control/expired-anchor fallback. No forced bottom jump from passive history updates. |
| Runtime missing metadata | Redacted500 plus minimal ErrorState/Retry; no503, preparation progress, automatic rebuild, false success or repeated automatic repair attempt. |
| Privacy | Public messages/positions and new diagnostics exclude private source/routes/model payloads/physical marks. New logs have no text/result/parameter/raw-query canaries. Existing privileged underlying-storage cause logging is not silently redesigned. |
| Independence | Model context, retention policy, Stop/steering authority, room behavior and tool policies are unchanged. No newly callable agent tool exists. |

Use generated wire types, existing internal address/ToolCallID invariants and local non-wire anchor/request state. A history position is not a transport cursor or model ordinal. Do not introduce nominal wrappers without additional invariants.

## Prerequisites, Development Setup and Tech Stack

| Topic | Source-grounded requirement |
|---|---|
| Platforms/runtime | Existing supported Linux/macOS/Windows gateway and embedded SPA; pure-Go backend, Go1.26.9 from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/go.mod`. React19, existing virtual-list/query libraries and Vitest4.1.11 from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/package.json`. No new application runtime dependency. |
| Services/auth | Existing authenticated gateway origin; paging needs no model call, new credential, database server or monitoring service. |
| Administrative script | Separate optional Python3 standard-library script, not imported/invoked by the product. Python is an operator-maintenance prerequisite only, not a gateway dependency. Team-lead tests it on copied data before live use. |
| Application setup | Follow `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/CLAUDE.md`::SPA embed / Running it locally and root build/test rules. Use the built gateway with fresh embedded assets, not stale SPA files or an unrelated dev server. Team-lead provisions the isolated data root/port. |
| This correction | No local heavy command, contract generation, application run or UAT. No setup/gate is claimed executed. |

Expected first open: newest bounded page and truthful current controls. Known setup traps remain absent SPA embed, missing build tags and bare root TypeScript no-op checks; none is a paging verdict. No new start/stop/health command is commissioned. Data remains file-based in the existing session directories.

## Deployment / Runtime and Integration Boundaries

Deploy matching backend/contracts/embedded SPA together; the messages array becomes an envelope. Old browser tabs reload. Do not add a permanent dual-array/full-history fallback.

Fresh sessions have a valid empty index established with session creation before history can be served, and every accepted archive append (including private model/model_ref records, which add no public row) advances its covered-tail metadata; chat/effect/disposition writes additionally maintain their projection references. Restart validates/reuses present metadata; it does not build missing metadata or scan an uncovered lifetime. A missing/corrupt/mismatched index, or a committed archive tail not covered by a validated index after a fault, fails visibly until operator repair. Canonical durable data is not rolled back or reported unsaved because derived publication failed. Repair of existing data is **D13's separate stopped-instance utility**, never ordinary product startup/open. This feature adds no generic importer/converter and no preparing-history UI.

| Boundary | Data / failure / development approach |
|---|---|
| Canonical archive → page | One public projection and selected correction/dependency references; no private content copies. Real file fixtures, deterministic append/sweep barriers; missing required index is error. |
| REST → SPA | Generated response/parameters and semantic continuation checks. Mock only network in component tests; real handler/schema integration tests. |
| WS → SPA | Same page reader, per-frame historical origin, existing projection/control/complete/live order. Real hub fixtures, no fake hub for catch-up claims. |
| Session navigation → history | Existing session routes/screens only. No new message route, parameter or search producer. |
| Offline utility → indexes | Backup-verified, stopped canonical data in; derived index generations and summary out. Dry-run writes nothing; product is never used as an implicit migration runner. |
| Train3 / interim | Build on current base; team-lead catches up as each lands. Re-read source before editing overlap and preserve room_ref/corrected anchors/render-once guarantees. |

## Technical Decisions and Affected Components

The following are target requirements, not assertions of implemented behavior. Founder decisions above are settled; technical refinements below are the architect's correction for round2 review.

### D1 — One bounded reader and a short immutable read cut

Add one UnifiedStore-owned bounded chat projection reader for REST messages, detail and snapshot. Keep intentional full ReadTranscript/model/recall readers unchanged. Reuse exact clear-marker recognition and C2's settle/project/retract/cancel/disposition rules. Neither reversing a fully decoded transcript nor invoking the older PartitionStore reader is acceptable.

Extend the existing private file-based content-free indexing approach with occurrence order/location, previous/next eligible links, clear marker, canonical duplicate ownership, correction/disposition locations and relevant span dependencies. Metadata references the canonical archive; it never becomes a second content history. No hot lifetime-content map. IDs/offsets remain private except the public fingerprint positions.

| Invariant | Required design |
|---|---|
| Early stop | Traverse eligible group metadata newest-first; decode/project at most selected candidates plus the one lookahead needed for truthful continuation/budget choice. Apply D4a's priority: whole groups and estimated two-screen floor before soft bytes, then row target/soft byte stop or100 source groups/clear/end. A group that completes the floor stays whole even if it crosses a soft target. Never decode an omitted lifetime prefix to compute size. |
| Consistent cut | Under the addressed session shard, briefly capture a validated immutable index-generation/root, published archive/effect/disposition end marks and retention generation. Acquire archive state only in the existing shard→archive order. Selection/decode/projection content I/O then occurs **outside both the session shard and the archive append mutex**, using immutable index metadata and read-only bounded record/file views capped at those marks. Later appends/effects belong to later cuts. |
| Existing seam limitation | C3 ReadAtRaw locks around file I/O; a page must not call it while retaining the shard or serialize its whole decode behind the append mutex. Add/refactor an internal read-snapshot seam with the same checked-address/record bounds. Brief handle/metadata capture is allowed; slow content reading is not a lock hold. Test with a blocked read barrier and an actual append completing before that read is released. |
| Concurrency | Rollover cannot rebind a chosen partition key. Before publishing a response, validate retention generation/required handles and reselect if expiry raced the cut. Do not hold a hub lock while taking session locks; no global cache lock across I/O or arbitrary parent/child shard nesting. Existing all-shard retention exception remains, not another lock order. |
| Derived publication | New writes maintain metadata as part of the owned append protocol. Durable archive bytes remain authority if index publication fails: mark not-readable and expose error, never claim the saved message was unsaved or serve an incomplete index. Validate covered-tail equality on read/restart; no automatic tail scan/rebuild is introduced. |
| Cold/fault | Present valid metadata gives bounded reads on cold process start. Missing/corrupt/incomplete metadata gives the D11 error, not a normal empty page or an application repair worker. D13 repairs only while stopped/backed up. |

Metadata format/generation storage is backend-owned internal design; it must support immutable read cuts and script/product parity without duplicating the content authority. Streaming script construction is allowed only offline. Required bodies/dependencies that are unreadable fail the page; malformed-line behavior preserves C2's established visible diagnostic distinction rather than inventing a permissive legacy reader.

### D2 — Stable same-session occurrence fingerprints

Keep `hp1_` plus an unpadded base64url SHA-256 digest of a length-delimited tuple: canonical session ID, stable internal partition key, encoded record-start offset and recorded entry ID. The public token is exactly47 ASCII characters. It is a selector, not authorization; raw/reversibly encoded disk marks are forbidden. A private index binds it to a checked occurrence and its private acceptance-order rank. Hash collision/duplicate binding is an integrity error, never address substitution. Do not sort opaque token strings. For script/product agreement, the hash preimage is four UTF-8 strings in that order, each prefixed by its unsigned64-bit big-endian byte length; offset is its canonical nonnegative decimal string (no leading zeroes except0), and an absent malformed historical ID contributes an empty string. Encode the digest canonically with unpadded URL-safe base64. Freeze cross-language position vectors in T02/T25; do not derive their expected values by calling the product encoder.

Appends, clear, restart and an offline reconstruction of **unchanged final canonical bytes** preserve the token. A conversion/rewrite that changes encoded offsets is outside that promise: C5's historical conversion must be completed **before** creating indexes/tokens. The utility refuses unconverted/inconsistent input; it does not rewrite it. No old message ID, timestamp, array index, live seq or model-slot ordinal is substituted.

On retention, keep content-free position/order tombstones so a previously issued boundary can remain an exclusive/inclusive range selector even when that occurrence's body expired. It cannot return expired content or authorize another session. Tombstones carry no bodies/results and live only with the session's derived index. Unknown/foreign token remains404; a known expired boundary selects actual retained neighbors, often an empty terminal page. Session deletion removes its index/tombstones. D14 defines truthful expiry and damage behavior.

Public Message has readOnly history_position for each paged occurrence; original recorded id is preserved. Existing duplicate-message/tool last-canonical-occurrence display and diagnostics stay. Malformed identity uses a position-stable visible placeholder, not a newly minted persisted identity. Canonical source IDs and source positions remain distinguishable from coalesced display-bubble aliases.

### D3 — Latest, exclusive paging and refresh of an existing window

| Read | Parameters / selection |
|---|---|
| Latest | No boundary/range; limit defaults40. Newest groups since the latest exact clear marker, ascending output. Include the marker only if selection reaches it; do not insert a distant divider across an omitted gap. |
| Older | before=known history position, exclusive; nearest retained predecessors, ascending output. Include/stop at the first encountered clear marker. before that marker is the explicit crossing into its preceding segment. |
| Newer | after=known history position, exclusive; nearest retained successors ascending. Required to reload a far edge evicted while browsing backward. It is not a message-ID jump or a live seq. |
| Existing-window refresh | Paired window_start/window_end positions bound the **closed already-displayed range** in private acceptance order; optional before pages backward within it. No after in this mode. Used only to revalidate a retained reader after a snapshot/expiry, never centered on a message or navigated to by a route. Select at most100 source groups/request; stop at the saved lower bound. At most500 selected source groups are materialized for a500-group reader. Known expired endpoints remain order boundaries, so remaining in-range groups can be returned without an expired-ID search. |

Outside a range refresh, before/after are mutually exclusive. Range endpoints must be paired, same-session and ordered; its optional before must lie inside or at the upper boundary and remains exclusive. A range can include already opened clear dividers; it does not newly open an unopened barrier or fetch every intervening row from the current live tail. The UI retains exactly the previous reader's source-position interval for this operation. No around selector, message-ID resolver or new message route is part of the contract.

Generated SessionHistoryResponse has session_id, entries (public Message array, non-null), has_earlier, has_later and page_info; next_before/next_after are present iff their flag is true and equal the first/last selected position. has_earlier/has_later describe actual eligible retained groups in this read's domain (the explicit closed range for window refresh, ordinary retained archive otherwise), not an implied gap-free union with a detached live tail. Empty response has both flags false/no continuation; page_info.mode distinguishes empty range from an empty latest conversation. Only validated ordinary-page metadata replaces browsing continuation; window-refresh metadata never falsely declares the whole session empty.

page_info contains mode (latest/older/newer/window_refresh), effective limit/min_rows, row_count, source_group_count, soft_byte_budget/projected_bytes/stop_reason/override accounting from D4a, first_position/last_position when nonempty, optional stopped_at_clear, optional latest_clear {entry_id,history_position}, retention_generation and bounded context (D4). No entry-reference manifest, frame ordinal/range/count side protocol or duplicate REST identity list remains. An exactly-full terminal page still has no continuation. A reached clear with retained predecessors reports has_earlier=true and next_before at that marker, but automatic fetching is blocked until explicit activation.

### D4 — Atomic tools, local replay ownership and public parity

Select whole owning chat entries with their tool calls/current projection. Start/result of a finished call are one logical tool row for sizing and never split across pages. A pending tool has its start/truthful pending state, not a fabricated completion. Do not include an entire turn, a child's entire transcript or every event between a delegate start/end merely to complete its display group.

**Replace the initial replay manifest with generated per-frame ownership.** Introduce proposed shared HistoryEntryOrigin {position, entry_id?}; position is a47-character token, entry_id is the recorded nonempty ID when valid. Each persisted source entry's replayed content/tool/special/media frame carries `history_origin` naming that actual source occurrence. A single typed stamping seam receives the source origin in the shared replay entry context; a tool-only record can create its own non-prose group without relying on the last assistant bubble. REST already carries positions/IDs/tool calls on entries and has no manifest to reconcile.

| Frame class | Ownership / behavior |
|---|---|
| replay_message, tool_call_start/result, replay_error, replay_provider_fallback, context_window_notice, judge_verdict, browser_handover_notice, helper_event, goal_outcome | Persisted page replay carries history_origin; live/unsequenced non-history copies can omit it. A later result still joins its real call/owner, never whichever row precedes the page edge. |
| Persisted subagent_start/state/message/end; tool-derived media; source-positioned historical question card | Same local origin, not a frame-number relationship. Supplement selected groups with actual relevant span facts from bounded context. Distinguish an entry-positioned card from a terminal card rehydrated from session state only. |
| Replay warning, replay done and session-level terminal-card/context rehydration with no source entry | Not chronological source rows; history_origin absent. Existing typed frame identity says what they are. No guessed owner, raw JSON card bubble or notification for old history. |
| Current session_state, active projection, held live tail | Existing authority/seq semantics, not a paged chronological row. Live streaming/control values must win overlaps with history. |

A server integration test drives the **real shared replay emitter** for every currently supported persisted entry/frame class and asserts the correct same-session origin on every entry-derived frame, including zero-prose/tool-only cases and media/special frames. Unknown added source-frame variants must fail the stamping/contract test rather than be silently unowned. This is a per-frame invariant, not a second ordinal coverage protocol. No new expectation that every tool start already carries a persisted identity live.

Keep bounded SessionHistoryContext for actual dependencies of selected groups: per-span actual start/latest state/message/end, proven has_run and duplicate-ID canonical-position/count facts. It contains existing generated public payloads/IDs only, not bodies copied into another store, whole-session span history or private addresses. Resolve cancel/disposition into selected Message/replay fields, not a second competing context value. No dependency absence is evidence of still-running/success; relevant real persisted state/lifecycle wins over delegate placeholder acknowledgements.

| Parity invariant | Required result |
|---|---|
| Same session/options/read cut | REST/detail/snapshot choose the same source range, ascending order and current public projection. Different carrier frame counts are not expected to equal source-row counts. |
| Emitted coverage | Track starts, terminal results and text coverage separately; call-ID presence alone must not suppress a projection's missing result. Preserve existing duplicate suppression without losing still-unpersisted output. |
| Steering/terminal outcomes | Preserve steer/user separation, turn/agent/goal association, terminal_outcome and stable tool text offsets. Prepend/refetch is historical hydration, not live-tail concatenation. |
| Special public content | Participant/reply labels, model, fallback notes, helper/goal outcomes, handover/diagnostic/verdict/truncation, attachments/result references **and room_ref** survive REST/live/replay. T4's room_ref parity is required after each Train3 catch-up; missing legacy value stays unknown, never invented room authority. |
| Per-item corruption | Keep a stable visible placeholder and diagnostics for an invalid item; valid metadata/neighbor rows remain usable. Broken page-level metadata rejects the page. |

**Settled oversized-group rule:** requests target20..100 drawn-row units; canonical weight is narration/system row plus visible logical tool rows, one start/result pair counting once. The browser measures the actual drawn model, not WS frames or both a parent wrapper and all children. Hidden metadata remains retained/projection context and is not a reason to automatically walk invisible history. If the nearest individual group exceeds a target, keep it whole, including101+ rows. A group needed to complete the estimated height floor is also not split at its boundary. Existing record admission and result-reference/preview rules remain. The founder's same-round Q4 amendment adds D4a's **soft** byte budget, subordinate to the two-screen floor/atomicity; it is not a hard byte ceiling.

### D4a — Soft page bytes with a two-screen floor

**Default:4 MiB (4,194,304 bytes), server-owned**, not a new operator setting. Rationale (design estimate, not measured production distribution): with800px viewport/80px average, the floor is20 rows and normal opening40. Four MiB allows roughly100 KiB per row for that opening, comfortably above small text/metadata/reference fixtures; e.g.40 rows at8 KiB plus metadata stay far below it. It still prevents adding many unnecessary large projected tool outputs once enough scrollable content is present. The floor and whole groups always win over this soft target; no hard safety/heap guarantee is invented.

| Rule | Exact selection / sizing contract |
|---|---|
| Floor derived from visible geometry | Client computes raw_floor=ceil(2H/A) from D5's measured/estimated drawn-row model. Send min_rows=min(limit,max(1,raw_floor)); REST min_rows and attach history_min_rows default20 when geometry is unknown. Both are strictly validated integers1..limit, with limit20..100. This is a row estimate of required2H height, not a raw-message count. |
| Identical carrier sizing | For the same candidate range/context, meter the **larger** of its generated public REST history envelope and its generated historical WS page-info/content replay payloads (UTF-8 JSON bytes). Exclude fixed diagnostic accounting fields from their own meter, transport headers and separately bounded authoritative live controls/projection. Use the same server-side projector/meter for REST/detail/snapshot selection so different wrapper overhead cannot choose inconsistent rows. Actual transferred bytes are separately measured. No raw private archive bytes count as visible content. |
| At least one whole group | If an eligible group exists, include it whole even when it alone exceeds4 MiB or the row target. Never split/drop its owner/start/result or required public rendering dependencies. If that one group does not fill the floor, keep taking nearest whole groups until the floor is met or content/clear/range/source cap ends. |
| Byte stop allowed only after floor | Once returned estimated row weight≥min_rows, stop **before** the next group would push the metered page above4 MiB, or stop now if the floor already required exceeding the budget. Before meeting the floor, a byte-budget stop is forbidden. A final group needed for the floor stays whole even across a row-target boundary; log atomic/floor override rather than truncate. |
| Other stop conditions | True eligible end, protected clear boundary, closed-range end and100 source groups may stop before the raw two-screen estimate. Bytes must not shorten that response further. When row_target/source_cap and byte threshold coincide, report row_target/source_cap (and any byte override separately), not a byte_budget cut of a capped chunk. Do not invent history or automatically cross a closed clear just to fill a floor. |
| Raw floor above the request cap | If ceil(2H/A)>100, keep request limit≤100 and min_rows=limit. The client treats consecutive contiguous responses as one logical fill cycle and continues until new rendered content covers2H (opening4H), terminal/closed clear or visible failure. The source/row request cap—not a byte stop—explains a smaller physical response. No hidden gap may be shown between those chunks. |
| Actual height check | After layout, measure newly added content height excluding loader/hint/Retry. Set remaining=max(0,goal_height−measured_added_height), with goal4H for opening/2H for prefetch; request the next contiguous top-up with limit=clamp(ceil(remaining/A),20,100) and min_rows=min(limit,max(1,ceil(2H/A))) using updated A. Re-evaluate after every response; do not blindly add another full2H when only a small remainder is needed. Original20-row minimum/atomic groups can overshoot an approximate target. Preserve anchor on each commit, never reattach or jump bottom for a prefetch. A failure stops top-ups visibly, never a silently shortened success. |
| Page accounting | page_info adds soft_byte_budget=4194304, projected_bytes (meter above), min_rows, byte_budget_exceeded boolean, stop_reason and optional budget_override (height_floor/atomic_group). stop_reason distinguishes byte_budget from row_target/source_cap/clear/end/range_end. Any exceedance has its reason; bytes alone never move cursors or hide a group. |

Budget and floor apply to each physical page;≈4-screen open/2-screen prefetch are measured logical fill goals. Ordinary pages under budget keep their original requested content amounts. Source exhaustion/clear barriers are truthful exceptions to available coverage, not a license for a budget to leave a reader with less than two screens. Large-output floor overrides are explicit tested outcomes, not an unresolved question.

### D5 — Pixel trigger, running measurements and virtual estimates

Server owns strict input validation/default40 and4 MiB soft budget. Client owns the visible **chat scroller** height H, not window/monitor height. Before the first attach, if committed H/A are available, request history_limit=clamp(ceil(4H/A),20,100) and history_min_rows=min(history_limit,max(1,ceil(2H/A))); otherwise omit both and use40/20 (four/two800px screens at80px). REST requests carry the corresponding limit/min_rows. No second attach merely to correct sizing. Byte-limited opening responses are followed by contiguous normal older top-ups to≈4H; a prefetch fill cycle acquires≈2H **new** content. Floor overrides are applied server-side and actual measured height is checked client-side; a4 MiB ceiling cannot silently redefine open as two screens or prefetch as less than two. Clear/end/error and no-invisible-history-loop rules remain truthful exceptions.

| Quantity / trigger | Definition |
|---|---|
| Initial A | 80 CSS pixels until measured, never a permanent estimate. |
| Average | Latest100 distinct drawn-row samples in the active width/presentation generation. A=max(1,sum(height)/count); re-observation replaces a sample. Exclude loaders/hints/Retry/hidden/zero/nonfinite/live-changing bubble. Reset on session/major width/Verbose change. |
| Inline tool rows | Count and measure actual tool rows even inside an outer message; do not double-count the wrapper and children. Include spacing consistently. |
| Unmeasured virtual size | **estimateSize follows current A**, not a fixed80. For a composite outer virtual item use its known drawn-row count times A, replacing with measured outer height when available; invalidate/recompute estimated prefix measurements when A changes. Otherwise accurate fetch-count math can still trigger too late. |
| Dtop | Distance from viewport top to earliest loaded content boundary, including actual prefix controls/spacers, using the live measurement model for unmounted items. Verify it through the real virtualizer, not only injected numbers. |
| Automatic older trigger | After opening/refetch commit: Dtop≤2H, has_earlier, no unopened clear/error/in-flight. H≤0 or hidden document disables automatic reads. |
| Requested rows / floor | limit=clamp(ceil(2H/A),20,100); min_rows=min(limit,max(1,ceil(2H/A))). H800/A80→20/20;800/40→40/40;800/400→20/4;1600/10→100/100 with contiguous top-ups until raw320-row/2H coverage. Byte cuts are permitted only after the floor, not merely after one group. |
| Underfill | Re-evaluate after each commit; no loop at clear/end/error or unchanged cursor. A page adding no positive drawn content height does not automatically chain another request; explicit earlier action advances it if desired. |
| Newer edge | Symmetric two-screen pixel rule for has_later after far-edge eviction. No transport seq as after cursor. |

Background page requests never set replaying/streaming or lock the composer. The plain fallback uses real element heights on scroll/resize when possible, otherwise explicit controls/80px sizing; it never loads the full server lifetime.

### D6 — Request generations, ordering and intentional clear

Request identity is `(session_id,view_generation,direction,boundary)`; at most one request per direction and coalesce duplicate triggers. Abort/fence old success **and failure** on leave/switch, snapshot generation replacement, applied own-clear or a replaced range refresh. A return A→B→A needs generation identity, not just equal session ID. Backend cancellation releases read resources, never execution.

Merge into **current** state; current/live values and terminal tool results win overlap. Preserve ascending occurrence order/display aliases/pending input. Pages do not mutate live cursor, totals, active turn, goal/approvals/questions, model context or attention. Reject wrong session, impossible continuation, repeated boundary and order contradictions against known overlap. Opaque positions cannot be lexically ordered; server/private order and overlap are the authority.

WS remains live-open authority; no parallel full initial REST read. REST supplies bounded initial fallback, older/newer and bounded existing-window revalidation. Header flags are replaced with page metadata committed atomically, including all C11 consumers and C14 clear refresh. Independent stale REST latest data cannot overwrite newer WS history/control state.

Own successful clear deliberately resets display/context-forward view after its operation-owned fresh page confirms a new marker; latest_clear can confirm even if the marker lies before the selected page. A historical page containing an old marker cannot settle a new clear. Refused/held clear does not reset. Other-tab/new-clear metadata discovered during reconnect updates the live-tail boundary/current context and reader generation, but **does not teleport an already historical reader**; retain/revalidate that explicitly browsed window per D7. New older requests still respect unopened clear barriers. No old display data is injected into model context.

### D7 — Snapshot while reading history, without returning the reader to latest

```
attach → auth + session eligibility → limit validation → bind/hold at W
    incremental: session_state → full journal tail → catch_up_complete(W)
    snapshot: shared latest page read before history replacement is announced
        → session_snapshot(page_info)
        → current session_state
        → selected history frames (local history_origin) → one replay done
        → uncovered active projection → catch_up_complete(W)
    → release held live frames in seq order → existing goal rehydrate/ack handling
```

History_limit never cuts incremental journal catch-up; an unservable cursor uses the bounded latest snapshot. `boot_mismatch`, `retention_exceeded`, `cursor_ahead` and unknown_position do not themselves authorize a reading-position reset.

| Existing view at snapshot | Exact keep / refresh / anchor behavior |
|---|---|
| First open or explicit following-latest | Stage the selected recent page; preserve pending/failed input and current control state; commit historical rows once and settle the opening at bottom only after complete snapshot/projection application. Failed page read causes no visible wipe or successful complete. |
| Historical reading, including 499/500-group edges | Keep the reader's actual source rows, coalescing aliases, opened-clear boundaries, retained interval endpoints, current measured sizes, focused element, first visible anchor and intra-row pixel offset. Mark the transport/snapshot generation new and abort old page reads, **not** the reader window. Stage the fresh latest page into the existing100-group tail cache separately; refresh current approvals/questions/secrets/goal/turn projection from authoritative session state. Do not put the distant tail below old rows. |
| After successful complete in historical mode | Live cursor can commit W because live catch-up completed; this is independent of reader revalidation. Issue window_start/window_end refresh pages against exactly the kept reader interval, newest-to-oldest, bounded to the prior≤500 groups and100/request. Stage refreshed public projections, then merge by occurrence; current/live terminal values win. Preserve the anchor before paint after every necessary layout. No tail-to-anchor walk, no around-message loader and no new UI navigation destination. |
| Window refresh in progress / failed | Keep all readable cached rows/anchor/focus with a small status “Refreshing loaded messages…” and aria-busy on that region. Failure uses “Could not refresh loaded messages.” + Retry, retaining cached rows with an explicit out-of-date notice. Retry repeats this range, not attach or a user send. Current live controls remain usable; page-prefetch pauses until the range is reconciled or explicitly left. |
| Some rows/anchor expired | Remove proven-expired rows after refresh. If the original anchor survives, keep it; otherwise use the nearest later surviving row in the **old range** (or nearest earlier if none), preserving its pre-refresh screen offset where possible. Announce “Some loaded messages have expired.” No forced bottom jump. Source positions/retention generation decide, not timestamp guesses. |
| Entire held range expired | Show “These loaded messages have expired.” in place of that old range, keep current controls and a focused/keyboard-reachable Return to latest action. Do not claim the whole session is empty and do not switch to latest without that action. |

A new clear intentionally sent/applied by this viewer is D6's explicit view replacement, not a passive reconnect. Routine incremental reconnect keeps the reader and applies journal updates without a bottom jump. Snapshot read/stream failure retains prior cursor/view, rejects staged partial history and offers recovery under existing replay-error handling. Revalidation failure **after a successful catch_up_complete** does not roll back the successfully caught-up live cursor or simulate a second attach failure.

**Interim composition:** only its dispatch-described intent is known: buffer attach replay until done, render once, settle initial bottom. Round1 C21 still finds no origin/local branch. **PRE-RED-1 (team-lead)**: obtain the chief's actual branch commit/patch even if unlanded, read the real changed replay/store/ChatScreen path and tests, record SHA/symbols and reconcile D7 before QA writes overlapping RED assertions. If no artifact exists yet, record that explicitly with the chief's ownership/source handoff; **the actual re-read remains pending and absence is not a substitute for completing it before RED**. Obtain the unlanded artifact from the chief rather than waiting for its landing. This is a required source-coordination task on the current base, not a Train3/interim landing gate on RED/GREEN.

When it lands, catch up and re-read the landed version. Preserve one bounded historical batch commit, not a second buffer. Replay done and catch_up_complete remain different: projection can follow done, so final opening readiness waits for successful complete/application. A live done never masquerades as attach completion. Paging supersedes full-history input, not render-once; historical-mode snapshots use its staged tail result without invoking its first-open bottom action on the old reader.

### D8 — Scroll ownership, focus and catalogued UI state

Stable occurrence/display-group keys replace index-key assumptions. Track current visible anchor + intra-row offset through every committed prepend/control layout and genuine reader movement. Apply **signed anchor content-offset delta** against the current scroll position; total scrollHeight includes live growth below and is not an anchor oracle. Remeasure images/diagrams/fonts/tool expansion and composite virtual rows before paint/after observer settlement. One coordinator owns compensation; `overflow-anchor:none` avoids native double correction; virtualizer/assistant-ui bottom-follow is suppressed during historical preservation.

| State / job | Exact component / focus and announcement contract |
|---|---|
| Initial loading | Catalogued **Skeleton** from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/ui/skeleton.tsx` for the bounded transcript area, plus one polite role=status “Loading messages…”. Skeleton is aria-hidden; it alone is not status. No product preparation/progress state. |
| Prefetch/clear-crossing/newer | Same Skeleton in a small stable boundary slot + role=status “Loading earlier messages…” / “Loading newer messages…”. No large blank replacement. aria-busy marks only the affected region. |
| Show earlier / Return to latest | Catalogued **Button** from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/ui/button.tsx`, explicit accessible names “Show earlier messages” / “Return to latest”, keyboard Enter/Space. Stable DOM keys. In-flight action uses aria-disabled + a guarded handler/deduplication, not removing/focus-disabling the focused control. Do not combine the Button's built-in generic pending announcement with a duplicate live-region announcement. |
| Older/newer/range failure | Catalogued **ErrorState** from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/ui/error-state.tsx` with action-specific text and Retry. Its role=alert announces the failure once; do not echo it in another live region. During Retry keep the existing error/control mounted with guarded single request, show polite loading once, then clear error on success. No focus() or dialog on failure. For repeated identical failures, update this same alert's message with the retry attempt (for example “Could not load earlier messages. Retry 2 failed.”) while retaining its Retry node, so a new failure is announced without remounting the focused control. |
| Return to latest pending/error/success | Keep old reader/pending input, Return control, small “Loading latest messages…” status. Failure: “Could not load latest messages.” with Retry of latest only. Success: commit fresh latest page, merge current tail/pending input, enter follow-latest and scroll to bottom. Focus remains the triggering control if mounted; if it disappears, move to the labelled history scroller with preventScroll, not the composer. |
| Prepend/refetch/final earlier page | Do not move focus on passive updates. If a user-triggered control legitimately disappears at terminal success, transfer focus to labelled “Conversation history” scroller with preventScroll; keep the anchor. If far-edge eviction would remove the focused row/control, protect it where possible or perform that same explicit scroller fallback before removal. Never silently focus the first new row. |
| Screen reader | One polite status per request transition, one alert per new failure; busy state resets on success/failure/abort. Loaded historical rows do not feed “New response” announcements or steal accessibility focus. Live answers retain their existing announcement behavior. |
| Reduced motion / zoom / no observer | No smooth-scroll anchor correction or required animated highlight. Use Skeleton's reduced-motion behavior; same action semantics at200% zoom/reflow and in plain-list fallback. No new focus ring, raw control or brand token. |
| Entire old range expired | Plain explanation plus the same catalogued Return Button; if anchor/control expired, focus fallback as above. No automatic latest navigation or empty-session welcome. |

Catalog entries were read (C19). No new local LoadingState/ErrorState/Return widget is commissioned. New historical tool grouping/scroll hooks are module extractions, not an expansion of ChatScreen beyond its existing responsibility. This is behavior/focus guidance, not a brand redesign.

### D9 — Settled row-window policy and inactive sessions

R1-Q4 selects **one active contiguous reader≤500 source groups plus a latest-tail cache≤100 complete groups**, retaining whole owning groups/alias/tool/measurement cleanup. Evict the whole far edge opposite traversal, never the newly fetched prepend, current anchor or pending/active execution controls. At least two screens of guard content remain where available; if a focused/anchor-protected edge prevents a safe trim, retain the current bounded window and defer the page commit rather than silently exceed500 or throw the reader to latest. The explicit control can retry after user movement changes the protected edge.

Live updates continue into current control/execution state and the100-group latest cache while detached. Do not concatenate a distant tail under old rows or let it evict the historical anchor. Return to latest is always available in historical mode, not only when unread counts are known; no invented count is required. It fetches a fresh latest page and merges current live values/pending input at commit. The cache supports current-tail reconciliation, not a license to assume continuity across dropped middle rows.

On session leave/switch, abort reads and release this feature's reader/tail arrays, source/display alias maps, derived page indexes, measured samples and cached media/result bodies from that inactive session and its query cache. Do not leave500+100 groups for every visited sessionsById bucket. Preserve existing genuinely pending user input and necessary active controls/execution state in their current owner, never duplicate them into a new history store or cancel them. Returning to the session opens its newest bounded page, not a forever-retained historical window. No new global bucket-count eviction scheme or persistent browser database is added.

A user send from old history remains addressed to the current conversation, never its displayed model context. Pending/failed input identity and draft stay through Return/latest/snapshot. Only an actual successful send/accepted queue action requests follow-latest; fetch/commit the latest page before changing the displayed window. A failed send or failed latest read keeps the old reader and its recovery control. History Retry/Return never resends that input; first-send/delivery Retry is a different existing action.

MAJ-008's simpler one-window alternative was considered; the founder explicitly retained the tail cache, so this is implemented/tested rather than reopened as a design question. The counts are row/source-group bounds, **not total-byte guarantees**.

### D10 — Contract-first inventory and exact shapes

No contract is changed by this correction. All new filenames/types/parameters below are **proposed deliverables**. Backend-lead alone edits/regenerates contracts before consumers.

| Artifact | Required change |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/openapi.yaml`::getSessionMessages | Replace array with SessionHistoryResponse; limit/min_rows, before, after and the paired existing-window refresh boundaries only. Document exact validation, retained-domain continuation and errors. No message/neighborhood lookup. Remove header side channel as paging authority. |
| Same OpenAPI::getSession; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionDetail.yaml` | Metadata/agent_removed unchanged; messages is bounded latest, with canonical page_info/has_earlier/has_later/next_before/next_after. Replace has_earlier_messages's clear-only meaning atomically. Detail owns no paging selectors. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionHistoryResponse.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionHistoryPageInfo.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionHistoryContext.yaml` | Shared envelope/metadata/context below. No SessionHistoryEntryRef manifest schema, frame-count ranges or duplicate REST identity list. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/HistoryEntryOrigin.yaml` | Shared generated local ownership {position,entry_id?}, referenced by entry-derived replay frames and their inline AsyncAPI mirrors. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/Message.yaml` | readOnly history_position; terminal_outcome parity; preserve all public special payloads including room_ref. Existing provider_fallback enum gap versus C8 is recorded for team-lead's explicit page-parity disposition, not a hidden placeholder or side fix. No private envelope export. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/AttachSessionFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/asyncapi.yaml`::AttachSessionFrame | Optional history_limit20..100/default40 and history_min_rows1..effective-limit/default20, snapshot-only. Same access-before-limit-then-floor-before-bind precedence. since_seq/boot_id/ack unchanged; neither field cuts a servable incremental journal. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionSnapshotFrame.yaml`; same AsyncAPI::SessionSnapshotFrame | Recent-page replay metadata/context, canonical continuation and mode; no second entries array or manifest. Retain unrelated secret/control fields and seq/reason semantics; historical reader preservation is a client state policy, not an omitted live catch-up. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/CatchUpCompleteFrame.yaml`; same AsyncAPI::CatchUpCompleteFrame | Describe selected-page replay plus projection completion, not lifetime content. No history position mixed into live cursor. |

**Exact history-origin carriers**: backend-lead adds the shared history_origin reference to existing component schemas (and corresponding inline definitions in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/asyncapi.yaml`):

| Existing component schema | Use |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ReplayMessageFrame.yaml` | Persisted narration/user/system/cancel source. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ToolCallStartFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ToolCallResultFrame.yaml` | Exact owning source occurrence for historical tool-only/grouped calls. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ReplayErrorFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ProviderFallbackNote.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ContextWindowNoticeFrame.yaml` | Error/fallback/diagnostic rows. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/JudgeVerdictFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/BrowserHandoverNoticeFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/HelperEventFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/GoalOutcomeFrame.yaml` | Existing typed special rows with their own ordinary identifiers preserved. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SubagentStartFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SubagentStateFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SubagentMessageFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SubagentEndFrame.yaml` | Actual persisted parent-side lifecycle source, not the child's full archive. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/AskUserQuestionFrame.yaml`; AsyncAPI::MediaFrame | Optional origin when emitted from a selected source entry. Session-only terminal card/context/media without a persisted source follows D4's explicit exception. No fictitious shared MediaFrame file is assumed. |

| Shape | Required / optional fields and invariants |
|---|---|
| SessionHistoryResponse | Required session_id string1..255, entries Message[] non-null/max100 source groups, has_earlier/has_later booleans, page_info. Optional next_before/next_after strings47 iff their flag is true, first/last selected position. Individual-group drawn weight may exceed100 under D4. |
| SessionHistoryPageInfo | Required mode enum latest/older/newer/window_refresh; limit integer20..100; min_rows integer1..limit; row_count/source_group_count nonnegative; soft_byte_budget integer enum4194304; projected_bytes nonnegative; byte_budget_exceeded boolean; stop_reason enum row_target/source_cap/clear/end/range_end/byte_budget; retention_generation opaque nonempty string changed on actual expiry; context. Optional budget_override enum height_floor/atomic_group iff byte_budget_exceeded; first_position/last_position iff nonempty; optional stopped_at_clear and latest_clear {entry_id,history_position}. All positions47; no null. Empty/true terminal/closed barrier can underfill; a byte_budget stop cannot underfill min_rows. |
| HistoryEntryOrigin | Required position string47; optional entry_id (recorded nonempty string). Optional on general frame schemas, semantically required for every entry-derived paged replay by D4's tested producer invariant. No session authorization or private address encoded in a parallel field. |
| SessionHistoryContext | Required non-null spans and duplicate_ids arrays restricted to selected/current relevant dependencies. Span item: span_id, actual existing generated start/state/message/end when recorded, has_run only when proven. Duplicate item: kind message/tool, id, canonical_position, occurrence_count≥2. No all-session span load or competing cancel/disposition values. |

Objects are closed; use generated payloads, omit optional absence rather than null, and test semantic cross-field rules that structural generation cannot express. REST/detail/snapshot reuse these shapes; only snapshot uses per-frame ownership. Add the shared schemas → reference in OpenAPI/AsyncAPI/manual mirrors → run `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/scripts/gen-contracts.sh` → atomically commit canonical/generated Go/TS/Zod/inbound artifacts → implement consumers with generated types only. `make verify-contracts` runs in the approved remote gate. No handwritten replacement wire structs/interfaces or validators bypassing the contract.

### D11 — Access, parser and errors

Use the **actual C15 access model**, not an invented multi-user read policy: REST auth wrapper / WS auth, valid session ID, main eligibility, same addressed store/session existence. Resolve every position/range only within that authorized session; knowing a token grants no parent/root/other-session read. Existing child sessions page their own archive.

**Both carriers' order:** authenticate → path/session-ID syntax → addressed-session eligibility/existence/access → limit/selector syntax and combinations → same-session position/range lookup → index/page read. Thus an inaccessible main with an invalid limit gets the existing access refusal; no WS/REST disagreement. Bad attach history_limit is checked after resolveSession-style access and **before bind/wipe**, with no successful complete. Keep inbound validation on/off parity.

| Authorized-session outcome | Exact behavior |
|---|---|
| Omitted limit |40, never unlimited. |
| Duplicate/malformed/empty/null-like/signed/fractional/exponent/whitespace/overflow/out-of20..100 limit |400 ErrorResponse.error=`limit must be an integer from 20 to 100`. Digits-only leading zeroes permitted. |
| Invalid min_rows after valid effective limit |400 `min_rows must be an integer from 1 to limit`. Omitted uses20; a supplied value must be one digits-only decimal integer1..effective-limit, with duplicate/malformed/null/overflow refused. Validate limit before min_rows, after access. |
| Invalid/duplicate/blank/malformed-percent/non47-character/wrong-version token for before/after/window boundary |400 `invalid history position`; no trimming/omission. |
| before+after, unpaired/reversed range endpoints, after with a range or before outside it |400 `invalid history page range`. Unknown/foreign well-formed tokens are404 first; order is compared only after same-session resolution. |
| Unknown/foreign position |404 `history position not found`, no foreign lookup/body/reset-to-latest. A **known expired** position remains a bound under D2/D14 and need not fail. |
| Session missing/hidden/deleted | Existing404 `session not found` before paging lookup; retain established auth errors. |
| Missing/corrupt/incompletely covered required index |500 ErrorResponse.error=`Session history is unavailable. Retry after the operator restores its index.` Do not expose disk details, return partial history, build it, poll an automatic repair or emit503. UI uses ErrorState with Retry; Retry rechecks the same read. |
| Other required storage/projection failure | Existing C6 redacted session-storage error500, preserving current view. |
| Bad attach limit/floor after session access | Existing session-scoped ErrorFrame message=`history_limit must be an integer from 20 to 100` or, after valid limit, `history_min_rows must be an integer from 1 to history_limit`; no bind/wipe/catch_up_complete. Null/fraction/overflow/wrong type refused, both validation modes. |
| Attach required index missing | Same fixed history-unavailable message in existing ErrorFrame/replay-error completion path, no snapshot wipe or successful catch_up_complete. |

Write a **chat-specific query parser in this feature**. It inspects raw percent decoding/multiplicity without silently dropping malformed pairs, with explicit tests for malformed key/value and both validation modes. No dependency on or refactor of the unlanded room handler/parser. The room precedent is evidence for pitfalls, not a required import. Unknown keys do not enable a new mode; no ID-based loader is implemented. Per-item public corruption keeps the existing stable placeholder; page metadata contradictions fail visibly.

### D12 — Build now, catch up as each overlap lands

R1-Q1=B replaces the initial wait-first plan. Start on the current release/v0.1.1-based feature branch; spec approval/RED/GREEN do **not** wait for Train3 or interim landing. No cherry-pick of the large unlanded train or anchor correction without its owning lineage. Team-lead owns PRE-RED-1's actual interim source handoff/re-read and conflict planning; absence of an origin ref is recorded, not invented implementation evidence.

As **each** overlap lands, catch the feature up with its landing destination and re-read the changed seams: archive TranscriptEntry/room_ref, replay buildEntryMessage, public/generated contracts, ChatScreen/state and regression tests. Team-lead chooses the repository-approved merge/rebase mechanics for an already published feature branch; no force push is authorized here. Build work can continue if one base has not landed, but team-lead holds this feature's integration landing until the founder's landing order is met. If GREEN is ready first, report the pending landing order to the chief/founder; do not invent a wait timeout or bypass it.

| Stage | Owner / dependency |
|---|---|
| PRE-RED-1 | Team-lead obtains/reads the chief's actual interim artifact and tests **before RED**, recording SHA/symbols. Origin absence leaves the re-read pending, not waived; get the chief's unlanded SHA/patch. Do not require that artifact to be landed first. |
| Source/contracts | Backend-lead writes this feature's parser and generated contract slice on current base. |
| RED | QA authors independent backend/frontend/operator-script tests from the corrected spec; tests-only CI proves intended failures. |
| GREEN | Backend-lead: metadata/read cut/expiry/projection/contracts/parser/diagnostics/offline script. Frontend-lead: generated adapters/state, reconnect preservation, sizing/anchor/focus/recovery, docs. |
| Each landing | Team-lead catches up/reconciles overlapping contracts/replay/room_ref and reruns affected gates. |
| Final gate/landing | Independent QA CHECK, five reviewers, docs-verifier, exact-commit UAT/validator, founder approval and satisfied landing order. Round2 grill/interview/fix still required for this Draft. |

Deliberate room differences remain: chat's opaque positions versus room seq; has_earlier versus has_older; measured20..100/default40 versus room1..1000/default100; two-screen pixel threshold versus room24px; virtual/image-aware scroll correction versus plain DOM list; indexed disk projection versus memory slice. Exclusive/ascending/actual-continuation semantics and corrected signed anchoring are reused. Neither live cursor is changed by older paging. No shared-parser extraction is required by this feature.

### D13 — Separate one-off offline index script deliverable

**Proposed location:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/scripts/migrations/chat_history_index.py`, standard-library Python3 only. This is a separately invoked operator script shipped/tested with the feature, never an application startup, constructor, REST/WS operation or background worker. Backend-lead owns the private index-format compatibility; integration tests compare its output through the real Go reader against independent frozen projection fixtures. This spec-only correction creates no script.

| Interface / precondition | Normative behavior |
|---|---|
| Inputs | Explicit absolute --data-root, --backup-root, --backup-manifest and --stopped-receipt; --confirm-stopped plus team-lead's recorded stopped-instance evidence; --dry-run or --apply, exactly one. No guessed default live home, discovery of a person's Documents or network download. |
| Stopped instance | Team-lead first stops the real instance and records the instance identity/data root/process/service evidence in the receipt. The script refuses without that acknowledgement/receipt or if the recorded process/current matching instance is still running. Source fingerprints are checked again before publication to detect an intervening writer. Process probing is an additional check, **not** a claim that a new cross-process gateway lock already exists or that a flag alone guarantees quiescence. Unknown/uncheckable stop evidence refuses, not a guessed safe state. |
| Full backup check | Backup must exist outside the live data root, identify that exact canonical root and cover **the full instance data**, not just sessions. Manifest lists normalized relative paths, kind, size and SHA-256; verify actual backup bytes and current canonical bytes against it, including metadata/dispositions/archive/config/encrypted credential files, without printing content. Refuse missing/tampered/incomplete/wrong-root backup, unsafe traversal/symlink escape, unsupported file kind or source drift before any apply write. A manifest saying success alone is not proof. The backup inventory covers all files present at backup time, including any prior derived indexes; verify those backup bytes too. For **current-source equality/idempotent rerun**, exclude only this script's designated derived-index/staging subtree, whose safe allowed paths are explicitly validated; no other new/missing/changed canonical file is ignored. Thus the same full backup remains valid after only this script's index writes, while unrelated source drift still refuses. |
| Current input only | Read already-canonical retained day archive and disposition/effect metadata; validate membership/identity/order/line framing and existing field semantics. Do not run old conversion, invent missing provider/provenance facts, change model windows or silently skip corruption. Produce an explicit per-session refusal and nonzero summary on bad/unconverted data. |
| Dry-run | Read/validate/compute plan and counts, **zero writes anywhere in the data/backup trees**, no temp/lock/index/log creation there, no chmod/mtime/content changes. Output deterministic sorted session actions and summary to stdout; diagnostics to stderr. Team-lead may save receipts outside the trees. |
| Apply | Create/publish only the designated derived chat-index generation files inside each session's private index subdirectory, using bounded streaming/disk scratch and atomic generation publication. No body/result copy to a second content store; no canonical archive/meta/window/disposition/config/credential/upload or backup write. No rename/delete/repartition/normalization of canonical bytes. |
| Idempotence / interruption | Matching complete index generation is unchanged, including bytes/mtime. Incomplete script-owned staging is safely replaced/resumed; a session's old valid generation remains published until a fully verified new one is ready. A partial batch reports which session indexes published; rerun skips them and completes remaining sessions without duplicate archive entries or renumbered tokens. No promise of atomicity for the entire instance batch. |
| Source race | Recheck canonical inventory/fingerprints before each publication and at completion. If changed, refuse/stop; do not claim an index prepared from a moving source is complete. Team-lead investigates, restores verified backup if necessary and reruns stopped. No generic automatic repair in the product. |
| Exit codes |0: validated dry-run or completed/unchanged apply;2: bad arguments/precondition/backup/stop evidence;1: read/integrity/publication/interruption failure. Failures are not demoted to log-only success. |

Proposed dry-run stdout for a three-session test fixture (counts are test data, not measured live data):

```text
session=session-a action=create groups=40 partitions=2
session=session-b action=unchanged groups=120 partitions=3
session=session-c action=create groups=0 partitions=0
mode=dry-run sessions=3 create=2 unchanged=1 rebuild=0 refused=0 canonical_writes=0
```

Each refusal includes session identity and a fixed reason class, not bodies/credentials/raw positions or private filesystem causes in a public page. Script diagnostics are operator-local; paths in the operator receipt are absolute. The utility must report actual creates/unchanged/rebuild/refused and canonical_writes=0, with tests independently hashing the trees rather than trusting the script's own counter.

**Operator sequence:** stop instance → produce/verify full backup + manifest/stop receipt → dry-run and inspect refusals/counts → apply → independently verify canonical/backup unchanged and all intended indexes readable → start the feature build → exact-session UI acceptance. If the product still reports a missing index, Retry only rechecks; team-lead returns to this stopped/backed-up workflow, never tells the person to wait for an invisible runtime job. This is the founder-specific existing-data deliverable, not a supported product upgrade/migration framework.

### D14 — Retention reconciliation and truthful boundaries

Use C18's actual sweep chain; do not claim rolled-partition expiry has no production caller. Extend derived metadata maintenance at the same owner: an actual successfully removed partition becomes absent in the retained-partition set and advances retention_generation; failed removals remain eligible if readable. Keep content-free position/order tombstones, remove content/dependency pointers to gone bodies and recompute first/last/clear/lookahead over **retained** eligible groups. Whole-session deletion removes all derived state. Retention-disabled is unchanged/no generation advance.

The reader also validates required partition existence against its immutable cut and C3's partitionGone/errNoPartition distinction. If a sweep raced a read, discard/reselect against the new retained generation before emitting success; a stale index lookahead must not perpetually advertise a gone prefix. Missing partition proven expired is not500; permissions, bad JSON, wrong checked ID and inconsistent metadata are not expiry and must surface. A known expired before/after/range endpoint is a valid ordering fence, selecting actual retained neighbors or terminal empty; unknown/foreign tokens stay404. Do not reopen a deleted session or reset a cursor to latest.

| Expiry case | Required result |
|---|---|
| First/oldest partition swept | No old phantom lookahead; reaching the first retained group gives has_earlier=false/no next_before. |
| Middle partition swept | Select across retained neighbors in acceptance order with no fake blank spacer/source rows for deleted content. Cursors don't renumber. |
| Last rolled partition swept, live current retained | Newest/forward pages select actual current/remaining content; no stale has_later pointing only to the swept partition. |
| Retained effect, its original call swept | Exclude the effect from visible chat and ignore only its proven-expired target, as the full projection rules permit; no orphan tool row/result or500 solely because the target expired. |
| Target retained, relevant effect unreadable/corrupt | Error, not silently stale success. |
| Page response races sweep | Response metadata truthful at a coherent retained cut; UI uses the changed generation to revalidate its bounded held range (D7), not infer deletion of every session. |

No retention duration, scheduler, agent model-window policy or all-shard lock exception is redesigned. The paging index follows existing deletions, not vice versa.

### Alternatives considered and consequences

| Alternative | Decision / rationale |
|---|---|
| Full ReadTranscriptView then slice or hot lifetime cache | Rejected: retains original content/heap/cold cost. D1 adds real derived metadata work instead. |
| Entry ID/timestamp/live seq/model ordinal as history cursor | Rejected: duplicate IDs/delayed timestamps/different view lifetimes. Keep occurrence identity. |
| **New monotonic per-session chat-occurrence number** | Considered separately from the rejected model ordinal. It is orderable and can simplify neighbor lookup, but requires another stable public allocator/publication/tombstone reconstruction invariant. Keep the already-drafted hash identity plus private ordering/index for this correction; no measured speed advantage is claimed. Final-byte stability caveat explicitly applies before and after offline reconstruction. |
| Reversible ArchiveAddress token | Rejected: exposes disk-only marks. Hash selector needs private reverse lookup but no public address/key lifecycle. |
| **Ordinal replay ownership manifest** | Replaced by generated local HistoryEntryOrigin. No duplicated REST source list or counting every optional/special frame to make a coverage-range protocol succeed. |
| **Per-frame origin/owner fields** | Selected: source association is local, generated and tested in the shared emitter. Touches several existing schemas/mirrors but no separate frame-count protocol or new transport cursor. |
| **Control-only WS + history via REST** | Rejected here: would introduce a second asynchronous snapshot transfer/read-cut/hold completion rendezvous and replace the existing replay path. Keep one shared reader/current attach lifecycle with local ownership instead. |
| Automatic boot/lazy/background rebuild | Rejected by founder R1-Q3; use D13's separate stopped/backed-up utility. Missing metadata is an honest product error. |
| **Hard byte cap / byte stop before two screens** | Rejected by same-round amended Q4. Select D4a's4 MiB soft budget with modeled two-screen floor, actual contiguous top-up and whole groups; record deliberate floor/atomic overrides. This fixes MAJ-002 under the founder's amended rule rather than reopening the choice. |
| **Single window without tail cache** | Not selected: founder R1-Q4 accepts MAJ-008 and retains500+100. Test explicit inactive-session payload release rather than silently switching policy. |
| Cherry-pick/wait for unlanded trains before work | Rejected: build current, chat-specific parser, catch up per landing; only landing order is gated. |

Affected components: session indexing/projection/read locks/retention, gateway/generated contracts/replay, chat state/adapters/geometry/focus, tests/CI discovery, the operator-only script and public docs. Backend-lead implements backend/contracts/script; frontend-lead implements UI; QA owns independent RED/CHECK; security-lead audits authority/privacy; docs-verifier audits drafted public docs. No design-system visual redesign, provider/scheduler/tool-policy rewrite or new content authority.

## Performance, Observability and Measurement

### Adopted performance envelope — not an achieved benchmark

Founder R1-Q4 retained the first Draft's envelope. Do not silently relax it after a failed run.

| Measure | Target / evidence method |
|---|---|
| Data | Synthetic100,000 chat groups over90 days, ≥100 MiB encoded, mixed tools/images/clear/system rows; additionally a permissioned anonymized **genuine long session**, recording actual size/shape. Synthetic is not real UAT. |
| Server healthy-index | p95≤300ms warm / ≤750ms cold-process over30 distinct opens/pages at40 and100 on a recorded reference host. No model latency; existing valid index, no hidden construction in timing. |
| UI ready | p95≤1.5s over30 opens, recorded browser/host, gateway round-trip≤100ms; selection→visible recent page/settled opening/control readiness includes all actual requests. |
| Normal reading |≤1 viewport/second, response≤500ms, no packet loss:30 consecutive boundaries with no viewport history gap and≤1 CSS pixel settled anchor drift. Out-of-envelope failures still require honest status/Retry. |
| Prefix independence | Same suffix/dependencies with1,000 vs100,000 prefix groups: no decoded omitted prefix; selected-content work and allocations vary≤10%. Count records/bytes, do not use a stopwatch as a discrete-work oracle. |
| Row memory/replay | Record server allocated/peak bytes, browser retained heap, transferred bytes/frame counts and source/row counts. Opening is not a lifetime intermediate.100-page traversal checks500-reader/100-tail caps, group/cache cleanup and inactive-session release. These are row bounds, not a new byte budget. |
| Offline utility | Separately measure stopped-script throughput/peak scratch and canonical/backup integrity. It is not hidden in ordinary boot/first-open metrics and has no runtime preparing state. |

A no-gap guarantee under arbitrary latency/scroll speed is not promised. MAJ-002 is fixed by amended Q4's4 MiB soft page target plus two-screen floor/whole-group override and measured coverage tests. It is deliberately not a hard byte/heap ceiling: a huge indivisible group or the content needed for2H can exceed4 MiB. Record these actual overrides and existing result protections; do not silently relax the screen floor, re-raise the settled decision or misreport a fixed byte maximum.

### Content-free observability

Extend local structured replay/catch-up diagnostics, not telemetry/a new monitoring service. Proposed history_page record: session ID, mode/direction, requested/effective limit, source-group/drawn-row counts, has_earlier/has_later, stop reason (row_target/source_cap/clear/end/range_end/byte_budget), min_rows, soft_byte_budget, projected_bytes, byte_budget_exceeded, budget_override (height_floor/atomic_group when required), duration_ms, selected archive records/bytes, dependency count, retention generation and outcome (success/abort/invalid_request/protocol/storage/index_unavailable). No index_rebuild/preparing success state. No body/parameter/result/secret/raw cursor/address/query string in new logs.

Browser development diagnostics: generation/direction, H/A/estimated versus measured prefix, request timing, anchor correction/drift, stale suppression, loaded/tail/sample counts, refresh mode/outcome; no content. UI recovery must not be log-only. Existing privileged storage-error cause logs remain as before; the new history_page summary stays sanitized.

**Log test:** capture real success/failure/abort/invalid-index/cursor paths with distinctive body, parameter, result, credential, raw token, private mark and query canaries. Assert required event/outcome/count fields and zero canary occurrences in new history diagnostics/browser diagnostics. Plant a raw-query/content log in the tested seam and prove the test fails; do not just inspect the implementation for a banned string.

### Instrument controls

Baseline full-history read/replay must produce lifetime counts; candidate bounded read must not. Disable anchor correction, omit negative header delta, substitute total-scrollHeight or index keys: geometry checks must fail. Fixed80 virtual estimates on40px rows must fail the real-virtualizer threshold test. Full-read/disposition-scan fallback, removed generation fence, old oldest-drop ring, wrong swept lookahead, held read mutex and raw-query log each have a specific negative control. Bind receipts to exact commit/data/host/config; no application green is claimed by this authoring task.

## Reachability

| Entry point | Actual required path |
|---|---|
| Normal open | Existing agent/main/session selection → ChatScreen → authoritative bounded snapshot (or bounded REST fallback) → recent end, no hidden full detail/messages fetch. |
| Read older/newer | Actual scroller and explicit short/plain-list boundary Button call shared page action; fetched content is readable, not just cached. Forward reload exists only to continue normal browsing after eviction. |
| Clear divider | Show earlier messages on that exact marker opens its preceding segment with the same paging/anchor/Retry mechanism; no model-context change. |
| Reconnect while reading | Keep/revalidate the current range through D7; a fresh tail/control snapshot is not a bottom-navigation action. |
| Return to latest | Named catalogued Button is keyboard-accessible in historical mode; explicit latest request/state/focus handling, no hidden gap or resend. |
| API | Authorized documented latest/before/after/window-refresh modes; detail has no unbounded escape hatch. |
| Existing-data operator | Shipped offline script has checked backup/stop/dry-run/apply path exercised on copied real structure before team-lead's stopped-instance use. |
| Agent tools | No new tool/catalog/policy entry; existing recall unchanged. |
| Verification | Built embedded UI on genuine long session with independent uat-validator, exact landing commit and approved UAT provider/model when live turns are exercised. |

No old-message navigation entry exists or is claimed. Code correct and tested, and reachable by a user/agent, remain two separate delivery claims; this spec-only correction delivers neither implementation claim.

## User-Facing Documentation TODO

Implementing leads update these pages **in the behavior change**; docs-verifier checks actual behavior. This Draft-only correction changes no public behavior and edits no public page.

| ID | Existing affected page | Required update |
|---|---|---|
| DOC-1 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/using-omnipus-ui.md`::The chat area / Clear a chat's context / Sessions / Limits and things to watch | Recent-first≈4-screen open,≈2-screen prefetch, keyboard earlier/Retry/Return, clear barriers,500+100 group policy,4 MiB soft page target overridden by2-screen content/whole-group floor (not a hard byte guarantee), reconnect keeps old reading position, refreshing-cached/expired/error states and operator-index recovery. Replace “reaching earlier messages is planned separately”. No target-link/search/copy-link TODO. |
| DOC-2 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/operations/observability.md`::Replay / Session transcript, new **Paged session API** subsection | Explain full retained archive vs bounded UI/read cuts, new content-free metrics, greenfield missing-index error and separate backed-up/stopped script. **API consumers learn the array→envelope break here**: concrete latest/before/after/window-refresh request/response examples, limit/min_rows and soft-byte/floor/override accounting, flags/expiry/error semantics and link to canonical OpenAPI. All-history scripts must page explicitly. |
| DOC-3 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/protocol/websocket-protocol.md`::Connection Lifecycle / Server to Client Frames | history_limit, recent snapshot metadata/local history_origin, replay done vs complete, unchanged full incremental journal, held live order, historical-reader preservation and matching-client/reload requirement. No ordinal ownership manifest. |

## BDD Scenarios

These use the concrete datasets below. One action per When; concurrent state is Given. Retired old-message scenarios are removed, not reused. Remaining IDs are intentionally non-contiguous so review references cannot silently change meaning.

### B01 — Open the recent end
Traces to: User Story 1, Acceptance Scenario 1.
Category: Happy Path.
**Given** D-A1 and H800 with no samples, **When** the session is opened, **Then** default40 recent row units appear at bottom with truthful earlier continuation, no omitted-prefix content read/replay and current controls.

### B02 — Scenario Outline: Empty, short and exactly full pages
Traces to: User Story 1, Acceptance Scenario 2; User Story 3, Acceptance Scenario 2.
Category: Edge Case.
**Given** D-A2 with `<count>` ordinary retained rows and limit `<limit>`, **When** the newest page is read, **Then** `<returned>` newest rows appear ascending, has_earlier is `<earlier>`, and cursor presence equals that flag; empty is[], not null.

| count | limit | returned | earlier |
|---|---|---|---|
|0|20|0|false|
|1|20|1|false|
|19|20|19|false|
|20|20|20|false|
|21|20|20|true|
|39|40|39|false|
|40|40|40|false|
|41|40|40|true|
|99|100|99|false|
|100|100|100|false|
|101|100|100|true|

### B03 — Latest clear remains a closed boundary
Traces to: User Story 1, Acceptance Scenario 2.
Category: Alternate Path.
**Given** D-A3's two clears, **When** the session opens, **Then** the recent latest segment is shown, exact reached marker is a divider, pre-clear content stays closed and latest-clear metadata is correct even outside the page.

### B04 — Failed snapshot/open retains the prior view
Traces to: User Story 1, Acceptance Scenario 3.
Category: Error Path.
**Given** D-E1 and a previous page, **When** its snapshot history read fails, **Then** ErrorState/Retry is visible, the prior page/cursor remains and no wipe/successful complete is committed.

### B05 — Snapshot with live tools/steer/current controls
Traces to: User Story 1, Acceptance Scenario 4.
Category: Happy Path.
**Given** D-R1's controlled persist/publish barrier, pending tool/ask/approval, active goal and selected span whose start is outside the page, **When** a new viewer attaches, **Then** selected corrected rows have exact local source ownership, finished calls have start/result, pending calls are not completed, actual current controls survive and held live frames follow complete once.

### B06 — Incremental catch-up does not truncate or jump
Traces to: User Story 1, Acceptance Scenario 4; User Story 2, Acceptance Scenario 6.
Category: Alternate Path.
**Given** D-R2 with a servable cursor and old reader, **When** it reconnects, **Then** the full journal tail applies once, old rows/anchor stay and no page limit cuts it; unservable paths instead obey B26, not an automatic latest jump.

### B07 — Scenario Outline: Pixels and drawn-row sizing
Traces to: User Story 2, Acceptance Scenario 1.
Category: Happy Path.
**Given** D-G1 with H/A/distance below, **When** the reader reaches that distance, **Then** the specified request count/limit applies, with repeated events coalesced and drawn tool rows included.

| H | A | distance px | requests | limit |
|---|---|---|---|---|
|800|80|1601|0|not requested|
|800|80|1600|1|20|
|800|80|1599|1|20|
|800|40|1600|1|40|
|800|400|1600|1|20|
|1600|10|3200|1|100|

### B08 — Prepend preserves the moving reader
Traces to: User Story 2, Acceptance Scenario 2.
Category: Happy Path.
**Given** D-G2 and reader motion/live growth during the request, **When** the earlier page commits, **Then** the current anchor offset stays within1 CSS pixel, live values win collisions and no forced bottom scroll/duplicate tool occurs.

### B09 — Signed control and late-image layout correction
Traces to: User Story 2, Acceptance Scenario 2.
Category: Edge Case.
**Given** D-G3's nonzero controls/gaps and deferred image growth, **When** its specified layout change commits, **Then** the current anchor stays within1px including negative terminal shrinkage/unchanged first row and no double compensation.

### B10 — Retry retains a failed boundary, not a new send
Traces to: User Story 2, Acceptance Scenario 3; User Story 3, Acceptance Scenario 3.
Category: Error Path.
**Given** D-E2's failed automatic or explicit pre-clear request, **When** Retry is activated, **Then** that same boundary retries once without resending clear/user input, current rows/cursor persist and ordinary send/Stop remain usable through another failure.

### B11 — Obsolete success and failure are fenced
Traces to: User Story 2, Acceptance Scenario 4.
Category: Edge Case.
**Given** D-R3's delayed A→B→A/own-clear/snapshot generation result, **When** the obsolete result arrives, **Then** it changes no current rows/continuation/error, the read is aborted when possible and no execution stops.

### B12 — Whole-group500/100 bounds preserve browse reachability
Traces to: User Story 2, Acceptance Scenario 5.
Category: Edge Case.
**Given** D-M1 at499/500/501 groups, **When** browsing continues, **Then** new prepends/anchor are not oldest-dropped, only safe whole far-edge groups leave, evicted edges reload and the latest cache never exceeds100 groups; inactive feature payloads are released on leave.

### B13 — Explicit crossing opens one clear segment
Traces to: User Story 3, Acceptance Scenario 1.
Category: Happy Path.
**Given** D-A3 at a closed retained divider, **When** Show earlier messages is activated, **Then** the preceding segment appears above it through shared paging/anchoring, no next barrier silently opens and context/session ID remain unchanged.

### B17 — Actual retained beginning ends fetches
Traces to: User Story 3, Acceptance Scenario 2.
Category: Edge Case.
**Given** D-A4's final predecessor count, **When** the final page commits, **Then** has_earlier=false/no next_before stops requests and control removal retains the anchor/focus fallback.

### B18 — Public corrected projection and session isolation agree
Traces to: User Story 4, Acceptance Scenario 1.
Category: Happy Path.
**Given** D-P1's effects/dispositions/cancel/duplicates/special fields and each session kind at one cut, **When** carriers are compared, **Then** source order/ownership/current projected fields agree, room_ref is preserved when present, private data is absent, child content stays in its child and metadata creates no fake prose.

### B19 — Invalid page input never means newest fallback
Traces to: User Story 4, Acceptance Scenario 2.
Category: Error Path.
**Given** D-V1's authorized session/query variant, **When** it is requested, **Then** D11's exact error occurs, invalid external limits do not clamp, malformed selectors do not disappear and no rejected request emits a page.

### B20 — Access precedes bad limit on REST and WS
Traces to: User Story 4, Acceptance Scenario 3.
Category: Error Path.
**Given** D-V2's auth/hidden-main/missing-child/bad-limit variant, **When** its REST or WS read is attempted, **Then** the same access-first outcome applies before token/limit lookup/bind, without foreign content or parent fallback.

### B21 — Healthy bounded work ignores unrelated prefix
Traces to: User Story 4, Acceptance Scenarios 1 and 4.
Category: Happy Path.
**Given** D-A1's equal suffix/dependencies and valid indexes, **When** newest pages open, **Then** counted read/allocation/replay work meets the adopted prefix envelope; a planted full-read/disposition-scan fallback fails the instrument.

### B22 — Stable final-byte tokens and fault refusal
Traces to: User Story 4, Acceptance Scenario 1; User Story 5, Acceptance Scenario 4.
Category: Edge Case.
**Given** D-R4's valid restart/rollover versus uncovered-tail/index-corruption variants, **When** history is read, **Then** unchanged final-byte occurrences keep their token and full projection, or the exact history-unavailable error occurs with no automatic repair/partial success.

### B23 — Contradictory metadata rejects only the failed page
Traces to: User Story 2, Acceptance Scenario 3; User Story 4, Acceptance Scenario 2.
Category: Error Path.
**Given** D-E3's broken continuation/session/known-overlap data, **When** it is consumed, **Then** no partial page/flags commit and Retry/diagnostics remain; valid page metadata with one bad item retains a visible position-stable placeholder.

### B24 — Large atomic group stays whole under the amended budget
Traces to: User Story 1, Acceptance Scenario 1; User Story 4, Acceptance Scenario 1.
Category: Edge Case.
**Given** D-P2's100/101+ drawn-row atomic group and payload above4 MiB, **When** its boundary page is read, **Then** the whole owner/start/result stays, atomic override/actual bytes are reported and continuation advances; the soft budget never truncates that group or falsely claims a hard maximum.

### B25 — Genuine long-session acceptance is independently evidenced
Traces to: User Story 4, Acceptance Scenario 4.
Category: Happy Path.
**Given** D-U1 on the exact candidate commit/reference envelope, **When** the campaign is independently evaluated, **Then** screenshots/traces/read/network/heap/focus/geometry evidence proves recent-first open, no normal-use gap, row bounds, clear/reconnect/Retry/Return reachability and no synthetic-only acceptance.

### B26 — Snapshot reconnect retains and refreshes the historical reader
Traces to: User Story 2, Acceptance Scenario 6.
Category: Happy Path.
**Given** D-R2b's reader3,000 rows back, saved anchor/focus,500-group edge, pending input and forced boot_mismatch/retention_exceeded snapshot, **When** that snapshot completes, **Then** only tail/current controls replace their owners, the reader stays at its anchor, its bounded interval refreshes directly, cursor commits only on complete and no new-tail-to-reader walk/bottom jump occurs.

### B27 — Sweep changes retained lookahead without500 loops
Traces to: User Story 3, Acceptance Scenario 2; User Story 4, Acceptance Scenario 5.
Category: Edge Case.
**Given** D-X1..D-X5's first/middle/last/effect-target/sweep-race cases, **When** the next page is read, **Then** only actual retained neighbors determine flags/cursors, known expired fences remain usable, no orphan result/phantom page appears and real I/O corruption remains an error.

### B28 — Keyboard-only paging actions and terminal focus fallback
Traces to: User Story 2, Acceptance Scenario 8.
Category: Happy Path.
**Given** D-K1 with keyboard focus on each catalogued action, **When** Enter/Space activates it, **Then** exactly one earlier/Retry/latest operation occurs, focus is retained during pending/failure/prepend and a removed terminal control follows the preventScroll scroller fallback without a jump.

### B29 — Announcements and reduced motion do not treat history as new answers
Traces to: User Story 2, Acceptance Scenario 9.
Category: Edge Case.
**Given** D-K2's screen-reader/reduced-motion/zoom/plain-layout matrix, **When** a history request settles, **Then** its busy state clears with one status or alert, no old-row “New response” announcements/focus theft occur and geometry is corrected without smooth motion or required shimmer.

### B30 — Offline dry-run and verified apply preserve canonical data
Traces to: User Story 5, Acceptance Scenario 1.
Category: Happy Path.
**Given** D-O1's stopped copied instance and independently verified full backup, **When** its selected dry-run/apply mode executes, **Then** dry-run has exact sorted plan/zero writes, or apply publishes readable matching derived indexes only, with independent canonical/backup hash equality and actual summary/exit0.

### B31 — Missing product index is a minimal error, never preparation
Traces to: User Story 1, Acceptance Scenario 3; User Story 5, Acceptance Scenario 4.
Category: Error Path.
**Given** D-O4's existing data without a valid index, **When** REST detail/messages or WS-backed UI opens it, **Then** D11's500/error and ErrorState/Retry appear without snapshot wipe/complete,503, preparation/automatic scan or empty/partial success; Retry merely rechecks and succeeds only after external repair.

### B32 — Slow page content reading does not block append
Traces to: User Story 4, Acceptance Scenario 6.
Category: Happy Path.
**Given** D-L1 with page content I/O blocked after immutable cut capture, **When** a new chat/effect append is attempted, **Then** that append completes before the read barrier is released, the returned page remains at its captured cut and no checked address/correction is mixed across cuts.

### B33 — New diagnostics exclude planted content/query/private canaries
Traces to: User Story 4, Acceptance Scenario 7.
Category: Error Path.
**Given** D-Z1's successful/refused/aborted/storage/index failures with distinct canaries, **When** new diagnostics are collected, **Then** required outcome/count fields are present with no raw content/token/query/private marker; a deliberately planted raw-query log fails this check.

### B34 — Forward reload and explicit latest keep pending input
Traces to: User Story 2, Acceptance Scenario 7.
Category: Alternate Path.
**Given** D-M2's old reader, evicted newer edge,100 cached tail groups and unresolved input, **When** its specified forward/Return/Retry action is activated, **Then** contiguous newer rows or a validated fresh latest page commit as specified, pending IDs/draft persist, no hidden middle gap/resend occurs and latest pending/failure focus states follow D8.

### B35 — Offline precondition failure refuses before writes
Traces to: User Story 5, Acceptance Scenario 2.
Category: Error Path.
**Given** D-O2's live process/missing or tampered backup/wrong root/source drift/unconverted input, **When** apply is invoked, **Then** its exact nonzero class and refusal appear with zero canonical/backup writes and no misleading prepared-session success.

### B36 — Offline repeat and interruption are idempotent
Traces to: User Story 5, Acceptance Scenario 3.
Category: Edge Case.
**Given** D-O3's completed generation or each staged-publication interruption, **When** apply is rerun with valid preconditions, **Then** matching indexes stay byte/mtime identical, incomplete derived staging is safely replaced, canonical bytes/tokens stay fixed and remaining sessions complete without duplicate content.

### B37 — Huge tool outputs stop at the soft budget without cutting the height floor
Traces to: User Story 1, Acceptance Scenario 5; User Story 4, Acceptance Scenario 1.
Category: Edge Case.
**Given** D-B1's100 eligible one-row groups and independent byte-cost fixture, H800/A80/min_rows20, **When** its limit100 page is requested, **Then** fewer than100 groups are selected when bytes bite, at least20 modeled visible rows remain, every tool/owner group is whole, an over4-MiB floor case is explicitly reported and true next_before/has_earlier continue the remaining groups.

### B38 — Normal page never hits the comfortable byte target
Traces to: User Story 1, Acceptance Scenario 5.
Category: Happy Path.
**Given** D-B2's normal40-row8-KiB/group conversation and default4-MiB budget, **When** its opening page is selected, **Then** the normal40-row request is unchanged, no byte_budget stop/override appears and its measured height approximates4H rather than a reduced two-screen open.

### B39 — Four-screen opening and two-screen prefetch survive byte cuts and estimate correction
Traces to: User Story 1, Acceptance Scenario 1; User Story 2, Acceptance Scenarios 1 and 2.
Category: Happy Path.
**Given** D-B3's budget-shortened candidates, row-cap-limited small rows and initially wrong height estimate with enough retained content, **When** the specified open or prefetch fill cycle commits, **Then** opening tops up to≈4H and prefetch acquires≈2H new visible content using bounded contiguous requests, the byte budget never causes an under-two-screen logical fill, and anchor/cursors/whole-group identity stay correct. A true clear/end/error is reported rather than fabricated content or silent short success.

## Test-Driven Development Plan

All tests below are **planned**, not written/executed here. QA's RED uses independent expected values/frozen fixtures; a separate CHECK audits it. Unit→integration→E2E ordering; deterministic concurrency barriers, not sleeps. Removed target-only test IDs are retired rather than reused.

| Order / proposed test | Level | BDD | Independent oracle / scope |
|---|---|---|---|
| T01 TestChatHistoryPage_SelectsNewestAndActualLookahead | Unit | B01,B02,B17 | Exact IDs/flags on empty/exact-full/off-by-one. |
| T02 TestChatHistoryPosition_StableOccurrenceAndSessionScope | Unit | B19,B20,B22,B27 | Equal timestamps/duplicate IDs, rollover/final-byte reconstruction/tombstone/unknown-session distinctions. |
| T03 TestChatHistoryPage_ClearStopsAndExplicitCrossing | Unit | B03,B13 | Exact marker and one-barrier crossing, unchanged context/canonical bytes. |
| T04 TestChatHistoryPage_MergedProjectionParity | Unit | B18,B24 | Hand-derived settle guards/project/retract/cancel/disposition/duplicates; whole101-row group and recorded atomic byte override. |
| T06 TestChatHistoryIndex_HealthyCutAndBoundedReads | Unit | B21,B22,B31 | Count actual addressed records/disposition seeks, no runtime rebuild/uncovered-tail success. |
| T07 chatHistorySizing.measuredRowsAndPixelThreshold | Unit | B07 | Formula tables plus **real virtualizer** with40px rows, A-driven estimates, prefix invalidation/tool child weights, hidden/H0/sample replacement. |
| T08 chatHistoryState.orderedMergeAndGenerationFences | Unit | B06,B08,B11,B23,B26 | Delayed promises, current live result, generation not session-only, split reader/tail snapshot and no frame-range protocol. |
| T09 chatHistoryWindow.readerAndTailPolicy | Unit | B12,B34 |499/500/501 and100/101; safe whole-edge eviction/defer, forward reload, inactive reader/tail/query/media cleanup, pending identity. |
| T10 chatHistoryAnchor.signedEveryLayoutCorrection | Unit | B08,B09,B17 | T3's independent loader/hint/Retry/gap geometry; mutations disable signed corrections/count tail growth. |
| T19 TestChatHistoryRetention_ActualRetainedContinuation | Unit | B27 | Real first/middle/last sweep, partial removal, effect-target expiry, generation race/known expired fence, permission/corrupt negative control. |
| T22 chatHistoryIndexScript.preconditionsDryRunAndIdempotence | Unit | B30,B35,B36 | Real temporary source/backup inventories; actual hashes/mtime and stdout/exit classes, no mock of backup validation under test. |
| T26 TestChatHistoryReadCut_SlowReadDoesNotHoldAppendLocks | Unit | B32 | Real blocked content reader; actual append/effect finishes before release, page cut unchanged, exercises both shard and archive mutex. |
| T27 TestChatHistoryPage_SoftBytesRespectTwoScreenFloor | Unit | B24,B37,B38,B39 | Independent encoded-size fixtures,4-MiB normal/bite/floor/atomic cases; same max-carrier meter/cut on REST/WS; invalid min_rows and row-cap floor/top-up model; removing budget or giving bytes priority over floor must fail. |
| T11 TestGetSessionHistory_ValidationAndAccess | Integration | B19,B20 | Real registered auth/gate/params, REST/WS inaccessible+bad limit, both inbound-validation settings. |
| T12 TestSessionHistory_RESTDetailSnapshotSelectSamePage | Integration | B01,B02,B03,B18,B24,B31,B37,B38 | Real store/handler/hub/generated schemas, identical max-carrier-budget source range/counters, all per-frame origins including special/media; missing-index no wipe/complete or503. |
| T13 TestAttach_PagedSnapshotPreservesLiveAndControls | Integration | B04,B05,B06,B26 | Bind/persist barriers, current pending controls, result coverage versus start only; retain old-reader window, successful live complete independent of range failure. |
| T14 chatHistoryClear.operationOwnedPageRefresh | Integration | B03,B11,B13 | Clear send ownership and marker outside recent page; foreign clear does not teleport historical reader. |
| T15 chatHistoryComponents.retryPlainAndLatestStates | Integration | B02,B10,B11,B23,B31,B34 | Real adapters/store/components, latest pending/error/Retry without resend, minimal index error and plain/manual path. |
| T20 chatHistoryAccessibility.keyboardFocusAndAnnouncements | Integration | B28,B29 | Real catalogued controls, Enter/Space, stable focus while pending, terminal preventScroll fallback, one status/alert and no old-response announce. |
| T23 TestHistoryPageDiagnostics_ContentFreeOnEveryOutcome | Integration | B33 | Capture real structured logs/browser diagnostic payloads; positive outcome/count assertions + canary absence, planted raw-query/content log kills test. |
| T25 chatHistoryIndexScript.productReaderCompatibility | Integration | B30,B35,B36 | Actual separate script output consumed by real Go page reader on effects/clear/dispositions/expired metadata; freeze expected records independently, interruption/second apply checks. |
| T16 chat-history-paging.open-scroll-anchor | E2E | B01,B07,B08,B09,B12,B13,B17 | Built gateway/embedded SPA, real styles/images/virtualizer, normal-use threshold/geometry/counts; preserves Train3 regressions after catch-up. |
| T18 chat-history-paging.prefix-and-real-session-evidence | E2E | B21,B22,B25 | Prefix instrument controls + genuine long session, exact commit/reference metrics/independent validator. |
| T21 chat-history-paging.keyboard-reader-states | E2E | B28,B29,B31,B34 | Keyboard-only actions,200% zoom/reflow/plain/reduced motion, real focus/announcement transitions, no preparing state. |
| T24 chat-history-paging.reconnect-retention-retry | E2E | B04,B05,B06,B10,B11,B20,B26,B27,B34 | Forced fresh snapshots while3,000 rows back, old anchor/refresh/expiry outcomes, session switch/stale error, pending approval/input and no middle gap. |
| T28 chat-history-paging.soft-budget-screen-coverage | E2E | B37,B38,B39 | Built gateway/real virtualizer/geometry plus huge projected tool output: byte-cut/override accounting,≥2H actual fill or explicit boundary/failure,≈4H open/≈2H prefetch, no anchor jump;40-row normal fixture unchanged. |

Backend tests live beside `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_view_test.go`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_effects_test.go`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/clear_view_test.go` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/ws_attach_catchup_test.go`. Proposed script tests: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/scripts/migrations/test_chat_history_index.py`; Go integration fixture alongside session tests. Proposed browser file: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/tests/e2e/chat-history-paging.spec.ts`. QA verifies CI discovery for every new test; a file never executed by CI cannot count.

### Test Datasets

Counts are source groups unless explicitly drawn rows/frames. Rows below often contain parameterized cases; every row traces to BDD, not a test-derived oracle.

#### D-A — Archive/page boundaries

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-A1 | Equal final200 rows/dependencies after1,000 vs100,000 groups;90 days;≥100 MiB long case; valid indexes | Large/scaling | Same40/100 suffix and actual lookahead, no prefix content read | B01,B21 |
| D-A2 |0,1,19,20,21,39,40,41,99,100,101 ordinary rows; limit20/40/100 | Empty/min±1/max±1 | B02 exact table; [] never null | B02 |
| D-A3 |250 old groups,C1,60 groups,C2,0/1/19/20/100 post-clear; marker-like ordinary text | Clear boundary | Latest only, one explicit crossing, exact discriminator, stable tokens | B03,B13 |
| D-A4 |0/19/20 final predecessors with limit20; unchanged first entry on empty terminal shrink | Terminal/exact full | No earlier cursor/fetch loop, correct focus/anchor | B17 |

#### D-G — Real geometry/sizing

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-G1 | H800,A80,Dtop1601/1600/1599 | Trigger±1 |0/1/1 requests,20 limit, dedup | B07 |
| D-G1b | H/A800/40,800/400,1600/10; empty→80 seed; zero/negative/NaN ignored | Clamp/samples |40/20/100; finite average | B07 |
| D-G1c | H0/hidden/H1; repeated same sample;101 samples; width/Verbose change | Hidden/zero/cap | No hidden reads; replace/reset/latest100 | B07 |
| D-G1d | Actual40px one-liners initially estimated80, measured tool children; real virtualizer prefix at2H±1 | Estimate error/real instrument | A-driven unmeasured estimates and actual trigger meet2H; fixed80 mutation fails | B07 |
| D-G2 |24/80/240/1200px rows; tool children;400px reader movement;10 live rows below | Concurrent height | Current anchor≤1px, no tail-delta error | B08 |
| D-G3 | Hint32,loader19.5,Retry72,gaps16; empty terminal; image80→640/table resize | Positive/negative/late layout | Signed every-layout≤1px, no double correction | B09 |

#### D-P — Projection/groups

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-P1 | Pending→guarded success→mismatching settle;P1/P2/retracts; far-day corrected call; discarded input; matching/nonmatching cancel | Effects | Frozen expected projection, bytes unchanged | B18 |
| D-P1b | Tool-only/duplicate IDs/equal timestamps; legacy missing optional turn/model; parent and native/external child | Occurrence/legacy/isolation | Stable origin/last-wins diagnostics/no fabricated facts/no parent graft | B18,B22 |
| D-P1c | Populated participant/reply/model/fallback/helper/goal/handover/diagnostic/verdict/media/**room_ref** plus private model/source canaries | Full public parity/privacy | Correct typed fields/rendering, zero private leak, source origins; legacy absent room_ref stays absent | B05,B18 |
| D-P2 |1/19/20/99/100/101+ drawn-row atomic groups, near-admission-size payload, pending no-result | Compound/max+1 | Whole group/logged atomic-budget override, no orphan/truncation/fake completion | B24 |

#### D-B — Founder-amended soft bytes and visible-height floor

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-B1 |100 one-row large-tool groups; each max-carrier encoded contribution in[200,201]KiB, fixed envelope/context<8KiB; H800/A80,limit100,min_rows20 | Budget bites above floor | Exactly20 whole groups fit4 MiB,21 do not; stop_reason=byte_budget, projected_bytes≤4MiB, row_count20≥floor20, has_earlier/next_before actual. Removing the budget incorrectly returns100 and fails. | B37 |
| D-B1b |40 one-row groups with[256,257]KiB/contribution, fixed overhead<8KiB; H800/A80,limit40,min_rows20 | Budget smaller than floor | Keep20 whole groups even though projected_bytes>5MiB; byte_budget_exceeded=true,budget_override=height_floor; no early16-or-fewer-group cut, no broken tools. | B37,B39 |
| D-B1c | A first atomic group>4MiB and101 drawn rows; separate case group1 row>4MiB followed by19 small rows to meet floor20 | Single-group/floor exception | First case whole group alone, atomic override; second case keep necessary19 following groups too, height_floor override. Never treat one tiny-height group as sufficient solely because it is large in bytes. | B24,B37 |
| D-B2 |40 normal one-row groups≤8KiB each plus<8KiB envelope/context; H800/A80,limit40,min_rows20 | Typical headroom | Entire40-row open fits comfortably, no byte stop/override; prefetch20 likewise unchanged. Fixture headroom is not a claim of measured production average. | B38,B39 |
| D-B3 | Opening byte-cut to20 of40 rows, then contiguous top-up20; prefetch floor20; true end/clear with<floor; delayed image/actual average changes |4H/2H/estimated-to-measured | Open≈4H via normal bounded top-up; prefetch≈2H new content; actual underfill corrected without jump, true boundary/error visible. Budget alone never declares a too-short logical fill complete. | B39 |
| D-B3b | H800,A10 raw floor160/open target320,limit100,min_rows100; enough normal rows, all fitting the500-group reader; separately row-target boundary90+20 drawn rows with floor100 | Clamp/atomic boundary | Multiple≤100-source-group responses fill logical2H/4H; bytes never shorten a capped chunk below100 estimated rows. A final whole group needed for floor is not split or dropped even across the row target. | B39 |
| D-B4 | min_rows omitted/1/20/limit vs0/limit+1/null/float/duplicate/malformed encoding; history_min_rows on inaccessible main + invalid limit | Floor input/precedence | Default20 or valid1..limit; exact D11 refusal, access and limit errors precede floor error, no bind/scan on rejection. | B19,B20,B37 |

#### D-R — Reconnect/races/index faults

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-R1 | Append/effect before bind/between bind-read/read-complete/after release; current controls | Concurrent | Selected/current items once, local origins/complete then held live | B05 |
| D-R2 | Servable same-boot cursor; ahead/unknown/evicted journal; unfinished answer | Incremental/snapshot | Whole tail or bounded latest snapshot, no history cursor mutation | B06 |
| D-R2b | Reader3,000 rows back;499/500 groups; forced boot_mismatch/retention_exceeded; pending input; delayed/failed closed-range refresh | Historical snapshot | Keep reader/focus/anchor; fresh tail/control only; bounded direct revalidation or cached/out-of-date Retry; no bottom jump | B26 |
| D-R3 | Deferred success/error A→B→A; own clear; other-tab clear; replacement snapshot/range | Generation | No obsolete rows/flags/errors; other-tab clear follows D6/D7 | B11 |
| D-R4 | Valid index restart/UTC rollover; missing/corrupt index or archive durable tail beyond metadata; unchanged final-byte script reconstruction | Fault/stability | Same valid token or fixed500/no automatic build/partial success | B22,B31 |

#### D-X — Actual retention

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-X1 | First rolled partition swept, first retained page exact-full | Old edge | has_earlier flips false/no cursor; known expired fence selects actual neighbors | B27 |
| D-X2 | Middle partition swept, both sides retained | Hole in disk retention | Ordered retained neighbors, no fake blank row/renumber | B27 |
| D-X3 | Last rolled partition swept, current/older retained; one removal fails | New edge/partial deletion | Only successful deletions excluded; truthful has_later | B27 |
| D-X4 | Effect retained but original call partition swept; retained target with unreadable/corrupt effect control | Dependency expiry vs damage | Expired target ignored/no orphan; real corruption fails | B27 |
| D-X5 | Sweep after read-cut before response; some/all held rows expire; anchor survives/doesn't; retention disabled control | Race/anchor/disabled | Coherent flags/generation; nearest in-range anchor or explicit expired-range state, never500 loop/auto-latest | B26,B27 |

#### D-V / D-E — Validation and failures

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-V1 | limit omitted/20/40/100/020 vs19/101/0/−1/1.5/+20/space/empty/null/NaN/Infinity/overflow/duplicate/%ZZ | Numeric/type/min±1/max±1 | Default/range or exact400, no server clamp/omission | B19 |
| D-V1b | valid47-char token;46/48/wrong version/bad alphabet/blank/null/control/duplicate/malformed encoding; unknown/foreign valid token | String/scope | Exact400 vs404; known expired boundary remains usable | B19,B20,B27 |
| D-V1c | before+after; one range endpoint; reversed endpoints; after with range; before outside range; range all expired | Mode/range | Exact400/404 precedence; known closed expired range empty with window_refresh mode, not empty whole session | B19,B26 |
| D-V2 | No/invalid auth; hidden main/missing child; legitimate child; bad limit on **both REST and WS**, each read mode | Access/precedence | Access-first refusal before bind/lookup; no user-owner invention/parent fallback | B20 |
| D-E1 | Unreadable required partition/effect, canceled snapshot/index read; previous view | Opening failure | No wipe/complete, retain view/cursor, visible Retry | B04 |
| D-E2 | Offline/timeout/500 older or explicit clear-crossing; Retry fails then succeeds; plain fallback | Dependency/recovery | Same boundary once, current controls usable, no resend | B10 |
| D-E3 | Wrong session, flag without cursor/cursor with false flag, empty nonterminal, repeated boundary, contradictory known overlap; one malformed item with valid metadata | Protocol/per-item | Reject contradictory page, preserve valid page with visible bad-item placeholder | B23 |

#### D-M / D-K — Window/live/focus/accessibility

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-M1 |499/500/501 reader groups,100-page traversal; focused/protected edge; live cache100/101; leave/revisit50 sessions | Row bounds/inactive lifetime | Safe trim/defer,100-tail cap, old page/media/query caches actually released; pending controls preserved | B12 |
| D-M2 | Evicted newer edge; fresh latest success/failure;100 cached rows; unresolved sent/failed/optimistic input/draft; live append during latest | Forward/Return | Contiguous page or fresh latest, stable pending IDs/no resend/hidden gap, exact pending/Retry focus state | B34 |
| D-K1 | Tab/Shift-Tab/Enter/Space on Show earlier/Retry/Return; duplicate activation; terminal control removed | Keyboard/focus | Exact one action; stable focus or specified preventScroll scroller fallback | B28 |
| D-K2 | Screen-reader event capture; reduced motion;200% zoom/reflow; observer absent; historical batch versus new live answer | Announcement/presentation | One polite transition/alert; aria-busy clears; no old-row new-answer announcements; no forced motion | B29 |

#### D-O / D-L / D-Z / D-U — Offline deliverable and proof

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-O1 |3 canonical copied sessions:40/120/0 groups; full verified backup+stop receipt; one index already valid; dry-run/apply | Operator happy/empty | Exact D13 plan/counts/exit0; dry-run no writes; apply only indexes, independently equal canonical/backup hashes | B30 |
| D-O2 | No confirmation/receipt; running PID; incomplete/tampered/wrong-root backup; symlink/traversal; permission/read failure; source drift; unconverted input | Precondition/error |2 for precondition,1 for integrity/I/O; no canonical/backup mutation, truthful refusal | B35 |
| D-O3 | Complete index repeat; interrupted temp-write/verification/publication/between-session stages | Idempotence/crash | Unchanged matching index bytes/mtime; safe derived staging, per-session atomic publication, stable tokens/no duplicate content | B36 |
| D-O4 | Existing canonical transcript without index; corrupt index; metadata coverage mismatch; then externally repaired while stopped | Product missing/fault | Fixed500/error/Retry, no scan503/preparing; valid fresh session empty index works | B31 |
| D-L1 | Real content read paused after cut capture; concurrent chat/effect append then release; later sweep/rollover | Lock/concurrency | Append completes before blocked read resumes, consistent earlier-cut projection | B32 |
| D-Z1 | Distinct text/parameter/result/credential/raw-token/physical-address/query canaries on success/refusal/abort/index/storage errors; planted raw-query log | Log/privacy | Required sanitized events, no canary; planted leak makes assertion fail | B33 |
| D-U1 | Permissioned anonymized genuine long session, actual host/OS/browser/commit,30 opens/30 normal boundaries and reconnect/clear/keyboard/error lanes | External real acceptance | Adopted envelope + saved screenshots/traces/heap/focus and independent validator; synthetic-only cannot pass | B25 |

### Regression Test Requirements

Preserve unrelated assertions. Only explicitly changed array/header/full-history/interim-note contracts receive replacement assertions, never skipped/deleted coverage to manufacture green. No pre-existing release failure is exempt.

| Existing source / behavior | Required protection |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_view_test.go`::TestReadTranscriptView_StartsAtTheLastClearMarker / TestReadTranscriptView_UsesTheLastOfSeveralMarkers / TestReadTranscriptView_OnlyTheExactMarkerCuts | Full helper semantics stay if retained; separate page tests prove bounded cuts/clear crossing. No global full-reader change. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_effects_test.go`::TestToolCallEffects_AppendOnlyAndMergeRules / TestProjectToolCalls_SurvivesDayRollover | Append-only/full merge invariants unchanged; T04 adds page parity. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/input_disposition_test.go`::TestInputDisposition_LabelsOnlyTheDiscardedEntryAndLeavesArchiveBytes; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/cancel_truncation_derive_test.go`::TestReadTranscript_CancelTruncatesOnlyItsTurnsLastAssistantEntry | Cross-page equivalent preserves original derived values/canonical bytes. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/ws_attach_catchup_test.go`::TestAttach_H4_IncrementalWhileStreaming_NoGapNoDuplicate / TestAttach_H6_PersistedBetweenBindAndRead_NoDuplicateNoGap / TestAttach_N4_UnreadableHistory_NeverWipesTheClient / TestAttach_FailedRebuild_NoCatchUpComplete | Existing ordering/read-before-wipe/no-false-complete assertions retained; T13/T24 cover new historical-mode branch without weakening incremental semantics. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/clear_view_test.go`::TestGetSessionMessages_StartsAtTheLastClearMarker / TestAttach_SnapshotReplaysSinceTheLastClearMarker | Replace envelope/header expectations, keep exact old/new/marker assertions, add explicit crossing. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.virtualization.test.tsx`::VirtualizedMessageList; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/cursor.test.ts`::gateFrameBySeq / applySnapshotHistoryWipe | Live/plain virtualization and transport semantics stay; historical snapshot protection is an explicit separate branch. DOM count is not heap proof. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.clear-marker.test.tsx`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.earlier-note.test.tsx` | Unknown-row/divider/refusal behavior stays; note-only expectation gets actionable accessible earlier tests. |
| Train3 corrected anchor/room traffic; chief's interim tests | Re-read and enumerate actual tests at PRE-RED/catch-up, preserve them; add room_ref page parity and one historical batch/no passive-bottom-jump tests. No unobserved test is claimed run. |

D-P1b/c, D-R1/2/3, D-G3 and D-E1 are the old-behavior regression datasets; D-X/D-K/D-O fill round1 gaps and D-B covers the same-round founder byte/floor amendment. QA captures tests-only RED then candidate GREEN plus instrument/mutation evidence and saved logs/exit codes in CI; this correction ran no application test/build/typecheck/browser/UAT.

## Functional Requirements

Requirement IDs remain stable; the removed message-target requirement is not repurposed.

| ID | Normative requirement |
|---|---|
| FR-001 | MUST open bounded recent content approximating four screens with D5 bootstrap/measured sizing, no lifetime read/replay. |
| FR-002 | MUST share newest-first early-stop/ascending projection across messages/detail/snapshot. |
| FR-003 | MUST use stable same-session occurrence positions, exclusive before/after, explicit closed existing-window refresh and actual retained-domain continuation; model/live cursors remain distinct. |
| FR-004 | MUST preserve latest-clear default, explicit one-barrier crossing and operation-owned clear refresh without model-context restoration. |
| FR-005 | MUST retain whole tool groups/current correction/dependency state and typed special content, including room_ref; D4's accepted individual-group row overshoot applies. |
| FR-006 | MUST preserve full incremental journal/bind-hold-live/current-control semantics and failed-snapshot/no-complete behavior; snapshot while historical keeps/revalidates reader per D7. |
| FR-007 | MUST use pixel prefetch, measured drawn-row average/80px seed, A-driven unmeasured virtual estimates and request clamp20..100/default40. |
| FR-008 | MUST preserve current anchor across prepend/control/late-height/live growth with stable keys/signed correction; plain fallback usable. |
| FR-009 | MUST coalesce/fence/abort obsolete generations and keep history mutation separate from live cursor/current control state. |
| FR-010 | MUST expose action-specific ErrorState/Retry, reject contradictory page metadata, preserve current rows/live use and stop at actual terminal history. |
| FR-012 | MUST retain≤500 active reader groups +≤100 tail groups with whole safe far-edge eviction/reload, release this feature's inactive history/tail/media/query payloads and preserve pending input; FR-021 separately governs the soft per-page bytes/floor. |
| FR-013 | MUST apply actual existing auth/main/session access before bad parameter lookup/bind on both REST and WS, with D11 outcomes. |
| FR-014 | MUST preserve public-field/session isolation and visible per-item degradation, exclude private data and use generated contract types/validators/local per-frame historical ownership only. |
| FR-015 | MUST expose sanitized read/replay/anchor/window measurements and meet the adopted genuine-long-session envelope with instrument controls. |
| FR-016 | MUST build on current base, perform PRE-RED-1 source coordination, catch up with each landed overlap and gate only landing order; complete DOC-1..3, feature gates and exact-commit UAT/independent validation. |
| FR-017 | MUST implement D8 keyboard/focus/status/alert/busy/reduced-motion/zoom/plain-fallback states for earlier/Retry/Return/refetch and avoid announcing old rows as new responses. |
| FR-018 | MUST reconcile actual expiry/partial removals/read races into retained-only lookahead/cursors/context; known expired fences remain truthful, real damage remains error. |
| FR-019 | MUST ship/test D13's separate offline stopped-instance/full-backup-verified dry-run/apply/idempotent script with exact exit/summary/integrity guarantees; product has no automatic migration/preparation path. |
| FR-020 | MUST capture a consistent immutable cut briefly and release session/append locks before history content I/O; an actual append must progress while a page read is blocked. |
| FR-021 | MUST apply a4-MiB soft projected-page budget subordinate to measured/estimated2-screen content floor and whole groups, always return at least one whole eligible group, expose byte/floor/override accounting and top up actual coverage to≈4 screens on open/≈2 new screens on prefetch without changing row request bounds or silently underfilling due to bytes. |

## Success Criteria

| ID | Measurable completion condition |
|---|---|
| SC-001 | Healthy indexed open reads no omitted prefix; selected complete groups/relevant dependencies only; full-read negative control detected. |
| SC-002 | All ordinary/retention page traversals have truthful exclusive/ascending continuation with no duplicated display occurrences/orphan results/stale-generation writes. |
| SC-003 |≤1px settled anchor drift and zero normal-use viewport gaps across30 boundaries, including required historical-snapshot/focus layouts. |
| SC-004 | Adopted p95 server/UI/prefix envelope passes with exact commit/data/host/raw samples and discrete work counts. |
| SC-005 |500-reader/100-tail/inactive-payload tests pass over100-page traversal and50 visited sessions; record heap/bytes without claiming a hard byte limit. |
| SC-006 | All five user stories reachable through actual UI/API/operator entry points; genuine-long-session exact-commit UAT independently validated. |
| SC-007 | Generated contracts, QA integrity, five-reviewer/security/docs and platform gates clean or founder-explicitly tracked/deferred; this Draft claims none run. |
| SC-008 | Round1 decisions/dispositions are applied consistently; round2 grill/interview/fix and approval still required. Status remains Draft. |
| SC-009 | Offline script refuses invalid backup/live data, dry-run writes nothing, apply/repeat/interruption preserve canonical/backup bytes and produce indexes verified through the real product reader. |
| SC-010 | B37–B39 prove the4-MiB budget bites only after the height floor on huge outputs, never bites the normal fixture, preserves atomic/floor overrides and≈4H open/≈2H prefetch using measured browser heights/top-ups; record exact cursors/bytes/override reasons, no unclaimed hard byte ceiling. |

## Traceability Matrix

| Requirement | User Story / Acceptance | BDD | Tests |
|---|---|---|---|
| FR-001 | US1.1–2 | B01,B02,B03,B24 | T01,T07,T12,T16 |
| FR-002 | US1.1,US4.1/4 | B01,B18,B21,B22 | T01,T04,T06,T12,T18 |
| FR-003 | US2.6–7,US3.2,US4.2/5 | B02,B17,B19,B22,B26,B27,B34 | T01,T02,T09,T11,T19,T24 |
| FR-004 | US1.2,US3.1/3 | B03,B10,B11,B13 | T03,T14,T15,T16 |
| FR-005 | US1.4,US4.1 | B05,B18,B24 | T04,T12,T13 |
| FR-006 | US1.3–4,US2.6 | B04,B05,B06,B26 | T08,T13,T24 |
| FR-007 | US2.1 | B01,B07 | T07,T16 |
| FR-008 | US2.2,US3.2 | B08,B09,B17,B26 | T10,T13,T16,T24 |
| FR-009 | US2.1/2/4/6 | B06,B07,B08,B11,B23,B26 | T07,T08,T14,T15,T24 |
| FR-010 | US1.3,US2.3/7,US3.2–3 | B02,B04,B10,B17,B23,B31,B34 | T01,T11,T13,T15,T21,T24 |
| FR-012 | US2.5/7 | B12,B25,B34 | T09,T16,T18,T21,T24 |
| FR-013 | US4.2–3 | B19,B20,B23 | T02,T11,T15,T24 |
| FR-014 | US1.4,US4.1/3 | B05,B18,B20,B22,B23 | T04,T06,T11,T12,T13,T15 |
| FR-015 | US4.4/7 | B07,B09,B21,B25,B33 | T06,T07,T10,T18,T23 |
| FR-016 | US1–5 | B09,B13,B25,B30 | T10,T14,T16,T18,T22,T25 + dependency/docs gate |
| FR-017 | US2.8–9 | B28,B29,B34 | T15,T20,T21,T24 |
| FR-018 | US3.2,US4.5 | B17,B26,B27 | T01,T02,T13,T19,T24 |
| FR-019 | US5.1–4,US1.3 | B22,B30,B31,B35,B36 | T06,T12,T15,T22,T25 |
| FR-020 | US4.6 | B32 | T26 |
| FR-021 | US1.1/5,US2.1–2,US4.1 | B24,B37,B38,B39 | T04,T12,T27,T28 |

Every live FR has BDD/tests; every BDD has at least one live FR/test and numbered story acceptance reference. No removed message-target scenario/requirement/test is hidden in a matrix range. Round1 dispositions below refer to the revised normative sections, not a self-issued review PASS.

## Round 1 disposition

Review input: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/specs/chat-history-paging-spec-review.md` at `7b6cc0884`. This table records corrections/settled founder answers, **not adjudication by the author of their own design**. Independent round2 review still applies.

| Finding | Disposition | Where / rationale |
|---|---|---|
| CRIT-001 | Fixed by founder-directed descope | Decisions R1-Q2, Non-goals, revised US3/D3/Reachability/DOC/TDD/matrix: no message-neighborhood loader/route/copy-link/search producer. Old target-only work removed entirely; clear crossing stays. |
| MAJ-001 | Fixed | D6/D7 exact kept reader/tail/control state, bounded existing-window revalidation/anchor/expiry/failure behavior; US2.6,B26,D-R2b/D-X5,T13/T24,FR-006. |
| MAJ-002 | **Fixed by founder-amended Q4** | Same-round2026-10-21 amendment supersedes the initial row-only answer. D4a adds4-MiB soft budget, modeled2-screen floor/actual top-up, whole-group and floor override, with shape/validation/observability and B37–B39,D-B,T27/T28,FR-021/SC-010.≈4H open/≈2H prefetch remain. No hard cut below floor, no re-raised decision; MAJ-008 stays accepted. |
| MAJ-003 | Fixed | C18/D14 sweep-chain/partial deletion/retained tombstones/read-cut reconciliation, US4.5,B27,D-X1..5,T19/T24,FR-018. Expected expiry is not500; damage still errors. |
| MAJ-004 | Fixed by founder decision | R1-Q3/D1/D11/D13: new sessions maintain metadata; runtime missing-index error/Retry only; separate stopped/backed-up script with dry-run/idempotence/real-reader tests. No automatic boot/lazy/background rebuild or preparing UI. B30/B31/B35/B36,T22/T25,FR-019. |
| MAJ-005 | Fixed by simpler ownership choice | D4/D10/Alternatives replace ordinal manifest and REST duplication with generated HistoryEntryOrigin on source frames. Explicitly weigh local owner fields and REST-history/control-only WS; emitter matrix tests in T12/T13. |
| MAJ-006 | Fixed / founder R1-Q1=B | D11 chat-specific parser; D12 current-base work/per-landing catch-up/landing-only gate/PRE-RED-1; C21 records missing interim ref; T4/D4/D-P1c/FR-005 cover room_ref parity. No unlanded room parser dependency. |
| MAJ-007 | Fixed | D8 exact Button/ErrorState/Skeleton/state/focus policy, US2.8–9,FR-017,B28/B29,D-K1/2,T20/T21 plus T15/T24; target-loading/highlight portion not applicable after descope. |
| MAJ-008 | **Accepted by founder** | R1-Q4/D9 retains500+100 rather than proposed single-window simplification. Explicit inactive-session history/tail/media/query release, D-M1/T09/SC-005. Accepted machinery is not reopened. |
| MIN-001 | Fixed | D5 unmeasured estimate follows A including composite row weights/prefix invalidation; D-G1d and T07/T16 exercise real virtualizer40px-row case, not just algebra. |
| MIN-002 | Fixed | D11 identical REST/WS access-first→limit→bind order; B20/D-V2/T11/T24 include inaccessible main + bad history_limit. |
| MIN-003 | Fixed | D1 captures immutable marks briefly, releases **both** shard/append mutex before content read; C3 acknowledges existing ReadAtRaw limitation. US4.6,B32,D-L1,T26,FR-020 prove real append progresses under slow read. |
| MIN-004 | Fixed | Observability canary/privacy negative control; US4.7,B33,D-Z1,T23/FR-014/015. Required event presence plus content absence, not source-string scan. |
| OBS-001 | Fixed (observation incorporated) | D2/Alternatives explicitly compare new monotonic chat-occurrence number with hash. Stability is only after final canonical bytes; offline script refuses conversion/rewrite. No claim every rebuild across historical conversion preserves offsets. |
| OBS-002 | Fixed (observation incorporated) | C19/D8 names catalogued Skeleton for visual pending, one separate polite status, Button for earlier/Return, ErrorState for alert/Retry; generated no local one-off control. |
| OBS-003 | Fixed (observation incorporated) | DOC-2 names a Paged session API subsection where programmatic consumers learn the breaking envelope/parameters/expiry, with canonical-contract reference. |

## Ambiguity Self-Audit and Clarifications

| Topic | Settled state / no silent assumption |
|---|---|
| Interview authority | Written issue model plus explicit2026-10-21 founder answers, not this author's recommendation, owns product scope/trade-offs. |
| Dependency source | Train3 source read; interim origin/local ref still absent. PRE-RED-1 is an explicit team-lead/chief source handoff, no fabricated implementation or landing gate on work. |
| Metadata lifecycle | Greenfield runtime only; index failure visible. Separate D13 offline script is a concrete deliverable, not deferred to a follow-up or hidden product migration. |
| Bytes/cache concern | Same-round amended Q4 fixes MAJ-002 with a4-MiB soft budget and2-screen/whole-group precedence; MAJ-008's500+100 policy stays founder-accepted. Performance envelope/atomic overshoot retained. No reopened choice. |
| Reconnect/expiry | Explicit D7/D14 outcomes; no silent automatic bottom reset or infinite500 at an expired boundary. |
| Review process | This is round1 correction only; round2 grill/interview/fix and independent gates remain. The author does not adjudicate disputes over this design. |
| Tests / evidence | All new test names/data are plans. Only lightweight document/git/source checks run in this correction; no application green/UAT/migration execution is claimed. |

## Evaluation Scenarios (Holdout)

External evaluator prompts, not development tests; excluded from TDD/matrix. In a shared spec these descriptions are **not secret**. Team-lead supplies an unrevealed genuine session/failure/geometry schedule to the independent evaluator; do not pretend these published prompts are hidden.

| ID / category | External setup/action | Expected observation |
|---|---|---|
| H1 / Happy Path | Open evaluator-selected genuine long conversation on a recorded ordinary device. | Recent content/control state within adopted envelope without lifetime transfer. |
| H2 / Happy Path | Read older work naturally while another viewer/agent continues. | No normal-use gap/forced bottom jump; tools remain truthful. |
| H3 / Happy Path | Cross a real clear divider using keyboard, then explicitly Return to latest. | Saved history/context distinction holds; latest is current; pending input not resent; actions/focus reachable. |
| H4 / Error | Lose network just before an earlier-page response. | Current reader stays; visible Retry recovers once, with no old-row announcements. |
| H5 / Error | On a copied test instance, remove required derived metadata before open. | Minimal unavailable-history error/Retry, no preparing state/lifetime repair/false empty. |
| H6 / Edge Case | Force a fresh snapshot after gateway restart while far back, with a late image expansion. | Reader anchor stays; current controls/tail refresh separately; old range revalidates directly. |
| H7 / Edge Case | Sweep eligible partitions and visit enough sessions to expose inactive payload leaks. | Truthful retained boundaries/expiry state; no phantom pages,500 loops or retained500+100 payloads per visited session. |

## Open questions for the founder

none
