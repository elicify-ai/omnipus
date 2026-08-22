# Spec — ADR-066: Context overflow — window resolution, per-result cap, empty-in-place, mid-turn window check

- **Source ADR:** `docs/internal/architecture/ADR-066-context-budget-and-tool-result-routing.md` (Proposed, restructured 2026-08-22; pass-2 findings resolved in §16a; amended 2026-08-22 from the spec review — commit `docs(adr): ADR-066 amendments from spec review`). Review records: `…-review-pass2.md` (ADR), `docs/internal/specs/adr-066-context-overflow-spec-review.md` (this spec, verdict BLOCK — every finding resolved in this revision; see §13).
- **Status:** Draft (plan-spec) — revision 2, 2026-08-22, branch `feat/context-budget-and-tool-result-routing`. The ADR is the confirmed requirements brief. Where the ADR was silent the §10 table records the operator's resolutions; every item is closed (A-18/A-19 accepted by the coordinator 2026-08-22; register #3 confirmed).
- **Scope:** ADR-066 only — D2, D3, D4, D5 (+ recall-by-`tool_call_id`), D6, D7, D9, D10, §16a, ADR §15 task 1 (bounding parameters — FR-038…FR-040), and the two **P0 preconditions for D5** added 2026-08-22 — **D5.4 recall injection at the tool-result site** and **D5.5 hydration must not overwrite the archive** (US-14, US-15; both ship as a hotfix first, then this branch inherits them). **D1 (the catalog) is ADR-067 — referenced, not specified.** **D8 is NOT ADOPTED** (ADR-066 commits `ec2e022d`, `80aef474`, `06e6cc17`). Subscriptions / provider deletion / provider UX are ADR-068.
- **Greenfield rule (operator, 2026-08-22):** no backward compatibility, no migration, no aliasing. Pre-existing state that does not match simply does not work: a `config.json` still carrying `summarize_token_percent` or `agents.defaults.context_window` has those keys silently ignored (`LoadConfig` has no `DisallowUnknownFields`) — no boot notice, no rejection; session meta without projection state loads as an empty set (a zero value, not a compatibility path).
- **Cross-spec seams (cross-spec review 2026-08-22, `docs/internal/specs/cross-spec-review-adr-066-067-068.md`):** this spec **requires ADR-067's spec (S67) merged first** — it consumes `pkg/providers/catalog.Resolve(provider, model).Window()`, the `locality` predicate, the `cli_driver` field, and the coordinated contract commit that S67 owns (`Agent.yaml`, the four `LLMError` copies). It depends on ADR-068's spec (S68) only for the UI tail (row/picker refusal state, default-model card). Landing order S67 → S68 → S66 backend → S66 UI tail. Grep gates (tests 6, 11 and S67/S68's) are evaluated on the **merged** branch; S68's removed-provider gate allow-lists the spec/ADR files, but this spec does not rely on that: the deleted id `claude-cli` appears in this document only in this sentence, and in no test literal.
- **Tech:** Go (`pkg/agent`, `pkg/memory`, `pkg/tools`, `pkg/mcp`, `pkg/gateway`, `pkg/providers`) · React 19 + Vite (SPA) · contract-first (`contracts/*`, Constraint #8).
- **Citation rule:** `pkg/agent/loop.go` and `pkg/agent/turn.go` are cited as `file::symbol` only — never by line number.
- **Test conventions:** Go tests run with `-tags goolm,stdjson`; never run the full gateway suite locally (CI is the authority); at most one narrowly-scoped local test (`-run '^TestName$' -p 1`).

---

## 1. Overview

On 2026-08-21 a production turn died silently after two MCP tool results (1.18 MB and 0.82 MB) entered the context in one turn. Four defects were diagnosed (ADR §1): the window was resolved from `max_tokens × 4` (wrong by 8×); the MCP path admits results of any size; the sliding window is consulted only before the first LLM call and can only cut at user-message boundaries; and four turn exits emit no log, event or transcript entry.

This spec covers the incident fix: resolve the window from a ladder — per-agent override → per-(provider, model) operator override → global default → on-demand live query → catalog → floor — with every override clamped to the model's capability and a loud floor for unknown cloud models, ask-or-refuse for local endpoints (D2–D3); admit every tool result through one choke point that caps it per surface **and clamps the cap to half the budget**, bound user messages where they become turns and tool-call arguments as a structured refusal (D4); when the window is over budget, empty the oldest eligible tool results in place with a recall mark that `recall_conversation` resolves by `(tool_call_id, archive line)` in pages (D5); run the one existing budget check after every tool result, never cutting mid-turn, with the whole last assistant step as a floor that is always satisfiable and a thrash guard that is unreachable by construction (D6); give every turn exit a typed code (D7); expose caps, trigger, ingest bound, the global default, model overrides and each agent's effective window with its source in Settings (D9); bound ingest at 8 MB on the transport read and bound the encoded archive line (D10).

Nothing is summarised, nothing is deleted from disk. One new cache file (`$OMNIPUS_HOME/cache/model_limits.json`) is introduced; no new store. `windowTrim` remains the only compaction path (ADR-028, extended not superseded).

---

## 2. Available Reference Patterns

`docs/reference/go-implementation/` does not exist in this repository. **N/A.** Internal patterns reused: the ADR-060 structured tool-failure family (`marshalWithinBudget`, single producer, inline asyncapi schema) for the argument refusal and the recall mark; ADR-028's archive-preserving `TruncateHistory`/`RollbackAppended`; ADR-051's `LLMError` classifier and generated copy catalogue; the `/settings/memory` + `MemorySettings.yaml` / `PerformanceSettingsUpdate.yaml` partial-update pattern for D9; the default-agent `TriggerReload` precedent for settings that must take effect live.

---

## 3. Existing Codebase Context

> GitNexus MCP tools were not exposed in the session that wrote this spec; context is from direct Read/Grep on the branch (the sanctioned fallback). Re-run `impact` on each "modifies" symbol before editing.

### Symbols Involved

| Symbol | Role | Context (verified) |
|---|---|---|
| `pkg/agent/loop.go::processMessage` | **modifies** | The single point where every `bus.InboundMessage` (channels via `pkg/channels/base.go`, SSE via `pkg/gateway/sse.go`, goal follow-ups, async notifier) becomes a turn → `runTurn`. D4's user-message bound lives here. |
| `pkg/agent/loop.go::runTurn` | **modifies** | Pre-turn `isOverContextBudget` → `windowTrim` → `assembleMessages` → save user message → tool loop. Tool results append to the in-memory `messages` slice; each LLM call uses `callMessages` built from it. Four `"turn canceled"`/`"turn timed out"` returns emit nothing. |
| **The twelve `role: "tool"` producers** | **all route through the choke point** (one exempt) | `loop.go`: the success-path `toolResultMsg`; seven `deniedMsg` sites; the `skippedMsg` synthetic result (iteration-skip). `pkg/agent/attach_hydrate.go` (hydrated attachments — builtin-success surface). `pkg/agent/recall_conversation.go::buildRecallSpanMessages` (re-injected span/page messages — builtin-success surface). `pkg/agent/repair.go` (orphan-repair placeholder — **exempt**, bounded by construction). Delegated sub-turn reports arrive as the parent's `delegate` tool result through the success path. |
| `pkg/agent/loop.go::windowTrim` | **modifies** | Budget `B = W − maxTokens − ceil(0.05·W) − pinnedCoreOverhead`; walks `parseTurnBoundaries`; advances `Skip` via `TruncateHistory`; flat `128000` fallback (removed). |
| `pkg/agent/context_budget.go::isOverContextBudget` | **modifies** | Today compares `msgTokens + toolTokens + maxTokens > contextWindow` — no headroom, no pinned overhead. Its threshold becomes **B** so all four consumers agree (CRIT-001). |
| `pkg/agent/context_budget.go::parseTurnBoundaries`, `::estimateMessageTokens` | **calls / unchanged** | Estimator `chars × 2/5` + 12 overhead + 256/media. |
| `pkg/agent/loop.go::assembleMessages` | **modifies** | Turn start, post-trim, reload; applies the same projection as the mid-turn path. |
| `pkg/agent/instance.go::NewAgentInstance` | **modifies** | `maxTokens * 4` fallback and `SummarizeTokenPercent` default removed; window resolved by the D2 ladder; `ContextWindow` read under the instance mutex. |
| `pkg/agent/loop.go` model-switch re-window (`newContextWindow = 128000`) | **modifies** | Consolidates onto the ladder. |
| `pkg/agent/loop.go::TriggerReload` | **calls** | Settings/override writes trigger it (MAJ-011). |
| `pkg/agent/turn.go::restoreSession`, `turnState.initialArchiveLen` / `initialHistoryLength` | **modifies** | The restore point = turn-start values, captured once in `newTurnState`. Gains `initialEmptiedSet`. |
| `pkg/agent/turn.go::refreshRestorePointFromSession`, `turnState.restorePointHistory` | **deleted** | Verified dead: `restorePointHistory` is written (`turn.go::refreshRestorePointFromSession`) and read nowhere. Greenfield (CRIT-003). |
| `pkg/memory/store.go::Store.RollbackAppended(ctx, key, targetLines, targetSkip)` | **modifies (interface)** | Gains the turn-start emptied-set; `pkg/agent/subturn.go::ephemeralSessionStore.RollbackAppended` implements it as a no-op parameter (in-memory projection state). |
| `pkg/memory/jsonl.go::sessionMeta` (`skip`, `count`), `::GetHistory`, `::ReadArchive`, `maxLineSize = 10 MB` | **extends** | Meta gains per-result projection state keyed `(tool_call_id, archive line index)`; scanner buffer is `maxLineSize` — a longer line breaks the rest of the session read, hence the encoded-line bound. |
| `pkg/agent/recall_conversation.go::RecallConversationTool` (`recallDefaultTokens = 4000`, `recallRangeTokens = 8000`, id remap; `Execute` → `setRecallSpan` → receipt *"Recalled N turn(s) … into context."*) | **extends / modifies** | `tool_call_id` (+ `archive_line`) paged mode; `max_results` (FR-040); **D5.4:** receipt rewritten to the real outcome; non-fit message. Verified: the span is read only by `loop.go::assembleMessages` and `loop.go::windowTrim`; mid-turn `callMessages` is built from `repairedHistory := messages` and never consults `activeRecallSpan`. |
| `pkg/agent/loop.go` tool loop (recall tool-result site), `turnState.injectedRecallSpan` (new), `pkg/agent/context.go::BuildMessages`, `::sanitizeHistoryForProvider` | **modifies / adds** | **D5.4:** splice the span into the in-memory slice right after the recall result is appended; budget check first; `assembleMessages` skips an injected span. |
| `pkg/agent/attach_hydrate.go::HydrateAgentHistoryFromTranscript` (callers: `pkg/gateway/websocket.go` attach_session — unconditional; `loop.go` self-heal — when history has no assistant/tool) | **modifies** | **D5.5:** attach path skips when the archive has ≥ 1 line; hydration emits tool results from the transcript's bounded `result`; meta `hydrated: true` until the transcript field lands. |
| `pkg/memory/jsonl.go::SetHistory` (rewrites the file, `meta.Skip = 0`) | **modifies** | **D5.5:** refuses when the file is non-empty; never resets `Skip` on an existing archive. |
| `contracts/components/schemas/ToolCall.yaml` `result` (transcript) | **extends** | **D5.5:** bounded `result` content written by the D4 choke point so hydration is not lossy (coordinate with S67's contract commit). |
| `pkg/agent/translate_error.go::contextOverflowSubstrings`, `::classifyByMessage`, `CodeContextTooLong`, `CodeUnknown` | **extends** | D7 codes only; classification job unchanged (D8 not adopted). |
| `LLMError` — **four copies**: `contracts/components/schemas/LLMError.yaml`, `LLMErrorReplay.yaml`, and the inline `components.schemas.LLMError` / `LLMErrorReplay` blocks in `contracts/asyncapi.yaml` (the generators read asyncapi) | **extends (semantics and copy owned here; file edit in S67's coordinated contract commit)** | `turn_canceled` (attribution `user` — new vocabulary value in all four `x-user-message-attributions`), `turn_timed_out` (`provider`), `context_unrecoverable` (`product`), `context_window_unknown` (`config`, the D3 refusal — X-09; `model_unavailable` is NOT reused, its copy describes a fallback). Guarded by `pkg/api/generated/llm_error_codes_test.go::TestContract_LLMError_AllClassifierCodesRoundTrip` and `llm_error_catalogue_test.go::TestContract_LLMErrorCatalogue_AllFourCopiesAgree`. |
| `contracts/components/schemas/ToolCall.yaml` | **extends** | `content_state: full \| capped \| emptied`. |
| `contracts/asyncapi.yaml` | **extends** | `tool_result_projection` frame; recall-mark and argument-refusal inline schemas (ADR-060 family). |
| `contracts/components/schemas/ContextSettings.yaml`, `ContextSettingsUpdate.yaml`, `ContextWindowSource.yaml` (new, owned here); `Agent.yaml`, `AgentUpdateRequest.yaml` (S67 commits the coordinated edit) | **adds / extends** | Caps, trigger, ingest bound, global default, `model_overrides[]`; `ContextWindowSource` enum `operator \| live \| catalog \| floor` `$ref`'d by `Agent.context_window_source` and S68's `DefaultModel.window_source` (X-06); per-agent `context_window_override` + derived `context_window_effective`, `context_window_source`, `context_window_clamped` — all three **optional** on the wire (absent until the resolver lands). Verified: `Agent.yaml` has no `context_window*` field today. |
| `pkg/config/config.go` `AgentDefaults.ContextWindow` (`json:"context_window"`, env `OMNIPUS_AGENTS_DEFAULTS_CONTEXT_WINDOW`) and `SummarizeTokenPercent` | **deleted** | Single home is `ContextSettings.default_context_window` (MAJ-014); greenfield. |
| `pkg/mcp/manager.go::sandboxedCommandTransport` / `sandboxedStdioConn` (wraps the SDK `IOTransport`), `::Manager.CallTool` | **modifies** | Transport-level ingest bound on the stdout stream; HTTP/SSE transports via `http.MaxBytesReader` on the client `RoundTripper`. No truncation anywhere today. |
| `pkg/tools/web.go` — `BraveSearchProvider.Search`, `DuckDuckGoSearchProvider.Search`, `PerplexitySearchProvider.Search` (`io.ReadAll(resp.Body)` unbounded); `GLMSearchProvider.Search`, `BaiduSearchProvider.Search` (`io.LimitReader(…, 1<<20)`); `fetch_url` `MaxBytesReader` 10 MB | **modifies** | All five bounded at the ingest bound; `fetch_url` fallback aligned to 8 MB. |
| `pkg/tools/filesystem.go::MaxReadFileSize` (64 KB), `web.go::defaultMaxChars` (50,000), `browser/tools.go::maxGetTextBytes` (100 KiB), `shell.go::maxForegroundOutputLen` (10,000) | **modifies** | Aligned to D4 figures. |
| `pkg/tools/result.go::ToolResult` (`IsError`), `::marshalWithinBudget`, ADR-060 family register (`scripts/check-no-handwritten-wire-types.sh`) | **extends** | Argument refusal and recall mark producers. |
| `pkg/providers/catalog` `locality` predicate and `cli_driver` field (owned by S67, X-16/X-14) | **calls** | `locality: local` ⇔ protocol ∈ {ollama, vllm} ∨ id = lmstudio ∨ custom row with loopback/private host; exempt ⇔ `cli_driver` is a subprocess driver. No classification table and no factory id lives in this spec. |
| `ResolveWindow(provider, model, agentID="")` (new, `pkg/agent`, owned here — X-07) | **adds** | Rungs 2–6 when `agentID` is empty (S68's default-model card and row expand call it); rung 1 applies only with an agent. Exempt → `context_window: 0`, `window_source` absent. |
| S67's providers-catalog `GET` projection | **extends (field owned here, X-08)** | Per-model `window_source` and, for a `locality: local` model whose live query failed, `window_unknown: true`; S68 renders it with a link to Settings → Models → Model overrides. Not a `Provider.status` value (status stays at six). |
| `pkg/gateway/metrics.go::toolMetrics` | **extends** | `tool_result_large_total`, `context_empties_total`. |
| `pkg/gateway/replay.go` (`InlineToolResultMaxBytes`, `tool_results/` store; `role:"turn_canceled"` replay frame) | **extends** | Transcript read returns projected content + `content_state`; full content stays in `tool_results/` for Verbose chat. |
| `src/lib/toolVisibility.ts::shouldRenderToolCall`; `src/components/settings/*` | **extends** | Mark rendered only under Verbose chat; new Context settings section. |
| `pkg/providers/capabilities` → `pkg/providers/catalog` (ADR-067) `Resolve(provider, model)` | **calls** | D2 catalog rung. |

### Impact Assessment

| Symbol Modified | Risk | d=1 Dependents | d=2 |
|---|---|---|---|
| `loop.go::runTurn` / `::processMessage` | **CRITICAL** | every turn; every channel; `subturn.go::spawnSubTurn`; `scenario_runturn_test.go`, `runturn_*_test.go`, `turn_test.go` | gateway frames, ActivityPanel |
| `windowTrim` + `isOverContextBudget` threshold | **HIGH** | pre-turn, timeout-recovery, model-switch sites; `window_trim_test.go`, `context_budget_test.go` | recall span drop |
| `Store.RollbackAppended` signature; `refreshRestorePointFromSession` deletion | **HIGH** | `JSONLStore`, `ephemeralSessionStore`, session adapters, all fakes; `turn.go` abort path | — |
| `NewAgentInstance` resolution; config field deletions | **HIGH** | every agent boot; `subturn.go` execSource copy; `config_test.go` | Settings read path |
| `LLMError.yaml` (+ `user` attribution), `ToolCall.yaml`, asyncapi frame | **MEDIUM** | generated Go/TS; `translate_error_test.go`, `llm-error.test.ts`, SPA zod edge | SPA rendering |
| `recall_conversation.go` | **MEDIUM** | `recall_conversation_test.go` | `windowTrim` span budgeting |
| `mcp` transport, `web.go` providers | **MEDIUM** | MCP wrapper, search tests | — |
| per-tool caps | **LOW** | tool tests asserting old numbers | skills assuming 100 KiB `browser_get_text` |

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Inbound message → `processMessage` → turn registration → `runTurn` | D4 user-message bound before registration/persistence; refusal is an outbound reply on the originating channel. |
| Turn start → pre-turn check (B) → `windowTrim` (cut and/or empty) → `assembleMessages` (projection) | Window from the D2 ladder; restore point captured once before this. |
| Tool loop: execute → filter → **choke point** (cap/clamp, encoded-line bound) → archive append + state → **D6 check (B, share)** → **D5 empty to target (never Skip)** → assemble → call | Order fixed (FR-032). |
| Turn abort → `restoreSession` → `RollbackAppended(lines, skip, emptiedSet)` | Turn-start values only. |
| `recall_conversation(tool_call_id[, archive_line], offset, length)` → streaming archive scan → re-paired page | Page = effective cap − mark framing. |
| Provider error / ctx error → typed `LLMError` | `turn_canceled` (user), `turn_timed_out` (provider), `context_unrecoverable` (product). |
| `PUT /settings/context`, `PUT /agents/{id}` override → `TriggerReload` | Next turn uses the new window. |

### Cluster Placement

Agent loop / context paging (`pkg/agent`, `pkg/memory`) primarily; tools + MCP for D4/D10; gateway + contracts for D7/D9/projection frame; providers for D2/D3; SPA Settings. The choke point (D4), the projection function (D5) and the single budget B (D6) are the three seams.

---

## 4. User Stories & Acceptance Criteria

### US-1 — Resolve the context window from a ladder with clamped overrides (D2) — **P0**
As an operator, I want each agent's window resolved per-agent override → per-(provider, model) override → global default → live query (cached) → catalog → floor, with every override clamped to the model's capability, so the record is never larger than reality.
- **Why P0:** §1.1 — every other decision computes against this number.
- **Independent test:** for fixed catalog/live values and each override rung, the resolved window and source match the expected rung, clamped.
- **Acceptance:**
  1. **Given** no overrides and catalog (openrouter, z-ai/glm-5.2) = 1,048,576, **When** resolved, **Then** 1,048,576, source `catalog`.
  2. **Given** a per-agent override of 100,000 and catalog 1,048,576, **Then** 100,000, source `operator`.
  3. **Given** a per-(provider, model) override of 200,000 and no per-agent override, **Then** 200,000, source `operator`; **Given** both, **Then** the per-agent value wins.
  4. **Given** an override of 2,000,000 and capability 1,048,576, **Then** 1,048,576, `clamped: true`, one WARN naming agent, override and clamp.
  5. **Given** a cloud provider whose limits endpoint returns 200,000 and no overrides, **Then** 200,000, source `live`, served from the on-disk cache (TTL 24 h, key (provider id, base URL, model), provider credential from the credential store; rung skipped when no credential). The query is **on demand only** — at the first resolution that reaches this rung, **never at boot and never on a timer** (X-18/X-36; ADR-067 §4.3's fourth sanctioned live call); the turn path never waits on it: a cold cache resolves from the next rung now and the live value applies at the next reload.
  6. **Given** a provider whose catalog row has a subprocess `cli_driver` (today `codex-cli` only; by field, never by id), **Then** `ContextWindow = 0`; pre-turn trim, mid-turn check and timeout-recovery check are all skipped. `openai-chatgpt` is an HTTP transport and is cloud, not exempt (X-20).
  7. **Given** any configuration, **Then** pre-turn, mid-turn, timeout-recovery and model-switch compare against one budget B from one resolved window.
  8. **Given** the catalog value for a model is lowered in a new release, **When** next resolved, **Then** overrides above it are re-clamped (overrides never expire).
  9. **Given** `ResolveWindow(provider, model)` with no agent, **Then** rungs 2–6 apply and the result carries window + source; with an agent id, rung 1 applies too; an exempt provider returns 0 with no source (X-07).
  10. **Given** a `model_overrides[]` entry whose provider no longer exists, **Then** the resolver ignores it and the next settings write prunes it.

### US-2 — Unknown window: floor for cloud, ask-or-refuse for local (D3) — **P0**
As an operator, a hosted model the catalog does not know gets a 128,000 floor with a WARN; a `locality: local` endpoint (ADR-067's predicate) is queried live and, if it reports no window, is unusable with an actionable message that names the exact field to set.
- **Why P0:** a 128,000 guess on an 8k local model is the incident again.
- **Independent test:** the catalog `locality` drives floor vs refusal; the override write makes the model usable without restart.
- **Acceptance:**
  1. **Given** a cloud model absent from catalog and live, **Then** 128,000, source `floor`, one WARN naming the model.
  2. **Given** an `ollama` model reporting 8,192 (`/api/ps`), **Then** 8,192, source `live`, never floored.
  3. **Given** a local endpoint whose live query fails or reports no window, **When** selected or when an agent bound to it starts a turn, **Then** the turn is refused with `LLMError` code `context_window_unknown` (attribution `config`; third in the pre-turn gate after `needs_provider` and `model_unassigned` — X-09) and the message *"This endpoint did not report a context length for <model>. Set it under Settings → Models → Model overrides → <provider> / <model> → Context length and try again."*; the providers-catalog `GET` projection carries `window_unknown: true` for that model so S68's row and picker show the state (X-08).
  4. **Given** that state, **When** the operator writes `model_overrides[{provider, model, context_window}]` via `PUT /api/v1/settings/context`, **Then** a reload is triggered and the next turn runs with that window (clamped), no restart.
  5. **Given** a custom row (operator id, e.g. `my-proxy`) whose base URL host is public, **Then** its `locality` is cloud: floored with a WARN, never refused.

### US-3 — Admit every tool result through one choke point, capped and clamped (D4) — **P0**
As the harness, every tool result — success, `IsError`, denied, skipped, hydrated attachment, recall page, delegated report, MCP — becomes a context message through one function that caps it per surface, clamps the cap to half the budget, bounds the encoded archive line, and marks over-cap results.
- **Why P0:** §1.2; and the clamp is what makes the floor satisfiable on small models (CRIT-002).
- **Independent test:** a 2 MB result from each producer class enters capped with a mark; archive holds the full content; reload shows the capped form; on an 8,192 window the cap is half the budget.
- **Acceptance:**
  1. **Given** an MCP success result of 1,178,522 chars on a large window, **Then** ≤ 62,500 chars, head-and-tail 50/50, mark counted, not an error.
  2. **Given** a builtin success result of 200,000 chars, **Then** ≤ 64,000; **Given** a builtin or MCP `IsError`/`isError` result of 50,000, **Then** ≤ 10,000; **Given** a denied/skipped result, **Then** ≤ 10,000 (failure surface); **Given** a `delegate` report of 200,000, **Then** ≤ 64,000 (builtin success; no exemption); **Given** a hydrated attachment, **Then** builtin success.
  3. **Given** a window whose budget B is 3,000 tokens (7,500 chars), **When** a 64,000-char builtin result arrives, **Then** it enters at most `floor(0.5 × 3,000 × 2.5) = 3,750` chars (effective cap), marked.
  4. **Given** an assistant message with N = 3 parallel calls on that window, **Then** each result is capped at `effective_cap / 3` (1,250 chars) and the three together fit in B.
  5. **Given** any capped result, **Then** the full filtered content is on the archive line, meta records `(tool_call_id, line) → capped`, and reload renders the capped form byte-identical to what the model saw.
  6. **Given** a result whose JSON-encoded archive line would exceed 0.8 × `maxLineSize` (8,388,608 bytes), **Then** it is capped so the encoded line fits (post-parse check, after filtering, before append).
  7. **Given** a result over 25,000 chars but under its cap, **Then** unmodified; one WARN log line and `tool_result_large_total` increment.
  8. **Given** any cap setting above 150,000 or below 1, **Then** HTTP 400.
  9. **Given** the shipped tools, **Then** `read_file` 64 KB unchanged, `browser_get_text` lowered to 64,000, shell 64,000 success / 10,000 failure; no per-server or per-tool opt-out.
  10. **Given** the sensitive-data filter is on, **Then** it runs on the full content before the cap; archive copy is the filtered full content.
  11. **Given** the settings change while a turn is in flight, **Then** the choke point reads settings per call — the next result uses the new values.

### US-4 — Bound user messages where they become turns (D4) — **P1**
As a user on any channel who pastes a huge document, I get a non-fatal refusal before a turn starts.
- **Why P1:** closes the user-content path to the guard; on §17.4.
- **Independent test:** a 64,001-char message over WS, SSE and a channel (Slack) is refused with the size and limit; nothing persisted; 64,000 accepted.
- **Acceptance:**
  1. **Given** a user message of N > 64,000 chars arriving by any intake, **When** `processMessage` receives it, **Then** a reply on the originating channel states N and the limit, no transcript entry, no turn registered, no error frame.
  2. **Given** exactly 64,000 chars (media refs not counted), **Then** a turn starts.
  3. **Given** the bound tracks the builtin success cap (not a separate setting), **When** the cap is changed, **Then** the reply quotes the live value.

### US-5 — Refuse oversized tool-call arguments as a structured result (D4) — **P1**
- **Why P1:** closes the argument path; §17.4.
- **Independent test:** serialised arguments > 64,000 chars → structured refusal, tool not executed, turn continues.
- **Acceptance:**
  1. **Given** arguments of 64,001 chars, **Then** the tool does not execute; the result is a structured refusal (ADR-060 family) naming tool, size and cap; the loop proceeds to the next LLM call.
  2. **Given** a retry with 10,000-char arguments, **Then** the tool executes.
  3. **Given** the refusal, **Then** it passes the choke point like any result.

### US-6 — Empty in place with a recall mark; persist projection state; roll back to turn start (D5) — **P0**
- **Why P0:** §1.3 root cause.
- **Independent test:** with a small budget and several results, the oldest eligible results become marks in the provider bytes; archive unchanged; reload identical; abort restores the turn-start triple.
- **Acceptance:**
  1. **Given** over budget and the oldest eligible content is a tool result (current turn, or an earlier turn the pre-turn floor kept), **Then** its content becomes the mark; role, `tool_call_id` and slot unchanged.
  2. **Given** the mark, **Then** it states tool name (≤ 64 chars, non-printables stripped), `tool_call_id` (sanitised the same way), archive line, full size in chars, turn number = 1 + the count of `role: user` archive lines preceding the result's line, and the recall hint; single typed producer via `marshalWithinBudget`.
  3. **Given** a mid-turn empty, **When** the session is reloaded and assembled, **Then** the bytes for that message are identical to the bytes sent to the provider.
  4. **Given** emptying, **Then** no archive line changes.
  5. **Given** a turn that emptied and then aborts, **Then** archive length, `Skip` and the emptied-set return to their **turn-start** values — never an intermediate state; entries whose line index ≥ the turn-start archive length are dropped.
  6. **Given** one trigger firing, **Then** emptying runs to the target in one pass; an immediate re-check does not re-fire.
  7. **Given** Verbose chat off / on, **Then** the mark is absent / present in the thread; the SPA learns of the change via the `tool_result_projection` frame and, on reload, via `content_state` on the transcript.
  8. **Given** a successful turn, **Then** its marks are permanent for the session (meta); nothing un-empties short of recall.
  9. **Given** `TruncateHistory` advances `Skip`, **Then** projection entries with line index < `Skip` are pruned.
  10. **Given** an emptying pass, **Then** one INFO line (session key, count, share before/after) and `context_empties_total` increment.

### US-7 — Recall a capped or emptied result by id, in pages (D5 §6.3) — **P0**
- **Why P0:** without it emptying is lossy in practice.
- **Independent test:** paging reaches the last byte; duplicates resolve; page + framing never exceeds the cap.
- **Acceptance:**
  1. **Given** archived result X (1,178,522 chars), **When** `recall_conversation(tool_call_id=X)` is called, **Then** one re-paired `role: "tool"` page whose payload = effective cap − mark framing, stating total and next offset; the message is ≤ the cap after framing and passes the choke point unmodified.
  2. **Given** `offset`/`length` (runes), **Then** pages are contiguous to the last byte; `offset ≥ total` → empty page with total; `offset < 0` or `length < 1` → error; `length` above the page size is clamped.
  3. **Given** two archive lines sharing `tool_call_id`, **Then** the most recent line wins unless `archive_line` is given.
  4. **Given** the `tool_call_id` mode, **Then** exempt from the 4,000/8,000 span budgets, counted by D6, emptiable later.
  5. **Given** an unknown id or an id from an aborted (rolled-back) turn, **Then** a tool error naming the id; **Given** two modes at once, **Then** error.
  6. **Given** the scan, **Then** the archive is streamed and stops at the matching line (no whole-archive load).

### US-8 — One budget, checked after every result; empty-only mid-turn; floor; guard (D6) — **P0**
- **Why P0:** §1.3 — "nobody looks while the turn fills".
- **Independent test:** 50 calls at the cap against a small window never exceed B; last step intact; `Skip` never moves mid-turn.
- **Acceptance:**
  1. **Given** N tool results, **Then** the check runs N times mid-turn plus once pre-turn, each against **B**.
  2. **Given** `total > B` or `share > 160,000`, **Then** emptying runs to 80 % of the condition that fired, or until no eligible result remains.
  3. **Given** the pre-turn site and the oldest over-budget content is an earlier complete turn, **Then** `Skip` advances (cut), as today; **Given** the mid-turn site in the same situation, **Then** `Skip` does not move and eligible results are emptied oldest-first.
  4. **Given** 3 parallel results on the last step, **Then** none is emptied and, by the clamp, they fit.
  5. **Given** the target is unreachable but no trigger condition remains exceeded, **Then** the turn continues with no error.
  6. **Given** an injected fault leaves a trigger condition exceeded after every eligible result is emptied, **Then** `context_unrecoverable`, one ERROR line, provider not called again.
  7. **Given** timeout recovery, **Then** it uses B; `summarize_token_percent` no longer exists.
  8. **Given** one result: ingest bound → filter → cap/clamp + encoded-line bound → archive append + state → check → empty → assemble → call.

### US-9 — No silent turn exits (D7) — **P1**
- **Acceptance:**
  1. **Given** a cancelled context (Stop button, shutdown), **Then** `turn_canceled`, attribution `user`, log line with raw cause, turn-end event, transcript entry; the SPA renders attribution-`user` codes as a neutral notice (existing `turn_canceled` replay role retained), not an error toast.
  2. **Given** a deadline, **Then** `turn_timed_out`, attribution `provider`, same artefacts.
  3. **Given** the guard, **Then** `context_unrecoverable`, attribution `product`.
  4. **Given** the codes, **Then** `LLMError.yaml` defines them with copy and attribution (`user` added to `x-user-message-attributions`); regenerated in the same commit.
  5. **Given** D4–D6, **Then** the only turn-fatal conditions are provider auth, provider unreachable, workspace unavailable, model unavailable (D3), and the guard (injected fault only).

### US-11 — Controls in Settings (D9) — **P1**
- **Acceptance:**
  1. **Given** `GET /api/v1/settings/context`, **Then** caps (62,500 / 64,000 / 10,000), `absolute_trigger_chars` 400,000, `ingest_bound_bytes` 8,000,000, `default_context_window` unset, `model_overrides: []`.
  2. **Given** `PUT` with a partial body (`ContextSettingsUpdate`, omitted = unchanged), **Then** 200 and a reload is triggered; cap > 150,000 or < 1, trigger < 1, ingest bound ≥ 8,388,608 → 400 naming the field and limit.
  3. **Given** an agent, **Then** `context_window_effective`, `context_window_source` (`operator | live | catalog | floor`), `context_window_clamped`, `context_window_override` — generated types only; a `PUT /agents/{id}` override write triggers a reload.
  4. **Given** the routes, **Then** `withAuth` (any authenticated user, the `/settings/memory` precedent), not `RequireNotBypass`.

### US-12 — Bound ingest at the transport and bound the encoded line (D10) — **P1**
- **Acceptance:**
  1. **Given** an MCP stdio server streaming > 8,000,000 bytes for one JSON-RPC message, **Then** the read is aborted on the transport (`sandboxedStdioConn` reader bound) and the call is a tool failure naming the bound — never fully buffered.
  2. **Given** an MCP HTTP/SSE transport, **Then** `http.MaxBytesReader` on the response body, same outcome.
  3. **Given** Brave / DuckDuckGo / Perplexity / GLM / Baidu responses > bound, **Then** tool failure; the two 1 MiB `LimitReader` sites are raised to the bound; `fetch_url` fallback 8 MB.
  4. **Given** 2 MB, **Then** accepted.
  5. **Given** 8,000,000 bytes of newlines (escapes to 16,000,000), **Then** the choke point's encoded-line bound caps it so the archive line ≤ 8,388,608 bytes and `GetHistory` still reads the session.

### US-13 — Bounding parameters for three Tier-1 tools (ADR §15 task 1) — **P2**
- **Acceptance:** `list_directory` gains `offset`/`limit` (entries); `inspect_session` gains `offset`/`limit` (entries); `recall_conversation`'s `query`/`turn_range` modes gain `max_results` (turns) — parameter names and semantics consistent with `read_file`'s existing `offset`/`length` interface; each validated (`offset ≥ 0`, `limit`/`max_results ≥ 1`) and documented in the tool schema. *(A-19 accepted.)*

### US-14 — Recall content reaches the model in the same turn (D5.4) — **P0, hotfix first**
As the model, when I call `recall_conversation`, the recalled text is in my very next request — or the tool result tells me it did not fit — so emptied content really does come back via recall.
- **Why P0:** verified bug: the span is only read at assembly/trim sites; mid-turn requests are built from the in-memory slice, so 25 live recalls reached the model as a receipt string only. D5 is false without this.
- **Independent test:** loop-level, fake recording provider: nonce in turn 1 evicted past `Skip`; provider's first response calls `recall_conversation(turn_range:"1-1")`; the provider's **second** request contains the nonce and the recall marker.
- **Acceptance:**
  1. **Given** a recall result is appended to `messages` mid-turn, **When** the span fits the one budget B, **Then** `span.Messages()` is spliced at the position `BuildMessages` uses (after the pinned core, before the window), the combined slice is sanitised for the provider, and the next request contains the recalled text and marker.
  2. **Given** the span does not fit B, **Then** nothing is spliced, nothing is dropped silently, and the tool result reads *"N turn(s) found (X estimator tokens) but they do not fit the current window; narrow with turn_range or query"*; the next request lacks the recalled text.
  3. **Given** an injected span, **Then** the receipt reads *"Recalled N turn(s) (turns A–B); their text is now in your context"*; a non-injected one never says "into context".
  4. **Given** a later reassembly (trim site, reload), **Then** an already-injected span (`ts.injectedRecallSpan`, by identity) is not doubled.
  5. **Given** the `tool_call_id` mode, **Then** each page is injected the same way.
  6. **Given** any span event (set / injected / refused / dropped), **Then** one INFO line with sizes.
  7. **Given** an injected span, **Then** it is subject to D5 emptying and D6 like any tool result.
  8. **Given** a delegated sub-turn calls recall, **Then** (known limitation, pinned by a test, not fixed here) it reads the parent store under the child's ephemeral key and returns nothing; the test asserts the empty outcome and the INFO line naming it.

### US-15 — Opening a session must not destroy the agent archive (D5.5) — **P0, hotfix first**
As an operator, opening a session in the browser must never rewrite the per-agent archive, so tool results persist, `Skip` is stable, and ADR-028's append-only invariant holds.
- **Why P0:** verified on the operator's data: attach rebuilds the archive from the transcript via `SetHistory` (21/53/36 lines → 22/42/0, `skip` 0). D5's "full result stays in the archive" is false until fixed.
- **Independent test:** attach the same session twice → archive bytes and `meta.skip` unchanged; attach to an empty archive → hydrated once, with tool results.
- **Acceptance:**
  1. **Given** an agent archive with ≥ 1 line, **When** `attach_session` runs, **Then** hydration is skipped; file bytes and `skip` unchanged.
  2. **Given** an empty archive, **When** attached, **Then** hydration runs once and the rebuilt archive contains one `role: "tool"` line per recorded tool call with the transcript's bounded `result` content.
  3. **Given** the self-heal path, **Then** its existing emptiness condition is unchanged.
  4. **Given** the transcript `tool_call.result` field (bounded by D4, written by the choke point) has not landed, **Then** a hydrated archive carries meta `hydrated: true` and recall by `tool_call_id` answers *"not available — session was rebuilt from the transcript"*.
  5. **Given** a non-empty archive, **When** `SetHistory` is called, **Then** it refuses (error logged) and `Skip` is untouched.
  6. **Given** the ADR-028 append-only invariant test, **Then** it includes an attach step and still passes.

### Edge Cases
- E1: exactly at cap → unmodified; +1 → capped.
- E2: a capped result later emptied → mark replaces the capped form; recall still reaches full content.
- E3: floor set alone over budget — impossible by the clamp (`N × effective_cap/N ≤ 0.5 B`); guard only by injected fault.
- E4: parallel N → floor is all N; emptying older steps oldest-first.
- E5: runes for caps, bytes for ingest; head/tail never split a rune.
- E6: hostile MCP tool name / id → sanitised in the mark.
- E7: recall id from an aborted turn → not found.
- E8: paging edge values per US-7.AC2.
- E9: live cache expired + endpoint down → cloud: catalog → floor; local: refusal; cold cache → next rung now, live at next reload (no boot/timer fetch).
- E10: override written while refused → usable after reload, no restart.
- E11: catalog lowered → re-clamp on next resolution.
- E12: media refs not counted toward the user bound.
- E13: abort at any point → turn-start triple.
- E14: exempt (subprocess `cli_driver`) providers → window 0, all checks skipped.
- E15: delegated sub-turn → ephemeral store keeps projection state in memory; rollback parameter no-op; child's report capped at 64,000 as the parent's tool result.
- E16: a previous oversized turn kept by the pre-turn floor → its results are eligible for emptying (pre-turn, after the cut fails to fit).
- E17: a tampered `model_limits.json` can only lower the window (clamp) — accepted.
- E19: recall of a span larger than B on a small window → non-fit message, no splice (US-14.AC2).
- E20: two recalls in one turn → the second replaces the first (existing `TestRecallSpan_ReplacedOnNextRecall`), and the replaced span is removed from the in-memory slice before the new one is spliced.
- E21: attach while a turn is in flight → hydration skipped (archive non-empty).
- E18: `max_tokens ≥ W` (so `B ≤ 0`) → `max_tokens` is clamped to `floor(W/4)` with a WARN naming the model; B is then positive and the cap clamp holds. *(A-18 accepted.)*

---

## 5. Behavioral Contract & Boundaries

### Behavioral Contract
- When an agent instance is built or reloaded, the system resolves its window via the six-rung ladder, clamps every override to capability (recomputed each time), records source and `clamped`.
- When a `locality: local` endpoint reports no window, the system refuses the model (`context_window_unknown`) with the message naming `Settings → Models → Model overrides` and marks the model `window_unknown: true` in the catalog projection.
- When any tool result is produced, the system admits it through the choke point: filter → cap per surface → clamp to half B (and `/N` for parallel) → encoded-line bound → archive full → record state.
- When a user message over the bound reaches `processMessage`, the system replies on the originating channel and starts no turn.
- When arguments exceed the cap, the system returns a structured refusal and continues.
- When `total > B` or `share > 160,000` after a result, the system empties eligible results oldest-first to 80 % of the fired condition, never the floor set, never advancing `Skip` mid-turn, persisting `(id, line) → emptied`, emitting the projection frame.
- When a turn aborts, the system restores archive length, `Skip` and emptied-set to turn start.
- When the model recalls by id, the system returns contiguous pages ≤ the cap after framing.
- When a recall result is appended mid-turn, the system splices the span into the next request if it fits B, otherwise tells the model it did not fit.
- When a session is attached, the system hydrates only an empty archive and never rewrites an existing one.
- When a turn is cancelled, times out, or trips the guard, the system ends it with the typed code and three artefacts.
- When settings or overrides are written, the system validates, persists, and triggers a reload.
- When a read exceeds the ingest bound, the system aborts it on the transport and fails the tool.

### Explicit Non-Behaviors & Safeguards

#### Qualitative Prohibitions
- Must not summarise or rewrite content (ADR-028/066).
- Must not modify archive lines when capping or emptying.
- Must not cut mid-turn (advance `Skip` inside a turn) — only empty; the pre-turn trim is the only cut site.
- Must not empty any result of the most recent assistant message.
- Must not raise the window above capability by any rung; must not fall back to `max_tokens × 4` or a flat `128000`.
- Must not floor a local endpoint; must not refuse a cloud endpoint reached via `custom`.
- Must not learn, infer or cache a window from provider error text (D8 not adopted). **Accepted cost:** a model not yet in the catalog or a plan-specific cap overflows with a typed `context_too_long` until the catalog or an override corrects it.
- Must not add a per-server/per-tool cap opt-out; must not exempt `delegate` reports or attachments from the choke point.
- Must not fetch live limits on the turn path, at boot, or on a timer — on demand only (X-18).
- Must not define provider locality or a factory id — ADR-067's `locality` predicate is the single definition (X-16/X-17).
- Must not persist, register or error-frame a refused user message.
- Must not hand-roll the mark or refusal with `fmt.Sprintf`.
- Must not add a second budget formula, a spill store, a reducer, or refetch recipes (ADR §14); must not keep a `0.9 × window` haircut.
- Must not keep `refreshRestorePointFromSession`/`restorePointHistory`, `agents.defaults.context_window`, its env var, or `summarize_token_percent`.
- Must not render the mark in the thread unless Verbose chat is on.
- Must not report a recall as "into context" unless the text is in the next request; must not drop a non-fitting span silently.
- Must not call `SetHistory` on a non-empty archive; must not reset `Skip` on attach.

#### Machine-Verifiable Constraints

**The one budget (D6), estimator tokens (`chars × 2/5`):**
```
B          = W − max_tokens − ceil(0.05 × W) − pinnedCoreOverhead      (windowTrim's formula, unchanged)
total      = Σ tokens(messages) + toolDefsTokens + recallSpanTokens
share      = Σ tokens(role == "tool" messages in the window)
over       ⇔ total > B  OR  share > absoluteShare        (absoluteShare = absolute_trigger_chars ÷ 2.5 = 160,000 by default)
target     = 0.8 × B (if total fired) / 0.8 × absoluteShare (if share fired); emptying stops at target or when no eligible result remains
guard      ⇔ after emptying, total > B OR share > absoluteShare still holds  →  context_unrecoverable
```
`isOverContextBudget`'s threshold is B (not `contextWindow`); the pre-turn, mid-turn, timeout-recovery and model-switch sites all use it. Exempt providers: `W = 0`, every check skipped.

**`max_tokens` clamp (A-18):** if `max_tokens ≥ W − ceil(0.05 × W) − pinnedCoreOverhead` (B would be ≤ 0), `NewAgentInstance` sets `max_tokens = floor(W / 4)` and logs one WARN naming the model and both values, so B > 0 always. Boundary: `W = 8,192`, `max_tokens = 8,192` → effective `max_tokens = 2,048`.

**Caps and clamp (chars = runes):**
```
configured: mcp 62,500 · builtin-success 64,000 · builtin-failure 10,000 · warn 25,000 · ceiling 150,000
effective_cap(surface) = min(configured_cap, floor(0.5 × B × 2.5))
parallel N (one assistant message): per-result cap = effective_cap / N   when N × effective_cap × 0.4 > B
encoded-line bound: len(json.Marshal(ArchivedMessage)) ≤ 0.8 × maxLineSize = 8,388,608 bytes
head/tail 50/50; mark length counted toward the cap; no rune split
```

**Surface table:**

| Producer | Surface | Cap |
|---|---|---|
| builtin success (`IsError == false`) | builtin-success | 64,000 |
| builtin failure (`IsError == true`) | builtin-failure | 10,000 |
| MCP success (`isError == false`) | mcp | 62,500 |
| MCP failure (`isError == true`) | builtin-failure | 10,000 |
| denied / skipped (`deniedMsg`, `skippedMsg`) | builtin-failure | 10,000 |
| hydrated attachment | builtin-success | 64,000 |
| recall page | builtin-success | 64,000 (payload = cap − framing) |
| `delegate` report | builtin-success | 64,000 |
| repair placeholder | exempt (bounded by construction) | — |

**Bounds:** user message = builtin-success cap (64,000), media refs excluded, enforced in `processMessage`; tool-call arguments = 64,000 on the serialised arguments string; ingest 8,000,000 bytes default, setting must be < 8,388,608.

**Provider locality (consumed from S67, not defined here):** `locality: local` ⇔ protocol ∈ {ollama, vllm} ∨ id = `lmstudio` ∨ custom row with loopback/private host → mandatory live query, no floor. `locality: cloud` (everything else, incl. a custom row at a public host) → floor 128,000 (`cloudWindowFloor`, the only `128000` constant in `pkg/agent`). Exempt ⇔ the row's `cli_driver` is a subprocess driver (today `codex-cli`); `openai-chatgpt` is cloud.

**Window resolution:** `effective = min(first-present(per-agent, per-(provider,model), global default), capability)` where capability = live-or-catalog value (or the floor when absent, cloud only); `clamped = chosen > capability`; live cache key `(provider id, base URL, model)` (a catalog `api` change therefore yields a new key), TTL 24 h, path `$OMNIPUS_HOME/cache/model_limits.json`, populated on demand only. `ResolveWindow(provider, model, agentID="")` is the single resolver; `NewAgentInstance` calls it with the agent id, S68's card without.

**HTTP / wire:**
- `GET/PUT /api/v1/settings/context` — `ContextSettings.yaml` / `ContextSettingsUpdate.yaml` (partial; omitted = unchanged); 400 `ErrorResponse` naming field and limit on: cap > 150,000 or < 1; `absolute_trigger_chars < 1`; `ingest_bound_bytes ≥ 8,388,608` or < 1; `model_overrides[].context_window < 1`. Every 200 write triggers `TriggerReload`. Middleware `withAuth`.
- `ContextWindowSource.yaml` (owned here): enum `operator | live | catalog | floor`; `$ref`'d by `Agent.context_window_source` and S68's `DefaultModel.window_source`. `Agent.yaml` (S67's coordinated commit): `context_window_effective` (int), `context_window_source` (`$ref`), `context_window_clamped` (bool) — all optional; `AgentUpdateRequest.yaml`: `context_window_override` (int ≥ 1, nullable to clear).
- Providers-catalog `GET` projection (S67's route): per-model `window_source` (`$ref ContextWindowSource`, absent for exempt) and `window_unknown` (bool, true iff `locality: local` and the live query failed).
- `ToolCall.yaml`: `content_state` (enum `full | capped | emptied`, default `full`).
- asyncapi: `tool_result_projection` `{tool_call_id, archive_line, content_state, mark}`; recall-mark and argument-refusal inline schemas (ADR-060 D1 checklist: schema, `*Code`, single producer via `marshalWithinBudget`, register entry).
- `LLMError` × 4 (`LLMError.yaml`, `LLMErrorReplay.yaml`, asyncapi inline `LLMError` and `LLMErrorReplay`): `turn_canceled` (`user`), `turn_timed_out` (`provider`), `context_unrecoverable` (`product`), `context_window_unknown` (`config`); `user` added to every copy's `x-user-message-attributions`; `TestContract_LLMError_AllClassifierCodesRoundTrip` and `TestContract_LLMErrorCatalogue_AllFourCopiesAgree` pass. Pre-turn gate order: `needs_provider` (S67) → `model_unassigned` (S68) → `context_window_unknown` (here).

**Tool interface:** `recall_conversation` exactly one of `query | turn_range | time | tool_call_id`; with `tool_call_id`: optional `archive_line ≥ 0`, `offset ≥ 0`, `length ≥ 1` (clamped to the page size); `max_results ≥ 1` on search modes. `list_directory`/`inspect_session`: `offset ≥ 0`, `limit ≥ 1`.

**Meta:** projection state keyed `(tool_call_id, archive_line)` with value `capped | emptied`; pruned when `archive_line < Skip`; restored to the turn-start set on rollback (entries with `archive_line ≥ initialArchiveLen` dropped).

**Logging / metrics (`pkg/gateway/metrics.go::toolMetrics`):** clamp WARN (agent, override, clamped value); floor WARN (model); warn-threshold WARN + `tool_result_large_total`; emptying INFO (session key, count, share before/after) + `context_empties_total`; guard ERROR; each typed exit: log line with raw cause + `EventKindTurnEnd` + transcript entry.

**Performance:** the check is O(window) estimator work per result, no LLM call, no archive read; recall-by-id streams the archive and stops at the match.

### Integration Boundaries

| System | In / Out | Contract | On failure | Development |
|---|---|---|---|---|
| Provider limits endpoints | out: model; in: limits | vendor HTTPS JSON; cached 24 h; credential from store | cloud → next rung; local → refusal | mocked HTTP in Go tests |
| Catalog (ADR-067 `Resolve`) | in: `context_window` | in-process | miss → floor (cloud) / refusal (local) | fixture catalog |
| MCP servers | in: results | Go SDK over bounded stdio/HTTP transport | > bound → tool failure at the transport | fake server |
| Provider chat completion | out: projected messages; in: response, errors | adapters | overflow → `context_too_long`; cancel/deadline → typed | fake provider |
| Session archive | out: full results, meta; in: history | JSONL + meta, atomic | write failure logged; trim reported failed | temp dir |
| SPA ↔ gateway | settings, agent fields, projection frame, `content_state`, codes | generated types + zod | zod drop + counter | generated |

---

## 6. BDD Scenarios

> HP/AP/EP/EC; every scenario `Traces to:` US-n.ACm.

### Feature: Window resolution (D2–D3)

**B-01 (HP)** catalog wins — US-1.AC1. Given no overrides, catalog 1,048,576 → 1,048,576 / `catalog`.
**B-02 (HP)** per-agent override lowers — US-1.AC2 → 100,000 / `operator`.
**B-02b (HP)** per-(provider, model) override — US-1.AC3. Given `model_overrides[{openrouter, z-ai/glm-5.2, 200000}]`, no per-agent → 200,000 / `operator`; with per-agent 100,000 → 100,000.
**B-03 (EC)** clamp — US-1.AC4. Override 2,000,000, capability 1,048,576 → 1,048,576, `clamped: true`, one WARN.
**B-03b (EC)** re-clamp on catalog change — US-1.AC8. Catalog lowered to 200,000 → next resolution 200,000; override persists.
**B-04 (AP)** live rung on demand, cached — US-1.AC5. Endpoint returns 200,000; resolved twice within 24 h → 200,000 / `live`, one fetch, key `(id, baseURL, model)`; no credential → rung skipped; cold cache → catalog now, live next reload; boot and a 25 h idle period perform **zero** fetches.
**B-04b (HP)** `ResolveWindow` without an agent — US-1.AC9. `(openrouter, z-ai/glm-5.2)` → 1,048,576 / `catalog`; with a per-agent override 100,000 and the agent id → 100,000; exempt provider → 0, no source.
**B-04c (EC)** dead override pruned — US-1.AC10.
**B-05 (AP)** exempt by driver — US-1.AC6. A row with subprocess `cli_driver` (`codex-cli`) → window 0; checks skipped; `openai-chatgpt` → cloud, floored.
**B-05b (EC)** max_tokens clamp — US-1.AC7 / E18. `W = 8,192`, `max_tokens = 8,192` → effective `max_tokens = 2,048`, one WARN, B > 0.
**B-06 (HP, outline)** one B for all — US-1.AC7. Consumers: pre-turn / mid-turn / timeout recovery / model-switch → same B.
**B-07 (AP)** cloud floor — US-2.AC1 → 128,000 / `floor`, WARN.
**B-08 (HP)** Ollama live — US-2.AC2 → 8,192 / `live`.
**B-09 (EP)** local refusal — US-2.AC3 → `context_window_unknown` with the Settings → Models → Model overrides message; catalog projection `window_unknown: true`; gate order third.
**B-10 (HP)** override clears refusal without restart — US-2.AC4. `PUT /settings/context` `model_overrides` 32,768 → reload triggered; next turn 32,768 / `operator`.
**B-10b (AP)** custom row at a public host is `locality: cloud` — US-2.AC5 → 128,000 / `floor`, never refused.

### Feature: Cap at the door (D4)

**B-11 (HP/EC, outline)** per-surface cap — US-3.AC1, AC2.
| producer | size | expected |
|---|---|---|
| mcp success | 62,500 / 62,501 / 1,178,522 | unmodified / capped / capped |
| builtin success | 64,000 / 64,001 / 200,000 | unmodified / capped / capped |
| builtin failure | 10,000 / 50,000 | unmodified / capped |
| mcp isError | 50,000 | ≤ 10,000 |
| denied / skipped | 50,000 | ≤ 10,000 |
| delegate report | 200,000 | ≤ 64,000 |
| hydrated attachment | 200,000 | ≤ 64,000 |
**B-11b (EC)** clamp on a small window — US-3.AC3. B = 3,000 tokens; 64,000-char result → ≤ 3,750 chars, marked.
**B-11c (EC)** parallel clamp — US-3.AC4. Three calls on that window → each ≤ 1,250; all three fit in B; floor intact; turn completes.
**B-12 (HP)** full in archive, capped on reload — US-3.AC5.
**B-12b (EC)** encoded-line bound — US-3.AC6, US-12.AC5. 8,000,000 newline bytes → archive line ≤ 8,388,608; `GetHistory` reads the session.
**B-13 (HP)** warn threshold observe-only — US-3.AC7.
**B-14 (EP)** cap ceiling — US-3.AC8, US-11.AC2. 150,001 → 400; 150,000 → 200; 0 → 400.
**B-15 (HP)** per-tool alignment — US-3.AC9.
**B-16 (HP)** filter before cap — US-3.AC10. A 100,000-char result with a secret straddling the head cut (around position `(64,000 − markLen)/2`) and another straddling the tail cut → both redacted in archive and capped copies, no fragment.
**B-16b (AP)** live settings — US-3.AC11. Cap lowered mid-turn → next result uses the new cap.
**B-17 (EP, outline)** oversized user message refused in `processMessage` — US-4.AC1. Intakes: WS, SSE, Slack channel → reply on that intake with N and 64,000; no transcript entry, no turn id, no error frame.
**B-18 (EC)** at the bound — US-4.AC2, AC3. 64,000 + 3 media refs → turn starts; cap changed to 50,000 → reply quotes 50,000.
**B-19 (EP)** argument refusal — US-5.AC1, AC3.
**B-20 (HP)** retry under cap — US-5.AC2.

### Feature: Empty in place (D5)

**B-21 (HP)** oldest eligible emptied — US-6.AC1, AC2. R1 → mark with name, id, line, size, turn number, hint; structure intact.
**B-21b (AP)** previous oversized turn eligible — US-6.AC1, E16. Pre-turn floor keeps turn T (oversized); D5 empties T's results oldest-first until under B.
**B-22 (HP)** live = reload bytes — US-6.AC3.
**B-23 (HP)** archive untouched — US-6.AC4 (asserts line count and every line's bytes).
**B-24 (HP)** abort → turn-start triple — US-6.AC5.
**B-25 (EC)** one pass; idempotent re-check — US-6.AC6.
**B-26 (AP)** mark hidden unless Verbose; frame + `content_state` — US-6.AC7. Emptying emits `tool_result_projection`; thread hides/shows; transcript read returns `content_state: emptied`.
**B-27 (HP)** marks permanent; prune on Skip — US-6.AC8, AC9.
**B-27b (HP)** emptying observability — US-6.AC10.

### Feature: Recall by id (D5 §6.3)

**B-28 (HP)** first page fits after framing — US-7.AC1. Payload = 64,000 − framing; message ≤ 64,000; unmodified by the choke point.
**B-29 (HP)** paging to the last byte; clamps/errors — US-7.AC2.
**B-29b (EC)** duplicate ids — US-7.AC3. Two lines with `call_0` → most recent; `archive_line` selects the other.
**B-30 (EC)** span-budget exempt, counted by D6 — US-7.AC4.
**B-31 (EP)** unknown / rolled-back id; mode exclusivity — US-7.AC5.
**B-31b (HP)** streaming scan — US-7.AC6.

### Feature: Mid-turn window check (D6)

**B-33 (HP)** check after every result against B — US-8.AC1.
**B-34 (HP)** trigger/target — US-8.AC2. Share 170,000 tokens → emptied to ≤ 128,000; total 1.1 B → emptied to ≤ 0.8 B.
**B-35 (HP, outline)** operation by site and position — US-8.AC3.
| site | oldest over-budget | operation |
|---|---|---|
| pre-turn | earlier complete turn | Skip advances (cut) |
| pre-turn | last turn, oversized (floor) | its results emptied oldest-first |
| mid-turn | earlier complete turn | **Skip unchanged**; its results emptied; request bytes shrink only by marks |
| mid-turn | current-turn result, older step | emptied |
| any | last assistant step | never |
**B-36 (HP)** floor satisfiable — US-8.AC4. 3 parallel results at effective cap against B → none emptied, fits, turn completes.
**B-36b (EC)** target unreachable, trigger satisfied → continue — US-8.AC5.
**B-37 (EP)** guard by injected fault only — US-8.AC6. `context_unrecoverable`, one ERROR, provider call count after guard = 0.
**B-38 (HP)** timeout recovery uses B; no `summarize_token_percent` — US-8.AC7.
**B-39 (HP)** order of operations, 2 MB result completes — US-8.AC8.

### Feature: Typed exits (D7)

**B-40 (EP, outline)** — US-9.AC1–3. cancel → `turn_canceled`/`user` (neutral notice); deadline → `turn_timed_out`/`provider`; guard → `context_unrecoverable`/`product`; each with log + event + transcript.
**B-41 (HP)** contract-defined, `user` attribution in vocabulary — US-9.AC4.

### Feature: Settings (D9)

**B-44 (HP)** read defaults / partial write round-trip / reload triggered — US-11.AC1, AC2.
**B-45 (HP)** agent fields derived; override write reloads; `withAuth` — US-11.AC3, AC4.

### Feature: Ingest (D10)

**B-46 (EP/HP, outline)** — US-12.AC1–4.
| source | bytes | outcome |
|---|---|---|
| MCP stdio | 8,000,000 | accepted |
| MCP stdio | 8,000,001 | aborted on transport; tool failure |
| MCP HTTP | 8,000,001 | aborted via MaxBytesReader; tool failure |
| Brave / DDG / Perplexity / GLM / Baidu | 8,000,001 | tool failure |
| fetch_url fallback | 8,000,001 | tool failure |
| Perplexity | 2,097,152 | accepted |

### Feature: Tier-1 bounding parameters (ADR §15.1)

**B-47 (HP, outline)** — US-13. `list_directory(offset, limit)`, `inspect_session(offset, limit)`, `recall_conversation(query, max_results)` bound their output; invalid values → tool error.

### Feature: Recall injection (D5.4)

**B-48 (HP)** nonce returns in the second request — US-14.AC1, AC3. Turn 1 holds nonce `N-7f3a`; Skip advanced past it; provider call 1 returns `recall_conversation(turn_range:"1-1")` → provider request 2 contains `N-7f3a` and the recall marker; tool result says "their text is now in your context".
**B-49 (EP)** span does not fit — US-14.AC2. Window too small → request 2 lacks the nonce; tool result states the non-fit with N and X tokens.
**B-50 (EC)** no double injection — US-14.AC4. A trim-site reassembly after injection → the marker appears once.
**B-50b (HP)** paged `tool_call_id` recall injects — US-14.AC5.
**B-50c (HP)** observability — US-14.AC6.
**B-50d (EC)** injected span emptied by D5 under pressure — US-14.AC7.
**B-51 (EC)** sub-turn limitation pinned — US-14.AC8. Child recall → empty result + INFO line.

### Feature: Hydration (D5.5)

**B-52 (HP)** attach twice, archive untouched — US-15.AC1. Archive with 21/53/36 lines → after two attaches bytes identical, `skip` unchanged.
**B-53 (HP)** empty archive hydrates once with tool results — US-15.AC2, AC3.
**B-53b (AP)** hydrated flag until the transcript field lands — US-15.AC4. `hydrated: true`; recall by id → "not available — session was rebuilt from the transcript".
**B-53c (EP)** `SetHistory` on a non-empty archive refused — US-15.AC5.
**B-53d (HP)** append-only invariant with an attach step — US-15.AC6.

---

## 7. TDD Plan

### Test Hierarchy
| Level | Scope |
|---|---|
| Unit | `pkg/agent` (ladder, choke point, clamp, projection, budget), `pkg/memory`, `pkg/tools`, `pkg/mcp`, `translate_error`, `pkg/config` |
| Integration | `runTurn`/`processMessage` with fake provider/tools/channel; gateway `httptest`; recall over a temp archive |
| E2E | vitest (Settings, mark visibility, codes); embedded-binary holdout |

### Test Implementation Order

| # | Test | Level | BDD | Description |
|---|---|---|---|---|
| 1 | `TestResolveContextWindow_Ladder` | Unit | B-01, B-02, B-02b, B-04, B-07, B-08 | six rungs, source |
| 2 | `TestResolveContextWindow_ClampAllRungs` | Unit | B-03, B-03b | clamp + WARN; re-clamp |
| 3 | `TestResolveContextWindow_ExemptByCliDriver` | Unit | B-05 | `codex-cli` row (subprocess driver) → 0; `openai-chatgpt` → cloud; no deleted-id literal |
| 3c | `TestResolveWindow_NoAgent` | Unit | B-04b, B-04c | rungs 2–6; exempt → 0/no source; dead override ignored |
| 3b | `TestNewAgentInstance_MaxTokensClampedWhenBudgetNonPositive` | Unit | B-05b | 8,192/8,192 → 2,048 + WARN |
| 4 | `TestResolveContextWindow_ByLocality` | Unit | B-08, B-09, B-10b | drives S67's `locality` predicate (fixture rows: ollama, lmstudio, custom loopback, custom public); asserts `window_unknown` in the projection and gate order third |
| 5 | `TestLiveLimits_OnDemandCacheKeyTTLCredential` | Unit | B-04 | key, TTL, no-credential skip, zero fetches at boot/idle |
| 6 | `TestWindowAgreement_OneBudgetAllSites` | Unit | B-06 | `isOverContextBudget` threshold = B at all four sites; source grep: no `maxTokens * 4`, no `contextWindow = 128000`/`newContextWindow = 128000`, exactly one `cloudWindowFloor` |
| 7 | `TestChokePoint_PerSurfaceCap` | Unit | B-11 | surface table |
| 8 | `TestChokePoint_ClampToHalfBudget` | Unit | B-11b, B-11c | effective cap; `/N` |
| 9 | `TestChokePoint_EncodedLineBound` | Unit | B-12b | 8,000,000 newlines |
| 10 | `TestChokePoint_FilterThenCap_AtRealCuts` | Unit | B-16 | secrets across head and tail cuts |
| 11 | `TestChokePoint_ProducerListByGrep` | Unit | US-3 | asserts the twelve `Role: "tool"` sites all call the choke point (repair exempt) |
| 12 | `TestToolArgsCap_StructuredRefusal` | Unit | B-19, B-20 | family shape |
| 13 | `TestRecallMark_SingleProducerSanitised` | Unit | B-21 | name/id sanitised; turn number = 1 + user lines |
| 14 | `TestProjection_PureFunction` | Unit | B-21, B-22 | one function, both views |
| 15 | `TestSessionMeta_ProjectionStateCompositeKey` (pkg/memory) | Unit | B-12, B-29b, B-27 | `(id, line)`; prune on Skip |
| 16 | `TestRollbackAppended_RestoresTurnStartEmptiedSet` (pkg/memory) | Unit | B-24 | drop entries ≥ initialArchiveLen; ephemeral no-op |
| 17 | `TestMidTurnBudget_OperationBySiteAndPosition` | Unit | B-35, B-36, B-21b | never Skip mid-turn; previous-turn eligible |
| 18 | `TestMidTurnBudget_TriggerTargetStop` | Unit | B-34, B-25, B-36b | fired condition; stop rule; no re-fire |
| 19 | `TestMidTurnBudget_SameBudgetAsWindowTrim` | Unit | B-38, B-06 | B only; `SummarizeTokenPercent` absent |
| 20 | `TestTranslateError_TypedExitsAndAttributions` | Unit | B-40, B-41 | codes, `user` attribution |
| 21 | `TestTranslateError_NoWindowLearning` | Unit | B-07 | overflow → `context_too_long`, no write-back |
| 22 | `TestConfig_NoContextWindowDefaultKey` (pkg/config) | Unit | US-11 | key/env removed; stale keys ignored silently |
| 23 | `TestIngestBound_MCPTransport` (pkg/mcp) | Unit | B-46 | stdio reader aborts at 8,000,001; HTTP MaxBytesReader |
| 24 | `TestIngestBound_SearchProvidersAll5` (pkg/tools) | Unit | B-46 | incl. GLM/Baidu raised |
| 25 | `TestIngestBound_SettingCeiling` | Unit | B-14 | < 8,388,608 |
| 26 | `TestRecallConversation_ToolCallID_PageFitsAfterFraming` | Integration | B-28 | ≤ 64,000 after framing, unmodified |
| 27 | `TestRecallConversation_ToolCallID_PagingReachesLastByte` | Integration | B-29 | concat equals archive; clamps |
| 28 | `TestRecallConversation_ToolCallID_DuplicateIds` | Integration | B-29b | most recent; `archive_line` |
| 29 | `TestRecallConversation_ToolCallID_ExemptNotFoundExclusiveStreaming` | Integration | B-30, B-31, B-31b | |
| 30 | `TestRunTurn_GuardTest_2MBResultCompletes` | Integration | B-39 (§17.1) | |
| 31 | `TestRunTurn_LongTurn_50CallsAtCap_SmallWindow` | Integration | B-33, B-21, B-36 (§17.2) | asserts archive bytes unchanged (B-23) |
| 32 | `TestRunTurn_LiveVsReloadBytesEqual` | Integration | B-22 (§17.2c) | |
| 33 | `TestRunTurn_AbortRestoresTurnStartTriple` | Integration | B-24 (§17.2b) | after two passes |
| 34 | `TestRunTurn_ThrashGuard_InjectedFaultOnly` | Integration | B-37 (§17.4) | call count after guard = 0; no reach without fault across DS-5 |
| 35 | `TestRunTurn_ArgsRefusal_TurnContinues` | Integration | B-19 | |
| 36 | `TestRunTurn_SilentExitsNowTyped` | Integration | B-40 (§17.5) | four sites |
| 37 | `TestRunTurn_LocalEndpointRefusedUntilOverrideReload` | Integration | B-09, B-10 (§17.6) | reload asserted |
| 38 | `TestProcessMessage_UserMessageBound_AllIntakes` | Integration | B-17, B-18 (§17.4) | WS, SSE, Slack fake channel |
| 39 | `TestRunTurn_MidTurnNeverAdvancesSkip` | Integration | B-35 row 3 (§17.4c) | request bytes shrink by marks only |
| 40 | `TestRunTurn_SmallWindowClamp` | Integration | B-11b, B-11c, B-36 (§17.4b) | 8,192 window |
| 41 | `TestGateway_ContextSettings_PartialUpdateCeilingReload` | Integration | B-14, B-44 | |
| 42 | `TestGateway_AgentWindowFieldsAndOverrideReload` | Integration | B-45 | |
| 43 | `TestGateway_ProjectionFrameAndContentState` | Integration | B-26 | frame emitted; transcript `content_state` |
| 44 | `contract_test.go` additions | Integration | B-41, B-26, B-44 | new schemas validate |
| 45 | `TestTier1Tools_BoundingParams` | Unit | B-47 | |
| 46 | `ContextSection.test.tsx` (Settings → Models section; independent of S68) | E2E | B-44, B-14 | |
| 46b | row/picker `window_unknown` state + default-model card window/source (**after S68's components land**) | E2E | B-09, B-04b | S66 UI tail |
| 47 | `toolVisibility.test.ts` + projection-frame zod test | E2E | B-26 | |
| 48 | `llm-error.test.ts` | E2E | B-41 | `user` attribution renders as notice |
| 49 | `TestRunTurn_RecallInjected_NonceInSecondRequest` | Integration | B-48, B-50c | fake recording provider; nonce + marker in request 2; INFO lines |
| 50 | `TestRunTurn_RecallNonFit_ToolResultStatesIt` | Integration | B-49 | no splice; message text |
| 51 | `TestRunTurn_RecallNotDoubledOnReassembly` | Integration | B-50 | marker count = 1 |
| 52 | `TestRunTurn_RecallByIdPageInjected` | Integration | B-50b | |
| 53 | `TestRunTurn_InjectedSpanSubjectToD5` | Integration | B-50d | |
| 54 | `TestSubTurn_RecallReadsParentStore_KnownLimitation` | Integration | B-51 | pins the empty outcome |
| 55 | `TestAttach_TwiceArchiveByteIdentical` (pkg/gateway, scoped) | Integration | B-52 | bytes + skip |
| 56 | `TestAttach_EmptyArchiveHydratesOnceWithToolResults` | Integration | B-53, B-53b | |
| 57 | `TestSetHistory_RefusesNonEmptyArchive` (pkg/memory) | Unit | B-53c | |
| 58 | `TestArchive_AppendOnlyWithAttachStep` | Integration | B-53d | extends the ADR-028 invariant test |

### Test Datasets

#### DS-1: Result size vs cap (chars)
| # | Surface | Size | Boundary | Expected | Traces |
|---|---|---|---|---|---|
| 1 | mcp | 62,500 | max | unmodified | B-11 |
| 2 | mcp | 62,501 | max+1 | capped | B-11 |
| 3 | mcp | 1,178,522 | incident | capped; archive full | B-11, B-12 |
| 4 | builtin-success | 64,000 / 64,001 | max / +1 | unmodified / capped | B-11 |
| 5 | builtin-failure | 10,001 | max+1 | capped | B-11 |
| 6 | mcp isError | 50,000 | — | ≤ 10,000 | B-11 |
| 7 | denied | 50,000 | — | ≤ 10,000 | B-11 |
| 8 | delegate | 200,000 | — | ≤ 64,000 | B-11 |
| 9 | attachment | 200,000 | — | ≤ 64,000 | B-11 |
| 10 | mcp, 4-byte runes at cut | 62,501 | unicode | no split | B-11 |
| 11 | builtin-success | 25,001 | warn | unmodified + WARN | B-13 |
| 12 | builtin-success | 100,000, secret across head cut | edge | redacted | B-16 |
| 13 | builtin-success | 100,000, secret across tail cut | edge | redacted | B-16 |
| 14 | builtin-success, B = 3,000 tok | 64,000 | small window | ≤ 3,750 | B-11b |
| 15 | 3 parallel, B = 3,000 tok | 64,000 each | small window | ≤ 1,250 each | B-11c |
| 16 | any | 8,000,000 newlines | encoded line | line ≤ 8,388,608 | B-12b |

#### DS-2: User message bound (chars), per intake (WS / SSE / Slack)
| # | Input | Expected | Traces |
|---|---|---|---|
| 1 | 63,999 / 64,000 | turn starts | B-18 |
| 2 | 64,001 / 500,000 | refused on the intake; nothing persisted | B-17 |
| 3 | 1,000 + 3 media refs | turn starts | B-18 |
| 4 | cap set to 50,000, message 50,001 | refused quoting 50,000 | B-18 |

#### DS-3: Argument cap
| # | Serialised args | Expected | Traces |
|---|---|---|---|
| 1 | 64,000 | executes | B-20 |
| 2 | 64,001 / 300,000 | refusal | B-19 |

#### DS-4: Window resolution
| # | Per-agent | Per-(p,m) | Global | Live | Catalog | Class | Expected | Traces |
|---|---|---|---|---|---|---|---|---|
| 1 | — | — | — | — | 1,048,576 | cloud | 1,048,576 catalog | B-01 |
| 2 | 100,000 | — | — | — | 1,048,576 | cloud | 100,000 operator | B-02 |
| 3 | — | 200,000 | — | — | 1,048,576 | cloud | 200,000 operator | B-02b |
| 4 | 100,000 | 200,000 | — | — | 1,048,576 | cloud | 100,000 operator | B-02b |
| 5 | 2,000,000 | — | — | — | 1,048,576 | cloud | 1,048,576 clamped + WARN | B-03 |
| 6 | — | — | 150,000 | — | 1,048,576 | cloud | 150,000 operator | B-02 |
| 7 | — | — | — | 200,000 | 1,048,576 | cloud | 200,000 live | B-04 |
| 8 | — | — | — | — | — | cloud | 128,000 floor + WARN | B-07 |
| 9 | — | — | — | 8,192 | — | ollama | 8,192 live | B-08 |
| 10 | — | — | — | none | — | vllm | refused (`context_window_unknown`) | B-09 |
| 11 | — | 32,768 | — | none | — | vllm | 32,768 operator | B-10 |
| 12 | — | — | — | none | — | custom row `my-proxy` @ public host (cloud) | 128,000 floor | B-10b |
| 13 | — | — | — | none | — | custom row `my-proxy` @ 127.0.0.1 (local) | refused `context_window_unknown` | B-09 |
| 14 | — | — | — | — | — | `codex-cli` (subprocess driver) | window 0, exempt | B-05 |
| 14b | — | — | — | — | catalog | `openai-chatgpt` (HTTP) | catalog value, cloud | B-05 |
| 15 | 1,048,576 | — | — | — | lowered to 200,000 | cloud | 200,000 clamped | B-03b |
| 16 | — | — | — | 8,192 (max_tokens 8,192) | — | ollama | W 8,192; max_tokens clamped to 2,048 + WARN | B-05b |

#### DS-5: Mid-turn budget positions
| # | Window (oldest → newest) | Fired | Expected | Traces |
|---|---|---|---|---|
| 1 | pre-turn: [prev turn][U][A(R1)][R1] | total | Skip advances past prev turn | B-35 |
| 2 | pre-turn: [U0][A][R×10 oversized] (single huge last turn) | total | floor keeps it; its results emptied oldest-first | B-35, B-21b |
| 3 | mid-turn: [prev turn][U][A(R1)][R1][A(R2)][R2] | total | Skip unchanged; prev turn's results emptied; bytes shrink by marks | B-35 |
| 4 | [U][A(R1)][R1][A(R2)][R2] | total | R1 emptied | B-35 |
| 5 | [U][A(R1,R2,R3)][R1][R2][R3] at effective cap | — | fits by clamp; nothing emptied | B-36 |
| 6 | 5 eligible, target needs 3 | share | 3 emptied in one pass; re-check no-op | B-25 |
| 7 | system prompt 30 % of B, all results emptied, total ≤ B | — | continue, no error | B-36b |
| 8 | injected oversized non-tool message | total | `context_unrecoverable` | B-37 |
| 9 | 8,192 window, one 200,000-char result | — | capped to ≤ 0.5 B; completes | B-11b, B-36 |

#### DS-6: Recall paging (total 1,178,522; payload = 64,000 − framing F)
| # | offset | length | Expected | Traces |
|---|---|---|---|---|
| 1 | 0 | — | chars 0…(64,000−F−1); message ≤ 64,000 | B-28 |
| 2 | last page offset | — | to 1,178,521 | B-29 |
| 3 | 1,178,522 | — | empty, total stated | B-29 |
| 4 | −1 | — | error | B-29 |
| 5 | 0 | 70,000 | clamped | B-29 |
| 6 | 0 | 0 | error | B-29 |
| 7 | duplicate id, no `archive_line` | — | most recent | B-29b |
| 8 | duplicate id, `archive_line` older | — | that line | B-29b |

#### DS-7: Ingest bound (bytes)
| # | Source | Bytes | Expected | Traces |
|---|---|---|---|---|
| 1 | MCP stdio | 2,097,152 / 8,000,000 | accepted | B-46 |
| 2 | MCP stdio | 8,000,001 | transport abort → failure | B-46 |
| 3 | MCP HTTP | 8,000,001 | MaxBytesReader → failure | B-46 |
| 4 | Brave / DDG / Perplexity / GLM / Baidu | 8,000,001 | failure | B-46 |
| 5 | fetch_url fallback | 8,000,001 | failure | B-46 |

#### DS-8: Settings validation
| # | Field | Value | Expected | Traces |
|---|---|---|---|---|
| 1 | mcp_result_cap | 150,000 / 150,001 / 0 | 200 / 400 / 400 | B-14 |
| 2 | absolute_trigger_chars | 400,000 / 0 | 200 / 400 | B-44 |
| 3 | ingest_bound_bytes | 8,388,607 / 8,388,608 / 10,485,760 | 200 / 400 / 400 | B-44 |
| 4 | model_overrides[].context_window | 32,768 / 0 | 200 / 400 | B-44 |
| 5 | partial body (only one field) | — | others unchanged; reload triggered | B-44 |
| 6 | agent context_window_override | 2,000,000 vs capability 1,048,576 | 200; effective 1,048,576 clamped; reload | B-45 |

#### DS-10: Recall injection / hydration
| # | Setup | Expected | Traces |
|---|---|---|---|
| 1 | span 2,000 tokens, B 100,000 | injected; receipt "now in your context" | B-48 |
| 2 | span 90,000 tokens, B 10,000 | not injected; non-fit message names N and 90,000 | B-49 |
| 3 | span exactly fits (total == B) | injected | B-48 |
| 4 | two recalls in one turn | second replaces first; one marker | B-50 / E20 |
| 5 | archive 110 lines, attach ×2 | identical bytes; skip unchanged | B-52 |
| 6 | archive 0 lines, transcript with 3 tool calls | 3 `role: tool` lines with result content | B-53 |
| 7 | archive 0 lines, transcript `result` absent (pre-field) | `hydrated: true`; recall by id → "not available…" | B-53b |
| 8 | `SetHistory` on 1-line archive | refused; skip unchanged | B-53c |

#### DS-9: Tier-1 bounding params
| # | Tool | Params | Expected | Traces |
|---|---|---|---|---|
| 1 | list_directory | offset 0, limit 50 | 50 entries | B-47 |
| 2 | inspect_session | limit 0 | error | B-47 |
| 3 | recall_conversation | query, max_results 3 | ≤ 3 turns | B-47 |

### Regression Test Requirements

| Existing behaviour | Existing test (unchanged) | New / updated | Notes |
|---|---|---|---|
| Turn-boundary cut pre-turn; archive-preserving; `SetHistory` never | `TestWindowTrim_CutsOnTurnBoundary`, `_KeepLastTurnAlignedFit`, `_NoDroppedMarker`, `_SetHistoryNeverCalled`, `TestArchive_FloorPathPreservesEvicted` | — | D6 additive |
| Single huge last turn kept whole pre-turn | `TestWindowTrim_SingleHugeTurn_KeepsLastUser` | **updated**: after the floor keeps it, D5 empties its results (B-21b) — assert Skip unchanged and marks present | pinned decision (register #3) |
| Recall-span drop-first | `TestWindowTrim_RecallSpanDropAloneReturnsOK`, `TestRecallSpan_*` | test 29 | |
| Model switch re-windows | `TestModelSwitch_*` | updated to the ladder | |
| Summariser stays deleted | `TestDecommission_NoForceCompressionSymbols` | extended (test 6) with `refreshRestorePointFromSession`, `restorePointHistory`, `SummarizeTokenPercent`, `maxTokens * 4`, the two `128000` fallback sites | greenfield |
| Estimator | `TestEstimateMessageTokens*`, `TestIsOverContextBudget*` | `TestIsOverContextBudget` updated to threshold B | |
| Orphan recovery | `TestRecovery_*` | `TestProjection_NeverOrphans` | |
| Rollback restores Skip | jsonl rollback tests; abort tests | test 16, 33 | new parameter; all fakes |
| `LLMError` copy rules | `translate_error_test.go`, `llm-error.test.ts` | extended | `user` attribution copy rules: may say "you stopped it" |
| Per-tool caps | tool tests | `browser_get_text`, shell updated | |
| ADR-060 family lint | `check-no-handwritten-wire-types.sh` | two discriminators registered | |
| Delegation identity | `subturn_target_identity_test.go` | **additive assertions only, after S67 re-keys the `mock` fixture** (X-31): child window from target's provider/model | |
| `AgentDefaults` / `defaults.go` edited by three specs (X-29) | `config_test.go` | S67's `TestSeeds_CanonicalProviderIDs` and S68's `TestDefaultsSeed_NoRemovedProvider` must pass after merge; land S67 → S68 → S66 | struct shrinks monotonically |
| `LLMError` four-copy agreement (X-01) | `llm_error_codes_test.go`, `llm_error_catalogue_test.go` | must pass with the four new codes + `user` | file edit in S67's contract commit |
| Grep gates (X-34) | tests 6, 11; S67 T29; S68 gate | evaluated on the merged branch | no deleted-provider id literal in this spec's tests |
| Recall span state + tool result string | `TestRecallConversation_*`, `TestRecallSpan_ReinjectedProviderValid`, `_ReplacedOnNextRecall`, `_NotPersistedToArchive` | **gap closed**: none asserted the next LLM request — tests 49–53 | receipt text changes (D5.4 d) → update string assertions |
| Attach hydration | `webchat_channel_test.go`, attach tests | tests 55–58 | `SetHistory` callers in tests must use empty stores |
| ADR-028 append-only archive | `TestArchive_*`, `TestWindowTrim_SetHistoryNeverCalled` | test 58 adds an attach step | |
| Replay `turn_canceled` role | replay tests | unchanged; `turn_canceled` LLMError is additional | |

---

## 8. Requirements & Success Criteria

### Functional Requirements

**D2 — resolution**
- **FR-001**: `ResolveWindow(provider, model, agentID="")` resolves per-agent override (only with `agentID`) → per-(provider, model) override (`ContextSettings.model_overrides[]`) → global default (`ContextSettings.default_context_window`) → on-demand live query (cached) → catalog (`catalog.Resolve(provider, model).Window()`, S67) → floor; records source (`ContextWindowSource`) and `clamped`; exempt → 0 with no source; entries for a deleted provider ignored and pruned on the next write.
- **FR-002**: `effective = min(chosen override, capability)` recomputed on every resolution; a clamp logs one WARN.
- **FR-003**: Live query **on demand only** (first resolution reaching the rung; never at boot, never on a timer), cached 24 h at `$OMNIPUS_HOME/cache/model_limits.json`, key (provider id, base URL, model), provider credential from the store, rung skipped without one; never on the turn path; cold cache → next rung now, live value at next reload.
- **FR-004**: `max_tokens × 4`, both `128000` fallbacks, `agents.defaults.context_window` (+ env var) and `summarize_token_percent` MUST NOT exist; exactly one `cloudWindowFloor` constant; all four consumers read one resolved window.
- **FR-005**: Providers whose catalog row has a subprocess `cli_driver` (today `codex-cli`; by field, never by id): `ContextWindow = 0`, pre-turn trim and every budget check skipped; `openai-chatgpt` is cloud.
- **FR-005b**: When `max_tokens` leaves `B ≤ 0`, `max_tokens` MUST be clamped to `floor(W/4)` with one WARN naming the model (A-18).

**D3 — unknown window**
- **FR-006**: Cloud class with no source → 128,000, source `floor`, one WARN.
- **FR-007**: `locality: local` (S67's predicate; not redefined here) → mandatory live query, no floor; `locality: cloud` → floor.
- **FR-008**: No live window → `context_window_unknown` (attribution `config`, third in the pre-turn gate) with the exact D3 message naming Settings → Models → Model overrides; the catalog `GET` projection carries `window_unknown: true` and per-model `window_source` (S68 renders); writing `model_overrides` triggers a reload and makes the model usable without restart.

**D4 — caps and bounds**
- **FR-009**: Exactly one function admits every tool result (the twelve producers; repair placeholder exempt and bounded by construction); a grep-style test enforces the list.
- **FR-010**: Caps per the surface table; warn threshold 25,000; ceiling 150,000; settings read per call; no opt-out.
- **FR-011**: `effective_cap = min(configured, floor(0.5·B·2.5))`; parallel N → `/N` when they would not fit; over-cap → 50/50 head-and-tail with the mark counted; full filtered content archived; state `capped` recorded.
- **FR-012**: Encoded-line bound: a result is capped so `len(json.Marshal(ArchivedMessage)) ≤ 0.8 × maxLineSize`.
- **FR-013**: Filter before cap; archive holds filtered content.
- **FR-014**: Per-tool alignment (`read_file` 64 KB; `browser_get_text` 64,000; shell 64,000/10,000).
- **FR-015**: User messages > builtin-success cap (media excluded) are refused in `processMessage` before registration/persistence, with a reply on the originating channel quoting the live cap; no error frame.
- **FR-016**: Arguments > 64,000 serialised → structured refusal (ADR-060 family), not executed, turn continues.

**D5 — empty in place**
- **FR-017**: Eligible = any `role: "tool"` whose call is in the window (including earlier turns kept by the pre-turn floor), excluding the floor set; empty oldest-first by replacing content with the mark in the in-memory slice before `callMessages` is built.
- **FR-018**: Mark: single typed producer via `marshalWithinBudget`; tool name and id ≤ 64 chars, non-printables stripped; archive line; size; turn number = 1 + preceding `role: user` lines; recall hint.
- **FR-019**: Projection state keyed `(tool_call_id, archive_line)` → `capped | emptied` in session meta; one pure projection function applied mid-turn and by `assembleMessages`; live and reload byte-identical; pruned when `archive_line < Skip`.
- **FR-020**: Restore point = turn-start `initialArchiveLen`, `initialHistoryLength`, `initialEmptiedSet`, captured once and never moved; `RollbackAppended(lines, skip, emptiedSet)` restores all three atomically; `refreshRestorePointFromSession` and `restorePointHistory` deleted; ephemeral store keeps state in memory, rollback parameter no-op.
- **FR-021**: One pass to target per trigger; no re-fire on immediate re-check.
- **FR-022**: Emptying emits `tool_result_projection`; transcript `ToolCall.content_state`; full content retained in the gateway `tool_results/` store; thread renders the mark only under Verbose chat.
- **FR-023**: Emptying INFO line + `context_empties_total`.

**D5 §6.3 — recall**
- **FR-024**: `tool_call_id` mode with optional `archive_line`, `offset`, `length` (runes); payload = effective cap − framing; contiguous pages to the last byte; `length` clamped; streaming scan stopping at the match.
- **FR-025**: Duplicate ids → most recent line unless `archive_line` given.
- **FR-026**: Exempt from span budgets; passes the choke point unmodified; counted by D6; emptiable.
- **FR-027**: Exactly one mode; unknown/rolled-back id → tool error with the id. Existing tool, new parameters — no policy change.

**D6 — one budget**
- **FR-028**: `B = W − max_tokens − ceil(0.05·W) − pinnedCoreOverhead`; `isOverContextBudget` threshold = B; pre-turn, mid-turn (after every result), timeout-recovery and model-switch all use it.
- **FR-029**: `over ⇔ total > B OR share > absoluteShare (160,000 default, = absolute_trigger_chars ÷ 2.5)`; target = 80 % of the fired condition; stop at target or when no eligible result remains.
- **FR-030**: Mid-turn never advances `Skip`; only the pre-turn trim cuts; `parseTurnBoundaries` unchanged.
- **FR-031**: Floor = every result of the most recent assistant message; satisfiable by FR-011.
- **FR-032**: Guard fires only if a trigger condition is still exceeded after all eligible results are emptied → `context_unrecoverable`, one ERROR, no further provider call; unreachable without an injected fault.
- **FR-033**: Order: ingest bound → filter → cap/clamp + line bound → archive append + state → check → empty → assemble → call.

**D7 — typed exits**
- **FR-034**: `turn_canceled` (attribution `user`, neutral notice in the SPA), `turn_timed_out` (`provider`), `context_unrecoverable` (`product`), `context_window_unknown` (`config`) in **all four** `LLMError` copies (`LLMError.yaml`, `LLMErrorReplay.yaml`, asyncapi inline `LLMError`/`LLMErrorReplay`); `user` added to every copy's attribution vocabulary; the round-trip and four-copies tests pass; the file edit ships in S67's coordinated contract commit; each exit → log line with raw cause + turn-end event + transcript entry.

**D8 — NOT ADOPTED**
- **FR-035**: No learning from provider error text; `contextOverflowSubstrings` classifies only.

**D9 — settings**
- **FR-036**: `GET/PUT /api/v1/settings/context` (`ContextSettings.yaml`, `ContextSettingsUpdate.yaml` partial) with caps, `absolute_trigger_chars`, `ingest_bound_bytes`, `default_context_window`, `model_overrides[{provider, model, context_window}]`; validation per §5; every write → `TriggerReload`; `withAuth`.
- **FR-037**: `ContextWindowSource.yaml` (owned here) is `$ref`'d by `Agent.context_window_source` and S68's `DefaultModel.window_source`; `Agent.yaml` (S67's coordinated commit) exposes the three read-only fields as optional; `AgentUpdateRequest.yaml` accepts `context_window_override`; write → `TriggerReload`; S68's default-model GET calls `ResolveWindow(provider, model)`.

**D10 — ingest**
- **FR-038**: Ingest bound default 8,000,000 bytes, setting < 8,388,608; MCP enforced on the transport read (stdio reader bound in `sandboxedStdioConn`; `http.MaxBytesReader` for HTTP/SSE); all five search providers bounded (the two 1 MiB sites raised); `fetch_url` fallback 8 MB; exceeding → tool failure, never truncation.

**D5.4 — recall injection (P0 hotfix, inherited)**
- **FR-041**: Immediately after a recall tool result is appended to `messages` mid-turn, the system MUST run the D6 fit check (budget B) and, if the span fits, splice `span.Messages()` at the `BuildMessages` position and run `sanitizeHistoryForProvider` on the combined slice.
- **FR-042**: If the span does not fit, the system MUST NOT splice and MUST NOT drop silently; the tool result MUST read *"N turn(s) found (X estimator tokens) but they do not fit the current window; narrow with turn_range or query"*.
- **FR-043**: The receipt MUST read *"Recalled N turn(s) (turns A–B); their text is now in your context"* only when injected; `assembleMessages` MUST skip a span already marked injected (`ts.injectedRecallSpan`, by identity); the `tool_call_id` mode MUST inject per page; set/injected/refused/dropped MUST log at INFO with sizes; an injected span is subject to D5/D6.
- **FR-044**: The sub-turn limitation (child recall reads the parent store under the ephemeral key → empty) MUST be pinned by a test and logged; it is not fixed here.

**D5.5 — hydration (P0 hotfix, inherited)**
- **FR-045**: The attach path MUST skip hydration when the agent archive has ≥ 1 line; the self-heal path keeps its emptiness condition.
- **FR-046**: When hydration runs, the rebuilt archive MUST contain a `role: "tool"` line per recorded tool call carrying the transcript's bounded `result` (D4-capped, written by the choke point); until that field lands, meta MUST carry `hydrated: true` and recall by id MUST answer *"not available — session was rebuilt from the transcript"*.
- **FR-047**: `SetHistory` MUST refuse a non-empty archive and MUST never reset `Skip` on an existing one.
- **FR-048**: The ADR-028 append-only invariant test MUST include an attach step.

**ADR §15 task 1**
- **FR-039**: `list_directory` and `inspect_session` MUST gain `offset`/`limit` (entries; `offset ≥ 0`, `limit ≥ 1`), named consistently with `read_file`'s interface.
- **FR-040**: `recall_conversation`'s `query`/`turn_range` modes MUST gain `max_results` (turns, ≥ 1).

### Success Criteria
- **SC-001**: 2 MB MCP result → turn completes, every request ≤ B (§17.1).
- **SC-002**: 50 calls at the cap on a 128,000 window → never over B; last step intact; older results marked; recall pages to the last byte (§17.2).
- **SC-003**: Abort after ≥1 empty → turn-start triple (§17.2b).
- **SC-004**: Catalog, `windowTrim`, pre-turn, mid-turn, timeout, model-switch → one window, one B (§17.3).
- **SC-005**: 64,001-char user message on WS, SSE and a channel → 0 transcript entries, 0 turns, 0 error frames; 64,001-char arguments → refusal then a further LLM call; the guard is reached only under an injected fault and then produces `context_unrecoverable` with 0 further provider calls (§17.4).
- **SC-006**: Each of the four silent returns → ≥1 log line, 1 turn-end event, 1 transcript entry; never `unknown` (§17.5).
- **SC-007**: `locality: local` endpoint with no window → `context_window_unknown`, `window_unknown: true` in the projection, never 128,000; override write → reload → next turn runs (§17.6).
- **SC-014**: After `recall_conversation(turn_range:"1-1")` of an evicted turn, the provider's second request contains the nonce and marker; with a too-small window it does not and the tool result states the non-fit (§17.8).
- **SC-015**: Attaching a session twice leaves the archive byte-identical with `skip` unchanged; an empty archive hydrates once with tool results; `SetHistory` on a non-empty archive is refused (§17.9).
- **SC-013**: `ResolveWindow(provider, model)` without an agent returns the rung-2–6 window + source; exempt → 0/no source (§17.7).
- **SC-008**: On an 8,192-token model: a 200,000-char result enters ≤ 0.5 B; a 3-call step fits; no guard (§17.4b).
- **SC-009**: `grep -rn 'maxTokens \* 4\|contextWindow = 128000\|newContextWindow = 128000\|SummarizeTokenPercent\|refreshRestorePointFromSession\|restorePointHistory' pkg/agent pkg/config` → empty; exactly one `cloudWindowFloor`.
- **SC-010**: Live bytes = reload bytes for an emptied message (§17.2c).
- **SC-011**: Mid-turn, `Skip` never changes (§17.4c).
- **SC-012**: `make verify-contracts`, `golangci-lint`, `gofmt -l | wc -l == 0`, `npm run typecheck`, `npx vitest run` exit 0.

### Traceability Matrix

| FR | US | BDD | Tests |
|---|---|---|---|
| FR-001 | US-1 | B-01, B-02, B-02b, B-04, B-04b, B-04c, B-07, B-08 | 1, 3c, 4, 5 |
| FR-002 | US-1 | B-03, B-03b | 2 |
| FR-003 | US-1 | B-04 | 5 |
| FR-004 | US-1 | B-06 | 6, 22 |
| FR-005 | US-1 | B-05 | 3 |
| FR-005b | US-1 | B-05b | 3b |
| FR-006 | US-2 | B-07, B-10b | 1, 4 |
| FR-007 | US-2 | B-08, B-09 | 4 |
| FR-008 | US-2 | B-09, B-10 | 4, 37, 46b |
| FR-009 | US-3 | B-11 | 7, 11 |
| FR-010 | US-3, US-11 | B-11, B-13, B-14, B-16b | 7, 41 |
| FR-011 | US-3 | B-11b, B-11c, B-12 | 8, 15, 40 |
| FR-012 | US-3, US-12 | B-12b | 9 |
| FR-013 | US-3 | B-16 | 10 |
| FR-014 | US-3 | B-15 | 7 |
| FR-015 | US-4 | B-17, B-18 | 38 |
| FR-016 | US-5 | B-19, B-20 | 12, 35 |
| FR-017 | US-6, US-8 | B-21, B-21b, B-35 | 14, 17 |
| FR-018 | US-6 | B-21 | 13 |
| FR-019 | US-6 | B-22, B-27, B-29b | 14, 15, 32 |
| FR-020 | US-6 | B-24 | 16, 33 |
| FR-021 | US-6 | B-25 | 18 |
| FR-022 | US-6 | B-26 | 43, 44, 47 |
| FR-023 | US-6 | B-27b | 31 |
| FR-024 | US-7 | B-28, B-29, B-31b | 26, 27, 29 |
| FR-025 | US-7 | B-29b | 28 |
| FR-026 | US-7 | B-30 | 29 |
| FR-027 | US-7 | B-31 | 29 |
| FR-028 | US-8 | B-33, B-38, B-06 | 19, 31 |
| FR-029 | US-8 | B-34, B-36b | 18 |
| FR-030 | US-8 | B-35 | 17, 39 |
| FR-031 | US-8 | B-36 | 17, 40 |
| FR-032 | US-8 | B-37 | 34 |
| FR-033 | US-8 | B-39 | 30 |
| FR-034 | US-9 | B-40, B-41 | 20, 36, 44, 48 |
| FR-035 | US-1 (non-behaviour) | B-07 | 21 |
| FR-036 | US-11 | B-44, B-14 | 41, 44, 46 |
| FR-037 | US-11 | B-45, B-04b | 42, 3c, 44 |
| FR-038 | US-12 | B-46 | 23, 24, 25 |
| FR-039 | US-13 | B-47 | 45 |
| FR-040 | US-13 | B-47 | 45 |
| FR-041 | US-14 | B-48, B-50b | 49, 52 |
| FR-042 | US-14 | B-49 | 50 |
| FR-043 | US-14 | B-48, B-50, B-50c, B-50d | 49, 51, 53 |
| FR-044 | US-14 | B-51 | 54 |
| FR-045 | US-15 | B-52 | 55 |
| FR-046 | US-15 | B-53, B-53b | 56 |
| FR-047 | US-15 | B-53c | 57 |
| FR-048 | US-15 | B-53d | 58 |

**Completeness:** every FR has ≥1 scenario and ≥1 test; every scenario above appears in a row (B-23 via test 31's archive-bytes assertion; B-42/B-43 withdrawn with D8).

### Exit-proof traceability (ADR §17, as amended)

| §17 | FRs | BDD | Tests | SC |
|---|---|---|---|---|
| 1 | FR-009–012, FR-017, FR-028, FR-033, FR-038 | B-39 | 30 | SC-001 |
| 2 | FR-017–019, FR-024–026, FR-028–031 | B-21, B-28, B-29, B-33, B-36 | 27, 31 | SC-002 |
| 2b | FR-020 | B-24 | 16, 33 | SC-003 |
| 2c | FR-019 | B-22 | 32 | SC-010 |
| 3 | FR-001, FR-004, FR-028 | B-06 | 6, 19 | SC-004 |
| 4 | FR-015, FR-016, FR-032 | B-17, B-19, B-37 | 34, 35, 38 | SC-005 |
| 4b | FR-011, FR-031 | B-11b, B-11c, B-36 | 40 | SC-008 |
| 4c | FR-030 | B-35 | 39 | SC-011 |
| 5 | FR-034 | B-40 | 20, 36 | SC-006 |
| 6 | FR-007, FR-008 | B-09, B-10, B-10b | 4, 37 | SC-007 |
| 7 | FR-001, FR-037 | B-04b | 3c | SC-013 |
| 8 | FR-041–044 | B-48, B-49, B-51 | 49, 50, 54 | SC-014 |
| 9 | FR-045–048 | B-52, B-53, B-53c | 55, 56, 57 | SC-015 |

---

## 9. Prerequisites, Setup, Stack, Runtime

- **Requires S67 merged** (`catalog.Resolve(provider, model).Window()`, `locality`, `cli_driver`, the coordinated contract commit) — until then test 1's catalog rows are skipped, not faked (X-27). S66's SPA work is split: the Settings → Models context section (test 46, independent) and the row/picker/card tail (test 46b, after S68). Land S67 → S68 → S66 (X-29).
- Go 1.26.4 toolchain (targets 1.22+), Node 20+, `golangci-lint`, `govulncheck`; no new runtime dependencies. Provider endpoints mocked in tests. `make gen-contracts` after editing `contracts/`; SPA embed sync before an embedded-binary check; push for CI.
- **Runtime:** one new cache file `$OMNIPUS_HOME/cache/model_limits.json`; no new listeners; logs in `$OMNIPUS_HOME/logs/gateway.log`.

---

## 10. Ambiguity Self-Audit

| # | Status | Resolution |
|---|---|---|
| A-1 | RESOLVED (ADR f01d5278) | Recall page = builtin success cap 64,000 (payload = cap − framing per MAJ-012). |
| A-2 | RESOLVED (ADR f01d5278) | User bound = builtin cap, tracks it, reply quotes live value; enforced in `processMessage` (MAJ-001). |
| A-3 | ACCEPTED | Argument cap 64,000 serialised; media refs excluded. |
| A-4 | ACCEPTED | `turn_canceled`, `turn_timed_out`, `context_unrecoverable`; attributions `user` / `provider` / `product` (MAJ-013). |
| A-5 | SUPERSEDED (CRIT-001) | One budget B, no 0.9; share > 160,000; target 80 % of the fired condition. |
| A-6 | ACCEPTED | Filter then cap. |
| A-7 | ACCEPTED | Name (and id) ≤ 64 chars, non-printables stripped. |
| A-8 | RESOLVED | 8 MB (8,000,000) transport bound, setting < 8,388,608; plus the encoded-line bound (MAJ-008). |
| A-9 | ACCEPTED | 24 h, `$OMNIPUS_HOME/cache/model_limits.json`; key (id, base URL, model). |
| A-10 | ACCEPTED | `operator \| live \| catalog \| floor` + `clamped`; now a shared `ContextWindowSource.yaml` (X-06). |
| A-11 | ACCEPTED | WARN + `tool_result_large_total` in `metrics.go::toolMetrics`. |
| A-12 | REVISED (MAJ-006) | Turn number = 1 + preceding `role: user` archive lines. |
| A-13 | ACCEPTED | `/api/v1/settings/context`, `ContextSettings.yaml`; agent fields on `Agent.yaml`/`AgentUpdateRequest.yaml` (S67 commits); user-facing location **Settings → Models** (X-37); ADR-068 route separate. |
| A-14 | MOOT | D8 not adopted. |
| A-15 | ACCEPTED | `capped \| emptied`, now keyed `(id, line)` (MAJ-007). |
| A-16 | ACCEPTED | 50/50, mark counted. |
| A-17 | ACCEPTED | No policy change. |
| A-18 | **ACCEPTED** (coordinator 2026-08-22) | When `max_tokens ≥ W` (B ≤ 0), `max_tokens` is clamped to `floor(W/4)` with a WARN naming the model; constraint in §5, boundary row DS-4 #16, FR-005b, B-05b. |
| A-19 | **ACCEPTED** (coordinator 2026-08-22) | `list_directory` and `inspect_session` gain `offset`/`limit`; `recall_conversation` query/range modes gain `max_results`; consistent with `read_file`'s interface (FR-039/040). |

---

## 11. Holdout Evaluation Scenarios *(post-implementation; NOT in traceability)*

1. **(Happy)** Real MCP server returning > 1 MB → turn completes; Verbose chat shows the mark; archive holds full content.
2. **(Happy)** 128k model, ~40 tool calls → no "didn't finish"; marks under Verbose; recall by id works.
3. **(Happy)** Per-agent override above capability → clamped value and `clamped` badge; WARN in log.
4. **(Happy)** Ask for a long delegated report → the parent's view is capped at 64,000 with a mark; recall retrieves the rest (product decision visible).
5. **(Error)** Paste 100,000 chars in the SPA, then the same via a Slack channel → both refused with size and limit; no session entry.
6. **(Error)** Press Stop mid-turn → neutral "you stopped this turn" notice (not an error toast); transcript entry; log line.
7. **(Edge)** Ollama model with no reported context length → refusal message names Settings → Models → Model overrides; the provider row shows the state; setting it works without restart.
8. **(Edge)** 8k Ollama model, a tool returning 200 KB → turn completes; result visibly capped.
9. **(Edge)** Abort right after results were emptied → new turn sees un-emptied (capped/full) results.
11. **(Happy)** Ask the agent to recall a specific earlier turn by number and quote a phrase from it → the quote is correct in the same reply (not "the text isn't appearing").
12. **(Edge)** Open an old session in the browser, then ask the agent about a tool result from that session → it can recall it; the archive file size did not shrink on open.
10. **(Edge)** Live-equals-reload through the embedded binary against a real provider adapter (Anthropic and an OpenAI-compatible one): provider request bytes for an emptied message equal the reload assembly.

---

## 12. Assumptions & Clarifications

- Register #3 confirmed by the coordinator (earlier oversized turns kept by the pre-turn floor are eligible for emptying — ADR D5 "When" amendment stands). A-18 and A-19 accepted.
- ADR-067 supplies `Resolve(provider, model).context_window`; consumed, not specified.
- The estimator (2.5 chars/token) is the unit of all token arithmetic (no calibration path).
- Recall `offset`/`length` are runes, not bytes (differs from `read_file`'s byte offsets; same parameter names only).

## 13. Review dispositions (2026-08-22)

**Cross-spec review (S66/A66 items):** X-01, X-06, X-07, X-08, X-09, X-16, X-17, X-18, X-19, X-20, X-27, X-28, X-29, X-31, X-34, X-35, X-36, X-37, X-42 — all verified against the tree and applied (X-37 per the coordinator: Settings → Models). None refuted.

**Single-document spec review:**

All 37 findings verified against the branch; none refuted. CRIT-001…004 per the coordinator's decisions; MAJ-001…016 applied as decided; MIN-001…012 applied; OBS-001…005 applied. Register #3 (pre-turn-kept oversized turns) resolved by making earlier-turn results eligible for emptying (FR-017, B-21b), pinned in the regression table — confirmed by the coordinator. A-18 and A-19 accepted and closed.

### Summary

- User stories: **14** (US-1…US-9, US-11…US-15; US-10 withdrawn) — US-14/US-15 are P0 hotfixes inherited by this branch
- BDD scenarios: **72** (HP 37 · AP 10 · EP 12 · EC 13; 10 outlines)
- Test datasets: **10**, **83** rows
- Functional requirements: **49** (incl. FR-005b; FR-035 is the D8 non-behaviour; FR-041…048 are the D5.4/D5.5 preconditions)
- Success criteria: **15**
- Tests planned: **61** (28 unit, 29 integration, 4 E2E)
- Ambiguities: **19** — **all closed**
