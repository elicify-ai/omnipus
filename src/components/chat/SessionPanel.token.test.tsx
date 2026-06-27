/**
 * SessionPanel token chip tests.
 *
 * Verifies:
 *   1. A session row with total_tokens > 0 renders a token chip.
 *   2. A session row with 0 or undefined total_tokens does NOT render a chip.
 *   3. seedSessionTokens is called with total_tokens when a session is selected.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Session } from '@/lib/api'

// ── Mocks ──────────────────────────────────────────────────────────────────────

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => vi.fn(),
    useLocation: () => ({ pathname: '/' }),
    Link: ({ children, to, ...rest }: { children?: React.ReactNode; to?: string } & Record<string, unknown>) => (
      <a href={typeof to === 'string' ? to : '#'} {...(rest as Record<string, unknown>)}>
        {children}
      </a>
    ),
  }
})

const mockSeedSessionTokens = vi.fn()
const mockAttachToSession = vi.fn()

vi.mock('@/store/chat', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/chat')>()
  return {
    ...actual,
    useChatStore: vi.fn((selector?: (state: Record<string, unknown>) => unknown) => {
      const state = {
        sessionsById: {},
        seedSessionTokens: mockSeedSessionTokens,
      }
      return selector ? selector(state as unknown as Parameters<typeof selector>[0]) : state
    }),
  }
})

vi.mock('@/store/session', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/session')>()
  return {
    ...actual,
    useSessionStore: vi.fn(() => ({
      activeSessionId: null,
      activeAgentId: null,
      setActiveSession: vi.fn(),
      attachToSession: mockAttachToSession,
      setActiveAgentType: vi.fn(),
    })),
  }
})

vi.mock('@/store/ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/ui')>()
  return {
    ...actual,
    useUiStore: vi.fn(() => ({
      sessionPanelOpen: true,
      closeSessionPanel: vi.fn(),
      addToast: vi.fn(),
    })),
  }
})

vi.mock('@/store/workspacesStore', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/workspacesStore')>()
  return {
    ...actual,
    useWorkspacesStore: vi.fn(() => null),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessions: vi.fn().mockResolvedValue([
      {
        id: 'sess-with-tokens',
        agent_id: 'agent-1',
        title: 'Rich session',
        type: 'chat',
        created_at: '2026-06-01T00:00:00Z',
        updated_at: '2026-06-02T00:00:00Z',
        message_count: 5,
        total_tokens: 44000,
      } as Session,
      {
        id: 'sess-no-tokens',
        agent_id: 'agent-1',
        title: 'Empty session',
        type: 'chat',
        created_at: '2026-06-01T00:00:00Z',
        updated_at: '2026-06-02T00:00:00Z',
        message_count: 0,
        total_tokens: 0,
      } as Session,
      {
        id: 'sess-undefined-tokens',
        agent_id: 'agent-1',
        title: 'Undefined session',
        type: 'chat',
        created_at: '2026-06-01T00:00:00Z',
        updated_at: '2026-06-02T00:00:00Z',
        message_count: 0,
      } as Session,
    ]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    renameSession: vi.fn(),
    deleteSession: vi.fn(),
    isApiError: vi.fn().mockReturnValue(false),
    workspacesQueryKeys: {
      list: (args?: Record<string, unknown>) => ['workspaces', args ?? {}],
      sessions: (id: string) => ['workspace-sessions', id],
    },
  }
})

import { SessionPanel } from './SessionPanel'

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <SessionPanel />
    </QueryClientProvider>,
  )
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('SessionPanel token chip', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders token chip for session with total_tokens > 0', async () => {
    renderPanel()
    await waitFor(() => expect(screen.getByText('Rich session')).toBeInTheDocument())
    const chips = screen.getAllByTestId('session-token-chip')
    // Only one chip for the session with tokens > 0
    expect(chips).toHaveLength(1)
    expect(chips[0].textContent).toBe('44.0k')
  })

  it('does not render token chip for session with total_tokens = 0', async () => {
    renderPanel()
    await waitFor(() => expect(screen.getByText('Empty session')).toBeInTheDocument())
    // Verify "Empty session" row exists but has no chip
    const chips = screen.getAllByTestId('session-token-chip')
    // Only 1 chip (for 44k session), not 2 or 3
    expect(chips).toHaveLength(1)
  })

  it('does not render token chip for session with undefined total_tokens', async () => {
    renderPanel()
    await waitFor(() => expect(screen.getByText('Undefined session')).toBeInTheDocument())
    const chips = screen.getAllByTestId('session-token-chip')
    expect(chips).toHaveLength(1)
  })

  it('calls seedSessionTokens with total_tokens when selecting a session with tokens', async () => {
    renderPanel()
    await waitFor(() => expect(screen.getByText('Rich session')).toBeInTheDocument())
    fireEvent.click(screen.getByLabelText('Open session: Rich session'))
    await waitFor(() => expect(mockSeedSessionTokens).toHaveBeenCalledWith(44000))
  })

  it('does not call seedSessionTokens when total_tokens is 0', async () => {
    renderPanel()
    await waitFor(() => expect(screen.getByText('Empty session')).toBeInTheDocument())
    fireEvent.click(screen.getByLabelText('Open session: Empty session'))
    await waitFor(() => expect(mockAttachToSession).toHaveBeenCalled())
    expect(mockSeedSessionTokens).not.toHaveBeenCalled()
  })
})
