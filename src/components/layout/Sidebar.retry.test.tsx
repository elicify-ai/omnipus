import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { onlineManager, QueryClient, QueryClientProvider, QueryObserver } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { fetchWorkspaces, workspacesQueryKeys } from '@/lib/api'
import type { Workspace } from '@/lib/api/generated/openapi-types'
import { useSidebarStore } from '@/store/sidebar'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { Sidebar } from './Sidebar'

// Followups item 1: one initial failed active fetch, then exactly one new
// fetch per enabled workspace list when the user clicks Retry. Keep Sidebar,
// the key factory, QueryClient, router, stores and API/schema validation real;
// only the network and jsdom's missing matchMedia are replaced.
const activeWorkspace: Workspace = {
  revision: '0'.repeat(64),
  id: 'ws-recovered',
  name: 'Recovered workspace',
  status: 'active',
  pinned: false,
  pin_order: 0,
  task_count: 0,
  created_at: '2026-10-05T00:00:00Z',
  updated_at: '2026-10-05T00:00:00Z',
}
const archivedWorkspace: Workspace = {
  ...activeWorkspace,
  id: 'ws-archived',
  name: 'Previously archived workspace',
  status: 'archived',
}

const initialSidebar = useSidebarStore.getState()
const initialWorkspaces = useWorkspacesStore.getState()
const initialOnline = onlineManager.isOnline()
const queryClients: QueryClient[] = []

beforeEach(() => {
  onlineManager.setOnline(true)
  vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
    matches: query === '(min-width: 1024px)',
    media: query,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })))
  useSidebarStore.setState({ isOpen: true, isPinned: true })
  useWorkspacesStore.setState({ activeWorkspaceId: null })
})

afterEach(() => {
  cleanup()
  for (const client of queryClients.splice(0)) client.clear()
  onlineManager.setOnline(initialOnline)
  useSidebarStore.setState(initialSidebar, true)
  useWorkspacesStore.setState(initialWorkspaces, true)
  vi.unstubAllGlobals()
})

async function renderSidebar() {
  const queryClient = new QueryClient({
    // Disable automatic retries so only the visible Retry can make attempt 2.
    defaultOptions: { queries: { retry: false, gcTime: 0, refetchOnWindowFocus: false } },
  })
  queryClients.push(queryClient)
  const root = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <Sidebar />
      </QueryClientProvider>
    ),
  })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await act(async () => {
    await router.load()
    render(<RouterProvider router={router} />)
  })
  return queryClient
}

function stubWorkspaceRequests(activeResponses: Response[]) {
  const requests = { active: 0, archived: 0, unrelated: 0 }
  vi.stubGlobal('fetch', vi.fn<typeof fetch>(async (input) => {
    const url = new URL(String(input), 'http://localhost')
    if (url.pathname === '/api/v1/workspaces') {
      const status = url.searchParams.get('status')
      if (status === 'active') {
        requests.active += 1
        const response = activeResponses[requests.active - 1]
        if (!response) throw new Error(`Unexpected active workspace-list request: ${requests.active}`)
        return response
      }
      if (status === 'archived') {
        requests.archived += 1
        return Response.json([archivedWorkspace])
      }
      if (status === null) {
        requests.unrelated += 1
        return Response.json([activeWorkspace])
      }
      throw new Error(`Unexpected workspace-list status: ${status}`)
    }
    switch (url.pathname) {
      case '/api/v1/sessions': return Response.json({ sessions: [] })
      case '/api/v1/agents': return Response.json([])
      case '/api/v1/state': return Response.json({
        onboarding_complete: true,
        dev_mode_bypass: false,
        identity: { mode: 'local', edition: 'core', signed_in: true },
      })
      case '/api/v1/gateway/god-mode': return Response.json({
        enabled: false, persisted: false, available: true, supported: true,
      })
      default: throw new Error(`Unexpected request: ${url.pathname}${url.search}`)
    }
  }))
  return requests
}

describe('Sidebar — workspace-list Retry', () => {
  it.each([false, true])('refetches the failed active list and any expanded archive (archive open: %s)', async (archiveOpen) => {
    const workspaceRequests: string[] = []
    const activeRequests = () => workspaceRequests.filter((status) => status === 'active')
    const archivedRequests = () => workspaceRequests.filter((status) => status === 'archived')
    vi.stubGlobal('fetch', vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'http://localhost')
      if (url.pathname === '/api/v1/workspaces') {
        const status = url.searchParams.get('status')
        if (status !== 'active' && status !== 'archived') {
          throw new Error(`Unexpected workspace-list status: ${status}`)
        }
        workspaceRequests.push(status)
        if (status === 'active') {
          return activeRequests().length === 1
            ? Response.json({ error: 'Workspace list unavailable' }, { status: 503 })
            : Response.json([activeWorkspace])
        }
        return Response.json([
          archivedRequests().length === 1
            ? archivedWorkspace
            : { ...archivedWorkspace, name: 'Refreshed archived workspace' },
        ])
      }
      switch (url.pathname) {
        case '/api/v1/sessions': return Response.json({ sessions: [] })
        case '/api/v1/agents': return Response.json([])
        case '/api/v1/state': return Response.json({
          onboarding_complete: true,
          dev_mode_bypass: false,
          identity: { mode: 'local', edition: 'core', signed_in: true },
        })
        case '/api/v1/gateway/god-mode': return Response.json({
          enabled: false, persisted: false, available: true, supported: true,
        })
        default: throw new Error(`Unexpected request: ${url.pathname}${url.search}`)
      }
    }))

    await renderSidebar()
    expect(await screen.findByText('Could not load workspaces')).toBeVisible()
    const retry = screen.getByRole('button', { name: 'Retry loading workspaces' })
    expect(retry).toBeVisible()
    // Exact counts derive from the brief: one initial attempt, no auto-retry.
    expect(activeRequests()).toHaveLength(1)
    expect(archivedRequests()).toHaveLength(0)

    if (archiveOpen) {
      fireEvent.click(screen.getByRole('button', { name: 'Archive' }))
      expect(await screen.findByRole('button', { name: archivedWorkspace.name })).toBeVisible()
      expect(activeRequests()).toHaveLength(1)
      expect(archivedRequests()).toHaveLength(1)
    }

    fireEvent.click(retry)
    await waitFor(() => expect(activeRequests()).toHaveLength(2))
    await waitFor(() => expect(archivedRequests()).toHaveLength(archiveOpen ? 2 : 0))
    expect(await screen.findByRole('button', { name: activeWorkspace.name })).toBeVisible()
    expect(screen.queryByText('Could not load workspaces')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry loading workspaces' })).not.toBeInTheDocument()
    if (archiveOpen) {
      expect(await screen.findByRole('button', { name: 'Refreshed archived workspace' })).toBeVisible()
      expect(screen.queryByRole('button', { name: archivedWorkspace.name })).not.toBeInTheDocument()
    }
    // Final exact counts also guard against duplicate requests during recovery.
    expect(activeRequests()).toHaveLength(2)
    expect(archivedRequests()).toHaveLength(archiveOpen ? 2 : 0)
  })

  it('R2: does not refetch an unrelated actively observed workspace query on Retry', async () => {
    const requests = stubWorkspaceRequests([
      Response.json({ error: 'Workspace list unavailable' }, { status: 503 }),
      Response.json([activeWorkspace]),
    ])
    const queryClient = await renderSidebar()
    expect(await screen.findByText('Could not load workspaces')).toBeVisible()

    // Seed the Agents screen's separate key and keep it actively observed.
    // Its one mount fetch proves the network counter can see this query.
    queryClient.setQueryData<Workspace[]>(['workspaces'], [activeWorkspace])
    const observer = new QueryObserver(queryClient, {
      queryKey: ['workspaces'],
      queryFn: () => fetchWorkspaces(),
      staleTime: 0,
    })
    const unsubscribe = observer.subscribe(() => undefined)
    try {
      await waitFor(() => expect(observer.getCurrentResult()).toMatchObject({
        status: 'success', fetchStatus: 'idle',
      }))
      expect(requests).toEqual({ active: 1, archived: 0, unrelated: 1 })

      fireEvent.click(screen.getByRole('button', { name: 'Retry loading workspaces' }))
      expect(await screen.findByRole('button', { name: activeWorkspace.name })).toBeVisible()
      await waitFor(() => expect(queryClient.isFetching()).toBe(0))
      // R2: one explicit Retry adds only the active-list request; closed Archive
      // and the unrelated query must not make any additional requests.
      expect(requests).toEqual({ active: 2, archived: 0, unrelated: 1 })
      expect(observer.getCurrentResult().data).toEqual([activeWorkspace])
      expect(screen.queryByText('Could not load workspaces')).not.toBeInTheDocument()
    } finally {
      unsubscribe()
    }
  })

  it('R3: keeps the error and Retry after a second failure and recovers on request three', async () => {
    const requests = stubWorkspaceRequests([
      Response.json({ error: 'Workspace list unavailable' }, { status: 503 }),
      Response.json({ error: 'Workspace list still unavailable' }, { status: 503 }),
      Response.json([activeWorkspace]),
    ])
    const queryClient = await renderSidebar()
    expect(await screen.findByText('Could not load workspaces')).toBeVisible()
    expect(requests).toEqual({ active: 1, archived: 0, unrelated: 0 })

    fireEvent.click(screen.getByRole('button', { name: 'Retry loading workspaces' }))
    await waitFor(() => expect(requests.active).toBe(2))
    await waitFor(() => expect(queryClient.getQueryState(
      workspacesQueryKeys.list({ status: 'active' }),
    )).toMatchObject({ status: 'error', fetchStatus: 'idle' }))
    expect(await screen.findByText('Could not load workspaces')).toBeVisible()
    const retryAgain = screen.getByRole('button', { name: 'Retry loading workspaces' })
    expect(retryAgain).toBeVisible()
    expect(requests).toEqual({ active: 2, archived: 0, unrelated: 0 })

    fireEvent.click(retryAgain)
    expect(await screen.findByRole('button', { name: activeWorkspace.name })).toBeVisible()
    expect(screen.queryByText('Could not load workspaces')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry loading workspaces' })).not.toBeInTheDocument()
    // R3 specifies two failed attempts, then a successful third request.
    expect(requests).toEqual({ active: 3, archived: 0, unrelated: 0 })
  })

  it('R4: shows the offline notice instead of an empty list while Retry is paused, then recovers on reconnect', async () => {
    const requests = stubWorkspaceRequests([
      Response.json({ error: 'Workspace list unavailable' }, { status: 503 }),
      Response.json([activeWorkspace]),
    ])
    const queryClient = await renderSidebar()
    expect(await screen.findByText('Could not load workspaces')).toBeVisible()
    expect(requests).toEqual({ active: 1, archived: 0, unrelated: 0 })

    act(() => onlineManager.setOnline(false))
    fireEvent.click(screen.getByRole('button', { name: 'Retry loading workspaces' }))
    await waitFor(() => expect(queryClient.getQueryState(
      workspacesQueryKeys.list({ status: 'active' }),
    )?.fetchStatus).toBe('paused'))
    expect(screen.queryByText(/No workspaces yet/)).not.toBeInTheDocument()
    expect(await screen.findByText('Offline — workspaces will load when you reconnect.')).toBeVisible()
    // R4: offline Retry is paused, not a second network attempt.
    expect(requests).toEqual({ active: 1, archived: 0, unrelated: 0 })

    act(() => onlineManager.setOnline(true))
    expect(await screen.findByRole('button', { name: activeWorkspace.name })).toBeVisible()
    expect(screen.queryByText('Offline — workspaces will load when you reconnect.')).not.toBeInTheDocument()
    expect(screen.queryByText('Could not load workspaces')).not.toBeInTheDocument()
    expect(screen.queryByText(/No workspaces yet/)).not.toBeInTheDocument()
    expect(requests).toEqual({ active: 2, archived: 0, unrelated: 0 })
  })

  it('O1: shows the genuine empty list only after an offline mount reconnects', async () => {
    const requests = stubWorkspaceRequests([Response.json([])])
    onlineManager.setOnline(false)
    const queryClient = await renderSidebar()

    await waitFor(() => expect(queryClient.getQueryState(
      workspacesQueryKeys.list({ status: 'active' }),
    )).toMatchObject({ status: 'pending', fetchStatus: 'paused' }))
    expect(await screen.findByText('Offline — workspaces will load when you reconnect.')).toBeVisible()
    expect(screen.queryByText(/No workspaces yet/)).not.toBeInTheDocument()
    // O1: mounting offline makes no request until the connection returns.
    expect(requests).toEqual({ active: 0, archived: 0, unrelated: 0 })

    act(() => onlineManager.setOnline(true))
    await waitFor(() => expect(queryClient.getQueryState(
      workspacesQueryKeys.list({ status: 'active' }),
    )).toMatchObject({ status: 'success', fetchStatus: 'idle' }))
    expect(await screen.findByText(/No workspaces yet/)).toBeVisible()
    const emptyStateRow = screen.getByText(/No workspaces yet/).parentElement!
    expect(within(emptyStateRow).getByRole('button', { name: 'New workspace' })).toBeVisible()
    expect(screen.queryByText('Offline — workspaces will load when you reconnect.')).not.toBeInTheDocument()
    expect(screen.queryByText('Could not load workspaces')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry loading workspaces' })).not.toBeInTheDocument()
    // O1: the confirmed empty result comes from exactly one resumed request.
    expect(requests).toEqual({ active: 1, archived: 0, unrelated: 0 })
  })

  it('O2: restores the error and Retry when reconnect fails again, then recovers on request three', async () => {
    const requests = stubWorkspaceRequests([
      Response.json({ error: 'Workspace list unavailable' }, { status: 503 }),
      Response.json({ error: 'Workspace list still unavailable' }, { status: 503 }),
      Response.json([activeWorkspace]),
    ])
    const queryClient = await renderSidebar()
    expect(await screen.findByText('Could not load workspaces')).toBeVisible()
    const retry = screen.getByRole('button', { name: 'Retry loading workspaces' })
    expect(retry).toBeVisible()
    expect(requests).toEqual({ active: 1, archived: 0, unrelated: 0 })

    act(() => onlineManager.setOnline(false))
    fireEvent.click(retry)
    await waitFor(() => expect(queryClient.getQueryState(
      workspacesQueryKeys.list({ status: 'active' }),
    )?.fetchStatus).toBe('paused'))
    expect(await screen.findByText('Offline — workspaces will load when you reconnect.')).toBeVisible()
    // O2: the paused Retry must not send request two while offline.
    expect(requests).toEqual({ active: 1, archived: 0, unrelated: 0 })

    act(() => onlineManager.setOnline(true))
    await waitFor(() => expect(requests.active).toBe(2))
    await waitFor(() => expect(queryClient.getQueryState(
      workspacesQueryKeys.list({ status: 'active' }),
    )).toMatchObject({ status: 'error', fetchStatus: 'idle' }))
    expect(await screen.findByText('Could not load workspaces')).toBeVisible()
    const retryAgain = screen.getByRole('button', { name: 'Retry loading workspaces' })
    expect(retryAgain).toBeVisible()
    expect(screen.queryByText('Offline — workspaces will load when you reconnect.')).not.toBeInTheDocument()
    expect(requests).toEqual({ active: 2, archived: 0, unrelated: 0 })

    fireEvent.click(retryAgain)
    expect(await screen.findByRole('button', { name: activeWorkspace.name })).toBeVisible()
    await waitFor(() => expect(queryClient.getQueryState(
      workspacesQueryKeys.list({ status: 'active' }),
    )).toMatchObject({ status: 'success', fetchStatus: 'idle' }))
    expect(screen.queryByText('Could not load workspaces')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry loading workspaces' })).not.toBeInTheDocument()
    expect(screen.queryByText('Offline — workspaces will load when you reconnect.')).not.toBeInTheDocument()
    // O2: two failed responses and one explicit successful Retry total three.
    expect(requests).toEqual({ active: 3, archived: 0, unrelated: 0 })
  })
})
