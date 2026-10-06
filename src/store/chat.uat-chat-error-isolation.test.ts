import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ErrorFrame, SubagentStateFrame } from '@/lib/api/generated/asyncapi-types'
import type { WsConnection } from '@/lib/ws'
import { getMessages, useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'

// U5-R1: per-chat errors never become an app-wide connection problem.
// Real stores, send/steer, navigation, routing and foreground synchronization.
// Socket only is fake. No fixture-built messages or terminal-status shortcuts.
// The untagged refusal below matches today's gateway shape; the late TAGGED
// cases test frontend isolation, not the separately needed backend correlation.
const HELPER = 'uat-error-helper'
const PARENT = 'uat-error-parent'
const REFUSAL = 'this agent is a worker and cannot be a chat target — workers are invoked via delegation'
const sender = { send: vi.fn<WsConnection['send']>() }

function reset() {
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  })
}
beforeEach(() => {
  reset()
  sender.send.mockReset().mockReturnValue(true)
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useSessionStore.getState().setActiveSession(HELPER, 'worker')
  })
})
afterEach(reset)

function streamAndSteerHelper() {
  act(() => {
    useChatStore.getState().sendMessage('Report progress', { clientMessageId: 'uat-helper-first' })
    useChatStore.getState().handleFrame({ type: 'token', session_id: HELPER, content: 'Original progress' })
    useChatStore.getState().sendMessage('Focus on the next step', { clientMessageId: 'uat-helper-steer' })
  })
  expect(getMessages(useChatStore.getState().sessionsById[HELPER]).filter((message) => message.content === 'Original progress').map((message) => message.closedBySteer)).toStrictEqual([true])
  expect(sender.send.mock.calls.map(([frame]) => frame.type)).toStrictEqual(['message', 'message'])
}

function assertLocalRefusal() {
  const messages = getMessages(useChatStore.getState().sessionsById[HELPER])
  expect(messages.filter((message) => message.status === 'error').map(({ content, role }) => ({ content, role }))).toStrictEqual([{ content: REFUSAL, role: 'assistant' }])
  expect(messages.filter((message) => message.content === 'Original progress').map((message) => message.closedBySteer)).toStrictEqual([true])
  expect(useConnectionStore.getState().isConnected).toBe(true)
  expect(useConnectionStore.getState().connectionError, 'U5: a chat refusal is not a lost connection').toBeNull()
}

describe('U5 — routed chat errors stay with their originating transcript', () => {
  it('an actual untagged worker refusal after a real mid-turn send does not leak into parent or new chats', () => {
    streamAndSteerHelper()
    act(() => useChatStore.getState().handleFrame({ type: 'error', message: REFUSAL }))
    assertLocalRefusal()
    const saved = structuredClone(getMessages(useChatStore.getState().sessionsById[HELPER]))
    act(() => useSessionStore.getState().setActiveSession(PARENT, 'jim'))
    expect(useChatStore.getState().messages).toStrictEqual([])
    expect(useConnectionStore.getState().connectionError).toBeNull()
    act(() => useSessionStore.getState().startNewSession())
    expect(useChatStore.getState().messages).toStrictEqual([])
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(getMessages(useChatStore.getState().sessionsById[HELPER])).toStrictEqual(saved)
    expect(useConnectionStore.getState().connectionError).toBeNull()
  })

  it.each(['streaming parent', 'new chat'] as const)('a delayed tagged helper refusal cannot change an already-open %s', (destination) => {
    streamAndSteerHelper()
    act(() => {
      if (destination === 'new chat') useSessionStore.getState().startNewSession()
      else {
        useSessionStore.getState().setActiveSession(PARENT, 'jim')
        useChatStore.getState().sendMessage('Keep working', { clientMessageId: 'uat-parent-user' })
        useChatStore.getState().handleFrame({ type: 'token', session_id: PARENT, content: 'Parent is working' })
      }
    })
    const foreground = structuredClone(useChatStore.getState().messages)
    const wasStreaming = useChatStore.getState().isStreaming
    const parent = structuredClone(useChatStore.getState().sessionsById[PARENT])
    const frame: ErrorFrame = { type: 'error', session_id: HELPER, client_message_id: 'uat-helper-steer', message: REFUSAL }
    act(() => useChatStore.getState().handleFrame(frame))
    assertLocalRefusal()
    expect(useChatStore.getState().messages).toStrictEqual(foreground)
    expect(useChatStore.getState().isStreaming).toBe(wasStreaming)
    expect(useChatStore.getState().sessionsById[PARENT]).toStrictEqual(parent)
    expect(useSessionStore.getState().activeSessionId).toBe(destination === 'new chat' ? null : PARENT)
  })

  it('preserves an unrouteable global protocol error instead of silently discarding it', () => {
    act(() => {
      useSessionStore.getState().startNewSession()
      useChatStore.getState().handleFrame({ type: 'error', message: 'Unsupported protocol version' })
    })
    expect(useConnectionStore.getState().connectionError).toBe('Unsupported protocol version')
    expect(useChatStore.getState().messages).toStrictEqual([])
  })

  it('still shows a global routing-protocol failure when a required session ID is missing', () => {
    streamAndSteerHelper()
    const saved = structuredClone(useChatStore.getState().sessionsById[HELPER])
    // Deliberately malformed server input, not a legal new wire type.
    const frame = { type: 'subagent_state', span_id: 'broken', state: 'stopped', created_at: '2026-10-06T00:00:00Z' } as SubagentStateFrame
    act(() => useChatStore.getState().handleFrame(frame))
    expect(useConnectionStore.getState().connectionError).toBe('internal: server frame missing session_id — please reload')
    expect(useChatStore.getState().sessionsById[HELPER]).toStrictEqual(saved)
  })
})
