// RED pack — WC-FIX RED-F, unit UF1 (DEL-F21, DEL-F22, DEL-F23).
//
// Spec source: docs/internal/specs/session-core-spec.md — DEL-F21/F22/F23 and
// BDD-12.2 ("Current correlated first-send/queued delivery … truly owned using
// canonical producer IDs, no fallback/latest guess").
//
//   DEL-F21  first-send-frames.ts::handleFirstSendFrame — old branch: a
//            `session_started` WITHOUT `client_message_id` "binds the known chat
//            but retains unconfirmed first-send state". Replacement: the
//            correlated `confirmFirstSend` path and `message_status` receipt
//            path; "nothing establishing a save from an old uncorrelated ack".
//   DEL-F22  frames.ts::createFrameSlice (`session_started`) — old branch: the
//            shared "Kickoff and legacy acknowledgements" tail migrates the
//            pending bucket/agent/mode "without a correlated receipt".
//            Replacement: ordinary `handleFirstSendFrame`/`confirmFirstSend`.
//   DEL-F23  first-send.ts::firstSendBlocksQueue — old branch: the
//            `pending.status !== 'unconfirmed'` exception lets a bound,
//            unconfirmed chat stop blocking queued sends. Replacement:
//            "correlated save/recovery gating; nothing that confirms the
//            legacy ack."
//
// Oracle provenance: the spec's replacement columns.
//   F21/F22 — an uncorrelated `session_started` must NOT establish a save:
//   the pending first-send's `sessionId` stays unset (no bound chat proved).
//   F23 — a pending first-send that is BOUND (has a sessionId) but still
//   UNCONFIRMED must keep blocking the outbound queue (the legacy exception is
//   removed).

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act } from 'react'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from './chat'
import { firstSendBlocksQueue } from './chat/first-send'
import type { ChatStore } from './chat/types'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'

const WORKSPACE = 'uf1-first-send-workspace'
const CLIENT_ID = 'uf1-client-1'
const CONTENT = 'first send that must not be saved by an uncorrelated ack'

function resetStores() {
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useSessionStore.setState({
      ...useSessionStore.getInitialState(),
      activeSessionId: null,
      activeAgentId: 'mia',
      attachedSessionType: null,
      attachedTaskTitle: null,
      sessionByWorkspace: {},
    }, true)
  })
}

function connectSender() {
  const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
  act(() => {
    useConnectionStore.setState({
      connection: sender as unknown as WsConnection,
      isConnected: true,
    })
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE })
  })
  return sender
}

beforeEach(resetStores)
afterEach(resetStores)

describe('UF1/DEL-F21+F22 — an uncorrelated session_started does not establish a first-send save', () => {
  it('leaves the pending first-send unbound when session_started carries no client_message_id', () => {
    const sender = connectSender()
    act(() => {
      useChatStore.getState().sendMessage(CONTENT, { clientMessageId: CLIENT_ID })
    })
    // Fixture: an ordinary session-less first send is pending.
    expect(sender.send).toHaveBeenCalledTimes(1)
    expect(useChatStore.getState().pendingFirstSend, 'fixture: pending first send exists').not.toBeNull()
    expect(useChatStore.getState().pendingFirstSend!.sessionId, 'fixture: not bound yet').toBeNull()

    // An uncorrelated ack arrives — NO client_message_id. This is exactly the
    // branch DEL-F21/F22 remove: it must not bind the pending chat.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started', session_id: 'uf1-saved-chat-1', agent_id: 'mia',
      })
    })

    const pending = useChatStore.getState().pendingFirstSend
    expect(pending, 'the pending first send must still exist').not.toBeNull()
    expect(pending!.sessionId, 'an uncorrelated ack must NOT bind/save the pending chat').toBeNull()
  })

  it('still binds the pending first send on a CORRELATED session_started (canonical positive control)', () => {
    const sender = connectSender()
    act(() => {
      useChatStore.getState().sendMessage(CONTENT, { clientMessageId: CLIENT_ID })
    })
    expect(sender.send).toHaveBeenCalledTimes(1)

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started', session_id: 'uf1-saved-chat-2', client_message_id: CLIENT_ID, agent_id: 'mia',
      })
    })

    // The correlated path (confirmFirstSend) clears the pending slot and homes
    // the message in the real session.
    expect(useChatStore.getState().pendingFirstSend).toBeNull()
    const user = useChatStore.getState().messages.find((m) => m.role === 'user')
    expect(user?.session_id).toBe('uf1-saved-chat-2')
  })
})

describe('UF1/DEL-F23 — firstSendBlocksQueue keeps blocking for a bound-but-unconfirmed send', () => {
  function stateWithPending(partial: { sessionId: string | null; status: string }): ChatStore {
    const base = useChatStore.getState()
    return {
      ...base,
      pendingFirstSend: {
        clientMessageId: CLIENT_ID,
        payload: { type: 'message', client_message_id: CLIENT_ID, content: CONTENT, agent_id: 'mia' } as never,
        workspaceId: WORKSPACE,
        attemptGeneration: 1,
        sessionId: partial.sessionId,
        assistantPlaceholderId: 'ph-1',
        status: partial.status,
      } as never,
      sessionsById: {
        ...base.sessionsById,
        [partial.sessionId ?? '__pending']: {
          ...(base.sessionsById[partial.sessionId ?? '__pending'] ?? {}),
          messagesById: { [CLIENT_ID]: { id: CLIENT_ID, clientMessageId: CLIENT_ID, role: 'user', content: CONTENT, session_id: partial.sessionId ?? '__pending' } },
          messageOrder: [CLIENT_ID],
        } as never,
      },
    } as ChatStore
  }

  it('returns true for a BOUND, UNCONFIRMED pending first send (legacy exception removed)', () => {
    // Spec (DEL-F23): the `status !== 'unconfirmed'` exception is removed, so a
    // bound-but-unconfirmed chat must keep blocking queued sends.
    expect(firstSendBlocksQueue(stateWithPending({ sessionId: 'uf1-bound-chat', status: 'unconfirmed' }))).toBe(true)
  })

  it('returns true for a bound, sending pending first send (canonical positive control)', () => {
    expect(firstSendBlocksQueue(stateWithPending({ sessionId: 'uf1-bound-chat', status: 'sending' }))).toBe(true)
  })

  it('returns true for an unbound pending first send (canonical positive control)', () => {
    expect(firstSendBlocksQueue(stateWithPending({ sessionId: null, status: 'sending' }))).toBe(true)
  })
})
