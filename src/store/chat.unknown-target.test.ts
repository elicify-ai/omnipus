/**
 * chat.unknown-target.test.ts — T1.14 regression test for unknown-target-sid
 * done frame force-clearing isStreaming.
 *
 * T1.14: done_for_unknown_targetSid_force_clears_isStreaming
 *   Inject a done frame for a session_id that is NOT in sessionsById.
 *   Assert that the active bucket's isStreaming is false (the stale spinner
 *   must be cleared).
 *
 * The guard: the active bucket must NOT be cleared when it recently sent a
 * user message (lastUserMessageAt < 10 s ago) — that indicates a legitimately
 * in-flight stream. This test covers the "stale spinner" case (no recent
 * user message), which is the regression that caused infinite spinners after
 * session switches or replay.
 *
 * Regression class: commit 8041478 "fix(chat): three issues from Mia/Jim
 * screenshot conversation" — the done_unknown_sid logic was introduced to
 * handle this case.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, makeBucketMessages } from './chat'
import { useSessionStore } from './session'

const ACTIVE_SID = 'unknown-target-active-session'
const UNKNOWN_SID = 'unknown-target-unknown-session-xyz'

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
    })
    useSessionStore.setState({
      activeSessionId: ACTIVE_SID,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

describe('chat.unknown-target — T1.14: done for unknown targetSid force-clears isStreaming', () => {
  it('a stale spinning active bucket is force-cleared when a done arrives for an unknown sid', () => {
    // Arrange: plant a spinning (isStreaming=true) active bucket
    // with NO recent lastUserMessageAt (null → stale spinner).
    const spinnerMsg = {
      id: 'asst-spinner',
      role: 'assistant' as const,
      content: '',
      timestamp: new Date().toISOString(),
      status: 'streaming' as const,
      isStreaming: true,
    }
    act(() => {
      useChatStore.setState(() => ({
        sessionsById: {
          [ACTIVE_SID]: {
            ...makeBucketMessages([spinnerMsg]),
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            isStreaming: true,
            isReplaying: false,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            cancelStage: null,
            // null = no user message sent recently → stale spinner
            lastUserMessageAt: null,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          },
        },
        messages: [spinnerMsg],
        isStreaming: true,
        toolCalls: {},
        toolCallOrder: [],
      }))
    })

    // Sanity: active bucket is currently streaming
    expect(useChatStore.getState().sessionsById[ACTIVE_SID]?.isStreaming).toBe(true)

    // Act: inject a done frame for UNKNOWN_SID (not in sessionsById)
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: UNKNOWN_SID,
        stats: { tokens: 5, cost: 0.0001, duration_ms: 10 },
      })
    })

    // Assert: the active bucket's isStreaming is now false
    const activeBucket = useChatStore.getState().sessionsById[ACTIVE_SID]
    expect(
      activeBucket?.isStreaming,
      'Active bucket isStreaming must be false after unknown-sid done cleared the stale spinner',
    ).toBe(false)
  })

  it('a legitimately mid-stream active bucket is NOT cleared by an unknown-sid done', () => {
    // Arrange: active bucket is streaming AND lastUserMessageAt is recent (< 10 s ago)
    const recentUserMessageAt = Date.now() - 1000 // 1 second ago

    const liveMsg = {
      id: 'asst-live',
      role: 'assistant' as const,
      content: 'partial response...',
      timestamp: new Date().toISOString(),
      status: 'streaming' as const,
      isStreaming: true,
    }
    act(() => {
      useChatStore.setState(() => ({
        sessionsById: {
          [ACTIVE_SID]: {
            ...makeBucketMessages([liveMsg]),
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            isStreaming: true,
            isReplaying: false,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            cancelStage: null,
            // RECENT user message → guard must preserve this spinner
            lastUserMessageAt: recentUserMessageAt,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          },
        },
        messages: [],
        isStreaming: true,
        toolCalls: {},
        toolCallOrder: [],
      }))
    })

    // Sanity check
    expect(useChatStore.getState().sessionsById[ACTIVE_SID]?.isStreaming).toBe(true)

    // Act: unknown-sid done arrives while active session is legitimately streaming
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: UNKNOWN_SID,
        stats: { tokens: 5, cost: 0.0001, duration_ms: 10 },
      })
    })

    // Assert: active bucket's isStreaming is preserved (not cleared)
    const activeBucket = useChatStore.getState().sessionsById[ACTIVE_SID]
    expect(
      activeBucket?.isStreaming,
      'Active bucket isStreaming must stay true — the unknown-sid done must not clear a live stream',
    ).toBe(true)
  })
})
