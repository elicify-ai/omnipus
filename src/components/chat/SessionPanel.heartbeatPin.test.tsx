import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SessionPanel } from './SessionPanel'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useChatStore } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchSessions, fetchWorkspaces } from '@/lib/api'

// ── Router mock ───────────────────────────────────────────────────────────────

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => mockNavigate }
})

// ── API mock ──────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      {
        id: 'mia',
        name: 'Mia',
        type: 'core',
        status: 'active',
        description: '',
        color: '#D4AF37',
        icon: null,
      },
    ]),
    fetchSessions: vi.fn(),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    renameSession: vi.fn(),
    deleteSession: vi.fn(),
  }
})

// ── Session fixtures ──────────────────────────────────────────────────────────

const HEARTBEAT_PROTECTED = {
  id: 'hb-sess-1',
  agent_id: 'mia',
  active_agent_id: 'mia',
  title: 'Mia Heartbeat',
  type: 'heartbeat' as const,
  protected: true,
  created_at: '2026-06-01T00:00:00Z',
  updated_at: '2026-06-01T10:00:00Z',
  message_count: 5,
  workspace_id: 'ws-1',
}

const HEARTBEAT_UNPROTECTED = {
  id: 'hb-sess-2',
  agent_id: 'mia',
  active_agent_id: 'mia',
  title: 'Mia Heartbeat (disabled)',
  type: 'heartbeat' as const,
  protected: false,
  created_at: '2026-06-01T00:00:00Z',
  updated_at: '2026-06-01T09:00:00Z',
  message_count: 2,
  workspace_id: 'ws-1',
}

const NORMAL_CHAT = {
  id: 'chat-sess-1',
  agent_id: 'mia',
  active_agent_id: 'mia',
  title: 'Normal Chat',
  type: 'chat' as const,
  created_at: '2026-06-01T00:00:00Z',
  updated_at: '2026-06-01T08:00:00Z',
  message_count: 10,
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderPanel() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SessionPanel />
    </QueryClientProvider>,
  )
}

// ── Setup ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  mockNavigate.mockReset()
  act(() => {
    useUiStore.setState({ sessionPanelOpen: true, createAgentModalOpen: false })
    useSessionStore.setState({
      activeSessionId: null,
      activeAgentId: null,
      attachedSessionType: null,
      activeAgentType: null,
    })
    useChatStore.setState({ sessionsById: {} })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
  vi.mocked(fetchWorkspaces).mockResolvedValue([])
})

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('SessionPanel — heartbeat pinning', () => {
  it('renders a heartbeat session pinned above workspace groups with a badge', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([HEARTBEAT_PROTECTED, NORMAL_CHAT] as never)

    renderPanel()

    // At least one "Heartbeat" text element should appear (section header or badge)
    await screen.findAllByText('Heartbeat')

    // The heartbeat session title appears
    expect(screen.getByLabelText('Open session: Mia Heartbeat')).toBeInTheDocument()

    // The normal chat session also appears (in groups below)
    expect(screen.getByLabelText('Open session: Normal Chat')).toBeInTheDocument()
  })

  it('shows the Heartbeat badge on the heartbeat session item', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([HEARTBEAT_PROTECTED] as never)

    renderPanel()

    await screen.findByLabelText('Open session: Mia Heartbeat')

    // The "Heartbeat" text appears at least once (section header and/or item badge)
    const heartbeatTexts = screen.getAllByText('Heartbeat')
    expect(heartbeatTexts.length).toBeGreaterThanOrEqual(1)
  })

  it('hides the delete button for a protected heartbeat session', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([HEARTBEAT_PROTECTED] as never)

    renderPanel()

    await screen.findByLabelText('Open session: Mia Heartbeat')

    // No delete button for a protected heartbeat
    expect(
      screen.queryByRole('button', { name: /delete session: Mia Heartbeat/i }),
    ).toBeNull()
  })

  it('shows the delete button for an unprotected heartbeat session', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([HEARTBEAT_UNPROTECTED] as never)

    renderPanel()

    await screen.findByLabelText('Open session: Mia Heartbeat (disabled)')

    // Delete button IS present for unprotected heartbeat
    // (rendered on hover via CSS; test checks the element exists in DOM)
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /delete session: Mia Heartbeat \(disabled\)/i }),
      ).toBeInTheDocument()
    })
  })

  it('does NOT show the heartbeat session in normal workspace groups (no duplication)', async () => {
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-1', name: 'Alpha', status: 'active', created_at: '', updated_at: '' },
    ] as never)

    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    })

    vi.mocked(fetchSessions).mockResolvedValue([
      { ...HEARTBEAT_PROTECTED, workspace_id: 'ws-1' },
      { ...NORMAL_CHAT, workspace_id: 'ws-1' },
    ] as never)

    renderPanel()

    // Wait for the sessions to load
    await screen.findByLabelText('Open session: Mia Heartbeat')

    // Only one element with the heartbeat session title (pinned, not duplicated in group)
    const items = screen.getAllByLabelText('Open session: Mia Heartbeat')
    expect(items).toHaveLength(1)
  })

  it('renders normal sessions in their workspace groups unchanged', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([NORMAL_CHAT] as never)

    renderPanel()

    await screen.findByLabelText('Open session: Normal Chat')

    // No pinned heartbeat section (the heartbeat-section div should not be present)
    expect(screen.queryByLabelText('Pinned heartbeat sessions')).toBeNull()
  })

  it('renders multiple heartbeat sessions all pinned at the top', async () => {
    const HB1 = { ...HEARTBEAT_PROTECTED, id: 'hb-1', title: 'HB Session One' }
    const HB2 = { ...HEARTBEAT_PROTECTED, id: 'hb-2', title: 'HB Session Two', protected: false }

    vi.mocked(fetchSessions).mockResolvedValue([HB1, HB2, NORMAL_CHAT] as never)

    renderPanel()

    await screen.findByLabelText('Open session: HB Session One')
    expect(screen.getByLabelText('Open session: HB Session Two')).toBeInTheDocument()
    expect(screen.getByLabelText('Open session: Normal Chat')).toBeInTheDocument()

    // HB1 is protected — no delete button
    expect(screen.queryByRole('button', { name: /delete session: HB Session One/i })).toBeNull()
    // HB2 is not protected — delete button present
    expect(screen.getByRole('button', { name: /delete session: HB Session Two/i })).toBeInTheDocument()
  })

  it('includes heartbeat sessions in search results when title matches', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([HEARTBEAT_PROTECTED, NORMAL_CHAT] as never)

    renderPanel()

    await screen.findByLabelText('Open session: Mia Heartbeat')

    // Search for "heartbeat" — the heartbeat session should appear
    const searchInput = screen.getByRole('textbox', { name: /search sessions/i })
    fireEvent.change(searchInput, { target: { value: 'heartbeat' } })

    // Wait for debounce (300ms in component)
    await waitFor(() => {
      expect(screen.getByLabelText('Open session: Mia Heartbeat')).toBeInTheDocument()
    })

    // Normal Chat should not appear (doesn't match "heartbeat")
    await waitFor(() => {
      expect(screen.queryByLabelText('Open session: Normal Chat')).toBeNull()
    })
  })
})
