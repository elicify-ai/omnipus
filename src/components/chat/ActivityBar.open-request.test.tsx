// Sessions Open (squad decision): selecting a session from Sessions requests
// that session's Activity panel. ActivityBar opens the panel only after that
// same session is the active chat, then clears the request. A request for a
// different session must not open this chat's panel and must not be cleared.
// Expectations come from that decision, not from observed output.
// The empty-panel sentence is ActivityPanel's own idle copy.

import { describe, it, expect, vi, beforeEach, afterEach, beforeAll } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ActivityBar } from './ActivityBar'
import { useSelectSession } from './useSelectSession'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { SearchModal } from '@/components/search/SearchModal'
import userEvent from '@testing-library/user-event'
import { fetchSessions, fetchWorkspaces } from '@/lib/api'
import type { Session, Workspace } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn().mockResolvedValue([]), fetchSessions: vi.fn(), fetchWorkspaces: vi.fn() }
})

const navigate = vi.hoisted(() => vi.fn())
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
}))

beforeAll(() => {
  // jsdom has no scrolling implementation; the real selection/store logic
  // is deliberately left intact.
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
})

const SESSION_ID = 'session-origin'
const OTHER_ID = 'session-other'

function session(overrides: Partial<Session> = {}): Session {
  return {
    id: SESSION_ID,
    agent_id: 'mia',
    title: 'Plan the release notes',
    type: 'chat',
    status: 'active',
    workspace_id: 'ws-1',
    ...overrides,
  } as Session
}

const workspace = { id: 'ws-1', name: 'My Workspace' } as Workspace

function renderBar() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <ActivityBar />
    </QueryClientProvider>,
  )
}

describe('activity panel request', () => {
  const originalAttach = useSessionStore.getState().attachToSession

  beforeEach(() => {
    navigate.mockReset()
    useUiStore.setState({ activityPanelRequest: null })
    useSessionStore.setState({ activeSessionId: null, attachToSession: originalAttach })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useChatStore.setState({ messages: [] })
  })

  afterEach(() => {
    useSessionStore.setState({ attachToSession: originalAttach, activeSessionId: null })
    useUiStore.setState({ activityPanelRequest: null })
  })

  it('stores the requested session id', () => {
    useUiStore.getState().requestActivityPanel(SESSION_ID)
    expect(useUiStore.getState().activityPanelRequest).toBe(SESSION_ID)
  })

  it('replaces an older request so only the latest Open is pending', () => {
    useUiStore.getState().requestActivityPanel(OTHER_ID)
    useUiStore.getState().requestActivityPanel(SESSION_ID)
    expect(useUiStore.getState().activityPanelRequest).toBe(SESSION_ID)
  })

  it('clears the request only when the consumed id is the one that was requested', () => {
    useUiStore.getState().requestActivityPanel(SESSION_ID)
    useUiStore.getState().consumeActivityPanelRequest(OTHER_ID)
    expect(useUiStore.getState().activityPanelRequest).toBe(SESSION_ID)
    useUiStore.getState().consumeActivityPanelRequest(SESSION_ID)
    expect(useUiStore.getState().activityPanelRequest).toBeNull()
  })

  it('opens the Activity panel once the requested session is the active chat, then clears the request', async () => {
    useSessionStore.setState({ activeSessionId: SESSION_ID })
    useUiStore.getState().requestActivityPanel(SESSION_ID)
    renderBar()
    await waitFor(() => {
      expect(screen.getByText('No background activity yet.')).toBeInTheDocument()
    })
    expect(useUiStore.getState().activityPanelRequest).toBeNull()
  })

  it('does not open the panel, and keeps the request, when the active chat is a different session', async () => {
    useSessionStore.setState({ activeSessionId: SESSION_ID })
    useUiStore.getState().requestActivityPanel(OTHER_ID)
    renderBar()
    await act(async () => {
      await Promise.resolve()
    })
    expect(screen.queryByText('No background activity yet.')).not.toBeInTheDocument()
    expect(useUiStore.getState().activityPanelRequest).toBe(OTHER_ID)
  })

  it.each([true, false])('wires origin-row Open through real attach, request store and ActivityBar when transport send=%s', async (sent) => {
    // FR-036 / DEP-ACT: neither useSelectSession nor its onSelected callback
    // is mocked. Only the transport send and HTTP listing are external edges.
    const send = vi.fn(() => sent)
    useConnectionStore.setState({ connection: { send } as unknown as NonNullable<ReturnType<typeof useConnectionStore.getState>['connection']>, isConnected: true, connectionError: null })
    useSessionStore.setState({ activeSessionId: OTHER_ID, attachToSession: originalAttach })
    useUiStore.setState({ searchModalOpen: true, searchModalMode: 'sessions', searchModalWorkspaceFilter: null, toasts: [] })
    vi.mocked(fetchSessions).mockResolvedValue([session({ background_command_count: 2 })])
    vi.mocked(fetchWorkspaces).mockResolvedValue([workspace])
    const requests: string[] = []
    const unsubscribe = useUiStore.subscribe((current, previous) => {
      if (current.activityPanelRequest !== previous.activityPanelRequest && current.activityPanelRequest) requests.push(current.activityPanelRequest)
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    try {
      render(<QueryClientProvider client={client}><SearchModal /><ActivityBar /></QueryClientProvider>)
      expect(await screen.findByText('2 background commands running')).toBeInTheDocument()
      expect(screen.queryByText('No background activity yet.')).not.toBeInTheDocument()
      await userEvent.click(screen.getByRole('button', { name: 'Open Plan the release notes' }))
      expect(send).toHaveBeenCalledTimes(1)
      expect(send).toHaveBeenCalledWith(expect.objectContaining({ type: 'attach_session', session_id: SESSION_ID }))
      if (sent) {
        expect(useSessionStore.getState().activeSessionId).toBe(SESSION_ID)
        expect(requests).toEqual([SESSION_ID])
        expect(await screen.findByText('No background activity yet.')).toBeInTheDocument()
        expect(useUiStore.getState().activityPanelRequest).toBeNull()
        expect(useUiStore.getState().searchModalOpen).toBe(false)
      } else {
        expect(requests).toEqual([])
        expect(useSessionStore.getState().activeSessionId).toBe(OTHER_ID)
        expect(useUiStore.getState().activityPanelRequest).toBeNull()
        expect(screen.queryByText('No background activity yet.')).not.toBeInTheDocument()
        expect(useUiStore.getState().searchModalOpen).toBe(true)
        expect(useUiStore.getState().toasts.map((toast) => toast.message)).toContain("Connection lost — couldn't open that session. It will not be switched.")
      }
    } finally {
      unsubscribe()
      useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    }
  })

  it('does not consume a real origin-row request in the wrong chat and opens it only after the matching attach', async () => {
    const send = vi.fn(() => true)
    useConnectionStore.setState({ connection: { send } as unknown as NonNullable<ReturnType<typeof useConnectionStore.getState>['connection']>, isConnected: true })
    useUiStore.setState({ searchModalOpen: true, searchModalMode: 'sessions', searchModalWorkspaceFilter: null })
    vi.mocked(fetchSessions).mockResolvedValue([session({ background_command_count: 2 })])
    vi.mocked(fetchWorkspaces).mockResolvedValue([workspace])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    try {
      render(<QueryClientProvider client={client}><SearchModal /></QueryClientProvider>)
      await userEvent.click(await screen.findByRole('button', { name: 'Open Plan the release notes' }))
      expect(useUiStore.getState().activityPanelRequest).toBe(SESSION_ID)
      act(() => { originalAttach(OTHER_ID, 'chat', 'Other chat', 'mia') })
      renderBar()
      await act(async () => { await Promise.resolve() })
      expect(screen.queryByText('No background activity yet.')).not.toBeInTheDocument()
      expect(useUiStore.getState().activityPanelRequest).toBe(SESSION_ID)
      act(() => { originalAttach(SESSION_ID, 'chat', 'Plan the release notes', 'mia') })
      expect(await screen.findByText('No background activity yet.')).toBeInTheDocument()
      expect(useUiStore.getState().activityPanelRequest).toBeNull()
    } finally {
      useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    }
  })

  it('requests the Activity panel only after the session attach succeeds', () => {
    useSessionStore.setState({ attachToSession: vi.fn(() => true) })
    const onSelected = vi.fn()
    const { result } = renderHook(() => useSelectSession({
      agents: [],
      workspaces: [workspace],
      onClose: vi.fn(),
      onSelected,
    }))
    act(() => {
      result.current(session())
    })
    expect(onSelected).toHaveBeenCalledTimes(1)
    expect(onSelected).toHaveBeenCalledWith(expect.objectContaining({ id: SESSION_ID }))
  })

  it('does not request the Activity panel when the attach fails', () => {
    useSessionStore.setState({ attachToSession: vi.fn(() => false) })
    const onSelected = vi.fn()
    const { result } = renderHook(() => useSelectSession({
      agents: [],
      workspaces: [workspace],
      onClose: vi.fn(),
      onSelected,
    }))
    act(() => {
      result.current(session())
    })
    expect(onSelected).not.toHaveBeenCalled()
    expect(navigate).not.toHaveBeenCalled()
  })

  it('still requests the panel for an unfiled session, which attaches on its own route', () => {
    const attach = vi.fn(() => true)
    useSessionStore.setState({ attachToSession: attach })
    const onSelected = vi.fn()
    const { result } = renderHook(() => useSelectSession({
      agents: [],
      workspaces: [workspace],
      onClose: vi.fn(),
      onSelected,
    }))
    act(() => {
      result.current(session({ workspace_id: undefined }))
    })
    expect(attach).not.toHaveBeenCalled()
    expect(onSelected).toHaveBeenCalledTimes(1)
    expect(navigate).toHaveBeenCalledWith({ to: '/sessions/$sessionId', params: { sessionId: SESSION_ID } })
  })
})
