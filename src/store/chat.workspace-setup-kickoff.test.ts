// FR-047/K9 migration: client kickoff sender/content/slot machinery is retired.
// Preserve this file's ordinary-message regressions and the ordinary portions
// of its three mixed scenarios. Exact retired titles/reasons are in the
// WU10-KICKOFF-ORACLE-LEDGER.md and retirement manifest, not silent skips.
// Ordinary fresh sends now name Mia explicitly (spec FR-122); Ava/default
// fallback is not a legal fixture. Existing ordinary matcher expressions stay.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages } from './chat'
import { useConnectionStore } from './connection'
import type { WsConnection } from '@/lib/ws'
import { useSessionStore } from './session'
import { enterNewChat } from '@/store/session/newChatKey'
import { useWorkspacesStore } from './workspacesStore'
import { useUiStore } from './ui'

const WORKSPACE_ID = 'ws-fresh-1'
const AGENT_ID = 'mia'
const AGENT_TYPE = 'core' as const
function resetStore() {
  act(() => {
    useSessionStore.setState({ ...useSessionStore.getInitialState(), activeAgentId: AGENT_ID, activeAgentType: AGENT_TYPE }, true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useUiStore.setState({ toasts: [] })
  })
}
beforeEach(resetStore)
function outboundQueueContents(): string[] {
  return useChatStore.getState().outboundQueue.map((item) => typeof item === 'string' ? item : item.content)
}
/** Spec 4.4.1: a first send is reached by entering a new chat, not by '__pending'. */
function openFreshChat(workspaceId = WORKSPACE_ID): string {
  let key = ''
  act(() => {
    useWorkspacesStore.setState({ activeWorkspaceId: workspaceId })
    key = enterNewChat(workspaceId, AGENT_ID, AGENT_TYPE)
  })
  return key
}

function connectMock(sendReturn = true) {
  const mockSend = vi.fn().mockReturnValue(sendReturn)
  act(() => {
    useConnectionStore.setState({
      connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
      isConnected: true,
    })
  })
  return mockSend
}

describe('ordinary message regressions preserved from the retired client-kickoff suite', () => {
  it('does NOT write a sessionByWorkspace descriptor for a first send from K', () => {
    // Spec 4.4.1 / 4.6.1: K is not a saved session, so the workspace map stays
    // empty and the frame carries {new_chat, K} with no session_id.
    const mockSend = connectMock(true)
    const key = openFreshChat()
    act(() => {
      useChatStore.getState().sendMessage('hello')
    })
    expect(mockSend.mock.calls[0][0]).toMatchObject({
      type: 'message',
      content: 'hello',
      origin: { kind: 'new_chat', new_chat_key: key },
    })
    expect(mockSend.mock.calls[0][0]).not.toHaveProperty('session_id')
    expect(WORKSPACE_ID in useSessionStore.getState().sessionByWorkspace).toBe(false)
  })

  it('a fresh turn from K omits session_id and carries {new_chat, K}', () => {
    // Spec 4.6.1: there is no saved session yet. The old '__pending' fixture
    // had no key, so the send was refused.
    const mockSend = connectMock(true)
    const key = openFreshChat()
    act(() => {
      useChatStore.getState().sendMessage('hello there')
    })
    expect(mockSend).toHaveBeenCalledTimes(1)
    const payload = mockSend.mock.calls[0][0]
    expect(payload).not.toHaveProperty('session_id')
    expect(payload).toMatchObject({
      type: 'message',
      content: 'hello there',
      origin: { kind: 'new_chat', new_chat_key: key },
    })
  })

  it('does NOT apply the guard while the "__pending" session is still streaming (mid-turn steering unaffected)', () => {
    const mockSend = connectMock(true)
    openFreshChat()
    act(() => {
      useSessionStore.setState({ activeSessionId: '__pending' })
      useChatStore.setState({ isStreaming: true })
    })

    act(() => {
      useChatStore.getState().sendMessage('steer me')
    })

    // Mid-turn steering under '__pending' degrades to offline-style
    // buffering (sendMessage's own isStreaming branch) — no send call.
    expect(mockSend).not.toHaveBeenCalled()
    expect(outboundQueueContents()).toContain('steer me')
  })

  it('does NOT trigger the kickoff-reject cleanup for an ordinary message-turn error (no kickoff pending)', () => {
    const SID = 'ordinary-session-1'
    connectMock(true)
    act(() => {
      useSessionStore.setState({ activeSessionId: SID })
    })

    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: 'boom' })
    })

    // Generic C8 error handling ran instead — no kickoff-specific side effects.
    // U5: a tagged chat failure stays visible in that chat, not AppShell.
    expect(useConnectionStore.getState().connectionError).toBeNull()
    expect(getMessages(useChatStore.getState().sessionsById[SID]).filter((message) => message.status === 'error').map((message) => message.content)).toStrictEqual(['boom'])
  })

  it('plain sendMessage session_started ack is unaffected (no pendingKickoff involved)', () => {
    const mockSend = connectMock(true)
    const key = openFreshChat()

    act(() => {
      useChatStore.getState().sendMessage('plain first message')
    })
    expect(mockSend).toHaveBeenCalledTimes(1)
    expect(mockSend.mock.calls[0][0]).toMatchObject({ origin: { kind: 'new_chat', new_chat_key: key } })

    // The gateway echoes the initiating message's client_message_id on this
    // ack (pkg/gateway/websocket_first_message.go::acknowledgeNewSession);
    // DEL-F21/F22 made that echo the only correlated receipt an ordinary
    // first send gets, so the fixture carries it.
    const plainSendCid = (mockSend.mock.calls[0][0] as { client_message_id?: string }).client_message_id
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started',
        session_id: 'sess-plain-1',
        agent_id: AGENT_ID,
        client_message_id: plainSendCid,
      })
    })

    expect(useSessionStore.getState().activeSessionId).toBe('sess-plain-1')
    // frame.agent_id (AGENT_ID) is what the ack carries — unrelated to the
    // kickoff's own agentId, confirming this path is unaffected by the
    // kickoff-specific handling exercised in the tests above.
    expect(useSessionStore.getState().sessionByWorkspace[WORKSPACE_ID]).toEqual({
      id: 'sess-plain-1',
      type: 'chat',
      title: null,
      agentId: AGENT_ID,
    })
  })

})

// These retain the generic assertions from original mixed cases 19/29/31.
// Their kickoff producer/duplicate-toast/special-ack contexts are no longer
// reachable. No alternate kickoff API or optimistic setup state is invented.
describe('ordinary send, error and queue guarantees after client-kickoff retirement', () => {
  it('mixed-19: an ordinary fresh send after reset omits the pending sentinel', () => {
    const mockSend = connectMock(true)
    const key = openFreshChat()
    mockSend.mockClear()
    act(() => {
      useChatStore.getState().sendMessage('hello')
    })
    expect(mockSend).toHaveBeenCalledTimes(1)
    const payload = mockSend.mock.calls[0][0]
    expect(payload).not.toHaveProperty('session_id')
    expect(payload).toMatchObject({
      type: 'message',
      content: 'hello',
      agent_id: AGENT_ID,
      origin: { kind: 'new_chat', new_chat_key: key },
    })
  })
  it('mixed-29: a tagged ordinary error stays in its named chat without workspace-setup side effects', () => {
    const SID = 'ordinary-session-tagged'
    connectMock(true)
    act(() => {
      useSessionStore.setState({ activeSessionId: SID })
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: 'boom' })
    })
    expect(useConnectionStore.getState().connectionError).toBeNull()
    expect(getMessages(useChatStore.getState().sessionsById[SID]).filter((message) => message.status === 'error').map((message) => message.content)).toStrictEqual(['boom'])
    expect(useUiStore.getState().toasts.some((t) => /workspace setup/i.test(t.message))).toBe(false)
  })
  it('mixed-31: a queued ordinary message survives disconnection and drains as one fresh turn without a sentinel session id', () => {
    const mockSend = connectMock(true)
    const key = openFreshChat('ws-other')
    act(() => {
      useConnectionStore.setState({ isConnected: false })
    })
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useSessionStore.getState().newChatKey).toBe(key)
    mockSend.mockClear()
    act(() => {
      useChatStore.getState().sendMessage('hello from ws-other')
    })
    expect(mockSend).not.toHaveBeenCalled()
    expect(outboundQueueContents()).toContain('hello from ws-other')
    act(() => {
      useConnectionStore.setState({ isConnected: true })
      useChatStore.getState().drainOutboundQueue()
    })
    expect(useChatStore.getState().outboundQueue).toEqual([])
    expect(mockSend).toHaveBeenCalledTimes(1)
    const payload = mockSend.mock.calls[0][0]
    expect(payload).toMatchObject({
      type: 'message',
      content: 'hello from ws-other',
      origin: { kind: 'new_chat', new_chat_key: key },
    })
    expect(payload).not.toHaveProperty('session_id')
    expect(useSessionStore.getState().newChatKey).toBe(key)
    expect(useChatStore.getState().messages.some((m) => m.role === 'user' && m.content === 'hello from ws-other')).toBe(true)
  })
})
