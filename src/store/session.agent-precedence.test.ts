// session.agent-precedence.test.ts — the AGENT PRECEDENCE RULE
// (src/store/session.ts).
//
// Regression cover for the silent mis-routing bug found in a CI trace:
//
//   6563ms  picker = Mia   ← SPA synced to a backend-created session
//   6604ms  picker = Jim   ← the user's own selection took effect
//   6752ms  message sent
//   ...     picker = Mia   ← reverted, on its own
//
// `activeAgentId` had several writers racing last-write-wins, so a session
// attach landing after the user's pick silently re-pointed the composer — and
// the outbound `message` frame's `agent_id` — at the session's own agent. The
// user saw the picker flip back by itself and their message was answered by an
// agent they had not chosen, with no error anywhere.
//
// These tests assert the OUTCOME the user experiences (which agent id the next
// message actually carries), not the internal bookkeeping that produces it.

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import { useUiStore } from './ui'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchSessions: vi.fn(), fetchWorkspace: vi.fn() }
})

import { fetchSessions, fetchWorkspace } from '@/lib/api'

const WS_A = 'ws-alpha'
const WS_B = 'ws-beta'
const SESSION_ID = 'sess-mia-created'

function resetStores() {
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      isReplaying: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      pendingKickoff: null,
      outboundQueue: [],
      pendingDrainQueue: [],
    })
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useSessionStore.setState({
      activeSessionId: null,
      activeAgentId: null,
      activeAgentType: null,
      agentSelectionSource: 'auto',
      agentSelectionWorkspaceId: null,
      attachedSessionType: null,
      attachedTaskTitle: null,
      sessionByWorkspace: {},
      workspaceEntry: null,
      resolvingSessionForWorkspace: {},
    })
    useWorkspacesStore.setState({ activeWorkspaceId: WS_A })
    useUiStore.setState({ toasts: [] })
    vi.mocked(fetchSessions).mockReset()
    // The entry gate now reports a rejected workspace input instead of
    // swallowing it. Supply the network boundary this owner test needs.
    vi.mocked(fetchWorkspace).mockReset().mockImplementation(async (id) => ({
      id, name: id, revision: 'rev-1', status: 'active', pinned: false,
      pin_order: 0, task_count: 0, member_configs: {},
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    }))
  })
}

beforeEach(resetStores)

/** Wires a fake WS connection and returns its `send` spy. */
function connectMock() {
  const mockSend = vi.fn().mockReturnValue(true)
  act(() => {
    useConnectionStore.setState({
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
      isConnected: true,
    })
  })
  return mockSend
}

/** The `agent_id` on the outbound `message` frame — i.e. who actually answers. */
function agentIdOfSentMessage(mockSend: ReturnType<typeof connectMock>): string | undefined {
  const messageFrames = mockSend.mock.calls
    .map((call) => call[0] as { type?: string; agent_id?: string })
    .filter((frame) => frame?.type === 'message')
  expect(messageFrames).toHaveLength(1)
  return messageFrames[0].agent_id
}

describe('agent precedence — the session owner is the send destination', () => {
  it('a session attach sets the immutable owner, and a later same-session hint does not move the next message', () => {
    const mockSend = connectMock()

    // FR-009: there is no composer picker. selectAgent is gone.
    expect(typeof (useSessionStore.getState() as { selectAgent?: unknown }).selectAgent).not.toBe('function')

    act(() => {
      useSessionStore.getState().attachToSession(SESSION_ID, 'chat', undefined, 'mia')
    })
    expect(useSessionStore.getState().activeSessionId).toBe(SESSION_ID)
    expect(useSessionStore.getState().activeAgentId).toBe('mia')

    // A hint for the same session must not replace the owner.
    act(() => {
      useSessionStore.getState().setActiveSession(SESSION_ID, 'jim', 'core')
    })
    expect(useSessionStore.getState().activeAgentId).toBe('mia')

    act(() => {
      useChatStore.getState().sendMessage('who is answering this?')
    })
    expect(agentIdOfSentMessage(mockSend)).toBe('mia')
  })

  it('a newly activated session adopts the owner passed to setActiveSession', () => {
    const mockSend = connectMock()

    act(() => {
      useSessionStore.getState().setActiveSession(SESSION_ID, 'mia', 'core')
    })

    expect(useSessionStore.getState().activeAgentId).toBe('mia')

    act(() => {
      useChatStore.getState().sendMessage('still Mia?')
    })
    expect(agentIdOfSentMessage(mockSend)).toBe('mia')
  })

  it('setActiveAgentType records the type of the attached owner', () => {
    connectMock()
    act(() => {
      useSessionStore.getState().attachToSession(SESSION_ID, 'chat', undefined, 'ava')
      useSessionStore.getState().setActiveAgentType('Main')
    })

    expect(useSessionStore.getState().activeAgentId).toBe('ava')
    expect(useSessionStore.getState().activeAgentType).toBe('Main')
  })

  it('starting a new chat keeps the attached owner and drops the session id', () => {
    act(() => {
      useSessionStore.getState().attachToSession(SESSION_ID, 'chat', undefined, 'mia')
      useSessionStore.getState().startNewSession()
    })
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    // FR-005: the extra composer keeps the pair's owner.
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
  })
})

describe('agent precedence — the ordinary case still adopts the session agent', () => {
  it('with no explicit selection, attaching a session adopts that session\'s agent, and the next message goes to it', () => {
    const mockSend = connectMock()

    act(() => {
      useSessionStore.getState().attachToSession(SESSION_ID, 'chat', undefined, 'mia')
    })

    expect(useSessionStore.getState().activeAgentId).toBe('mia')

    act(() => {
      useChatStore.getState().sendMessage('hello')
    })
    expect(agentIdOfSentMessage(mockSend)).toBe('mia')
  })

  it('with no explicit selection, setActiveSession adopts the session agent and its type', () => {
    act(() => {
      useSessionStore.getState().setActiveSession(SESSION_ID, 'mia', 'core')
    })
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
    expect(useSessionStore.getState().activeAgentType).toBe('core')
  })

  it('an attach still adopts its agent when the pick was made but there is no agent yet (a pin on "no agent" is meaningless)', () => {
    // Defensive: activeAgentId nulled out from under a stale 'user' source
    // must not wedge the composer with nothing to route to.
    act(() => {
      useSessionStore.setState({ agentSelectionSource: 'user', activeAgentId: null })
      useSessionStore.getState().attachToSession(SESSION_ID, 'chat', undefined, 'mia')
    })
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
  })

  it('there is no picker action that can point the composer at a second agent', () => {
    // FR-009: re-selecting in the composer is gone with the picker.
    expect(typeof (useSessionStore.getState() as { selectAgent?: unknown }).selectAgent).not.toBe('function')
    act(() => {
      useSessionStore.getState().attachToSession(SESSION_ID, 'chat', undefined, 'mia')
    })
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
  })
})

describe('agent precedence — scope and override', () => {
  it('entering another workspace restores that workspace session\'s owner, not a picker pin', async () => {
    connectMock()
    act(() => {
      useSessionStore.setState({
        sessionByWorkspace: {
          [WS_B]: { id: 'sess-beta', type: 'chat', title: null, agentId: 'ray' },
        },
      })
      useSessionStore.getState().attachToSession('sess-alpha', 'chat', undefined, 'jim')
    })
    expect(useSessionStore.getState().activeAgentId).toBe('jim')

    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-beta',
        agent_id: 'ray',
        title: 'Beta',
        type: 'chat',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        message_count: 1,
        workspace_id: WS_B,
      },
    ])
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: WS_B })
    })
    await useSessionStore.getState().enterWorkspaceChat(WS_B)

    // FR-004 / FR-009: the visible pointer's immutable owner, not the previous pin.
    expect(useSessionStore.getState().activeAgentId).toBe('ray')
    expect(useSessionStore.getState().activeSessionId).toBe('sess-beta')
  })

  it('re-entering the same workspace restores the session owner, not a picker pin', async () => {
    connectMock()
    act(() => {
      useSessionStore.setState({
        sessionByWorkspace: {
          [WS_A]: { id: 'sess-alpha', type: 'chat', title: null, agentId: 'jim' },
        },
        activeSessionId: null,
        activeAgentId: null,
      })
    })
    vi.mocked(fetchSessions).mockResolvedValue([
      {
        id: 'sess-alpha',
        agent_id: 'mia',
        title: 'Alpha',
        type: 'chat',
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        message_count: 1,
        workspace_id: WS_A,
      },
    ])
    await useSessionStore.getState().enterWorkspaceChat(WS_A)
    // The pointer remembered jim; the session's immutable owner is mia.
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
    expect(useSessionStore.getState().activeSessionId).toBe('sess-alpha')
  })

  it('a later hint for the same session does not replace the attached owner (FR-009)', () => {
    const mockSend = connectMock()

    act(() => {
      useSessionStore.getState().setActiveSession(SESSION_ID, 'jim', 'core')
    })
    expect(useSessionStore.getState().activeAgentId).toBe('jim')

    act(() => {
      useSessionStore.getState().setActiveSession(SESSION_ID, 'ava', 'Main')
    })
    expect(useSessionStore.getState().activeAgentId).toBe('jim')

    act(() => {
      useChatStore.getState().sendMessage('who now?')
    })
    expect(agentIdOfSentMessage(mockSend)).toBe('jim')
  })

  it('attaching records the session owner on the workspace pointer', () => {
    act(() => {
      useSessionStore.setState({
        sessionByWorkspace: {
          [WS_A]: { id: 'sess-alpha', type: 'chat', title: null, agentId: 'mia' },
        },
      })
      useSessionStore.getState().attachToSession('sess-alpha', 'chat', undefined, 'mia')
    })
    expect(useSessionStore.getState().sessionByWorkspace[WS_A]?.agentId).toBe('mia')
    expect(typeof (useSessionStore.getState() as { selectAgent?: unknown }).selectAgent).not.toBe('function')
  })

  it('there is no picker action that detaches the session or wipes the Task banner', () => {
    act(() => {
      useSessionStore.setState({
        activeSessionId: 'sess-task',
        attachedSessionType: 'task',
        attachedTaskTitle: 'Migrate the database',
      })
    })
    expect(typeof (useSessionStore.getState() as { selectAgent?: unknown }).selectAgent).not.toBe('function')
    const state = useSessionStore.getState()
    expect(state.activeSessionId).toBe('sess-task')
    expect(state.attachedSessionType).toBe('task')
    expect(state.attachedTaskTitle).toBe('Migrate the database')
  })
})
