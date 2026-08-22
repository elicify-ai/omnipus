# ADR-066: Context overflow — the sliding window extended mid-turn, tool results emptied with a recall mark, and a per-result cap at the door

- **Status:** Proposed (2026-08-21; restructured 2026-08-22). Drafted from a live production incident on the operator's own instance; awaiting operator ratification before implementation.
- **Date:** 2026-08-22
- **Related:** [ADR-028](ADR-028-context-paging-sliding-window-recall.md) (`windowTrim` as the only compaction path — **extended, not superseded**: D6 changes *when* it runs and *what it may do mid-turn*; it remains the only path, and nothing here summarises); [ADR-051](ADR-051-media-handling-and-provider-error-translation.md) (`LLMError` classifier — extended by D7); [ADR-060](ADR-060-structured-tool-failure-family.md) (D5's recall mark is a candidate family member, §12); CLAUDE.md **Constraint #1** (single binary), **Constraint #6** (explicit tool policy), **Constraint #8** (contract-first wire formats).
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1 for everything cited as read. Incident facts were read on the build tree the failing binary came from (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/build-v0.1.1` @ `6acd378`); design facts on this branch @ `4684e8c7`. Cited as `file::symbol` per CLAUDE.md except where a line number is itself the claim. Absences are cited as searches. Items marked **[UNVERIFIED]** are collected in §16.
- **History:** an earlier draft (commits `f4aaf37c`..`4684e8c7`) built a separate tool-result subsystem — spill-to-disk handles, a four-shape reducer, schema-derived refetch recipes, a second per-turn budget. The first adversarial review ([ADR-066 review](ADR-066-context-budget-and-tool-result-routing-review.md), verdict BLOCK, 44 findings) and the operator's direction on 2026-08-22 replaced it with the three changes below; the earlier decisions are retired in §14. After the second review ([pass 2](ADR-066-context-budget-and-tool-result-routing-review-pass2.md), BLOCK, 4 critical) the operator split the provider/catalog programme out on 2026-08-22: the registry-fed catalog and provider identity are **[ADR-067](ADR-067-registry-fed-catalog-and-provider-identity.md)**; subscription policy, provider deletion, the default model and the provider UX are **[ADR-068](ADR-068-subscriptions-provider-deletion-and-provider-ux.md)**. This document is the incident fix alone.

> **Scope note.** This ADR decides how the harness keeps a request inside the model's window **at every point in a turn**, and what happens when it would not. It adds no summarisation, deletes nothing from disk, and introduces no new storage. It reverses one unratified heuristic (`maxTokens * 4`) and extends one ratified mechanism (ADR-028's window).
>
> **Greenfield rule (operator, 2026-08-22), applies to the entire scope:** no backward compatibility, no migration, no aliasing of old names, no grace periods, no mixed-version tolerance, no retired-name lists, no boot notifications about removed things. Pre-existing Omnipus state that does not match this design simply does not work. Runtime fallbacks that are part of the design (embedded snapshot when offline, conservative floor for an unknown model) are not compatibility mechanisms and stay.

---

## 1. The incident

On 2026-08-21, session `session_01M0HYPY0QMX10ZBP8C0C6FTXD` (agent `Jarvis-Chief-of-Staff`, model `z-ai/glm-5.2` via OpenRouter) ended with *"This turn didn't finish, and we can't tell why."* The user had asked for two inboxes. Three tool calls ran, all succeeded, and the live window ends there:

| # | role | size |
|---|---|---|
| 30 | assistant | 38 chars + 3 tool calls |
| 31 | tool — `mcp_composio_work_gmail_fetch_emails` | **1,178,522 chars** |
| 32 | tool — `mcp_composio_personal_gmail_fetch_emails` | **816,912 chars** |
| 33 | tool — `read_inbox` | 1,774 chars |
| — | *(no reply — file ends)* | |

≈ 850,000 estimated tokens (`estimateMessageTokens`, 2.5 chars/token) in one turn.

### 1.1 The window was wrong by 8×

`agents.defaults.context_window` is unset, unseeded (searched `pkg/config/defaults.go`, `pkg/coreagent/core.go`), and not settable from the UI or API (searched `contracts/`, `src/lib/api.ts`). `pkg/agent/instance.go::NewAgentInstance` fell back to `maxTokens * 4 = 131,072`. OpenRouter reports `z-ai/glm-5.2` at `context_length=1048576`. The proactive trims at 17:48:40 and 17:49:34 were evicting history from an agent with eight times the headroom it believed it had. `max_tokens` bounds *output*; deriving an *input* bound from it is a category error with unbounded drift.

### 1.2 The MCP path admits anything

Builtins already cap: shell 10,000 chars (`pkg/tools/shell.go`), `fetch_url` 50,000 (`pkg/tools/web.go::defaultMaxChars`), `browser_get_text` 100 KiB (`pkg/tools/browser/tools.go`), `read_file` 64 KB (`pkg/tools/filesystem.go::MaxReadFileSize`, comment: *"64KB limit to avoid context overflow"*). **`pkg/mcp/manager.go` contains no truncation of any kind** (searched). The 1.18 MB result is the proof.

### 1.3 The sliding window is never consulted mid-turn, and could not act if it were — this is the root cause

Verified on this branch:

- **When it runs.** `isOverContextBudget` has two call sites: `pkg/agent/loop.go::runTurn` once before the first LLM call, and inside timeout recovery. **Neither runs after a tool result is appended.** The window had its one chance before the 2 MB existed.
- **What it may cut.** `windowTrim` finds *"the smallest boundary index b such that window[b:] fits in budget"* — already a water-glass, not a turn-sized step. But the boundaries come from `pkg/agent/context_budget.go::parseTurnBoundaries`, which returns **only indices where `msg.Role == "user"`**. Inside a turn there are none. `windowTrim` advances `meta.Skip` via `TruncateHistory` and deletes nothing (archive-preserving), so the archive is intact — but the pointer can only land on a user message.
- **The pairing rule.** A cut that separates an assistant tool call from its `role: "tool"` answer produces a request the provider rejects. The codebase already treats that as corruption: `pkg/agent/session_recovery.go::findOrphanedToolCalls` / `RecoverOrphanedToolCalls`.

So the window rests on an unwritten assumption — **a single turn always fits** — with nothing whose job it was to notice when one did not. Long-running agents violate that assumption routinely: fifty tool calls in one turn is normal, and this incident needed only three.

### 1.4 The failure left no trace

`runTurn` has four returns (`"turn canceled"` / `"turn timed out"`, two sites each) that emit no event, no log, no transcript entry. The classified path immediately below them logs `"LLM call failed"` and appends to the transcript; neither artefact exists for this turn, which is how we know which exit was taken. The strings then fall through `TranslateTurnError` to `CodeUnknown` — the sentence the user saw. **This ADR cannot say whether the request overflowed or timed out; that is itself the finding.**

---

## 2. What the field does — only what bears on the decisions

| Harness | Per-result cap | Cross-call mechanism |
|---|---|---|
| Claude Code (MCP) | 25k tokens → persist + reference | auto-compact at `window − 13,000` (absolute reserve); `%` override clamped so it can only lower **[secondary source: deobfuscated bundle, claude-code#31806]** |
| Anthropic API | — | `clear_tool_uses_20250919`: at 100,000 input tokens, **replace the oldest tool results with placeholder text**, keep 3 — Anthropic: *"one of the safest, lightest touch forms of compaction"* |
| Gemini CLI | 40,000 | `COMPRESSION_FUNCTION_RESPONSE_TOKEN_BUDGET = 50,000`, cumulative tool-output budget walked **newest → oldest** |
| Codex CLI | 10,000 | operator value **absolute**, `0.9 × window` as a **ceiling** via `min()`; explicit "window record may be wrong" machinery |
| Cline | 48,000 chars | relative 0.9, absolute fallback 128,000 |
| LangChain | — | `ClearToolUsesEdit`: placeholder `"[cleared]"`, trigger 100,000, keep 3 |
| **Omnipus** | builtins yes, **MCP none** | turn-boundary window only |

Three facts drive the design:

1. **The MCP spec defines no result size limit in any revision**, and `tools/call` has no cursor. The cap cannot be delegated to servers.
2. **Emptying tool results in place, leaving a placeholder, is the established cross-call mechanism** — shipped by Anthropic's API and LangChain. It keeps the call/answer structure valid for every provider, which a mid-turn *cut* does not.
3. **No harness ships a bare percentage trigger.** Every relative trigger carries an absolute component; Codex makes the absolute value primary and the percentage a ceiling. Claude Code shipped fixes for *this exact* failure mode (auto-compact on unrecognised model IDs; `CLAUDE_CODE_DISABLE_1M_CONTEXT` holding 1M models to 200K): the field's answer to an unreliable window record is an absolute fallback plus a clamp.

Cline's cost argument applies throughout: a tool result is re-sent on every subsequent request, so an oversized one costs **quadratically** over the rest of the turn.

---

## 3. The design in one paragraph

Three changes to things that already exist. **D4** — a cap at the door: no single tool result may enter the window above a fixed size, because spilling cannot help with an item larger than the glass. **D5** — when the glass is near full and the oldest content is a tool result whose call is still in the window, *empty it in place* and leave a recall mark; the full content stays in the archive and `recall_conversation` already reads it. **D6** — run the window check after every tool result, not only at turn start, so the glass overflows from the oldest end whenever it is full, turn boundary or not. Nothing is summarised, nothing is deleted, no new storage exists. D2–D3 fix how the window record is *resolved*; D7 and D10 make failures legible and bound ingest; D9 exposes the controls (D8 — learning limits from provider errors — was not adopted). Where the window record *comes from* — the registry-fed catalog — is **D1, which lives in ADR-067**.

---

## 4. D2–D3 — Resolve the window

*(D1 — the catalog itself — is [ADR-067](ADR-067-registry-fed-catalog-and-provider-identity.md) §2. This section decides only which number wins once a catalog exists.)*

**D2 — resolution ladder.** *(Amended 2026-08-22 after the spec review, CRIT-004: a per-(provider, model) operator override is a real rung with a real contract.)* Per-agent override → **per-(provider, model) operator override** (`ContextSettings.model_overrides[]`, `PUT /api/v1/settings/context`, D9) → global default (Settings, D9) → **live provider query** (Anthropic `/v1/models` `max_input_tokens`/`max_tokens`; Google `inputTokenLimit`/`outputTokenLimit`; OpenRouter `context_length`/`top_provider.max_completion_tokens`, no key needed; Mistral `max_context_length`; Groq `context_window`; xAI `/v1/language-models`; Ollama `/api/show` then `/api/ps` for the loaded window; vLLM `max_model_len` — OpenAI, DeepSeek, Z.ai, Moonshot and MiniMax expose no limits on their model endpoints, verified by the 2026-08-22 survey) → catalog (ADR-067 D1) → conservative floor **with a WARN naming the model**. Live answers are cached on disk for 24 h (`$OMNIPUS_HOME/cache/model_limits.json`, key (provider id, base URL, model)) and the query is made **on demand only — at the first resolution that reaches this rung for a (provider, model), never at boot and never on a timer** (cross-spec review X-18/X-36; ADR-067 §4.3 lists it as its fourth sanctioned live call; the turn path never waits on it — a cold cache resolves from the next rung now and the live value applies at the next reload). **For endpoints whose catalog row has `locality: local` (ADR-067's single predicate: protocol ∈ {ollama, vllm}, or id `lmstudio`, or a custom row whose host is loopback/private) the live query is not a rung among others — it is mandatory: see D3.** **Operator decision 2026-08-22:** both the global default and the per-agent override stay, but **an override can only lower, never raise** — the effective window is `min(override, capability)` where *capability* is the live-or-catalog value, recomputed on every resolution — this applies to every override rung (per-agent, per-model, global default). A value above the model's real capability is the incident in §1.1 by another route, so it is clamped and a WARN names the agent and the clamp. (Codex's `min(limit, 0.9 × window)` is the same shape.) `maxTokens * 4` is retired. The two other flat `128000` fallbacks (`windowTrim`, model switch) consolidate onto the ladder — three paths currently give three answers. **Exempt providers** (they manage their own context): a provider is exempt when its catalog row's driver is a **subprocess CLI** (`cli_driver = codex-subprocess`, ADR-067 X-14) — today that is `codex-cli` only, and any future subprocess driver qualifies by that field, **never by id**. `openai-chatgpt` is an HTTP transport with a real window and is **cloud**, not exempt (cross-spec review X-20). `claude-cli` **no longer exists** — it is deleted by ADR-068 — and appears in this document only in this sentence (X-35). (`antigravity` is deleted outright — ADR-068 §2.4.)

**D3 — unknown window ⇒ conservative and loud; local endpoints ⇒ ask or refuse, never guess.** `Catalog.optimistic` assumes image support for unknown models; correct for modality, where optimism costs a retry. For a window, optimism costs a dead turn. The two policies diverge deliberately; this sentence is the record.

- **Cloud models of a known vendor — floor 128,000 tokens** (operator decision 2026-08-22): what nearly every current hosted model holds at minimum; a larger model is trimmed earlier than necessary, which is visible (the WARN) and harmless, where a higher guess would overflow a smaller model, which is the bug.
- **Local and self-hosted endpoints — no floor at all** (operator decision 2026-08-22, after the second review showed the 128,000 floor would overflow an 8k or 32k Ollama/vLLM model — the incident again, on the operator's own machine). **Local-classified endpoints** (ADR-067 `locality: local`; e.g. `ollama` — `/api/show` for the model's maximum, `/api/ps` for the window actually loaded — `vllm` (`max_model_len`), `lmstudio`, and custom rows at loopback/private hosts) are **always queried live** for their window (X-42: a custom row at a public host is cloud and floored). If the query fails or the endpoint does not report one, **the model is not usable**: the provider row and the model picker show the message *"This endpoint did not report a context length for &lt;model&gt;. Set it under Settings → Models → Model overrides → &lt;provider&gt; / &lt;model&gt; → Context length and try again."* (user-facing location per ADR-068's vocabulary, cross-spec review X-37; the field is the per-(provider, model) override, D2 rung 2 — `ContextSettings.model_overrides[]` on `PUT /api/v1/settings/context`; the turn refusal carries the new `LLMError` code `context_window_unknown`, attribution `config`, evaluated third in the pre-turn gate after ADR-067's `needs_provider` and ADR-068's `model_unassigned` — X-09) — a named, actionable state, never a guessed number. Setting the override makes the model usable immediately — the settings write triggers a registry reload (`TriggerReload`, the default-agent precedent), so no restart; the override is clamped like every other (D2). **Classification is not defined here** (X-16/X-17): ADR-067 owns the single predicate `locality: local | cloud` on the catalog row, and this ADR consumes it; there is no classification table and no `lmstudio` factory id in ADR-066. The per-model refusal state is carried on the wire by ADR-067's providers-catalog `GET` projection — per-model `window_source` and, for a local model whose live query failed, `window_unknown: true` — which ADR-068's row and picker render (X-08); it is **not** a `Provider.status` value.

---

## 5. D4 — The cap at the door

**Why a cap is still needed once the window works mid-turn.** The glass overflows from the oldest end. An item larger than the whole glass cannot be fitted and cannot be split — no amount of pouring helps. The cap is not a second context system; it is the guarantee that nothing indivisible arrives oversized.

**One choke point.** Today there is none: `role: "tool"` messages are built at **twelve** sites (spec review MAJ-002, verified): nine in `loop.go` (the success-path `toolResultMsg`, seven `deniedMsg` sites, and the `skippedMsg` synthetic result), plus attachment hydration (`pkg/agent/attach_hydrate.go`), orphan repair (`pkg/agent/repair.go`) and recall-span re-injection (`pkg/agent/recall_conversation.go::buildRecallSpanMessages`). D4 introduces **one function through which every tool result becomes a context message**, so MCP and builtins are covered by construction and a server connected tomorrow is covered on its first call. **Every** result goes through it — success, `isError`, denied, skipped, hydrated attachments, recall pages and delegated sub-turn reports alike (MAJ-016); only the repair placeholder is exempt, being bounded by construction.

**The numbers** — Claude Code's, in characters (operator decision; `estimateMessageTokens` is an unvalidated 2.5-chars/token heuristic, so a character cap is exact where a token cap is a guess):

| Surface | Cap | Claude Code equivalent |
|---|---|---|
| MCP result | **62,500 chars** | 25,000 tokens |
| Builtin result, success | **64,000 chars** | Claude Code's Bash limit is 30,000; Omnipus keeps `read_file`'s 64 KB — pass-2 MAJ-007, operator-confirmed |
| Builtin result, failure | **10,000 chars**, head-and-tail | Bash failure path |
| Warn threshold (metric) | **25,000 chars** | 10,000-token warning |
| Operator ceiling | **150,000 chars** | `BASH_MAX_OUTPUT_LENGTH` ceiling |

**Align the shipped per-tool caps to these** rather than layering (operator decision). `read_file`'s independent choice of 64 KB — within 2% of the MCP figure — is corroboration the magnitude is right. **No per-server opt-out.**

**Over-cap behaviour.** The result enters the window **truncated head-and-tail with a marker stating the full size and that the complete result is in the archive** — the same recall mark D5 uses, so the model has one vocabulary for "this was bigger than what you see". The **full result is still appended to the archive** (§6.2). It is not an error.

**Window-independent — amended (spec review CRIT-002).** The configured caps are identical at 131,072 and 1,048,576, so correct even where D1–D3 resolve badly. But a cap that ignores the window can exceed a small local model's whole budget (one 64,000-char result is ≈25,600 estimator tokens; an 8k Ollama window has a budget of a few thousand), which would make the D6 floor unsatisfiable and the thrash guard reachable with no fault. **Decision:** the per-result cap is **clamped to the budget** — `effective_cap = min(configured_cap, floor(0.5 × B × 2.5))` chars, where `B` is the one budget of D6 (tokens) and 2.5 the estimator ratio — so a single result can never exceed half the budget; and when an assistant message issues `N` parallel calls with `N × effective_cap_tokens > B`, each of its results is capped at `effective_cap / N`. The floor ("never empty the last assistant step") is therefore always satisfiable. The same clamp bounds the archive line: a result is capped so that its JSON-encoded archive line is ≤ 0.8 × `maxLineSize` (MAJ-008).

**Not only tool results — corrected after the second review.** The first draft capped tool results alone, so an oversized *user message* (a pasted 500 KB document) or oversized *tool-call arguments* (the model writing a whole file into a parameter) would reach D6 with nothing to empty and hit the thrash guard — a typed turn death, contradicting §8's "nothing size-related is turn-fatal". Two more bounds, same numbers, different timing:
- **User messages** are checked **where an inbound message becomes a turn — `pkg/agent/loop.go::processMessage`, before turn registration and before the user message is persisted** (spec review MAJ-001: every channel, SSE, goal follow-ups and the async notifier enter there; the WebSocket handler is only one intake): over the builtin cap, the message is refused with a clear, non-fatal reply on the originating channel (*"that message is N chars; the limit is 64,000 — attach it as a file or shorten it"*). Nothing is persisted, no turn is registered, the user edits and resends.
- **Tool-call arguments** over the cap are **refused as a structured tool result** (the ADR-060 family), not executed and not turn-fatal; the model sees the refusal and retries smaller. `max_output_tokens` already bounds what the model can emit, but 131,072 tokens is still far above the cap.

With those and the budget clamp above, the thrash guard is **unreachable by construction** — every tool result fits in half the budget, the floor set fits in the budget, user messages and arguments are bounded before they arrive — and §17.4 tests it with an injected fault only.

---

## 6. D5 — Empty in place, leave a recall mark

**When.** The window is over budget (D6), the oldest candidate is a tool result, and its assistant call is still in the window — so advancing `Skip` past it would orphan the call. *(Clarified 2026-08-22, spec review register #3.)* **Eligible** = every `role: "tool"` message in the window whose assistant call is in the window, **including the results of an earlier, completed turn that the pre-turn trim kept** (`windowTrim`'s floor keeps the last complete turn whole even when it is oversized), **excluding the floor set** (every result of the most recent assistant message). Emptying proceeds oldest-first across that set.

**What.** The slot stays; its content becomes a short deterministic mark:

```
[tool result emptied — search_email, 1,178,522 chars, turn 6, tool_call_id=call_978a85… · recall_conversation(tool_call_id=…) returns it in pages]
```

The call/answer structure is untouched, so the request stays valid for every provider — including those that require a window to open with a user message, where a mid-turn *cut* would not. This is the Anthropic `clear_tool_uses` / LangChain `[cleared]` mechanism, with the placeholder pointing at a real retrieval path.

### 6.1 Where emptying is applied — corrected after the second review

**[CORRECTED 2026-08-22.]** The first version said emptying is applied "at assembly time" in `assembleMessages`. That does not reach the case that matters: verified in `loop.go`, `assembleMessages` runs at **four** sites only — the start of the turn and after each of three kinds of trim — while **every mid-turn LLM call is made with `callMessages`, built from the in-memory `messages` slice to which tool results are appended directly** (`messages = append(messages, …)`). A projection confined to `assembleMessages` would empty nothing during the tool loop, which is exactly where the incident happened. (Re-running `assembleMessages` mid-turn is not the fix either — it would re-add the user message already persisted at turn start.)

**Decision.** Emptying acts on **the in-memory `messages` slice, inside the tool loop, right after D6's budget check** — replacing the oldest eligible tool result's `Content` with the mark before `callMessages` is built. The archive is never touched (append-only, ADR-028 D14), so nothing on disk changes. The set of emptied `tool_call_id`s is **also** persisted in the window's meta file alongside `skip`/`count`, and `assembleMessages` applies the same substitution when it does run (turn start, post-trim, reload) — so the in-memory view and the reloaded view agree. Pure function of (messages, budget, emptied-set): deterministic, no LLM call.

**Same rule for D4.** The first draft said an over-cap result is "truncated in the window, full in the archive" without saying how. Same mechanism: the full result is appended to the archive; the in-memory `messages` entry carries the capped content plus the mark; the meta file records the id so reload shows the capped form too.

### 6.2 Recall is already wired

Verified:

- `SessionReader.ReadArchive` returns *"the FULL archived log … ignoring meta.Skip. Evicted (skipped) turns are included."*
- `pkg/agent/recall_conversation.go` calls `ReadArchive`, **re-injects `role: "tool"` messages, and remaps their `tool_call_id`s** so the pairing stays valid on re-entry.
- The write path (`JSONLBackend.AddMessage` / `AddFullMessage`) applies **no content truncation** (searched), and the archive reader's line ceiling is `maxLineSize = 10 MB` (`pkg/memory/jsonl.go`, scanner grows from 64 KB) — a 1.18 MB result is written and read back whole.
- `windowTrim` already budgets the active recall span (FR-019, `al.recallSpans`), and may drop it alone to fit.
- Archive lines are `ArchivedMessage{providers.Message; TS}` (`pkg/memory/jsonl.go`), so every tool result carries its `ToolCallID` on disk — there is something to address by.

### 6.3 Recall by tool result — closing the gap

`recall_conversation` takes `query`, `turn_range`, `time.from/to`; it cannot address a tool result by id, and its span budgets are **4,000 tokens** (query/time mode) and **8,000** (turn-range mode) (`pkg/agent/recall_conversation.go`). A 1.18 MB result cannot come back whole through either — and must not: it would overflow the window it was emptied from.

**Decision.** Add a fourth mode, `tool_call_id`, with optional `offset` and `length` in characters:

- Matches the archive line whose `ToolCallID` equals the argument; returns that one `role: "tool"` message, re-paired via the existing id remap.
- Returns **one page of at most 64,000 chars** (the builtin success cap, since recall is a builtin) — `offset`/`length` page through the rest, the same interface `read_file` uses. The mark states the total size so the model knows whether to page.
- **The `tool_call_id` mode is exempt from the 4,000 / 8,000-token span budgets** — those exist to bound *search* results across many turns; a single addressed page is bounded by its own cap instead. It is still a tool result, so it is subject to the D4 cap, counts toward the D6 running total, and can itself be emptied by D5 later. (The first draft said both "bounded by the 62,500 cap" and "counts against the span budget" — contradictory; this replaces it.)
- The mark carries the `tool_call_id`, so the pointer resolves in a single call.

**What recall does not cover, by design.** A turn that **aborts** is rolled back: `pkg/agent/turn.go::restoreSession` calls `RollbackAppended`, truncating the archive to its turn-start line count and restoring `Skip` *"so mid-turn evictions are undone"*. Verified on the incident: the archive went from 1,317,446 bytes (snapshot 19:19) to 39,004 (21:04) when the turn died. So recall retrieves the results of turns that **completed**; a failed turn's results leave the archive with the rest of its restore point, exactly as today. With D4–D6 the incident's turn would have completed, so this is consistent rather than a hole. **The D5 emptied-set joins the same restore point** — rolled back with `Skip` on abort, so a retried turn starts from an un-emptied window. *(Amended 2026-08-22, spec review CRIT-003.)* The restore point is the **turn-start** triple `restoreSession` actually uses — archive line count, `Skip`, and now the emptied-set as of turn start — captured once in `newTurnState` and **never moved during the turn**. `turnState.restorePointHistory` and `refreshRestorePointFromSession` are dead state today (written, read nowhere) and are **deleted** (greenfield), together with the sentence in §16a MAJ-017 that said the restore point is refreshed after each empty. Abort rolls back to turn start, full stop.

(The incident payload survives only in the gateway's browser-facing `tool_results/` store — 826,690 and 1,244,567 bytes — which is gitignored and not model-reachable, and stays that way per §14.1.)

---

### 6.4 D5.4 — Recall is injected at the tool-result site (P0; ships as a hotfix first, then this branch inherits it)

**The bug (operator report 2026-08-22, severity High), verified on this branch.** `pkg/agent/recall_conversation.go::Execute` builds the full-text span (whole turns, verbatim), calls `setRecallSpan`, and returns only *"Recalled N turn(s) … into context."* The span is read in exactly **two** places — `loop.go::assembleMessages` (turn start and the trim sites) and `loop.go::windowTrim` (FR-019, which **drops** it under pressure). Inside the tool loop every LLM call uses `callMessages` built from `repairedHistory := messages` — the in-memory slice — and `activeRecallSpan` is never consulted there. So a recall made mid-turn reaches the model only if a trim happens to re-assemble before the next call; otherwise the next request carries the receipt string and nothing else. Live evidence (sessions `01M0HYPY…`, `01M0MS6W…`): 25 recall calls, all "success", the model's next answer quotes nothing; no span set/drop is logged. Existing tests (`TestRecallSpan_ReinjectedProviderValid` and siblings) assert the tool result string and span state only — none asserts that the **next LLM request** contains recalled text. **This is a precondition for D5: "emptied content comes back via recall" is false until it is fixed.**

**Decision.** Recall is injected **at the tool-result site, inside the tool loop, immediately after the recall tool result is appended to `messages`** — the same in-memory mutation point D5/D6 use:

- (a) splice `span.Messages()` at the position `BuildMessages` would use (after the pinned core, before the window) and run `sanitizeHistoryForProvider` on the combined slice;
- (b) mark the span injected (`ts.injectedRecallSpan`, by identity); `assembleMessages` skips an already-injected span so a later Site-3/4 reassembly does not double it;
- (c) **budget first:** before splicing, run the same fit check `windowTrim` uses (D6's one budget B); if the span does not fit, do **not** splice and do **not** drop silently — the tool result says *"N turn(s) found (X estimator tokens) but they do not fit the current window; narrow with turn_range or query"*, so the model learns the real outcome;
- (d) the receipt is rewritten to describe what happened: *"Recalled N turn(s) (turns A–B); their text is now in your context"* **only** when injected;
- (e) the `tool_call_id` mode (§6.3) injects the same way, one page at a time;
- (f) span set / injected / refused / dropped are logged at INFO with sizes (there is no observability today);
- (g) once injected, the span is subject to D5 emptying and D6 budget like any tool result.

**Regression test (loop level, fake recording provider):** seed a nonce in turn 1, evict it (advance `Skip`), have the provider's first response call `recall_conversation(turn_range:"1-1")`, assert the provider's **second** request contains the nonce and the recall marker; variant with a window too small → the tool result states the non-fit and the request lacks the nonce.

**Known limitation, recorded with a test, not fixed here:** `spawnSubTurn` gives the child the parent's tool registry (`Tools: cfg.Tools`) but its own `ephemeralSessionStore`; the recall tool was constructed with the parent's `agent.Sessions`, so a child's recall reads the parent store under the child's ephemeral key and returns nothing.

### 6.5 D5.5 — Hydration may only fill an empty archive, never overwrite one (P0; ships as a hotfix first, then this branch inherits it)

**The bug, verified on this branch and on the operator's data.** `pkg/agent/attach_hydrate.go::HydrateAgentHistoryFromTranscript` rebuilds the per-agent archive **from the UI transcript** — user/assistant text plus one `role: "tool"` stub per recorded tool call, whose content is `marshalToolResult(tc)` (the transcript's bounded inline `result` map when present — ≤ 50 KiB, larger results are offloaded stubs — else `{"status":…}`) — and writes it with `SetHistory`, which (`pkg/memory/jsonl.go::SetHistory`) rewrites the whole file and sets `meta.Skip = 0`. It is called **unconditionally** from the `attach_session` handler (`pkg/gateway/websocket.go`) and conditionally from `loop.go` (self-heal when the history has no assistant/tool message). Evidence: the 08-21 Jarvis archive held 21 user / 53 assistant / 36 tool lines at the 19:19 snapshot and 22 / 42 / 0 afterwards with `skip = 0` — a whole-file replacement, not a rollback. Consequences: every reopen discards all real tool results (recall returns text only), resets the window pointer, and violates ADR-028's append-only archive — so D5's "the full result stays in the archive" is false until this is fixed.

**Decision.** Hydration may only **fill an empty archive**: the attach path checks the agent archive first and skips hydration when it has ≥ 1 line; the self-heal path keeps its existing emptiness condition. When hydration does run (a genuinely empty archive) it must preserve tool results: the UI transcript's `tool_call` entries gain a bounded `result` field, capped by D4 and written by the same choke point, so the rebuilt archive is not lossy; until that transcript-shape change lands, a hydrated archive is marked `hydrated: true` in meta and recall by `tool_call_id` answers *"not available — session was rebuilt from the transcript"*. `SetHistory` must never reset `Skip` on an existing archive — greenfield: it refuses when the file is non-empty.

**Regression tests:** attach the same session twice → the archive file is byte-identical and `meta.skip` unchanged; attach to a session whose archive is empty → hydration runs once and includes tool results; the ADR-028/FR-019 append-only invariant test gains an attach step.

## 7. D6 — The window runs mid-turn

Two changes to `windowTrim`'s caller and to its notion of a legal operation. Either alone fails: checking mid-turn with user-only boundaries finds no legal cut; allowing finer operations while checking only at turn start means nobody looks while the turn fills.

**When.** Invoke the budget check **after every tool result is appended** (inside the tool loop, before the next LLM call), in addition to the existing pre-turn site. Cost: the estimator is linear in window size and already runs per turn.

**What, by position.**

| Oldest over-budget content | Operation |
|---|---|
| an earlier, complete turn — **pre-turn site only** | advance `Skip` to its end — **today's behaviour, unchanged** (the pre-turn trim may cut) |
| any eligible tool result (D5 *When*) — **mid-turn site** | **empty in place** (D5) — never cut, so structure and provider ordering rules hold; **mid-turn never advances `Skip`** (spec review MAJ-003: a mid-turn `Skip` advance would not change the in-memory slice the request is built from) |
| every result of the most recent assistant message | **never** — the floor; the model is reasoning about it; always satisfiable by the D4 clamp |

Mid-turn the window therefore **never cuts, only empties**. `parseTurnBoundaries` stays as it is; the new operation is orthogonal to it.

**Trigger — ONE budget, no new formula** *(superseding the earlier `min(absoluteBudget, 0.9 × resolvedWindow)` wording; coordinator decision 2026-08-22 after the spec review, CRIT-001).* The budget is `windowTrim`'s existing one, in estimator tokens:

```
B = W − max_tokens − ceil(0.05 × W) − pinnedCoreOverhead
```

The pre-turn site, the mid-turn site, timeout recovery and the model-switch path all call the same `isOverContextBudget` against **B** (its threshold changes from the bare `contextWindow` to B). **The 0.9 factor is dropped entirely** — the 5 % headroom and the `max_tokens` reserve already play that role. The absolute term is a **second condition on the tool-result share only**: `share = Σ tokens(role == "tool" messages in the window)`, with `share > 160,000` estimator tokens (= 400,000 chars ÷ 2.5, operator-settable as `absolute_trigger_chars`, default 400,000).

```
over_budget ⇔ total > B  OR  share > 160,000
```

**Target** after emptying = 80 % of whichever condition fired (total ≤ 0.8 × B, or share ≤ 0.8 × 160,000). Emptying stops when the target is reached **or no eligible result remains**; it does not re-fire on the next check unless a condition is exceeded again. Absolute term because D1–D3 can be wrong (§1.1). Denominated through the estimator for the same reason as D4 — revisit if a real tokenizer is ever adopted (D8 is not adopted, so no calibration source exists).

**Order of operations** for a tool result: ingest bound (D10) → D4 cap → append to archive → D6 budget check → D5 empty oldest as needed → assemble → LLM call.

**Thrash guard.** If, after every eligible result is emptied, **a trigger condition (not the target) is still exceeded** — only possible if a non-tool message is itself oversized — stop, log one ERROR, and surface the typed error `context_unrecoverable` (D7) rather than loop. With the D4 clamp and the user/argument bounds this is **unreachable by construction**; §17.4 reaches it only through an injected fault.

---

## 8. D7 — No silent turn exits

The four returns in §1.4 gain a typed code, a log line with the raw cause, and a transcript entry — matching the classified path beside them. `"turn timed out"`/`"turn canceled"` stop resolving to `CodeUnknown`; codes `turn_canceled` (attribution **`user`** — the Stop button is the common cause; `user` is added to `x-user-message-attributions` and rendered by the SPA as a neutral notice, not an error toast), `turn_timed_out` (attribution **`provider`**) and `context_unrecoverable` (attribution `product`, the thrash guard) are added to `contracts/components/schemas/LLMError.yaml` and regenerated (spec review MAJ-013). Remaining turn-fatal conditions: provider auth rejected, provider unreachable after retries, workspace unavailable. **Nothing size-related is turn-fatal** once D4–D6 are in.

## 9. D8 — Learn the window from the provider: NOT ADOPTED

*(Operator decision 2026-08-22, reversing the earlier draft.)* The draft proposed parsing the provider's context-overflow error (`pkg/agent/translate_error.go::contextOverflowSubstrings` already matches *"maximum context length"* and six siblings) and caching the stated limit as a lower-only override, reset on catalog change — Hermes's mechanism. **Rejected: the catalog is the source of limits; Omnipus does not infer limits from error text.** Consequences: the D2 ladder has no "learned" rung; D9's source label is `operator | live | catalog | floor`; §16a MAJ-003 applies to operator overrides only (they never expire). The accepted cost is stated plainly: a brand-new model not yet in the daily catalog, or a plan-specific cap a provider does not publish, overflows until the catalog or the operator corrects it — each time a typed, loud error (D7), never a silent one. `contextOverflowSubstrings` keeps its existing job (classifying the error for the user); nothing is written back from it.

## 10. D9 — Controls in Settings and the UI

First-class surfaces per Constraint #8 (schema, generated types, REST, SPA): the per-surface caps from D4 with the 150,000 ceiling; the D6 absolute trigger; the D10 ingest bound; the **global default window** (the single home — `agents.defaults.context_window` and its env var are **removed**, greenfield, spec review MAJ-014); the **per-(provider, model) overrides** (`model_overrides[]`, D2 rung 2); all on `GET/PUT /api/v1/settings/context` (`ContextSettings.yaml` / `ContextSettingsUpdate.yaml`). Per agent: the **effective context window shown read-only with its source** (`ContextWindowSource.yaml` — `operator | live | catalog | floor` — owned here and `$ref`'d by `Agent.context_window_source` and ADR-068's `DefaultModel.window_source`, X-06) and a `clamped` flag, plus the per-agent override on `Agent.yaml` / `AgentUpdateRequest.yaml` (the read-only fields are **optional** on the wire; ADR-067 commits the coordinated `Agent.yaml` edit). A `(provider, model)`-level resolver, `ResolveWindow(provider, model, agentID="")`, runs rungs 2–6 when no agent is given so ADR-068's default-model card and row expand can show window + source; exempt providers resolve to `context_window: 0` with `window_source` absent (X-07). **User-facing location: Settings → Models** (ADR-068's wording, X-37); the REST route stays `/api/v1/settings/context`. Every write triggers a registry reload (`TriggerReload`), so the next turn uses the new value without restart (spec review MAJ-011). That number is currently unreachable from the UI and the API, which is half of why the 8× error stayed invisible.

*The walkthrough of the onboarding and provider screens, and the UX review at 190 providers, are [ADR-068](ADR-068-subscriptions-provider-deletion-and-provider-ux.md) §4–§5.*

## 11. D10 — Bound what enters memory

D4 protects the window; it cannot protect the process — by the time a result is measured it has been received, held and parsed. Every network or subprocess read is bounded at ingest. `fetch_url` is already correct (`http.MaxBytesReader`, configurable, 10 MB *"Security Fallback"*). Three search providers read unbounded (`BraveSearchProvider.Search`, `DuckDuckGoSearchProvider.Search`, `PerplexitySearchProvider.Search` — `io.ReadAll(resp.Body)` directly after `client.Do`), while two other sites in the same file use `io.LimitReader(…, 1<<20)`. The MCP path has none; the Go SDK's `MaxBytes` is `MemoryEventStore` SSE resumability, not a response cap, and Omnipus never sets it. *(Amended 2026-08-22, spec review MAJ-009.)* For MCP the bound is enforced **on the transport read**, not after parsing: the stdio transport Omnipus already owns (`pkg/mcp/manager.go::sandboxedCommandTransport` / `sandboxedStdioConn`, wrapping the SDK's `IOTransport`) gets an `io.LimitReader`-style bound on the server's stdout stream per JSON-RPC message, and the HTTP/SSE transports get `http.MaxBytesReader` on the response body via the client's `http.RoundTripper` — so a 500 MB response is aborted at 8 MB, not buffered. The two existing `io.LimitReader(…, 1<<20)` sites (`GLMSearchProvider.Search`, `BaiduSearchProvider.Search` in `pkg/tools/web.go`) are raised to the same bound. Exceeding the ingest bound is a tool failure, not a truncation — half a JSON document is not partially useful. A second, post-parse check bounds the **encoded archive line** (MAJ-008): after filtering and before append, a result whose JSON-encoded line would exceed 0.8 × `maxLineSize` is capped by D4's clamp so the line always fits.

---

## 12. Contract impact (Constraint #8)

- The emptied-set in window meta is an internal file, not a wire type. It is keyed by **(tool_call_id, archive line index)** — ids are provider-generated and not unique across an archive (spec review MAJ-007); the mark carries both, and `recall_conversation` accepts an optional `archive_line` beside `tool_call_id` (most recent line wins when omitted).
- **The SPA must learn that a result was capped or emptied** (spec review MAJ-005): transcript `ToolCall` entries gain `content_state: full | capped | emptied` (`contracts/components/schemas/ToolCall.yaml`), and emptying emits a `tool_result_projection` WS frame `{tool_call_id, archive_line, content_state, mark}` defined in `contracts/asyncapi.yaml`. The transcript read returns the projected content plus `content_state`; the gateway's `tool_results/` store keeps the full content for Verbose chat.
- D5's recall mark reaches the SPA inside a tool-result message. **Operator decision 2026-08-22: it is rendered in the chat thread only when Verbose chat is on** (`src/lib/toolVisibility.ts`, Settings → Chat); otherwise it stays in the transcript and the ActivityPanel like other infra-only output. It must not be hand-rolled with `fmt.Sprintf` (ADR-060's `%q` finding). Whether it formally joins the ADR-060 family or is typed beside it is left to the implementing commit — the enforcement (schema, no string assembly) is what is decided here, not the family's name.
- D7's `LLMError` codes (`turn_canceled`, `turn_timed_out`, `context_unrecoverable`, plus D3's `context_window_unknown`) and the new `user` attribution land in **four copies** — `contracts/components/schemas/LLMError.yaml`, `LLMErrorReplay.yaml`, and the inline `components.schemas.LLMError` / `LLMErrorReplay` blocks in `contracts/asyncapi.yaml` (the generators read asyncapi) — guarded by `pkg/api/generated/llm_error_codes_test.go` and `llm_error_catalogue_test.go` (cross-spec review X-01). The contract **file** edit is made in ADR-067's single coordinated contract commit; this ADR owns the semantics and copy. D9's `ContextSettings` / `ContextSettingsUpdate` / `ContextWindowSource` schemas, the `Agent.yaml` window fields, `ToolCall.content_state` and the `tool_result_projection` frame all require `make gen-contracts`.
- The catalog file/schema, the providers-catalog endpoint and the provider-id changes are ADR-067 §5; the `DELETE` route, the default-model `PUT`, the `ProbeProviderRequest` enum change and the SPA catalog file are ADR-068 §7.

## 13. Consequences

**Positive.** No single result exceeds the budget (D4); no turn exceeds the window regardless of length (D5+D6); the window is resolved with a clamp and a loud floor (D2–D3) and is visible (D9); failures are diagnosable (D7). Nothing is summarised or deleted; every result of a completed turn remains recallable, in pages. No new storage, no new retention policy, no new file surface. Per-turn token cost falls by the quadratic argument.

**Negative / accepted.** The conservative floor over-trims an unseeded cloud model — visible, harmless. A local model whose endpoint reports no window is unusable until the operator sets one — deliberate. Emptied results cost a recall round-trip when the model does need them. The per-result budget check adds linear work per tool call.

**Bears on the running release (ADR-068 §2.4):** the shipped `antigravity` OAuth provider is the practice Google's Antigravity terms §6 name and enforce with account suspension, and it is the fresh-install default model. Its removal precedes shipping this branch; the decision and checklist are ADR-068's.

**All four diagnosed defects have a decision:** §1.1 → D2–D3 here with D1 in ADR-067, §1.2 → D4 (+D10), §1.3 → D5+D6, §1.4 → D7.

## 14. Retired from the earlier draft — and why

1. **Spill-to-disk handles in the agent `work/` tree.** The content is already on disk in the archive and already reachable by `recall_conversation`; a second copy meant two stores, two lifecycles, a new retention rule and a new file surface to duplicate what exists. (The draft's *security* objection to spill was itself inverted — `agents/*/work/` is gitignored at `.gitignore:39`; `sessions/` and `.context/` are committed by design — but the simpler reason above is sufficient on its own.)
2. **A four-shape structural reducer and schema-derived refetch recipes.** Only ~9 of 89 builtins accept a narrowing parameter (audit, §15.1), so refetch was a dead end in the common case; and a complete index is unnecessary when the full content is one recall away.
3. **A second per-turn budget beside the sliding window.** Two systems disagreeing about the budget. Replaced by running the one window mid-turn (D6).
4. **Clamping old tool results to ~2,000 chars (Cline-style).** Lossy compression by another name — operator direction: no summarisation — and unworkable as written because `GetHistory` re-reads from disk.
5. **Relocating results at assembly time.** Subsumed: D5's emptying *is* assembly-time substitution, now with a recall mark instead of a path.
6. Also rejected, unchanged from the first draft: raising `max_tokens` to fix the window; setting `context_window` once and stopping; OpenRouter `middle-out`; waiting for MCP; mid-turn **cutting** (breaks provider ordering rules; emptying does not); iteration count as a context control (`max_tool_iterations` stays a runaway guard — it resolves per-agent → defaults → hardcoded **200**, so a fresh install is four times more exposed than the operator's 50). Catalog-related rejections (a dedicated window registry, boot-time fetch, exempting `antigravity`) are ADR-067 §6 and ADR-068.

## 15. Implementation tasks

1. Tier-1 gaps from the audit: add a bounding parameter to `list_directory` (`path` only), `inspect_session` (`session_id`, `tool_name`, `role`), `recall_conversation` (`query`, `turn_range`, `time`). With D4 at the door these are hygiene rather than blockers.
2. `recall_conversation`: the `tool_call_id` mode with `offset`/`length` paging (§6.3); the emptied-set added to the turn restore point.
3. The local-endpoint live window query and the "set the context length" state on the provider row and model picker (D3).
4. Bound the three search providers' reads (D10).
5. Confirm the provider ordering rule in §16 before allowing any mid-turn cut in future.
6. **P0 hotfix, inherited by this branch:** D5.4 — recall injection at the tool-result site, with the loop-level nonce test and the sub-turn limitation test.
7. **P0 hotfix, inherited by this branch:** D5.5 — hydration fills only an empty archive; `SetHistory` refuses a non-empty file; the transcript `tool_call.result` field; the attach-twice byte-identity test.

### 15.1 Tier-1 audit (2026-08-22, this branch)

Catalog from the per-agent policy map (Constraint #6 requires it to enumerate every static builtin): **89 tools, 39 capable of bulk output.** Already bounded: `read_file` (`offset`/`length`, 64 KB cap), `library_read`, `read_inbox`, `search_email`, `search_web`, `fetch_url`, `list_jobs`, `recall_memory`, `find_skills`. Gaps: the three in task 1, read per-receiver. Compliant rows for tools sharing a source file were read at file level — spot-check before relying on an individual row.

## 16. Unverified

- **[UNVERIFIED]** That the Anthropic Messages API rejects a window whose first message is not `user` (the reason D6 empties rather than cuts mid-turn). Not found in `pkg/providers/claude_provider.go` (searched); design is safe either way, since emptying is valid for every provider.
- **[UNVERIFIED]** Ollama's `/api/show` context field name (daemon not running when checked); D3 depends on the *existence* of a live answer, verified by survey, not on the field name.

## 16a. Pass-2 review resolutions (2026-08-22)

Each item names the pass-2 finding it closes (`ADR-066-…-review-pass2.md`).

- **MAJ-001 — one budget, one unit.** `windowTrim` already computes the budget in estimated tokens as `window − max_tokens − 5% headroom − pinnedCoreOverhead`. D6 does **not** add a second formula: the mid-turn check calls the same `isOverContextBudget` with the same budget (its threshold becomes B — today it compares against the bare window), and the 400,000-char `absoluteBudget` is expressed through the estimator's 2.5 chars/token ratio (≈160,000 tokens) as a *ceiling on the tool-result share* of that one budget. The `0.9 × window` term of the earlier D6 wording is **superseded** (spec review CRIT-001). The `SummarizeTokenPercent` scaling in timeout recovery is a leftover of the deleted summariser and is removed; timeout recovery uses the same budget.
- **MAJ-003 — the ratchets.** With D8 not adopted there is no learned value to reset. An operator override never expires; the clamp against the model's capability is recomputed from the catalog on every resolution.
- **MAJ-007 — cap alignment corrected.** Aligning the builtin success cap to Claude Code's 30,000 would have *halved* `read_file` (64 KB) and `fetch_url` (50,000) while citing `read_file`'s 64 KB as corroboration — contradictory. The builtin success cap is **64,000 chars** (= `read_file`'s existing, independently chosen limit; within 2 % of the MCP cap); `browser_get_text` (100 KiB) is lowered to it; shell's 10,000 stays as its *failure-path* value and rises to 64,000 on success. **Operator confirmed 2026-08-22:** 64,000 for builtins, 62,500 for MCP, 10,000 for failures.
- **MAJ-008 — D10 has numbers.** Ingest bound default **8 MB** per response, operator-settable, **strictly below** the archive reader's `maxLineSize = 10 MB` — JSON escaping inflates a raw result on its way into the archive line, so an ingest bound equal to the line ceiling (the first draft's 10 MB, matching `fetch_url`'s fallback) would admit results that cannot be read back. The setting's ceiling is enforced as `< maxLineSize × 0.8`; `fetch_url`'s own fallback is aligned to 8 MB. §17.1's 2 MB test fits under it.
- **MAJ-009 — prompt caching is acknowledged.** Emptying a result changes the request prefix, which invalidates the provider's cached prefix for the next call. Accepted, and bounded: D5 empties **down to the target in one pass per trigger** (not one result per call), so the prefix changes once per trigger rather than once per iteration; the cache is then warm again for the following calls.
- **MAJ-016 — the floor is the whole last step.** "The most recent tool result is never emptied" means **every result of the most recent assistant message** (a parallel call has N); the floor is that set, not one entry.
- **MAJ-017 — restore point carries the emptied-set.** The turn-start restore point records `Skip` *and* the emptied-set; `restoreSession` → `RollbackAppended` restores both (new parameter, written atomically with `Skip` in the meta file). ~~`refreshRestorePointFromSession` runs after each empty exactly as after each trim.~~ **Withdrawn 2026-08-22 (spec review CRIT-003):** that function writes dead state; the restore point is captured once at turn start and never moved — see §6.3.
- **MAJ-002, MAJ-004, MAJ-015** — resolved by the local-endpoint rule in D3, the 2.0.0 schema, and the three-way split.

## 17. Exit proof

1. **Guard test** — feed a ~2 MB tool result through the loop: the assembled request stays under the window, the model sees a marked result and continues, **the turn completes with no user-facing error**.
2. **Long-turn test** — a turn of 50 tool calls each at the cap, against a small window: the request never exceeds the window at any iteration; the most recent result is always intact; emptied results carry marks; `recall_conversation(tool_call_id=…)` returns any emptied result, paged under the D4 cap, and paging reaches its last byte.
   2b. **Rollback test** — a turn that aborts after emptying restores archive length, `Skip` and the emptied-set to their **turn-start** values (never an intermediate state).
   2c. **Live-equals-reload test** — after a mid-turn empty, the bytes sent to the provider for that message and the bytes assembled on reload are identical.
3. **Window-agreement test** — the catalog and `windowTrim` resolve the same window for a given model (the three-way disagreement that exists today), and the pre-turn, mid-turn, timeout-recovery and model-switch sites compare against one budget B.
4. **Thrash-guard test** — an oversized user message is refused in `processMessage` before the turn, on every intake (WS, SSE, a channel such as Slack) — no transcript entry, no error frame; oversized tool-call arguments produce a structured refusal and the turn continues; the thrash guard itself is reached only by an injected fault and then produces `context_unrecoverable`, not a loop.
   4b. **Small-window test** — on an 8,192-token local model, a tool returning 200,000 chars enters capped to at most half the budget, a 3-call parallel step fits in the budget with its results capped at `effective_cap / 3`, the floor is never emptied, and the turn completes.
   4c. **Mid-turn-never-cuts test** — when the oldest over-budget content mid-turn is an earlier complete turn, `Skip` does not move and the request bytes shrink only by emptying.
5. **Silent-exit test** — each of the four §1.4 returns now produces a log line, an event and a transcript entry.
6. **Local-endpoint test** — a `locality: local` model whose endpoint fails to report a window is refused with `context_window_unknown` and the "set the context length under Settings → Models → Model overrides" message, never assigned 128,000, and the catalog `GET` projection shows `window_unknown: true` for it; writing the per-(provider, model) override via `PUT /api/v1/settings/context` triggers a reload and makes it usable without restart. A custom row at a public host is `locality: cloud` and floored, never refused.
7. **Resolver test** — `ResolveWindow(provider, model)` with no agent returns the rung-2–6 window and source for a catalog model; an exempt (subprocess-CLI) provider returns 0 with no source.
8. **Recall-injection test (D5.4)** — a nonce evicted past `Skip` is present in the provider's second request after `recall_conversation(turn_range:"1-1")`, with the recall marker; with a window too small, the tool result states the non-fit and the request lacks the nonce; the sub-turn limitation is pinned by a test.
9. **Hydration test (D5.5)** — attaching a session twice leaves the archive byte-identical and `skip` unchanged; attaching to an empty archive hydrates once with tool results; `SetHistory` on a non-empty archive is refused.
