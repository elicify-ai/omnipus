// chat.recipient-send.test.ts — the @-recipient on the outbound WS frame
// (founder decision 2026-10-11 Q1 B: the restored @ agent menu addresses the
// MESSAGE; FR-029 — no in-chat handover).
//
// Contract under test (contracts/asyncapi.yaml MessageFrame.recipient —
// "valid only together with session_id"; server side:
// pkg/gateway/ws_recipient.go::refuseRecipient):
//   - the recipient is derived AT SEND TIME from the leading "@<Agent name>"
//     prefix of the message content (src/lib/recipientAddress.ts) and the
//     content keeps the @-text verbatim;
//   - the frame carries recipient {workspace_id, agent_id} TOGETHER WITH the
//     current chat's session_id;
//   - an edited/deleted prefix sends NO recipient (not sticky);
//   - a message with no session_id yet never sends (or queues into) a
//     recipient — and an @-addressed QUEUED message with no session holds the
//     outbound queue with a visible warning instead of degrading to an
//     unaddressed send (never silently drop the @).

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { makeBucketMessages, useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import { useUiStore } from './ui'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'
import type { Agent } from '@/lib/api'
import { makeAgent } from '@/test/factories'

const TEST_SESSION_ID = 'sess_recipient_test'
const WORKSPACE_ID = 'ws_recipient_test'

const agentFixtures: Agent[] = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active' }),
  makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'idle' }),
  makeAgent({ id: 'max', name: 'Max Worker', type: 'Subagent', status: 'active' }),
]

function resetStores() {
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      outboundQueue: [],
      pendingDrainQueue: [],
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
      reconnectPhase: null,
      reconnectAttempt: 0,
    })
    useSessionStore.setState({
      activeSessionId: null,
      activeAgentId: null,
      activeAgentType: null,
    })
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
    useUiStore.setState({ toasts: [] })
  })
  queryClient.setQueryData(['agents'], agentFixtures)
}

beforeEach(resetStores)

function connectWithSendSpy() {
  const send = vi.fn().mockReturnValue(true)
  act(() => {
    useConnectionStore.setState({
      connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
      isConnected: true,
      connectionError: null,
    })
  })
  return send
}

function sentPayloads(send: ReturnType<typeof vi.fn>): Array<Record<string, unknown>> {
  return send.mock.calls.map((call) => call[0] as Record<string, unknown>)
}

describe('sendMessage — the @-recipient rides the frame with session_id', () => {
  it('an ordinary send in an existing chat carries recipient + session_id, content verbatim', () => {
    act(() => { useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' }) })
    const send = connectWithSendSpy()

    act(() => { useChatStore.getState().sendMessage('@Mia please review X') })

    expect(send).toHaveBeenCalledTimes(1)
    expect(sentPayloads(send)[0]).toMatchObject({
      type: 'message',
      content: '@Mia please review X',
      session_id: TEST_SESSION_ID,
      recipient: { workspace_id: WORKSPACE_ID, agent_id: 'mia' },
    })
  })

  it('a mid-turn steering send carries the recipient too (session_id present)', () => {
    act(() => { useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' }) })
    const send = connectWithSendSpy()
    // Start a real turn so isStreaming is true at the bucket level.
    act(() => { useChatStore.getState().sendMessage('first message') })
    send.mockClear()

    act(() => { useChatStore.getState().sendMessage('@Mia also check the logs') })

    expect(send).toHaveBeenCalledTimes(1)
    expect(sentPayloads(send)[0]).toMatchObject({
      type: 'message',
      content: '@Mia also check the logs',
      session_id: TEST_SESSION_ID,
      recipient: { workspace_id: WORKSPACE_ID, agent_id: 'mia' },
    })
  })

  it('an EDITED prefix sends NO recipient (the derivation is not sticky)', () => {
    act(() => { useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' }) })
    const send = connectWithSendSpy()

    act(() => { useChatStore.getState().sendMessage('@Miax please review X') })
    act(() => { useChatStore.getState().sendMessage('@mia please review X') }) // case edit is an edit

    for (const payload of sentPayloads(send)) {
      expect(payload.recipient).toBeUndefined()
      expect(payload.session_id).toBe(TEST_SESSION_ID)
    }
  })

  it('a partial or non-leading @ never produces a recipient', () => {
    act(() => { useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' }) })
    const send = connectWithSendSpy()

    act(() => { useChatStore.getState().sendMessage('@M please review X') })
    act(() => { useChatStore.getState().sendMessage('please @Mia review X') })

    for (const payload of sentPayloads(send)) {
      expect(payload.recipient).toBeUndefined()
    }
  })

  it('addressing the chat\'s OWN agent sends no recipient (the server would refuse it)', () => {
    act(() => { useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' }) })
    const send = connectWithSendSpy()

    act(() => { useChatStore.getState().sendMessage('@Jim I mean you') })

    expect(sentPayloads(send)[0].recipient).toBeUndefined()
  })

  it('a worker named in the prefix sends no recipient (workers are never addressable)', () => {
    act(() => { useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' }) })
    const send = connectWithSendSpy()

    act(() => { useChatStore.getState().sendMessage('@Max Worker dig here') })

    expect(sentPayloads(send)[0].recipient).toBeUndefined()
  })

  it('no active workspace means no recipient (the pair needs a workspace_id)', () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: null })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' })
    })
    const send = connectWithSendSpy()

    act(() => { useChatStore.getState().sendMessage('@Mia please review X') })

    expect(sentPayloads(send)[0].recipient).toBeUndefined()
  })
})

describe('the outbound queue holds an @-addressed message while no session exists', () => {
  it('a queued @-message does not drain into an unaddressed first send: it holds with a visible warning', () => {
    // Offline: the @-message queues (existing offline buffering).
    act(() => { useChatStore.getState().sendMessage('@Mia please review X') })
    expect(useChatStore.getState().outboundQueue).toHaveLength(1)

    // Reconnect while the chat STILL has no session (brand-new chat).
    connectWithSendSpy()
    act(() => { useChatStore.getState().drainOutboundQueue() })

    // Nothing was sent — the message can neither go out addressed (no
    // session_id) nor silently unaddressed (that would drop the @).
    expect(useChatStore.getState().pendingDrainQueue).toHaveLength(1)
    expect(useUiStore.getState().toasts.some((t) => t.message.includes('@Mia'))).toBe(true)
  })

  it('once a session exists, the held message drains WITH its recipient', () => {
    act(() => { useChatStore.getState().sendMessage('@Mia please review X') })
    const send = connectWithSendSpy()
    act(() => { useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' }) })

    act(() => { useChatStore.getState().drainOutboundQueue() })

    expect(send).toHaveBeenCalledTimes(1)
    expect(sentPayloads(send)[0]).toMatchObject({
      content: '@Mia please review X',
      session_id: TEST_SESSION_ID,
      recipient: { workspace_id: WORKSPACE_ID, agent_id: 'mia' },
    })
    expect(useChatStore.getState().pendingDrainQueue).toHaveLength(0)
  })

  it('an unaddressed queued message drains exactly as before (no behavioural change)', () => {
    act(() => { useChatStore.getState().sendMessage('plain first message') })
    const send = connectWithSendSpy()
    act(() => { useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' }) })

    act(() => { useChatStore.getState().drainOutboundQueue() })

    expect(send).toHaveBeenCalledTimes(1)
    expect(sentPayloads(send)[0]).toMatchObject({ content: 'plain first message', session_id: TEST_SESSION_ID })
    expect(sentPayloads(send)[0].recipient).toBeUndefined()
  })
})

describe('resendMessage (Retry) re-derives the recipient from the kept content', () => {
  it('a retried @-message carries the recipient again', () => {
    act(() => { useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' }) })
    // A send that FAILS marks the user bubble 'error' with the Retry affordance.
    const send = vi.fn().mockReturnValue(false)
    act(() => {
      useConnectionStore.setState({
        connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
        connectionError: null,
      })
    })
    act(() => { useChatStore.getState().sendMessage('@Mia please review X') })
    const failed = useChatStore.getState().messages.find((m) => m.role === 'user')
    expect(failed).toBeDefined()

    // Retry over a healthy connection.
    const retrySend = connectWithSendSpy()
    act(() => { useChatStore.getState().resendMessage(failed!.id) })

    expect(retrySend).toHaveBeenCalledTimes(1)
    expect(sentPayloads(retrySend)[0]).toMatchObject({
      content: '@Mia please review X',
      session_id: TEST_SESSION_ID,
      recipient: { workspace_id: WORKSPACE_ID, agent_id: 'mia' },
    })
  })

  it('a retried message whose prefix was edited before the failure carries NO recipient', () => {
    act(() => { useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' }) })
    const send = vi.fn().mockReturnValue(false)
    act(() => {
      useConnectionStore.setState({
        connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
        connectionError: null,
      })
    })
    act(() => { useChatStore.getState().sendMessage('@Mi please review X') })
    const failed = useChatStore.getState().messages.find((m) => m.role === 'user')!

    const retrySend = connectWithSendSpy()
    act(() => { useChatStore.getState().resendMessage(failed.id) })

    expect(sentPayloads(retrySend)[0].recipient).toBeUndefined()
  })
})

describe('first-send payloads never carry a recipient', () => {
  it('the minting frame (no session_id) omits recipient even for @-prefixed text — the composer blocks that path in the UI', () => {
    const send = connectWithSendSpy()

    act(() => { useChatStore.getState().sendMessage('@Mia please review X') })

    // The frame went out (this store-level guard is defence in depth; the
    // composer's visible block is what the user meets first) — without a
    // recipient, because recipient is valid only together with session_id.
    expect(sentPayloads(send)[0]).toMatchObject({ type: 'message', content: '@Mia please review X' })
    expect(sentPayloads(send)[0].recipient).toBeUndefined()
    expect(sentPayloads(send)[0].session_id).toBeUndefined()
  })
})

// C8 (R3 delta re-review, SFH F6): the @ address must never be dropped
// SILENTLY. Two corners did exactly that: a Retry from the '__pending'
// first-send bucket (no session for the pair to ride on — the frame went out
// unaddressed), and an agents cache that has never resolved at send time
// (no candidates — every @ read as ordinary text). The rule for both: carry
// the recipient correctly, or block VISIBLY the way the composer blocks an
// @-send in a brand-new chat.
describe('C8 — the @ address is never dropped silently', () => {
  it("a Retry from the '__pending' first-send bucket of an @-message is refused visibly, not sent unaddressed", () => {
    // A failed @-send sitting in the '__pending' bucket, no longer the
    // retained pendingFirstSend (that path owns retryFirstSend) — the
    // resendMessage scan is what reaches it.
    act(() => {
      const bucket = makeBucketMessages([
        {
          id: 'pending-attempt', role: 'user', content: '@Mia please review X',
          timestamp: new Date().toISOString(), status: 'error', deliveryStatus: 'failed',
        },
      ])
      useChatStore.setState({
        sessionsById: {
          __pending: {
            ...bucket,
            // The fields makeBucketMessages does not carry (clearStreamingState
            // iterates every bucket's tool-call bookkeeping on the next reset).
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
          },
        },
        messages: [],
        isStreaming: false,
        isReplaying: false,
      })
      useSessionStore.setState({ activeSessionId: '__pending', activeAgentId: 'jim' })
    })
    const send = connectWithSendSpy()

    act(() => { useChatStore.getState().resendMessage('pending-attempt') })

    // Nothing went out unaddressed; the warning names the addressee; the
    // row stays failed so Retry remains available for when a session exists.
    expect(send).not.toHaveBeenCalled()
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'warning' && t.message.includes('@Mia'))).toBe(true)
    const row = useChatStore.getState().sessionsById['__pending']?.messagesById['pending-attempt']
    expect(row?.deliveryStatus).toBe('failed')
  })

  it('an @-queued message holds when the agents cache has never resolved — it does not drain unaddressed', () => {
    // The team list has never resolved in this tab (fresh mount, evicted
    // cache): the address cannot be checked.
    act(() => { queryClient.removeQueries({ queryKey: ['agents'] }) })
    expect(queryClient.getQueryData(['agents'])).toBeUndefined()

    act(() => { useChatStore.getState().sendMessage('@Mia please review X') }) // offline → queued
    expect(useChatStore.getState().outboundQueue).toHaveLength(1)

    const send = connectWithSendSpy()
    act(() => { useChatStore.getState().drainOutboundQueue() })

    expect(send).not.toHaveBeenCalled()
    expect(useChatStore.getState().pendingDrainQueue).toHaveLength(1)
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'warning' && t.message.includes('@Mia'))).toBe(true)
  })

  it('a Retry with an agents cache that has never resolved is refused visibly, not sent unaddressed', () => {
    act(() => { useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'jim' }) })
    // The row fails with the cache LOADED (the store-level send is defence
    // in depth); by retry time the cache has been evicted/removed.
    const send = vi.fn().mockReturnValue(false)
    act(() => {
      useConnectionStore.setState({
        connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
        connectionError: null,
      })
    })
    act(() => { useChatStore.getState().sendMessage('@Mia please review X') })
    const failed = useChatStore.getState().messages.find((m) => m.role === 'user')!

    act(() => { queryClient.removeQueries({ queryKey: ['agents'] }) })
    const retrySend = connectWithSendSpy()
    act(() => { useChatStore.getState().resendMessage(failed.id) })

    expect(retrySend).not.toHaveBeenCalled()
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'warning' && t.message.includes('@Mia'))).toBe(true)
    expect(failed.deliveryStatus).toBe('failed')
  })
})

// C2 (contract: ARCHITECT-C2-contract.md, decided 2026-10-11): the server
// tags a message-scoped refusal with the sender's session_id AND
// client_message_id (no first_message_error), followed by a
// MessageStatusFrame{failed} for the row. The SPA must route the refusal to
// THAT chat as a visible notice and touch nothing turn-shaped — no
// streaming-bubble sweep, no isStreaming=false, no activeTurnId clear —
// because the turn the message was sent into keeps running (the Stop button
// must survive). An error frame without client_message_id keeps today's
// full turn-ending behaviour.
describe('C2 — a message-scoped refusal routes to its own chat and never ends a live turn', () => {
  const CHAT_X = 'chat-x'
  const CHAT_Y = 'chat-y'
  const REFUSAL_TEXT = 'That agent is not addressable here'

  /** A chat with a live turn: a streaming assistant bubble, an active turn,
   * and a mid-turn @-steer row (clientMessageId) appended after the bubble. */
  function seedLiveChat(sid: string, steerClientMessageId: string): void {
    act(() => {
      const bucket = {
        ...makeBucketMessages([
          { id: 'asst-1', role: 'assistant', content: 'Working on it…', timestamp: '2026-10-11T10:00:00Z', status: 'streaming', isStreaming: true },
          { id: steerClientMessageId, role: 'user', content: '@Ava also check the logs', timestamp: '2026-10-11T10:00:05Z', status: 'done', deliveryStatus: 'sending' },
        ]),
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        isStreaming: true,
        activeTurnId: `turn-${sid}`,
      }
      useChatStore.setState((s) => ({
        sessionsById: { ...s.sessionsById, [sid]: bucket },
      }))
    })
  }

  function refusalFrames(steerClientMessageId: string, tagged: boolean): Array<Record<string, unknown>> {
    const error: Record<string, unknown> = { type: 'error', message: REFUSAL_TEXT }
    if (tagged) {
      error.session_id = CHAT_X
      error.client_message_id = steerClientMessageId
    }
    return [
      error,
      { type: 'message_status', session_id: CHAT_X, client_message_id: steerClientMessageId, state: 'failed' },
    ]
  }

  it('(a) a refusal for chat X lands in X while chat Y is active — Y untouched, X turn intact', () => {
    seedLiveChat(CHAT_X, 'steer-1')
    act(() => {
      const plainY = {
        ...makeBucketMessages([
          { id: 'y-1', role: 'user', content: 'hello', timestamp: '2026-10-11T09:00:00Z', status: 'done', deliveryStatus: 'received' },
          { id: 'y-2', role: 'assistant', content: 'Hi!', timestamp: '2026-10-11T09:00:10Z', status: 'done', deliveryStatus: 'received' },
        ]),
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        isStreaming: false,
      }
      useChatStore.setState((s) => ({ sessionsById: { ...s.sessionsById, [CHAT_Y]: plainY } }))
      useSessionStore.setState({ activeSessionId: CHAT_Y, activeAgentId: 'jim' })
    })
    const yOrderBefore = useChatStore.getState().sessionsById[CHAT_Y]!.messageOrder

    for (const frame of refusalFrames('steer-1', true)) {
      act(() => { useChatStore.getState().handleFrame(frame as never) })
    }

    const x = useChatStore.getState().sessionsById[CHAT_X]!
    // The refusal is VISIBLE in X: a notice bubble carrying the server's text.
    const notice = x.messageOrder.map((id) => x.messagesById[id]).find((m) => m?.role === 'assistant' && m.status === 'error')
    expect(notice?.content).toBe(REFUSAL_TEXT)
    // The refused row exits 'sending'.
    expect(x.messagesById['steer-1']?.deliveryStatus).toBe('failed')
    // X's live turn is untouched: streaming bubble intact, busy flag up,
    // active turn announced.
    expect(x.messagesById['asst-1']?.status).toBe('streaming')
    expect(x.messagesById['asst-1']?.isStreaming).toBe(true)
    expect(x.isStreaming).toBe(true)
    expect(x.activeTurnId).toBe(`turn-${CHAT_X}`)
    // Y is untouched: same rows, no error bubble, still not streaming.
    const y = useChatStore.getState().sessionsById[CHAT_Y]!
    expect(y.messageOrder).toEqual(yOrderBefore)
    expect(y.isStreaming).toBe(false)
    expect(useChatStore.getState().isStreaming).toBe(false)
  })

  it('(b) a refusal in the ACTIVE chat with a live turn keeps Stop: isStreaming and activeTurnId survive', () => {
    seedLiveChat(CHAT_X, 'steer-2')
    act(() => {
      useSessionStore.setState({ activeSessionId: CHAT_X, activeAgentId: 'jim' })
      useChatStore.setState({ messages: [], isStreaming: true })
    })

    for (const frame of refusalFrames('steer-2', true)) {
      act(() => { useChatStore.getState().handleFrame(frame as never) })
    }

    const x = useChatStore.getState().sessionsById[CHAT_X]!
    const notice = x.messageOrder.map((id) => x.messagesById[id]).find((m) => m?.role === 'assistant' && m.status === 'error')
    expect(notice?.content).toBe(REFUSAL_TEXT)
    expect(x.messagesById['steer-2']?.deliveryStatus).toBe('failed')
    // THE POINT of the contract's no-sweep rule: the running turn keeps its
    // Stop button (isStreaming) and its announced turn id.
    expect(x.isStreaming).toBe(true)
    expect(x.activeTurnId).toBe(`turn-${CHAT_X}`)
    expect(x.messagesById['asst-1']?.status).toBe('streaming')
  })

  it('(c) an untagged error frame keeps the existing turn-ending behaviour in full', () => {
    seedLiveChat(CHAT_X, 'steer-3')
    act(() => {
      useSessionStore.setState({ activeSessionId: CHAT_X, activeAgentId: 'jim' })
      useChatStore.setState({ messages: [], isStreaming: true })
    })

    for (const frame of refusalFrames('steer-3', false)) {
      act(() => { useChatStore.getState().handleFrame(frame as never) }
      )
    }

    const x = useChatStore.getState().sessionsById[CHAT_X]!
    // Untagged ⇒ turn-ending, exactly as before: the streaming bubble is
    // closed with the error text, the busy flag drops, the turn id clears.
    expect(x.messagesById['asst-1']?.status).toBe('error')
    expect(x.isStreaming).toBe(false)
    expect(x.activeTurnId).toBeNull()
  })
})
