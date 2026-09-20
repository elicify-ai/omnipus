import { act } from 'react'
import { beforeEach, describe, expect, it } from 'vitest'

import { useChatStore } from './chat'
import type { PositionedToolCall } from './chat'
import { useSessionStore } from './session'

const sessionID = 'async-tool-error-session'

function firstToolCall(): PositionedToolCall | undefined {
  const assistant = useChatStore.getState().messages.find((message) => message.role === 'assistant')
  return (assistant?.tool_calls as PositionedToolCall[] | undefined)?.[0]
}

function bakedToolCalls(): PositionedToolCall[] {
  return useChatStore.getState().messages.flatMap(
    (message) => (message.tool_calls as PositionedToolCall[] | undefined) ?? [],
  )
}

describe('chat store — delayed async tool failures', () => {
  beforeEach(() => {
    useSessionStore.setState({ ...useSessionStore.getState(), activeSessionId: sessionID })
    act(() => { useChatStore.getState().resetSession() })
  })

  it('renders a late failure separately when a later turn reuses the provider call ID', () => {
    act(() => {
      const chat = useChatStore.getState()
      chat.handleFrame({ type: 'token', content: 'Started.', session_id: sessionID })
      chat.handleFrame({
        type: 'tool_call_start', call_id: 'call_0', tool: 'bash', params: { action: 'run' }, session_id: sessionID,
      })
      chat.handleFrame({
        type: 'tool_call_result', call_id: 'call_0', tool: 'bash', result: 'running', status: 'success', session_id: sessionID,
      })
      chat.handleFrame({ type: 'done', session_id: sessionID })
    })
    expect(firstToolCall()?.status).toBe('success')

    act(() => { useChatStore.getState().startToolCall('call_0', 'bash', { action: 'run', command: 'second' }) })
    expect(useChatStore.getState().toolCalls.call_0?.status).toBe('running')

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'call_0:async-error:123',
        tool: 'bash',
        result: 'Tool `bash` failed:\ncommand timed out',
        status: 'error',
        session_id: sessionID,
      })
    })

    expect(firstToolCall()).toEqual(expect.objectContaining({ id: 'call_0', status: 'success' }))
    expect(useChatStore.getState().toolCalls.call_0).toEqual(expect.objectContaining({ status: 'running' }))
    expect(bakedToolCalls()).toContainEqual(expect.objectContaining({
      id: 'call_0:async-error:123',
      status: 'error',
      result: 'Tool `bash` failed:\ncommand timed out',
    }))
  })
})
