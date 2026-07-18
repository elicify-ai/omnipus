// useWorkspaceSetupKickoff.test.ts — Unit C trigger-hook coverage.
//
// The hook itself owns none of the wire mechanics (that's
// useChatStore.sendWorkspaceSetupKickoff, covered by
// chat.workspace-setup-kickoff.test.ts) — its job is purely: decide WHEN to
// fire (setup_pending && connected && agent ready && no active session),
// fire AT MOST ONCE per workspace id, and optimistically clear
// `setup_pending` in the react-query cache afterward.
//
// `sendWorkspaceSetupKickoff` is swapped for a spy via `useChatStore.setState`
// (established pattern — see src/lib/omnipus-runtime.attachments.test.tsx /
// omnipus-runtime.model-name.test.ts overriding useUiStore.addToast the same
// way) so this file never touches the real WS/connection plumbing.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import type { Agent, Workspace } from '@/lib/api'
import { workspacesQueryKeys } from '@/lib/api'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
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
import { useWorkspaceSetupKickoff } from './useWorkspaceSetupKickoff'

function makeWorkspace(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: 'ws-1',
    name: 'Alpha Workspace',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2025-01-01T00:00:00Z',
    updated_at: '2025-01-01T00:00:00Z',
    ...overrides,
  }
}

const AVA: Agent = makeAgent({ id: 'ava', name: 'Ava', type: 'core', status: 'active' })

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client }, children)
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function connect() {
  act(() => {
    useConnectionStore.setState({
      connection: { send: vi.fn(), disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
      isConnected: true,
    })
  })
}

beforeEach(() => {
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([AVA])
  vi.mocked(fetchWorkspaces).mockReset().mockResolvedValue([])
  act(() => {
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  act(() => {
    useChatStore.setState({ sendWorkspaceSetupKickoff: useChatStore.getInitialState().sendWorkspaceSetupKickoff })
  })
})

describe('useWorkspaceSetupKickoff — fire conditions', () => {
  it('fires exactly once when setup_pending && connected && agent ready', async () => {
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ id: 'ws-1', name: 'Alpha Workspace', setup_pending: true, core_team: ['ava'] })
    const client = makeClient()
    const { rerender } = renderHook(({ ws }) => useWorkspaceSetupKickoff(ws), {
      wrapper: makeWrapper(client),
      initialProps: { ws: workspace },
    })

    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })
    expect(kickoffSpy).toHaveBeenCalledWith({
      workspaceId: 'ws-1',
      workspaceName: 'Alpha Workspace',
      agentId: 'ava',
      agentType: 'core',
    })

    // Re-render with the SAME workspace object (setup_pending still true on
    // the prop — the hook doesn't mutate its input) must NOT fire again; the
    // ref guard is the primary defense against a duplicate kickoff.
    rerender({ ws: workspace })
    rerender({ ws: workspace })
    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })
  })

  it('does not fire when setup_pending is false', async () => {
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ setup_pending: false, core_team: ['ava'] })
    const client = makeClient()
    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    // Give any pending effects/microtasks a chance to run, then assert it never fired.
    await waitFor(() => { expect(fetchAgents).toHaveBeenCalled() })
    expect(kickoffSpy).not.toHaveBeenCalled()
  })

  it('does not fire when setup_pending is absent (undefined)', async () => {
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ core_team: ['ava'] })
    delete (workspace as { setup_pending?: boolean }).setup_pending
    const client = makeClient()
    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(fetchAgents).toHaveBeenCalled() })
    expect(kickoffSpy).not.toHaveBeenCalled()
  })

  it('does not fire when a session is already active', async () => {
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()
    act(() => { useSessionStore.setState({ activeSessionId: 'existing-session' }) })

    const workspace = makeWorkspace({ setup_pending: true, core_team: ['ava'] })
    const client = makeClient()
    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(fetchAgents).toHaveBeenCalled() })
    expect(kickoffSpy).not.toHaveBeenCalled()
  })

  it('does not fire when disconnected', async () => {
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    // Deliberately do NOT call connect() — isConnected stays false.

    const workspace = makeWorkspace({ setup_pending: true, core_team: ['ava'] })
    const client = makeClient()
    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(fetchAgents).toHaveBeenCalled() })
    expect(kickoffSpy).not.toHaveBeenCalled()
  })

  it('does not fire when the target agent is not resolvable (not yet loaded / not ready)', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([]) // agent list empty — 'ava' unresolvable
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ setup_pending: true, core_team: ['ava'] })
    const client = makeClient()
    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(fetchAgents).toHaveBeenCalled() })
    expect(kickoffSpy).not.toHaveBeenCalled()
  })

  it('falls back to the "ava" agent id when the workspace has no core_team', async () => {
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ setup_pending: true })
    delete (workspace as { core_team?: string[] }).core_team
    const client = makeClient()
    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })
    expect(kickoffSpy).toHaveBeenCalledWith(expect.objectContaining({ agentId: 'ava' }))
  })
})

describe('useWorkspaceSetupKickoff — cache clear after firing', () => {
  it('clears setup_pending on the active-list, archived-list, and detail caches after a successful fire', async () => {
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ id: 'ws-9', name: 'Fresh WS', setup_pending: true, core_team: ['ava'] })
    const other = makeWorkspace({ id: 'ws-other', name: 'Other WS', setup_pending: true })
    const client = makeClient()
    client.setQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'active' }), [workspace, other])
    client.setQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'archived' }), [workspace])
    client.setQueryData<Workspace>(workspacesQueryKeys.detail('ws-9'), workspace)

    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })

    await waitFor(() => {
      const activeList = client.getQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'active' }))
      expect(activeList?.find((w) => w.id === 'ws-9')?.setup_pending).toBe(false)
    })
    // Untouched workspace in the same list keeps its own flag.
    const activeList = client.getQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'active' }))
    expect(activeList?.find((w) => w.id === 'ws-other')?.setup_pending).toBe(true)

    const archivedList = client.getQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'archived' }))
    expect(archivedList?.find((w) => w.id === 'ws-9')?.setup_pending).toBe(false)

    const detail = client.getQueryData<Workspace>(workspacesQueryKeys.detail('ws-9'))
    expect(detail?.setup_pending).toBe(false)
  })

  it('does NOT clear the cache when the kickoff bails (send returns false)', async () => {
    const kickoffSpy = vi.fn().mockReturnValue(false)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ id: 'ws-9', setup_pending: true, core_team: ['ava'] })
    const client = makeClient()
    client.setQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'active' }), [workspace])

    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })

    const activeList = client.getQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'active' }))
    expect(activeList?.find((w) => w.id === 'ws-9')?.setup_pending).toBe(true)
  })
})
