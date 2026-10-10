// useRunningActivity — FR-033 task/scheduler run rows from
// GET /agents/{id}/activity-runs: mapping, MAIN label, queued/waiting parking,
// unknown tokens, and the "one MAIN row" de-duplication against a spawned span.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import type { AgentActivityRun } from '@/lib/api/generated/openapi-types'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn(), fetchAgentActivityRuns: vi.fn() }
})

import { fetchAgents, fetchAgentActivityRuns } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { useRunningActivity } from './useRunningActivity'
import type { TaskRunActivityItem } from './useRunningActivity'

function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return ({ children }: { children: React.ReactNode }) => React.createElement(QueryClientProvider, { client }, children)
}

function wireRun(overrides: Partial<AgentActivityRun>): AgentActivityRun {
  return {
    run_id: 'run-1', task_id: 'task-1', task_title: 'Nightly report', kind: 'scheduled', mode: 'isolated',
    state: 'running', role: 'assignee', agent_id: 'ava', started_at: new Date(Date.now() - 5_000).toISOString(),
    ...overrides,
  }
}

beforeEach(() => {
  vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'ava', name: 'Ava' })])
  act(() => {
    useChatStore.setState({ messages: [], toolCalls: {} })
    useSessionStore.setState({ activeAgentId: 'ava' })
  })
})

describe('useRunningActivity — task and scheduler runs', () => {
  it('maps a running run to a task row with the assignee and unknown tokens', async () => {
    vi.mocked(fetchAgentActivityRuns).mockResolvedValue([wireRun({ session_id: 'sess-run-1' })])
    const { result } = renderHook(() => useRunningActivity(), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.running).toHaveLength(1))
    const item = result.current.running[0] as TaskRunActivityItem
    expect(vi.mocked(fetchAgentActivityRuns)).toHaveBeenCalledWith('ava')
    expect(item).toMatchObject({ kind: 'task', key: 'run-1', taskLabel: 'Nightly report', status: 'running', sessionId: 'sess-run-1', agentName: 'Ava', availableTokens: null })
    expect(result.current.runningCount).toBe(1)
  })

  it('labels a MAIN run and parks queued and waiting runs outside the running count', async () => {
    vi.mocked(fetchAgentActivityRuns).mockResolvedValue([
      wireRun({ run_id: 'm', mode: 'main' }),
      wireRun({ run_id: 'q', state: 'queued' }),
      wireRun({ run_id: 'w', state: 'waiting' }),
    ])
    const { result } = renderHook(() => useRunningActivity(), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.running).toHaveLength(3))
    const byKey = Object.fromEntries((result.current.running as TaskRunActivityItem[]).map((i) => [i.key, i]))
    expect(byKey.m.taskLabel).toBe('MAIN · Nightly report')
    expect(byKey.q).toMatchObject({ runState: 'queued', status: 'parked' })
    expect(byKey.w).toMatchObject({ runState: 'waiting', status: 'parked' })
    expect(result.current.runningCount).toBe(1)
  })

  it('does not fetch without an active agent', async () => {
    act(() => useSessionStore.setState({ activeAgentId: null }))
    vi.mocked(fetchAgentActivityRuns).mockClear()
    renderHook(() => useRunningActivity(), { wrapper: wrapper() })
    await new Promise((r) => setTimeout(r, 20))
    expect(vi.mocked(fetchAgentActivityRuns)).not.toHaveBeenCalled()
  })

  it('shows one row, not two, when the run is already this chat\'s child span', async () => {
    vi.mocked(fetchAgentActivityRuns).mockResolvedValue([wireRun({ mode: 'main', session_id: 'sess-child' })])
    act(() => {
      useChatStore.setState({
        messages: [{
          id: 'm1', role: 'assistant', content: '', timestamp: new Date().toISOString(), status: 'done',
          spans: [{ spanId: 's1', parentCallId: 'c1', taskLabel: 'Nightly report', status: 'running', agentId: 'ava', childSessionId: 'sess-child', lifecycleState: 'running' }],
        } as never],
      })
    })
    const { result } = renderHook(() => useRunningActivity(), { wrapper: wrapper() })
    await waitFor(() => expect(vi.mocked(fetchAgentActivityRuns)).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 30))
    expect(result.current.running.map((i) => i.kind)).toEqual(['agent'])
  })
})
