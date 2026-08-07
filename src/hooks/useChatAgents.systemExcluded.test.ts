// useChatAgents.systemExcluded.test.ts — ADR-049 D3/FR-096/US-13 AS-3.
//
// A `type: 'system'` agent (the locked Judge) must never be a chat target —
// it must be excluded from `chatAgents`, which both the AgentPicker dropdown
// and the "@" mention menu (useSlashMenu.ts) consume. Companion to the
// existing worker/status-exclusion coverage in useChatAgents.test.ts (kept
// separate per this wave's task list rather than folded into that file).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import type { Agent } from '@/lib/api'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { makeAgent } from '@/test/factories'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    fetchWorkspaces: vi.fn(),
  }
})

import { fetchAgents, fetchWorkspaces } from '@/lib/api'
import { useChatAgents } from './useChatAgents'

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client }, children)
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

const mockAgents: Agent[] = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active' }),
  makeAgent({ id: 'judge', name: 'Judge', type: 'system', locked: true, status: 'active' }),
]

beforeEach(() => {
  vi.clearAllMocks()
  act(() => { useWorkspacesStore.setState({ activeWorkspaceId: null }) })
})

describe('useChatAgents — type:system excluded (ADR-049 D3)', () => {
  it('chatAgents excludes the System agent even though it is active/non-worker', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
    vi.mocked(fetchWorkspaces).mockResolvedValue([])

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.chatAgents.length).toBeGreaterThan(0) })
    expect(result.current.chatAgents.map((a) => a.id)).not.toContain('judge')
    expect(result.current.chatAgents.map((a) => a.id)).toEqual(['mia'])
  })

  it('the unfiltered `agents` list still carries the System agent (only chatAgents is scoped)', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
    vi.mocked(fetchWorkspaces).mockResolvedValue([])

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.agents).toHaveLength(2) })
    expect(result.current.agents.map((a) => a.id)).toContain('judge')
  })
})
