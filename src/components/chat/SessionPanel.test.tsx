import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SessionPanel } from './SessionPanel'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchSessions, fetchWorkspaces, fetchWorkspaceSessions } from '@/lib/api'

// W2-1: SessionPanel chat-session routing regression test.
//
// Tests that handleSelectSession always routes through attachToSession (never
// setActiveSession — which would trigger the REST-clobber bug fixed in c76ac73).
//
// Traces to: temporal-puzzling-melody.md W2-1
// Traces to: sprint-i-historical-replay-fidelity-spec.md FR-I-014

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
    // Default: no workspaces and no links. Individual workspace-scoping tests
    // override these with mockResolvedValueOnce. With no active workspace the
    // panel never calls them, so the default value is irrelevant for the
    // non-workspace tests above.
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchWorkspaceSessions: vi.fn().mockResolvedValue([]),
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
// These tests exercise the session list grouped BY WORKSPACE. Each group is
// collapsible and its header shows the workspace name + session count.
// Sessions with no workspace link fall under a "No workspace" fallback group.
// The active workspace (when set) is placed first in the list.

// Helper sessions for workspace grouping tests
const wsGroupSessions = [
  {
    id: 'sess-ws1-1',
    agent_id: 'agent-chat-1',
    active_agent_id: 'agent-chat-1',
    title: 'Alpha Project Session',
    type: 'chat' as const,
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
    channel: 'webchat',
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T01:00:00Z',
    message_count: 3,
  },
]

const wsGroupWorkspaces = [
  { id: 'ws-alpha', name: 'Alpha Project', is_default: false, status: 'active' as const, pinned: false, pin_order: 0, task_count: 0, created_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z' },
  { id: 'ws-beta',  name: 'Beta Project',  is_default: false, status: 'active' as const, pinned: false, pin_order: 0, task_count: 0, created_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z' },
]

// Test A: all sessions in one workspace → flat list (showGroups=false)
describe('SessionPanel — workspace grouping: single workspace renders flat list (Test A)', () => {
  beforeEach(() => {
    // One workspace, one session linked to it — only one group, so showGroups=false.
    vi.mocked(fetchWorkspaces).mockResolvedValue([wsGroupWorkspaces[0]] as never)
    vi.mocked(fetchWorkspaceSessions).mockResolvedValue([
      { workspace_id: 'ws-alpha', session_id: 'sess-ws1-1' } as never,
    ])
    vi.mocked(fetchSessions).mockResolvedValue([wsGroupSessions[0]])
  })

  it('renders sessions as a flat list with no group header buttons when there is only one workspace', async () => {
    // BDD: Given all sessions belong to the same workspace
    // BDD: Then showGroups=false and no collapsible group header buttons are rendered
    renderPanel()

    await screen.findByText('Alpha Project Session')

    // No workspace group header buttons should be present
    const groupButtons = screen.queryAllByRole('button', { name: /workspace sessions, (collapse|expand)/i })
    expect(groupButtons).toHaveLength(0)
  })

  it('renders flat list when sessions have no workspace links (all go to fallback)', async () => {
    // BDD: Given sessions have no workspace links (legacy/global sessions)
    // BDD: Then all sessions land in the "No workspace" fallback — one group, showGroups=false
    vi.mocked(fetchWorkspaces).mockResolvedValue([] as never)
    vi.mocked(fetchWorkspaceSessions).mockResolvedValue([] as never)
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

    renderPanel()

    await screen.findByText('Legacy Session One')

    const groupButtons = screen.queryAllByRole('button', { name: /workspace sessions, (collapse|expand)/i })
    expect(groupButtons).toHaveLength(0)
  })
})

// Test B: sessions in multiple workspaces → workspace group headers appear
describe('SessionPanel — workspace grouping: multi-workspace renders group headers (Test B)', () => {
  beforeEach(() => {
    vi.mocked(fetchWorkspaces).mockResolvedValue(wsGroupWorkspaces as never)
    // fetchWorkspaceSessions is called once per workspace; return the appropriate link for each.
    vi.mocked(fetchWorkspaceSessions)
      .mockResolvedValueOnce([{ workspace_id: 'ws-alpha', session_id: 'sess-ws1-1' }] as never)
      .mockResolvedValueOnce([{ workspace_id: 'ws-beta',  session_id: 'sess-ws2-1' }] as never)
    vi.mocked(fetchSessions).mockResolvedValue(wsGroupSessions)
  })

  it('renders a group header button for each workspace', async () => {
    // BDD: Given sessions span 2 workspaces (Alpha Project, Beta Project)
    // BDD: Then showGroups=true and a header button is rendered for each workspace
    renderPanel()

    const alphaHeader = await screen.findByRole('button', { name: /Alpha Project workspace sessions/i })
    const betaHeader = await screen.findByRole('button', { name: /Beta Project workspace sessions/i })

    expect(alphaHeader).toBeTruthy()
    expect(betaHeader).toBeTruthy()
  })

  it('renders both session items visible under their respective workspace groups', async () => {
    // BDD: Given 2 workspace groups, all expanded by default
    // BDD: Then both session items are visible in the DOM
    renderPanel()

    await screen.findByLabelText('Open session: Alpha Project Session')
    await screen.findByLabelText('Open session: Beta Project Session')
  })

  it('shows count badge of 1 for each workspace group', async () => {
    // BDD: Given 1 session per workspace
    // BDD: Then each group header badge shows "1"
    renderPanel()

    await screen.findByRole('button', { name: /Alpha Project workspace sessions/i })

    const badges = screen.getAllByText('1')
    expect(badges.length).toBeGreaterThanOrEqual(2)
  })
})

// Test C: workspace group collapse/expand toggle
describe('SessionPanel — workspace grouping: collapse/expand toggle (Test C)', () => {
  beforeEach(() => {
    vi.mocked(fetchWorkspaces).mockResolvedValue(wsGroupWorkspaces as never)
    vi.mocked(fetchWorkspaceSessions)
      .mockResolvedValueOnce([{ workspace_id: 'ws-alpha', session_id: 'sess-ws1-1' }] as never)
      .mockResolvedValueOnce([{ workspace_id: 'ws-beta',  session_id: 'sess-ws2-1' }] as never)
    vi.mocked(fetchSessions).mockResolvedValue(wsGroupSessions)
  })

  it('collapses a workspace group on first click and expands it on second click', async () => {
    // BDD: Given a multi-workspace panel (Alpha + Beta), all groups expanded
    // BDD: When the user clicks the Beta Project group header
    // BDD: Then aria-expanded=false and its session item leaves the DOM
    // BDD: When the user clicks the Beta header again
    // BDD: Then aria-expanded=true and the session item is visible again
    // BDD: And the Alpha group remains unaffected throughout
    renderPanel()

    const betaHeader = await screen.findByRole('button', { name: /Beta Project workspace sessions/i })

    // Initially expanded
    await screen.findByLabelText('Open session: Beta Project Session')
    expect(betaHeader).toHaveAttribute('aria-expanded', 'true')

    // Collapse
    fireEvent.click(betaHeader)

    await waitFor(() => {
      expect(screen.queryByLabelText('Open session: Beta Project Session')).toBeNull()
    })
    expect(betaHeader).toHaveAttribute('aria-expanded', 'false')

    // Alpha group is unaffected
    expect(screen.getByLabelText('Open session: Alpha Project Session')).toBeTruthy()

    // Expand again
    fireEvent.click(betaHeader)

    await screen.findByLabelText('Open session: Beta Project Session')
    expect(betaHeader).toHaveAttribute('aria-expanded', 'true')
  })
})

// Test D: search filters sessions; workspace group header disappears when empty
describe('SessionPanel — workspace grouping: search filters and hides empty groups (Test D)', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.mocked(fetchWorkspaces).mockResolvedValue(wsGroupWorkspaces as never)
    vi.mocked(fetchWorkspaceSessions)
      .mockResolvedValueOnce([{ workspace_id: 'ws-alpha', session_id: 'sess-ws1-1' }] as never)
      .mockResolvedValueOnce([{ workspace_id: 'ws-beta',  session_id: 'sess-ws2-1' }] as never)
    vi.mocked(fetchSessions).mockResolvedValue(wsGroupSessions)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('hides a workspace group header when its sessions do not match the search term', async () => {
    // BDD: Given a multi-workspace panel with Alpha and Beta groups visible
    // BDD: When the user types "Alpha" (matches only "Alpha Project Session")
    // BDD: Then the Beta Project group header disappears
    // BDD: And the Alpha session remains visible
    renderPanel()

    await screen.findByRole('button', { name: /Beta Project workspace sessions/i })

    const searchInput = screen.getByRole('textbox', { name: /search sessions/i })
    fireEvent.change(searchInput, { target: { value: 'Alpha' } })

    act(() => {
      vi.advanceTimersByTime(350)
    })

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /Beta Project workspace sessions/i })).toBeNull()
    })

    expect(screen.getByLabelText('Open session: Alpha Project Session')).toBeTruthy()
  })
})

// Test E: sessions with no workspace link appear under "No workspace" fallback
describe('SessionPanel — workspace grouping: ungrouped sessions go to "No workspace" (Test E)', () => {
  it('renders unlinked sessions under "No workspace" group alongside workspace-linked sessions', async () => {
    // BDD: Given one session is linked to ws-alpha and another has no workspace link
    // BDD: Then two workspace groups appear: "Alpha Project" and "No workspace"
    vi.mocked(fetchWorkspaces).mockResolvedValue([wsGroupWorkspaces[0]] as never)
    vi.mocked(fetchWorkspaceSessions).mockResolvedValue([
      { workspace_id: 'ws-alpha', session_id: 'sess-ws1-1' } as never,
    ])
    vi.mocked(fetchSessions).mockResolvedValue([
      wsGroupSessions[0],
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

// ── Workspace scoping (IA reframe) ────────────────────────────────────────────
//
// Regression for the IA reframe: the global "/" front door redirects into the
// DEFAULT workspace's chat, which sets activeWorkspaceId. The panel must NOT
// strip unlinked / global / REST-created sessions while in the default
// workspace (the home/inbox), otherwise those sessions become unreachable —
// this broke the iframe-preview / warmup e2e specs whose seedAndOpenSession
// helper creates a session via REST (no workspace→task link) and then opens it
// from the panel. A NON-default workspace, by contrast, must scope strictly to
// its own link set.
describe('SessionPanel — workspace scoping (IA reframe)', () => {
  it('default workspace shows unlinked sessions (no scoping)', async () => {
    // BDD: Given the active workspace is the default workspace
    //      And a session exists that is NOT in any workspace→session link set
    //      When the panel renders
    //      Then the unlinked session is still listed (inbox behaviour)
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-default', name: 'Inbox', is_default: true } as never,
    ])
    // The link set for the default workspace is empty — the REST-created
    // session has no link. Strict scoping would hide it; we must not.
    vi.mocked(fetchWorkspaceSessions).mockResolvedValue([])
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

  it('non-default workspace scopes to its own link set, hiding unlinked sessions', async () => {
    // BDD: Given the active workspace is a NON-default workspace
    //      And a linked session plus an unlinked session both exist
    //      When the panel renders
    //      Then only the linked session is listed
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-default', name: 'Inbox', is_default: true } as never,
      { id: 'ws-project', name: 'Project', is_default: false } as never,
    ])
    vi.mocked(fetchWorkspaceSessions).mockResolvedValue([
      { workspace_id: 'ws-project', session_id: 'sess-linked-1' } as never,
    ])
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-linked-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Linked Project Session',
        type: 'chat',
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
    ])

    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-project' })
    })

    renderPanel()

    expect(
      await screen.findByLabelText('Open session: Linked Project Session'),
    ).toBeTruthy()
    await waitFor(() => {
      expect(
        screen.queryByLabelText('Open session: Unlinked Other Session'),
      ).toBeNull()
    })
  })
})
