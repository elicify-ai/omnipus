// #1140 — Retry on an unconfirmed first message must never be sent to another chat.
//
// Oracle: the issue's own steps 1-5 and its Definition of done: "Retry on A
// resends A's original request (same chat-less frame, same metadata) or is
// clearly refused; it never targets chat B." No expected value was read from
// the implementation. Real: chat/session/connection stores, frame handler,
// reconnect helper, resend action. Replaced edge: the socket sender only.
// jsdom/store boundary: the server-side effect of a misrouted message is not
// observable here (the issue says it is unknown).
import { act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'
import type { MessageFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { seedCanonicalSession } from '../../../tests/unit/fixtures/canonical-session'

const A_ID = 'f1140-client-a'
const A_TEXT = 'First message of chat A, never confirmed.'
const A_WORKSPACE = 'f1140-workspace-a'
const A_MODEL = 'f1140-model-a'
let run = 0
let CHAT_B = 'f1140-chat-b'
let FOREIGN_ID = 'f1140-client-b'

const ORIGINAL_A_FRAME: MessageFrame = {
  type: 'message', client_message_id: A_ID, content: A_TEXT, agent_id: 'mia',
  metadata: { model_name: A_MODEL, workspace_id: A_WORKSPACE }, auto_approve: false,
}

function resetStores() {
  act(() => {
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useSessionStore.setState({
      ...useSessionStore.getInitialState(), activeSessionId: null, activeAgentId: 'mia', sessionByWorkspace: {},
    }, true)
    // Fresh containers: the shared initial state's own objects must not carry a previous test's chat bucket.
    useChatStore.setState({ ...useChatStore.getInitialState(), sessionsById: {}, abandonedFirstSendIds: [], pendingDrainQueue: [], outboundQueue: [] }, true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    localStorage.clear()
    sessionStorage.clear()
  })
  queryClient.clear()
}
beforeEach(resetStores)
afterEach(resetStores)

/** Steps 1-4 of the issue: unconfirmed first send in A, then B's foreign acknowledgement and finished answer. */
function unconfirmedAThenForeignB() {
  // Fresh ids per run: no module-level state keyed by an earlier run's chat id can leak in.
  run += 1
  CHAT_B = `f1140-chat-b-${run}`
  FOREIGN_ID = `f1140-client-b-${run}`
  const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useWorkspacesStore.setState({ activeWorkspaceId: A_WORKSPACE })
    useChatStore.setState({ pendingAutoApproveChoice: false })
    useChatStore.getState().sendMessage(A_TEXT, { clientMessageId: A_ID, model_name: A_MODEL })
  })
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: A went out once as a chat-less first message')
    .toStrictEqual([ORIGINAL_A_FRAME])

  // The acknowledgement for A was lost; the connection drops and comes back.
  act(() => {
    const chat = useChatStore.getState()
    useConnectionStore.getState().recordDisconnect(chat.isStreaming ? chat.lastAssistantMessageId : null)
    useChatStore.getState().clearStreamingState()
    useConnectionStore.getState().setConnected(true)
    useConnectionStore.getState().setConnectionError(null)
    reattachActiveSession(sender, useConnectionStore.getState().setConnectionError)
  })
  expect(useChatStore.getState().pendingFirstSend?.status, 'fixture: A shows Delivery not confirmed')
    .toBe('unconfirmed')

  // A confirmation for a DIFFERENT chat arrives and B finishes its answer.
  act(() => {
    const chat = useChatStore.getState()
    chat.handleFrame({ type: 'session_started', session_id: CHAT_B, agent_id: 'mia', client_message_id: FOREIGN_ID })
    chat.handleFrame({ type: 'token', session_id: CHAT_B, message_id: 'f1140-b-answer', turn_id: 'f1140-b-turn', content: 'B answer' })
    chat.handleFrame({ type: 'done', session_id: CHAT_B })
  })
  // In real use B is an ordinary loaded chat, owned by another agent and workspace.
  seedCanonicalSession(CHAT_B, 'jim', 'f1140-workspace-b')
  return sender
}

/** Every frame sent after the original first message. */
const resent = (sender: { send: ReturnType<typeof vi.fn> }) => sender.send.mock.calls.slice(1).map(([frame]) => frame as MessageFrame)

const REFUSAL = 'This message belongs to another chat, send it again.'
const toastMessages = () => useUiStore.getState().toasts.map((t) => t.message)

const ORPHAN_OTHER_PENDING = () => ({
  clientMessageId: 'f1140-other-first-send', payload: { ...ORIGINAL_A_FRAME, client_message_id: 'f1140-other-first-send', content: 'Another first message' },
  workspaceId: null, attemptGeneration: 99, sessionId: null, assistantPlaceholderId: 'f1140-other-placeholder', status: 'unconfirmed' as const,
})

describe('#1140: Retry on an unconfirmed first message never targets another chat', () => {
  it('the visible Retry button (resendMessage) refuses with a message and sends nothing', () => {
    const sender = unconfirmedAThenForeignB()
    act(() => { useChatStore.getState().resendMessage(A_ID) })
    expect(resent(sender), 'nothing at all goes out on the socket').toStrictEqual([])
    expect(toastMessages(), 'the user is told why').toContain(REFUSAL)
  })

  it('the direct retryFirstSend action refuses with a message and sends nothing', () => {
    const sender = unconfirmedAThenForeignB()
    act(() => { useChatStore.getState().retryFirstSend() })
    expect(resent(sender)).toStrictEqual([])
    expect(toastMessages()).toContain(REFUSAL)
  })

  it('the refusal survives abandonPendingFirstSend: the old bubble still never goes to chat B', () => {
    const sender = unconfirmedAThenForeignB()
    act(() => { useChatStore.getState().abandonPendingFirstSend() })
    act(() => { useChatStore.getState().resendMessage(A_ID) })
    expect(resent(sender), 'nothing goes out after the pending record was cleared').toStrictEqual([])
    expect(toastMessages()).toContain(REFUSAL)
  })

  it('the refusal survives the pending record being cleared or taken by another message', () => {
    const sender = unconfirmedAThenForeignB()
    act(() => { useChatStore.setState({ pendingFirstSend: null }) })
    act(() => { useChatStore.getState().resendMessage(A_ID) })
    expect(resent(sender)).toStrictEqual([])
    expect(toastMessages()).toContain(REFUSAL)
    act(() => {
      useChatStore.setState({ pendingFirstSend: { ...ORPHAN_OTHER_PENDING() } })
      useChatStore.getState().resendMessage(A_ID)
    })
    expect(resent(sender), 'a different message owning the slot does not free A to be sent').toStrictEqual([])
  })

  it.each([
    ['an ordinary failed message of chat B', {}],
    ['a saved first message of chat B whose later delivery state is failed', { firstSendStatus: 'saved' as const }],
  ])('no false positive: Retry on %s still resends it to chat B', (_label, extra) => {
    const sender = unconfirmedAThenForeignB()
    act(() => {
      useChatStore.setState((state) => {
        const bucket = state.sessionsById[CHAT_B]
        const message = {
          id: 'f1140-b-failed', clientMessageId: 'f1140-b-failed', session_id: CHAT_B, role: 'user' as const,
          content: 'B follow-up that failed', timestamp: new Date().toISOString(), status: 'error' as const,
          deliveryStatus: 'failed' as const, ...extra,
        }
        return {
          sessionsById: {
            ...state.sessionsById,
            [CHAT_B]: { ...bucket, messagesById: { ...bucket.messagesById, [message.id]: message }, messageOrder: [...bucket.messageOrder, message.id] },
          },
        }
      })
    })
    act(() => { useChatStore.getState().resendMessage('f1140-b-failed') })
    const frames = resent(sender)
    expect(frames.map((frame) => ({ id: frame.client_message_id, sid: frame.session_id })),
      'exactly that message goes to its own chat').toStrictEqual([{ id: 'f1140-b-failed', sid: CHAT_B }])
    expect(toastMessages()).not.toContain(REFUSAL)
  })

  it('normal case: Retry with no foreign acknowledgement resends the original request', () => {
    const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
    act(() => {
      useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
      useConnectionStore.getState().setConnected(true)
      useWorkspacesStore.setState({ activeWorkspaceId: A_WORKSPACE })
      useChatStore.setState({ pendingAutoApproveChoice: false })
      useChatStore.getState().sendMessage(A_TEXT, { clientMessageId: A_ID, model_name: A_MODEL })
    })
    act(() => {
      useConnectionStore.getState().recordDisconnect(null)
      useChatStore.getState().clearStreamingState()
      useConnectionStore.getState().setConnected(true)
      reattachActiveSession(sender, useConnectionStore.getState().setConnectionError)
    })
    expect(useChatStore.getState().pendingFirstSend?.status).toBe('unconfirmed')
    act(() => { useChatStore.getState().resendMessage(A_ID) })
    expect(resent(sender), 'exactly the original chat-less request goes out again').toStrictEqual([ORIGINAL_A_FRAME])
    expect(toastMessages()).not.toContain(REFUSAL)
  })
})
