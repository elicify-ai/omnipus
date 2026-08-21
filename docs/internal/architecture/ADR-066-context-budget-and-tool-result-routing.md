# ADR-066: Context budget and tool-result routing — provider-sourced windows, an absolute result cap, and in-band recovery

- **Status:** Proposed (2026-08-21). Drafted from a live production incident on the operator's own instance; awaiting operator ratification before implementation.
- **Date:** 2026-08-21
- **Related:** [ADR-028](ADR-028-context-paging-sliding-window-recall.md) (`windowTrim` as the only compaction path — **extended, not superseded**, see D7); [ADR-051](ADR-051-media-handling-and-provider-error-translation.md) (`LLMError` classifier and the write choke point — extended by D8); [ADR-060](ADR-060-structured-tool-failure-family.md) (structured tool-failure family — D5's payload is a candidate member, see §6); CLAUDE.md **Constraint #1** (single binary, no new runtime deps), **Constraint #6** (explicit tool policy, no defaults — see D5.3), **Constraint #8** (contract-first wire formats).
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1 for everything cited as read; 3 for the two items marked **[UNVERIFIED]** in §15. Code claims were read in-session on the running build tree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/build-v0.1.1` @ `6acd378` — the tree the failing binary was built from, not `main`. Cited as `file::symbol` per CLAUDE.md, except where a line number is the claim itself. Claims that are *absences* are cited as searches.

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

The only output cap in `pkg/tools` is `maxOutputBufferSize = 1 * 1024 * 1024` for **bash sessions** (`pkg/tools/session.go:16`). `pkg/mcp/manager.go` contains no truncation of any kind (searched). A 1.18 MB single MCP result is the proof.

`pkg/gateway/tool_result_store.go` already offloads any result over `InlineToolResultMaxBytes = 50 KiB` to disk and hands the SPA a 4 KiB preview plus a reference. **That machinery points at the browser. Nothing points it at the model.**

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
| **Omnipus** | **none** |

Two findings shape the decisions below.

**The MCP specification defines no result size limit, in any revision** (2025-06-18, 2025-11-25, 2026-07-28). `CallToolResult` carries no byte, token, or character cap. Pagination is specified for `resources/list`, `prompts/list`, `tools/list` — **`tools/call` has no cursor**. Two proposals to change this are open and unadopted. Servers split accordingly: `google_workspace_mcp` truncates bodies at 20,000 chars and offers page tokens; `GongRzhe/Gmail-MCP-Server` returns entire bodies with no page token. Composio behaved like the latter. **The cap therefore cannot be delegated to server authors, now or later.**

**Cline's source states the cost argument**, and it is the strongest reason to cap tightly rather than raise the limit: a tool result is re-sent on every subsequent request in the run, so an oversized result costs **quadratically** across the remaining turn, not linearly. The 2.0 MB above would have been re-paid on every iteration.

The consensus shape is not truncation. It is **middle-elision or offload, with the next action named in the surviving text** — Codex, Cline, Roo, Claude Code and Cursor converged on it independently.

---

## 3. D1 — Extend `pkg/providers/capabilities`; do not build a second catalog

The capability catalog needed for context windows **already exists**, built for media modality:

- `pkg/providers/capabilities/embed.go` — `//go:embed data/providers_capabilities_seed.json`, documented as "the guaranteed last-resort source-of-truth" when no store is configured and the network is unreachable.
- The seed carries **78 models across 9 providers**: openai 14, anthropic 14, z-ai 11, mistral 10, moonshot 9, google 8, minimax 5, xai 4, deepseek 3.
- `pkg/providers/capabilities/puller.go` — `GHReleasePuller`, GitHub Release with raw fallback, checksum-verified (`ErrChecksumMismatch`).
- `pkg/providers/capabilities/version.go` — semver-aware `Version.Compare`, so `Catalog.Refresh` cannot downgrade.
- `catalog.go::Store` — persistence; `catalog.go::seedFile.validate` — permissive DTO → invariant-bearing domain type.
- `catalog.go::Catalog.Resolve` → `catalog.go::resolveStrippedPrefix` — **id normalisation is already solved**: `z-ai/glm-5.2` resolves to the seed's `glm-5.2`, which is present.

The per-model wire keys today are exactly four: `id`, `provider`, `input_modalities`, `resize_budget`. A search for any `context`, `window`, or `token` key across the seed returns **none**.

**Decision.** Add `context_window` and `max_output_tokens` to `modelDTO` and `Model`, bump `schema_version` (1.0.0 → 1.1.0), populate all 78 entries, and expose `resolvedModel.ContextWindow()` alongside the existing `Supports()` and `Budget()`.

**Rationale.** This inherits embedding, signed refresh, version-regression protection, persistence and degraded-transport reporting for free; it satisfies Constraint #1 with no new runtime dependency and no boot-time network requirement; and because the seed is provider-keyed, the fix reaches all nine vendors at once rather than only OpenRouter-routed models.

**Generation source.** The OpenRouter public models endpoint (`GET https://openrouter.ai/api/v1/models`, unauthenticated) returned 420 models covering every vendor in the seed — openai 94, qwen 51, google 41, anthropic 28, mistralai 19, z-ai 15, deepseek 14, x-ai 6. It publishes `context_length` for models regardless of whether Omnipus routes through OpenRouter, so it can seed direct-to-OpenAI and direct-to-Anthropic entries too. Seed generation is a **build-time script** in the `scripts/gen-*` family whose output is committed, never a runtime fetch.

**Caveat to encode.** OpenRouter's `context_length` and `top_provider.context_length` can differ (observed: `z-ai/glm-5` 204,800 vs 198,000). Treat catalogue values as accurate-not-exact and reserve a margin in the budget calculation rather than sizing to the published number exactly.

## 4. D2 — Resolution ladder for the effective window

In order, first hit wins:

1. **Operator override** — `agents.defaults.context_window`, or a new per-agent field. No network. The escape hatch for `custom`/passthrough endpoints.
2. **Catalog** (D1).
3. **Learned override** (D9).
4. **Conservative floor, with a WARN naming the model and provider.**

`maxTokens * 4` at `pkg/agent/instance.go:249` is **retired**. The two divergent flat `128000` fallbacks at `pkg/agent/loop.go:11306` (`windowTrim`) and `pkg/agent/loop.go:11611` (model switch) are consolidated onto the same ladder — three code paths currently answer "what window do we assume" with three different numbers.

**CLI-backed providers (`claude-cli`, `codex-cli`, `antigravity`) are exempt from budgeting entirely.** Those harnesses manage their own context; imposing a second, guessed budget on top causes the needless trimming of §1.1 without preventing anything.

## 5. D3 — The unknown-window default is conservative and loud

`catalog.go::Catalog.optimistic` returns an *optimistic* default for unknown models (FR-026): unknown ⇒ assume image support. That is correct for modality — optimism costs a rejected request and a retry.

**For a context window, optimism costs a dead turn.** D3 therefore departs deliberately: an unknown window resolves to a conservative floor and emits a warning naming the model. The two policies now disagree by design, and this paragraph is the record of why.

## 6. D4/D5 — An absolute result cap, and routing instead of failing

**D4.** One choke point where a tool result becomes a context message, so MCP servers and builtins are covered by construction rather than per-tool. **Budget in bytes/characters, not lines** — characters track token cost, and line-based caps are being actively retired elsewhere in the field (Roo's much-quoted 500-line limit is commented in-source as a display limit, "not LLM context limits"). Initial value to be fixed at implementation; the surveyed cluster is 10k–48k characters.

**The cap is window-independent.** This is load-bearing: it behaves identically whether the resolved window is 131,072 or 1,048,576, so D4 remains correct even where D1–D3 resolve badly. It is therefore shippable *before* the catalog work completes.

**D5.** An over-cap result is **not an error**. It is a different shape of success, delivered in-band to the model:

```
status:  truncated
reason:  1,178,522 chars exceeds the 48,000 char budget
shape:   10 messages; fields: from, subject, date, snippet, body
preview: <first N KB>
handle:  <workspace path>
next:    re-call with max_results=3, or grep the handle for a sender
```

Four properties are mandatory:

1. **The next action is named**, concretely, with the parameter that would work.
2. **The shape is described**, so the model can decide whether it needs the bulk at all. Usually it does not.
3. **`handle` is issued only when that agent has a reader tool allowed.** Under Constraint #6 every tool policy is explicit per agent; handing back a path an agent cannot open is a dead end. Absent a reader, emit a larger preview instead.
4. **It is not flagged as a failure.** Models that receive an error-shaped result tend to apologise and stop rather than continue.

*Precedent that this works in Omnipus today:* in the same incident session, entries 21 and 23 of the context file are both `Command blocked by safety guard (dangerous pattern detected)`. The agent hit a hard refusal twice, adapted, and completed the turn — because the refusal arrived as a tool result, not a turn error. It also shows the failure mode D5.1 guards against: the guard named *what was blocked* but not *what was allowed*, so the agent guessed, failed again, and abandoned the approach.

**D6 — escalating hints and a thrash guard.** Track per-turn overflow count per tool. First occurrence: neutral hint. Second: name the exact narrowing parameter. Third: stop offering the retry and instruct the model to proceed with what it has. This is Claude Code's compaction-thrash guard applied in-band to the model rather than out-of-band to the user.

## 7. D7 — Tool-result-first eviction

`windowTrim` (ADR-028) evicts whole turns and remains the compaction path. D7 **adds a cheaper prior step**: clamp *old tool results in place* before evicting turns or summarising. Precedent: Cline clamps to 2,000 chars during compaction; the Anthropic API ships this as a first-class primitive (`clear_tool_uses_20250919`, defaults `trigger` 100,000 / `keep` 3); LangChain ships `ClearToolUsesEdit`. Anthropic describes tool-result clearing as "one of the safest, lightest touch forms of compaction."

This does not reintroduce an LLM summariser and does not violate ADR-028's "windowTrim is the only compaction path" — it is a pre-step within the same eviction call, with zero LLM calls, and deletes nothing on disk.

## 8. D8 — No silent turn exits

The four returns in §1.4 gain a typed code, a log line carrying the raw cause, and a transcript entry — matching `loop.go:9245`. `"turn timed out"` and `"turn canceled"` stop resolving to `CodeUnknown`; new codes are added to `contracts/components/schemas/LLMError.yaml` and regenerated per Constraint #8.

**Closed list of remaining turn-fatal conditions:** provider auth rejected, provider unreachable after retries, workspace unavailable. **Nothing size-related is ever turn-fatal after D4/D5** — an assembled request still over budget means the loop failed to do its job, which is a bug to fix, not a message to show a user.

## 9. D9 — Learn the window from the provider

`pkg/agent/translate_error.go::contextOverflowSubstrings` already matches `"maximum context length"`, `"context_length_exceeded"`, `"context window exceeded"` and four siblings. Today that detection only produces user-facing copy. D9 feeds it back: when a provider states its real limit, cache it for that model as a runtime override (ladder rung 3). A provider that publishes no catalog teaches its limit once and never again.

---

## 10. Consequences

**Positive.** The incident class becomes impossible: no single tool result can exceed the budget, and a result that would have is routed rather than dropped. Needless trimming stops. Failures become diagnosable. The fix reaches all nine seeded vendors at once. Per-run token cost falls, by the quadratic argument in §2.

**Negative / accepted.** The seed acquires two fields that must be maintained at release cadence, and a stale seed silently under-reports a new model's window until refreshed (mitigated by D9). The conservative unknown-window floor will over-trim for unseeded models — visible and harmless, by design. D5's payload adds a wire type (§11). D7 adds a step to a hot path.

**Explicitly out of scope.** Whether `max_tokens: 32768` is itself under-set for GLM-5.2 (OpenRouter reports `max_completion_tokens=131072`); sub-agent context isolation as a context-management strategy; code-execution-over-tool-calls.

## 11. Contract impact (Constraint #8)

- `capabilities` seed `schema_version` 1.0.0 → 1.1.0. Internal file, not a wire format, but the puller's version comparison must tolerate a mixed fleet: an older binary must ignore unknown fields, and a newer binary must survive a seed lacking them (falling to ladder rung 4).
- D5's truncated-result payload crosses to the model and to the SPA. It is a **candidate member of the ADR-060 structured tool-failure family** — schema in `contracts/components/schemas/`, gateway allow-list entry, SPA renderer — except that it is not a *failure*, which is the one property every current member shares. Membership is left open for the implementing branch to decide and record; what is decided here is that it must not be hand-rolled with `fmt.Sprintf`, per ADR-060's `%q` finding.
- D8's new `LLMError` codes require `make gen-contracts` and committed generated artifacts.

## 12. Alternatives rejected

1. **Raise `max_tokens` so the heuristic lands closer.** Works by accident; breaks the next time output length is tuned. Rejected.
2. **Set `agents.defaults.context_window` and stop there.** Fixes one install, leaves every other install and every unseeded model wrong, and does nothing about §1.2–§1.4. Rejected as a complete answer; retained as the immediate stopgap.
3. **Enable OpenRouter's `middle-out` transform.** Lossy, provider-specific, auto-disabled above 8,192 context, and it hides the problem rather than routing around it. Rejected.
4. **Wait for MCP to specify a limit.** Silent through three revisions with two unadopted proposals. Rejected as unbounded.
5. **Build a dedicated context-window registry.** Duplicates embedding, refresh, checksum, versioning, persistence and id-normalisation that `pkg/providers/capabilities` already implements. Rejected in favour of D1.
6. **Fetch provider catalogs live at boot.** Adds a network dependency to startup and fails for `custom`, CLI-backed and offline installs. Rejected in favour of build-time generation plus D9.
7. **Plain truncation with a marker, no handle.** The floor the field has moved past; discards data the agent often needs. Retained only as D5's fallback where no reader tool is allowed.

## 13. Exit proof

A guard test that feeds a ~2 MB tool result through the loop and asserts:

1. the assembled request stays under the resolved window;
2. the agent receives a usable handle-or-preview and issues a follow-up call;
3. **the turn completes successfully** — no user-facing error frame.

Assertion 3 is the one that encodes the requirement: not an error the user sees, but a condition the model notices and works around.

Plus a contract test that the capability catalog and the trim path agree on the resolved window for a given model — the disagreement that exists today between `instance.go:249`, `loop.go:11306` and `loop.go:11611`.

## 14. Open questions for ratification

1. The cap's initial value (surveyed cluster: 10k–48k characters).
2. The conservative floor for an unknown window.
3. Whether D5's payload joins the ADR-060 family or is modelled separately (§11).
4. Whether the per-agent `context_window` override is worth the wire-surface cost, or whether the global default plus the catalog suffices.

## 15. Unverified items

- **[UNVERIFIED]** Whether the OpenAI and Anthropic model-list endpoints publish context length. Confirming requires the operator's API keys, deliberately not used. Immaterial to D1 (the seed is generated from OpenRouter's catalog) but must be checked before any claim that a native catalog is unavailable.
- **[UNVERIFIED]** Ollama's local field name for context length (`/api/show`). The operator's daemon was not running. Needed only if D2 gains a live local-query rung for Ollama.
