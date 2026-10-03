import { act, createElement, Fragment } from 'react'
import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { VirtualUserMessageRow } from '@/components/chat/ChatScreen'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'
import type { AttachSessionFrame, ErrorFrame, MessageFrame, SessionStartedFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { useWorkspacesStore } from './workspacesStore'

// Oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/design-1090.md,
// D7, "PR B silent-failure review rulings" (SF-2/F5), and the final normal
// session-handling ruling. Independent missing cases: proof-fe1-report.md F1/F2.
// Different NONEMPTY IDs must not confirm A's save or settle A's attach check.
// Real: stores/actions, frame routing, reconnect, queue and user-message row.
// Replaced edges: socket sender and clock, following the two existing packs.
// Both cases are negative; matching recovery/receipt is a positive setup control.
// No numeric bounds here: the cursor fixture below tests propagation only.
// Empty/matching IDs are covered by the unchanged packs; persistence and UAT
// are outside this boundary. Current-code GREEN is this dispatch's requirement.
// RED/mutation proof is deferred to a DIFFERENT CHECK instance, never this writer:
// S4 removes the different-client-ID rejection in handleFirstSendFrame;
// A5 changes its exact-session error comparison into any nonempty session ID.

const CLIENT_A = '1090-foreign-ids-message-a'
const CLIENT_B = '1090-foreign-ids-message-b'
const SESSION_A = '1090-foreign-ids-session-a'
const SESSION_B = '1090-foreign-ids-session-b'
const CONTENT = 'Retain message A\nand its original trailing spaces.  '
const WORKSPACE = '1090-foreign-ids-workspace'
const MODEL = '1090-foreign-ids-model'
const MEDIA = ['media://1090-foreign-ids-image', 'media://1090-foreign-ids-file']
const NOW = '2026-10-01T12:00:00.000Z'
const ORIGINAL_FRAME: MessageFrame = {
  type: 'message', client_message_id: CLIENT_A, content: CONTENT,
  agent_id: 'mia', media: [...MEDIA],
  metadata: { model_name: MODEL, workspace_id: WORKSPACE }, auto_approve: false,
}
const ATTACH_A: AttachSessionFrame = { type: 'attach_session', session_id: SESSION_A }
const QUEUED_ID = '1090-foreign-ids-follow-up'
const QUEUED_TEXT = 'Do not send this follow-up until session A is checked.'
const QUEUED = { id: QUEUED_ID, content: QUEUED_TEXT, timestamp: NOW }
const FOREIGN_BOOT_ID = '1090-foreign-ids-boot-b'
// Arbitrary positive input position, not a timing/length threshold or boundary.
const FOREIGN_SEQ = 9
const FOREIGN_ACK: SessionStartedFrame = {
  type: 'session_started', session_id: SESSION_B, client_message_id: CLIENT_B,
  agent_id: 'jim', seq: FOREIGN_SEQ, boot_id: FOREIGN_BOOT_ID,
}
const FOREIGN_ERROR: ErrorFrame = {
  type: 'error', message: 'session not found', session_id: SESSION_B,
}

function resetForeignIdStores() {
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
  vi.setSystemTime(NOW)
  resetForeignIdStores()
})

afterEach(() => {
  cleanup()
  resetForeignIdStores()
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

function sendFirstA(sender: ReturnType<typeof connectSender>) {
  act(() => useChatStore.getState().sendMessage(CONTENT, {
    clientMessageId: CLIENT_A, mediaRefs: [...MEDIA], model_name: MODEL,
  }))
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: A sends exactly the original session-less request')
    .toStrictEqual([ORIGINAL_FRAME])
  expect(useSessionStore.getState().activeSessionId, 'fixture: A has no acknowledged real session').toBe('__pending')
  expect(useChatStore.getState().pendingKickoff, 'fixture: A is an ordinary message, not setup kickoff').toBeNull()
  expect(useChatStore.getState().pendingFirstSend?.payload, 'fixture: A owns the exact original recovery request')
    .toStrictEqual(ORIGINAL_FRAME)
  expect(useChatStore.getState().messagesById[CLIENT_A].firstSendStatus, 'fixture: A has no save confirmation yet')
    .toBe('sending')
  expect(useChatStore.getState().messagesById[CLIENT_A].deliveryStatus, 'fixture: sending A is not a received receipt')
    .toBe('sending')
}

function ForeignIdRows() {
  const messages = useChatStore((state) => state.messages)
  return createElement(Fragment, null, ...messages.filter((message) => message.role === 'user')
    .map((message) => createElement(VirtualUserMessageRow, {
      key: message.id, message, skills: [], commandLabels: [], agentName: 'Mia', latest: true,
    })))
}

function reconnect(sender: Pick<WsConnection, 'send'>) {
  act(() => {
    const chat = useChatStore.getState()
    useConnectionStore.getState().recordDisconnect(chat.isStreaming ? chat.lastAssistantMessageId : null)
    useChatStore.getState().clearStreamingState()
    useConnectionStore.getState().setConnected(true)
    useConnectionStore.getState().setConnectionError(null)
    reattachActiveSession(sender, useConnectionStore.getState().setConnectionError)
    useChatStore.getState().drainOutboundQueue()
  })
}

function beginSessionACheck() {
  const sender = connectSender()
  sendFirstA(sender)
  act(() => useChatStore.getState().sendMessage(QUEUED_TEXT, { clientMessageId: QUEUED_ID, queuedAt: NOW }))
  expect(useChatStore.getState().outboundQueue, 'fixture: A owns its queued follow-up before acknowledgement')
    .toStrictEqual([QUEUED])

  reconnect(sender)
  expect(useChatStore.getState().pendingFirstSend?.status, 'fixture: losing A\'s acknowledgement is delivery uncertainty')
    .toBe('unconfirmed')
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: reconnect does not retransmit A')
    .toStrictEqual([ORIGINAL_FRAME])
  act(() => {
    useChatStore.getState().retryFirstSend()
    useChatStore.getState().handleFrame({
      type: 'session_started', session_id: SESSION_A, agent_id: 'mia', client_message_id: CLIENT_A, recovered: true,
    })
    useChatStore.getState().handleFrame({
      type: 'message_status', session_id: SESSION_A, client_message_id: CLIENT_A, state: 'received',
    })
  })
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: matching recovery of A attaches without a cursor')
    .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME, ATTACH_A])

  reconnect(sender)
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: checking known session A reconnects by attach only')
    .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME, ATTACH_A, ATTACH_A])
  expect(useSessionStore.getState().activeSessionId, 'fixture: session A owns the foreground check').toBe(SESSION_A)
  expect(useChatStore.getState().pendingFirstSend?.sessionId, 'fixture: the check owns A, not just any nonempty session')
    .toBe(SESSION_A)
  expect(useChatStore.getState().pendingFirstSend?.status, 'fixture: B\'s error will arrive while A is checking_chat')
    .toBe('checking_chat')
  expect(useChatStore.getState().messagesById[CLIENT_A].deliveryStatus, 'fixture: A has a genuine correlated saved receipt')
    .toBe('received')
  expect(useChatStore.getState().outboundQueue, 'fixture: the follow-up is waiting in the drain queue, not discarded')
    .toStrictEqual([])
  expect(useChatStore.getState().pendingDrainQueue, 'fixture: A still owns the undrained follow-up').toStrictEqual([QUEUED])
  return sender
}

describe('#1090 SF-2/D7 — different nonempty IDs cannot settle the owned first message', () => {
  it('F1/S4: message B\'s acknowledgement cannot save A, discard its request or resend; normal B binding still runs', () => {
    const sender = connectSender()
    sendFirstA(sender)
    render(createElement(ForeignIdRows))
    const before = useChatStore.getState()
    const pending = structuredClone(before.pendingFirstSend)
    const messageOrder = [...before.sessionsById.__pending.messageOrder]

    act(() => useChatStore.getState().handleFrame(FOREIGN_ACK))

    const after = useChatStore.getState()
    // S4 must fail here on the spec-derived expected-versus-actual, not setup.
    expect(after.messagesById[CLIENT_A].firstSendStatus,
      'SF-2/F1/S4: message B\'s acknowledgement must leave message A sending, not saved').toBe('sending')
    expect(after.messagesById[CLIENT_A].deliveryStatus,
      'SF-2/F1: B\'s client ID is not a received receipt for A').toBe('sending')
    expect(after.pendingFirstSend, 'D7/F1: B cannot discard or rewrite A\'s original recovery request')
      .toStrictEqual(pending)
    expect(after.pendingFirstSend?.payload, 'D7/F1: retain A\'s exact text, ordered media, routing, model and false Auto')
      .toStrictEqual(ORIGINAL_FRAME)
    expect(screen.getByTestId('user-message-delivery-status').textContent,
      'D7/F1: A still shows its exact unconfirmed-send copy').toBe('Sending…')
    expect(screen.queryByText('Saved', { exact: true }), 'SF-2/F1: a foreign client ID cannot show Saved for A')
      .not.toBeInTheDocument()
    expect(after.messages.filter((message) => message.role === 'user')
      .map(({ id, clientMessageId, content }) => ({ id, clientMessageId, content })),
    'D7/F1: keep exactly A\'s original user bubble and identity')
      .toStrictEqual([{ id: CLIENT_A, clientMessageId: CLIENT_A, content: CONTENT }])
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D7/F1: a foreign acknowledgement causes no resend or attach')
      .toStrictEqual([ORIGINAL_FRAME])

    // F5/normal-session ruling: unknown-to-this-tab client IDs FALL THROUGH;
    // rejecting their first-send confirmation must not reject normal binding.
    expect(useSessionStore.getState().activeSessionId, 'F5/F1: normal unrelated-session binding still selects B').toBe(SESSION_B)
    expect(useSessionStore.getState().activeAgentId, 'normal handling/F1: B\'s acknowledged agent still syncs').toBe('jim')
    expect(after.sessionsById.__pending, 'normal handling/F1: migration releases the shared pending bucket').toBeUndefined()
    expect(after.sessionsById[SESSION_B].messageOrder, 'normal handling/F1: pending messages migrate in their original order')
      .toStrictEqual(messageOrder)
    expect(after.sessionsById[SESSION_B].messagesById[CLIENT_A].content, 'normal handling/F1: migration keeps A\'s exact text')
      .toBe(CONTENT)
    expect(after.sessionsById[SESSION_B].cursor, 'F5/F1: normal handling initializes B\'s own cursor and boot identity')
      .toStrictEqual({ seq: FOREIGN_SEQ, bootId: FOREIGN_BOOT_ID })
    expect(after.pendingAutoApproveChoice, 'normal handling/F1: mint-time Auto choice is cleared as before').toBeNull()
  })

  it('F2/A5: session B\'s error cannot fail session A\'s check, expose Retry or change its receipt, request, queue or traffic', () => {
    const sender = beginSessionACheck()
    render(createElement(ForeignIdRows))
    expect(within(screen.getByTestId('user-message')).getByRole('status').textContent,
      'fixture: the production row shows the owned in-flight check').toBe('Checking chat…')
    const before = useChatStore.getState()
    const pending = structuredClone(before.pendingFirstSend)
    const ownedBucket = structuredClone(before.sessionsById[SESSION_A])
    const originalBubble = structuredClone(before.messagesById[CLIENT_A])
    const generation = before.firstSendGeneration

    // No client_message_id or first_message_error: only the DIFFERENT NONEMPTY
    // session_id could be misinterpreted by A5 as an owned attach refusal.
    act(() => useChatStore.getState().handleFrame(FOREIGN_ERROR))
    act(() => useChatStore.getState().drainOutboundQueue())

    const after = useChatStore.getState()
    // A5 must fail here with checking_chat versus check_failed.
    expect(after.pendingFirstSend?.status,
      'D7/F2/A5: session B\'s error must leave session A checking_chat, not check_failed').toBe('checking_chat')
    expect(after.pendingFirstSend, 'D7/F2: B\'s error cannot change any field of A\'s retained request')
      .toStrictEqual(pending)
    expect(after.pendingFirstSend?.payload, 'D7/F2: A retains the exact original request, not B\'s error data')
      .toStrictEqual(ORIGINAL_FRAME)
    expect(after.firstSendGeneration, 'D7/F2: B\'s error is not another attempt for A').toBe(generation)
    expect(after.messagesById[CLIENT_A], 'D7/F2: A\'s saved bubble and receipt stay byte-for-byte unchanged')
      .toStrictEqual(originalBubble)
    expect(after.messagesById[CLIENT_A].deliveryStatus, 'D7/F2: B cannot revoke A\'s genuine save receipt').toBe('received')
    expect(after.messagesById[CLIENT_A].firstSendStatus, 'D7/F2: A\'s bubble is still checking, not failed').toBe('checking_chat')
    expect(after.sessionsById[SESSION_A], 'D7/F2: A\'s whole owned bucket, replay wait and recovery marker stay unchanged')
      .toStrictEqual(ownedBucket)
    expect(after.sessionsById[SESSION_A].awaitingCatchUp, 'D7/F2: B cannot finish A\'s authoritative check').toBe(true)
    expect(after.isReplaying, 'D7/F2: A remains in its in-flight check').toBe(true)
    expect(useSessionStore.getState().activeSessionId, 'D7/F2: the foreign error does not switch the foreground from A')
      .toBe(SESSION_A)
    expect(after.outboundQueue, 'D7/F2: the visible queue remains unchanged').toStrictEqual([])
    expect(after.pendingDrainQueue, 'D7/F2: the owned follow-up stays queued after an explicit drain attempt')
      .toStrictEqual([QUEUED])
    expect(within(screen.getByTestId('user-message')).getByRole('status').textContent,
      'D7/F2: the exact Checking chat… label remains, not Could not check this chat · Retry').toBe('Checking chat…')
    expect(screen.queryByRole('button', { name: /^Retry$/ }), 'D7/F2: B\'s error must not expose Retry for A')
      .not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Generate again/ }), 'D7/F2: B\'s error cannot authorize a second answer')
      .not.toBeInTheDocument()
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D7/F2: B\'s error causes no resend, attach or queued-message send')
      .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME, ATTACH_A, ATTACH_A])
  })
})
