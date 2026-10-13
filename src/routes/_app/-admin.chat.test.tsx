/**
 * -admin.chat.test.tsx — the /admin/chat redirect stub.
 *
 * founder request 2026-10-11 (brief "nav-adminentry"): the sidebar's
 * standalone "Admin chat" entry is deleted; Admin has ONE main chat, in the
 * default workspace (ADR-20261007), reached from the sidebar like any agent.
 * The route must redirect old links onto that main via the SAME entry path
 * the sidebar row uses (the session-core seam read → useSelectSession) and
 * must NEVER mint a standalone Admin session (the old behaviour called
 * createSession('admin') on every visit). Unresolvable → the sidebar's
 * unavailable state with Retry, never a blank page.
 *
 * The store edges around the entry path are mocked; useSelectSession itself
 * is REAL, so the tests pin the actual sidebar-row sequence: attach →
 * workspace switch → navigate to the workspace chat route.
 */
import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fetchWorkspaces, fetchAgents, fetchSessions } from '@/lib/api'
import type { Agent, Session, Workspace } from '@/lib/api'
import { makeAgent } from '@/test/factories'

const { mockNavigate, mockCreateSession, mockAttachToSession, mockSetActiveWorkspaceId, mockAddToast, mockSessionState } =
  vi.hoisted(() => ({
    mockNavigate: vi.fn(),
    mockCreateSession: vi.fn(),
    mockAttachToSession: vi.fn(),
    mockSetActiveWorkspaceId: vi.fn(),
    mockAddToast: vi.fn(),
    // The slice of the session store the stub reads beyond the entry path:
    // activeSessionId is attachToSession's own success signal (C7).
    mockSessionState: { activeAgentId: null as string | null, activeSessionId: null as string | null },
  }))

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: { component: React.ComponentType }) => options,
  useNavigate: () => mockNavigate,
}))

vi.mock('@/lib/api', () => ({
  fetchWorkspaces: vi.fn(),
  fetchAgents: vi.fn(),
  fetchSessions: vi.fn(),
  // The stub must never mint a session — mocked so a regression is LOUD.
  createSession: mockCreateSession,
  workspacesQueryKeys: { list: (params?: unknown) => ['workspaces', params] },
}))

// The entry path's store edges. attachToSession's boolean is the real
// contract: false aborts the selection (no navigation), true proceeds.
vi.mock('@/store/session', () => {
  const state = {
    get activeAgentId() { return mockSessionState.activeAgentId },
    get activeSessionId() { return mockSessionState.activeSessionId },
    attachToSession: mockAttachToSession,
    setActiveAgentType: vi.fn(),
  }
  const useSessionStore = (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state)
  useSessionStore.getState = () => state
  return { useSessionStore }
})
vi.mock('@/store/session/workspaceEntryFlow', () => ({
  supersedeNavigationIntent: vi.fn(),
}))
vi.mock('@/store/chat', () => ({
  useChatStore: (selector: (s: { seedSessionTokens: ReturnType<typeof vi.fn> }) => unknown) =>
    selector({ seedSessionTokens: vi.fn() }),
}))
vi.mock('@/store/workspacesStore', () => {
  const state = { activeWorkspaceId: null as string | null, setActiveWorkspaceId: mockSetActiveWorkspaceId }
  return {
    useWorkspacesStore: (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state),
  }
})
vi.mock('@/store/ui', () => {
  const state = { addToast: mockAddToast }
  const useUiStore = (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state)
  useUiStore.getState = () => state
  return { useUiStore }
})

function makeWorkspace(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: 'ws-default',
    name: 'Default',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    revision: '0'.repeat(64),
    is_default: true,
    ...overrides,
  }
}

function makeAdminMainSession(overrides: Partial<Session> = {}): Session {
  return {
    id: 'main-admin-1',
    agent_id: 'admin',
    title: 'Admin',
    type: 'main',
    workspace_id: 'ws-default',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    message_count: 0,
    ...overrides,
  }
}

const ADMIN = makeAgent({ id: 'admin', name: 'Admin', type: 'core' })

function mockApi(workspaces: Workspace[], agents: Agent[], sessions: Session[]) {
  vi.mocked(fetchWorkspaces).mockResolvedValue(workspaces)
  vi.mocked(fetchAgents).mockResolvedValue(agents)
  vi.mocked(fetchSessions).mockResolvedValue(sessions)
}

let AdminChatRoute: React.ComponentType

beforeEach(async () => {
  vi.clearAllMocks()
  mockAttachToSession.mockReturnValue(true)
  mockSessionState.activeAgentId = null
  mockSessionState.activeSessionId = null
  const module = await import('./admin.chat')
  AdminChatRoute = (module.Route as unknown as { component: React.ComponentType }).component
})

function renderRoute() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <AdminChatRoute />
    </QueryClientProvider>,
  )
}

describe('/admin/chat — redirect stub onto the default workspace Admin main', () => {
  it('never calls createSession and enters Admin\'s default-workspace main via the sidebar entry path', async () => {
    await mockApi(
      [makeWorkspace({ admin_main_session_id: 'main-admin-1' })],
      [ADMIN],
      [makeAdminMainSession()],
    )
    renderRoute()

    await waitFor(() => {
      // The seam found the main, the attach ran, and the workspace switched —
      // exactly the sidebar row's sequence.
      expect(mockAttachToSession).toHaveBeenCalledWith('main-admin-1', 'main', 'Admin', 'admin')
    })
    expect(mockSetActiveWorkspaceId).toHaveBeenCalledWith('ws-default')
    // replace: true — team-lead ruling on the nav-adminentry note: a push
    // would leave /admin/chat in the history, and Back from Admin's main
    // would bounce straight off the stub again (a back-button trap).
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId: 'ws-default' },
      replace: true,
    })
    // The one prohibition the brief makes explicit: no standalone session.
    expect(mockCreateSession).not.toHaveBeenCalled()
  })

  it('keeps the workspace switch and the attach in the sidebar row order', async () => {
    await mockApi(
      [makeWorkspace({ admin_main_session_id: 'main-admin-1' })],
      [ADMIN],
      [makeAdminMainSession()],
    )
    renderRoute()

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith(
        expect.objectContaining({ to: '/workspaces/$workspaceId/chat' }),
      )
    })
    // The active workspace points at the main's home BEFORE the navigation —
    // the workspace chat screen must mount with Admin's main as its context.
    expect(mockSetActiveWorkspaceId.mock.invocationCallOrder[0]).toBeLessThan(
      mockNavigate.mock.invocationCallOrder[0],
    )
  })

  it('shows the sidebar\'s unavailable state with Retry when Admin\'s main id is missing', async () => {
    await mockApi([makeWorkspace({ admin_main_session_id: undefined })], [ADMIN], [])
    renderRoute()

    expect(await screen.findByRole('status', { name: 'Main chat unavailable' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(mockAttachToSession).not.toHaveBeenCalled()
    expect(mockNavigate).not.toHaveBeenCalled()
    expect(mockCreateSession).not.toHaveBeenCalled()
  })

  it('shows the unavailable state when the main id exists but no session row carries it', async () => {
    await mockApi(
      [makeWorkspace({ admin_main_session_id: 'main-admin-1' })],
      [ADMIN],
      [], // the session list does not (yet) contain Admin's main
    )
    renderRoute()

    expect(await screen.findByRole('status', { name: 'Main chat unavailable' })).toBeInTheDocument()
    expect(mockAttachToSession).not.toHaveBeenCalled()
    expect(mockCreateSession).not.toHaveBeenCalled()
  })

  it('Retry refetches and lands on the main once it resolves', async () => {
    vi.mocked(fetchWorkspaces).mockResolvedValue([makeWorkspace({ admin_main_session_id: undefined })])
    vi.mocked(fetchAgents).mockResolvedValue([ADMIN])
    vi.mocked(fetchSessions).mockResolvedValue([])

    renderRoute()
    expect(await screen.findByRole('status', { name: 'Main chat unavailable' })).toBeInTheDocument()

    // The server now reports the resolved main (the unavailable state's Retry
    // is what a user leans on until the data arrives).
    vi.mocked(fetchWorkspaces).mockResolvedValue([makeWorkspace({ admin_main_session_id: 'main-admin-1' })])
    vi.mocked(fetchSessions).mockResolvedValue([makeAdminMainSession()])
    await act(async () => {
      screen.getByRole('button', { name: 'Retry' }).click()
    })

    await waitFor(() => {
      expect(mockAttachToSession).toHaveBeenCalledWith('main-admin-1', 'main', 'Admin', 'admin')
    })
    expect(mockNavigate).toHaveBeenCalledWith(
      expect.objectContaining({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-default' } }),
    )
    expect(mockCreateSession).not.toHaveBeenCalled()
  })

  // C5 (R3 delta re-review, PTA F3): the attach-refused + Retry branch was
  // never driven — every fixture attached successfully. attachToSession's
  // boolean is the real contract: false aborts the selection (toast, no
  // navigation) and the stub offers Retry.
  it('a refused attach is visible — the entry toast, the unavailable state and Retry, no navigation', async () => {
    await mockApi(
      [makeWorkspace({ admin_main_session_id: 'main-admin-1' })],
      [ADMIN],
      [makeAdminMainSession()],
    )
    mockAttachToSession.mockReturnValue(false) // the WS send failed mid-reconnect
    renderRoute()

    expect(await screen.findByRole('status', { name: 'Main chat unavailable' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(mockAttachToSession).toHaveBeenCalledTimes(1)
    expect(mockNavigate).not.toHaveBeenCalled()
    expect(mockCreateSession).not.toHaveBeenCalled()
    // useSelectSession's own attach-failure toast fired — the refusal is not
    // silent even before the stub's Retry screen appears.
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' }))
  })

  it('Retry after a refused attach re-runs the entry path and lands on the main (C5)', async () => {
    await mockApi(
      [makeWorkspace({ admin_main_session_id: 'main-admin-1' })],
      [ADMIN],
      [makeAdminMainSession()],
    )
    mockAttachToSession.mockReturnValue(false)
    renderRoute()
    expect(await screen.findByRole('status', { name: 'Main chat unavailable' })).toBeInTheDocument()
    const attemptsBeforeRetry = mockAttachToSession.mock.calls.length

    mockAttachToSession.mockReturnValue(true)
    await act(async () => {
      screen.getByRole('button', { name: 'Retry' }).click()
    })

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith(
        expect.objectContaining({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-default' } }),
      )
    })
    expect(mockAttachToSession.mock.calls.length).toBeGreaterThan(attemptsBeforeRetry)
    expect(mockCreateSession).not.toHaveBeenCalled()
  })

  // C5: the stub's own inline default-workspace rule — the seam's
  // admin_main_session_id is read from the DEFAULT workspace only. A
  // non-default workspace carrying the field is never entered.
  it('a non-default workspace carrying admin_main_session_id is never entered', async () => {
    await mockApi(
      [
        makeWorkspace({ id: 'ws-other', name: 'Other', is_default: false, admin_main_session_id: 'main-admin-9' }),
        makeWorkspace({ admin_main_session_id: undefined }), // the default ws has no main
      ],
      [ADMIN],
      [makeAdminMainSession({ id: 'main-admin-9', workspace_id: 'ws-other' })],
    )
    renderRoute()

    expect(await screen.findByRole('status', { name: 'Main chat unavailable' })).toBeInTheDocument()
    expect(mockAttachToSession).not.toHaveBeenCalled()
    expect(mockNavigate).not.toHaveBeenCalled()
    expect(mockSetActiveWorkspaceId).not.toHaveBeenCalledWith('ws-other')
    expect(mockCreateSession).not.toHaveBeenCalled()
  })

  // C7 (R3 delta re-review, CR L-1 / SFH F8): the redirect used to flash
  // "Main chat unavailable" + Retry for the render between the attempt and
  // the route swap. A successful attach (the entry path's own outcome
  // signal: activeSessionId now names the main) must hold the neutral
  // resolving state instead — "unavailable" is for real failures only.
  it('a successful entry holds the resolving state — never the unavailable flash (C7)', async () => {
    await mockApi(
      [makeWorkspace({ admin_main_session_id: 'main-admin-1' })],
      [ADMIN],
      [makeAdminMainSession()],
    )
    // The real attachToSession sets the store's activeSessionId; the mock
    // mirrors that contract.
    mockAttachToSession.mockImplementation((sid: string) => {
      mockSessionState.activeSessionId = sid
      return true
    })
    renderRoute()

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith(
        expect.objectContaining({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-default' } }),
      )
    })
    // Still mounted (the mocked navigate swaps no route): the surface shows
    // the neutral resolving presentation, and the unavailable state never
    // flashed.
    expect(screen.queryByRole('status', { name: 'Main chat unavailable' })).not.toBeInTheDocument()
    expect(screen.getByText('Restoring your conversation…')).toBeInTheDocument()
  })
})
