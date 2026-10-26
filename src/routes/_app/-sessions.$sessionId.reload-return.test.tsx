// Oracle: #1437 Expected — a session link and reload of its resulting address
// must retain exactly that session and workspace, never a different chat.
// REAL: production session/workspace/chat routes, full chat UI/runtime,
// WorkspaceTabContainer, workspace-entry flow, stores, router and storage.
// Only HTTP, socket send and missing browser facilities are substitutes.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createHashHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import type { ClientFrame, WsConnection } from '@/lib/ws'
import type { Session, Workspace } from '@/lib/api/generated/openapi-types'
import {
  agent, MAIN_SESSION, SESSION_A, SESSION_B, WORKSPACE_ID, session, workspace,
  installSessionReturnBrowser, installSessionReturnNetwork,
} from '../../../tests/session-return/fixtures'

async function loadPageModules(includeSidebar = false) {
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
  const { Sidebar } = await import('@/components/layout/Sidebar')
  const { ToastContainer } = await import('@/components/ui/toast-container')
  const { useSidebarStore } = await import('@/store/sidebar')
  const api = await import('@/lib/api')
  // The app-level authentication/WebSocket lifecycle are outside this route
  // unit. Supply a real AssistantUI runtime, not a mocked chat component.
  function TestRoot() {
    const runtime = useOmnipusRuntime()
    return <AssistantRuntimeProvider runtime={runtime}>{includeSidebar && <Sidebar />}<Outlet /><ToastContainer /></AssistantRuntimeProvider>
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
  return { useChatStore, useSessionStore, useWorkspacesStore, useConnectionStore, useSidebarStore, routeTree, api }
}

let page: Awaited<ReturnType<typeof loadPageModules>>
let unexpectedRequests: string[]
let restoreBrowser: () => void
let originalElementScrollTo: PropertyDescriptor | undefined
const clients: QueryClient[] = []
const histories: ReturnType<typeof createHashHistory>[] = []
const originalUrl = window.location.href

beforeEach(async () => {
  localStorage.clear()
  sessionStorage.clear()
  vi.resetModules()
  restoreBrowser = installSessionReturnBrowser()
  // AssistantUI scrolls the thread element from requestAnimationFrame;
  // jsdom has no element scrollTo. Keep this shim local to this file.
  originalElementScrollTo = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollTo')
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', { configurable: true, writable: true, value: vi.fn() })
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
  // Unmount first so the viewport cancels its scheduled animation frame,
  // then restore the exact own descriptor (or the inherited/missing state).
  if (originalElementScrollTo) Object.defineProperty(HTMLElement.prototype, 'scrollTo', originalElementScrollTo)
  else Reflect.deleteProperty(HTMLElement.prototype, 'scrollTo')
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
  const send = vi.fn<(frame: ClientFrame) => boolean>(() => true)
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

const OTHER_WORKSPACE = 'session-return-other-workspace'
const OTHER_SESSION = 'session-return-other-chat'
const OTHER_MAIN = 'session-return-other-main'

function reviewScenario() {
  const first: Workspace = { ...workspace, member_configs: { [agent.id]: { main_session_id: MAIN_SESSION } } }
  const second: Workspace = {
    ...workspace, id: OTHER_WORKSPACE, name: 'Other workspace',
    member_configs: { [agent.id]: { main_session_id: OTHER_MAIN } },
  }
  const sessions: Session[] = [
    session(MAIN_SESSION, 'main'), session(SESSION_A), session(SESSION_B),
    { ...session(OTHER_MAIN, 'main'), workspace_id: OTHER_WORKSPACE },
    { ...session(OTHER_SESSION), workspace_id: OTHER_WORKSPACE },
  ]
  return { workspaces: [first, second], sessions }
}

function rememberChat(workspaceId: string, id: string) {
  page.useSessionStore.getState().setWorkspaceSessionDescriptor(workspaceId, {
    id, type: 'chat', title: `Conversation ${id}`, agentId: agent.id,
  })
}

async function expectChatInWorkspace(workspaceId: string, id: string) {
  await waitFor(() => {
    expect(selectedTuple(), 'the workspace, session and owner must be one committed destination').toEqual({ workspace: workspaceId, session: id, agent: agent.id })
    expect(document.querySelector('[data-active-session-id]'), 'the real chat surface must display that same destination').toHaveAttribute('data-active-session-id', id)
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


describe('#1437 review gaps — linked targets survive navigation and recovery', () => {
  it('R2 restores and attaches the destination workspace chat after a real sidebar switch drops open', async () => {
    unexpectedRequests = installSessionReturnNetwork(reviewScenario())
    page = await loadPageModules(true)
    act(() => rememberChat(OTHER_WORKSPACE, OTHER_SESSION))
    const mounted = await mountAddress(`#/sessions/${SESSION_A}`)
    await expectChatInWorkspace(WORKSPACE_ID, SESSION_A)
    await waitFor(() => expect(mounted.router.state.location.search.open).toBe(SESSION_A))
    const originalHeader = screen.getByTestId('workspace-top-bar')
    act(() => page.useSidebarStore.getState().open())
    fireEvent.click(await screen.findByRole('button', { name: 'Other workspace' }))
    await waitFor(() => {
      expect(mounted.router.state.location.pathname).toBe(`/workspaces/${OTHER_WORKSPACE}/chat`)
      expect(mounted.router.state.location.search.open).toBeUndefined()
      expect(screen.getByTestId('workspace-name-button')).toHaveTextContent('Other workspace')
    })
    expect(screen.getByTestId('workspace-top-bar'), 'the workspace shell must be reused across this navigation').toBe(originalHeader)
    await expectChatInWorkspace(OTHER_WORKSPACE, OTHER_SESSION)
    expect(mounted.send).toHaveBeenCalledWith(expect.objectContaining({ type: 'attach_session', session_id: OTHER_SESSION }))
    act(() => page.useChatStore.getState().sendMessage('Message for the destination workspace'))
    expect(mounted.send.mock.calls.map(([frame]) => frame).filter((frame) => frame.type === 'message').at(-1)).toMatchObject({
      type: 'message', session_id: OTHER_SESSION, agent_id: agent.id,
    })
  })

  it('R2 control keeps a deliberately new chat fresh when open disappears in the same workspace', async () => {
    unexpectedRequests = installSessionReturnNetwork(reviewScenario())
    const mounted = await mountAddress(`#/workspaces/${WORKSPACE_ID}/chat?open=${SESSION_A}`)
    await expectChatInWorkspace(WORKSPACE_ID, SESSION_A)
    const beforeNewChat = mounted.send.mock.calls.length
    act(() => {
      expect(page.useSessionStore.getState().startNewSession(agent.id)).toBe('started')
    })
    await waitFor(() => {
      expect(mounted.router.state.location.search.open).toBeUndefined()
      expect(page.useWorkspacesStore.getState().activeWorkspaceId).toBe(WORKSPACE_ID)
      expect(page.useSessionStore.getState().activeSessionId).toBeNull()
      expect(document.querySelector('[data-active-session-id]')).toHaveAttribute('data-active-session-id', '')
    })
    expect(mounted.send.mock.calls.slice(beforeNewChat).map(([frame]) => frame)).not.toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: SESSION_A }))
  })

  it('R3 adopts a different same-workspace open target instead of projecting the attached chat back into the address', async () => {
    unexpectedRequests = installSessionReturnNetwork(reviewScenario())
    act(() => rememberChat(WORKSPACE_ID, SESSION_B))
    const mounted = await mountAddress(`#/workspaces/${WORKSPACE_ID}/chat`)
    await expectChatInWorkspace(WORKSPACE_ID, SESSION_B)
    const beforeLink = mounted.send.mock.calls.length
    await act(async () => {
      await mounted.router.navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: WORKSPACE_ID }, search: { open: SESSION_A } })
    })
    await expectChatInWorkspace(WORKSPACE_ID, SESSION_A)
    expect(mounted.router.state.location.search.open).toBe(SESSION_A)
    expect(new URLSearchParams(window.location.hash.split('?')[1]).get('open')).toBe(SESSION_A)
    expect(mounted.send.mock.calls.slice(beforeLink).map(([frame]) => frame)).toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: SESSION_A }))
  })

  it.each(['notice', 'toast'] as const)('R5 retries the failed linked target rather than the remembered chat via %s', async (retrySurface) => {
    let recovered = false
    unexpectedRequests = installSessionReturnNetwork({ ...reviewScenario(), sessionListStatus: () => recovered ? 200 : 503 })
    act(() => rememberChat(WORKSPACE_ID, SESSION_B))
    const mounted = await mountAddress(`#/workspaces/${WORKSPACE_ID}/chat?open=${SESSION_A}`)
    await waitFor(() => expect(page.useSessionStore.getState().workspaceEntry?.status).toBe('failed-attempt'))
    const notice = await screen.findByTestId('unavailable-chat-notice')
    expect(notice).toBeVisible()
    recovered = true
    if (retrySurface === 'notice') {
      fireEvent.click(within(notice).getByRole('button', { name: 'Retry' }))
    } else {
      const warning = screen.getAllByRole('status').find((element) => element !== notice && element.textContent?.includes('Could not restore your last conversation'))
      if (!warning) throw new Error('The real failed-entry warning did not expose its Retry action')
      fireEvent.click(within(warning).getByRole('button', { name: 'Retry' }))
    }
    await expectChatInWorkspace(WORKSPACE_ID, SESSION_A)
    expect(mounted.router.state.location.search.open).toBe(SESSION_A)
    expect(mounted.send).toHaveBeenCalledWith(expect.objectContaining({ type: 'attach_session', session_id: SESSION_A }))
    expect(mounted.send.mock.calls.map(([frame]) => frame)).not.toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: SESSION_B }))
  })

  it('R6 explicitly explains a deleted linked chat instead of silently opening the available main', async () => {
    const scenario = reviewScenario()
    unexpectedRequests = installSessionReturnNetwork({
      ...scenario, sessions: scenario.sessions.filter((item) => item.id !== SESSION_A), deletedSessionIds: [SESSION_A],
    })
    const mounted = await mountAddress(`#/workspaces/${WORKSPACE_ID}/chat?open=${SESSION_A}`)
    await waitFor(() => expect(Object.keys(page.useSessionStore.getState().resolvingSessionForWorkspace)).toEqual([]))
    const notices = screen.queryAllByText(/(?:chat|conversation|session).*(?:no longer exists|not found|deleted|unavailable|does not exist)|(?:deleted|not found|unavailable).*(?:chat|conversation|session)/i)
    expect.soft(notices.length, 'a missing linked chat must be explained explicitly to the user').toBeGreaterThan(0)
    for (const notice of notices) expect.soft(notice).toBeVisible()
    expect(page.useSessionStore.getState().activeSessionId, 'main is not an automatic substitute for a deleted explicit link').not.toBe(MAIN_SESSION)
    expect(mounted.send.mock.calls.map(([frame]) => frame)).not.toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: MAIN_SESSION }))
  })
})


function heldSessionListResponse() {
  let release!: () => void
  const ready = new Promise<void>((resolve) => { release = resolve })
  return { ready, release }
}

describe('#1437 round 2 — overlapping explicit targets own their loading gate', () => {
  it.each(['A-first', 'B-first'] as const)('N2 only adopts B when linked A then B complete %s', async (order) => {
    const scenario = reviewScenario()
    const a = heldSessionListResponse()
    const b = heldSessionListResponse()
    const requested = new Set<string>()
    let holding = false
    unexpectedRequests = installSessionReturnNetwork({
      ...scenario,
      sessionListResponse: () => {
        const target = new URLSearchParams(window.location.hash.split('?')[1]).get('open')
        if (!holding || (target !== SESSION_A && target !== SESSION_B)) return Promise.resolve(Response.json({ sessions: scenario.sessions }))
        requested.add(target)
        // Each fetch gets a fresh Response; the shared latch controls only
        // network completion, never a production selection/entry function.
        return (target === SESSION_A ? a.ready : b.ready).then(() => Response.json({ sessions: scenario.sessions }))
      },
    })
    act(() => rememberChat(WORKSPACE_ID, MAIN_SESSION))
    const mounted = await mountAddress(`#/workspaces/${WORKSPACE_ID}/chat`)
    await expectChatInWorkspace(WORKSPACE_ID, MAIN_SESSION)
    const beforeIntents = mounted.send.mock.calls.length
    const { runWorkspaceEntry } = await import('@/store/session/workspaceEntryFlow')
    let entryA: Promise<void> | undefined
    let entryB: Promise<void> | undefined
    holding = true
    try {
      await act(async () => {
        await mounted.router.navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: WORKSPACE_ID }, search: { open: SESSION_A } })
      })
      await waitFor(() => expect(requested.has(SESSION_A), 'A must really be waiting at the HTTP boundary').toBe(true))
      // Retrieve the real same-target job while it is the current in-flight
      // request. This waits for completion below; it never mocks the flow.
      entryA = runWorkspaceEntry(WORKSPACE_ID, SESSION_A)
      await act(async () => {
        await mounted.router.navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: WORKSPACE_ID }, search: { open: SESSION_B } })
      })
      await waitFor(() => expect(requested, 'both explicit target requests must be in flight').toEqual(new Set([SESSION_A, SESSION_B])))
      entryB = runWorkspaceEntry(WORKSPACE_ID, SESSION_B)
      expect(page.useSessionStore.getState().resolvingSessionForWorkspace[WORKSPACE_ID]).toBe(true)
      const lateFrames = () => mounted.send.mock.calls.slice(beforeIntents).map(([frame]) => frame)
      if (order === 'A-first') {
        await act(async () => { a.release(); await entryA })
        expect(page.useSessionStore.getState().resolvingSessionForWorkspace[WORKSPACE_ID], "stale A cannot clear B's unresolved send gate").toBe(true)
        expect(lateFrames()).not.toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: SESSION_A }))
        act(() => page.useChatStore.getState().sendMessage('Must stay blocked until linked B resolves'))
        expect(lateFrames().filter((frame) => frame.type === 'message'), 'actual send remains blocked while B is unresolved').toEqual([])
        await act(async () => { b.release(); await entryB })
        await expectChatInWorkspace(WORKSPACE_ID, SESSION_B)
      } else {
        await act(async () => { b.release(); await entryB })
        await expectChatInWorkspace(WORKSPACE_ID, SESSION_B)
        await act(async () => { a.release(); await entryA })
      }
      await waitFor(() => expect(page.useSessionStore.getState().resolvingSessionForWorkspace[WORKSPACE_ID]).toBeUndefined())
      await expectChatInWorkspace(WORKSPACE_ID, SESSION_B)
      expect(lateFrames().filter((frame) => frame.type === 'attach_session').map((frame) => frame.session_id), 'only the latest requested chat may attach').toEqual([SESSION_B])
      expect(mounted.router.state.location.search.open).toBe(SESSION_B)
      act(() => page.useChatStore.getState().sendMessage('B is the only final destination'))
      expect(lateFrames().filter((frame) => frame.type === 'message').map((frame) => frame.session_id)).toEqual([SESSION_B])
    } finally {
      // Always release both network latches so a failed assertion cannot leave
      // a background request or workspace-entry gate alive for another test.
      await act(async () => { a.release(); b.release(); await Promise.all([a.ready, b.ready, entryA, entryB]) })
    }
  })
})

const CHILD_SESSION = 'session-return-delegate-child'

/** Contract (GET /sessions, ADR-057 FR-091/FR-104): the default listing pages
 *  over ROOT sessions only; a delegate child is listed only under its parent's
 *  parent_session_id filter or flat=true. The single-session detail still
 *  resolves it. The HTTP boundary honours that shape so the entry flow sees
 *  exactly what the gateway returns (train 3 hands-on row F4). */
function serveContractSessionList(all: Session[]) {
  const fixtureFetch = globalThis.fetch
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, 'http://localhost')
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    if (method !== 'GET' || url.pathname !== '/api/v1/sessions') return fixtureFetch(input, init)
    const parent = url.searchParams.get('parent_session_id')
    const flat = url.searchParams.get('flat') === 'true'
    const listed = all.filter((item) => flat || (parent ? item.parent_session_id === parent : !item.parent_session_id))
    return Response.json({ sessions: listed })
  }))
}

function delegateChild(workspaceId: string = WORKSPACE_ID): Session {
  return { ...session(CHILD_SESSION, 'delegate'), parent_session_id: MAIN_SESSION, title: 'Helper task', workspace_id: workspaceId }
}

describe('F4 — a delegated child chat link retains its identity on reload', () => {
  it('reopens the same child chat after reloading the address its link produced', async () => {
    const all = [...reviewScenario().sessions, delegateChild()]
    unexpectedRequests = installSessionReturnNetwork({ ...reviewScenario(), sessions: all })
    serveContractSessionList(all)
    const firstPage = await mountAddress(`#/sessions/${CHILD_SESSION}`)
    // Live control: opening the child from its link works before any reload.
    await expectChatInWorkspace(WORKSPACE_ID, CHILD_SESSION)
    await waitFor(() => expect(firstPage.router.state.location.search.open).toBe(CHILD_SESSION))
    const addressAtReload = window.location.href
    firstPage.unmount()
    firstPage.history.destroy()
    firstPage.client.clear()

    vi.resetModules()
    page = await loadPageModules()
    const reloaded = await mountAddress(addressAtReload)
    await waitFor(() => expect(reloaded.router.state.status).toBe('idle'))
    expect(screen.queryByTestId('unavailable-chat-notice'), 'an existing child chat is not an unavailable link').toBeNull()
    expect(page.useSessionStore.getState().workspaceEntry?.status).not.toBe('unavailable')
    await expectChatInWorkspace(WORKSPACE_ID, CHILD_SESSION)
    expect(reloaded.router.state.location.search.open).toBe(CHILD_SESSION)
    expect(reloaded.send).toHaveBeenCalledWith(expect.objectContaining({ type: 'attach_session', session_id: CHILD_SESSION }))
    expect(reloaded.send.mock.calls.map(([frame]) => frame), 'the parent main is not a substitute for its child').not.toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: MAIN_SESSION }))
  })

  it('keeps a helper link with Retry when its detail check fails, then opens it on Retry', async () => {
    const all = [...reviewScenario().sessions, delegateChild()]
    unexpectedRequests = installSessionReturnNetwork({ ...reviewScenario(), sessions: all })
    serveContractSessionList(all)
    // The helper's own detail check (GET /sessions/{id}) fails at the HTTP
    // boundary until recovered; every other request uses the contract fixture.
    let detailRecovered = false
    const listFetch = globalThis.fetch
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, 'http://localhost')
      if (!detailRecovered && url.pathname === `/api/v1/sessions/${CHILD_SESSION}`) {
        return Response.json({ message: 'Session store temporarily unavailable' }, { status: 503 })
      }
      return listFetch(input, init)
    }))
    act(() => rememberChat(WORKSPACE_ID, SESSION_B))
    const mounted = await mountAddress(`#/workspaces/${WORKSPACE_ID}/chat?open=${CHILD_SESSION}`)
    await waitFor(() => expect(page.useSessionStore.getState().workspaceEntry?.status).toBe('failed-attempt'))
    const notice = await screen.findByTestId('unavailable-chat-notice')
    expect(notice).toHaveTextContent('Could not restore your last conversation. Retry to try again.')
    expect(screen.queryByText(/This linked conversation is unavailable/), 'a failed check is not proof the helper is gone').toBeNull()
    expect(page.useSessionStore.getState().workspaceEntry).toMatchObject({ linkedSessionId: CHILD_SESSION, attempted: { sessionId: CHILD_SESSION } })
    expect(mounted.router.state.location.search.open, 'the link keeps its target').toBe(CHILD_SESSION)
    const frames = () => mounted.send.mock.calls.map(([frame]) => frame)
    expect(frames(), 'the main chat is not a fallback').not.toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: MAIN_SESSION }))
    expect(frames(), 'the remembered chat is not a fallback').not.toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: SESSION_B }))

    detailRecovered = true
    fireEvent.click(within(notice).getByRole('button', { name: 'Retry' }))
    await expectChatInWorkspace(WORKSPACE_ID, CHILD_SESSION)
    expect(mounted.router.state.location.search.open).toBe(CHILD_SESSION)
    expect(mounted.send).toHaveBeenCalledWith(expect.objectContaining({ type: 'attach_session', session_id: CHILD_SESSION }))
    expect(frames()).not.toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: MAIN_SESSION }))
  })

  it('still reports a child chat from another workspace as unavailable', async () => {
    const all = [...reviewScenario().sessions, delegateChild(OTHER_WORKSPACE)]
    unexpectedRequests = installSessionReturnNetwork({ ...reviewScenario(), sessions: all })
    serveContractSessionList(all)
    const mounted = await mountAddress(`#/workspaces/${WORKSPACE_ID}/chat?open=${CHILD_SESSION}`)
    await waitFor(() => expect(page.useSessionStore.getState().workspaceEntry?.status).toBe('unavailable'))
    expect(await screen.findByTestId('unavailable-chat-notice')).toHaveTextContent('This linked conversation is unavailable')
    expect(page.useSessionStore.getState().activeSessionId).not.toBe(CHILD_SESSION)
    expect(mounted.send.mock.calls.map(([frame]) => frame)).not.toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: CHILD_SESSION }))
    expect(mounted.send.mock.calls.map(([frame]) => frame)).not.toContainEqual(expect.objectContaining({ type: 'attach_session', session_id: MAIN_SESSION }))
  })
})
