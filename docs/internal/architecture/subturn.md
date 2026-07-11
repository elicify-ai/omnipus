# 🔄 SubTurn Mechanism

> Back to [README](../../../README.md)

## Overview

The `SubTurn` mechanism is a core feature in Omnipus that allows tools to spawn isolated, nested agent loops to handle complex sub-tasks.

By using a SubTurn, an agent can break down a problem and run a separate LLM invocation in an independent, ephemeral session. This ensures that intermediate reasoning, background tasks, or sub-agent outputs do not pollute the main conversation history.

## Core Capabilities

- **Context Isolation**: Each SubTurn uses an `ephemeralSessionStore`. Its message history does not leak into the parent task and is destroyed upon completion. The ephemeral session holds at most **50 messages**; older messages are automatically truncated when this limit is reached.
- **Depth & Concurrency Limits**: Prevents infinite loops and resource exhaustion.
  - **Maximum Depth**: Up to 3 nested levels.
  - **Maximum Concurrency**: Up to 5 concurrent sub-turns per parent turn (managed via a semaphore with a 30-second timeout).
- **Context Protection**: Supports soft context limits (`MaxContextRunes`). It proactively truncates old messages (while preserving system prompts and recent context) before hitting the provider's hard context window limit.
- **Error Recovery**: Automatically detects and recovers from provider context length exceeded errors and truncation errors by compressing history and retrying.

## Configuration (`SubTurnConfig`)

When spawning a SubTurn, you must provide a `SubTurnConfig`:

| Field | Type | Description |
| :--- | :--- | :--- |
| `Model` | `string` | The LLM model to use for the sub-turn (e.g., `gpt-4o-mini`). **Required.** |
| `Tools` | `[]tools.Tool` | Tools granted to the sub-turn. If empty, it inherits the parent's tools. |
| `SystemPrompt` | `string` | The task description for the sub-turn. Sent as the first user message to the LLM (not as a system prompt override). |
| `ActualSystemPrompt` | `string` | Optional explicit system prompt to replace the agent's default. Leave empty to inherit the parent agent's system prompt. |
| `MaxTokens` | `int` | Maximum tokens for the generated response. |
| `Async` | `bool` | Controls the result delivery mode (Synchronous vs. Asynchronous). |
| `Critical` | `bool` | If `true`, the sub-turn continues running even if the parent finishes gracefully. |
| `Timeout` | `time.Duration` | Maximum execution time (default: 5 minutes). |
| `MaxContextRunes`| `int` | Soft context limit. `0` = auto-calculate (75% of model's context window, recommended), `-1` = no limit (disable soft truncation, rely only on hard context error recovery), `>0` = use specified rune limit. |

> **Note:** The `Async` flag does **not** make the call non-blocking. It only controls whether the result is also delivered to the parent's `pendingResults` channel. Both modes block the caller until the sub-turn completes. For true non-blocking execution, the caller must spawn the sub-turn in a separate goroutine.

## Execution Modes

### Synchronous (`Async: false`)

This is the standard mode where the caller needs the result immediately to proceed.

- The caller blocks until the sub-turn completes.
- The result is **only** returned directly via the function return value.
- It is **not** delivered to the parent's pending results channel.

**Example:**
```go
cfg := agent.SubTurnConfig{
    Model:        "gpt-4o-mini",
    SystemPrompt: "Analyze the provided codebase...",
    Async:        false,
}
result, err := agent.SpawnSubTurn(ctx, cfg)
// Process result immediately
```

### Asynchronous (`Async: true`)

Used for "fire-and-forget" operations or parallel processing where the parent turn collects results later.

- The result is delivered to the parent turn's `pendingResults` channel.
- The result is **also** returned via the function return value (for consistency).
- The parent's Agent Loop will poll this channel in subsequent iterations and automatically inject the results into the ongoing conversation context as `[SubTurn Result]`.

**Example:**
```go
cfg := agent.SubTurnConfig{
    Model:        "gpt-4o-mini",
    SystemPrompt: "Run a background security scan...",
    Async:        true,
}
result, err := agent.SpawnSubTurn(ctx, cfg)
// The result will also be injected into the parent loop later via channel
```

## Error Recovery and Retries

SubTurns implement automatic retry mechanisms for transient errors:

| Error Type | Max Retries | Recovery Action |
|:-----------|:------------|:----------------|
| Context Length Exceeded | 2 | Force compress history and retry |
| Response Truncated (`finish_reason="truncated"`) | 2 | Inject recovery prompt and retry |

### Truncation Recovery
When the LLM response is truncated (`finish_reason="truncated"`), SubTurn automatically:
1. Detects the truncation from `turnState.lastFinishReason`
2. Injects a recovery prompt: "Your previous response was truncated due to length. Please provide a shorter, complete response..."
3. Retries up to 2 times

### Context Error Recovery
When the provider returns a context length error (e.g., `context_length_exceeded`):
1. Force compresses the message history (drops oldest 50% of conversation)
2. Retries with the compressed context
3. Up to 2 retries before failing

## Lifecycle and Cancellation

SubTurns operate within an independent context but maintain a structural link to their parent `turnState`.

### Graceful Parent Finish
When the parent task finishes naturally (`Finish(false)`):
- **Non-critical** sub-turns receive a signal to exit gracefully without throwing an error.
- **Critical** (`Critical: true`) sub-turns continue running in the background. Once finished, their results are emitted as **Orphan Results** so the data is not lost.

### Hard Abort
When the parent task is forcefully aborted (e.g., user interrupts with `/stop`):
- A cascading cancellation is triggered, instantly terminating all child and grandchild sub-turns.
- The root turn's session history rolls back to the snapshot taken at turn start (`initialHistoryLength`), preventing dirty context. SubTurns are not affected by this rollback as they use ephemeral sessions that are discarded anyway.

## Agent Loop Integration

### Bus Draining During Processing

When a message enters the `Run()` loop, the agent starts a `drainBusToSteering` goroutine before calling `processMessage`. This goroutine runs concurrently with the entire processing lifecycle and continuously consumes any new inbound messages from the bus, redirecting them into the **steering queue** instead of dropping them.

This ensures that if a user sends a follow-up message while the agent is processing (including during SubTurn execution), the message is not lost — it will be picked up between tool call iterations via `dequeueSteeringMessages`.

The drain goroutine stops automatically when `processMessage` returns (via a cancellable context).

### Pending Result Polling

The agent loop polls for async SubTurn results at two points per iteration:
1. **Before the LLM call**: injects any arrived results as `[SubTurn Result]` messages into the conversation context.
2. **After all tool executions**: polls again during the tool loop to catch results that arrived during tool execution.
3. **After the final iteration**: one last poll before the turn ends to avoid losing late-arriving results.

### Turn State Tracking

All active root turns are registered in `AgentLoop.activeTurnStates` (`sync.Map`, keyed by session key). This allows `HardAbort` and `/subagents` observability commands to find and operate on active turns.

## Event Bus Integration

SubTurns emit specific events to the Omnipus `EventBus` for observability and debugging:

| Event Kind | When Emitted | Payload |
|:------|:-------------|:--------|
| `subturn_spawn` | Sub-turn successfully initialized | `SubTurnSpawnPayload{AgentID, Label, ParentTurnID}` |
| `subturn_end` | Sub-turn finishes (success or error) | `SubTurnEndPayload{AgentID, Status}` |
| `subturn_result_delivered` | Async result successfully delivered to parent | `SubTurnResultDeliveredPayload{TargetChannel, TargetChatID, ContentLen}` |
| `subturn_orphan` | Result cannot be delivered (parent finished or channel full) | `SubTurnOrphanPayload{ParentTurnID, ChildTurnID, Reason}` |

## Async Result Delivery (AsyncNotifier — Post-Turn Re-Injection)

The mechanisms above (`pendingResults` polling, Orphan Results) both assume the
**parent turn is still the one that will consume the result** — either it is
still running and polls for it, or it has finished and the result is simply
dropped with an inert event. There is a third, separate mechanism for a
different situation: an **async-capable tool's background work finishes and
the result needs to reach the user as a genuinely new turn**, regardless of
whether the originating turn (or even the originating WebSocket connection)
still exists.

### What Triggers It

Any tool implementing `tools.AsyncExecutor` (e.g. `DelegateTool` with
`async=true` — see `pkg/tools/delegate.go`) receives an `AsyncCallback` from
`ToolRegistry.ExecuteWithContext` and invokes it when its background work
completes, independent of the turn that launched it. In the agent loop, this
callback is the `asyncCallback` closure constructed per tool call (around the
tool-execution loop in `pkg/agent/loop.go`). When the callback fires with
non-empty `result.ContentForLLM()`, it calls:

```go
al.asyncNotifier.Notify(ctx, AsyncNotifyEvent{
    Channel:             ts.channel,
    ChatID:              ts.chatID,
    AgentID:             ts.agent.ID,             // the ORIGINATING turn's own agent
    TranscriptSessionID: ts.transcriptSessionID,   // the ORIGINATING turn's transcript binding
    SourceKind:          toolName,
    Content:             content,
})
```

`ts` here is the *originating* turn's own `turnState`, captured by the
closure — critically, this identity is captured **at tool-launch time**, not
re-derived later, because by the time the callback fires the originating turn
may have already finished (that is precisely the scenario this mechanism
exists to handle).

### AsyncNotifier.Notify (`pkg/agent/async_notifier.go`)

`Notify` is the single reusable implementation of this delivery path
(async-notifier-spec.md). It:

1. Rejects an ambiguous destination (empty `Channel`/`ChatID`) before doing
   anything else.
2. Truncates `Content` to a 1MB cap (mirroring `exec`'s background-output
   buffering convention) and filters sensitive data via
   `config.FilterSensitiveData`.
3. Offers a copy of the event to every registered observer, independent of
   publish outcome (a currently-unused extension point for a future Goals
   feature — no producer populates it today).
4. Publishes a synthetic `bus.InboundMessage` on the message bus:

   ```go
   bus.InboundMessage{
       Channel: "system",
       Sender:  bus.SenderInfo{CanonicalID: "async:<SourceKind>"},
       ChatID:  "<Channel>:<ChatID>",
       Content: content,
       // Dedicated carriers (see pkg/bus/types.go) — the originating
       // turn's real identity and transcript binding, threaded through so
       // the consumer never has to guess.
       AsyncOriginAgentID:       event.AgentID,
       AsyncTranscriptSessionID: event.TranscriptSessionID,
   }
   ```

5. Emits `EventKindFollowUpQueued` to the EventBus — but only *after* the
   publish has actually succeeded (a failed publish never claims a
   compensating "queued" state).

### Consuming the Result: `processSystemMessage` Starts a Real New Turn

`AgentLoop.Run()`'s dispatch loop routes every `Channel == "system"` inbound
message directly to `processSystemMessage`, on its own bare goroutine (system
messages bypass the per-session worker pool entirely — there is no
serialization scope for them). `processSystemMessage`:

1. Resolves the **originating agent**: if `msg.AsyncOriginAgentID` is set, it
   looks up that exact agent via `GetRegistry().GetAgent(...)`.
   `GetRegistry().GetDefaultAgent()` is used **only** as a last resort — an
   unset origin (a message with no async provenance) or a named agent that no
   longer exists.
2. Resolves the **transcript session**: if `msg.AsyncTranscriptSessionID` is
   set, it resolves the owning store via `AgentLoop.ResolveSessionStore(...)`
   — the same "run a turn that must land in a specific, pre-existing session"
   pattern `AgentLoop.ProcessScheduled` and `spawnSubTurn` (its
   `TranscriptSessionID: parentTS.transcriptSessionID` threading) already use.
3. Calls `runAgentLoop` with the resolved agent and
   `TranscriptSessionID`/`TranscriptStore` set on `processOptions`. This is a
   **genuine new turn**: the resolved agent's own LLM is invoked with
   `"[System: <sender>] <content>"` as the user message, and — because a
   transcript session is now bound — the response is persisted to
   `transcript.jsonl` through the exact same paths any other turn uses
   (`turnState.appendAssistantTranscript` on the non-streaming/no-live-
   connection path, or the live `wsStreamer` path when a WebSocket connection
   is still open for that chat). Persistence does **not** depend on a live
   connection existing — it depends only on the transcript session still
   resolving to a store.

### Contrast With Orphan Results

`SubTurnOrphanResultEvent` (see [Orphan Results](#orphan-results) above) is a
narrower, SubTurn-specific signal: it fires only for a `Critical: true`
sub-turn whose result can't reach its *still-in-memory* parent's
`pendingResults` channel (channel full, or the parent has finished). It is
genuinely inert — no new turn is started, nothing is persisted, and it exists
purely for external/observability consumers. **AsyncNotifier is a different,
more general mechanism** (any async-capable tool, not just `Critical`
sub-turns) that *does* start a real, persisted turn. Do not conflate the two
when tracing "what happens to a background result after its turn ends."

### Historical Bug (Fixed)

Before this delivery path threaded `AsyncOriginAgentID`/
`AsyncTranscriptSessionID` through, `processSystemMessage` had no way to know
either value: it always guessed `GetDefaultAgent()` (misattributing a live
speaker when the true producer wasn't the default agent) and never bound a
transcript session at all. Persistence then depended *entirely* on a live
WebSocket connection still being open for the chat (via the gateway's
`chatID`→`sessionID` `GetStreamer` lookup) — if that connection had already
closed (tab close, reload, idle timeout) by the time the async result
arrived, the result was silently, permanently lost: never written to
`transcript.jsonl`, unrecoverable even by reopening the conversation. See
`pkg/agent/async_result_persistence_test.go` for regression coverage of both
the attribution fix and the persistence-without-a-live-connection fix.

## API Reference

### SpawnSubTurn (Public Entry Point)

```go
func SpawnSubTurn(ctx context.Context, cfg SubTurnConfig) (*tools.ToolResult, error)
```

This is the exported package-level entry point for agent-internal code (e.g., tests, direct invocations). It retrieves `AgentLoop` and `turnState` from context and delegates to the internal `spawnSubTurn`.

**Requirements:**
- `AgentLoop` must be injected into context via `WithAgentLoop()`
- Parent `turnState` must exist in context (automatically set when called from tools)

**Returns:**
- `*tools.ToolResult`: Contains `ForLLM` field with the sub-turn's output
- `error`: One of the defined error types or context errors

### AgentLoopSpawner (Interface Implementation)

```go
type AgentLoopSpawner struct { al *AgentLoop }

func (s *AgentLoopSpawner) SpawnSubTurn(ctx context.Context, cfg tools.SubTurnConfig) (*tools.ToolResult, error)
```

This implements the `tools.SubTurnSpawner` interface for use by tools that need to spawn sub-turns without a direct import of the `agent` package (avoiding circular dependencies). It converts `tools.SubTurnConfig` → `agent.SubTurnConfig` before delegating to the internal `spawnSubTurn`.

### NewSubTurnSpawner

```go
func NewSubTurnSpawner(al *AgentLoop) *AgentLoopSpawner
```

Creates a new spawner instance for the given AgentLoop. Pass the returned value to `SpawnTool.SetSpawner()` or `SubagentTool.SetSpawner()` during tool registration.

### Continue

```go
func (al *AgentLoop) Continue(ctx context.Context, sessionKey string) error
```

Resumes an idle agent turn by injecting any queued steering messages as a new LLM iteration. Used when the agent is waiting and a deferred steering message needs to be processed without a new inbound message arriving.

## Context Propagation

SubTurn relies on context values for proper operation:

| Context Key | Purpose |
|:------------|:--------|
| `agentLoopKey` | Stores `*AgentLoop` for tool access and SubTurn spawning |
| `turnStateKey` | Stores `*turnState` for hierarchy tracking and result delivery |

### Injecting Dependencies

```go
// Before calling tools that may spawn SubTurns
ctx = WithAgentLoop(ctx, agentLoop)
ctx = withTurnState(ctx, turnState)
```

### Independent Child Context

**Important**: The child SubTurn uses an **independent context** derived from `context.Background()`, not from the parent context. This design choice:

- Allows critical SubTurns to continue after parent cancellation
- Prevents parent timeout from affecting child execution
- Child has its own timeout for self-protection (`Timeout` config or 5 minutes default)

## Error Types

| Error | Condition |
|:------|:----------|
| `ErrDepthLimitExceeded` | SubTurn depth exceeds 3 levels |
| `ErrInvalidSubTurnConfig` | Required field `Model` is empty |
| `ErrConcurrencyTimeout` | All 5 concurrency slots occupied for 30+ seconds |
| Context errors | Parent context cancelled during semaphore acquisition |

## Thread Safety

SubTurns are designed for concurrent execution:

- **Parent-child relationships**: Managed under mutex (`parentTS.mu.Lock()`)
- **Active turn tracking**: Uses `sync.Map` for concurrent access to `activeTurnStates`
- **ID generation**: Uses `atomic.Int64` for unique SubTurn IDs (format: `subturn-N`, globally monotonic per `AgentLoop` instance)
- **Result delivery**: Reads parent state under lock, releases before channel send (small race window acceptable)

## Orphan Results

An orphan result occurs when:
1. Parent turn finishes before the SubTurn completes
2. The `pendingResults` channel is full (buffer size: 16)

When a result becomes orphan:
- `SubTurnOrphanResultEvent` is emitted to EventBus
- The result is **NOT** delivered to the LLM context
- External systems can listen to this event for custom handling

> **Not to be confused with [Async Result Delivery (AsyncNotifier)](#async-result-delivery-asyncnotifier--post-turn-re-injection)
> below** — a separate, more general mechanism (any async-capable tool, not
> just `Critical` sub-turns) that *does* start a real, persisted new turn
> when a background result needs to reach the user after its originating
> turn has ended. This section's orphan event is genuinely inert by design.

### Preventing Orphan Results
- Use `Critical: true` for important SubTurns that must complete
- Monitor `SubTurnOrphanResultEvent` for observability
- Consider the 16-buffer limit when spawning many async SubTurns

## Tool Inheritance

### When `cfg.Tools` is empty:
- SubTurn inherits **all** tools from the parent agent
- Tools are registered in a new `ToolRegistry` instance
- Tool TTL is managed independently from parent

### When `cfg.Tools` is specified:
- Only the specified tools are available to the SubTurn
- Parent tools are **NOT** merged
- Use this to restrict SubTurn capabilities for security or focus

**Example - Restricted SubTurn:**
```go
cfg := agent.SubTurnConfig{
    Model: "gpt-4o-mini",
    Tools: []tools.Tool{readOnlyTool}, // Only read-only access
    SystemPrompt: "Analyze the file structure...",
}
```

## Reference

| Constant | Value |
|:---------|:------|
| `maxSubTurnDepth` | 3 |
| `maxConcurrentSubTurns` | 5 |
| `concurrencyTimeout` | 30s |
| `defaultSubTurnTimeout` | 5m |
| `maxEphemeralHistorySize` | 50 messages |
| `pendingResults` buffer | 16 |
| `MaxContextRunes` default | 75% of model context window |
