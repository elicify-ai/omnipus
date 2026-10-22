// Founder Q10=A / security F1. checklistDisplay is private: exercise it through
// the real public hook, real frame reducer/store and production query client.
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { ToolCallStartFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { __resetFinishedTurnIdsForTests } from '@/store/chat/frames'
import { useCurrentTodo } from './useCurrentTodo'

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchSessionMessages: vi.fn().mockResolvedValue([]),
}))

// not-wire-format: independent local test expectations, not gateway data types.
type Q10Case = { name: string; input: string; expected: string; reject: boolean }
const fixture: { source: string; cases: Q10Case[] } = JSON.parse(readFileSync(resolve('tests/fixtures/q10-active-form-normalization.json'), 'utf8'))
const SID = 'q10-normalization-selector'

function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

beforeEach(() => {
  __resetFinishedTurnIdsForTests()
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: SID, activeAgentId: 'mia' }, true)
  useConnectionStore.setState({ ...useConnectionStore.getInitialState(), isConnected: true }, true)
  act(() => {
    useChatStore.getState().handleFrame({ type: 'token', session_id: SID, message_id: 'q10-answer', turn_id: 'q10-turn', content: 'Preparing the report' })
    // activeTurnId is authoritative session state, not inferred from a token.
    useChatStore.getState().handleFrame({
      type: 'session_state', session_id: SID, user_id: 'operator', pending_approvals: [], emitted_at: '2026-10-22T10:00:00Z',
      active_turn: { turn_id: 'q10-turn', agent_id: 'mia', started_at: '2026-10-22T09:59:00Z' },
    })
  })
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  vi.restoreAllMocks()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
})

describe('Q10 checklistDisplay normalization through the real public hook', () => {
  it('loads all 38 independently specified inputs rather than collecting an empty fixture', () => {
    expect(fixture.source).toBe('Founder Q10=A, PR #1406 round 2; security F1')
    expect(fixture.cases).toHaveLength(38)
    expect(new Set(fixture.cases.map((item) => item.name)).size).toBe(38)
  })

  it.each(fixture.cases)('$name', async (tc) => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { result } = renderHook(() => useCurrentTodo(true), { wrapper: Wrapper })
    expect(result.current).toEqual({ activeForm: null })
    const todos: ToolCallStartFrame['params']['todos'] = [{ text: 'Draft the summary', status: 'in_progress', active_form: tc.input }]
    const original = structuredClone(todos)
    const callId = 'q10-accepted-call'
    await act(async () => {
      useChatStore.getState().handleFrame({ type: 'tool_call_start', session_id: SID, call_id: callId, tool: 'set_todos', params: { outcome: 'Normalization proof', todos } })
      useChatStore.getState().handleFrame({ type: 'tool_call_result', session_id: SID, call_id: callId, tool: 'set_todos', status: 'success', result: 'Checklist saved' })
    })
    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.isStreaming).toBe(true)
    expect(bucket.activeTurnId).toBe('q10-turn')
    expect(bucket.toolCalls[callId].status).toBe('success')
    expect(bucket.toolCalls[callId].params.todos, 'normalize a display copy, never the recorded raw arguments').toEqual(original)
    expect(result.current, 'exact normalized selector output, with invisible-only/over-limit copy absent').toEqual({
      activeForm: tc.reject || tc.expected === '' ? null : tc.expected,
    })
    if (tc.reject) {
      expect(warn).toHaveBeenCalledExactlyOnceWith('chat.current_todo_validation_failed', {
        sessionId: SID, callId, issues: [{ code: 'too_big', path: '0.active_form' }],
      })
    } else {
      expect(warn, 'normalization must precede generated validation, including the 499/500 padded cases').not.toHaveBeenCalled()
    }
  })
})
