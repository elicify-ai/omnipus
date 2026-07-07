/**
 * chat.outbound-queue.test.ts — Fix 3: outbound message queue
 *
 * Traces to: fix(spa): resilient WS reconnect + visible state + outbound queue
 *
 * Tests:
 *   1. enqueueOutboundMessage adds to the queue.
 *   2. enqueueOutboundMessage rejects when queue has >= 5 messages.
 *   3. drainOutboundQueue sends queued messages ONE AT A TIME as each turn
 *      completes, and clears the queue up front (BUG 1 fix — see below).
 *   4. sendMessage enqueues when WS is disconnected instead of showing a hard error.
 *
 * BUG 1 (fixed 2026-07): the previous drainOutboundQueue implementation
 * looped `sendMessage` synchronously over the whole queue. The first call
 * flips `isStreaming` synchronously (via zustand's `set()`); every
 * subsequent call in that same loop then hit sendMessage's `isStreaming`
 * guard, which shows a "please wait" toast and returns WITHOUT
 * re-enqueuing (unlike the disconnected-WS branch) — so messages 2-5 were
 * silently dropped with no chat bubble and no error. The fix moves the
 * drained batch into `pendingDrainQueue` and sends one at a time, driven by
 * `maybeDrainNext()` calls at every place a turn ends (done/error frames,
 * cancelStream, clearStreamingState, markLastMessageInterrupted, and
 * sendMessage's own failed-send rollback). The tests below assert actual
 * delivery via a `connection.send` spy — not just that outboundQueue ends
 * up empty (which was true even under the old buggy code, since the buggy
 * loop cleared outboundQueue up front regardless of whether every message
 * was actually delivered).
 */

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'

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
      // BUG 1 fix: must be reset per-test too — it's module-level store state,
      // not scoped to sessionsById, so a leftover value from a previous test
      // would otherwise leak in and corrupt drain-order assertions.
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
  })
}

/** Connect the store with a `send` spy that always succeeds, so drained
 * messages actually attempt delivery instead of hitting the disconnected-WS
 * re-enqueue branch. Returns the spy for assertions. */
function connectWithSendSpy() {
  const send = vi.fn().mockReturnValue(true)
  act(() => {
    useConnectionStore.setState({
      connection: { send } as unknown as ReturnType<typeof useConnectionStore.getState>['connection'],
      isConnected: true,
      connectionError: null,
    })
  })
  return send
}

// ── enqueueOutboundMessage ─────────────────────────────────────────────────────

describe('outboundQueue — enqueueOutboundMessage', () => {
  beforeEach(resetStores)

  it('adds a message to the queue and returns true', () => {
    let ok = false
    act(() => {
      ok = useChatStore.getState().enqueueOutboundMessage('hello')
    })
    expect(ok).toBe(true)
    expect(useChatStore.getState().outboundQueue).toEqual(['hello'])
  })

  it('accumulates up to 5 messages in order', () => {
    act(() => {
      for (let i = 1; i <= 5; i++) {
        useChatStore.getState().enqueueOutboundMessage(`msg ${i}`)
      }
    })
    expect(useChatStore.getState().outboundQueue).toHaveLength(5)
    expect(useChatStore.getState().outboundQueue[0]).toBe('msg 1')
    expect(useChatStore.getState().outboundQueue[4]).toBe('msg 5')
  })

  it('rejects a 6th message when queue is full — returns false', () => {
    act(() => {
      for (let i = 0; i < 5; i++) {
        useChatStore.getState().enqueueOutboundMessage(`fill ${i}`)
      }
    })
    let ok = true
    act(() => {
      ok = useChatStore.getState().enqueueOutboundMessage('overflow')
    })
    expect(ok).toBe(false)
    // Queue must still have exactly 5 — overflow message was not added.
    expect(useChatStore.getState().outboundQueue).toHaveLength(5)
    expect(useChatStore.getState().outboundQueue).not.toContain('overflow')
  })

  it('rejects when queue already has exactly 5 entries (boundary condition)', () => {
    act(() => {
      useChatStore.setState({ outboundQueue: ['a', 'b', 'c', 'd', 'e'] })
    })
    let result = false
    act(() => {
      result = useChatStore.getState().enqueueOutboundMessage('extra')
    })
    expect(result).toBe(false)
    expect(useChatStore.getState().outboundQueue).toHaveLength(5)
  })
})

// ── drainOutboundQueue ────────────────────────────────────────────────────────

describe('outboundQueue — drainOutboundQueue', () => {
  beforeEach(resetStores)

  it('clears outboundQueue immediately, but only actually SENDS the first message — the rest wait for the turn to complete', () => {
    const send = connectWithSendSpy()
    act(() => {
      useChatStore.setState({ outboundQueue: ['msg1', 'msg2'] })
    })
    act(() => {
      useChatStore.getState().drainOutboundQueue()
    })
    // outboundQueue is cleared up front (unchanged from before the fix)...
    expect(useChatStore.getState().outboundQueue).toHaveLength(0)
    // ...but BUG 1: msg2 must NOT be dropped. It should be waiting in
    // pendingDrainQueue, and only msg1 was actually handed to the WS so far —
    // sendMessage's isStreaming guard would otherwise have silently discarded
    // msg2 had both been sent synchronously in the same tick.
    expect(send).toHaveBeenCalledTimes(1)
    expect(send).toHaveBeenCalledWith(expect.objectContaining({ content: 'msg1' }))
    expect(useChatStore.getState().pendingDrainQueue).toEqual(['msg2'])
    expect(useChatStore.getState().isStreaming).toBe(true)

    // Completing msg1's turn (the server's `done` frame) must release msg2 —
    // this is the actual regression: previously msg2 was gone for good, with
    // no chat bubble and no error toast.
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: 'test-session' })
    })
    expect(send).toHaveBeenCalledTimes(2)
    expect(send).toHaveBeenCalledWith(expect.objectContaining({ content: 'msg2' }))
    expect(useChatStore.getState().pendingDrainQueue).toEqual([])
  })

  it('drains 4 queued messages one at a time as each turn completes (BUG 1 regression — full delivery)', () => {
    const send = connectWithSendSpy()
    const queued = ['q-msg-1', 'q-msg-2', 'q-msg-3', 'q-msg-4']
    act(() => {
      useChatStore.setState({ outboundQueue: [...queued] })
    })

    act(() => {
      useChatStore.getState().drainOutboundQueue()
    })
    expect(useChatStore.getState().outboundQueue).toEqual([])

    // Drive each turn to completion in order and confirm every single
    // message actually reaches connection.send exactly once, in the
    // original order — not just that outboundQueue ends up empty (which the
    // old buggy implementation also satisfied, despite dropping messages).
    for (let i = 0; i < queued.length; i++) {
      // Message i has just been sent — i+1 total sends so far.
      expect(send).toHaveBeenCalledTimes(i + 1)
      expect(send.mock.calls[i][0]).toEqual(expect.objectContaining({ content: queued[i] }))
      // Everything after message i is still waiting its turn.
      expect(useChatStore.getState().pendingDrainQueue).toEqual(queued.slice(i + 1))
      if (i < queued.length - 1) {
        // Complete message i's turn — this must release message i+1.
        act(() => {
          useChatStore.getState().handleFrame({ type: 'done', session_id: 'test-session' })
        })
      }
    }

    // All 4 messages were delivered; nothing left queued anywhere.
    expect(send).toHaveBeenCalledTimes(queued.length)
    expect(useChatStore.getState().outboundQueue).toEqual([])
    expect(useChatStore.getState().pendingDrainQueue).toEqual([])
  })

  it('is a no-op on an empty queue', () => {
    const initialQueue: string[] = []
    act(() => {
      useChatStore.setState({ outboundQueue: initialQueue })
    })
    // drainOutboundQueue should complete without throwing.
    act(() => {
      useChatStore.getState().drainOutboundQueue()
    })
    expect(useChatStore.getState().outboundQueue).toHaveLength(0)
    expect(useChatStore.getState().pendingDrainQueue).toHaveLength(0)
  })

  it('queue is empty after drain even when sendMessage re-enqueues (connection still down)', () => {
    // If sendMessage hits isConnected=false and re-enqueues, the drain function
    // cleared the queue first — so the net effect is the queue is replaced with
    // whatever sendMessage re-queued (not doubled). This verifies no infinite loop.
    act(() => {
      useChatStore.setState({
        outboundQueue: ['first', 'second'],
        isStreaming: false,
      })
      useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting' })
    })
    act(() => {
      useChatStore.getState().drainOutboundQueue()
    })
    // After draining, the head message was re-enqueued (sendMessage sees
    // isConnected=false). The critical checks: no infinite loop, no message
    // is lost — 'first' is back in outboundQueue and 'second' is still
    // waiting in pendingDrainQueue (neither array grows unboundedly).
    expect(useChatStore.getState().outboundQueue.length).toBeLessThanOrEqual(2)
    expect(
      useChatStore.getState().outboundQueue.length + useChatStore.getState().pendingDrainQueue.length
    ).toBe(2)
  })
})

// ── sendMessage queues when disconnected ─────────────────────────────────────

describe('sendMessage — queues when disconnected (Fix 3)', () => {
  beforeEach(resetStores)

  it('enqueues the message instead of hard-erroring when reconnecting', () => {
    // Preconditions: isConnected=false, reconnectPhase='reconnecting' (set in resetStores).
    act(() => {
      useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting' })
    })
    act(() => {
      useChatStore.getState().sendMessage('queued message')
    })
    expect(useChatStore.getState().outboundQueue).toContain('queued message')
    // The old hard-error message must NOT appear.
    const err = useConnectionStore.getState().connectionError
    expect(err).not.toBe(
      'Cannot send message — not connected to the server. Check your connection and try again.'
    )
  })

  it('enqueues the message when slow retry phase', () => {
    act(() => {
      useConnectionStore.setState({ isConnected: false, reconnectPhase: 'slow' })
    })
    act(() => {
      useChatStore.getState().sendMessage('slow-phase message')
    })
    expect(useChatStore.getState().outboundQueue).toContain('slow-phase message')
  })

  it('sets connectionError when queue is full and another message is attempted', () => {
    act(() => {
      useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting' })
      useChatStore.setState({ outboundQueue: ['a', 'b', 'c', 'd', 'e'] })
    })
    act(() => {
      useChatStore.getState().sendMessage('one too many')
    })
    const err = useConnectionStore.getState().connectionError
    expect(err).not.toBeNull()
    expect(err).toContain('Queue full')
  })
})
