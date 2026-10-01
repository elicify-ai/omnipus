import { act, createElement, Fragment } from 'react'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { VirtualUserMessageRow } from '@/components/chat/ChatScreen'
import type { ErrorFrame, MessageFrame, SessionStartedFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { useWorkspacesStore } from './workspacesStore'

// Oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/design-1090.md,
// "PR B silent-failure review rulings", SF-2, and D7. State names/exact copy:
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/wt-red7fe/src/components/chat/ConnectionStatus.tsx::FIRST_SEND_COPY.
// A session-created frame is not a save receipt without client-ID correlation.
// Real: store/actions, frame handlers, and the user-message row. Replaced edges:
// socket sender and clock only, following the first-message-gaps harness.
// Cases: untagged acknowledgement alone; untagged then tagged not_saved;
// matching tagged acknowledgement; established-send compatibility control.
// The untagged replacement state is deliberately unspecified by SF-2: assert
// the no-Saved invariant, not a new lifecycle design. Two of four cases are negative.
// CHECK probes (deferred): infer save from an untagged ack; discard correlation
// before not_saved arrives; reject a matching tagged acknowledgement.
// Gaps: gateway size bounds/persistence and other receipt/replay paths belong
// to other packs. This file injects their wire outcomes, not a size-bound oracle.
// GREEN, mutation checks, and proof-of-failability gate are deferred to CHECK.

const CLIENT_ID = '1090-red7-first-client'
const CONTENT = 'Keep this first message and its original client identity.'
const SESSION_ID = '1090-red7-chat'
const WORKSPACE_ID = '1090-red7-workspace'
const MEDIA_REFS = ['media://1090-red7-file']
const MODEL_NAME = '1090-red7-model'
const ORIGINAL_FRAME: MessageFrame = {
  type: 'message',
  client_message_id: CLIENT_ID,
  content: CONTENT,
  agent_id: 'mia',
  media: [...MEDIA_REFS],
  metadata: { model_name: MODEL_NAME, workspace_id: WORKSPACE_ID },
  auto_approve: false,
}
const UNTAGGED_ACK: SessionStartedFrame = {
  type: 'session_started',
  session_id: SESSION_ID,
  agent_id: 'mia',
}
const NOT_SAVED_ERROR: ErrorFrame = {
  type: 'error',
  message: 'The first message was not saved.',
  client_message_id: CLIENT_ID,
  first_message_error: 'not_saved',
}

function resetSavedLabelStores() {
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
  resetSavedLabelStores()
})

afterEach(() => {
  cleanup()
  resetSavedLabelStores()
  vi.clearAllTimers()
  vi.useRealTimers()
})

function connectSavedLabelSender() {
  const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
    useChatStore.setState({ pendingAutoApproveChoice: false })
  })
  return sender
}

function sendFirstForSavedLabel(sender: ReturnType<typeof connectSavedLabelSender>) {
  act(() => useChatStore.getState().sendMessage(CONTENT, {
    clientMessageId: CLIENT_ID, mediaRefs: [...MEDIA_REFS], model_name: MODEL_NAME,
  }))
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: real sendMessage sends the original session-less request')
    .toStrictEqual([ORIGINAL_FRAME])
  expect(useSessionStore.getState().activeSessionId, 'fixture: first send awaits a real session').toBe('__pending')
  expect(useChatStore.getState().pendingKickoff, 'fixture: ordinary first send, not setup kickoff').toBeNull()
  expect(useChatStore.getState().pendingFirstSend?.payload, 'fixture: real first-send lifecycle retains this request')
    .toStrictEqual(ORIGINAL_FRAME)
  expect(useChatStore.getState().messagesById[CLIENT_ID].firstSendStatus, 'fixture: no server confirmation yet')
    .toBe('sending')
}

function SavedLabelRows() {
  const messages = useChatStore((state) => state.messages)
  return createElement(Fragment, null, ...messages.filter((message) => message.role === 'user')
    .map((message) => createElement(VirtualUserMessageRow, {
      key: message.id, message, skills: [], commandLabels: [], agentName: 'Mia', latest: true,
    })))
}

describe('#1090 SF-2 — Saved requires a correlated first-message confirmation', () => {
  it('(a) an untagged session_started alone must not mark the first message saved', () => {
    const sender = connectSavedLabelSender()
    sendFirstForSavedLabel(sender)
    render(createElement(SavedLabelRows))

    // No message_status, saved replay entry, or subsequent server frame follows.
    act(() => useChatStore.getState().handleFrame(UNTAGGED_ACK))

    expect(useChatStore.getState().messagesById[CLIENT_ID].firstSendStatus,
      'SF-2: session creation without client_message_id is not proof that this message was saved')
      .not.toBe('saved')
    expect(screen.queryByText('Saved', { exact: true }), 'SF-2: do not show a durable-save label without correlation')
      .not.toBeInTheDocument()
    expect(useChatStore.getState().messages.filter((message) => message.role === 'user')
      .map(({ id, content }) => ({ id, content })), 'SF-2: the original bubble remains visible')
      .toStrictEqual([{ id: CLIENT_ID, content: CONTENT }])
    expect(sender.send.mock.calls.map(([frame]) => frame), 'SF-2: an untagged acknowledgement cannot cause an automatic resend')
      .toStrictEqual([ORIGINAL_FRAME])
  })

  it('(b) an untagged session_started followed by tagged not_saved must show Could not save message · Retry', () => {
    const sender = connectSavedLabelSender()
    sendFirstForSavedLabel(sender)
    render(createElement(SavedLabelRows))

    act(() => useChatStore.getState().handleFrame(UNTAGGED_ACK))
    act(() => useChatStore.getState().handleFrame(NOT_SAVED_ERROR))

    expect(useChatStore.getState().messagesById[CLIENT_ID].firstSendStatus,
      'SF-2/D7: a correlated not_saved error must settle the original first message as not_saved')
      .toBe('not_saved')
    expect(screen.getByTestId('user-message-delivery-status').textContent, 'D7/FIRST_SEND_COPY: exact proven-no-save copy')
      .toBe('Could not save message · Retry')
    expect(screen.getByRole('button', { name: /^Retry$/ }), 'D7: recovery remains actionable while connected').toBeEnabled()
    expect(screen.queryByText('Saved', { exact: true }), 'SF-2: proven non-save cannot retain Saved').not.toBeInTheDocument()
    expect(useChatStore.getState().messages.filter((message) => message.role === 'user')
      .map(({ id, content }) => ({ id, content })), 'D7: failure keeps exactly the original message')
      .toStrictEqual([{ id: CLIENT_ID, content: CONTENT }])
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D7: recovery requires an explicit Retry, not an automatic resend')
      .toStrictEqual([ORIGINAL_FRAME])
  })

  it('(c) control: a client-ID-tagged session_started acknowledgement marks the matching first message saved', () => {
    const sender = connectSavedLabelSender()
    sendFirstForSavedLabel(sender)
    render(createElement(SavedLabelRows))
    const taggedAck: SessionStartedFrame = { ...UNTAGGED_ACK, client_message_id: CLIENT_ID }

    act(() => useChatStore.getState().handleFrame(taggedAck))

    expect(useChatStore.getState().messagesById[CLIENT_ID].firstSendStatus, 'SF-2/D7: matching acknowledgement proves save')
      .toBe('saved')
    expect(useChatStore.getState().messagesById[CLIENT_ID].deliveryStatus, 'D7: confirmed save is received').toBe('received')
    expect(useSessionStore.getState().activeSessionId, 'D7: matching acknowledgement binds the confirmed chat').toBe(SESSION_ID)
    expect(screen.getByTestId('user-message-delivery-status').textContent, 'FIRST_SEND_COPY: exact confirmed-save label')
      .toBe('Saved')
    expect(sender.send.mock.calls.map(([frame]) => frame), 'control: normal acknowledgement causes no delivery resend')
      .toStrictEqual([ORIGINAL_FRAME])
  })
})
