import { act, createElement, Fragment } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { VirtualUserMessageRow } from '@/components/chat/ChatScreen'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'
import type {
  ErrorFrame,
  MessageFrame,
  SessionStartedFrame,
} from '@/lib/api/generated/asyncapi-types'
import type { WsConnection } from '@/lib/ws'
import { getMessages, useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { useWorkspacesStore } from './workspacesStore'

// Oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/design-1090.md,
// D7, FOUNDER DECISION "middle way", and Team-lead ruling (frontend scope).
// Plan: T8 disconnect/reconnect; T9 exact-frame, same-ID, single-flight Retry;
// T10 recovered attach + unknown_position replay; T11 both delivery errors;
// T12 saved-but-unanswered fresh-ID Generate again. General resend is a
// separately labelled characterization control, not a new-recovery green.
// Real: store, frame reducers, reconnect helper, production user-message row.
// Replaced process edges: socket sender and clock only. No store actions,
// reducers, rendering components, or library internals are mocked or spied.
// CHECK mutations (deferred): rebuild Retry from live selections; omit recovered
// attach/flag; reuse the original ID or drop media during Generate again.
// Gaps: full browser reload, server durability/dedupe, and real-browser reachability
// belong to other lanes. No numeric bounds are introduced by this state-machine
// pack. GREEN, order-independence proof and implementation mutation belong to CHECK.

const CLIENT_ID = '1090-first-recovery'
const CONTENT = 'Keep this exact first message\nand both original attachments.  '
const SESSION_ID = '1090-recovered-session'
const SERVER_ENTRY_ID = '1090-server-user-entry'
const MEDIA_REFS = ['media://1090-original-image', 'media://1090-original-file']
// Fixture values, not observed outputs. D7 requires preserving every original
// outbound field, including whitespace, ordered refs, metadata and false Auto.
const ORIGINAL_FRAME: MessageFrame = {
  type: 'message',
  content: CONTENT,
  client_message_id: CLIENT_ID,
  agent_id: 'mia',
  media: [...MEDIA_REFS],
  metadata: { model_name: '1090-original-model', workspace_id: '1090-origin-workspace' },
  auto_approve: false,
}

function resetRecovery1090Stores() {
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
}

beforeEach(() => {
  vi.useFakeTimers()
  resetRecovery1090Stores()
})

afterEach(() => {
  cleanup()
  resetRecovery1090Stores()
  vi.clearAllTimers()
  vi.useRealTimers()
})

function connectRecovery1090Sender() {
  const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
  })
  return sender
}

function sendRecovery1090First(sender: ReturnType<typeof connectRecovery1090Sender>) {
  act(() => {
    useWorkspacesStore.setState({ activeWorkspaceId: '1090-origin-workspace' })
    useChatStore.setState({ pendingAutoApproveChoice: false })
    useChatStore.getState().sendMessage(CONTENT, {
      clientMessageId: CLIENT_ID,
      mediaRefs: [...MEDIA_REFS],
      model_name: '1090-original-model',
    })
  })
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: exact ordinary first send')
    .toStrictEqual([ORIGINAL_FRAME])
  expect(useSessionStore.getState().activeSessionId, 'fixture: no acknowledgement has arrived')
    .toBe('__pending')
  expect(useChatStore.getState().pendingKickoff, 'fixture: ordinary send is not a setup kickoff')
    .toBeNull()
  expect(recovery1090Users(), 'fixture: first user message is visible before the drop')
    .toStrictEqual([{ id: CLIENT_ID, content: CONTENT }])
}

function dropRecovery1090Socket() {
  act(() => {
    // Same actions/order as WsLifecycle::onDisconnected, as in T3/T4.
    const chat = useChatStore.getState()
    useConnectionStore.getState().recordDisconnect(
      chat.isStreaming ? chat.lastAssistantMessageId : null,
    )
    useChatStore.getState().clearStreamingState()
  })
}

function reconnectRecovery1090Socket(sender: ReturnType<typeof connectRecovery1090Sender>) {
  act(() => {
    // Same actions/order as WsLifecycle::onConnected. This must distinguish
    // an ordinary pending first send from a pending workspace-setup kickoff.
    useConnectionStore.getState().setConnected(true)
    useConnectionStore.getState().setConnectionError(null)
    reattachActiveSession(sender, useConnectionStore.getState().setConnectionError)
    useChatStore.getState().drainOutboundQueue()
  })
}

function recovery1090Users() {
  return useChatStore.getState().messages
    .filter((message) => message.role === 'user')
    .map(({ id, content }) => ({ id, content }))
}

function Recovery1090Rows() {
  const messages = useChatStore((state) => state.messages)
  return createElement(Fragment, null, ...messages
    .filter((message) => message.role === 'user')
    .map((message) => createElement(VirtualUserMessageRow, {
      key: message.id, message, skills: [], commandLabels: [], agentName: 'Mia', latest: true,
    })))
}

function requireRecovery1090Action(label: 'Retry' | 'Generate again', caseId: string) {
  const action = screen.queryByRole('button', { name: new RegExp(label) })
  if (!action) {
    // Missing implementation is a loud failing test, never a skip or fallback
    // to an unrelated action that would let an unwired UI pass.
    throw new Error(`BLOCKED: ${caseId} ${label} control not implemented — required by #1090 D7`)
  }
  return action
}

function recovery1090OutboundMessages(sender: ReturnType<typeof connectRecovery1090Sender>) {
  return sender.send.mock.calls.flatMap(([frame]) => frame.type === 'message' ? [frame] : [])
}

const DELIVERY_ERRORS = [
  { kind: 'not_saved', copy: 'Could not save message' },
  { kind: 'delivery_unknown', copy: 'Delivery not confirmed' },
] as const

describe('#1090 D7 — ordinary first-send recovery, not setup kickoff', () => {
  it('T8: drop before session_started keeps the bubble, removes the empty reply and shows Delivery not confirmed + Retry only', () => {
    const sender = connectRecovery1090Sender()
    sendRecovery1090First(sender)
    render(createElement(Recovery1090Rows))

    dropRecovery1090Socket()

    expect.soft(recovery1090Users(), 'T8: unacknowledged user bubble must stay in the foreground')
      .toStrictEqual([{ id: CLIENT_ID, content: CONTENT }])
    expect.soft(useChatStore.getState().messages.filter((message) => message.role === 'assistant'),
      'T8: remove the inert empty assistant placeholder; no answer has been confirmed',
    ).toStrictEqual([])
    expect.soft(screen.queryByText('Delivery not confirmed', { exact: true }),
      'T8: a socket drop is delivery uncertainty, not proof of a failed save',
    ).toBeInTheDocument()
    expect.soft(screen.queryByRole('button', { name: /^Retry$/ }), 'T8: keep a visible delivery Retry')
      .toBeInTheDocument()
    expect.soft(screen.queryByRole('button', { name: /^Retry$/ }), 'T8: Retry is disabled while offline')
      .toBeDisabled()
    expect.soft(screen.queryByRole('button', { name: /Generate again/ }),
      'T8: Generate again must not be offered before delivery is confirmed',
    ).not.toBeInTheDocument()

    reconnectRecovery1090Socket(sender)

    expect.soft(recovery1090Users(), 'T8: ordinary pending chat must not be abandoned on reconnect')
      .toStrictEqual([{ id: CLIENT_ID, content: CONTENT }])
    expect.soft(sender.send.mock.calls.map(([frame]) => frame), 'T8: reconnect must not retransmit automatically')
      .toStrictEqual([ORIGINAL_FRAME])
    expect.soft(screen.queryByText('Delivery not confirmed', { exact: true }), 'T8: uncertainty survives reconnect')
      .toBeInTheDocument()
    expect.soft(screen.queryByRole('button', { name: /^Retry$/ }), 'T8: Retry remains reachable after reconnect')
      .toBeInTheDocument()
    expect.soft(screen.queryByRole('button', { name: /^Retry$/ }), 'T8: reconnect enables Retry')
      .toBeEnabled()
  })

  it('T9: pressing Retry twice resends the exact original frame once with the same ID and shows Checking delivery…', () => {
    const sender = connectRecovery1090Sender()
    sendRecovery1090First(sender)
    render(createElement(Recovery1090Rows))
    dropRecovery1090Socket()
    reconnectRecovery1090Socket(sender)
    act(() => {
      // Deliberately different live controls cannot replace the saved request.
      useSessionStore.setState({ activeAgentId: 'jim' })
      useWorkspacesStore.setState({ activeWorkspaceId: '1090-other-workspace' })
      useChatStore.setState({ pendingAutoApproveChoice: true })
    })

    const retry = requireRecovery1090Action('Retry', 'T9')
    act(() => {
      // Two activations before React commits a disabled/removed button exercise
      // the store's single-flight guard, not just a rendered disabled attribute.
      fireEvent.click(retry)
      fireEvent.click(retry)
    })

    expect(recovery1090OutboundMessages(sender), 'T9: one verbatim session-less Retry, never a reconstructed request')
      .toStrictEqual([ORIGINAL_FRAME, ORIGINAL_FRAME])
    expect(recovery1090Users(), 'T9: Retry must reuse the bubble, not append a new one')
      .toStrictEqual([{ id: CLIENT_ID, content: CONTENT }])
    expect(screen.queryByText('Checking delivery…', { exact: true }), 'T9: retry-in-flight copy must be exact')
      .toBeInTheDocument()
    expect(useChatStore.getState().outboundQueue, 'T9: second press must not queue another retry').toStrictEqual([])
    expect(useChatStore.getState().pendingDrainQueue, 'T9: second press must not hide a retry in the drain queue')
      .toStrictEqual([])
  })

  it('T10: recovered:true attaches without a cursor and replays one server entry, then offers Generate again only after catch-up', () => {
    const sender = connectRecovery1090Sender()
    sendRecovery1090First(sender)
    render(createElement(Recovery1090Rows))
    // Server response is the process-edge fixture: D6 recovered:true always
    // means an already-saved entry, not permission to pre-mark streaming.
    // Additive fields were read from generated TS at e060bd065; no branch merge
    // or parallel hand-written wire type is needed to send this structural value.
    const recoveryFields = { client_message_id: CLIENT_ID, recovered: true }
    const ack: SessionStartedFrame = {
      type: 'session_started', session_id: SESSION_ID, agent_id: 'mia', ...recoveryFields,
    }
    act(() => useChatStore.getState().handleFrame(ack))

    expect.soft(sender.send.mock.calls.map(([frame]) => frame), 'T10: recovered acknowledgement must immediately attach with no cursor')
      .toStrictEqual([ORIGINAL_FRAME, { type: 'attach_session', session_id: SESSION_ID }])
    expect.soft(useChatStore.getState().isStreaming, 'T10: recovery must not invent a new running answer').toBe(false)
    expect.soft(screen.queryByText('Checking chat…', { exact: true }), 'T10: checking_existing copy is exact')
      .toBeInTheDocument()
    expect.soft(screen.queryByRole('button', { name: /Generate again/ }), 'T10: no Generate again before authoritative catch-up')
      .not.toBeInTheDocument()

    act(() => {
      // Zero is a valid fixture cursor; unknown_position is deliberately NOT
      // boot_mismatch. D7 carries an explicit recovered-first-send flag.
      useChatStore.getState().handleFrame({
        type: 'session_snapshot', session_id: SESSION_ID, reason: 'unknown_position', seq: 0, boot_id: '1090-boot',
      })
      useChatStore.getState().handleFrame({
        type: 'session_state', session_id: SESSION_ID, user_id: '1090-user',
        pending_approvals: [], emitted_at: '2026-09-30T12:00:00Z', auto_approve_modifier: true,
      })
      const replay = {
        type: 'replay_message' as const, session_id: SESSION_ID, role: 'user' as const,
        id: SERVER_ENTRY_ID, client_message_id: CLIENT_ID, content: CONTENT,
      }
      useChatStore.getState().handleFrame(replay)
      // Replaying the same entry is idempotent even after local ID re-keying.
      useChatStore.getState().handleFrame(replay)
    })
    expect.soft(recovery1090Users(), 'T10: replay reconciles by client ID to exactly one server entry')
      .toStrictEqual([{ id: SERVER_ENTRY_ID, content: CONTENT }])
    expect.soft(screen.queryByRole('button', { name: /Generate again/ }), 'T10: session_state alone is not completed catch-up')
      .not.toBeInTheDocument()
    act(() => useChatStore.getState().handleFrame({
      type: 'catch_up_complete', session_id: SESSION_ID, seq: 0, boot_id: '1090-boot', mode: 'snapshot',
    }))

    expect.soft(recovery1090Users(), 'T10: completion must not duplicate the original bubble')
      .toStrictEqual([{ id: SERVER_ENTRY_ID, content: CONTENT }])
    expect.soft(useChatStore.getState().sessionsById.__pending, 'T10: pending bucket migrated to the real session')
      .toBeUndefined()
    expect.soft(useChatStore.getState().sessionsById[SESSION_ID].unansweredLastUserMessageId,
      'T10: recovered unknown_position + last-user/no-active/no-answer is unfinished',
    ).toBe(SERVER_ENTRY_ID)
    expect.soft(screen.queryByText("Couldn't finish", { exact: true }), 'T10: recovered unanswered copy is exact')
      .toBeInTheDocument()
    expect.soft(screen.queryByRole('button', { name: /Generate again/ }), 'T10: confirmed unanswered entry offers Generate again')
      .toBeInTheDocument()
    expect.soft(useChatStore.getState().sessionsById[SESSION_ID].autoApproveEffective,
      'T10: attached server permission wins over the original mint-time false choice',
    ).toBe(true)
    expect.soft(recovery1090OutboundMessages(sender), 'T10: recovery and replay never start another answer turn')
      .toStrictEqual([ORIGINAL_FRAME])
  })

  it.each(DELIVERY_ERRORS)('T11: tagged $kind shows "$copy" + Retry without Generate again', ({ kind, copy }) => {
    const sender = connectRecovery1090Sender()
    sendRecovery1090First(sender)
    render(createElement(Recovery1090Rows))
    const taggedFields = { client_message_id: CLIENT_ID, first_message_error: kind }
    const frame: ErrorFrame = { type: 'error', message: 'First message could not be processed.', ...taggedFields }
    act(() => useChatStore.getState().handleFrame(frame))

    expect.soft(recovery1090Users(), `T11 ${kind}: keep the original message in place`)
      .toStrictEqual([{ id: CLIENT_ID, content: CONTENT }])
    expect.soft(screen.queryByText(copy, { exact: true })?.textContent,
      `T11 ${kind}: render the exact tagged delivery outcome`,
    ).toBe(copy)
    expect.soft(screen.queryByRole('button', { name: /^Retry$/ }), `T11 ${kind}: delivery Retry must be reachable`)
      .toBeInTheDocument()
    expect.soft(screen.queryByRole('button', { name: /^Retry$/ }), `T11 ${kind}: connected Retry is enabled`)
      .toBeEnabled()
    expect.soft(screen.queryByRole('button', { name: /Generate again/ }), `T11 ${kind}: delivery is not yet confirmed`)
      .not.toBeInTheDocument()
    expect.soft(useSessionStore.getState().activeSessionId, `T11 ${kind}: no invented real session`)
      .toBe('__pending')
    expect.soft(recovery1090OutboundMessages(sender), `T11 ${kind}: errors must not auto-resend`)
      .toStrictEqual([ORIGINAL_FRAME])
  })

  it('T12: answer_not_started shows Message saved, but no answer started; Generate again sends one fresh-ID turn with original media in the known session', () => {
    const sender = connectRecovery1090Sender()
    sendRecovery1090First(sender)
    render(createElement(Recovery1090Rows))
    const acknowledgementFields = { client_message_id: CLIENT_ID }
    const ack: SessionStartedFrame = {
      type: 'session_started', session_id: SESSION_ID, agent_id: 'mia', ...acknowledgementFields,
    }
    const errorFields = { client_message_id: CLIENT_ID, first_message_error: 'answer_not_started' as const }
    const error: ErrorFrame = {
      type: 'error', session_id: SESSION_ID, message: 'First message could not be processed.', ...errorFields,
    }
    act(() => {
      useChatStore.getState().handleFrame(ack)
      useChatStore.getState().handleFrame({
        type: 'message_status', session_id: SESSION_ID, client_message_id: CLIENT_ID, state: 'received',
      })
      useChatStore.getState().handleFrame(error)
    })

    expect.soft(screen.queryByText('Message saved, but no answer started', { exact: true })?.textContent,
      'T12: a saved entry with failed admission is not a delivery failure',
    ).toBe('Message saved, but no answer started')
    expect.soft(screen.queryByRole('button', { name: /^Retry$/ }), 'T12: no delivery Retry after confirmed save')
      .not.toBeInTheDocument()
    expect.soft(useSessionStore.getState().activeSessionId, 'T12: save identifies the existing real session')
      .toBe(SESSION_ID)
    const generate = requireRecovery1090Action('Generate again', 'T12')
    fireEvent.click(generate)

    const frames = recovery1090OutboundMessages(sender)
    expect(frames, 'T12: one intentional new turn, not recovery or multiple sends').toHaveLength(2)
    const freshId = frames[1].client_message_id
    expect(freshId, 'T12: Generate again supplies a nonempty client ID').toEqual(expect.any(String))
    expect(freshId, 'T12: Generate again must not hit the original first-send dedupe key').not.toBe(CLIENT_ID)
    expect(freshId, 'T12: empty is not a usable fresh ID').not.toBe('')
    expect(frames[1], 'T12: deliberate new turn keeps original content/media and targets the confirmed session')
      .toMatchObject({ type: 'message', session_id: SESSION_ID, content: CONTENT, media: MEDIA_REFS })
    expect(recovery1090Users().filter((message) => message.id === CLIENT_ID),
      'T12: original user bubble remains exactly once, not re-keyed into the new turn',
    ).toStrictEqual([{ id: CLIENT_ID, content: CONTENT }])
    expect(useChatStore.getState().sessionsById.__pending, 'T12: Generate again must not mint a second chat')
      .toBeUndefined()
  })

  it('T12: recovered unfinished Generate again uses neither the original client ID nor the re-keyed server ID and keeps original media', () => {
    const sender = connectRecovery1090Sender()
    sendRecovery1090First(sender)
    render(createElement(Recovery1090Rows))
    const recoveryFields = { client_message_id: CLIENT_ID, recovered: true }
    const ack: SessionStartedFrame = {
      type: 'session_started', session_id: SESSION_ID, agent_id: 'mia', ...recoveryFields,
    }
    act(() => {
      useChatStore.getState().handleFrame(ack)
      useChatStore.getState().handleFrame({
        type: 'session_snapshot', session_id: SESSION_ID, reason: 'unknown_position', seq: 0, boot_id: '1090-boot',
      })
      useChatStore.getState().handleFrame({
        type: 'session_state', session_id: SESSION_ID, user_id: '1090-user',
        pending_approvals: [], emitted_at: '2026-09-30T12:00:00Z', auto_approve_modifier: true,
      })
      useChatStore.getState().handleFrame({
        type: 'replay_message', session_id: SESSION_ID, role: 'user',
        id: SERVER_ENTRY_ID, client_message_id: CLIENT_ID, content: CONTENT,
      })
      useChatStore.getState().handleFrame({
        type: 'catch_up_complete', session_id: SESSION_ID, seq: 0, boot_id: '1090-boot', mode: 'snapshot',
      })
    })
    expect(recovery1090Users(), 'T12 recovered fixture: replay has genuinely re-keyed the original bubble')
      .toStrictEqual([{ id: SERVER_ENTRY_ID, content: CONTENT }])
    const generate = requireRecovery1090Action('Generate again', 'T12 recovered unfinished')
    fireEvent.click(generate)

    const frames = recovery1090OutboundMessages(sender)
    expect(frames, 'T12 recovered: only the original send and one intentional new turn').toHaveLength(2)
    const freshId = frames[1].client_message_id
    expect(freshId, 'T12 recovered: Generate again supplies a usable fresh ID').toEqual(expect.any(String))
    expect(freshId, 'T12 recovered: fresh ID must not reuse the first-send claim key').not.toBe(CLIENT_ID)
    expect(freshId, 'T12 recovered: the mutable server-entry bubble ID is not a fresh client ID')
      .not.toBe(SERVER_ENTRY_ID)
    expect(freshId, 'T12 recovered: empty is not a usable fresh ID').not.toBe('')
    expect(frames[1], 'T12 recovered: replay must not lose original media refs needed by the deliberate new turn')
      .toMatchObject({ type: 'message', session_id: SESSION_ID, content: CONTENT, media: MEDIA_REFS })
    expect(recovery1090Users().filter((message) => message.id === SERVER_ENTRY_ID),
      'T12 recovered: authoritative original entry remains exactly once',
    ).toStrictEqual([{ id: SERVER_ENTRY_ID, content: CONTENT }])
    expect(useChatStore.getState().sessionsById.__pending, 'T12 recovered: Generate again stays in the known chat')
      .toBeUndefined()
  })

  it('T12 characterization control: general resendMessage for an unrelated known-session message keeps its ID, media and single bubble', () => {
    // Characterization test: the ruling explicitly leaves other messages'
    // existing resend behavior unchanged. This green does not verify recovery.
    const sender = connectRecovery1090Sender()
    const otherId = '1090-unrelated-message'
    const otherSid = '1090-unrelated-session'
    const otherContent = 'An unrelated existing-session message'
    act(() => useSessionStore.getState().setActiveSession(otherSid, 'mia'))
    sender.send.mockReturnValueOnce(false)
    act(() => useChatStore.getState().sendMessage(otherContent, {
      clientMessageId: otherId, mediaRefs: [...MEDIA_REFS],
    }))
    expect(useChatStore.getState().messagesById[otherId].deliveryStatus, 'control: first attempt genuinely failed')
      .toBe('failed')
    act(() => useChatStore.getState().resendMessage(otherId))

    expect(sender.send).toHaveBeenCalledTimes(2)
    expect(sender.send).toHaveBeenNthCalledWith(2, {
      type: 'message', session_id: otherSid, client_message_id: otherId,
      content: otherContent, agent_id: 'mia', media: MEDIA_REFS,
    })
    expect(recovery1090Users(), 'control: ordinary resend keeps the existing bubble in place')
      .toStrictEqual([{ id: otherId, content: otherContent }])
    expect(getMessages(useChatStore.getState().sessionsById[otherSid])
      .filter((message) => message.role === 'user').map(({ id, content }) => ({ id, content })),
    'control: bucket and foreground agree on the single unrelated user message',
    ).toStrictEqual([{ id: otherId, content: otherContent }])
  })
})
