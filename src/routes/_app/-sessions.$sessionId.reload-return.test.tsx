// Oracle: #1437 Expected — a session link and reload of its resulting address
// must retain exactly that session and workspace, never a different chat.
// REAL: production session/workspace/chat routes, full chat UI/runtime,
// WorkspaceTabContainer, workspace-entry flow, stores, router and storage.
// Only HTTP, socket send and missing browser facilities are substitutes.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createHashHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import type { WsConnection } from '@/lib/ws'
import {
  agent, MAIN_SESSION, SESSION_A, SESSION_B, WORKSPACE_ID, session,
  installSessionReturnBrowser, installSessionReturnNetwork,
} from '../../../tests/session-return/fixtures'

async function loadPageModules() {
  // A real page reload also discards the dev-only hot-reload router global.
  // Without this, re-import is mistaken for an HMR patch of the old router.
  Reflect.deleteProperty(window, '__TSR_ROUTER__')
  const { useChatStore } = await import('@/store/chat')
  const { useSessionStore } = await import('@/store/session')
  const { useWorkspacesStore } = await import('@/store/workspacesStore')
  const { useConnectionStore } = await import('@/store/connection')
  const { Route: SessionRoute } = await import('./sessions.$sessionId')
  const { Route: WorkspaceRoute } = await import('./workspaces.$workspaceId')
  const { Route: ChatRoute } = await import('./workspaces.$workspaceId.chat')
  const { useOmnipusRuntime } = await import('@/lib/omnipus-runtime')
  const { AssistantRuntimeProvider } = await import('@assistant-ui/react')
  const api = await import('@/lib/api')
  // The app-level authentication/WebSocket lifecycle are outside this route
  // unit. Supply a real AssistantUI runtime, not a mocked chat component.
  function TestRoot() {
    const runtime = useOmnipusRuntime()
    return <AssistantRuntimeProvider runtime={runtime}><Outlet /></AssistantRuntimeProvider>
  }
  const root = createRootRoute({ component: TestRoot })
  const app = createRoute({ getParentRoute: () => root, id: '_app', component: Outlet })
  // Same file-route registration as routeTree.gen.ts, limited to this flow.
  const linked = SessionRoute.update({
    id: '/sessions/$sessionId', path: '/sessions/$sessionId', getParentRoute: () => app,
  } as Parameters<typeof SessionRoute.update>[0])
  const workspaceRoute = WorkspaceRoute.update({
    id: '/workspaces/$workspaceId', path: '/workspaces/$workspaceId', getParentRoute: () => app,
  } as Parameters<typeof WorkspaceRoute.update>[0])
  const chat = ChatRoute.update({
    id: '/chat', path: '/chat', getParentRoute: () => workspaceRoute,
  } as Parameters<typeof ChatRoute.update>[0])
  const routeTree = root.addChildren([app.addChildren([linked, workspaceRoute.addChildren([chat])])])
  return { useChatStore, useSessionStore, useWorkspacesStore, useConnectionStore, routeTree, api }
}

let page: Awaited<ReturnType<typeof loadPageModules>>
let unexpectedRequests: string[]
let restoreBrowser: () => void
const clients: QueryClient[] = []
const histories: ReturnType<typeof createHashHistory>[] = []
const originalUrl = window.location.href

beforeEach(async () => {
  localStorage.clear()
  sessionStorage.clear()
  vi.resetModules()
  restoreBrowser = installSessionReturnBrowser()
  unexpectedRequests = installSessionReturnNetwork()
  page = await loadPageModules()
})

afterEach(() => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  for (const history of histories.splice(0)) history.destroy()
  Reflect.deleteProperty(window, '__TSR_ROUTER__')
  window.history.replaceState(null, '', originalUrl)
  restoreBrowser()
  vi.unstubAllGlobals()
  expect(unexpectedRequests, 'all route HTTP calls must use declared contract fixtures').toEqual([])
  localStorage.clear()
  sessionStorage.clear()
})

async function mountAddress(address: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  clients.push(client)
  // Preload through the REAL API/validators so SessionRoute does not render
  // its unrelated inline/unfiled fallback while the workspace list loads.
  await client.fetchQuery({ queryKey: page.api.workspacesQueryKeys.list({ status: 'active' }), queryFn: () => page.api.fetchWorkspaces({ status: 'active' }) })
  await client.fetchQuery({ queryKey: ['agents'], queryFn: page.api.fetchAgents })
  await client.fetchQuery({ queryKey: ['providers'], queryFn: page.api.fetchProviders })
  const send = vi.fn(() => true)
  // Socket transport boundary only: production attach and selection run.
  page.useConnectionStore.setState({ connection: { send, close: vi.fn(), isConnected: true } as unknown as WsConnection, isConnected: true })
  window.history.replaceState(null, '', address)
  const history = createHashHistory()
  histories.push(history)
  const router = createRouter({ routeTree: page.routeTree, history })
  const mounted = render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  await waitFor(() => expect(router.state.status).toBe('idle'))
  await waitFor(() => expect(Object.keys(page.useSessionStore.getState().resolvingSessionForWorkspace)).toEqual([]))
  return { ...mounted, router, history, client, send }
}

function selectedTuple() {
  return {
    workspace: page.useWorkspacesStore.getState().activeWorkspaceId,
    session: page.useSessionStore.getState().activeSessionId,
    agent: page.useSessionStore.getState().activeAgentId,
  }
}

async function expectLinkedSession() {
  await waitFor(() => expect(selectedTuple()).toEqual({ workspace: WORKSPACE_ID, session: SESSION_A, agent: agent.id }))
  await waitFor(() => {
    expect(document.querySelector('[data-active-session-id]'), 'the real chat surface must render the linked session').toHaveAttribute('data-active-session-id', SESSION_A)
    expect(screen.getByTestId('workspace-name-button')).toHaveTextContent('Linked workspace')
  })
}

describe('#1437 — a session deep link retains its identity on reload', () => {
  it.each([
    { otherChat: 'the agent\'s main chat', id: MAIN_SESSION, type: 'main' as const },
    { otherChat: 'another extra chat', id: SESSION_B, type: 'chat' as const },
  ])('reloads the linked chat even if another tab remembered $otherChat', async ({ id, type }) => {
    const firstPage = await mountAddress(`#/sessions/${SESSION_A}`)
    await expectLinkedSession()
    expect(firstPage.send).toHaveBeenCalledWith(expect.objectContaining({ type: 'attach_session', session_id: SESSION_A }))
    // Save the address the REAL redirect produced, not a hand-built repaired
    // link. The issue is that this address can lose the original session id.
    const addressAtReload = window.location.href
    firstPage.unmount()
    firstPage.history.destroy()
    firstPage.client.clear()

    // Another tab may view a different chat in the same workspace. Use the
    // real storage writer, never hard-code or pre-seed its private key.
    const other = session(id, type)
    act(() => page.useSessionStore.getState().setWorkspaceSessionDescriptor(WORKSPACE_ID, {
      id: other.id, type: other.type, title: other.title, agentId: other.agent_id,
    }))

    const oldSessionStore = page.useSessionStore
    vi.resetModules()
    page = await loadPageModules()
    expect(page.useSessionStore, 'reload must discard the old in-memory session store').not.toBe(oldSessionStore)
    const reloaded = await mountAddress(addressAtReload)
    await waitFor(() => expect(reloaded.router.state.status).toBe('idle'))
    await expectLinkedSession()
    expect(reloaded.send).toHaveBeenCalledWith(expect.objectContaining({ type: 'attach_session', session_id: SESSION_A }))
    expect(reloaded.send.mock.calls.flat(), 'reload must never attach the conflicting remembered chat').not.toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: id }))
  })
})
