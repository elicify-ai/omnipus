# Bug 5: Replay Event Ordering on Reconnect

## Repro Steps

1. Start a session and send a message that triggers multiple sequential tool calls (e.g. a task
   that runs shell commands).
2. While the agent is actively executing (tool_call_start frames are still arriving), close the
   browser tab or navigate away.
3. Return to the session (or open the session list and click the session) — the SPA sends an
   `attach_session` frame.
4. Observe the replayed frame stream. Depending on timing you will see one or more of:
   - A `tool_call_result` appearing in the stream **after** the `done` frame that terminates the
     replay sequence.
   - A `token` frame appearing after `done`.
   - Frames from a live in-progress turn (tool_call_start, tool_call_result) appearing **before**
     earlier transcript entries that were buffered in the divert channel during replay.

## Root Cause

File: `pkg/gateway/websocket.go`, function `handleAttachSession`, lines 1085–1143.

### The Divert-Then-Drain Pattern

When `attach_session` arrives, `handleAttachSession` arms an atomic flag
(`wc.isReplayingLive`) that diverts all concurrent live frames (arriving via `sendConnGenFrame`
from the `eventForwarder` goroutine or `wsStreamer`) into a bounded side-channel
(`wc.replayDivertCh`) instead of `wc.sendCh`.

`streamReplay` then emits historical frames directly into `wc.sendCh` via the inline `emitFn`.
After `streamReplay` returns (having written the replay `done` frame into `wc.sendCh`), the code
is supposed to:

1. Disarm the divert.
2. Drain buffered live frames from `replayDivertCh` into `wc.sendCh` in arrival order.

### The Bug: Flag Cleared Before Drain

The code at line 1087 calls `wc.isReplayingLive.Store(false)` **before** the drain loop at
lines 1132–1143. This is the ordering violation.

```go
// line 1085-1143 (pkg/gateway/websocket.go)
wc.isReplayingLive.Store(false)         // ← flag cleared HERE

durationMS := time.Since(replayStart).Milliseconds()
// ... error handling ...

// FR-I-009: drain any live events buffered during replay, in arrival order.
for {                                    // ← drain happens AFTER flag cleared
    select {
    case raw := <-wc.replayDivertCh:
        select {
        case wc.sendCh <- raw:
        ...
    default:
        goto drainDone
    }
}
```

Between the `Store(false)` at line 1087 and the end of the drain loop, any concurrent goroutine
that calls `sendConnGenFrame` or `sendRawFrameBytes` now sees `isReplayingLive = false` and writes
its frame **directly to `wc.sendCh`**.

Because `wc.sendCh` is a FIFO buffered channel (capacity 256), those newly arrived live frames
occupy positions in the channel **before** the buffered divert frames that the drain loop
subsequently adds. The `writePump` goroutine dispatches them to the wire in FIFO order, so the
client receives:

```
[historical replay frames] [replay_done] [new live frame F_n] [buffered frame F_divert_1] [buffered frame F_divert_2]
```

`F_divert_1` and `F_divert_2` arrived at the server **before** `F_n` (they were captured in
`replayDivertCh` while replay was running), but the client sees them **after** `F_n` because the
drain races with concurrent writers.

### Concrete Scenarios That Trigger It

**Scenario A — tool_call_result before tool_call_start (most severe)**

The agent executes tool T1. `tool_call_start(T1)` is captured in `replayDivertCh` during replay.
`tool_call_result(T1)` arrives just after `Store(false)` clears the flag — it goes to `sendCh`
directly. The drain then moves `tool_call_start(T1)` into `sendCh` after `tool_call_result(T1)`.
The client sees result before start.

**Scenario B — token / done inversion**

`wsStreamer.Finalize` (called from the agent loop goroutine) emits the `done` frame via
`sendConnGenFrame`. If this fires in the window between `Store(false)` and the end of the drain,
the live `done` frame enters `sendCh` before buffered token frames that are subsequently drained
from `replayDivertCh`. The client sees `done` before the last few tokens.

**Scenario C — live done frame before replay done frame is received by the client**

The replay `done` frame is the last item written to `sendCh` by `emitFn` (inside `streamReplay`).
`writePump` drains `sendCh` asynchronously; at the moment the live `done` bypasses the divert,
the replay `done` may not yet have been dispatched to the wire. The client's UI tracks two `done`
frames for the same session and enters an inconsistent state.

### Why wsStreamer.Update Also Bypasses the Divert

`wsStreamer.Update` at line 1829 writes token frames directly to `s.conn.sendCh` without routing
through `sendRawFrameBytes`:

```go
select {
case s.conn.sendCh <- data:   // ← bypasses isReplayingLive check
    s.accumulated.WriteString(content)
default:
    ...
}
```

If the agent loop's streaming path fires token frames during the replay window, those frames land
in `sendCh` immediately, ahead of whatever historical frames are still being written there by
`emitFn`. In practice this path only fires when a turn is actively streaming AND the same `wsConn`
is being used — which requires the agent to have started a new turn on the reconnected connection
before replay finishes. It is an independent correctness hole from Bug 3 but can produce the same
symptom (tokens appear before historical content or out of order relative to replay frames).

## Proposed Fix

### Fix A — Drain Before Disarm (primary fix, addresses Bug 3)

Swap the ordering: drain `replayDivertCh` into `sendCh` **while `isReplayingLive` is still
`true`**, then disarm the flag. Because the flag is still set, no new frames can enter
`replayDivertCh` after we begin reading from it — the drain is guaranteed to be complete once
the channel's read returns `default` (empty). After the drain is complete, clearing the flag is
safe: future live frames will bypass the divert and go directly to `sendCh` in the correct
position (after all drained frames).

```go
// Correct order:
// 1. Drain the divert (flag still true — no new arrivals possible after drain starts).
// 2. Disarm the flag (future live frames go directly to sendCh, after all drained frames).

for {
    select {
    case raw := <-wc.replayDivertCh:
        select {
        case wc.sendCh <- raw:
        case <-ctx.Done():
            return
        }
    default:
        goto drainDone
    }
}
drainDone:
wc.isReplayingLive.Store(false)   // ← moved AFTER drain
```

This works because `sendRawFrameBytes` checks `isReplayingLive.Load()` before choosing
`replayDivertCh` vs `sendCh`. While `isReplayingLive` is `true`, all concurrent callers still
route to `replayDivertCh`. The drain loop reads from `replayDivertCh` non-blockingly; once
`default` fires, the channel is empty and — since the flag is still set — no new items can enter
it. Disarming after this point is then safe.

### Fix B — Route wsStreamer.Update through sendRawFrameBytes (secondary fix, addresses wsStreamer bypass)

Replace the direct `s.conn.sendCh <- data` write in `wsStreamer.Update` with a call to
`sendRawFrameBytes(s.conn, "token", data)` so token frames respect the replay-divert logic.

## Files Changed

- `pkg/gateway/websocket.go` — swap drain/disarm order; fix wsStreamer.Update routing
- `pkg/gateway/websocket_replay_order_test.go` — new regression test (see implementation)
