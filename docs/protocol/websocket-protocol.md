# WebSocket Protocol Reference

**Version**: 1.0
**Created**: 2026-04-01
**Status**: Authoritative

This document is the single canonical reference for all WebSocket frame types in Omnipus. All specs that reference WebSocket frames must link here rather than defining frames inline. Engineers implementing the backend (`pkg/gateway/`) and the frontend (`src/lib/ws.ts`, chat components) must treat this document as the contract between them.

**Endpoint**: `ws://<host>:<port>/api/v1/chat/ws` (HTTP Upgrade from `/api/v1/chat/ws`)

**Source**: Frame definitions derived from `src/lib/ws.ts` (TypeScript types) as the implemented reference. The backend Go implementation must match these schemas exactly.

---

## Connection Lifecycle

1. Client establishes WebSocket connection via HTTP Upgrade.
2. Client immediately sends an `auth` frame (if `OMNIPUS_BEARER_TOKEN` is configured).
3. Server authenticates; connection is ready for message exchange.
4. Client sends keep-alive `ping` frames every 30 seconds via the connection manager.
5. On disconnect, the client reconnects with a two-phase backoff (`src/lib/ws.ts:366-372`):
   - **Fast phase** — `MAX_FAST_ATTEMPTS = 10` retries with exponential backoff (`2^attempt * 1000 ms`) capped at `MAX_FAST_DELAY_MS = 30_000` ms.
   - **Slow phase** — `MAX_SLOW_ATTEMPTS = 20` retries at a fixed `SLOW_RETRY_DELAY_MS = 60_000` ms interval.
   - **Give up** — after 30 total attempts the connection surfaces an error and the user must click "Reconnect now" in the SPA.

---

## Client to Server Frames

These frames are sent by the frontend to the backend over the WebSocket connection.

### `auth`

Authenticates the WebSocket connection. Must be sent immediately after connection open when a bearer token is configured.

```json
{
  "type": "auth",
  "token": "<bearer_token>"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"auth"` |
| token | string | yes | The bearer token. The SPA reads it as `sessionStorage.getItem('omnipus_auth_token') ?? localStorage.getItem('omnipus_auth_token')` (`src/lib/ws.ts:688-689`); `sessionStorage` is preferred (XSS protection) and `localStorage` is the legacy fallback. |

**Producer**: `WsConnection._createSocket()` in `src/lib/ws.ts`
**Consumer**: Gateway auth middleware in `pkg/gateway/rest.go`

---

### `message`

Sends a user chat message to the agent for the given session.

```json
{
  "type": "message",
  "content": "What is the weather in London?",
  "session_id": "sess_abc123",
  "agent_id": "agent_def456"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"message"` |
| content | string | yes | The user's message text |
| session_id | string | no | The session to send the message to. If omitted, uses the active session. |
| agent_id | string | no | The agent to handle the message. If omitted, uses the active session's agent. |

**Producer**: Chat input component in `src/components/chat/`
**Consumer**: Gateway WebSocket handler in `pkg/gateway/`

---

### `cancel`

Cancels an in-progress streaming response or tool execution for the given session.

```json
{
  "type": "cancel",
  "session_id": "sess_abc123"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"cancel"` |
| session_id | string | yes | The session whose active turn is to be cancelled |

**Producer**: Stop button in chat input (`src/components/chat/`)
**Consumer**: Gateway WebSocket handler — cancels the LLM call and saves partial response

---

### `exec_approval_response`

Responds to a pending execution approval request from the backend.

```json
{
  "type": "exec_approval_response",
  "id": "approval_xyz789",
  "decision": "allow"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"exec_approval_response"` |
| id | string | yes | The approval request ID from the corresponding `exec_approval_request` frame |
| decision | string | yes | One of `"allow"`, `"deny"`, `"always"` |

**Producer**: Exec approval block component in `src/components/chat/`
**Consumer**: Gateway exec approval handler in `pkg/gateway/` or `pkg/tools/`

---

### `ping`

Keep-alive ping sent every 30 seconds to prevent proxy/firewall timeout. No response is expected from the server (distinct from WebSocket protocol-level ping/pong).

```json
{
  "type": "ping"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"ping"` |

**Producer**: `WsConnection._startHeartbeat()` in `src/lib/ws.ts`
**Consumer**: The server emits a `pong` frame in response (see `PongFrame` below); the SPA consumes that for liveness tracking and **does not** surface the `pong` to the chat reducers (`src/lib/ws.ts:591`). The `ping` itself is consumed by the SPA's `_startHeartbeat` after send.

---

### `whatsapp_pairing_subscribe`

Registers or clears THIS connection's interest in a channel's `whatsapp_pairing` (QR/status) frames. The SPA sends `active: true` when an operator opens a channel's pairing UI and `active: false` when they leave, so the QR pairing secret is delivered only to the connection that is watching it rather than to every authenticated admin connection (#283).

```json
{
  "type": "whatsapp_pairing_subscribe",
  "channel_id": "whatsapp_native",
  "active": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"whatsapp_pairing_subscribe"` |
| channel_id | string | yes | The channel whose pairing frames this connection wants (e.g. `"whatsapp_native"`) |
| active | boolean | yes | `true` to start receiving pairing frames for `channel_id` on this connection, `false` to stop |

**Producer**: `useWhatsAppPairingStore` / `WhatsAppNativeNotice` in the Configure panel (`src/store/whatsappPairing.ts`)
**Consumer**: Gateway WebSocket handler — gates per-connection delivery of `whatsapp_pairing` frames.

---

## Server to Client Frames

These frames are sent by the backend to the frontend over the WebSocket connection.

### `token`

A single token chunk from a streaming LLM response. Tokens are appended to the current assistant message as they arrive.

```json
{
  "type": "token",
  "session_id": "sess_abc123",
  "content": "Hello"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"token"` |
| session_id | string | yes | The session this token belongs to. Required by the AsyncAPI component schema (`TokenFrame`, `contracts/asyncapi.yaml:1033-1053`). |
| content | string | yes | The token text to append to the current assistant message |

**Producer**: Agent loop streaming path in `pkg/agent/`
**Consumer**: Chat message component — appends to streaming buffer with blinking cursor

---

### `done`

Signals that a streaming response has completed. The partial buffer is finalized and rendered as markdown.

```json
{
  "type": "done",
  "stats": {
    "tokens": 342,
    "cost": 0.0021,
    "duration_ms": 4120,
    "tokens_dropped": 0
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"done"` |
| stats | object | no | Usage statistics for the completed turn |
| stats.tokens | number | no | Total tokens consumed (prompt + completion) |
| stats.cost | number | no | Estimated cost in USD |
| stats.duration_ms | number | no | Wall-clock duration of the turn in milliseconds |
| stats.tokens_dropped | number | no | Tokens pruned during tool result compression |

**Producer**: Agent loop turn completion in `pkg/agent/`
**Consumer**: Chat message component — removes cursor, renders markdown, shows stats below message

---

### `error`

Signals an unrecoverable error during a turn. Renders inline as an error message.

```json
{
  "type": "error",
  "message": "Model context limit exceeded"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"error"` |
| message | string | yes | Human-readable error description |

**Producer**: Gateway and agent loop error handlers in `pkg/gateway/`, `pkg/agent/`
**Consumer**: Chat message component — renders inline error with retry button

---

### `tool_call_start`

Signals that the agent has invoked a tool and execution is beginning. Renders an in-progress tool call badge.

```json
{
  "type": "tool_call_start",
  "session_id": "sess_abc123",
  "tool": "bash",
  "call_id": "call_abc123",
  "params": {
    "command": "ls -la"
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"tool_call_start"` |
| session_id | string | yes | The session this tool call belongs to. Required by the AsyncAPI component schema (`ToolCallStartFrame`, `contracts/asyncapi.yaml:1127-1162`). |
| tool | string | yes | Tool name (e.g., `"bash"`, `"read_file"`, `"web_search"`) |
| call_id | string | yes | Unique identifier for this tool invocation |
| params | object | yes | Tool input parameters (tool-specific schema). **Always an object — never `null`** (nil-safety contract enforced in `ToolCallStartFrame`, `contracts/asyncapi.yaml:1130-1131`). |

**Producer**: Tool execution layer in `pkg/tools/`
**Consumer**: Tool call badge component — shows tool name + spinner, collapsed by default

---

### `tool_call_result`

Signals that a tool invocation has completed. Updates the tool call badge with result status.

```json
{
  "type": "tool_call_result",
  "session_id": "sess_abc123",
  "tool": "bash",
  "call_id": "call_abc123",
  "result": "file1.txt\nfile2.txt\n",
  "status": "success",
  "duration_ms": 142,
  "error": null
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"tool_call_result"` |
| session_id | string | yes | The session this tool call belongs to. Required by the AsyncAPI component schema (`ToolCallResultFrame`, `contracts/asyncapi.yaml:1220-1268`). |
| tool | string | yes | Tool name — matches the corresponding `tool_call_start` |
| call_id | string | yes | Tool invocation ID — matches the corresponding `tool_call_start` |
| result | any | yes | Tool output (type depends on tool; may be string, object, or null) |
| status | string | yes | One of `"success"` or `"error"` |
| duration_ms | number | no | Tool execution time in milliseconds |
| error | string | no | Error message if `status` is `"error"` |

**Producer**: Tool execution layer in `pkg/tools/`
**Consumer**: Tool call badge component — updates to success/error state, expandable for detail

---

### `exec_approval_request`

Requests explicit user approval before executing a potentially dangerous command. Renders an inline approval block that blocks further execution until the user responds.

```json
{
  "type": "exec_approval_request",
  "session_id": "sess_abc123",
  "id": "approval_xyz789",
  "command": "rm -rf /tmp/cache",
  "working_dir": "/home/user/project",
  "matched_policy": "requires_approval"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"exec_approval_request"` |
| session_id | string | yes | The session this approval belongs to. Required by the AsyncAPI component schema (`ExecApprovalRequestFrame`, `contracts/asyncapi.yaml:1348-1391`). |
| id | string | yes | Unique approval request ID — must be echoed in the `exec_approval_response` |
| command | string | yes | The full command string requiring approval |
| working_dir | string | no | The working directory in which the command will execute |
| matched_policy | string | no | The policy rule that triggered the approval requirement |

**Producer**: Exec approval gate in `pkg/tools/` or `pkg/policy/`
**Consumer**: Exec approval block component — renders command + 3 buttons (Allow / Deny / Always Allow)

---

### `exec_approval_expired`

Notifies the frontend that a pending approval request expired without a response. Updates the approval block to an expired state.

```json
{
  "type": "exec_approval_expired",
  "session_id": "sess_abc123",
  "id": "approval_xyz789"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"exec_approval_expired"` |
| session_id | string | yes | The session this expired approval belongs to. Required by the AsyncAPI component schema (`ExecApprovalExpiredFrame`, `contracts/asyncapi.yaml:1884-1904`). |
| id | string | yes | The approval request ID that expired |

**Producer**: Exec approval timeout handler in `pkg/policy/` or `pkg/agent/`
**Consumer**: Exec approval block component — updates buttons to disabled, shows "Expired" label

---

### `timeout`

System-initiated interruption: the backend timed out the turn mid-stream. Partial content (if any) is preserved. This event type must NOT be silently ignored — it is semantically equivalent to a system-initiated cancel and must render visibly. (MAJ-004)

```json
{
  "type": "timeout",
  "partial_content": "The weather in London is...",
  "message": "Turn timed out after retry."
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"timeout"` |
| partial_content | string | no | Any tokens streamed before the timeout (may be empty string) |
| message | string | yes | Human-readable explanation shown below the partial message |

**Producer**: Agent reliability layer in `pkg/agent/` (turn timeout handler)
**Consumer**: Chat message component — renders partial content (if any) followed by a muted "(timed out)" label, analogous to cancel's "(interrupted)" label. Does NOT send a `done` frame; the `timeout` frame serves as the turn terminator.

---

### `compaction`

System-initiated notification: the backend compacted the conversation context (pruned old messages and replaced them with a summary). Renders as a system message in the chat thread. (MAJ-004)

```json
{
  "type": "compaction",
  "summary": "Earlier conversation about weather API integration has been summarized to preserve context window space."
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"compaction"` |
| summary | string | yes | A human-readable summary of what was compacted |

**Producer**: Context compaction layer in `pkg/agent/`
**Consumer**: Chat message component — renders as a system message "Context compacted — [summary]", styled as a neutral info banner (not user/assistant bubble). Analogous to the compaction entry rendered from JSONL transcripts in message history.

---

### `task_status_changed`

Notifies the frontend that a task's status has changed, enabling real-time task board updates without polling. (MAJ-004, agent-task-management-spec)

```json
{
  "type": "task_status_changed",
  "session_id": "sess_abc123",
  "task_id": "task_abc123",
  "status": "assigned",
  "agent_id": "agent_def456",
  "updated_at": "2026-04-01T12:34:56Z"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"task_status_changed"` |
| session_id | string | yes | The session the task belongs to. Required by the AsyncAPI component schema (`TaskStatusChangedFrame`, `contracts/asyncapi.yaml:1393-1422`). |
| task_id | string | yes | The ID of the task whose status changed |
| status | string | yes | New status: one of `"queued"`, `"assigned"`, `"running"`, `"completed"`, `"failed"` (five-value enum per `contracts/asyncapi.yaml:1413-1420`) |
| agent_id | string | yes | The agent the task is assigned to |
| updated_at | string | yes | ISO 8601 timestamp of the status change |

**Producer**: Task executor in `pkg/agent/task_executor.go`
**Consumer**: Task board component (`TaskList.tsx`) — invalidates TanStack Query cache for the task list, triggering a re-render. The frame is broadcast to all authenticated WebSocket connections for the workspace (not session-scoped).

---

### `whatsapp_pairing`

Emitted by the WhatsApp native/QR channel during linked-device pairing so the SPA can render the QR code and live status in the browser instead of requiring the operator to read the gateway terminal (#283). Delivered only to connections that have sent a `whatsapp_pairing_subscribe` with `active: true` for the matching `channel_id`.

```json
{
  "type": "whatsapp_pairing",
  "channel_id": "whatsapp_native",
  "status": "code",
  "qr": "2@abc123...",
  "message": ""
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"whatsapp_pairing"` |
| channel_id | string | yes | The channel emitting the pairing event (e.g. `"whatsapp_native"`) |
| status | string | yes | Pairing lifecycle: `"waiting"` (connecting), `"code"` (a QR is available to scan), `"linked"` (device paired), `"timeout"` (the displayed QR expired and a fresh one follows), `"error"` (pairing failed — see `message`) |
| qr | string | no | The QR payload to render (whatsmeow linked-device code). Present when `status` is `"code"`; empty otherwise. Transient pairing secret — the SPA holds it only while the config panel is open and never persists it |
| message | string | no | Optional human-readable detail, e.g. the error reason when `status` is `"error"` |

**Producer**: WhatsApp native channel (`pkg/channels/whatsapp_native/`), forwarded by the gateway WebSocket handler (`pkg/gateway/websocket.go`)
**Consumer**: `WhatsAppNativeNotice` in the channel Configure panel — renders the QR (`qrcode.react`) when `status === "code"` and the live waiting → linked status otherwise.

---

## Frame Type Summary

| Direction | Type | Description |
|-----------|------|-------------|
| C→S | `auth` | Authenticate the connection |
| C→S | `message` | Send a user chat message |
| C→S | `cancel` | Cancel in-progress turn |
| C→S | `exec_approval_response` | Respond to exec approval request |
| C→S | `ping` | Keep-alive |
| C→S | `attach_session` | Attach to an existing session and replay its transcript (with optional `since` for incremental replay) |
| C→S | `device_pairing_response` | Admin approval/rejection of a pending device pairing request |
| C→S | `session_close` | Explicit session close request (server replies with `session_close_ack`) |
| C→S | `whatsapp_pairing_subscribe` | Start/stop receiving WhatsApp pairing frames on this connection (#283) |
| S→C | `session_started` | New session ID minted by the server (response to a `message` without `session_id`) |
| S→C | `token` | LLM response token chunk |
| S→C | `done` | Turn complete, with usage stats |
| S→C | `error` | Unrecoverable error (does not terminate the connection) |
| S→C | `tool_call_start` | Tool invocation began |
| S→C | `tool_call_result` | Tool invocation completed |
| S→C | `subagent_start` | Subagent span opened (FR-H-004) |
| S→C | `subagent_end` | Subagent span closed (FR-H-004) |
| S→C | `exec_approval_request` | Approval required before command execution |
| S→C | `exec_approval_expired` | Pending approval timed out |
| S→C | `exec_approval_response_ack` | Server acknowledgement that an `exec_approval_response` was resolved |
| S→C | `task_status_changed` | Task status updated (MAJ-004) |
| S→C | `replay_message` | One replayed transcript entry during an `attach_session` |
| S→C | `rate_limit` | Rate-limit denial applied to an agent action (SEC-26) |
| S→C | `media` | One or more media attachments from a tool (parts array, never null) |
| S→C | `agent_switched` | Active agent for a session changed (`switch_agent`) |
| S→C | `tool_approval_required` | Tool call paused for ask-policy approval (FR-011, FR-082); `args` always an object, never null |
| S→C | `session_state` | One-shot approval-state snapshot on every WS reconnect (FR-052, FR-073, FR-081) |
| S→C | `system_overload` | System at capacity — an agent action was blocked (FR-016, MAJ-009) |
| S→C | `replay_warning` | Transcript contained duplicate `tool_call_id`s during replay |
| S→C | `cancel_stage` | Cancel progress notification (B3) — `graceful`, `hard`, or `detached` |
| S→C | `pong` | Server-originated heartbeat reply to a client `ping` (consumed by SPA for liveness; not surfaced to reducers) |
| S→C | `session_close_ack` | Acknowledgement that a `session_close` request was processed |
| S→C | `device_pairing_request` | A remote device requests pairing; SPA shows the approval modal |
| S→C | `timeout` | System-initiated turn timeout (MAJ-004) |
| S→C | `compaction` | Context window compacted (MAJ-004) |
| S→C | `whatsapp_pairing` | WhatsApp linked-device pairing QR/status (#283) |
| S→C | `notification` | User-facing notification raised for the recipient user (#264); delivered only to that user's connections |

The complete type set, including required fields and per-field constraints, is
defined in `contracts/asyncapi.yaml` (components: `WsFrameType` enum at
`contracts/asyncapi.yaml:803-835`, and the per-frame component schemas
thereafter) and is the implementation reference in `src/lib/ws.ts:14-57`
(generated AsyncAPI TypeScript types). Any new frame type must be added to
the AsyncAPI spec first, then to this document.

---

## Unknown Frame Types

When the frontend receives a frame with an unrecognized `type` field, it MUST log a warning to the console and discard the frame — it must NOT crash or disconnect. Exception: if the unknown frame count exceeds the `droppedFrameThreshold` (currently 5 consecutive invalid frames), an error is surfaced to the user.

When the backend receives an unrecognized `type` field from the client, it MUST return an `error` frame describing the unknown type and continue — it must NOT close the connection.

---

## Maintenance

This document is owned by the backend-lead. Any spec adding a new WebSocket frame type must:
1. Add the frame definition to this document before implementation.
2. Update the Frame Type Summary table.
3. Specify producer and consumer components.
4. Reference this document from the relevant spec.
