# WebSocket Protocol Reference

**Version**: 1.0
**Created**: 2026-04-01
**Status**: Explanatory guide (the AsyncAPI contract is authoritative)

This page is an explanatory protocol guide. The authoritative wire contract is the AsyncAPI specification (`contracts/asyncapi.yaml`); frontend and backend frame types are generated from it. When this guide and the generated contract disagree, follow the contract. The endpoint is `/api/v1/chat/ws` on the gateway's HTTP or HTTPS origin; use `ws` or `wss` accordingly.

---

## Connection Lifecycle

1. Client establishes WebSocket connection via HTTP Upgrade.
2. The web app authenticates through its HttpOnly `omnipus-session` cookie, automatically attached to the WebSocket handshake. It neither reads a bearer token from browser storage nor sends an `auth` frame. Programmatic clients without that cookie can use the gateway's supported first-frame bearer authentication (see `auth` below). The authentication handler is `WSHandler.authenticateWS` in `pkg/gateway/websocket.go`; the web app's connection manager only opens the socket.
3. Server authenticates; connection is ready for message exchange.
4. The client sends an application `ping` every 30 seconds via the connection manager; the gateway replies with `pong` for liveness tracking.
5. On disconnect, the client reconnects with a two-phase backoff (`src/lib/ws.ts::WsConnection._scheduleReconnect`):
   - **Fast phase** — `MAX_FAST_ATTEMPTS = 10` retries with exponential backoff (`2^attempt * 1000 ms`) capped at `MAX_FAST_DELAY_MS = 30_000` ms.
   - **Slow phase** — `MAX_SLOW_ATTEMPTS = 20` retries at a fixed `SLOW_RETRY_DELAY_MS = 60_000` ms interval.
   - **Give up** — after ten fast and twenty slow reconnect attempts, the connection manager reports the `gave_up` reconnect state. Chat shows its reconnect recovery control rather than a technical error banner. Authentication rejection is separate and can sign the user out.

---

### Bounded history and control catch-up

The gateway and web app must use matching generated contracts; reload older tabs after deploying the paged-history change. A snapshot reconstructs a bounded recent page, not the session's lifetime. Ordinary incremental catch-up still delivers the full servable journal tail; page-size options do not truncate it.

```text
attach → access/parameter checks → bind and hold live frames at W
  incremental: current session_state → journal tail → catch_up_complete(W)
  snapshot: session_snapshot(page metadata) → current session_state
            → source-owned history frames → replay done
            → uncovered active projection → catch_up_complete(W)
  snapshot history unavailable: session_state(history_state=unavailable)
            → current projection → catch_up_complete(W, live_only)
  servable journal with unavailable history: session_state(history_state=unavailable)
            → full journal tail → catch_up_complete(W, incremental)
then held live frames after W, in order
```

Replay `done` and `catch_up_complete` are distinct: current projection can arrive after replay done. Below the local attach-buffer cap, the web client holds the historical batch through successful complete and commits it once. If the buffer exceeds 8 MiB of retained serialized UTF-16 data or 4,096 frames, it shows the large-history notice and an initially blank reader can display saved frames progressively before completion; a cached reader stays in place. Partial display is not history-success proof. The client never treats a live done as attach completion and discards incomplete history on refusal/replay failure/disconnect, restoring prior cached history and preserving genuinely pending input. An incomplete socket/control delivery cannot complete catch-up or advance a successful cursor.

A person reading older history keeps that reader window, opened clear boundaries, focus and scroll anchor through a replacement snapshot. The new recent page goes into a separate bounded tail cache. After completed catch-up, the browser revalidates only the kept closed interval through REST window-refresh pages. A failed interval check leaves cached, explicitly out-of-date rows and range-only Retry; it does not roll back a completed live cursor or return the reader to latest.

## Client to Server Frames

These frames are sent by the frontend to the backend over the WebSocket connection.

### `attach_session` — bounded history and live catch-up

Attach names the authorized session. Optional `since_seq` / `boot_id` request journal catch-up from a previously completed live cursor. Optional `history_limit` targets 20–100 drawn-row units for a snapshot (default 40); `history_min_rows` is its advisory 1–limit two-screen floor (default 20). Null, wrong types, fractional/exponent and out-of-range values are rejected. Session syntax and addressed-session access are checked before these history values; invalid values never bind, wipe history or send successful completion. Inbound-validation on/off uses the same precedence. A malformed envelope that cannot decode into an attach frame is dropped before binding; the operator receives one sanitized `ws: malformed attach_session frame` warning with a fixed phase classification, never the raw body, credentials, session identity or decoder cause. This syntax warning is separate from an authorized history-page attempt; a following valid attach can still proceed.

| Branch | Ordered output | What completion proves |
|---|---|---|
| Servable incremental cursor | Current `session_state`, the complete journal tail, `catch_up_complete(mode=incremental)`, then held live frames. The state can still report `history_state=unavailable`. | Live/control catch-up to the announced sequence. History limits never cut the journal tail. Unavailable history stays unavailable; this is not reconstructed-history proof and does not clear its error. |
| Latest-page snapshot | Prepare the bounded page first; then `session_snapshot`, current `session_state`, selected source-derived historical frames, one replay `done`, uncovered active projection, `catch_up_complete(mode=snapshot)`, then held live frames. | The selected historical page and current live/control catch-up completed. It does not claim lifetime replay. |
| Snapshot history unavailable, authorized controls deliverable | No snapshot/wipe. Send typed unavailable `history_state` in current `session_state`, current active projection, `catch_up_complete(mode=live_only)`, then held live frames. | Current live/control catch-up only. It does not reconstruct history, clear the history error or acknowledge attention as seen. A servable journal instead follows the incremental branch above, even while history remains unavailable. |
| Control, socket or cancellation failure | Existing failed-attach recovery; no successful completion over an incomplete catch-up. | No new completed cursor or history proof. |

A missing/inconsistent index or suffix above 64 records is not a reason to stop a turn or to silently build partial history. The unavailable history area can show Retry while the composer, Stop, approvals, questions and other current controls remain independently usable. Explicit history Retry attaches **without** `since_seq`, attempts bounded recovery once, and never resends user input. A later journal-only catch-up is still not proof that history loaded.

`session_snapshot` carries canonical `page_info`, `has_earlier` / `has_later`, optional `next_before` / `next_after`, and the actual selected source range/context. It has no second messages array or ordinal ownership manifest. A historical reader keeps its loaded window, anchor and focus, stages the recent page into its separate tail cache, and revalidates only its already loaded closed interval through REST. A snapshot reason such as `boot_mismatch` or `retention_exceeded` does not authorize a passive jump to latest.

Replay `done` and `catch_up_complete` are different boundaries: active projection can follow replay done. Replay done's `stats.frames_emitted` counts the actual prepared content frames, excluding the done terminator; it is not a lifetime-history count. Below the local attach-buffer cap, matching clients stage historical replay through successful catch-up completion and commit the batch once, not once per content frame; the progressive exception is described above. The completion mode must be handled explicitly; `live_only` releases live/control attach locks but never sets history-loaded or replay-completed state. Deploy the matching server and client together and reload old tabs.

### Recorded session usage during attach

A session-scoped `session_state` includes optional `session_tokens_total` only when the lifetime accounting baseline is **known**, including on `live_only` attach. It is a nonnegative integer reporting recorded session consumption, independent of the selected history page. It does not imply that history loaded. Unscoped connection-open state omits it; unavailable accounting is unknown and also omits it. A verified unused session can supply real zero. The corresponding REST page uses the same optional absolute field and known-baseline check.

Session detail's legacy `session.stats.tokens_total` uses the same provider/cache accounting definition but does **not** guarantee that the lifetime baseline is valid. After baseline loss it can contain zero or a partial, unproven number while the optional `session_tokens_total` field correctly remains absent. New activity does not reconstruct the missing historical baseline. **Sessions badges and Usage → By session remain affected until [#1430](https://github.com/elicify-ai/omnipus/issues/1430)**; their legacy values are not proof of authoritative lifetime usage in that state. Legacy metadata can also seed the chat counter; later omission of an absolute observation neither validates nor clears that seed.

Apply an absolute observation only to its addressed session/view, retaining a newer known total when a stale page or replay arrives. Do not add an absolute value to the per-turn delta. Replay completion, duplicate delivery and an omitted field must not reset or double-count usage. No extra field on `session_snapshot` is required: its current-state frame carries the optional known-baseline observation.

### `auth`

First-frame bearer authentication for programmatic clients that do not carry the `omnipus-session` cookie. The web app does not send this frame.

```json
{
  "type": "auth",
  "token": "<bearer_token>"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"auth"` |
| token | string | yes | The bearer token. |

**Schema**: `contracts/asyncapi.yaml::AuthFrame`
**Consumer**: `pkg/gateway/websocket.go::WSHandler.authenticateWS`

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
| session_id | string | no | Include to continue an existing session; omit to start a new session. |
| agent_id | string | no | The agent to handle the message. If omitted, uses the active session's agent. |
| client_message_id | string | no | Your ID for this message (1 to 128 characters). With it the gateway reports the message's delivery with `message_status` frames and ties any refusal to the message. |
| recipient | object `{workspace_id, agent_id}` | no | Send the message as a request to a peer agent, the way the web app's `@` menu does. Valid only together with `session_id`; refused otherwise. The request is delivered to that agent's own chat under a header the server writes (who sent it, and how to answer); an answer comes back into this chat only when the receiving agent answers with the `answer_request` tool. If the receiver is stopped, the request is held (`message_status` `held`) and delivered when it runs again. |
| auto_approve | boolean or null | no | Auto-approve choice for a **new** session only: `true` turns Auto on for that chat, even if the global default is off; `false` turns it off; `null` or omission leaves it following the global default. Ignored when `session_id` is present; use `session_mode_update` to change an existing session. |

To start a new chat with Auto off from its first tool call, carry the choice on the first `message` and omit `session_id`:

```json
{
  "type": "message",
  "content": "Review the project before making changes.",
  "agent_id": "agent_def456",
  "auto_approve": false
}
```

The gateway records this choice before dispatching the new session's first turn. Sending a separate `session_mode_update` only after the `session_started` acknowledgement can be too late for the first tool call. Auto is a separate setting for tools whose effective policy is `ask`; it does not change an `allow` or `deny` policy and does not require an enforcing kernel sandbox.

**Schema**: `contracts/asyncapi.yaml::MessageFrame`
**Producer**: Chat input component in `src/components/chat/`
**Consumer**: `pkg/gateway/websocket.go::dispatchFrame`, then `pkg/gateway/websocket_chat.go::applyMintTimeAutoApproveChoice` for a new session

---

### `session_mode_update`

**Direction**: Client → server. Set or clear the Auto-approve choice for an existing session. This per-chat choice may turn Auto on even when the global default is off, because a human is making the choice in that chat. It does not change the global configuration or the tool's `allow`/`ask`/`deny` policy.

```json
{
  "type": "session_mode_update",
  "session_id": "sess_abc123",
  "auto_approve": false
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"session_mode_update"` |
| session_id | string | yes | Non-empty ID of an existing session known to the gateway |
| auto_approve | boolean or null | yes | `true` turns Auto on for this chat; `false` turns it off; `null` clears the choice so the chat follows the current global default and future changes to it. Omission is invalid. |

A valid request receives `session_mode_updated` with the resolved per-session setting. A malformed request or unknown session receives an `error` frame instead. The per-chat choice is held in the session-mode store, not written to `config.json`, and does not survive a server restart.

**Schema**: `contracts/asyncapi.yaml::SessionModeUpdateFrame`
**Producer**: Chat store's `sendSessionModeUpdate` action in `src/store/chat/slices/outbound-responses.ts`
**Consumer**: `pkg/gateway/ws_session_mode.go::handleSessionModeUpdateFrame`

---

### `attach_session`

Attach to an authorized existing session. `since_seq` plus `boot_id` requests incremental catch-up when the journal can serve that cursor; otherwise the server selects a recent snapshot. Access and session eligibility precede paging validation and binding.

| Field | Meaning |
|---|---|
| `session_id` | Addressed session; no parent/other-session fallback. |
| `since_seq`, `boot_id` | Live transport cursor, separate from REST occurrence positions. Omit for an explicit fresh history Retry. |
| `history_limit` | Optional integer 20–100, default 40; logical drawn-row target for snapshots only. |
| `history_min_rows` | Optional integer 1–effective limit, default 20; advisory estimated two-screen floor. Whole source groups and this floor take precedence over the 4 MiB soft historical-page target. |

With known geometry, the browser estimates roughly four screens for opening and two for the floor. Otherwise it omits the size fields and uses server defaults; it does not send a second attach merely to correct sizing. REST contiguous top-ups correct actual height underfill. Invalid size fields reject before bind/wipe, never become an unlimited replay.

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

### `ping`

Application-level keep-alive sent every 30 seconds to prevent proxy/firewall timeout. The gateway replies with a `pong` frame (distinct from WebSocket protocol-level ping/pong).

```json
{
  "type": "ping"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"ping"` |

**Producer**: `WsConnection._startHeartbeat()` in `src/lib/ws.ts`
**Consumer**: `pkg/gateway/websocket.go::wsHandlerReadLoop.dispatchFrame` replies with `pong`. The connection manager consumes `pong`; chat reducers do not receive it.

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

### History metadata, ownership and availability

| Frame/field | Contract |
|---|---|
| `session_snapshot` | Carries the selected page's `page_info`, `has_earlier`/`has_later` and matching optional cursors. No duplicate entries array or ordinal/frame-range manifest. A rejected history read sends no successful snapshot or history wipe. |
| Source frame `history_origin` | `{position, entry_id?}` names the actual persisted occurrence. Narration, tool start/result, source-derived special/media/span/question frames carry it when replayed from a page. A tool-only source does not borrow the previous assistant bubble. |
| Session-only frames | Replay warning/done, current controls and session-only terminal-card rehydration have no invented chronological owner. A replayed pending tool has no fabricated terminal result. |
| Session-scoped `session_state.history_state` | `available`, or `unavailable` with sanitized `reason` and `message`. Missing history metadata is not successful empty history. Reasons are `index_missing`, `index_inconsistent`, `catchup_limit`, `storage_error`, `projection_error`. |
| `catch_up_complete.mode` | `snapshot`, `incremental`, or `live_only`. Only completed control/projection delivery advances the live cursor. `live_only` is explicitly not reconstructed history, a history-derived restart/unanswered sweep or an attention-seen acknowledgement. |

When history is unavailable but access and current control delivery succeed, the connection remains bound. The web app shows a persistent history-only ErrorState/Retry while Stop, current approvals/questions and the normal composer remain in their independent owners. Existing question/read-only/connection locks still apply. New output is distinguished as **Live output — earlier history unavailable**, not concatenated into a falsely gap-free history. New sends follow ordinary durable-save rules and are not automatically resent.

History Retry sends `attach_session` **without** `since_seq`/`boot_id`, preserving already-ready current controls and pending input. A repeated history refusal can complete controls as `live_only` again; it does not start a repair poll or lifetime rebuild. An incremental live reconnect while history remains unavailable does not clear that error. Only validated history success clears the matching history error. See [paged REST history](../operations/observability.md#paged-session-api).

### Prepared historical media and readiness

Each entry-derived historical frame has generated `history_origin={position, entry_id?}`. The opaque 47-character position names its actual persisted source occurrence; the optional entry ID is that occurrence's recorded nonempty ID. It is not an ordering number, transport sequence, model position or permission grant. A tool-only entry can own tool frames without a preceding prose bubble. Every source group the page counts carries at least one frame with its origin: a record that has nothing of its own to replay (for example the content-less assistant record that starts an agent turn) is sent as one empty `replay_message` with that origin, so `source_group_count`, the first/last positions and `row_count` always match the frames. The web client hides such a record when it is a blank, otherwise-successful assistant record with no tools, media, status explanation or other visible content; this is not a guarantee for every empty system or special record, and a system-role carrier can still draw an empty container. Reserving that source's recorded ID for an early tool does not count as already-applied narration: later prose for the same source is retained once, with its tool rows. Finished start/result frames share their owning source; relevant span dependencies are bounded public facts, not a child's full history. For a legacy delegation with no saved task or label, its span uses the recorded tool name as a display label rather than inventing task text.

Tool-derived media is deduplicated across the selected page by its resolved underlying file, not merely by reference string or source group. If two distinct references name the same file, the earliest selected occurrence keeps the media frame and its original `history_origin`; a genuinely different file still appears. Tool rows and their saved results remain intact. Selection reuses prepared media and resolved references rather than resolving or replaying each cumulative candidate again.

Ownership applies to replay messages, tool starts/results, replay errors, provider-fallback and context-window notices, judge verdicts, handover/helper/goal events, persisted subagent lifecycle frames, tool-derived media and source-positioned historical question cards. Session-only replay warnings, replay done and terminal-card rehydration have no chronological source origin. Current session state, active projection and held live output retain their separate current/live authority.

Attach's session-scoped `session_state` includes generated `history_state`. `status=available` omits reason/message. `status=unavailable` requires both: reason is `index_missing`, `index_inconsistent`, `catchup_limit`, `storage_error` or `projection_error`, and message is fixed sanitized copy, never the storage cause. The unscoped connection-open state does not claim history readiness. Readiness is not proof that this client has loaded a page; only actual successful history application clears that client-side error.

Session-scoped attach state can also carry optional generated `session_tokens_total`, the nonnegative absolute recorded session consumption, including during `live_only`. It has the same meaning as `SessionStats.tokens_total` and the REST history envelope's field. The unscoped connection-open state supplies no session usage baseline. A missing value is unknown, not a zero or a reason to erase a known total.

### `session_mode_updated`

**Direction**: Server → client. Acknowledges a valid `session_mode_update` request with the resulting Auto-approve setting for that session, including when the request cleared the per-chat choice.

```json
{
  "type": "session_mode_updated",
  "session_id": "sess_abc123",
  "auto_approve_effective": false
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"session_mode_updated"` |
| session_id | string | yes | Non-empty ID of the session whose setting was updated |
| auto_approve_effective | boolean | yes | Resolved Auto-approve setting for **this session** after applying or clearing the choice. Unlike the gateway-wide default in `SandboxStatus.auto_approve_effective`, this value includes the per-chat modifier. It is not a kernel-enforcement signal; tool-specific Auto checks can still require a prompt. |

**Schema**: `contracts/asyncapi.yaml::SessionModeUpdatedFrame`
**Producer**: `pkg/gateway/ws_session_mode.go::handleSessionModeUpdateFrame`
**Consumer**: Chat store's `session_mode_updated` case in `src/store/chat/slices/replay-and-status-frames.ts`, which updates the session's `autoApproveEffective` value

---

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
| session_id | string | yes | The session this token belongs to. Required by the AsyncAPI component schema (`TokenFrame`). |
| content | string | yes | The token text to append to the current assistant message |

**Producer**: Agent loop streaming path in `pkg/agent/`
**Consumer**: Chat message component — appends to streaming buffer with blinking cursor

---

### `done`

Signals that a streaming response has completed. The partial buffer is finalized and rendered as markdown.

```json
{
  "type": "done",
  "session_id": "sess_abc123",
  "stats": {
    "tokens": 342,
    "duration_ms": 4120
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"done"` |
| session_id | string | yes | Session whose turn has completed |
| stats | object | no | Usage statistics for the completed turn (always an object, never null, when present) |
| stats.tokens | number | no | Accounted tokens consumed by this turn; retains its existing per-turn meaning |
| stats.session_tokens_total | integer | no | Authoritative absolute session-wide consumed-token total, minimum zero; omitted when unknown and on replay-only done, never JSON null |
| stats.frames_emitted | number | no | Replay-only count of prepared content frames, excluding the terminator; not tokens or lifetime history |
| stats.cost | number | no | Estimated cost in USD |
| stats.duration_ms | number | no | Wall-clock duration of the turn in milliseconds |

The footer uses the absolute session observation, not that value plus `stats.tokens`. Merge known observations for the addressed session without decreasing a newer total. A replay-terminating `done` carries replay diagnostics, not an absolute usage baseline; duplicate/live/replay overlap and stale pages must not add the same turn twice. A recorded zero is distinct from missing usage.

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
| session_id | string | no | The session the error relates to. |
| client_message_id | string | no | Present when the error is about one message you sent. |

An error that carries both `session_id` and `client_message_id` (and no `first_message_error`) is about that one message: it was refused, the error ends no turn, and the chat's running turn continues. A `message_status` frame with `state: "failed"` for the same message follows it.

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
| session_id | string | yes | The session this tool call belongs to. Required by the AsyncAPI component schema (`ToolCallStartFrame`). |
| tool | string | yes | Tool name (e.g., `"bash"`, `"read_file"`, `"web_search"`) |
| call_id | string | yes | Unique identifier for this tool invocation |
| params | object | yes | Tool input parameters (tool-specific schema). **Always an object — never `null`** (nil-safety contract enforced in `ToolCallStartFrame`). |

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
  "duration_ms": 142
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"tool_call_result"` |
| session_id | string | yes | The session this tool call belongs to. Required by the AsyncAPI component schema (`ToolCallResultFrame`). |
| tool | string | yes | Tool name — matches the corresponding `tool_call_start` |
| call_id | string | yes | Tool invocation ID — matches the corresponding `tool_call_start` |
| result | any | yes | Tool output (type depends on tool; may be string, object, or null) |
| status | string | yes | One of `"success"`, `"error"` or `"cancelled"`; saved cancellation is not converted to error during replay |
| duration_ms | number | no | Tool execution time in milliseconds |
| error | string | no | Error message if `status` is `"error"`; omit the field rather than sending JSON null |

**Producer**: Tool execution layer in `pkg/tools/`
A confirmed cancelled native call retains its original call identity, parameters and owner in saved history, with known duration and `result: null` when no result was admitted. This terminal event also follows the normal attach bind/hold journal ordering. A late execution return cannot replace cancellation, and reload does not execute a tool. The separate `turn_canceled` marker describes the interrupted turn; it is not a substitute tool result. Historical calls that were never saved are not invented during replay.

**Consumer**: Tool call badge component — consumes success/error/cancelled state, expandable for available detail

For a cancelled call, required `result` can be null when no result is available; it is not fake successful output. The original call ID, tool and owning source remain intact through REST paging and snapshots. Replaying those facts does not execute the tool again or cancel an unrelated call.

A call still recorded as `pending` or `running` in saved history replays only its `tool_call_start`; no `tool_call_result` is invented for it from that status alone. This covers ordinary calls and delegation calls whose span carries no saved closing evidence; a delegation call whose span already has a saved end is reconstructed from that span evidence, and a contradictory record (a `pending` call whose span evidence says closed) can then surface a result mapped to `error`. On attach, a historical start and its terminal result are covered independently: when history replayed only the start (the call was still pending at the history cut), the active projection still delivers that call's terminal `tool_call_result` once, without a second start. A result is never delivered without its start.


---

### `task_status_changed`

Notifies the frontend that a task's status has changed, enabling real-time task board updates without polling. (MAJ-004, agent-task-management-spec)

```json
{
  "type": "task_status_changed",
  "session_id": "sess_abc123",
  "task_id": "task_abc123",
  "status": "in_progress",
  "agent_id": "agent_def456"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | yes | Always `"task_status_changed"` |
| session_id | string | yes | The session the task belongs to. Required by the AsyncAPI component schema (`contracts/components/schemas/TaskStatusChangedFrame.yaml`). |
| task_id | string | yes | The ID of the task whose status changed |
| status | string | yes | New status: one of `"inbox"`, `"next"`, `"in_progress"`, `"blocked"`, `"done"` or `"failed"` (`contracts/components/schemas/TaskStatusChangedFrame.yaml`) |
| agent_id | string | no | The agent managing the task |

`updated_at` is not a property of this frame.

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
| C→S | `auth` | First-frame bearer authentication for programmatic clients (the web app uses its session cookie) |
| C→S | `message` | Send a user chat message; optional `auto_approve` sets the initial choice when starting a new session |
| C→S | `session_mode_update` | Set or clear an existing session's Auto-approve choice |
| C→S | `cancel` | Cancel in-progress turn |
| C→S | `ping` | Keep-alive |
| C→S | `attach_session` | Attach to an authorized session. Use `since_seq` and `boot_id` for servable incremental catch-up; otherwise receive a bounded recent snapshot or an unavailable-history live/control result. |
| C→S | `device_pairing_response` | Admin approval/rejection of a pending device pairing request |
| C→S | `whatsapp_pairing_subscribe` | Start/stop receiving WhatsApp pairing frames on this connection (#283) |
| S→C | `session_started` | New session ID minted by the server (response to a `message` without `session_id`) |
| S→C | `session_mode_updated` | Acknowledge a per-chat mode change with the resolved Auto-approve setting |
| S→C | `token` | LLM response token chunk |
| S→C | `done` | Turn complete, with usage stats |
| S→C | `error` | Unrecoverable error (does not terminate the connection) |
| S→C | `message_status` | Delivery state of one message you sent with a `client_message_id`: `received`, `working`, `failed`, `discarded`, or `held` (an `@` request waiting because the recipient is stopped; it is not a failure) |
| S→C | `tool_call_start` | Tool invocation began |
| S→C | `tool_call_result` | Tool invocation completed |
| S→C | `subagent_start` | Subagent span opened (FR-H-004) |
| S→C | `subagent_end` | Subagent span closed (FR-H-004) |
| S→C | `task_status_changed` | Task status updated (MAJ-004) |
| S→C | `replay_message` | One replayed transcript entry during an `attach_session` |
| S→C | `rate_limit` | Rate-limit denial applied to an agent action (SEC-26) |
| S→C | `media` | One or more media attachments from a tool (parts array, never null) |
| S→C | `tool_approval_required` | Tool call paused for ask-policy approval (FR-011, FR-082); `args` always an object, never null |
| S→C | `tool_approval_resolved` | A pending tool approval was resolved (approved, denied, or expired) — sent to every connected tab so all of them drop the request together |
| S→C | `session_state` | One-shot approval-state snapshot on every WS reconnect (FR-052, FR-073, FR-081) |
| S→C | `system_overload` | System at capacity — an agent action was blocked (FR-016, MAJ-009) |
| S→C | `replay_warning` | Transcript contained duplicate `tool_call_id`s during replay |
| S→C | `cancel_stage` | Cancel progress notification (B3) — `graceful`, `hard`, or `detached` |
| S→C | `pong` | Server-originated heartbeat reply to a client `ping` (consumed by SPA for liveness; not surfaced to reducers) |
| S→C | `device_pairing_request` | A remote device requests pairing; SPA shows the approval modal |
| S→C | `whatsapp_pairing` | WhatsApp linked-device pairing QR/status (#283) |
| S→C | `notification` | User-facing notification raised for the recipient user (#264); delivered only to that user's connections |

The current complete frame discriminator set and every field constraint are defined by the generated AsyncAPI contract (`contracts/asyncapi.yaml`, `WsFrameType` and the per-frame component schemas). This page's examples are not a complete frame inventory. In particular, current session-core clients also use redirect, message delivery/status, subagent state/message, tool-result projection, snapshot/catch-up and attention-acknowledgement fields. Consult the corresponding current component schema before implementing a client. Any new frame type must be added to the AsyncAPI spec first, then to this document.

The `timeout`, `compaction`, `session_close` and `session_close_ack` frame types are not part of the current protocol. Context relief changes the retained model view without a summarization call. Stop uses `cancel` with `scope: "session"` or `scope: "tree"`; it ends selected turns, not their goals. Do not wait for an explicit session-close acknowledgement.

---

## Unknown Frame Types

The web app drops invalid or unknown inbound frames and reports five consecutive invalid frames through its error callback (`src/lib/ws.ts::WsConnection`, `droppedFrameThreshold`); it does not crash or disconnect.

The current server logs and ignores an unknown client frame type, then continues the connection; it does not promise an error response for every unknown type (`pkg/gateway/websocket.go::wsHandlerReadLoop.dispatchFrame`).

---

## Maintenance

This document is owned by the backend-lead. Any spec adding a new WebSocket frame type must:
1. Add the frame definition to this document before implementation.
2. Update the Frame Type Summary table.
3. Specify producer and consumer components.
4. Reference this document from the relevant spec.
