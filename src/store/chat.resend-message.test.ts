/**
 * chat.resend-message.test.ts — review finding 17 (SQUAD-BRIEF-AZ /
 * REVIEW-OPUS-823.md #17): "Try again" on a failed user message duplicates it.
 *
 * "Try again" on a failed user message used to call sendMessage(content) with
 * a brand-new id — leaving the failed bubble in place and appending a
 * SECOND, duplicate bubble, and silently dropping any attachments (only the
 * plain content string was threaded through). resendMessage(id) fixes both:
 * same id, in place, original attachments.
 *
 * Split out of chat.test.ts (not merged into it) because chat.test.ts is a
 * grandfathered budget entry (scripts/budgets/files.txt) that may only
 * shrink — see docs/internal/architecture/draft-module-map.md, "Size
 * budgets". Self-contained reset/helpers, matching every other split
 * chat.*.test.ts file's convention (e.g. chat.outbound-queue.test.ts).
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore, makeBucketMessages } from './chat'
import type { SessionChatState, ChatMessage } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import type { WsConnection } from '@/lib/ws'

const TEST_SESSION_ID = 'test-session-resend'

function resetStore() {
  act(() => {
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
  })
}

beforeEach(resetStore)

function bucketFor(msgs: ChatMessage[]): SessionChatState {
  return {
    ...makeBucketMessages(msgs),
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
    spanByParentCallId: {},
  }
}

describe('chat store — resendMessage (review finding 17)', () => {
  it('resends the SAME message in place — no duplicate bubble — and includes its original attachments', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
      useChatStore.setState({
        sessionsById: {
          [TEST_SESSION_ID]: bucketFor([
            {
              id: 'failed-msg-1',
              session_id: TEST_SESSION_ID,
              role: 'user',
              content: 'do not duplicate me',
              timestamp: '2026-09-22T00:00:00Z',
              status: 'error',
              deliveryStatus: 'failed',
              mediaRefs: ['media://pic1'],
              media: [{ type: 'image', url: '/api/v1/uploads/s/pic.png', filename: 'pic.png', contentType: 'image/png' }],
            },
          ]),
        },
      })
    })

    act(() => {
      useChatStore.getState().resendMessage('failed-msg-1')
    })

    const state = useChatStore.getState()
    const userMessages = state.messages.filter((m) => m.role === 'user')
    // BUG REGRESSION: exactly ONE user bubble, not two — sendMessage(content)
    // used to mint a fresh id and append a second one.
    expect(userMessages).toHaveLength(1)
    expect(userMessages[0]?.id).toBe('failed-msg-1')
    expect(userMessages[0]?.content).toBe('do not duplicate me')
    expect(userMessages[0]?.deliveryStatus).toBe('sending')

    // BUG REGRESSION: the original media ref must ride along on the resend —
    // sendMessage(content) dropped it because only a plain string was passed.
    expect(mockSend).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'message',
        content: 'do not duplicate me',
        client_message_id: 'failed-msg-1',
        media: ['media://pic1'],
      }),
    )
  })

  it('marks the message failed again (in place) if the resend itself fails to send', () => {
    const mockSend = vi.fn().mockReturnValue(false)
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
      useChatStore.setState({
        sessionsById: {
          [TEST_SESSION_ID]: bucketFor([
            {
              id: 'failed-msg-2',
              session_id: TEST_SESSION_ID,
              role: 'user',
              content: 'still cannot send',
              timestamp: '2026-09-22T00:00:00Z',
              status: 'error',
              deliveryStatus: 'failed',
            },
          ]),
        },
      })
    })

    act(() => {
      useChatStore.getState().resendMessage('failed-msg-2')
    })

    const state = useChatStore.getState()
    const userMessages = state.messages.filter((m) => m.role === 'user')
    expect(userMessages).toHaveLength(1)
    expect(userMessages[0]?.deliveryStatus).toBe('failed')
  })

  it('is a no-op for an unknown message id', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
    })

    act(() => {
      useChatStore.getState().resendMessage('does-not-exist')
    })

    expect(mockSend).not.toHaveBeenCalled()
  })
})
