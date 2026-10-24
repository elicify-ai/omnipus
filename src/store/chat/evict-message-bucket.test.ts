// Exact three-case relocation from the oversized chat store suite; behavioral
// setup/assertions unchanged. Seam K adds only the canonical fresh history field.
import { beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { evictMessageFromBucket, useChatStore, type SessionChatState } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { emptyHistoryFixture } from '../../../tests/chat-history-paging/empty-history'

const TEST_SESSION_ID = 'test-session-1'

function resetStore() {
  act(() => {
    // F-S3: clearStreamingState() also drains the module-scoped
    // pendingCancelAckSids tracker (see chat.ts) — call it before rebuilding
    // state so a cancelStream(sessionId) from a PRIOR test can never bleed
    // into this test's missing-session_id frame-routing fallback.
    useChatStore.getState().clearStreamingState()
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
      lastReceivedEventTime: null,
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: TEST_SESSION_ID,
      activeAgentId: null,
      activeAgentType: null,
    })
    // M4: default to "not in a workspace" so the global-chat assertions hold
    // unless a test explicitly sets an active workspace.
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
}

beforeEach(resetStore)

// ── evictMessageFromBucket unit tests ─────────────────────────────────────────

describe('evictMessageFromBucket — full sweep of dependent maps', () => {
  it('removes the message from messagesById and messageOrder', () => {
    const bucket: SessionChatState = {
      ...emptyHistoryFixture(),
      messagesById: { 'm1': { id: 'm1', role: 'user', content: 'hi', timestamp: '', status: 'done' } },
      messageOrder: ['m1'],
      trimmedCount: 0,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: null,
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    }
    evictMessageFromBucket(bucket, 'm1')
    expect(bucket.messageOrder).toHaveLength(0)
    expect(bucket.messagesById['m1']).toBeUndefined()
  })

  it('removes tool calls owned by the evicted message (via tool_calls array)', () => {
    const bucket: SessionChatState = {
      ...emptyHistoryFixture(),
      messagesById: {
        'm1': {
          id: 'm1', role: 'assistant', content: '', timestamp: '', status: 'done',
          tool_calls: [{ id: 'tc1', tool: 'exec', params: {}, status: 'success' }],
        },
      },
      messageOrder: ['m1'],
      trimmedCount: 0,
      toolCalls: { tc1: { id: 'tc1', call_id: 'tc1', tool: 'exec', params: {}, status: 'success' } },
      toolCallOrder: ['tc1'],
      textAtToolCallStart: { tc1: 'some text' },
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: null,
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    }
    evictMessageFromBucket(bucket, 'm1')
    expect(bucket.toolCalls['tc1']).toBeUndefined()
    expect(bucket.toolCallOrder).toHaveLength(0)
    expect(bucket.textAtToolCallStart['tc1']).toBeUndefined()
  })

  // ADR-091 D10: spanByParentCallId is deleted along with the child-step-
  // nesting mechanism it served; spanBySpanId is now the only span index —
  // this test is updated to prove the surviving index's eviction.
  it('removes spanBySpanId entries pointing at the evicted message', () => {
    const bucket: SessionChatState = {
      ...emptyHistoryFixture(),
      messagesById: {
        'm1': { id: 'm1', role: 'assistant', content: '', timestamp: '', status: 'done' },
      },
      messageOrder: ['m1'],
      trimmedCount: 0,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      spanBySpanId: {
        'span1': { messageId: 'm1', spanIdx: 0 },
        'span2': { messageId: 'm2', spanIdx: 0 }, // belongs to a different message — must survive
      },
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: null,
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    }
    evictMessageFromBucket(bucket, 'm1')
    expect(bucket.spanBySpanId?.['span1']).toBeUndefined()
    // span2 belongs to a different message and must not be touched.
    expect(bucket.spanBySpanId?.['span2']).toBeDefined()
  })
})
