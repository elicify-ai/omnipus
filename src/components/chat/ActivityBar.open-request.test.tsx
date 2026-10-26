// Founder ruling 2026-10-25: Sessions/History Open goes straight to chat,
// with no automatic panel. Q9/P3 explicitly retires the five unused
// request/consume-API capability cases; manual Activity stays one click away.
// Q8b removes only the remaining retired request-field dependencies. All
// five cases keep their other assertions; successful Open is verified through
// the real UI (no Activity panel and no dialog). Production removal is separate.

import { describe, it, expect, vi, beforeEach, afterEach, beforeAll } from 'vitest'
import { render, screen, act } from '@testing-library/react'
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

describe('activity panel request', () => {
  const originalAttach = useSessionStore.getState().attachToSession

  beforeEach(() => {
    navigate.mockReset()
    useSessionStore.setState({ activeSessionId: null, attachToSession: originalAttach })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useChatStore.setState({ messages: [] })
  })

  afterEach(() => {
    useSessionStore.setState({ attachToSession: originalAttach, activeSessionId: null })
  })

  it.each([false])('wires origin-row Open through real attach, request store and ActivityBar when transport send=%s', async (sent) => {
    // FR-036 / DEP-ACT: neither useSelectSession nor its onSelected callback
    // is mocked. Only the transport send and HTTP listing are external edges.
    const send = vi.fn(() => sent)
    useConnectionStore.setState({ connection: { send } as unknown as NonNullable<ReturnType<typeof useConnectionStore.getState>['connection']>, isConnected: true, connectionError: null })
    useSessionStore.setState({ activeSessionId: OTHER_ID, attachToSession: originalAttach })
    useUiStore.setState({ searchModalOpen: true, searchModalMode: 'sessions', searchModalWorkspaceFilter: null, toasts: [] })
    vi.mocked(fetchSessions).mockResolvedValue([session({ background_command_count: 2 })])
    vi.mocked(fetchWorkspaces).mockResolvedValue([workspace])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    try {
      render(<QueryClientProvider client={client}><SearchModal /><ActivityBar /></QueryClientProvider>)
      expect(await screen.findByText('2 background commands running')).toBeInTheDocument()
      expect(screen.queryByText('No background activity yet.')).not.toBeInTheDocument()
      await userEvent.click(screen.getByRole('button', { name: 'Open Plan the release notes' }))
      expect(send).toHaveBeenCalledTimes(1)
      expect(send).toHaveBeenCalledWith(expect.objectContaining({ type: 'attach_session', session_id: SESSION_ID }))
      expect(useSessionStore.getState().activeSessionId).toBe(OTHER_ID)
      expect(screen.queryByText('No background activity yet.')).not.toBeInTheDocument()
      expect(useUiStore.getState().searchModalOpen).toBe(true)
      expect(useUiStore.getState().toasts.map((toast) => toast.message)).toContain("Connection lost — couldn't open that session. It will not be switched.")
    } finally {
      useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    }
  })


  it('Sessions Open attaches successfully without requesting or opening the Activity panel', async () => {
    const send = vi.fn(() => true)
    useConnectionStore.setState({ connection: { send } as unknown as NonNullable<ReturnType<typeof useConnectionStore.getState>['connection']>, isConnected: true, connectionError: null })
    useSessionStore.setState({ activeSessionId: OTHER_ID, attachToSession: originalAttach })
    useUiStore.setState({ searchModalOpen: true, searchModalMode: 'sessions', searchModalWorkspaceFilter: null, searchModalAgentFilter: null, toasts: [] })
    vi.mocked(fetchSessions).mockResolvedValue([session({ background_command_count: 2 })])
    vi.mocked(fetchWorkspaces).mockResolvedValue([workspace])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    try {
      render(<QueryClientProvider client={client}><SearchModal /><ActivityBar /></QueryClientProvider>)
      expect(await screen.findByText('2 background commands running')).toBeInTheDocument()
      await userEvent.click(screen.getByRole('button', { name: 'Open Plan the release notes' }))
      await act(async () => { await Promise.resolve() })
      expect(send).toHaveBeenCalledTimes(1)
      expect(send).toHaveBeenCalledWith(expect.objectContaining({ type: 'attach_session', session_id: SESSION_ID }))
      expect(useSessionStore.getState().activeSessionId, 'a no-panel check cannot pass because Open failed').toBe(SESSION_ID)
      expect(useUiStore.getState().searchModalOpen).toBe(false)
      expect(screen.queryByText('No background activity yet.'), 'Open must not open the Activity panel automatically').not.toBeInTheDocument()
      expect(screen.queryByRole('dialog'), 'no modal panel is left over the opened chat').toBeNull()
    } finally {
      client.clear()
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
