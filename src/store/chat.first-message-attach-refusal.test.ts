import { act, createElement, Fragment } from 'react'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { VirtualUserMessageRow } from '@/components/chat/ChatScreen'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'
import type { ErrorFrame, MessageFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'
import { getMessages, useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { useWorkspacesStore } from './workspacesStore'

// Oracles: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/receipts/combo-silent.md,
// COMBO-SF-1; /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/design-1090.md,
// D7; and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/receipts/final-qa-sf1-scope-ruling.log.
// The gateway regression supplies RED. These frontend cases are compatibility
// controls: matching session_id settles checking_chat as check_failed (the
// ConnectionStatus.tsx::FIRST_SEND_COPY state for "Could not check this chat · Retry");
// a bare unrelated error must not settle it. Queues remain owned until an
// explicit attach-only Retry completes authoritative replay/catch-up.
// Real: chat/session/connection actions, reconnect helper, frame handler, queue,
// and user status/Retry row. Replaced edges: socket sender and clock only.
// No bounded numeric input. Persistence, actual server deletion and browser UAT
// are outside this store boundary. GREEN/mutations are deferred to CHECK:
// remove session matching, intercept all bare errors, or resend a receipt-backed
// original instead of attaching. No expected value was copied from a test run.

const ORIGINAL_ID = 'combo-sf1-original-client'
const ORIGINAL_TEXT = 'Keep this saved first message\nand its trailing spaces.  '
const SESSION_ID = 'combo-sf1-deleted-chat'
const WORKSPACE = 'combo-sf1-workspace'
const MODEL = 'combo-sf1-model'
const MEDIA = ['media://combo-sf1-image', 'media://combo-sf1-file']
const NOW = '2026-10-01T12:00:00.000Z'
const ORIGINAL_FRAME: MessageFrame = {
  type: 'message', client_message_id: ORIGINAL_ID, content: ORIGINAL_TEXT,
  agent_id: 'mia', media: [...MEDIA],
  metadata: { model_name: MODEL, workspace_id: WORKSPACE }, auto_approve: false,
}
const ATTACH_FRAME = { type: 'attach_session', session_id: SESSION_ID } as const
// Exactly the desired early-refusal shape asserted by the gateway regression:
// session_id from generated ErrorFrame, no client ID or first-message outcome.
const EARLY_REFUSAL: ErrorFrame = {
  type: 'error', message: 'session not found', session_id: SESSION_ID,
}
const BARE_UNRELATED_ERROR: ErrorFrame = { type: 'error', message: 'session not found' }
const QUEUED_ID = 'combo-sf1-queued-client'
const QUEUED_TEXT = 'Send this follow-up only after this chat is checked.'
const QUEUED = { id: QUEUED_ID, content: QUEUED_TEXT, timestamp: NOW }
const QUEUED_FRAME: MessageFrame = {
  type: 'message', session_id: SESSION_ID, client_message_id: QUEUED_ID,
  content: QUEUED_TEXT, agent_id: 'mia', metadata: { workspace_id: WORKSPACE },
}
const SERVER_ENTRY = 'combo-sf1-saved-entry'

function resetAttachRefusalStores() {
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
  resetAttachRefusalStores()
})

afterEach(() => {
  cleanup()
  resetAttachRefusalStores()
  vi.clearAllTimers()
  vi.useRealTimers()
})

function users() {
  return useChatStore.getState().messages.filter((message) => message.role === 'user')
    .map(({ id, clientMessageId, content }) => ({ id, clientMessageId, content }))
}

function AttachRefusalRows() {
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

function beginKnownChatCheck() {
  const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE })
    useChatStore.setState({ pendingAutoApproveChoice: false })
    useChatStore.getState().sendMessage(ORIGINAL_TEXT, {
      clientMessageId: ORIGINAL_ID, mediaRefs: [...MEDIA], model_name: MODEL,
    })
    useChatStore.getState().sendMessage(QUEUED_TEXT, { clientMessageId: QUEUED_ID, queuedAt: NOW })
  })
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: first send plus a queued follow-up, not two appends')
    .toStrictEqual([ORIGINAL_FRAME])
  expect(useChatStore.getState().outboundQueue, 'fixture: the unacknowledged first send owns the follow-up')
    .toStrictEqual([QUEUED])

  // The save acknowledgement was lost. Reconnect cannot resend it; explicit
  // same-ID Retry receives an authoritative recovered acknowledgement/receipt.
  reconnect(sender)
  expect(useChatStore.getState().pendingFirstSend?.status, 'fixture: a lost acknowledgement leaves delivery uncertain')
    .toBe('unconfirmed')
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: reconnect never retransmits the original')
    .toStrictEqual([ORIGINAL_FRAME])
  act(() => {
    useChatStore.getState().retryFirstSend()
    useChatStore.getState().handleFrame({
      type: 'session_started', session_id: SESSION_ID, agent_id: 'mia', client_message_id: ORIGINAL_ID, recovered: true,
    })
    useChatStore.getState().handleFrame({
      type: 'message_status', session_id: SESSION_ID, client_message_id: ORIGINAL_ID, state: 'received',
    })
  })
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: recovery checks the known saved chat without a cursor')
    .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME, ATTACH_FRAME])

  // Now the browser really reconnects with a known ID. Another tab's deletion
  // is represented by the incoming gateway refusal, not by seeding store state.
  reconnect(sender)
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: known-chat reconnect attaches only')
    .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME, ATTACH_FRAME, ATTACH_FRAME])
  expect(useChatStore.getState().pendingFirstSend?.status, 'fixture: early refusal arrives during checking_chat')
    .toBe('checking_chat')
  expect(useChatStore.getState().messagesById[ORIGINAL_ID].deliveryStatus, 'fixture: the genuine saved receipt is retained')
    .toBe('received')
  expect(useChatStore.getState().pendingDrainQueue, 'fixture: recovery has not drained the owned follow-up')
    .toStrictEqual([QUEUED])
  expect(users(), 'fixture: no reconnect, Retry or receipt creates a duplicate original bubble')
    .toStrictEqual([{ id: ORIGINAL_ID, clientMessageId: ORIGINAL_ID, content: ORIGINAL_TEXT }])
  return sender
}

function completeAuthoritativeCheck() {
  // A successful later response is a separate network-edge control. It is NOT
  // a claim that a deleted chat can be recreated by Retry. Only authoritative
  // replay of the original permits the waiting queue to resume.
  act(() => {
    useChatStore.getState().handleFrame({
      type: 'session_snapshot', session_id: SESSION_ID, reason: 'unknown_position', seq: 0, boot_id: 'combo-sf1-boot',
    })
    useChatStore.getState().handleFrame({
      type: 'session_state', session_id: SESSION_ID, user_id: 'combo-sf1-user', pending_approvals: [],
      emitted_at: NOW, auto_approve_modifier: true,
    })
    useChatStore.getState().handleFrame({
      type: 'replay_message', session_id: SESSION_ID, role: 'user', id: SERVER_ENTRY,
      client_message_id: ORIGINAL_ID, content: ORIGINAL_TEXT,
    })
    useChatStore.getState().handleFrame({
      type: 'catch_up_complete', session_id: SESSION_ID, seq: 0, boot_id: 'combo-sf1-boot', mode: 'snapshot',
    })
  })
}

describe('#1090 COMBO-SF-1 — early attach refusal ownership controls', () => {
  it('matching session refusal retains the saved request, offers attach-only Retry, and releases the queue only after catch-up', () => {
    const sender = beginKnownChatCheck()
    render(createElement(AttachRefusalRows))
    expect(within(screen.getByTestId('user-message')).getByRole('status').textContent, 'fixture: real known-chat checking row')
      .toBe('Checking chat…')
    const originalRequest = structuredClone(useChatStore.getState().pendingFirstSend)

    act(() => useChatStore.getState().handleFrame(EARLY_REFUSAL))

    const failed = useChatStore.getState()
    expect(failed.pendingFirstSend, 'COMBO-SF-1: only the check status changes; retain the original ID, request, workspace and known chat')
      .toStrictEqual({ ...originalRequest, status: 'check_failed' })
    expect(failed.pendingFirstSend?.payload, 'COMBO-SF-1: original whitespace, ordered media, routing, model and false Auto survive')
      .toStrictEqual(ORIGINAL_FRAME)
    expect(failed.messagesById[ORIGINAL_ID].firstSendStatus, 'COMBO-SF-1: the owned bubble leaves checking_chat')
      .toBe('check_failed')
    expect(failed.messagesById[ORIGINAL_ID].deliveryStatus, 'COMBO-SF-1: a failed chat check cannot revoke the saved receipt')
      .toBe('received')
    expect(failed.messagesById[ORIGINAL_ID].status, 'COMBO-SF-1: check failure is not confirmed failed delivery').toBe('done')
    expect(failed.isStreaming, 'COMBO-SF-1: a refusal cannot invent an answer turn').toBe(false)
    expect(failed.isReplaying, 'COMBO-SF-1: refusal releases the replay lock').toBe(false)
    expect(failed.sessionsById[SESSION_ID].awaitingCatchUp, 'COMBO-SF-1: no silently unfinished attach wait').toBe(false)
    expect(users(), 'COMBO-SF-1: exactly the original saved bubble survives the refusal')
      .toStrictEqual([{ id: ORIGINAL_ID, clientMessageId: ORIGINAL_ID, content: ORIGINAL_TEXT }])
    expect(within(screen.getByTestId('user-message')).getByRole('status').textContent, 'COMBO-SF-1: exact check-failed copy/action')
      .toBe('Could not check this chat · Retry')
    expect(screen.queryByRole('button', { name: /Generate again/ }), 'COMBO-SF-1: failed checking cannot authorize a second answer')
      .not.toBeInTheDocument()
    act(() => useChatStore.getState().drainOutboundQueue())
    expect(useChatStore.getState().pendingDrainQueue, 'D7: a failed check still owns queued messages').toStrictEqual([QUEUED])
    expect(sender.send.mock.calls.map(([frame]) => frame), 'COMBO-SF-1: refusal neither resends nor drains into the deleted chat')
      .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME, ATTACH_FRAME, ATTACH_FRAME])

    const retry = screen.getByRole('button', { name: /^Retry$/ })
    expect(retry, 'COMBO-SF-1: the real Retry action is available again').toBeEnabled()
    fireEvent.click(retry)
    act(() => useChatStore.getState().retryFirstSend())
    expect(sender.send.mock.calls.map(([frame]) => frame), 'COMBO-SF-1: one explicit Retry attaches only; no repeat append or concurrent retry')
      .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME, ATTACH_FRAME, ATTACH_FRAME, ATTACH_FRAME])
    expect(useChatStore.getState().pendingFirstSend?.status, 'COMBO-SF-1: usable Retry starts checking the same known chat')
      .toBe('checking_chat')
    expect(useChatStore.getState().pendingFirstSend?.payload, 'COMBO-SF-1: attach Retry still retains the original request')
      .toStrictEqual(ORIGINAL_FRAME)
    expect(useChatStore.getState().pendingDrainQueue, 'D7: queued messages cannot overtake the check').toStrictEqual([QUEUED])

    completeAuthoritativeCheck()

    const checked = useChatStore.getState()
    expect(checked.pendingFirstSend, 'D7: successful authoritative replay relinquishes first-send queue ownership').toBeNull()
    expect(checked.outboundQueue, 'D7: no visible queue remains stranded').toStrictEqual([])
    expect(checked.pendingDrainQueue, 'D7: the waiting follow-up is actually drained').toStrictEqual([])
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D7: only the queued follow-up sends after catch-up; original Retry stayed attach-only')
      .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME, ATTACH_FRAME, ATTACH_FRAME, ATTACH_FRAME, QUEUED_FRAME])
    // ChatMessage::clientMessageId is optional. The saved first send has a
    // replay-rekeyed id plus its original wire identity; a just-sent follow-up
    // is still keyed by its client ID (buildQueuedUserMessage's documented
    // contract). Assert the stable identity, not a receipt we never supplied.
    expect(checked.sessionsById[SESSION_ID].messagesById[QUEUED_ID].deliveryStatus,
      'D7: draining is a send, not a fabricated saved receipt for the follow-up').toBe('sending')
    expect(getMessages(checked.sessionsById[SESSION_ID]).filter((message) => message.role === 'user')
      .map(({ id, clientMessageId, content }) => ({ id, clientMessageId: clientMessageId ?? id, content })),
    'D7: authoritative original and one queued follow-up retain their exact stable identities')
      .toStrictEqual([
        { id: SERVER_ENTRY, clientMessageId: ORIGINAL_ID, content: ORIGINAL_TEXT },
        { id: QUEUED_ID, clientMessageId: QUEUED_ID, content: QUEUED_TEXT },
      ])
    expect(checked.sessionsById[SESSION_ID].messagesById[SERVER_ENTRY].deliveryStatus, 'D7: later queue drain still retains the genuine saved receipt')
      .toBe('received')
  })

  it('control: a bare unrelated error leaves the checking first send, original saved bubble and owned queue untouched', () => {
    const sender = beginKnownChatCheck()
    render(createElement(AttachRefusalRows))
    const before = useChatStore.getState()
    const pending = structuredClone(before.pendingFirstSend)
    const originalBubble = structuredClone(before.messagesById[ORIGINAL_ID])
    const sent = structuredClone(sender.send.mock.calls.map(([frame]) => frame))

    act(() => useChatStore.getState().handleFrame(BARE_UNRELATED_ERROR))
    act(() => useChatStore.getState().drainOutboundQueue())

    const after = useChatStore.getState()
    expect(after.pendingFirstSend, 'COMBO-SF-1 control: prose alone cannot assign a global error to the owned first send')
      .toStrictEqual(pending)
    expect(after.messagesById[ORIGINAL_ID], 'COMBO-SF-1 control: bare error cannot alter the original saved bubble or receipt')
      .toStrictEqual(originalBubble)
    expect(after.pendingFirstSend?.status, 'COMBO-SF-1 control: unrelated error does not settle the in-flight first-send check')
      .toBe('checking_chat')
    expect(after.pendingDrainQueue, 'COMBO-SF-1 control: unrelated error cannot free the owned queue').toStrictEqual([QUEUED])
    expect(within(screen.getByTestId('user-message')).getByRole('status').textContent, 'COMBO-SF-1 control: no unrelated failed-check action appears')
      .toBe('Checking chat…')
    expect(screen.queryByRole('button', { name: /^Retry$/ }), 'COMBO-SF-1 control: unrelated bare error must not authorize Retry')
      .not.toBeInTheDocument()
    expect(sender.send.mock.calls.map(([frame]) => frame), 'COMBO-SF-1 control: no automatic send, attach or queued turn')
      .toStrictEqual(sent)
  })
})
