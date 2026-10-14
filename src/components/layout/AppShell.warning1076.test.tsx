// #1076 Expected / CHECK F3: a real invalid-row warning reaches the user's
// actual application host, not only the formatter, console or an empty guard.
// REAL AppShell, ToastContainer (via its existing host), reporter, fetchAgents,
// generated validation, QueryClient and stores. No component/hook/helper mock.
// Only companion network APIs and raw HTTP/WebSocket process edges are doubled.
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClientProvider } from '@tanstack/react-query'
import { AppShell } from './AppShell'
import { queryClient } from '@/lib/queryClient'
import { useUiStore } from '@/store/ui'
import { useSidebarStore } from '@/store/sidebar'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useChatStore } from '@/store/chat'
import { useAuthStore } from '@/store/auth'
import { JIM, MIA, agent } from '../../../tests/omnipus-l1-ui-red/fixtures'

vi.mock('@/lib/api', async (original) => {
  const api = await original<typeof import('@/lib/api')>()
  return {
    ...api, // fetchAgents and its HTTP/validation/reporting path stay real.
    fetchAppState: vi.fn<typeof api.fetchAppState>().mockResolvedValue({ onboarding_complete: true,
      dev_mode_bypass: false, identity: { mode: 'local', edition: 'core', signed_in: true, blocked_reason: 'none' } }),
    fetchNotifications: vi.fn<typeof api.fetchNotifications>().mockResolvedValue({ notifications: [], unread_count: 0 }),
    fetchTasks: vi.fn<typeof api.fetchTasks>().mockResolvedValue([]),
    fetchWorkspaces: vi.fn<typeof api.fetchWorkspaces>().mockResolvedValue([]),
    fetchSessions: vi.fn<typeof api.fetchSessions>().mockResolvedValue([]),
    fetchProviders: vi.fn<typeof api.fetchProviders>().mockResolvedValue([]),
    fetchActivity: vi.fn<typeof api.fetchActivity>().mockResolvedValue({ events: [] }),
    fetchSkills: vi.fn<typeof api.fetchSkills>().mockResolvedValue([]),
    fetchPerformanceSettings: vi.fn<typeof api.fetchPerformanceSettings>().mockResolvedValue({}),
    fetchGodMode: vi.fn<typeof api.fetchGodMode>().mockResolvedValue({ enabled: false, persisted: false, supported: true, available: true }),
    fetchVersion: vi.fn<typeof api.fetchVersion>().mockResolvedValue({ version: '0.1.1', build_sha: 'warning-fixture' }),
  }
})

const INVALID_ID = 'broken-visible-app-shell-warning'
let rows: unknown[]
let unexpectedHttp: string[]
let unexpectedSocketSends: string[]

// Never opens a network connection. The real WsConnection may start its
// lifecycle in AppShell, but this process-edge transport stays unconnected.
class OfflineWebSocket extends EventTarget {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3
  readyState = OfflineWebSocket.CONNECTING
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  constructor(readonly url: string) { super() }
  send(body: string) {
    unexpectedSocketSends.push(body)
    throw new Error('The warning fixture never opens its transport')
  }
  close() { this.readyState = OfflineWebSocket.CLOSED }
}

beforeEach(() => {
  localStorage.clear()
  queryClient.clear()
  vi.clearAllMocks()
  rows = [MIA, agent(INVALID_ID, { name: 'n'.repeat(101) }), JIM]
  unexpectedHttp = []
  unexpectedSocketSends = []
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(async (input) => {
    if (String(input) === '/api/v1/agents') return new Response(JSON.stringify(rows), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    })
    unexpectedHttp.push(String(input))
    throw new Error(`Unspecified companion HTTP edge: ${String(input)}`)
  }))
  vi.stubGlobal('WebSocket', OfflineWebSocket)
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('min-width'), media: query, onchange: null,
    addListener: () => {}, removeListener: () => {}, addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  }))
  useUiStore.setState(useUiStore.getInitialState(), true)
  useSidebarStore.setState({ isOpen: false, isPinned: false })
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useChatStore.setState(useChatStore.getInitialState(), true)
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  useAuthStore.setState({ username: 'warning-test' })
})
afterEach(() => {
  cleanup()
  queryClient.clear()
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  expect(unexpectedHttp, 'all companion network edges must be explicit; no real HTTP').toEqual([])
  expect(unexpectedSocketSends, 'the raw socket fake never transmits').toEqual([])
})

function mountRealShell() {
  const root = createRootRoute({ component: AppShell })
  const page = createRoute({ getParentRoute: () => root, path: '/', component: () => <p>Warning delivery fixture</p> })
  const router = createRouter({ routeTree: root.addChildren([page]), history: createMemoryHistory({ initialEntries: ['/'] }) })
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>)
}

describe('#1076 delivered warning in the real application host', () => {
  it('shows a real visible invalid-row warning naming id/name while healthy agents remain cached', async () => {
    // Fresh fixture id is not used by any boundary case, avoiding page dedup.
    mountRealShell()
    expect(await screen.findByText('Warning delivery fixture')).toBeVisible()
    await waitFor(() => {
      // Characterization of the implementation's permitted skip strategy:
      // #1076 also allows a valid flagged row. Warning delivery below is the
      // independent product oracle; cache exactness is not a skip-only mandate.
      expect(queryClient.getQueryData(['agents'])).toEqual([MIA, JIM])
      const notices = screen.getAllByRole('status').filter((notice) => notice.textContent?.includes(INVALID_ID))
      expect(notices).toHaveLength(1)
      expect(notices[0]).toBeVisible()
      expect(notices[0]).toHaveTextContent(INVALID_ID)
      expect(notices[0]).toHaveTextContent(/\bname\b/)
      expect(notices[0]).toHaveTextContent(/invalid/i)
      expect(notices[0]).not.toHaveTextContent(/different version|please refresh/i)
    })
  })
  it('does not manufacture an invalid-agent warning for an entirely valid response', async () => {
    rows = [MIA, JIM]
    mountRealShell()
    expect(await screen.findByText('Warning delivery fixture')).toBeVisible()
    await waitFor(() => expect(queryClient.getQueryData(['agents'])).toEqual([MIA, JIM]))
    expect(useUiStore.getState().toasts.filter((toast) => /invalid.*agent|agent.*invalid/i.test(toast.message))).toEqual([])
    expect(screen.queryByText(/agent.*data.*invalid/i)).not.toBeInTheDocument()
  })
})
