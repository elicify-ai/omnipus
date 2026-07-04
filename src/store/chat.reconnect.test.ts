/**
 * chat.reconnect.test.ts — T1.9 regression test for WS reconnect replay.
 *
 * T1.9: reconnect_replay_does_not_double_bucket
 *   Seed a bucket with N messages from a first replay. Then call
 *   resetSessionForReplay(sid) and replay the same N messages. Assert that
 *   the bucket has exactly N messages (not 2N). This verifies that
 *   resetSessionForReplay wipes the bucket before the second replay, so
 *   the reconnect path does not double-render every existing bubble.
 *
 * Regression class: WS reconnect duplicate bubbles (the gateway re-sends the
 * full transcript replay on every reconnect; without resetSessionForReplay
 * the store appends to the existing bucket instead of rebuilding from scratch).
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages } from './chat'
import { useSessionStore } from './session'

const SID = 'reconnect-test-session'
const N = 5 // number of messages in each replay pass

function makeReplayMessage(index: number) {
  return {
    type: 'replay_message' as const,
    session_id: SID,
    role: index % 2 === 0 ? ('user' as const) : ('assistant' as const),
    content: `Message number ${index}`,
    timestamp: new Date(Date.now() - (N - index) * 1000).toISOString(),
  }
}

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
      activeSessionId: SID,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

describe('chat.reconnect — T1.9: reconnect_replay_does_not_double_bucket', () => {
  it(`after resetSessionForReplay, replaying the same ${N} messages yields exactly ${N} messages (not ${N * 2})`, () => {
    // ── Phase 1: First replay (simulating initial WS connection) ─────────────
    // Manually create the bucket as if the gateway replayed N messages.
    act(() => {
      useChatStore.getState().resetSessionForReplay(SID)
    })

    // Feed N replay_message frames
    for (let i = 0; i < N; i++) {
      act(() => {
        useChatStore.getState().handleFrame(makeReplayMessage(i))
      })
    }

    // Simulate done frame arriving (ends replay)
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: SID,
        stats: { tokens: 10, cost: 0.001, duration_ms: 50 },
      })
    })

    const afterFirstReplayBucket = useChatStore.getState().sessionsById[SID]
    expect(
      afterFirstReplayBucket ? getMessages(afterFirstReplayBucket) : [],
      'After first replay, bucket should have exactly N messages',
    ).toHaveLength(N)

    // ── Phase 2: WS reconnects — call resetSessionForReplay then replay again ─
    act(() => {
      useChatStore.getState().resetSessionForReplay(SID)
    })

    // After reset, bucket should be empty (wiped for fresh replay)
    const afterResetBucket = useChatStore.getState().sessionsById[SID]
    expect(
      afterResetBucket ? getMessages(afterResetBucket) : [],
      'After resetSessionForReplay, bucket must be wiped to empty',
    ).toHaveLength(0)
    expect(
      afterResetBucket?.isReplaying,
      'After resetSessionForReplay, isReplaying must be true',
    ).toBe(true)

    // Replay the same N messages a second time
    for (let i = 0; i < N; i++) {
      act(() => {
        useChatStore.getState().handleFrame(makeReplayMessage(i))
      })
    }

    // Simulate done frame
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        session_id: SID,
        stats: { tokens: 10, cost: 0.001, duration_ms: 50 },
      })
    })

    const afterSecondReplayBucket = useChatStore.getState().sessionsById[SID]
    const afterSecondReplayMsgs = afterSecondReplayBucket ? getMessages(afterSecondReplayBucket) : []

    // Core assertion: exactly N messages, not 2N
    expect(
      afterSecondReplayMsgs,
      `After reconnect replay, bucket must have exactly ${N} messages, not ${N * 2}. ` +
        `Got: ${afterSecondReplayMsgs.length}. ` +
        'This means resetSessionForReplay did NOT wipe the bucket before the second replay.',
    ).toHaveLength(N)

    // Verify message content is correct (not corrupted by merging)
    const messages = afterSecondReplayMsgs
    for (let i = 0; i < N; i++) {
      expect(messages[i].content).toBe(`Message number ${i}`)
    }
  })

  it('resetSessionForReplay on a non-existent session still creates a clean bucket', () => {
    const freshSid = 'fresh-reconnect-test-sid'
    act(() => {
      useChatStore.getState().resetSessionForReplay(freshSid)
    })

    const bucket = useChatStore.getState().sessionsById[freshSid]
    expect(bucket, 'resetSessionForReplay must create the bucket even if it did not exist').toBeDefined()
    expect(bucket ? getMessages(bucket) : []).toHaveLength(0)
    expect(bucket?.isReplaying).toBe(true)
  })
})
