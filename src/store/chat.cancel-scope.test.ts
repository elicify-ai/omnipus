// ADR-20260928 D9 + MAJ-002 — cancelStream scope gate and failed-send surface
// (Stream A F1/F2 coverage-gap PINs, qa-lead 2026-10-02).
//
// Store-level pack: drives the REAL outbound-slice action (cancelStream) through
// the real chat/session/connection/ui stores. The only test double is the
// process edge — the WebSocket `connection.send` spy; cancelStream itself is
// never mocked or spied.
//
// Oracles (no expected value was derived from running the implementation):
//   - D9: a CONFIRMED tree stop sends even when this session's own turn
//     already ended locally (the tree's descendants may still be running);
//     a session-scoped cancel on a COMPLETED turn is a NO-OP — no frame
//     (a wasted round-trip, audit-log noise). Source: Stream A CHECK report
//     row 7 and the D9 comment on outbound-lifecycle.ts::cancelStream.
//   - MAJ-002: scope rides the generated CancelFrame verbatim. Omitted (every
//     pre-existing call path) goes out WITHOUT the key — the wire default is
//     session — and the frame stays byte-identical to the pre-stop-all shape;
//     only the confirmed tree stop passes 'tree'.
//   - Stream A CHECK row 8: a cancel whose wire send reports failure must
//     surface a VISIBLE error toast; a successful send emits no failure toast.
//     The exact toast copy below is pinned independently (characterization of
//     the approved shipped user-facing text) — it is deliberately NOT imported
//     from the implementation, so a copy change breaks this test loudly.
//
// Mutations this pack is built to kill (Stream A M5 survived the whole
// ChatScreen.stop-scope pack with the gate widened to `if (true)`):
//   M5  completed-turn no-op gate widened to `if (true)`  → first test fails
//   Mb  `|| scope === 'tree'` term dropped from the gate  → tree contrast fails
//   Mc  a scope key added to the default-scope frame      → exact-frame fails
//   Md  failed-send toast dropped or text/variant changed → toast test fails
//   Me  toast moved out of the `!sent` branch (always shown) → success test fails

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useUiStore } from './ui'
import type { CancelFrame } from '@/lib/api/generated/asyncapi-types'

// ── Store reset helper ─────────────────────────────────────────────────────────

function resetStores() {
  act(() => {
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      outboundQueue: [],
      pendingDrainQueue: [],
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
      reconnectPhase: 'reconnecting',
      reconnectAttempt: 1,
    })
    useSessionStore.setState({
      activeSessionId: 'test-session',
      activeAgentId: 'agent-jim',
      activeAgentType: null,
      attachedSessionType: null,
      attachedTaskTitle: null,
    })
    useUiStore.setState({ toasts: [] })
  })
}

/** Connect the store with a `send` spy whose return value the caller picks —
 * `true` models a delivered frame, `false` models a dead socket at the wire
 * edge. Returns the spy for wire assertions. */
function connectWithSend(sendResult: boolean) {
  const send = vi.fn().mockReturnValue(sendResult)
  act(() => {
    useConnectionStore.setState({
      connection: { send } as unknown as ReturnType<typeof useConnectionStore.getState>['connection'],
      isConnected: true,
      connectionError: null,
    })
  })
  return send
}

function sentCancelFrames(send: ReturnType<typeof vi.fn>): CancelFrame[] {
  return send.mock.calls
    .map((call) => call[0] as CancelFrame)
    .filter((frame) => frame && frame.type === 'cancel')
}

// ── Scope gate (F1) ────────────────────────────────────────────────────────────

describe('cancelStream scope gate (ADR-20260928 D9/MAJ-002)', () => {
  beforeEach(resetStores)

  it('PIN session-scoped cancel on a completed turn sends no cancel frame (D9 no-op gate)', () => {
    // Completed turn: isStreaming false, session established, connection up.
    // The ONLY reason no frame may go out is the D9 completed-turn no-op gate —
    // exactly the mutant (gate widened to `if (true)`) that survived the whole
    // ChatScreen.stop-scope pack as Stream A finding F1.
    const send = connectWithSend(true)
    expect(useChatStore.getState().isStreaming).toBe(false)

    act(() => {
      useChatStore.getState().cancelStream()
    })

    expect(send).not.toHaveBeenCalled()
    expect(sentCancelFrames(send)).toHaveLength(0)
  })

  it('PIN confirmed tree cancel still sends after the local turn already ended (D9 contrast)', () => {
    // Same completed-turn state as above — the tree scope is what makes the
    // frame go out (descendants may still run). Kills the inverse mutant that
    // drops `|| scope === 'tree'` from the gate.
    const send = connectWithSend(true)
    expect(useChatStore.getState().isStreaming).toBe(false)

    act(() => {
      useChatStore.getState().cancelStream(undefined, 'tree')
    })

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(1)
    expect(frames[0]).toEqual({ type: 'cancel', session_id: 'test-session', scope: 'tree' })
  })

  it('PIN streaming session-scoped cancel sends exactly the pre-stop-all frame shape — no scope key (MAJ-002)', () => {
    // MAJ-002: the omitted scope goes out WITHOUT the key — the exact-shape
    // equality fails if any default scope is filled in ('session' included).
    const send = connectWithSend(true)
    act(() => {
      useChatStore.setState({ isStreaming: true })
    })

    act(() => {
      useChatStore.getState().cancelStream()
    })

    const frames = sentCancelFrames(send)
    expect(frames).toHaveLength(1)
    expect(frames[0]).toEqual({ type: 'cancel', session_id: 'test-session' })
  })
})

// ── Failed-send surface (F2) ───────────────────────────────────────────────────

describe('cancelStream failed-send toast (Stream A CHECK row 8)', () => {
  beforeEach(resetStores)

  it('PIN wire send reporting false on a streaming cancel surfaces the exact error toast', () => {
    // Streaming session-scope path — the send is genuinely attempted (the
    // no-op gate must not short-circuit this test) and the wire reports
    // failure. The user must SEE the failure: exactly one toast, the shipped
    // copy, error variant — asserted against the REAL ui store state.
    const send = connectWithSend(false)
    act(() => {
      useChatStore.setState({ isStreaming: true })
    })

    act(() => {
      useChatStore.getState().cancelStream()
    })

    expect(send).toHaveBeenCalledTimes(1)
    const toasts = useUiStore.getState().toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0].message).toBe(
      'Could not send cancel — connection dropped. The response may continue briefly.'
    )
    expect(toasts[0].variant).toBe('error')
  })

  it('PIN successful cancel send emits no failure toast', () => {
    const send = connectWithSend(true)
    act(() => {
      useChatStore.setState({ isStreaming: true })
    })

    act(() => {
      useChatStore.getState().cancelStream()
    })

    expect(send).toHaveBeenCalledTimes(1)
    expect(useUiStore.getState().toasts).toHaveLength(0)
  })
})
