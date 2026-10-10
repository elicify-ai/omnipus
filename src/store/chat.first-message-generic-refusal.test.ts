import { act, createElement, Fragment } from 'react'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { VirtualUserMessageRow } from '@/components/chat/ChatScreen'
import type { ErrorFrame, MessageFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'
import { getMessages, useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { useWorkspacesStore } from './workspacesStore'

// Oracles: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/design-1090.md,
// C3 ruling; /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/receipts/review-tests.md,
// T1 and team-lead's ID-conflict extension. check_failed and exact visible copy
// were derived from that ruling and ConnectionStatus.tsx::FIRST_SEND_COPY first.
// Real: chat/session actions, frame handler and user-message status/action row.
// Replaced process edges: socket sender and clock; no reducer/action mocks.
// Cases: two pending refusals, one late abandoned refusal, one established-chat
// generic-error control. Original whitespace, ordered media and false Auto survive.
// No bounded numeric input. Known gaps: backend persistence/dedupe and live browser
// acceptance are other lanes. CHECK probes: remove pending interception,
// forget abandoned IDs, or swallow unrelated errors. No GREEN/mutation proof here.

const ORIGINAL_ID = '1090-red6-original-client'
const ORIGINAL_TEXT = 'Keep this exact first message\nand its trailing spaces.  '
const WORKSPACE = '1090-red6-workspace'
const MODEL = '1090-red6-model'
const MEDIA = ['media://1090-red6-image', 'media://1090-red6-file']
const ORIGINAL_FRAME: MessageFrame = {
  type: 'message',
  client_message_id: ORIGINAL_ID,
  content: ORIGINAL_TEXT,
  agent_id: 'mia',
  media: [...MEDIA],
  metadata: { model_name: MODEL, workspace_id: WORKSPACE },
  auto_approve: false,
}
const REFUSALS = [
  {
    label: 'chat-check refusal',
    frame: { type: 'error', message: 'Could not check this chat', client_message_id: ORIGINAL_ID },
  },
  {
    label: 'ID-conflict refusal',
    frame: {
      type: 'error',
      message: 'client_message_id conflict: this ID was already used for a different request',
      client_message_id: ORIGINAL_ID,
    },
  },
] as const satisfies readonly { label: string; frame: ErrorFrame }[]
const NEW_ID = '1090-red6-new-client'
const NEW_TEXT = 'Only this message belongs to the new chat.'
const ESTABLISHED_SESSION = '1090-red6-established-chat'
const ESTABLISHED_ID = '1090-red6-established-client'
const ESTABLISHED_TURN = '1090-red6-established-turn'

function resetRefusalStores() {
  act(() => {
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useSessionStore.setState({
      ...useSessionStore.getInitialState(), activeSessionId: null, activeAgentId: 'mia', sessionByWorkspace: {},
    }, true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    localStorage.clear()
    sessionStorage.clear()
  })
  queryClient.clear()
}

beforeEach(() => {
  vi.useFakeTimers()
  resetRefusalStores()
})

afterEach(() => {
  cleanup()
  resetRefusalStores()
  vi.clearAllTimers()
  vi.useRealTimers()
})

function connectSender() {
  const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE })
    useChatStore.setState({ pendingAutoApproveChoice: false })
  })
  return sender
}

function users() {
  return useChatStore.getState().messages.filter((message) => message.role === 'user')
    .map(({ id, clientMessageId, content }) => ({ id, clientMessageId, content }))
}

function beginSameIdRetry() {
  const sender = connectSender()
  // A local transport failure cannot establish whether the gateway saved it.
  sender.send.mockReturnValueOnce(false)
  act(() => useChatStore.getState().sendMessage(ORIGINAL_TEXT, {
    clientMessageId: ORIGINAL_ID, mediaRefs: [...MEDIA], model_name: MODEL,
  }))
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: exact original session-less request')
    .toStrictEqual([ORIGINAL_FRAME])
  expect(useChatStore.getState().pendingFirstSend?.status, 'fixture: local failure leaves delivery uncertain')
    .toBe('unconfirmed')
  act(() => {
    useConnectionStore.getState().setConnectionError(null)
    useChatStore.getState().retryFirstSend()
  })
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: explicit Retry reuses the complete original frame')
    .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME])
  expect(useChatStore.getState().pendingFirstSend?.status, 'fixture: refusal must arrive during an in-flight check')
    .toBe('retrying')
  expect(useSessionStore.getState().activeSessionId, 'fixture: no session acknowledgement was received').toBe('__pending')
  return sender
}

function RefusalRows() {
  const messages = useChatStore((state) => state.messages)
  return createElement(Fragment, null, ...messages.filter((message) => message.role === 'user')
    .map((message) => createElement(VirtualUserMessageRow, {
      key: message.id, message, skills: [], commandLabels: [], agentName: 'Mia', latest: true,
    })))
}

describe('#1090 C3 — generic correlated first-message refusals', () => {
  it.each(REFUSALS)('$label ends same-ID Retry with check_failed, retained request and usable Retry', ({ frame, label }) => {
    const sender = beginSameIdRetry()
    render(createElement(RefusalRows))
    expect(screen.getByText('Checking delivery…', { exact: true }).textContent,
      'fixture: the real status row shows the in-flight check').toBe('Checking delivery…')

    // Exact backend shape: intentionally NO session_id or first_message_error.
    act(() => useChatStore.getState().handleFrame(frame))

    const chat = useChatStore.getState()
    expect(chat.pendingFirstSend?.status, 'C3: correlated refusal must end retrying as a failed check').toBe('check_failed')
    expect(chat.pendingFirstSend?.clientMessageId, 'C3: correlation identity survives the refusal').toBe(ORIGINAL_ID)
    expect(chat.pendingFirstSend?.payload, 'C3: preserve text, ordered media, routing and false Auto').toStrictEqual(ORIGINAL_FRAME)
    expect(chat.pendingFirstSend?.sessionId, 'C3: refusal cannot invent a confirmed saved chat').toBeNull()
    expect(users(), 'C3: exactly one original bubble remains, including its whitespace and wire ID')
      .toStrictEqual([{ id: ORIGINAL_ID, clientMessageId: ORIGINAL_ID, content: ORIGINAL_TEXT }])
    expect(chat.messagesById[ORIGINAL_ID].status, 'C3: check failure is not confirmed non-delivery').toBe('done')
    expect(chat.messagesById[ORIGINAL_ID].firstSendStatus, 'C3: bubble exposes the failed-check state').toBe('check_failed')
    expect(['received', 'working', 'failed'], 'C3: no confirmed save, running answer or confirmed non-delivery')
      .not.toContain(chat.messagesById[ORIGINAL_ID].deliveryStatus)
    expect(chat.isStreaming, 'C3: refusal ends the in-flight activity').toBe(false)
    expect(chat.isReplaying, 'C3: refusal cannot leave Retry behind a replay lock').toBe(false)
    expect(chat.messages.filter((message) => message.role === 'assistant'), 'C3: refusal cannot create a new answer turn')
      .toStrictEqual([])
    expect(Object.keys(chat.sessionsById), 'C3: refusal cannot mint or attach another chat').toStrictEqual(['__pending'])
    expect(within(screen.getByTestId('user-message')).getByRole('status').textContent, 'C3: exact failed-check copy and action')
      .toBe('Could not check this chat · Retry')
    expect(screen.queryByRole('button', { name: /Generate again/ }), 'C3: no new-turn action for an unconfirmed save')
      .not.toBeInTheDocument()
    if (label === 'ID-conflict refusal') {
      expect(useConnectionStore.getState().connectionError, 'T1: keep the exact conflict error available to the visible error banner')
        .toBe(frame.message)
    }
    expect(sender.send.mock.calls.map(([outbound]) => outbound), 'C3: no automatic resend on refusal')
      .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME])

    const retry = screen.getByRole('button', { name: /^Retry$/ })
    expect(retry, 'C3: the real recovery Retry control is enabled again').toBeEnabled()
    fireEvent.click(retry)
    expect(sender.send.mock.calls.map(([outbound]) => outbound), 'C3: one explicit click retries the original payload and ID')
      .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME, ORIGINAL_FRAME])
    expect(useChatStore.getState().pendingFirstSend?.status, 'C3: usable Retry starts the next check').toBe('retrying')
    expect(users(), 'C3: Retry never duplicates or replaces the original bubble')
      .toStrictEqual([{ id: ORIGINAL_ID, clientMessageId: ORIGINAL_ID, content: ORIGINAL_TEXT }])
  })

  it('a late abandoned first-send refusal after confirmed + New chat leaves the new bucket, status and turn unchanged', () => {
    const sender = beginSameIdRetry()
    // Wave-2 FR-005: this is the accepted + New chat confirmation, not typed
    // /new (now server-owned). Keep C3's full stale-refusal isolation assertions.
    act(() => useSessionStore.getState().startNewSession({ choice: 'confirm', clientMessageId: ORIGINAL_ID }))
    expect(useChatStore.getState().pendingFirstSend, 'fixture: confirmed + New chat really abandons the old request').toBeNull()
    expect(useChatStore.getState().sessionsById.__pending, 'fixture: confirmed + New chat clears the old shared bucket').toBeUndefined()
    act(() => useChatStore.getState().sendMessage(NEW_TEXT, { clientMessageId: NEW_ID }))
    expect(users(), 'fixture: only the new message occupies the new chat')
      .toStrictEqual([{ id: NEW_ID, clientMessageId: NEW_ID, content: NEW_TEXT }])
    const before = useChatStore.getState()
    expect(before.pendingFirstSend?.status, 'fixture: the newer send is still in flight').toBe('sending')
    expect(before.isStreaming, 'fixture: the new chat has an unrelated open turn').toBe(true)
    const newBucket = structuredClone(before.sessionsById.__pending)
    const newRequest = structuredClone(before.pendingFirstSend)
    const newMessages = structuredClone(before.messages)
    const sentBefore = structuredClone(sender.send.mock.calls.map(([outbound]) => outbound))
    const connectionErrorBefore = useConnectionStore.getState().connectionError

    act(() => useChatStore.getState().handleFrame(REFUSALS[0].frame))

    const after = useChatStore.getState()
    expect(after.sessionsById.__pending, 'C3: abandoned client-ID-only refusal must not enter the new chat generic reducer')
      .toStrictEqual(newBucket)
    expect(after.pendingFirstSend, 'C3: newer request and sending status are unchanged').toStrictEqual(newRequest)
    expect(after.messages, 'C3: newer user/assistant contents and message statuses are unchanged').toStrictEqual(newMessages)
    expect(after.isStreaming, 'C3: old refusal cannot end the new chat turn').toBe(before.isStreaming)
    expect(after.isReplaying, 'C3: old refusal cannot change new replay state').toBe(before.isReplaying)
    expect(after.sessionsById.__pending.activeTurnId, 'C3: old refusal cannot clear the newer active-turn identity')
      .toBe(newBucket.activeTurnId)
    expect(after.sessionsById.__pending.activeTurnAgentId, 'C3: old refusal cannot change the newer active-turn agent')
      .toBe(newBucket.activeTurnAgentId)
    expect(useSessionStore.getState().activeSessionId, 'C3: foreground remains the newer chat').toBe('__pending')
    expect(useConnectionStore.getState().connectionError, 'C3: old refusal cannot raise a new-chat/global error')
      .toBe(connectionErrorBefore)
    expect(sender.send.mock.calls.map(([outbound]) => outbound), 'C3: old refusal cannot trigger any new transmission')
      .toStrictEqual(sentBefore)
  })

  it('control: an unrelated established-chat error still reaches the generic reducer unchanged', () => {
    const sender = connectSender()
    act(() => {
      useSessionStore.getState().setActiveSession(ESTABLISHED_SESSION, 'mia')
      useChatStore.getState().sendMessage('An established-chat message.', { clientMessageId: ESTABLISHED_ID })
      useChatStore.getState().handleFrame({
        type: 'session_state', session_id: ESTABLISHED_SESSION, user_id: '1090-red6-user',
        pending_approvals: [], emitted_at: '2026-10-01T12:00:00Z', auto_approve_modifier: true,
        active_turn: { turn_id: ESTABLISHED_TURN, agent_id: 'mia', started_at: '2026-10-01T11:59:00Z' },
      })
    })
    const before = useChatStore.getState().sessionsById[ESTABLISHED_SESSION]
    expect(before.isStreaming, 'control fixture: there is an established in-flight reply').toBe(true)
    expect(before.activeTurnId, 'control fixture: real session_state announces the turn').toBe(ESTABLISHED_TURN)
    expect(useChatStore.getState().pendingFirstSend, 'control fixture: this is not a pending first send').toBeNull()
    const userBefore = structuredClone(getMessages(before).filter((message) => message.role === 'user'))
    const assistantIds = getMessages(before).filter((message) => message.role === 'assistant').map(({ id }) => id)
    expect(assistantIds, 'control fixture: the generic reducer has exactly one real assistant placeholder to close').toHaveLength(1)
    const sentBefore = structuredClone(sender.send.mock.calls.map(([outbound]) => outbound))
    const frame: ErrorFrame = {
      type: 'error', message: 'Could not check this chat',
      session_id: ESTABLISHED_SESSION, client_message_id: ESTABLISHED_ID,
    }

    act(() => useChatStore.getState().handleFrame(frame))

    const after = useChatStore.getState().sessionsById[ESTABLISHED_SESSION]
    expect(getMessages(after).filter((message) => message.role === 'assistant')
      .map(({ id, content, status, isStreaming }) => ({ id, content, status, isStreaming })),
    'C3 control: preserve generic inline-error semantics and exact message without a duplicate bubble')
      .toStrictEqual(assistantIds.map((id) => ({ id, content: frame.message, status: 'error', isStreaming: false })))
    expect(getMessages(after).filter((message) => message.role === 'user'), 'C3 control: generic error does not alter user entries')
      .toStrictEqual(userBefore)
    expect(after.isStreaming, 'C3 control: a genuine established-chat error ends its own turn').toBe(false)
    expect(after.activeTurnId, 'C3 control: generic reducer clears the finished turn ID').toBeNull()
    expect(after.activeTurnAgentId, 'C3 control: generic reducer clears the finished turn agent').toBeNull()
    expect(useChatStore.getState().isStreaming, 'C3 control: foreground also stops streaming').toBe(false)
    expect(useChatStore.getState().pendingFirstSend, 'C3 control: generic error does not invent first-send recovery').toBeNull()
    expect(useConnectionStore.getState().connectionError, 'C3 control: inline error is not a connection-down banner').toBeNull()
    expect(sender.send.mock.calls.map(([outbound]) => outbound), 'C3 control: error handling does not resend')
      .toStrictEqual(sentBefore)
  })
})
