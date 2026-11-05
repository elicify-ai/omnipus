// Oracles: issue #1632 (automatic recovery after a gateway outage) and the
// dispatch's T1/T2/T3 controls. Keep the shell, router, query client, stores,
// API function and generated response validator real; double only raw HTTP
// and the clock. Browser internet connectivity never changes in this scenario.
import { Profiler } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import {
  createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider,
} from '@tanstack/react-router'
import { QueryClientProvider } from '@tanstack/react-query'
import type { Workspace } from '@/lib/api/generated/openapi-types'
import { ApiError } from '@/lib/api-error'
import { workspacesQueryKeys } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { useActiveWorkspace, WorkspaceTabContainer } from './WorkspaceTabContainer'

const WORKSPACE: Workspace = {
  revision: 'a'.repeat(64),
  id: 'ws-1632',
  name: 'Recovery workspace',
  status: 'active',
  pinned: false,
  pin_order: 0,
  task_count: 0,
  core_team: [],
  is_default: true,
  created_at: '2026-11-05T00:00:00Z',
  updated_at: '2026-11-05T00:00:00Z',
}
// Exact copy reported in issue #1632, not imported from the implementation.
const ERROR_TEXT = 'Failed to load workspace. Check your connection and try again.'
const ACTIVE_PATH = '/api/v1/workspaces?status=active'
const ACTIVE_KEY = workspacesQueryKeys.list({ status: 'active' })
// The reported outage duration is scenario input, not a wall-clock assertion.
const OUTAGE_MS = 20_000
const productionDefaults = queryClient.getDefaultOptions()
let responseMode: 'failure' | 'success' | 'held'
let requests: { method: string; path: string }[]
let unexpectedRequests: string[]
let heldResponses: ((response: Response) => void)[]

function workspaceResponse() {
  return new Response(JSON.stringify([WORKSPACE]), {
    status: 200, headers: { 'Content-Type': 'application/json' },
  })
}

// A test route consumer, not a mock of a production tab: the shell must
// actually resolve the workspace and publish its context before this mounts.
function WorkspaceContent() {
  const workspace = useActiveWorkspace()
  return (
    <main data-testid="workspace-reconnect-content">
      <h1>{workspace.name}</h1>
      <p>{workspace.id}</p>
    </main>
  )
}

async function mountWorkspace(onCommit = () => {}) {
  const root = createRootRoute({ component: Outlet })
  const workspace = createRoute({
    getParentRoute: () => root,
    path: '/workspaces/$workspaceId',
    component: () => <WorkspaceTabContainer workspaceId={WORKSPACE.id} />,
  })
  // Non-chat content avoids unrelated chat-runtime/provider setup. The list
  // query and blocking-error/loading branches belong to the common shell.
  const content = createRoute({ getParentRoute: () => workspace, path: 'settings', component: WorkspaceContent })
  const router = createRouter({
    routeTree: root.addChildren([workspace.addChildren([content])]),
    history: createMemoryHistory({ initialEntries: [`/workspaces/${WORKSPACE.id}/settings`] }),
  })
  await router.load()
  return render(
    <QueryClientProvider client={queryClient}>
      <Profiler id="workspace-reconnect" onRender={onCommit}>
        <RouterProvider router={router} />
      </Profiler>
    </QueryClientProvider>,
  )
}

async function advanceClock(ms: number) {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms) })
}

async function expectInitialFailure() {
  await mountWorkspace()
  expect(await screen.findByText(ERROR_TEXT, { exact: true })).toBeVisible()
  expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible()
  expect(requests).toEqual([{ method: 'GET', path: ACTIVE_PATH }])
  expect(queryClient.getQueryState(ACTIVE_KEY)).toMatchObject({ status: 'error', fetchStatus: 'idle' })
  const error = queryClient.getQueryState(ACTIVE_KEY)?.error
  expect(error).toBeInstanceOf(ApiError)
  expect(error).toMatchObject({ status: 0 })
  expect(screen.queryByTestId('workspace-top-bar')).toBeNull()
}

async function expectRecoveredWorkspace() {
  expect(await screen.findByRole('heading', { name: WORKSPACE.name })).toBeVisible()
  expect(screen.getByTestId('workspace-reconnect-content')).toHaveTextContent(WORKSPACE.id)
  expect(screen.getByTestId('workspace-top-bar')).toBeVisible()
  expect(screen.queryByText(ERROR_TEXT, { exact: true })).toBeNull()
  expect(screen.queryByTestId('workspace-container-error')).toBeNull()
  expect(queryClient.getQueryState(ACTIVE_KEY)).toMatchObject({ status: 'success', fetchStatus: 'idle' })
  expect(queryClient.getQueryData(ACTIVE_KEY)).toEqual([WORKSPACE])
}

beforeEach(() => {
  localStorage.clear()
  queryClient.clear()
  // Begin in the issue's exhausted-retry state after ONE failed HTTP attempt.
  // No reconnect, focus, stale-time or queryFn options are overridden. This
  // tests recovery, not the transport's independent retry/backoff policy.
  queryClient.setDefaultOptions({ ...productionDefaults, queries: { ...productionDefaults.queries, retry: false } })
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useWorkspacesStore.setState({ ...useWorkspacesStore.getInitialState(), activeWorkspaceId: WORKSPACE.id }, true)
  useUiStore.setState(useUiStore.getInitialState(), true)
  // An already-selected chat is normal workspace state. Its real entry action
  // takes the already-attached path, leaving unrelated session HTTP out of scope.
  useSessionStore.setState({
    ...useSessionStore.getInitialState(),
    activeSessionId: 'existing-session-1632',
    activeAgentId: 'mia',
    sessionByWorkspace: {
      [WORKSPACE.id]: { id: 'existing-session-1632', agentId: 'mia', type: 'main', title: null },
    },
  }, true)
  useConnectionStore.getState().recordDisconnect(null)
  responseMode = 'failure'
  requests = []
  unexpectedRequests = []
  heldResponses = []
  // The real router resets browser scroll; jsdom has no implementation.
  vi.stubGlobal('scrollTo', vi.fn())
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(async (input, init) => {
    const url = new URL(String(input), 'http://localhost')
    const method = (init?.method ?? 'GET').toUpperCase()
    const path = url.pathname + url.search
    if (method !== 'GET' || path !== ACTIVE_PATH) {
      unexpectedRequests.push(`${method} ${path}`)
      throw new Error(`Unexpected reconnect-test HTTP: ${method} ${path}`)
    }
    requests.push({ method, path })
    if (responseMode === 'failure') throw new TypeError('Failed to fetch')
    if (responseMode === 'held') return new Promise<Response>((resolve) => { heldResponses.push(resolve) })
    return workspaceResponse()
  }))
})

afterEach(async () => {
  cleanup()
  queryClient.clear()
  await act(async () => { heldResponses.splice(0).forEach((resolve) => resolve(workspaceResponse())) })
  vi.useRealTimers()
  vi.unstubAllGlobals()
  queryClient.setDefaultOptions(productionDefaults)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
  useUiStore.setState(useUiStore.getInitialState(), true)
  localStorage.clear()
  expect(unexpectedRequests, 'no unstubbed network or unrelated API success fallback').toEqual([])
})

describe('WorkspaceTabContainer — gateway reconnect (#1632)', () => {
  it('T1: refetches an errored workspace list on reconnect and replaces the error without a Retry click', async () => {
    await expectInitialFailure()
    vi.useFakeTimers()
    await advanceClock(OUTAGE_MS)
    expect(requests).toEqual([{ method: 'GET', path: ACTIVE_PATH }])
    vi.useRealTimers()

    responseMode = 'success'
    act(() => useConnectionStore.getState().setConnected(true))
    expect(useConnectionStore.getState().isConnected).toBe(true)
    // No click, remount, browser online event or query invalidation in the test.
    await waitFor(() => expect(requests, 'reconnect must issue one new active-workspaces GET').toEqual([
      { method: 'GET', path: ACTIVE_PATH },
      { method: 'GET', path: ACTIVE_PATH },
    ]))
    await expectRecoveredWorkspace()
  })

  it('T2a: stays quiet while disconnected and a manual Retry still recovers the workspace', async () => {
    await expectInitialFailure()
    vi.useFakeTimers()
    for (const phase of ['reconnecting', 'slow', 'gave_up'] as const) {
      act(() => {
        useConnectionStore.getState().recordDisconnect(null)
        useConnectionStore.getState().setReconnectState(phase, 1)
      })
      await advanceClock(OUTAGE_MS)
      expect(requests, `no refetch while disconnected (${phase})`).toEqual([{ method: 'GET', path: ACTIVE_PATH }])
    }
    responseMode = 'success'
    await advanceClock(OUTAGE_MS)
    expect(requests, 'HTTP availability alone is not a reconnect notification').toHaveLength(1)
    expect(screen.getByText(ERROR_TEXT, { exact: true })).toBeVisible()
    vi.useRealTimers()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await expectRecoveredWorkspace()
    expect(requests).toEqual([{ method: 'GET', path: ACTIVE_PATH }, { method: 'GET', path: ACTIVE_PATH }])
    expect(useConnectionStore.getState().isConnected).toBe(false)
  })

  it('T2b: an unsuccessful reconnect and duplicate connected notifications cannot cause a refetch storm', async () => {
    await expectInitialFailure()
    vi.useFakeTimers()
    for (const cycle of [1, 2]) {
      const beforeReconnect = requests.length
      act(() => useConnectionStore.getState().setConnected(true))
      await advanceClock(OUTAGE_MS)
      // Upper-bound control only: T1 independently requires a request. Retry
      // is disabled above so each recovery attempt is one raw HTTP request.
      expect(requests.length - beforeReconnect, `at most one attempt for reconnect ${cycle}`).toBeLessThanOrEqual(1)
      const afterReconnect = requests.length
      act(() => {
        useConnectionStore.getState().setConnected(true)
        useConnectionStore.getState().setReconnectState(null, 0)
      })
      await advanceClock(OUTAGE_MS)
      expect(requests, 'same connected state must not trigger another attempt').toHaveLength(afterReconnect)
      expect(screen.getByText(ERROR_TEXT, { exact: true })).toBeVisible()
      act(() => useConnectionStore.getState().recordDisconnect(null))
      await advanceClock(OUTAGE_MS)
      expect(requests, 'dropping the connection must not refetch').toHaveLength(afterReconnect)
    }
  })

  it('T3: reconnect never replaces an already-loaded workspace with a loading screen, even during a held refresh', async () => {
    const commits: { header: Element | null; content: Element | null }[] = []
    responseMode = 'held'
    await mountWorkspace(() => {
      commits.push({
        header: document.querySelector('[data-testid="workspace-top-bar"]'),
        content: document.querySelector('[data-testid="workspace-reconnect-content"]'),
      })
    })
    await waitFor(() => expect(heldResponses).toHaveLength(1))
    expect(queryClient.getQueryState(ACTIVE_KEY)).toMatchObject({ status: 'pending', fetchStatus: 'fetching' })
    // Instrument control: the Profiler really observed the initial unloaded
    // screen before it observed loaded content; it can detect a later flash.
    expect(commits.some(({ header, content }) => header === null && content === null)).toBe(true)
    await act(async () => { heldResponses.splice(0).forEach((resolve) => resolve(workspaceResponse())) })
    await expectRecoveredWorkspace()
    const header = screen.getByTestId('workspace-top-bar')
    const content = screen.getByTestId('workspace-reconnect-content')
    const firstReconnectCommit = commits.length
    vi.useFakeTimers()
    act(() => useConnectionStore.getState().setConnected(true))
    act(() => useConnectionStore.getState().recordDisconnect(null))
    act(() => useConnectionStore.getState().setConnected(true))
    await advanceClock(OUTAGE_MS)
    expect(screen.getByTestId('workspace-top-bar')).toBe(header)
    expect(screen.getByTestId('workspace-reconnect-content')).toBe(content)
    expect(screen.queryByText(ERROR_TEXT, { exact: true })).toBeNull()
    expect(commits.slice(firstReconnectCommit).every((commit) => commit.header === header && commit.content === content),
      'every reconnect render must retain the loaded shell, not just the final render').toBe(true)
    // An invisible background refresh is allowed. If one occurred, finish it
    // and assert that it also leaves the same workspace DOM mounted.
    await act(async () => { heldResponses.splice(0).forEach((resolve) => resolve(workspaceResponse())) })
    await advanceClock(OUTAGE_MS)
    expect(screen.getByTestId('workspace-reconnect-content')).toBe(content)
    expect(commits.slice(firstReconnectCommit).every((commit) => commit.header === header && commit.content === content)).toBe(true)
  })
})
