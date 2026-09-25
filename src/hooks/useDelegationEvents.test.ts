/**
 * The hook reads one session's bucket. Lines for another session must not
 * appear, and a child's own result — present on the stored span — must not
 * appear on a line.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import type { Agent, ToolCall } from '@/lib/api'
import { useChatStore } from '@/store/chat'
import type { ChatMessage, SubagentSpan } from '@/store/chat'
import { emptySessionState } from '@/store/chat/session'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn() }
})

import { fetchAgents } from '@/lib/api'
import { useDelegationEvents } from './useDelegationEvents'

const SENTINEL = 'zz-child-authored-7f3c2a'

function wrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client }, children)
  }
}

function assistant(id: string, partial: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id,
    role: 'assistant',
    content: '',
    timestamp: '2026-09-25T12:00:00.000Z',
    status: 'done',
    ...partial,
  } as ChatMessage
}

function bucketFor(message: ChatMessage, live: ToolCall[] = []) {
  const bucket = emptySessionState()
  bucket.messagesById = { [message.id]: message }
  bucket.messageOrder = [message.id]
  bucket.toolCalls = Object.fromEntries(live.map((call) => [call.id, { ...call, call_id: call.id }]))
  bucket.toolCallOrder = live.map((call) => call.id)
  bucket.toolCallOwnerMessageId = Object.fromEntries(live.map((call) => [call.id, message.id]))
  return bucket
}

beforeEach(() => {
  vi.mocked(fetchAgents).mockReset()
  vi.mocked(fetchAgents).mockResolvedValue([
    { id: 'ray', name: 'Ray', type: 'Subagent', locked: false, status: 'active' } as Agent,
  ])
  useChatStore.setState({ sessionsById: {} })
})

describe('useDelegationEvents', () => {
  it('reads the named session, including a tool call baked onto its message', async () => {
    const denied: ToolCall = {
      id: 'run-denied',
      tool: 'delegate',
      status: 'error',
      params: { action: 'run', agent_id: 'ray' },
      result: JSON.stringify({ error: 'delegation_denied', reason: 'depth cap reached', policy: 'depth', tool: 'delegate' }),
    }
    const other = assistant('m-other', {
      spans: [
        {
          spanId: 'span-other',
          parentCallId: 'run-other',
          taskLabel: 'Other session work',
          status: 'success',
          durationMs: 10,
          childSessionId: 'child-other',
          agentId: 'ray',
        } as SubagentSpan,
      ],
      tool_calls: [
        {
          id: 'run-other',
          tool: 'delegate',
          status: 'success',
          params: { action: 'run', agent_id: 'ray' },
          result: JSON.stringify({ session_id: 'child-other', generation: 1, is_3p: false, state: 'running' }),
        },
      ],
    })
    useChatStore.setState({
      sessionsById: {
        'session-a': bucketFor(assistant('m-a', { tool_calls: [denied] })),
        'session-b': bucketFor(other),
      },
    })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { result } = renderHook(() => useDelegationEvents('session-a'), { wrapper: wrapper(client) })

    expect(result.current.map((event) => event.kind)).toEqual(['refused'])
    expect(result.current[0].reason).toBe('depth cap reached')
    expect(result.current[0].childSessionId).toBeUndefined()
    expect(JSON.stringify(result.current)).not.toContain('Other session work')

    const none = renderHook(() => useDelegationEvents(null), { wrapper: wrapper(client) })
    expect(none.result.current).toEqual([])
    const missing = renderHook(() => useDelegationEvents('missing'), { wrapper: wrapper(client) })
    expect(missing.result.current).toEqual([])
  })

  it('AC-7: the stored child result is present, and the hook does not put it on a line', async () => {
    const child: SubagentSpan = {
      spanId: 'span-1',
      parentCallId: 'run-1',
      taskLabel: 'Audit the logs',
      status: 'success',
      durationMs: 40,
      agentId: 'ray',
      childSessionId: 'child-1',
      finalResult: `done ${SENTINEL}`,
      lifecycleState: 'completed',
    }
    expect(child.finalResult).toContain(SENTINEL)
    const message = assistant('m1', {
      spans: [child],
      tool_calls: [
        {
          id: 'run-1',
          tool: 'delegate',
          status: 'success',
          params: { action: 'run', agent_id: 'ray', label: 'Audit the logs' },
          result: JSON.stringify({ session_id: 'child-1', generation: 1, is_3p: false, state: 'running' }),
        },
      ],
    })
    useChatStore.setState({ sessionsById: { 'session-a': bucketFor(message) } })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { result } = renderHook(() => useDelegationEvents('session-a'), { wrapper: wrapper(client) })

    expect(result.current.length).toBeGreaterThan(0)
    expect(JSON.stringify(result.current)).not.toContain(SENTINEL)
    await waitFor(() => {
      expect(result.current.find((event) => event.kind === 'delegated')?.agentName).toBe('Ray')
    })
  })

  it('sees a refusal that is still on the live turn and not yet baked', () => {
    const denied: ToolCall = {
      id: 'run-live',
      tool: 'delegate',
      status: 'error',
      params: { action: 'run' },
      result: JSON.stringify({ error: 'skill_not_found', message: 'no such skill', tool: 'delegate', skill: 'nope' }),
    }
    useChatStore.setState({
      sessionsById: { 'session-a': bucketFor(assistant('m1'), [denied]) },
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { result } = renderHook(() => useDelegationEvents('session-a'), { wrapper: wrapper(client) })
    expect(result.current).toMatchObject([{ kind: 'refused', reason: 'no such skill' }])
    expect(result.current[0].childSessionId).toBeUndefined()
  })
})
