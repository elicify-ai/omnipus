/**
 * session_started agent synchronization after the Wave-2 picker removal.
 *
 * Oracle: agent-first-navigation-spec, FR-007/FR-024 and DEP-U1's immutable
 * owner. A same-session hint is no longer an explicit recipient selection:
 * it must keep the attached owner, including while the first ack is in flight.
 * A later acknowledgement binding a different session still adopts its owner.
 * This deliberately replaces the OLD picker-choice oracle, not the protection
 * against silently sending to the wrong agent. The real stores remain in use.
 */
import { act } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'

const MINTED_SID = 'agent-sync-race-minted-sid'

function resetStores() {
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
    })
    // No active session: the next send is a MINT (no session_id on the wire).
    useSessionStore.setState({
      ...useSessionStore.getInitialState(),
      activeSessionId: null,
      activeAgentId: 'mia',
      activeAgentType: null,
    }, true)
  })
}

beforeEach(resetStores)

/** Connects the store with a `send` spy that always succeeds. */
function connectWithSendSpy() {
  const send = vi.fn().mockReturnValue(true)
  act(() => {
    useConnectionStore.setState({
      connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as ReturnType<
        typeof useConnectionStore.getState
      >['connection'],
      isConnected: true,
      connectionError: null,
    })
  })
  return send
}

describe('chat — session_started agent sync with the session owner', () => {
  it('adopts the server owner when the first session is acknowledged', () => {
    // Given a mint sent while "mia" was active…
    connectWithSendSpy()
    act(() => {
      useChatStore.getState().sendMessage('delegate something')
    })

    // When the ack comes back naming the agent the server resolved…
    act(() => {
      useChatStore
        .getState()
        .handleFrame({ type: 'session_started', session_id: MINTED_SID, agent_id: 'mia' } as never)
    })

    // Then the server's answer is adopted (ordinary, unchanged behaviour).
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
  })

  it('keeps the pending session owner when a different agent hint arrives before its ack', () => {
    const send = connectWithSendSpy()
    act(() => {
      useChatStore.getState().sendMessage('delegate something', { clientMessageId: 'owner-race-first' })
    })
    expect(send).toHaveBeenCalledTimes(1)
    expect(send).toHaveBeenNthCalledWith(1, {
      type: 'message', content: 'delegate something', client_message_id: 'owner-race-first', agent_id: 'mia',
    })

    // Wave-2 removed the picker: setActiveSession on the same id is a hint,
    // not a user-authorized owner switch. Both before and after the ack must
    // still name the agent to whom the actual first message was sent.
    act(() => {
      useSessionStore.getState().setActiveSession(useSessionStore.getState().activeSessionId, 'jim')
    })
    expect(useSessionStore.getState().activeSessionId).toBe('__pending')
    expect(useSessionStore.getState().activeAgentId).toBe('mia')

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started', session_id: MINTED_SID, agent_id: 'mia', client_message_id: 'owner-race-first',
      })
    })
    expect(useSessionStore.getState().activeSessionId).toBe(MINTED_SID)
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
    expect(send).toHaveBeenCalledTimes(1)
    act(() => {
      useChatStore.getState().handleFrame({ type: 'done', session_id: MINTED_SID })
      useChatStore.getState().sendMessage('next turn', { clientMessageId: 'owner-race-followup' })
    })
    expect(send).toHaveBeenCalledTimes(2)
    expect(send).toHaveBeenNthCalledWith(2, {
      type: 'message', session_id: MINTED_SID, content: 'next turn',
      client_message_id: 'owner-race-followup', agent_id: 'mia',
    })
  })

  it('adopts the owner when a later acknowledgement binds a different session', () => {
    // Guard against over-correction: the marker is cleared on the first ack,
    // so a later session binding is not mistaken for a same-session hint.
    const send = connectWithSendSpy()
    act(() => {
      useChatStore.getState().sendMessage('first')
    })
    // The gateway echoes the initiating message's client_message_id on the
    // first send's ack (pkg/gateway/websocket_first_message.go::
    // acknowledgeNewSession) — DEL-F21/F22 made that echo the only
    // correlated receipt, so the first ack carries it. The SECOND ack below
    // stays uncorrelated (kickoff shape) to drive the preserved tail.
    const firstCid = (send.mock.calls[0][0] as { client_message_id?: string }).client_message_id
    act(() => {
      useChatStore
        .getState()
        .handleFrame({ type: 'session_started', session_id: MINTED_SID, agent_id: 'mia', client_message_id: firstCid } as never)
    })

    act(() => {
      useChatStore
        .getState()
        .handleFrame({ type: 'session_started', session_id: 'another-sid', agent_id: 'ava' } as never)
    })

    expect(useSessionStore.getState().activeAgentId).toBe('ava')
  })
})
