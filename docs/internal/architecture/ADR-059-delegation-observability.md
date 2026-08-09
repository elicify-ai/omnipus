# ADR-059: Delegation observability — tell a working subagent apart from a hung one

- **Status:** Proposed
- **Date:** 2026-08-10
- **Related:** [ADR-057](ADR-057-session-parent-child-parity.md) (session parent/child parity), [ADR-058](ADR-058-tool-denial-semantics.md) (what the system *tells the model* when a call is refused — governing precedent for D4 and for §7), [ADR-032](ADR-032-external-agent-workspace-execution.md) (delegation identity — a sub-turn runs as the target agent's own instance), [ADR-036](ADR-036-consolidate-shell-and-subagent-tools.md) (`bash` consolidation)
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1 for source claims — every `file:line` below was read on `release/v0.1.1` @ `ae93a45e` and can be re-run by any reviewer. **Evidence level 3 for the incident figures:** they were extracted from session files on a deployment with **no volume**, which has since been redeployed, so they are author-extracted and **not independently reproducible**. They are recorded in [`docs/internal/uat/delegation-observability-2026-08-09.md`](../uat/delegation-observability-2026-08-09.md) and cited by section below. Claims marked **[INFERRED]** were not directly observed.

> **Scope note.** This ADR decides how a delegating agent learns whether a delegated worker is making progress or is stuck, and how a tool tells its caller that a refusal is not a failure. It changes one provider interface (D1) and no storage layout. It does not change delegation identity (ADR-032) or the session model (ADR-057).

---

## 1. Context

### 1.1 The evidence

Full report: [UAT 2026-08-09](../uat/delegation-observability-2026-08-09.md) §1–§3.

An operator asked the orchestrator (Jim) for three artifacts via parallel subagents. The turn ran
**114 seconds** and issued **232 tool calls**, of which **132 were `delegate`** — 11 `run`, **75
`status`**, 7 `peek`, 7 `inbox`, 4 `steer`, 28 `cancel` (UAT §2). **11 subagents were spawned to
produce 3 files** (UAT §3).

**All three artifacts were complete at 08:50:03 — 72 seconds in.** The remaining 42 seconds were
spent over finished work (UAT §1).

Two wave-1 workers each ran **46.6 seconds** and, on every surface the parent could observe, did
nothing. Their own transcripts each hold exactly one assistant message — the narration preceding a
tool call whose multi-kilobyte SVG argument was still streaming (UAT §3.1). They were cancelled for
it. **They were working.**

### 1.2 The control case — this is not model indiscipline

Same session, same model, same `worker` agent (UAT §3.2). The Markdown worker finished in 16 s. The
wave-1 SVG workers, given prompts with **no size limit**, went silent for 46.6 s and were killed.
Wave-2 workers, given *"Keep it concise (under 100 lines of SVG)"*, finished in **13–16 s**.

One variable: output length. **Output length is invisible.**

### 1.3 Root cause, read in source

Three layers independently lose the signal. All citations verified on `release/v0.1.1` @ `ae93a45e`.

**Layer 1 — the provider never emits it.** In `pkg/providers/openai_compat/provider.go`, the
streaming callback `onChunk` is invoked *only* in the text-delta branch (`:333-337`). Tool-call
argument deltas are handled separately (`:340-358`) and call nothing. While a multi-kilobyte argument
streams, **zero bytes reach any observable surface**.

The native Anthropic provider was worse: no `ChatStream` method existed anywhere in
`pkg/providers/anthropic/` on that baseline (verified by search, not by line range), so the agent
loop's `StreamingProvider` assertion at `loop.go:8335` failed and the entire call was opaque.

**Layer 2 — nothing records liveness.** `session.LifecycleState` (`pkg/session/lifecycle.go:61-75`)
has exactly 8 values — `queued, running, needs_input, paused, completed, failed, cancelled,
timed_out`. There is no intermediate "generating" state and no advancing last-activity timestamp.
`DelegateTaskState.LastStatusRead` is stamped when the **parent polls** — it tracks the observer, not
the worker.

**Layer 3 — the read surfaces only see completed work.** `delegate action=status`
(`pkg/tools/delegate.go:2251-2369`) resolves activity via `recentActivityLines`
(`delegate.go:2532-2569`), which reads transcript rows **already persisted to disk**; those are
written only at full-LLM-round completion (`loop.go:9260`). `peek` (`delegate.go:3557-3616`) reads the
lifecycle enum plus any checkpoint the child *chose* to send. `inbox` (`delegate.go:2768-2838`) drains
only child-initiated messages.

A child mid-generation has written nothing, sent nothing and changed no state. All three surfaces
correctly report silence. **Silence is all they can report, and it means two opposite things.**

### 1.4 Why the existing controls did not help

Four independent inputs fed the cancel/respawn loop:

1. **`steer` had no effect.** 4 steers at 08:49:29–30 changed nothing; a steer is consumed at a round
   boundary and the workers were inside a round (UAT §1).
2. **Cancel reported routine cleanup as breakage.** `executeCancel` returned `ErrorResult` for an
   already-terminal session — **20 of the 28 cancel calls** (UAT §2). The caller re-issued cancels and
   re-spawned workers instead of reading "already done" as success. Fixed on this branch (`00e1d7f5`);
   see D5.
3. **`write_file` collisions read as this worker's failure.** Five duplicates lost a race to a sibling
   that had already succeeded and received `"already exists"` (UAT §3.3). See D4.
4. **`bash` was 100% dead** — all 11 calls failed, the first being `sleep 8` seven seconds in with
   ~3 GB free and loadavg 0.00 (UAT §4). `RLIMIT_NPROC` was sized in processes while the kernel
   enforces against tasks; fixed in `a79976c4` (`pkg/sandbox/hardened_exec_linux.go`), hardened in
   `f7d0f4bf` and `29ab227f`. The parent therefore could not `ls` and self-correct.

---

## 2. Decisions

### D1 — Progress is a **per-call parameter** on the streaming interface

`StreamingProvider.ChatStream` gains an `onProgress protocoltypes.OnToolCallProgress` parameter,
alongside the `onChunk` callback it already carries as a per-call parameter
(`pkg/providers/types.go:43-52`).

**Requirement this must satisfy: per-call scoping under concurrency.** Parallel delegations to the
same target agent run on that agent's instance (ADR-032), and `AgentInstance.Provider` is a single
shared pointer returned directly by `GetProviderForCandidate` on the unpinned path
(`pkg/agent/instance.go:437-447`); `providerPool` entries are likewise shared. The ADR's own wave-1
pair would therefore share one provider object.

**Rejected: a setter-style capability interface** (`SetToolCallProgressHandler`). It mutates state on
a shared provider instance, so it is last-writer-wins between concurrent turns — worker A's progress
lands in worker B's record or is dropped. It cannot express D3's per-child scoping at all. This was
the author's first proposal and it is wrong.

**Rejected: the `options map[string]any` key** (`OnToolCallProgressKey`), which is what currently
ships (`pkg/providers/protocoltypes/progress.go`, `c36c28a1`). It *is* correctly per-call, so it does
not have the defect above, and it is the reason the delivered implementation is sound. But it is
untyped: a provider that never reads the key is indistinguishable from one that does, the accessor
must accept two type shapes and return `nil` on anything else, and `hooks.BeforeLLM` can replace the
map wholesale (`loop.go:~8244`), which the delivered code has to defend against explicitly. A
parameter is compile-enforced for every implementer with none of that.

**Correction to an earlier draft:** this ADR previously claimed a capability interface makes a missing
implementation "a build error rather than silent degradation". **That is false.** Optional capabilities
in this codebase are consulted by runtime type assertion (`loop.go:8335`, and `ThinkingCapable` at
`:8207`); a non-implementer fails the assertion silently. The only compile signal is a hand-written
`var _ I = (*T)(nil)`, and on this branch that assertion lives in a **test** file, so it is not a
`make build` error. A per-call parameter is the one shape that *is* compile-enforced — which is the
actual argument for D1, and the reason the claim has been rewritten rather than deleted.

### D2 — Progress reports *liveness*, never content

`ToolCallProgress` carries the tool name, a provider-scoped index and **byte counts only**. Never
argument content: arguments are frequently large and may be sensitive, and the only question a
consumer needs answered is *"is this still moving?"*. `ArgsBytes` is a length, not a digest — it
discloses size and nothing else.

`Index` is **provider-defined and stable only within one stream**. OpenAI's delta index and
Anthropic's content-block index are different ordinals (Anthropic's counts text blocks too).
Consumers MUST NOT treat it as an ordinal over tool calls. The doc comment on
`protocoltypes.ToolCallProgress` currently says *"the tool call's position in the response (OpenAI's
delta index)"* and must be corrected to match (W4).

### D3 — Liveness is a turn-scoped record, read by `delegate status` through a pull seam

The agent loop installs a progress handler for the turn. The handler writes a liveness record —
last-progress timestamp, accumulated argument bytes, tool name — held **on `turnState`** as plain
atomics, not on `DelegateTaskState`. Per-turn by construction, so concurrent turns write their own
record and never another's, which is what satisfies D1's concurrency requirement end to end. Plain
atomics rather than the turn's `RWMutex`: this is a per-delta hot path needing only eventual
consistency, and contending `ts.mu` for a monitoring signal would slow every other consumer.

`DelegateTool` reads it through a narrow pull interface (`DelegateProgressReader`), following the
existing `SubTurnSpawner` / `DelegateAgentRegistry` seam pattern between `pkg/tools` and `pkg/agent`,
and resolving through the existing `activeTurnStates` registry rather than a new global.

`delegateStatusExtra` consults live progress **before** falling back to `recentActivityLines`. That
ordering is the decision: the persisted path is blind precisely during an in-flight round, which is
exactly when a worker looks dead. Rendered as e.g.
`generating tool arguments — 14.2 KB, last progress 1.3s ago`.

**Handler discipline.** The handler fires on every argument delta of a live stream. It MUST be cheap,
non-blocking and MUST NOT panic — a panic unwinds through the SSE read loop and takes down the turn,
which is strictly worse than the blindness being fixed. Providers invoke it synchronously.

### D4 — Machine-readable tool outcomes follow ADR-058, and the consumer is the **calling agent**

A refusal that is not a failure must be readable as such by the agent that **made the call**. Per
ADR-058, the discriminator belongs **inside the tool-result text that agent receives**, as structured
JSON — not in a Go struct field no model can see.

**The consumer is the worker, not the orchestrator.** A tool result is delivered to the agent that
invoked the tool; in a delegated flow that is the subagent. The chain is: worker calls `write_file` →
worker reads a result it can classify without parsing prose → worker reports the outcome to its parent
**in its own words**. Delegation already has a reporting path. **No special channel from the tool to
the parent is required, and none should be built.**

*Corollary:* surfacing persisted failure text to a parent agent (e.g. via `inspect_session`, which
still never reads `ToolCall.Error`) is a forensics convenience, not the mechanism by which a parent
learns an outcome, and must not be justified as closing this gap.

**What the discriminator buys that the prose does not.** The existing sentence
(`"file: X already exists. Set overwrite=true to replace."`) already names the condition and a remedy,
so the gain is *not* "the worker can now tell what happened". It is that the classification is
**stable**: a structured `already_exists` field is checkable without relying on wording that varies by
tool and changes over time, and it can be distinguished from a genuine I/O failure whose text is
arbitrary (`ErrorResult(err.Error())`). What it does **not** answer — whether the existing file is a
sibling doing this same task, or something unrelated — is out of scope here; the `clobberNote` /
last-writer audit lookup already in `filesystem.go` is the closer instrument for that question.

**Rejected: a `Reason` field on `ToolResult`** (`pkg/tools/result.go`, `0fb79b19`). It is unreachable
by any model — `ToolResult` is not serialised across the model boundary, so an agent sees only
`ForLLM`, whose prose is unchanged. It is **currently in live use** at `write_file`'s no-overwrite
guard with its own test file; W3 removes it.

**Evidence limitation.** All five workers that hit "already exists" were terminated within seconds;
every transcript ends at the errored call with no assistant turn after it (UAT §3.3). The incident
shows the ambiguity being created but **never shows a worker acting on it**. **[INFERRED]** that a
structured result would have produced a correct report.

### D5 — Cancelling an already-terminal session is a successful no-op

Recorded here because it is the second half of the same root cause (§1.4 item 2) and currently exists
only as a code comment. `executeCancel` returns success with wording distinct from the real-cancel
wording, preserving issue #588's actual requirement (never claim an action that did not occur). It
continues to return an error when a genuine background-shell kill failed or the descendant walk was
incomplete, so no real failure is downgraded. Delivered in `00e1d7f5`.

---

## 3. Consequences

**Positive.** A polling orchestrator can distinguish generation from a stall, removing **one of the
four inputs** to the cancel/respawn amplification (§1.4). D5 removes a second. The other two —
ineffective `steer` and the collision semantics of `write_file` — are addressed by D4 and by nothing
in this ADR respectively (see §8).

**Negative / accepted.**
- `ChatStream`'s signature changes; all three implementers (`openai_compat`, `anthropic`,
  `HTTPProvider`) must be updated. This is a compile-enforced break, which is the point.
- Per-delta handler invocation on the hot streaming path, bounded by D3's discipline.
- Anthropic-backed **child** turns will now acquire the parent's streamer and push text into the
  parent's WS stream where previously they did not. `WSHandler.GetStreamer`
  (`pkg/gateway/websocket.go:476-489`) gates existence on `h.sessions[chatID]` alone and uses
  `sessionID` only to select the transcript store, and `SSEHandler.GetStreamer` (`sse.go:87`) discards
  it outright — so a child passing `parentTS.chatID` does acquire the parent's connection. This is
  parity with `openai_compat`, but it is a real user-visible change on the Anthropic path and requires
  UAT (R3).

---

## 4. Out of scope

Whether `steer` should be deliverable mid-round; whether unbounded worker output should be bounded by
mechanism rather than prompt guidance; whether `write_file` should expose "who wrote this and when" to
a racing sibling; whether `inspect_session` should surface persisted failure reasons (not yet tracked —
see §8).

---

## 5. Risks

| ID | Risk | Mitigation |
|---|---|---|
| R1 | Handler on the hot path degrades streaming throughput | D3 discipline; AC-05 measures it |
| R2 | A panicking or blocking consumer takes down the turn | D3 mandates non-panicking; AC-06 |
| R3 | Anthropic child turns now stream into the parent's WS surface | UAT before release; not covered by any automated test |
| R4 | `ChatStream` signature change breaks an out-of-tree provider | Compile-enforced, so it fails loudly rather than silently |
| R5 | D4's structured JSON changes what the SPA renders in chat | §7 |

---

## 6. Work items

| ID | Decision | Change |
|---|---|---|
| W1 | D1 | Add `onProgress` to `StreamingProvider.ChatStream`; update all three implementers |
| W2 | D1 | Delete `OnToolCallProgressKey`, `ToolCallProgressFromOptions` and the `llmOpts` injection once W1 lands |
| W3 | D4 | Remove `ToolResult.Reason` / `ResultReason` / `WithReason`, its `write_file` call site and `write_file_reason_test.go` |
| W4 | D2 | Correct `ToolCallProgress.Index`'s doc comment to the provider-scoped contract |
| W5 | D4 | Emit a structured discriminator in `write_file`'s no-overwrite result, per ADR-058's convention |
| W6 | §7 | Resolve the contract impact of W5 before implementing it |
| W7 | D3 | Keep the delivered turn-scoped record and pull seam (already landed in `64371fa3`) |

## 7. Contract impact (Constraint #8) — to be investigated, not assumed

**This section is incomplete and W6 blocks W5.** ADR-058 §3 deliberately scoped *out* the
`*ToolResult` refusal family, pricing the extension as "not free"; D4 extends into exactly that class,
so the same analysis is owed here.

What must be traced before W5 is implemented:

1. `ForLLM` → `contentForLLM` (`loop.go:10058`) → `tcRecord.Error` → `ToolCall.error`, a top-level
   field on a schema with `additionalProperties: false`
   (`contracts/components/schemas/ToolCall.yaml`), mirrored into
   `pkg/gateway/inboundschemas/ToolCall.yaml` and generated into `src/lib/api/generated/schemas.ts`.
2. The live `tool_call_result` WS frame (`pkg/gateway/websocket.go`).
3. **Whether the SPA chat surface would begin rendering a JSON blob where it renders a sentence** —
   the user-visible question, and the one most likely to force a different shape for W5.
4. Whether the 5-step contract pipeline and `make verify-contracts` are required.

## 8. Acceptance criteria

Inheriting ADR-058 §8's bar: **a green test that does not exercise a production caller does not
satisfy this ADR.**

| ID | Criterion |
|---|---|
| AC-01 | The agent loop supplies a non-nil progress handler to `ChatStream` on a real turn. Must fail if the wiring is removed. |
| AC-02 | The handler survives a `BeforeLLM` hook replacing the options/parameters wholesale. |
| AC-03 | For **each** implementing provider: a stream carrying tool-call arguments produces **more than one** progress event with strictly increasing `ArgsBytes` **before the tool call completes**. (Provider-neutral. Worked example: on Anthropic, between `content_block_start` and `content_block_stop`; on `openai_compat`, across successive argument deltas.) |
| AC-04 | A child mid-tool-argument-generation is reported by `delegate status` as progressing, distinguishably from an idle child. Fails on pre-fix code. |
| AC-05 | The liveness record is race-free under concurrent write (provider goroutine) and read (polling goroutine), including two concurrent sub-turns on the same target agent. |
| AC-06 | A panicking progress handler does not terminate the turn, **or** the parser's propagation behaviour is documented as deliberate. |
| AC-07 | D4: `write_file`'s no-overwrite result carries the discriminator in the text the calling agent receives, and it survives into the persisted transcript. **This is explicitly a plumbing test** — D4's real outcome is model behaviour and is not deterministically testable; D4 is accepted on judgement, and §8's bar is waived for it alone. |

## 9. Open questions

- **Q1.** Does W5's structured result change SPA chat rendering (§7 item 3)? Blocks W5.
- **Q2.** Should the RC-3 cancel fix (D5) live here or in its own ADR? Recorded here because it has no
  decision record at all, only a code comment ending *"do not re-fix this back to `ErrorResult`"*.
- **Q3.** `inspect_session` dropping `ToolCall.Error` — file an issue, or accept as out of scope?
- **Q4.** No GitHub issue is linked to this ADR yet.
