/**
 * chat.error-during-replay.test.ts — round-3 regression.
 *
 * Bug: the `error`-frame reducer cleared isStreaming but NEVER cleared
 * isReplaying. An error frame arriving WHILE a session was replaying left the
 * session permanently wedged behind the "Loading session history…" overlay with
 * a disabled composer — and, unlike the `done` path, nothing else ever cleared
 * it (no timer, no further frame).
 *
 * Fix: the error reducer now mirrors the done reducer's replay-clear logic —
 * clear isReplaying immediately once MIN_REPLAY_DISPLAY_MS (750ms) has elapsed,
 * otherwise defer to a timer. The composer (ChatScreen.tsx:1414) is disabled
 * while foreground isReplaying is true, so clearing it re-enables the composer.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'

const SID = 'error-during-replay-session'

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
      lastReceivedEventTime: null,
    })
    useSessionStore.setState({
      activeSessionId: SID,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)
afterEach(() => {
  vi.useRealTimers()
})

describe('chat store — error frame during replay (round-3 wedge regression)', () => {
  it('clears BOTH isStreaming and isReplaying when the replay window has elapsed', () => {
    vi.useFakeTimers()

    // Begin a replay: isReplaying=true, replayingStartedAt=now.
    act(() => {
      useChatStore.getState().resetSessionForReplay(SID)
    })
    // A streaming assistant bubble is in flight (replay rebuilding history).
    act(() => {
      useChatStore.setState((s) => ({
        sessionsById: {
          ...s.sessionsById,
          [SID]: {
            ...s.sessionsById[SID],
            isStreaming: true,
            messagesById: {
              a1: {
                id: 'a1',
                role: 'assistant',
                content: 'thinking…',
                timestamp: new Date().toISOString(),
                status: 'streaming',
                isStreaming: true,
              },
            },
            messageOrder: ['a1'],
          },
        },
      }))
    })

    expect(useChatStore.getState().sessionsById[SID].isReplaying).toBe(true)

    // Let the minimum replay-display window (750ms) fully elapse so the error
    // reducer clears isReplaying immediately rather than deferring.
    vi.advanceTimersByTime(800)

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: 'gateway exploded mid-replay',
      })
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    // BOTH flags clear — the composer (gated on isStreaming || isReplaying) re-enables.
    expect(bucket.isStreaming).toBe(false)
    expect(bucket.isReplaying).toBe(false)
    // Foreground projection (what ChatScreen reads) is synced too.
    expect(useChatStore.getState().isReplaying).toBe(false)
    expect(useChatStore.getState().isStreaming).toBe(false)
    // The in-flight assistant bubble is closed and flagged error.
    expect(bucket.messagesById['a1'].status).toBe('error')
    expect(bucket.messagesById['a1'].isStreaming).toBe(false)
  })

  it('defers the isReplaying clear when the error arrives before the min window, then clears it', () => {
    vi.useFakeTimers()

    act(() => {
      useChatStore.getState().resetSessionForReplay(SID)
    })
    act(() => {
      useChatStore.setState((s) => ({
        sessionsById: {
          ...s.sessionsById,
          [SID]: {
            ...s.sessionsById[SID],
            isStreaming: true,
            messagesById: {
              a1: {
                id: 'a1',
                role: 'assistant',
                content: 'thinking…',
                timestamp: new Date().toISOString(),
                status: 'streaming',
                isStreaming: true,
              },
            },
            messageOrder: ['a1'],
          },
        },
      }))
    })

    // Error arrives EARLY (100ms < 750ms).
    vi.advanceTimersByTime(100)
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: 'gateway exploded early',
      })
    })

    // isStreaming clears immediately; isReplaying is deferred (still true).
    let bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.isStreaming).toBe(false)
    expect(bucket.isReplaying).toBe(true)

    // After the remaining window elapses, the deferred timer clears isReplaying.
    act(() => {
      vi.advanceTimersByTime(700)
    })
    bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.isReplaying).toBe(false)
  })

  it('no-message error during replay still clears isReplaying (pushes an error bubble)', () => {
    vi.useFakeTimers()

    act(() => {
      useChatStore.getState().resetSessionForReplay(SID)
    })
    // No assistant message in the bucket — the reducer takes the push-a-message path.
    expect(useChatStore.getState().sessionsById[SID].isReplaying).toBe(true)

    vi.advanceTimersByTime(800)
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        session_id: SID,
        message: 'no assistant bubble yet',
      })
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.isReplaying).toBe(false)
    expect(bucket.isStreaming).toBe(false)
    // An error bubble was pushed.
    const hasErrorBubble = bucket.messageOrder
      .map((id) => bucket.messagesById[id])
      .some((m) => m?.status === 'error')
    expect(hasErrorBubble).toBe(true)
  })
})
