/**
 * chat.agent-sync-race.test.ts — the session_started ack must not override a
 * newer explicit agent choice.
 *
 * Contract being pinned:
 *   - A mint (send with no session_id) goes out under whatever agent is active.
 *     Its `session_started` ack carries the agent the SERVER resolved.
 *   - If the user has NOT touched the picker since that send, the ack's
 *     agent_id is authoritative and is adopted (the ordinary case: the client
 *     had no agent, the server picked one).
 *   - If the user HAS switched the picker while the ack was in flight, their
 *     newer explicit choice wins and the stale echo is ignored.
 *
 * Why this matters: without the guard, "pick Mia → send → switch to Jim" lets
 * the in-flight ack (resolved under Mia) snap the picker back to Mia, and the
 * user's next turn silently runs as an agent they did not choose. This was a
 * contributing factor in e2e T24a, where the turn ran as Mia — whose tool
 * policy denies `delegate` — after the picker had been switched to Jim.
 */
import { act } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'

const MINTED_SID = 'agent-sync-race-minted-sid'

function resetStores() {
  act(() => {
    useChatStore.setState({ sessionsById: {}, isStreaming: false })
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
    })
    // No active session: the next send is a MINT (no session_id on the wire).
    useSessionStore.setState({
      activeSessionId: null,
      activeAgentId: 'mia',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

/** Connects the store with a `send` spy that always succeeds. */
function connectWithSendSpy() {
  const send = vi.fn().mockReturnValue(true)
  act(() => {
    useConnectionStore.setState({
      connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
      isConnected: true,
      connectionError: null,
    })
  })
  return send
}

describe('chat — session_started agent sync vs. a newer explicit selection', () => {
  it('adopts the server agent when the user has NOT re-picked since the mint', () => {
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

  it('keeps the user\'s newer pick when the picker changed while the ack was in flight', () => {
    // Given a mint sent while "mia" was active…
    connectWithSendSpy()
    act(() => {
      useChatStore.getState().sendMessage('delegate something')
    })

    // …and the user switches to "jim" BEFORE the ack lands.
    act(() => {
      useSessionStore.getState().setActiveSession(useSessionStore.getState().activeSessionId, 'jim')
    })

    // When the stale ack arrives still naming "mia"…
    act(() => {
      useChatStore
        .getState()
        .handleFrame({ type: 'session_started', session_id: MINTED_SID, agent_id: 'mia' } as never)
    })

    // Then the explicit user choice wins — the echo must not reassign the agent.
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
  })

  it('does not suppress a later ack once the mint marker is consumed', () => {
    // Guard against over-correction: the marker is cleared on the first ack, so
    // a subsequent server-driven agent change is still honoured.
    connectWithSendSpy()
    act(() => {
      useChatStore.getState().sendMessage('first')
    })
    act(() => {
      useChatStore
        .getState()
        .handleFrame({ type: 'session_started', session_id: MINTED_SID, agent_id: 'mia' } as never)
    })

    act(() => {
      useChatStore
        .getState()
        .handleFrame({ type: 'session_started', session_id: 'another-sid', agent_id: 'ava' } as never)
    })

    expect(useSessionStore.getState().activeAgentId).toBe('ava')
  })
})
