# ADR-087 — Truncation is an outcome, not a silence

- **Status:** Proposed (revision 5 — 2026-09-13). **Implementation-ready.** Revisions 1–4 were
  reviewed by four independent passes (citation audit, design grill, two Codex passes at high
  reasoning effort); every accepted finding is folded in here and each decision names the finding
  it answers. History in §10.
- **Relates to:** ADR-071, ADR-057 (turn identity, cancel state machine), ADR-082, ADR-084 (the
  Judge), ADR-028 (`windowTrim`), Constraint #8 (contract-first)
- **Corrects:** the reasoning in commit `b12dfa83`'s code comment, not its behaviour.
- **Spec:** none, by operator directive.

## 1. Operator direction (verbatim, 2026-09-13)

> hey we are not talkiing to Vllm we talk to openai compatibel or anthropic compatible apis
> primary please validate your analsis of the cut of messages again

> so what are now the remaining issues regarding the truncation , you say claude code react to a
> signal that omnipus does not react to

> yes hold the uat , and design and plan the implementation of the 4 points , adr only no spec one
> adr grill than straight into implementation

Auto-continue aligned separately: **build it, one rule everywhere, bounded, gated on content
produced.**

## 2. Evidence

### 2.1 The signal is reliable, it arrives, and nothing reads it

| Transport | Field | Received at |
|---|---|---|
| OpenAI-compatible, non-streaming | `finish_reason: "length"` | `pkg/providers/common/common.go:331` → `normalizeFinishReason` (`:338`) |
| OpenAI-compatible, streaming | `finish_reason: "length"` | `pkg/providers/openai_compat/provider.go:377-378`, out at `:437` |
| Anthropic Messages | `stop_reason: "max_tokens"` | `pkg/providers/anthropic_messages/provider.go:418-419` |
| Anthropic | `stop_reason: "max_tokens"` | `pkg/providers/anthropic/provider.go:515` |
| Bedrock | `StopReasonMaxTokens` | `pkg/providers/bedrock/provider_bedrock.go:576` |

`pkg/agent/loop.go::isTruncatedFinishReason` (`:828`) accepts all three spellings.

```
SetLastFinishReason        → exactly one caller   (loop.go:10807)
GetLastFinishReason        → ZERO callers         (turn.go:2305)
isTruncatedFinishReason    → exactly one production caller (loop.go:817, a re-prompt to the MODEL)
```

The signal is written onto the turn and never read back.

### 2.2 Reproduced: a truncated answer is reported as an *empty* one

Observed live (`build/uat-home`, GLM 5.3, `max_tokens` lowered to 48 and again to 400):

```
WRN loop.go:11034 > Empty response from LLM, retrying  attempt=1
WRN loop.go:11110 > LLM returned empty response after retry; using fallback message
```

`loop.go:11032` tests only `strings.TrimSpace(responseContent) == ""`, never `FinishReason`. It
re-sends the identical request at `:11059` (futile) and substitutes a canned fallback at
`:11107`. A *second* fallback at `:12744-12762` does the same and calls `markTurnFailed()`.

The "reasoning tokens" explanation offered in r3 is **withdrawn** — the streaming parser
populates no reasoning field (`provider.go:433-438`). The observation stands; decisions are keyed
on `finish_reason`, not on why content was empty.

### 2.3 The copy blames the wrong party

`LLMError.yaml:78`: *"The model filled in a tool's arguments incorrectly."* It was cut off.

### 2.4 There is already a `truncated` boolean, and it means something else

`Message.yaml:94-100` / `daypartition.go:294` declare a persisted `truncated` boolean meaning
**"the user cancelled mid-stream"** — sole writer `cancel.go:558` via
`unified.go::MarkLastEntryTruncated` (`:1223`), which hardcodes `true` at `:1301` and takes no
reason. The SPA never reads it (`grep -c truncated src/lib/api.ts` → 0). `status: 'interrupted'`
is never persisted either (`sse.go:235` is dead: `h.partitions` hardcoded nil at `:71`).

`ReplayMessageFrame` is `additionalProperties: false` (`asyncapi.yaml:2813`); `DoneStats` is
`true` (`DoneStats.yaml:9`, intentional) and already has an unrelated `truncated_result_count`.

**Legacy entries (Codex pass 2, M3):** every existing `truncated: true` entry has no reason.
**Absent reason = `cancelled`.** Pinned by fixture in §7.

### 2.5 The two repair ladders cannot share a site

On the truncated-tool-call path the provider returns `nil, err` (`openai_compat/provider.go:415`),
pinned by test. The orphan ladder dereferences a live `response` (`:10807`, `:10839`, `:10943`,
`:10969`); control breaks at `:10463` ~300 lines earlier.

### 2.6 A truncated Judge verdict scores the user's work UNMET

`dispatchVerifierTurn` → `runTurn` (`verifier_adjudication.go:1752`). Unparseable partial JSON →
`failClosedProseVerdicts` (`:1555-1561`) → **every criterion `Met: false`**. Parseable-but-short →
each absent criterion `Met: false` (`:1598-1601`). **Codex pass 1 verified native Judge execution
reaches the same `runTurn`, so D6 does reach it.** External-CLI Judges take a separate path and
are a documented gap (§5).

### 2.7 `runTurn` calls the provider from THREE places (Codex C5, M4)

| Site | `loop.go` | What it is |
|---|---|---|
| Main | `:10262` inside `for retry := 0; retry <= maxRetries; retry++` | the ordinary call |
| Media-downgrade retry | `:10312` | after `TryMediaDowngrade` succeeds |
| Empty-response retry | `:11059` | inside the FR-006 recovery loop |

A design that intercepts only the first leaves two paths where truncation is handled by the old
code. **D9 exists to close this.**

### 2.8 The streamer is per-call, not per-turn (Codex C1)

`WSHandler.GetStreamer` (`websocket.go:568`) constructs a **new** `wsStreamer` on every provider
call; `setLastStreamer` (`loop.go:10161`) replaces the previous one; only the last is finalised;
and `wsStreamer.Finalize` (`:5756`) prefers its own buffer, falling back to `finalContent` only
when the buffer is empty. `finalizeStreamer` firing once per turn (`:9114`) is true and
**insufficient**: a continuation's streamer holds only the continuation's text. r4's "VERIFIED —
one bubble, one entry" was a true premise and a false conclusion.

## 3. Decisions

### D1 — A cut-off answer is reported on the message it happened to

Suffix `(cut off at the output limit)` in the existing muted footer slot. **No retry advice** —
the same request truncates identically.

| Render site | file:line | Status |
|---|---|---|
| Live assistant-ui bubble | `src/components/chat/ChatScreen.tsx:1957` | live |
| Virtualized row | `src/components/chat/ChatScreen.tsx:1528` | live |
| **`InterruptedMessageMarkers`** | `src/components/chat/ChatScreen.tsx:982` | **live, E2E-visible** — skip it and the notice is untestable |
| `MessageItem` footer | `src/components/chat/MessageItem.tsx:246` | test-only (`ChatThread` has no non-test importer); update for consistency |

**Precedence:** `cancelled` beats `max_output_tokens`. Render one suffix, never two.

### D2 — `truncated` keeps its meaning; the reason is a narrow enum; two carriers ship

- `Message.truncated` keeps meaning "incomplete"; gains a second writer.
- New `Message.truncation_reason`: `cancelled` | `max_output_tokens`. Absent = `cancelled`
  (legacy, §2.4).
- **`MarkLastEntryTruncated(sessionID, turnID string, reason string)`** — new third parameter.
  `cancel.go:558` passes `cancelled`. **Breaks 8 test call sites** (`pkg/session/unified_test.go`
  ×7, `pkg/gateway/websocket_producer_agent_id_test.go:312`) — update them to pass `cancelled`,
  preserving every existing assertion.
- **Carriers shipped:** `Message` (REST cold-load) and **`ReplayMessageFrame`** (WS replay) —
  both gain `truncated` + `truncation_reason`. The `ReplayMessageFrame` edit MUST land in the
  **inline generating copy** in `contracts/asyncapi.yaml:2800` (its `additionalProperties: false`
  at `:2813`), not only `contracts/components/schemas/ReplayMessageFrame.yaml`.
- **`DoneStats` is NOT extended.** The SPA reads no `DoneStats` field today; the live bubble picks
  the notice up on reattach via replay. Dropping it also removes the turn-scope hazard (a per-turn
  flag on a per-call event).
- **Replay must emit annotated empty entries (Codex C8).** `replay.go:352` gates on
  `entry.Content != ""`; an entry with `Truncated && Content == ""` must pass through, and the SPA
  must render a suffix with no body.
- **Non-streamed live done** (`webchat_channel.go:111`, no `Stats` object) is a documented gap.

### D3 — A truncated tool call gets one bounded repair per round before the turn dies

Insertion: `runTurn`, immediately after `response, err = callLLM(...)` at `:10262` — **and
identically at the two other call sites, via D9.** Before `ClassifyError` (`:10432`) and before
`synthesizeImageRejection` (`:10269`).

1. **Keyed on `errors.Is(err, common.ErrToolArgumentsUndecodable)`** — survives
   `FallbackChain.Execute` (`types.go:116`, `fallback.go:413`, `:564`); **pin with a test.**
2. **Gate on `StreamedContentLen() == 0`** (`loop.go:10487` precedent). Repair only when nothing
   was streamed; otherwise fall through to the terminal path. Prevents duplicated prose.
3. **Gate on `retry < maxRetries`** (Codex pass 1 minor) — a `continue` on the last retry exits
   the loop without making the promised call. Do not count or announce a repair that cannot run.
4. **Bound: ONE repair per `turnLoop` round**, counter reset at the top of each round. The
   `continue` consumes a retry; a second identical re-prompt carries no new information.
5. **Append to a fresh copy of `callMessages`** —
   `append(append([]providers.Message(nil), callMessages...), note)` (`:9867` idiom). Never
   `messages`; never bare `append` (aliasing via `:9797`/`:9802`).
6. **Reuse the copy** at `loop.go:818-819`. Factor it out.
7. **Never append the refused response to history** (protects `anthropic/provider.go:307-336`).
8. **Re-check `ts.hardAbortRequested()` explicitly** before the repair call; `continue` skips
   `sleepWithContext`.
9. **Account the refused attempt's usage (Codex pass 2, M1).** `parseStreamResponse` discards
   `Usage` when it returns `nil, err` (`:415` vs `:434-438`). The refused call was billed by the
   provider; carry its usage on the error (see D5's evidence type) and debit it once.
10. **`anthropic_messages` is not covered** (assigns `block.Input` directly, `provider.go:404`).

### D4 — Truncation on the success arm: one branch, three outcomes

Placed **ahead of** the empty-response loop at `:11032`, guarded by `len(response.ToolCalls) == 0`:

```
if isTruncatedFinishReason(response.FinishReason) && len(response.ToolCalls) == 0 {
    if strings.TrimSpace(responseContent) == "" {
        → D4a  no retry, no fallback, no turnFailed; persist zero-content entry stamped
               truncated/max_output_tokens; end turn
    } else if continuationEligible() {          // D6 — ALL of: count < 2, iteration capacity
        → D6   remains, !gracefulTerminalUsed, context admits, no pending cancel
    } else {
        → D4b  annotate the ACCUMULATED answer (D6.1) truncated/max_output_tokens; end turn
    }
}
```

**D4a edits BOTH fallbacks** (`:11107` and `:12744-12762`) and suppresses `markTurnFailed` on
this path at both. The zero-content entry is what carries the annotation (nothing else exists).

**Truncated AND has complete tool calls** (Codex pass 1 major): reachable — `parseStreamResponse`
returns success when all collected arguments decode, regardless of `finishReason`. Decision:
**execute the validated calls exactly once, and carry the truncation forward as pending
(D6.1).** If the next model round completes normally the pending state clears; if the turn ends
without a further completed text round, D4b annotates. Never re-execute a tool call because of a
continuation.

### D5 — The error copy tells the truth, and can tell truncation from a wrong shape

New `LLMError` code **`tool_call_truncated`**, attribution `model`: the call was cut off at the
output limit; ask for less in one step. `tool_args` stays for genuinely malformed arguments.

**`ErrToolArgumentsUndecodable` does not prove truncation (Codex C7)** — it also fires for valid
JSON of the wrong shape (`42`, `true`, `[1,2,3]`; `common.go:444`, pinned by
`TestDecodeToolCallArguments_NonObjectRefused`). Therefore:

- `pkg/providers/common` gains a typed wrapper carrying **truncation evidence**:
  `finish_reason` as received, plus the refused attempt's `Usage` (D3.9). Set only when the
  parser observed `length`/`max_tokens`, or the fragment is EOF-shaped (`{`, `{"key`) — never on
  a well-formed non-object.
- **`TranslateTurnError` (`translate_error.go:739`) gains a sentinel branch:** with evidence →
  `tool_call_truncated`; without → `tool_args`.
- **The terminal path (`loop.go:10743`) and the empty-retry error path (`:11087`) switch to
  `TranslateTurnError`** — but its fallback must **preserve structured HTTP classification**
  (Codex C6): keep `errorToProviderError(err)` and route the fallback through
  `TranslateLLMError(pe, msg)`, not `TranslateLLMError(nil, msg)`. A 401/413 must still classify
  correctly.
- **Both error enums** (Codex C9): `LLMError.yaml` **and** `LLMErrorReplay.yaml` **and** the
  inline asyncapi copy at `asyncapi.yaml:1774`. `src/lib/llm-error.test.ts:55` asserts
  `ALL_CODES` equals the generated enum exactly — update `ALL_CODES` and `codeToDisplay`.

### D6 — Auto-continue: built, bounded at 2, content-gated, with an explicit accumulator

One rule everywhere (chat, tasks, plans, goals, delegated sub-turns, native Judge).

1. **The continuation accumulator is the load-bearing object (Codex C1, #10).** A turn-scoped
   `continuationAccum` on `turnState` (mutex accessors, matching `turn.go` convention for state
   read at finalize time) holding the concatenated answer across calls. It is:
   - the value returned in `turnResult.finalContent`;
   - what `finalizeStreamer` passes to `Finalize`, **and `Finalize` must prefer it over its own
     buffer whenever the turn had ≥1 continuation** (new `streamerContinuationSetter` optional
     interface alongside `streamerFailedSetter`, `turn.go:1446`);
   - what D4b annotates;
   - what every terminal exit preserves (below).
   **Live stream:** on a continuation, `callLLM` MUST reuse `ts.lastStreamer` rather than
   acquiring a new one, so tokens keep flowing to the same bubble and the buffer accumulates.
   Pin: prefix + suffix both persisted; browser shows each token exactly once.
2. **Content gate:** `strings.TrimSpace(responseContent) != ""`.
3. **Bound `maxTruncationContinuations = 2`**, turn-scoped, declared beside
   `orphanToolMarkupRepairs` (`:9519`).
4. **Iteration capacity (Codex C4):** eligible only if `ts.currentIteration()+1 < MaxIterations`.
   Otherwise D4b, never the `toolLimitResponse` fallback.
5. **Never after a graceful stop (Codex C3):** ineligible once `markGracefulTerminalUsed()` has
   run. Cancellation beats recovery. Pin: graceful stop → truncated terminal → **no tools return**.
6. **Context admission (Codex pass 2, M2):** `continue turnLoop` does **not** re-run the
   proactive `windowTrim` (`:9455` is above the label at `:9521`). Before continuing, run the
   existing mid-turn check; if the request cannot fit, D4b with the partial. **The continuation
   chain (partial + instruction) is never a trim victim.**
7. **History rebuilds must restore the chain (Codex C2):** context-overflow recovery (`:10712`)
   and timeout recovery (`:10559`) rebuild from `Sessions.GetHistory`. The chain lives in the
   accumulator, not only in `messages`; after any rebuild it is re-appended exactly once. The
   continue instruction is **not** persisted as a user-role message (`parseTurnBoundaries`,
   `context_budget.go:22`, would treat it as a new turn and could evict the partial while keeping
   the instruction).
8. **Every terminal exit preserves the partial (Codex #10):** rate-limit denial (`:9563-9580`),
   auth failure, deadline, exhausted retries, `typedTurnExit` (`:12856`), hard cancel. A pending-
   incomplete state is set when a continuation begins and cleared only on successful completion;
   each exit annotates the accumulator and keeps its own real error. A refused continuation makes
   no provider call and consumes no allowance.
9. **Append partial + instruction to `messages`, `continue turnLoop`.** Instruction: *"Your
   previous message was cut off at the output-token limit. Continue from exactly where it
   stopped. Do not repeat any text you already wrote, and do not restate or summarise it."*
10. **A successful continuation emits no notice** and clears the pending state.
11. **Cost:** up to 2 extra rounds; with retries and fallback candidates the request count can be
    higher. Continuations enlarge the permitted overshoot of the application budget within an
    active turn (which stops at turn boundaries by design).

### D7 — The `b12dfa83` comment is corrected

`common.go:355-364`: `finish_reason` is reliable on the primary transports; vLLM
(`vllm#47903`) is demoted to "a minority of OpenAI-compatible servers get this wrong"; parsing
arguments is cheap local insurance. Comment only.

### D8 — Delete `Get/SetLastFinishReason`, record the gap they named

`turn.go:289`, `:2305`, `:2312`; `loop.go:10807`. No test references. The write-site comment
(*"for SubTurn truncation detection"*) names a requirement never built — §5.1.

### D9 — One outcome handler for every provider call

`runTurn` calls the provider at three sites (§2.7). D3, D4, D5 and D6 are implemented **once**,
in a single `handleProviderOutcome(response, err, site)` applied at all three, so a truncation on
the media-downgrade retry or the empty-response retry is handled identically to the main call.
The empty-response retry's own error path (`:11087`) is subsumed. **Pin: truncation on each of the
three sites reaches the same branch.**

## 4. Consequences

- The SPA reads `truncated` for the first time; the stale `Message.yaml:97-99` description is
  fixed in the same change.
- A truncated Judge verdict can finish rather than scoring work unmet (§2.6).
- A cut-off answer costs more and arrives complete; a cut-off tool call gets one repair; a denied
  continuation keeps what was written.
- `EventKindLLMRetry` stays log-only. D3/D6 are invisible while they run — accepted.

## 5. Known gaps, stated

1. **Delegated sub-turns:** a truncated child still reports `"Subagent task completed"`
   (`delegate.go:2343`); `turnResult` has no truncation field. D6 reduces, does not close.
2. **Non-webchat channels:** `bus.OutboundMessage` carries only `Content`; no notice.
3. **Unwatched turns** (cron/heartbeat/task/plan): `turnResult.turnFailed` has zero production
   readers; no signal leaves the webchat WS.
4. **Non-streamed live done:** `DoneFrame` has no `Stats`.
5. **External-CLI Judges** bypass D6 (`verifier_adjudication.go:1692`).
6. **`getSessionMessages` bypasses generated types** (`rest.go:1017-1021`). Own issue.
7. **Fallback candidates share request options**; a smaller failover model gets the same
   `max_tokens`. Pre-existing; D6 increases exposure.

## 6. Non-goals

Changing default `max_tokens`; the anthropic outbound exception; live retry visibility; a
setting for auto-continue; extending `DoneStats`.

## 7. Test obligations

1. **Contract fixtures:** seed `sessions_wire_test.go` with `Truncated`+reason set; extend
   `TestContract_ReplayMessageFrame_Populated` (`contract_test.go:670`); pin absent-reason =
   `cancelled` on cold-load and replay.
2. **SPA six-layer plumbing:** `schemas.ts:2004` (generated, silent strip), `RawMessage`
   (`api.ts:1372`), `rawToMessage` (`:1470-1484`), `MessageBase`/`AssistantMessage`, `ChatMessage`
   (`store/chat.ts:150`), replay reducer (`:5445-5460`). Test each layer passes the field.
3. **Sentinel survives `FallbackChain.Execute`** (D3.1).
4. **Accumulator:** prefix+suffix persisted; tokens shown once; Finalize prefers accumulator.
5. **D4a double-fallback:** fails if `:11107` or `:12744` re-substitutes on a truncated-empty turn.
6. **Graceful stop → truncated terminal → no tools** (D6.5).
7. **Denied continuation keeps the partial:** partial→rate denial, →auth failure, →deadline,
   →hard cancel (D6.8).
8. **Iteration cap:** truncation on the last permitted iteration → D4b, not `toolLimitResponse`.
9. **All three call sites** reach the same branch (D9).
10. **`TestAgentLoop_EmptyModelResponseUsesAccurateFallback` (`loop_test.go:2516`) MUST survive
    unchanged** — it supplies empty content with a *normal* finish reason.
11. **`ALL_CODES` test** updated for `tool_call_truncated`.
12. Mutation checks per fix, restored byte-identical.

## 8. Rejected alternatives

| Alternative | Why rejected |
|---|---|
| Reuse `truncated` alone | Conflates cancel with cap. |
| `ErrorFrame` | Terminates the turn in the SPA; separate bubble. |
| `DoneStats.turn_failed` / extend `DoneStats` | Wrong semantics; SPA reads none of it; per-turn flag on a per-call event. |
| Share one ladder with orphan-markup | `response` is nil on the error path. |
| Defer auto-continue (r1–r3) | Overruled; §2.6 shows the deferral left the Judge scoring work unmet. |
| Auto-continue without content gate / iteration check / graceful-stop check | Each reintroduces a loop, a fallback overwrite, or a safety regression. |
| Intercept only the main call site | Two other sites bypass everything (§2.7). |
| Switch terminal path to `TranslateTurnError` with a nil-provider fallback | Loses 401/413 classification. |
| Treat `ErrToolArgumentsUndecodable` as proof of truncation | Mislabels `42` as cut off. |

## 9. Implementation plan — work packages

File ownership is exclusive; interfaces are fixed here so packages run in parallel.

| WP | Owner files | Depends on |
|---|---|---|
| **A Contracts** | `contracts/**`, `pkg/api/generated/**`, `src/lib/api/generated/**`, `pkg/gateway/inboundschemas/**`, `src/lib/llm-error.ts`, `src/lib/llm-error.test.ts` | — (first) |
| **B Session** | `pkg/session/**`, `pkg/agent/cancel.go` (one line) | A |
| **C Loop core** | `pkg/agent/loop.go`, `pkg/agent/turn.go`, `pkg/agent/*_test.go` for these | A, interfaces from D/E |
| **D Errors** | `pkg/agent/translate_error.go` (+test), `pkg/providers/common/common.go` (evidence type; **D7 comment**), `pkg/providers/openai_compat/provider.go` (attach evidence+usage) | A |
| **E Gateway** | `pkg/gateway/websocket.go` (wsStreamer), `pkg/gateway/replay.go`, `pkg/gateway/*_test.go` | A, B |
| **F SPA** | `src/**` except generated and llm-error | A |

**Fixed interfaces (do not deviate):**

- D→C: `translate_error.go` exports `TranslateTurnError(err error) LLMError` handling
  `*common.ToolArgumentsError` → `CodeToolCallTruncated` when `.Truncated`, else `CodeToolArgs`;
  fallback preserves `errorToProviderError`. `common.ToolArgumentsError{Cause error; FinishReason
  string; Truncated bool; Usage *UsageInfo}` implements `Unwrap()` → `ErrToolArgumentsUndecodable`.
- E→C: `wsStreamer` implements `SetContinuationContent(full string)`; when set, `Finalize` writes
  `full` (not its buffer). `turn.go` declares the optional interface `streamerContinuationSetter`.
- B→C/E: `MarkLastEntryTruncated(sessionID, turnID, reason string) error`; `TranscriptEntry`
  gains `TruncationReason string \`json:"truncation_reason,omitempty"\``.
- A→all: generated types only; constants `CodeToolCallTruncated LLMErrorCode = "tool_call_truncated"`;
  enum values `cancelled`, `max_output_tokens`.

**Definition of done per WP:** gofmt clean; `go build -tags goolm,stdjson ./...` exit 0;
golangci-lint with `--max-issues-per-linter=0 --max-same-issues=0` on owned packages exit 0;
named tests for each §7 obligation the WP owns, run narrowly with exit codes captured without a
pipe; one mutation check per fix restored `diff -q` identical; **no `Co-Authored-By` trailer**;
report with evidence, not claims.

## 10. Revision history

- **r1** initial; auto-continue deferred. **r2** operator overruled: built everywhere. **r3**
  citation audit: 9 line numbers + 3 claims fixed. **r4** design grill: 5 blockers fixed; Judge
  case (§2.6) added. **r5** Codex ×2 (high effort): 10 blockers + 8 majors folded in — the
  accumulator (D6.1), three call sites (D9), graceful-stop precedence (D6.5), denied-continuation
  preservation (D6.8), truncation evidence distinct from undecodability (D5), both error enums,
  replay of empty annotated entries, `windowTrim` not re-run on `continue`, legacy reason
  default, and the enumerated breaking tests. Two r4 "verified" claims withdrawn (§2.8, D6.6).
