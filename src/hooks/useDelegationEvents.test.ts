/**
 * The hook reads one session's bucket. Lines for another session must not
 * appear, and a child's own result — present on the stored span — must not
 * appear on a line.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import type { Agent, ToolCall } from '@/lib/api'
import { useChatStore } from '@/store/chat'
import type { ChatMessage, SessionChatState, SubagentSpan } from '@/store/chat'
import { emptySessionState } from '@/store/chat/session'

/**
 * Counts real derivation runs. The wrapper calls the real function, so line
 * contents stay the derivation's own; only the call count is intercepted.
 * A token must not increment it. Replacing a spans array must.
 */
const deriveCalls = vi.hoisted(() => ({ n: 0 }))

vi.mock('@/lib/delegationEvents', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/delegationEvents')>()
  return {
    ...actual,
    deriveDelegationEvents: (source: Parameters<typeof actual.deriveDelegationEvents>[0]) => {
      deriveCalls.n += 1
      return actual.deriveDelegationEvents(source)
    },
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn() }
})

import { fetchAgents } from '@/lib/api'
import { delegationMessageViewsBuilt, useDelegationEvents } from './useDelegationEvents'

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
  deriveCalls.n = 0
  vi.mocked(fetchAgents).mockReset()
  vi.mocked(fetchAgents).mockResolvedValue([
    { id: 'ray', name: 'Ray', type: 'Subagent', locked: false, status: 'active' } as Agent,
  ])
  useChatStore.setState({ sessionsById: {} })
})

/** A finished child: one delegated line and one finished line (spec event table). */
function finishedChildMessage(id: string): ChatMessage {
  return assistant(id, {
    spans: [
      {
        spanId: 'span-1',
        parentCallId: 'run-1',
        taskLabel: 'Audit the logs',
        status: 'success',
        durationMs: 40,
        agentId: 'ray',
        childSessionId: 'child-1',
        lifecycleState: 'completed',
      } as SubagentSpan,
    ],
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
}

function replaceMessage(
  sessionId: string,
  messageId: string,
  patch: Partial<ChatMessage>,
): void {
  const current = useChatStore.getState().sessionsById[sessionId]
  if (!current) throw new Error(`missing session ${sessionId}`)
  const message = current.messagesById[messageId]
  if (!message) throw new Error(`missing message ${messageId}`)
  // ChatMessage is a role union. Spreading it widens the role; the patch
  // does not change role, so the result is still that same message.
  const nextMessage = { ...message, ...patch } as ChatMessage
  const nextBucket: SessionChatState = {
    ...current,
    messagesById: { ...current.messagesById, [messageId]: nextMessage },
  }
  useChatStore.setState({
    sessionsById: { ...useChatStore.getState().sessionsById, [sessionId]: nextBucket },
  })
}

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

describe('useDelegationEvents derivation cost', () => {
  it('a token-only update does not re-derive unchanged messages and keeps the same lines', async () => {
    // The streaming message is the one a token rewrites. The settled message
    // holds the delegation. Neither message's spans or tool calls change.
    const streaming = assistant('m-stream', { content: 'Hello', status: 'streaming' })
    const settled = finishedChildMessage('m-settled')
    useChatStore.setState({
      sessionsById: { 'session-a': bucketForMessages([settled, streaming]) },
    })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { result } = renderHook(() => useDelegationEvents('session-a'), { wrapper: wrapper(client) })

    await waitFor(() => {
      expect(result.current.map((event) => event.kind)).toEqual(['delegated', 'finished'])
      expect(result.current[0].agentName).toBe('Ray')
      expect(result.current[0].childSessionId).toBe('child-1')
    })
    const callsAfterLoad = deriveCalls.n
    expect(callsAfterLoad).toBeGreaterThan(0)
    const lines = result.current

    act(() => {
      replaceMessage('session-a', 'm-stream', { content: 'Hello token' })
    })

    expect(deriveCalls.n).toBe(callsAfterLoad)
    expect(result.current).toBe(lines)
    expect(result.current.map((event) => event.id)).toEqual(lines.map((event) => event.id))
  })

  it('replacing one message spans re-derives only that message and shows the new ending', async () => {
    const settled = finishedChildMessage('m-settled')
    const spans = settled.spans
    expect(spans).toBeDefined()
    // A status poll is not a line (spec). It has its own tool-call array, so
    // a rebuild of every message would count it. Only the changed message may.
    const poll = assistant('m-poll', {
      tool_calls: [
        {
          id: 'poll-1',
          tool: 'delegate',
          status: 'success',
          params: { action: 'status', session_id: 'child-1' },
          result: JSON.stringify({ session_id: 'child-1', state: 'completed' }),
        },
      ],
    })
    useChatStore.setState({ sessionsById: { 'session-a': bucketForMessages([settled, poll]) } })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { result } = renderHook(() => useDelegationEvents('session-a'), { wrapper: wrapper(client) })
    await waitFor(() => {
      expect(result.current.map((event) => event.kind)).toEqual(['delegated', 'finished'])
      expect(result.current[0].agentName).toBe('Ray')
    })
    const callsAfterLoad = deriveCalls.n
    const viewsAfterLoad = delegationMessageViewsBuilt()
    expect(callsAfterLoad).toBeGreaterThan(0)
    expect(viewsAfterLoad).toBeGreaterThan(0)

    const original = spans?.[0]
    if (!original || original.status === 'running') {
      throw new Error('fixture span must already be terminal')
    }
    const failed: SubagentSpan = { ...original, status: 'error' }
    act(() => {
      replaceMessage('session-a', 'm-settled', { spans: [failed] })
    })

    // error, not success: the child ended without finishing (spec event table).
    expect(result.current.map((event) => event.kind)).toEqual(['delegated', 'stopped'])
    expect(deriveCalls.n).toBe(callsAfterLoad + 1)
    expect(delegationMessageViewsBuilt()).toBe(viewsAfterLoad + 1)
  })

  it('a new message timestamp re-derives and shifts every line time by that delta', async () => {
    const settled = finishedChildMessage('m-settled')
    useChatStore.setState({ sessionsById: { 'session-a': bucketForMessages([settled]) } })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { result } = renderHook(() => useDelegationEvents('session-a'), { wrapper: wrapper(client) })
    await waitFor(() => {
      expect(result.current.map((event) => event.kind)).toEqual(['delegated', 'finished'])
    })
    const callsAfterLoad = deriveCalls.n
    const before = result.current.map((event) => event.at)
    // Five seconds later. Line time is the anchor message's timestamp plus
    // the line's position on that message, so every line moves by this delta.
    const deltaMs = 5_000
    const later = new Date(Date.parse(settled.timestamp) + deltaMs).toISOString()

    act(() => {
      replaceMessage('session-a', 'm-settled', { timestamp: later })
    })

    expect(deriveCalls.n).toBeGreaterThan(callsAfterLoad)
    expect(result.current.map((event) => event.at)).toEqual(before.map((at) => at + deltaMs))
  })
})

function bucketForMessages(messages: ChatMessage[]): SessionChatState {
  const bucket = emptySessionState()
  bucket.messagesById = Object.fromEntries(messages.map((message) => [message.id, message]))
  bucket.messageOrder = messages.map((message) => message.id)
  return bucket
}
