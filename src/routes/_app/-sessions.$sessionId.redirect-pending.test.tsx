// B25 transport finding: a legacy /sessions/{id} deep link must not mount
// inline ChatScreen while its workspace redirect is still undecided.
// Current brief pins FOUR SECONDS independently of the production constant.
// REAL: SessionRoute, QueryClient/useQuery, onlineManager and session stores.
// EDGES: HTTP functions, navigation and the child ChatScreen render marker.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { onlineManager, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Workspace } from '@/lib/api/generated/openapi-types'

const edges = vi.hoisted(() => ({
  fetchWorkspaces: vi.fn<typeof import('@/lib/api')['fetchWorkspaces']>(),
  fetchSessionDetail: vi.fn<typeof import('@/lib/api')['fetchSessionDetail']>(),
  navigate: vi.fn(),
  inline: vi.fn(),
}))
vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => { edges.inline(); return <div data-testid="inline-chat-screen" /> },
}))

const mockSessionId = 'sess-redirect-pending'
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: { component: React.ComponentType; loader?: unknown }) => ({
      ...opts,
      useParams: () => ({ sessionId: mockSessionId }),
      useLoaderData: () => detail,
    }),
    useParams: () => ({ sessionId: mockSessionId }),
    useNavigate: () => edges.navigate,
  }
})
vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchWorkspaces: edges.fetchWorkspaces,
  fetchSessionDetail: edges.fetchSessionDetail,
}))

import { workspacesQueryKeys } from '@/lib/api'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { availableHistoryDetail } from '../../../tests/chat-history-paging/legacy-fixtures'
import { deferred } from '../../../tests/chat-history-paging/fixtures'
import { Route } from './sessions.$sessionId'

let detail: ReturnType<typeof sessionDetail>
let client: QueryClient
let lookup: ReturnType<typeof deferred<Workspace[]>>
const workspaceKey = workspacesQueryKeys.list({ status: 'active' })
function sessionDetail() {
  return {
    ...availableHistoryDetail({ id: mockSessionId, agent_id: 'mia', title: 'Chat in ws-1', type: 'chat', workspace_id: 'ws-1',
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', message_count: 3 }),
    agent_removed: false,
  }
}
function owningWorkspace(): Workspace {
  return {
    id: 'ws-1', name: 'Owning workspace', status: 'active', pinned: false, pin_order: 0,
    revision: '0'.repeat(64), task_count: 0, core_team: ['mia'],
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
  }
}
const SessionRoute = (Route as unknown as { component: React.ComponentType }).component
function openRoute() {
  return render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
}
function expectPendingLookup() {
  expect(client.getQueryState(workspaceKey)?.status, 'the REAL query is pending, not an assigned mock flag').toBe('pending')
  expect(client.getQueryState(workspaceKey)?.fetchStatus).toBe('fetching')
  expect(edges.fetchWorkspaces).toHaveBeenCalledExactlyOnceWith({ status: 'active' })
}
function expectWorkspaceRedirect() {
  expect(edges.navigate).toHaveBeenCalledExactlyOnceWith({
    to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-1' }, replace: true,
  })
  expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-1')
  expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toStrictEqual({
    id: mockSessionId, type: 'chat', title: 'Chat in ws-1', agentId: 'mia',
  })
  expect(screen.queryByTestId('inline-chat-screen')).toBeNull()
}

describe('legacy session deep link while its redirect is undecided', () => {
  beforeEach(() => {
    onlineManager.setOnline(true)
    client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } })
    lookup = deferred<Workspace[]>()
    edges.fetchWorkspaces.mockReset().mockImplementation(() => lookup.promise)
    edges.fetchSessionDetail.mockReset()
    edges.navigate.mockClear()
    edges.inline.mockClear()
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null, sessionByWorkspace: {} })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null, reconnectPhase: null, reconnectAttempt: 0 })
    detail = sessionDetail()
    edges.fetchSessionDetail.mockResolvedValue(detail)
  })
  afterEach(async () => {
    cleanup()
    await client.cancelQueries()
    client.clear()
    onlineManager.setOnline(true)
    vi.useRealTimers()
  })

  it('shows a visible opening status, not the inline thread (no history read), while the workspace list is loading', () => {
    openRoute()
    expectPendingLookup()
    expect(screen.queryByTestId('inline-chat-screen')).toBeNull()
    expect(screen.getByRole('status'), 'never an empty chat area').toHaveTextContent('Opening chat…')
    expect(edges.inline).not.toHaveBeenCalled()
    expect(edges.navigate).not.toHaveBeenCalled()
  })

  it('falls back to the inline thread when the workspace list stays pending past the bounded wait', () => {
    vi.useFakeTimers()
    openRoute()
    expectPendingLookup()
    expect(screen.queryByTestId('inline-chat-screen')).toBeNull()
    // Commissioned four-second boundary, NOT imported from the implementation.
    act(() => { vi.advanceTimersByTime(3_999) })
    expect(screen.queryByTestId('inline-chat-screen'), 'still within the bound').toBeNull()
    expect(screen.getByRole('status')).toHaveTextContent('Opening chat…')
    act(() => { vi.advanceTimersByTime(2) }) //4001ms: just OVER four seconds.
    expect(screen.getByTestId('inline-chat-screen'), 'a stalled list cannot leave the chat empty').toBeTruthy()
    expect(screen.queryByRole('status')).toBeNull()
    expectPendingLookup()
    expect(edges.navigate).not.toHaveBeenCalled()
  })

  it('opens the inline thread at once when the list request is paused offline', () => {
    onlineManager.setOnline(false)
    openRoute()
    expect(client.getQueryState(workspaceKey)?.status).toBe('pending')
    expect(client.getQueryState(workspaceKey)?.fetchStatus, 'real QueryClient pause, not an injected query-result object').toBe('paused')
    expect(screen.getByTestId('inline-chat-screen'), 'its connection notice and Retry stay reachable').toBeTruthy()
    expect(screen.queryByRole('status')).toBeNull()
    expect(edges.fetchWorkspaces).not.toHaveBeenCalled()
    expect(edges.navigate).not.toHaveBeenCalled()
  })

  it('mounts the inline thread once the list resolves without that workspace', async () => {
    openRoute()
    expectPendingLookup()
    expect(screen.queryByTestId('inline-chat-screen')).toBeNull()
    await act(async () => { lookup.resolve([]) })
    await waitFor(() => expect(client.getQueryState(workspaceKey)?.status).toBe('success'))
    await screen.findByTestId('inline-chat-screen')
    expect(screen.getByTestId('inline-chat-screen'), 'positive control: the inline fallback still renders').toBeTruthy()
    expect(client.getQueryState(workspaceKey)?.fetchStatus).toBe('idle')
    expect(screen.queryByRole('status')).toBeNull()
    expect(edges.navigate).not.toHaveBeenCalled()
    expect(edges.fetchWorkspaces).toHaveBeenCalledExactlyOnceWith({ status: 'active' })
  })

  it('opens inline immediately after a failed list lookup instead of rendering nothing', async () => {
    const failure = new Error('workspace lookup failed')
    openRoute()
    expectPendingLookup()
    expect(screen.queryByTestId('inline-chat-screen')).toBeNull()
    await act(async () => { lookup.reject(failure) })
    await waitFor(() => expect(client.getQueryState(workspaceKey)?.status).toBe('error'))
    expect(client.getQueryState(workspaceKey)?.error).toBe(failure)
    expect(client.getQueryState(workspaceKey)?.fetchStatus).toBe('idle')
    expect(await screen.findByTestId('inline-chat-screen'), 'a failed lookup cannot strand an empty chat area').toBeTruthy()
    expect(screen.queryByRole('status')).toBeNull()
    expect(edges.navigate).not.toHaveBeenCalled()
    expect(edges.fetchWorkspaces).toHaveBeenCalledExactlyOnceWith({ status: 'active' })
  })

  it('opens inline after four seconds still pending and applies the late workspace redirect when the real query settles', async () => {
    vi.useFakeTimers()
    openRoute()
    expectPendingLookup()
    act(() => { vi.advanceTimersByTime(3_999) })
    expect(screen.queryByTestId('inline-chat-screen')).toBeNull()
    act(() => { vi.advanceTimersByTime(2) })
    expect(screen.getByTestId('inline-chat-screen')).toBeTruthy()
    expectPendingLookup()
    expect(edges.navigate).not.toHaveBeenCalled()
    // Resolve the SAME in-flight fetch, then drain its zero-delay query
    // notification without advancing another deadline or forcing a rerender.
    await act(async () => {
      lookup.resolve([owningWorkspace()])
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(client.getQueryState(workspaceKey)?.status).toBe('success')
    expect(client.getQueryState(workspaceKey)?.fetchStatus).toBe('idle')
    expectWorkspaceRedirect()
    expect(edges.fetchWorkspaces).toHaveBeenCalledExactlyOnceWith({ status: 'active' })
  })

  it('redirects on a real pending-to-settled matching lookup before the bound without ever mounting inline', async () => {
    openRoute()
    expectPendingLookup()
    expect(edges.inline).not.toHaveBeenCalled()
    await act(async () => { lookup.resolve([owningWorkspace()]) })
    await waitFor(() => expect(edges.navigate).toHaveBeenCalledExactlyOnceWith({
      to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-1' }, replace: true,
    }))
    expect(client.getQueryState(workspaceKey)?.status).toBe('success')
    expectWorkspaceRedirect()
    expect(edges.inline, 'no mount means no competing inline history read').not.toHaveBeenCalled()
    expect(edges.fetchWorkspaces).toHaveBeenCalledExactlyOnceWith({ status: 'active' })
  })
})
