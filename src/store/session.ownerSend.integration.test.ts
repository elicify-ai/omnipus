/**
 * Blank-composer owner. Oracles: FR-009, FR-007, founder Q10.
 *
 * With the AgentPicker gone, the attached session's immutable owner
 * (session.agent_id) is the agent the next message is sent to.
 * selectAgent and agentSelectionSource are not reachable: calling the old
 * picker path, or a session hint that carries a different agent, must not
 * change that destination.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useSessionStore } from './session'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useWorkspacesStore } from './workspacesStore'

const OWNER = 'mia'
const SESSION = 'main-mia'

vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: () => undefined,
  isMainSession: () => false,
  sessionAttention: () => 'unknown' as const,
  attachAckFields: () => ({}),
  attentionBoundOfFrame: () => undefined,
}))

function resetAll() {
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    agentSelectionSource: 'auto',
    agentSelectionWorkspaceId: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {},
    resolvingSessionForWorkspace: {},
  })
  useWorkspacesStore.setState({ activeWorkspaceId: 'operations' })
  useChatStore.setState({
    sessionsById: {},
    isStreaming: false,
    isReplaying: false,
    pendingFirstSend: null,
  })
  useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
  localStorage.clear()
}

function connect() {
  const send = vi.fn().mockReturnValue(true)
  useConnectionStore.setState({
    connection: { send, close: vi.fn(), isConnected: true } as never,
    isConnected: true,
  })
  return send
}

function messageAgent(send: ReturnType<typeof vi.fn>): string | null {
  const message = send.mock.calls
    .map((call) => call[0] as Record<string, unknown>)
    .filter((frame) => frame.type === 'message')
    .at(-1)
  return typeof message?.agent_id === 'string' ? message.agent_id : null
}

describe('send destination is the immutable session owner (FR-009)', () => {
  beforeEach(resetAll)

  it('selectAgent cannot point the next message at anyone but the attached session owner', () => {
    const send = connect()
    useSessionStore.getState().attachToSession(SESSION, 'chat', 'Mia main', OWNER)
    const select = (useSessionStore.getState() as { selectAgent?: (id: string) => void }).selectAgent
    if (typeof select === 'function') select('ava')
    useChatStore.getState().sendMessage('hello')

    expect({
      sendAgent: messageAgent(send),
      selectionSource: (useSessionStore.getState() as { agentSelectionSource?: unknown }).agentSelectionSource ?? null,
    }).toEqual({
      sendAgent: OWNER,
      selectionSource: null,
    })
  })

  it('a session hint for a different agent does not change the send destination', () => {
    const send = connect()
    useSessionStore.getState().attachToSession(SESSION, 'chat', 'Mia main', OWNER)
    useSessionStore.getState().setActiveSession(SESSION, 'jim', 'core')
    useChatStore.getState().sendMessage('hello')

    expect({
      sendAgent: messageAgent(send),
      selectionSource: (useSessionStore.getState() as { agentSelectionSource?: unknown }).agentSelectionSource ?? null,
    }).toEqual({
      sendAgent: OWNER,
      selectionSource: null,
    })
  })
})
