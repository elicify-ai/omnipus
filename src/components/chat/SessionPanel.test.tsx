import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SessionPanel } from './SessionPanel'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useChatStore, makeBucketMessages } from '@/store/chat'

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
