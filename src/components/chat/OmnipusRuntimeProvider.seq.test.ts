/**
 * OmnipusRuntimeProvider.seq.test.ts — #823 phase 2 attach site #1.
 *
 * `reattachActiveSession` is the onConnected path: every socket open (first
 * connect and every reconnect) re-binds the active session. Phase 2 changes two
 * things about it:
 *
 *   1. it sends the session's applied-frame cursor as `since_seq` (omitted when
 *      the SPA has no position for that session, so the gateway does a full
 *      replay), and
 *   2. it only WIPES the transcript when there is no cursor. A reconnect that
 *      carries a cursor keeps the messages on screen — the gateway re-delivers
 *      only the frames after that cursor, so the local transcript is the prefix
 *      of the correct final state, not stale state to be discarded.
 *
 * The wipe/preserve decision is the observable outcome these tests pin: the
 * timestamp-cursor version wiped unconditionally, which is why a reconnect
 * showed an empty chat until the replay landed (and lost the transcript
 * entirely if the replay never arrived).
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { reattachActiveSession } from './OmnipusRuntimeProvider'
import { useChatStore, getMessages } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import type { ClientFrame } from '@/lib/ws'

const SID = 'reattach-seq-session'

function resetStores() {
  act(() => {
    useChatStore.setState({
      sessionsById: {},
      messages: [],
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

/** A stand-in WsConnection whose send() always succeeds. */
function sender(sent = true) {
  // Typed as the real client frame the connection sends, so this stand-in
  // satisfies ReattachSender exactly and cannot drift from its signature.
  return {
    send: (frame: ClientFrame) => {
      sentFrames.push(frame as unknown as Record<string, unknown>)
      return sent
    },
  }
}

let sentFrames: Array<Record<string, unknown>> = []

/** Apply a real token frame so the bucket's cursor is populated by the store itself. */
function seedCursor(seq: number, content = 'seen live') {
  act(() => {
    useChatStore.getState().handleFrame({
      type: 'token',
      session_id: SID,
      content,
      seq,
    } as never)
  })
}

function bucketMessages(): string[] {
  const bucket = useChatStore.getState().sessionsById[SID]
  return bucket ? getMessages(bucket).map((m) => m.content) : []
}

beforeEach(() => {
  sentFrames = []
})

describe('#823 phase 2 — reattachActiveSession sends the sequence cursor', () => {
  it('sends since_seq equal to the session cursor, read from that session own bucket', () => {
    seedCursor(12, 'answer so far')

    const ok = reattachActiveSession(sender(), () => {})

    expect(ok).toBe(true)
    expect(sentFrames).toHaveLength(1)
    expect(sentFrames[0]).toMatchObject({ type: 'attach_session', session_id: SID, since_seq: 12 })
  })

  it('omits since_seq when the SPA holds no position for the session (first load)', () => {
    // A first load has no bucket at all yet.
    const ok = reattachActiveSession(sender(), () => {})

    expect(ok).toBe(true)
    expect(sentFrames).toHaveLength(1)
    expect(sentFrames[0].type).toBe('attach_session')
    expect('since_seq' in sentFrames[0]).toBe(false)
  })
})

describe('#823 phase 2 — reattachActiveSession resets only when there is no cursor', () => {
  it('keeps the transcript when a cursor exists (the gateway will send only what was missed)', () => {
    seedCursor(3, 'first half')
    seedCursor(4, ' second half')

    reattachActiveSession(sender(), () => {})

    expect(bucketMessages()).toEqual(['first half second half'])
    // The catch-up window must still arm, so the composer stays disabled until
    // the catch-up's terminating done arrives.
    expect(useChatStore.getState().sessionsById[SID]?.isReplaying).toBe(true)
  })

  it('wipes the transcript on a first load (no cursor), exactly as before', () => {
    // Messages with no sequence position — a bucket seeded before the cursor existed.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        session_id: SID,
        role: 'assistant',
        content: 'from an earlier connect',
        id: 'm-stale-1',
      } as never)
    })
    expect(bucketMessages()).toEqual(['from an earlier connect'])

    reattachActiveSession(sender(), () => {})

    expect(bucketMessages()).toEqual([])
    expect(useChatStore.getState().sessionsById[SID]?.isReplaying).toBe(true)
  })

  it('leaves the transcript intact when the attach frame could not be sent, cursor or not', () => {
    seedCursor(5, 'kept')

    const ok = reattachActiveSession(sender(false), () => {})

    expect(ok).toBe(false)
    expect(bucketMessages()).toEqual(['kept'])
    expect(useChatStore.getState().sessionsById[SID]?.isReplaying).toBe(false)
  })
})