// Unit tests for Bug 2: /sessions/{id} redirects to the session's workspace.
//
// Verifies the useEffect in SessionRoute fires the correct navigation and
// store actions when a session has a workspace_id that resolves to an active
// workspace.
//
// All state assertions are outcome-based (not function spies) to avoid
// cross-test spy contamination with Zustand singleton stores.

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, act, waitFor } from '@testing-library/react'

// ── Mocks ─────────────────────────────────────────────────────────────────────

vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => null,
}))

vi.mock('framer-motion', () => ({
  motion: new Proxy({}, {
    get: (_: object, prop: string) =>
      React.forwardRef(({ children, ...props }: Record<string, unknown>, ref: React.Ref<unknown>) =>
        React.createElement(prop as string, { ...props, ref }, children as React.ReactNode)),
  }),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

const mockNavigate = vi.fn()
const mockSessionId = 'sess-workspace-001'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: (_path: string) => (opts: { component: React.ComponentType; loader?: unknown }) => ({
      ...opts,
      useParams: () => ({ sessionId: mockSessionId }),
      useLoaderData: () => _mockUseQueryData,
    }),
    useParams: () => ({ sessionId: mockSessionId }),
    useNavigate: () => mockNavigate,
  }
})

let _mockUseQueryData: unknown = null
let _mockWorkspaces: unknown[] = []

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: vi.fn().mockImplementation(({ queryKey }: { queryKey: unknown[] }) => {
      const key = JSON.stringify(queryKey)
      if (key.includes('workspaces')) {
        return { data: _mockWorkspaces, isLoading: false, isError: false }
      }
      return { data: _mockUseQueryData, isLoading: false, isError: false }
    }),
  }
})

// ── Real stores ────────────────────────────────────────────────────────────────
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { SessionDetail } from '@/lib/api'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeChatSessionWithWorkspace(overrides?: Partial<SessionDetail['session']>): SessionDetail {
  return {
    session: {
      id: mockSessionId,
      agent_id: 'mia',
      title: 'Chat in ws-1',
      type: 'chat',
      workspace_id: 'ws-1',
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
      message_count: 5,
      ...overrides,
    },
    messages: [],
    agent_removed: false,
  }
}

function makeChatSessionNoWorkspace(overrides?: Partial<SessionDetail['session']>): SessionDetail {
  return {
    session: {
      id: mockSessionId,
      agent_id: 'mia',
      title: 'Standalone chat',
      type: 'chat',
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
      message_count: 2,
      ...overrides,
    },
    messages: [],
    agent_removed: false,
  }
}

function makeWorkspace(id: string) {
  return { id, name: `Workspace ${id}`, is_default: false, core_team: [] }
}

function makeMockConnection() {
  return { send: vi.fn().mockReturnValue(true), close: vi.fn(), isConnected: true }
}

function resetStores() {
  // Merge (not replace) to preserve Zustand action references.
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {},
  })
  useConnectionStore.setState({
    connection: null,
    isConnected: false,
    connectionError: null,
    reconnectPhase: null,
    reconnectAttempt: 0,
    liteMode: false,
  })
  useWorkspacesStore.setState({
    activeWorkspaceId: null,
    activeMilestoneId: null,
    boardAltitude: 'top-level',
  })
  mockNavigate.mockClear()
  _mockWorkspaces = []
  _mockUseQueryData = null
}

// ── Component under test ───────────────────────────────────────────────────────
let SessionRoute: React.ComponentType | null = null

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('SessionRoute — Bug 2: deep-link redirects to workspace', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    resetStores()

    if (!SessionRoute) {
      const mod = await import('./sessions.$sessionId')
      SessionRoute = (mod.Route as unknown as { component: React.ComponentType }).component
    }
  })

  it(
    'sets activeWorkspaceId and activeSessionId then navigates to workspace chat (WS connected)',
    async () => {
      // BDD: Given a session with workspace_id='ws-1' and WS connected,
      //   When SessionRoute mounts,
      //   Then activeWorkspaceId='ws-1', activeSessionId=sessId, and
      //   navigate to /workspaces/ws-1/chat fires.
      const detail = makeChatSessionWithWorkspace()
      _mockUseQueryData = detail
      _mockWorkspaces = [makeWorkspace('ws-1')]

      const mockConn = makeMockConnection()
      useConnectionStore.setState({ connection: mockConn as never, isConnected: true })

      const Route = SessionRoute
      if (!Route) throw new Error('SessionRoute not loaded')
      await act(async () => { render(<Route />) })

      await waitFor(() => {
        expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-1')
        expect(useSessionStore.getState().activeSessionId).toBe(mockSessionId)
        expect(mockNavigate).toHaveBeenCalledWith({
          to: '/workspaces/$workspaceId/chat',
          params: { workspaceId: 'ws-1' },
          replace: true,
        })
      })
    },
  )

  it(
    'sets activeWorkspaceId and activeSessionId then navigates when WS is offline',
    async () => {
      // BDD: Given a session with workspace_id='ws-1' and WS NOT connected,
      //   When SessionRoute mounts,
      //   Then setActiveSession fires (sets activeSessionId) and navigate fires.
      const detail = makeChatSessionWithWorkspace()
      _mockUseQueryData = detail
      _mockWorkspaces = [makeWorkspace('ws-1')]
      // No connection

      const Route = SessionRoute
      if (!Route) throw new Error('SessionRoute not loaded')
      await act(async () => { render(<Route />) })

      await waitFor(() => {
        expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-1')
        expect(useSessionStore.getState().activeSessionId).toBe(mockSessionId)
        expect(mockNavigate).toHaveBeenCalledWith({
          to: '/workspaces/$workspaceId/chat',
          params: { workspaceId: 'ws-1' },
          replace: true,
        })
      })
    },
  )

  it(
    'does NOT navigate when session has NO workspace_id (fallback inline path)',
    async () => {
      // BDD: Given a session with no workspace_id,
      //   When SessionRoute mounts,
      //   Then activeSessionId is set and navigate to workspace is NOT called.
      const detail = makeChatSessionNoWorkspace()
      _mockUseQueryData = detail
      _mockWorkspaces = []

      const mockConn = makeMockConnection()
      useConnectionStore.setState({ connection: mockConn as never, isConnected: true })

      const Route = SessionRoute
      if (!Route) throw new Error('SessionRoute not loaded')
      await act(async () => { render(<Route />) })

      await waitFor(() => {
        // Session is attached (inline path)
        expect(useSessionStore.getState().activeSessionId).toBe(mockSessionId)
      }, { timeout: 2000 })

      // Navigate to workspace must NOT have been called
      expect(mockNavigate).not.toHaveBeenCalled()
    },
  )

  it(
    'does NOT navigate when workspace_id is set but workspace not in active list (deleted/archived)',
    async () => {
      // BDD: Given a session with workspace_id='deleted-ws' but that workspace
      //   is not in the active workspaces list,
      //   When SessionRoute mounts,
      //   Then no workspace redirect fires (inline path used instead).
      const detail = makeChatSessionWithWorkspace({ workspace_id: 'deleted-ws' })
      _mockUseQueryData = detail
      _mockWorkspaces = []  // workspace not found

      const mockConn = makeMockConnection()
      useConnectionStore.setState({ connection: mockConn as never, isConnected: true })

      const Route = SessionRoute
      if (!Route) throw new Error('SessionRoute not loaded')
      await act(async () => { render(<Route />) })

      await waitFor(() => {
        // Inline attach fires (fallback path)
        expect(useSessionStore.getState().activeSessionId).toBe(mockSessionId)
      }, { timeout: 2000 })

      // No workspace redirect
      expect(mockNavigate).not.toHaveBeenCalled()
    },
  )

  it(
    'sessionByWorkspace is populated under wsId so enterWorkspaceChat is a no-op after redirect',
    async () => {
      // BDD: After SessionRoute attaches the session and navigates to the workspace,
      //   sessionByWorkspace['ws-1'] holds the descriptor.
      //   When enterWorkspaceChat('ws-1') then runs (on WorkspaceTabContainer mount),
      //   descriptor.id === activeSessionId → no-op (Bug 2 handoff contract).
      const detail = makeChatSessionWithWorkspace()
      _mockUseQueryData = detail
      _mockWorkspaces = [makeWorkspace('ws-1')]

      const mockConn = makeMockConnection()
      useConnectionStore.setState({ connection: mockConn as never, isConnected: true })
      // Pre-bind workspace so sessionByWorkspace writes land correctly
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })

      const Route = SessionRoute
      if (!Route) throw new Error('SessionRoute not loaded')
      await act(async () => { render(<Route />) })

      // Wait for navigate to confirm the redirect happened
      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalled()
      })

      // After redirect, sessionByWorkspace['ws-1'] must hold the descriptor
      const sessionState = useSessionStore.getState()
      const descriptor = sessionState.sessionByWorkspace['ws-1']
      expect(descriptor).toBeTruthy()
      expect(descriptor?.id).toBe(mockSessionId)

      // Simulate enterWorkspaceChat — it must be a no-op since descriptor.id === activeSessionId
      const activeSessionBefore = sessionState.activeSessionId
      sessionState.enterWorkspaceChat('ws-1')
      const activeSessionAfter = useSessionStore.getState().activeSessionId
      // Session unchanged
      expect(activeSessionAfter).toBe(activeSessionBefore)
    },
  )
})
