# Omnipus — As-Is Architecture (Evidence-Based)

**Amended 2026-10-06 (founder):** this remains a dated source walkthrough, not proof of current implementation. **Stop click 1 / Esc 1 / `/stop` stops this chat's current turn only and opens a 3 s window; A second Stop / Esc / `/stop` within it stops this chat and its whole helper tree. `/cancel` does that immediately. No separate button or offer. `/stop-redirect <instruction>` stops this chat's turn and continues this chat with the instruction, in any root or helper chat.** Same semantics on web, CLI and channels. Plain Stop leaves background shells running; a second Stop / Esc within 3 s, or `/cancel`, kills them. Agent delegate `stop` / `stop_all` is unchanged. Authority: founder decision, 2026-10-06. **[External-worker qualification, U5b 2026-10-09]:** the "any root or helper chat" redirect scope is native-engine behaviour; an external-CLI (subagent_3p) helper chat refuses `/stop-redirect` (`not_steerable`) and steers by interrupt + native-conversation resume, not tool-boundary injection (FR-043). People/agents share `AgentLoop.StopSession` with polite now, force at 3 s and detach 3 s later; no agent grace/cancel_grace/public hard option. Root completion is lifecycle done/completed, not archive/hide; scheduled/heartbeat new rounds may revive completed as system principal but never stopped. Durable delegated injection, exactly-one poll-or-wake final, held stopped-parent results and goal-tail ownership follow [The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](ADR-20260928-sub-agent-control-plane.md)::Amended 2026-10-06. Unrelated older source claims are not silently refreshed by this document-only task.

**Chat-history amendment — 2026-10-23:** Section 2, the session-history row in Section 7, and the memory qualification in Section 8 describe source read on `feature/1266-green-be` at `980a1a2f23f186172d50a742882a4e02a98a03b9`. They replace the old description of chat reads as full transcript/JSONL playback. **Code wins over docs.** This is source verification, not fresh runtime, browser, platform or release acceptance. Current accuracy of unrelated, pre-existing sections is **Unknown**: they were not re-audited in this amendment.

**Original walkthrough date:** 2026-04-27
**Method:** The original source-only walkthrough covered the backend. Its untouched line-number citations are historical, not revalidated current evidence. The chat-history amendment uses absolute `file::symbol` citations to source read for this amendment; anything outside that verified scope is marked **Unknown**, not inferred absent.
**Scope:** Original agent loop, memory & sessions, tools system (incl. MCP and skills), channels & bus; the 2026-10-23 refresh covers the session-history store, gateway carriers, standalone repair command and frontend reader.

---

## 1. Agent Loop

### 1.1 Entry points and turn engine

- `AgentLoop.Run(ctx)` is the top-level dispatcher: an infinite `select` over the message bus that hands each inbound message to `processMessage` — `pkg/agent/loop.go:1037`, `pkg/agent/loop.go:1051`, `pkg/agent/loop.go:1114`.
- A *turn* is the unit of work the loop performs for one inbound message. The turn engine is `AgentLoop.runTurn(ctx, ts *turnState)` — `pkg/agent/loop.go:2701`.
- Turn state (phase, iteration counter, parent/child correlation, cancellation handles) is defined in `pkg/agent/turn.go:49`.

### 1.2 Per-turn control flow

The loop is a classic ReAct-style tool-using loop, implemented inline rather than as a state machine:

1. **Context build** — Load history + summary from `Sessions.GetHistory` / `GetSummary`, then `ContextBuilder.BuildMessages` produces the LLM-ready message list. If the budget is exceeded, compression runs first. `pkg/agent/loop.go:2772-2820`.
2. **User-message persist** — `pkg/agent/loop.go:2823-2835`.
3. **Iterate up to `2 * MaxIterations`** — `pkg/agent/loop.go:2865-2875`. Soft limit is `agent.MaxIterations` (default 20, `pkg/agent/instance.go:140`); hard ceiling is the doubled value.
4. **Model selection** — `selectCandidates` picks primary + fallbacks, optionally routing to a light model via `routing.Router` (`pkg/agent/loop.go:2838`, `pkg/agent/instance.go:183-213`).
5. **LLM call** — `provider.Chat` or `ChatStream`, with a `FallbackChain` retrying across candidates on timeout / context overflow / auth / rate-limit errors. `pkg/agent/loop.go:3134-3191`, `pkg/agent/loop.go:3216-3242`.
6. **Tool dispatch** — for each `response.ToolCalls`:
   - normalize name (`.` → `_`), `pkg/agent/loop.go:3714`
   - run before/approval hooks, `pkg/agent/loop.go:3718-3794`
   - rate-limit check (per-agent-per-minute), `pkg/agent/loop.go:3906-3959`
   - registry execute via `Tools.ExecuteWithContext(...)`, `pkg/agent/loop.go:3966`
   - sanitize untrusted results with `PromptGuard.Sanitize` (web_search, web_fetch, browser_*, read_file), `pkg/agent/loop.go:4079-4128`
   - append result message with `ToolCallID` correlation, `pkg/agent/loop.go:4130-4164`
7. **Termination** — exit when no tool calls remain, when iteration ceiling is hit, when graceful interrupt has been requested, or on hard abort. Final response persisted, summarization optionally fired. `pkg/agent/loop.go:4337-4393`.

### 1.3 Agent construction

- `AgentInstance` (`pkg/agent/instance.go:25-64`) is the runtime agent. Fields: `ID`, `Name`, `Model`, `Fallbacks`, `Workspace`, `MaxIterations`, `MaxTokens`, `Temperature`, `ThinkingLevel`, `ContextWindow`, plus injected dependencies `Provider`, `Sessions`, `ContextBuilder`, `Tools`, optional `Router`/`LightProvider`. Constructed by `NewAgentInstance` at `pkg/agent/instance.go:67`.
- `AgentRegistry` (`pkg/agent/registry.go:35`) holds all agents in a normalized-ID map. `GetAgent` (`:88`) and `ResolveRoute` (`:98`) are the lookup paths.
- **Core / Custom differentiation is runtime-thin:** the same `AgentInstance` struct is used for both. The differences are:
  - Core agents (Jim, Ava, Mia, Ray, Max) are seeded with `Locked=true` and have their prompts compiled into the binary via the `prompts` map (`pkg/coreagent/core.go:24-150`, prompts at `:86`, seed at `:109-128`). They receive a seeded `system.*: allow` policy.
  - Custom agents come from `config.Agents.List` and use the same pipeline with no identity locks. They receive a seeded `system.*: deny` default policy.
  - There is no separate "system agent" type at runtime. The old `ScopeSystem` / `IsSystemAgent` distinction has been replaced by the per-agent `ToolPolicyCfg` filter (see §3.2).

### 1.4 Provider abstraction

- `LLMProvider` (`pkg/providers/types.go:24-52`): `Chat(ctx, messages, tools, model, options) (*LLMResponse, error)` + `GetDefaultModel()`.
- Optional capabilities expressed as separate interfaces: `StreamingProvider.ChatStream(...)`, `ThinkingCapable.SupportsThinking()`. Detected via type assertion at call sites (`pkg/agent/loop.go:3062`, `:3172`).
- Concrete providers: `claude_provider.go`, `factory_provider.go` (OpenAI-compatible incl. OpenRouter), `legacy_provider.go`.

### 1.5 Streaming

- When `StreamingProvider` is implemented and the bus has a streamer, `ChatStream` is invoked with an `onChunk(accumulated)` callback that pushes deltas to `streamer.Update(...)` — `pkg/agent/loop.go:3167-3187`.
- The streamer is finalized once at turn end (`finalizeStreamer`, `pkg/agent/turn.go:305-321`) so a single "done" frame is emitted per turn even when intermediate tool loops continue.

### 1.6 Termination, cancellation, error handling

> **Correction 2026-10-14 — gated idle expiry ([PR #1311](https://github.com/elicify-ai/omnipus/pull/1311)::Decisions recorded, team-lead 2026-10-14, “Idle expiry is gated, not removed”; [issue #1306](https://github.com/elicify-ai/omnipus/issues/1306)).** The earlier assignment to delete idle expiry is superseded: retain the idle policy, setting/overrides, sweeps and expiry outcomes for genuinely wedged/abandoned work, never while the addressed session, its own turn, descendants, queued work or a pending cron-lane dispatch is in flight. A registered wedged turn counts as busy and requires Stop; after all work settles, the unchanged activity clock may already allow expiry on the next eligible sweep. Gate suppression is not new activity or a clock reset. **The per-turn timeout bullet below remains superseded** as a 2026-04-27 observation. At frozen `7c148ca00`, turn contexts are cancel-only; `agents.defaults.timeout_seconds` and the runtime timeout field are removed. Delegated dispatch/re-entry, external-CLI task dispatch, scheduled runs and plan wake/supervision turns install no work-lifetime deadline. Explicit Stop/cancel, provider request/stream-silence guards, command/check limits and the terminal viewer's wait remain separate. The Judge keeps its timers and must conclude visibly with the work result. Original source citations remain pinned to `7c148ca00`: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/no-time-limits-adr/pkg/agent/loop_run_turn.go`::createTurnContext / registerTurnContext; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/no-time-limits-adr/pkg/agent/steer_launcher.go`::runDispatchedSteeredTurn; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/no-time-limits-adr/pkg/agent/external_dispatch.go`::startAndDrain; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/no-time-limits-adr/pkg/gateway/schedules.go`::RunScheduled; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/no-time-limits-adr/pkg/agent/plan_engine.go`::dispatchPlanTurn. The later gated-idle requirement is not proof that this dated walkthrough or a newer integrated runtime has passed acceptance.

- **Iteration**: hard ceiling `2 × MaxIterations` (`pkg/agent/loop.go:2865`).
- **Per-turn timeout**: `context.WithTimeout` from `agent.TimeoutSeconds` (`pkg/agent/instance.go:218-221`, `pkg/agent/loop.go:2709-2714`).
- **Graceful interrupt**: `ts.requestGracefulInterrupt()` (`pkg/agent/turn.go:329`) suppresses tool execution; the next assistant text is treated as final (`pkg/agent/loop.go:3017`, `:3046`).
- **Hard abort**: `ts.requestHardAbort()` (`pkg/agent/turn.go:352`) cancels both `turnCancel` and `providerCancel`, then cascades to all `childTurnIDs` (`pkg/agent/turn.go:505-514`); session is rolled back to a restore point (`pkg/agent/loop.go:4380`).
- **Context overflow**: triggers compression (`pkg/agent/loop.go:2795-2819`); other recoverable errors fall through `FallbackChain`.

### 1.7 Audit and rate limiting

- `audit.Logger` is wired into the tool registry and the loop (`pkg/agent/loop.go:259-289`); policy decisions, prompt-guard mutations, and rate-limit denials are logged. `cfg.Sandbox.AuditLog=false` disables logging but **not** policy enforcement.
- Per-agent sliding-window rate limits — LLM calls per hour and tool calls per minute — are tracked via `rateLimiter.GetOrCreate` (`pkg/agent/loop.go`).

---

## 2. Session storage and bounded chat history

**Evidence scope:** Source-verified at `980a1a2f23f186172d50a742882a4e02a98a03b9`, 2026-10-23. This section describes chat display history, not every product memory or model-context feature. Wider claims about semantic retrieval, tokenization, summaries, daily memory, heartbeat or export facilities are **Unknown** in this refresh; the old negative claims about those features are not carried forward as current facts.

### 2.1 Content authority versus the display reader

The addressed, append-only archive is the content authority. `ArchiveDayStore.Append` writes and syncs the canonical record before returning its address. The history index contains locations, digests, occurrence ranks and correction/reference metadata, **not a second copy of message bodies**. The gateway reads selected source groups through `ReadChatHistoryPage`, not a lifetime transcript scan.

```text
canonical archive + input dispositions
                |
       bounded reconciliation
                v
     derived checkpoint + ordered tree
                |
       pinned, immutable read cut
                v
  selected groups + bounded dependencies
                |
     shared gateway projector / weigher
                v
     REST page or WS history snapshot
```

`ReadTranscriptView` still exists: it reads the merged transcript with dispositions, limits it to the segment since the last clear, and removes saved inputs. Its continued existence does **not** mean the paged messages/detail/snapshot path uses that full-read helper.

Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/archive_day_store.go::ArchiveDayStore.Append`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_storage.go::historyRecord`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/transcript_view.go::ReadTranscriptView`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_projection.go::historyPageRequest.Read`.

### 2.2 Derived per-session index and publication

For an indexed canonical session, the private index directory is named by `ChatHistoryIndexDirectory`, currently format version 1. The store resolves its format marker, checkpoint, generation journals and staging area from that session's directory. This is not the former per-agent context-store layout.

| Component | Implemented role |
|---|---|
| Checkpoint | `historyCheckpoint` records format/session ownership, publication and retention generations, journal file, root, committed mutation end, maximum rank, covered archive ends, disposition end and source anchor. Loading rejects a missing/null root and a nonempty rank with root zero rather than fabricating empty history. |
| Ordered tree | Copy-on-write nodes preserve old roots for readers. Node and commit frames use `CHI1` framing, a bounded length and SHA-256 checksum; decoding validates the frame variant. |
| Publication | After canonical acceptance, `historyPublish` flushes derived nodes, writes the transaction footer, syncs the generation file, then publishes the checkpoint/root. An accepted canonical save and derived readiness are separate outcomes. |
| New identity | `initEmptyChatHistoryIndexLocked` publishes an empty baseline for the new-identity path. An interrupted creation can resume only through verified empty, unpublished state; existing content is not replaced by an invented empty index. |

Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_index.go::ChatHistoryIndexDirectory`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_storage.go::historyCheckpoint`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_storage.go::historyLoadCheckpoint`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_storage.go::historyCreateTree`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_tree.go::chatHistoryIndexTree.set`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_tree.go::chatHistoryIndexTree.readFrame`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_storage.go::historyPublish`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_index.go::initEmptyChatHistoryIndexLocked`.

### 2.3 Online recovery and lock boundaries

Online reconciliation starts from a valid checkpoint and collects **at most 64 combined archive, disposition and expired-partition mutations**. Item 65 returns typed unavailable history with `catchup_limit`; it is not processed as another batch of a larger historical rebuild. Missing or inconsistent baseline state is not a successful empty conversation.

The per-session `chatHistoryOwner.mu` serializes reconciliation **across its I/O**. Within that ownership, reconciliation briefly takes the session shard and archive append mutex to capture source ends, releases those capture locks before reading suffix content, and takes the session shard again for publication. Canonical acceptance callbacks use a nonblocking `TryLock`: they do not wait behind that owner while retaining the session shard. A skipped derived update leaves a suffix for the bounded reconciler.

Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_reconcile.go::historyCollectMutations`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_reconcile.go::reconcileHistoryOwned`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_index.go::reconcileChatHistoryIndex`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_index.go::historyAccepted`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_index.go::historyDispositionAccepted`.

### 2.4 Read cuts, retention generations and cleanup errors

The page reader captures a checkpoint, opens its tree and pins that generation under the session shard, then releases the shard. Selection, canonical-body reads and the injected gateway weigher run against that immutable cut without retaining the session shard or archive append mutex. Selection keeps source groups whole, seeks by index metadata and passes each candidate page to the real carrier weigher; it caps a page at 100 source groups. The byte budget is soft: the row floor or an indivisible group can override it, with the override recorded in page metadata.

| Boundary | Implemented behavior |
|---|---|
| Retention publication | `historyCompact` creates a fresh generation, removes rich metadata for expired partitions and preserves opaque position-to-rank tombstones. The checkpoint's retention generation lets a reader detect that its captured content cut moved. |
| Retention caller | `RetentionSweep` expires rolled archive content through `expireArchiveSessions`, then releases all session shards before `reconcileExistingChatHistory`, including a zero-removal pass. |
| Reselect bound | A moved retention cut reclassifies that attempt's selected/lookahead/dependency reads as discarded and increments retries. There are **three attempts total: initial selection plus at most two reselects**. A third moved cut returns typed unavailable history with existing reason `index_inconsistent`. |
| Pins and retirement | Old rich generation files remain while readers pin them. Attempt-scoped defers close the tree handle before releasing its pin, including error and panic unwinding. |
| Release failure | The last-reader release callback returns errors. A real removal failure remains pending and surfaces to the page caller as typed `storage_error`, even if retention also moved. Normal reconciliation retries pending retirement, including zero-mutation reads; an already absent file is success. |

Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_select.go::readChatHistoryPage`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_select.go::historySelect`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_compact.go::historyCompact`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/retention_sweep.go::RetentionSweep`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_storage.go::chatHistoryOwner.retain`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_storage.go::chatHistoryOwner.retire`.

### 2.5 Generated REST page and session-detail union

`newHistoryPageRequest` is the shared prepared reader/projector/weigher for REST messages, session detail and snapshot replay. It measures the actual REST and historical-WebSocket encodings and uses the larger carrier size, with memoization scoped to a request/cut. The messages handler performs addressed-session access checks before strict history-query validation, and writes the prepared, validated envelope rather than a raw message array or legacy paging headers.

| Carrier | Implemented shape |
|---|---|
| Messages | Generated `SessionHistoryResponse`: entries, page information, earlier/later flags, optional before/after continuations, session identity and optional recorded absolute token total. |
| Detail: available | Generated `SessionDetail` with `history_state.status = available`, authorized session metadata, messages and page fields. |
| Detail: unavailable | The same generated carrier with `history_state.status = unavailable`; authorized session metadata remains, but messages, page information and paging fields are omitted. This is not an empty successful page. |

The available/unavailable distinction is a **semantic union**, enforced by the detail writer's field construction and runtime schema validation; it is not a claim that the generated Go carrier is a language-level discriminated union.

Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_projection.go::newHistoryPageRequest`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_projection.go::historyPageRequest.prepare`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_http.go::serveSessionHistory`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_http.go::writePagedSessionDetail`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/api/generated/openapi_types.gen.go::SessionHistoryResponse`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/api/generated/openapi_types.gen.go::SessionDetail`.

### 2.6 WebSocket attach, live-only recovery and source ownership

Attach resolves the addressed session and checks access before validating history sizing, then binds the connection with live frames held. Snapshot preparation must succeed **before** a snapshot frame can tell the client to replace history.

```text
success: snapshot metadata -> current state -> historical frames / replay done
         -> uncovered current projection -> catch_up_complete(snapshot)
         -> held live frames

history unavailable: current state(unavailable) -> current projection
                     -> catch_up_complete(live_only) -> held live frames
                     [no snapshot or history wipe]
```

`live_only` does not claim history loaded or acknowledge attention as successful history delivery. A servable incremental journal takes its own complete-tail path; page sizing does not truncate that journal. Cancellation or delivery failure does not produce successful completion.

For historical entry-derived frames, `prepareGroup` creates a generated `HistoryEntryOrigin` from the actual source occurrence. `stampHistoryOrigin` applies it to the supported typed message, tool, error and media frame variants. Session-only completion or current-projection cards do not receive an invented historical source owner. Historical replay `done` is a diagnostic boundary, not a fresh live-turn completion or fresh absolute usage observation.

Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/websocket_replay.go::handleAttachSessionWithAck`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/websocket_replay.go::sendIncremental`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_websocket.go::sendPreparedHistorySnapshot`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_prepare.go::historyPageRequest.prepareGroup`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_replay_entry.go::stampHistoryOrigin`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_projection.go::historyPageRequest.prepare`.

### 2.7 Content-free page diagnostics

An authorized page attempt has one `history_page` summary. Its explicit fields describe carrier/mode/outcome, requested sizing, projected counts/bytes, selected/lookahead/dependency/recovery/discarded reads, index/disposition reads, retention retries and successful-page budget/retention metadata. The summary contains no message bodies, tool arguments, session/message identifiers, history positions, filesystem paths or raw storage causes. This statement applies to that summary, **not every gateway log**. `preparationComplete` freezes its duration before response/control delivery can block.

Source: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_diagnostics.go::historyPageAttempt.finish`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_diagnostics.go::historyPageAttempt.preparationComplete`.

### 2.8 Standalone offline repair, not a product-binary fallback

The repair program has its own `main` and an explicit operator-only Makefile target, `history-index-repair`. It is **not shipped inside the Omnipus application binary**, nor a dependency of the normal build/install/release targets. The product entry point separately calls `app.Main`.

Repair supports dry-run, apply and verify. `prepareAllSessions` invokes the shared `PrepareChatHistoryIndex` builder; the offline builder can traverse retained canonical input rather than obeying the online 64-mutation recovery limit. It uses the same archive/disposition reducers. The command plans all sessions before publication and invokes the shared index verifier afterward.

Apply prerequisites are **check-only**, not an exclusion lock: `repairGuards` checks stopped-instance evidence, backup/source validity and stopped evidence again, with checks around publication and at the end. They do not guarantee that another process cannot start after a check; that residual race remains. A late guard/publication failure can follow an already-published derived checkpoint. The command reports the failure and actual publication counts; it does not automatically restore the backup or retry the operation.

Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/cmd/chat-history-index-repair/main.go::main`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/Makefile::history-index-repair`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/cmd/omnipus/main.go::main`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/cmd/chat-history-index-repair/plan.go::prepareAllSessions`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_build.go::PrepareChatHistoryIndex`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/cmd/chat-history-index-repair/plan.go::repairGuards`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/cmd/chat-history-index-repair/evidence.go::verifyStoppedEvidence`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/cmd/chat-history-index-repair/plan.go::runRepairPlan`.

### 2.9 Frontend reader, live tail and inactive-body release

`createHistorySlice` owns latest/older/newer/window-refresh actions. Each asynchronous action captures a view generation, an `AbortController` and its own baseline/staging; it checks ownership before committing. Refresh is bounded to the saved reader interval and 500 positions, with continuation/progress and retention-generation checks.

| State boundary | Implemented behavior |
|---|---|
| Reader window | `applyHistoryPageToBucket` merges by source position and trims the committed reader interval to **500 source positions**, dropping the opposite edge. Positions are not sorted lexicographically. If trimming would remove protected positions, this source version returns no patch; a later successful deferred commit is **Unknown**, not established by that branch. |
| Recent/live window | A latest page seeds up to **100** recent entries. While viewing historical messages, `detachedLivePatch` keeps the distant live tail separate from reader order and shares a 100-entry allowance between hidden live owners and cached recent entries. Pending/current execution state is handled separately; 500 + 100 is not a claim that every client object is globally capped at 600. |
| Body ownership | `historyRowsPatch` replaces message bodies and prunes associated tool, text, alias and span-owner maps to retained rows rather than keeping every visited history body. |
| Inactive session | Foreground switching calls `releaseSessionHistory` for the previous session. It aborts reads, clears staging/protection and messages/detail query caches, retains necessary pending/current owners, empties the history window, advances its view generation and requires a new snapshot. |
| Rendering | `VirtualizedMessageList` in the chat message-list module uses the virtualized renderer when available and the plain-list fallback without `ResizeObserver`. Both use the history viewport hook and older/newer boundary components; virtualization alone is not the body-memory policy. |

Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/src/store/chat/history.ts::createHistorySlice`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/src/store/chat/history-merge.ts::applyHistoryPageToBucket`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/src/store/chat/history-live.ts::detachedLivePatch`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/src/store/chat/history-state.ts::historyRowsPatch`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/src/store/chat.ts::registerSyncChatForeground`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/src/components/chat/ChatMessageList.tsx::VirtualizedMessageList`.

### 2.10 Verification limits

This amendment verifies implementation seams by reading source. Fresh execution of repair on every supported operating system, built-UI acceptance, independent reviewer closure and whole-release readiness are **Unknown in this documentation task**. The held session-list usage-contract/projection path is not changed or certified here.

---

## 3. Tools System

### 3.1 The `Tool` interface and registry

```go
// pkg/tools/base.go:22-30
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any
    Execute(ctx context.Context, args map[string]any) *ToolResult
    Scope() ToolScope
    RequiresAdminAsk() bool   // added by central tool registry redesign
    Category() ToolCategory   // added by central tool registry redesign
}
```

The central tool registry redesign (completed; see `docs/internal/specs/tool-registry-redesign-spec.md`) replaced the old per-agent `ToolRegistry` instances with **two shared registries**:

- **`BuiltinRegistry`** (`pkg/tools/builtin_registry.go:42`) — a single shared `map[string]Tool` (RWMutex-guarded) populated once at boot by `registerSharedTools` (`pkg/agent/loop.go:677`). All 35 native builtins live here, including the `system.*` group.
- **`MCPRegistry`** (`pkg/tools/mcp_registry.go:73`) — a separate dynamic registry populated by `MCPRegistry.RegisterServerTools` / `RegisterServerToolsWithOpts` as MCP servers connect. Eviction runs on disconnect (`EvictServer`, `:271`). Name-collision detection against builtins happens at registration time.

Registration is still explicit — `registerSharedTools` calls `New*Tool()` constructors — but each call is a one-time boot-time operation against the shared `BuiltinRegistry`, not per-agent.

- **Hidden tools have a TTL**; core tools persist (`pkg/tools/registry.go:141-149`). MCP tools begin visible immediately on `MCPRegistry`; skills promote them via `PromoteTools` (TTL=1).
- **Async tools**: `AsyncExecutor` interface (`pkg/tools/types.go:133-139`); `ExecuteWithContext` detects and dispatches async tools with a callback.

### 3.2 Central registry: per-agent policy filter

The `FilterToolsByPolicy` function (`pkg/tools/compositor.go:143`) is the primary runtime filter applied **before each LLM call**. It resolves:

```
{BuiltinRegistry.All()} ∪ {MCPRegistry.All()}
    → scope gate (ScopeCore blocks custom agents from core-only tools)
    → policy filter (GlobalPolicies then per-agent Policies map; deny > ask > allow)
    → tools[] sent to model
```

Per-agent policy lives in a `ToolPolicyCfg` struct (`pkg/tools/compositor.go:109`) with `Policies map[string]string`, `DefaultPolicy`, `GlobalPolicies`, `GlobalDefaultPolicy`. Each `AgentInstance` holds an `atomic.Pointer[tools.ToolPolicyCfg]` (`pkg/agent/instance.go:66`) updated hot by `ReloadProviderAndConfig` without rebuilding the registry.

**Admin-ask fence** (`pkg/policy/admin_ask_fence.go:56` — `ApplyAdminAskFence`): tools whose `RequiresAdminAsk()` returns `true` (all `system.*` tools) route through an approval state machine in `pkg/gateway/approvals.go`. `ApprovalState` transitions: `pending → approved | denied_*` (`approvals.go:37-65`). The agent loop pauses at the fence, the gateway emits `tool_approval_required` over the WebSocket bus, and execution resumes or a `permission_denied` result is injected into the LLM context.

### 3.3 Tool catalog and JSON schema

- Tool catalog is derived at runtime from `BuiltinRegistry.Describe()` (`pkg/tools/builtin_registry.go:112`), replacing the deleted static `builtinCatalog` slice (`pkg/tools/catalog.go`).
- File implementations include: `shell.go`, `filesystem.go`, `web.go`, `browser/`, `send_file.go`, `edit.go`, `handoff.go`, `message.go`, `mcp_tool.go`, `build_static.go`, `cron.go`, `spawn.go`, `subagent.go`, `task.go`, `skills_install.go`, `skills_search.go`, `skills_remove.go`.
- Schema export: `ToolToSchema` (`pkg/tools/types.go:141-150`) emits OpenAI/Anthropic function format `{type:"function", function:{name, description, parameters}}`.

### 3.4 System tools (`pkg/sysagent/tools`)

All 35 `system.*` tools are present and implemented in `pkg/sysagent/tools/`:
- `agent.go` (6), `project.go` (4), `task.go` (4), `channel.go` (5), `skill.go` (4), `mcp.go` (3), `provider.go` (4), `pin.go` (3), `config.go` (2), `diag.go` (4).
- These are **ordinary builtins** registered on the central `BuiltinRegistry` at boot. There is no dedicated `BuildRegistry()` for a "system agent". Per-agent policy (default `system.*: deny` seeded on every new custom agent) governs exposure. Core agents receive `system.*: allow` via their seeded policy.
- `RequiresAdminAsk()` returns `true` for all `system.*` tools, routing them through the admin-ask fence for custom-agent callers.

### 3.5 Permissions / sandboxing wiring

- Per-agent filtering runs via `FilterToolsByPolicy` (`pkg/tools/compositor.go:143`) at LLM-call assembly time, not at registry build time.
- Sandbox enforcement (Landlock/seccomp) is set up in the boot path at `pkg/agent/loop.go:672+`, independently of the tool filter.

### 3.6 Plugin loading — what's actually there

- **No `plugin.Open`** for `.so`/`.dll`.
- **No subprocess execution** of tools (the only `exec.Command` in channel/tool code is Weixin's SILK voice transcoder, a media codec — `pkg/channels/weixin/media.go`).
- **No RPC tool transport.**
- The only out-of-process extension is **MCP** (see §3.7).

---

## 4. MCP

- `pkg/mcp/manager.go:100-114` — `Manager` holds `servers map[string]*ServerConnection`; each `ServerConnection` wraps an `*mcp.Client`, a `*mcp.ClientSession`, and a discovered `[]*mcp.Tool` list.
- **Configuration**: `MCPServerConfig` with `Name`, `URL`, `Command`, `Args`, `Headers`, `EnvFile`, `Enabled` (`pkg/mcp/manager.go:124-237`). Transport is auto-detected: `Command` → stdio, `URL` → HTTP/SSE.
- **Lifecycle**: `LoadFromConfig` connects all enabled servers concurrently → `ConnectServer` initializes a session → `GetAllTools` returns `map[serverName][]*mcp.Tool` → `CallTool(server, tool, args)` invokes (`pkg/mcp/manager.go:239-330+`).
- **Unification with the native registry**: `MCPTool` wrapper (`pkg/tools/mcp_tool.go`) implements `Tool` with name `serverName:toolName` (sanitized, `:57-101`). At compose time, native tools and MCP tools merge under one registry (`pkg/tools/compositor.go:30-75`); on name collision, MCP wins. MCP tools enter as **hidden** until a skill's `allowed-tools` promotes them via TTL.

---

## 5. Skills

- **Format**: `SKILL.md` with YAML/JSON frontmatter + Markdown body. Frontmatter fields: `name`, `description`, `argument-hint`, `context`, `allowed-tools`, `model-hint`, `extra:*` (`pkg/skills/loader.go:28-39`).
- **Discovery**: `SkillsLoader` (`pkg/skills/loader.go:99-187`) scans, in priority order: workspace `{workspace}/skills/`, global `~/.omnipus/skills/`, then compiled-in builtins. `ListSkills` walks directories for `SKILL.md`.
- **Skills are not tools.** They are prompt-fragments + a declared tool allow-list. `DiscoverAllTools` (`pkg/skills/discovery.go:16-44`) extracts the `allowed-tools` field across loaded skills; the compositor then registers (or unhides) those tools subject to policy.
- **Registry**: `SkillRegistry` interface with `Search`, `GetSkillMeta`, `DownloadAndInstall` (`pkg/skills/registry.go:49-117`). `RegistryManager` aggregates registry sources; ClawHub is the only implementation (REST + hash verification, surfaces `IsMalwareBlocked` / `IsSuspicious` / `Verified` flags).
- **Promotion to runtime**: hidden tools declared by a skill are promoted via `PromoteTools` (TTL=1) so they become visible to the LLM only when the skill is active.

---

## 6. Channels & Bus

### 6.1 The `Channel` interface

```go
// pkg/channels/base.go:47-56
type Channel interface {
    Name() string
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Send(ctx context.Context, msg bus.OutboundMessage) error
    IsRunning() bool
    IsAllowed(senderID string) bool
    IsAllowedSender(sender bus.SenderInfo) bool
    ReasoningChannelID() string
}
```

Capability extensions are opt-in interfaces: `TypingCapable`, `MessageEditor`, `MessageDeleter`, `ReactionCapable`, `PlaceholderCapable`, `StreamingCapable`, `CommandRegistrarCapable` (`pkg/channels/interfaces.go:13-70`). Detected via type assertion.

### 6.2 Implementations present

- **In-process Go channels**: telegram, discord, feishu, matrix, line, qq, dingtalk, slack, irc, wecom, weixin, googlechat, onebot, maixcam, whatsapp_native.
- **External bridge**: whatsapp (WebSocket to a separate process, configured via `BridgeURL` — `pkg/channels/whatsapp/whatsapp.go:31-46`).
- All are compiled into the binary; the WhatsApp bridge is a config-driven WebSocket client, not a generalized `BridgeAdapter` type. **No separate `BridgeAdapter` interface exists in the code.**

### 6.3 Registration pattern

```go
// pkg/channels/registry.go (paraphrased)
type ChannelFactory func(*config.Config, credentials.SecretBundle, *bus.MessageBus) (Channel, error)
func RegisterFactory(name string, f ChannelFactory) // sync.Mutex-guarded map
```

Each subpackage calls `RegisterFactory` from its `init.go`:

```go
// pkg/channels/telegram/init.go:10-16
func init() {
    channels.RegisterFactory("telegram", func(...) (channels.Channel, error) {
        return NewTelegramChannel(cfg, secrets, b)
    })
}
```

This is **compile-time open** (anyone implementing the interface and importing the package gets registered), but **runtime closed** (no plugin load, no `.so`, no subprocess discovery).

### 6.4 Activation is hardcoded

`Manager.initChannels()` (`pkg/channels/manager.go:433-530`) is a **fixed if-ladder over typed config fields**:

```go
if channels.Telegram.Enabled && channels.Telegram.TokenRef != "" {
    m.initChannel("telegram", "Telegram")
}
// ... 16 more branches, including the WhatsApp Native vs Bridge fork
```

Each branch references a hand-written field in `ChannelsConfig` (`pkg/config/config.go:673-690`). Adding a new channel requires editing both `ChannelsConfig` and `initChannels()` — even though the factory map itself would accept a new name without changes.

### 6.5 Bus model

- `MessageBus` (`pkg/bus/bus.go:33-44`) carries three buffered Go channels: `inbound`, `outbound`, `outboundMedia` (default buffer 64, `:15`).
- Methods: `PublishInbound` / `PublishOutbound` / `PublishOutboundMedia` and matching read-only chan accessors (`pkg/bus/bus.go:79-99`).
- Topic model: each message has a `Channel` field (`pkg/bus/types.go:31, 45, 62`); routing by name happens in `Manager.dispatchOutbound` (`pkg/channels/manager.go:1050-1070`), which forwards to per-channel worker queues (`runWorker` at `:900+`).

### 6.6 End-to-end flow

```
Channel.HandleMessage  →  bus.PublishInbound
            ↓
AgentLoop.Run reads bus.InboundChan()
            ↓
runTurn → provider.Chat / tools / runTurn iterates
            ↓
agent publishes via bus.PublishOutbound
            ↓
Manager.dispatchOutbound → worker queue → Channel.Send
```

Channels publish *into* and the manager consumes *from* the bus — channels themselves do not subscribe directly (`pkg/channels/base.go:232-315`, `pkg/channels/manager.go:1050-1070`, `pkg/agent/loop.go:1058`).

### 6.7 Routing

`pkg/routing/router.go:27-82` is **model-tier routing**, not agent routing. `SelectModel(msg, history, primaryModel)` returns either the primary or a light model based on a complexity classifier. Agent selection lives in `AgentLoop.processMessage` (which agent owns this session/inbound).

---

## 7. Cross-cutting summary table

Only the session-history row was refreshed on 2026-10-23; current accuracy of the other historical rows is **Unknown** in this amendment.

| Subsystem | Interface | Registry | Registration site | Runtime extension? |
|---|---|---|---|---|
| Tools (native) | `tools.Tool` | `tools.BuiltinRegistry` (shared, mutex map) | One-time boot in `registerSharedTools`; per-agent filtered by `FilterToolsByPolicy` | No (compile-in only) |
| Tools (MCP) | `tools.Tool` via `MCPTool` wrapper | `tools.MCPRegistry` (dynamic) | `MCPRegistry.RegisterServerTools` on connect; `EvictServer` on disconnect | **Yes — config-driven, stdio or HTTP/SSE subprocess** |
| Skills | `skills.SkillRegistry` (for sources) | `RegistryManager` + filesystem scan | `SkillsLoader` scans 3 dirs | **Yes — filesystem drop-in + ClawHub install** |
| Channels | `channels.Channel` (+ capability ifaces) | Factory map (`RegisterFactory`) | `func init()` per subpackage | No (also requires editing `ChannelsConfig` + `initChannels()` switch) |
| Session chat history (2026-10-23 source refresh) | `ReadChatHistoryPage`; generated REST/detail/WS carriers | Private per-session derived index over the canonical archive | Shared gateway page request and frontend history slice; sources in Section 2 | Compiled implementation; broader memory extension behavior **Unknown** |
| Providers | `providers.LLMProvider` (+ optional capability ifaces) | n/a (selected by name in agent config) | Hardcoded in factory | No |

---

## 8. Historical gap claims — current status qualified

These original observations are not a current absence audit. Except for the chat-history correction below, their current status is **Unknown** in the 2026-10-23 amendment.

1. **Superseded for chat reads:** the original “only linear JSONL playback” description is false for the bounded indexed REST/detail/snapshot reader described and source-cited in Section 2. The broader availability or absence of vector/semantic memory is **Unknown** here.
2. No provider-specific tokenization — single chars/token heuristic (`pkg/agent/context_budget.go:89-131`).
3. `memory/daily/` directory is provisioned but unused (`pkg/datamodel/init.go:140-172` vs. no writers).
4. No generalized `BridgeAdapter` type for external channels — WhatsApp encodes the bridge directly (`pkg/channels/whatsapp/whatsapp.go:31-46`).
5. No runtime plugin loading anywhere (no `plugin.Open`, no subprocess channels, no subprocess tools other than MCP servers).
6. Channel activation requires editing two files (`pkg/config/config.go` + `pkg/channels/manager.go:initChannels`) on top of `RegisterFactory`.
7. Streaming is opt-in per provider; non-streaming providers degrade silently to single-shot responses.
8. Per-turn iteration ceiling is hardcoded as `2 × MaxIterations` (`pkg/agent/loop.go:2865`); the soft limit is the only config knob.

---

## 9. Central tool registry redesign — implementation note

The central tool registry redesign (spec: `docs/internal/specs/tool-registry-redesign-spec.md`, revision 6) is **fully implemented** as of the `feature/iframe-preview-tier13` branch. Key seams:

| File | Role |
|------|------|
| `pkg/tools/builtin_registry.go` | `BuiltinRegistry` — shared, immutable-after-boot catalog of all native tools |
| `pkg/tools/mcp_registry.go` | `MCPRegistry` — dynamic MCP tool catalog; collision-checked against builtins |
| `pkg/tools/compositor.go::FilterToolsByPolicy` | Per-agent filter applied before each LLM call; logic preserved from pre-redesign |
| `pkg/agent/instance.go:66` | `atomic.Pointer[tools.ToolPolicyCfg]` — hot-swappable policy per agent |
| `pkg/gateway/approvals.go` | Approval state machine (`ApprovalState` transitions) for `ask`-policy tools |
| `pkg/policy/admin_ask_fence.go` | `ApplyAdminAskFence` — enforces admin confirmation for `RequiresAdminAsk()` tools |

Pre-redesign symbols `WireSystemTools`, `WireAvaAgentTools`, `ScopeSystem`, `IsSystemAgent`, `ComposeAndRegister`, and the static `builtinCatalog` slice are all removed. The `omnipus-system` agent ID is fictional and has no runtime representation.

---

## 10. Contract-first wire-format pipeline

Hard-constraint #8 makes every byte that crosses the gateway/SPA boundary schema-defined. The pipeline is fully wired as of Phase 7. This section describes the implementation seams; the design decisions behind it live in ADR-012, ADR-013, ADR-014, and ADR-015.

### Spec source of truth

| File | Role |
|---|---|
| `contracts/openapi.yaml` | REST endpoints, pinned to `openapi: 3.0.3` (see ADR-012) |
| `contracts/asyncapi.yaml` | WebSocket frame definitions |
| `contracts/components/schemas/` | Shared JSON Schema component definitions referenced from both OpenAPI and AsyncAPI |

The spec files are the single source of truth. Any wire type that isn't here doesn't exist as far as hard-constraint #8 is concerned.

### Generated artifacts (committed to repo)

| Directory | Tooling | Contents |
|---|---|---|
| `pkg/api/generated/` | `oapi-codegen` | Go request/response types, server interfaces, contract test fixtures |
| `src/lib/api/generated/` | `openapi-typescript` + `openapi-zod-client` + `scripts/_gen-asyncapi-types.mjs` | TypeScript types, Zod schemas for REST responses and WS frames, AsyncAPI Zod schemas concatenated into `schemas.ts` |
| `pkg/gateway/inboundschemas/` | `scripts/gen-contracts.sh` step 5 | Mirror of `contracts/components/schemas/`, embedded via `//go:embed *.yaml` for runtime inbound validation (ADR-013) |

All three are committed to the repo and regenerated by a single codegen entry point. Editing generated files directly is forbidden and will be overwritten on the next run.

### Codegen entry point

```bash
make gen-contracts   # runs scripts/gen-contracts.sh
```

The script lints both specs, regenerates every artifact, and is idempotent — running it twice on a clean tree produces zero diff. CI's `make verify-contracts` job runs codegen and fails if the working tree is dirty afterward.

### SPA edge validation (server → SPA)

Every `request<T>()` call in `src/lib/queryClient.ts` validates the response payload through the matching Zod schema:

- **Success** — payload validates; caller receives the typed object.
- **Failure** — payload is dropped, `_apiSchemaErrorCount` is incremented, and (in dev mode) a toast surfaces the schema-validation error. Production neither crashes nor renders an error UI; the dropped frame counter is the observability hook.

The same edge logic applies to inbound WS frames in `src/lib/ws.ts`: every frame is matched to its `type` field's Zod schema, dropped on failure, and counted via `_droppedFrameCount` / `_unknownFrameTypeCount`. Schema-validation failures throw `ApiSchemaError`, which `queryClient.ts` handles centrally rather than letting individual handlers retry blindly.

### Backend inbound validation (SPA → server)

The backend symmetrically validates inbound traffic when `gateway.validate_inbound: true` (default `false` for v0.1; the flip is unscheduled — the v0.2 target label was retired 2026-09-25 — per ADR-013):

| Direction | Helper | Schema lookup | Failure response |
|---|---|---|---|
| REST request body | `decodeAndValidate(w, r, "SchemaName", &dst, validateEnabled)` (ADR-015) | `pkg/gateway/inboundschemas/<SchemaName>.yaml` via boot-pre-compiled map | `400 Bad Request` with `{error, schema}` JSON envelope |
| WS inbound frame | `ValidateInboundFrameJSON` (Phase 7 fix-AD) | Same embed FS, keyed by frame `type` | Drop + counter increment (WS protocol has no clean error slot) |

`additionalProperties: false` is required on all `*Request.yaml` schemas (ADR-014) so unknown fields produce 400s rather than silent acceptance. Response schemas are closed by default with documented per-case exceptions (`PUT /config` body is the canonical exception).

### Pre-compile guard

`PreCompileAllInboundSchemas()` runs at gateway boot (`pkg/gateway/server.go`). It walks every YAML in the embed FS, compiles each into the runtime validator, and aborts boot on any compile error. The guard runs **unconditionally** — independent of `gateway.validate_inbound` — because a schema that fails to compile is a contract bug we never want to ship past, regardless of whether validation is currently enforcing verdicts.

The compile-failure count is exposed via `InboundSchemaCompileFailures()` for tests and (eventually) `/metrics`.

### Lint enforcement: no hand-written wire types

`scripts/check-no-handwritten-wire-types.sh` runs in CI and blocks two patterns:

1. **Go side** — any non-test, non-generated, package-level struct in `pkg/gateway/*.go` with ≥2 `json:` struct tags. Opt-out: a `// not-wire-format` comment on the type for internal-only structs.
2. **TS side** — any `export interface` or `export type = { ... }` (object literal) in `src/lib/api.ts` or `src/lib/ws.ts`. Opt-out: same `// not-wire-format` marker on the line above for internal callback/helper types.

Together with `make verify-contracts`, this prevents two classes of regression: hand-written Go wire structs that drift from the spec, and hand-written TS types that bypass the Zod safety net.

### Adding a new wire type — five steps

Per hard-constraint #8, the canonical sequence is:

1. Add the schema to `contracts/components/schemas/<TypeName>.yaml`.
2. Reference it from `contracts/openapi.yaml` (REST) and/or `contracts/asyncapi.yaml` (WS).
3. Run `scripts/gen-contracts.sh`.
4. Commit the spec change and the regenerated artifacts in one atomic commit.
5. Write the handler (Go) or consumer (TS) using the generated type only — no hand-rolled parallel struct/interface.

Any other order is a constraint violation and will trip `make verify-contracts` in CI.

### Cross-references

- **ADR-012** — OpenAPI 3.0.3 vs 3.1.0 trade-off (why the spec is pinned to 3.0.3).
- **ADR-013** — inbound validation strategy (opt-in flag, pre-compile boot guard, default-flip target, counter exposure).
- **ADR-014** — `additionalProperties` policy (closed requests, per-case responses, future CI lint).
- **ADR-015** — `decodeAndValidate` pipeline contract (body limits, schema-name string, error response shape, known typo risk and mitigations).

---

## 11. Unified goal / plan / subagent system

> **Added 2026-07-23 (post-original-walkthrough).** The original §1–§10 sweep was scoped to the agent loop, memory, tools, channels. This section records the as-built **unified goal / plan / subagent system** ratified by [ADR-053](ADR-053-unified-goal-plan-subagent.md) (Accepted), built per the [delivery brief](../design/unified-goal-plan-subagent-DELIVERY-GOAL.md) across Phases 0–2 and gated green. Design intent lives in the [v2.2 target design](../design/unified-goal-plan-subagent-target-design-v2.2.html) (marked Implemented); this section is the code-grounded reality and wins on any disagreement.

### 11.1 The thesis — one goal core, three bindings

A goal is `prompt + goal definition + acceptance criteria` (criteria in three kinds: machine / behavior / prose). **One core owns the goal shape, the claim-or-idle trigger, the question→pause, the evidence ladder, feedback steering, typed messaging, the cancel cascade, and the count bounds.** The core is bound three times — chat goal (agent-compiled from user intent, blue goal pill), standalone task (Board/List/Graph), plan Definition-of-Done (plan tile → Graph) — differing only in (a) the deterministic DAG dispatch engine (plan-only) and (b) the UI/data-model binding. Anti-parallel-systems discipline (delivery brief DoD-11): a second goal store, messaging envelope, claim-marker parser, or bounds path is a blocking review finding.

### 11.2 The shared spine — S1–S6 (six built-once seams)

| # | Seam | Built once as | Code |
|---|---|---|---|
| **S1** | Unified goal / criteria record | one schema, authored two ways (chat = agent-compiled; task = explicit) | `pkg/agent/goal_compile.go:265` (`compileGoalIntent` + compile-time feasibility gate, FR-111/D9); `pkg/agent/goal_loop.go:42` (`/goal` command → compile/echo/confirm/amend) |
| **S2** | Durable session record + 8-state lifecycle | one persisted per-entity JSONL record; enum `queued / running / needs_input / paused / completed / failed / cancelled / timed_out` | `pkg/session` (`LifecycleRecord`, `MessageInboxStore`); plan phase `awaiting_owner_correction` at `pkg/plan/plan.go:190` |
| **S3** | SessionMessage envelope family | one inline `oneOf` + discriminator envelope (ADR-034 precedent) over `pkg/bus`'s 4th channel | `pkg/agent/session_messaging_wire.go:61,125,228` (bus consumer, per-agent wiring, content-egress filter) |
| **S4** | Owner ↔ Judge ↔ messaging ↔ plan-engine interlock | the wiring contract: Judge feedback = a `steer`; member telemetry = owner inbox; waiting-on-owner = `question(wait=true)` | `pkg/agent/plan_engine.go:2209` (`AppendCorrection`, the S4 correction handler); `pkg/plan/plan.go:325` (`OwnerSessionID` durable linkage) |
| **S5** | Count bounds | attempts + JudgeRounds | `pkg/config/planning.go` (`EffectiveTaskMaxAttempts`, `EffectivePlanJudgeMaxRounds`) |
| **S6** | Claim / marker family | `[goal:evidence]` + `GOAL_STATUS: met / waiting_on_user` | `pkg/agent/goal_triggers.go:102` (`goalTriggerState`: `bareClaimStreak`, `waitingOnUser`, `idleSettling`) |

### 11.3 Claim-or-idle trigger discipline (replaces after-every-turn)

The shipped chat-goal trigger that re-adjudicated after *every* worker turn (superseded in ADR-052/049 — see both) is replaced by **claim-or-idle**: the Judge fires only on (a) an explicit completion claim (`[goal:evidence]` + `GOAL_STATUS: met`) or (b) event-driven idle settlement (claimless, quiet-window debounced, per goal-id). A `GOAL_STATUS: waiting_on_user` turn **pauses with no verdict and no round burned** (the pill goes amber). The first bare-claim bounce is free (claiming stays cheaper than idling — incentive gradient G-2/G-4). State lives in `pkg/agent/goal_triggers.go:102` (`goalTriggerState`); the idle-settlement vs no-signal-penalty precedence is resolved at the S4 interlock.

### 11.4 The evidence ladder Judge (reused from ADR-052, fail-closed)

The Judge is a real verifier agent in its own session, with a three-rung AND-combined ladder: deterministic machine-check (rung 1) and behavior-scan (rung 2) execute first, prose judgment (rung 3) only if judgment criteria remain. Single reusable entrypoint: `pkg/agent/judge.go:256` (`AgentLoop.JudgeCriteria`), rung ordering + AND-combine at `:300`. It is **fail-closed** (NFR-2): a machine check that could not run returns "unable to verify" and is re-run, never scored as absent evidence (G-3); an unknown criterion kind is a fail-closed unmet (`:294`) that DOES consume the attempt. A blocked check is honestly reported, never hidden.

### 11.5 Session-control plane — typed messaging, isolated message history

Child sessions are **isolated-but-linked**: own durable `SessionID`, a typed `SessionMessage` channel as the only bridge, a curated context snapshot at spawn, with **isolated message history**.

> **Superseded by ADR-057 (verified against code at `d364a5f8`).** This paragraph previously stated that "the transcript *session id* IS shared — a child inherits its parent's `transcriptSessionID` (FR-6a) so the chat-wide `/cancel` cascade can reach sub-turns." **That is now the exact inverse of shipped behaviour**, and the shared-namespace design it described is retired. ADR-057 splits the one overloaded id into **two independent fields**:
>
> | Field | Delegated child gets | Purpose | Code |
> |---|---|---|---|
> | `transcriptSessionID` | its **OWN** (`childID`) — *not* the parent's | persistence: the child owns a real store-backed session and its own `transcript.jsonl`; the parent's transcript is deliberately **empty** of the child's writes | `pkg/agent/subturn.go:1113` (`TranscriptSessionID: childID`) |
> | `routingSessionID` | **inherited verbatim** from the parent, through the whole subtree | cancel/interrupt reachability — this, not the transcript id, is what lets a chat-wide tree-scope stop reach sub-turns | `pkg/agent/subturn.go:1130` (`childTS.routingSessionID = parentTS.routingSessionID`) |
>
> So the cascade guarantee the old wording attributed to a *shared transcript id* is real, but it is carried by `routingSessionID`. `subturn.go:1130` looks like a copy-paste bug and is load-bearing: delete it and every chat-wide **`/cancel` or a second Stop / Esc within 3 s** silently stops reaching delegated sub-turns (plain Stop is self-only; amended 2026-10-06). Defaulting happens at `pkg/agent/turn.go:406` (a root turn's `routingSessionID` = its own transcript id); the full contract is in `routingSessionID`'s doc comment at `turn.go:251-288`.
>
> Consequently the transcript **visibility filter is deleted, not replaced**, at all four former read sites (FR-034), and FR-038 forbids reintroducing one at any read boundary — there is no longer a shared transcript for a filter to act on. See `pkg/gateway/replay.go`'s `streamReplay` contract and `session.TranscriptEntry.ParentSpawnCallID`'s doc comment.

What is isolated is therefore both the message *history* (the child's turn content lives in an ephemeral in-memory store and never persists into the parent's session history) **and** the transcript *namespace*. The bus gained a 4th channel (`bus.SessionMessageChan`); `pkg/agent/session_messaging_wire.go:61` (`SetSessionMessagingStores`) wires the inbox + lifecycle stores, `:125` (`wireSessionMessagingForAgent`) wires per-agent, and `:228` (`filterSessionMessage`) applies the content-egress filter to every free-text/path field of every bus-delivered message. Ad-hoc `delegate` inboxes are keyed to the durable chat/plan id (survives a parent Stop/Play, D16); message ceiling is per-child (D15).

### 11.6 Git evidence layer (go-git, spike-gated GO)

Real per-attempt, write-set-scoped diffs back the Judge via an embedded **go-git** repository at `<workspace>/work/.git` (no cgo; the Phase-0 footprint spike returned GO at +3.04 MiB stripped). `pkg/gitevidence` owns: `Repo.Commit` at `pkg/gitevidence/commit.go:89` (boundary + write-set-scoped commit), `Repo.CheckIntegrity` at `pkg/gitevidence/integrity.go:46` (detects HEAD divergence from the last-known hash), `OpenIsolatedCheckout` at `pkg/gitevidence/isolation.go:141` (the D10 isolation ladder: system-git worktree → go-git clone → subdir), diff evidence (`pkg/gitevidence/diff.go`), and a media size-guard (`pkg/gitevidence/sizeguard.go`). Plan-lint **write-set disjointness** rejects overlapping parallel members **at approve** (not silently): `pkg/plan/lint.go:123` (`Lint`), violation kind `write_set_overlap` at `:49`.

### 11.7 Boot recovery — boot sweep + intent-log replay

A `kill -9` mid-plan no longer wedges: `pkg/agent/plan_engine.go:70` (`PlanEngine.runBootSweep`, FR-118/G-13/INV-9) reconciles every persisted non-terminal session that has no live turn under a bounded budget. Transactional multi-file corrections (append N members + edges + revision entry + plan-record patch across several files) get an all-or-nothing guarantee from the **write-ahead intent-log**, not from per-file temp+rename: `pkg/plan/intent_log.go:469` (`IntentLog.CommitCorrection`) sequences the self-contained intent through AppendIntent → MarkCommitted (fsync, the linearization point) → Apply → MarkDone; `ReplayAtBoot` classifies every intent as discard (uncommitted) / replay-forward (committed-not-done, idempotent) / already-done.

### 11.8 Correction + Play — append-only, SUPERSEDE, targeted retry; resume from last commit

Owner correction (`pkg/agent/plan_engine.go:2209`, `AppendCorrection`) supports three verbs — **append** (tail + revision entry), **supersede** (mark a done member's outcome ignored-by-Judge; the record stays immutable), **targeted retry** (retry a transient/frozen member without a full Stop/Play, D4) — each recording a revision entry, committed transactionally via the intent-log (INV-6/N-8). After commit, append/supersede auto-reset all live-round failed members (excludes frozen/done, G-10); the durable unmet signature is cleared (INV-7); the DoD stays immutable (G-11). **Play = a new `resumed_from` generation** (`pkg/agent/plan_engine.go:2499`, D13/G-12): a cancelled/failed member resumes from its **last git commit** (JudgeRounds reset to 0); a no-commit member falls back to a fresh attempt. Plan members have **no individual start/cancel/resume** (D7) — the plan owns lifecycle.

### 11.9 The 9-action delegate set + message_parent

`delegate` (`pkg/tools/delegate.go`) was expanded from `run | status` to a **9-action set**: `run, status, inbox, inbox_ack, steer, respond, cancel, follow_up, peek` (enum at `:598`). A subagent can now ask its parent a question, report a checkpoint, hand back structured results, and be steered mid-run — delegation is no longer fire-and-collect-only. The child-side counterpart is **`message_parent`** (`pkg/tools/message_parent.go:5`, ADR-053 §5.1) — the first-class tool a child uses to post to its parent's durable inbox (the parent routes `correlation_id`; only a direct session/plan owner asks the human, conversationally in chat — D2). 3P (external-CLI) workers are honest fire-and-collect: `respond` = corrective re-dispatch (a new session), `needs_input`/`question` never advertised to 3P (D5). Delegation depth is configurable with a shipped backstop of 3 (D6).

### 11.10 Sandbox guard — `.git` denied by operation (D17)

Because a go-git repo is a byte-identical `.git` the real `git` CLI could `--amend`, D17 denies `.git` **by operation, not by path**: `pkg/sandbox/gitguard.go` allows `log / blame / show / diff` and denies `commit / amend / rebase / rm` (plus a kernel/Landlock + bash-policy `.git/` block so the bypass through `bash`/exec is denied, not just the tool surface). This is the security-lead Phase-1 dependency that makes the in-scope git layer safe.

### 11.11 As-built deferrals (accepted-with-issue, tracked)

Three deferrals were accepted with tracked issues rather than blocking delivery (zero live callers today; each is safe until its trigger-to-fix): the AppendCorrection **owner-authority gate** (sec-MAJOR-2), **per-member work-tree checkout** for Play-from-commit (the D13 baseline is persisted; the per-member git checkout/restore defers to the D10 worktree-isolation rung), and the intent-log **tamper-evidence/HMAC + dir 0700 + fsync** hardening (sec-MINOR-3). See the issues linked from the delivery brief.
