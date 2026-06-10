import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SessionBar } from '../chat/SessionBar'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'

// SessionBar now uses useNavigate (fix for #417 — New Chat must update the URL).
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => mockNavigate }
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

  it('navigates to "/" when New Chat is clicked (fix #417)', async () => {
    // Regression test: "New Chat" from a session route must navigate to '/'
    // so RootChatScreen mounts and its useEffect wires the first-send
    // navigation to /sessions/<newId>. Previously startNewSession() was
    // called directly without navigating, leaving the URL at the old session.
    renderBar()
    // Wait for the button to appear (agents query must resolve first).
    const [newChatBtn] = await vi.waitFor(() => {
      const btns = screen.getAllByRole('button', { name: /new chat/i })
      if (btns.length === 0) throw new Error('not rendered')
      return btns
    })
    fireEvent.click(newChatBtn)
    expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
  })
})

// DELETED: "calls openSessionPanel when Sessions button is clicked" — SessionBar no longer has
// a Sessions button. That button was moved to SessionPanel. The test was asserting a UI element
// that no longer exists in this component. Session panel opening is now tested in
// SessionPanel.test.tsx (or via the parent layout component).
