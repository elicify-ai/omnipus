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
