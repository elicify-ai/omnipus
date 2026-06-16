import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SessionPanel } from './SessionPanel'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import { fetchSessions } from '@/lib/api'

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

// ── Channel grouping tests ────────────────────────────────────────────────────

// Test A: webchat-only sessions render flat list (no group headers)
describe('SessionPanel — channel grouping: webchat-only renders flat list (Test A)', () => {
  beforeEach(() => {
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-wc-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'WebChat Session One',
        type: 'chat',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 2,
      },
      {
        id: 'sess-wc-2',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'WebChat Session Two',
        type: 'chat',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 4,
      },
    ])
  })

  it('renders sessions as a flat list with no group header buttons', async () => {
    // BDD: Given all sessions have channel="webchat" (single distinct channel)
    // BDD: Then showGroups=false, so no collapsible group header buttons are rendered
    // BDD: And both session items are listed directly
    renderPanel()

    await screen.findByText('WebChat Session One')
    await screen.findByText('WebChat Session Two')

    // No group header buttons should be present.
    // Group headers have aria-label matching "sessions, collapse" / "sessions, expand".
    const groupButtons = screen.queryAllByRole('button', { name: /sessions, (collapse|expand)/i })
    expect(groupButtons).toHaveLength(0)
  })

  it('renders flat list for sessions with no channel field (undefined treated as webchat)', async () => {
    // BDD: Given sessions have no channel field (legacy sessions)
    // BDD: Then they are treated as webchat and rendered flat (showGroups=false)
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-legacy-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Legacy Session One',
        type: 'chat',
        // channel field intentionally omitted
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 1,
      },
    ])

    renderPanel()

    await screen.findByText('Legacy Session One')

    const groupButtons = screen.queryAllByRole('button', { name: /sessions, (collapse|expand)/i })
    expect(groupButtons).toHaveLength(0)
  })
})

// Test B: multi-channel renders group headers
describe('SessionPanel — channel grouping: multi-channel renders group headers (Test B)', () => {
  beforeEach(() => {
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-wc-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'WebChat Session One',
        type: 'chat',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 2,
      },
      {
        id: 'sess-tg-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Telegram Session One',
        type: 'chat',
        channel: 'telegram',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 3,
      },
    ])
  })

  it('renders group header buttons for each distinct channel', async () => {
    // BDD: Given sessions span 2 distinct channels (webchat, telegram)
    // BDD: Then showGroups=true and a header button is rendered for each channel
    renderPanel()

    // Both group headers must appear
    const webChatHeader = await screen.findByRole('button', { name: /Web Chat sessions/i })
    const telegramHeader = await screen.findByRole('button', { name: /Telegram sessions/i })

    expect(webChatHeader).toBeTruthy()
    expect(telegramHeader).toBeTruthy()
  })

  it('renders both session items visible under their respective groups', async () => {
    // BDD: Given the multi-channel sessions are loaded
    // BDD: Then both session title items are visible (groups are expanded by default)
    renderPanel()

    await screen.findByLabelText('Open session: WebChat Session One')
    await screen.findByLabelText('Open session: Telegram Session One')
  })

  it('shows count badge of 1 for each group', async () => {
    // BDD: Given 1 session per channel
    // BDD: Then each group header badge shows "1"
    renderPanel()

    // Wait for headers to appear
    await screen.findByRole('button', { name: /Web Chat sessions/i })

    // Both count badges display "1"
    const badges = screen.getAllByText('1')
    expect(badges.length).toBeGreaterThanOrEqual(2)
  })
})

// Test C: group collapse/expand toggle
describe('SessionPanel — channel grouping: collapse/expand toggle (Test C)', () => {
  beforeEach(() => {
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-wc-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'WebChat Session One',
        type: 'chat',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 2,
      },
      {
        id: 'sess-tg-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Telegram Session One',
        type: 'chat',
        channel: 'telegram',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 3,
      },
    ])
  })

  it('collapses a group on first click and expands it on second click', async () => {
    // BDD: Given a multi-channel panel (webchat + telegram), all groups expanded
    // BDD: When the user clicks the Telegram group header
    // BDD: Then aria-expanded=false and the telegram session item is removed from the DOM
    // BDD: When the user clicks the Telegram header again
    // BDD: Then aria-expanded=true and the telegram session item is visible again
    renderPanel()

    // Wait for groups to load
    const telegramHeader = await screen.findByRole('button', { name: /Telegram sessions/i })

    // Initially expanded: session item is visible
    await screen.findByLabelText('Open session: Telegram Session One')
    expect(telegramHeader).toHaveAttribute('aria-expanded', 'true')

    // Click to collapse
    fireEvent.click(telegramHeader)

    // After collapse: session item gone, aria-expanded=false
    await waitFor(() => {
      expect(screen.queryByLabelText('Open session: Telegram Session One')).toBeNull()
    })
    expect(telegramHeader).toHaveAttribute('aria-expanded', 'false')

    // Webchat session is still visible (other group unaffected)
    expect(screen.getByLabelText('Open session: WebChat Session One')).toBeTruthy()

    // Click to expand again
    fireEvent.click(telegramHeader)

    // After expand: session item visible again, aria-expanded=true
    await screen.findByLabelText('Open session: Telegram Session One')
    expect(telegramHeader).toHaveAttribute('aria-expanded', 'true')
  })
})

// Test D: search filters sessions and collapses empty groups
describe('SessionPanel — channel grouping: search filters and hides empty groups (Test D)', () => {
  beforeEach(() => {
    // Use fake timers but allow testing-library's internal polling to advance automatically
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-wc-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Webchat Alpha',
        type: 'chat',
        channel: 'webchat',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T01:00:00Z',
        message_count: 2,
      },
      {
        id: 'sess-tg-1',
        agent_id: 'agent-chat-1',
        active_agent_id: 'agent-chat-1',
        title: 'Telegram Beta',
        type: 'chat',
        channel: 'telegram',
        created_at: '2026-04-01T00:00:00Z',
        updated_at: '2026-04-01T02:00:00Z',
        message_count: 3,
      },
    ])
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('hides the Telegram group header when the search term matches only the webchat session', async () => {
    // BDD: Given a multi-channel panel (webchat + telegram), both groups visible
    // BDD: When the user types "Alpha" (matches only "Webchat Alpha")
    // BDD: Then the Telegram group header disappears (no sessions pass the filter)
    // BDD: And the webchat session remains visible (either flat or as the sole group)
    renderPanel()

    // Wait for both groups to load
    await screen.findByRole('button', { name: /Telegram sessions/i })

    // Type a search term that only matches the webchat session
    const searchInput = screen.getByRole('textbox', { name: /search sessions/i })
    fireEvent.change(searchInput, { target: { value: 'Alpha' } })

    // Advance past the 300ms debounce
    act(() => {
      vi.advanceTimersByTime(350)
    })

    // After debounce: Telegram group header must be gone
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /Telegram sessions/i })).toBeNull()
    })

    // The webchat session must still be present
    expect(screen.getByLabelText('Open session: Webchat Alpha')).toBeTruthy()
  })
})
