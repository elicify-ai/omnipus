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
    // Fix 3: kickoffAttemptStatus is now module-level store state (not a
    // component-local useRef), so it survives across tests in this file
    // unless explicitly reset — several tests below reuse the same
    // workspace id ('ws-1', 'ws-9', ...), and a leftover 'in-flight' entry
    // from an earlier test's successful fire would otherwise permanently
    // block every later test for that same id.
    useChatStore.setState({ pendingKickoff: null, kickoffAttemptStatus: {} })
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  act(() => {
    useChatStore.setState({
      sendWorkspaceSetupKickoff: useChatStore.getInitialState().sendWorkspaceSetupKickoff,
      pendingKickoff: null,
      kickoffAttemptStatus: {},
    })
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

  it('cancelQueries is called for the active-list, archived-list, and detail keys before the optimistic clear lands', async () => {
    // Fix 4: an in-flight refetch that read the pre-clear (setup_pending:true)
    // disk state must be cancelled BEFORE the optimistic setQueryData below —
    // otherwise its (stale) result can land AFTER our write and resurrect the
    // flag, letting a later remount refire the kickoff.
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ id: 'ws-cancel', setup_pending: true, core_team: ['ava'] })
    const client = makeClient()
    client.setQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'active' }), [workspace])
    client.setQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'archived' }), [])
    client.setQueryData<Workspace>(workspacesQueryKeys.detail('ws-cancel'), workspace)
    const cancelSpy = vi.spyOn(client, 'cancelQueries')

    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })
    await waitFor(() => {
      const detail = client.getQueryData<Workspace>(workspacesQueryKeys.detail('ws-cancel'))
      expect(detail?.setup_pending).toBe(false)
    })

    const cancelledKeys = cancelSpy.mock.calls.map((call) =>
      JSON.stringify((call[0] as { queryKey: unknown }).queryKey),
    )
    expect(cancelledKeys).toContain(JSON.stringify(workspacesQueryKeys.list({ status: 'active' })))
    expect(cancelledKeys).toContain(JSON.stringify(workspacesQueryKeys.list({ status: 'archived' })))
    expect(cancelledKeys).toContain(JSON.stringify(workspacesQueryKeys.detail('ws-cancel')))
  })
})

describe('useWorkspaceSetupKickoff — Fix 4: archived workspaces are skipped', () => {
  it('does not fire for an archived workspace even when setup_pending is true', async () => {
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ status: 'archived', setup_pending: true, core_team: ['ava'] })
    const client = makeClient()
    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(fetchAgents).toHaveBeenCalled() })
    expect(kickoffSpy).not.toHaveBeenCalled()
  })
})

describe('useWorkspaceSetupKickoff — Fix 4: Set-based fired guard (A → B → A)', () => {
  it('fires for a second, different workspace while the first is still outstanding, but never refires the first', async () => {
    // Mirrors the real scenario (Fix 3): the store's own sendWorkspaceSetupKickoff
    // bails while a DIFFERENT kickoff is in flight, so this spy — which always
    // succeeds — stands in for "no other kickoff is pending" at the store
    // level; this test is purely about the HOOK's own per-workspace guard.
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const wsA = makeWorkspace({ id: 'ws-a', name: 'Workspace A', setup_pending: true, core_team: ['ava'] })
    const wsB = makeWorkspace({ id: 'ws-b', name: 'Workspace B', setup_pending: true, core_team: ['ava'] })
    const client = makeClient()

    const { rerender } = renderHook(({ ws }) => useWorkspaceSetupKickoff(ws), {
      wrapper: makeWrapper(client),
      initialProps: { ws: wsA },
    })
    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })
    expect(kickoffSpy).toHaveBeenLastCalledWith(expect.objectContaining({ workspaceId: 'ws-a' }))

    // Navigate to a DIFFERENT workspace that also needs setup — the Set
    // guard must not block this; only same-id refires are blocked.
    rerender({ ws: wsB })
    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(2) })
    expect(kickoffSpy).toHaveBeenLastCalledWith(expect.objectContaining({ workspaceId: 'ws-b' }))

    // Navigate back to A. Its prop object still has setup_pending:true (the
    // hook never mutates its input, and this simulates the cache-not-yet-
    // refetched window) — a single-slot ref would have forgotten A the
    // moment B fired, allowing a refire here. The Set guard must not.
    rerender({ ws: wsA })
    rerender({ ws: wsA })
    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(2) })
  })
})

describe('useWorkspaceSetupKickoff — Fix 4: agent targeting fallback', () => {
  it('prefers "ava" over core_team[0] when both are chat-eligible', async () => {
    const RAY: Agent = makeAgent({ id: 'ray', name: 'Ray', type: 'core', status: 'active' })
    vi.mocked(fetchAgents).mockResolvedValue([RAY, AVA])
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ id: 'ws-pref', setup_pending: true, core_team: ['ray', 'ava'] })
    const client = makeClient()
    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })
    expect(kickoffSpy).toHaveBeenCalledWith(expect.objectContaining({ agentId: 'ava' }))
  })

  it('falls back to the first chat-eligible core_team member when "ava" is not chat-eligible', async () => {
    const JIM: Agent = makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'active' })
    vi.mocked(fetchAgents).mockResolvedValue([JIM]) // 'ava' absent entirely — not chat-eligible
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ id: 'ws-fallback', setup_pending: true, core_team: ['nonexistent-agent', 'jim'] })
    const client = makeClient()
    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })
    expect(kickoffSpy).toHaveBeenCalledWith(expect.objectContaining({ agentId: 'jim' }))
  })

  it('logs (debug) and bails — never silently forever — when no chat-eligible agent exists at all', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([]) // nobody chat-eligible
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()
    const debugSpy = vi.spyOn(console, 'debug').mockImplementation(() => {})

    const workspace = makeWorkspace({ id: 'ws-noagent', setup_pending: true, core_team: ['nonexistent'] })
    const client = makeClient()
    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(fetchAgents).toHaveBeenCalled() })
    expect(kickoffSpy).not.toHaveBeenCalled()
    await waitFor(() => {
      expect(debugSpy).toHaveBeenCalledWith(
        expect.stringContaining('useWorkspaceSetupKickoff'),
        expect.objectContaining({ workspaceId: 'ws-noagent' }),
      )
    })
  })
})

describe('useWorkspaceSetupKickoff — Fix 3: honest retry after a synchronous send failure', () => {
  it('releases the guard on !sent, proven by a second fire once conditions re-trigger the effect', async () => {
    const kickoffSpy = vi.fn().mockReturnValueOnce(false).mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const workspace = makeWorkspace({ id: 'ws-retry-sent', setup_pending: true, core_team: ['ava'] })
    const client = makeClient()
    renderHook(() => useWorkspaceSetupKickoff(workspace), { wrapper: makeWrapper(client) })

    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })
    // The guard was released (marked 'failed', not left 'in-flight').
    expect(useChatStore.getState().kickoffAttemptStatus['ws-retry-sent']).toBe('failed')

    // Toggle isConnected off/on — mirrors a real reconnect — to re-trigger
    // the fire effect. With the guard released, this must fire again.
    act(() => { useConnectionStore.setState({ isConnected: false }) })
    act(() => { useConnectionStore.setState({ isConnected: true }) })

    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(2) })
  })
})

describe('useWorkspaceSetupKickoff — Fix 3: honest retry after an async server-side rejection', () => {
  // These tests simulate the ASYNC failure signal directly via
  // `resolveKickoffAttempt` (the same store action chat.ts's own
  // 'error'-frame / disconnect handling calls internally) rather than
  // driving a real WS round trip — this file deliberately never touches the
  // real WS/connection plumbing (see the file header comment), and the
  // signal this hook reacts to is exactly that store transition regardless
  // of what produced it.

  it('acceptance flow: invalidates on failure, then fires again once the refetched workspace still reports setup_pending:true', async () => {
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const client = makeClient()
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries')
    const workspace = makeWorkspace({ id: 'ws-retry-async', setup_pending: true, core_team: ['ava'] })

    const { rerender } = renderHook(({ ws }) => useWorkspaceSetupKickoff(ws), {
      wrapper: makeWrapper(client),
      initialProps: { ws: workspace },
    })
    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })

    // The kickoff terminally fails server-side, reported asynchronously.
    act(() => {
      useChatStore.getState().resolveKickoffAttempt('ws-retry-async', 'failed')
    })

    // The hook's invalidate-on-failure effect rolls back its own premature
    // optimistic cache clear by invalidating (not just patching) the
    // workspace queries — server truth wins.
    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((c) => JSON.stringify((c[0] as { queryKey: unknown }).queryKey))
      expect(keys).toContain(JSON.stringify(workspacesQueryKeys.list({ status: 'active' })))
      expect(keys).toContain(JSON.stringify(workspacesQueryKeys.detail('ws-retry-async')))
    })

    // "User navigates away and back (no reload)" — the refetched workspace
    // object (a fresh object, same content) confirms setup_pending is STILL
    // true — a genuine failure, not a duplicate. Re-opening retries.
    const refetchedWorkspace = makeWorkspace({ id: 'ws-retry-async', setup_pending: true, core_team: ['ava'] })
    rerender({ ws: refetchedWorkspace })

    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(2) })
  })

  it('duplicate case (server truth resolves setup_pending:false) does NOT retry-loop', async () => {
    const kickoffSpy = vi.fn().mockReturnValue(true)
    act(() => { useChatStore.setState({ sendWorkspaceSetupKickoff: kickoffSpy }) })
    connect()

    const client = makeClient()
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries')
    const workspace = makeWorkspace({ id: 'ws-dup', setup_pending: true, core_team: ['ava'] })

    const { rerender } = renderHook(({ ws }) => useWorkspaceSetupKickoff(ws), {
      wrapper: makeWrapper(client),
      initialProps: { ws: workspace },
    })
    await waitFor(() => { expect(kickoffSpy).toHaveBeenCalledTimes(1) })

    // Server rejects as a DUPLICATE — an EARLIER attempt already succeeded,
    // so setup_pending is already correctly false server-side.
    act(() => {
      useChatStore.getState().resolveKickoffAttempt('ws-dup', 'failed')
    })
    await waitFor(() => { expect(invalidateSpy).toHaveBeenCalled() })

    // The refetch confirms setup_pending is false (the duplicate case) —
    // the hook's OWN ordinary early-return guard stops any retry, with no
    // special-casing needed.
    const refetchedWorkspace = makeWorkspace({ id: 'ws-dup', setup_pending: false, core_team: ['ava'] })
    rerender({ ws: refetchedWorkspace })

    // Give the effect a chance to run, then assert it did NOT fire again.
    await waitFor(() => { expect(fetchAgents).toHaveBeenCalled() })
    expect(kickoffSpy).toHaveBeenCalledTimes(1)
  })
})
