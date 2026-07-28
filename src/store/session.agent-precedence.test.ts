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
    })
    useWorkspacesStore.setState({ activeWorkspaceId: WS_A })
    useUiStore.setState({ toasts: [] })
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

describe('agent precedence — an explicit user selection outranks a session attach', () => {
  it('a session attach does not move the picker off the agent the user chose, and the next message goes to that agent', () => {
    const mockSend = connectMock()

    // The user picks Jim in the composer's agent picker / "@" menu.
    act(() => {
      useSessionStore.getState().selectAgent('jim', 'core')
    })
    expect(useSessionStore.getState().activeAgentId).toBe('jim')

    // A session attach lands afterwards carrying the session's own agent
    // (Mia) — the exact write that used to clobber the pick.
    act(() => {
      useSessionStore.getState().attachToSession(SESSION_ID, 'chat', undefined, 'mia')
    })

    // The picker still reads Jim.
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
    expect(useSessionStore.getState().activeAgentType).toBe('core')
    // The attach itself still happened.
    expect(useSessionStore.getState().activeSessionId).toBe(SESSION_ID)

    // And the message the user sends next is routed to Jim, not Mia.
    act(() => {
      useChatStore.getState().sendMessage('who is answering this?')
    })
    expect(agentIdOfSentMessage(mockSend)).toBe('jim')
  })

  it('a backend-created session synced via setActiveSession does not clobber the pick either', () => {
    const mockSend = connectMock()

    act(() => {
      useSessionStore.getState().selectAgent('jim', 'core')
    })
    // `session_started` ack / deep-link seed / reattach reseed all land here.
    act(() => {
      useSessionStore.getState().setActiveSession(SESSION_ID, 'mia', 'core')
    })

    expect(useSessionStore.getState().activeAgentId).toBe('jim')

    act(() => {
      useChatStore.getState().sendMessage('still Jim?')
    })
    expect(agentIdOfSentMessage(mockSend)).toBe('jim')
  })

  it('a session-derived agent TYPE cannot land on top of the picked agent either (no mismatched id/type pair)', () => {
    connectMock()
    act(() => {
      useSessionStore.getState().selectAgent('jim', 'core')
      // useSelectSession does exactly this right after its attach.
      useSessionStore.getState().attachToSession(SESSION_ID, 'chat', undefined, 'ava')
      useSessionStore.getState().setActiveAgentType('Main')
    })

    expect(useSessionStore.getState().activeAgentId).toBe('jim')
    expect(useSessionStore.getState().activeAgentType).toBe('core')
  })

  it('the pick survives /new (startNewSession) — a fresh conversation is not an un-choosing', () => {
    act(() => {
      useSessionStore.getState().selectAgent('jim', 'core')
      useSessionStore.getState().startNewSession()
    })
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
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

  it('re-selecting a different agent replaces the previous pick', () => {
    act(() => {
      useSessionStore.getState().selectAgent('jim', 'core')
      useSessionStore.getState().selectAgent('ava', 'Main')
    })
    expect(useSessionStore.getState().activeAgentId).toBe('ava')
    expect(useSessionStore.getState().activeAgentType).toBe('Main')
  })
})

describe('agent precedence — scope and override', () => {
  it('the pick is released when the user moves to a DIFFERENT workspace, so that workspace\'s own agent is restored', () => {
    connectMock()
    act(() => {
      useSessionStore.setState({
        sessionByWorkspace: {
          [WS_B]: { id: 'sess-beta', type: 'chat', title: null, agentId: 'ray' },
        },
      })
      useSessionStore.getState().selectAgent('jim', 'core')
    })
    expect(useSessionStore.getState().activeAgentId).toBe('jim')

    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: WS_B })
      useSessionStore.getState().enterWorkspaceChat(WS_B)
    })

    // Workspace beta's own remembered agent wins — the pick was scoped to alpha.
    expect(useSessionStore.getState().activeAgentId).toBe('ray')
  })

  it('re-entering the SAME workspace keeps the pick', () => {
    connectMock()
    act(() => {
      useSessionStore.setState({
        sessionByWorkspace: {
          [WS_A]: { id: 'sess-alpha', type: 'chat', title: null, agentId: 'mia' },
        },
      })
      useSessionStore.getState().selectAgent('jim', 'core')
      useSessionStore.getState().enterWorkspaceChat(WS_A)
    })
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
  })

  it('a server-driven handover (WS agent_switched) DOES override the pick — the picker must name whoever is actually answering', () => {
    const mockSend = connectMock()

    act(() => {
      useSessionStore.getState().selectAgent('jim', 'core')
      useSessionStore.getState().setActiveSession(SESSION_ID, null, null)
    })
    expect(useSessionStore.getState().activeAgentId).toBe('jim')

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'agent_switched',
        agent_id: 'ray',
        session_id: SESSION_ID,
      })
    })
    expect(useSessionStore.getState().activeAgentId).toBe('ray')

    // And the pin is genuinely released, not merely bypassed once: a later
    // session-derived hint is adopted normally.
    act(() => {
      useSessionStore.getState().setActiveSession(SESSION_ID, 'ava', 'Main')
    })
    expect(useSessionStore.getState().activeAgentId).toBe('ava')

    act(() => {
      useChatStore.getState().sendMessage('who now?')
    })
    expect(agentIdOfSentMessage(mockSend)).toBe('ava')
  })

  it('selecting an agent updates the workspace\'s remembered agent, so a later mount restores the pick', () => {
    act(() => {
      useSessionStore.setState({
        sessionByWorkspace: {
          [WS_A]: { id: 'sess-alpha', type: 'chat', title: null, agentId: 'mia' },
        },
      })
      useSessionStore.getState().selectAgent('jim', 'core')
    })
    expect(useSessionStore.getState().sessionByWorkspace[WS_A]?.agentId).toBe('jim')
  })

  it('selecting an agent does not detach the session or wipe the "Task:" banner', () => {
    act(() => {
      useSessionStore.setState({
        activeSessionId: 'sess-task',
        attachedSessionType: 'task',
        attachedTaskTitle: 'Migrate the database',
      })
      useSessionStore.getState().selectAgent('jim', 'core')
    })
    const state = useSessionStore.getState()
    expect(state.activeSessionId).toBe('sess-task')
    expect(state.attachedSessionType).toBe('task')
    expect(state.attachedTaskTitle).toBe('Migrate the database')
  })
})
