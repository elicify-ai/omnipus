# Spec — ADR-066: Context overflow — window resolution, per-result cap, empty-in-place, mid-turn window check

- **Source ADR:** `docs/internal/architecture/ADR-066-context-budget-and-tool-result-routing.md` (Proposed, restructured 2026-08-22; pass-2 review findings resolved in §16a). Review record: `docs/internal/architecture/ADR-066-context-budget-and-tool-result-routing-review-pass2.md`.
- **Status:** Draft (plan-spec) — written 2026-08-22 against branch `feat/context-budget-and-tool-result-routing`. Phase 1 gate: the ADR is treated as the confirmed requirements brief (reviewed twice, every finding resolved). Where the ADR is silent this spec does **not** decide — it records the gap in §10 (Ambiguity Self-Audit) with the assumption it proceeds under, clearly labelled **[A-n]** wherever that assumption is used.
- **Scope:** ADR-066 only — D2, D3, D4, D5 (+ recall-by-`tool_call_id`), D6, D7, D9, D10, and the §16a pass-2 resolutions. **D8 (learn the window from provider error text) is NOT ADOPTED** (operator decision, ADR-066 commits `ec2e022d`, `80aef474`, `06e6cc17`) — nothing here learns limits from provider errors. **D1 (the registry-fed catalog) is ADR-067 and is referenced, not specified.** Subscriptions / provider deletion / provider UX are ADR-068 and are out of scope.
- **Greenfield rule (operator, 2026-08-22):** no backward compatibility, no migration, no aliasing of old names, no grace periods. Pre-existing state that does not match this design simply does not work. Designed runtime fallbacks (conservative floor for an unknown cloud model) are not compatibility mechanisms.
- **Tech:** Go (`pkg/agent`, `pkg/memory`, `pkg/tools`, `pkg/mcp`, `pkg/gateway`, `pkg/providers`) · React 19 + Vite (SPA) · contract-first (`contracts/*`, Constraint #8).
- **Citation rule:** `pkg/agent/loop.go` and `pkg/agent/turn.go` are cited as `file::symbol` only — never by line number (they churn).
- **Test conventions:** Go tests run with `-tags goolm,stdjson`; never run the full gateway suite locally (CI is the authority); at most one narrowly-scoped local test (`-run '^TestName$' -p 1`).

---

## 1. Overview

On 2026-08-21 a production turn died silently after two MCP tool results (1.18 MB and 0.82 MB) entered the context in one turn. Four defects were diagnosed (ADR-066 §1): the context window was resolved from `max_tokens × 4` (wrong by 8×); the MCP path admits results of any size; the sliding window is consulted only before the first LLM call of a turn and can only cut at user-message boundaries; and four turn exits emit no log, event or transcript entry.

This spec covers the incident fix: resolve the window from a ladder with a lower-only clamp and a loud floor (D2–D3); cap every tool result at one choke point, bound user messages at the gateway and tool-call arguments as a structured refusal (D4); when the window is over budget mid-turn, empty the oldest eligible tool result in place and leave a recall mark that `recall_conversation` can resolve by `tool_call_id`, in pages (D5); run the window check after every tool result with a floor of the whole last assistant step and a thrash guard (D6); give every turn exit a typed code (D7); expose caps, trigger and the effective window with its source in Settings (D9); bound ingest at 8 MB, strictly below the archive line ceiling (D10).

Nothing is summarised, nothing is deleted from disk, no new storage is introduced. `windowTrim` remains the only compaction path (ADR-028, extended not superseded).

---

## 2. Available Reference Patterns

`docs/reference/go-implementation/` does not exist in this repository (checked 2026-08-22). **N/A.** Internal patterns reused instead: the ADR-060 structured tool-failure family (`marshalWithinBudget`, single producer, inline asyncapi schema) for the D4 argument refusal and the D5 recall mark; the ADR-028 archive-preserving `TruncateHistory`/`RollbackAppended` discipline; the ADR-051 `LLMError` classifier and generated user-message catalogue for D7; the `/settings/memory` + `MemorySettings.yaml` dedicated-settings pattern for D9.

---

## 3. Existing Codebase Context

> GitNexus MCP tools were not exposed in the session that wrote this spec (ToolSearch returned no `gitnexus` tool). Context below is from direct Read/Grep on branch `feat/context-budget-and-tool-result-routing` — the sanctioned fallback. Re-run `impact` on each "modifies" symbol before editing, per CLAUDE.md.

### Symbols Involved

| Symbol | Role | Context (verified) |
|---|---|---|
| `pkg/agent/loop.go::runTurn` | **modifies** | Pre-turn `isOverContextBudget` check → `windowTrim` → `assembleMessages` (site 2) → user message saved to session → tool loop. Tool results are appended to the in-memory `messages` slice; each LLM call uses `callMessages` built from it. Four `"turn canceled"`/`"turn timed out"` returns emit nothing. One success-path `toolResultMsg` site plus seven `deniedMsg` sites build `role: "tool"` messages. |
| `pkg/agent/loop.go::windowTrim` | **modifies** | Budget = `contextWindow − maxTokens − ceil(5%) − pinnedCoreOverhead` in estimated tokens; walks `parseTurnBoundaries` (user-role indices only); advances `Skip` via `TruncateHistory`; flat `128000` fallback when `ContextWindow <= 0`. |
| `pkg/agent/loop.go::assembleMessages` | **modifies** | Runs at turn start, post-trim, and reload sites only; must apply the same projection (capped/emptied) as the mid-turn path. |
| `pkg/agent/context_budget.go::isOverContextBudget`, `::parseTurnBoundaries`, `::estimateMessageTokens` | **calls / unchanged** | Estimator is `chars × 2/5` (2.5 chars/token) + 12 overhead + 256/media item. `parseTurnBoundaries` stays as is (D6: the new operation is orthogonal). |
| `pkg/agent/instance.go::NewAgentInstance` | **modifies** | `contextWindow = maxTokens * 4` fallback (retired); `SummarizeTokenPercent` default 75 (removed). |
| `pkg/agent/loop.go` model-switch re-window (`newContextWindow = 128000`) | **modifies** | Second flat fallback; consolidates onto the D2 ladder. |
| `pkg/agent/turn.go::restoreSession`, `::refreshRestorePointFromSession`, `turnState.initialArchiveLen/initialHistoryLength` | **modifies** | Restore point today = archive line count + `Skip`; gains the emptied-set. |
| `pkg/memory/store.go::Store.RollbackAppended(ctx, key, targetLines, targetSkip)` | **modifies (interface)** | Gains an emptied-set target, written atomically with `Skip`. |
| `pkg/memory/jsonl.go::sessionMeta` (`skip`, `count`), `::GetHistory`, `::ReadArchive`, `maxLineSize = 10 MB` | **extends** | Meta gains a per-`tool_call_id` projection state; archive lines are `ArchivedMessage{providers.Message; TS}` so `ToolCallID` is on disk. |
| `pkg/agent/recall_conversation.go::RecallConversationTool` (`recallDefaultTokens = 4000`, `recallRangeTokens = 8000`, `buildRecallSpanMessages` id remap) | **extends** | Gains the `tool_call_id` mode with `offset`/`length`. |
| `pkg/agent/translate_error.go::contextOverflowSubstrings`, `::classifyByMessage`, `CodeContextTooLong`, `CodeUnknown`, generated `LLMErrorUserAttributions` | **extends** | D7 new codes only. `contextOverflowSubstrings` keeps its classification job; no numeric extraction, no feedback (D8 not adopted). |
| `contracts/components/schemas/LLMError.yaml` (`x-user-messages` catalogue) | **extends** | New codes + copy + attribution (both Go/TS catalogues generated). |
| `pkg/mcp/manager.go::Manager.CallTool` | **modifies** | No truncation, no ingest bound today. |
| `pkg/tools/web.go` (`io.ReadAll(resp.Body)` at the Brave/DuckDuckGo/Perplexity search providers; two `LimitReader(…, 1<<20)` sites; `fetch_url` `MaxBytesReader` 10 MB) | **modifies** | D10 bounds the three unbounded reads and aligns `fetch_url`'s fallback to 8 MB. |
| `pkg/tools/filesystem.go::MaxReadFileSize` (64 KB), `pkg/tools/web.go::defaultMaxChars` (50,000), `pkg/tools/browser/tools.go::maxGetTextBytes` (100 KiB), `pkg/tools/shell.go::maxForegroundOutputLen` (10,000) | **modifies** | Per-tool caps aligned to D4 figures. |
| `pkg/tools/result.go::ToolResult`, `::marshalWithinBudget`, ADR-060 family register (`scripts/check-no-handwritten-wire-types.sh`) | **extends** | D4 argument refusal and D5 recall mark producers. |
| `pkg/gateway/websocket.go::handleChatMessage` (and the webchat/SSE intake) | **modifies** | D4 user-message bound, before a turn is registered. |
| `contracts/openapi.yaml` `/settings/memory` + `MemorySettings.yaml` | **pattern for** | D9 `ContextSettings` endpoint. |
| `src/lib/toolVisibility.ts::shouldRenderToolCall`; `src/components/settings/ChatSection.tsx`, `MemorySection.tsx` | **extends** | Mark rendered only under Verbose chat; new Context settings section. |
| `pkg/providers/capabilities` (`Catalog.Resolve`, `optimistic`) — being folded into `pkg/providers/catalog` by ADR-067 | **calls** | D2 catalog rung reads `Resolve(provider, model).context_window` (ADR-067 D1). |

### Impact Assessment

| Symbol Modified | Risk | d=1 Dependents (WILL BREAK / must test) | d=2 (LIKELY AFFECTED) |
|---|---|---|---|
| `loop.go::runTurn` tool loop (choke point, mid-turn check, typed exits) | **CRITICAL** | every turn; `subturn.go::spawnSubTurn` (delegated children use the same loop); `scenario_runturn_test.go`, `runturn_*_test.go`, `turn_test.go` | channels, gateway WS `DoneFrame`/error frames, ActivityPanel |
| `loop.go::windowTrim` budget + fallback | **HIGH** | pre-turn trim site, timeout-recovery site, model-switch re-window; `window_trim_test.go` (11 tests) | recall span drop (`TestWindowTrim_RecallSpanDropAloneReturnsOK`) |
| `memory.Store.RollbackAppended` signature | **HIGH** | `JSONLStore`, `ephemeralSessionStore` (`subturn.go`), `session` unified store adapters, all `Store` fakes in tests | `restoreSession` rollback verification |
| `instance.go::NewAgentInstance` window resolution | **HIGH** | every agent boot; `subturn.go` execSource copy; `TestModelSwitch_*` | Settings "effective window" read path |
| `LLMError.yaml` enum | **MEDIUM** | generated Go/TS catalogues; `translate_error_test.go`; `src/lib/llm-error.test.ts` | SPA error rendering |
| `recall_conversation.go` | **MEDIUM** | `recall_conversation_test.go` (17 tests), tool policy seed (Constraint #6 — new parameters, not a new tool) | `windowTrim` span budgeting |
| `mcp/manager.go::CallTool`, `tools/web.go` search providers | **MEDIUM** | `search_tools_*_test.go`, MCP tool wrapper | — |
| gateway chat intake | **MEDIUM** | `webchat_channel_test.go`, WS handler tests | SPA composer |
| per-tool cap constants | **LOW** | `filesystem`/`web`/`browser`/`shell` tests asserting the old numbers | skills/prompts assuming 100 KiB `browser_get_text` (lowered to 64,000) |

**HIGH/CRITICAL flag:** `runTurn` and `windowTrim` are the core of every turn. The implementing agent must run `impact` on both before editing and keep the D6 change additive (new call site + new operation), leaving `parseTurnBoundaries` and the existing cut path unchanged.

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Turn start → pre-turn budget check → `windowTrim` → `assembleMessages` → save user message | Unchanged in order; `assembleMessages` now applies the projection; window resolved by D2 instead of `maxTokens×4`. |
| Tool loop: execute tool → filter sensitive data → build `role: "tool"` message → append → next LLM call | Becomes: ingest bound → filter → **D4 cap at the single choke point** → append full to archive + record cap state → **D6 check** → **D5 empty to target** → refresh restore point → assemble → call. |
| Turn abort → `restoreSession` → `RollbackAppended` | Restores line count, `Skip` **and the emptied-set**. |
| `recall_conversation` → `ReadArchive` → span re-injection with id remap | Gains `tool_call_id` paged mode, exempt from span token budgets. |
| Provider error → `TranslateTurnError` → `LLMError` frame | D7 typed exits; overflow classifies to `context_too_long` as today. |
| Gateway chat intake → turn registration | D4 user-message bound refuses before registration. |

### Cluster Placement

Primarily the **agent loop / context paging** cluster (`pkg/agent`, `pkg/memory`); touches **tools** (`pkg/tools`, `pkg/mcp`) for D4/D10, **gateway + contracts** for D7/D9 and the user-message bound, **providers** for D2/D3, and the **SPA Settings** cluster for D9. Cross-cluster by necessity; the single choke point (D4) and the single projection function (D5) are the two seams that keep it coherent.

---

## 4. User Stories & Acceptance Criteria

Priorities: P0 = incident fix cannot ship without it; P1 = required for the ADR's exit proof; P2 = required by the ADR but not on the exit-proof path.

### US-1 — Resolve the context window from a ladder with a lower-only clamp (D2) — **P0**
As an operator, I want each agent's context window resolved from the best available source — per-agent override → global default → live provider query → catalog → floor — with overrides only able to lower the result, so the window record is never larger than the model's real capability.
- **Why P0:** §1.1 — the 8× error is the first diagnosed defect; every other decision computes against this number.
- **Independent test:** for a fixed model with known catalog/live values and various overrides, the resolved window equals the expected rung's value, clamped to the minimum of override and catalog-or-live, and the resolution is reported with its source.
- **Acceptance:**
  1. **Given** no override and a catalog entry of 1,048,576 for (provider, model), **When** the agent is built, **Then** the effective window is 1,048,576 with source `catalog`.
  2. **Given** a per-agent override of 100,000 and a catalog value of 1,048,576, **When** resolved, **Then** the effective window is 100,000 with source `operator`.
  3. **Given** an override of 2,000,000 and a catalog value of 1,048,576, **When** resolved, **Then** the effective window is 1,048,576, a WARN names the agent and the clamp, and the source reports the clamp.
  4. **Given** a cloud provider with a live limits endpoint and no override/default, **When** resolved, **Then** the live value is used ahead of the catalog, and the live answer is served from an on-disk cache with a TTL — never fetched on the turn path.
  5. **Given** an agent on `claude-cli` or `codex-cli`, **When** resolved, **Then** no window is computed or enforced by Omnipus for it (exempt).
  6. **Given** any configuration, **When** the pre-turn check, the mid-turn check, the timeout-recovery check and the model-switch re-window each need a window, **Then** all four use the same resolved value (the `max_tokens × 4` heuristic and both flat `128000` fallbacks no longer exist).

### US-2 — Unknown window: conservative floor for cloud, ask-or-refuse for local endpoints (D3) — **P0**
As an operator running a hosted model the catalog does not know, I want a conservative 128,000-token floor with a WARN naming the model; running a local/self-hosted model (`ollama`, `vllm`, LM Studio, `custom`), I want the endpoint queried for its window and, if it reports none, the model marked unusable with an actionable message — never a guessed number.
- **Why P0:** a 128,000 guess on an 8k Ollama model is the incident again on the operator's own machine (pass-2 MAJ-002).
- **Independent test:** an unknown cloud model resolves to 128,000 with source `floor` + WARN; a local endpoint that reports no window yields the "set the context length" state; setting the per-model override makes it usable without restart.
- **Acceptance:**
  1. **Given** a cloud model of a known vendor absent from catalog and live sources, **When** resolved, **Then** the window is 128,000, source `floor`, and one WARN names the model.
  2. **Given** an `ollama` model whose endpoint reports a loaded window of 8,192, **When** resolved, **Then** the window is 8,192 with source `live` and the 128,000 floor is never applied.
  3. **Given** a `vllm`/LM Studio/`custom` endpoint whose live query fails or reports no window, **When** the model is selected or an agent bound to it starts a turn, **Then** the model is not usable and the provider row and model picker show: *"This endpoint did not report a context length for <model>. Set it under Settings → Models → <provider> → <model> → Context length (per-model override, D2 rung 1) and try again."*
  4. **Given** that state, **When** the operator sets the per-model override, **Then** the model is usable immediately (no restart) and the override is clamped like every other.

### US-3 — Cap every tool result at one choke point (D4) — **P0**
As the harness, I must ensure no single tool result enters the window above a fixed size, for builtins and MCP alike, through one function every tool result passes through — so a server connected tomorrow is covered on its first call.
- **Why P0:** §1.2 — an item larger than the glass cannot be fitted by any window mechanism.
- **Independent test:** a synthetic 2 MB MCP result and a 2 MB builtin result each enter the window truncated head-and-tail with a mark stating the full size and that the complete result is in the archive; the archive line holds the full content; reload shows the capped form.
- **Acceptance:**
  1. **Given** an MCP result of 1,178,522 chars, **When** it becomes a context message, **Then** the in-window content is ≤ 62,500 chars, head-and-tail, with a mark stating the full size and the archive path; it is not an error.
  2. **Given** a builtin success result of 200,000 chars, **When** it becomes a context message, **Then** the in-window content is ≤ 64,000 chars with the same mark.
  3. **Given** a builtin failure result of 50,000 chars, **When** it becomes a context message, **Then** it is ≤ 10,000 chars, head-and-tail.
  4. **Given** any capped result, **Then** the full (sensitive-data-filtered) content is on the archive line and the meta records the id's cap state, so a reload renders the capped form identically to what the model saw.
  5. **Given** a result over 25,000 chars but under its cap, **When** it enters, **Then** it is unmodified and a warn-threshold log line and counter fire.
  6. **Given** any operator cap setting above 150,000, **When** saved, **Then** it is rejected (ceiling).
  7. **Given** the shipped per-tool caps, **Then** `read_file` stays at 64 KB, `browser_get_text` is lowered to 64,000, shell output is 64,000 on success and 10,000 on failure, and there is no per-server or per-tool opt-out.
  8. **Given** the sensitive-data filter is enabled, **Then** it runs on the full content before the cap is applied, and the archive copy is the filtered full content. *(A-6 accepted.)*

### US-4 — Bound user messages at the gateway (D4) — **P1**
As a user who pastes a huge document, I want a clear, non-fatal refusal before a turn starts, so the thrash guard is never reachable through a user message.
- **Why P1:** closes pass-2 CRIT-004 for user content; on the §17.4 exit-proof path.
- **Independent test:** a chat message over the bound is refused with a reply stating the size and the limit; nothing is persisted; no turn is registered; a message at the bound is accepted.
- **Acceptance:**
  1. **Given** a user message of N chars where N exceeds the bound, **When** it is sent, **Then** the user sees a reply of the form *"that message is N chars; the limit is <bound> — attach it as a file or shorten it"*, no transcript entry exists, no turn is registered, no error frame is emitted.
  2. **Given** a message exactly at the bound, **When** sent, **Then** a turn starts normally.
  3. **Given** the refusal, **When** the user edits and resends under the bound, **Then** the turn starts normally.

### US-5 — Refuse oversized tool-call arguments as a structured tool result (D4) — **P1**
As the model, when I write arguments over the cap (e.g. a whole file into a parameter), I get a structured refusal as the tool result — not executed, not turn-fatal — so I can retry smaller.
- **Why P1:** closes CRIT-004 for arguments; §17.4.
- **Independent test:** a tool call whose serialised arguments exceed the cap yields a structured refusal result naming the size and the cap; the tool is not executed; the turn continues.
- **Acceptance:**
  1. **Given** a tool call with arguments over the cap, **When** dispatched, **Then** the tool is not executed and the `role: "tool"` result is a structured refusal (ADR-060 family shape) naming the tool, the argument size and the cap.
  2. **Given** that refusal, **When** the model retries under the cap, **Then** the tool executes normally.
  3. **Given** the refusal, **Then** it passes the D4 choke point like every other result and the turn does not end.

### US-6 — Empty in place with a recall mark; persist the emptied-set; roll it back on abort (D5) — **P0**
As the harness, when the window is over budget mid-turn and the oldest candidate is a tool result whose call is still in the window, I replace its content with a short deterministic mark (never cut), persist which ids were emptied so reload agrees with what the model saw, and restore the set on turn abort.
- **Why P0:** §1.3 root cause; the cross-call mechanism the whole design rests on.
- **Independent test:** with a small budget and several tool results in one turn, the oldest eligible results are replaced by marks in the bytes sent to the provider; the archive is unchanged; a reload renders the same marks; an abort restores both `Skip` and the emptied-set.
- **Acceptance:**
  1. **Given** the mid-turn check is over budget and the oldest over-budget content is a tool result of the current turn, **When** emptying runs, **Then** that message's content is the mark and its slot, role and `tool_call_id` are unchanged.
  2. **Given** the mark, **Then** it states the tool name, the full size in chars, the turn number, the `tool_call_id`, and that `recall_conversation(tool_call_id=…)` returns it in pages; it is produced by a single typed producer (no ad-hoc string formatting).
  3. **Given** emptying acted on the in-memory messages mid-turn, **When** the same session is reloaded from disk, **Then** the assembled history shows the same marks for the same ids (live and reload agree byte-for-byte on those messages).
  4. **Given** the archive, **Then** no line is modified by emptying (append-only).
  5. **Given** a turn that emptied ids and then aborts, **When** the session is restored, **Then** `Skip` and the emptied-set both return to their turn-start values, and a retried turn starts from an un-emptied window.
  6. **Given** one trigger firing, **When** emptying runs, **Then** it empties down to the target in one pass (not one result per LLM call), so the provider's cached prefix changes once per trigger.
  7. **Given** Verbose chat is off, **Then** the mark is not rendered in the chat thread; **Given** it is on, **Then** it is.
  8. **Given** the restore point, **Then** it is refreshed after every mid-turn empty, exactly as after every trim.

### US-7 — Recall an emptied or capped result by `tool_call_id`, in pages (D5 §6.3) — **P0**
As the model, when I need an emptied result, I call `recall_conversation` with the `tool_call_id` from the mark and page through the full content under the builtin cap.
- **Why P0:** without it, emptying is lossy in practice (pass-2 CRIT-003).
- **Independent test:** after an empty, `recall_conversation(tool_call_id=X)` returns the first page; `offset`/`length` paging reaches the last byte; a wrong id returns a clear error.
- **Acceptance:**
  1. **Given** an archived tool result with id X of 1,178,522 chars, **When** `recall_conversation(tool_call_id=X)` is called, **Then** one `role: "tool"` page of at most the page size is returned, re-paired via the existing id remap, stating the total size and the offset reached.
  2. **Given** `offset`/`length`, **When** paging, **Then** pages are contiguous, the final page ends at the last byte, and an `offset` ≥ total returns an empty page stating the total.
  3. **Given** the `tool_call_id` mode, **Then** it is exempt from the 4,000/8,000-token span budgets, still passes the D4 choke point, counts toward the D6 running total, and can itself be emptied later.
  4. **Given** an id that does not exist in the archive, **Then** a tool error names the id; **Given** the id belongs to a turn that aborted (rolled back), **Then** the same not-found error (by design).
  5. **Given** `tool_call_id` combined with `query`/`turn_range`/`time`, **Then** the call is rejected (exactly one mode).

### US-8 — Run the window check after every tool result, with a floor and a thrash guard (D6) — **P0**
As the harness, I check the budget after each tool result is appended, before the next LLM call, using the one existing budget formula; mid-turn I never cut, only empty; every result of the most recent assistant message is never emptied; if still over budget after every eligible result is emptied, I stop with a typed error rather than loop.
- **Why P0:** §1.3 — "nobody looks while the turn fills".
- **Independent test:** a 50-call turn at the cap against a small window never exceeds the window at any iteration; the last step's results are intact; older results carry marks.
- **Acceptance:**
  1. **Given** a turn with N tool results, **When** each is appended, **Then** the budget check runs before the next LLM call (N checks + the pre-turn check).
  2. **Given** the trigger `min(absoluteBudget, 0.9 × resolvedWindow)` with `absoluteBudget` defaulting to 400,000 chars (≈160,000 estimator tokens), **When** the window is over it, **Then** emptying runs down to a target below the trigger.
  3. **Given** the oldest over-budget content is an earlier complete turn, **Then** today's `Skip`-advance applies unchanged; **Given** it is a tool result of the current turn, **Then** it is emptied; **Given** it belongs to the results of the most recent assistant message, **Then** it is never emptied.
  4. **Given** an assistant message with 3 parallel calls, **When** the third result triggers the check, **Then** none of the three is emptied (the floor is the whole set).
  5. **Given** every eligible result is emptied and the window is still over budget, **Then** the turn ends with a typed error (D7), one log line, and no loop.
  6. **Given** the timeout-recovery path, **Then** it uses the same budget as the pre-turn and mid-turn checks; the `summarize_token_percent` setting no longer exists.
  7. **Given** the order of operations for one result: ingest bound → cap → append to archive → budget check → empty as needed → assemble → LLM call.

### US-9 — No silent turn exits (D7) — **P1**
As a user and an operator, when a turn is cancelled, times out, or hits the thrash guard, I see a typed error with a real reason, and the log and transcript record it.
- **Why P1:** §1.4 — "this ADR cannot say whether the request overflowed or timed out".
- **Independent test:** each of the four silent returns now produces a log line with the raw cause, an event, and a transcript entry, and translates to a non-`unknown` code.
- **Acceptance:**
  1. **Given** the provider call is cancelled, **Then** the turn ends with code `turn_canceled` (new), a log line, an event and a transcript entry.
  2. **Given** the provider call exceeds the deadline, **Then** code `turn_timed_out` (new), same three artefacts.
  3. **Given** the thrash guard fires, **Then** the code `context_unrecoverable` (attribution `product`), distinct from `context_too_long`, same artefacts. *(A-4 accepted.)*
  4. **Given** any new code, **Then** it exists in the `LLMError` schema with user copy and an attribution, and both generated catalogues are regenerated in the same commit.
  5. **Given** D4–D6 in place, **Then** the only remaining turn-fatal conditions are provider auth rejected, provider unreachable after retries, workspace unavailable, and the thrash guard (reachable only by an injected fault).

### US-11 — Controls in Settings: caps, trigger, effective window with source, override (D9) — **P1**
As an operator, I can see and set the per-surface caps (ceiling 150,000), the D6 absolute trigger, the effective context window per agent read-only with its source, and a lower-only override — as first-class contract types.
- **Why P1:** "that number is currently unreachable from the UI and the API, which is half of why the 8× error stayed invisible."
- **Independent test:** the settings endpoint round-trips the caps and trigger; the agent view shows the effective window and source; a cap above 150,000 is rejected.
- **Acceptance:**
  1. **Given** Settings, **When** I read the context settings, **Then** I see MCP cap, builtin success cap, builtin failure cap, absolute trigger, ingest bound, and the global default window, with their defaults (62,500 / 64,000 / 10,000 / 400,000 chars / 8 MB / unset).
  2. **Given** a cap value of 150,001, **When** saved, **Then** HTTP 400 names the ceiling; 150,000 is accepted.
  3. **Given** an agent, **When** I view it, **Then** I see its effective window and its source (`operator` / `live` / `catalog` / `floor`) read-only, plus a `clamped` flag, plus an override field.
  4. **Given** an override above the catalog value, **When** saved, **Then** it is accepted and the effective window shows the clamped value with a clamp indicator.
  5. **Given** every one of these fields, **Then** it is defined in `contracts/` and crosses the boundary only as generated types.

### US-12 — Bound what enters memory at ingest (D10) — **P1**
As the process, every network or subprocess read is bounded at 8 MB by default (operator-settable, ceiling strictly below the archive line ceiling) so an oversized response is a tool failure before it is held or parsed — never a truncation.
- **Why P1:** D4 protects the window, not the process; the §17.1 2 MB test must fit under it.
- **Independent test:** an 8 MB + 1 byte MCP response or search response fails as a tool error naming the bound; a 2 MB response succeeds; a setting at or above 0.8 × the archive line ceiling is rejected.
- **Acceptance:**
  1. **Given** an MCP tool result whose serialised content exceeds the bound, **Then** the call is a tool failure naming the bound; no partial content enters the window or archive.
  2. **Given** a Brave/DuckDuckGo/Perplexity response over the bound, **Then** the same.
  3. **Given** a 2 MB response, **Then** it is accepted and flows to D4.
  4. **Given** a configured ingest bound, **Then** it is accepted only if strictly below 0.8 × the archive reader's line ceiling (`maxLineSize` stays 10 MB, so the ceiling is < 8,388,608 bytes); a value at or above it is rejected, so every archived line produced from an admitted result remains readable. *(A-8 resolved.)*

### Edge Cases
- E1: a tool result of exactly 62,500 (MCP) / 64,000 (builtin) chars → enters unmodified; +1 → capped.
- E2: a capped result that is later the oldest over-budget content → emptied (mark replaces the head-and-tail form; recall still reaches the full content).
- E3: the window is over budget and the only candidates are the last assistant step's results → nothing emptied; if still over budget → thrash guard (reachable only by injected fault once D4 and the user/argument bounds exist).
- E4: parallel tool calls (N results for one assistant message) → the floor is all N; emptying order among older steps is oldest-first.
- E5: multibyte content — caps and bounds are measured in characters (runes) for caps, bytes for ingest; head-and-tail truncation never splits a rune.
- E6: an MCP tool name containing instruction-like text → the mark renders it sanitised: ≤ 64 chars, non-printables stripped. *(A-7 accepted.)*
- E7: `recall_conversation(tool_call_id)` for a result of a turn that aborted → not found (rolled back), by design.
- E8: paging past the end, negative `offset`, `length` > page size → empty page / error / clamped to the page size respectively.
- E9: live provider query cache expired and the endpoint is down → cloud: next rung (catalog → floor); local: "set the context length" state, never a guess.
- E10: override set while the "set the context length" state is shown → usable without restart.
- E11: the catalog entry for a model is lowered in a new catalog release → the capability clamp is recomputed from the catalog on the next resolution; an operator override above it is clamped to the new value (overrides never expire, they are re-clamped).
- E12: the user message bound and attachments — attachments are not counted toward the character bound (they are media refs). *(A-3 accepted.)*
- E13: an abort between a mid-turn empty and the restore-point refresh → rollback to the last refreshed restore point (consistent, since refresh happens immediately after each empty).
- E14: `claude-cli`/`codex-cli` agents → D2–D3, D6 and D9's window fields do not apply; D4/D10 still apply to their tool results if any pass through the loop.
- E15: a delegated sub-turn → runs the same loop; its own session's emptied-set and restore point are independent of the parent's.

---

## 5. Behavioral Contract & Boundaries

### Behavioral Contract
- When an agent instance is built, the system resolves its window from override → global default → live (cached) → catalog → floor, clamps any override to the catalog-or-live value (recomputed on every resolution), and records the source.
- When a local/self-hosted endpoint reports no window, the system marks the model unusable with the actionable message and never assigns a guessed number.
- When any tool result becomes a context message, the system passes it through one choke point that caps it per surface, marks over-cap results, and archives the full content.
- When a user message exceeds the bound, the system refuses it before a turn starts, with the size and the limit, persisting nothing.
- When tool-call arguments exceed the cap, the system returns a structured refusal as the tool result and continues the turn.
- When the window is over budget after a tool result, the system empties the oldest eligible results of the current turn down to the target in one pass, never the last assistant step's results, never cutting mid-turn; it persists the emptied ids and refreshes the restore point.
- When the model calls `recall_conversation` with a `tool_call_id`, the system returns the archived result in pages under the builtin cap, exempt from the span budgets.
- When a turn aborts, the system restores archive length, `Skip` and the emptied-set to their turn-start values.
- When a turn is cancelled, times out, or trips the thrash guard, the system ends it with a typed code, a log line, an event and a transcript entry.
- When a provider rejects a request for exceeding its window, the system classifies it as `context_too_long` (D7 typed) and changes no window record.
- When an operator reads or writes context settings, the system serves them via generated contract types, rejecting caps above 150,000.
- When any network/subprocess read exceeds the ingest bound, the system fails the tool call rather than truncating.

### Explicit Non-Behaviors & Safeguards

#### Qualitative Prohibitions
- The system must not summarise, compress with an LLM, or otherwise rewrite any message content — ADR-028/066: `windowTrim` is the only compaction path and it only evicts or empties.
- The system must not delete or modify any archive line when capping or emptying — append-only; the full result is always recallable for completed turns.
- The system must not cut the window mid-turn (separate an assistant tool call from its `role: "tool"` answer) — provider ordering rules; only empty in place.
- The system must not empty any result of the most recent assistant message — the model is reasoning about it.
- The system must not apply an override or any other source that raises the window above the catalog-or-live value — the incident by another route.
- The system must not learn, infer or cache a context window from provider error text (D8 not adopted) — `contextOverflowSubstrings` classifies only. **Accepted cost, stated:** a model not yet in the catalog, or a plan-specific cap below the catalog value, overflows with a typed D7 `context_too_long` error until the catalog or the operator (override) corrects it.
- The system must not fall back to `max_tokens × 4`, a flat `128000`, or any window not produced by the ladder — three paths giving three answers was the defect.
- The system must not assign a floor to a local/self-hosted endpoint — ask or refuse.
- The system must not add a per-server or per-tool opt-out from the cap.
- The system must not fetch a live provider limit on the turn path — cached on disk with a TTL only.
- The system must not persist a refused user message, register a turn for it, or emit an error frame — it is an ordinary "edit and resend".
- The system must not hand-roll the recall mark or the argument refusal with `fmt.Sprintf` — single typed producer (ADR-060 `%q` finding).
- The system must not introduce a second budget formula, a spill-to-disk store, a reducer, or refetch recipes — retired in ADR-066 §14.
- The system must not render the recall mark in the chat thread unless Verbose chat is on.
- The system must not add migration, aliasing or compatibility for `summarize_token_percent`, `max_tokens × 4`, or pre-existing window records — greenfield.

#### Machine-Verifiable Constraints

**Sizes (characters = Unicode runes unless stated):**
- MCP result cap default **62,500**; builtin success cap **64,000**; builtin failure cap **10,000** (head-and-tail); warn threshold **25,000** (log + counter, no modification); operator ceiling **150,000** on every cap.
- User-message bound = the builtin success cap (**64,000**); not a separate setting — it tracks the builtin cap; the gateway reply quotes the live value. *(A-2 resolved.)*
- Tool-call argument cap = the builtin success cap (**64,000**) measured on the serialised arguments string; media refs are not counted toward the user-message bound. *(A-3 accepted.)*
- Recall page size = the builtin success cap (**64,000**). *(A-1 resolved.)*
- D6 budget formula *(A-5 accepted; machine-verifiable)*, all quantities in estimator tokens (`chars × 2/5`):
  - `W` = resolved window; `Wc = min(W, 0.9 × W)` (the 0.9 ceiling applied **before** subtraction);
  - `budget = Wc − maxTokens − ceil(0.05 × W) − pinnedCoreOverhead` (the same formula `windowTrim` uses, with `Wc` in place of `W`);
  - `absoluteShare = 160,000` tokens (= 400,000 chars ÷ 2.5), a ceiling on the **tool-result share** `Σ tokens(role == "tool" messages in the window)`;
  - **over budget** ⇔ `Σ tokens(messages) + toolDefsTokens + recallSpanTokens > budget` **or** `toolResultShare > absoluteShare`;
  - **target** = 80 % of the binding trigger: emptying proceeds oldest-first (floor excluded) until `toolResultShare ≤ 0.8 × absoluteShare` **and** `Σ tokens(messages) + toolDefsTokens + recallSpanTokens ≤ 0.8 × budget`;
  - `absoluteShare` is operator-settable via the `absolute_trigger_chars` setting (default 400,000 chars).
- Ingest bound default **8 MB (8,000,000 bytes)** per response, operator-settable; the setting's ceiling is enforced as **< `maxLineSize` × 0.8 = 8,388,608 bytes** (`maxLineSize` stays 10 MB = 10,485,760 bytes); `fetch_url`'s own fallback is aligned to 8 MB. *(A-8 resolved.)*
- Cloud floor **128,000 tokens**; local/self-hosted floor: none.
- Live-query cache TTL **24 h**, on disk at `$OMNIPUS_HOME/cache/model_limits.json`. *(A-9 accepted.)*

**HTTP / wire:**
- `PUT` context settings with any cap > 150,000 → **HTTP 400**, body an `ErrorResponse` whose message names the field and the ceiling `150000`.
- `PUT` with a negative or zero cap/trigger/bound → **HTTP 400**.
- Agent view exposes `context_window_effective` (integer) and `context_window_source` (enum `operator | live | catalog | floor`), `context_window_clamped` (boolean) and `context_window_override` (integer, optional). *(A-10 accepted.)*
- New `LLMError` codes `turn_canceled`, `turn_timed_out`, and the thrash-guard code `context_unrecoverable` (attribution `product`), each with `x-user-messages` copy and attribution; generated Go and TS catalogues regenerated in the same commit (`make verify-contracts` exit 0).
- The recall mark and the argument refusal each have an inline asyncapi schema with `additionalProperties: false` and a `const` discriminator, an exported `*Code`, a single producer through `marshalWithinBudget`, and an entry in the family register (ADR-060 D1 checklist).

**Tool interface:**
- `recall_conversation` accepts exactly one of `query | turn_range | time | tool_call_id`; with `tool_call_id`, optional `offset ≥ 0` and `1 ≤ length ≤ page size`; more than one mode → tool error `"provide exactly one of …"`.
- Unknown `tool_call_id` → tool error containing the id.

**Timing / performance:**
- The mid-turn budget check is O(window size) per tool result (estimator only; zero LLM calls, zero disk reads of the archive).
- Emptying runs at most once per trigger firing per turn iteration (one pass to target).

**Logging / events:**
- Clamp: one WARN per agent build naming agent id, override, clamped value.
- Floor: one WARN per agent build naming the model.
- Warn threshold: one log line per result over 25,000 chars naming tool, size; counter `tool_result_large_total`. *(A-11 accepted.)*
- Thrash guard: one ERROR line with session key, window, budget, emptied count.
- Each of the four typed exits: log line with the raw cause, `EventKindTurnEnd` with the code, transcript entry.

### Integration Boundaries

| System | In / Out | Contract | On failure | Development |
|---|---|---|---|---|
| Provider limits endpoints (Anthropic `/v1/models`, Google, OpenRouter, Mistral, Groq, xAI, Ollama `/api/show` + `/api/ps`, vLLM `max_model_len`) | out: model id; in: input/output token limits | HTTPS JSON per vendor; cached on disk with TTL; off the turn path | cloud: next rung; local: "set the context length" state | mocked HTTP servers in Go tests; real endpoints in holdout |
| Catalog (ADR-067 `Resolve(provider, model)`) | in: `context_window` | in-process Go API | miss → floor (cloud) / refuse (local); clamp recomputed from the catalog on every resolution | real package with a test fixture catalog |
| MCP servers (`pkg/mcp`) | in: `CallToolResult` content | MCP over stdio/HTTP via Go SDK | over ingest bound → tool failure; over cap → capped | fake server in tests |
| Provider chat completion | out: assembled messages (projected); in: response, overflow errors | provider adapters | overflow → `context_too_long` (no learning); cancel/deadline → typed exits | fake provider in `pkg/agent` tests |
| Session archive (`pkg/memory`) | out: full results, meta (`skip`, `count`, projection state); in: history, archive | JSONL + meta JSON, atomic writes | write failure → logged, trim reported as failed (existing M4 guard) | real temp-dir store |
| SPA ↔ gateway | context settings, agent effective window/source, new `LLMError` codes, recall-mark frame under Verbose chat | generated types + zod (Constraint #8) | zod drop + counter on mismatch | generated + zod |

---

## 6. BDD Scenarios

> Categories: HP = Happy Path, AP = Alternate Path, EP = Error Path, EC = Edge Case. Every scenario `Traces to:` US-n.ACm.

### Feature: Window resolution (D2–D3)

**Scenario (HP) B-01: catalog value wins when no override** — Traces to US-1.AC1
Given no per-agent override and no global default, and the catalog has (openrouter, z-ai/glm-5.2) = 1,048,576
When the agent instance is built
Then the effective window is 1,048,576 and the source is `catalog`.

**Scenario (HP) B-02: override lowers** — Traces to US-1.AC2
Given a per-agent override of 100,000 and catalog 1,048,576
When resolved
Then the effective window is 100,000 and the source is `operator`.

**Scenario (EC) B-03: override above capability is clamped with a WARN** — Traces to US-1.AC3
Given an override of 2,000,000 and catalog 1,048,576
When resolved
Then the effective window is 1,048,576, the source reports the clamp, and exactly one WARN names the agent and both numbers.

**Scenario (AP) B-04: live rung precedes catalog and is cached** — Traces to US-1.AC4
Given a cloud provider whose limits endpoint returns 200,000 and a catalog value of 1,048,576, with no override/default
When resolved twice within the TTL
Then both resolutions return 200,000 with source `live`, and the endpoint was called once.

**Scenario (AP) B-05: external CLI agents are exempt** — Traces to US-1.AC5
Given an agent whose provider is `claude-cli`
When resolved
Then no window is enforced and no WARN is logged.

**Scenario Outline (HP) B-06: all four consumers agree** — Traces to US-1.AC6
Given a resolved window of `<window>`
When `<consumer>` needs a window
Then it uses `<window>`.
| window | consumer |
|---|---|
| 200,000 | pre-turn check |
| 200,000 | mid-turn check |
| 200,000 | timeout recovery |
| 200,000 | model-switch re-window |

**Scenario (AP) B-07: unknown cloud model floors at 128,000** — Traces to US-2.AC1
Given a model absent from catalog and live sources, on a known cloud vendor
When resolved
Then the window is 128,000, source `floor`, one WARN names the model.

**Scenario (HP) B-08: Ollama loaded window used, never floored** — Traces to US-2.AC2
Given an `ollama` model whose `/api/ps` reports 8,192
When resolved
Then the window is 8,192, source `live`.

**Scenario (EP) B-09: local endpoint reports nothing → unusable with message** — Traces to US-2.AC3
Given a `vllm` endpoint whose live query returns no window
When an agent bound to it starts a turn
Then the turn is refused with the message *"This endpoint did not report a context length for <model>. Set it under Settings → Models → <provider> → <model> → Context length (per-model override, D2 rung 1) and try again."* and the provider row and model picker show the same state.

**Scenario (HP) B-10: setting the override makes it usable without restart** — Traces to US-2.AC4
Given the state in B-09
When the operator sets the per-model override to 32,768
Then the next turn runs with window 32,768, source `operator`, with no gateway restart.

### Feature: Cap at the door (D4)

**Scenario Outline (HP/EC) B-11: per-surface cap** — Traces to US-3.AC1, AC2, AC3
Given a `<surface>` result of `<size>` chars
When it becomes a context message
Then the in-window content length is `<expected>` and `<marked>`.
| surface | size | expected | marked |
|---|---|---|---|
| mcp | 62,500 | 62,500 | no |
| mcp | 62,501 | ≤ 62,500 | yes |
| mcp | 1,178,522 | ≤ 62,500 | yes |
| builtin-success | 64,000 | 64,000 | no |
| builtin-success | 64,001 | ≤ 64,000 | yes |
| builtin-success | 200,000 | ≤ 64,000 | yes |
| builtin-failure | 10,000 | 10,000 | no |
| builtin-failure | 50,000 | ≤ 10,000 | yes |

**Scenario (HP) B-12: capped result — full in archive, capped on reload** — Traces to US-3.AC4
Given a capped MCP result with id X
When the session is reloaded and history assembled
Then the archive line for X holds the full filtered content, and the assembled message for X equals the capped form the model saw.

**Scenario (HP) B-13: warn threshold is observe-only** — Traces to US-3.AC5
Given a builtin result of 30,000 chars
When it enters
Then it is unmodified and one warn-threshold log line and counter increment are observed.

**Scenario (EP) B-14: cap above ceiling rejected** — Traces to US-3.AC6, US-11.AC2
Given a context-settings write with MCP cap 150,001
When saved
Then HTTP 400 names the ceiling; a write of 150,000 returns 200.

**Scenario (HP) B-15: per-tool caps aligned** — Traces to US-3.AC7
Given the shipped tools
When `read_file`, `browser_get_text`, shell success and shell failure results of 70,000 chars pass the choke point
Then they are capped at 64,000 / 64,000 / 64,000 / 10,000 respectively.

**Scenario (HP) B-16: filter before cap** — Traces to US-3.AC8
Given a 100,000-char result containing an API key at position 63,990–64,030
When it enters with the sensitive filter enabled
Then the archive copy and the capped copy both contain the redaction and never a fragment of the key.

**Scenario (EP) B-17: oversized user message refused at the gateway** — Traces to US-4.AC1
Given a chat message of 64,001 chars
When sent
Then the reply states the size and the limit, no transcript entry exists, no turn id is allocated, no error frame is emitted.

**Scenario (EC) B-18: user message at the bound accepted** — Traces to US-4.AC2, AC3
Given a chat message of exactly 64,000 chars
When sent
Then a turn starts normally.

**Scenario (EP) B-19: oversized arguments → structured refusal, turn continues** — Traces to US-5.AC1, AC3
Given a tool call whose serialised arguments are 64,001 chars
When dispatched
Then the tool's execute function is never invoked, the tool result is a structured refusal naming the tool, 64,001 and 64,000, and the loop proceeds to the next LLM call.

**Scenario (HP) B-20: retry under the cap executes** — Traces to US-5.AC2
Given the refusal in B-19
When the model re-issues the call with 10,000-char arguments
Then the tool executes.

### Feature: Empty in place (D5)

**Scenario (HP) B-21: oldest eligible result emptied, structure intact** — Traces to US-6.AC1, AC2
Given a budget that admits two results and three results R1, R2, R3 appended in one turn (R3 from the latest assistant message)
When the mid-turn check runs after R3
Then R1's content is the mark (tool name, size, turn, `tool_call_id`, recall hint), its role and `tool_call_id` are unchanged, and the assistant message that called it is still present.

**Scenario (HP) B-22: live and reload agree byte-for-byte** — Traces to US-6.AC3
Given B-21
When the provider request bytes are captured and the session is reloaded and assembled
Then the message for R1 is identical in both.

**Scenario (HP) B-23: archive untouched** — Traces to US-6.AC4
Given B-21
Then the archive line count and every line's bytes are unchanged by emptying.

**Scenario (HP) B-24: abort restores Skip and emptied-set** — Traces to US-6.AC5
Given a turn that emptied R1 and then aborts
When the session is restored
Then `Skip` and the emptied-set equal their turn-start values and a new turn assembles R1 with its capped/full content.

**Scenario (EC) B-25: one pass to target** — Traces to US-6.AC6
Given five eligible results and a target that requires emptying three
When the trigger fires once
Then exactly three are emptied in that pass and the next LLM call runs under the target.

**Scenario (AP) B-26: mark hidden unless Verbose chat** — Traces to US-6.AC7
Given an emptied result in the session
When the thread renders with Verbose chat off / on
Then the mark is absent / present in the thread.

**Scenario (HP) B-27: restore point refreshed after each empty** — Traces to US-6.AC8
Given two successive triggers in one turn
When an abort occurs after the second
Then rollback restores to the turn-start values, not to an intermediate state.

### Feature: Recall by tool_call_id (D5 §6.3)

**Scenario (HP) B-28: first page** — Traces to US-7.AC1
Given archived result X of 1,178,522 chars
When `recall_conversation(tool_call_id=X)` is called
Then one `role: "tool"` page of ≤ 64,000 chars is returned, re-paired, stating total 1,178,522 and the next offset.

**Scenario (HP) B-29: paging reaches the last byte** — Traces to US-7.AC2
Given X
When pages are requested at offsets 0, 64,000, … until an empty page
Then the concatenation equals the archived content exactly.

**Scenario (EC) B-30: exempt from span budget, subject to cap and D6** — Traces to US-7.AC3
Given the recall span budget is 8,000 tokens
When a 64,000-char page is recalled
Then it is not dropped for exceeding the span budget, passes the choke point unmodified, and is counted by the next mid-turn check.

**Scenario (EP) B-31: unknown id** — Traces to US-7.AC4
When `recall_conversation(tool_call_id="nope")` is called
Then a tool error contains `nope`.

**Scenario (EP) B-32: mode exclusivity** — Traces to US-7.AC5
When called with both `tool_call_id` and `query`
Then the tool error says to provide exactly one mode.

### Feature: Mid-turn window check (D6)

**Scenario (HP) B-33: check after every result** — Traces to US-8.AC1
Given a turn with 5 tool calls
When the turn completes
Then the budget check ran 6 times (1 pre-turn + 5).

**Scenario (HP) B-34: trigger and target** — Traces to US-8.AC2
Given a window of 1,048,576 and `absoluteBudget` 400,000 chars
When the tool-result share exceeds 400,000 chars
Then emptying runs until it is below the target.

**Scenario Outline (HP) B-35: operation by position** — Traces to US-8.AC3
Given the oldest over-budget content is `<position>`
When the check runs
Then the operation is `<operation>`.
| position | operation |
|---|---|
| an earlier complete turn | advance Skip (cut) |
| a tool result of the current turn, older step | empty in place |
| a result of the most recent assistant message | none |

**Scenario (EC) B-36: floor is the whole last step** — Traces to US-8.AC4
Given an assistant message with 3 parallel calls at the cap and a budget that admits two
When the third result triggers the check
Then none of the three is emptied.

**Scenario (EP) B-37: thrash guard is typed, not a loop** — Traces to US-8.AC5
Given an injected fault that makes a non-tool message oversized
When every eligible result is emptied and the window is still over budget
Then the turn ends with the thrash-guard code, one ERROR line, and the LLM was not called again.

**Scenario (HP) B-38: timeout recovery uses the one budget** — Traces to US-8.AC6
Given a timed-out call
When recovery checks the budget
Then it uses the same value as the pre-turn check and no `summarize_token_percent` field exists in config.

**Scenario (HP) B-39: order of operations** — Traces to US-8.AC7
Given a 2 MB MCP result
When it is processed
Then the observed order is ingest-bound → cap → archive append → budget check → empty → assemble → LLM call, and the turn completes without a user-facing error.

### Feature: Typed exits (D7)

**Scenario Outline (EP) B-40: no silent exits** — Traces to US-9.AC1, AC2, AC3
Given `<cause>`
When the turn ends
Then the code is `<code>`, and a log line, a turn-end event and a transcript entry exist.
| cause | code |
|---|---|
| context cancelled | turn_canceled |
| deadline exceeded | turn_timed_out |
| thrash guard | context_unrecoverable |

**Scenario (HP) B-41: codes are contract-defined** — Traces to US-9.AC4
When `make verify-contracts` runs
Then it exits 0 and both generated catalogues contain the new codes with copy and attribution.

### Feature: Settings (D9)

**Scenario (HP) B-44: read/write context settings** — Traces to US-11.AC1
When the settings are read on a fresh install
Then they equal the defaults; when written and re-read they round-trip.

**Scenario (HP) B-45: agent effective window and source** — Traces to US-11.AC3, AC4, AC5
Given an agent with override 2,000,000 and catalog 1,048,576
When the agent is read
Then `context_window_effective` = 1,048,576, source indicates the clamp, and the field set is generated from the contract.

### Feature: Ingest bound (D10)

**Scenario Outline (EP/HP) B-46: bound at ingest** — Traces to US-12.AC1, AC2, AC3
Given a `<source>` response of `<bytes>` bytes
When read
Then the outcome is `<outcome>`.
| source | bytes | outcome |
|---|---|---|
| MCP | 8,000,000 | accepted |
| MCP | 8,000,001 | tool failure naming the bound |
| Brave search | 8,000,001 | tool failure naming the bound |
| Perplexity | 2,097,152 | accepted |

---

## 7. TDD Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | `pkg/agent` (resolution, projection, cap, estimator budget), `pkg/memory` (meta + rollback), `pkg/tools`/`pkg/mcp` (bounds), `translate_error` | Logic in isolation with fakes |
| Integration | `runTurn` with a fake provider and fake tools; gateway handlers with `httptest`; recall tool against a real temp archive | Components together |
| E2E | SPA vitest for Settings + mark visibility; one embedded-binary smoke (holdout) | User-visible behaviour |

All Go tests: `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -run '^TestName$' -p 1 ./pkg/...` locally (one at a time); the full suite in CI.

### Test Implementation Order

| # | Test Name | Level | Traces to | Description |
|---|---|---|---|---|
| 1 | `TestResolveContextWindow_Ladder` (pkg/agent) | Unit | B-01, B-02, B-04, B-07, B-08 | table-driven ladder order and source |
| 2 | `TestResolveContextWindow_OverrideClampsLowerOnly` | Unit | B-03 | clamp + single WARN |
| 3 | `TestResolveContextWindow_ExternalCLIExempt` | Unit | B-05 | no window for `claude-cli`/`codex-cli` |
| 4 | `TestResolveContextWindow_LocalEndpointNoFloor` | Unit | B-08, B-09 | refuse state, never 128,000 |
| 5 | `TestResolveContextWindow_LiveCacheTTL` | Unit | B-04 | one fetch within TTL; never on turn path |
| 6 | `TestWindowAgreement_AllConsumers` | Unit | B-06 | pre-turn / mid-turn / timeout / model-switch read one value; `maxTokens*4` and `128000` absent (grep-style assertion on source like `TestDecommission_NoForceCompressionSymbols`) |
| 7 | `TestToolResultCap_PerSurface` | Unit | B-11 | table at cap / cap+1 / huge |
| 8 | `TestToolResultCap_HeadTailRuneSafe` | Unit | E5 | no split rune |
| 9 | `TestToolResultCap_FilterThenCap` | Unit | B-16 | redaction precedes cap |
| 10 | `TestToolResultCap_WarnThreshold` | Unit | B-13 | log + counter |
| 11 | `TestToolResultCap_PerToolAlignment` | Unit | B-15 | `read_file`, `browser_get_text`, shell |
| 12 | `TestToolArgsCap_StructuredRefusal` | Unit | B-19, B-20 | execute never invoked; family shape via `marshalWithinBudget` |
| 13 | `TestRecallMark_SingleProducer` | Unit | B-21 (mark), E6 | typed producer, fields, name sanitised |
| 14 | `TestProjection_PureFunction` | Unit | B-21, B-22 | same function for in-memory and assembled views; deterministic |
| 15 | `TestSessionMeta_ProjectionStateRoundTrip` (pkg/memory) | Unit | B-12, B-22 | per-id `capped`/`emptied` state persisted with `skip`/`count` |
| 16 | `TestRollbackAppended_RestoresEmptiedSet` (pkg/memory) | Unit | B-24 | new parameter atomic with Skip |
| 17 | `TestMidTurnBudget_OperationByPosition` | Unit | B-35, B-36 | cut / empty / none; floor = whole last step |
| 18 | `TestMidTurnBudget_TriggerAndTarget` | Unit | B-34, B-25 | `min(absolute, 0.9W)`; one pass to target |
| 19 | `TestMidTurnBudget_SameBudgetAsWindowTrim` | Unit | B-38 | one formula; `SummarizeTokenPercent` absent |
| 20 | `TestTranslateError_TypedExits` | Unit | B-40 | cancel / deadline / thrash → codes, never `unknown` |
| 21 | `TestTranslateError_NoWindowLearning` | Unit | B-07 (non-behaviour) | an overflow error with a numeric limit classifies to `context_too_long` and changes no window record (D8 not adopted) |
| 22 | `TestResolveContextWindow_ClampRecomputedFromCatalog` | Unit | B-03 / E11 | catalog value lowered → override re-clamped on next resolution; override itself persists |
| 23 | `TestIngestBound_MCP` (pkg/mcp) | Unit | B-46 | bound ± 1 |
| 24 | `TestIngestBound_SearchProviders` (pkg/tools) | Unit | B-46 | Brave / DDG / Perplexity |
| 25 | `TestIngestBound_BelowArchiveLineCeiling` | Unit | US-12.AC4 | bound ≤ `maxLineSize` |
| 26 | `TestRecallConversation_ToolCallID_FirstPage` | Integration | B-28 | real temp archive |
| 27 | `TestRecallConversation_ToolCallID_PagingReachesLastByte` | Integration | B-29 | concatenation equals archive |
| 28 | `TestRecallConversation_ToolCallID_SpanBudgetExempt` | Integration | B-30 | not dropped; passes cap; counted by D6 |
| 29 | `TestRecallConversation_ToolCallID_NotFoundAndExclusive` | Integration | B-31, B-32 | errors |
| 30 | `TestRunTurn_GuardTest_2MBResultCompletes` | Integration | B-39 (§17.1) | fake provider records request sizes; no user-facing error |
| 31 | `TestRunTurn_LongTurn_50CallsAtCap_SmallWindow` | Integration | B-33, B-21, B-36 (§17.2) | never over window; last step intact; marks present |
| 32 | `TestRunTurn_LiveVsReloadBytesEqual` | Integration | B-22 | provider bytes vs reload assembly |
| 33 | `TestRunTurn_AbortRestoresSkipAndEmptiedSet` | Integration | B-24, B-27 (§17.2b) | restore point refreshed per empty |
| 34 | `TestRunTurn_ThrashGuard_InjectedFaultOnly` | Integration | B-37 (§17.4) | typed error; no loop; unreachable without fault |
| 35 | `TestRunTurn_ArgsRefusal_TurnContinues` | Integration | B-19 (§17.4) | next LLM call happens |
| 36 | `TestRunTurn_SilentExitsNowTyped` | Integration | B-40 (§17.5) | four sites: log + event + transcript |
| 37 | `TestRunTurn_LocalEndpointRefusedUntilOverride` | Integration | B-09, B-10 (§17.6) | no restart |
| 38 | `TestGateway_UserMessageBound` (pkg/gateway, scoped) | Integration | B-17, B-18 (§17.4) | no transcript, no turn, no error frame |
| 39 | `TestGateway_ContextSettings_RoundTripAndCeiling` | Integration | B-14, B-44 | 400 on 150,001 |
| 40 | `TestGateway_AgentEffectiveWindowAndSource` | Integration | B-45 | derived fields |
| 41 | `contract_test.go` additions (pkg/api/generated) | Integration | B-41 | new schemas validate |
| 42 | `ContextSection.test.tsx` (vitest) | E2E | B-44, B-14 | renders, saves, shows ceiling error |
| 43 | `toolVisibility.test.ts` additions | E2E | B-26 | mark hidden unless Verbose |
| 44 | `llm-error.test.ts` additions | E2E | B-41 | new codes have copy |

### Test Datasets

#### Dataset DS-1: Result size vs cap (chars)

| # | Surface | Input size | Boundary | Expected | Traces to |
|---|---|---|---|---|---|
| 1 | mcp | 0 | zero | unmodified (empty) | B-11 |
| 2 | mcp | 62,499 | max−1 | unmodified | B-11 |
| 3 | mcp | 62,500 | max | unmodified | B-11 |
| 4 | mcp | 62,501 | max+1 | capped + mark | B-11 |
| 5 | mcp | 1,178,522 | incident | capped + mark; archive full | B-11, B-12 |
| 6 | builtin-success | 64,000 | max | unmodified | B-11 |
| 7 | builtin-success | 64,001 | max+1 | capped + mark | B-11 |
| 8 | builtin-failure | 10,001 | max+1 | capped head-and-tail | B-11 |
| 9 | mcp | 62,501 with 4-byte runes at the cut | unicode | no split rune | B-11 / E5 |
| 10 | builtin-success | 25,001 | warn | unmodified + warn log | B-13 |
| 11 | builtin-success | 100,000 with secret across 64,000 | edge | redacted both copies | B-16 |

#### Dataset DS-2: User message bound (chars)

| # | Input | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | 0 (empty, no media) | zero | existing behaviour (no turn) | B-18 |
| 2 | 63,999 | max−1 | turn starts | B-18 |
| 3 | 64,000 | max | turn starts | B-18 |
| 4 | 64,001 | max+1 | refused; nothing persisted | B-17 |
| 5 | 500,000 | huge | refused | B-17 |
| 6 | 1,000 + 3 media refs | media | turn starts (media not counted) | B-18 |

#### Dataset DS-3: Argument cap

| # | Serialised args size | Expected | Traces to |
|---|---|---|---|
| 1 | 64,000 | executes | B-20 |
| 2 | 64,001 | structured refusal, not executed | B-19 |
| 3 | 300,000 | refusal | B-19 |

#### Dataset DS-4: Window resolution

| # | Override | Global default | Live | Catalog | Provider class | Expected (value, source) | Traces to |
|---|---|---|---|---|---|---|---|
| 1 | — | — | — | 1,048,576 | cloud | 1,048,576 catalog | B-01 |
| 2 | 100,000 | — | — | 1,048,576 | cloud | 100,000 operator | B-02 |
| 3 | 2,000,000 | — | — | 1,048,576 | cloud | 1,048,576 operator-clamped + WARN | B-03 |
| 4 | — | 150,000 | — | 1,048,576 | cloud | 150,000 operator | B-02 |
| 5 | — | — | 200,000 | 1,048,576 | cloud | 200,000 live | B-04 |
| 6 | — | — | — | — | cloud | 128,000 floor + WARN | B-07 |
| 7 | — | — | 8,192 | — | ollama | 8,192 live | B-08 |
| 8 | — | — | none | — | vllm | unusable, message | B-09 |
| 9 | 32,768 | — | none | — | vllm | 32,768 operator | B-10 |
| 10 | — | — | — | — | claude-cli | exempt | B-05 |

#### Dataset DS-5: Mid-turn budget positions

| # | Window composition (oldest → newest) | Over budget by | Expected | Traces to |
|---|---|---|---|---|
| 1 | [prev turn][U][A(R1)][R1][A(R2)][R2] | R1 | Skip advances past prev turn (cut) | B-35 |
| 2 | [U][A(R1)][R1][A(R2)][R2] | R1 | R1 emptied | B-35 |
| 3 | [U][A(R1,R2,R3)][R1][R2][R3] | R1+R2 | nothing (floor = whole last step) | B-36 |
| 4 | [U][A(R1)][R1]…[A(R5)][R5], target needs 3 | — | R1,R2,R3 emptied in one pass | B-25 |
| 5 | [U(oversized via fault)][A(R1)][R1] | U | thrash guard typed error | B-37 |

#### Dataset DS-6: Recall paging (total 1,178,522 chars, page 64,000)

| # | offset | length | Expected | Traces to |
|---|---|---|---|---|
| 1 | 0 | — | chars 0–63,999, total stated | B-28 |
| 2 | 1,152,000 | — | chars 1,152,000–1,178,521 (last page, 26,522) | B-29 |
| 3 | 1,178,522 | — | empty page, total stated | B-29 / E8 |
| 4 | −1 | — | tool error | E8 |
| 5 | 0 | 70,000 | clamped to 64,000 | E8 |
| 6 | 0 | 0 | tool error | E8 |

#### Dataset DS-7: Ingest bound (bytes)

| # | Source | Bytes | Expected | Traces to |
|---|---|---|---|---|
| 1 | MCP | 2,097,152 | accepted | B-46 |
| 2 | MCP | 8,000,000 | accepted (at bound) | B-46 |
| 3 | MCP | 8,000,001 | tool failure | B-46 |
| 4 | Brave | 8,000,001 | tool failure | B-46 |
| 5 | DuckDuckGo | 8,000,001 | tool failure | B-46 |
| 6 | Perplexity | 8,000,001 | tool failure | B-46 |
| 7 | fetch_url (fallback) | 8,000,001 | tool failure (fallback aligned to 8 MB) | B-46 |

#### Dataset DS-8: Settings validation

| # | Field | Value | Expected | Traces to |
|---|---|---|---|---|
| 1 | mcp_result_cap | 150,000 | 200 | B-14 |
| 2 | mcp_result_cap | 150,001 | 400 | B-14 |
| 3 | builtin_failure_cap | 0 | 400 | B-14 |
| 4 | absolute_trigger_chars | 400,000 | 200 | B-44 |
| 5 | ingest_bound_bytes | 8,388,607 | 200 (strictly below 0.8 × maxLineSize) | B-44 |
| 5b | ingest_bound_bytes | 8,388,608 | 400 (at the ceiling; must be strictly below) | B-44 |
| 5c | ingest_bound_bytes | 10,485,760 | 400 | B-44 |
| 6 | context_window_override (agent) | 2,000,000 vs catalog 1,048,576 | 200; effective 1,048,576 clamped | B-45 |

### Regression Test Requirements

This feature **modifies existing functionality**.

| Existing behaviour | Existing test (must pass unchanged) | New regression test | Notes |
|---|---|---|---|
| Turn-boundary cut at user messages; archive-preserving `TruncateHistory`; `SetHistory` never called | `TestWindowTrim_CutsOnTurnBoundary`, `TestWindowTrim_KeepLastTurnAlignedFit`, `TestWindowTrim_SingleHugeTurn_KeepsLastUser`, `TestWindowTrim_NoDroppedMarker`, `TestWindowTrim_SetHistoryNeverCalled`, `TestArchive_FloorPathPreservesEvicted` | No | D6 is additive; `parseTurnBoundaries` unchanged |
| Recall-span drop-first | `TestWindowTrim_RecallSpanDropAloneReturnsOK`, `TestRecallSpan_*` | `TestRecallConversation_ToolCallID_SpanBudgetExempt` | new mode must not alter query/turn/time budgets |
| Model switch re-windows without summary | `TestModelSwitch_ReWindowsNoSummary`, `TestModelSwitch_UpsizeKeepsSkipForward` | Yes — update to use the ladder value instead of `128000` | the flat fallback is removed |
| Summariser stays deleted | `TestDecommission_NoForceCompressionSymbols` | Yes — extend to assert `SummarizeTokenPercent`, `maxTokens * 4` and `= 128000` are absent from `pkg/agent` | greenfield |
| Estimator and budget arithmetic | `TestEstimateMessageTokens*`, `TestIsOverContextBudget*` | No | unchanged |
| Orphan recovery | `TestRecovery_*` | `TestProjection_NeverOrphans` — emptying never produces an orphan | structural invariant |
| Rollback restores Skip | `jsonl_test.go` rollback tests; `TestRunTurn_*` abort tests | `TestRollbackAppended_RestoresEmptiedSet` | interface gains a parameter; all fakes updated |
| `LLMError` copy rules (no "contact support"; `product`/`config` never say switch models) | `translate_error_test.go`, `src/lib/llm-error.test.ts` | extend for the new codes | copy rules apply to new entries |
| Existing per-tool caps | `filesystem`/`web`/`browser`/`shell` tests asserting 64 KB / 50,000 / 100 KiB / 10,000 | update `browser_get_text` and shell-success assertions | `read_file` unchanged |
| ADR-060 family lint | `scripts/check-no-handwritten-wire-types.sh` | register the two new discriminators | lint fails otherwise |
| Delegated sub-turns use the target's settings (ADR-032) | `subturn_target_identity_test.go` | extend: child's window resolved from the target's provider/model | D2 via `execSource` |

---

## 8. Requirements & Success Criteria

### Functional Requirements

**D2 — resolution**
- **FR-001**: The system MUST resolve an agent's context window in the order per-agent override → global default → live provider query (cached) → catalog (ADR-067) → floor, and record the winning source.
- **FR-002**: The effective window MUST be `min(override-or-default, live-or-catalog value)`, recomputed on every resolution; a clamp MUST log one WARN naming the agent and both values.
- **FR-003**: The live query MUST be served from an on-disk cache with a TTL **[A-9: 24 h]** and MUST NOT be performed on the turn path.
- **FR-004**: The `max_tokens × 4` heuristic and both flat `128000` fallbacks MUST NOT exist; pre-turn, mid-turn, timeout-recovery and model-switch paths MUST read the one resolved value.
- **FR-005**: Agents on `claude-cli` and `codex-cli` MUST be exempt from window resolution and enforcement.

**D3 — unknown window**
- **FR-006**: For a cloud model of a known vendor with no other source, the window MUST be 128,000 with source `floor` and one WARN naming the model.
- **FR-007**: For `ollama`, `vllm`, LM Studio and `custom` endpoints the live query MUST be mandatory; no floor MUST ever be applied.
- **FR-008**: When a local endpoint reports no window, the model MUST be unusable and the provider row, model picker and turn refusal MUST show the exact D3 message; setting the per-model override MUST make it usable without restart.

**D4 — caps and bounds**
- **FR-009**: Every tool result (builtin success, builtin failure, denied, MCP) MUST become a context message through exactly one function; no other site MUST construct a `role: "tool"` message.
- **FR-010**: Caps MUST default to 62,500 (MCP) / 64,000 (builtin success) / 10,000 (builtin failure, head-and-tail) chars; a warn threshold of 25,000 chars MUST log and count without modifying; every cap MUST be operator-settable with a 150,000 ceiling; there MUST be no per-server or per-tool opt-out.
- **FR-011**: An over-cap result MUST enter the window head-and-tail truncated (50/50 split, the mark's length counted toward the cap) with the recall mark; the full (filtered) content MUST be appended to the archive; the meta MUST record the id's state (`capped`) so reload renders the capped form; the per-id states in window meta are exactly `capped | emptied`.
- **FR-012**: Shipped per-tool caps MUST be aligned: `read_file` 64 KB unchanged; `browser_get_text` lowered to 64,000; shell 64,000 on success, 10,000 on failure.
- **FR-013**: The sensitive-data filter MUST run on the full content before the cap, and the archive copy MUST be the filtered content.
- **FR-014**: A user message over the bound **[A-2: 64,000 chars]** MUST be refused at the gateway before a turn is registered, with a reply stating the size and the limit; nothing MUST be persisted and no error frame emitted.
- **FR-015**: Tool-call arguments over the cap **[A-3: 64,000 chars, serialised]** MUST yield a structured refusal as the tool result (ADR-060 family: inline schema, `*Code`, single producer via `marshalWithinBudget`, register entry); the tool MUST NOT execute; the turn MUST continue.

**D5 — empty in place**
- **FR-016**: When the mid-turn check is over budget and the oldest over-budget content is a tool result of the current turn, the system MUST replace that message's content with the recall mark in the in-memory messages before the next LLM call, leaving role and `tool_call_id` intact.
- **FR-017**: The recall mark MUST be produced by a single typed producer, MUST state tool name (≤ 64 chars, non-printables stripped), full size in chars, turn number (index into `parseTurnBoundaries` of the current window plus the archive offset), `tool_call_id`, and the recall hint, and MUST NOT be assembled with ad-hoc formatting.
- **FR-018**: The emptied-set MUST be persisted in the session meta alongside `skip`/`count`; the same pure projection function MUST be applied to the in-memory slice mid-turn and by history assembly at turn start, post-trim and reload, so the two views agree byte-for-byte.
- **FR-019**: Emptying MUST NOT modify any archive line.
- **FR-020**: The turn restore point MUST carry the emptied-set; rollback MUST restore archive length, `Skip` and the emptied-set atomically; the restore point MUST be refreshed after every mid-turn empty.
- **FR-021**: Emptying MUST run down to the target in one pass per trigger.
- **FR-022**: The mark MUST be rendered in the chat thread only when Verbose chat is on.

**D5 §6.3 — recall**
- **FR-023**: `recall_conversation` MUST accept a `tool_call_id` mode (existing tool, new parameters — no Constraint #6 policy change) with optional `offset`/`length` (chars), returning one re-paired `role: "tool"` page of at most the page size **[A-1: 64,000]** stating the total size and next offset; pages MUST be contiguous and reach the last byte.
- **FR-024**: The `tool_call_id` mode MUST be exempt from the 4,000/8,000-token span budgets, MUST pass the D4 choke point, MUST count toward the D6 total, and MAY itself be emptied later.
- **FR-025**: Exactly one mode MUST be given; an unknown id MUST return a tool error containing the id.

**D6 — mid-turn check**
- **FR-026**: The budget check MUST run after every tool result is appended and before the next LLM call, in addition to the pre-turn site, using the same budget formula as `windowTrim`.
- **FR-027**: The trigger MUST be `min(absoluteBudget, 0.9 × resolvedWindow)` with `absoluteBudget` defaulting to 400,000 chars (≈160,000 estimator tokens), expressed through the estimator as a ceiling on the tool-result share of the one budget, composed exactly as the D6 formula in §5 (Machine-Verifiable Constraints); emptying MUST run to the target (80 % of the binding trigger).
- **FR-028**: By position: an earlier complete turn → advance `Skip` (unchanged); a current-turn tool result from an older step → empty; any result of the most recent assistant message → never.
- **FR-029**: Mid-turn the system MUST NOT cut; `parseTurnBoundaries` MUST be unchanged.
- **FR-030**: If still over budget after every eligible result is emptied, the system MUST stop, log one ERROR and end the turn with a typed code — never loop.
- **FR-031**: The `summarize_token_percent` setting and its scaling in timeout recovery MUST be removed; timeout recovery MUST use the one budget.
- **FR-032**: Order for one result MUST be ingest bound → cap → archive append → budget check → empty → assemble → LLM call.

**D7 — typed exits**
- **FR-033**: The four cancel/timeout returns MUST produce a log line with the raw cause, a turn-end event with the code, and a transcript entry; codes `turn_canceled` and `turn_timed_out` MUST be added to `LLMError.yaml` with copy and attribution; the thrash guard MUST use `context_unrecoverable` (attribution `product`, never `context_too_long`); generated catalogues MUST be regenerated in the same commit.

**D8 — NOT ADOPTED**
- **FR-034**: The system MUST NOT learn, infer or persist a context window from provider error text; `contextOverflowSubstrings` MUST only classify (→ `context_too_long`). Operator overrides MUST never expire; the capability clamp MUST be recomputed from the catalog on every resolution.
- *(FR-035 withdrawn — `prompt_tokens` calibration is not adopted.)*

**D9 — settings**
- **FR-036**: Per-surface caps, the absolute trigger, the ingest bound and the global default window MUST be first-class contract types on `GET/PUT /api/v1/settings/context` (`contracts/components/schemas/ContextSettings.yaml`); each agent MUST expose `context_window_effective`, `context_window_source` (`operator | live | catalog | floor`), `context_window_clamped` and `context_window_override` on `Agent.yaml` / `AgentUpdateRequest.yaml` (the ADR-068 `/api/v1/providers/default-model` route is a separate concern and stays separate); all MUST cross the boundary as generated types.

**D10 — ingest**
- **FR-037**: Every network/subprocess read (MCP results, Brave/DuckDuckGo/Perplexity) MUST be bounded at ingest, default 8 MB (8,000,000 bytes), operator-settable; exceeding it MUST be a tool failure naming the bound, never a truncation; the setting's ceiling MUST be enforced as strictly below `maxLineSize` × 0.8 (`maxLineSize` stays 10 MB); `fetch_url`'s own fallback MUST be aligned to 8 MB.

### Success Criteria
- **SC-001**: A synthetic 2 MB MCP result through `runTurn` completes the turn with no user-facing error; every provider request ≤ the window (§17.1).
- **SC-002**: A 50-call turn at the cap against a 128,000 window never sends a request over the window; the last step's results are intact; all older results carry marks; recall by id pages to the last byte (§17.2).
- **SC-003**: After an abort following ≥1 empty, `Skip` and the emptied-set equal turn-start values (§17.2b).
- **SC-004**: For any (provider, model), the catalog, `windowTrim`, the pre-turn check and the model-switch path report one identical window (§17.3).
- **SC-005**: A 64,001-char user message yields zero transcript entries, zero turn registrations, zero error frames; 64,001-char arguments yield a structured refusal and a subsequent LLM call; the thrash guard is reached only under an injected fault and then produces a typed error with exactly one further LLM call count of 0 (§17.4).
- **SC-006**: Each of the four silent returns produces ≥1 log line, 1 turn-end event and 1 transcript entry; `TranslateTurnError` never yields `unknown` for them (§17.5).
- **SC-007**: A local endpoint reporting no window is never assigned 128,000; after setting the override, the next turn runs without restart (§17.6).
- **SC-008**: `make verify-contracts`, `golangci-lint`, `gofmt -l | wc -l == 0`, `npm run typecheck`, `npx vitest run` all exit 0 on the branch.
- **SC-009**: `grep -rn 'maxTokens \* 4\|= 128000\|SummarizeTokenPercent' pkg/agent pkg/config` returns nothing.
- **SC-010**: Live bytes and reload bytes for an emptied message are identical (pass-2 CRIT-002 exit proof).

### Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | B-01, B-04, B-07, B-08 | 1, 4, 5 |
| FR-002 | US-1 | B-02, B-03 | 1, 2 |
| FR-003 | US-1 | B-04 | 5 |
| FR-004 | US-1 | B-06 | 6 |
| FR-005 | US-1 | B-05 | 3 |
| FR-006 | US-2 | B-07 | 1 |
| FR-007 | US-2 | B-08, B-09 | 4 |
| FR-008 | US-2 | B-09, B-10 | 4, 37 |
| FR-009 | US-3 | B-11, B-15, B-19 | 7, 11, 12 |
| FR-010 | US-3, US-11 | B-11, B-13, B-14 | 7, 10, 39 |
| FR-011 | US-3 | B-12 | 15, 32 |
| FR-012 | US-3 | B-15 | 11 |
| FR-013 | US-3 | B-16 | 9 |
| FR-014 | US-4 | B-17, B-18 | 38 |
| FR-015 | US-5 | B-19, B-20 | 12, 35 |
| FR-016 | US-6 | B-21 | 14, 17, 31 |
| FR-017 | US-6 | B-21 | 13 |
| FR-018 | US-6 | B-22 | 14, 15, 32 |
| FR-019 | US-6 | B-23 | 31 |
| FR-020 | US-6 | B-24, B-27 | 16, 33 |
| FR-021 | US-6 | B-25 | 18 |
| FR-022 | US-6 | B-26 | 43 |
| FR-023 | US-7 | B-28, B-29 | 26, 27 |
| FR-024 | US-7 | B-30 | 28 |
| FR-025 | US-7 | B-31, B-32 | 29 |
| FR-026 | US-8 | B-33 | 31 |
| FR-027 | US-8 | B-34, B-25 | 18 |
| FR-028 | US-8 | B-35, B-36 | 17 |
| FR-029 | US-8 | B-35 | 17, regression `TestWindowTrim_*` |
| FR-030 | US-8 | B-37 | 34 |
| FR-031 | US-8 | B-38 | 19, 6 |
| FR-032 | US-8 | B-39 | 30 |
| FR-033 | US-9 | B-40, B-41 | 20, 36, 41, 44 |
| FR-034 | US-1 (non-behaviour) | B-07, B-03 | 21, 22 |
| FR-036 | US-11 | B-44, B-45, B-14 | 39, 40, 41, 42 |
| FR-037 | US-12 | B-46 | 23, 24, 25 |

**Completeness check:** every FR has ≥1 scenario and ≥1 test; every scenario B-01…B-41 and B-44…B-46 appears above (B-42/B-43 withdrawn with D8).

### Exit-proof traceability (ADR-066 §17 → this spec)

| §17 item | FRs | BDD | Tests | SC |
|---|---|---|---|---|
| 1 Guard test (2 MB) | FR-009–011, FR-016, FR-026, FR-032, FR-037 | B-39 | 30 | SC-001 |
| 2 Long-turn test | FR-016–019, FR-023–024, FR-026–028 | B-21, B-28, B-29, B-33, B-36 | 27, 31 | SC-002 |
| 2b Rollback test | FR-020 | B-24, B-27 | 16, 33 | SC-003 |
| 3 Window-agreement test | FR-001, FR-004 | B-06 | 6 | SC-004 |
| 4 Thrash-guard test | FR-014, FR-015, FR-030 | B-17, B-19, B-37 | 34, 35, 38 | SC-005 |
| 5 Silent-exit test | FR-033 | B-40 | 20, 36 | SC-006 |
| 6 Local-endpoint test | FR-007, FR-008 | B-09, B-10 | 4, 37 | SC-007 |
| (pass-2 CRIT-002) live = reload | FR-018 | B-22 | 32 | SC-010 |

---

## 9. Prerequisites, Setup, Stack, Runtime

- **Prerequisites:** Go 1.26.4 toolchain (targets 1.22+), Node 20+, `golangci-lint`, `govulncheck`; no new runtime dependencies (Constraint #1). No external accounts for tests (provider endpoints mocked); real endpoints only in holdout.
- **Development setup:** standard checkout; `make gen-contracts` after editing `contracts/`; `npm run build && rm -rf pkg/gateway/spa && cp -r dist/spa/* pkg/gateway/spa/` before an embedded-binary check. Push for CI; do not run the full Go suite locally.
- **Tech stack:** unchanged (Go single binary; React 19 SPA; JSONL sessions).
- **Runtime:** no new files except the live-limit cache `$OMNIPUS_HOME/cache/model_limits.json` (A-9); no new listeners; logs in `$OMNIPUS_HOME/logs/gateway.log`.

---

## 10. Ambiguity Self-Audit

The ADR is the confirmed brief; the operator cannot be asked during this phase. A-1, A-2 and A-8 were resolved by ADR commit `f01d5278` (2026-08-22) and the spec body now reflects them; the remaining rows were resolved by the operator on 2026-08-22 (A-3…A-13 and A-15…A-17 accepted as stated; A-14 moot because D8 is not adopted) and the spec body reflects every acceptance. The table is kept as the record.

| # | What's ambiguous | Likely agent assumption (used above) | Question to resolve |
|---|---|---|---|
| A-1 | **RESOLVED** (ADR f01d5278) — §6.3 now states 64,000. | Recall page = builtin success cap = **64,000** chars. | — |
| A-2 | **RESOLVED** (ADR f01d5278) — §5 now says 64,000. | User-message bound = builtin success cap = **64,000**; the gateway reply quotes the live value; it is not a separate setting — it tracks the builtin cap. | — |
| A-3 | **ACCEPTED** | Cap = builtin success cap (64,000) on the serialised arguments string; media refs are not counted toward the user-message bound. | — |
| A-4 | **ACCEPTED** | New codes `turn_canceled`, `turn_timed_out`, thrash `context_unrecoverable` (attribution `product`); not `context_too_long`. | — |
| A-5 | **ACCEPTED** | Target = 80 % of the trigger; `windowTrim`'s budget with the `min(W, 0.9W)` ceiling; the absolute term caps the tool-result share at 160,000 estimator tokens. Written as a machine-verifiable constraint in §5. | — |
| A-6 | **ACCEPTED** | Filter first, then cap; archive holds the filtered full content. | — |
| A-7 | **ACCEPTED** | Tool name in the mark ≤ 64 chars, non-printables stripped. | — |
| A-8 | **RESOLVED** (ADR f01d5278, §16a MAJ-008). | Ingest bound default **8 MB** (8,000,000 bytes), operator-settable; ceiling enforced as `< maxLineSize × 0.8` (8,388,608; `maxLineSize` stays 10 MB); `fetch_url` fallback aligned to 8 MB. Note: 8 MB is read as decimal (8,000,000) — 8 MiB (8,388,608) would sit exactly at the ceiling and fail the strict `<`. | — |
| A-9 | **ACCEPTED** | 24 h; `$OMNIPUS_HOME/cache/model_limits.json`. | — |
| A-10 | **ACCEPTED** (without `learned` — D8 not adopted) | Enum `operator \| live \| catalog \| floor`, plus boolean `clamped`. | — |
| A-11 | **ACCEPTED** | WARN log line + in-process counter `tool_result_large_total`. | — |
| A-12 | **ACCEPTED** | Index into `parseTurnBoundaries` of the current window + archive offset (stable across evictions). | — |
| A-13 | **ACCEPTED** | `GET/PUT /api/v1/settings/context` with `ContextSettings.yaml`; agent override fields on `Agent.yaml` / `AgentUpdateRequest.yaml`. The ADR-068 spec's `/api/v1/providers/default-model` is a different concern and stays separate. | — |
| A-14 | **MOOT** — D8 not adopted; no learned windows, no `prompt_tokens` samples, no persistence file. | — | — |
| A-15 | **ACCEPTED** | Two per-id states `capped \| emptied` in window meta. | — |
| A-16 | **ACCEPTED** | 50/50 head-and-tail; the mark's length counts toward the cap. | — |
| A-17 | **ACCEPTED** | No policy change (existing tool, new parameters). | — |

---

## 11. Holdout Evaluation Scenarios *(post-implementation; NOT in the traceability matrix)*

1. **(Happy)** Connect a real MCP server that returns a >1 MB result; ask the agent a question that triggers it. Expect: the turn completes, the reply is useful, Verbose chat shows a capped result with a mark, the archive line holds the full content.
2. **(Happy)** On a 128k model, run a task that needs ~40 tool calls. Expect: no "didn't finish" message; older results show marks under Verbose chat; the agent can recall one by id when asked.
3. **(Happy)** Set a per-agent override above the model's catalog value. Expect: the agent view shows the clamped effective window with its source and a WARN in the log.
4. **(Error)** Paste a 100,000-character document into chat. Expect: an immediate reply with the size and limit, no new session entry, no error toast.
5. **(Error)** Cancel a long turn mid-call. Expect: a specific "turn was cancelled" message (not "we can't tell why"), a transcript entry, a log line naming the cause.
6. **(Edge)** Point an agent at an Ollama model with the daemon reporting no context length. Expect: the provider row and picker show the "set the context length" message; setting it makes the next turn work without restarting the gateway.
7. **(Edge)** Abort a turn right after several results were emptied, then send a new message. Expect: the new turn sees the un-emptied (capped/full) results; nothing from the aborted turn remains.

---

## 12. Assumptions & Clarifications

- Assumptions are the **[A-n]** rows in §10; each is used as labelled and is reversible by the operator's answer.
- ADR-067 supplies `Resolve(provider, model)` with `context_window` and an entry version; this spec consumes both and specifies neither.
- The existing estimator (2.5 chars/token) remains the unit of all token arithmetic (no calibration path — D8 not adopted).

### Summary

- User stories: **11** (P0: 6, P1: 5) — US-10 (D8) withdrawn: not adopted
- BDD scenarios: **44** (Happy Path 24 · Alternate Path 4 · Error Path 10 · Edge Case 6; 7 are outlines with 39 example rows) — B-42/B-43 withdrawn
- Test datasets: **8**, **61** rows (DS-5 provider-overflow messages withdrawn; DS-4 learned column and two rows removed)
- Functional requirements: **36** active (FR-034 re-stated as the D8 non-behaviour; FR-035 withdrawn)
- Success criteria: **10**
- Tests planned: **44** (25 unit, 16 integration, 3 E2E) — tests 21/22 re-pointed from learning to the non-behaviour and the clamp recompute
- Ambiguities: **17** listed in §10 — **all closed** (A-1/A-2/A-8 resolved per ADR f01d5278; A-3…A-13, A-15…A-17 accepted by the operator 2026-08-22; A-14 moot with D8)
- Gaps: GitNexus impact analysis must be re-run on `runTurn`, `windowTrim`, `RollbackAppended` and `NewAgentInstance` before editing (tools unavailable when this spec was written).
