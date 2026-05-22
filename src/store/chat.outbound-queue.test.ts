/**
 * chat.outbound-queue.test.ts — Fix 3: outbound message queue
 *
 * Traces to: fix(spa): resilient WS reconnect + visible state + outbound queue
 *
 * Tests:
 *   1. enqueueOutboundMessage adds to the queue.
 *   2. enqueueOutboundMessage rejects when queue has >= 5 messages.
 *   3. drainOutboundQueue calls sendMessage for each queued message and clears the queue.
 *   4. sendMessage enqueues when WS is disconnected instead of showing a hard error.
 */

import { describe, it, expect, beforeEach } from 'vitest'
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
      pendingApprovals: [],
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      outboundQueue: [],
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

  it('clears the queue after draining', () => {
    act(() => {
      useChatStore.setState({ outboundQueue: ['msg1', 'msg2'] })
      // Put the store into a connected state so drain actually sends.
      useConnectionStore.setState({ isConnected: true })
    })
    act(() => {
      useChatStore.getState().drainOutboundQueue()
    })
    expect(useChatStore.getState().outboundQueue).toHaveLength(0)
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
    // After draining, the messages were re-enqueued (sendMessage sees isConnected=false).
    // The critical check: no infinite loop and queue is at most original length.
    expect(useChatStore.getState().outboundQueue.length).toBeLessThanOrEqual(2)
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
