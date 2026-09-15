// ADR-087 (Truncation is an outcome, not a silence) — WP F, §7.2 layer 6:
// the WS-replay reducer (`case 'replay_message'`) must carry
// `truncated`/`truncation_reason` from a `ReplayMessageFrame` onto the
// resulting `ChatMessage`, applying the same D2 legacy-default rule
// (absent reason on a truncated entry means 'cancelled') the cold-load
// path (`rawToMessage`, src/lib/api.test.ts) applies.
//
// Harness pattern (handleFrame + act) mirrored from
// chat.replay-coalesce.test.ts.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'

const SESSION_ID = 'truncation-replay-test'

function resetStore() {
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
      lastReceivedEventTime: null,
    })
    useSessionStore.setState({
      activeSessionId: SESSION_ID,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStore)

describe('chat store — replay_message truncation plumbing (ADR-087 D2)', () => {
  it('a fresh-bubble replay_message frame with truncated:true + truncation_reason carries both onto the ChatMessage', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'The answer was cut off right',
        session_id: SESSION_ID,
        truncated: true,
        truncation_reason: 'max_output_tokens',
      })
    })

    const msg = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(msg).toBeDefined()
    expect(msg!.truncated).toBe(true)
    expect(msg!.truncationReason).toBe('max_output_tokens')
  })

  it('legacy rule: truncated:true with no wire reason replays as truncationReason:"cancelled"', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'Cancelled mid-stream',
        session_id: SESSION_ID,
        truncated: true,
      })
    })

    const msg = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(msg).toBeDefined()
    expect(msg!.truncated).toBe(true)
    expect(msg!.truncationReason).toBe('cancelled')
  })

  it('a non-truncated replay_message frame leaves truncated/truncationReason unset', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'A complete answer.',
        session_id: SESSION_ID,
      })
    })

    const msg = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(msg).toBeDefined()
    expect(msg!.truncated).toBeUndefined()
    expect(msg!.truncationReason).toBeUndefined()
  })

  it('D4a: a truncated replay_message frame with EMPTY content still creates the bubble, stamped truncated', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: '',
        session_id: SESSION_ID,
        truncated: true,
        truncation_reason: 'max_output_tokens',
      })
    })

    const msg = useChatStore.getState().messages.find((m) => m.role === 'assistant')
    expect(msg).toBeDefined()
    expect(msg!.content).toBe('')
    expect(msg!.truncated).toBe(true)
    expect(msg!.truncationReason).toBe('max_output_tokens')
  })

  it('coalesce path: a truncated frame that closes an empty tool-call placeholder stamps the SAME bubble', () => {
    // tool_call_start creates an empty assistant placeholder (same setup as
    // chat.replay-coalesce.test.ts); the following replay_message(assistant)
    // frame coalesces into it since its content was ''.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_trunc_1',
        tool: 'read_file',
        params: { path: '/a.txt' },
        session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_trunc_1',
        tool: 'read_file',
        result: { content: 'hello' },
        status: 'success',
        session_id: SESSION_ID,
      })
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'Reading the file and then cut off',
        session_id: SESSION_ID,
        truncated: true,
        truncation_reason: 'max_output_tokens',
      })
    })

    const assistants = useChatStore.getState().messages.filter((m) => m.role === 'assistant')
    expect(assistants).toHaveLength(1)
    expect(assistants[0].tool_calls?.map((tc) => tc.id)).toEqual(['tc_trunc_1'])
    expect(assistants[0].truncated).toBe(true)
    expect(assistants[0].truncationReason).toBe('max_output_tokens')
  })
})
