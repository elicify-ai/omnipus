// useChatAgents.test.ts — direct unit coverage of the shared agent-list
// query + scoping extracted in commit 103c5fd0 (feat(chat): @ agent-mention
// menu). AgentPicker.test.tsx already exercises this hook transitively
// through AgentPicker's own rendering (worker exclusion, a SET core_team,
// the all-draft branch), and useSlashMenu.test.ts exercises worker exclusion
// and name/id filtering transitively through the "@" mention menu — but
// neither file drives the hook directly, and neither covers the
// "core_team is [] " / "core_team is absent" no-filtering paths explicitly.
// This file closes that gap with a fast, direct renderHook suite so a
// scoping regression here fails close to the cause instead of only showing
// up as a wrong row count three components away.
//
// Reset convention: a single top-level `beforeEach` (vi.clearAllMocks +
// workspace store reset) mirrors AgentPicker.test.tsx rather than an
// `afterEach` — resetting mocks/store in `afterEach` raced this suite's own
// still-mounted renderHook instance (global cleanup() from src/test/setup.ts
// runs its unmount AFTER a locally-registered afterEach), which produced a
// benign but noisy "Query data cannot be undefined" console warning.
//
// Traces to: src/hooks/useChatAgents.ts (new in 103c5fd0).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import type { Agent, Workspace } from '@/lib/api'
import { useWorkspacesStore } from '@/store/workspacesStore'

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

// Distinct-prefix, distinct-status, distinct-type fixture set shared across
// tests — deliberately includes one of every exclusion category so each
// test only needs to vary the workspace/core_team side of the equation.
const mockAgents: Agent[] = [
  { id: 'mia', name: 'Mia', type: 'core', status: 'active', description: 'Assistant' } as Agent,
  { id: 'jim', name: 'Jim', type: 'core', status: 'idle', description: 'Orchestrator' } as Agent,
  { id: 'ray-draft', name: 'Ray Draft', type: 'Main', status: 'draft', description: 'Not ready' } as Agent,
  { id: 'sub1', name: 'Sub Worker', type: 'Subagent', status: 'active', description: 'Delegation-only' } as Agent,
  { id: 'sub2', name: 'External Worker', type: 'subagent_3p', status: 'active', description: 'External CLI worker' } as Agent,
]

beforeEach(() => {
  vi.clearAllMocks()
  act(() => { useWorkspacesStore.setState({ activeWorkspaceId: null }) })
})

describe('useChatAgents — status + worker scoping (no workspace set)', () => {
  it('agents carries the FULL unfiltered list straight from the query', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
    vi.mocked(fetchWorkspaces).mockResolvedValue([])

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.agents).toHaveLength(5) })
    expect(result.current.agents.map((a) => a.id).sort()).toEqual(
      ['jim', 'mia', 'ray-draft', 'sub1', 'sub2'].sort(),
    )
  })

  it('chatAgents excludes draft-status agents (not active/idle) — only ready-to-chat agents remain', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
    vi.mocked(fetchWorkspaces).mockResolvedValue([])

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.chatAgents.length).toBeGreaterThan(0) })
    expect(result.current.chatAgents.map((a) => a.id)).not.toContain('ray-draft')
  })

  it('chatAgents excludes worker types: "Subagent" and "subagent_3p"', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
    vi.mocked(fetchWorkspaces).mockResolvedValue([])

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.chatAgents.length).toBeGreaterThan(0) })
    const ids = result.current.chatAgents.map((a) => a.id)
    expect(ids).not.toContain('sub1')
    expect(ids).not.toContain('sub2')
  })

  it('chatAgents ends up with exactly the two ready, non-worker agents (mia, jim) — no workspace scoping active', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
    vi.mocked(fetchWorkspaces).mockResolvedValue([])

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.chatAgents).toHaveLength(2) })
    expect(result.current.chatAgents.map((a) => a.id).sort()).toEqual(['jim', 'mia'])
  })
})

describe('useChatAgents — core_team scoping', () => {
  it('a SET core_team narrows chatAgents to just the listed ids', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-1', name: 'Team WS', status: 'active', core_team: ['mia'] } as unknown as Workspace,
    ])
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' }) })

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.chatAgents).toHaveLength(1) })
    expect(result.current.chatAgents[0].id).toBe('mia')
  })

  it('an EMPTY core_team array ([]) means no team filtering — both ready agents still show', async () => {
    // Traces to useChatAgents.ts's filter: `!teamIds || teamIds.length === 0
    // || teamIds.includes(a.id)` — the `.length === 0` branch is the case
    // under test: a workspace that HAS a core_team field, but it's empty.
    vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-2', name: 'Empty Team WS', status: 'active', core_team: [] } as unknown as Workspace,
    ])
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws-2' }) })

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.chatAgents).toHaveLength(2) })
    expect(result.current.chatAgents.map((a) => a.id).sort()).toEqual(['jim', 'mia'])
  })

  it('an ABSENT core_team (workspace has no core_team field at all) means no team filtering', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-3', name: 'No Team Field WS', status: 'active' } as unknown as Workspace,
    ])
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws-3' }) })

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.chatAgents).toHaveLength(2) })
    expect(result.current.chatAgents.map((a) => a.id).sort()).toEqual(['jim', 'mia'])
  })

  it('no active workspace at all (activeWorkspaceId null) means no team filtering — differentiates from the scoped-down case above', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    // activeWorkspaceId stays null (top-level beforeEach default).

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.chatAgents).toHaveLength(2) })
    expect(result.current.chatAgents.map((a) => a.id).sort()).toEqual(['jim', 'mia'])
  })
})

describe('useChatAgents — isError passthrough', () => {
  it('isError is true when the agents query rejects', async () => {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('network error'))
    vi.mocked(fetchWorkspaces).mockResolvedValue([])

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.isError).toBe(true) })
    expect(result.current.agents).toEqual([])
    expect(result.current.chatAgents).toEqual([])
  })

  it('isError is false on the happy path', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
    vi.mocked(fetchWorkspaces).mockResolvedValue([])

    const { result } = renderHook(() => useChatAgents(), { wrapper: makeWrapper(makeClient()) })

    await waitFor(() => { expect(result.current.agents.length).toBeGreaterThan(0) })
    expect(result.current.isError).toBe(false)
  })
})
