# Agent Loop & Turn Engine

How a message becomes a response in Omnipus: from the inbound WebSocket frame,
through per-session workers and the turn loop, to streamed tokens and tool
execution — and how a running turn is cancelled.

This document is the architecture reference for `pkg/agent/loop.go` and its
neighbours (`turn.go`, `session_worker.go`, `steering.go`, `cancel.go`). Line
numbers are accurate as of the v0.1 codebase; treat the code as the source of
truth where it has since moved.

---

## Cast of components

| Component | Where | Role |
|---|---|---|
| `MessageBus` | `pkg/bus/bus.go` | In-process pub/sub: inbound, outbound, outbound-media channels, plus a streamer delegate. No "control" channel. |
| `AgentLoop` | `pkg/agent/loop.go` | Owns the registry, the bus, session workers, and the turn engine. |
| `sessionWorker` | `pkg/agent/session_worker.go` | One goroutine per session scope; serialises turns for that session. |
| `turnState` | `pkg/agent/turn.go` | Per-turn state: context, cancel funcs, the first-cancel-wins gate. |
| `wsStreamer` | `pkg/gateway/websocket.go` | Implements `bus.Streamer`; pushes `token`/`done` frames to the browser. |
| `webchatChannel` | `pkg/gateway/webchat_channel.go` | Outbound sink for the web SPA; dedups streamed vs. published responses. |
| Tool registry | `pkg/tools/` | Per-agent `ToolSet`; `ToProviderDefs()` exposes tools to the LLM. |

---

## Lifecycle in one diagram

```mermaid
sequenceDiagram
    autonumber
    participant Browser
    participant WS as WSHandler
    participant Bus as MessageBus
    participant Run as AgentLoop.Run()
    participant SW as sessionWorker
    participant PM as processMessage()
    participant RT as runTurn()
    participant Prov as LLM Provider
    participant Str as wsStreamer
    participant WCC as webchatChannel
    participant Tools as ToolRegistry

    Note over Browser,Bus: 1 — Ingress
    Browser->>WS: WS frame {type:"message", content, agent_id, session_id}
    WS->>WS: record user msg, create/resolve session
    WS->>Bus: PublishInbound(InboundMessage{channel:"webchat", chatID})

    Note over Bus,SW: 2 — Dispatch (per-session worker)
    Bus-->>Run: InboundChan() delivers msg
    Run->>Run: resolveSteeringTarget(msg) → scope
    alt worker exists & in a turn
        Run->>SW: enqueueSteeringFromMessage(msg)  %% mid-turn: becomes steering
    else no worker
        Run->>Run: admission.TryAdmit(scope)
        Run->>SW: newSessionWorker + go runLoop(); enqueue(msg)
    end
    SW->>PM: processTurn → processMessage(ctx, msg)

    Note over PM,RT: 3 — Route & run
    PM->>PM: resolveMessageRoute(msg) → agent, scope
    PM->>PM: message tool ResetSentInRound()
    PM->>RT: runAgentLoop → runTurn (opts.SendResponse = false for webchat)

    Note over RT,Prov: 4 — Turn loop (iterates ≤ MaxIterations)
    RT->>RT: BuildMessages(history, summary, user msg) + ToProviderDefs()
    RT->>RT: setTurnCancel(turnCancel)

    loop until no tool calls OR interrupt OR MaxIterations
        RT->>RT: callLLM closure: providerCtx, setProviderCancel(providerCancel)
        alt provider implements StreamingProvider AND a streamer exists
            RT->>Bus: GetStreamer(ctx, "webchat", chatID, sessionID)
            Bus->>Str: delegate → wsStreamer
            RT->>Prov: ChatStream(providerCtx, msgs, tools, …, onChunk)
            loop token deltas
                Prov-->>RT: delta
                RT->>Str: streamer.Update(delta)
                Str->>Browser: WS {type:"token", content}
            end
            Prov-->>RT: final (content + tool_calls)
        else non-streaming
            RT->>Prov: provider.Chat(providerCtx, msgs, tools, …)
            Prov-->>RT: full response
        end

        alt response has NO tool calls
            RT->>RT: finalContent = response.Content; break
        else response HAS tool calls
            RT->>RT: normalize, append assistant msg to history
            loop each tool call
                RT->>RT: hooks.BeforeTool() (may modify/deny)
                RT->>RT: resolve policy (allow / ask / deny) at exec time
                alt needs approval (ask)
                    RT->>Browser: WS {type:"exec_approval_request"}
                    Browser-->>RT: WS {type:"exec_approval_response", decision}
                end
                RT->>RT: emitEvent(ToolExecStart) → tool_call_start frame
                RT->>Tools: ExecuteWithContext(turnCtx, name, args)
                Tools-->>RT: ToolResult
                RT->>RT: emitEvent(ToolExecEnd) → tool_call_result frame
                RT->>RT: append tool result to history
            end
            RT->>RT: dequeue steering for scope; continue loop
        end
    end

    Note over RT,Browser: 5 — Finalize (once per turn)
    RT->>Str: finalizeStreamer → wsStreamer.Finalize()
    Str->>WCC: markStreamed(chatID)  %% only if content was streamed
    Str->>Browser: WS {type:"done", tokens, cost}
    RT-->>PM: turnResult{finalContent, status}

    Note over PM,WCC: 6 — Publish guard (avoids double-send)
    PM->>PM: publishResponseIfNeeded(ctx, agent, channel, chatID, finalContent)
    alt message tool already sent this round (HasSentInRound)
        PM->>PM: skip — no outbound
    else not sent
        PM->>Bus: PublishOutbound(OutboundMessage)
        Bus-->>WCC: Send(msg)
        alt streamed[chatID] set
            WCC->>WCC: consume flag (delete) and skip — already streamed
        else not streamed
            WCC->>Browser: deliver content (non-streaming fallback)
        end
    end

    Note over RT,RT: 7 — Post-turn: maybeSummarize() may compress history in the background
```

---

## Stage 1 — Ingress

A browser sends a WebSocket frame `{type:"message", content, agent_id,
session_id}`. The `WSHandler` records the user message, resolves or creates the
session, and republishes it onto the bus as an `InboundMessage`
(`channel:"webchat"`, a `chatID`, and the `session_id`). Channels other than the
web SPA (Telegram, Discord, …) publish the same `InboundMessage` shape from
their own receive loops, so everything downstream is channel-agnostic.

The bus (`pkg/bus/bus.go`) is deliberately small: `PublishInbound` /
`InboundChan`, `PublishOutbound` / `OutboundChan`, an outbound-media pair, and a
`GetStreamer` delegate (`bus.go:111`) used for live token streaming. There is no
control/cancel channel on the bus — cancellation is a direct method call
(see [Cancellation](#cancellation)).

## Stage 2 — Dispatch to a per-session worker

`AgentLoop.Run` (`loop.go:1507`) is the dispatcher. Its `select` waits on just
two things: the run context's `Done()` (graceful shutdown) and
`bus.InboundChan()`. It is not a busy-poll — an earlier 100 ms ticker was
removed in favour of pure context cancellation.

For each inbound message:

1. **System messages** (`channel == "system"`) are handled inline via
   `processSystemMessage` (`loop.go:3089`).
2. Everything else is mapped to a **session scope** via
   `resolveSteeringTarget(msg)` (`loop.go:3066`) and dispatched to a
   `sessionWorker`.

### The session worker model

Concurrency is **one goroutine per session scope** (`session_worker.go`). A
worker owns a buffered `inbox` channel (capacity `workerInboxCap = 8`) and a
`Background`-derived context independent of any single request. Its `runLoop`
reads the inbox sequentially and processes one turn at a time, so **turns within
a session never overlap** while **different sessions run concurrently**.

- **Spawn:** when no worker exists for a scope, `Run` first calls
  `admission.TryAdmit(scope)` (a concurrency cap; rejects with a
  capacity reply when full), then `newSessionWorker(...)` + `go w.runLoop()` and
  enqueues the message (`loop.go:~1608–1637`).
- **Mid-turn messages become steering.** If a worker is already inside a turn
  (`w.inTurn.Load()`), a new same-scope message is routed to the steering queue
  via `enqueueSteeringFromMessage` rather than the inbox
  (`session_worker.go:~111`). See [Steering](#steering-mid-turn-injection).
- **Idle exit:** a worker self-terminates after `workerIdleTimeout` (60 s).
- **Shutdown:** `stopSessionWorkers` (`loop.go:1645`) cancels every worker and
  waits up to a 5 s drain budget per worker.

## Stage 3 — Route and run

The worker calls `processMessage` (`loop.go:2790`), which:

1. **Resolves the route** via `resolveMessageRoute` (`loop.go:2954`): explicit
   `agent_id` metadata → session-scoped handoff override → registry routing
   rules → default agent.
2. **Resets per-round state** on the message tool (`ResetSentInRound`) so the
   double-send guard starts clean for this user message.
3. **Calls `runAgentLoop`** (`loop.go:3156`) with `processOptions`. For webchat,
   `SendResponse: false` — the response is delivered by streaming and the
   publish guard, not by an unconditional `PublishOutbound`. Non-streaming
   channels set `SendResponse: true` so `runAgentLoop` publishes the final
   content directly (`loop.go:~3207`).

`runAgentLoop` builds the `turnState`, runs the turn, re-publishes any follow-up
messages the turn queued, and returns the final content.

## Stage 4 — The turn loop (`runTurn`)

`runTurn` (`loop.go:3288`) is the heart of the engine (~1,800 lines). Per turn:

**Setup.**
- A turn context is created — `context.WithTimeout` when the agent sets
  `TimeoutSeconds`, otherwise `context.WithCancel` — and stored via
  `setTurnCancel` for later hard-abort (`loop.go:~3294–3303`).
- Messages are assembled by the context builder from session history, the
  running summary, and the new user message; media refs are resolved.
- Tool definitions come from `agent.Tools.ToProviderDefs()`.

**Iteration.** The loop runs while
`currentIteration < agent.MaxIterations` (plus pending steering / graceful-
interrupt conditions), with a hard ceiling at `2 × MaxIterations` that breaks
unconditionally (`loop.go:~3451`, `~3463`) so a misbehaving model can't loop
forever.

**The LLM call** is a local closure, `callLLM` (`loop.go:3774`). It creates a
per-call `providerCtx` and registers `setProviderCancel(providerCancel)` so a
cancel can abort the in-flight HTTP call specifically. It then picks a path:

```go
// loop.go:~3807 — streaming only when the provider supports it AND a live
// streamer exists for this channel+chat.
if sp, ok := activeProvider.(providers.StreamingProvider); ok && al.bus != nil {
    if streamer, hasStreamer := al.bus.GetStreamer(providerCtx, ts.channel, ts.chatID, ts.transcriptSessionID); hasStreamer {
        resp, streamErr := sp.ChatStream(providerCtx, msgs, toolDefs, model, opts, func(accumulated string) {
            streamer.Update(providerCtx, delta) // → WS {type:"token"}
        })
        ...
    }
}
return activeProvider.Chat(providerCtx, msgs, toolDefs, model, opts) // non-streaming fallback
```

- **Native search:** if the provider supports native web search and the agent
  prefers it, the client-side `web_search` tool is filtered out of the defs
  (`loop.go:~3650`).
- **Thinking:** when the agent's thinking level is on and the provider is
  `ThinkingCapable`, a `thinking_level` option is added (`loop.go:~3701`).
- **Retries:** transient/provider errors back off and retry (max 2);
  context-limit errors trigger `forceCompression` and a retry on refreshed
  history; an empty-content response retries once via the closure without
  advancing the outer iteration (`loop.go:~3997–4097`, `~4248`).

**Tool-call decision.**
- *No tool calls* → `finalContent = response.Content` and the loop breaks.
- *Tool calls* → they are normalized, the assistant message (with tool calls)
  is appended to history, and each call runs through:
  `hooks.BeforeTool` (may rewrite/deny) → **policy re-resolution at exec time**
  (closes the TOCTOU window if policy changed mid-turn) → approval if the policy
  is `ask` (an `exec_approval_request` frame to the browser, answered with
  `exec_approval_response`) → `emitEvent(ToolExecStart)` → `ExecuteWithContext`
  → `emitEvent(ToolExecEnd)` → append the tool result to history.

Tool-exec events are surfaced to the SPA as `tool_call_start` /
`tool_call_result` frames.

After tools run, the loop **dequeues any steering** for the scope and continues
to the next iteration.

## Stage 5 — Finalize (exactly once per turn)

When the turn ends, `finalizeStreamer` calls `wsStreamer.Finalize()`
(`websocket.go:~2158`), which:

- calls `webchatChannel.markStreamed(chatID)` **only if content was actually
  streamed** (`websocket.go:~2184`), and
- sends a single `{type:"done", tokens, cost}` frame.

`Finalize` is deferred to turn end, so the `done` frame is sent once when the
turn truly completes — not after each intermediate LLM call that precedes tool
execution.

## Stage 6 — The publish guard (no double-send)

`publishResponseIfNeeded` (`loop.go:1680`) is the single choke point that
prevents a response being delivered twice (once by streaming, once by an
outbound publish). It checks the message tool's per-round flag:

```go
// loop.go:~1690
if mt, ok := tool.(*tools.MessageTool); ok {
    alreadySent = mt.HasSentInRound()
}
if alreadySent { return } // the message tool already delivered this round
al.bus.PublishOutbound(ctx, bus.OutboundMessage{Channel: channel, ChatID: chatID, Content: response})
```

If a publish does happen, `webchatChannel.Send` provides a second guard using a
streamed-flag map:

```go
// webchat_channel.go:~62
alreadyStreamed := c.streamed[msg.ChatID]
delete(c.streamed, msg.ChatID) // consume the flag
if alreadyStreamed { return nil } // already delivered via the stream — no-op
```

So the normal webchat path is: tokens streamed → `Finalize` marks streamed →
`done` sent → any later `Send` for that chat is a consumed no-op. The flag is
deleted on consumption so a *subsequent* turn on the same chat sends normally.
`buildContinuationTarget` (`loop.go:1723`) computes the scope used to drain
queued steering after the turn; it returns `nil` for the `system` channel.

## Steering (mid-turn injection)

**Steering** lets a user keep talking while a turn is still running. Instead of
queueing a brand-new turn, a same-scope message that arrives mid-turn is pushed
into a per-scope steering queue (`steering.go`) and **injected into the running
turn** as an additional user message.

`runTurn` drains the steering queue at several checkpoints —
`dequeueSteeringMessagesForScope(ts.sessionKey)` around `loop.go:3539`, `4232`,
and `5020` — appends the messages to the context (and to history unless
`NoHistory`), emits a `SteeringInjected` event, and continues the loop. The
queue supports one-at-a-time or drain-all modes
(`Agents.Defaults.SteeringMode`). This is also the mechanism the graceful stage
of cancellation rides on (below).

## Summarization & compression

History is kept in check three ways:

- **Background summarization** — `maybeSummarize` (`loop.go:5231`) runs after a
  turn when message count exceeds `SummarizeMessageThreshold` or the token
  estimate exceeds `SummarizeTokenPercent` of the context window. It launches
  `summarizeSession` (`loop.go:5426`) in a goroutine, deduped per session so two
  summaries don't run at once.
- **Emergency compression** — `forceCompression` (`loop.go:5270`) runs inline
  when the provider returns a context-limit error: it drops roughly the oldest
  half of complete turns (or, as a last resort, keeps only the most recent user
  message), records a compression note in the summary, and the turn retries.
- **Summary-aware rebuild** — every turn rebuilds its prompt from the current
  summary + remaining history, so compression is transparent to the next turn.

> Note: history compression here is single-layer (drop-oldest + summary note).
> There is no separate "tool-result pruning" pass.

---

## Cancellation

A cancel can arrive while a turn is mid-LLM-call or mid-tool. Omnipus runs a
**three-stage escalation — graceful → hard → detached** — through one canonical
entry point so audit, transcript, abuse-detection, and approval-deny behaviour
are identical regardless of where the cancel came from.

### Canonical entry point: `RequestCancel`

All surfaces converge on `AgentLoop.RequestCancel`
(`cancel.go:90`). Given a `CancelScope` (either a `SessionID`, or a
`Channel`+`ChatID` it resolves by walking active turns), it:

1. records the attempt with the **abuse detector** (`cancel_abuse.go`), which
   emits a `cancel.abuse_pattern` warning if a user exceeds ~10 cancels/60 s;
2. performs the **first-cancel-wins** claim via `ClaimCancel` (`turn.go:282`) —
   an atomic gate so a double-cancel is a no-op;
3. emits a `turn.cancel.attempt` audit event (always, even for no-op cancels);
4. registers an `onCancelFinish` callback **before** interrupting (closing a
   race where the turn could `Finish` before the callback is set), which on turn
   exit marks the last transcript entry truncated, appends a `turn_canceled`
   transcript entry, and emits `turn.cancelled`;
5. runs the staged timers below.

### The three stages

```mermaid
sequenceDiagram
    autonumber
    participant U as Canceller (SPA / cmd / channel)
    participant RC as RequestCancel
    participant IS as InterruptSession*
    participant TS as turnState
    participant Prov as in-flight LLM / tool
    participant Br as Browser

    U->>RC: cancel (scope, canceller, hooks)
    RC->>TS: ClaimCancel() (first-cancel-wins)
    RC->>RC: audit turn.cancel.attempt; register onCancelFinish

    Note over RC,Prov: Stage 1 — GRACEFUL (immediate)
    RC->>IS: InterruptSession(sessionID)
    IS->>TS: providerCancel() first, then set gracefulInterrupt flag
    TS-->>Prov: in-flight HTTP stream aborted
    RC->>Br: cancel_stage {stage:"graceful"}
    RC->>RC: auto-deny pending approvals; session → interrupted

    Note over RC,Prov: Stage 2 — HARD (+3s, if still alive)
    RC->>IS: InterruptSessionHard(sessionID)
    IS->>TS: requestHardAbort(): set hardAbort, fire providerCancel + turnCancel
    TS-->>Prov: turn context cancelled (tools see ctx.Done())
    RC->>Br: cancel_stage {stage:"hard"}

    Note over RC,Br: Stage 3 — DETACHED (+5s after hard, if still alive)
    RC->>TS: MarkAbandoned() (zombie goroutine output suppressed)
    RC->>Br: cancel_stage {stage:"detached"}
    RC->>RC: audit turn.cancel.stuck (warn)
```

- **Graceful (immediate)** — `InterruptSession` (`steering.go:423`) walks every
  turn whose `transcriptSessionID` matches (root **and** sub-turns) and, for
  each, fires `providerCancel()` **first** so the in-flight HTTP stream aborts
  at once, then sets the graceful-interrupt flag the loop polls at its tool
  checkpoints. The turn stops cleanly between tools. The SPA gets a
  `cancel_stage {stage:"graceful"}` frame; pending approvals are auto-denied and
  the session is marked `interrupted`.
- **Hard (+3 s, if still alive)** — `InterruptSessionHard` (`steering.go:492`)
  calls `requestHardAbort`, which sets `hardAbort` and fires **both**
  `providerCancel` and `turnCancel`, cancelling the whole turn context. Running
  tools observe `ctx.Done()` via `ExecuteWithContext`. SPA gets
  `cancel_stage {stage:"hard"}`.
- **Detached (+5 s after hard, if still alive)** — the goroutine is presumed
  stuck. `MarkAbandoned` (`turn.go:292`) flips a flag that suppresses any
  further transcript/frame/cost writes from the zombie, the SPA gets
  `cancel_stage {stage:"detached"}`, and a `turn.cancel.stuck` warning is
  audited. The worst case is a leaked goroutine that can no longer affect state.

`turnState.Finish` (`turn.go:672`) fires the `onCancelFinish` callback exactly
once when the turn finally exits, tagging the method as `"graceful"` or
`"hard"`. The cascade always covers sub-turns, so a cancel on a parent stops its
spawned children too.

### Cancel surfaces

| Surface | Path to `RequestCancel` | Transport hooks |
|---|---|---|
| **Web SPA** | `cancel` WS frame → `WSHandler.handleCancel` (`websocket.go:~933`) → `RequestCancel` | sends `cancel_stage` frames, auto-denies pending approvals, sets session `interrupted` |
| **`/cancel` command** | `cmd_cancel.go` → `Runtime.CancelActiveTurn` → `RequestCancelForSession` (`cancel.go:320`) | none |
| **Text channels** | `/cancel` text → `DispatchCancelIfRecognized` (`channels/cancelparse.go`) → `RequestCancelByChannelChat` (`cancel.go:342`) | none; resolves session by channel+chat |
| **CLI (double-ESC)** | two ESC presses within 500 ms → `escapeDetector.feed` → `cancelFn` → `agentLoop.RequestCancel` (`cmd/omnipus/internal/agent/helpers.go:163`) | none (in-process) |

`IsCancelCommand` matches only the exact message `/cancel` (case-insensitive,
trimmed) — never a substring — so it won't fire on a sentence that merely
mentions `/cancel`. The text-channel path is wired across the text-parsing
channels (Matrix, WhatsApp, WhatsApp-native, Line, IRC, QQ, OneBot, WeCom,
Weixin) via the shared `DispatchCancelIfRecognized` helper in `pkg/channels`.

The **CLI** surface (`cmd/omnipus/internal/agent/`) runs the agent loop
in-process, so it calls `RequestCancel` directly rather than over REST. A
raw-stdin watcher (`startRawStdinWatcher` → `runEscapeReadLoop`) feeds each byte
to an `escapeDetector` (`escape_detector.go`): the first ESC arms a 500 ms
window (a `[`/`O` introducer within 50 ms is treated as an arrow/F-key sequence
and passed through, not a cancel); a second ESC inside the window fires the
cancel callback, which calls `agentLoop.RequestCancel` with
`CancelScope{SessionID}` and `Channel:"cli"`. Requires a TTY in raw mode; when
stdin is not a TTY the shortcut is unavailable and the user falls back to
Ctrl+C.

### Cancel-related audit events

Defined in `pkg/audit/events.go`:

| Constant | String | Severity | When |
|---|---|---|---|
| `EventTurnCancelAttempt` | `turn.cancel.attempt` | info | every cancel attempt (incl. no-ops) |
| `EventTurnCancelled` | `turn.cancelled` | info | once, when a cancelled turn exits |
| `EventTurnCancelStuck` | `turn.cancel.stuck` | warn | detached stage — goroutine outlived hard abort |
| `EventCancelAbusePattern` | `cancel.abuse_pattern` | warn | a user exceeds the cancel-rate threshold |

On graceful shutdown, `writeTurnCancelledRestartForActiveTurns`
(`loop.go:1840`) appends a synthetic marker to each active session so the
crash-recovery path doesn't try to resume a turn that was killed at exit.

---

## Quick reference

| Function | File:line | Responsibility |
|---|---|---|
| `Run` | `loop.go:1507` | dispatcher; inbound → session worker |
| `sessionWorker.runLoop` | `session_worker.go` | serialise turns per session |
| `processMessage` | `loop.go:2790` | route + per-round reset + run |
| `runAgentLoop` | `loop.go:3156` | turn setup + `SendResponse` handling |
| `runTurn` | `loop.go:3288` | turn loop: LLM, streaming, tools, retries |
| `callLLM` (closure) | `loop.go:3774` | streaming vs. non-streaming provider call |
| `publishResponseIfNeeded` | `loop.go:1680` | double-send guard (`HasSentInRound`) |
| `buildContinuationTarget` | `loop.go:1723` | scope for post-turn steering drain |
| `markStreamed` / `Send` | `webchat_channel.go:45/62` | streamed-vs-published dedup |
| `resolveSteeringTarget` | `loop.go:3066` | scope resolution for steering |
| `maybeSummarize` / `forceCompression` | `loop.go:5231/5270` | background + emergency history compaction |
| `RequestCancel` | `cancel.go:90` | canonical cancel state machine |
| `InterruptSession` / `InterruptSessionHard` | `steering.go:423/492` | graceful / hard cascade to all turns |
| `ClaimCancel` / `Finish` | `turn.go:282/672` | first-cancel-wins + exactly-once finish callback |
