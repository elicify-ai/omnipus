// U3-C06. L1 amendment §1.2 R6 / §1.5, BDD-11.3 supersede workspace-only
// scope: "not this chat" points to root_session_id, never the acting helper.
// Legacy empty/name/count/navigation/resolution assertions retained, rerouted
// to root-chat scope. Real stores, QueryClient and router; fetch boundary only.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { createRootRoute, createRoute, createRouter, createMemoryHistory, RouterProvider, Outlet } from '@tanstack/react-router'
import type { ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
import type { Session, Workspace } from '@/lib/api/generated/openapi-types'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSessionStore, readPersistedSessionByWorkspace } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { CrossWorkspaceApprovalBanner } from './CrossWorkspaceApprovalBanner'
import { cleanupApprovalState, installNetwork, jsonResponse, resetSharedState, approvalPosts } from '../../../tests/l1-ui-red-u23/support'

function makeWorkspace(id: string, name: string): Workspace {
  return { id, name, revision: '0'.repeat(64), status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-09-14T00:00:00Z', updated_at: '2026-09-14T00:00:00Z' }
}
const workspaces = [makeWorkspace('ws-active', 'Home Base'), makeWorkspace('ws-other', 'UAT-T2')]
const sessions: Session[] = [
  { id: 'main-here', workspace_id: 'ws-active', agent_id: 'mia', type: 'main', title: 'Mia main' },
  { id: 'main-other', workspace_id: 'ws-other', agent_id: 'jim', type: 'main', title: 'Jim main' },
  { id: 'main-same-ws', workspace_id: 'ws-active', agent_id: 'jim', type: 'main', title: 'Jim main' },
  { id: 'no-workspace-main', agent_id: 'jim', type: 'main', title: 'Jim main' },
  { id: 'omnipus-main', agent_id: 'omnipus', type: 'main', title: 'Omnipus' },
].map(entry => ({
  ...entry, type: 'main', status: 'active', channel: 'webchat', partitions: [],
  created_at: '2026-10-14T09:00:00Z', updated_at: '2026-10-14T09:00:00Z',
  stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
}))
function requiredFrame(id: string, extra: Partial<ToolApprovalRequiredFrame> = {}): ToolApprovalRequiredFrame {
  return {
    type: 'tool_approval_required', approval_id: id, tool_call_id: `call-${id}`, tool_name: 'write_file', args: {},
    agent_id: 'jim', acting_agent_name: 'Jim', session_id: 'main-here', turn_id: 'turn-x', expires_in_ms: 600_000, ...extra,
  }
}
async function renderBanner() {
  const root = createRootRoute({ component: () => <QueryClientProvider client={queryClient}><CrossWorkspaceApprovalBanner /><textarea aria-label="Chat composer" /><Outlet /></QueryClientProvider> })
  const paths = ['/', '/omnipus', '/sessions/$id', '/workspaces/$workspaceId/chat'] as const
  const children = paths.map(path => createRoute({ getParentRoute: () => root, path, component: () => <div>Destination chat</div> }))
  const router = createRouter({ routeTree: root.addChildren(children), history: createMemoryHistory({ initialEntries: ['/'] }) })
  await act(async () => { await router.load(); render(<RouterProvider router={router} />) })
  return router
}
beforeEach(() => {
  resetSharedState('main-here')
  useWorkspacesStore.setState({ activeWorkspaceId: 'ws-active' })
  // The cache holds the documented SPA Session projection, not the REST
  // envelope; the network boundary below still returns generated wire rows.
  queryClient.setQueryData(['sessions'], sessions.map(session => ({
    ...session, message_count: session.stats.message_count,
    total_tokens: session.stats.tokens_total, total_cost: session.stats.cost,
  })))
  queryClient.setQueryData(['agents'], [makeAgent({ id: 'mia', name: 'Mia' }), makeAgent({ id: 'jim', name: 'Jim' }), makeAgent({ id: 'omnipus', name: 'Omnipus' })])
  queryClient.setQueryData(workspacesQueryKeys.list({ status: 'all' }), workspaces)
  installNetwork(call => {
    if (call.path === '/api/v1/workspaces') return jsonResponse(workspaces)
    if (call.path === '/api/v1/sessions') return jsonResponse({ sessions })
    if (call.path === '/api/v1/agents') return jsonResponse([makeAgent({ id: 'jim', name: 'Jim' })])
    throw new Error(`Unexpected ${call.method} ${call.path}`)
  })
})
afterEach(async () => {
  await cleanupApprovalState()
  useSessionStore.setState({ sessionByWorkspace: {} })
  localStorage.removeItem('omnipus.sessionByWorkspace.v1')
})
const enqueue = (frame: ToolApprovalRequiredFrame) => act(() => useToolApprovalStore.getState().enqueue(frame))

describe('CrossWorkspaceApprovalBanner — now waiting in another chat (amendment R6)', () => {
  it('L1 R6: renders nothing when the queue is empty', async () => {
    await renderBanner()
    expect(screen.queryByTestId('cross-workspace-approval-banner')).not.toBeInTheDocument()
  })

  it('L1 R6: renders nothing for the current root chat, regardless of its workspace', async () => {
    enqueue(requiredFrame('a1', { workspace_id: 'ws-other', session_id: 'helper-child', root_session_id: 'main-here' }))
    await renderBanner()
    expect(screen.queryByTestId('cross-workspace-approval-banner')).not.toBeInTheDocument()
    expect(useToolApprovalStore.getState().queue).toHaveLength(1)
  })

  it('L1 R6: renders nothing for an unscoped approval only when this IS its owning chat', async () => {
    enqueue(requiredFrame('a1', { workspace_id: undefined }))
    await renderBanner()
    expect(screen.queryByTestId('cross-workspace-approval-banner')).not.toBeInTheDocument()
  })

  it('L1 R6 / BDD-11.3: singular notice labels the target root chat, not a raw workspace id', async () => {
    enqueue(requiredFrame('a1', { workspace_id: 'ws-other', session_id: 'main-other' }))
    await renderBanner()
    const notice = screen.getByTestId('cross-workspace-approval-banner')
    expect(notice).toHaveTextContent(/1 approval waiting/i)
    expect(notice.textContent).toMatch(/Jim's chat in UAT-T2/)
    expect(notice).not.toHaveTextContent('ws-other')
  })

  it('L1 R6: plural notice counts several approvals in the same other root chat', async () => {
    enqueue(requiredFrame('a1', { workspace_id: 'ws-other', session_id: 'main-other' }))
    enqueue(requiredFrame('a2', { workspace_id: 'ws-other', session_id: 'main-other' }))
    await renderBanner()
    expect(screen.getByTestId('cross-workspace-approval-banner')).toHaveTextContent(/2 approvals waiting/i)
    expect(screen.getByTestId('cross-workspace-approval-banner').textContent).toMatch(/Jim's chat in UAT-T2/)
  })

  it('L1 R6: Go there opens the owning workspace chat and the exact root, never its helper', async () => {
    // CHECK2 B3-banner-root: a cached helper must not silently win the
    // resolver. The previous missing-helper fixture let fallback compensate.
    const conflicting = {
      ...sessions[1], id: 'helper-child', agent_id: 'helper', workspace_id: 'ws-active',
      type: 'delegate' as const, parent_session_id: 'main-other', title: 'Wrong helper destination',
    }
    queryClient.setQueryData(['sessions'], [...sessions, conflicting].map(session => ({
      ...session, message_count: session.stats.message_count,
      total_tokens: session.stats.tokens_total, total_cost: session.stats.cost,
    })))
    queryClient.setQueryData(['agents'], [makeAgent({ id: 'jim', name: 'Jim' }), makeAgent({ id: 'helper', name: 'Conflicting helper', type: 'Subagent' })])
    enqueue(requiredFrame('a1', { workspace_id: 'ws-other', session_id: 'helper-child', root_session_id: 'main-other' }))
    const router = await renderBanner()
    const notice = screen.getByTestId('cross-workspace-approval-banner')
    expect(notice).toHaveTextContent("Jim's chat in UAT-T2")
    expect(notice).not.toHaveTextContent('Home Base')
    expect(notice).not.toHaveTextContent('Conflicting helper')
    fireEvent.click(screen.getByTestId('cross-workspace-approval-banner'))
    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-other/chat'))
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-other')
    expect(useSessionStore.getState().activeSessionId).toBe('main-other')
    expect(approvalPosts()).toEqual([])
  })

  it('L1 R6: disappears on server-confirmed resolution of the elsewhere approval', async () => {
    enqueue(requiredFrame('a1', { workspace_id: 'ws-other', session_id: 'main-other' }))
    await renderBanner()
    expect(screen.getByTestId('cross-workspace-approval-banner')).toBeInTheDocument()
    act(() => useToolApprovalStore.getState().markResolved('a1'))
    expect(screen.queryByTestId('cross-workspace-approval-banner')).not.toBeInTheDocument()
  })

  it('L1 R6 / BDD-11.3: another chat in the SAME workspace is never invisible', async () => {
    enqueue(requiredFrame('a1', { workspace_id: 'ws-active', session_id: 'helper-child', root_session_id: 'main-same-ws' }))
    await renderBanner()
    expect(screen.getByTestId('cross-workspace-approval-banner')).toHaveTextContent(/1 approval waiting/i)
    expect(approvalPosts()).toEqual([])
  })

  it('L1 R6: no workspace_id does not hide an approval in another chat', async () => {
    enqueue(requiredFrame('a1', { session_id: 'no-workspace-main', workspace_id: undefined }))
    await renderBanner()
    expect(screen.getByTestId('cross-workspace-approval-banner')).toHaveTextContent(/1 approval waiting/i)
  })

  it('FR-092 / R6: Omnipus helper points to /omnipus without changing active workspace', async () => {
    // CHECK2 F4 / B3-banner-workspace: the docs promise that going back
    // returns to this workspace's OWN prior conversation. Seed the complete
    // real descriptor, not only activeWorkspaceId/activeSessionId.
    const remembered = { id: 'remembered-extra', type: 'chat' as const, title: 'Sales follow-up', agentId: 'mia' }
    const other = { id: 'other-remembered', type: 'chat' as const, title: 'Other conversation', agentId: 'jim' }
    const expected = { 'ws-active': remembered, 'ws-other': other }
    const storageKey = 'omnipus.sessionByWorkspace.v1'
    localStorage.removeItem(storageKey)
    act(() => {
      useConnectionStore.setState({ connection: null, isConnected: false })
      useSessionStore.setState({ sessionByWorkspace: {} })
      useSessionStore.getState().setWorkspaceSessionDescriptor('ws-active', remembered)
      useSessionStore.getState().setWorkspaceSessionDescriptor('ws-other', other)
    })
    expect(useSessionStore.getState().sessionByWorkspace).toEqual(expected)
    expect(readPersistedSessionByWorkspace()).toEqual(expected)
    const persistedBefore = localStorage.getItem(storageKey)
    expect(JSON.parse(persistedBefore!)).toEqual(expected)
    enqueue(requiredFrame('a1', { agent_id: 'helper', acting_agent_name: 'Setup helper', session_id: 'helper-child', root_session_id: 'omnipus-main', workspace_id: undefined }))
    const router = await renderBanner()
    expect(screen.getByTestId('cross-workspace-approval-banner')).toHaveTextContent('Omnipus')
    fireEvent.click(screen.getByTestId('cross-workspace-approval-banner'))
    await waitFor(() => expect(router.state.location.pathname).toBe('/omnipus'))
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-active')
    expect(useSessionStore.getState().activeSessionId).toBe('omnipus-main')
    expect(useSessionStore.getState().sessionByWorkspace).toEqual(expected)
    expect(readPersistedSessionByWorkspace()).toEqual(expected)
    expect(localStorage.getItem(storageKey)).toBe(persistedBefore)
    expect(approvalPosts()).toEqual([])
  })

  it('L1 R6: appearing notice keeps composer focus and resolution is never a banner action', async () => {
    await renderBanner()
    const composer = screen.getByLabelText('Chat composer')
    composer.focus()
    enqueue(requiredFrame('a1', { workspace_id: 'ws-other', session_id: 'main-other' }))
    expect(screen.getByTestId('cross-workspace-approval-banner')).toBeInTheDocument()
    expect(composer).toHaveFocus()
    fireEvent.keyDown(composer, { key: 'Enter', code: 'Enter' })
    expect(approvalPosts()).toEqual([])
    expect(useToolApprovalStore.getState().queue).toHaveLength(1)
  })
})
