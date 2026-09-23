// frame-cases.catchup.test.ts: BE-DESIGN.md §4/§6 new frame reducers Lane C
// owns — session_snapshot, catch_up_complete, user_message. Each test drives
// the REAL useChatStore.handleFrame (not a hand-rolled reducer double), per
// the squad brief's "use frame orders produced by the real gateway ... until
// it exists, derive orders from the design and mark them provisional" rule —
// PROVISIONAL, see SQUAD-REPORT-BEC.md.

import { beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { emptySessionState } from './session'
import type { ChatMessage } from './types'
import type { WsReceiveFrame } from '@/lib/ws'

const SID = 'session-catchup-1'

beforeEach(() => {
  useSessionStore.setState({ activeSessionId: SID })
  useChatStore.setState({
    sessionsById: {},
    messages: [],
    messagesById: {},
  } as never)
})

function bucket() {
  return useChatStore.getState().sessionsById[SID]
}

describe('session_snapshot (§6.2/§4.6)', () => {
  it('D1: wipes history but preserves the pending tail and out-of-band state', () => {
    // Seed a bucket with one acknowledged assistant reply, one queued
    // (unsent) user message, and an active-turn announcement — mirrors
    // §4.6's "the wipe never erases pendingAsk/activeTurn/goal status".
    useChatStore.setState({
      sessionsById: {
        [SID]: {
          ...emptySessionState(),
          messagesById: {
            a1: { id: 'a1', role: 'assistant', content: 'old reply', timestamp: 't', status: 'done' } as ChatMessage,
            u2: { id: 'u2', role: 'user', content: 'still sending', timestamp: 't', deliveryStatus: 'sending' } as ChatMessage,
          },
          messageOrder: ['a1', 'u2'],
          activeTurnId: 'turn-live',
          activeTurnAgentId: 'agent-1',
        },
      },
    } as never)

    useChatStore.getState().handleFrame({
      type: 'session_snapshot',
      session_id: SID,
      seq: 42,
      boot_id: 'boot-A',
      reason: 'unknown_position',
    } as WsReceiveFrame)

    const b = bucket()
    expect(b.messageOrder).toEqual(['u2'])
    expect(b.messagesById.a1).toBeUndefined()
    expect(b.messagesById.u2?.deliveryStatus).toBe('sending')
    expect(b.activeTurnId).toBe('turn-live')
    expect(b.activeTurnAgentId).toBe('agent-1')
    expect(b.awaitingCatchUp).toBe(true)
    expect(b.cursor).toEqual({ bootId: 'boot-A', seq: 42 })
  })
})

describe('catch_up_complete (§4.1/§6.2)', () => {
  // D2b: regression found while building the F2 fixture — catch_up_complete
  // typically carries the SAME seq as the last regular frame the connection
  // already applied (W is the head at bind time, §4.1), which the ordinary
  // applySeqGate rule (seq <= cursor.seq -> drop) would misclassify as an
  // already-applied duplicate and silently swallow, so awaitingCatchUp/
  // isReplaying would NEVER clear. Proves catch_up_complete (and
  // session_snapshot/session_started, cursor.ts's CURSOR_MINTING_FRAME_TYPES)
  // are exempted from the ordinary gate.
  it('D2b: is NOT dropped by the seq gate when its own seq equals the cursor already established by a prior frame', () => {
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: 'x', turn_id: 't1', message_id: 'm1', seq: 18,
    } as WsReceiveFrame)
    expect(bucket().cursor).toEqual({ bootId: '', seq: 18 })

    useChatStore.getState().handleFrame({
      type: 'catch_up_complete', session_id: SID, seq: 18, boot_id: 'boot-X', mode: 'incremental',
    } as WsReceiveFrame)

    // If this were dropped by the seq gate, awaitingCatchUp would stay at
    // its emptySessionState() default (false) and cursor.bootId would still
    // be '' — assert both actually changed.
    expect(bucket().cursor).toEqual({ bootId: 'boot-X', seq: 18 })
    expect(bucket().isReplaying).toBe(false)
  })

  it('D2: sets the cursor, clears isReplaying and awaitingCatchUp', () => {
    useChatStore.setState({
      sessionsById: {
        [SID]: { ...emptySessionState(), isReplaying: true, awaitingCatchUp: true },
      },
    } as never)

    useChatStore.getState().handleFrame({
      type: 'catch_up_complete',
      session_id: SID,
      seq: 50,
      boot_id: 'boot-A',
      mode: 'incremental',
    } as WsReceiveFrame)

    const b = bucket()
    expect(b.cursor).toEqual({ bootId: 'boot-A', seq: 50 })
    expect(b.isReplaying).toBe(false)
    expect(b.awaitingCatchUp).toBe(false)
  })
})

describe('user_message (§1.2/§4.7, founder decision Q1)', () => {
  it('D3: a message from ANOTHER tab is inserted into this tab\'s history', () => {
    useChatStore.getState().handleFrame({
      type: 'user_message',
      session_id: SID,
      id: 'server-msg-1',
      client_message_id: 'other-tab-client-id',
      content: 'hello from tab 2',
      timestamp: '2026-09-23T00:00:00Z',
      seq: 10,
    } as WsReceiveFrame)

    const b = bucket()
    expect(b.messageOrder).toEqual(['server-msg-1'])
    expect(b.messagesById['server-msg-1']).toMatchObject({
      role: 'user',
      content: 'hello from tab 2',
    })
  })

  it('D4: the SENDING tab\'s own optimistic bubble (keyed by client_message_id) is left as-is, not duplicated', () => {
    useChatStore.setState({
      sessionsById: {
        [SID]: {
          ...emptySessionState(),
          messagesById: {
            'my-client-id': { id: 'my-client-id', role: 'user', content: 'hi', timestamp: 't', deliveryStatus: 'sending' } as ChatMessage,
          },
          messageOrder: ['my-client-id'],
        },
      },
    } as never)

    useChatStore.getState().handleFrame({
      type: 'user_message',
      session_id: SID,
      id: 'server-msg-2',
      client_message_id: 'my-client-id',
      content: 'hi',
      timestamp: '2026-09-23T00:00:01Z',
      seq: 11,
    } as WsReceiveFrame)

    const b = bucket()
    expect(b.messageOrder).toEqual(['my-client-id'])
    expect(Object.keys(b.messagesById)).toEqual(['my-client-id'])
  })

  it('D5: idempotent — a duplicate user_message (same server id) is not inserted twice', () => {
    useChatStore.getState().handleFrame({
      type: 'user_message', session_id: SID, id: 'server-msg-3', content: 'x', timestamp: 't', seq: 12,
    } as WsReceiveFrame)
    useChatStore.getState().handleFrame({
      type: 'user_message', session_id: SID, id: 'server-msg-3', content: 'x', timestamp: 't', seq: 12,
    } as WsReceiveFrame)

    const b = bucket()
    expect(b.messageOrder).toEqual(['server-msg-3'])
  })
})

describe('applySeqGate wiring (§6.2) — a frame TYPE with no id-based dedup of its own', () => {
  it('D6: a duplicate-seq `token` frame is applied exactly once, proving the seq gate (not a per-case dedup) is what stops it', () => {
    const tokenFrame = (): WsReceiveFrame =>
      ({ type: 'token', session_id: SID, content: 'hi', turn_id: 't1', message_id: 'm1', seq: 7 } as WsReceiveFrame)
    useChatStore.getState().handleFrame(tokenFrame())
    useChatStore.getState().handleFrame(tokenFrame())

    const b = bucket()
    const assistantMsgs = b.messageOrder.map((id) => b.messagesById[id]).filter((m) => m.role === 'assistant')
    expect(assistantMsgs).toHaveLength(1)
    // Applied once, not twice — 'hi', never 'hihi'.
    expect(assistantMsgs[0].content).toBe('hi')
  })
})
