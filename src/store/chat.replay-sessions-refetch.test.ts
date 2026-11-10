// A reconnect/sign-in replay delivers one frame per persisted event. Before
// this fix every replayed subagent_* frame invalidated the ['sessions'] query,
// so a transcript with many subagent rows produced dozens of GET
// /api/v1/sessions in seconds (observed: 86 in 8 s, test gateway 2026-10-11).
// Contract: a replay batch refreshes the session list at most once (at its
// terminating `done`); live frames outside a replay still refresh as before.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { queryClient } from '@/lib/queryClient'

const SID = 'replay-refetch-session'
const N = 40

function sessionsInvalidations(spy: { mock: { calls: unknown[][] } }): number {
  return spy.mock.calls.filter(([arg]) => JSON.stringify((arg as { queryKey?: unknown })?.queryKey) === '["sessions"]').length
}

function subagentState(i: number, state: 'queued' | 'running') {
  return {
    type: 'subagent_state' as const,
    span_id: `span-${i}`,
    state,
    created_at: '2026-10-11T08:30:00.000Z',
    session_id: SID,
  }
}

beforeEach(() => {
  vi.restoreAllMocks()
  act(() => {
    useChatStore.setState({ sessionsById: {}, messages: [], isStreaming: false, isReplaying: false })
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: null })
  })
})

describe('sessions list refetch during replay', () => {
  it('N replayed subagent_state frames invalidate the sessions query at most once', () => {
    const spy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      useChatStore.getState().setReplaying(true)
      for (let i = 0; i < N; i++) useChatStore.getState().handleFrame(subagentState(i, 'queued'))
      useChatStore.getState().handleFrame({ type: 'done', session_id: SID } as never)
    })
    const n = sessionsInvalidations(spy)
    expect(n).toBeGreaterThanOrEqual(1) // the batch still refreshes the list once
    expect(n).toBeLessThanOrEqual(1)
  })

  it('live (non-replay) subagent_state frames still invalidate each time', () => {
    const spy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      for (let i = 0; i < 3; i++) useChatStore.getState().handleFrame(subagentState(i, 'running'))
    })
    expect(sessionsInvalidations(spy)).toBe(3)
  })
})
