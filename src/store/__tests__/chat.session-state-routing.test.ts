// chat.session-state-routing.test.ts — ADR-082 review findings CR3 and S2
// (the 'session_state' case in chat.ts).
//
// CR3 (HIGH): the generated `SessionStateFrame` now carries an optional
// `session_id`. Before this fix the case routed `active_turn` unconditionally
// to whatever session was foreground (`activeSid`) — a session_state
// snapshot for a BACKGROUND session (a second tab's own reconnect, or a
// stale broadcast racing a session switch) would wrongly stamp its
// `active_turn` onto whatever the user happened to be looking at. The fix
// routes by `frame.session_id` when present, falling back to `activeSid`
// only when it is absent (older gateway / legacy frame).
//
// S2 (HIGH, session_state half): a `session_state.active_turn` snapshot can
// name a turn this client already finalized on an earlier connection cycle
// (the announcement was snapshotted server-side before the done landed, or
// simply arrives late/duplicated). Re-applying it would set
// isStreaming:true / activeTurnId again with no second `done` ever coming to
// clear it a second time — a permanent Stop button and locked composer. The
// fix ignores an `active_turn` whose turn_id is already recorded finished
// for that session (see chat.ts's `markTurnFinished`/`isTurnFinished`).
//
// This file also pins the companion "session_state WITHOUT active_turn"
// clearing behaviour added alongside CR3/S2: a snapshot that says no turn is
// in flight must clear a stale announcement rather than leave it to wedge,
// but must not stomp on a bubble that is genuinely still streaming.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages, __resetFinishedTurnIdsForTests } from '../chat'
import { useSessionStore } from '../session'
import type { WsSessionStateFrame, TokenFrame, DoneFrame } from '@/lib/ws'

const SID_A = 'routing-session-a'
const SID_B = 'routing-session-b'
const AGENT_ID = 'agent-jim'
const TURN_ID = 'turn-routing-xyz'

function resetStores() {
  __resetFinishedTurnIdsForTests()
  act(() => {
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
    })
    useSessionStore.setState({
      activeSessionId: SID_B,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

function sessionStateFor(sessionId: string, turnId = TURN_ID): WsSessionStateFrame {
  return {
    type: 'session_state',
    user_id: 'user-1',
    pending_approvals: [],
    session_id: sessionId,
    active_turn: {
      turn_id: turnId,
      agent_id: AGENT_ID,
      started_at: '2026-09-08T10:00:00.000Z',
    },
    emitted_at: '2026-09-08T10:00:01.000Z',
  }
}

function sessionStateNoTurnFor(sessionId: string): WsSessionStateFrame {
  return {
    type: 'session_state',
    user_id: 'user-1',
    pending_approvals: [],
    session_id: sessionId,
    emitted_at: '2026-09-08T10:00:01.000Z',
  }
}

function token(sessionId: string, content: string): TokenFrame {
  return { type: 'token', session_id: sessionId, content, agent_id: AGENT_ID }
}

function replayTerminatorDone(sessionId: string): DoneFrame {
  return { type: 'done', session_id: sessionId, stats: { frames_emitted: 0 } }
}

function turnDone(sessionId: string): DoneFrame {
  return { type: 'done', session_id: sessionId, stats: { tokens: 10, cost: 0.001 } }
}

function bucket(sessionId: string) {
  return useChatStore.getState().sessionsById[sessionId]
}

function assistantMessages(sessionId: string) {
  const b = bucket(sessionId)
  return b ? getMessages(b).filter((m) => m.role === 'assistant') : []
}

describe('chat.session_state routing by session_id (review CR3)', () => {
  it('a session_state.active_turn for a BACKGROUND session (session_id != activeSid) updates only that session\'s bucket', () => {
    // SID_B is active (per resetStores). A session_state snapshot arrives
    // tagged for SID_A — a session the user is not currently looking at.
    act(() => {
      useChatStore.getState().handleFrame(sessionStateFor(SID_A))
    })

    // SID_A's bucket got the announcement.
    expect(bucket(SID_A)?.activeTurnId).toBe(TURN_ID)
    expect(bucket(SID_A)?.activeTurnAgentId).toBe(AGENT_ID)
    expect(bucket(SID_A)?.isStreaming).toBe(true)

    // SID_B (the ACTIVE session) must be completely untouched — no bucket
    // was even created for it by this frame, and the foreground/flat
    // projection (which always mirrors the active session) must still read
    // as idle.
    expect(bucket(SID_B)).toBeUndefined()
    expect(useChatStore.getState().isStreaming).toBe(false)
  })

  it('switching active session mid-replay: an in-flight session_state for the OLD session never leaks into the NEW active one', () => {
    // Attach to SID_A (foreground) and start its replay.
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      useChatStore.getState().setReplaying(true)
    })
    // The user switches to SID_B before SID_A's own session_state.active_turn
    // frame (still in flight on the wire) arrives.
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_B })
    })
    act(() => {
      useChatStore.getState().handleFrame(sessionStateFor(SID_A))
    })

    // SID_A's bucket reflects the announcement (routed by its own session_id).
    expect(bucket(SID_A)?.activeTurnId).toBe(TURN_ID)
    expect(bucket(SID_A)?.isStreaming).toBe(true)
    // SID_B — now the active/foreground session — must not have been
    // stamped with SID_A's turn.
    expect(bucket(SID_B)?.activeTurnId ?? null).toBeNull()
    expect(useChatStore.getState().isStreaming).toBe(false)
  })
})

describe('chat.session_state without active_turn clears stale announcements (companion to CR3)', () => {
  it('clears a stale activeTurnId and forces isStreaming:false when NO bubble has opened yet', () => {
    act(() => {
      useChatStore.getState().handleFrame(sessionStateFor(SID_A))
    })
    expect(bucket(SID_A)?.activeTurnId).toBe(TURN_ID)
    expect(bucket(SID_A)?.isStreaming).toBe(true)
    expect(bucket(SID_A)?.activeTurnBubbleOpened).toBe(false)

    // A later snapshot for the same session says no turn is in flight.
    act(() => {
      useChatStore.getState().handleFrame(sessionStateNoTurnFor(SID_A))
    })

    expect(bucket(SID_A)?.activeTurnId).toBeNull()
    expect(bucket(SID_A)?.activeTurnAgentId).toBeNull()
    expect(bucket(SID_A)?.activeTurnBubbleOpened).toBe(false)
    expect(bucket(SID_A)?.isStreaming).toBe(false)
  })

  it('clears activeTurnId but leaves isStreaming alone when a bubble IS already open and streaming', () => {
    act(() => {
      useChatStore.getState().handleFrame(sessionStateFor(SID_A))
      useChatStore.getState().handleFrame(token(SID_A, 'partial content'))
    })
    expect(bucket(SID_A)?.activeTurnBubbleOpened).toBe(true)
    expect(assistantMessages(SID_A)).toHaveLength(1)

    act(() => {
      useChatStore.getState().handleFrame(sessionStateNoTurnFor(SID_A))
    })

    expect(bucket(SID_A)?.activeTurnId).toBeNull()
    expect(bucket(SID_A)?.activeTurnBubbleOpened).toBe(false)
    // The bubble is genuinely mid-stream — this snapshot must not force it
    // closed; its own done will finalize it normally.
    expect(bucket(SID_A)?.isStreaming).toBe(true)
    expect(assistantMessages(SID_A)).toHaveLength(1)
    expect(assistantMessages(SID_A)[0].isStreaming).toBe(true)
  })

  it('a session_state with no session_id at all falls back to routing against the active session (legacy/older-gateway compatibility)', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      useChatStore.getState().handleFrame({
        type: 'session_state',
        user_id: 'user-1',
        pending_approvals: [],
        active_turn: { turn_id: TURN_ID, agent_id: AGENT_ID, started_at: '2026-09-08T10:00:00.000Z' },
        emitted_at: '2026-09-08T10:00:01.000Z',
      })
    })

    expect(bucket(SID_A)?.activeTurnId).toBe(TURN_ID)
    expect(useChatStore.getState().isStreaming).toBe(true)
  })
})

describe('chat.session_state ignores an already-finished turn (review S2)', () => {
  it('a stale/racing session_state.active_turn for a turn whose done already landed is ignored — no permanent Stop', () => {
    // Full, ordinary lifecycle: the turn announces, streams, and finishes.
    act(() => {
      useChatStore.getState().handleFrame(sessionStateFor(SID_A))
      useChatStore.getState().handleFrame(replayTerminatorDone(SID_A))
      useChatStore.getState().handleFrame(token(SID_A, 'final answer'))
      useChatStore.getState().handleFrame(turnDone(SID_A))
    })
    expect(bucket(SID_A)?.isStreaming).toBe(false)
    expect(bucket(SID_A)?.activeTurnId).toBeNull()
    expect(assistantMessages(SID_A)).toHaveLength(1)
    expect(assistantMessages(SID_A)[0].status).toBe('done')

    // A stale session_state re-announces the SAME turn_id (the client's
    // earlier connection already finalized it — e.g. the announcement was
    // snapshotted server-side before the done landed, or a duplicate
    // broadcast). This must be a complete no-op.
    act(() => {
      useChatStore.getState().handleFrame(sessionStateFor(SID_A))
    })

    expect(bucket(SID_A)?.isStreaming).toBe(false)
    expect(bucket(SID_A)?.activeTurnId).toBeNull()
    expect(assistantMessages(SID_A)).toHaveLength(1)
    expect(assistantMessages(SID_A)[0].status).toBe('done')
  })

  it('a session_state announcing a NEW turn_id for the same session (after a prior one finished) is applied normally — the guard is turn-id-scoped, not session-wide', () => {
    act(() => {
      useChatStore.getState().handleFrame(sessionStateFor(SID_A, 'turn-one'))
      useChatStore.getState().handleFrame(replayTerminatorDone(SID_A))
      useChatStore.getState().handleFrame(token(SID_A, 'first reply'))
      useChatStore.getState().handleFrame(turnDone(SID_A))
    })
    expect(assistantMessages(SID_A)).toHaveLength(1)

    // A genuinely NEW turn for the same session must be announced normally.
    act(() => {
      useChatStore.getState().handleFrame(sessionStateFor(SID_A, 'turn-two'))
    })

    expect(bucket(SID_A)?.activeTurnId).toBe('turn-two')
    expect(bucket(SID_A)?.isStreaming).toBe(true)
  })
})
