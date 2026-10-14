// WU-10 RED: FR-100/101/109/112; BDD-12.1/2/14, 10.2/3/6; ADR OSA-10 U1/U2.
// Real Sidebar/controls/stores. Navigation and network/socket transport are boundaries.
import type { ReactNode } from 'react'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Sidebar } from './Sidebar'
import { WsConnection } from '@/lib/ws'
import { useSidebarStore } from '@/store/sidebar'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { DEFAULT, MIA, OMNIPUS, OMNIPUS_MAIN, SALES, SALES_MAIN, session } from '../../../tests/omnipus-l1-ui-red/fixtures'

const navigate = vi.hoisted(() => vi.fn())
vi.mock('@tanstack/react-router', async (original) => ({
  ...await original<typeof import('@tanstack/react-router')>(),
  useNavigate: () => navigate,
  useLocation: () => ({ pathname: '/workspaces/sales/chat', search: {} }),
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))
vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchWorkspaces: vi.fn(), fetchAgents: vi.fn(), fetchSessions: vi.fn(),
  fetchAppState: vi.fn(), fetchGodMode: vi.fn(),
}))
import { fetchWorkspaces, fetchAgents, fetchSessions, fetchAppState, fetchGodMode } from '@/lib/api'
let client: QueryClient
let socket: WsConnection
let send = vi.fn<WsConnection['send']>()

beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: !query.includes('reduced-motion'), media: query, onchange: null,
    addListener: () => {}, removeListener: () => {}, addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  }))
  Object.defineProperty(document, 'hidden', { configurable: true, value: false })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.mocked(fetchWorkspaces).mockResolvedValue([DEFAULT, SALES])
  vi.mocked(fetchAgents).mockResolvedValue([OMNIPUS, MIA])
  vi.mocked(fetchSessions).mockResolvedValue([
    session(OMNIPUS_MAIN, 'omnipus', 'default', { type: 'main', needs_attention: false }),
    session(SALES_MAIN, 'mia', 'sales', { type: 'main', needs_attention: false }),
  ])
  vi.mocked(fetchAppState).mockResolvedValue({ onboarding_complete: true, dev_mode_bypass: false,
    identity: { mode: 'platform', edition: 'hosted', signed_in: true } })
  vi.mocked(fetchGodMode).mockResolvedValue({ enabled: false, available: true, supported: true, persisted: false })
  socket = new WsConnection({ onConnected: () => {}, onDisconnected: () => {}, onError: () => {} })
  send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  vi.spyOn(socket, 'send').mockImplementation(send)
  useConnectionStore.setState({ connection: socket, isConnected: true, connectionError: null })
  useSidebarStore.setState({ isOpen: true, isPinned: true })
  useWorkspacesStore.setState({ activeWorkspaceId: 'sales' })
  useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: SALES_MAIN, activeAgentId: 'mia', sessionByWorkspace: {
    sales: { id: SALES_MAIN, type: 'main', title: 'Mia main', agentId: 'mia' },
  } })
  useUiStore.setState({ activePanel: null, toasts: [] })
})
afterEach(() => {
  client.clear()
  socket.disconnect()
  useConnectionStore.setState({ connection: null, isConnected: false })
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})
function renderSidebar() {
  render(<QueryClientProvider client={client}><Sidebar /></QueryClientProvider>)
}
async function entry() {
  await waitFor(() => expect(client.isFetching()).toBe(0))
  return screen.getByRole('button', { name: /^omnipus(?:\s|$)/i })
}
function expectAttachedWithoutWorkspaceSwitch() {
  expect(send.mock.calls.map(([frame]) => frame)).toEqual([{ type: 'attach_session', session_id: OMNIPUS_MAIN }])
  expect(useSessionStore.getState().activeSessionId).toBe(OMNIPUS_MAIN)
  expect(useSessionStore.getState().activeAgentId).toBe('omnipus')
  expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('sales')
  expect(useSessionStore.getState().sessionByWorkspace.sales).toEqual({ id: SALES_MAIN, type: 'main', title: 'Mia main', agentId: 'mia' })
  expect(navigate).toHaveBeenCalledWith({ to: '/omnipus' })
}

describe('WU-10 fixed Omnipus entry', () => {
  it('FR-100 is a single fixed entry before Workspaces, never a duplicate team row', async () => {
    renderSidebar()
    const button = await entry()
    // FR-004 / CHECK F2: collapsed Default made the negative row oracle blind.
    // Open the actual disclosure; ordinary Mia proves its rows really render.
    await userEvent.setup().click(screen.getByRole('button', { name: 'Show Default agents' }))
    expect(screen.getByRole('button', { name: 'Hide Default agents' })).toHaveAttribute('aria-expanded', 'true')
    const defaultRows = screen.getByRole('group', { name: 'Default' })
    expect(await within(defaultRows).findByRole('button', { name: /^mia$/i })).toBeVisible()
    expect(within(defaultRows).queryByRole('button', { name: /^omnipus(?:\s|$)/i })).not.toBeInTheDocument()
    expect(within(defaultRows).queryByRole('group', { name: /^omnipus$/i })).not.toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /^omnipus(?:\s|$)/i })).toHaveLength(1)
    const heading = screen.getByText(/^workspaces$/i)
    expect(button.compareDocumentPosition(heading) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
  })
  it('BDD-12.1 opens the singleton by click and preserves the Sales remembered chat', async () => {
    renderSidebar()
    await userEvent.setup().click(await entry())
    expectAttachedWithoutWorkspaceSwitch()
  })
  it('BDD-12.14 opens from Sales by keyboard alone', async () => {
    renderSidebar()
    const button = await entry()
    button.focus()
    await userEvent.setup().keyboard('{Enter}')
    expectAttachedWithoutWorkspaceSwitch()
  })
  it('BDD-12.2 unresolved main shows Omnipus unavailable plus Retry and sends no guessed attach', async () => {
    vi.mocked(fetchWorkspaces).mockResolvedValue([{ ...DEFAULT, member_configs: { mia: { main_session_id: 'opaque-default-mia-main' } } }, SALES])
    renderSidebar()
    await userEvent.setup().click(await entry())
    expect(screen.getByText(/omnipus unavailable/i)).toBeVisible()
    expect(screen.getByRole('button', { name: /^retry$/i })).toBeEnabled()
    expect(send.mock.calls).toEqual([])
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('sales')
  })
  // FR-100's VISIBLE disabled composer is a mandatory browser/UAT follow-up;
  // this component test pins only attach failure, Retry and tuple preservation.
  it('BDD-12.2 a failed attach retains the prior destination and offers Retry', async () => {
    send.mockReturnValue(false)
    renderSidebar()
    await userEvent.setup().click(await entry())
    expect(screen.getByRole('button', { name: /^retry$/i })).toBeEnabled()
    expect(useSessionStore.getState().activeSessionId).toBe(SALES_MAIN)
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('sales')
  })
  it('BDD-10.2 pending attention on the Omnipus main is announced as needs attention', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([session(OMNIPUS_MAIN, 'omnipus', 'default', { type: 'main', needs_attention: true })])
    renderSidebar()
    expect(await entry()).toHaveAccessibleName(/needs attention/i)
  })
  it('BDD-10.3 a plain message with false attention does not fabricate a pending-decision cue', async () => {
    renderSidebar()
    const button = await entry()
    expect(button).not.toHaveAccessibleName(/needs attention|unknown/i)
  })
  it('BDD-10.6 a failed session list marks attention Unknown instead of nothing pending', async () => {
    vi.mocked(fetchSessions).mockRejectedValue(new Error('Attention unavailable'))
    renderSidebar()
    expect(await entry()).toHaveAccessibleName(/unknown/i)
  })
  it('FR-112 reconnecting retains cached chat but labels attention stale, not freshly known', async () => {
    renderSidebar()
    await entry()
    vi.mocked(fetchSessions).mockRejectedValue(new Error('Attention refresh offline'))
    await act(async () => { await client.invalidateQueries({ queryKey: ['sessions'] }) })
    // FR-112 is a visible-state oracle, not a same-tick observer-delivery oracle.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^omnipus(?:\s|$)/i })).toHaveAccessibleName(/stale|out of date|last known/i)
    })
  })
})
