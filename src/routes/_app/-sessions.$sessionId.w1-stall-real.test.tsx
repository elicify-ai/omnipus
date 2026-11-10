// W1 row: an unresolved saved-chat workspace lookup must become an explained
// stall after~4s, remain retryable, then adopt exactly once or show an error.
// REAL saved-session route/ChatScreen/router/query/API/stores; network and
// clock/offline signal only. No account or browser-UAT data in these fixtures.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { QueryClientProvider, onlineManager } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { queryClient } from '@/lib/queryClient'
import { AssistantRuntimeProvider } from '@assistant-ui/react'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { fetchSessionDetail } from '@/lib/api'
import { activeWorkspacesQueryOptions } from '@/lib/workspaceQueries'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { Route as SavedSessionRoute } from './sessions.$sessionId'
import { Stage1Network, resetStage1App, stopStage1App } from '../../../tests/l1-room-ui/stage1-harness'
import { json, workspace } from '../../../tests/l1-room-ui/harness'
import { availableDetail, deferred, page, session } from '../../../tests/chat-history-paging/fixtures'

const SID = 'w1-synthetic-saved-chat', WS = 'w1-synthetic-workspace'
const DEST = `/workspaces/${WS}/chat`
const target = workspace({ id: WS, name: 'W1 target workspace' })
const defaults = queryClient.getDefaultOptions()
let network: Stage1Network
let unsubscribe: (() => void) | undefined
let unsubscribeWorkspace: (() => void) | undefined
const lookups: { signal: AbortSignal | undefined; response: ReturnType<typeof deferred<Response>> }[] = []
const adopted: string[] = []
const workspaceMoves: string[] = []

beforeEach(() => {
  resetStage1App()
  vi.useFakeTimers()
  onlineManager.setOnline(true)
  queryClient.setDefaultOptions({ ...defaults, queries: { ...defaults.queries, retry: false, gcTime: Infinity } })
  useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null, sessionByWorkspace: {} })
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  lookups.length = 0; adopted.length = 0; workspaceMoves.length = 0
  unsubscribeWorkspace = useWorkspacesStore.subscribe((current, previous) => {
    if (current.activeWorkspaceId === WS && previous.activeWorkspaceId !== WS) workspaceMoves.push(WS)
  })
  network = new Stage1Network()
  const history = page([], { sessionId: SID })
  const detail = availableDetail(history, { ...session(SID), workspace_id: WS, title: 'W1 saved chat' })
  network.respond('GET', `/api/v1/sessions/${SID}`, () => json(detail))
  network.respond('GET', `/api/v1/sessions/${SID}/messages`, () => json(history))
  vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(input instanceof Request ? input.url : String(input), 'http://localhost')
    if (url.pathname === '/api/v1/workspaces' && url.searchParams.get('status') === 'active') {
      expect([...url.searchParams.entries()], 'the held real API request is the exact active-workspace lookup').toStrictEqual([['status', 'active']])
      const response = deferred<Response>(), signal = init?.signal ?? (input instanceof Request ? input.signal : undefined)
      signal?.addEventListener('abort', () => response.reject(new DOMException('aborted', 'AbortError')), { once: true })
      lookups.push({ signal: signal ?? undefined, response })
      return response.promise
    }
    // Every other process-edge request still passes through strict declared
    // Stage1 routes; no wildcard successful empty response masks a bug.
    return network.fetch(input, init)
  })
})
afterEach(async () => {
  unsubscribe?.(); unsubscribe = undefined
  unsubscribeWorkspace?.(); unsubscribeWorkspace = undefined
  cleanup()
  await queryClient.cancelQueries()
  queryClient.clear()
  queryClient.setDefaultOptions(defaults)
  onlineManager.setOnline(true)
  vi.useRealTimers()
  await stopStage1App()
  expect(network.unexpected, 'all non-workspace process-edge reads remain declared').toStrictEqual([])
})
function RuntimeRoot() {
  const runtime = useOmnipusRuntime()
  return <AssistantRuntimeProvider runtime={runtime}><Outlet /></AssistantRuntimeProvider>
}
async function mountSavedRoute() {
  const root = createRootRoute({ component: RuntimeRoot })
  const app = createRoute({ getParentRoute: () => root, id: '_app', component: Outlet })
  const saved = createRoute({ getParentRoute: () => app, path: '/sessions/$sessionId', component: SavedSessionRoute.options.component,
    // Test-owned loader supplies the component's real HTTP detail boundary;
    // auth/not-found file-loader behavior is explicitly outside W1's unit.
    loader: ({ params, abortController }) => fetchSessionDetail(params.sessionId, { signal: abortController.signal }),
  })
  // The real route's handoff is the unit boundary; only its destination is a
  // test-owned terminal route, not a mocked ChatScreen or navigation hook.
  const destination = createRoute({ getParentRoute: () => app, path: '/workspaces/$workspaceId/chat', component: () => <p>W1 workspace destination</p> })
  const history = createMemoryHistory({ initialEntries: [`/sessions/${SID}`] })
  let previousPath = history.location.pathname
  unsubscribe = history.subscribe(({ location }) => {
    // W1 counts a workspace MOVE, not a same-path address-bookkeeping write
    // (the real route also stamps this saved session into the open search key).
    if (location.pathname === DEST && previousPath !== DEST) adopted.push(location.pathname)
    previousPath = location.pathname
  })
  const router = createRouter({ routeTree: root.addChildren([app.addChildren([saved, destination])]), history, defaultPendingMinMs: 0 })
  await act(async () => {
    render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>)
    await router.load()
    await vi.advanceTimersByTimeAsync(1)
  })
  return router
}
async function advance(ms = 1) { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }
function expectRealInlineNotice(prefix: string) {
  const status = screen.getByText((content, element) => element?.getAttribute('role') === 'status' && content.startsWith(prefix))
  expect(status.textContent?.startsWith(prefix), 'W1 exact row-specified public copy, not silent loading').toBe(true)
  const slot = screen.getByTestId('workspace-lookup-chat-slot')
  expect(slot.querySelector('[data-testid="chat-input"]'), 'the REAL ChatScreen mounted, not a test stand-in').not.toBeNull()
  expect(slot.contains(status), 'notice cannot be an overlay child of the chat').toBe(false)
  expect(status.compareDocumentPosition(slot) & Node.DOCUMENT_POSITION_FOLLOWING, 'notice precedes the actual chat slot in document flow').not.toBe(0)
  expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
  expect(adopted, 'an unresolved target never navigates early').toStrictEqual([])
}

it('W1 pending lookup explains the stall and real Retry resolves the saved chat into its workspace exactly once', async () => {
  const router = await mountSavedRoute()
  expect(lookups, 'the actual first lookup started').toHaveLength(1)
  expect(lookups[0].signal).toBeInstanceOf(AbortSignal)
  expect(screen.queryByTestId('workspace-lookup-chat-slot'), 'bounded initial lookup still waits').toBeNull()
  await advance(4001) // From W1's~4s row, NOT the production timeout constant.
  expectRealInlineNotice("Still finding this chat's workspace")
  expect(queryClient.getQueryState(activeWorkspacesQueryOptions.queryKey)).toMatchObject({ status: 'pending', fetchStatus: 'fetching' })
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Retry' })); await vi.advanceTimersByTimeAsync(1) })
  expect(lookups, 'Retry starts the second real request').toHaveLength(2)
  expect(lookups[0].signal?.aborted).toBe(true)
  expect(lookups[1].signal?.aborted).toBe(false)
  await act(async () => { lookups[1].response.resolve(json([target])); await vi.advanceTimersByTimeAsync(1) })
  expect(router.state.location.pathname).toBe(DEST)
  expect(adopted).toStrictEqual([DEST])
  expect(workspaceMoves, 'one actual workspace adoption, not search-key bookkeeping').toStrictEqual([WS])
  expect(useWorkspacesStore.getState().activeWorkspaceId).toBe(WS)
  expect(useSessionStore.getState().sessionByWorkspace[WS]).toMatchObject({ id: SID, agentId: 'mia', type: 'chat' })
  expect(screen.queryByTestId('workspace-lookup-chat-slot')).toBeNull()
  expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
  await advance(60_000)
  expect(adopted, 'settled lookup cannot trigger a second route move').toStrictEqual([DEST])
})

it('W1 a failed held lookup replaces the stall with a visible workspace error and usable Retry', async () => {
  await mountSavedRoute()
  expect(lookups).toHaveLength(1)
  await advance(4001)
  expectRealInlineNotice("Still finding this chat's workspace")
  await act(async () => { lookups[0].response.resolve(json({ error: 'forbidden' }, 403)); await vi.advanceTimersByTimeAsync(1) })
  expect(queryClient.getQueryState(activeWorkspacesQueryOptions.queryKey)).toMatchObject({ status: 'error', fetchStatus: 'idle', error: { status: 403 } })
  const alert = screen.getByRole('alert')
  expect(alert).toBeVisible()
  expect(alert).toHaveTextContent(/workspace/iu)
  expect(screen.queryByText(/^Still finding this chat's workspace/u)).toBeNull()
  expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
  expect(screen.getByTestId('workspace-lookup-chat-slot').querySelector('[data-testid="chat-input"]')).not.toBeNull()
  expect(adopted).toStrictEqual([])
})

it('W1 paused offline lookup shows the connection stall and adopts once after the real query resumes', async () => {
  onlineManager.setOnline(false)
  const router = await mountSavedRoute()
  await advance(1)
  expect(lookups, 'a paused real query has not sent a workspace request').toHaveLength(0)
  expect(queryClient.getQueryState(activeWorkspacesQueryOptions.queryKey)).toMatchObject({ status: 'pending', fetchStatus: 'paused' })
  expectRealInlineNotice('Waiting for the connection')
  await act(async () => { onlineManager.setOnline(true); await vi.advanceTimersByTimeAsync(1) })
  expect(lookups).toHaveLength(1)
  await act(async () => { lookups[0].response.resolve(json([target])); await vi.advanceTimersByTimeAsync(1) })
  expect(router.state.location.pathname).toBe(DEST)
  expect(adopted).toStrictEqual([DEST])
  expect(workspaceMoves, 'one actual workspace adoption, not search-key bookkeeping').toStrictEqual([WS])
  expect(useWorkspacesStore.getState().activeWorkspaceId).toBe(WS)
  expect(screen.queryByTestId('workspace-lookup-chat-slot')).toBeNull()
  await advance(60_000)
  expect(adopted).toStrictEqual([DEST])
  expect(workspaceMoves, 'one actual workspace adoption, not search-key bookkeeping').toStrictEqual([WS])
})
