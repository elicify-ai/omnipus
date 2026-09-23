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

  it('opens a bubble carrying the replacement text when the previous bubble is already closed (guard: replace must not rewrite a finalized bubble)', () => {
    apply({ type: 'token', session_id: SID, content: 'old answer' })
    realDoneSeq(SID, 2)

    apply({ type: 'token', session_id: SID, content: 'new catch-up text', replace: true })

    expect(texts(SID)).toEqual(['old answer', 'new catch-up text'])
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