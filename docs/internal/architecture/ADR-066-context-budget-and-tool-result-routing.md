# ADR-066: Context budget and tool-result routing — provider-sourced windows, source-bound tools, and spill-with-index recovery

- **Status:** Proposed (2026-08-21). Drafted from a live production incident on the operator's own instance; awaiting operator ratification before implementation.
- **Date:** 2026-08-21
- **Related:** [ADR-028](ADR-028-context-paging-sliding-window-recall.md) (`windowTrim` as the only compaction path — **extended, not superseded**, see D9); [ADR-051](ADR-051-media-handling-and-provider-error-translation.md) (`LLMError` classifier and the write choke point — extended by D10; its `offloadSink{workDir: wsDir}` media pattern is the precedent D5 **follows**, see §7.2); [ADR-060](ADR-060-structured-tool-failure-family.md) (structured tool-failure family — D5's payload is a candidate member, see §16); CLAUDE.md **Constraint #1** (single binary, no new runtime deps), **Constraint #6** (explicit tool policy, no defaults), **Constraint #8** (contract-first wire formats).
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1 for everything cited as read; 3 for the items marked **[UNVERIFIED]** in §21. Code claims were read in-session on the running build tree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/build-v0.1.1` @ `6acd378` — the tree the failing binary was built from, not `main`. Cited as `file::symbol` per CLAUDE.md, except where a line number is the claim itself. Claims that are *absences* are cited as searches.

> **Scope note.** This ADR decides **how the harness bounds what it sends to a model, and what happens when a bound is crossed**. It does not change tool policy, delegation, or the classifier's code vocabulary beyond adding entries. It reverses no prior decision except the context-window heuristic in `pkg/agent/instance.go`, which was never ratified in an ADR.

---

## 1. Context — the incident

On 2026-08-21 a turn in session `session_01M0HYPY0QMX10ZBP8C0C6FTXD` (agent `Jarvis-Chief-of-Staff`, `c019b0ce-…`, model `z-ai/glm-5.2` via OpenRouter) ended with the user-facing copy *"This turn didn't finish, and we can't tell why."*

The user had asked the agent to check two inboxes. Three tool calls ran and **all three succeeded**. The live LLM window on disk ends there:

| # | role | size |
|---|---|---|
| 30 | assistant | 38 chars + 3 tool calls |
| 31 | tool — `mcp_composio_work_gmail_fetch_emails` | **1,178,522 chars** |
| 32 | tool — `mcp_composio_personal_gmail_fetch_emails` | **816,912 chars** |
| 33 | tool — `read_inbox` | 1,774 chars |
| — | *(no assistant reply — file ends)* | |

Two MCP results, ~2.0 MB combined, of raw HTML email bodies. By the codebase's own estimator (`estimateMessageTokens`, `chars × 2/5`) the assembled request was ≈ 850,000 tokens.

Four independent defects combined to produce that outcome.

### 1.1 The context window was wrong by 8×

`agents.defaults.context_window` is absent from the operator's `config.json`, absent from the agent entity, **and never seeded** (searched: `pkg/config/defaults.go`, `pkg/coreagent/core.go`). It is also not settable from the UI or API (searched: `contracts/`, `src/lib/api.ts` — no `context_window` field). So `pkg/agent/instance.go:249` applied its heuristic:

```go
contextWindow := defaults.ContextWindow
if contextWindow == 0 {
    contextWindow = maxTokens * 4   // 32768 * 4 = 131_072
}
```

OpenRouter's public catalog reports `z-ai/glm-5.2` at `context_length=1048576`, `max_completion_tokens=131072`. **The harness assumed one eighth of the real window.** The proactive trims logged at 17:48:40 and 17:49:34 were therefore evicting live history from an agent that had eight times the headroom it believed it had.

`max_tokens` is an *output* limit. Deriving an *input* limit from it is a category error, and the drift is unbounded: at `max_tokens: 8192` the same code would have assumed 32k on a 1M model.

### 1.2 Tool results enter context uncapped

**[CORRECTED 2026-08-22]** An earlier draft claimed the only cap in `pkg/tools` was bash's 1 MB session buffer. That was wrong. Model-facing caps already ship:

| Cap | Location |
|---|---|
| shell output 10,000 chars | `pkg/tools/shell.go:1294` |
| `fetch_url` 50,000 chars | `pkg/tools/web.go:34` (`defaultMaxChars`) |
| `browser_get_text` 100 KiB | `pkg/tools/browser/tools.go:20` |
| bash session buffer 1 MB | `pkg/tools/session.go:16` |

What is absent is a cap on the **MCP path**: `pkg/mcp/manager.go` contains no truncation of any kind (searched). A 1.18 MB single MCP result is the proof. The gap is third-party tools, not the builtin surface.

`pkg/gateway/tool_result_store.go` already offloads any result over `InlineToolResultMaxBytes = 50 KiB` to disk and hands the SPA a 4 KiB preview plus a reference. **That machinery points at the browser.** D5 does not extend it — the gateway store keeps its own location and lifecycle; the model-facing spill goes to the agent's gitignored `work/` tree instead (§7.2).

### 1.3 The budget is checked once per turn, never after tool results

`isOverContextBudget` has exactly two call sites (searched, `pkg/agent/*.go` excluding tests):

- `pkg/agent/loop.go:8059` — once, before the turn's first LLM call, **outside** `turnLoop`.
- `pkg/agent/loop.go:9016` — inside the timeout-recovery branch only, gated on `isTimeoutError`.

Neither runs after a tool result is appended. And `windowTrim` evicts **whole earlier turns**; when the oversized item is the *current* turn's own tool result, there is nothing eligible to evict. No recovery path exists for this shape of overflow.

### 1.4 The failure left no trace

`runTurn` has four returns that emit no event, write no log line, and append no transcript entry:

```
pkg/agent/loop.go:9219  return turnResult{}, fmt.Errorf("turn canceled")
pkg/agent/loop.go:9222  return turnResult{}, fmt.Errorf("turn timed out")
pkg/agent/loop.go:9438  return turnResult{}, fmt.Errorf("turn canceled")
pkg/agent/loop.go:9441  return turnResult{}, fmt.Errorf("turn timed out")
```

The classified path 25 lines below (`loop.go:9245`) logs `"LLM call failed"` *and* calls `ts.appendClassifiedError`. No such log exists in `gateway.log`, and no error entry exists in the transcript — which is how we know the turn exited through one of the four silent returns rather than through a classified provider error.

Those strings then reach `TranslateTurnError` → `classifyByMessage`, match no pinned substring, and fall to `CodeUnknown`, whose catalogue copy is verbatim the sentence the user saw.

**Consequence: the one fact that would distinguish "the request overflowed" from "the request timed out" was discarded by the code. This ADR cannot state which occurred, and that is itself the finding.**

---

## 2. Industry position

Every production harness surveyed caps per-result output. Verified figures:

| Harness | Per-result cap |
|---|---|
| Claude Code (MCP) | warn 10k tokens, cap **25k**, then persist to disk + file reference |
| Claude Code (Bash, success) | ~30k chars inline, then file path + preview |
| Cline | **48,000 chars** (command / read / search alike) |
| Gemini CLI | 40,000 |
| LangChain Deep Agents | 20,000 tokens → `/large_tool_results/`, greppable |
| Roo Code (terminal → model) | **10 KB**, spill to disk, `read_command_output` |
| Codex CLI | 10,000 |
| Copilot CLI | 20 KiB → temp file + preview |
| **Omnipus (builtins)** | shell 10,000 chars · `fetch_url` 50,000 · `browser_get_text` 100 KiB |
| **Omnipus (MCP path)** | **none** |

Two findings shape the decisions below.

**The MCP specification defines no result size limit, in any revision** (2025-06-18, 2025-11-25, 2026-07-28). `CallToolResult` carries no byte, token, or character cap. Pagination is specified for `resources/list`, `prompts/list`, `tools/list` — **`tools/call` has no cursor**. Two proposals to change this are open and unadopted. Servers split accordingly: `google_workspace_mcp` truncates bodies at 20,000 chars and offers page tokens; `GongRzhe/Gmail-MCP-Server` returns entire bodies with no page token. Composio behaved like the latter. **The cap therefore cannot be delegated to server authors, now or later.**

**Cline's source states the cost argument**, and it is the strongest reason to cap tightly rather than raise the limit: a tool result is re-sent on every subsequent request in the run, so an oversized result costs **quadratically** across the remaining turn, not linearly. The 2.0 MB above would have been re-paid on every iteration.

The consensus shape is not truncation. It is **elision with the next action named in the surviving text** — Codex, Cline, Roo, Claude Code and Cursor converged on it independently.

---

## 3. D1 — Extend `pkg/providers/capabilities`; do not build a second catalog

The capability catalog needed for context windows **already exists**, built for media modality:

- `pkg/providers/capabilities/embed.go` — `//go:embed data/providers_capabilities_seed.json`, documented as "the guaranteed last-resort source-of-truth" when no store is configured and the network is unreachable.
- The seed carries **78 models across 9 providers**: openai 14, anthropic 14, z-ai 11, mistral 10, moonshot 9, google 8, minimax 5, xai 4, deepseek 3.
- `puller.go` — `GHReleasePuller`, GitHub Release with raw fallback, checksum-verified (`ErrChecksumMismatch`).
- `version.go` — semver-aware `Version.Compare`, so `Catalog.Refresh` cannot downgrade.
- `catalog.go::Store` — persistence; `catalog.go::seedFile.validate` — permissive DTO → invariant-bearing domain type.
- `catalog.go::Catalog.Resolve` → `catalog.go::resolveStrippedPrefix` — **id normalisation is already solved**: `z-ai/glm-5.2` resolves to the seed's `glm-5.2`, which is present.

The per-model wire keys today are exactly four: `id`, `provider`, `input_modalities`, `resize_budget`. A search for any `context`, `window`, or `token` key across the seed returns **none**.

**Decision.** Add `context_window` and `max_output_tokens` to `modelDTO` and `Model`, bump `schema_version` (1.0.0 → 1.1.0), populate all 78 entries, and expose `resolvedModel.ContextWindow()` alongside the existing `Supports()` and `Budget()`.

**Rationale.** This inherits embedding, signed refresh, version-regression protection, persistence and degraded-transport reporting for free; it satisfies Constraint #1 with no new runtime dependency and no boot-time network requirement; and because the seed is provider-keyed, the fix reaches all nine vendors at once rather than only OpenRouter-routed models.

**Generation source.** The OpenRouter public models endpoint (`GET https://openrouter.ai/api/v1/models`, unauthenticated) returned 420 models covering every vendor in the seed — openai 94, qwen 51, google 41, anthropic 28, mistralai 19, z-ai 15, deepseek 14, x-ai 6. It publishes `context_length` for models regardless of whether Omnipus routes through OpenRouter, so it can seed direct-to-OpenAI and direct-to-Anthropic entries too. Seed generation is a **build-time script** in the `scripts/gen-*` family whose output is committed, never a runtime fetch.

**Caveat to encode.** OpenRouter's `context_length` and `top_provider.context_length` can differ (observed: `z-ai/glm-5` 204,800 vs 198,000). Treat catalogue values as accurate-not-exact and reserve a margin rather than sizing to the published number exactly.

## 4. D2 — Resolution ladder for the effective window

In order, first hit wins:

1. **Operator override** — set in Settings (D12), not only in a config file.
2. **Catalog** (D1).
3. **Learned override** (D11).
4. **Conservative floor, with a WARN naming the model and provider.**

`maxTokens * 4` at `pkg/agent/instance.go:249` is **retired**. The two divergent flat `128000` fallbacks at `pkg/agent/loop.go:11306` (`windowTrim`) and `pkg/agent/loop.go:11611` (model switch) are consolidated onto the same ladder — three code paths currently answer "what window do we assume" with three different numbers.

**CLI-backed providers (`claude-cli`, `codex-cli`) are exempt from budgeting entirely.** **[CORRECTED 2026-08-22]** An earlier draft also exempted `antigravity`. That was wrong: it is a plain HTTPS provider (`antigravityBaseURL = "https://cloudcode-pa.googleapis.com"`, `http.NewRequestWithContext`, no `exec.Command`) **and** the seeded default model (`pkg/config/defaults.go:200` → `antigravity/gemini-3-flash`). Exempting it would have stripped budgeting from the default provider on every fresh install. Those harnesses manage their own context; imposing a second, guessed budget on top causes the needless trimming of §1.1 without preventing anything.

## 5. D3 — The unknown-window default is conservative and loud

`catalog.go::Catalog.optimistic` returns an *optimistic* default for unknown models (FR-026): unknown ⇒ assume image support. That is correct for modality — optimism costs a rejected request and a retry.

**For a context window, optimism costs a dead turn.** D3 therefore departs deliberately: an unknown window resolves to a conservative floor and emits a warning naming the model. The two policies now disagree by design, and this paragraph is the record of why.

## 6. D4 — Two-tier bounding

**Tier 1 — bound at the source, for tools Omnipus owns.** A builtin must never *produce* an oversized result. It returns a bounded page and says so. Reducing a 400 KB file read after performing it is wasted work; refusing to load it is cheaper and more honest. This is Claude Code's model for `Read` (first page + `PARTIAL view` notice; an explicit over-limit range errors *before* loading) and `Grep` (100 files, `head_limit`/`offset`), and the MCP reference `fetch` server's (`max_length` + `start_index`).

**Tier 2 — reduce at the choke point, for everything else.** One place where a tool result becomes a context message, so MCP servers and any builtin that slips through are covered by construction rather than per-tool. A server connected tomorrow is covered on its first call, with no registration and no code.

After the Tier-1 audit (§19) Tier 2 should fire almost exclusively on third-party MCP servers — precisely where no other control exists, and precisely where this incident originated.

### 6.1 The caps

Characters, not lines: characters track token cost, and line-based caps are being retired elsewhere in the field (Roo's much-quoted 500-line limit is commented in-source as a display limit, "not LLM context limits").

| Surface | Cap | Claude Code equivalent |
|---|---|---|
| MCP tool result | **62,500 chars** | `MAX_MCP_OUTPUT_TOKENS` = 25,000 tokens |
| Builtin tool result, success | **30,000 chars** | Bash inline limit |
| Builtin tool result, failure | **10,000 chars**, head-and-tail, no refetch offer | Bash failure path |
| Warn threshold (metric only) | **25,000 chars** | 10,000-token MCP warning |
| Operator override ceiling | **150,000 chars** | `BASH_MAX_OUTPUT_LENGTH` ceiling |

**Tokens vs characters.** Claude Code caps MCP in tokens. Omnipus caps in **characters** at the token-equivalent, because `estimateMessageTokens` is `chars × 2/5` — an unvalidated heuristic whose error on HTML is unknown, and HTML is exactly the payload that caused the incident. A character cap is exact even where the token label is approximate. Revisit once D11 supplies real `prompt_tokens` to calibrate against; nothing here blocks that move.

**The failure path is deliberately tighter and offers no refetch.** On a failure the tail *is* the error; a refetch instruction only delays it.

**The warn threshold is the fleet-wide early warning.** A metric saying "this Composio tool routinely returns 200k chars" would have surfaced weeks before the incident. It changes no behaviour.

**No per-server opt-out.** Claude Code offers servers `_meta["anthropic/maxResultSizeChars"]` up to 500,000. Omnipus does not. A server-controlled lever to raise the cap is a lever to recreate the incident, and §2 shows how servers behave when left to self-limit. One cap, no negotiation.

**Reconciling the caps that already ship.** Operator decision, 2026-08-22: **align the existing per-tool caps to the numbers in this table** rather than leaving three tuned-by-hand values in place. shell's 10,000 and `fetch_url`'s 50,000 become the builtin success cap of 30,000; `browser_get_text`'s 100 KiB likewise. One number per surface, explainable in a sentence, with the per-tool values retired rather than layered.

**The cap is window-independent** — identical behaviour at 131,072 and 1,048,576 — so D4 remains correct even where D1–D3 resolve badly, and is shippable before the catalog work completes.

## 7. D5 — The truncated-result contract: complete index, spilled payload, optional refetch hint

An over-cap result is **not an error**. It is a different shape of success, delivered in-band to the model:

```
status:  truncated — 10 messages, 1,178,522 chars exceeds the 62,500 char limit
index:   1  Ingrid <…>        "Owner action — flip p1 to done"  2026-08-12   4 KB
         2  Sven Möhler <…>   "Re: intro"                       2026-08-11   6 KB
         …  all 10 records listed …
handle:  <agent work dir>/tool_results/<ref>/result.jsonl
next:    sed -n '2p' <handle>   |   grep -n "Sven" <handle>
         (this tool also accepts message_id= for a single body)
```

Four mandatory properties:

1. **The index is complete**, listing every record — not a byte-prefix of the payload. See §7.1.
2. **The next action is named**, concretely, with the parameter that would work (D7).
3. **The shape is stated** — record count, total size — so the model can judge whether it needs the bulk at all.
4. **It is not flagged as a failure.** A model that receives an error-shaped result tends to apologise and stop rather than continue.

### 7.1 The preview is an index, not the first N bytes

Claude Code shows a head preview because for a shell transcript the head is meaningful. For a collection it is not: the first 4 KB of a marketing email is a tracking URL and a CSS reset. **Paginate the metadata, never the payload.** In the incident, a complete index of ten subject lines *is* the answer to the question the user asked, and no refetch ever occurs.

Completeness matters more than size. An agent that cannot tell what was cut re-calls with a larger limit "just in case" — the exact thrash D8 guards against. A complete index removes the uncertainty and the agent stops fetching.

### 7.2 Spill to the agent work dir; refetch is the bonus, not the mechanism

**[REVERSED 2026-08-22 — operator decision, after the security argument behind the previous version was shown to be inverted.]**

An earlier draft rejected spill-to-disk, ranking *security* first: it claimed the workspace `work/` dir is git-tracked and self-committing. **That was wrong, and it was wrong because the check was run against the parent directories rather than the actual target.** Re-run precisely:

```
agents/                                   TRACKED
agents/<id>/work/                         IGNORED  (.gitignore:39  agents/*/work/)
workspaces/<id>/work/                     IGNORED  (.gitignore:38  workspaces/*/work/)
sessions/                                 TRACKED
agents/<id>/sessions/.context/            TRACKED
```

`.gitignore` carries a 12-line comment explaining that `work/` is deliberately the agent scratch tree and deliberately excluded. **The spill target was the safe location all along.** The tracked location is `sessions/` and `.context/` — where the payload already lands today, by design (`.gitignore:2`: *"Everything not listed here IS committed, including sessions/"*). Spilling does not create that exposure; it is the one thing that could reduce it, by keeping the bulk out of the transcript.

The second argument for refetch-only also fails on the evidence. §19.1 found **only ~9 of 89 builtin tools accept any narrowing parameter**. Refetch-only therefore lands on D7.2's "honest floor" — *"this data is not retrievable in smaller pieces"* — for the overwhelming majority of tools, making it a dead end in the common case rather than the exception.

**Decision.** On overflow: write the full result to the agent's `work/tool_results/<ref>/`, return the complete index plus the path, **and** a narrowing hint where the tool's schema offers one (D7). Recovery is random-access, works for every tool including non-idempotent ones, and costs no second call to a third-party service.

**Retention.** Spilled files live in the agent's scratch tree, which is gitignored and already understood as ephemeral. They are removed with the session.

**The spill must be readable, not merely stored.** A 1.18 MB payload written as one JSON line defeats `grep`, `head` and any line-oriented read. The writer reshapes: collections become newline-delimited (one record per line), oversized fields inside each record are clamped with a marker, and an index file is written alongside. **The caps in §6.1 apply to reads of the spill as well as to the original result** — otherwise a `grep` returning one 400 KB line reproduces the original failure through the recovery path built to prevent it.

### 7.3 Non-idempotent and expensive tools — solved by spill, not by refetch

This was the strongest argument against refetch-only, and §7.2 resolves it: a 90-second shell command, a scrape, or any stateful operation is never re-executed. Its output is on disk and read from there.

**[CORRECTED 2026-08-22]** An earlier draft asserted that foreground `bash` could refetch from its own 1 MB session buffer. The adversarial review disproved it: a foreground call **creates no session and retains no buffer**, and its output is already head-truncated at 10,000 chars (`pkg/tools/shell.go:1294`); the background buffer is a *destructive drain* with a 30-minute GC. Under refetch-only, shell output over the cap would have been unrecoverable. Under D5 as decided, it spills like everything else.

## 8. D6 — Generic reduction: four result shapes

Shape is detected **from the bytes, not the tool name** — cheap, deterministic, no registry, no per-tool code.

| Shape | Detected by | Index form | Refetch verb |
|---|---|---|---|
| **Collection** | JSON array, or object with one dominant array field | per-record fields under N bytes kept; fields over it elided as `<omitted, 412 KB>` | by id, or smaller `max_results` |
| **Line-oriented** | text, many newlines, consistent line lengths — shell output, grep hits, logs, code | total lines + bytes, head N + tail N | `offset` / `limit` |
| **Document** | one long run, prose or markup — web fetch, `browser_get_text`, HTML | heading outline where one exists, else byte-offset map + head | `start_index`, or a narrower selector |
| **Deep object** | JSON, no dominant array | structural skeleton: keys kept, large values → `<omitted, N bytes>` | field/path selector, else the honest floor (D7.2) |

One rule underneath all four: **keep what is small, elide what is large, report the shape.** Only the elision unit changes — records, lines, byte ranges, subtrees.

**Why size alone is sufficient.** The reducer needs no semantics. Applied to the incident payload with no Gmail knowledge, the size split keeps `from`, `subject`, `date`, `messageId`, `labelIds` and elides `messageText` at 412 KB — exactly the index a human would design. That is not a coincidence about Gmail: a result is oversized *because* one or two fields dominate, and the identifying fields are always the small ones.

**Worked mapping to the existing tool surface:**

| Tool | Tier | Shape when it overflows |
|---|---|---|
| `read_file` | 1 — pages natively; an over-limit explicit range errors before loading | Line-oriented |
| `bash` / shell | 1 — 1 MB session buffer + bounded read-back (§7.3) | Line-oriented |
| web fetch | 1 — `max_length` + `start_index` | Document |
| web search | 2 | Collection — titles and URLs kept, bodies elided |
| `browser_get_text` | 1 where a selector exists, else 2 | Document |
| any third-party MCP tool | 2 | whichever of the four the bytes indicate |

**The reducer is pure and deterministic — no LLM call.** It runs on every oversized result; a summarisation call on the path meant to cut cost and latency would reintroduce both.

## 9. D7 — The refetch recipe is derived from the tool's own input schema

Every tool declares its parameters — MCP servers publish `inputSchema` in `tools/list`, builtins carry theirs in the registry. The harness already holds this and has never used it for this purpose.

### 9.1 Convention table

| Schema parameter matches | Generated hint |
|---|---|
| `limit`, `max_results`, `max`, `count`, `page_size`, `top_k`, `n` | re-call with a smaller value |
| `id`, `*_id`, `ids` | re-call for one record — **the index already lists the ids** |
| `query`, `q`, `filter`, `search` | narrow the query |
| `fields`, `format`, `verbosity`, `full`, `include_*` | request the terse form |
| `offset`, `start_index`, `cursor`, `page_token` | continue from a position |

The id case is strongest, and works only because D6 and D7 compose: the index surfaces the identifiers, the schema confirms the tool accepts one. Together they produce a correct, concrete recipe with no tool-specific code.

### 9.2 The honest floor

If no parameter matches, **say so**:

> *This tool has no narrowing parameter. The index above is the complete record list; full content is not retrievable in smaller pieces.*

A fabricated `next` is worse than none: the model tries it, it fails, and that is a wasted turn plus a thrash increment. Admitting the dead end lets the model work with the index and move on.

### 9.3 Opt-in override

A small optional interface a builtin may implement to declare its own index projection and narrowing hint, for the few where the structural guess is poor. MCP tools cannot implement it and do not need to — the generic path covers them.

## 10. D8 — Escalating hints and a thrash guard

Track per-turn overflow count per tool. First occurrence: neutral hint. Second: name the exact narrowing parameter. Third: withdraw the retry offer and instruct the model to proceed with what it has. This is Claude Code's compaction-thrash guard applied in-band to the model rather than out-of-band to the user.

## 11. D9 — Tool-result-first eviction

`windowTrim` (ADR-028) evicts whole turns and remains the compaction path. D9 **adds a cheaper prior step**: clamp *old tool results in place* before evicting turns. Precedent: Cline clamps to 2,000 chars during compaction; the Anthropic API ships this as a first-class primitive (`clear_tool_uses_20250919`, defaults `trigger` 100,000 / `keep` 3); LangChain ships `ClearToolUsesEdit`. Anthropic describes tool-result clearing as "one of the safest, lightest touch forms of compaction."

This does not reintroduce an LLM summariser and does not violate ADR-028's "windowTrim is the only compaction path" — it is a pre-step within the same eviction call, with zero LLM calls, and deletes nothing on disk.

## 12. D10 — No silent turn exits

The four returns in §1.4 gain a typed code, a log line carrying the raw cause, and a transcript entry — matching `loop.go:9245`. `"turn timed out"` and `"turn canceled"` stop resolving to `CodeUnknown`; new codes are added to `contracts/components/schemas/LLMError.yaml` and regenerated per Constraint #8.

**Closed list of remaining turn-fatal conditions:** provider auth rejected, provider unreachable after retries, workspace unavailable. **Nothing size-related is ever turn-fatal after D4–D7** — an assembled request still over budget means the loop failed to do its job, which is a bug to fix, not a message to show a user.

## 13. D11 — Learn the window from the provider

`pkg/agent/translate_error.go::contextOverflowSubstrings` already matches `"maximum context length"`, `"context_length_exceeded"`, `"context window exceeded"` and four siblings. Today that detection only produces user-facing copy. D11 feeds it back: when a provider states its real limit, cache it for that model as a runtime override (ladder rung 3). A provider that publishes no catalog teaches its limit once and never again. The same path collects reported `prompt_tokens` for estimator calibration (§6.1).

## 14. D12 — Operator controls in Settings and the UI

The limits and the window override are **first-class product surfaces**, not env vars. Per Constraint #8: schema in `contracts/components/schemas/`, regenerated Go and TS types, REST endpoint, SPA control.

**Settings → Chat → Tool output** (or a new *Limits* group):

| Control | Default | Notes |
|---|---|---|
| Tool output limit | 62,500 chars | the primary control |
| Advanced ▸ MCP tool limit | 62,500 chars | disclosure |
| Advanced ▸ Builtin tool limit | 30,000 chars | |
| Advanced ▸ Failed-call limit | 10,000 chars | |
| Advanced ▸ Warn threshold | 25,000 chars | metric only |

All bounded by the 150,000-char ceiling (§6.1).

**Settings → Models (or beside the model picker):** the **effective context window shown read-only**, with its source (operator / catalog / learned / floor), plus an override field. Half the reason the 8× error stayed invisible is that this number is currently unreachable from the UI *and* the API.

---

## 15. Consequences

**Positive.** No single tool result can exceed the budget, and one that would have is routed rather than dropped. Needless trimming stops. Failures become diagnosable. The fix reaches all nine seeded vendors at once. Per-run token cost falls, by the quadratic argument in §2. Spilling the bulk to the gitignored `work/` tree keeps it **out** of the git-tracked `sessions/`/`.context/` files where it lands today, so the change reduces data-at-rest exposure rather than adding to it.

**Not yet addressed — see §22.** The aggregate across a turn is still unbounded; §1.3 remains the one diagnosed defect without a decision.

**Negative / accepted.** The seed acquires two fields to maintain at release cadence, and a stale seed under-reports a new model's window until refreshed (mitigated by D11). The conservative unknown-window floor over-trims for unseeded models — visible and harmless, by design. Refetch costs an extra call to the external service where it occurs (latency, rate limit, possibly billing) — accepted as strictly cheaper than the alternative it replaces. Non-idempotent tools degrade to lossy truncation (§7.3). D9 adds a step to a hot path.

**Explicitly out of scope.** Whether `max_tokens: 32768` is itself under-set for GLM-5.2 (OpenRouter reports `max_completion_tokens=131072`); sub-agent context isolation as a context-management strategy; code-execution-over-tool-calls.

## 16. Contract impact (Constraint #8)

- `capabilities` seed `schema_version` 1.0.0 → 1.1.0. Internal file, not a wire format, but the puller's version comparison must tolerate a mixed fleet: an older binary must ignore unknown fields, and a newer binary must survive a seed lacking them (falling to ladder rung 4).
- D5's truncated-result payload crosses to the model and to the SPA. It is a **candidate member of the ADR-060 structured tool-failure family** — schema, gateway allow-list entry, SPA renderer — except that it is not a *failure*, which is the one property every current member shares. Membership is left to the implementing branch to decide and record; what is decided here is that it must not be hand-rolled with `fmt.Sprintf`, per ADR-060's `%q` finding.
- D10's new `LLMError` codes require `make gen-contracts` and committed generated artifacts.
- D12 adds a settings schema plus REST surface for the limits and the window override.

## 17. Alternatives rejected

1. **Raise `max_tokens` so the heuristic lands closer.** Works by accident; breaks the next time output length is tuned.
2. **Set `agents.defaults.context_window` and stop there.** Fixes one install, leaves every other install and every unseeded model wrong, and does nothing about §1.2–§1.4. Retained as the immediate stopgap, rejected as the answer.
3. **Enable OpenRouter's `middle-out` transform.** Lossy, provider-specific, auto-disabled above 8,192 context, and it hides the problem rather than routing around it.
4. **Wait for MCP to specify a limit.** Silent through three revisions with two unadopted proposals. Rejected as unbounded.
5. **Build a dedicated context-window registry.** Duplicates embedding, refresh, checksum, versioning, persistence and id-normalisation that `pkg/providers/capabilities` already implements.
6. **Fetch provider catalogs live at boot.** Adds a network dependency to startup and fails for `custom`, CLI-backed and offline installs. Rejected in favour of build-time generation plus D11.
7. **Refetch-only, with no spill** (this ADR's own earlier position). Rejected 2026-08-22: its security rationale was inverted (§7.2), and §19.1 shows only ~9 of 89 builtins accept a narrowing parameter, so it degrades to "not retrievable" in the common case. Retained only as the *bonus* path where a schema offers one.
8. **Pagination of the tool result.** Possible only where the *server* implements it — MCP's `tools/call` has no cursor, and Composio's tool had none. Harness-side pagination over an already-received result is strictly dominated: by the time you could paginate you hold the whole payload, so forcing sequential access is worse than a complete index plus targeted refetch. Sequential paging also re-accumulates every walked page in context, reproducing the quadratic cost in instalments. The one property worth keeping — the iterator contract, "there is more, here is how to get it" — is preserved by D5's `next` field.
9. **Plain truncation with a marker, no index and no recipe.** The floor the field has moved past. Retained only as the failure-path and non-idempotent-tool fallback (§6.1, §7.3).

## 18. Exit proof

A guard test that feeds a ~2 MB tool result through the loop and asserts:

1. the assembled request stays under the resolved window;
2. the agent receives a complete index and issues a **narrowed refetch**;
3. **the turn completes successfully** — no user-facing error frame.

Assertion 3 encodes the actual requirement: not an error the user sees, but a condition the model notices and works around.

Plus a contract test that the capability catalog and the trim path agree on the resolved window for a given model — the disagreement that exists today between `instance.go:249`, `loop.go:11306` and `loop.go:11611`.

## 19. Implementation tasks

1. **Close the Tier-1 gaps.** The audit is **done** — see §19.1. Three tools need a bounding parameter added; the rest already comply.
2. **Verify foreground `bash` buffer retention** (§7.3) before committing to refetch-only for shell output.
3. **Seed-generation script** for `context_window` / `max_output_tokens` across the 78 entries.
4. **Confirm the OpenAI and Anthropic model-list endpoint shapes** (§21) before asserting a native catalog is unavailable.

### 19.1 Tier-1 compliance audit — result

Performed 2026-08-22 on this branch @ `f4aaf37c`. The authoritative catalog is the per-agent policy map, which under Constraint #6 must enumerate every static builtin: **89 builtin tools**, of which **39** can plausibly return bulk output.

**Already Tier-1 compliant — no work needed:**

| Tool | Bounding parameter | Hard cap |
|---|---|---|
| `read_file` | `offset`, `length` | **`MaxReadFileSize = 64 * 1024`** — source comment: *"64KB limit to avoid context overflow"* |
| `library_read` | `offset`, `length` | — |
| `read_inbox`, `search_email` | `limit` | truncates |
| `search_web`, `fetch_url` | `max_results`, `count`, `top_k` | truncates |
| `list_jobs` | `count`, `limit` | truncates |
| `recall_memory` | `limit` | — |
| `find_skills` | `limit` | — |

**Tier-1 gaps — a bounding parameter must be added:**

| Tool | Current parameters | Exposure |
|---|---|---|
| `list_directory` | `path` only | a directory with tens of thousands of entries is unbounded |
| `inspect_session` | `session_id`, `tool_name`, `role` | reads session transcripts, which are exactly the artefacts this ADR shows reaching megabytes |
| `recall_conversation` | `query`, `turn_range`, `time.from/to` | `turn_range` selects a range but bounds no size |

**This materially corrects D4's framing.** Tier 1 is not aspirational — it is largely built, and three tools are outliers rather than the rule. It also **corroborates the cap chosen in §6.1**: this codebase independently picked **64 KB** as its own "avoid context overflow" threshold for file reads, within 2% of the 62,500-char MCP cap derived from Claude Code. Two independent derivations landing on the same number is the strongest evidence available that the order of magnitude is right.

**Method and its limits.** Receivers were resolved from each tool's `Name()` implementation and parameter names read from that receiver's own `Parameters()` body, so the three gaps are precise. The "already compliant" rows for tools sharing a source file with another tool were read at file level and are accurate to the file, not necessarily to the receiver — they should be spot-checked before the implementing commit relies on any individual row.

## 20. Open questions for ratification

1. Whether D5's payload joins the ADR-060 family or is modelled separately (§16).
2. The conservative floor value for an unknown window.
3. Whether the per-agent `context_window` override is worth the wire-surface cost, or whether the global default plus the catalog suffices.
4. The byte threshold N in D6's per-field keep/elide rule.

## 21. Unverified items

- **[UNVERIFIED]** Whether the OpenAI and Anthropic model-list endpoints publish context length. Confirming requires the operator's API keys, deliberately not used. Immaterial to D1 (the seed is generated from OpenRouter's catalog) but must be checked before any claim that a native catalog is unavailable.
- **[UNVERIFIED]** Ollama's local field name for context length (`/api/show`). The operator's daemon was not running. Needed only if D2 gains a live local-query rung for Ollama.
- ~~Whether foreground `bash` retains a session output buffer~~ — **resolved 2026-08-22: it does not.** See §7.3.
- ~~Which builtin tools already accept a source-bounding parameter~~ — **resolved 2026-08-22, see §19.1.**

---

## 22. Open — the aggregate budget (CRIT-001)

The adversarial review's first blocker stands unresolved, and is recorded here rather than papered over.

D4 caps each result. **Nothing caps their sum.** `max_tool_iterations` is configurable — per-agent, then `agents.defaults`, then a hardcoded fallback of **200** (`pkg/agent/instance.go:241`); the operator's install sets 50. At 50 iterations the builtin cap alone admits 50 x 30,000 chars; at the shipped default of 200 it admits far more than any current model can hold. §1.3 is therefore the one defect of the four diagnosed in §1 that this ADR does not yet fix, and §12's "nothing size-related is ever turn-fatal" is not yet true.

Operator direction, 2026-08-22: **no summarisation of tool results.** That rules out the Cline-style clamp-to-2,000-chars approach and, with it, D9 as originally written.

Candidate under discussion — *retroactive spill*: track the running total across the turn; as it approaches budget, take the **oldest** tool results still in the window and apply the D5 treatment to them (payload already on disk, context entry replaced by its complete index plus path). Nothing is summarised and nothing is lost — the same mechanism, applied after the fact. Not yet decided.
