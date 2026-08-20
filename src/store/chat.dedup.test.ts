/**
 * chat.dedup.test.ts — T1.7 + T1.8 regression tests for tool-call-id
 * deduplication and replay_message tail-dedup.
 *
 * T1.7: sendMessage_merges_duplicate_tool_call_ids
 *   A previous assistant already has tool_calls: [{id:'tc1'}] baked in AND
 *   tc1 is still live in toolCallOrder. Calling sendMessage must not produce
 *   duplicate ids on prevAssistant.tool_calls.
 *
 * T1.8: replay_message_tail_dedupe_drops_identical_re_emit
 *   Two replay_message frames with identical (role, content) — the second
 *   must be silently dropped, leaving messages.length unchanged.
 *
 * Regression class: commit 45a233d "fix(chat): dedupe tool-call ids to prevent
 * React duplicate-key crash".
 */

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages, makeBucketMessages } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'

const SID = 'dedup-test-session'

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
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
    })
  })
}

beforeEach(resetStores)

// ---------------------------------------------------------------------------
// T1.7: sendMessage_merges_duplicate_tool_call_ids
// ---------------------------------------------------------------------------

describe('chat.dedup — T1.7: sendMessage merges duplicate tool_call ids', () => {
  it('prevAssistant.tool_calls has no duplicate ids after sendMessage when tc1 was already baked AND still live', () => {
    // Arrange: a mock connection
    const send = vi.fn().mockReturnValue(true)
    useConnectionStore.setState({
      connection: { send } as unknown as ReturnType<typeof useConnectionStore.getState>['connection'],
      isConnected: true,
      connectionError: null,
      setConnectionError: useConnectionStore.getState().setConnectionError,
      setConnection: useConnectionStore.getState().setConnection,
      setConnected: useConnectionStore.getState().setConnected,
    })

    // Manually construct a bucket where the previous assistant message already
    // has tool_calls: [{id:'tc1'}] baked in (from a previous turn), BUT tc1 is
    // also still present in the live toolCallOrder. This is the state that
    // triggered the duplicate-key crash: an attach+replay re-emits the
    // tool_call_start for tc1 (adding it to the live map) even though it was
    // already baked into the message during the original sendMessage call.
    act(() => {
      useChatStore.setState(() => {
        const seedMsgs = [
          {
            id: 'user-1',
            role: 'user' as const,
            content: 'first message',
            timestamp: '2026-01-01T00:00:00Z',
            status: 'done' as const,
          },
          {
            id: 'asst-1',
            role: 'assistant' as const,
            content: 'I ran some things.',
            timestamp: '2026-01-01T00:00:01Z',
            status: 'done' as const,
            isStreaming: false,
            // tc1 already baked via a prior sendMessage call
            tool_calls: [{ id: 'tc1', tool: 'write_file', params: { path: '/x' }, status: 'success' as const }],
          },
        ]
        const bucket = {
          ...makeBucketMessages(seedMsgs),
          toolCalls: {
            // tc1 also still present in the live map (simulating replay re-inject)
            tc1: { id: 'tc1', call_id: 'tc1', tool: 'write_file', params: { path: '/x' }, status: 'success' as const },
          },
          toolCallOrder: ['tc1'],
          textAtToolCallStart: { tc1: '' },
          isStreaming: false,
          isReplaying: false,
          replayCompletedForSession: null,
          sessionTokens: 0,
          sessionCost: 0,
          rateLimitEvent: null,
          cancelStage: null,
          lastUserMessageAt: null,
          lastReceivedEventTime: null,
          spanByParentCallId: {},
        }
        return {
          sessionsById: { [SID]: bucket },
          // sync foreground selectors
          messages: getMessages(bucket),
          toolCalls: bucket.toolCalls,
          toolCallOrder: bucket.toolCallOrder,
          textAtToolCallStart: bucket.textAtToolCallStart,
          isStreaming: false,
          isReplaying: false,
        }
      })
    })

    // Act: send a second message (turn 2), which triggers the baking logic
    act(() => {
      useChatStore.getState().sendMessage('second user message')
    })

    const state = useChatStore.getState()
    const assistants = state.messages.filter((m) => m.role === 'assistant')
    expect(assistants).toHaveLength(2)

    const prevAssistant = assistants[0]
    const toolCallIds = prevAssistant.tool_calls?.map((tc) => tc.id) ?? []

    // Core assertion: no duplicate ids
    const uniqueIds = new Set(toolCallIds)
    expect(
      toolCallIds,
      `prevAssistant.tool_calls contains duplicate ids: [${toolCallIds.join(', ')}]`,
    ).toHaveLength(uniqueIds.size)

    // And tc1 appears exactly once
    expect(
      toolCallIds.filter((id) => id === 'tc1'),
    ).toHaveLength(1)
  })
})

// ---------------------------------------------------------------------------
// T1.8: replay_message_tail_dedupe_drops_identical_re_emit
// ---------------------------------------------------------------------------

describe('chat.dedup — T1.8: replay_message tail dedup drops identical re-emit', () => {
  it('second replay_message with same role+content leaves messages.length unchanged', () => {
    // Arrange: empty bucket at SID (no live WS needed for replay frames)
    // The FALLBACK_SID in test mode routes frames without session_id to '__default',
    // but we use explicit session_id here for determinism.
    act(() => {
      // The session bucket must exist before handling replay frames
      useChatStore.setState(() => ({
        sessionsById: {
          [SID]: {
            ...makeBucketMessages([]),
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            isStreaming: false,
            isReplaying: true,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            cancelStage: null,
            lastUserMessageAt: null,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          },
        },
        messages: [],
        toolCalls: {},
        toolCallOrder: [],
        isStreaming: false,
        isReplaying: true,
      }))
    })

    const replayFrame = {
      type: 'replay_message' as const,
      session_id: SID,
      role: 'assistant' as const,
      content: 'I completed the task successfully.',
      timestamp: '2026-01-01T00:00:00Z',
    }

    // First replay_message push — must be accepted
    act(() => {
      useChatStore.getState().handleFrame(replayFrame)
    })

    const afterFirstBucket = useChatStore.getState().sessionsById[SID]
    const afterFirst = afterFirstBucket ? getMessages(afterFirstBucket) : []
    expect(afterFirst).toHaveLength(1)

    // Second push: identical role + content — must be dropped (tail dedup)
    act(() => {
      useChatStore.getState().handleFrame(replayFrame)
    })

    const afterSecondBucket = useChatStore.getState().sessionsById[SID]
    const afterSecond = afterSecondBucket ? getMessages(afterSecondBucket) : []
    expect(
      afterSecond,
      'Second identical replay_message must not create a duplicate bubble',
    ).toHaveLength(1)

    // Confirm the message content is still the expected text (not corrupted)
    expect(afterSecond[0].content).toBe('I completed the task successfully.')
  })

  it('second replay_message with DIFFERENT content IS accepted (only exact-match is deduped)', () => {
    act(() => {
      useChatStore.setState(() => ({
        sessionsById: {
          [SID]: {
            ...makeBucketMessages([]),
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            isStreaming: false,
            isReplaying: true,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            cancelStage: null,
            lastUserMessageAt: null,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          },
        },
        messages: [],
        toolCalls: {},
        toolCallOrder: [],
        isStreaming: false,
        isReplaying: true,
      }))
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message' as const,
        session_id: SID,
        role: 'assistant' as const,
        content: 'First message.',
        timestamp: '2026-01-01T00:00:00Z',
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message' as const,
        session_id: SID,
        role: 'assistant' as const,
        content: 'Second, different message.',
        timestamp: '2026-01-01T00:00:01Z',
      })
    })

    const finalBucket = useChatStore.getState().sessionsById[SID]
    const messages = finalBucket ? getMessages(finalBucket) : []
    expect(
      messages,
      'Two replay_messages with different content must both appear',
    ).toHaveLength(2)
  })
})
