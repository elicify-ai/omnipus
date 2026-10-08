// Sessions Open (squad decision): selecting a session from Sessions requests
// that session's Activity panel. ActivityBar opens the panel only after that
// same session is the active chat, then clears the request. A request for a
// different session must not open this chat's panel and must not be cleared.
// Expectations come from that decision, not from observed output.
// The empty-panel sentence is ActivityPanel's own idle copy.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ActivityBar } from './ActivityBar'
import { useSelectSession } from './useSelectSession'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useChatStore } from '@/store/chat'
import type { Session, Workspace } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn().mockResolvedValue([]) }
})

const navigate = vi.hoisted(() => vi.fn())
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
}))

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
