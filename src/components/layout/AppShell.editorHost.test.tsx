/**
 * Exactly ONE store-driven agent editor (AgentProfile) exists per app render.
 * Oracle: CR F6 / architect F4 / pr-test F5 — the editor reads
 * useUiStore.editAgentId, so EVERY surface mounting its own <AgentProfile>
 * host opens a second simultaneous sheet the moment the sidebar sits next to
 * that surface (two competing dialogs, two focus owners).
 *
 * This file mounts the REAL composition — the app shell with the REAL
 * Sidebar (whose agent rows carry the ⋯ Edit item), the REAL Team tab in the
 * Outlet, and the REAL AgentProfile (never mocked here) — then opens the
 * editor the way the row menu's Edit item does. Exactly one editor dialog may
 * answer. If any surface re-gains a private host, the count becomes 2 and
 * this file fails.
 */
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AppState, NotificationList } from '@/lib/api/generated/openapi-types'
import type { Session, Workspace, WorkspaceDelegation } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { useSidebarStore } from '@/store/sidebar'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

// AppShell composes a wide tree (AppShell.test.tsx scopes the same surfaces).
// Everything here is a black-box stub EXCEPT the composition under test:
// Sidebar (real → WorkspaceAgentList real), the Team tab (real), and
// AgentProfile (real — the component whose host count this file pins).
vi.mock('./NotificationPanel', () => ({ NotificationPanel: () => null }))
vi.mock('@/components/ui/toast-container', () => ({ ToastContainer: () => null }))
vi.mock('@/components/chat/MediaLightbox', () => ({ MediaLightbox: () => null }))
vi.mock('@/components/search/SearchModal', () => ({ SearchModal: () => null }))
vi.mock('@/components/chat/OmnipusRuntimeProvider', () => ({
  OmnipusRuntimeProvider: ({ children }: { children?: React.ReactNode }) => children ?? null,
}))
vi.mock('@/hooks/useVersionCheck', () => ({ useVersionCheck: vi.fn() }))
// The delegation GRAPH is not the host under test — the Team tab itself is
// (its AgentProfile mount, if one re-appears, sits in WorkspaceTeamTab).
// Stubbing it avoids react-flow geometry shims while keeping the tab mounted.
vi.mock('@/components/workspaces/team/WorkspaceTeamGraph', () => ({
  WorkspaceTeamGraph: () => null,
}))

const WS = 'ws-1'

function workspaceFixture(): Workspace {
  return {
    id: WS,
    name: 'Alpha',
    is_default: false,
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    revision: '0'.repeat(64),
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    core_team: ['mia', 'jim'],
    member_configs: { mia: {}, jim: {} },
  }
}

const DELEGATION: WorkspaceDelegation = {
  revision: '2'.repeat(64),
  workspace_id: WS,
  team: ['mia', 'jim'],
  edges: [{ from_agent: 'mia', to_agent: 'jim', modes: ['direct'] }],
  default_depth: 10,
}

function chatSession(id: string, agentId: string): Session {
  return {
    id,
    type: 'chat',
    agent_id: agentId,
    title: id,
    workspace_id: WS,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    message_count: 0,
  }
}

const seam = vi.hoisted(() => {
  const mains = new Map<object, string>()
  return {
    mains,
    mainSessionIdOfMember: (member: object) => mains.get(member),
    isMainSession: (session: unknown) => {
      const id = session && typeof session === 'object' && 'id' in session
        ? String((session as { id: unknown }).id)
        : ''
      return id === 'seam-mia' || id === 'seam-jim'
    },
    sessionAttention: () => 'off' as const,
    attachAckFields: () => ({}),
    attentionBoundOfFrame: (_frame: unknown) => {
      void _frame
      return undefined
    },
  }
})

vi.mock('@/lib/nav/sessionCoreSeam', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/nav/sessionCoreSeam')>(),
  mainSessionIdOfMember: (member: object) => seam.mainSessionIdOfMember(member),
  isMainSession: (session: unknown) => seam.isMainSession(session),
  sessionAttention: () => seam.sessionAttention(),
  attachAckFields: () => seam.attachAckFields(),
  attentionBoundOfFrame: (frame: unknown) => seam.attentionBoundOfFrame(frame),
}))

// The Team tab reads the active workspace from the container's context hook.
// The factory stays value-free (vi.mock factories are hoisted above this
// file's top-level constants) — the implementation is set in beforeEach.
vi.mock('@/components/workspaces/WorkspaceTabContainer', () => ({
  useActiveWorkspace: vi.fn(),
}))

const mockRouter = vi.hoisted(() => ({ navigate: vi.fn() }))
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    // The real Team tab component, in the real shell.
    Outlet: () => <TeamTabOutletStub />,
    useRouter: () => mockRouter,
    useNavigate: () => vi.fn(),
    useLocation: () => ({ pathname: '/' }),
    Link: ({ children, to }: { children: React.ReactNode; to: string }) => <a href={to}>{children}</a>,
  }
})

// Value-free factory (hoisting) — every resolved value is wired in beforeEach.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(),
    fetchNotifications: vi.fn(),
    fetchTasks: vi.fn(),
    fetchAgents: vi.fn(),
    fetchSessions: vi.fn(),
    fetchWorkspaces: vi.fn(),
    fetchWorkspaceDelegation: vi.fn(),
    fetchGodMode: vi.fn(),
  }
})

if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }),
  })
}
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

const APP_STATE_OK: AppState = {
  onboarding_complete: true,
  dev_mode_bypass: false,
  identity: { mode: 'local', edition: 'core', signed_in: false, blocked_reason: 'signed_out' },
}
const NOTIFICATIONS_EMPTY: NotificationList = { notifications: [], unread_count: 0 }

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

import { AppShell } from './AppShell'
import { WorkspaceTeamTab } from '@/components/workspaces/WorkspaceTeamTab'
import {
  fetchAppState,
  fetchAgents,
  fetchGodMode,
  fetchNotifications,
  fetchSessions,
  fetchTasks,
  fetchWorkspaceDelegation,
  fetchWorkspaces,
} from '@/lib/api'
import { useActiveWorkspace } from '@/components/workspaces/WorkspaceTabContainer'

function TeamTabOutletStub() {
  return <WorkspaceTeamTab workspaceId={WS} />
}

function renderShell() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <AppShell />
    </QueryClientProvider>,
  )
}

describe('the agent editor has exactly one host (CR F6)', () => {
  beforeEach(() => {
    // Wire every mocked fetch's resolved value here (factories stay hoisted
    // and value-free). The fixture objects double as the seam keys below.
    const ws = workspaceFixture()
    const members = ws.member_configs ?? {}
    const miaAgent = makeAgent({ id: 'mia', name: 'Mia', type: 'core' })
    const jimAgent = makeAgent({ id: 'jim', name: 'Jim', type: 'core' })
    vi.mocked(fetchAppState).mockResolvedValue(APP_STATE_OK)
    vi.mocked(fetchNotifications).mockResolvedValue(NOTIFICATIONS_EMPTY)
    vi.mocked(fetchTasks).mockResolvedValue([])
    vi.mocked(fetchAgents).mockResolvedValue([miaAgent, jimAgent])
    vi.mocked(fetchSessions).mockResolvedValue([
      chatSession('seam-mia', 'mia'),
      chatSession('seam-jim', 'jim'),
    ])
    vi.mocked(fetchWorkspaces).mockResolvedValue([ws])
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue(DELEGATION)
    vi.mocked(fetchGodMode).mockResolvedValue({ enabled: false, available: false, supported: true, persisted: false })
    vi.mocked(useActiveWorkspace).mockReturnValue(ws)
    seam.mains.clear()
    // The sidebar roster validates mains through the seam map keyed by the
    // member-config objects inside workspaceFixture().
    seam.mains.set(members.mia as object, 'seam-mia')
    seam.mains.set(members.jim as object, 'seam-jim')
    act(() => {
      useSidebarStore.setState({ isOpen: true, isPinned: true })
      useWorkspacesStore.setState({ activeWorkspaceId: WS })
      useSessionStore.setState(useSessionStore.getInitialState(), true)
      useUiStore.setState({
        toasts: [],
        editAgentId: null,
        editAgentWorkspaceId: null,
        searchModalOpen: false,
      })
    })
  })

  afterEach(() => {
    cleanup()
    act(() => {
      useUiStore.setState({ editAgentId: null, editAgentWorkspaceId: null })
    })
  })

  it('opens exactly ONE agent editor when the row menu asks, however many surfaces render', async () => {
    renderShell()

    // The sidebar is really mounted: Mia's row carries the ⋯ trigger.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'More actions for Mia' })).toBeTruthy()
    })

    // Open the editor exactly the way the row menu's Edit item does.
    await act(async () => {
      useUiStore.getState().openEditAgentSlideOver('mia', WS)
    })

    await waitFor(() => {
      expect(screen.getAllByRole('dialog').length).toBeGreaterThan(0)
    })
    expect(
      screen.getAllByRole('dialog'),
      'two or more editor sheets answering one store entry means a duplicate host',
    ).toHaveLength(1)
  })
})
