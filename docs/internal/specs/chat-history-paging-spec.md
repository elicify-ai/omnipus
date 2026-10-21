# Feature Specification: Chat history paging

Status: Draft
Created: 2026-10-21
Amended: 2026-10-21 — final fix round 2, after both founder review interviews; no third grill
Issue: https://github.com/elicify-ai/omnipus/issues/1266
Size: Feature
Author: architect
Repository/worktree: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266
Code grounding revision: `5c2753da7fbf4caa728e66e3c7a6a89c3712690d`
Round-1 input: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/specs/chat-history-paging-spec-review.md`, commit `7b6cc0884`
Round-2 input: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/specs/chat-history-paging-spec-review-round2.md`, commit `11d85508f`
Branch: `feature/1266-chat-history-paging`

Correction: the first Draft required waiting for unlanded dependencies, proposed an unreachable message-target route, and mixed automatic index rebuild designs. Those clauses are superseded by the founder's 2026-10-21 answers: build now, gate only landing order, remove message-target navigation entirely, and ship a separate offline operator script rather than a product upgrade path. The founder selected the 500-group reader window plus 100-group tail cache and subsequently amended Q4 in this same round to add a soft page-byte budget subordinate to a two-screen content floor and whole-tool-group atomicity. This correction does not claim implementation, independent review approval or runtime acceptance.

Correction, round 2: the previous correction forbade even a small post-crash tail catch-up and chose a separate-language repair implementation. That was wrong for routine index-publication gaps and one-authority projection maintenance. Founder R2-Q1 now permits only a bounded64-record suffix reconciliation; R2-Q2 selects a separately built Go command reusing the product's index/projection code. Earlier contradictory no-tail-heal and Python clauses are replaced below. The interim replay source is now available and was read at `da4b75df28c8a5bde751152659531e89b00062ad`; its actual completion boundary is catch_up_complete, not replay done.

## Decisions log

The following answers are recorded verbatim in substance from the founder's round-1 dispatch, dated **2026-10-21**. **R1-Q** labels refer to the review interview; they must not be confused with the first Draft's differently numbered questions.

| Decision | Founder answer / authority | Normative effect |
|---|---|---|
| R1-Q1 = B | Build now on the current release/v0.1.1 base; catch up with Train 3 and the chief's interim replay fix, branch `fix/chat-replay-render-once`, as each lands. **Only the landing ORDER is gated.** The chat query parser is written in this feature; do not depend on parseHistoryQuery in the room handler. The interim fix is re-read before RED. | D11/D12: no dependency-landing gate on spec approval, RED or GREEN; no cherry-pick of the unlanded train. Team-lead owns the pre-RED source handoff and subsequent catch-up/landing coordination. |
| R1-Q2 — removed requirement | Founder: **“to jump to a specific message is no requirement.”** | No message-neighborhood loader, message-target route parameter, search-hit integration or copy-link action. The old-target stories/scenarios/requirements/tests/datasets/documentation/reachability work is removed, not renamed. Show earlier messages across a `/clear` divider remains required. |
| R1-Q3 = greenfield | **NO upgrade/migration path and NO preparing-history state in the product.** New sessions get their index as they are written. Existing data on the founder's live instance gets a **SEPARATE one-off migration script**, run by team-lead with the instance stopped after a full backup. The script ships with the feature, is tested, idempotent, has dry-run output and checks the backup precondition. A missing product index is a clear error with Retry, never silent empty/partial history. | D1/D13: no boot-time **lifetime backfill**, background/lazy rebuild, automatic503/retry cadence or progress UI. Round2 explicitly clarifies: known-prefix≤64 recovery is allowed (R2-Q1); the separate deliverable is a shared-Go command (R2-Q2), not a second-language product upgrade service. |
| R1-Q4 — initial interview answer, later amended below | Keep the **window plus the 100-group tail cache**, initially **row cap only, NO byte budget**; the review concerns were accepted on that basis. Keep the oversized-group and performance-envelope answers as drafted. | Historical decision record only for the no-byte-budget clause: the same-day amendment below supersedes it. The 500+100 group policy, atomic-group exception and numeric performance envelope remain selected. MAJ-008 remains founder-accepted. |
| R1-Q4 amendment — 2026-10-21, same fix round | Keep the drafted window + 100-group tail cache + row cap and **ADD a page size limit**: **(1)** soft byte budget per page; **(2)** it must never be so small that the user has less than two screens to scroll—the budget never cuts below content needed for at least two screens using the measured/estimated row-height model; **(3)** never split a tool group, always at least one whole group where content exists. Pick a comfortably sized default and test huge-output/normal/open/prefetch cases. | D4a selects **4 MiB soft projected-page budget**, two-screen floor takes precedence, whole groups take precedence; row request cap remains20..100. Actual height is verified/topped up at the client. MAJ-002 is **fixed by founder-amended Q4**, not accepted as-is; MAJ-008 stays accepted. B37–B39/T27–T28/FR-021 cover the amendment. Do not reopen either decision. |
| R2-Q1 = A — founder, 2026-10-21 | Allow a small automatic catch-up of **ONLY records after the index's last known point**, bounded at about64 records, at app start/open after a crash and the partition-delete/index gap. Larger or inconsistent gaps still produce the visible error and need the repair tool. Mirror the existing ordinal-index self-heal pattern, not a lifetime rebuild. | D1a specifies exact64 combined mutation records, publication order, deterministic idempotent retry and fault limits. Missing baseline index is never seeded from a suffix; no preparing UI or old-data backfill. |
| R2-Q2 = A — founder, 2026-10-21 | The one-off repair tool is a **separate Go command**, not shipped in the product binary, with a build step; it **REUSES the product's index and projection code**, never a second implementation. Operator flow: **dry-run → verified backup → stopped instance** before apply. | D13 replaces the former script with shared-Go construction, an explicit manifest/backup producer/schema, cutover-first preflight, post-stop backup/source verification, exact refusal exits and concrete CI steps. Preflight dry-run does not need a prior backup/stop receipt; apply does. |

The initial written issue model remains the interview for recent-first opening, two-screen pixel prefetch, drawn-row sizing, seamless prepend, shared early-stop reader and real long-session UI acceptance, **except** that R1-Q2 removes its old-message jump sentence. This is the **final correction after grill round2 and the founder interview**, not another ADR or review verdict. Status stays Draft until founder approval. There is no third grill; any remaining genuine blocking dispute over this authored design goes to the founder, not a repeated review cycle.

## Discovery, authority and scope

Open a long conversation at its recent end without replaying its lifetime. Load approximately four chat screens initially. As the person scrolls upward, fetch approximately two more screens before the loaded beginning, preserving the reader's position. Earlier history remains saved and reachable by scrolling, including history before `/clear`. Display paging does not restore the agent's old model context or change execution.

| Dimension | Scope |
|---|---|
| Actors | A signed-in person reading main, existing extra, channel, scheduled, task or helper conversations; the existing gateway; an authorized paged API client; team-lead performing the separate offline data preparation. |
| Problem | Months-long standing conversations make opening, transferring and reconstructing history proportional to their lifetime. Virtualizing the display alone does not reduce server reads or replay. |
| In scope | Recent-page open, upward prefetch, stable scroll/focus, explicit clear-divider crossing, reader-centered bounded history and forward reload after eviction, Return to latest, shared REST/snapshot paging, reconnect while reading history, retention-aware continuation, visible errors/Retry, obsolete-read cancellation, observability and real long-session acceptance. The separate backed-up/offline Go command ships with the feature. |
| Priority | P1-high, matching the issue. Work can begin on the current release base; only integration landing order is gated by the overlapping branches. |
| Human acceptance | Open a genuine long session in the built UI; read upward during live activity; cross a clear divider; reconnect after sleep/restart while reading older history; use Retry/Return to latest with keyboard and assistive-technology checks. Retain geometry, screenshots, network and memory evidence on the exact candidate commit. |

### Non-goals

| Excluded work | Safeguard |
|---|---|
| Jump to a specific message, message-content search, a loader around a message, message-target route parameter and copy-link/permalink action | Entirely out of this issue by R1-Q2. Ordinary cursor paging and refetching an already loaded window are not an ID lookup or a new navigation destination. |
| Automatic old-data upgrade, lifetime boot/open rebuild, background migration and preparing-history state | No lifetime scan to manufacture a page. R2-Q1 permits **only** D1a's≤64 suffix from a known valid baseline; absent old-data baseline is established only by the separate Go command. The already-existing C23 boot cutover is run first when needed, never reimplemented by this feature. |
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
| C11 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/lib/api/sessions.ts`::fetchSessionMessages / fetchSessionDetail / parseWireMessageList / rawToMessage; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatThread.tsx`::ChatThread; **full verified route/workspace/test consumer inventory in D15** | Existing array adapter/per-item placeholder/header side channel, route flag reader, workspace detail-pointer reader and whole-history/mock consumers all change atomically. D15 names actual files/symbols and the exhaustive RED sweep; C11 is not a two-file completeness claim. |
| C12 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/session.ts`::applyMessageArray / bakeToolCallsByOwner / stampToolCallOffset; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/messages.ts`::MAX_MESSAGES_PER_SESSION / getMessages / evictMessageFromBucket | Oldest-drop 500-message ring would discard a prepend. Duplicate display IDs keep the last occurrence; tool ownership/text offsets already matter. |
| C13 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/slices/catchup-frames.ts`::handleSessionSnapshotFrame / handleCatchUpCompleteFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/cursor.ts`::applySnapshotHistoryWipe / gateFrameBySeq | Current snapshot wipes history; complete commits the live cursor. Preserving the historical reader is a target change, not existing behavior. |
| C14 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/clear-refetch.ts`::runClearRefetch / armClearRefresh / flushHeldClearProjection; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/has-earlier.ts`::recordSessionHasEarlier | A particular clear send owns its refresh; busy application is deferred; old markers cannot confirm newer operations. |
| C15 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/main_session.go`::gateMainRead / mainSessionAllowed / resolveMainSessionForRead; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/rest.go`::withAuth / jsonErr; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/auth.go`::checkBearerAuth | Actual authenticated session read and main-eligibility gates. No separate per-user read-ownership predicate is claimed. |
| C16 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/search/SearchModal.tsx`::SearchModal; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/useSelectSession.ts`::useSelectSession; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/session.ts`::attachToSession | Existing search opens sessions, not message hits. It remains unchanged; no new target route is commissioned. |
| C17 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/openapi.yaml`::getSession / getSessionMessages; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/asyncapi.yaml`::AttachSessionFrame / SessionSnapshotFrame / CatchUpCompleteFrame / MediaFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/Message.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ToolCallStartFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/ToolCallResultFrame.yaml` | No chat-page envelope yet; snapshot describes a full replay. Tool frames lack persisted-source ownership. Shared WS schemas and inline mirrors must change together. |
| C18 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/retention_sweep.go`::RetentionSweep / expireArchiveSessions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/archive_expiry.go`::SweepExpired / sweepRolledPartitionsLocked / expireSession / advancePastExpiredLocked / rowResolvable | Round-1 source confirms rolled-partition expiry is called from retention; partial delete success is real; absent partition differs from unreadable/corrupt content. Main identity is protected while old rolled content expires. |
| C19 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/design-system/catalog.json`::Button / ErrorState / Skeleton entries; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/ui/button.tsx`::Button / ActionAnnouncement; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/ui/error-state.tsx`::ErrorState; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/ui/skeleton.tsx`::Skeleton | ErrorState supplies role=alert/Retry; Skeleton is aria-hidden and reduced-motion aware; Button supports pending feedback but pending disables the native control. D8 avoids unintended focus loss and duplicate announcements. |
| C20 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/store.ts`::withBucket / syncForeground / sessionsById | Buckets are keyed by session. No verified global inactive-reader eviction policy is assumed; D9 explicitly releases this feature's inactive history/tail contents. |
| C21 | **Git object newly available in round2:** origin/fix/chat-replay-render-once `da4b75df28c8a5bde751152659531e89b00062ad`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/lib/attach-replay.ts`::AttachReplayBuffer; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/lib/ws.ts`::WsConnection; ChatScreen::VirtualizerAutoFollow | Earlier absence was true at round1, now superseded. Actual source buffers through catch_up_complete, handles multiple outstanding/serialized attaches and refusal/replay-error/disconnect, and respects upward scroll intent. No runtime test receipt is claimed. Train3 remains `573c3674462650ce0f7af8e3e02de0fca50ae295`. |
| C22 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/ordinal_index.go`::repairOrdinalIndexLocked / readRowAt / tornRowTail / rebuildOffsetsLocked | Durable mutation-row/derived-offset saved-tail repair is an existing precedent; framed corruption differs from a provably torn unpublished tail. Its broader rebuild fallback is not authorization to ignore this feature's64 bound. |
| C23 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/unified_conv.go`::CutoverSavedChatsAtBoot / CutoverSavedChatsAtBootWithAgentStores; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/agent/loop_construct.go`::initializeConvertedSessions | Existing explicit boot cutover runs before store/cache/dispatch, can rewrite legacy bytes and is idempotent on current format. Repair preflight refuses unconverted data; authoritative backup must be after successful cutover, never a stale pre-conversion fingerprint. |
| C24 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/.github/workflows/build.yml`::build; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/scripts/go-test-shards.py`::package_set / partition / check; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/deploy/ci-worker/runci.sh`::run_gotest | Current candidate Python test is explicitly named; Go sharder does dynamically discover new Go packages. Neither proves new command/native-Windows tests ran. D13 names actual required wiring/steps. |
| C25 | `/usr/local/Cellar/go/1.27.1/libexec/src/syscall/syscall_windows.go`::Open, read from local toolchain source; C18::sweepRolledPartitionsLocked | Local Go source opens with FILE_SHARE_READ and FILE_SHARE_WRITE, not share-delete. This is source evidence, not certification of the pinned build/native Windows behavior. D14/T19/T26/native CI cover sharing-busy retention and per-record handle release. |
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
| Offline Go command | Operator-only canonical-file inventory, backup verification and derived metadata; no application constructor with implicit conversion or model/credential/network behavior. |

Manual flows: session selection → attach → bounded snapshot/incremental journal or typed live-only controls → live; scroll → page → anchored prepend; snapshot during reading → tail/control refresh + bounded existing-window revalidation; clear → operation-owned latest page; retention → truthful retained boundaries; known checkpoint →≤64 suffix recovery; built command → dry-run→verified backup→stop/equality→apply/verify. These are not invented GitNexus processes/clusters.

## User Stories & Acceptance Criteria

### User Story 1 — Open a long conversation quickly (Priority: P1)

A reader opens a standing conversation and sees recent messages and truthful current controls without transferring months of history.

**Why this priority:** primary issue cost. **Independent Test:** compare equal recent suffixes with short/long prefixes.

1. **Given** a long conversation, **When** it opens, **Then** approximately four screens appear at the recent end without a lifetime content read/replay.
2. **Given** an empty, short or cleared conversation, **When** it opens, **Then** only actual available content appears, respecting the newest clear boundary.
3. **Given** unavailable storage or metadata after any permitted≤64 catch-up attempt, **When** opening is attempted, **Then** a clear history error/Retry preserves cached rows and authorized live controls; neither empty success nor lifetime data preparation is invented.
4. **Given** live tool/steer activity and a lost browser connection, **When** the viewer reconnects, **Then** current controls/progress and historical tool ownership remain correct without duplicate or orphan rows.
5. **Given** normal or huge-output history, **When** a page is selected under the soft size budget, **Then** bytes limit unnecessary extra content only after the modeled two-screen floor; whole groups remain and normal content is not needlessly cut.
6. **Given** authorized active work in a session whose history index is unavailable, **When** the person attaches or sends a new instruction, **Then** its saved/live state, current approvals and Stop stay reachable with an explicit history error; no suffix-only partial history or automatic resend is invented.

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
10. **Given** the held range is refreshing and fails or expires, **When** that outcome commits, **Then** the exact cached/out-of-date/Retry or partial/full expiry state and focus fallback are visible, prefetch is paused as specified and no passive Return to latest occurs.

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
8. **Given** a valid saved index and a small post-crash suffix, **When** startup/open reconciles it, **Then** at most64 consistent mutations heal once; a missing baseline, larger gap or inconsistency stays a visible error needing operator repair.
9. **Given** different REST/tool/special-frame encoded sizes, **When** REST and WS select at the same cut/options, **Then** their one shared meter chooses the same source range or explicitly fails; it never falls back to a guessed weight.
10. **Given** a reader-held file on a supported platform, **When** retention runs, **Then** actual deleted/present state and tombstone privacy remain truthful, including Windows busy/continue/retry rather than fake expiry or a stopped sweep.

### User Story 5 — Prepare existing founder data offline, explicitly (Priority: P1)

Team-lead prepares the founder's current canonical data with the instance stopped and a verified full backup, independently of ordinary product operation.

**Why this priority:** greenfield runtime behavior must not strand the founder's existing data or hide a migration in first open. **Independent Test:** real temporary data/backup trees, dry-run, apply, repeat and interruption checks before touching live data.

1. **Given** a stopped instance and verified full backup, **When** dry-run is invoked, **Then** its deterministic plan/counts are shown and nothing is written.
2. **Given** missing/incorrect backup evidence, a running instance or changed source data, **When** preparation is invoked, **Then** it refuses with a nonzero exit and no canonical write.
3. **Given** a completed or interrupted prior preparation, **When** apply is rerun, **Then** completed matching indexes are unchanged, unfinished derived staging is safely resumed/replaced and canonical content/identities remain byte-identical.
4. **Given** history still lacks a usable baseline or exceeds bounded recovery at ordinary read time, **When** it is opened, **Then** history shows an error/Retry while authorized live controls remain usable; no lifetime backfill, preparing state or silently partial/empty transcript appears.
5. **Given** unconverted data or a stale pre-cutover backup, **When** the command preflights it, **Then** it refuses with the exact class/exit and directs canonicalization first, followed by a fresh authoritative dry-run/backup rather than indexing moving bytes.
6. **Given** the standalone command source at the feature revision, **When** it is built and its manifest/repair tests run in the named CI job, **Then** a runnable separate command uses the product's Go core, produces/validates a specified complete manifest and refuses incomplete/incorrect evidence; no unexecuted-test claim counts.

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
| Snapshot / live-only degradation | Only a completed live/control catch-up advances its live cursor. mode=live_only is explicitly **not** a successful history snapshot, replayCompletedForSession, seen acknowledgement or old-history proof. Older-page/range refresh never changes live cursor/attention or reactivates historical cards. |
| Focus/geometry | ≤1 CSS pixel settled anchor drift in the adopted reference envelope; preserve actual focus except the explicitly defined removed-control/expired-anchor fallback. No forced bottom jump from passive history updates. |
| Runtime missing metadata / bounded suffix | Valid-prefix≤64 suffix can reconcile once per cut. No baseline,65+ or inconsistent state: messages500, explicit metadata-only unavailable detail, and authorized attach's typed live_only controls/Stop path. No503, preparing progress, automatic lifetime construction or false history success. |
| Privacy | Public messages/positions and new diagnostics exclude private source/routes/model payloads/physical marks. New logs have no text/result/parameter/raw-query canaries. Existing privileged underlying-storage cause logging is not silently redesigned. |
| Independence | Model context, retention policy, Stop/steering authority, room behavior and tool policies are unchanged. No newly callable agent tool exists. |

Use generated wire types, existing internal address/ToolCallID invariants and local non-wire anchor/request state. A history position is not a transport cursor or model ordinal. Do not introduce nominal wrappers without additional invariants.

## Prerequisites, Development Setup and Tech Stack

| Topic | Source-grounded requirement |
|---|---|
| Platforms/runtime | Existing supported Linux/macOS/Windows gateway and embedded SPA; pure-Go backend, Go1.26.9 from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/go.mod`. React19, existing virtual-list/query libraries and Vitest4.1.11 from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/package.json`. No new application runtime dependency. |
| Services/auth | Existing authenticated gateway origin; paging needs no model call, new credential, database server or monitoring service. |
| Administrative command | Separately built pure-Go maintenance command at the feature commit, linking the product's session-index/projection package. No separate-language implementation, new application runtime dependency or product-binary subcommand. Go toolchain/build step is an operator prerequisite; copied-data tests precede live use. |
| Application setup | Follow `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/CLAUDE.md`::SPA embed / Running it locally and root build/test rules. Use the built gateway with fresh embedded assets, not stale SPA files or an unrelated dev server. Team-lead provisions the isolated data root/port. |
| This correction | No local heavy command, contract generation, application run or UAT. No setup/gate is claimed executed. |

Expected first open: newest bounded page and truthful current controls. Known setup traps remain absent SPA embed, missing build tags and bare root TypeScript no-op checks; none is a paging verdict. No new start/stop/health command is commissioned. Data remains file-based in the existing session directories.

## Deployment / Runtime and Integration Boundaries

Deploy matching backend/contracts/embedded SPA together; the messages array becomes an envelope. Old browser tabs reload. Do not add a permanent dual-array/full-history fallback.

Fresh sessions have a valid empty index established with session creation before history can be served, and every accepted archive append (including private model/model_ref records, which add no public row) advances its covered-tail metadata; chat/effect/disposition writes additionally maintain their projection references. Restart/open reuses a validated index and may perform **D1a's bounded suffix catch-up only**: at most64 complete new mutation records after its durable known point, never a lifetime recount. Missing baseline,65+ pending mutations or inconsistency still needs D13's separate Go repair command and a visible error. Sending/canonical appends remain allowed and never create a suffix-only partial index for such a session; D7a keeps authorized live controls and Stop reachable during history failure. Canonical durable data is not rolled back or reported unsaved because derived publication failed. This adds no generic importer/converter or preparing-history UI; the established saved-chat boot cutover is sequenced before offline backup/index preparation as D13 requires.

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
| Derived publication | Canonical record fsync precedes a derived mutation-row fsync; derived offsets/root/covered-end publication follows, as D1a details. A durable canonical save remains saved if index publication fails. An existing valid prefix can heal only its≤64-record suffix; never advance a root over an unreconciled gap or seed a missing index with new writes. |
| Cold/fault | At startup after existing cutover and at first indexed-session read/append, run the same D1a reconciler from its validated durable checkpoint.0 delta is a no-op;≤64 consistent mutations heal; missing baseline/corrupt prefix/65+ delta returns history_unavailable. No lifetime or background backfill/preparing worker. |
| Session without usable index | Canonical chat/model/effect/disposition appends continue under their existing admission/durability rules. Do not create/extend a valid-looking partial index, change covered marks or drop saved records. Emit explicit unavailable history state; only D13 full retained-data repair can establish that session's baseline. This is not an unsaved-message failure or permission to skip model/control authority. |

Metadata format/generation storage must support immutable read cuts and the common Go engine, without duplicating content/projection authority. Required bodies/dependencies that are unreadable fail the page; malformed-line behavior preserves C2's established diagnostic distinction rather than inventing a permissive legacy reader. The reader/selector's byte-weigher is the single gateway-owned seam specified in D4b; session code never imports gateway or guesses frame sizes.

### D1a — Durable publication and bounded post-crash reconciliation

**Single engine/home:** backend-lead adds the chat-index writer, reducer/projection-state construction and reconciler in the existing `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/` package. The product and D13 Go command invoke those same routines/types/codecs; offline full construction and online suffix reconciliation are modes of one engine, not two implementations. Reuse/extract the current C2 effect/disposition/cancel/duplicate rules. The command does not call an agent/gateway constructor or copy these algorithms.

The publication precedent is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/ordinal_index.go`::repairOrdinalIndexLocked / readRowAt / tornRowTail: durable rows before derived offsets, saved-tail completion and distinction between a torn unpublished tail and framed corruption. **Do not copy its lifetime offsets-rebuild fallback into online chat recovery**; this feature's64 bound is strict.

| Stage | Write order / crash interpretation |
|---|---|
| Fresh creation | Prepare/fsync a schema-versioned valid **empty** chat-index checkpoint and its root before publishing a new session's readable identity. Failure refuses creation, never publishes a fake empty/history-ready session. Every creator uses the common session creation path; existing nonempty sessions never take this initializer. |
|1 Canonical mutation | Under normal session→archive publisher ordering, append one complete canonical archive or disposition record and fsync. Capture its stable address/end. Private model/model_ref records advance coverage but add no public row. Acceptance/durability is not undone if a later derived write fails. |
|2 Durable derived mutation row | Append/fsync an idempotent index mutation/coverage row keyed to that exact canonical occurrence (or a successfully removed rolled partition); it carries refs/metadata, not a second content body. Save per-stream covered-end/last-record identity and retained-partition catalog in the checkpoint format. |
|3 Derived lookup/root publication | Build/update derived offsets/lookups from durable mutation rows, then atomically publish the validated generation/root and covered-end checkpoint. A root must never point past durable rows. A crash between2/3 completes only the saved suffix; between1/2 indexes only that known canonical suffix. |
| Delete / retention | Existing owner removes an expired rolled partition first, then fsyncs a retention mutation row and publishes retained-set/tombstone/root/generation updates. Persisted retained-partition catalog allows detection even if crash occurred after unlink but before the mutation row; a later sweep deleting zero files does not hide that gap. Missing current partition, changed identity or unknown re-pointed file is inconsistency, not rolled expiry. |

**Exact bound:64 recovery items per addressed session attempt**, combining (a) complete archive records after saved covered end, (b) complete disposition records after their saved end, and (c) newly confirmed absent rolled-partition facts. Each unique unpublished mutation counts **once**, whether canonical-only or already durable in the derived row log but not yet in the published checkpoint; its durable row is reused idempotently, never charged twice or treated as a free unbounded row-log replay. Capture the observed ends/catalog before preflight; do not chase a moving live tail. Validate the last saved anchor with at most one bounded checked read, then read **only the suffix** plus one overflow probe (the65th item) to detect excess. The existing per-record admission bound limits scratch; no whole-prefix scan, ordinal recount or missing-index inference. Referenced earlier projection facts resolve through already valid index metadata, not a lifetime content walk.

| Outcome / synchronization | Required behavior |
|---|---|
|0 pending | No new writes, no error or UI status; read continues. |
|1..64 consistent items | Validate the whole candidate delta first against the saved generation/addresses/IDs/framing/expected predecessor state and actual retained catalog. Apply the shared Go reducer once, fsync missing mutation rows/derived state and publish one coherent new checkpoint. Replay on interruption deduplicates by canonical occurrence. No earlier public token is renumbered. |
|65+ items | Stop after the overflow probe, publish **none** of this candidate delta as a history-ready root and return the fixed D11 history-unavailable error. Do not make repeated64-item batches silently walk a lifetime. Ordinary later Retry against the same gap still refuses until repair. |
| Inconsistent / missing baseline | Missing or incompatible checkpoint, changed/truncated saved prefix, framed corruption, unreadable required state or re-pointed address refuses without trimming canonical bytes or resetting to0. A provably torn **unpublished derived** tail can be discarded and redone within64; canonical corruption/torn content is not silently truncated by recovery. |
| Delete gap | Compare the checkpoint's retained-partition catalog to current actual rolled partitions, including absence discovered at read/open. Up to64 missing facts are reconciled even when the current sweep reports removed_count=0; tombstone/generation publication is idempotent. Busy-but-present Windows files are not absent. |
| Concurrent activity | A per-session recovery owner coalesces simultaneous attempts. Capture/validate outside long content-lock holds; publication checks the original root/ends. Never hold a session shard/append mutex over slow suffix content I/O. Writes beyond the captured cut remain the next suffix and are not claimed covered; no wrong-generation publish. |
| New append after fault | If a valid prefix exists, attempt the same bounded catch-up before extending its index; if refusal persists, canonical append still follows existing save/control rules and history remains unavailable. If no baseline exists, leave all index coverage uninitialized/unadvanced: never make only the newest send appear to be the whole history. |

Invocation points are startup initialization of existing **indexed** sessions after the established saved-chat cutover, first page/open/attach, append before extending a stale known index and sweep/read detection of a delete/index gap. There is no generic startup scan/build of unindexed old content and no preparing-history product state. Any larger or inconsistent case uses the visible error and D13 operator command; canonical sends and authorized live control access remain available per D7a.

### D2 — Stable same-session occurrence fingerprints

Keep `hp1_` plus an unpadded base64url SHA-256 digest of a length-delimited tuple: canonical session ID, stable internal partition key, encoded record-start offset and recorded entry ID. The public token is exactly47 ASCII characters. It is a selector, not authorization; raw/reversibly encoded disk marks are forbidden. A private index binds it to a checked occurrence and its private acceptance-order rank. Hash collision/duplicate binding is an integrity error, never address substitution. Do not sort opaque token strings. For command/product agreement, the hash preimage is four UTF-8 strings in that order, each prefixed by its unsigned64-bit big-endian byte length; offset is its canonical nonnegative decimal string (no leading zeroes except0), and an absent malformed historical ID contributes an empty string. Encode the digest canonically with unpadded URL-safe base64. Freeze independently derived position vectors in T02/T25; do not derive their expected values by calling the product encoder.

Appends, clear, restart and an offline reconstruction of **unchanged final canonical bytes** preserve the token. A conversion/rewrite that changes encoded offsets is outside that promise: C5's historical conversion must be completed **before** creating indexes/tokens. The utility refuses unconverted/inconsistent input; it does not rewrite it. No old message ID, timestamp, array index, live seq or model-slot ordinal is substituted.

On retention, keep content-free position/order tombstones so a previously issued boundary can remain an exclusive/inclusive range selector even when that occurrence's body expired. It cannot return expired content or authorize another session. Each tombstone contains **only the opaque position token and its private acceptance-order rank**. No entry ID, timestamp, size, agent/participant/call ID, result/body or physical address survives in that tombstone. Format/version and session ownership are index-container metadata, not per-tombstone copied provenance. Disk metadata growth proportional to lifetime expired occurrences is an explicit accepted design cost for stable old boundary selectors; there is no lifetime in-memory tombstone map. Session deletion removes it. Expired lookup/projection fragments are retired rather than relabelled as richer tombstones. Unknown/foreign token remains404; a known expired boundary selects actual retained neighbors, often an empty terminal page. Session deletion removes its index/tombstones. D14 defines truthful expiry and damage behavior.

Public Message has readOnly history_position for each paged occurrence; original recorded id is preserved. Existing duplicate-message/tool last-canonical-occurrence display and diagnostics stay. Malformed identity uses a position-stable visible placeholder, not a newly minted persisted identity. Canonical source IDs and source positions remain distinguishable from coalesced display-bubble aliases.

### D3 — Latest, exclusive paging and refresh of an existing window

| Read | Parameters / selection |
|---|---|
| Latest | No boundary/range; limit defaults40. Newest groups since the latest exact clear marker, ascending output. Include the marker only if selection reaches it; do not insert a distant divider across an omitted gap. |
| Older | before=known history position, exclusive; nearest retained predecessors, ascending output. Include/stop at the first encountered clear marker. before that marker is the explicit crossing into its preceding segment. |
| Newer | after=known history position, exclusive; nearest retained successors ascending. Required to reload a far edge evicted while browsing backward. It is not a message-ID jump or a live seq. |
| Existing-window refresh | Paired window_start/window_end positions bound the **closed already-displayed range** in private acceptance order; optional before pages backward within it. No after in this mode. Used only to revalidate a retained reader after a snapshot/expiry, never centered on a message or navigated to by a route. Select at most100 source groups/request; stop at the saved lower bound. **Every refresh response obeys the same4-MiB soft budget and min_rows/whole-group precedence** as ordinary pages. Continue within the saved interval using the returned next_before until its lower bound/terminal; byte stops can therefore require more than five requests. Only the prior≤500 source groups are materialized in total, with each physical request bounded; this is not a five-request guarantee or a walk from the live tail. Known expired endpoints remain order boundaries, so remaining in-range groups can be returned without an expired-ID search. |

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
| Identical carrier sizing | D4b's **one gateway-owned projector/weigher**, passed through the same request factory by REST messages/detail and WS snapshot, meters the larger of generated public REST history bytes and historical WS bytes for the same candidate cut/context. Session code only invokes the typed read-only weigher contract; it imports no gateway and performs no WS-size estimate. Exclude diagnostic accounting fields from their own meter, transport headers and separately governed live controls/projection. Reuse prepared bytes for final emit; actual transferred bytes are measured separately. |
| At least one whole group | If an eligible group exists, include it whole even when it alone exceeds4 MiB or the row target. Never split/drop its owner/start/result or required public rendering dependencies. If that one group does not fill the floor, keep taking nearest whole groups until the floor is met or content/clear/range/source cap ends. |
| Byte stop allowed only after floor | Once returned estimated row weight≥min_rows, stop **before** the next group would push the metered page above4 MiB, or stop now if the floor already required exceeding the budget. Before meeting the floor, a byte-budget stop is forbidden. A final group needed for the floor stays whole even across a row-target boundary; log atomic/floor override rather than truncate. |
| Other stop conditions | True eligible end, protected clear boundary, closed-range end and100 source groups may stop before the raw two-screen estimate. Bytes must not shorten that response further. When row_target/source_cap and byte threshold coincide, report row_target/source_cap (and any byte override separately), not a byte_budget cut of a capped chunk. Do not invent history or automatically cross a closed clear just to fill a floor. |
| Raw floor above the request cap | If ceil(2H/A)>100, keep request limit≤100 and min_rows=limit. The client treats consecutive contiguous responses as one logical fill cycle and continues until new rendered content covers2H (opening4H), terminal/closed clear or visible failure. The source/row request cap—not a byte stop—explains a smaller physical response. No hidden gap may be shown between those chunks. |
| Actual height check | After layout, measure newly added content height excluding loader/hint/Retry. Set remaining=max(0,goal_height−measured_added_height), with goal4H for opening/2H for prefetch; request the next contiguous top-up with limit=clamp(ceil(remaining/A),20,100) and min_rows=min(limit,max(1,ceil(2H/A))) using updated A. Re-evaluate after every response; do not blindly add another full2H when only a small remainder is needed. Original20-row minimum/atomic groups can overshoot an approximate target. Preserve anchor on each commit, never reattach or jump bottom for a prefetch. A failure stops top-ups visibly, never a silently shortened success. |
| Page accounting | page_info reports soft_byte_budget (current server constant4194304, schema integer≥1 rather than enum), projected_bytes (meter above), min_rows, byte_budget_exceeded boolean, stop_reason and optional budget_override (height_floor/atomic_group). stop_reason distinguishes byte_budget from row_target/source_cap/clear/end/range_end. Any exceedance has its reason; bytes alone never move cursors or hide a group. |

Budget and floor apply to each physical page;≈4-screen open/2-screen prefetch are measured logical fill goals. Ordinary pages under budget keep their original requested content amounts. Source exhaustion/clear barriers are truthful exceptions to available coverage, not a license for a budget to leave a reader with less than two screens. Large-output floor overrides are explicit tested outcomes, not an unresolved question.

**Floor trust boundary (MIN-201):** min_rows is an advisory claim from an authenticated client, not server proof of viewport geometry. A caller can set min_rows=limit=100 and force the height-floor override on very large groups. The soft budget is **not** an authenticated-caller denial-of-service defense; retain the founder-selected floor/atomicity rules and expose actual override bytes rather than pretending otherwise. The SPA excludes invalid samples but an erroneous1px average still needs deterministic accounting and measured top-up tests. No new geometry restriction or byte-hard-cap decision is reopened.

### D4b — One injected gateway weigher, no layering cycle

**Chosen seam/home:** a single prepared-public-history projector/weigher in proposed `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/history_page_projection.go`, owned by backend-lead. Existing C6 Message/verdict adaptation and C8 per-entry generated replay construction are refactored into/used by this one seam, not copied into session or the repair command. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/` defines only an internal `weigh` dependency contract over a projected core group/read-cut/context and the resulting nonnegative encoded weights; this is not a public wire type.

```
REST messages / detail ─┐
                       ├─ gateway history-page request factory
WS snapshot ───────────┘      ├─ one read-only prepared projector/weigher + memo
                             └─ UnifiedStore page selector(read cut, options, weigh)
                                    └─ same core groups/dependencies → same weigh
                       → chosen range + retained prepared bytes
                       → generated REST response OR existing WS replay emit
Go repair command → common session index/reducer engine only (no gateway import)
```

The gateway factory supplies the **same implementation and options** to both carriers, including the same exact public generated encodings, special/tool/media/card handling, context and larger-of-REST/WS formula. The store never guesses WS bytes from REST, never imports gateway and has no hidden default callback. Weighing is side-effect-free with respect to publishing: no send, hub numbering, card mutation, audit/action execution, durable result write, notification or lifecycle change. Required read-only reference/preview resolution is done once for the cut using the existing adapters. Memoize prepared group encodings and incremental weights/context so each content group is projected once, not dry-run replayed again on every prefix or rebuilt for final send; discard the one unselected lookahead/memo on cancellation/failure.

The final response/replay reuses the selected prepared generated data/bytes. Session state/active projection/held live frames remain separate normal attach output, not charged as historical content. A missing, failed, overflowing, negative or otherwise inconsistent weigh result fails the page with a sanitized projection error; **no fallback estimate, minimum0 or full-read retry**. WS may retain authorized controls through D7a live-only completion but must not claim a successful history snapshot. T12/T27/T32 compare REST/snapshot exact selected positions/flags/meter on a tool-heavy versus special-frame-heavy fixture with maximally differing carrier sizes; a deliberately wrong weigh result fails visibly. The command reuses session's canonical projection/index logic, not this gateway-only wire/render meter.

### D5 — Pixel trigger, running measurements and virtual estimates

Server owns strict input validation/default40 and4 MiB soft budget. Client owns the visible **chat scroller** height H, not window/monitor height. Before the first attach, if committed H/A are available, request history_limit=clamp(ceil(4H/A),20,100) and history_min_rows=min(history_limit,max(1,ceil(2H/A))); otherwise omit both and use40/20 (four/two800px screens at80px). REST requests carry the corresponding limit/min_rows. No second attach merely to correct sizing. Byte-limited opening responses are followed by contiguous normal older top-ups to≈4H; a prefetch fill cycle acquires≈2H **new** content. Floor overrides are applied server-side and actual measured height is checked client-side; a4 MiB ceiling cannot silently redefine open as two screens or prefetch as less than two. Clear/end/error and no-invisible-history-loop rules remain truthful exceptions.

| Quantity / trigger | Definition |
|---|---|
| Initial A | 80 CSS pixels until measured, never a permanent estimate. |
| Average | Latest100 distinct drawn-row samples in the active width/presentation generation. A=max(1,sum(height)/count); re-observation replaces a sample. Exclude loaders/hints/Retry/hidden/zero/nonfinite/live-changing bubble. Reset on session/major width/Verbose change. |
| Inline tool rows | Count and measure actual tool rows even inside an outer message; do not double-count the wrapper and children. Include spacing consistently. |
| Unmeasured virtual size | **estimateSize follows current A**, not a fixed80. For a composite outer virtual item use its known drawn-row count times A, replacing with measured outer height when available; invalidate/recompute estimated prefix measurements when A changes. Otherwise accurate fetch-count math can still trigger too late. |
| Dtop | Distance from viewport top to earliest loaded content boundary, including actual prefix controls/spacers, using the live measurement model for unmounted items. Verify it through the real virtualizer, not only injected numbers. |
| Automatic older trigger / fill owner | **One per-view/per-direction fill owner**, not separate opening and scroll effects. Until first≈4H coverage is reached, the ordinary older trigger and byte-cut top-up feed that same owner's opening goal4H; afterward a user threshold requests goal2H new content. Requests for the same boundary join the in-flight chain. A byte-cut opening at≈2H may satisfy Dtop≤2H, but that observer event cannot start a second chain. Refetch/error/closed-clear/terminal guards and hidden/H≤0 behavior apply to this same owner. |
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
| First open or explicit following-latest | Stage the selected recent page; preserve pending/failed input and current control state; commit historical rows once and settle the opening at bottom only after complete snapshot/projection application. A history-only pre-replay failure causes no history wipe/history-success; if current controls can be delivered, use D7a live_only, otherwise retain the failed-attach/no-complete behavior. |
| Historical reading, including 499/500-group edges | Keep the reader's actual source rows, coalescing aliases, opened-clear boundaries, retained interval endpoints, current measured sizes, focused element, first visible anchor and intra-row pixel offset. Mark the transport/snapshot generation new and abort old page reads, **not** the reader window. Stage the fresh latest page into the existing100-group tail cache separately; refresh current approvals/questions/secrets/goal/turn projection from authoritative session state. Do not put the distant tail below old rows. |
| After successful complete in historical mode | Live cursor can commit W because live catch-up completed; this is independent of reader revalidation. Issue window_start/window_end refresh pages against exactly the kept reader interval, newest-to-oldest, bounded to the prior≤500 groups and100/request. Stage refreshed public projections, then merge by occurrence; current/live terminal values win. Preserve the anchor before paint after every necessary layout. No tail-to-anchor walk, no around-message loader and no new UI navigation destination. |
| Window refresh in progress / failed | Keep all readable cached rows/anchor/focus with a small status “Refreshing loaded messages…” and aria-busy on that region. Failure uses “Could not refresh loaded messages.” + Retry, retaining cached rows with an explicit out-of-date notice. Retry repeats this range, not attach or a user send. Current live controls remain usable; page-prefetch pauses until the range is reconciled or explicitly left. |
| Some rows/anchor expired | Remove proven-expired rows after refresh. If the original anchor survives, keep it; otherwise use the nearest later surviving row in the **old range** (or nearest earlier if none), preserving its pre-refresh screen offset where possible. Announce “Some loaded messages have expired.” No forced bottom jump. Source positions/retention generation decide, not timestamp guesses. |
| Entire held range expired | Show “These loaded messages have expired.” in place of that old range, keep current controls and a focused/keyboard-reachable Return to latest action. Do not claim the whole session is empty and do not switch to latest without that action. |

A new clear intentionally sent/applied by this viewer is D6's explicit view replacement, not a passive reconnect. Routine incremental reconnect keeps the reader and applies journal updates without a bottom jump. Interrupted replay/socket/control delivery retains prior cursor/view, rejects staged partial history and uses existing replay-error handling; a classified history-only pre-replay failure takes D7a only if its separate live/control completion genuinely succeeds. Revalidation failure **after a successful catch_up_complete** does not roll back the successfully caught-up live cursor or simulate a second attach failure.

**Interim composition — now source-verified:** branch origin/fix/chat-replay-render-once at `da4b75df28c8a5bde751152659531e89b00062ad`, implementation commit `a0798ba02c7dd348c4f4949310493d9152156656`. Read `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/lib/attach-replay.ts`::AttachReplayBuffer and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/lib/ws.ts`::WsConnection from that Git object: begin on a successfully sent attach; per-session outstanding requests and serialized request order; release buffered validated frames on **catch_up_complete, not replay done**; strip incomplete snapshot rows on replay_error/refusal; clear on disconnect/socket generation. ChatScreen::VirtualizerAutoFollow tracks a known bottom pin while yielding to actual upward wheel/key/pointer intent and rechecks at paint. Read its unit/transport and real-browser regression sources; no execution is claimed. PRE-RED-1's source acquisition is complete for this revision; team-lead must re-read any newer/landed revision before overlapping RED/GREEN and preserve its tests. Work still does not wait for landing.

When it lands, catch up and re-read the landed version. Preserve one bounded historical batch commit, not a second buffer. Replay done and catch_up_complete remain different: projection can follow done, so final opening readiness waits for successful complete/application. A live done never masquerades as attach completion. Paging supersedes full-history input, not render-once; historical-mode snapshots use its staged tail result without invoking its first-open bottom action on the old reader.

### D7a — Authorized attach with unavailable history still exposes live controls

This is an explicit **new degradation branch**, not the current C7::failCatchUp unbind/discard behavior. First apply existing authentication/session eligibility and parameter checks; unauthorized/missing sessions get no live-only bypass. For an authorized session whose baseline is missing, over the64 bound or inconsistent, do not send session_snapshot/wipe, fabricated history rows, generic turn ErrorFrame or replay done that could mark the agent stopped.

| Wire/state step | Required meaning |
|---|---|
| Bind/hold and cut | Keep the connection bound; capture W and the same active projection as normal attach. A history error is not a turn error and does not cancel, pause, clear or relaunch work. |
| Session state | Send existing generated SessionStateFrame with current approvals/questions/secrets/active_turn/attention facts and new generated `history_state={status:unavailable,reason,message}`. Reasons are index_missing/index_inconsistent/catchup_limit/storage_error/projection_error; message is sanitized fixed copy. Successful full history paths instead send history_state.status=available. Absence is not successful empty history. |
| Projection and complete | Deliver current uncovered active projection to the live portion, then **CatchUpCompleteFrame.mode=live_only** at W, then release held seq>W frames. This mode completes only live/control catch-up and commits the live cursor; it never claims history reconstruction, clears the history error or writes the attention seen mark. This branch is not labelled a successful snapshot. A later servable incremental journal catch-up may succeed for live frames while history_state/error stays unavailable; it is still not history-loaded proof. |
| SPA | Leave any cached historical rows explicitly out-of-date, or show persistent catalogued ErrorState/Retry in the **history area only**. Show current Stop/composer/approval/question controls outside that failure slot; clear replay/attach-only send lock on live_only complete. Distinguish any visible new output as “Live output — earlier history unavailable”, not a gap-free full history. Old source rows/cards are not invented or reactivated. |
| New sends | Existing canonical save/admission/control rules apply; first-send receipt truth stays tied to durable canonical save. Unindexed/mismatched session never creates or advances a partial index from this send. Its live echo/output may render with the explicit unavailable-history boundary; Stop and tool approvals remain actionable. |
| Retry | Explicit history Retry sends attach without since_seq (normal snapshot request), keeps current controls/pending input, and attempts D1a once. A repeated refusal returns live_only again; no repair poll storm, no resend and no automatic lifetime construction. After operator repair or valid≤64 reconciliation, successful snapshot replaces/refreshes history normally per D7 and clears only the matching history error. |
| Failed live/control delivery | If session state/projection/socket cannot be delivered, use existing connection/attach failure handling and retain truthful cached state; never emit live_only complete over an incomplete control catch-up. No successful cursor/seen acknowledgement on that path. |

REST messages still returns the fixed500 for unavailable history. **Session detail remains usable for routing/metadata**: return a generated explicit metadata-only SessionDetail with session, agent_removed? and required history_state.status=unavailable, **omit messages/page_info/continuation flags**. It is not an empty array or a claim that a partial transcript loaded. On available detail require messages and full page metadata. The generated schema plus runtime semantic checks enforce those mutually exclusive field sets. This lets the existing session route and workspace pointer resolver identify/attach the authorized session rather than replacing the entire screen with a metadata-loader failure. All consumers must inspect history_state before recording flags or applying rows (D15).

**Client flag separation:** SessionState.history_state describes server history readiness, not proof that the client loaded a page. live_only clears awaitingCatchUp/isReplaying for live controls but does not set replayCompletedForSession, run a history-based unanswered/restart sweep, confirm an unproved first send or clear history_error. A later incremental attach may catch up live frames while history stays unavailable; only an actual successful history snapshot/page clears that error. History Retry keeps a live_controls_ready path so it does not hide Stop or disable already authorized current interactions while waiting.

### D8 — Scroll ownership, focus and catalogued UI state

Stable occurrence/display-group keys replace index-key assumptions. Track current visible anchor + intra-row offset through every committed prepend/control layout and genuine reader movement. Apply **signed anchor content-offset delta** against the current scroll position; total scrollHeight includes live growth below and is not an anchor oracle. Remeasure images/diagrams/fonts/tool expansion and composite virtual rows before paint/after observer settlement. One coordinator owns compensation; `overflow-anchor:none` avoids native double correction; virtualizer/assistant-ui bottom-follow is suppressed during historical preservation.

| State / job | Exact component / focus and announcement contract |
|---|---|
| Authorized history unavailable / live controls ready | Persistent ErrorState in history area with fixed unavailable copy/Retry and optional explicitly labelled live-output tail. Composer, Stop, current question/approval/secret interactions stay in their ordinary independent owners. Retry never posts an old send or sets a turn-ending error. A metadata-only detail is not welcome/empty history; keep cached reader as out-of-date where present. |
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
| Same OpenAPI::getSession; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionDetail.yaml` | Metadata/agent_removed unchanged; required history_state explicitly distinguishes available history from metadata-only unavailable detail. Available: messages is bounded latest with canonical page_info/has_earlier/has_later/next_before/next_after. Unavailable: keep authorized session/agent_removed metadata, omit messages and every page/continuation field. Generated shape and producer/consumer semantic checks reject wrong combinations; never manufacture messages:[]. Replace has_earlier_messages's clear-only meaning atomically. Detail owns no paging selectors. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionHistoryResponse.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionHistoryPageInfo.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionHistoryContext.yaml` | Shared envelope/metadata/context below. No SessionHistoryEntryRef manifest schema, frame-count ranges or duplicate REST identity list. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/HistoryEntryOrigin.yaml` | Shared generated local ownership {position,entry_id?}, referenced by entry-derived replay frames and their inline AsyncAPI mirrors. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/Message.yaml` | readOnly history_position; terminal_outcome parity; preserve all public special payloads including room_ref. Existing provider_fallback enum gap versus C8 is recorded for team-lead's explicit page-parity disposition, not a hidden placeholder or side fix. No private envelope export. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/AttachSessionFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/asyncapi.yaml`::AttachSessionFrame | Optional history_limit20..100/default40 and history_min_rows1..effective-limit/default20, snapshot-only. Same access-before-limit-then-floor-before-bind precedence. since_seq/boot_id/ack unchanged; neither field cuts a servable incremental journal. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionSnapshotFrame.yaml`; same AsyncAPI::SessionSnapshotFrame | Recent-page replay metadata/context, canonical continuation and mode; no second entries array or manifest. Retain unrelated secret/control fields and seq/reason semantics; historical reader preservation is a client state policy, not an omitted live catch-up. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/CatchUpCompleteFrame.yaml`; same AsyncAPI::CatchUpCompleteFrame | Add mode=live_only beside incremental/snapshot. live_only commits only completed controls/current projection/live cursor, never reconstructed history or attention seen. Snapshot completion still means selected-page replay plus projection, not lifetime content. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionHistoryState.yaml` (new); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/contracts/components/schemas/SessionStateFrame.yaml` and AsyncAPI::SessionStateFrame | New shared generated history_state: required status enum available/unavailable; unavailable requires reason enum index_missing/index_inconsistent/catchup_limit/storage_error/projection_error and fixed sanitized message; available omits reason/message. Required on new SessionDetail and on attach's session-scoped SessionStateFrame (not unscoped connection-open state). User-visible error does not travel through a turn-ending generic ErrorFrame. |

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
| SessionHistoryPageInfo | Required mode enum latest/older/newer/window_refresh; limit integer20..100; min_rows integer1..limit; row_count/source_group_count nonnegative; soft_byte_budget positive integer (minimum1, **not a one-value enum**); projected_bytes nonnegative; byte_budget_exceeded boolean; stop_reason enum row_target/source_cap/clear/end/range_end/byte_budget; retention_generation opaque nonempty string changed on actual expiry; context. Optional budget_override enum height_floor/atomic_group iff byte_budget_exceeded; first_position/last_position iff nonempty; optional stopped_at_clear and latest_clear {entry_id,history_position}. All positions47; no null. Empty/true terminal/closed barrier can underfill; a byte_budget stop cannot underfill min_rows. |
| HistoryEntryOrigin | Required position string47; optional entry_id (recorded nonempty string). Optional on general frame schemas, semantically required for every entry-derived paged replay by D4's tested producer invariant. No session authorization or private address encoded in a parallel field. |
| SessionHistoryContext | Required non-null spans and duplicate_ids arrays restricted to selected/current relevant dependencies. Span item: span_id, actual existing generated start/state/message/end when recorded, has_run only when proven. Duplicate item: kind message/tool, id, canonical_position, occurrence_count≥2. No all-session span load or competing cancel/disposition values. |

Objects are closed; use generated payloads, omit optional absence rather than null, and test semantic cross-field rules that structural generation cannot express. REST/detail/snapshot reuse these shapes; only snapshot uses per-frame ownership. Add the shared schemas → reference in OpenAPI/AsyncAPI/manual mirrors → run `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/scripts/gen-contracts.sh` → atomically commit canonical/generated Go/TS/Zod/inbound artifacts → implement consumers with generated types only. `make verify-contracts` runs in the approved remote gate. No handwritten replacement wire structs/interfaces or validators bypassing the contract.

### D11 — Access, parser and errors

Use the **actual C15 access model**, not an invented multi-user read policy: REST auth wrapper / WS auth, valid session ID, main eligibility, same addressed store/session existence. Resolve every position/range only within that authorized session; knowing a token grants no parent/root/other-session read. Existing child sessions page their own archive.

**Both carriers' order:** authenticate → path/session-ID syntax → addressed-session eligibility/existence/access → limit/selector syntax and combinations → same-session position/range lookup → index/page read. Thus an inaccessible main with an invalid limit gets the existing access refusal; no WS/REST disagreement. Bad attach history_limit is checked after resolveSession-style access and **before bind/wipe**, with no successful complete. Keep inbound validation on/off parity.

| Authorized-session outcome | Exact behavior |
|---|---|
| Malformed percent-encoding in an unrecognized raw query key or in an unrecognized parameter's value (for example `bad%ZZkey=x` or `unused=%ZZ`) |400 ErrorResponse.error=`invalid history query`. After addressed-session access, reject this raw-query decoding failure before typed paging validation; never silently discard the pair. Decodable recognized keys retain their existing field-specific value errors; well-formed unrecognized parameters do not enable a new mode. B19/T11 cover both malformed-key and malformed-value cases with inbound validation on and off. |
| Omitted limit |40, never unlimited. |
| Duplicate/malformed/empty/null-like/signed/fractional/exponent/whitespace/overflow/out-of20..100 limit |400 ErrorResponse.error=`limit must be an integer from 20 to 100`. Digits-only leading zeroes permitted. |
| Invalid min_rows after valid effective limit |400 `min_rows must be an integer from 1 to limit`. Omitted uses20; a supplied value must be one digits-only decimal integer1..effective-limit, with duplicate/malformed/null/overflow refused. Validate limit before min_rows, after access. |
| Invalid/duplicate/blank/malformed-percent/non47-character/wrong-version token for before/after/window boundary |400 `invalid history position`; no trimming/omission. |
| before+after, unpaired/reversed range endpoints, after with a range or before outside it |400 `invalid history page range`. Unknown/foreign well-formed tokens are404 first; order is compared only after same-session resolution. |
| Unknown/foreign position |404 `history position not found`, no foreign lookup/body/reset-to-latest. A **known expired** position remains a bound under D2/D14 and need not fail. |
| Session missing/hidden/deleted | Existing404 `session not found` before paging lookup; retain established auth errors. |
| Required index unavailable after D1a check | Messages endpoint500 ErrorResponse.error=`Session history is unavailable. Retry after the operator restores its index.` This applies to missing baseline, corrupt/inconsistent state or65+ delta; a consistent≤64 suffix heals once before deciding availability. No private details, lifetime build, preparing UI,503 or repair poll. Session detail returns its explicit metadata-only unavailable variant, never a successful empty history. UI ErrorState/Retry is confined to history; live controls/send/Stop remain D7a-reachable. |
| Other required storage/projection failure | Existing C6 redacted session-storage error500, preserving current view. |
| Bad attach limit/floor after session access | Existing session-scoped ErrorFrame message=`history_limit must be an integer from 20 to 100` or, after valid limit, `history_min_rows must be an integer from 1 to history_limit`; no bind/wipe/catch_up_complete. Null/fraction/overflow/wrong type refused, both validation modes. |
| Authorized attach history/index unavailable | D7a: keep live binding, send typed session-scoped history_state=unavailable in current session_state, current projection, then catch_up_complete{mode:live_only} and held live tail. No history wipe, generic turn-ending error/replay done, successful snapshot/history flag or attention ack. Invalid auth/session/parameters never take this bypass; ordinary incomplete replay/socket failure still cannot claim successful complete. |

Write a **chat-specific query parser in this feature**. It inspects raw percent decoding/multiplicity without silently dropping malformed pairs, with explicit tests for malformed key/value and both validation modes. No dependency on or refactor of the unlanded room handler/parser. The room precedent is evidence for pitfalls, not a required import. Unknown keys do not enable a new mode; no ID-based loader is implemented. Per-item public corruption keeps the existing stable placeholder; page metadata contradictions fail visibly.

### D12 — Build now, catch up as each overlap lands

R1-Q1=B replaces the initial wait-first plan. Start on the current release/v0.1.1-based feature branch; spec approval/RED/GREEN do **not** wait for Train3 or interim landing. No cherry-pick of the large unlanded train or anchor correction without its owning lineage. C21/D7 now records the newly available interim source/read at da4b75df2, superseding round1's absence report. Team-lead owns PRE-RED-1's latest-revision handoff/re-read and conflict planning; do not confuse reading the artifact with its landing or executed-test evidence.

As **each** overlap lands, catch the feature up with its landing destination and re-read the changed seams: archive TranscriptEntry/room_ref, replay buildEntryMessage, public/generated contracts, ChatScreen/state and regression tests. Team-lead chooses the repository-approved merge/rebase mechanics for an already published feature branch; no force push is authorized here. Build work can continue if one base has not landed, but team-lead holds this feature's integration landing until the founder's landing order is met. If GREEN is ready first, report the pending landing order to the chief/founder; do not invent a wait timeout or bypass it.

| Stage | Owner / dependency |
|---|---|
| PRE-RED-1 | Artifact acquisition/read completed in this correction at C21's da4b75df2. Team-lead confirms/re-reads the latest artifact/tests **before RED**, recording SHA/symbols and any change since that read. Do not require it to land first or claim a source read means its tests ran. |
| Source/contracts | Backend-lead writes this feature's parser and generated contract slice on current base. |
| RED | QA authors independent backend/frontend/standalone-Go-command tests from the corrected spec; tests-only CI proves intended failures. D13 names command/native-Windows execution, not assumed discovery. |
| GREEN | Backend-lead: metadata/read cut/expiry/projection/contracts/parser/diagnostics/offline Go command. Frontend-lead: generated adapters/state, reconnect preservation, sizing/anchor/focus/recovery, docs. |
| Each landing | Team-lead catches up/reconciles overlapping contracts/replay/room_ref and reruns affected gates. |
| Final gate/landing | This final round2 correction follows both grills/interviews; no third grill. Draft awaits founder approval, then planning/RED/GREEN/CHECK and independent five-reviewer/docs/platform/exact-commit UAT/validator gates before founder-authorized landing in the agreed order. Any genuine remaining blocker over this authored design escalates to the founder, never self-adjudicated. |

Deliberate room differences remain: chat's opaque positions versus room seq; has_earlier versus has_older; measured20..100/default40 versus room1..1000/default100; two-screen pixel threshold versus room24px; virtual/image-aware scroll correction versus plain DOM list; indexed disk projection versus memory slice. Exclusive/ascending/actual-continuation semantics and corrected signed anchoring are reused. Neither live cursor is changed by older paging. No shared-parser extraction is required by this feature.

### D13 — Separately built Go repair command, one product implementation

**Proposed command source:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/cmd/chat-history-index-repair/main.go`; tests in that same package. Build output is proposed `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/build/tools/chat-history-index-repair` (native executable suffix on Windows). This is a separate operator-built pure-Go command, **not shipped in the Omnipus product binary or installers**, not a new product CLI verb, startup worker or REST action. Backend-lead adds the build recipe; no build or tool execution occurs in this spec-only correction.

**One authority:** it links and invokes **the same pkg/session chat-index constructor/reducer/projection/codecs as the product**, including effects/retracts, disposition, cancellation, duplicate ownership, stable positions and retention metadata. It may perform full retained-archive construction offline, while the runtime uses the same engine only for new writes and D1a bounded suffix mode. It does not translate these rules into a second language, import/boot gateway or agent, invoke model/providers or call a normal constructor that can mutate saved-chat formats. Gateway's D4b wire weigher is not needed for index construction.

| Command phase | Inputs / effects / outcome |
|---|---|
| `dry-run` | Required absolute --data-root; no backup/stop input needed. **No prior backup or stop receipt required** for this initial read-only preflight. Inspect current canonical format first, use the shared constructor in no-publish mode and print deterministic per-session plan/refusals. Default stdout-only invocation writes nothing: no source/index/temp/lock/log write, chmod or mtime/content change. Explicit optional --plan-output/--scratch-root may write only their designated out-of-tree locations under the rule below, never source/backup. Filesystem read-access timestamp changes are outside zero-tool-write guarantees. A running-instance plan is advisory; source drift invalidates it before apply. |
| `manifest` | Explicit --data-root, --backup-root and --output absolute manifest file outside the source root. Producer inventories the **actual complete backup copy**, verifies its bytes against the named source snapshot and writes the manifest schema below atomically. It does not make a partial sessions-only copy look like a full-instance backup. Backup contents are read-only except this designated manifest output; no source mutation. The manifest is outside source and its own output is excluded from self-inventory; its producer is not part of apply. A live source may drift later: final post-stop equality is still mandatory. |
| `stopped-receipt` | After team-lead actually stops the instance, inspect the identified process/service and write a version1 stopped receipt outside source/backup. Required fields: canonical data_root, instance_id, previous_pid, checked_at (UTC RFC3339), inspection_method=native_process, operator and tool_commit. Unknown/running refuses; this command never stops the gateway or infers quiescence from a flag alone. The same command supplies/validates the private receipt format. |
| `apply` | Explicit --data-root, --backup-root, --backup-manifest, --stopped-receipt and --confirm-stopped. Validate **all sessions' format/preconditions/integrity before publishing any new generation**. Verify the backup and stopped-source equality, then shared-Go construction and per-session atomic index publication only. No canonical record/meta/model-window/config/credential/upload/backup write, normalization, repartition or legacy conversion. |
| `verify` | Explicit roots/manifest; check published derived generations with the real Go reader and independently compare canonical/backup integrity. Read-only; not a hidden repair or a claim that first UI acceptance ran. |
| Stopped evidence | Apply requires the recorded instance identity/source root/process/service/stop-time evidence and a live process check. A running or unknown/uncheckable instance refuses. A receipt flag alone is not proof or a new cross-process gateway lock. Recheck source identity/fingerprints before each publication and at completion; race refuses. |
| Idempotence/interruption | A matching completed index is byte/mtime unchanged. Redo only the command's designated incomplete derived staging; keep an old valid generation until the replacement is complete. Report per-session publications on batch interruption; rerun skips completed matching sessions. No duplicate canonical entries, new identities or public-token renumbering. Full-instance batch atomicity is not promised. |

Read-only construction may use only an explicitly supplied --scratch-root **outside** source/backup for disposable bounded disk scratch; no guessed live-tree temp files. Optional plan/receipt/log outputs are outside those trees. Apply's designated derived-index staging is the only source-tree write exception. All modes preserve canonical file bytes/mode/mtime; directory mtimes may change only because a permitted derived subtree is created, and never enter canonical equality as noted below.

**Explicit build recipe (target, not executed here):** from the stated worktree, backend-lead's new build target runs CGO_ENABLED=0 Go build with tags goolm,stdjson on `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/cmd/chat-history-index-repair`, outputting the native command under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/build/tools/`. The command's Go package/tests are separate from product candidate/release artifacts. No gateway import/SPA embed or Python prerequisite is introduced.

#### Backup manifest: schema, producer and verification

**Proposed schema:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/cmd/chat-history-index-repair/backup-manifest.schema.json`, shipped with the command source/embedded for validation. This is a **private operator-file format, never read by the SPA or sent over the gateway**; it is not a parallel REST contract. The Go manifest producer and decoder use this single versioned format, reject unknown fields and have schema/positive/negative fixture parity tests.

| JSON member | Required shape / invariant |
|---|---|
| schema_version | Integer1; unknown version is precondition refusal, not guessed compatible input. |
| source_root / backup_root | Canonical absolute native paths; exact source identity, backup outside source and not a prefix/alias of it. No guessed home or path from a session record. |
| created_at / tool_commit | RFC3339 UTC timestamp and actual built tool commit identity; not proof of backup by themselves. |
| entries | Required non-null array sorted by normalized relative UTF-8 path bytes. Each source/backup directory/file is accounted for, including config, encrypted credentials, uploads, archives/dispositions/meta and any preexisting indexes. No absolute paths, empty/dot/dotdot segments, duplicate resolved names, path traversal or platform case-collision. Only regular file/directory kinds are accepted in v1; symlinks, sockets/devices and escape aliases refuse for explicit operator handling, never silent omission. |
| entries[].path / kind | path as above; kind enum file/directory. Directory size0 and no digest. |
| entries[].size / sha256 | File size nonnegative integer, sha256 exactly64 lowercase hex of actual bytes. Zero-byte files require the real empty digest, not missing/null. Read/size/hash failure fails verification. |
| entries[].mtime_unix_ns | Canonical signed-decimal **string** preserving file mtime without floating-point precision loss; read mtime from source and preserve in the full backup. Do not use access time as source drift. |
| canonical_fingerprint | Lowercase SHA-256 of the length-prefixed canonical **source** inventory: sorted path, kind, size, digest, and file mtime; directory mtime is encoded as empty because adding a derived subtree legitimately changes its parent directory mtime. Directory names/kinds and every canonical file still compare exactly. Exclude only designated derived chat-index/staging paths from current-source equality and the manifest output itself from backup self-inventory. Scalars are canonical UTF-8 with unsigned64-bit big-endian byte-length prefix; golden vectors freeze this encoding. |

A minimal schema-valid empty-tree test manifest still has entries:[] and actual fingerprints. The manifest generator hashes **backup bytes**, verifies the full source/copy inventory and checks there are no unlisted extra backup files except the manifest output. It does not trust a user-written success boolean. Current-source comparison excludes only known derived index artifacts so the **same verified backup can be reused for an idempotent rerun after only index writes**; the backup's prior index bytes are nevertheless verified against its saved entries. Private contents/digests/paths never enter public history diagnostics. Independent tests build expected JSON and hashes without calling the producer under test.

#### Cutover-first and the operator sequence

**Founder sequence:** build → **dry-run → backup (verified) → stopped instance** → final source/backup equality check → apply → verify → restart/UI acceptance. Dry-run is not blocked on backup/stop; apply always is. Backup may be made before stopping, but verify its copied bytes then recheck equality against the **stopped** canonical source immediately before apply. If any live/shutdown write caused drift, refuse, rerun dry-run and take a fresh verified backup with the instance already stopped; never bypass the check or reuse a stale manifest.

**Unconverted-first guard:** the first read-only dry-run detects legacy/noncanonical saved sessions before backup-dependent apply preconditions and returns per-session class **unconverted, exit2**. Team-lead must first run the compatible product to successful completion of its existing `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/unified_conv.go`::CutoverSavedChatsAtBootWithAgentStores (C23), via the normal boot flow, then repeat authoritative dry-run and create a **fresh post-cutover full backup/manifest**. A pre-cutover backup is preserved as rollback evidence, never used as the post-conversion equality baseline. The repair command does not run that conversion or an agent constructor itself. If cutover fails, stop and report that failure; do not index half-converted bytes or start the repair anyway. Whether the founder's current instance is already converted remains Unknown until this preflight; no extra live-instance probe is run by this author.

| Refusal / aggregate outcome | Exact exit mapping |
|---|---|
| Valid dry-run plan, successful manifest verification, complete/unchanged apply or verify |0. A dry-run success is a plan, not a prepared-live-instance claim. |
| arguments, unsupported_manifest_version, backup_missing/incomplete/tampered/wrong_root, unsafe_path/kind, stopped_evidence_missing/unknown, instance_running, source_changed |2, zero publication after failed preflight; name all affected session/precondition classes. |
| **Per-session unconverted / unsupported canonical format** |**2**, before indexing/backup-dependent apply. Preserve bytes and direct operator to cutover-first sequence. |
| **Per-session corrupt_archive, invalid_reference, index/projection verification failure; filesystem read/write/fsync/permission error** |**1**, explicit integrity/I/O refusal, no silent skipped session. A failed preflight publishes no sessions. |
| Interrupted/publication-failed batch after valid preflight |1 with exact published/unchanged/refused session summary; published sessions remain valid, rerun completes others. |
| Multiple refusal classes | Report each class; any precondition/unconverted class selects2, otherwise integrity/I/O selects1. Never exit0 with refused>0. |

Proposed dry-run output for the frozen three-session fixture (not live measurements):

```text
session=session-a action=create groups=40 partitions=2
session=session-b action=unchanged groups=120 partitions=3
session=session-c action=create groups=0 partitions=0
mode=dry-run sessions=3 create=2 unchanged=1 rebuild=0 refused=0 canonical_writes=0
```

The command prints fixed reason classes/IDs/counts, not bodies/credentials/raw cursor/physical marks. Operator receipts use absolute locations. T22/T25 independently hash source/backup trees and compare Go-reader projection, rather than trusting canonical_writes=0.

#### Required CI execution — backend-lead owns wiring

This is a work deliverable, not a claim that current CI already runs it. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/.github/workflows/build.yml` currently runs one explicitly named candidate Python test; it is **not** discovery for this command. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/scripts/go-test-shards.py` dynamically discovers Go packages, but that alone is not sufficient proof of command/special-platform execution.

Backend-lead adds required job **history-index-repair** to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/.github/workflows/pr.yml`, with native Linux and Windows legs. Named steps: **Build standalone history-index repair command**, **Test repair command and manifest**, and **Test bounded recovery and retention/read-handle semantics**. Build/test use the declared tags and CGO_ENABLED=0; command tests run the **entire new command package** once, while core tests target the new named chat-index recovery/Windows cases. The job must be included in the required aggregate gate, triggered for command/session/schema/CI changes (or run unconditionally), and never silently path-filtered out. Logs assert named tests actually ran and Windows cases did not all skip; zero-match is failure. No added race run in parallel on the local machine.

Mirror the Linux standalone build/command-test step in the Fly **go tier**, sequentially in that tier, through backend-owned `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/deploy/ci-worker/runci.sh` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/deploy/ci-worker/ci-cluster.sh` gate map. Native Windows behavior is certified by the Windows CI leg, not an Ubuntu cross-build. QA records tests-only RED, command build/test exit codes, exact candidate SHA, named-test counts and a planted backup/codec/publication mutation failure before claiming this deliverable tested. No heavy CI/local execution is performed in this spec task.

### D14 — Retention reconciliation and truthful boundaries

Use C18's actual sweep chain; do not claim rolled-partition expiry has no production caller. Extend derived metadata maintenance at the same owner: an actual successfully removed partition becomes absent in the retained-partition set and advances retention_generation; failed removals remain eligible if readable. Keep content-free position/order tombstones, remove content/dependency pointers to gone bodies and recompute first/last/clear/lookahead over **retained** eligible groups. Whole-session deletion removes all derived state. Retention-disabled is unchanged/no generation advance.

The same D1a reconciler compares the **persisted retained-partition catalog** to actual rolled files on open/read and sweep completion, even if remove_count=0 in the current pass. After a crash between unlink and index publication,≤64 confirmed missing facts advance retained-set/tombstones/generation exactly once;65+ or inconsistent catalog/current-file state refuses until full repair. This does not depend on a later sweep reporting the already deleted key again.

The reader also validates required partition existence against its immutable cut and C3's partitionGone/errNoPartition distinction. If a sweep raced a read, discard/reselect against the new retained generation before emitting success; a stale index lookahead must not perpetually advertise a gone prefix. Missing partition proven expired is not500; permissions, bad JSON, wrong checked ID and inconsistent metadata are not expiry and must surface. A known expired before/after/range endpoint is a valid ordering fence, selecting actual retained neighbors or terminal empty; unknown/foreign tokens stay404. Do not reopen a deleted session or reset a cursor to latest.

| Expiry case | Required result |
|---|---|
| First/oldest partition swept | No old phantom lookahead; reaching the first retained group gives has_earlier=false/no next_before. |
| Middle partition swept | Select across retained neighbors in acceptance order with no fake blank spacer/source rows for deleted content. Cursors don't renumber. |
| Last rolled partition swept, live current retained | Newest/forward pages select actual current/remaining content; no stale has_later pointing only to the swept partition. |
| Retained effect, its original call swept | Exclude the effect from visible chat and ignore only its proven-expired target, as the full projection rules permit; no orphan tool row/result or500 solely because the target expired. |
| Target retained, relevant effect unreadable/corrupt | Error, not silently stale success. |
| Page response races sweep | Response metadata truthful at a coherent retained cut; UI uses the changed generation to revalidate its bounded held range (D7), not infer deletion of every session. |

**Windows/read handles:** page reads open/close a file for each bounded record read, not pin every selected partition for the entire slow page. A Windows sharing-busy removal while a record handle is open is **not expiry or generic permission success**: the retention owner skips that partition, keeps its index/generation presence and continues other eligible partitions, then retries it at the next normal sweep after handles close. Only the actual sharing-busy condition takes this path; permission/corruption errors remain reported failures with accurate partial-deletion state. Linux unlink/reselect and Windows busy/skip are separate tested outcomes. No busy spin, forced handle close, false deletion count or abort of all later eligible partitions solely for a reader-held sharing violation.

Retire expired per-partition rich lookup/projection metadata with the removed body, leaving D2's **token+rank-only ledger**. Do not retain old entry IDs/timestamps/sizes in renamed tombstone payloads or a forever-hot lifetime map. Content-free ledger growth on disk is accepted for stable expired boundary selectors and is reported/documented; it disappears with session deletion. Existing backup retention is an operator concern, not a hidden product copy.

No retention duration, scheduler, agent model-window policy or all-shard lock exception is redesigned. The paging index follows actual deletions, not vice versa; required native-Windows CI asserts skip/continue/retry and exact remaining partition/index state.

### D15 — Complete consumer and regression migration inventory

The envelope/history-state change is atomic across these current consumers. A successful **metadata-only** detail proves session identity/access, not an empty transcript; only available history updates row/continuation state. No stale header/has_earlier_messages write may overwrite a valid newer page or turn unavailable into false.

| Read consumer / verified source | Required migration and regression |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/lib/api/sessions.ts`::fetchSessionMessages / fetchSessionDetail / parseWireMessageList | Generated envelope/detail/state validation plus explicit semantic combinations; preserve per-item visible degradation, remove header side channel and no old-array fallback. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.tsx`::ChatScreen; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatThread.tsx`::ChatThread | Bounded page ownership/fallback, live-only independent composer/Stop and history-state guard. A bad history read replaces only the history slot, not the whole thread/control shell. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/clear-refetch.ts`::runClearRefetch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/has-earlier.ts`::recordSessionHasEarlier | Page-bound clear operation/readiness and atomic continuation; no unavailable/metadata-only result confirms clear or sets false history flags. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/routes/_app/sessions.$sessionId.tsx`::SessionRoute / Route.loader | Consume detail.session even if history_state=unavailable, retain one attach/redirect owner, stop reading retired detail.has_earlier_messages and show D7a controls/history error rather than a fake welcome/deletion. Actual404 still means SessionNotFound. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/session/workspaceEntryFlow.ts`::resolvePartialListPointer | Metadata-only detail still proves remembered session/workspace identity; no invented deleted/hidden/main fallback because its chat index is unavailable. Keep existing access/404/partial-list distinctions. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/slices/catchup-frames.ts`::handleSessionSnapshotFrame / handleCatchUpCompleteFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/types.ts`; source-verified C21::AttachReplayBuffer / WsConnection | New generated live_only and history_state handling; successful live/control cursor differs from history_loaded. Buffer releases all modes on complete, does not turn unavailable history into success, and preserves controls/user upward intent. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/tests/e2e/chat.spec.ts`::sessionMessages | Replace Array.isArray(body)?body:[] with generated envelope validation and explicit **all-retained-page enumeration** for assertions that mean whole history, including deliberate crossing of clear boundaries and terminal/progress checks. Wrong shape/failure must fail, not become an empty list. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/tests/e2e/parent-wake-stream.spec.ts` (other-session history assertion) | Parse generated envelope; if checking absence in all retained history, enumerate all pages rather than searching only a bounded suffix/JSON wrapper. Preserve session isolation oracle. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/tests/e2e/stop-scope-tree.spec.ts`::mockChatWebSocket / savedHistory fixture | Replace raw-array HTTP route mock with generated valid page/state fixture and durable-send history semantics; assert Stop under available and live-only history, not a false404/empty fallback. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/lib/api/sessions.has-earlier.test.ts`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/has-earlier.test.ts`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/test/integration/message-history.test.tsx`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/test/integration/chat-streaming.test.tsx` | Replace retired array/header/detail/snapshot assertions with new generated fixtures while preserving original behavior oracles; add unavailable and differing positive-budget values. |
| Chat/store mock inventory | Controlled search found dozens of array-return mocks, including `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.replay.test.tsx`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.earlier-note.test.tsx`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.dead-session.test.tsx` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/test/screens.test.tsx`. QA/implementing leads enumerate **all** matches of fetchSessionMessages/fetchSessionDetail/has_earlier_messages/X-Has-Earlier-Messages and session-message route mocks under the full source/test trees at RED, retain that inventory as gate evidence, migrate each real consumer/fixture or document an internal-only array, and prove CI discovery. A hand-picked dozen is not closure or a source-text substitute for behavioral tests. |

T33 mounts the actual route/workspace/detail/adapter consumers with generated available/unavailable data, exercises the e2e whole-history helper over multiple pages/clear, and proves a wrong response is red. Existing old-format tests are updated to the intentional contract; no skip/assertion weakening. All public/generated wire edits/regeneration remain backend-owned. The feature's user docs include DOC-4's operator runbook, not repair flags hidden in an API metrics subsection.

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
| Automatic **lifetime** boot/lazy/background rebuild | Rejected by founder R1-Q3; D13's separate stopped/backed-up command constructs missing baselines. R2-Q1 allows only D1a's≤64 saved-prefix suffix/delete-gap completion, not a lifetime repair hidden in repeated chunks. |
| **Hard byte cap / byte stop before two screens** | Rejected by same-round amended Q4. Select D4a's4 MiB soft budget with modeled two-screen floor, actual contiguous top-up and whole groups; record deliberate floor/atomic overrides. This fixes MAJ-002 under the founder's amended rule rather than reopening the choice. |
| **Single window without tail cache** | Not selected: founder R1-Q4 accepts MAJ-008 and retains500+100. Test explicit inactive-session payload release rather than silently switching policy. |
| Cherry-pick/wait for unlanded trains before work | Rejected: build current, chat-specific parser, catch up per landing; only landing order is gated. |

Affected components: session indexing/projection/reconciliation/read locks/retention, gateway/generated contracts/replay/prepared weigher, chat state/adapters/routes/geometry/focus, tests/CI/native platform gates, the operator-only Go command and public docs/runbook. Backend-lead implements backend/contracts/command/CI and drafts its docs; frontend-lead implements UI/consumer behavior and its docs. QA owns independent RED/CHECK; security-lead audits authority/privacy; docs-verifier audits drafted public docs. No design-system visual redesign, provider/scheduler/tool-policy rewrite or new content authority.

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
| Offline utility | Separately measure stopped-command throughput/peak scratch and canonical/backup integrity. It is not hidden in ordinary boot/first-open metrics and has no runtime preparing state. |

A no-gap guarantee under arbitrary latency/scroll speed is not promised. MAJ-002 is fixed by amended Q4's4 MiB soft page target plus two-screen floor/whole-group override and measured coverage tests. It is deliberately not a hard byte/heap ceiling: a huge indivisible group or the content needed for2H can exceed4 MiB. Record these actual overrides and existing result protections; do not silently relax the screen floor, re-raise the settled decision or misreport a fixed byte maximum.

### Content-free observability

Extend local structured replay/catch-up diagnostics, not telemetry/a new monitoring service. **Exact production JSON message: `message="history_page"`, with structured `event="history_page"`.** Use the existing gateway logger/structured bridge, not a new formatter or an ID-bound logger. Emit exactly one summary per authorized history-page attempt, including rejected query parsing, cancellation and failed preparation; not one per candidate, weigher callback or core/REST/WS layer. Access refusals precede the page attempt and keep existing access logging. A servable incremental journal attach that performs no page read emits no history_page summary. An explicit Retry is a new attempt. The existing logger's time/level/caller metadata is allowed; **no session/agent/user IDs, query strings or raw key/value text, bodies, parameters, results, secrets, positions, addresses, paths or unredacted error causes** in this new summary. This supersedes the earlier proposed session-ID field; private test-hook events are not log payloads.

| Field group | Exact names and required presence |
|---|---|
| Identity/classification — **every outcome** | `message="history_page"`, `event="history_page"`; `carrier` enum `rest_messages`/`rest_detail`/`ws_attach`; `mode` enum `latest`/`older`/`newer`/`window_refresh`/`unparsed`; `outcome` from the table below. `unparsed` means selectors were not successfully validated, not a guessed latest request. |
| Timing/budget — **every outcome** | `duration_ms`: finite nonnegative numeric elapsed preparation/validation time; `soft_byte_budget`: positive integer, current server value4194304. These are server measurements/constants, never raw query echoes. |
| Actual archive I/O — **every outcome**, nonnegative integers | `selected_archive_records`, `selected_archive_bytes`; `lookahead_archive_records`, `lookahead_archive_bytes`; `dependency_archive_records`, `dependency_archive_bytes`; `recovery_archive_records`, `recovery_archive_bytes`; `discarded_archive_records`, `discarded_archive_bytes`; totals `archive_records_read`, `archive_bytes_read`. |
| Other actual work — **every outcome**, nonnegative integers | `disposition_records_read`, `disposition_bytes_read`, `index_bytes_read`, `dependency_count`, `recovery_items`, `recovery_anchor_records`, `recovery_overflow_records`, `retention_retries`. |
| Validated options only | `limit` and `min_rows` after defaults/validation; `requested_limit` only if an explicit supplied limit parsed successfully. Omit unknown values rather than substituting0 or logging raw text. |
| Valid successful page only | `source_group_count`, `row_count`, `projected_bytes` as nonnegative integers; `has_earlier`, `has_later`, `byte_budget_exceeded` as booleans; `stop_reason` enum `row_target`/`source_cap`/`clear`/`end`/`range_end`/`byte_budget`; opaque `retention_generation`. `budget_override` is present exactly for an exceeded budget and is `height_floor` or `atomic_group`. These fields are omitted on failure, not fabricated as an empty successful page. |
| Allocations — **not a production summary field** | `allocated_bytes` is required in the isolated T18 measurement receipt defined below, not in each concurrent production history_page event. Never substitute process-wide heap growth, projected/serialized bytes or selected buffer sizes for attributable allocation evidence. |

| Exact `outcome` | Meaning |
|---|---|
| `success` | Valid bounded page and prepared public payload are ready for the carrier. This is not proof of completed socket/control catch-up, UI rendering, reachability or UAT. |
| `abort` | Attempt canceled before successful preparation; record actual work already performed, close resources. |
| `invalid_request` | Authorized request rejected for malformed/duplicate/out-of-range input, invalid selectors/range or unknown/foreign position, including400 `invalid history query` and404 position-not-found. No raw offending input is logged. |
| `protocol` | Internally contradictory page/continuation metadata prevents a valid page, not a caller syntax error. |
| `storage` | Required content/index I/O, framing or checked-address failure that is not a classified missing/inconsistent baseline or catch-up-limit refusal. |
| `index_unavailable` | Missing/incompatible/inconsistent required baseline or more than64 recovery items. May still be followed by D7a's independently successful live_only control completion. |
| `projection_error` | Nil/error/negative/overflow/inconsistent weigher or failure to prepare required public data. No guessed-weight success. |

Count **actual physical work**, including partial work on failure and discarded/reselected cuts. A zero counter means no such work occurred; never use0 for unavailable instrumentation. Archive record/byte totals equal the sum of the five disjoint archive categories above. Successful-cut returned source bodies are `selected`; an unreturned candidate checked for continuation/bytes is `lookahead`; extra effects/span/correction bodies are `dependency`; bounded recovery/anchor/overflow reads are `recovery`; body work from abandoned cuts is `discarded`. A physical read is charged once; a reread is extra work, not deduplicated out of the totals. Byte counters are actual encoded bytes obtained, including framing when read, not decoded text length or projected transfer size. Disposition/index counters similarly cover actual I/O, not estimates; `dependency_count` counts distinct relevant dependencies resolved, `recovery_items` counts unique mutation facts (overflow reported as65, not an invented lifetime total), and anchor/overflow counters count actual validation/probe records across their streams. No lifetime scan is allowed just to compute a diagnostic.

**T18 oracle:** on the healthy one-row-per-group, normal-size, no-effect/no-disposition/no-retention-race fixture with limit40, require `source_group_count=40`, `selected_archive_records=40`, and `lookahead_archive_records` in0..1. This **replaces an exact41-read assumption**: D1 permits at most one lookahead and a metadata-only continuation check need not decode an extra body. Any actual saved-anchor validation is counted separately in recovery (at most one per D1a attempt), not disguised as selected work. More complex fixtures account for their independently known dependencies/recovery/discards; no forced useless read just to satisfy41. Compare the1,000/100,000-prefix pair's measured selected-content/dependency record/byte work and allocations under the adopted≤10% bound, with zero omitted-prefix decoding and all other I/O categories visible; a full-read mutant must fail.

**Allocation evidence:** the isolated profiling/benchmark receipt is a test artifact with `message="history_page_measurement"`, fixed `case` enum `short_prefix`/`long_prefix`, positive integer `sample_count`, validated `limit`/`min_rows`, and nonnegative integer `allocated_bytes` (measured allocated bytes per complete page operation). Measure the real shared reader + gateway preparation/carrier path in an isolated process with unrelated work stopped/joined; fixture construction/index preparation is outside the measurement interval, measured page work is not. Go benchmark allocated-bytes-per-operation accounting is an acceptable instrument under that isolation, not evidence of per-request attribution in a busy product process. Bind receipts to the exact candidate/fixtures in the evidence pack, report setup and samples, and prove a planted full-read/allocation regression is detected. T18 consumes this receipt for allocations and real history_page summaries for I/O; missing allocation evidence is unverified, not0 or PASS. No production process-global allocation-delta polling or serialization of unrelated requests is introduced. No index_rebuild/preparing success state.

Browser development diagnostics: generation/direction, H/A/estimated versus measured prefix, request timing, anchor correction/drift, stale suppression, loaded/tail/sample counts, refresh mode/outcome; no content. UI recovery must not be log-only. Existing privileged storage-error cause logs remain as before; the new history_page summary stays sanitized.

**Log test:** capture real success/failure/abort/invalid-index/cursor paths with distinctive session/agent/user-ID, body, parameter, result, credential, raw token, private mark and query canaries. Assert exactly one `message=history_page`/`event=history_page` summary per authorized attempt, the exact outcome enum and every required numeric field above, conditional-field presence/absence and zero canary occurrences in new history diagnostics/browser diagnostics. Capture every outcome class, not only success; unknown counters cannot masquerade as0. Plant a raw-query/content/ID log in the tested seam and prove the test fails; do not just inspect the implementation for a banned string.

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
| Existing-data operator | Shipped offline Go command has checked backup/stop/dry-run/apply path exercised on copied real structure before team-lead's stopped-instance use. |
| Agent tools | No new tool/catalog/policy entry; existing recall unchanged. |
| Verification | Built embedded UI on genuine long session with independent uat-validator, exact landing commit and approved UAT provider/model when live turns are exercised. |

No old-message navigation entry exists or is claimed. Code correct and tested, and reachable by a user/agent, remain two separate delivery claims; this spec-only correction delivers neither implementation claim.

## User-Facing Documentation TODO

Implementing leads update these pages **in the behavior change**; docs-verifier checks actual behavior. This Draft-only correction changes no public behavior and edits no public page.

| ID | Existing / proposed affected page | Required update |
|---|---|---|
| DOC-1 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/using-omnipus-ui.md`::The chat area / Clear a chat's context / Sessions / Limits and things to watch | Recent-first≈4-screen open,≈2-screen prefetch, keyboard earlier/Retry/Return, clear barriers,500+100 policy, soft4-MiB target with2-screen/whole-group precedence, not a hard byte guarantee. Explain cached/out-of-date/partial/full expiry copy, paused-prefetch Retry, live-only history error with usable send/current approvals/Stop and when to contact the operator; link DOC-4. Replace “reaching earlier messages is planned separately”. No target-link/search/copy-link TODO. |
| DOC-2 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/operations/observability.md`::Replay / Session transcript, new **Paged session API** subsection | API and content-free metrics only. Concrete array→envelope latest/before/after/window-refresh examples, metadata-only unavailable detail/history_state, limit/min_rows advisory trust boundary, soft bytes/floor/override, positive budget values, retained flags/tombstones and bounded64 recovery vs fixed error. All-history clients explicitly enumerate retained pages. Link canonical OpenAPI and DOC-4 for operator repair; do not place build/backup/stop/receipt/repair flags here. |
| DOC-3 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/protocol/websocket-protocol.md`::Connection Lifecycle / Server to Client Frames | history_limit/history_min_rows, bounded snapshot/page metadata/local history_origin, done vs complete and interim buffering, full incremental journal/held order. Document typed history_state and mode=live_only, completed live cursor vs unproved history/seen, Retry/current-control independence, historical-reader preservation and matching-client/reload requirement. No ordinal manifest. |
| DOC-4 | **New operator runbook**, proposed `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/operations/chat-history-index.md` | Backend-lead drafts build prerequisites/recipe and all D13 command phases/absolute CLI examples, schema/manifest producer and actual full-copy verification, stopped-receipt producer/check, exact exit/refusal/summary classes and copied-data rehearsal. Order: initial dry-run→verified backup→stop→stopped-source equality→apply→verify→restart/UI. Include unconverted-first normal boot/cutover, preserved rollback backup vs fresh authoritative post-cutover backup, source-drift refusal, safe rerun/interruption, bounded64 automatic recovery vs operator-only full repair, Windows busy next-sweep behavior and token/rank-only ledger growth/privacy. State that real conversion status is a preflight result, not assumed. Docs-verifier audits against actual command/CI/read behavior before landing. |

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

### B04 — Aborted/incomplete attach retains the prior view and never claims completion
Traces to: User Story 1, Acceptance Scenario 3.
Category: Error Path.
**Given** D-E1's prior page and an interrupted or undeliverable attach, **When** that attach fails before complete live/control delivery, **Then** ErrorState/reconnect recovery retains the prior page/cursor and no successful complete is committed. A delivered history-only failure with valid control projection instead follows the explicitly different B31/B43 live_only branch, never a successful history snapshot.

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
**Given** D-V1's authorized session/query variant, including an unrecognized malformed raw key `bad%ZZkey=x` or value `unused=%ZZ`, **When** it is requested, **Then** D11's exact error occurs: those unrecognized raw-query decoding failures return400 `invalid history query` with inbound validation on and off, invalid external limits do not clamp, malformed selectors do not disappear and no rejected request emits a page.

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
**Given** D-R4's valid checkpoint/restart/rollover variants and D-H1/D-H2's post-crash deltas, **When** history is read, **Then** unchanged occurrences keep their token and complete projection;≤64 consistent suffix mutations reconcile once, while missing baseline/65+/inconsistency returns the fixed history error with D7a live controls, never partial success or a lifetime rebuild.

### B23 — Contradictory metadata rejects only the failed page
Traces to: User Story 2, Acceptance Scenario 3; User Story 4, Acceptance Scenario 2.
Category: Error Path.
**Given** D-E3's broken continuation/session/known-overlap data or D-B5's budget-value variants, **When** it is consumed, **Then** contradictory metadata/nonpositive budgets reject without partial rows/flags and Retry/diagnostics remain; consistent other positive budget values are accepted, and valid page metadata with one bad item retains a visible position-stable placeholder.

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

### B31 — Missing baseline is a history error, not lost live control or false preparation
Traces to: User Story 1, Acceptance Scenario 3; User Story 5, Acceptance Scenario 4.
Category: Error Path.
**Given** D-O4's session without a usable baseline (not merely a≤64 suffix), **When** REST detail/messages or WS-backed UI opens it, **Then** messages gives the fixed500, detail explicitly returns authorized metadata/history_state=unavailable without page fields, and attach completes only live/control state with mode=live_only; history ErrorState/Retry and live Stop/current interactions coexist without a wipe, successful history flag,503, lifetime scan or empty/partial success. Retry cannot seed that missing baseline.

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
**Given** D-B1/D-B1b's independent large-output fixtures with H800/A80/min_rows20, or D-B4b's min_rows=limit=100 near8-MiB valid groups, **When** its limit100 page is requested, **Then** the20-floor fixture byte-cuts only after20 whole rows while the100-floor fixture retains all100 with height_floor override despite its large bytes; actual projected_bytes/stop_reason/override and retained continuation are exact. Advisory min_rows and the soft target are not a hard defense against the authenticated caller; neither group nor required modeled floor is truncated.

### B38 — Normal page never hits the comfortable byte target
Traces to: User Story 1, Acceptance Scenario 5.
Category: Happy Path.
**Given** D-B2's normal40-row8-KiB/group conversation and default4-MiB budget, **When** its opening page is selected, **Then** the normal40-row request is unchanged, no byte_budget stop/override appears and its measured height approximates4H rather than a reduced two-screen open.

### B39 — Four-screen opening and two-screen prefetch survive byte cuts and estimate correction
Traces to: User Story 1, Acceptance Scenario 1; User Story 2, Acceptance Scenarios 1 and 2.
Category: Happy Path.
**Given** D-B3/D-B3b's budget-shortened candidates, row-cap-limited small rows and initially wrong height estimate with enough retained content, including simultaneous opening≈2H threshold/top-up events, **When** the specified open or prefetch fill cycle commits, **Then** exactly one coalesced contiguous request chain tops opening up to≈4H or acquires≈2H new prefetch content; the byte budget never causes an under-two-screen logical fill, and anchor/cursors/whole-group identity stay correct. A true clear/end/error is reported rather than fabricated content or silent short success.

### B40 — Scenario Outline: Bounded suffix reconciliation is not a lifetime rebuild
Traces to: User Story 4, Acceptance Scenario 8; User Story 1, Acceptance Scenario 3.
Category: Edge Case.
**Given** D-H1's valid durable known prefix and `<pending>` combined recovery items after it, **When** startup/open invokes the shared reconciler, **Then** `<outcome>` occurs with only suffix/anchor/overflow reads, no token renumbering and no history-ready partial root; an interrupted retry applies each canonical occurrence once.

| pending | outcome |
|---|---|
|0|No-op, unchanged metadata|
|1|Reconcile1 and serve complete history|
|63|Reconcile63 and serve complete history|
|64|Reconcile64 and serve complete history|
|65|Fixed unavailable-history outcome, operator repair required, no candidate root publication|

### B41 — Crash after rolled-partition delete reconciles even when next sweep deletes zero
Traces to: User Story 4, Acceptance Scenarios 5 and 8.
Category: Edge Case.
**Given** D-H2's persisted retained catalog with a rolled file already absent after crash, and current sweep removed_count=0, **When** the session opens, **Then** the missing fact within64 is published exactly once, retention generation/true earlier lookahead update, and no phantom prefix or perpetual500 appears; an inconsistent/current-file disappearance takes the explicit error instead.

### B42 — Sending into unindexed history saves canonically without creating a partial index
Traces to: User Story 1, Acceptance Scenario 6; User Story 5, Acceptance Scenario 4.
Category: Alternate Path.
**Given** D-N1's authorized nonempty session with missing/mismatched baseline or over-bound suffix, **When** a new instruction is accepted, **Then** its original canonical record/receipt/live output persist once, old content stays, no new suffix-only baseline/covered-end is invented, messages still truthfully errors and metadata/live control access stays explicit.

### B43 — History-unavailable attach completes only live control access
Traces to: User Story 1, Acceptance Scenario 6.
Category: Happy Path.
**Given** D-N2's authorized active turn with pending approval/question and unavailable index, **When** attach completes, **Then** typed history_state=unavailable, current state/projection, live_only complete and held live seq order are delivered without a history wipe/false history-loaded/attention mark; composer, Stop and current interactions remain reachable beside the history ErrorState/Retry.

### B44 — Built Go repair command produces/verifies the specified full-backup manifest
Traces to: User Story 5, Acceptance Scenarios 1 and 6.
Category: Happy Path.
**Given** D-GO1's built standalone command, canonical fixture and full copied backup, **When** its selected dry-run/manifest/apply/verify phase runs, **Then** the documented schema/summary/exit0 and phase-specific writes hold, actual backup hashes/inventory are checked, and indexes come from the same Go constructor/projection engine used by the product. CI must show named command tests/build, not a presumed discovery pass.

### B45 — Unconverted-first and post-cutover backup sequencing refuse stale baselines
Traces to: User Story 5, Acceptance Scenarios 2 and 5.
Category: Error Path.
**Given** D-GO2's unconverted data or preserved pre-cutover backup after successful normal product cutover, **When** the corresponding command preflight runs, **Then** unconverted returns class unconverted/exit2 before apply, or stale backup returns source_changed/exit2; no index publishes until fresh post-cutover dry-run/backup/stop checks. Corrupt canonical input is the distinct integrity exit1, never silent refusal with0.

### B46 — One gateway meter selects both carriers and invalid weighing fails visibly
Traces to: User Story 4, Acceptance Scenarios 1 and 9.
Category: Error Path.
**Given** D-W1's tool-heavy/special-heavy candidate fixtures or injected nil/error/negative/overflow weigher, **When** the shared gateway page factory is exercised, **Then** valid REST/WS select the same positions/weights using one prepared implementation with no publish side effects/reprojection; invalid weighing fails rather than estimates/falls back, and authorized live controls can degrade independently.

### B47 — Window refresh failure retains out-of-date reader, pauses prefetch and retries that range
Traces to: User Story 2, Acceptance Scenarios 3 and 10.
Category: Error Path.
**Given** D-F1's retained historical anchor/focus and failed closed-range refresh, **When** that refresh failure commits, **Then** “Could not refresh loaded messages.” and Retry/out-of-date notice are visible with existing rows, aria-busy clears, ordinary prefetch pauses, current controls remain usable and Retry targets this same range without attach/user resend or passive latest navigation.

### B48 — Scenario Outline: Partial and full range expiry use exact text/anchor/focus states
Traces to: User Story 2, Acceptance Scenarios 6 and 10; User Story 4, Acceptance Scenario 5.
Category: Edge Case.
**Given** D-F2 with `<survival>` in the held range, **When** its refresh commits, **Then** `<notice>` and `<reader>` appear without declaring the entire session empty or jumping bottom; a removed focused row uses preventScroll scroller fallback while unrelated composer focus stays.

| survival | notice | reader |
|---|---|---|
|some; anchor retained|Some loaded messages have expired.|Same anchor/offset|
|some; anchor expired|Some loaded messages have expired.|Nearest later old-range survivor, else nearest earlier|
|none|These loaded messages have expired.|Explicit Return to latest; no automatic latest navigation|

### B49 — Stop actually works after live-only history error
Traces to: User Story 1, Acceptance Scenario 6.
Category: Happy Path.
**Given** D-N2 after a completed live_only attach with a genuinely running turn, **When** the person activates Stop, **Then** the existing current-turn Stop path is invoked/observed by the running engine, current control outcome arrives, historical ErrorState persists truthfully and no lost viewer/history failure had already cancelled or relaunched that work.

### B50 — Native Windows busy partition defers only that removal and preserves private tombstones
Traces to: User Story 4, Acceptance Scenarios 5 and 10.
Category: Edge Case.
**Given** D-X6's native Windows sharing-busy reader handle or its closed-handle variant, **When** the sweep runs, **Then** busy-present data remains indexed and later eligible files are still processed, or the closed file is removed normally; only actual deletion advances generation and resulting tombstones contain exactly token/rank, never entry ID/time/size.

### B51 — Existing consumers handle pages and metadata-only detail without fake empty history
Traces to: User Story 1, Acceptance Scenarios 2, 3 and 6; User Story 4, Acceptance Scenario 1.
Category: Happy Path.
**Given** D-C1's real route/workspace/adapters and paginated whole-history fixtures, **When** those consumers read the new contract, **Then** session identity still attaches with unavailable history, flags/rows apply only when available, helpers enumerate every intended retained page and a wrong response shape is an honest failure, never Array.isArray fallback to[].

## Test-Driven Development Plan

All tests below are **planned**, not written/executed here. QA's RED uses independent expected values/frozen fixtures; a separate CHECK audits it. Unit→integration→E2E ordering; deterministic concurrency barriers, not sleeps. Removed target-only test IDs are retired rather than reused.

| Order / proposed test | Level | BDD | Independent oracle / scope |
|---|---|---|---|
| T01 TestChatHistoryPage_SelectsNewestAndActualLookahead | Unit | B01,B02,B17 | Exact IDs/flags on empty/exact-full/off-by-one. |
| T02 TestChatHistoryPosition_StableOccurrenceAndSessionScope | Unit | B19,B20,B22,B27 | Equal timestamps/duplicate IDs, rollover/final-byte reconstruction/tombstone/unknown-session distinctions. |
| T03 TestChatHistoryPage_ClearStopsAndExplicitCrossing | Unit | B03,B13 | Exact marker and one-barrier crossing, unchanged context/canonical bytes. |
| T04 TestChatHistoryPage_MergedProjectionParity | Unit | B18,B24 | Hand-derived settle guards/project/retract/cancel/disposition/duplicates; whole101-row group and recorded atomic byte override. |
| T06 TestChatHistoryIndex_HealthyCutAndBoundedReads | Unit | B21,B22,B31,B40 | Count actual addressed records/disposition seeks;0/1/63/64/65 suffixes, no missing-baseline/lifetime success and no candidate publication above bound. |
| T07 chatHistorySizing.measuredRowsAndPixelThreshold | Unit | B07 | Formula tables plus **real virtualizer** with40px rows, A-driven estimates, prefix invalidation/tool child weights, hidden/H0/sample replacement. |
| T08 chatHistoryState.orderedMergeAndGenerationFences | Unit | B06,B08,B11,B23,B26 | Delayed promises, current live result, generation not session-only, split reader/tail snapshot and no frame-range protocol. |
| T09 chatHistoryWindow.readerAndTailPolicy | Unit | B12,B34 |499/500/501 and100/101; safe whole-edge eviction/defer, forward reload, inactive reader/tail/query/media cleanup, pending identity. |
| T10 chatHistoryAnchor.signedEveryLayoutCorrection | Unit | B08,B09,B17 | T3's independent loader/hint/Retry/gap geometry; mutations disable signed corrections/count tail growth. |
| T19 TestChatHistoryRetention_ActualRetainedContinuation | Unit | B27,B41,B50 | Real first/middle/last sweep, post-unlink crash+next removed_count0,≤64/65 missing facts, token/rank-only tombstones and sharing-busy vs permission failure. Native Windows skip/continue/later retry is required, not inferred from a cross-build. |
| T22 TestHistoryRepairCommand_PreflightManifestAndIdempotence | Unit | B30,B35,B36,B44,B45 | Common Go engine plus real temporary full source/backup/schema/receipt data; initial dry-run before backup, manifest producer/decoder, exact per-session exit mapping/cutover drift, independent hashes/mtime. No copied projection implementation or mocked backup validation. |
| T26 TestChatHistoryReadCut_SlowReadDoesNotHoldAppendLocks | Unit | B32,B50 | Real blocked content reader; append/effect completes before release, no shard/append mutex held over content I/O; close each bounded handle. Native Windows held-record deletion and follow-up sweep verified with exact files/index state. |
| T27 TestChatHistoryPage_SoftBytesRespectTwoScreenFloor | Unit | B23,B24,B37,B38,B39 | Independent encoded-size fixtures; pin4194304 server default, accept other positive advertised budgets, refuse nonpositive/contradictory metadata. Normal/bite/floor/atomic cases plus min_rows=limit=100 with near8-MiB valid groups prove advisory-floor override accounting, not a hard byte defense. Removing budget or prioritizing bytes over floor fails. |
| T29 TestChatHistoryRecovery_PublicationCrashBoundAndIdempotence | Unit | B22,B40,B41,B42 | Real canonical/derived files, every D1a publication crash point;0/1/63/64/65 combined archive/disposition/delete facts, private model records, torn unpublished derived tail vs framed/canonical corruption. Count suffix/anchor/overflow reads and root publications;65th item prevents candidate publication, repeated retries cannot advance64 at a time. Prior durable mutation deduplicates exactly once. |
| T32 TestHistoryPageProjection_OnePreparedSideEffectFreeWeigher | Unit | B18,B23,B37,B38,B46 | One real gateway projector/request factory over tool-heavy/special-heavy carrier divergence. Independently encode expected generated payloads, count one projection/group and no publish/action side effects, reuse final prepared payloads. Nil/error/negative/overflow/inconsistent dependency fails, never guessed0/estimate/full read. |
| T36 TestChatHistoryRetention_WindowsBusyContinueAndRetry | Unit | B27,B41,B50 | Native Windows actual held file handle, later eligible partitions, then closed-handle next sweep. Assert only sharing-busy skips, actual removed/present counts/catalog/generation, token+rank-only persisted tombstones and rich-metadata retirement; genuine permission error still reports failure. Linux unlink/reselect has its own real-file case. |
| T11 TestGetSessionHistory_ValidationAndAccess | Integration | B19,B20 | Real registered auth/gate/params, REST/WS inaccessible+bad limit, both inbound-validation settings. Unrecognized malformed raw keys (`bad%ZZkey=x`) and values (`unused=%ZZ`) return400 `invalid history query` after access and before typed paging validation; recognized-key value failures keep their field-specific errors. |
| T12 TestSessionHistory_RESTDetailSnapshotSelectSamePage | Integration | B01,B02,B03,B18,B24,B31,B37,B38,B43,B46,B51 | Real store/handler/hub/generated schemas with one D4b request factory/weigher. Identical range/counters on tool/special divergence; local origins; typed metadata-only detail/live_only history-failure distinction; no false history success or503. |
| T13 TestAttach_PagedSnapshotPreservesLiveAndControls | Integration | B04,B05,B06,B26,B31,B43 | Bind/persist barriers, pending controls/result coverage, kept old-reader window. Separate successful snapshot, fully delivered live_only and incomplete-control/socket abort: only first proves history, only first two commit live cursor, failed reader revalidation never rolls back completed live catch-up. No false seen/history flag or history-derived restart sweep. |
| T14 chatHistoryClear.operationOwnedPageRefresh | Integration | B03,B11,B13 | Clear send ownership and marker outside recent page; foreign clear does not teleport historical reader. |
| T15 chatHistoryComponents.retryPlainAndLatestStates | Integration | B02,B10,B11,B23,B31,B34 | Real adapters/store/components, latest pending/error/Retry without resend, minimal index error and plain/manual path. |
| T20 chatHistoryAccessibility.keyboardFocusAndAnnouncements | Integration | B28,B29 | Real catalogued controls, Enter/Space, stable focus while pending, terminal preventScroll fallback, one status/alert and no old-response announce. |
| T23 TestHistoryPageDiagnostics_ContentFreeOnEveryOutcome | Integration | B33 | Capture real structured logs/browser diagnostic payloads; positive outcome/count assertions + canary absence, planted raw-query/content log kills test. |
| T25 TestHistoryRepairCommand_SharedConstructorAndRealReader | Integration | B30,B35,B36,B44,B45 | Build/invoke the standalone Go command on actual canonical/cutover fixtures using product index/reducer code. Validate generated indexes through real reader plus independent expected projection/position vectors and canonical/backup hashes; dry-run before backup, stale pre-cutover manifest refusal and interrupted/second apply. Named command-package tests run in required CI. |
| T30 TestHistoryRepairCommand_BuiltPhasesCutoverAndManifest | Integration | B30,B35,B36,B44,B45 | Invoke built command phases on independent real trees: initial dry-run with neither backup nor receipt; producer/schema decoder including empty/zero-byte/full inventory; post-cutover stale backup, all refusal/aggregate exit classes and no preflight publication. Shared session engine is real, not copied or mocked; required Linux/Windows job emits build exit and named test counts. |
| T31 TestUnavailableHistory_CanonicalAppendAndLiveOnlyControls | Integration | B04,B20,B31,B40,B42,B43,B49 | Actual append/session/gateway/hub and reducer: no suffix-only baseline, saved receipt once, typed metadata-only detail, live_only at W then held frames. Current approval/question/secret interactions and engine-observed Stop operate; auth/parameter failure cannot bypass access and incomplete controls cannot complete. History Retry neither resends nor disables existing live controls. |
| T33 chatHistoryConsumers.envelopeDetailRoutesAndWholeHistory | Integration | B02,B23,B31,B51 | Mount actual D15 route/workspace/adapters with available and metadata-only detail. Enumerate all retained pages/clear in whole-history helpers; malformed envelope cannot become[]. Save exhaustive consumer/mock disposition inventory and CI discovery; internal arrays are not wrongly banned. |
| T34 chatHistoryRefresh.failureExpiryAndBudgetContinuation | Integration | B26,B27,B47,B48 | Actual range reducer/components/generated adapters with exact D7/D8 copy, busy/Retry/prefetch/focus states. Stage multiple byte-limited pages>5 requests for500 held groups, no extra tail walk, failed partial staging cannot delete cached rows or declare whole-session empty. Retry repeats identical saved interval. |
| T16 chat-history-paging.open-scroll-anchor | E2E | B01,B07,B08,B09,B12,B13,B17 | Built gateway/embedded SPA, real styles/images/virtualizer, normal-use threshold/geometry/counts; preserves Train3 regressions after catch-up. |
| T18 chat-history-paging.prefix-and-real-session-evidence | E2E | B21,B22,B25 | Prefix instrument controls + genuine long session, exact commit/reference metrics/independent validator. Consume exact `history_page` I/O fields: normal limit40 fixture has40 selected records plus at most1 lookahead, not mandatory41 total reads; account separately for recovery/dependencies/discards. Require the isolated `history_page_measurement.allocated_bytes` receipt and≤10% prefix-work/allocation variance; no production process-wide heap delta or missing-evidence-as0. |
| T21 chat-history-paging.keyboard-reader-states | E2E | B28,B29,B31,B34 | Keyboard-only actions,200% zoom/reflow/plain/reduced motion, real focus/announcement transitions, no preparing state. |
| T24 chat-history-paging.reconnect-retention-retry | E2E | B04,B05,B06,B10,B11,B20,B26,B27,B34,B47,B48 | Forced snapshots while3,000 rows back, exact cached/out-of-date/paused-prefetch Retry state, partial/full expiry text/focus, byte-limited range continuation>5 requests, pending controls and no passive latest jump. |
| T28 chat-history-paging.soft-budget-screen-coverage | E2E | B37,B38,B39 | Built gateway/real virtualizer/geometry plus huge projected tool output: byte-cut/override accounting,≥2H actual fill or explicit boundary/failure,≈4H open/≈2H prefetch, no anchor jump;40-row normal fixture unchanged. Simultaneous≈2H opening observer/top-up events produce exactly one contiguous request chain. |
| T35 chat-history-paging.unavailable-history-live-controls | E2E | B20,B31,B42,B43,B49,B51 | Built embedded UI, genuine active turn and unavailable index on copied data. Real session route/workspace entry still attaches; history ErrorState stays while saved send/current approvals/question/secret controls work and Stop reaches running engine. Retry/live reconnect preserve controls and never resend, wipe cached history or assert history-loaded/seen. Exact-commit UAT and independent validator also exercise this path. |

Backend tests live beside `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_view_test.go`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_effects_test.go`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/clear_view_test.go` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/ws_attach_catchup_test.go`. Proposed command tests: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/cmd/chat-history-index-repair/main_test.go`; shared-core integration fixtures alongside session tests, prepared-weigher tests alongside gateway tests. Proposed browser file: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/tests/e2e/chat-history-paging.spec.ts`. QA verifies CI discovery/named execution for every new test, including D13's required job and native Windows T36. A test merely written or an Ubuntu cross-build cannot count as executed/native certification.

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
| D-B3 | Opening byte-cut to20 of40 rows, then contiguous top-up20; ordinary2H observer fires during same opening; prefetch floor20; true end/clear with<floor; delayed image/actual average changes |4H/2H/estimated-to-measured/owner | One coalesced chain, open≈4H via bounded top-up; prefetch≈2H new content; actual underfill corrected without jump, true boundary/error visible. No duplicate opening/prefetch chains and budget alone never declares a too-short logical fill complete. | B39 |
| D-B3b | H800,A10 raw floor160/open target320,limit100,min_rows100; enough normal rows, all fitting the500-group reader; separately row-target boundary90+20 drawn rows with floor100 | Clamp/atomic boundary | Multiple≤100-source-group responses fill logical2H/4H; bytes never shorten a capped chunk below100 estimated rows. A final whole group needed for floor is not split or dropped even across the row target. | B39 |
| D-B4 | min_rows omitted/1/20/limit vs0/limit+1/null/float/duplicate/malformed encoding; history_min_rows on inaccessible main + invalid limit | Floor input/precedence | Default20 or valid1..limit; exact D11 refusal, access and limit errors precede floor error, no bind/scan on rejection. | B19,B20,B37 |
| D-B4b |100 one-row groups, each valid encoded canonical record just below8-MiB admission; limit=min_rows=100, including SPA1px-average variant | Authenticated advisory-floor override | All100 whole groups admitted despite4-MiB soft target; byte_budget_exceeded=true,budget_override=height_floor, actual max-carrier bytes reported and row_target/source_cap rather than byte_budget. No claim the floor or soft budget prevents this authorized large response. Independent fixture verifies admission and measured encoded bytes, not100×a guessed size. | B37,B39 |
| D-B5 | Advertised soft_byte_budget2097152/4194304/8388608 with consistent actual accounting vs0/−1/null/fraction; ordinary default factory | Positive wire range/default | Positive integers accepted by generated parser plus semantic checks, not pinned to a one-value enum; nonpositive/invalid values rejected. Server factory default remains exactly4194304; alternate advertised values do not change that default test. | B23,B38,B46 |

#### D-R — Reconnect/races/index faults

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-R1 | Append/effect before bind/between bind-read/read-complete/after release; current controls | Concurrent | Selected/current items once, local origins/complete then held live | B05 |
| D-R2 | Servable same-boot cursor; ahead/unknown/evicted journal; unfinished answer | Incremental/snapshot | Whole tail or bounded latest snapshot, no history cursor mutation | B06 |
| D-R2b | Reader3,000 rows back;499/500 groups; forced boot_mismatch/retention_exceeded; pending input; delayed/failed closed-range refresh | Historical snapshot | Keep reader/focus/anchor; fresh tail/control only; bounded direct revalidation or cached/out-of-date Retry; no bottom jump | B26 |
| D-R3 | Deferred success/error A→B→A; own clear; other-tab clear; replacement snapshot/range | Generation | No obsolete rows/flags/errors; other-tab clear follows D6/D7 | B11 |
| D-R4 | Valid index restart/UTC rollover; missing/corrupt index; durable post-checkpoint archive/disposition/delete deltas0/1/63/64/65; unchanged-final-byte Go reconstruction | Fault/stability | Same token on valid unchanged occurrences;≤64 consistent suffix completes, missing baseline/65+/inconsistency gives D11 unavailable outcome, never lifetime construction/partial success. Messages500 is distinct from metadata-only detail/live_only controls. | B22,B31,B40,B41 |

#### D-H / D-N — Publication recovery and unavailable-history live operation

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-H1 | Valid known prefix; combined0/1/63/64/65 archive+disposition mutations, including private model/model_ref and far-day effects; kill after canonical fsync, derived-row fsync, lookup staging, checkpoint publication. Duplicate redo; torn unpublished derived tail; complete bad row, changed/truncated canonical anchor; simultaneous recoverers and append beyond captured end. | Saved-tail bounds/crash/framing/concurrency |0 unchanged;1..64 unique items reconcile once with only bounded anchor/suffix/probe reads, correct shared projection and stable tokens.65 publishes no candidate ready root and repeated Retry cannot batch-walk. Private records advance coverage, not rows. Torn unpublished derived tail only can be redone; framed/canonical damage refuses without trimming. Coalesced owner publishes original cut only; later writes remain next suffix. | B22,B40,B42 |
| D-H2 | Saved retained catalog then crash after unlink/before mutation-row or root; next sweep removed_count0.1/63/64/65 absent rolled facts, mixed32 canonical+32 expiry vs33+32; already durable retention row; current partition absent or identity changed. | Delete-gap/combined bound/idempotence |≤64 unique combined facts publish coherent retained generation/tombstones once;65 refuses without partial ready root. Never depend on current pass's deletion count; current-file loss/repoint is inconsistency, busy-present file never expiry. Retained-only continuation, no phantom old prefix. | B27,B40,B41,B50 |
| D-N1 | Nonempty authorized session with missing baseline, incompatible/mismatched checkpoint,65+ pending; new chat/model/effect/disposition append; fresh empty-session positive control | Append without derived baseline | Canonical append/fsync/receipt succeeds under normal rules, new live output once; old bytes untouched, no suffix-only empty/newest baseline or invalid covered-end advancement. History still unavailable pending repair; fresh creation alone validly initializes before identity publication. | B31,B42 |
| D-N2 | Actual active engine turn, approval/question/secret pending, unavailable index, hub frames held around W; history Retry and reconnect. Variants unauthorized/missing/bad limit, failed control delivery, and real Stop activation. | Reachability/control vs history failure | Typed unavailable state/current projection→live_only at W→seq>W; no snapshot wipe/history_loaded/restart sweep/seen or turn error. Existing send/approval/question/secret/Stop work and engine observes Stop. Retry does not resend or hide ready controls. Access/params refuse before bind; incomplete control/socket delivery never completes. | B04,B20,B31,B43,B49 |

#### D-X — Actual retention

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-X1 | First rolled partition swept, first retained page exact-full | Old edge | has_earlier flips false/no cursor; known expired fence selects actual neighbors | B27 |
| D-X2 | Middle partition swept, both sides retained | Hole in disk retention | Ordered retained neighbors, no fake blank row/renumber | B27 |
| D-X3 | Last rolled partition swept, current/older retained; one removal fails | New edge/partial deletion | Only successful deletions excluded; truthful has_later | B27 |
| D-X4 | Effect retained but original call partition swept; retained target with unreadable/corrupt effect control | Dependency expiry vs damage | Expired target ignored/no orphan; real corruption fails | B27 |
| D-X5 | Sweep after read-cut before response; some/all held rows expire; anchor survives/doesn't; retention disabled control | Race/anchor/disabled | Coherent flags/generation; nearest in-range anchor or explicit expired-range state, never500 loop/auto-latest | B26,B27,B48 |
| D-X6 | Native Windows real record handle open on an eligible rolled file, later eligible rolled files; sweep before/after handle close; true permission-error control. Linux real unlink/reselect comparison; rich lookup fields seeded before expiry. | Platform busy/partial success/privacy | Windows sharing-busy skips only held file and continues later eligible deletes; presence stays indexed, deletion count/generation reflect only actual removals. Next sweep after close removes normally; genuine permission failure remains reported. Every resulting tombstone has only token/rank; no old ID/time/size/result/physical address or renamed rich metadata, no lifetime RAM map. Linux reselects coherent retained cut after unlink. | B27,B41,B50 |

#### D-W / D-F / D-C — Weigher, refresh UI and existing consumers

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-W1 | Matched tool-heavy vs special-frame-heavy pages where REST/WS byte contributions differ most; same cut/options/floor; nil/error/negative/overflow/inconsistent weigher and canceled lookahead; generated positive-budget variants | Carrier parity/layering/failure | One gateway request factory/prepared projector selects identical positions/flags/max-carrier bytes for REST/detail/WS; selected group projected once and final output reused. Weighing has zero send/action/hub/durable/card/lifecycle effects. Bad dependency returns explicit projection failure; no guessed estimate/0/full read. | B18,B23,B37,B38,B46 |
| D-F1 | Reader3,000 rows back with500 groups; snapshot complete then held-range refresh fails before/after some staged chunks; pending input, cached anchor/focus; Retry fail/succeed. Valid500-group interval requiring25 byte-limited20-group pages under D-B1 costs. | Refresh error/continuation/staging | Exact “Could not refresh loaded messages.”+Retry and out-of-date notice; cached reader/control/focus stay, busy clears, normal prefetch paused. Retry repeats saved range, not attach/send. Successful interval may need>5 requests, each≤100 groups with budget/floor accounting, never read omitted tail-to-reader middle; partial staging is not proof rows expired. | B26,B47 |
| D-F2 | Same old range with some survivors/anchor retained, some survivors/anchor gone, zero survivors; focused row/control expires vs unrelated composer focus; short final range under floor | Partial/full expiry UI | Exact B48 text; surviving anchor same offset else nearest later/earlier old-range survivor. No survivors means explicit Return to latest, not welcome or passive latest. Removed focus uses labelled scroller preventScroll; composer focus stays. Range continuation is not whole-session continuation, and true range_end may underfill. | B26,B27,B48 |
| D-C1 | D15 actual route/workspace/adapters with generated available vs unavailable detail; messages envelope vs old array/malformed metadata; multiple retained pages and two clear boundaries; raw-array route mocks and internal-only normalized arrays | Consumer migration/whole-history oracle | Identity route/remembered pointer still attaches with unavailable detail; no unavailable flags/rows applied. Whole-history helpers enumerate all intended retained pages/clear with progress/terminal guards; wrong shape fails rather than[]. Preserve internal arrays only with documented disposition. Positive-controlled full source/test inventory and named CI execution cover every real consumer/mock. | B02,B23,B31,B51 |

#### D-V / D-E — Validation and failures

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-V1 | limit omitted/20/40/100/020 vs19/101/0/−1/1.5/+20/space/empty/null/NaN/Infinity/overflow/duplicate/%ZZ | Numeric/type/min±1/max±1 | Default/range or exact400, no server clamp/omission | B19 |
| D-V1b | valid47-char token;46/48/wrong version/bad alphabet/blank/null/control/duplicate/malformed encoding; unknown/foreign valid token | String/scope | Exact400 vs404; known expired boundary remains usable | B19,B20,B27 |
| D-V1c | before+after; one range endpoint; reversed endpoints; after with range; before outside range; range all expired | Mode/range | Exact400/404 precedence; known closed expired range empty with window_refresh mode, not empty whole session | B19,B26 |
| D-V2 | No/invalid auth; hidden main/missing child; legitimate child; bad limit on **both REST and WS**, each read mode | Access/precedence | Access-first refusal before bind/lookup; no user-owner invention/parent fallback | B20 |
| D-E1 | Previous page; interrupted/undeliverable attach and failed current-state/projection/socket delivery vs pre-replay history-only storage/index error with healthy controls | Abort vs history-only degradation | Incomplete live/control catch-up: no wipe or complete/cursor advance. History-only error before snapshot: typed unavailable/live_only branch can complete controls/live cursor, never successful history/seen. Cached rows retained, Retry visible, no turn cancellation or lifetime fallback. | B04,B31,B43 |
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
| D-O2 | No confirmation/receipt; running or unknown PID; incomplete/tampered/wrong-root backup; symlink/traversal; source drift; unconverted/unsupported format vs corrupt archive/reference/read/write/fsync/permission | Exact refusal classes | D13 named preconditions/unconverted=2, corrupt_archive/invalid_reference/index verification and filesystem I/O=1; mixed classes select2 if any precondition else1. No canonical/backup mutation, all-session preflight refusal publishes none, never exit0 with refused>0. | B35,B45 |
| D-O3 | Complete index repeat; interrupted temp-write/verification/publication/between-session stages | Idempotence/crash | Unchanged matching index bytes/mtime; safe derived staging, per-session atomic publication, stable tokens/no duplicate content | B36 |
| D-O4 | Existing canonical transcript without baseline; corrupt/mismatched baseline or over64 suffix, then externally repaired while stopped; fresh empty session control | Product missing/fault vs fresh | Messages fixed500, explicit metadata-only detail and live_only attach/controls with history ErrorState/Retry; no lifetime scan/503/preparing/false history completion. New sends do not initialize partial indexes. After repair real snapshot/page clears matching error; valid fresh empty-session initializer remains separate. | B31,B42,B43 |
| D-L1 | Real content read paused after cut capture; concurrent chat/effect append then release; later sweep/rollover | Lock/concurrency | Append completes before blocked read resumes, consistent earlier-cut projection | B32 |
| D-Z1 | Distinct text/parameter/result/credential/raw-token/physical-address/query canaries on success/refusal/abort/index/storage errors; planted raw-query log | Log/privacy | Required sanitized events, no canary; planted leak makes assertion fail | B33 |
| D-U1 | Permissioned anonymized genuine long session, actual host/OS/browser/commit,30 opens/30 normal boundaries and reconnect/clear/keyboard/error/live-only-control lanes | External real acceptance | Adopted envelope + saved screenshots/traces/heap/focus and independent validator; synthetic-only cannot pass. Exact-commit live-only send/approval/Stop is an executed control-reachability claim distinct from history readiness. | B25,B42,B43,B49 |

#### D-GO — Standalone shared-Go command, manifest and cutover

| ID | Input | Boundary type | Expected output | Traces to |
|---|---|---|---|---|
| D-GO1 | Built standalone command; real source/full copy for three sessions40/120/0, including effects/dispositions/cancel/duplicates/room_ref when applicable; first dry-run without backup/receipt. Independently encoded v1 manifest/golden fingerprint and zero-byte/empty/Unicode paths; backup tampering/extra/missing/type/unknown-field cases; interrupted/repeated apply. | Built phases/schema/shared core | Initial dry-run works read-only; manifest producer checks actual complete copied bytes and specified schema, not a success flag. Verified backup+stop apply uses product constructor/projection; real reader plus independent vectors matches. Invalid evidence refuses; valid repeat preserves index byte/mtime. Required native Linux/Windows job logs command build and named tests plus mutation failures, not assumed discovery. | B30,B35,B36,B44 |
| D-GO2 | Unconverted session before normal boot; successful cutover alters offsets/bytes; preserved old backup vs fresh post-cutover backup; conversion failure/half-converted state, source drift, per-session corrupt archive/unsupported format, mixed refusal batch | Conversion/backup order/exits | First format preflight unconverted/unsupported returns2 before backup-dependent checks. Cutover runs only through established product boot, not repair tool. Old backup then source_changed/2, no generation published. Repeat dry-run→fresh verified backup→stop/source equality permits apply; corrupt_archive/invalid_reference/I/O=1; mixed precondition+integrity=2. Failed conversion never proceeds. | B35,B44,B45 |

### Regression Test Requirements

Preserve unrelated assertions. Only explicitly changed array/header/full-history/interim-note and **history-only live_only degradation** contracts receive replacement assertions, never skipped/deleted coverage to manufacture green. No pre-existing release failure is exempt. History-unavailable is not an attach/control-delivery failure; tests must independently prove both outcomes, not delete every no-complete assertion.

| Existing source / behavior | Required protection |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_view_test.go`::TestReadTranscriptView_StartsAtTheLastClearMarker / TestReadTranscriptView_UsesTheLastOfSeveralMarkers / TestReadTranscriptView_OnlyTheExactMarkerCuts | Full helper semantics stay if retained; separate page tests prove bounded cuts/clear crossing. No global full-reader change. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/transcript_effects_test.go`::TestToolCallEffects_AppendOnlyAndMergeRules / TestProjectToolCalls_SurvivesDayRollover | Append-only/full merge invariants unchanged; T04 adds page parity. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/input_disposition_test.go`::TestInputDisposition_LabelsOnlyTheDiscardedEntryAndLeavesArchiveBytes; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/session/cancel_truncation_derive_test.go`::TestReadTranscript_CancelTruncatesOnlyItsTurnsLastAssistantEntry | Cross-page equivalent preserves original derived values/canonical bytes. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/ws_attach_catchup_test.go`::TestAttach_H4_IncrementalWhileStreaming_NoGapNoDuplicate / TestAttach_H6_PersistedBetweenBindAndRead_NoDuplicateNoGap / TestAttach_N4_UnreadableHistory_NeverWipesTheClient / TestAttach_FailedRebuild_NoCatchUpComplete | H4/H6 ordering stays. N4 **no snapshot/wipe** stays; replace its history-only generic error/no-complete expectation intentionally with typed unavailable state + complete{live_only}, bound ordered live frames, no history/seen proof (T13/T31). FailedRebuild's canceled/incomplete attach still requires no complete/unbound/no numbered held frames. Add undeliverable-control negative; never globally relax no-complete into “any failure can succeed.” T24/T35 verify UI control reachability without changing incremental semantics. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/pkg/gateway/clear_view_test.go`::TestGetSessionMessages_StartsAtTheLastClearMarker / TestAttach_SnapshotReplaysSinceTheLastClearMarker | Replace envelope/header expectations, keep exact old/new/marker assertions, add explicit crossing. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.virtualization.test.tsx`::VirtualizedMessageList; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/chat/cursor.test.ts`::gateFrameBySeq / applySnapshotHistoryWipe | Live/plain virtualization and transport semantics stay; historical snapshot protection is an explicit separate branch. DOM count is not heap proof. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.clear-marker.test.tsx`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/components/chat/ChatScreen.earlier-note.test.tsx` | Unknown-row/divider/refusal behavior stays; note-only expectation gets actionable accessible earlier tests. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/routes/_app/sessions.$sessionId.tsx`::SessionRoute / Route.loader; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/store/session/workspaceEntryFlow.ts`::resolvePartialListPointer | T33/T35 prove metadata-only unavailable detail still resolves session identity/attach without old flag writes, false404/welcome or pointer deletion; real404/access semantics stay. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/tests/e2e/chat.spec.ts`::sessionMessages; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/tests/e2e/parent-wake-stream.spec.ts`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/tests/e2e/stop-scope-tree.spec.ts`::mockChatWebSocket | T33 preserves cross-session/full-history absence assertions by explicit multi-page/clear enumeration and generated envelope fixtures; no Array.isArray→[] concealment, latest-suffix-only oracle or weakened Stop assertion. |
| D15 API/header/clear/detail readers and exhaustive chat/store/integration mocks | T33 records full positive-controlled consumer inventory and disposition, generates available/unavailable/budget fixtures and preserves original behavior assertions. Internally normalized Message arrays stay arrays where not wire formats. All affected tests must actually execute in CI. |
| Train3 corrected anchor/room traffic; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/src/lib/ws.attach-replay.test.tsx` and ChatScreen.virtualization/test/e2e sources at C21 | Actual interim source/tests read at da4b75df2; team-lead re-reads newer/landed versions. Preserve overlap A→B→A, refused/replay-error/disconnect handling, pending approval/secret and upward-intent bottom-follow tests. Add live_only release without false history flag and one historical batch/no passive-bottom-jump; room_ref parity remains. No execution claimed. |

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
| FR-006 | MUST preserve full incremental journal/bind-hold-live/current-control semantics and D7's historical-reader revalidation. Incomplete socket/control catch-up never completes; classified history-only failure follows D7a's explicit live_only/control completion, never a successful history snapshot, attention seen or history-derived restart sweep. |
| FR-007 | MUST use pixel prefetch, measured drawn-row average/80px seed, A-driven unmeasured virtual estimates and request clamp20..100/default40. |
| FR-008 | MUST preserve current anchor across prepend/control/late-height/live growth with stable keys/signed correction; plain fallback usable. |
| FR-009 | MUST coalesce/fence/abort obsolete generations and keep history mutation separate from live cursor/current control state. |
| FR-010 | MUST expose action-specific ErrorState/Retry, reject contradictory page metadata but accept consistent positive advertised budgets, preserve current rows/live use and stop at actual terminal history. D7/D8 refresh failure pauses prefetch with cached/out-of-date/Retry state; partial/full expiry has exact copy/anchor/focus/explicit Return state, never passive latest or whole-session-empty inference. |
| FR-012 | MUST retain≤500 active reader groups +≤100 tail groups with whole safe far-edge eviction/reload, release this feature's inactive history/tail/media/query payloads and preserve pending input; FR-021 separately governs the soft per-page bytes/floor. |
| FR-013 | MUST apply actual existing auth/main/session access before bad parameter lookup/bind on both REST and WS, with D11 outcomes. |
| FR-014 | MUST preserve public-field/session isolation and visible per-item degradation, exclude private data and use generated contract types/validators/local per-frame historical ownership only. |
| FR-015 | MUST expose sanitized read/replay/anchor/window measurements and meet the adopted genuine-long-session envelope with instrument controls. |
| FR-016 | MUST build on current base, perform PRE-RED-1 source coordination/re-read, catch up with each landed overlap and gate only landing order; complete DOC-1..4 including the operator runbook, feature gates and exact-commit UAT/independent validation. Final round2 correction stays Draft pending founder approval; no third grill. |
| FR-017 | MUST implement D8 keyboard/focus/status/alert/busy/reduced-motion/zoom/plain-fallback states for earlier/Retry/Return/refetch and avoid announcing old rows as new responses. |
| FR-018 | MUST reconcile actual expiry/partial removals/read races into retained-only lookahead/cursors/context, including D1a's bounded delete/index crash gap even when the next sweep removes0. Known expired fences use token+rank-only tombstones with disclosed disk growth; rich content metadata retires. Windows sharing-busy skips only that present partition/continues/retries later; real permission/corruption remains error and native Windows behavior is tested. |
| FR-019 | MUST ship/test D13's separately built, non-product Go command linking the same session index/projection engine. Initial dry-run precedes backup/stop; apply requires verified full backup, produced/schema-validated manifest, stopped evidence and exact source equality. Include cutover-first/fresh-backup refusal, deterministic exact exits, preflight/no-publication, idempotence/interruption and named native Linux/Windows CI/build plus DOC-4 runbook. Product has no lifetime upgrade/preparation path. |
| FR-020 | MUST capture a consistent immutable cut briefly and release session/append locks before history content I/O; an actual append must progress while a page read is blocked. |
| FR-021 | MUST apply a4-MiB server-default soft projected-page budget subordinate to measured/estimated2-screen floor and whole groups, always return at least one whole eligible group, expose byte/floor/override accounting and top up actual coverage to≈4 screens on open/≈2 new screens on prefetch with one fill owner. Every range-refresh page uses these same rules and continuation, even beyond5 requests. Advertise budget as positive integer, not enum; min_rows is advisory, not authenticated-caller hard-byte protection. Request bounds20..100 remain. |
| FR-022 | MUST implement D1a's canonical-fsync→derived-row-fsync→root/checkpoint publication and single shared-Go reconciler. From valid known baseline only,0 is no-op,1..64 combined unique archive/disposition/absent-rolled facts reconcile once;65/missing/inconsistent refuses without candidate ready-root publication, repeated chunk-walk, canonical trimming or lifetime prefix content read. Saved-tail/overflow/crash/idempotence/concurrent-owner tests are mandatory. |
| FR-023 | MUST allow canonical sends into unavailable-history sessions without suffix-only initialization/coverage advancement or misreporting durable saves. Metadata-only authorized detail and typed live_only attach preserve current send/approval/question/secret/Stop reachability independently of history ErrorState/Retry. Only actual history success clears history-loaded error; no auth bypass, turn cancellation, false seen/restart sweep or user resend. |
| FR-024 | MUST use D4b's one gateway request factory/prepared projector-weigher injected into session selection by REST/detail/WS. Same cut/options choose identical max-carrier weights/range; no session→gateway cycle, duplicate projection, publish side effects, guessed WS estimate,0/full-read fallback or hidden default on failed dependency. Reuse prepared selected output. |
| FR-025 | MUST migrate every D15 real consumer/mock atomically to generated envelope/history_state semantics, including session route/workspace pointers and all-retained-page e2e helpers. Wrong shape must fail rather than become[]/latest-suffix-only proof. Preserve genuine internal arrays and capture positive-controlled exhaustive inventory/dispositions and named CI execution. |

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
| SC-008 | Both founder interviews and both correction dispositions are consistent, all16 round1/all11 round2 findings have locations, no removed target work or third grill. This final correction stays Draft pending founder approval; author asserts no independent review verdict. |
| SC-009 | Separate command is actually built/tested in required native Linux/Windows CI; initial dry-run works without backup/receipt, v1 manifest producer/schema/full-copy verification and cutover/exit classes pass. Apply/repeat/interruption preserve canonical/backup bytes and produce indexes verified through real shared-core reader plus independent vectors. |
| SC-010 | B37–B39 prove the4-MiB default bites only after floor, never bites normal fixture, keeps atomic/advisory100-floor overrides and≈4H/≈2H measured fill with one chain. Record exact cursors/bytes/reasons; generated parsing accepts other positive budgets. B26/B47/B48 also prove budget-limited existing-window continuation>5 requests and exact refresh failure/expiry states. |
| SC-011 | Every D1a crash point and0/1/63/64/65 combined delta passes discrete read/publication assertions;≤64 heals once,65 cannot publish/chunk-walk, no-baseline/error never lifetime-constructs. Post-unlink/next-sweep0 case and native Windows skip/continue/retry are actually executed. |
| SC-012 | Real available/unavailable route/workspace/control paths pass: saved send once, pending current interactions and engine-observed Stop after live_only; no history wipe/false loaded/seen/restart/resend, unauthorized bypass or incomplete-control complete. Exact-commit UAT/independent validation confirms control reachability separately from history readiness. |
| SC-013 | Exhaustive D15 consumer/mock inventory has a migration or documented internal-only disposition for every match, affected tests actually run and whole-history helper assertions traverse all retained pages/clear. Wrong-shape/metadata-only negatives detect fake empty/latest-suffix oracles. |
| SC-014 | Tool-heavy/special-heavy carriers at identical cuts choose identical positions/byte accounting through one prepared gateway meter. Bad/nil/overflow/negative dependency fails; projection-call and publish-side-effect assertions prove one projection/group and prepared output reuse. |

## Traceability Matrix

| Requirement | User Story / Acceptance | BDD | Tests |
|---|---|---|---|
| FR-001 | US1.1–2 | B01,B02,B03,B24,B39 | T01,T04,T12,T16,T28 |
| FR-002 | US1.1,US4.1/4/9 | B01,B18,B21,B22,B46 | T01,T04,T06,T12,T18,T32 |
| FR-003 | US1.2,US2.6–7/10,US3.2,US4.1/2/5 | B02,B17,B19,B22,B26,B27,B34,B48 | T01,T02,T09,T11,T19,T24,T34 |
| FR-004 | US1.2,US2.4,US3.1/3 | B03,B10,B11,B13 | T03,T14,T15,T16 |
| FR-005 | US1.1/4,US4.1 | B05,B18,B24 | T04,T12,T13 |
| FR-006 | US1.3–4/6,US2.6 | B04,B05,B06,B26,B31,B43 | T08,T13,T24,T31 |
| FR-007 | US1.1,US2.1–2 | B01,B07,B39 | T07,T16,T28 |
| FR-008 | US2.2/6/10,US3.2,US4.5 | B08,B09,B17,B26,B48 | T10,T16,T24,T34 |
| FR-009 | US2.1–4/6 | B06,B07,B08,B11,B23,B26,B39 | T07,T08,T14,T15,T24,T28 |
| FR-010 | US1.3,US2.3/7/10,US3.2–3,US4.2/5 | B02,B04,B10,B17,B23,B31,B34,B47,B48 | T01,T13,T15,T21,T24,T34 |
| FR-012 | US2.5/7,US4.4 | B12,B25,B34 | T09,T16,T18,T21,T24 |
| FR-013 | US4.2–3 | B19,B20,B23 | T02,T11,T15,T24,T31 |
| FR-014 | US1.4,US2.3,US4.1–3/7 | B05,B18,B20,B22,B23,B33 | T04,T06,T11,T12,T13,T15,T23 |
| FR-015 | US2.1–2,US4.4/7 | B07,B09,B21,B25,B33 | T06,T07,T10,T18,T23 |
| FR-016 | US1–5 | B09,B13,B25,B30,B44,B51 | T10,T14,T16,T18,T22,T25,T30,T33 + DOC-1..4/dependency/CI/UAT gate |
| FR-017 | US2.7–10 | B28,B29,B34,B47,B48 | T15,T20,T21,T24,T34 |
| FR-018 | US2.6/10,US3.2,US4.5/8/10 | B17,B26,B27,B41,B48,B50 | T01,T19,T24,T29,T34,T36 |
| FR-019 | US1.3,US5.1–6 | B22,B30,B31,B35,B36,B44,B45 | T06,T12,T15,T22,T25,T30 |
| FR-020 | US4.6/10 | B32,B50 | T26,T36 |
| FR-021 | US1.1/5,US2.1–2/6/10,US4.1/2 | B23,B24,B26,B37,B38,B39,B46,B47,B48 | T12,T24,T27,T28,T32,T34 |
| FR-022 | US1.3/6,US4.1/5/8,US5.4 | B22,B40,B41,B42 | T06,T29,T31 |
| FR-023 | US1.3/6,US4.3,US5.4 | B04,B20,B31,B42,B43,B49,B51 | T12,T13,T31,T33,T35 |
| FR-024 | US1.5,US4.1/2/9 | B18,B23,B37,B38,B46 | T12,T27,T32 |
| FR-025 | US1.2/3/6,US2.3,US4.1/2 | B02,B23,B31,B51 | T12,T15,T33,T35 |

Every live FR has BDD/tests; every BDD has at least one live FR/test and numbered story acceptance reference. No removed message-target scenario/requirement/test is hidden in a matrix range. Round1 dispositions below refer to the revised normative sections, not a self-issued review PASS.

## Round 1 disposition

Review input: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/specs/chat-history-paging-spec-review.md` at `7b6cc0884`. This table records corrections/settled founder answers, **not adjudication by the author of their own design**. The completed final round2 review at11d85508f and founder interview are incorporated in the next disposition table; updated cross-references below reflect the final correction.

| Finding | Disposition | Where / rationale |
|---|---|---|
| CRIT-001 | Fixed by founder-directed descope | Decisions R1-Q2, Non-goals, revised US3/D3/Reachability/DOC/TDD/matrix: no message-neighborhood loader/route/copy-link/search producer. Old target-only work removed entirely; clear crossing stays. |
| MAJ-001 | Fixed | D6/D7 exact kept reader/tail/control state, bounded existing-window revalidation/anchor/expiry/failure behavior; US2.6,B26,D-R2b/D-X5,T13/T24,FR-006. |
| MAJ-002 | **Fixed by founder-amended Q4** | Same-round2026-10-21 amendment supersedes the initial row-only answer. D4a adds4-MiB soft budget, modeled2-screen floor/actual top-up, whole-group and floor override, with shape/validation/observability and B37–B39,D-B,T27/T28,FR-021/SC-010.≈4H open/≈2H prefetch remain. No hard cut below floor, no re-raised decision; MAJ-008 stays accepted. |
| MAJ-003 | Fixed | C18/D14 sweep-chain/partial deletion/retained tombstones/read-cut reconciliation, US4.5,B27,D-X1..5,T19/T24,FR-018. Expected expiry is not500; damage still errors. |
| MAJ-004 | Fixed by founder decision; round2 clarification applied | R1-Q3 still forbids lifetime boot/lazy/background rebuild/preparing UI. R2-Q1 permits only known-prefix≤64 suffix completion (D1a); R2-Q2 makes the separate stopped/backed-up deliverable a shared-Go command. D7a keeps metadata/live controls during history error. B30/B31/B35/B36/B40/B42/B43,T22/T25/T29/T31,FR-019/022/023. |
| MAJ-005 | Fixed by simpler ownership choice | D4/D10/Alternatives replace ordinal manifest and REST duplication with generated HistoryEntryOrigin on source frames. Explicitly weigh local owner fields and REST-history/control-only WS; emitter matrix tests in T12/T13. |
| MAJ-006 | Fixed / founder R1-Q1=B | D11 chat-specific parser; D12 current-base work/per-landing catch-up/landing-only gate/PRE-RED-1. C21/D7 now source-read actual interim da4b75df2 (earlier absence superseded); latest pre-RED re-read remains team-lead's duty. T4/D4/D-P1c/FR-005 preserve room_ref. No unlanded room-parser dependency. |
| MAJ-007 | Fixed | D8 exact Button/ErrorState/Skeleton/state/focus policy, US2.8–9,FR-017,B28/B29,D-K1/2,T20/T21 plus T15/T24; target-loading/highlight portion not applicable after descope. |
| MAJ-008 | **Accepted by founder** | R1-Q4/D9 retains500+100 rather than proposed single-window simplification. Explicit inactive-session history/tail/media/query release, D-M1/T09/SC-005. Accepted machinery is not reopened. |
| MIN-001 | Fixed | D5 unmeasured estimate follows A including composite row weights/prefix invalidation; D-G1d and T07/T16 exercise real virtualizer40px-row case, not just algebra. |
| MIN-002 | Fixed | D11 identical REST/WS access-first→limit→bind order; B20/D-V2/T11/T24 include inaccessible main + bad history_limit. |
| MIN-003 | Fixed | D1 captures immutable marks briefly, releases **both** shard/append mutex before content read; C3 acknowledges existing ReadAtRaw limitation. US4.6,B32,D-L1,T26,FR-020 prove real append progresses under slow read. |
| MIN-004 | Fixed | Observability canary/privacy negative control; US4.7,B33,D-Z1,T23/FR-014/015. Required event presence plus content absence, not source-string scan. |
| OBS-001 | Fixed (observation incorporated) | D2/Alternatives explicitly compare new monotonic chat-occurrence number with hash. Stability is only after final canonical bytes; offline Go command refuses conversion/rewrite. No claim every rebuild across historical conversion preserves offsets. |
| OBS-002 | Fixed (observation incorporated) | C19/D8 names catalogued Skeleton for visual pending, one separate polite status, Button for earlier/Return, ErrorState for alert/Retry; generated no local one-off control. |
| OBS-003 | Fixed (observation incorporated) | DOC-2 names a Paged session API subsection where programmatic consumers learn the breaking envelope/parameters/expiry, with canonical-contract reference. |

## Round 2 disposition

Final review input: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266/docs/internal/specs/chat-history-paging-spec-review-round2.md` at `11d85508f`; founder interview dated2026-10-21 answers R2-Q1=A and R2-Q2=A. **“Fixed” below means corrected in this specification**, not implementation/test acceptance or the author judging their own design independently. No third grill is scheduled; any genuine remaining blocker is escalated to the founder.

| Finding | Disposition | Where / concrete correction and planned proof |
|---|---|---|
| MAJ-201 | Fixed by R2-Q1=A | D1a explicit canonical fsync→derived row fsync→root/covered-end order, exact64 combined unique suffix/delete facts, durable-prefix anchor/overflow guard and one recovery owner. Persisted retained catalog detects unlink-before-index even when next removed_count=0;65+/missing/inconsistent refuses, no repeated64-chunk build. B22/B40/B41,D-H1/2,T06/T19/T29,FR-022/SC-011. |
| MAJ-202 | Fixed by R2-Q2=A | D1a/D13 one shared pkg/session constructor/reducer/projection engine, separately built Go command outside product binary/installers. No Python/second projection implementation or gateway constructor. B44,D-GO1,T22/T25/T30,FR-019/SC-009. |
| MAJ-203 | Fixed | D13 concrete private v1 manifest schema+producer/verification, first dry-run without backup/receipt, unconverted/2 then established normal cutover→fresh authoritative backup, stopped-source equality, exact per-session/aggregate exits and required native Linux/Windows history-index-repair job/named steps plus Fly go-tier wiring. B35/B44/B45,D-GO1/2,T22/T25/T30,FR-019,DOC-4. No tool/build/CI run claimed here. |
| MAJ-204 | Fixed by choosing injected-weigher seam | D4b one proposed gateway history_page_projection.go request factory/prepared projector injected into session; same max-carrier meter for REST/detail/WS, one projection/group, prepared output reuse/no side effects, nil/error/overflow/negative failure. No session→gateway import/guessed WS estimate. B46,D-W1,T12/T27/T32,FR-024/SC-014. |
| MAJ-205 | Fixed | D1/D1a canonical sends continue without partial baseline/invalid coverage; D7a/D10 metadata-only detail plus typed history_state and complete{live_only} keep controls/Stop/live binding independent from history failure. No wipe/history-loaded/seen/restart sweep/turn error or auth bypass. B31/B42/B43/B49,D-N1/2,T31/T35,FR-023/SC-012; intentional N4 regression replacement preserves canceled-attach no-complete. |
| MIN-201 | Fixed; trust boundary stated, settled soft-floor choice retained | D4a explicitly says authenticated min_rows is advisory, not geometry proof or hard-byte/denial-of-service defense. B37/D-B4b/T27 exercises min_rows=limit=100 with near8-MiB valid groups, exact height_floor override/bytes and unchanged row cap. No new restriction or reopened Q4. |
| MIN-202 | Fixed | D3/D7 every range page obeys budget/floor and can need>5 requests. B47/B48 exact cached/out-of-date/Retry/prefetch pause and partial/full expiry copy/anchor/focus/explicit Return; D-F1/2,T24/T34,FR-010/018/021. |
| MIN-203 | Fixed | D10 soft_byte_budget integer minimum1, not enum;4194304 remains server default. B23/D-B5,T27/T33 accept consistent different positive budget values and reject nonpositive/contradictory metadata. |
| MIN-204 | Fixed | D2/D14 token+rank-only tombstones, no IDs/time/size/address/rich metadata, accepted lifetime disk growth/no lifetime RAM map. Per-record handles plus native Windows sharing-busy skip-only/continue/next-sweep retry; true permission errors still fail. B50,D-X6,T19/T26/T36,FR-018/SC-011. |
| MIN-205 | Fixed | C11→D15 complete route/workspace/API/whole-history e2e and mock inventory; real available/unavailable/detail/envelope regressions and all-retained-page helper traversal in B51/D-C1/T33/T35,FR-025/SC-013. DOC-4 is the new proposed operator runbook, DOC-2 limited to API/metrics with link. |
| OBS-201 | Incorporated | D5 single per-view/per-direction owner handles opening4H and ordinary2H trigger. B39/D-B3/T28 explicitly assert simultaneous≈2H opening threshold/top-up yields one chain, not duplicate requests. |

## Ambiguity Self-Audit and Clarifications

| Topic | Settled state / no silent assumption |
|---|---|
| Interview authority | Written issue model plus explicit2026-10-21 founder answers, not this author's recommendation, owns product scope/trade-offs. |
| Dependency source | Train3 remains573c3674; interim now available/read at da4b75df2, superseding round1 absence. PRE-RED-1 confirms/re-reads latest revision before RED; only landing order is gated, source/test reads are not executed gates. |
| Metadata lifecycle | R2-Q1 permits exact64 saved-prefix suffix/delete-gap completion, never absent-baseline/lifetime construction. R2-Q2/D13 shared-Go command is a separately built deliverable with manifest producer/schema/CI/runbook, not hidden product migration; real conversion state remains preflight Unknown. |
| Bytes/cache concern | Same-round amended Q4 fixes MAJ-002 with a4-MiB soft budget and2-screen/whole-group precedence; MAJ-008's500+100 policy stays founder-accepted. Performance envelope/atomic overshoot retained. No reopened choice. |
| Reconnect/expiry | D7/D14 exact refresh error/partial/full expiry, continued soft-budget range pages and native Windows busy behavior. D7a history-only error keeps authorized live controls/Stop independent; no false history-ready/seen or passive latest reset. |
| Meter/consumer ownership | D4b gateway owns single injected prepared meter, session owns shared index/reducer, D15 enumerates every consumer/mock at RED. min_rows is explicitly advisory, wire budget is positive integer not constant enum. |
| Review process | Both grills and founder interviews precede this final round2 correction. Status remains Draft pending founder approval; no third grill. Implementation still requires independent gates. Any remaining genuine blocking design dispute goes to the founder; author recuses from adjudicating it. |
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
