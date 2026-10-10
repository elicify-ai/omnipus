/**
 * Sidebar agent rows. Oracles: FR-001, FR-003, FR-005, FR-006, FR-014,
 * BDD-01.1, BDD-03.2, BDD-03.3, BDD-04.3, BDD-04.4, BDD-12.2, datasets
 * N01, N10, A09, A10. T-11 expand and workspace-entry refresh is included
 * because those gestures live on this shell (FR-010).
 *
 * Attention cues are observed without computed CSS (Tailwind does not apply
 * in this runner): the collapsed 8px dot sets data-cue-px="8", motion sets
 * data-attention-motion to "loop" or "static", and an expanded attention
 * icon sets data-attention-halo="warning". There is no 8px dot inside an
 * agent row.
 */
import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent, within, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Agent, Session, Workspace, WorkspaceMemberConfig } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import { useSidebarStore } from '@/store/sidebar'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSessionStore } from '@/store/session'

let reducedMotion = false

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: query.includes('prefers-reduced-motion') ? reducedMotion : true,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }),
})

if (typeof HTMLElement !== 'undefined') {
  HTMLElement.prototype.hasPointerCapture = () => false
  HTMLElement.prototype.scrollIntoView = () => {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => mockNavigate,
  Link: ({ children, to }: { children: React.ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    logout: vi.fn().mockResolvedValue(undefined),
    fetchSessions: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionPage: vi.fn().mockResolvedValue({ sessions: [] }),
    fetchAppState: vi.fn().mockResolvedValue({
      onboarding_complete: true,
      dev_mode_bypass: false,
      identity: { mode: 'platform', edition: 'hosted', signed_in: true },
    }),
    fetchGodMode: vi.fn().mockResolvedValue({ enabled: false, available: true, supported: true, persisted: false }),
  }
})

const { mockSelectSession } = vi.hoisted(() => ({ mockSelectSession: vi.fn() }))

vi.mock('@/components/chat/useSelectSession', () => ({
  useSelectSession: () => mockSelectSession,
}))

vi.mock('@/store/auth', () => {
  const state = { clearAuth: vi.fn(), username: 'testuser', token: null, role: null }
  const useAuthStore = (selector?: (value: typeof state) => unknown) => (selector ? selector(state) : state)
  useAuthStore.getState = () => state
  return { useAuthStore }
})

vi.mock('@/store/notifications', () => ({
  useNotificationsStore: (selector?: (value: { unreadCount: number }) => unknown) => {
    const state = { unreadCount: 0 }
    return selector ? selector(state) : state
  },
}))

vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuSeparator: () => <hr />,
}))

vi.mock('@/components/workspaces/NewWorkspaceSlideOver', () => ({ NewWorkspaceSlideOver: () => null }))

vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className }: React.HTMLAttributes<HTMLElement>) => <aside className={className}>{children}</aside>,
    div: ({ children, className }: React.HTMLAttributes<HTMLDivElement>) => <div className={className}>{children}</div>,
  },
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

const seam = vi.hoisted(() => {
  const mains = new Map<object, string | undefined>()
  const attention = new Map<string, 'on' | 'off' | 'unknown'>()
  return {
    mains,
    attention,
    mainSessionIdOfMember: (member: object) => mains.get(member),
    isMainSession: (session: unknown) => {
      const id = session && typeof session === 'object' && 'id' in session ? String((session as { id: unknown }).id) : ''
      return id === 'seam-opaque-mia' || id === 'seam-opaque-jim' || id === 'seam-opaque-admin'
    },
    sessionAttention: (session: unknown) => {
      const id = session && typeof session === 'object' && 'id' in session ? String((session as { id: unknown }).id) : ''
      return attention.get(id) ?? 'unknown'
    },
    attachAckFields: () => ({}),
    // Preserve the seam forwarding signature without inventing a read bound.
    attentionBoundOfFrame: (_frame: unknown) => { void _frame; return undefined },
  }
})

vi.mock('@/lib/nav/sessionCoreSeam', async (importOriginal) => ({
  // Real seam for everything the stubs don't override (notably
  // adminMainSessionIdOfWorkspace: the Admin-main rule stays real).
  ...await importOriginal<typeof import('@/lib/nav/sessionCoreSeam')>(),
  mainSessionIdOfMember: (member: object) => seam.mainSessionIdOfMember(member),
  isMainSession: (session: unknown) => seam.isMainSession(session),
  sessionAttention: (session: unknown) => seam.sessionAttention(session),
  attachAckFields: () => seam.attachAckFields(),
  attentionBoundOfFrame: (frame: unknown) => seam.attentionBoundOfFrame(frame),
}))

import { fetchWorkspaces, fetchSessions, fetchAgents } from '@/lib/api'
import { Sidebar } from './Sidebar'

const miaMember: WorkspaceMemberConfig = {}
const jimMember: WorkspaceMemberConfig = {}
const adminMember: WorkspaceMemberConfig = {}

function workspace(partial: Pick<Workspace, 'id' | 'name' | 'is_default'> & Partial<Workspace>): Workspace {
  return {
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    revision: '0'.repeat(64),
    ...partial,
  }
}

const product = workspace({
  id: 'product-launch',
  name: 'Product launch',
  is_default: false,
  core_team: ['mia', 'jim'],
  member_configs: { mia: miaMember, jim: jimMember },
})

const home = workspace({
  id: 'default',
  name: 'Default',
  is_default: true,
  // ARCHITECT-ANSWER-ADMIN-MAIN-BOUND Q1: Admin's main id rides the default
  // workspace itself (admin_main_session_id), never member_configs['admin'] —
  // which is why the admin entry below carries no main and the row still shows.
  admin_main_session_id: 'seam-opaque-admin',
  core_team: ['mia'],
  member_configs: { mia: miaMember, admin: adminMember },
})

function chatSession(partial: Pick<Session, 'id' | 'agent_id' | 'title' | 'updated_at' | 'workspace_id'>): Session {
  return {
    type: 'chat',
    created_at: '2026-01-01T00:00:00Z',
    message_count: 1,
    ...partial,
  }
}

function renderSidebar(client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return render(
    <QueryClientProvider client={client}>
      <Sidebar />
    </QueryClientProvider>,
  )
}

async function expandAgents(workspaceName: string) {
  // The active workspace arrives expanded with the async list. Wait for
  // either real disclosure state, then expand only when it is folded.
  await waitFor(() => {
    const disclosure = screen.queryByRole('button', { name: `Show ${workspaceName} agents` })
      ?? screen.queryByRole('button', { name: `Hide ${workspaceName} agents` })
    expect(disclosure).not.toBeNull()
  })
  const show = screen.queryByRole('button', { name: `Show ${workspaceName} agents` })
  if (show) fireEvent.click(show)
  expect(screen.getByRole('button', { name: `Hide ${workspaceName} agents` })).toHaveAttribute('aria-expanded', 'true')
}

beforeEach(() => {
  reducedMotion = false
  seam.mains.clear()
  seam.attention.clear()
  seam.mains.set(miaMember, 'seam-opaque-mia')
  seam.mains.set(jimMember, 'seam-opaque-jim')
  // No mains.set(adminMember, …): the membership-supplied Admin main was the
  // pre-ruling mechanism. Admin's main comes only from home.admin_main_session_id
  // (seam-read by workspaceRoster.adminDefault), so the admin member-config
  // entry must stay mainless and the Admin row must still render.
  seam.attention.set('seam-opaque-mia', 'on')
  seam.attention.set('seam-opaque-jim', 'on')
  seam.attention.set('seam-opaque-admin', 'off')
  seam.attention.set('extra-newer', 'off')
  mockSelectSession.mockReset()
  mockNavigate.mockReset()
  act(() => {
    useSidebarStore.setState({ isOpen: true, isPinned: true })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useUiStore.setState({
      searchModalOpen: false,
      searchModalWorkspaceFilter: null,
      searchModalMode: 'sessions',
    })
  })
  vi.mocked(fetchWorkspaces).mockImplementation(async (params) => {
    if (params?.status === 'archived') return []
    return [home, product]
  })
  vi.mocked(fetchAgents).mockResolvedValue([
    makeAgent({ id: 'mia', name: 'Mia', type: 'core' }),
    makeAgent({ id: 'jim', name: 'Jim', type: 'core' }),
    makeAgent({ id: 'admin', name: 'Admin', type: 'core' }),
    makeAgent({ id: 'native-worker', name: 'Native worker', type: 'Subagent' }),
    makeAgent({ id: 'judge', name: 'Judge', type: 'system' }),
  ])
  vi.mocked(fetchSessions).mockResolvedValue([
    chatSession({ id: 'seam-opaque-mia', agent_id: 'mia', workspace_id: 'product-launch', title: 'Mia main', updated_at: '2026-01-01T00:00:00Z' }),
    chatSession({ id: 'seam-opaque-jim', agent_id: 'jim', workspace_id: 'product-launch', title: 'Jim main', updated_at: '2026-01-01T00:00:00Z' }),
    chatSession({ id: 'extra-newer', agent_id: 'mia', workspace_id: 'product-launch', title: 'Newer extra', updated_at: '2026-10-08T00:00:00Z' }),
    chatSession({ id: 'seam-opaque-admin', agent_id: 'admin', workspace_id: 'default', title: 'Admin main', updated_at: '2026-01-01T00:00:00Z' }),
  ])
})

describe('Sidebar agent rows (T-01, T-13, T-14)', () => {
  it('uses the wireframe workspace-group margins and a single inner indent', async () => {
    renderSidebar()
    const collapsedGroup = await screen.findByRole('group', { name: 'Product launch' })
    // Wireframe .ws-agents[hidden] consumes no group spacing when folded.
    expect(collapsedGroup.className).toBe('')
    await expandAgents('Product launch')
    const group = screen.getByRole('group', { name: 'Product launch' })
    // Wireframe .ws-agents: margin 4px 8px 8px 24px; padding-left 12px.
    // This runner does not apply Tailwind, so pin the registered declarations,
    // not a claimed browser measurement. The agent button keeps its own inset.
    expect(group).toHaveClass(
      'mt-[var(--space-1)]', 'mr-[var(--space-2)]', 'mb-[var(--space-2)]',
      'ml-[var(--space-4)]', 'pl-[var(--space-2-5)]', 'border-l',
    )
    expect(group).not.toHaveClass('pb-[var(--space-1)]')
    for (const name of ['Mia', 'Jim']) {
      const row = within(group).getByRole('group', { name })
      expect(row).not.toHaveClass('pl-[var(--space-2-5)]')
      expect(row).not.toHaveClass('pr-[var(--space-2)]')
    }
  })

  it('N01 clicking Mia in Product launch opens her seam main, not the newer extra', async () => {
    renderSidebar()
    // FR-003 / BDD-01.1: the click is the Product launch pair. Mia is also a
    // member of Default (FR-001), so an unscoped name matches two rows.
    // FR-002: a collapsed workspace hides its rows, so the click expands first.
    await expandAgents('Product launch')
    const productGroup = await screen.findByRole('group', { name: 'Product launch' })
    fireEvent.click(within(productGroup).getByRole('button', { name: 'Mia' }))
    expect(mockSelectSession).toHaveBeenCalledTimes(1)
    expect(mockSelectSession).toHaveBeenCalledWith(expect.objectContaining({ id: 'seam-opaque-mia', agent_id: 'mia' }))
    expect(mockSelectSession).not.toHaveBeenCalledWith(expect.objectContaining({ id: 'extra-newer' }))
  })

  it('N02-style Mia is a separate row under Default and under Product launch', async () => {
    renderSidebar()
    // FR-001: an eligible member is listed in each workspace they belong to.
    // This fixture shares one member record, so it proves per-workspace
    // presence, not two identities. The identity half of N02 / BDD-E01 is
    // eligibleMainAgents in src/lib/nav/eligibleMains.test.ts.
    await expandAgents('Product launch')
    await expandAgents('Default')
    const productGroup = await screen.findByRole('group', { name: 'Product launch' })
    const defaultGroup = await screen.findByRole('group', { name: 'Default' })
    const productMia = within(productGroup).getByRole('button', { name: 'Mia' })
    const defaultMia = within(defaultGroup).getByRole('button', { name: 'Mia' })
    expect(productMia).not.toBe(defaultMia)
  })

  it('BDD-03.3 Past sessions comes before New chat, as two independent named buttons', async () => {
    renderSidebar()
    await expandAgents('Product launch')
    const productGroup = await screen.findByRole('group', { name: 'Product launch' })
    const mia = within(productGroup).getByRole('group', { name: 'Mia' })
    const past = within(mia).getByRole('button', { name: /^Past sessions with Mia$/ })
    const newer = within(mia).getByRole('button', { name: /^New chat with Mia$/ })
    expect(past.compareDocumentPosition(newer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(past.contains(newer)).toBe(false)
    expect(newer.contains(past)).toBe(false)
  })

  it('R22 keeps icon-only labelled actions beside the selected agent, including in an extra chat', async () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: product.id })
      useSessionStore.setState({ activeSessionId: 'extra-newer', activeAgentId: 'mia' })
    })
    renderSidebar()
    await expandAgents('Product launch')
    const group = await screen.findByRole('group', { name: 'Product launch' })
    const row = within(group).getByRole('group', { name: 'Mia' })
    const main = within(row).getByRole('button', { name: /^Mia$/ })
    const past = within(row).getByRole('button', { name: /^Past sessions with Mia$/ })
    const extra = within(row).getByRole('button', { name: /^New chat with Mia$/ })

    expect(row).toHaveAttribute('data-selected', 'true')
    expect(main).toHaveAttribute('aria-current', 'true')
    expect(past).toHaveAttribute('title', 'Past sessions with Mia')
    expect(extra).toHaveAttribute('title', 'New chat with Mia')
    expect(past.textContent).toBe('')
    expect(extra.textContent).toBe('')
    expect(past.querySelector('svg')).not.toBeNull()
    expect(extra.querySelector('svg')).not.toBeNull()
    expect(main.contains(past)).toBe(false)
    expect(main.contains(extra)).toBe(false)
    expect(main.compareDocumentPosition(past) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
    expect(past.compareDocumentPosition(extra) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
  })

  it('R22 selection follows the immutable owner in this workspace, never the same agent in another workspace', async () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: product.id })
      useSessionStore.setState({ activeSessionId: 'extra-newer', activeAgentId: 'mia' })
    })
    renderSidebar()
    await expandAgents('Product launch')
    await expandAgents('Default')
    const productGroup = await screen.findByRole('group', { name: 'Product launch' })
    const defaultGroup = await screen.findByRole('group', { name: 'Default' })
    const productMia = within(productGroup).getByRole('group', { name: 'Mia' })
    const defaultMia = within(defaultGroup).getByRole('group', { name: 'Mia' })
    expect(productMia).toHaveAttribute('data-selected', 'true')
    expect(defaultMia).toHaveAttribute('data-selected', 'false')

    act(() => useSessionStore.setState({ activeSessionId: 'seam-opaque-jim', activeAgentId: 'jim' }))
    expect(productMia).toHaveAttribute('data-selected', 'false')
    expect(within(productGroup).getByRole('group', { name: 'Jim' })).toHaveAttribute('data-selected', 'true')
  })

  it('BDD-03.2 Past sessions opens one modal filtered by this workspace and this agent', async () => {
    renderSidebar()
    await expandAgents('Product launch')
    const productGroup = await screen.findByRole('group', { name: 'Product launch' })
    const mia = within(productGroup).getByRole('group', { name: 'Mia' })
    fireEvent.click(within(mia).getByRole('button', { name: /^Past sessions with Mia$/ }))
    const modal = useUiStore.getState() as {
      searchModalOpen: boolean
      searchModalWorkspaceFilter: string | null
      searchModalAgentFilter?: string
    }
    expect(modal.searchModalOpen).toBe(true)
    expect(modal.searchModalWorkspaceFilter).toBe('product-launch')
    expect(modal.searchModalAgentFilter).toBe('mia')
  })

  it('N10 Admin is a row in the default workspace and not in Product launch', async () => {
    renderSidebar()
    await expandAgents('Default')
    await expandAgents('Product launch')
    const homeGroup = await screen.findByRole('group', { name: 'Default' })
    const productGroup = await screen.findByRole('group', { name: 'Product launch' })
    expect(within(homeGroup).getByRole('button', { name: 'Admin' })).toBeTruthy()
    expect(within(productGroup).queryByRole('button', { name: 'Admin' })).toBeNull()
    expect(within(productGroup).queryByRole('button', { name: 'Native worker' })).toBeNull()
    expect(within(productGroup).queryByRole('button', { name: 'Judge' })).toBeNull()
  })

  it('FR-002 a collapsed workspace shows no agent rows, only the 8px dot and the count', async () => {
    renderSidebar()
    const cue = await screen.findByRole('status', { name: '2 main chats need your attention' })
    expect(cue).toHaveAttribute('data-cue-px', '8')
    const show = await screen.findByRole('button', { name: 'Show Product launch agents' })
    expect(show).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryAllByRole('button', { name: 'Mia' }), 'collapsed: no Mia row').toEqual([])
    expect(screen.queryAllByRole('button', { name: 'Jim' }), 'collapsed: no Jim row').toEqual([])
    expect(screen.queryAllByRole('button', { name: 'Admin' }), 'collapsed: no Admin row').toEqual([])
    expect(screen.queryAllByRole('group', { name: 'Mia' }), 'collapsed: no Mia group').toEqual([])
  })

  it('the active workspace starts expanded and a collapsed workspace still hides its rows', async () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'product-launch' })
    })
    renderSidebar()
    const hide = await screen.findByRole('button', { name: 'Hide Product launch agents' })
    expect(hide).toHaveAttribute('aria-expanded', 'true')
    const productGroup = await screen.findByRole('group', { name: 'Product launch' })
    expect(within(productGroup).getByRole('button', { name: 'Mia' })).toBeTruthy()
    expect(within(productGroup).getByRole('button', { name: 'Jim' })).toBeTruthy()
    const showDefault = await screen.findByRole('button', { name: 'Show Default agents' })
    expect(showDefault).toHaveAttribute('aria-expanded', 'false')
    const defaultGroup = screen.getByRole('group', { name: 'Default' })
    expect(within(defaultGroup).queryByRole('button', { name: 'Mia' })).toBeNull()
    expect(within(defaultGroup).queryByRole('button', { name: 'Admin' })).toBeNull()
  })

  it('A10 collapsed workspace shows a static-or-looping count of two and no agent-row dot', async () => {
    renderSidebar()
    const cue = await screen.findByRole('status', { name: '2 main chats need your attention' })
    expect(cue).toHaveAttribute('data-cue-px', '8')
    expect(cue).toHaveAttribute('data-attention-motion', 'loop')
    const productGroup = await screen.findByRole('group', { name: 'Product launch' })
    expect(productGroup.querySelector('[data-cue-px="8"]')).toBeNull()
  })

  it('A10 reduced motion keeps the count and sets the cue static', async () => {
    reducedMotion = true
    renderSidebar()
    const cue = await screen.findByRole('status', { name: '2 main chats need your attention' })
    expect(cue).toHaveAttribute('data-attention-motion', 'static')
    expect(cue).toHaveAttribute('data-cue-px', '8')
  })

  it('A10 an expanded workspace uses the icon halo and not a row dot', async () => {
    renderSidebar()
    const expand = await screen.findByRole('button', { name: 'Show Product launch agents' })
    fireEvent.click(expand)
    const productGroup = await screen.findByRole('group', { name: 'Product launch' })
    const mia = within(productGroup).getByRole('group', { name: 'Mia' })
    expect(within(productGroup).getByRole('button', { name: 'Mia' })).toBeTruthy()
    expect(within(productGroup).getByRole('button', { name: 'Jim' })).toBeTruthy()
    expect(mia.querySelector('[data-attention-halo="warning"]')).toBeTruthy()
    expect(mia.querySelector('[data-cue-px="8"]')).toBeNull()
    expect(screen.queryByRole('status', { name: '2 main chats need your attention' })).toBeNull()
  })

  it('BDD-04.4 unknown attention is unavailable with Retry, never a zero count', async () => {
    seam.attention.set('seam-opaque-mia', 'unknown')
    seam.attention.set('seam-opaque-jim', 'unknown')
    renderSidebar()
    expect(await screen.findByRole('status', { name: 'Attention unavailable' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
    expect(screen.queryByRole('status', { name: /0 main/ })).toBeNull()
  })

  it('BDD-01.4 a failed agent roster is Retry, not an empty-team success', async () => {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    renderSidebar()
    const expand = await screen.findByRole('button', { name: 'Show Product launch agents' })
    fireEvent.click(expand)
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeTruthy()
    expect(screen.queryByText('No sessions yet')).toBeNull()
  })
})

// FR-011 / BDD-01.4: keep the shell, QueryClient, roster derivation, cache,
// attention projection and Retry handlers real. Only network replies and the
// unpublished main/attention seam are fixtures. The singleton matters: row
// actions invalidate this same client, not a disconnected provider cache.
function renderRefreshableSidebar() {
  vi.restoreAllMocks()
  const defaults = queryClient.getDefaultOptions()
  queryClient.clear()
  queryClient.setDefaultOptions({
    ...defaults,
    queries: { ...defaults.queries, retry: false },
  })
  act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'product-launch' }) })
  const view = renderSidebar(queryClient)
  return {
    restore: () => {
      view.unmount()
      queryClient.clear()
      queryClient.setDefaultOptions(defaults)
    },
  }
}

async function cachedProductRows() {
  const group = await screen.findByRole('group', { name: 'Product launch' })
  await waitFor(() => {
    expect(queryClient.getQueryState(['agents'])).toMatchObject({ status: 'success', fetchStatus: 'idle' })
    expect(queryClient.getQueryState(['sessions'])).toMatchObject({ status: 'success', fetchStatus: 'idle' })
    expect(within(group).getAllByRole('group').map((row) => row.getAttribute('aria-label'))).toEqual(['Mia', 'Jim'])
  })
  // Known attention and real rows deliberately avoid the unrelated
  // unknown-attention/missing-main error branches masking the stale-cache gap.
  expect(screen.queryByRole('status', { name: 'Attention unavailable' })).toBeNull()
  expect(screen.queryByRole('status', { name: 'Main chat unavailable' })).toBeNull()
  expect(within(group).queryByRole('button', { name: 'Retry' })).toBeNull()
  return group
}

async function failCachedRosterRefresh() {
  vi.mocked(fetchAgents).mockClear()
  vi.mocked(fetchAgents).mockRejectedValue(new Error('roster refresh failed'))
  await act(async () => {
    await queryClient.invalidateQueries({ queryKey: ['agents'] })
  })
  await waitFor(() => {
    expect(fetchAgents).toHaveBeenCalledTimes(1)
    expect(queryClient.getQueryState(['agents'])).toMatchObject({ status: 'error', fetchStatus: 'idle' })
    expect(queryClient.getQueryState(['agents'])?.error).toEqual(new Error('roster refresh failed'))
  })
  expect(queryClient.getQueryData<Agent[]>(['agents'])?.map((agent) => agent.id)).toEqual([
    'mia', 'jim', 'admin', 'native-worker', 'judge',
  ])
}

// The spec fixes the notice's meaning, not its wording or placement: the
// retained team must be visibly identified as stale/out-of-date/cached, with
// a Retry action. The shared roster may use a shell-wide or workspace notice.
const STALE_TEAM_NOTICE = /stale|out[- ]of[- ]date|cached/i

describe('Sidebar stale cached-roster failure and recovery (FR-011, BDD-01.4)', () => {
  it('BDD-01.4 a failed background refresh retains cached main rows with a visible stale warning and Retry', async () => {
    const shell = renderRefreshableSidebar()
    try {
      const group = await cachedProductRows()
      await failCachedRosterRefresh()

      expect(within(group).getAllByRole('group').map((row) => row.getAttribute('aria-label'))).toEqual(['Mia', 'Jim'])
      expect(screen.queryByText('No sessions yet')).toBeNull()
      expect(await screen.findByText(STALE_TEAM_NOTICE)).toBeVisible()
      expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
    } finally {
      shell.restore()
    }
  })

  it('BDD-01.4 Retry keeps the stale warning on failure or a pending refresh and clears it only after the real roster recovers', async () => {
    const shell = renderRefreshableSidebar()
    try {
      const group = await cachedProductRows()
      await failCachedRosterRefresh()
      // Independently prove Retry is reachable, not just that stale copy exists.
      const retry = await screen.findByRole('button', { name: 'Retry' })
      expect(await screen.findByText(STALE_TEAM_NOTICE)).toBeVisible()

      // A failed supported Retry must not convert cached rows into success.
      fireEvent.click(retry)
      await waitFor(() => {
        expect(fetchAgents).toHaveBeenCalledTimes(2)
        expect(queryClient.getQueryState(['agents'])).toMatchObject({ status: 'error', fetchStatus: 'idle' })
      })
      expect(await screen.findByText(STALE_TEAM_NOTICE)).toBeVisible()
      expect(within(group).getAllByRole('group').map((row) => row.getAttribute('aria-label'))).toEqual(['Mia', 'Jim'])

      // Hold the recovered network response to prove clicking Retry alone
      // cannot clear the warning. No timers or fabricated component state.
      let resolveRoster!: (agents: Agent[]) => void
      const recoveredRoster = new Promise<Agent[]>((resolve) => { resolveRoster = resolve })
      vi.mocked(fetchAgents).mockReturnValue(recoveredRoster)
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
      await waitFor(() => {
        expect(fetchAgents).toHaveBeenCalledTimes(3)
        expect(queryClient.getQueryState(['agents'])?.fetchStatus).toBe('fetching')
      })
      expect(await screen.findByText(STALE_TEAM_NOTICE)).toBeVisible()
      expect(within(group).getAllByRole('group').map((row) => row.getAttribute('aria-label'))).toEqual(['Mia', 'Jim'])

      await act(async () => {
        resolveRoster([
          makeAgent({ id: 'mia', name: 'Mia recovered', type: 'core' }),
          makeAgent({ id: 'jim', name: 'Jim', type: 'core' }),
          makeAgent({ id: 'admin', name: 'Admin', type: 'core' }),
        ])
        await recoveredRoster
      })
      await waitFor(() => {
        expect(queryClient.getQueryState(['agents'])).toMatchObject({ status: 'success', fetchStatus: 'idle' })
        expect(within(group).getAllByRole('group').map((row) => row.getAttribute('aria-label'))).toEqual(['Mia recovered', 'Jim'])
        expect(screen.queryByText(STALE_TEAM_NOTICE)).toBeNull()
        expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
      })
      // Authoritative renamed Mia replaces the cached label, not her main ID;
      // membership is unchanged, so Jim must not disappear during recovery.
      expect(within(group).queryByRole('button', { name: 'Mia' })).toBeNull()
      expect(within(group).getByRole('button', { name: 'Jim' })).toBeInTheDocument()
      fireEvent.click(within(group).getByRole('button', { name: 'Mia recovered' }))
      expect(mockSelectSession).toHaveBeenCalledTimes(1)
      expect(mockSelectSession).toHaveBeenCalledWith(expect.objectContaining({
        id: 'seam-opaque-mia', agent_id: 'mia', workspace_id: 'product-launch',
      }))
    } finally {
      shell.restore()
    }
  })
})

describe('Sidebar freshness (T-11, FR-010)', () => {
  it('expanding a workspace inside staleTime invalidates agents and membership', async () => {
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)
    renderSidebar()
    const expand = await screen.findByRole('button', { name: 'Show Product launch agents' })
    invalidate.mockClear()
    fireEvent.click(expand)
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['agents'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: workspacesQueryKeys.list({ status: 'active' }) })
  })

  it('opening a workspace by its name inside staleTime invalidates agents and membership', async () => {
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)
    renderSidebar()
    const name = await screen.findByRole('button', { name: 'Product launch' })
    invalidate.mockClear()
    fireEvent.click(name)
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['agents'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: workspacesQueryKeys.list({ status: 'active' }) })
  })
})
