# ADR-059: Delegation observability — tell a working subagent apart from a hung one

- **Status:** Proposed
- **Date:** 2026-08-10
- **Related:** [ADR-057](ADR-057-session-parent-child-parity.md) (session parent/child parity; FR-043 status snapshots), [ADR-058](ADR-058-tool-denial-semantics.md) (what the system *tells the model* when a call is refused — the governing precedent for D4), [ADR-032](ADR-032-external-agent-workspace-execution.md) (delegation identity — a sub-turn runs as the target agent's own instance), [ADR-036](ADR-036-consolidate-shell-and-subagent-tools.md) (`bash` consolidation)
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1 — forensic analysis of a real UAT session (`session_01KZJSVAX1QWYFFRA29HEHYGYP`, 2026-08-09, `omnipus-uat-swimlane`), reading the persisted transcripts of the parent and all 11 subagent sessions off the running machine, plus direct source verification on `release/v0.1.1`. Every behavioural claim below carries either a transcript figure or a `file:line`. Claims marked **[INFERRED]** were not directly reproduced.

> **Scope note.** This ADR decides one thing: how a delegating agent learns whether a delegated worker is *making progress* or is *stuck*. It does not change delegation identity (ADR-032), the session model (ADR-057), or any storage layout. It does introduce one provider-layer contract.

---

## 1. Context

### 1.1 The evidence

A UAT operator asked the orchestrator (Jim) to produce three artifacts — two SVGs and one Markdown document — via parallel subagents. Reading the persisted transcripts directly:

- The whole episode lasted **114 seconds** (08:48:51 → 08:50:45).
- Jim issued **232 tool calls**. **132 of them were `delegate`** — of which **75 were `status` polls**, 7 `peek`, 7 `inbox`, 4 `steer`, 28 `cancel`.
- Jim spawned **11 subagents to produce 3 files**.
- **All three artifacts were complete at 08:50:03**, 72 seconds in. The remaining 42 seconds were spent thrashing over finished work.

The two wave-1 SVG workers (`b5a76216`, `4259f3c6`) each ran for **46.6 seconds** and, from every surface Jim could observe, did **nothing at all**: no transcript rows, no inbox messages, no state change beyond `running`. Jim concluded they had stalled and cancelled them.

Their own transcripts say otherwise. Each contains exactly one assistant message:

> `b5a76216`: *"Creating a clean workspace-dashboard SVG and saving it to `workspace-card.svg`."*

That is the narration preceding a tool call whose arguments — a multi-kilobyte SVG document — were still being generated. **The workers were working.** They were killed for it.

### 1.2 The control case — this is not model indiscipline

In the same session, the Markdown worker (`9bdc7376`) received a comparable prompt, wrote its file at 08:49:04, and completed in 16 seconds. Wave-2 SVG workers, given prompts constrained with *"Keep it concise (under 100 lines of SVG)"*, completed in 13–16 seconds.

Same session, same model, same worker agent. **The difference is output length, and output length is invisible.** A worker producing a long tool argument is indistinguishable from one that has hung.

### 1.3 Root cause, read in source

Three layers each independently lose the signal.

**Layer 1 — the provider never emits it.** In `pkg/providers/openai_compat/provider.go`, the streaming callback `onChunk` is invoked *only* inside the text-delta branch (`:333-337`). Tool-call argument deltas are handled in a separate block (`:340-358`) that appends to an accumulator and calls nothing. While a multi-kilobyte argument streams in, **zero bytes reach any observable surface**.

The native Anthropic provider was worse: it implemented no `ChatStream` at all (`pkg/providers/anthropic/provider.go:31-38`), so the agent loop's `StreamingProvider` assertion (`loop.go:8334`) failed and the whole call was a black box.

**Layer 2 — nothing records liveness.** `session.LifecycleState` (`pkg/session/lifecycle.go:61-75`) has exactly 8 values — `queued, running, needs_input, paused, completed, failed, cancelled, timed_out`. There is no intermediate "generating" state and no last-activity timestamp that advances during a turn. `DelegateTaskState.LastStatusRead` (`pkg/tools/delegate.go`) is stamped when the **parent polls** — it tracks the observer, not the worker.

**Layer 3 — the read surfaces only see completed work.** `delegate action=status` (`delegate.go:2251-2369`) resolves recent activity via `recentActivityLines` (`:2484-2569`), which reads `session.TranscriptEntry` rows **already persisted to disk**. Those rows are written only at full-LLM-round completion (`loop.go:9260`, `appendIntermediateAssistantTranscript`). `peek` (`:3557-3616`) reads the lifecycle enum plus any checkpoint the child *chose* to send via `message_parent`. `inbox` (`:2768-2840`) drains only child-initiated messages.

So a child that is mid-generation has written nothing, sent nothing, and changed no state. All three surfaces correctly report silence. **Silence is the only thing they can report, and it means two opposite things.**

### 1.4 Why the existing controls did not help

- **`steer` had no effect.** Jim sent 4 steers at 08:49:29–30; nothing changed. A steer is consumed at a round boundary, and the workers were inside a round.
- **Cancel-and-retry reproduced the problem.** Jim spawned 6 replacements for 2 remaining files. Duplicates then collided on `write_file`'s no-overwrite guard, and those collisions were reported as failures — so a *sibling's success* read as *this worker's failure*, prompting yet more spawns.
- **`bash` was 100% dead** (a separate defect, fixed on `fix/uat-delegation-rootcauses`), so Jim could not simply `ls` and see the files already existed.

The delegation controls behaved correctly given their inputs. The inputs were wrong.

---

## 2. Decision

### D1 — Progress is a provider **capability**, expressed as an opt-in interface

Providers that can report tool-argument progress declare it via a narrow interface, following the codebase's established pattern for optional provider behaviour (`StreamingProvider`, `ThinkingCapable`) and the channel layer's capability family (`TypingCapable`, `MessageEditor`, …):

```go
// ToolCallProgressCapable is implemented by streaming providers that can
// report forward progress while a tool call's arguments are still arriving.
type ToolCallProgressCapable interface {
    SetToolCallProgressHandler(protocoltypes.OnToolCallProgress)
}
```

**Rejected: carrying the callback in the existing `options map[string]any`.** That map is for *model parameters* — values sent to the API (`max_tokens`, `temperature`, `prompt_cache_key`, `thinking_level`). A Go closure is not a model parameter; it is a capability handshake. Putting it there gives up everything the type system was doing:

- a provider that never reads the key is indistinguishable from one that does — no compile error, no runtime error, **no signal** (the precise silent-degradation failure this ADR exists to end);
- the accessor must defensively accept two type shapes and return `nil` on anything else, which is a silent failure by construction;
- `options` is passed through `hooks.BeforeLLM`, which may replace the map wholesale (`loop.go:~8232`), dropping the callback with no diagnostic.

An interface is compile-checkable and supports the same compliance assertion already used for `StreamingProvider`.

### D2 — Progress reports *liveness*, never content

`ToolCallProgress` carries the tool name, a provider-scoped index, and **byte counts only**. It never carries argument content. Arguments are frequently large and may contain sensitive material; the only question a consumer needs answered is *"is this still moving?"*.

`Index` is **provider-defined and stable only within one stream** — OpenAI's delta index and Anthropic's content-block index are not the same ordinal. Consumers must not treat it as a tool-call ordinal.

### D3 — Liveness is recorded on the delegate task state, and rendered by `status`

The agent loop installs a progress handler scoped to the child's spawn call. The handler updates a liveness record — last-progress timestamp and accumulated argument bytes — on `DelegateTaskState`, which `DelegateTool` already owns and mutex-protects.

`delegateStatusExtra` renders it **when `recentActivityLines` is empty** — i.e. exactly the case that previously produced silence:

> `generating tool arguments — 14.2 KB, last progress 1.3s ago`

This makes "still working" and "silent for 90 seconds" visibly different, which is the entire point. The signal crosses `pkg/agent` → `pkg/tools`; the loop owns the signal, `DelegateTool` owns the state, and the handler is the designed channel between them.

**Handler discipline.** The handler fires on every argument delta of a live stream. It MUST be cheap, non-blocking, and must not panic — a panic would unwind through the SSE read loop and take down the turn, which is strictly worse than the blindness being fixed. Providers invoke it synchronously; the implementation is responsible for its own throttling.

### D4 — Machine-readable outcomes follow ADR-058, and the consumer is the CALLING agent

A refusal that is *not* a failure — most concretely `write_file`'s "already exists" — must be distinguishable by the agent that **made the call**. Per ADR-058, the discriminator belongs **inside the tool-result text that agent receives**, as structured JSON, not in a Go struct field no model can see.

**The consumer is the worker, not the orchestrator.** A tool result is delivered to the agent that invoked the tool. In a delegated flow that is the *subagent*. The correct chain is:

1. the worker calls `write_file` and the file already exists;
2. the worker receives a result it can unambiguously read as *precondition already satisfied*, not *write failed*;
3. the worker concludes its task is effectively done and **reports that back to its parent in its own words**.

The orchestrator learns through the worker's ordinary report. **No special channel from the tool to the parent is required, and none should be built.** Delegation already has a reporting path; this decision only has to make the worker's own read of the result unambiguous.

Corollary: surfacing persisted failure text to a *parent* agent (e.g. via `inspect_session`) is a debugging and forensics convenience, not the mechanism by which a parent learns an outcome. It should not be justified as closing this gap.

**Rejected: a `Reason` field on `ToolResult`.** Implemented earlier on this branch and superseded here before use. It is unreachable by any model: `ToolResult` is not serialised across the model boundary, so an agent sees only `ForLLM`, whose prose was unchanged. A Go field cannot fix a language model's discrimination problem.

**Evidence limitation.** In the 2026-08-09 session all five workers that hit "already exists" were cancelled within seconds of the failed call — every one of their transcripts ends at the errored `write_file` with no assistant turn after it. So the incident shows the ambiguity being *created*, but never shows a worker acting on it. The step this decision improves was not reached. **[INFERRED]** that a clearer result would have produced a correct report.

---

## 3. Consequences

**Positive.** A polling orchestrator can distinguish generation from a stall, which removes the cause of the cancel/respawn amplification. The capability interface makes a missing implementation a build error rather than silent degradation. Byte-count-only reporting adds no new data exposure.

**Negative / accepted.**
- One new provider interface; each streaming provider must implement it or explicitly not.
- Per-delta handler invocation on the hot streaming path. Bounded by D3's discipline; measured before merge.
- Anthropic-backed **child** turns will now acquire the parent's streamer and push text into the parent's WS stream where previously they did not. This is parity with `openai_compat` (which already does this), but it is a real user-visible change on the Anthropic path and requires UAT, not just a compile-time assertion. **[INFERRED]** — reasoned from `spawnSubTurn` passing `parentTS.channel`/`chatID` and `GetStreamer` keying on `chatID`; not yet observed live.

**Not decided here.** Whether `steer` should be deliverable mid-round; whether an unbounded-output worker should be bounded by mechanism rather than prompt guidance; whether `inspect_session` should surface persisted failure reasons to a parent agent (it currently drops them — a real gap, tracked separately).

---

## 4. Verification

This ADR is only satisfied when a test proves the **outcome**, not the plumbing:

1. A delegated worker mid-tool-argument-generation is reported by `delegate status` as making progress, distinguishably from an idle child. This must fail on the pre-fix code.
2. A test asserting the agent loop actually installs the handler — the only test that would notice the wiring being dropped.
3. A provider-level test per implementing provider that progress fires **between** block start and block stop. (The Anthropic emitter initially read `AsToolUse()`, which re-unmarshals from a snapshot the deltas never update, and therefore emitted 2 bytes and then nothing until the block had already finished. It was green against an interface-compliance test. That is the failure mode this clause exists to prevent.)
4. Race coverage: the liveness record is written from the provider stream goroutine and read from the polling goroutine.

A green test that does not exercise a production caller does not satisfy this ADR.
