import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
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
const queryClients: QueryClient[] = []

beforeEach(() => {
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
})
