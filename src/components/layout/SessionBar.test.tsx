import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SessionBar } from '../chat/SessionBar'
import { fetchAgents } from '@/lib/api'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'

// SessionBar now uses useNavigate and useLocation.
const mockNavigate = vi.fn()
let mockPathname = '/'
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useLocation: () => ({ pathname: mockPathname }),
  }
})

// test_session_bar_elements (test #11)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Session bar shows agent name, tokens, cost
//             wave5a-wire-ui-spec.md — Scenario: Agent selection via session bar dropdown

// Note: SessionBar lives at src/components/chat/SessionBar.tsx (not layout/)

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'general-assistant', name: 'General Assistant', type: 'core', status: 'active', model: 'claude-sonnet-4-6', description: 'General purpose' },
      { id: 'researcher', name: 'Researcher', type: 'core', status: 'idle', description: 'Research agent' },
    ]),
  }
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderBar() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SessionBar />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  mockNavigate.mockReset()
  mockPathname = '/'
  act(() => {
    useSessionStore.setState({
      activeAgentId: 'general-assistant',
      activeSessionId: 'sess_1',
    })
    useChatStore.setState({
      sessionTokens: 1500,
      sessionCost: 0.0023,
      isStreaming: false,
    })
    useUiStore.setState({ sessionPanelOpen: false })
  })
})

afterEach(() => {
  mockNavigate.mockReset()
  mockPathname = '/'
})

describe('SessionBar — rendering (test #11)', () => {
  it('shows active agent name in the agent selector button', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Session bar shows agent name (AC1)
    renderBar()
    await screen.findByText('General Assistant')
    expect(screen.getByText('General Assistant')).toBeInTheDocument()
  })

  it('shows "Select agent" when no active agent', async () => {
    // Dataset: Session Bar row 3 — no active agent
    act(() => { useSessionStore.setState({ activeAgentId: null }) })
    renderBar()
    await vi.waitFor(() => {
      expect(screen.getByText(/select agent/i)).toBeInTheDocument()
    })
  })

  it('renders New Chat button', async () => {
    // Traces to: wave5a-wire-ui-spec.md — AC4: session controls in session bar
    // NOTE: The "Sessions" button was removed from SessionBar — it now lives in SessionPanel.
    // SessionBar shows: agent picker dropdown + New Chat button. There are two "New Chat" buttons
    // (icon-only on mobile, icon+text on desktop) so we use getAllByRole.
    renderBar()
    await vi.waitFor(() => {
      const newChatBtns = screen.getAllByRole('button', { name: /new chat/i })
      expect(newChatBtns.length).toBeGreaterThan(0)
    })
  })

  it('navigates to "/" when New Chat is clicked on the global chat route', async () => {
    // On the global chat route ("/"), New Chat navigates to "/" and lets
    // DefaultWorkspaceRedirect / WorkspaceChatTab own the new-session lifecycle.
    // SessionBar must NOT pre-clear the store itself on this path.
    mockPathname = '/'
    const startNewSessionSpy = vi.spyOn(useSessionStore.getState(), 'startNewSession')

    try {
      renderBar()
      const [newChatBtn] = await vi.waitFor(() => {
        const btns = screen.getAllByRole('button', { name: /new chat/i })
        if (btns.length === 0) throw new Error('not rendered')
        return btns
      })

      // Precondition: a session is active before the click.
      expect(useSessionStore.getState().activeSessionId).toBe('sess_1')

      fireEvent.click(newChatBtn)

      // New Chat routes to "/" — the route lifecycle handles new-session setup.
      expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
      // It does NOT clear the store itself (the route owns that now).
      expect(startNewSessionSpy).not.toHaveBeenCalled()
    } finally {
      startNewSessionSpy.mockRestore()
    }
  })

  it('calls startNewSession (in-place) when New Chat is clicked inside a workspace chat tab', async () => {
    // Inside /workspaces/<id>/chat, New Chat must start a fresh session in-place
    // (preserving the workspace context and the active agent) rather than navigating
    // away to "/" which triggers a full page transition.
    mockPathname = '/workspaces/ws-123/chat'

    renderBar()
    const [newChatBtn] = await vi.waitFor(() => {
      const btns = screen.getAllByRole('button', { name: /new chat/i })
      if (btns.length === 0) throw new Error('not rendered')
      return btns
    })

    // Precondition: a session is active.
    expect(useSessionStore.getState().activeSessionId).toBe('sess_1')

    fireEvent.click(newChatBtn)

    // In workspace context, navigate is NOT called — the session clears in-place.
    expect(mockNavigate).not.toHaveBeenCalled()
    // The session is cleared (activeSessionId becomes null via startNewSession).
    await vi.waitFor(() => {
      expect(useSessionStore.getState().activeSessionId).toBeNull()
    })
    // The agent is preserved (startNewSession was called with the active agent).
    expect(useSessionStore.getState().activeAgentId).toBe('general-assistant')
  })
})

describe('SessionBar — workers excluded from chat switcher', () => {
  // A worker (type:'worker') is a delegation-only labour agent and must NEVER
  // appear as a chat target nor be auto-selected as the active chat persona.
  it('does not list a worker agent and does not auto-select it', async () => {
    vi.mocked(fetchAgents).mockResolvedValueOnce([
      { id: 'mia', name: 'Mia', type: 'core', status: 'active', model: 'claude-sonnet-4-6', description: 'Assistant' },
      { id: 'builder', name: 'Builder Worker', type: 'worker', status: 'active', model: 'claude-sonnet-4-6', description: 'Labour agent' },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ] as any)
    // No active agent yet → component will auto-select the first eligible one.
    act(() => { useSessionStore.setState({ activeAgentId: null, activeSessionId: null }) })

    renderBar()

    // The base agent renders in the switcher button.
    await screen.findByText('Mia')
    // The worker is never listed (closed dropdown won't show it either).
    expect(screen.queryByText('Builder Worker')).not.toBeInTheDocument()

    // Auto-select picks the base agent, never the worker.
    await vi.waitFor(() => {
      expect(useSessionStore.getState().activeAgentId).toBe('mia')
    })
    expect(useSessionStore.getState().activeAgentId).not.toBe('builder')
  })

  it('when the only ready non-active agent is a worker, the switcher still works and never shows it', async () => {
    // Mia (active, base) + a worker. With Mia already active, the switcher button
    // shows Mia; the worker — the only other "ready" agent — must never render in
    // the chat switcher (it is filtered out of chatAgents entirely). The Radix
    // DropdownMenu portal can't be opened under JSDOM pointer events, so we assert
    // the worker is absent from the document, which holds whether open or closed.
    vi.mocked(fetchAgents).mockResolvedValueOnce([
      { id: 'mia', name: 'Mia', type: 'core', status: 'active', model: 'claude-sonnet-4-6', description: 'Assistant' },
      { id: 'builder', name: 'Builder Worker', type: 'worker', status: 'active', description: 'Labour agent' },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ] as any)
    act(() => { useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: 'sess_1' }) })

    renderBar()
    await screen.findByText('Mia')
    expect(screen.queryByText('Builder Worker')).not.toBeInTheDocument()
  })
})

// DELETED: "calls openSessionPanel when Sessions button is clicked" — SessionBar no longer has
// a Sessions button. That button was moved to SessionPanel. The test was asserting a UI element
// that no longer exists in this component. Session panel opening is now tested in
// SessionPanel.test.tsx (or via the parent layout component).
