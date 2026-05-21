# Observability: session history and the event stream

Every action an Omnipus agent takes is visible. There are two complementary surfaces:

- **Session transcript** — the persistent, on-disk JSONL of everything that happened in a conversation. Survives restarts, replays at any time, drives the SPA's chat history.
- **Event stream** — the live runtime feed of typed events emitted as the agent loop runs. Powers the SPA in real time, drives subprocess hooks, and feeds the audit log.

You don't enable these. They run on every install, by default, with no extra services.

## Session transcript

Each session has a day-partitioned JSONL file at `~/.omnipus/sessions/<session_id>/<YYYY-MM-DD>.jsonl`. One line per entry, append-only, atomic writes via temp-file-rename.

### Entry types (`pkg/session/daypartition.go:30-40`)

| `type` value | What it records |
|---|---|
| `message` (or empty) | A user or assistant chat turn |
| `tool_call` | A tool invocation — name, args, status, duration, result |
| `compaction` | A context-compression summary written when token budget is exceeded |
| `system` | System-level event (boot, shutdown, sandbox state change) |
| `turn_canceled` | A turn that was cancelled mid-stream, with cancel method + descendants |

### What's on every entry (`pkg/session/daypartition.go:115-145`)

```json
{
  "id":         "01HMXG…",          // ULID
  "type":       "tool_call",         // see table above
  "role":       "assistant",         // user | assistant | system
  "content":    "…",
  "timestamp":  "2026-05-21T13:45:02.123Z",
  "tokens":     247,
  "cost":       0.0019,
  "status":     "ok",                // ok | error | interrupted
  "attachments": [/* media */],
  "tool_calls": [
    {
      "id":         "call_…",
      "tool":       "web_search",
      "status":     "success",      // success | error | pending | denied
      "duration_ms": 1230,
      "parameters": {"query": "…"},
      "result":     {"…"}
    }
  ],
  "agent_id":   "ray"
}
```

Every entry carries `agent_id` so multi-agent transcripts can be filtered to a single agent's contribution (FR-002).

### Tool call fidelity

Each `ToolCall` records the full parameters AND the full result. When sub-agents are involved, `parent_tool_call_id` correlates child calls to their parent spawn (FR-H-001), so a deeply nested `subagent` → `spawn` → tool chain is reconstructable.

### Cancel context

When a turn is cancelled, a dedicated `turn_canceled` entry is appended carrying `turn_id`, `canceled_by_user`, `canceled_by_channel`, `cancel_method` (graceful or hard), and any `descendants_canceled` (sub-agent IDs that were cascade-cancelled). Plus the assistant entry that got interrupted is marked `truncated: true`.

### Replay

The SPA reads the transcript from disk on demand. There's no separate "chat history" database — the JSONL **is** the history. To replay a session in code or in tests, `store.ReadTranscript(sessionID)` returns the entries in order.

## Live event stream

While a turn is running, the agent loop emits typed events into the in-process event bus (`pkg/agent/events.go`). 24 event kinds (`pkg/agent/events.go::EventKind`):

| Category | Events |
|---|---|
| **Turn lifecycle** | `turn_start`, `turn_end`, `turn_timeout` |
| **LLM lifecycle** | `llm_request`, `llm_delta`, `llm_response`, `llm_retry`, `empty_response_retry` |
| **Context management** | `context_compress`, `compaction_retry`, `session_summarize` |
| **Tool execution** | `tool_exec_start`, `tool_exec_end`, `tool_exec_skipped` |
| **Sub-turns (sub-agents, spawn)** | `sub_turn_spawn`, `sub_turn_end`, `sub_turn_result_delivered`, `sub_turn_orphan` |
| **Steering / interrupt** | `steering_injected`, `follow_up_queued`, `interrupt_received` |
| **System / errors** | `error`, `rate_limit`, `background_process_kill` |

### Who consumes the events

Three consumers in the runtime:

1. **WebSocket subscribers (SPA chat view).** Every event is fanned out to connected clients. The chat UI shows the live progression: "Tool call: web_search" → "Tool call complete" → "LLM response streaming" → "Turn end". This is what gives the chat its real-time feel.

2. **Subprocess hooks.** External processes can subscribe to the same event feed via JSON-RPC over stdin/stdout. Every event becomes a `hook.event` notification. Authors observe (read-only), or in-process hooks can intercept and rewrite. See [hooks/README.md](hooks/README.md). The wire format uses the canonical string name of each `EventKind` so authors don't carry private int → name tables (per issue #164).

3. **Audit log.** A subset of security-relevant events (tool calls, denials, sandbox state changes, cancel events, rate limits) are mirrored into `~/.omnipus/system/audit.jsonl` with HMAC chain integrity (v0.2 hardening, #155 item 1). See [security_configuration.md](security_configuration.md).

### Event payload shape

Every event carries:

```go
type Event struct {
    Kind      EventKind                  // canonical string on the wire
    Timestamp time.Time
    SessionID string
    AgentID   string
    Payload   map[string]any             // event-specific fields
}
```

For `tool_exec_start` the payload includes the tool name, arguments preview, and iteration. For `llm_request` the payload includes the model, message count, token budget, and tool list size. Full reference: read the call sites of `eventBus.Publish` in `pkg/agent/loop.go`.

### Buffer sizes

The observer subscription buffer is 1024 events (`pkg/agent/hooks.go::hookObserverBufferSize`, raised from 64 after a sustained-burst load test in #165). A slow consumer that falls behind drops events past the buffer rather than blocking the agent loop. The drop counter is exposed at `EventSubscription.Dropped()` for monitoring.

## Putting it together

A normal turn produces, in order:

1. `turn_start` event → SPA shows the spinner
2. Transcript entry: `type=message, role=user, content="..."` lands on disk
3. `llm_request` → SPA shows model + tool count
4. `llm_response` → SPA shows assistant message streaming in
5. `tool_exec_start` for each tool call → SPA renders tool-call chips
6. Transcript entry: `type=tool_call` per tool, with full params + result
7. `tool_exec_end` per tool
8. Final assistant text → second LLM round → `llm_response`
9. Transcript entry: `type=message, role=assistant, content="..."`
10. `turn_end` → spinner clears

Everything is captured. Nothing is silent. An operator can audit any turn after the fact by reading the JSONL; a developer can subscribe live via a subprocess hook to watch every event as it happens.

## See also

- [hooks/README.md](hooks/README.md) — how to write a subprocess hook that consumes the event stream
- [security_configuration.md](security_configuration.md) — audit log + HMAC chain + sensitive-data redaction
- [memory.md](memory.md) — how transcripts become memory via auto-recap at session close
