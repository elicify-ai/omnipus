# ADR-066: Context overflow — the sliding window extended mid-turn, tool results emptied with a recall mark, and a per-result cap at the door

- **Status:** Proposed (2026-08-21; restructured 2026-08-22). Drafted from a live production incident on the operator's own instance; awaiting operator ratification before implementation.
- **Date:** 2026-08-22
- **Related:** [ADR-028](ADR-028-context-paging-sliding-window-recall.md) (`windowTrim` as the only compaction path — **extended, not superseded**: D6 changes *when* it runs and *what it may do mid-turn*; it remains the only path, and nothing here summarises); [ADR-051](ADR-051-media-handling-and-provider-error-translation.md) (`LLMError` classifier — extended by D7); [ADR-060](ADR-060-structured-tool-failure-family.md) (D5's recall mark is a candidate family member, §12); CLAUDE.md **Constraint #1** (single binary), **Constraint #6** (explicit tool policy), **Constraint #8** (contract-first wire formats).
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1 for everything cited as read. Incident facts were read on the build tree the failing binary came from (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/build-v0.1.1` @ `6acd378`); design facts on this branch @ `4684e8c7`. Cited as `file::symbol` per CLAUDE.md except where a line number is itself the claim. Absences are cited as searches. Items marked **[UNVERIFIED]** are collected in §16.
- **History:** an earlier draft (commits `f4aaf37c`..`4684e8c7`) built a separate tool-result subsystem — spill-to-disk handles, a four-shape reducer, schema-derived refetch recipes, a second per-turn budget. The adversarial review ([ADR-066 review](ADR-066-context-budget-and-tool-result-routing-review.md), verdict BLOCK, 44 findings) and the operator's direction on 2026-08-22 replaced it with the three changes below. The earlier decisions are retired in §14 rather than deleted, so the reasoning that rejected them survives.

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

Three changes to things that already exist. **D4** — a cap at the door: no single tool result may enter the window above a fixed size, because spilling cannot help with an item larger than the glass. **D5** — when the glass is near full and the oldest content is a tool result whose call is still in the window, *empty it in place* and leave a recall mark; the full content stays in the archive and `recall_conversation` already reads it. **D6** — run the window check after every tool result, not only at turn start, so the glass overflows from the oldest end whenever it is full, turn boundary or not. Nothing is summarised, nothing is deleted, no new storage exists. D1–D3 fix the window record itself; D7–D10 make failures legible, learn from providers, expose the controls, and bound ingest.

---

## 4. D1–D3 — Know the window

**D1 — the catalog is assembled from public registries by a daily job in a dedicated repository, not typed by hand.** *(Operator decision 2026-08-22, replacing the earlier "add two fields to the hand-curated seed".)*

*One catalog, not two (operator question 2026-08-22, resolved).* Omnipus today has **two** catalog packages by accident of history — `pkg/providers/capabilities` (per-model modalities + resize limits, built for the media pipeline) and `pkg/providers/catalog` (23 provider entries, built for the picker). Greenfield removes the reason to keep both. **Decision: one package, one embedded file, one schema — providers with their models nested**, exactly the registry's own shape:

```
provider  { id, api, protocol, env, region, plan, tier, subscription_policy, resize_limits }
  └ model { id, context_window, max_output_tokens, input_modalities, tool_call, status }
```

The provider+model key is the nesting; a model's limits live under the route that serves it. The assembly job publishes **one** signed file. `pkg/providers/capabilities` is folded into `pkg/providers/catalog`; `Resolve(provider, model)` is the only lookup; the media pipeline, the agent loop, Settings and the picker all read the same in-memory document. Two files kept in step would have been the same mistake as thirty-six provider ids kept in step.

*What exists.* `pkg/providers/capabilities` already has every piece of the runtime machinery: an embedded seed (`embed.go`), a checksum-verified `GHReleasePuller` that fetches the release asset `providers_capabilities.json` + `.sha256` from GitHub with a raw fallback (`pkg/gateway/gateway.go` wires it to `elicify-ai/omnipus`, interval `capabilityCatalogRefreshInterval = 7 * 24 * time.Hour`, timeout 30 s), a semver-aware refresh that cannot downgrade, a persisted store, a validated DTO → domain parse, and `Catalog.Resolve`. **Its `resolveStrippedPrefix` fallback — which maps `z-ai/glm-5.2` to a bare `glm-5.2` by dropping the provider — is removed:** under the provider+model key it is an alias that returns the *wrong route's* limits (1,000,000 for a request that goes via OpenRouter's 1,048,576). Lookup is exact on (provider, model); a miss falls to the D2 ladder. **Nothing else in that machinery changes.** What changes is where the file comes from: today a human reads provider docs and writes the JSON (`source: "freeze-gate re-validation 2026-07-28 …"`; no generator exists in `scripts/`).

*Why.* Validated live on 2026-08-22 against all 78 seeded models: **models.dev** (MIT; 193 providers, 7,246 models; regenerated hourly; correctable by PR) carries `limit.context`, `limit.output` and `modalities.input` incl. `image` and `pdf` on **every** entry. Cross-checked against **LiteLLM's** `model_prices_and_context_window.json` (MIT; 3,111 entries; independently maintained): where the hand-curated seed and models.dev disagreed (19 models on PDF, 4 on image) and LiteLLM could adjudicate, **it sided with models.dev every time** — `gpt-4o`/`gpt-4.1`/`o3` accept PDFs (seed said no), `o3-mini` does not accept images (seed said yes). The hand-typed catalog is the stale one. The field agrees: OpenCode, Kilo, Hermes, Cline and Goose consume models.dev; Crush and OpenClaw run their own published feeds (`catwalk.charm.land`, `catalog.openclaw.ai`) — the shape adopted here.

*The assembly repository* (open source, separate from Omnipus) runs a daily job that:

1. pulls models.dev `api.json` and LiteLLM's JSON, recording the upstream commits in a manifest;
2. merges into the Omnipus schema — `context_window`, `max_output_tokens`, `input_modalities`, tool-calling, deprecation status — **keyed by provider + model**, because limits differ by route (`z-ai/glm-5.2`: 1,048,576 via OpenRouter, 1,000,000 direct);
3. applies `overrides/` (local corrections that win over both registries — e.g. `gemini-3-pro` PDF, where the registries disagree and the provider accepts PDFs in practice — and legacy models the registries have retired) and `resize_limits.json`;
4. **opens an issue on any disagreement between the two registries rather than silently choosing** — the discipline that exposed the stale seed;
5. publishes **one** file — `providers_catalog.json` + `.sha256` + signature — as a GitHub Release, `schema_version` 2.0.0 (new shape, greenfield; the old `providers_capabilities.json` is not produced).

*Closing the resize gap.* `resize_budget` (`long_edge_px`, `max_bytes`) is in **neither** registry (searched both). It is not a model fact but a provider's upload limit, documented once per vendor — the 78-model seed uses exactly **four distinct values**, one per vendor. It lives in the assembly repo as a small per-provider table, hand-maintained and PR-reviewed; the job joins it onto every model of that provider.

*Omnipus-side changes* — four, all small: the seed schema gains the new fields (1.0.0 → 1.1.0); the puller's owner/repo points at the assembly repo (asset name and sidecar unchanged); the refresh interval drops from 7 days to **24 hours, plus one background pull at startup** (never blocking boot; the existing 30 s timeout applies); and the `go:embed` snapshot is **generated from the same feed at build time**, so offline boot and online refresh agree on schema.

*Signing — required, not optional.* The feed becomes a dependency of every install's behaviour, produced by an unattended job. A checksum proves integrity in transit, not authorship. Releases are **signed** (sigstore/cosign or a pinned public key compiled into the binary); a missing or bad signature falls back to the embedded snapshot with a WARN. Blast radius is bounded regardless — a wrong value causes overflow or over-trimming, not a security breach — but the door is locked before it is opened.

*Not adopted.* **OpenRouter as a generation source**: its terms forbid automated copying of Service data; it remains a live-query source only (rung 3 of D2). **Hand-curation of the main table**: demonstrated stale. **A boot-time fetch with no bundled fallback**: violates the single-binary offline-boot requirement; every surveyed harness ships a snapshot.

**D2 — resolution ladder.** Per-agent override → global default (Settings, D9) → **live provider query** (Anthropic `/v1/models` `max_input_tokens`/`max_tokens`; Google `inputTokenLimit`/`outputTokenLimit`; OpenRouter `context_length`/`top_provider.max_completion_tokens`, no key needed; Mistral `max_context_length`; Groq `context_window`; xAI `/v1/language-models`; Ollama `/api/show` then `/api/ps` for the loaded window — OpenAI, DeepSeek, Z.ai, Moonshot and MiniMax expose no limits on their model endpoints, verified by the 2026-08-22 survey) → catalog (D1) → learned (D8) → conservative floor **with a WARN naming the model**. Live answers are cached on disk with a TTL, never fetched on the hot path. **Operator decision 2026-08-22:** both the global default and the per-agent override stay, but **an override can only lower, never raise** — the effective window is `min(override, catalogOrLearnedWindow)`. A value above the model's real capability is the incident in §1.1 by another route, so it is clamped and a WARN names the agent and the clamp. (Codex's `min(limit, 0.9 × window)` is the same shape.) `maxTokens * 4` is retired. The two other flat `128000` fallbacks (`windowTrim`, model switch) consolidate onto the ladder — three paths currently give three answers. **`claude-cli` and `codex-cli` are exempt** (they manage their own context). (`antigravity` is deleted outright — §11c.4.)

**D3 — unknown window ⇒ conservative and loud.** `Catalog.optimistic` assumes image support for unknown models; correct for modality, where optimism costs a retry. For a window, optimism costs a dead turn. The two policies diverge deliberately; this sentence is the record. **Floor value (operator decision 2026-08-22): 128,000 tokens** — what nearly every current model holds at minimum; a larger model is trimmed earlier than necessary, which is visible (the WARN) and harmless, where a higher guess would overflow a smaller model, which is the bug.

---

## 5. D4 — The cap at the door

**Why a cap is still needed once the window works mid-turn.** The glass overflows from the oldest end. An item larger than the whole glass cannot be fitted and cannot be split — no amount of pouring helps. The cap is not a second context system; it is the guarantee that nothing indivisible arrives oversized.

**One choke point.** Today there is none: the success path builds the `role: "tool"` message in `loop.go` (`toolResultMsg := providers.Message{Role: "tool", Content: contentForLLM, …}`, two sites) and six further sites build denied-result messages. D4 introduces **one function through which every tool result becomes a context message**, so MCP and builtins are covered by construction and a server connected tomorrow is covered on its first call.

**The numbers** — Claude Code's, in characters (operator decision; `estimateMessageTokens` is an unvalidated 2.5-chars/token heuristic, so a character cap is exact where a token cap is a guess):

| Surface | Cap | Claude Code equivalent |
|---|---|---|
| MCP result | **62,500 chars** | 25,000 tokens |
| Builtin result, success | **30,000 chars** | Bash inline limit |
| Builtin result, failure | **10,000 chars**, head-and-tail | Bash failure path |
| Warn threshold (metric) | **25,000 chars** | 10,000-token warning |
| Operator ceiling | **150,000 chars** | `BASH_MAX_OUTPUT_LENGTH` ceiling |

**Align the shipped per-tool caps to these** rather than layering (operator decision). `read_file`'s independent choice of 64 KB — within 2% of the MCP figure — is corroboration the magnitude is right. **No per-server opt-out.**

**Over-cap behaviour.** The result enters the window **truncated head-and-tail with a marker stating the full size and that the complete result is in the archive** — the same recall mark D5 uses, so the model has one vocabulary for "this was bigger than what you see". The **full result is still appended to the archive** (§6.2). It is not an error.

**Window-independent.** Identical at 131,072 and 1,048,576, so correct even where D1–D3 resolve badly; shippable first.

---

## 6. D5 — Empty in place, leave a recall mark

**When.** The window is over budget (D6), the oldest candidate is a tool result, and its assistant call is still in the window — so advancing `Skip` past it would orphan the call.

**What.** The slot stays; its content becomes a short deterministic mark:

```
[tool result emptied — search_email, 1,178,522 chars, turn 6, tool_call_id=call_978a85… · recall_conversation(tool_call_id=…) returns it in pages]
```

The call/answer structure is untouched, so the request stays valid for every provider — including those that require a window to open with a user message, where a mid-turn *cut* would not. This is the Anthropic `clear_tool_uses` / LangChain `[cleared]` mechanism, with the placeholder pointing at a real retrieval path.

### 6.1 Projection, not mutation

`JSONLBackend.GetHistory` delegates to the store, which reads the window from disk on every call — an in-memory edit is discarded before the next assembly. And the archive is append-only (ADR-028 D14). So emptying is **applied at assembly time**: the set of emptied `tool_call_id`s is persisted in the window's meta file alongside `skip`/`count` (observed shape of `.context/*.meta.json`), and `assembleMessages` substitutes the mark while building the request. Pure function of (window, budget, emptied-set): deterministic, no LLM call, archive untouched, and live and reload agree — a divergence the review flagged as a recurring hazard.

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
- Returns **one page, bounded by the D4 cap for that surface** (62,500 / 30,000 chars); `offset`/`length` page through the rest — the same interface `read_file` already uses (`offset`, `length`, 64 KB cap). The mark states the total size so the model knows whether to page.
- Counts against the recall span budget like every other mode; the span may be dropped alone to fit (FR-019), unchanged.
- The mark carries the `tool_call_id`, so the pointer resolves in a single call.

**What recall does not cover, by design.** A turn that **aborts** is rolled back: `pkg/agent/turn.go::restoreSession` calls `RollbackAppended`, truncating the archive to its turn-start line count and restoring `Skip` *"so mid-turn evictions are undone"*. Verified on the incident: the archive went from 1,317,446 bytes (snapshot 19:19) to 39,004 (21:04) when the turn died. So recall retrieves the results of turns that **completed**; a failed turn's results leave the archive with the rest of its restore point, exactly as today. With D4–D6 the incident's turn would have completed, so this is consistent rather than a hole. **The D5 emptied-set joins the same restore point** — rolled back with `Skip` on abort, so a retried turn starts from an un-emptied window.

(The incident payload survives only in the gateway's browser-facing `tool_results/` store — 826,690 and 1,244,567 bytes — which is gitignored and not model-reachable, and stays that way per §14.1.)

---

## 7. D6 — The window runs mid-turn

Two changes to `windowTrim`'s caller and to its notion of a legal operation. Either alone fails: checking mid-turn with user-only boundaries finds no legal cut; allowing finer operations while checking only at turn start means nobody looks while the turn fills.

**When.** Invoke the budget check **after every tool result is appended** (inside the tool loop, before the next LLM call), in addition to the existing pre-turn site. Cost: the estimator is linear in window size and already runs per turn.

**What, by position.**

| Oldest over-budget content | Operation |
|---|---|
| an earlier, complete turn | advance `Skip` to its end — **today's behaviour, unchanged** |
| a tool result in the current turn | **empty in place** (D5) — never cut, so structure and provider ordering rules hold |
| the most recent tool result | **never** — the floor; the model is reasoning about it |

Mid-turn the window therefore **never cuts, only empties**. `parseTurnBoundaries` stays as it is; the new operation is orthogonal to it.

**Trigger** — operator-settable, shaped per the survey: `trigger = min(absoluteBudget, 0.9 × resolvedWindow)`, with **`absoluteBudget` = 400,000 characters by default** (operator decision 2026-08-22; ≈ 160,000 tokens, about six MCP results at the cap — comfortably inside a 200k model with room for the reply; on a 1M model the 90% term never binds, so this is the working limit); run down to a **target** below the trigger so it does not re-fire immediately; the most recent result is the **floor**. Absolute-primary because D1–D3 can be wrong (§1.1); the ceiling catches a model smaller than the absolute. Denominated in characters for the same reason as D4 — revisit when D8 supplies real `prompt_tokens`.

**Order of operations** for a tool result: ingest bound (D10) → D4 cap → append to archive → D6 budget check → D5 empty oldest as needed → assemble → LLM call.

**Thrash guard.** If the glass is still over budget after every eligible result is emptied — only possible if a non-tool message is itself oversized — stop, log, and surface a typed error (D7) rather than loop. With D4 in place this should be unreachable, and a test asserts it.

---

## 8. D7 — No silent turn exits

The four returns in §1.4 gain a typed code, a log line with the raw cause, and a transcript entry — matching the classified path beside them. `"turn timed out"`/`"turn canceled"` stop resolving to `CodeUnknown`; codes added to `contracts/components/schemas/LLMError.yaml` and regenerated. Remaining turn-fatal conditions: provider auth rejected, provider unreachable after retries, workspace unavailable. **Nothing size-related is turn-fatal** once D4–D6 are in.

## 9. D8 — Learn the window from the provider

`pkg/agent/translate_error.go::contextOverflowSubstrings` already matches `"maximum context length"` and six siblings; today it only produces copy. Feed it back: when a provider states its real limit numerically, cache it as a learned override. Verified by survey: OpenAI (*"maximum context length is 16385 tokens. However, your messages resulted in 16648"*), Anthropic (*"prompt is too long: 208310 tokens > 200000 maximum"*), Google, OpenRouter, DeepSeek, xAI and Moonshot all state the number; **Groq, Z.ai and MiniMax do not**, so for those the catalog remains the only source. The messages are observed in the wild, not documented — match loosely, treat as hints. **Rule, copied from Hermes (`agent/model_metadata.py::get_context_length_from_provider_error`): a learned value may only LOWER the current belief, never raise it** — a misread can make the harness more cautious, never less. The same path collects reported `prompt_tokens` to calibrate the estimator.

## 10. D9 — Controls in Settings and the UI

First-class surfaces per Constraint #8 (schema, generated types, REST, SPA): the per-surface caps from D4 with the 150,000 ceiling; the D6 trigger; and the **effective context window shown read-only with its source** (operator / catalog / learned / floor) plus an override. That number is currently unreachable from the UI and the API, which is half of why the 8× error stayed invisible.

## 11. D10 — Bound what enters memory

D4 protects the window; it cannot protect the process — by the time a result is measured it has been received, held and parsed. Every network or subprocess read is bounded at ingest. `fetch_url` is already correct (`http.MaxBytesReader`, configurable, 10 MB *"Security Fallback"*). Three search providers read unbounded (`BraveSearchProvider.Search`, `DuckDuckGoSearchProvider.Search`, `PerplexitySearchProvider.Search` — `io.ReadAll(resp.Body)` directly after `client.Do`), while two other sites in the same file use `io.LimitReader(…, 1<<20)`. The MCP path has none; the Go SDK's `MaxBytes` is `MemoryEventStore` SSE resumability, not a response cap, and Omnipus never sets it. Exceeding the ingest bound is a tool failure, not a truncation — half a JSON document is not partially useful.

---

## 11a. D11 — Provider identity comes from the registry too

*(Operator decision 2026-08-22.)* models.dev is a **provider** catalog as much as a model catalog. Every one of its 193 providers carries `api` (base URL), `npm` (wire protocol: `@ai-sdk/openai-compatible`, `@ai-sdk/anthropic`, …), `env` (key variable) and `doc`; and **region, plan and protocol variants are separate providers** with their own URL, protocol and model list — `zai` / `zhipuai` (international / China), `zai-coding-plan` / `zhipuai-coding-plan`, `moonshotai` / `moonshotai-cn`, `minimax` / `minimax-cn`, `alibaba` / `alibaba-cn` / `alibaba-coding-plan` / `alibaba-token-plan`, `kimi-for-coding`, and so on — **24 of 193** are such variants (read live 2026-08-22). The coding-plan variants expose a *subset* of models (`zai-coding-plan`: 5 of `zai`'s 14), so plan availability is a catalog lookup as well.

### 11a.1 Where provider identity lives in Omnipus today

**[CORRECTED 2026-08-22]** An earlier version of this section said the factory switch was the only registry. It is not. **`pkg/providers/catalog` already exists** and describes itself as *"the backend-owned single source of truth for the 23 user-facing LLM provider variants available in the Omnipus picker"* — hand-authored `Entries`, one per billable endpoint keyed by **company × plan × region**, with the Anthropic-compatible sibling folded into `AnthropicId` rather than a separate row (FIX-5, `provider-ux-fixes-plan.md`). Entry fields: `id, company, label, plan, wire, endpointHint, subtitle, logoSlug` (+ `anthropicId`). `gen/main.go` emits `data/providers_catalog.json` **and** a generated TypeScript file for the SPA picker from that Go slice (*"Source of truth: pkg/providers/catalog/catalog.go → Entries"*). So the picker is already data-driven — from a hand-typed Go slice of 23. **D11 therefore replaces the data source of an existing catalog; it does not introduce one.** The 23 ids today: `openai anthropic google openrouter groq mistral nvidia cerebras ollama azure z-ai zhipu z-ai-coding zhipu-coding moonshot moonshot-cn minimax minimax-cn deepseek qwen qwen-intl qwen-us coding-plan`.

- Transport dispatch is one `switch` in `pkg/providers/factory_provider.go` (~40 `case` labels) is the only registry; aliases are ad hoc (`"z-ai", "z.ai", "zai"`). **1,241 string literals across 36 distinct ids** in non-test Go (searched), including three spellings of one thing — `qwen-intl`, `qwen-us`, `qwen-international`.
- Wire protocol is encoded as a *suffix on the provider id* (`z-ai-anthropic`, `moonshot-cn-anthropic`, `alibaba-coding-anthropic`), so every provider that offers two protocols exists twice.
- The wire `provider` field is a **free string** (`contracts/components/schemas/Agent.yaml` gives `"openrouter"` as an *example*, not an enum) — so renaming ids needs no contract enum change.
- Credential refs are independent of provider id: `api_key_ref` in `config.json` (`openrouter_API_KEY`, `z-ai-coding_API_KEY`) is the key name, so changing provider ids does not invalidate stored secrets.

### 11a.2 Decision

1. **Canonical provider ids are models.dev's.** `zai`, `zhipuai`, `zai-coding-plan`, `moonshotai`, `moonshotai-cn`, `alibaba`, `alibaba-cn`, `alibaba-coding-plan`, `google`, … One vocabulary shared with OpenCode, Cline, Hermes and Goose; new plans and regions appear without anyone in Omnipus typing anything.
2. **Protocol becomes a field, not a suffix.** The provider table carries `protocol` from the catalog (`npm`); the `-anthropic` ids collapse into `(id, protocol=anthropic)`. Where models.dev records one protocol but the vendor also serves the other (Z.ai, Moonshot, DeepSeek all expose Anthropic-compatible endpoints alongside OpenAI-compatible ones), the override file in the assembly repo adds the second endpoint — the registry is the default, not the ceiling.
3. **The factory switch dispatches on protocol, not on provider name.** ~40 cases become ~5 (`openai-compatible`, `anthropic`, `google`, `ollama`, `cli`); base URL, key variable and defaults come from the table. A provider unknown to the table but with an explicit endpoint is still accepted as `custom` (the existing `rest_onboarding.go` path).
4. **The assembly repo publishes one document** — providers with nested models (§4 D1). **`pkg/providers/catalog` becomes the single catalog package**: `capabilities` folds into it, `Entries` stops being a hand-typed Go slice and is loaded from the feed (embedded snapshot + refreshed copy). `gen/main.go` inverts: it generates the SPA file *from the feed*, not from Go. Providers with no registry entry stay in a local file shipped with the feed: `ollama`, `vllm`, `litellm`, `custom`, `codex-cli`, `shengsuanyun`, `volcengine`, `avian`, `mimo`. (`novita` is `novita-ai` in the registry; listed in the §11a.3 reference table.)

**Aggregators are in the registry as providers in their own right** — `openrouter` (359 models, 60 upstream vendors), `vercel`, `requesty`, `amazon-bedrock`, `nvidia`, `novita-ai`, `kilo`, and ~100 more hosts and gateways; 102 of the 193 providers are aggregators or hosts rather than first-party vendors (read live 2026-08-22). Their models are keyed with the vendor prefix (`openrouter` → `z-ai/glm-5.2`), and their **limits are the aggregator's, not the vendor's** — `z-ai/glm-5.2` is 1,048,576 via `openrouter` and 1,000,000 via `zai`. That is exactly the provider+model key this ADR requires, and why the key cannot be model-only.
5. **Settings lists providers from the table**, grouped by vendor → region → plan, with protocol shown. That is a new read-only wire surface (`GET` providers catalog) and goes through Constraint #8.

### 11a.3 Canonical-id reference — documentation only, not a code artefact

Old Omnipus id → canonical registry id, for an operator hand-editing their own `config.json` or agent entities. **Nothing in the binary reads this table.** Every target was verified present in the live registry on 2026-08-22.

| Old Omnipus id | Canonical | Old Omnipus id | Canonical |
|---|---|---|---|
| `z-ai`, `z.ai` | `zai` | `moonshot`, `moonshot-cn` | `moonshotai`, `moonshotai-cn` |
| `zhipu` | `zhipuai` | `moonshot-anthropic` (+`-cn`) | `moonshotai` (+`-cn`), protocol=anthropic |
| `z-ai-coding`, `glm-coding` | `zai-coding-plan` | `qwen` | `alibaba-cn` |
| `zhipu-coding` | `zhipuai-coding-plan` | `qwen-intl`, `qwen-international`, `dashscope-intl` | `alibaba` |
| `z-ai-anthropic`, `zhipu-anthropic` | `zai` / `zhipuai`, protocol=anthropic | `qwen-us`, `dashscope-us` | `alibaba`, region=us |
| `minimax-anthropic` (+`-cn`) | `minimax` (+`-cn`) — registry already records anthropic protocol | `coding-plan`, `alibaba-coding`, `qwen-coding` | `alibaba-coding-plan` |
| `deepseek-anthropic` | `deepseek`, protocol=anthropic | `…-coding-anthropic` | `alibaba-coding-plan`, protocol=anthropic |
| `gemini`, `anthropic-messages` | `google`, `anthropic` | `novita` | `novita-ai` |

- **No code rewrites existing config or agent entities.** A `provider` value that is not a canonical id (and not `custom` with an endpoint) is an unknown provider and fails on the generic unknown-provider path.
- **The factory's ad-hoc alias strings** (`"z-ai", "z.ai", "zai"`, the three `qwen-*` spellings, the `-anthropic` suffix ids) **are deleted** with the switch collapse (§11a.2 item 3); only canonical ids resolve. No deprecation WARN names an old id.
- Note for operators re-keying their own config: `api_key_ref` is the credential name, not the provider id, so secrets do not need re-entering.
- The fresh-install seed (`pkg/config/defaults.go`, `config/config.example.json`) is written in canonical ids.

### 11a.4 Not adopted

- Keeping Omnipus's own names and maintaining any mapping to the registry in code: two vocabularies for no gain.
- Treating protocol as a suffix in the canonical ids: models.dev does not, and it is what produced the duplicate-provider sprawl.

## 11b. D12 — Provider tiers: declare the two that already exist

*(Operator direction 2026-08-22: review what ships today and what OpenCode and Hermes ship.)*

**Today, undeclared.** The factory switch accepts **36 distinct ids**; **~10** have a display name and a validation probe (`pkg/providers/displayname.go::knownDisplayNames`, `validate.go::probeModelDefaults`: `openrouter openai anthropic google/gemini groq deepseek zhipu/z-ai moonshot azure`); **4** have onboarding key-format hints (`src/lib/constants.ts`: `anthropic openai groq openrouter`). `azure` has a name but no factory case; `xai`, `mistral`, `ollama`, `minimax` have factory cases but no name or probe. The tiers exist; nobody decided them.

**The field** (research 2026-08-22, pinned commits): OpenCode exposes all 193 models.dev providers but pins **6** as "Popular" in its picker, documents 50, bundles code for 23. Hermes ships 37 plugins split into "First-Class API-Key Providers" (~13) and "Other Compatible Providers". Goose: 34 coded + 42 declarative. Crush/Catwalk: 41. Roo: 28, and it retired 9 in one PR as "low-usage". **Typical tier 1 is 5–15; the tail is 40 to unbounded.** Every harness but Gemini CLI offers a custom OpenAI-compatible endpoint.

**Decision (revised 2026-08-22 — operator: follow the OpenCode pattern exactly).** Every provider in the registry is selectable; a small "Popular" set is pinned in the picker; the rest sit behind "show all". No subscription login for Anthropic or Google (D13). **No new SDKs** — validated below.

### 11b.1 Validation: can the existing transports reach all 193?

Omnipus speaks exactly two wire protocols today — **OpenAI-compatible HTTP** (`pkg/providers/http_provider.go`; the `google` case already uses Gemini's OpenAI-compatible endpoint `generativelanguage.googleapis.com/v1beta/openai`) and **Anthropic Messages** (`claude_provider.go`) — plus the CLI/OAuth specials. Checked against every registry provider's declared protocol (`npm`) on 2026-08-22:

| Registry protocol | Providers | Reachable with existing infra? |
|---|---|---|
| `@ai-sdk/openai-compatible` | **154** | **Yes, directly** — base URL, key variable and models all in the registry; **0 of the 154 lack a URL** |
| `@ai-sdk/anthropic` | **9** (minimax, minimax-cn, kimi-for-coding, …) | **Yes** — `claude_provider.go` with the registry's URL |
| `@ai-sdk/openai` | 4 (openai, meta, perplexity-agent, vivgrid) | Yes — same wire as openai-compatible |
| `@ai-sdk/google` | 1 (google) | Yes — via the OpenAI-compatible Gemini endpoint already in use; API key only |
| Dedicated SDKs that are OpenAI-compatible on the wire | ~20 (groq, mistral, xai, deepseek, cerebras, togetherai, deepinfra, perplexity, openrouter, cohere, azure, …) | **Yes, with a base URL from the override file** — the registry records no `api` for them (26 providers lack one; all are dedicated-SDK entries). Omnipus already hardcodes the OpenAI-compatible URLs for groq, mistral, deepseek and cerebras in `factory_provider.go`, which is the proof of shape. |
| **Cloud-IAM auth, not a key** | **~5**: `amazon-bedrock` (SigV4 request signing), `google-vertex`, `google-vertex-anthropic` (GCP service-account OAuth), `watsonx` (IBM IAM), `sap-ai-core` | **No** — these need request-signing or cloud-credential code Omnipus does not have. **Excluded**, listed in the provider table as `unsupported: cloud-iam`, revisitable per provider. |

**Result: 163 providers reachable from the registry alone, ~20 more with a URL row in the override file, ~5 excluded.** No new SDK, no new runtime dependency (Constraint #1 holds). The override file's URL rows are the one piece of hand-maintained data this adds, and it is ~20 lines.

*Caveat:* the "OpenAI-compatible on the wire" claim for the ~20 dedicated-SDK providers is established for the four Omnipus already ships and is my assessment for the rest from their public API docs; each is confirmed by the tier-1 probe at the time its URL row is added.

### 11b.2 The tiers

- **Popular (pinned, ~6–8):** the OpenCode shape — `openai`, `openrouter`, `anthropic` (API key), `google` (API key), `xai`, `groq`, `mistral`, `deepseek`. Named, probed, guided, tested.
- **Everything else (~155):** selectable behind "show all providers", reachable through D11's protocol dispatch with URL, key variable and limits from the table. Best-effort; no probe, no onboarding hint, no test matrix.
- **Unsupported (~5):** the cloud-IAM set above, shown with the reason.
- **Custom endpoint stays** (any OpenAI- or Anthropic-compatible URL).
- A config naming a provider that is not in the table fails on the generic unknown-provider path (`rest_onboarding.go`). **There is no retired-provider list.**

## 11c. D13 — Subscription login: only where the vendor permits it, verified from the vendor's own terms

*(Operator direction 2026-08-22: "only where the vendor does not forbid it". The operator's recollection — Anthropic and Google forbid, others tolerate — was checked against each vendor's own published terms on 2026-08-22 and is confirmed, with one material consequence for code Omnipus ships today.)*

### 11c.1 Vendor by vendor — primary sources

| Vendor | Borrowing the subscription token in a third-party tool | Driving the vendor's own CLI as a subprocess | Source |
|---|---|---|---|
| **Anthropic** | **Prohibited.** *"Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, or Max plan credentials on behalf of their users. Moreover, developers may not collect, store, or intermediate Claude.ai credentials or session tokens."* Since 2026-04-04 a Claude login in a third-party tool no longer draws on subscription limits at all (Boris Cherny, Head of Claude Code). | **Permitted only if** the `claude` binary is unmodified and the end user signs in inside it themselves: *"Nor does it prevent an end user from signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code."* Whether a harness-driven subprocess is "ordinary use" after 2026-04-04 is **unclear**. | code.claude.com/docs/en/legal-and-compliance; anthropic.com/legal/consumer-terms §3(7) |
| **Google** | **Prohibited, explicitly, naming the practice.** Antigravity Additional Terms §6: *"Using third party software, tools, or services to access the Service (e.g. using OpenClaw with Antigravity OAuth) is a breach of this Agreement"* and *"may be grounds for suspension or termination of your account."* Gemini CLI ToS: *"Directly accessing the services powering Gemini CLI … using third-party software … is a violation."* **Enforced:** Antigravity accounts of OpenClaw-OAuth users suspended, Feb 2026; Google staff cite §6 on the official forum. | Not addressed by the text — **unclear**. | antigravity.google/terms §6; geminicli.com/docs/resources/tos-privacy; discuss.ai.google.dev thread 126426 |
| **OpenAI** | **Permitted in practice, not in text.** Sam Altman, 2026-05-01: *"you can sign in to openclaw with your chatgpt account now and use your subscription there!"* No enforcement found. The ToS still prohibits *"Automatically or programmatically extract data or Output"* with no carve-out. | Fits the Help Center's supported-client list (Codex CLI / app-server). Lowest risk. | help.openai.com "Using Codex with your ChatGPT plan"; openai.com/policies/terms-of-use; x.com/sama/status/2050357911915028689 |
| **xAI** | **Permitted and vendor-sanctioned** for named agents: xAI published first-party OAuth integrations for Hermes (2026-05-18), OpenClaw (05-19), OpenCode (05-21), Kilo (05-29), Warp (06-15) — *"Use your SuperGrok or X Premium subscription inside OpenCode … More open-source agents and integrations are coming soon."* The AUP still bans unauthorised bots, so an unlisted harness is **medium** confidence. | Not addressed. | x.ai/news/grok-opencode, grok-openclaw, grok-hermes; x.ai/legal/acceptable-use-policy |
| **GitHub Copilot** | Raw token to `api.githubcopilot.com`: **not prohibited, not sanctioned** — unclear. | **Permitted via the official Copilot SDK / CLI**, billed to the subscription: *"A GitHub Copilot subscription is required to use the GitHub Copilot SDK … each prompt being counted towards your usage allowance."* | github.com/github/copilot-sdk; GitHub changelog 2026-06-02 |
| Kilo | Permitted (Gateway API offered for third-party apps). | — | kilo.ai/terms |
| Mistral (consumer) | Prohibited without written authorisation; the API is a separate business product. | — | legal.mistral.ai EU consumer terms |
| Cursor, Windsurf | No sanctioned consumer-credential path — skip. | — | — |

### 11c.2 What Omnipus ships today, against that table

Verified in `pkg/providers` on this branch:

- **`claude-cli`** — `exec.CommandContext(ctx, "claude", …)`; the file handles no token, credential or keychain (searched). This is the shape Anthropic permits — but it is a *subscription* path, and the operator descoped all Anthropic subscription paths (§11c.3 item 2). **Descoped.**
- **`codex-cli`** — despite the name, **not** a subprocess: `factory_provider.go` case `"codex-cli"` → `NewCodexProviderWithTokenSource(CreateCodexCliTokenSource())`, which `ReadCodexCliCredentials` from the Codex CLI's `auth.json` and calls `https://chatgpt.com/backend-api/codex` directly. That is token reuse. OpenAI tolerates and publicly encourages it, but the ToS text does not. **Keep, documented as resting on practice; prefer the `codex_cli_provider.go` subprocess path where both exist.**
- **`antigravity`** — Google OAuth (`auth.GoogleAntigravityOAuthConfig`, `RefreshAccessToken`) against the Antigravity backend. **Deleted outright, greenfield — §11c.4.** **This is the practice Google's §6 names and suspends accounts for — and it is the seeded default model on a fresh install (`pkg/config/defaults.go` → `antigravity/gemini-3-flash`).** Hermes removed the equivalent (PR #50492: *"Google now actively bans accounts … a ban can extend to the entire Google account"*); Goose deprecated it.

**Decision: delete the `antigravity` OAuth provider entirely (§11c.4), and change the fresh-install default model to a Popular-tier API-key provider.** Google's sanctioned route for third-party tools is the Gemini API or Vertex key, which stays. This is the one finding in this ADR that bears on the running release rather than the design, and it is flagged as such in §13.

### 11c.3 The policy — as decided

*(Operator decisions 2026-08-22.)*

1. **API keys stay for every vendor, Anthropic and Google included.** That is the route both vendors name as the sanctioned one for third-party tools (`anthropic` via the Console key; `google` via the Gemini API key through the OpenAI-compatible endpoint already in use).
2. **Every Anthropic and Google *subscription* path is descoped.** Google: the `antigravity` OAuth provider — deleted, §11c.4. Anthropic: no OAuth path ever existed; **`claude-cli` is descoped with the rest** — it exists to use a Claude subscription through the official binary, and since 2026-04-04 that login no longer draws on the subscription for third-party tools, so its reason to exist is gone. (It can return later as a plain "drive the vendor CLI" integration if there is a non-subscription case for it; that would be a new decision.)
3. **Subscription login is offered only where the vendor's own terms or an official vendor statement permit it**, cited in §11c.1: **GitHub Copilot** via the official SDK/CLI; **xAI** via the published OAuth flow (ask xAI to list Omnipus, as the five named agents are); **OpenAI** via ChatGPT login, documented as practice-based.
4. **Never collect, store, proxy or refresh a vendor's consumer credential where the vendor prohibits it.**
5. **Prefer driving the vendor's own CLI as a subprocess over borrowing its token** wherever both exist.
6. The table in §11c.1 is re-verified each release; a vendor that changes position moves tier, or is removed outright the way §11c.4 removes `antigravity`.

### 11c.4 `antigravity` — deleted, no trace, no backward compatibility

**Greenfield removal (operator direction 2026-08-22).** Inventory on this branch: 33 files reference it. Everything below is removed in one commit. **No code deals with antigravity afterwards in any form** — no alias, no shim, no migration, no retired-list row, no boot notification, no error string that names it. After the commit the word does not occur in `pkg/`, `cmd/`, `src/`, `contracts/`, `config/`, or `docs/` outside historical decision records.

| Area | What goes |
|---|---|
| **Provider code** | `pkg/providers/antigravity_provider.go` (105 refs) + `_test.go`; the `case "antigravity"` in `factory_provider.go` and its test rows; `AntigravityModelInfo`, `FetchAntigravityModels` |
| **OAuth config** | `pkg/auth/oauth.go::GoogleAntigravityOAuthConfig` and the `OMNIPUS_GOOGLE_CLIENT_ID` / `OMNIPUS_GOOGLE_CLIENT_SECRET` env reads. **The file stays** — `OpenAIOAuthConfig`, `RequestDeviceCode`, `RefreshAccessToken` are used by `codex_provider.go` (verified). |
| **Default model** | `pkg/config/defaults.go` → `antigravity/gemini-3-flash` replaced by a Popular-tier API-key model; `config.go` protocol comment; `config/config.example.json` |
| **Wire contract (Constraint #8)** | the `antigravity` enum value in `contracts/components/schemas/ProbeProviderRequest.yaml` and its inbound copy `pkg/gateway/inboundschemas/ProbeProviderRequest.yaml`; regenerate `pkg/api/generated/openapi_types.gen.go`, `src/lib/api/generated/openapi-types.ts`, `schemas.ts`; commit spec + generated artifacts atomically |
| **Catalog allow-list** | `pkg/providers/catalog/catalog_test.go` "CLI executor / non-API-key ids" entry |
| **Docs** | `docs/ANTIGRAVITY_USAGE.md` deleted; mentions removed from `docs/providers.md`, `docs/configuration.md`, `docs/README.md`, `docs/migration/model-list-migration.md`, `docs/internal/provider-endpoint-audit-2026-06.md`, `docs/internal/design/provider-refactoring*.md` |
| **Kept deliberately** | historical decision records that mention it as history (ADR-031 and its review, ADR-059 spec reviews, the cli-minimization and workspace-rename specs, the turn-truncation root-cause note, this ADR and its review). Rewriting a past decision's text to erase a name is falsifying the record, not removing a trace. |

**Backward compatibility: none, and nothing antigravity-specific to provide it.** A `config.json` or agent entity that still names `antigravity` is simply an unknown provider id and takes the generic unknown-provider path that already exists (`rest_onboarding.go`: *"unknown provider %q and no endpoint override supplied"*). That path is not touched and never mentions antigravity.

**Exit proof:** `grep -ri antigravity pkg cmd src contracts config docs` returns only the historical decision records listed above; `make verify-contracts` passes after regeneration; `go build` and `npm run typecheck` pass with the files gone.

### 11c.5 Not adopted

- **"Support everything that technically works, user's risk."** Google's remedy is account termination that can extend to the whole Google account; Omnipus would be the tool that caused it.
- **"API keys only."** Removes three shipped paths, two of which are sanctioned or tolerated.

## 12. Contract impact (Constraint #8)

- One catalog file, `schema_version` 2.0.0 (providers with nested models); the binary reads 2.0.0 only — a persisted catalog at any other version is ignored in favour of the embedded snapshot (the same path as a signature failure).
- The emptied-set in window meta is an internal file, not a wire type.
- D5's recall mark reaches the SPA inside a tool-result message. **Operator decision 2026-08-22: it is rendered in the chat thread only when Verbose chat is on** (`src/lib/toolVisibility.ts`, Settings → Chat); otherwise it stays in the transcript and the ActivityPanel like other infra-only output. It must not be hand-rolled with `fmt.Sprintf` (ADR-060's `%q` finding). Whether it formally joins the ADR-060 family or is typed beside it is left to the implementing commit — the enforcement (schema, no string assembly) is what is decided here, not the family's name.
- D7's `LLMError` codes and D9's settings schema require `make gen-contracts`.
- D11's read-only providers-catalog endpoint (Settings picker) is a new wire surface: schema in `contracts/components/schemas/`, generated types, SPA consumes the generated type only. The `provider` field itself stays a free string — no enum — because the provider set is data (registry table + `custom`), not a compiled enum.

## 13. Consequences

**Positive.** No single result exceeds the budget (D4); no turn exceeds the window regardless of length (D5+D6); the window record is right (D1–D3) and visible (D9); failures are diagnosable (D7). Nothing is summarised or deleted; every result of a completed turn remains recallable, in pages. No new storage, no new retention policy, no new file surface. Per-turn token cost falls by the quadratic argument. The change is three focused edits to existing mechanisms.

**Negative / accepted.** The seed acquires two fields to maintain. A stale seed under-reports a new model until refreshed (mitigated by D8). The conservative floor over-trims for unseeded models — visible, harmless. Emptied results cost a recall round-trip when the model does need them. The per-result budget check adds linear work per tool call.

**Bears on the running release, not only the design (D13):** the shipped `antigravity` OAuth provider is the practice Google's Antigravity terms §6 name and enforce with account suspension, and it is the fresh-install default model. Removal and a new default precede shipping this branch.

**All four diagnosed defects have a decision:** §1.1 → D1–D3, §1.2 → D4 (+D10), §1.3 → D5+D6, §1.4 → D7.

## 14. Retired from the earlier draft — and why

1. **Spill-to-disk handles in the agent `work/` tree.** The content is already on disk in the archive and already reachable by `recall_conversation`; a second copy meant two stores, two lifecycles, a new retention rule and a new file surface to duplicate what exists. (The draft's *security* objection to spill was itself inverted — `agents/*/work/` is gitignored at `.gitignore:39`; `sessions/` and `.context/` are committed by design — but the simpler reason above is sufficient on its own.)
2. **A four-shape structural reducer and schema-derived refetch recipes.** Only ~9 of 89 builtins accept a narrowing parameter (audit, §15.1), so refetch was a dead end in the common case; and a complete index is unnecessary when the full content is one recall away.
3. **A second per-turn budget beside the sliding window.** Two systems disagreeing about the budget. Replaced by running the one window mid-turn (D6).
4. **Clamping old tool results to ~2,000 chars (Cline-style).** Lossy compression by another name — operator direction: no summarisation — and unworkable as written because `GetHistory` re-reads from disk.
5. **Relocating results at assembly time.** Subsumed: D5's emptying *is* assembly-time substitution, now with a recall mark instead of a path.
6. **Exempting `antigravity`.** Moot — it is deleted, §11c.4.
7. Also rejected, unchanged from the first draft: raising `max_tokens` to fix the window; setting `context_window` once and stopping; OpenRouter `middle-out`; waiting for MCP; a dedicated window registry; boot-time catalog fetch; mid-turn **cutting** (breaks provider ordering rules; emptying does not); iteration count as a context control (`max_tool_iterations` stays a runaway guard — it resolves per-agent → defaults → hardcoded **200**, so a fresh install is four times more exposed than the operator's 50).

## 15. Implementation tasks

1. Tier-1 gaps from the audit: add a bounding parameter to `list_directory` (`path` only), `inspect_session` (`session_id`, `tool_name`, `role`), `recall_conversation` (`query`, `turn_range`, `time`). With D4 at the door these are hygiene rather than blockers.
2. `recall_conversation`: the `tool_call_id` mode with `offset`/`length` paging (§6.3); the emptied-set added to the turn restore point.
3. The assembly repository and its daily job (D1), publishing the single providers-with-models document (D1, D11); the Omnipus-side puller retarget, 24 h + startup refresh, build-time snapshot generation, and release-signature verification.
3a. D11: `pkg/providers/capabilities` folded into `pkg/providers/catalog`; `catalog.Entries` loaded from the feed; factory switch collapsed to protocol dispatch; all ad-hoc alias strings and `-anthropic` suffix ids deleted; SPA key-format hint map re-keyed by canonical id; fresh-install seed written in canonical ids; `resolveStrippedPrefix` removed (§4 D1).
4. Bound the three search providers' reads (D10).
5. Confirm the provider ordering rule in §16 before allowing any mid-turn cut in future.

### 15.1 Tier-1 audit (2026-08-22, this branch)

Catalog from the per-agent policy map (Constraint #6 requires it to enumerate every static builtin): **89 tools, 39 capable of bulk output.** Already bounded: `read_file` (`offset`/`length`, 64 KB cap), `library_read`, `read_inbox`, `search_email`, `search_web`, `fetch_url`, `list_jobs`, `recall_memory`, `find_skills`. Gaps: the three in task 1, read per-receiver. Compliant rows for tools sharing a source file were read at file level — spot-check before relying on an individual row.

## 16. Unverified

- **[UNVERIFIED]** That the Anthropic Messages API rejects a window whose first message is not `user` (the reason D6 empties rather than cuts mid-turn). Not found in `pkg/providers/claude_provider.go` (searched); design is safe either way, since emptying is valid for every provider.
- **[UNVERIFIED]** OpenAI / Anthropic native model-list endpoints publishing context length (needs the operator's keys; immaterial to D1).
- **[UNVERIFIED]** Ollama's `/api/show` context field (daemon not running; needed only for a future local-query rung).

## 17. Exit proof

1. **Guard test** — feed a ~2 MB tool result through the loop: the assembled request stays under the window, the model sees a marked result and continues, **the turn completes with no user-facing error**.
2. **Long-turn test** — a turn of 50 tool calls each at the cap, against a small window: the request never exceeds the window at any iteration; the most recent result is always intact; emptied results carry marks; `recall_conversation(tool_call_id=…)` returns any emptied result, paged under the D4 cap, and paging reaches its last byte.
   2b. **Rollback test** — a turn that aborts after emptying restores both `Skip` and the emptied-set to their turn-start values.
3. **Window-agreement test** — the catalog and `windowTrim` resolve the same window for a given model (the three-way disagreement that exists today).
4. **Thrash-guard test** — an oversized non-tool message produces a typed error, not a loop.
5. **Silent-exit test** — each of the four §1.4 returns now produces a log line, an event and a transcript entry.
6. **Greenfield test** — `grep -rnE '_migrated|alias|deprecat|retired' pkg/providers pkg/config` returns nothing provider-related; a config with `provider: "z-ai"` or `"moonshot-cn-anthropic"` fails as unknown-provider with no rename and no WARN naming a canonical id; `grep -ri antigravity pkg cmd src contracts config docs` returns only historical decision records.
