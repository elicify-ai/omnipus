import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SessionPanel } from './SessionPanel'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchSessions, fetchWorkspaces } from '@/lib/api'

// W2-1: SessionPanel chat-session routing regression test.
//
// Tests that handleSelectSession always routes through attachToSession (never
// setActiveSession — which would trigger the REST-clobber bug fixed in c76ac73).
//
// Traces to: temporal-puzzling-melody.md W2-1
// Traces to: sprint-i-historical-replay-fidelity-spec.md FR-I-014

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      {
        id: 'agent-chat-1',
        name: 'Chat Agent',
        type: 'core',
        status: 'active',
        description: 'Chat agent',
        color: '#ff0000',
        icon: null,
      },
      {
        id: 'agent-task-1',
        name: 'Task Agent',
        type: 'custom',
        status: 'active',
        description: 'Task agent',
        color: '#00ff00',
        icon: null,
      },
    ]),
    fetchSessions: vi.fn().mockResolvedValue([
      {
        id: 'sess-chat-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Chat Session One',
        type: 'chat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 3,
      },
      {
        id: 'sess-task-1',
        agent_id: 'agent-task-1',
        active_agent_id: 'agent-task-1',
        title: 'Task Session One',
        type: 'task',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 5,
      },
    ]),
    createSession: vi.fn(),
    // Default: no workspaces. Individual workspace-grouping tests override via
    // mockResolvedValueOnce. With no active workspace the panel uses unscoped mode.
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
  }
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderPanel() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SessionPanel />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  mockNavigate.mockReset()
  act(() => {
    useUiStore.setState({ sessionPanelOpen: true, createAgentModalOpen: false })
    useSessionStore.setState({
      activeSessionId: null,
      activeAgentId: null,
      activeAgentType: null,
    })
    // Reset workspace scope to "no active workspace" so the bulk of the panel
    // tests exercise the unscoped path; workspace-scoping tests opt in.
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
})

// W2-1 case 1: chat-type session routes through attachToSession, not setActiveSession.
describe('SessionPanel — handleSelectSession routing guard (W2-1)', () => {
  it('clicking a chat-type session calls attachToSession with correct args, never setActiveSession', async () => {
    // BDD: Given a SessionPanel with a chat session
    // BDD: When the user clicks the chat session
    // BDD: Then attachToSession(session.id, 'chat', title, agentId) is called exactly once
    // BDD: And setActiveSession is NOT invoked
    // Traces to: temporal-puzzling-melody.md W2-1

    const attachToSessionSpy = vi.fn()
    const setActiveSessionSpy = vi.fn()

    act(() => {
      useSessionStore.setState({
        attachToSession: attachToSessionSpy,
        setActiveSession: setActiveSessionSpy,
      } as unknown as Parameters<typeof useSessionStore.setState>[0])
    })

    renderPanel()

    // Wait for sessions to load
    const chatSessionItem = await screen.findByText('Chat Session One')
    fireEvent.click(chatSessionItem)

    // attachToSession must have been called exactly once
    expect(attachToSessionSpy).toHaveBeenCalledTimes(1)
    expect(attachToSessionSpy).toHaveBeenCalledWith(
      'sess-chat-1',
      'chat',
      'Chat Session One',
      'agent-chat-1',
    )

    // setActiveSession must NOT have been called (REST-clobber bug path)
    expect(setActiveSessionSpy).not.toHaveBeenCalled()
  })

  it('clicking a task-type session calls attachToSession with type=task, never setActiveSession', async () => {
    // BDD: Given a SessionPanel with a task session
    // BDD: When the user clicks the task session
    // BDD: Then attachToSession(session.id, 'task', title, agentId) is called exactly once
    // BDD: And setActiveSession is NOT invoked
    // Traces to: temporal-puzzling-melody.md W2-1

    const attachToSessionSpy = vi.fn()
    const setActiveSessionSpy = vi.fn()

    act(() => {
      useSessionStore.setState({
        attachToSession: attachToSessionSpy,
        setActiveSession: setActiveSessionSpy,
      } as unknown as Parameters<typeof useSessionStore.setState>[0])
    })

    renderPanel()

    const taskSessionItem = await screen.findByText('Task Session One')
    fireEvent.click(taskSessionItem)

    expect(attachToSessionSpy).toHaveBeenCalledTimes(1)
    expect(attachToSessionSpy).toHaveBeenCalledWith(
      'sess-task-1',
      'task',
      'Task Session One',
      'agent-task-1',
    )

    // Must not have called setActiveSession
    expect(setActiveSessionSpy).not.toHaveBeenCalled()
  })

  it('clicking a chat session sets activeAgentType from the clicked agent type', async () => {
    // BDD: Given a chat session whose agent has type="core"
    // BDD: When the user clicks that session
    // BDD: Then activeAgentType is set to 'core' on the session store
    // Traces to: temporal-puzzling-melody.md W2-1

    const attachToSessionSpy = vi.fn()

    act(() => {
      useSessionStore.setState({
        attachToSession: attachToSessionSpy,
      } as unknown as Parameters<typeof useSessionStore.setState>[0])
    })

    renderPanel()
    await screen.findByText('Chat Session One')
    fireEvent.click(screen.getByText('Chat Session One'))

    // The agent for sess-chat-1 has type 'core'
    await waitFor(() => {
      // activeAgentType should reflect the agent type of the clicked session's agent
      // (set via useSessionStore.setState({ activeAgentType: agent.type }) in handleSelectSession)
      const state = useSessionStore.getState()
      expect(state.activeAgentType).toBe('core')
    })
  })

  it('two different session clicks call attachToSession with two different session IDs (differentiation test)', async () => {
    // Differentiation test: two different inputs produce two different outputs.
    // Guards against a hardcoded response that always calls with the same ID.
    // Traces to: temporal-puzzling-melody.md W2-1

    const calls: string[] = []
    const attachToSessionSpy = vi.fn().mockImplementation((sessionId: string) => {
      calls.push(sessionId)
    })

    act(() => {
      useSessionStore.setState({
        attachToSession: attachToSessionSpy,
      } as unknown as Parameters<typeof useSessionStore.setState>[0])
    })

    renderPanel()

    await screen.findByText('Chat Session One')
    fireEvent.click(screen.getByText('Chat Session One'))

    // Re-open panel for second click
    act(() => { useUiStore.setState({ sessionPanelOpen: true }) })

    await screen.findByText('Task Session One')
    fireEvent.click(screen.getByText('Task Session One'))

    expect(calls[0]).toBe('sess-chat-1')
    expect(calls[1]).toBe('sess-task-1')
    expect(calls[0]).not.toBe(calls[1])
  })
})

// F-S11: per-session streaming dot for non-foreground sessions
describe('SessionPanel — per-session isStreaming dot (F-S11)', () => {
  it('shows a pulse dot for a background session that is streaming', async () => {
    // BDD: Given sess-chat-1 is NOT the active session (background)
    // BDD: And sess-chat-1 has isStreaming=true in its bucket
    // BDD: Then the SessionItem for sess-chat-1 shows an aria-label="Generating" element
    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess-task-1' })
      // Seed sessionsById so the background session appears streaming
      useChatStore.setState((s) => ({
        ...s,
        sessionsById: {
          ...s.sessionsById,
          'sess-chat-1': {
            ...makeBucketMessages([]),
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            pendingApprovals: [],
            isStreaming: true,
            isReplaying: false,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            cancelStage: null,
            lastUserMessageAt: null,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          },
        },
      }))
    })

    renderPanel()

    await screen.findByText('Chat Session One')

    // The pulse dot has aria-label="Generating" and is rendered for non-active streaming sessions
    const dot = await screen.findByLabelText('Generating')
    expect(dot).toBeTruthy()
  })

  it('does NOT show the pulse dot for the active session even when it is streaming', async () => {
    // BDD: Given sess-chat-1 IS the active session
    // BDD: And it is streaming — the active session shows the green "active" dot, not the pulse dot
    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess-chat-1' })
      useChatStore.setState((s) => ({
        ...s,
        sessionsById: {
          ...s.sessionsById,
          'sess-chat-1': {
            ...makeBucketMessages([]),
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            pendingApprovals: [],
            isStreaming: true,
            isReplaying: false,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            cancelStage: null,
            lastUserMessageAt: null,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          },
        },
      }))
    })

    renderPanel()

    await screen.findByText('Chat Session One')

    // Pulse dot must NOT be present for the active session
    const dots = screen.queryAllByLabelText('Generating')
    expect(dots).toHaveLength(0)
  })
})

// ── Workspace grouping tests ──────────────────────────────────────────────────
//
// Grouping is now driven by each session's own workspace_id field (set by the
// backend and passed through rawToSession). The panel is always scoped to the
// active workspace's sessions + a "No workspace" group for orphaned/unlinked
// sessions. Other workspaces' sessions are excluded from view.

// Helper workspaces for workspace grouping tests
const wsGroupWorkspaces = [
  { id: 'ws-alpha', name: 'Alpha Project', is_default: false, status: 'active' as const, pinned: false, pin_order: 0, task_count: 0, created_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z' },
  { id: 'ws-beta',  name: 'Beta Project',  is_default: false, status: 'active' as const, pinned: false, pin_order: 0, task_count: 0, created_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z' },
]

// Test A: active workspace's session renders under its group header
describe('SessionPanel — workspace grouping: single workspace renders group header (Test A)', () => {
  beforeEach(() => {
    vi.mocked(fetchWorkspaces).mockResolvedValue([wsGroupWorkspaces[0]] as never)
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-ws1-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Alpha Project Session',
        type: 'chat' as const,
        workspace_id: 'ws-alpha',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 2,
      },
    ])
    // Set ws-alpha as active so its session is visible.
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-alpha' })
    })
  })

  it('renders a collapsible workspace group header even when there is only one workspace', async () => {
    // BDD: Given all sessions belong to the active workspace (via workspace_id)
    // BDD: Then showGroups=true and a collapsible group header button is rendered
    // BDD: And the session appears under that group header
    renderPanel()

    const groupHeader = await screen.findByRole('button', { name: /Alpha Project workspace sessions/i })
    expect(groupHeader).toBeTruthy()
    expect(groupHeader).toHaveAttribute('aria-expanded', 'true')

    await screen.findByLabelText('Open session: Alpha Project Session')
  })

  it('collapses and expands the single workspace group on click', async () => {
    // BDD: Given a single-workspace panel (Alpha Project)
    // BDD: When the user clicks the group header
    // BDD: Then the group collapses (session leaves the DOM) and aria-expanded=false
    // BDD: When the user clicks again, the group expands
    renderPanel()

    const groupHeader = await screen.findByRole('button', { name: /Alpha Project workspace sessions/i })

    // Initially expanded
    await screen.findByLabelText('Open session: Alpha Project Session')

    // Collapse
    fireEvent.click(groupHeader)
    await waitFor(() => {
      expect(screen.queryByLabelText('Open session: Alpha Project Session')).toBeNull()
    })
    expect(groupHeader).toHaveAttribute('aria-expanded', 'false')

    // Expand again
    fireEvent.click(groupHeader)
    await screen.findByLabelText('Open session: Alpha Project Session')
    expect(groupHeader).toHaveAttribute('aria-expanded', 'true')
  })

  it('renders sessions under "No workspace" group header when sessions have no workspace_id', async () => {
    // BDD: Given sessions have no workspace_id (legacy/global sessions) and no active workspace
    // BDD: Then all sessions land in the "No workspace" fallback group
    vi.mocked(fetchWorkspaces).mockResolvedValue([] as never)
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-legacy-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Legacy Session One',
        type: 'chat' as const,
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 1,
      },
    ])
    // No active workspace: all sessions show as "No workspace" fallback.
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: null })
    })

    renderPanel()

    // "No workspace" group header must be present
    const noWsHeader = await screen.findByRole('button', { name: /No workspace workspace sessions/i })
    expect(noWsHeader).toBeTruthy()
    expect(noWsHeader).toHaveAttribute('aria-expanded', 'true')

    await screen.findByLabelText('Open session: Legacy Session One')
  })
})

// Test B: no active workspace → all sessions appear in "No workspace" fallback
describe('SessionPanel — workspace grouping: no active workspace renders "No workspace" group (Test B)', () => {
  beforeEach(() => {
    vi.mocked(fetchWorkspaces).mockResolvedValue(wsGroupWorkspaces as never)
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-ws1-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Alpha Project Session',
        type: 'chat' as const,
        workspace_id: 'ws-alpha',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 2,
      },
      {
        id: 'sess-ws2-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Beta Project Session',
        type: 'chat' as const,
        workspace_id: 'ws-beta',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 3,
      },
    ])
    // No active workspace: both sessions are shown in "No workspace" fallback.
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: null })
    })
  })

  it('renders all sessions under "No workspace" when no active workspace is set', async () => {
    // BDD: Given no active workspace and sessions exist
    // BDD: Then all sessions appear in the "No workspace" fallback group
    renderPanel()

    const noWsHeader = await screen.findByRole('button', { name: /No workspace workspace sessions/i })
    expect(noWsHeader).toBeTruthy()
  })

  it('renders both session items visible under the "No workspace" fallback group', async () => {
    renderPanel()

    await screen.findByLabelText('Open session: Alpha Project Session')
    await screen.findByLabelText('Open session: Beta Project Session')
  })

  it('shows count badge of 2 for the "No workspace" fallback group', async () => {
    renderPanel()

    await screen.findByRole('button', { name: /No workspace workspace sessions/i })

    const badges = screen.getAllByText('2')
    expect(badges.length).toBeGreaterThanOrEqual(1)
  })
})

// Test C: workspace group collapse/expand toggle
describe('SessionPanel — workspace grouping: collapse/expand toggle (Test C)', () => {
  beforeEach(() => {
    vi.mocked(fetchWorkspaces).mockResolvedValue(wsGroupWorkspaces as never)
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-ws1-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Alpha Project Session',
        type: 'chat' as const,
        workspace_id: 'ws-alpha',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 2,
      },
      {
        id: 'sess-ws2-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Beta Project Session',
        type: 'chat' as const,
        workspace_id: 'ws-beta',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 3,
      },
    ])
    // No active workspace: all sessions in "No workspace" fallback.
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: null })
    })
  })

  it('collapses the "No workspace" group on first click and expands it on second click', async () => {
    // BDD: Given a panel with a "No workspace" fallback group
    // BDD: When the user clicks the group header it collapses
    // BDD: When the user clicks again it expands
    renderPanel()

    const noWsHeader = await screen.findByRole('button', { name: /No workspace workspace sessions/i })

    // Initially expanded
    await screen.findByLabelText('Open session: Alpha Project Session')
    expect(noWsHeader).toHaveAttribute('aria-expanded', 'true')

    // Collapse
    fireEvent.click(noWsHeader)

    await waitFor(() => {
      expect(screen.queryByLabelText('Open session: Alpha Project Session')).toBeNull()
    })
    expect(noWsHeader).toHaveAttribute('aria-expanded', 'false')

    // Expand again
    fireEvent.click(noWsHeader)

    await screen.findByLabelText('Open session: Alpha Project Session')
    expect(noWsHeader).toHaveAttribute('aria-expanded', 'true')
  })
})

// Test D: search filters sessions
describe('SessionPanel — workspace grouping: search filters (Test D)', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.mocked(fetchWorkspaces).mockResolvedValue(wsGroupWorkspaces as never)
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-ws1-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Alpha Project Session',
        type: 'chat' as const,
        workspace_id: 'ws-alpha',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 2,
      },
      {
        id: 'sess-ws2-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Beta Project Session',
        type: 'chat' as const,
        workspace_id: 'ws-beta',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 3,
      },
    ])
    // No active workspace: both sessions visible in fallback group.
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: null })
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('hides sessions that do not match the search term', async () => {
    // BDD: Given both sessions visible
    // BDD: When user types "Alpha" (only matches "Alpha Project Session")
    // BDD: Then "Beta Project Session" disappears
    renderPanel()

    await screen.findByLabelText('Open session: Alpha Project Session')
    await screen.findByLabelText('Open session: Beta Project Session')

    const searchInput = screen.getByRole('textbox', { name: /search sessions/i })
    fireEvent.change(searchInput, { target: { value: 'Alpha' } })

    act(() => {
      vi.advanceTimersByTime(350)
    })

    await waitFor(() => {
      expect(screen.queryByLabelText('Open session: Beta Project Session')).toBeNull()
    })

    expect(screen.getByLabelText('Open session: Alpha Project Session')).toBeTruthy()
  })
})

// Test E: sessions with no workspace_id appear under "No workspace" alongside workspace-scoped ones
describe('SessionPanel — workspace grouping: ungrouped sessions go to "No workspace" (Test E)', () => {
  it('renders sessions without workspace_id under "No workspace" alongside workspace-linked sessions', async () => {
    // BDD: Given active workspace is ws-alpha
    //      And one session has workspace_id='ws-alpha', another has no workspace_id
    //      Then two groups appear: "Alpha Project" and "No workspace"
    vi.mocked(fetchWorkspaces).mockResolvedValue([wsGroupWorkspaces[0]] as never)
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-ws1-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Alpha Project Session',
        type: 'chat' as const,
        workspace_id: 'ws-alpha',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 2,
      },
      {
        id: 'sess-unlinked-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Unlinked Session',
        type: 'chat' as const,
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T03:00:00Z',
        message_count: 1,
      },
    ])
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-alpha' })
    })

    renderPanel()

    // Both workspace group headers must appear
    const alphaHeader = await screen.findByRole('button', { name: /Alpha Project workspace sessions/i })
    const noWsHeader  = await screen.findByRole('button', { name: /No workspace workspace sessions/i })
    expect(alphaHeader).toBeTruthy()
    expect(noWsHeader).toBeTruthy()

    // Both sessions are visible under their respective groups
    await screen.findByLabelText('Open session: Alpha Project Session')
    await screen.findByLabelText('Open session: Unlinked Session')
  })
})

// ── Workspace scoping ────────────────────────────────────────────────────────
//
// The panel always scopes to the active workspace + "No workspace" for orphans.
// Sessions belonging to OTHER known workspaces are excluded from view.
describe('SessionPanel — workspace scoping', () => {
  it('default workspace shows sessions without workspace_id under "No workspace"', async () => {
    // BDD: Given the active workspace is the default workspace
    //      And a session exists that has NO workspace_id
    //      Then the session is listed under "No workspace" (orphan group)
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-default', name: 'Inbox', is_default: true } as never,
    ])
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-global-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Global Unlinked Session',
        type: 'chat',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 1,
      },
    ])

    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-default' })
    })

    renderPanel()

    expect(
      await screen.findByLabelText('Open session: Global Unlinked Session'),
    ).toBeTruthy()
  })

  it('non-default workspace scopes to sessions with matching workspace_id; unlinked sessions appear under "No workspace"; other workspace sessions are hidden', async () => {
    // BDD: Given the active workspace is ws-project (NON-default)
    //      And a session with workspace_id='ws-project', a session without workspace_id,
    //      and a session with workspace_id='ws-default' (another known workspace) all exist
    //      Then "Linked Project Session" is visible (active workspace group)
    //      And "Unlinked Other Session" is visible under "No workspace" (orphan group)
    //      And "Inbox Session" (ws-default) is NOT shown (other workspace excluded)
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-default', name: 'Inbox', is_default: true } as never,
      { id: 'ws-project', name: 'Project', is_default: false } as never,
    ])
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-linked-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Linked Project Session',
        type: 'chat',
        workspace_id: 'ws-project',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 1,
      },
      {
        id: 'sess-other-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Unlinked Other Session',
        type: 'chat',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 1,
      },
      {
        id: 'sess-inbox-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Inbox Session',
        type: 'chat',
        workspace_id: 'ws-default',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T03:00:00Z',
        message_count: 1,
      },
    ])

    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-project' })
    })

    renderPanel()

    // Active workspace session is visible
    expect(
      await screen.findByLabelText('Open session: Linked Project Session'),
    ).toBeTruthy()

    // Unlinked session appears under "No workspace" — visible
    expect(
      await screen.findByLabelText('Open session: Unlinked Other Session'),
    ).toBeTruthy()

    // Session belonging to another known workspace (ws-default) is NOT shown
    await waitFor(() => {
      expect(
        screen.queryByLabelText('Open session: Inbox Session'),
      ).toBeNull()
    })
  })
})

describe('SessionPanel — scheduled session badge', () => {
  beforeEach(() => {
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-sched-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Heartbeat run',
        type: 'scheduled' as const,
        channel: 'scheduled',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 2,
      },
    ] as never)
  })

  it('renders a "Scheduled" badge and the session for a scheduled-type session', async () => {
    renderPanel()
    expect(await screen.findByLabelText('Open session: Heartbeat run')).toBeTruthy()
    expect(await screen.findByText('Scheduled')).toBeInTheDocument()
  })
})

// ── Q4: Active-workspace scoping + open-in-workspace (Bug 2) ─────────────────
//
// (a) Panel shows only the active workspace's sessions + a "No workspace" group.
//     A session from another workspace is NOT shown.
//     An orphaned (no workspace_id) session appears under "No workspace".
//
// (b) Opening a session in the SAME workspace attaches in place (no navigation).
//
// (c) Opening a session whose workspace_id is a DIFFERENT existing workspace
//     sets the active workspace + navigates to /workspaces/{id}/chat.
//
// (d) Opening a "No workspace" session attaches in place without navigating.

const q4Workspaces = [
  { id: 'ws-q4-a', name: 'Project A', is_default: false, status: 'active' as const, pinned: false, pin_order: 0, task_count: 0, created_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z' },
  { id: 'ws-q4-b', name: 'Project B', is_default: false, status: 'active' as const, pinned: false, pin_order: 0, task_count: 0, created_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z' },
]

describe('SessionPanel — Q4 active-workspace scoping', () => {
  beforeEach(() => {
    vi.mocked(fetchWorkspaces).mockResolvedValue(q4Workspaces as never)
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-q4-same',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Session in Project A',
        type: 'chat' as const,
        workspace_id: 'ws-q4-a',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T03:00:00Z',
        message_count: 1,
      },
      {
        id: 'sess-q4-other',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Session in Project B',
        type: 'chat' as const,
        workspace_id: 'ws-q4-b',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 1,
      },
      {
        id: 'sess-q4-orphan',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Orphan Session',
        type: 'chat' as const,
        // no workspace_id
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 1,
      },
    ] as never)
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-q4-a' })
    })
  })

  it('(a) shows only the active workspace sessions + "No workspace" orphans; hides other workspace sessions', async () => {
    // BDD: Given active workspace is ws-q4-a
    //      And sessions exist for ws-q4-a, ws-q4-b, and one with no workspace_id
    //      Then "Session in Project A" is visible (active workspace)
    //      And "Orphan Session" is visible under "No workspace"
    //      And "Session in Project B" is NOT visible (different existing workspace)
    renderPanel()

    expect(await screen.findByLabelText('Open session: Session in Project A')).toBeTruthy()
    expect(await screen.findByLabelText('Open session: Orphan Session')).toBeTruthy()

    await waitFor(() => {
      expect(screen.queryByLabelText('Open session: Session in Project B')).toBeNull()
    })
  })
})

describe('SessionPanel — Q4 Bug 2: open-in-workspace', () => {
  const attachToSessionSpy = vi.fn()

  beforeEach(() => {
    attachToSessionSpy.mockReset()

    vi.mocked(fetchWorkspaces).mockResolvedValue(q4Workspaces as never)
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-same-ws',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Same Workspace Session',
        type: 'chat' as const,
        workspace_id: 'ws-q4-a',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T04:00:00Z',
        message_count: 1,
      },
      {
        id: 'sess-diff-ws',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Different Workspace Session',
        type: 'chat' as const,
        workspace_id: 'ws-q4-b',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 1,
      },
      {
        id: 'sess-no-ws',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'No Workspace Session',
        type: 'chat' as const,
        // no workspace_id
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 1,
      },
    ] as never)

    act(() => {
      useSessionStore.setState({
        attachToSession: attachToSessionSpy,
      } as unknown as Parameters<typeof useSessionStore.setState>[0])
    })
  })

  it('(b) opening a same-workspace session attaches in place without navigating', async () => {
    // BDD: Given active workspace is ws-q4-a
    //      And a session with workspace_id='ws-q4-a' exists
    //      When the user clicks that session
    //      Then attachToSession is called for it
    //      And navigate is NOT called (same workspace)
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-q4-a' })
    })

    renderPanel()

    const sessionBtn = await screen.findByLabelText('Open session: Same Workspace Session')
    fireEvent.click(sessionBtn)

    expect(attachToSessionSpy).toHaveBeenCalledWith('sess-same-ws', 'chat', 'Same Workspace Session', 'agent-chat-1')
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('(c) opening a session in a different existing workspace sets active workspace and navigates', async () => {
    // BDD: Given no active workspace (all sessions in "No workspace" fallback)
    //      And a session with workspace_id='ws-q4-b' exists (a known existing workspace)
    //      When the user clicks that session
    //      Then setActiveWorkspaceId('ws-q4-b') is called
    //      And attachToSession is called for that session
    //      And navigate is called to /workspaces/ws-q4-b/chat
    const setActiveWorkspaceIdSpy = vi.fn()
    act(() => {
      useWorkspacesStore.setState({
        activeWorkspaceId: null,
        setActiveWorkspaceId: setActiveWorkspaceIdSpy,
      })
    })

    renderPanel()

    const sessionBtn = await screen.findByLabelText('Open session: Different Workspace Session')
    fireEvent.click(sessionBtn)

    expect(setActiveWorkspaceIdSpy).toHaveBeenCalledWith('ws-q4-b')
    expect(attachToSessionSpy).toHaveBeenCalledWith('sess-diff-ws', 'chat', 'Different Workspace Session', 'agent-chat-1')
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId: 'ws-q4-b' },
    })
  })

  it('(d) opening a "No workspace" session attaches in place without navigating', async () => {
    // BDD: Given active workspace is ws-q4-a
    //      And a session with no workspace_id exists (visible under "No workspace")
    //      When the user clicks that session
    //      Then attachToSession is called for it
    //      And navigate is NOT called (no workspace switch)
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-q4-a' })
    })

    renderPanel()

    const sessionBtn = await screen.findByLabelText('Open session: No Workspace Session')
    fireEvent.click(sessionBtn)

    expect(attachToSessionSpy).toHaveBeenCalledWith('sess-no-ws', 'chat', 'No Workspace Session', 'agent-chat-1')
    expect(mockNavigate).not.toHaveBeenCalled()
  })
})
