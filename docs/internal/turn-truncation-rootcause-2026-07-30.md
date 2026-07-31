# Root cause: agent stops mid-sentence on a truncated LLM response

**Date:** 2026-07-30
**Found in:** live UAT (`uat-omnipus`), session `session_01KYSA6AH8RYXJDR9C22AQKSCX`, agent `jim`, turn `jim-turn-11`
**Severity:** High — silent failure. The turn reports success; the user sees the agent stop working with no error.

## Symptom

Jim was researching agent file tools and was about to write the specification
document. Eight tool iterations ran normally at 6–15 s each. Then:

| Time | Event |
|---|---|
| `16:13:47` | *"I now have comprehensive data… Let me update progress and write the document"* + `set_todos` |
| | **2 min 28 s gap** — 10–25× every other iteration |
| `16:16:16` | `"Now I have all the research data. Let"` — cut mid-word, **zero tool calls**, turn ends |

The turn ended as a **success**. Nothing was logged, nothing was surfaced in
chat, no retry, no continuation. The work was never finished.

## Root cause

**The agent loop never inspects `finish_reason`.** A truncated response is
structurally indistinguishable from a completed one.

The value is captured correctly and then discarded:

1. `pkg/providers/openai_compat/provider.go:360` — reads `finish_reason` from the API
2. `pkg/providers/openai_compat/provider.go:419` — returns it on `LLMResponse`
3. `pkg/agent/loop.go:7801` — stores it: `innerTS.SetLastFinishReason(response.FinishReason)`,
   commented *"Save finishReason to turnState for SubTurn truncation detection"*
4. `pkg/agent/turn.go:1532` — `GetLastFinishReason` has **zero callers**.
   The truncation detection it was scaffolded for was never implemented.
5. `pkg/agent/loop.go:7873` — `if len(response.ToolCalls) == 0 { … }` treats the
   content as the final answer and ends the turn. No `finish_reason` check.
6. `turnFailed` stays `false` → the done frame reports success.

## Trigger

`max_tokens: 32768` (config default, `pkg/config/defaults.go:34`) and
`z-ai/glm-5v-turbo` is a reasoning model. A 2.5-minute call yielding ~8 visible
tokens means the output budget was consumed — by reasoning tokens and/or the
large document being generated — before the answer could be emitted. Almost
certainly `finish_reason: "length"`.

**Caveat:** the exact value cannot be proven from the logs *because the code
never persists it*. It is held in memory and dropped. That unrecoverability is
itself part of the defect. The inference rests on the clean stream termination
plus the mid-word cut.

## Hypotheses rejected (evidence)

All counts from `gateway.prev.log` on the UAT machine.

| Hypothesis | Evidence against |
|---|---|
| Context window exceeded / compression | `"Proactive window trim"` = 0, `"compress"` = 0. Never fired. |
| Stream error / EOF / reset / deadline | All 0. Decisively: the provider's own `"stream ended without finish_reason"` warn (`provider.go:409`) did **not** fire — proving a finish_reason **was** received. Clean termination, not a network cut. |
| Malformed tool-call args dropped | `provider.go:392` would log `"failed to decode tool call arguments"` and still emit the call with `args["raw"]`. Count 0; no tool call was in flight. |
| MaxIterations (200) reached | Turn had 9 entries. That path sets `turnFailed` + a `defaultResponse` sentinel — would produce sentinel text, not truncated prose. |
| Synthetic-error-loop abort | Emits a `turn_aborted` message; count 0. |
| User cancel / orphan watchdog / panic | All route through error paths that log. Log silent at 16:16; gateway still serving at 16:17. |

The 7 errors present in the log are all tool-level and hours earlier
(10:48–15:42) — unrelated.

## Fix

Four layers. (1) and (2) are the correctness fix; (3) and (4) are the operator's
added requirements — a truncated turn must never just stop dead.

### 1. Treat truncation as a first-class terminal state

At `pkg/agent/loop.go:7873`, before accepting content as the final answer, read
the `finish_reason` that is already captured. `length` (and `unknown` / `error`)
must not be treated as a completed answer.

### 2. Log and persist it

Put `finish_reason` on the turn log line and the transcript entry, so the next
occurrence is diagnosable without source archaeology. Today it is unrecoverable
after the fact.

### 3. Graceful conclusion instead of a severed sentence

On detecting `length`, run a **bounded continuation**, then a **forced wrap-up**:

- **Continue** — re-issue with the partial assistant content prefilled and an
  instruction to resume. Bounded (2 attempts max): the truncation here was caused
  by reasoning consuming the budget, so a naive retry can hit the same wall
  repeatedly.
- **Wrap up** — if continuation is exhausted, spend one small, capped call
  (~500 tokens, reasoning minimised) instructing the model to close out: state
  where it got to and what remains. A few hundred extra tokens buys a coherent
  ending instead of a cut-off word.
- Merge the parts into one assistant message so the transcript stays clean.

Continuation is only safe for **text**. A tool call truncated mid-JSON must not
be stitched — retry the call fresh or fail loudly.

### 4. Explicit error in chat

A visible, non-dismissable notice on the message: *"Response hit the output
token limit (32768) and was cut off."* — plus what was done about it (continued
/ wrapped up / gave up). Set `turnFailed = true` so the done frame carries it
and automation clients can detect it without parsing prose.

## Design change: our internal response types (the structural remedy)

Layers 1–4 above patch *this* bug. The reason it was possible at all is a type
design problem, and that is worth fixing directly — otherwise the next
provider signal we care about gets dropped the same way.

**Every major API makes truncation an explicit terminal state:**

| API | Field | Truncation value |
|---|---|---|
| Anthropic Messages | `stop_reason` | `max_tokens` (vs `end_turn`, `tool_use`) |
| OpenAI Chat Completions | `finish_reason` | `length` (vs `stop`, `tool_calls`) |
| OpenAI Responses | `status` + `incomplete_details.reason` | `incomplete` + `max_output_tokens` |

The third row is the one to learn from. Chat Completions puts truncation in a
**field beside perfectly normal-looking content** — you must actively check it,
and ignoring it looks exactly like success. That is precisely the trap this
codebase fell into: `LLMResponse` carries `Content` (populated, plausible) and
`FinishReason` (ignored) side by side, so the consuming code read a truncated
answer as a finished one and could not have noticed.

The Responses API instead makes it **structural**: `status != "completed"`. An
incomplete response cannot be accidentally read as a finished one, because
success is a state you must match on before you can reach the content.

**Requirement: redesign `providers.LLMResponse` so truncation is
unrepresentable as success.** Sketch — the exact shape is for the ADR, not this
document:

- Success and non-success become *distinct states*, not a string field on one
  struct. The final content is reachable only through the completed state.
- The terminal reason is a **typed enum**, not a raw provider string: at
  minimum `Completed`, `ToolCalls`, `Truncated`, `Filtered`, `Unknown`. Each
  provider adapter maps its own vocabulary (`length` / `max_tokens` /
  `max_output_tokens`) onto it once, at the boundary.
- Consuming a response without handling the non-success states should be a
  **compile-time** problem, not a code-review problem. Today `turnFailed`
  defaulting to `false` means "silently succeeded" is the path of least
  resistance; it should be the path that does not compile.
- `Unknown` must be handled explicitly and loudly. `provider.go:409` already
  synthesises `"unknown"` when a stream ends without a reason — today that also
  falls through to the success path.

This applies to the internal type only. The wire contract is unaffected
(Constraint #8 — `contracts/*.yaml` remains the source of truth for anything
crossing the gateway/SPA boundary).

**Related class of defect.** `GetLastFinishReason` was a *captured-but-never-read*
provider signal. That is a category, not a one-off — see the sweep below.

## Sweep: other captured-but-never-read signals

Audit run 2026-07-30 across `pkg/agent/`, `pkg/providers/`, `pkg/tools/`,
`pkg/session/`, `pkg/gateway/`, and the SPA consumers in `src/`.

Signature of the class: **a setter is called in production code, a getter or
field exists, and nothing ever reads it.** The data is faithfully collected and
dropped. Comments frequently name an intent that was never implemented ("for X
tracking", "for Y detection").

The two HIGH cases below were re-verified independently of the audit agent;
the commands and their results are recorded per case.

### Case 1 — delegation/team token budget is a silent no-op (HIGH)

**Symbols**
- `turnState.tokenBudget *atomic.Int64` — `pkg/agent/turn.go:189`
- `turnState.lastUsage` + `SetLastUsage` / `GetLastUsage` — `pkg/agent/turn.go:190-191, 1545-1557`
- `SubTurnConfig.InitialTokenBudget` — `pkg/tools/delegate.go:61`, `pkg/agent/subturn.go:199`

**Written (data really flows in)**
- `pkg/agent/loop.go:7801-7805` — every LLM response's usage stored via
  `innerTS.SetLastUsage(response.Usage)`, commented *"Save usage for token budget tracking"*.
- `pkg/agent/subturn.go:922-933` — a child sub-turn inherits `tokenBudget` from
  `cfg.InitialTokenBudget`, the parent, or `rtCfg.defaultTokenBudget`
  (operator-settable via `agents.defaults.subturn.default_token_budget`,
  `pkg/config/config.go:1156`).

**Never read**
- `rg -n "tokenBudget\.(Load|Add|Store|CompareAndSwap)" pkg/` → **no match**.
  The atomic counter is never decremented, never consulted; only ever
  re-pointed.
- `rg -n "GetLastUsage" --type go .` → definition and doc comment only,
  **zero callers including tests**.
- `rg -n "InitialTokenBudget" --type go .` → only the declaration and the
  pass-through at `subturn.go:354`. **No tool call site populates it**, so it is
  always `nil` in practice.
- The one working consumer of usage (`AddTurnStats`, `loop.go:7858`) reads
  `response.Usage` **directly** — `lastUsage` contributes nothing to the
  (separately functional) cost/token UI.

**Consequence** — an operator who configures a delegation/team token ceiling
gets a limit that is silently unenforced. Nothing decrements it, nothing checks
it, nothing even populates it. This is the highest-severity shape: **a safety /
cost limit that appears configurable but does nothing.**

### Case 2 — a canceled turn renders as completed after reload (HIGH)

**Symbol** — `session.TranscriptEntry.Truncated bool`, declared
`pkg/session/daypartition.go:179`, written by `UnifiedStore.MarkLastEntryTruncated`
(`pkg/session/unified.go:751-862`).

**Written (data really flows in)**
- `pkg/agent/cancel.go:338-344` — on every turn cancel, the `SetOnCancelFinish`
  callback calls `MarkLastEntryTruncated(sessionID, turnID)`.
- `pkg/session/unified.go:829` — sets `target.Truncated = true`. It sets **only**
  that; it never touches `TranscriptEntry.Status` (whose own doc comment at
  `daypartition.go:150` advertises `"ok" | "error" | "interrupted"`).
- The cancel path *does* set `interrupted` — but on the **session meta**
  (`InterruptSession` / `SetSessionInterrupted`), not on the per-message entry.

**Never read**
- The field reaches the wire (`json:"truncated,omitempty"`; present in the
  generated `Message` zod schema).
- But `rg -n "truncated" src/lib/api.ts src/store/chat.ts src/components/chat/MessageItem.tsx`
  → every hit is the **unrelated tool-result** truncation concept
  (`_truncated_client`, `_truncated`). The message-level field is never read.
  `RawMessage` does not even declare it; `rawToMessage()` branches only on
  `raw.status`.

**Consequence** — while the tab stays open, cancel correctly shows
"(interrupted)" (stamped live in memory from the WS event). After a reload or
reopening the session, `MessageItem`'s `status === 'interrupted'` check can
never be true, because `TranscriptEntry.Status` is never set to `interrupted`
and `truncated` is never read. **A response cut off mid-stream by a cancel is
rendered identically to a complete answer.** Same shape as the root bug — a
failure read as success — but crossing the Go/TS boundary rather than staying
inside `pkg/agent`.

Note `src/store/chat.ts:4035` carries a comment justifying the WS replay path
on the grounds that *"unlike a fresh REST cold-load, where the persisted
TranscriptEntry.Status already says 'interrupted'"* — an assumption the Go side
does not satisfy.

### Case 3 — the root bug is wider than truncation (severity raise)

Not a new symbol; a re-scoping of `GetLastFinishReason`. Every provider adapter
does real, provider-specific work to normalise a meaningful reason before it is
dropped on the same dead getter:

- `pkg/providers/bedrock/provider_bedrock.go:570-571` — `StopReasonContentFiltered`
  → `"content_filter"`
- `pkg/providers/antigravity_provider.go:485` — Gemini `"MAX_TOKENS"`
- `pkg/providers/openai_responses_common/responses_common.go:272-274` —
  Responses API `Status == "incomplete"` → `"length"`

Verified: `rg -n "content_filter|ContentFiltered" pkg/providers/` confirms the
Bedrock mapping.

**Consequence** — it is not only truncation that is swallowed. **A safety-filter
refusal is also read as a normal answer.** This raises the priority of the
response-type redesign above: it is not a length edge case, it is the whole
terminal-state channel.

### Case 4 — `ProcessSession.ptyKeyMode` accessor pair, fully inert (LOW)

`GetPtyKeyMode` / `SetPtyKeyMode` / `ptyKeyMode` — `pkg/tools/session.go:92-93,
126-129, 132-135`. `rg -n "GetPtyKeyMode|SetPtyKeyMode|ptyKeyMode" --type go .`
returns only the four definition lines — no caller, not even a test.

Distinct from the cases above: **neither side is wired**, so no live signal is
being discarded. Unfinished scaffolding for PTY arrow-key mode (CSI vs SS3),
listed only because it shares the shape. No active silent failure.

### Precedent — the same defect, already fixed once

`providers/common.ProviderError.BodyTruncated` (`pkg/providers/common/common.go:409-445`)
was written in three places with zero readers, and a prior review pass caught
it: `Error()` now folds the flag into the formatted message (`common.go:435-440`),
with regression coverage in `TestProviderError_Error_ReflectsBodyTruncated`
(`common_test.go:755-771`).

Useful as a remediation shape: **wire the dead field into the nearest existing
read site** — an `Error()`/`String()` method, or a log line — rather than
inventing a new consumer.

### Sweep conclusion

Three live instances (the root bug plus Cases 1 and 2), one inert pair, and one
already-fixed precedent. **The pattern is recurring, not a one-off.** It
concentrates at objects that accumulate per-turn or per-call state — `turnState`,
`TranscriptEntry`, provider responses — where a setter is added for a documented
future purpose and the consuming read is never written.

It is also **not confined to Go**: Case 2 crosses the wire. Any future audit of
this class must check contract fields against their actual SPA consumers, not
only Go-side callers.

## Prevention

- Raise `max_tokens` for reasoning models, or reserve headroom so reasoning
  cannot consume the entire visible-output budget.
- Steer large artifacts to **chunked file writes** rather than one giant
  response or tool call. A document written in several `write_file` /
  `edit_file` calls never approaches the cap — this is the structural fix and
  matters more than any retry logic.

## Related

The same log is dominated by 315 WebRTC DTLS warnings
(`forward sender report to viewer failed … DTLS transport has not started yet`)
— separate issue, tracked with the browser-panel investigation.
