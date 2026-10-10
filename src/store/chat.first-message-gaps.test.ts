import { act, createElement, Fragment } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { VirtualUserMessageRow } from '@/components/chat/ChatScreen'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'
import type { MessageFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'
import { getMessages, useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { useWorkspacesStore } from './workspacesStore'

// Oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/design-1090.md,
// D7/D8, as retained by the founder's "middle way" ruling.
// T4 bypasses confirmation and checks only subsequent user bubbles. T10 checks
// a successful attach of an unanswered chat, not transport/replay failures or
// old-attempt correlation after abandoning a chat. This pack adds those outcomes.
// Real: store/actions, frame reducers, reconnect helper, and user-message row.
// Replaced edges: socket sender and clock. Query invalidation remains real.
// Fixtures preserve whitespace, ordered media and explicit false Auto (D7).
// No bounded numeric input is introduced. Negative cases include local sends,
// attach rejection, replay failure, missing transport and old tagged frames.
// CHECK probes (not applied here): remove failed-attach handling; resend instead
// of attaching a saved chat; accept an abandoned client ID; omit /new cleanup.
// Known gaps: /new dialog is tested in the companion composer file. Backend
// dedupe/durability, full reload and browser reachability belong to other lanes.
// RED-before-GREEN proof and mutation proof are not claimed by this pack.

type InboundFrame = Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0]

const OLD_ID = '1090-red5-original-client'
const OLD_CONTENT = 'Keep this original message\nand its exact trailing spaces.  '
const OLD_SESSION = '1090-red5-saved-chat'
const WORKSPACE = '1090-red5-workspace'
const NEW_ID = '1090-red5-new-client'
const NEW_CONTENT = 'Only this message belongs to the new chat.'
const ORIGINAL_MEDIA = ['media://1090-red5-image', 'media://1090-red5-file']
const ORIGINAL_FRAME: MessageFrame = {
  type: 'message',
  client_message_id: OLD_ID,
  content: OLD_CONTENT,
  agent_id: 'mia',
  media: [...ORIGINAL_MEDIA],
  metadata: { model_name: '1090-red5-model', workspace_id: WORKSPACE },
  auto_approve: false,
}
const ATTACH_FRAME = { type: 'attach_session', session_id: OLD_SESSION } as const
const SESSION_LIST_KEY = ['sessions', '1090-red5-discovery-control']

function resetGapStores() {
  act(() => {
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useSessionStore.setState({
      ...useSessionStore.getInitialState(),
      activeSessionId: null,
      activeAgentId: 'mia',
      attachedSessionType: null,
      attachedTaskTitle: null,
      sessionByWorkspace: {},
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
  resetGapStores()
})

afterEach(() => {
  cleanup()
  resetGapStores()
  vi.clearAllTimers()
  vi.useRealTimers()
})

function connectGapSender() {
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
    .map(({ id, content }) => ({ id, content }))
}

function sendFirst(sender: ReturnType<typeof connectGapSender>) {
  act(() => useChatStore.getState().sendMessage(OLD_CONTENT, {
    clientMessageId: OLD_ID,
    mediaRefs: [...ORIGINAL_MEDIA],
    model_name: '1090-red5-model',
  }))
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: exact original session-less request')
    .toStrictEqual([ORIGINAL_FRAME])
  expect(useSessionStore.getState().activeSessionId, 'fixture: no acknowledgement yet').toBe('__pending')
  expect(useChatStore.getState().pendingKickoff, 'fixture: ordinary first send, not a setup kickoff').toBeNull()
  expect(users(), 'fixture: original bubble is visible').toStrictEqual([{ id: OLD_ID, content: OLD_CONTENT }])
  expect(useChatStore.getState().pendingFirstSend?.payload, 'fixture: original recovery request is retained')
    .toStrictEqual(ORIGINAL_FRAME)
}

function dropAndReconnect(sender: ReturnType<typeof connectGapSender>) {
  act(() => {
    const state = useChatStore.getState()
    useConnectionStore.getState().recordDisconnect(state.isStreaming ? state.lastAssistantMessageId : null)
    useChatStore.getState().clearStreamingState()
    useConnectionStore.getState().setConnected(true)
    useConnectionStore.getState().setConnectionError(null)
    reattachActiveSession(sender, useConnectionStore.getState().setConnectionError)
    useChatStore.getState().drainOutboundQueue()
  })
  expect(useChatStore.getState().pendingFirstSend?.status, 'fixture: delivery is uncertain after disconnect')
    .toBe('unconfirmed')
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: reconnect did not retransmit')
    .toStrictEqual([ORIGINAL_FRAME])
}

function recoverFirst() {
  act(() => useChatStore.getState().handleFrame({
    type: 'session_started', session_id: OLD_SESSION, agent_id: 'mia',
    client_message_id: OLD_ID, recovered: true,
  }))
}

function GapRows() {
  const messages = useChatStore((state) => state.messages)
  return createElement(Fragment, null, ...messages.filter((message) => message.role === 'user')
    .map((message) => createElement(VirtualUserMessageRow, {
      key: message.id, message, skills: [], commandLabels: [], agentName: 'Mia', latest: true,
    })))
}

function expectFailedCheck() {
  expect(useSessionStore.getState().activeSessionId, 'D7: failed attach retains the identified saved chat')
    .toBe(OLD_SESSION)
  expect(users(), 'D7: failed attach keeps the original message, not an empty welcome screen')
    .toStrictEqual([{ id: OLD_ID, content: OLD_CONTENT }])
  const chat = useChatStore.getState()
  expect(chat.pendingFirstSend?.clientMessageId, 'D7: failed check retains the original correlation ID').toBe(OLD_ID)
  expect(chat.pendingFirstSend?.payload, 'D7: failed check must not reconstruct the request').toStrictEqual(ORIGINAL_FRAME)
  expect(chat.pendingFirstSend?.sessionId, 'D7: Retry must know which saved chat to check').toBe(OLD_SESSION)
  expect(chat.pendingFirstSend?.status, 'D7: failed attach is check failure, not failed delivery').toBe('check_failed')
  expect(chat.messages.filter((message) => message.role === 'assistant'), 'D7: no inert empty reply remains')
    .toStrictEqual([])
  expect(chat.messagesById[OLD_ID].firstSendStatus, 'D7: bubble carries the failed-check state').toBe('check_failed')
  expect(chat.messagesById[OLD_ID].deliveryStatus, 'D7: known saved message stays received').toBe('received')
  expect(chat.isStreaming, 'D7: a failed attach does not invent a running turn').toBe(false)
  expect(chat.isReplaying, 'D7: failed attach releases the replay lock').toBe(false)
  expect(chat.sessionsById[OLD_SESSION].awaitingCatchUp, 'D7: failed attach is not silently left waiting forever').toBe(false)
  expect(screen.getByText('Could not check this chat', { exact: true }).textContent,
    'D7: exact recovery-check failure copy').toBe('Could not check this chat')
  expect(screen.getByRole('button', { name: /^Retry$/ }), 'D7: keep recovery Retry reachable').toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /Generate again/ }), 'D7: unverified replay cannot offer a new answer')
    .not.toBeInTheDocument()
}

const SERVER_ENTRY = '1090-red5-server-entry'
const ANSWER_ENTRY = '1090-red5-answer-entry'
const ANSWER_CONTENT = 'This answer was already completed before recovery.'
const ACTIVE_TURN = '1090-red5-active-turn'

function replayOriginal() {
  act(() => {
    // Zero is a valid fixture cursor. D7 explicitly distinguishes this
    // recovered unknown_position snapshot from a boot-mismatch snapshot.
    useChatStore.getState().handleFrame({
      type: 'session_snapshot', session_id: OLD_SESSION, reason: 'unknown_position', seq: 0, boot_id: '1090-red5-boot',
    })
    useChatStore.getState().handleFrame({
      type: 'session_state', session_id: OLD_SESSION, user_id: '1090-red5-user',
      pending_approvals: [], emitted_at: '2026-10-01T12:00:00Z', auto_approve_modifier: true,
    })
    useChatStore.getState().handleFrame({
      type: 'replay_message', session_id: OLD_SESSION, role: 'user',
      id: SERVER_ENTRY, client_message_id: OLD_ID, content: OLD_CONTENT,
    })
  })
  expect(users(), 'fixture: replay reconciled exactly one authoritative original entry')
    .toStrictEqual([{ id: SERVER_ENTRY, content: OLD_CONTENT }])
}

function seedExistingActiveTurn() {
  // Network-edge fixture using the generated SessionStateFrame shape. The
  // session_state reducer and recovery catch-up decision both remain real.
  act(() => useChatStore.getState().handleFrame({
    type: 'session_state', session_id: OLD_SESSION, user_id: '1090-red5-user',
    pending_approvals: [], emitted_at: '2026-10-01T12:00:00Z', auto_approve_modifier: true,
    active_turn: { turn_id: ACTIVE_TURN, agent_id: 'mia', started_at: '2026-10-01T11:59:00Z' },
  }))
  expect(useChatStore.getState().sessionsById[OLD_SESSION].activeTurnId,
    'fixture: catch-up must evaluate the server-reported active turn').toBe(ACTIVE_TURN)
}

function replayExistingAnswer() {
  act(() => useChatStore.getState().handleFrame({
    type: 'replay_message', session_id: OLD_SESSION, role: 'assistant', id: ANSWER_ENTRY, content: ANSWER_CONTENT,
  }))
}

function finishRecovery() {
  act(() => useChatStore.getState().handleFrame({
    type: 'catch_up_complete', session_id: OLD_SESSION, seq: 0, boot_id: '1090-red5-boot', mode: 'snapshot',
  }))
}

const LATE_FRAMES: { label: string; frame: InboundFrame; discoversSavedChat: boolean }[] = [
  {
    label: 'recovered acknowledgement',
    frame: { type: 'session_started', session_id: OLD_SESSION, agent_id: 'mia', client_message_id: OLD_ID, recovered: true },
    discoversSavedChat: true,
  },
  {
    label: 'not-saved error',
    frame: { type: 'error', message: 'Save failed.', client_message_id: OLD_ID, first_message_error: 'not_saved' },
    discoversSavedChat: false,
  },
  {
    label: 'delivery-unknown error',
    frame: { type: 'error', message: 'Delivery unknown.', client_message_id: OLD_ID, first_message_error: 'delivery_unknown' },
    discoversSavedChat: false,
  },
  {
    label: 'saved-but-unanswered error',
    frame: { type: 'error', session_id: OLD_SESSION, message: 'No answer started.', client_message_id: OLD_ID, first_message_error: 'answer_not_started' },
    discoversSavedChat: true,
  },
  {
    label: 'received receipt',
    frame: { type: 'message_status', session_id: OLD_SESSION, client_message_id: OLD_ID, state: 'received' },
    discoversSavedChat: true,
  },
  {
    label: 'failed receipt',
    frame: { type: 'message_status', session_id: OLD_SESSION, client_message_id: OLD_ID, state: 'failed' },
    discoversSavedChat: false,
  },
]

describe('#1090 red5 — first-message state and correlation gaps', () => {
  it('D8 / FR-005: confirming + New chat clears the old pending bucket and recovery request, making old Retry inert', () => {
    const sender = connectGapSender()
    sendFirst(sender)
    dropAndReconnect(sender)

    // Wave-2 FR-005: startNewSession is the guarded + New chat action.
    // Typed /new is server-owned; it no longer dispatches this local action.
    // Opening the confirmation must retain the original delivery. Only the
    // accepted choice below may release its shared slot (the original D8 intent).
    const oldBucket = structuredClone(useChatStore.getState().sessionsById.__pending)
    const oldRequest = structuredClone(useChatStore.getState().pendingFirstSend)
    act(() => useSessionStore.getState().startNewSession())
    expect(useSessionStore.getState().newChatPrompt).toStrictEqual({
      action: 'prompt', mainSessionId: '', clientMessageId: OLD_ID, started: false,
    })
    expect(useSessionStore.getState().activeSessionId, 'FR-005: prompting does not leave the pending chat').toBe('__pending')
    expect(useChatStore.getState().sessionsById.__pending, 'FR-005: prompting preserves the complete pending bucket')
      .toStrictEqual(oldBucket)
    expect(useChatStore.getState().pendingFirstSend, 'FR-005: prompting preserves the exact recovery request')
      .toStrictEqual(oldRequest)
    expect(sender.send.mock.calls.map(([frame]) => frame), 'FR-005: prompting must not transmit or retry')
      .toStrictEqual([ORIGINAL_FRAME])
    act(() => useSessionStore.getState().startNewSession({ choice: 'confirm', clientMessageId: OLD_ID }))
    expect(useSessionStore.getState().newChatPrompt, 'FR-005: accepted confirmation closes the prompt').toBeNull()

    expect(useSessionStore.getState().activeSessionId, 'D8: foreground is now the new empty chat').toBeNull()
    expect(users(), 'D8: explicitly discarded message no longer occupies the foreground').toStrictEqual([])
    expect(useChatStore.getState().sessionsById.__pending, 'D8: clear the shared slot before another send can use it')
      .toBeUndefined()
    expect(useChatStore.getState().pendingFirstSend, 'D8: clear the request, not just the visible bubble').toBeNull()
    act(() => useChatStore.getState().retryFirstSend())
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D8: abandoned message cannot be retried from this tab')
      .toStrictEqual([ORIGINAL_FRAME])
  })

  it.each(LATE_FRAMES)('D8: late old $label after confirmed + New chat cannot change the new pending chat', ({ frame, discoversSavedChat }) => {
    const sender = connectGapSender()
    sendFirst(sender)
    dropAndReconnect(sender)
    act(() => {
      // FR-005: this fixture is the accepted + New chat decision, not /new.
      useSessionStore.getState().startNewSession({ choice: 'confirm', clientMessageId: OLD_ID })
      useChatStore.getState().sendMessage(NEW_CONTENT, { clientMessageId: NEW_ID })
    })
    expect(users(), 'fixture: shared pending slot belongs only to the new message')
      .toStrictEqual([{ id: NEW_ID, content: NEW_CONTENT }])
    expect(sender.send.mock.calls, 'fixture: old and new first sends each happened once').toHaveLength(2)
    expect(sender.send.mock.calls[1][0], 'fixture: second frame belongs to the new client ID')
      .toMatchObject({ type: 'message', client_message_id: NEW_ID, content: NEW_CONTENT })
    // D8 is an invariance requirement: compare the complete already-established
    // new attempt before/after the unrelated frame, not an implementation oracle.
    const newBucket = structuredClone(useChatStore.getState().sessionsById.__pending)
    const newRequest = structuredClone(useChatStore.getState().pendingFirstSend)
    const sentBefore = structuredClone(sender.send.mock.calls.map(([outbound]) => outbound))
    queryClient.setQueryData(SESSION_LIST_KEY, [])
    expect(queryClient.getQueryState(SESSION_LIST_KEY)?.isInvalidated,
      'instrument control: the session-list query exists and is fresh before the old frame').toBe(false)

    act(() => useChatStore.getState().handleFrame(frame))

    expect(useSessionStore.getState().activeSessionId, 'D8: old frame cannot foreground the abandoned saved chat')
      .toBe('__pending')
    expect(useWorkspacesStore.getState().activeWorkspaceId, 'D8: old frame cannot switch workspace').toBe(WORKSPACE)
    expect(users(), 'D8: no abandoned bubble contaminates the new foreground')
      .toStrictEqual([{ id: NEW_ID, content: NEW_CONTENT }])
    expect(useChatStore.getState().sessionsById.__pending, 'D8: old frame cannot change the new bucket or delivery status')
      .toStrictEqual(newBucket)
    expect(useChatStore.getState().pendingFirstSend, 'D8: old frame cannot clear or rewrite the new recovery request')
      .toStrictEqual(newRequest)
    expect(getMessages(useChatStore.getState().sessionsById.__pending)
      .filter((message) => message.role === 'user').map(({ id, content }) => ({ id, content })),
    'D8: new bucket still contains exactly its own user entry').toStrictEqual([{ id: NEW_ID, content: NEW_CONTENT }])
    expect(sender.send.mock.calls.map(([outbound]) => outbound), 'D8: no stale attach or automatic resend')
      .toStrictEqual(sentBefore)
    if (discoversSavedChat) {
      expect(queryClient.getQueryState(SESSION_LIST_KEY)?.isInvalidated,
        'D8: valid saved-chat ID must refresh session discovery without changing foreground').toBe(true)
    }
  })

  it('D7: a locally unsuccessful first send keeps the exact request and shows Delivery not confirmed, not Generate again', () => {
    const sender = connectGapSender()
    sender.send.mockReturnValueOnce(false)
    sendFirst(sender)
    render(createElement(GapRows))

    expect(useChatStore.getState().pendingFirstSend?.status, 'D7: local send failure does not prove a failed server save')
      .toBe('unconfirmed')
    expect(useChatStore.getState().messages.filter((message) => message.role === 'assistant'),
      'D7: local send failure removes only the empty reply placeholder').toStrictEqual([])
    expect(screen.getByText('Delivery not confirmed', { exact: true }).textContent, 'D7: exact uncertainty copy')
      .toBe('Delivery not confirmed')
    expect(screen.getByRole('button', { name: /^Retry$/ }), 'D7: connected user can check delivery').toBeEnabled()
    expect(screen.queryByRole('button', { name: /Generate again/ }), 'D7: no new-answer action without confirmed delivery')
      .not.toBeInTheDocument()
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D7: no automatic second send after local failure')
      .toStrictEqual([ORIGINAL_FRAME])
  })

  it('D7: a locally unsuccessful delivery Retry returns to unconfirmed without duplicating the bubble or changing its request', () => {
    const sender = connectGapSender()
    sendFirst(sender)
    dropAndReconnect(sender)
    render(createElement(GapRows))
    sender.send.mockReturnValueOnce(false)

    fireEvent.click(screen.getByRole('button', { name: /^Retry$/ }))

    expect(sender.send.mock.calls.map(([frame]) => frame), 'D7: Retry uses the same exact original frame even on failure')
      .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME])
    expect(users(), 'D7: unsuccessful Retry keeps exactly one original bubble')
      .toStrictEqual([{ id: OLD_ID, content: OLD_CONTENT }])
    expect(useChatStore.getState().pendingFirstSend?.payload, 'D7: preserve the request for a later explicit retry')
      .toStrictEqual(ORIGINAL_FRAME)
    expect(useChatStore.getState().pendingFirstSend?.status, 'D7: unsuccessful Retry releases its in-flight state')
      .toBe('unconfirmed')
    expect(screen.getByText('Delivery not confirmed', { exact: true }).textContent, 'D7: unsuccessful check restores uncertainty')
      .toBe('Delivery not confirmed')
    expect(screen.getByRole('button', { name: /^Retry$/ }), 'D7: user is not stuck in Checking delivery…').toBeEnabled()
    expect(useChatStore.getState().outboundQueue, 'D7: no hidden delivery resend').toStrictEqual([])
    expect(useChatStore.getState().pendingDrainQueue, 'D7: no hidden draining delivery resend').toStrictEqual([])
  })

  it('D7: recovered attach returning false keeps the message with Could not check this chat; Retry attaches the known chat only', () => {
    const sender = connectGapSender()
    sendFirst(sender)
    render(createElement(GapRows))
    sender.send.mockReturnValueOnce(false)

    recoverFirst()

    expectFailedCheck()
    expect(screen.getByRole('button', { name: /^Retry$/ }), 'D7: connected failed-attach Retry is enabled').toBeEnabled()
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D7: recovered ack attempted a cursor-free attach, not another message')
      .toStrictEqual([ORIGINAL_FRAME, ATTACH_FRAME])
    fireEvent.click(screen.getByRole('button', { name: /^Retry$/ }))
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D7: failed-attach Retry checks the saved chat without a new turn')
      .toStrictEqual([ORIGINAL_FRAME, ATTACH_FRAME, ATTACH_FRAME])
    expect(users(), 'D7: repeating attach keeps the same original message')
      .toStrictEqual([{ id: OLD_ID, content: OLD_CONTENT }])
    expect(useChatStore.getState().pendingFirstSend?.payload, 'D7: attach Retry still retains the original first-send ID/payload')
      .toStrictEqual(ORIGINAL_FRAME)
    expect(screen.getByText('Checking chat…', { exact: true }).textContent, 'D7: successful attach transmission awaits replay')
      .toBe('Checking chat…')
  })

  it.each(['disconnected socket', 'missing connection'] as const)(
    'D7: recovered acknowledgement with %s keeps the saved bubble and a disabled check Retry', (edge) => {
      const sender = connectGapSender()
      sendFirst(sender)
      render(createElement(GapRows))
      act(() => {
        useConnectionStore.getState().setConnected(false)
        if (edge === 'missing connection') useConnectionStore.setState({ connection: null })
      })

      recoverFirst()

      expectFailedCheck()
      expect(screen.getByRole('button', { name: /^Retry$/ }), 'D7: offline check Retry must be disabled').toBeDisabled()
      expect(sender.send.mock.calls.map(([frame]) => frame), 'D7: cannot attach without a connected transport')
        .toStrictEqual([ORIGINAL_FRAME])
    },
  )

  it.each([
    { label: 'server attach error', frame: { type: 'error', session_id: OLD_SESSION, message: 'The saved chat could not be attached.' } },
    { label: 'replay failure', frame: { type: 'done', session_id: OLD_SESSION, stats: { replay_error: true } } },
  ] satisfies { label: string; frame: InboundFrame }[])(
    'D7: recovered $label preserves the original saved message and permits only a check Retry', ({ frame }) => {
      const sender = connectGapSender()
      sendFirst(sender)
      render(createElement(GapRows))
      recoverFirst()
      expect(sender.send.mock.calls.map(([outbound]) => outbound), 'fixture: recovered attach was sent successfully')
        .toStrictEqual([ORIGINAL_FRAME, ATTACH_FRAME])

      act(() => useChatStore.getState().handleFrame(frame))

      expectFailedCheck()
      fireEvent.click(screen.getByRole('button', { name: /^Retry$/ }))
      expect(sender.send.mock.calls.map(([outbound]) => outbound), 'D7: Retry after failed replay does not resubmit the user entry')
        .toStrictEqual([ORIGINAL_FRAME, ATTACH_FRAME, ATTACH_FRAME])
      expect(useChatStore.getState().pendingFirstSend?.payload, 'D7: server failure never changes the original request')
        .toStrictEqual(ORIGINAL_FRAME)
    },
  )

  it('D7: recovered catch-up with an existing active turn stays streaming and does not offer Generate again', () => {
    const sender = connectGapSender()
    sendFirst(sender)
    render(createElement(GapRows))
    recoverFirst()
    replayOriginal()
    seedExistingActiveTurn()

    finishRecovery()

    const bucket = useChatStore.getState().sessionsById[OLD_SESSION]
    expect(bucket.activeTurnId, 'D7: delivery recovery does not replace the already-running turn').toBe(ACTIVE_TURN)
    expect(bucket.isStreaming, 'D7: catch-up displays the actual active turn').toBe(true)
    expect(useChatStore.getState().isStreaming, 'D7: active turn remains streaming in the foreground').toBe(true)
    expect(bucket.messagesById[SERVER_ENTRY].firstSendStatus, 'D7: active chat is saved, not unfinished').toBe('saved')
    expect(bucket.unansweredLastUserMessageId, 'D7: last-user alone cannot imply unanswered while a turn is active').toBeNull()
    expect(useChatStore.getState().pendingFirstSend, 'D7: successful replay releases the delivery recovery request').toBeNull()
    expect(bucket.recoveredFirstSend, 'D7: completed recovery cannot be applied again on a later catch-up').toBeUndefined()
    expect(screen.queryByRole('button', { name: /Generate again/ }), 'D7: do not offer a duplicate answer while one is active')
      .not.toBeInTheDocument()
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D7: recovery starts no new answer turn')
      .toStrictEqual([ORIGINAL_FRAME, ATTACH_FRAME])
  })

  it('D7: recovered catch-up with an existing answer keeps the answer and never labels the original message unfinished', () => {
    const sender = connectGapSender()
    sendFirst(sender)
    render(createElement(GapRows))
    recoverFirst()
    replayOriginal()
    replayExistingAnswer()

    finishRecovery()

    expect(useChatStore.getState().messages.map(({ id, role, content }) => ({ id, role, content })),
      'D7: recovery keeps the single original entry followed by its completed answer').toStrictEqual([
      { id: SERVER_ENTRY, role: 'user', content: OLD_CONTENT },
      { id: ANSWER_ENTRY, role: 'assistant', content: ANSWER_CONTENT },
    ])
    const bucket = useChatStore.getState().sessionsById[OLD_SESSION]
    expect(bucket.messagesById[SERVER_ENTRY].firstSendStatus, 'D7: an answered original entry is saved').toBe('saved')
    expect(bucket.unansweredLastUserMessageId, 'D7: an existing answer excludes the narrow unfinished rule').toBeNull()
    expect(bucket.isStreaming, 'D7: a completed answer is not a newly running turn').toBe(false)
    expect(useChatStore.getState().pendingFirstSend, 'D7: completed answer recovery releases the pending request').toBeNull()
    expect(screen.queryByText("Couldn't finish", { exact: true }), 'D7: completed answer cannot display interrupted copy')
      .not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Generate again/ }), 'D7: completed answer is not a first-send recovery failure')
      .not.toBeInTheDocument()
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D7: completed replay never regenerates automatically')
      .toStrictEqual([ORIGINAL_FRAME, ATTACH_FRAME])
  })

  it.each(['active turn', 'completed answer'] as const)(
    'D5/D7: a recovered received receipt cannot downgrade an %s or reopen delivery Retry', (outcome) => {
      const sender = connectGapSender()
      sendFirst(sender)
      render(createElement(GapRows))
      recoverFirst()
      replayOriginal()
      if (outcome === 'active turn') seedExistingActiveTurn()
      else replayExistingAnswer()
      finishRecovery()
      const transcript = useChatStore.getState().messages.map(({ id, role, content }) => ({ id, role, content }))

      act(() => useChatStore.getState().handleFrame({
        type: 'message_status', session_id: OLD_SESSION, client_message_id: OLD_ID, state: 'received',
      }))

      const bucket = useChatStore.getState().sessionsById[OLD_SESSION]
      expect(bucket.messagesById[SERVER_ENTRY].firstSendStatus, 'D5: received is a delivery lower bound, not a pending send')
        .toBe('saved')
      expect(bucket.messagesById[SERVER_ENTRY].deliveryStatus, 'D5: recovered receipt retains confirmed delivery').toBe('received')
      expect(bucket.isStreaming, 'D5: receipt cannot change actual active/completed state').toBe(outcome === 'active turn')
      expect(bucket.activeTurnId, 'D5: receipt cannot invent or end the existing turn')
        .toBe(outcome === 'active turn' ? ACTIVE_TURN : null)
      expect(bucket.unansweredLastUserMessageId, 'D5: receipt cannot mark an active/answered chat as unanswered').toBeNull()
      expect(useChatStore.getState().pendingFirstSend, 'D5: receipt cannot reopen a finished recovery request').toBeNull()
      expect(useChatStore.getState().messages.map(({ id, role, content }) => ({ id, role, content })),
        'D5: receipt cannot alter the recovered transcript').toStrictEqual(transcript)
      expect(screen.queryByRole('button', { name: /^Retry$/ }), 'D5: no delivery Retry after authoritative catch-up')
        .not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /Generate again/ }), 'D5: lower-bound receipt cannot authorize another answer')
        .not.toBeInTheDocument()
      expect(sender.send.mock.calls.map(([frame]) => frame), 'D5: lower-bound receipt cannot start a turn')
        .toStrictEqual([ORIGINAL_FRAME, ATTACH_FRAME])
    },
  )

  it('D7: successful unanswered recovery releases the request so delivery Retry cannot send again', () => {
    const sender = connectGapSender()
    sendFirst(sender)
    recoverFirst()
    replayOriginal()

    finishRecovery()

    expect(useChatStore.getState().sessionsById[OLD_SESSION].messagesById[SERVER_ENTRY].firstSendStatus,
      'D7: this is the authoritative unanswered outcome').toBe('unfinished')
    expect(useChatStore.getState().pendingFirstSend, 'D7: confirmed/reconciled entry no longer owns a delivery Retry request')
      .toBeNull()
    expect(useChatStore.getState().sessionsById[OLD_SESSION].recoveredFirstSend,
      'D7: the one-shot recovery flag is consumed after successful catch-up').toBeUndefined()
    act(() => useChatStore.getState().retryFirstSend())
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D7: only a deliberate Generate again may start another turn')
      .toStrictEqual([ORIGINAL_FRAME, ATTACH_FRAME])
    expect(users(), 'D7: finished recovery leaves exactly the authoritative original bubble')
      .toStrictEqual([{ id: SERVER_ENTRY, content: OLD_CONTENT }])
  })

  it('D7: an ordinary unknown_position snapshot without first-send recovery is not globally treated as unfinished', () => {
    const sender = connectGapSender()
    act(() => useSessionStore.getState().setActiveSession(OLD_SESSION, 'mia'))
    expect(useChatStore.getState().pendingFirstSend, 'control fixture: this is not an ordinary pending first send').toBeNull()
    render(createElement(GapRows))
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_snapshot', session_id: OLD_SESSION, reason: 'unknown_position', seq: 0, boot_id: '1090-red5-boot',
      })
      useChatStore.getState().handleFrame({
        type: 'session_state', session_id: OLD_SESSION, user_id: '1090-red5-user',
        pending_approvals: [], emitted_at: '2026-10-01T12:00:00Z', auto_approve_modifier: true,
      })
      useChatStore.getState().handleFrame({
        type: 'replay_message', session_id: OLD_SESSION, role: 'user', id: SERVER_ENTRY, content: OLD_CONTENT,
      })
    })

    finishRecovery()

    expect(users(), 'D7: ordinary snapshot still renders its transcript').toStrictEqual([{ id: SERVER_ENTRY, content: OLD_CONTENT }])
    const bucket = useChatStore.getState().sessionsById[OLD_SESSION]
    expect(bucket.unansweredLastUserMessageId, 'D7: unknown_position alone is not interruption evidence').toBeNull()
    expect(bucket.messagesById[SERVER_ENTRY].firstSendStatus, 'D7: do not apply recovered-first-send copy to an ordinary attach')
      .toBeUndefined()
    expect(screen.queryByText("Couldn't finish", { exact: true }), 'D7: no global unfinished classification')
      .not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Generate again/ }), 'D7: ordinary unknown-position control must not offer regeneration')
      .not.toBeInTheDocument()
    expect(sender.send.mock.calls, 'D7: ordinary snapshot processing cannot send an answer turn').toStrictEqual([])
  })
})
