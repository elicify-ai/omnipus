/**
 * chat.seq-catchup.test.ts — #823 phase 2, the SPA half of reconnect catch-up
 * by per-session sequence numbers.
 *
 * The defect these tests pin: catch-up used to be driven by a TIMESTAMP cursor
 * (`lastReceivedEventTime` → `attach_session.since`). Two entries written with
 * the same timestamp, or an entry persisted with an earlier timestamp than the
 * last live frame, were silently filtered out — a finished answer never
 * arrived and the turn sat "running" forever (#822 / S-10). A sequence number
 * is a positional fact, not a clock reading, so the client stores the highest
 * number it has APPLIED per session and:
 *
 *   1. ignores any frame whose seq it has already applied (idempotent
 *      redelivery — the gateway re-delivers retained frames byte-exact), and
 *   2. sends that number back as `attach_session.since_seq` so the gateway
 *      serves exactly what was missed.
 *
 * Every assertion below is about the OUTCOME the user sees (which text is on
 * screen, how many bubbles, whether the transcript survived a reconnect) or
 * about the one documented wire-facing field, `lastAppliedSeq`.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages } from './chat'
import { useSessionStore } from './session'

const SID = 'seq-catchup-session'
const OTHER_SID = 'seq-catchup-other'

function resetStores() {
  act(() => {
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      messagesById: {},
      isStreaming: false,
      isReplaying: false,
      replayCompletedForSession: null,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      rateLimitEvent: null,
      lastUserMessageAt: null,
    })
    useSessionStore.setState({
      activeSessionId: SID,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

function bucket(sid: string) {
  return useChatStore.getState().sessionsById[sid]
}

function cursor(sid: string): number | null {
  return bucket(sid)?.lastAppliedSeq ?? null
}

function texts(sid: string): string[] {
  const b = bucket(sid)
  return b ? getMessages(b).map((m) => m.content) : []
}

function apply(frame: unknown) {
  act(() => {
    useChatStore.getState().handleFrame(frame as never)
  })
}

function tokenSeq(sid: string, seq: number, content: string) {
  apply({ type: 'token', session_id: sid, content, seq })
}

function realDoneSeq(sid: string, seq: number) {
  apply({
    type: 'done',
    session_id: sid,
    seq,
    stats: { tokens: 12, cost: 0.001, duration_ms: 40, agent_id: 'agent-1', started_at: '2026-09-23T10:00:00Z' },
  })
}

describe('#823 phase 2 — apply-by-sequence', () => {
  it('applies a frame carrying a new seq and advances the session cursor to it', () => {
    tokenSeq(SID, 1, 'first')

    expect(texts(SID)).toEqual(['first'])
    expect(cursor(SID)).toBe(1)
  })

  it('ignores a redelivered frame whose seq was already applied (idempotent catch-up)', () => {
    tokenSeq(SID, 7, 'hello')
    // The gateway re-delivers retained frames byte-exact on catch-up, so the
    // very same frame can arrive twice. Appending it twice is the defect.
    tokenSeq(SID, 7, 'hello')

    expect(texts(SID)).toEqual(['hello'])
    expect(cursor(SID)).toBe(7)
  })

  it('ignores a frame whose seq is BELOW the cursor (out-of-order redelivery)', () => {
    tokenSeq(SID, 5, 'five')
    tokenSeq(SID, 3, 'three')

    expect(texts(SID)).toEqual(['five'])
    expect(cursor(SID)).toBe(5)
  })

  it('applies a frame with NO seq exactly as before and leaves the cursor alone', () => {
    tokenSeq(SID, 4, 'numbered')

    apply({ type: 'replay_message', session_id: SID, role: 'assistant', content: 'replayed', id: 'm-replay-1' })

    expect(texts(SID)).toEqual(['numbered', 'replayed'])
    expect(cursor(SID)).toBe(4)
  })

  it('keeps a cursor per session, read from that session own bucket (an attach may target a non-active session)', () => {
    tokenSeq(OTHER_SID, 9, 'background turn')
    tokenSeq(SID, 1, 'foreground turn')

    expect(cursor(OTHER_SID)).toBe(9)
    expect(cursor(SID)).toBe(1)
    // The foreground session must not have adopted the background session's
    // position — a foreground read instead of a sessionsById read would.
    expect(texts(SID)).toEqual(['foreground turn'])
  })

  it('advances the cursor on a done that carries a seq, and closes the turn as today', () => {
    tokenSeq(SID, 1, 'answer')
    realDoneSeq(SID, 2)

    expect(cursor(SID)).toBe(2)
    expect(bucket(SID)?.isStreaming).toBe(false)
    expect(getMessages(bucket(SID)!).at(-1)?.isStreaming).toBe(false)
  })

  it('leaves the cursor untouched by a replay-terminating done (stats.frames_emitted, no seq)', () => {
    tokenSeq(SID, 6, 'seen')

    apply({ type: 'done', session_id: SID, stats: { frames_emitted: 3 } })

    expect(cursor(SID)).toBe(6)
  })
})

describe('#823 phase 2 — session_snapshot replaces state', () => {
  it('replaces the session bucket with an empty one and adopts the frame seq', () => {
    tokenSeq(SID, 3, 'stale one')
    tokenSeq(SID, 4, 'stale two')
    // Both tokens land in the SAME open bubble (tokens append), so the state to
    // be replaced is one message holding both fragments.
    expect(texts(SID)).toEqual(['stale onestale two'])

    apply({ type: 'session_snapshot', session_id: SID, seq: 9, reason: 'retention_exceeded' })

    expect(texts(SID)).toEqual([])
    expect(cursor(SID)).toBe(9)
  })

  it('still replaces when its seq is BELOW the cursor (the cursor_ahead reason: the gateway is behind us, not ahead)', () => {
    tokenSeq(SID, 10, 'from a gateway generation that no longer exists')

    apply({ type: 'session_snapshot', session_id: SID, seq: 4, reason: 'cursor_ahead' })

    expect(texts(SID)).toEqual([])
    expect(cursor(SID)).toBe(4)
  })

  it('creates the bucket when the snapshot targets a session we hold no state for', () => {
    apply({ type: 'session_snapshot', session_id: OTHER_SID, seq: 2, reason: 'unknown_position' })

    expect(texts(OTHER_SID)).toEqual([])
    expect(cursor(OTHER_SID)).toBe(2)
  })

  // SQUAD-BRIEF-AY finding 2: the gateway sends session_state (pending
  // AskUserQuestion cards, the running-turn announcement) BEFORE the
  // session_snapshot on reconnect (pkg/gateway/replay.go:543-552 — pending
  // question cards are delivered only through session_state). Wiping to
  // emptySessionState() on the snapshot threw that state away: a user
  // waiting on an AskUserQuestion saw the card vanish, the Stop button
  // disappeared, and the composer unlocked mid-turn — with no way to answer
  // until a full page reload.
  it('preserves pendingAsk and the running-turn state a session_state already delivered, instead of wiping them', () => {
    const card = {
      card_id: 'card-1',
      session_id: SID,
      agent_id: 'agent-1',
      status: 'pending' as const,
      created_at: '2026-09-23T10:00:00Z',
      questions: [{ header: 'Q', question: 'Proceed?', options: [{ label: 'Yes' }, { label: 'No' }] }],
    }
    apply({
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [],
      pending_asks: [card],
      session_id: SID,
      active_turn: { turn_id: 'turn-1', agent_id: 'agent-1', started_at: '2026-09-23T10:00:00Z' },
      emitted_at: '2026-09-23T10:00:01Z',
    })
    expect(bucket(SID)?.pendingAsk).toEqual(card)
    expect(bucket(SID)?.activeTurnId).toBe('turn-1')
    expect(bucket(SID)?.isStreaming).toBe(true)

    apply({ type: 'session_snapshot', session_id: SID, seq: 5, reason: 'retention_exceeded' })

    // The transcript still wipes and the cursor still adopts the frame's seq
    // — this is not a merge.
    expect(texts(SID)).toEqual([])
    expect(cursor(SID)).toBe(5)
    // But the state session_state already delivered for THIS reconnect
    // survives the wipe.
    expect(bucket(SID)?.pendingAsk).toEqual(card)
    expect(bucket(SID)?.activeTurnId).toBe('turn-1')
    expect(bucket(SID)?.activeTurnAgentId).toBe('agent-1')
    expect(bucket(SID)?.isStreaming).toBe(true)
  })

  // SQUAD-BRIEF-AY finding 3: a message sent while offline (queued, then
  // drained onto the reconnected WS the moment the connection came back —
  // OmnipusRuntimeProvider.tsx's onConnected handler) is added to the KEPT
  // chat as an optimistic bubble. But the gateway had already computed
  // attach_session's snapshot/replay decision from ITS OWN state BEFORE that
  // message reached it — the attach and the drained send race independently
  // over the wire — so neither the snapshot nor the replay that follows it
  // ever includes the message. Wiping to emptySessionState() on the snapshot
  // then erased the user's own words with nothing left to bring them back:
  // their "received"/"working" ticks find no bubble, and the eventual answer
  // appears with no question above it.
  it('carries an unacknowledged user message across the snapshot wipe instead of losing it', () => {
    tokenSeq(SID, 1, 'earlier bot text')
    const sendingMsg = {
      id: 'user-msg-1',
      session_id: SID,
      role: 'user' as const,
      content: 'queued while offline',
      timestamp: '2026-09-23T10:00:00Z',
      status: 'done' as const,
      deliveryStatus: 'sending' as const,
    }
    act(() => {
      useChatStore.setState((s) => {
        const b = s.sessionsById[SID]!
        return {
          sessionsById: {
            ...s.sessionsById,
            [SID]: {
              ...b,
              messagesById: { ...b.messagesById, [sendingMsg.id]: sendingMsg },
              messageOrder: [...b.messageOrder, sendingMsg.id],
            },
          },
        }
      })
    })
    expect(texts(SID)).toEqual(['earlier bot text', 'queued while offline'])

    apply({ type: 'session_snapshot', session_id: SID, seq: 9, reason: 'retention_exceeded' })

    // The stale bot text is gone (a real wipe, not a merge) but the user's
    // own not-yet-acknowledged message survives it.
    expect(texts(SID)).toEqual(['queued while offline'])
    expect(cursor(SID)).toBe(9)
  })
})

// SQUAD-BRIEF-AY finding 16: a stored seq of 0 was handled inconsistently.
// TokenFrame.yaml documents "0 means the session has no numbered event yet",
// and the attach sites already treat it that way (OmnipusRuntimeProvider.tsx
// only sends `since_seq` when `appliedSeq > 0`), but the read side
// (readAppliedSeq, consulted by prepareSessionForReplay/replayStartPatch)
// used to treat a stored 0 as a real position, KEEPING the local transcript
// for a session the gateway is about to fully replay — a mismatch that
// leaves stale content on screen next to the full replay landing on top of
// it. 0 must mean "no position" on every read path, matching the attach.
describe('#823 phase 2 — a stored seq of 0 means "no position", not a real one (finding 16)', () => {
  it('getLastAppliedSeq reports null, not 0, when the stored cursor is 0', () => {
    apply({ type: 'session_snapshot', session_id: SID, seq: 0, reason: 'unknown_position' })

    expect(useChatStore.getState().getLastAppliedSeq(SID)).toBeNull()
  })

  it('prepareSessionForReplay wipes stale content for a 0 cursor exactly like no cursor at all, instead of keeping it', () => {
    apply({ type: 'session_snapshot', session_id: SID, seq: 0, reason: 'unknown_position' })
    // Content that landed while the bucket sat at cursor 0 (e.g. a
    // replay/legacy frame carrying no seq of its own).
    apply({ type: 'replay_message', session_id: SID, role: 'assistant', content: 'stale content', id: 'm-stale' })
    expect(texts(SID)).toEqual(['stale content'])

    act(() => {
      useChatStore.getState().prepareSessionForReplay(SID)
    })

    // A cursor of 0 means "no position" — the gateway's next attach will omit
    // since_seq and do a full replay, so the local transcript must be wiped
    // exactly as it would be for a session with no cursor at all. Keeping it
    // (the pre-fix behaviour) leaves stale content on screen that the
    // incoming full replay then duplicates alongside.
    expect(texts(SID)).toEqual([])
    expect(bucket(SID)?.isReplaying).toBe(true)
  })
})

describe('#823 phase 2 — token.replace replaces the open bubble', () => {
  it('replaces the open bubble content instead of appending to it', () => {
    tokenSeq(SID, 1, 'hel')
    tokenSeq(SID, 2, 'lo')

    apply({ type: 'token', session_id: SID, content: 'hello world', replace: true })

    expect(texts(SID)).toEqual(['hello world'])
  })

  it('is idempotent: applying the same replace token twice yields the same text', () => {
    apply({ type: 'token', session_id: SID, content: 'hello world', replace: true })
    apply({ type: 'token', session_id: SID, content: 'hello world', replace: true })

    expect(texts(SID)).toEqual(['hello world'])
  })

  it('keeps appending for an ordinary token (replace absent) and for replace:false', () => {
    apply({ type: 'token', session_id: SID, content: 'a' })
    apply({ type: 'token', session_id: SID, content: 'b', replace: false })

    expect(texts(SID)).toEqual(['ab'])
  })

  // SQUAD-BRIEF-AY finding 4 — provenance note: this test originally used a
  // REAL numbered `done` (realDoneSeq) to close the first bubble and asserted
  // the two-bubble, duplicated-text outcome below as correct "guard"
  // behaviour. REVIEW-OPUS-823 flags that exact assertion as encoding
  // finding 4's bug: a hard disconnect ALSO closes a bubble this same way
  // (clearStreamingState marks it 'done', not a distinct status) and the
  // gateway's reconnect catch-up for a turn that never actually finished
  // arrives as this same `replace: true` shape — so the store could not tell
  // "genuinely finished, a new answer follows" from "disconnected mid-answer,
  // this Is the rest of the SAME answer", and always chose the first
  // (duplicating text into a second bubble and leaving tool cards stuck
  // 'cancelled'). The fix distinguishes the two: a bubble closed by a REAL
  // `done` (this test, unchanged below) is never rewritten by a later
  // catch-up — that guard is correct and stays. A bubble closed by
  // `clearStreamingState` (disconnect) is reopened instead — see the new
  // describe block immediately below, which is the corrected coverage for
  // the scenario this test used to (wrongly) stand in for.
  it('opens a bubble carrying the replacement text when the previous bubble is already closed by a REAL done (guard: replace must not rewrite a genuinely finished bubble)', () => {
    apply({ type: 'token', session_id: SID, content: 'old answer' })
    realDoneSeq(SID, 2)

    apply({ type: 'token', session_id: SID, content: 'new catch-up text', replace: true })

    expect(texts(SID)).toEqual(['old answer', 'new catch-up text'])
  })
})

// SQUAD-BRIEF-AY finding 4: after a hard disconnect mid-answer, the incremental
// (kept-chat) reconnect path used to split the answer into two bubbles with
// duplicated text and leave its tool cards stuck 'cancelled'. Root cause:
// clearStreamingState() closes an interrupted bubble the same way a genuinely
// finished one closes (status 'done', isStreaming false) — the store had no
// way to tell them apart, so the token-boundary rule always "abandoned" the
// closed bubble and opened a new one for the reconnect's catch-up token,
// which carries the FULL round's text (replace: true) — the first part
// appearing twice, once in each bubble.
describe('#823 phase 2 — reconnect after a hard disconnect reopens the interrupted bubble, instead of duplicating it (finding 4)', () => {
  it('reopens the bubble a disconnect closed and restores its disconnect-cancelled tool card, instead of opening a duplicate', () => {
    tokenSeq(SID, 1, 'partial answer before the drop')
    apply({ type: 'tool_call_start', call_id: 'call_1', tool: 'bash', params: { action: 'run' }, session_id: SID })

    act(() => {
      useChatStore.getState().clearStreamingState()
    })

    // The disconnect closed the bubble and cancelled its running tool call —
    // this is the existing, correct disconnect behaviour, unchanged.
    expect(texts(SID)).toEqual(['partial answer before the drop'])
    const closedBucket = bucket(SID)!
    const closedMsgId = closedBucket.messageOrder[closedBucket.messageOrder.length - 1]
    expect(closedBucket.messagesById[closedMsgId].status).toBe('done')
    expect(closedBucket.messagesById[closedMsgId].tool_calls?.[0]?.status).toBe('cancelled')

    // The turn never actually finished server-side — reconnect's catch-up
    // delivers the FULL accumulated text for the SAME turn (replace: true).
    apply({
      type: 'token',
      session_id: SID,
      content: 'partial answer before the drop and the rest of it',
      replace: true,
    })

    // ONE bubble holding the complete, non-duplicated text — not the first
    // part closed-and-kept plus a second bubble repeating it.
    expect(texts(SID)).toEqual(['partial answer before the drop and the rest of it'])
    expect(bucket(SID)!.messageOrder).toHaveLength(1)
    const reopened = getMessages(bucket(SID)!)[0]
    expect(reopened.isStreaming).toBe(true)
    // The tool card cancelled only because of the disconnect is restored to
    // running and moved back into LIVE tracking — not left stuck baked as
    // 'cancelled' on the (now reopened, no-longer-final) bubble. The current/
    // still-streaming bubble's tool calls render from the live bucket map,
    // not message.tool_calls (see src/components/chat/CLAUDE.md and the
    // 'done' case's own bake-at-finalize comment) — so a real tool_call_result
    // for this call_id can still resolve it normally instead of finding it
    // already terminally baked.
    expect(reopened.tool_calls ?? []).toEqual([])
    expect(bucket(SID)!.toolCalls['call_1']?.status).toBe('running')
    expect(bucket(SID)!.toolCallOrder).toContain('call_1')
  })
})

describe('#823 phase 2 — the read helper the attach sites use', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('reports the stored cursor for a session, and null when we have no position for it', () => {
    expect(useChatStore.getState().getLastAppliedSeq(SID)).toBeNull()

    tokenSeq(SID, 8, 'positioned')

    expect(useChatStore.getState().getLastAppliedSeq(SID)).toBe(8)
    expect(useChatStore.getState().getLastAppliedSeq('never-seen')).toBeNull()
  })
})