// frame-cases.catchup.test.ts: BE-DESIGN.md §4/§6 new frame reducers Lane C
// owns — session_snapshot, catch_up_complete, user_message. Each test drives
// the REAL useChatStore.handleFrame (not a hand-rolled reducer double), per
// the squad brief's "use frame orders produced by the real gateway ... until
// it exists, derive orders from the design and mark them provisional" rule —
// PROVISIONAL, see SQUAD-REPORT-BEC.md.

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { emptySessionState } from './session'
import { inFlightReattachSids } from './runtime-state'
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
  useConnectionStore.setState({ connection: null } as never)
  // Module-scoped state (runtime-state.ts) survives across tests in the same
  // file/worker — clear it so item 7's in-flight re-attach guard doesn't
  // leak between tests that reuse SID.
  inFlightReattachSids.clear()
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
    // Real-browser regression, orchestrator round 4 (scenarios c/e/f):
    // catch_up_complete IS the definitive "catch-up is over" signal for
    // this attach (this describe block's own §4.1/§6.2 reference) — it
    // must set ChatScreen.tsx's replayCompletedForSession flag exactly like
    // the live `done` handler does, or a REST history fetch that resolves
    // out of order after this reattach can merge stale data against a
    // session the WS-driven store state already fully reconstructed.
    expect(b.replayCompletedForSession).toBe(SID)
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

describe('token{replace:true} (§4.4 projection replay)', () => {
  it('D7: replace:true OVERWRITES an existing bubble\'s content instead of appending', () => {
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: 'partial answer so far', turn_id: 't1', message_id: 'm1', seq: 1,
    } as WsReceiveFrame)
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: ' more', turn_id: 't1', message_id: 'm1', seq: 2,
    } as WsReceiveFrame)
    let b = bucket()
    expect(b.messagesById.m1.content).toBe('partial answer so far more')

    // A projection replay token (§4.4) with replace:true — the same
    // message_id, but the frame's content is the FULL accumulated text,
    // not a delta. Must overwrite, not append.
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: 'partial answer so far more', turn_id: 't1', message_id: 'm1', replace: true, seq: 3,
    } as WsReceiveFrame)
    b = bucket()
    expect(b.messagesById.m1.content).toBe('partial answer so far more')

    // A SECOND replace with different content proves it isn't coincidentally
    // matching — a real append would have doubled it.
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: 'entirely different replayed text', turn_id: 't1', message_id: 'm1', replace: true, seq: 4,
    } as WsReceiveFrame)
    b = bucket()
    expect(b.messagesById.m1.content).toBe('entirely different replayed text')
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

describe('applySeqGate gap recovery (§6.2 "gap" row) — the re-attach SIDE EFFECT', () => {
  // D8: gateFrameBySeq's own 'gap' decision is unit-tested directly in
  // cursor.test.ts (C1e); this drives the REAL handleFrame end-to-end and
  // asserts the actual connection.send call the gap triggers — the piece
  // cursor.test.ts cannot exercise (it has no connection to send through).
  it('D8: a genuine sequence gap re-sends attach_session{since_seq, boot_id} from the bucket\'s existing cursor, and does NOT apply the gap frame', () => {
    const sent: unknown[] = []
    useConnectionStore.setState({
      connection: { send: (frame: unknown) => { sent.push(frame); return true } },
    } as never)

    // Establish a cursor at seq 5.
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: 'a', turn_id: 't1', message_id: 'm1', seq: 5, boot_id: 'boot-gap',
    } as WsReceiveFrame)
    expect(bucket().cursor).toEqual({ bootId: 'boot-gap', seq: 5 })

    // A frame arrives at seq 9 — a genuine gap (expected 6).
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: 'GAP', turn_id: 't1', message_id: 'm2', seq: 9,
    } as WsReceiveFrame)

    // The cursor is untouched by the gap frame (still at 5) — proving it
    // was never applied, only the re-attach fired.
    expect(bucket().cursor).toEqual({ bootId: 'boot-gap', seq: 5 })
    expect(bucket().messagesById.m2).toBeUndefined()

    expect(sent).toEqual([{
      type: 'attach_session',
      session_id: SID,
      since_seq: 5,
      boot_id: 'boot-gap',
    }])
  })

  it('D8c (Opus review round 2 item 7): a burst of frames during the SAME unresolved gap sends attach_session only ONCE, not once per frame', () => {
    const sent: unknown[] = []
    useConnectionStore.setState({
      connection: { send: (frame: unknown) => { sent.push(frame); return true } },
    } as never)

    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: 'a', turn_id: 't1', message_id: 'm1', seq: 5, boot_id: 'boot-gap',
    } as WsReceiveFrame)

    // Three more gapped frames arrive in a row, all still ahead of the same
    // unresolved gap (cursor never advances past 5) — only the FIRST should
    // trigger a re-attach.
    for (const seq of [9, 10, 11]) {
      useChatStore.getState().handleFrame({
        type: 'token', session_id: SID, content: 'GAP', turn_id: 't1', message_id: 'm2', seq,
      } as WsReceiveFrame)
    }
    expect(sent).toHaveLength(1)

    // Once the gap resolves (a cursor-minting frame arrives), the guard
    // clears — a LATER, genuinely new gap sends again.
    useChatStore.getState().handleFrame({
      type: 'catch_up_complete', session_id: SID, seq: 5, boot_id: 'boot-gap', mode: 'incremental',
    } as WsReceiveFrame)
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: 'GAP2', turn_id: 't1', message_id: 'm3', seq: 20,
    } as WsReceiveFrame)
    expect(sent).toHaveLength(2)
  })

  it('D8b: no gap (seq === cursor.seq + 1) never sends attach_session', () => {
    const send = vi.fn().mockReturnValue(true)
    useConnectionStore.setState({ connection: { send } } as never)

    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: 'a', turn_id: 't1', message_id: 'm1', seq: 1, boot_id: 'boot-ok',
    } as WsReceiveFrame)
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: 'b', turn_id: 't1', message_id: 'm1', seq: 2,
    } as WsReceiveFrame)

    expect(send).not.toHaveBeenCalled()
  })
})
