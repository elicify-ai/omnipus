// Issue #1428 / C1 RED-first pack — client turn ownership at close.
//
// Base: feature/1266-chat-history-paging @ 7d2d03675. Oracle (chief brief,
// item 6): "a late done/finished signal for turn A never closes a still-
// running turn B; B's remaining text still renders live."
//
// Sequence shape from the C1 triage's controlled reproduction
// (omnipus-scratch/l1/bundle2-ci/uat-continuation/TRIAGE.md §2): the late
// terminal is a DISTINCT, later-sequenced done(A) — not a duplicate seq the
// cursor would drop — arriving after turn B already began. These are
// controlled sequences, not a claim that this exact ordering was captured on
// the wire in T3 (the triage itself marks the live-capture ordering
// Unknown; the defect demonstrated here is nonetheless real and
// reproducible).
//
// Harness: real chat store and real frame reducer via handleFrame, every
// fixture validated through parseFrameSafe (real generated inbound
// validation). Only the transport edge is mocked (recorded vi.fn, never
// called by these inbound sequences).

import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { queryClient } from '@/lib/queryClient'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { __resetFinishedTurnIdsForTests } from '@/store/chat/frames'
import { parseFrameSafe } from '@/lib/ws'
import type { ServerFrame, WsConnection } from '@/lib/ws'
import type { TokenFrame, DoneFrame, SessionStateFrame } from '@/lib/api/generated/asyncapi-types'

const SID = 'session_01JCE142C1TURnownerSHIP000'
const TURN_A = 'c1-turn-a'
const TURN_B = 'c1-turn-b'
const MSG_A = 'c1-msg-a'
const MSG_B = 'c1-msg-b'
const PRIOR = 'Prior answer'
const FINAL = 'PINEAPPLE-C1-final'
const OWNER = 'mia'

let seq = 0
let send: ReturnType<typeof vi.fn>

function f(frame: TokenFrame | DoneFrame | SessionStateFrame): void {
  const parsed = parseFrameSafe(frame)
  expect(parsed, 'fixture must pass real generated inbound validation').not.toBeNull()
  act(() => useChatStore.getState().handleFrame(parsed as unknown as ServerFrame))
}
function token(turn: string, id: string, content: string): void {
  f({ type: 'token', session_id: SID, agent_id: OWNER, turn_id: turn, message_id: id, content, seq: ++seq })
}
function done(turn: string, id: string): number {
  const used = ++seq
  f({ type: 'done', session_id: SID, turn_id: turn, message_id: id, stats: { tokens: 1, cost: 0 }, seq: used })
  return used
}
function announce(turn: string): void {
  f({
    type: 'session_state', session_id: SID, user_id: 'u-c1', pending_approvals: [],
    active_turn: { turn_id: turn, agent_id: OWNER, started_at: '2026-10-23T08:00:00Z' },
    emitted_at: '2026-10-23T08:00:00Z',
  })
}
function assistants() {
  return useChatStore.getState().messages.filter((m) => m.role === 'assistant')
}

beforeEach(() => {
  seq = 0
  queryClient.clear()
  __resetFinishedTurnIdsForTests()
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    useSessionStore.getState().setActiveSession(SID, OWNER)
    send = vi.fn().mockReturnValue(true)
    useConnectionStore.setState({
      connection: { send, isConnected: true, connect: vi.fn(), disconnect: vi.fn() } as unknown as WsConnection,
      isConnected: true,
    })
  })
})

afterEach(() => queryClient.clear())

describe('C1 — a late terminal for turn A never closes a still-running turn B', () => {
  it.each([false, true])('B survives a later-sequenced done(A) and its remaining text renders live (announcement=%s)', (withAnnouncement) => {
    const { result } = renderHook(() => useOmnipusRuntime())
    token(TURN_A, MSG_A, PRIOR)
    done(TURN_A, MSG_A)
    if (withAnnouncement) announce(TURN_B)
    token(TURN_B, MSG_B, ' ')
    expect(useChatStore.getState().isStreaming, 'turn B is running').toBe(true)

    // The defect ordering: a DISTINCT later-sequenced terminal for A arrives
    // after B already began. It owns turn A only.
    done(TURN_A, MSG_A)

    expect(useChatStore.getState().isStreaming, 'chief brief: done(A) must not close still-running turn B').toBe(true)
    const bubbleB = assistants().find((m) => m.turnId === TURN_B)
    expect(bubbleB?.isStreaming, 'the bubble of turn B must still be live after the late done(A)').toBe(true)

    // B's remaining text still renders live.
    token(TURN_B, MSG_B, FINAL)
    done(TURN_B, MSG_B)

    expect(assistants().map((m) => ({ turn: m.turnId, content: m.content, status: m.status }))).toEqual([
      { turn: TURN_A, content: PRIOR, status: 'done' },
      { turn: TURN_B, content: ` ${FINAL}`, status: 'done' },
    ])
    // Live through the real AssistantUI runtime, not just the store.
    const visible = result.current.thread.getState().messages
      .map((m) => m.content.filter((p) => p.type === 'text').map((p) => p.text).join(''))
    expect(visible).toEqual([PRIOR, ` ${FINAL}`])
  })

  it('guard: normal completion order closes each turn with its own done and nothing else', () => {
    token(TURN_A, MSG_A, PRIOR)
    done(TURN_A, MSG_A)
    announce(TURN_B)
    token(TURN_B, MSG_B, ` ${FINAL}`)
    done(TURN_B, MSG_B)
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(assistants().map((m) => ({ turn: m.turnId, content: m.content, status: m.status }))).toEqual([
      { turn: TURN_A, content: PRIOR, status: 'done' },
      { turn: TURN_B, content: ` ${FINAL}`, status: 'done' },
    ])
  })

  it('guard: a duplicate done(A) on the same sequence number is dropped by the cursor and closes nothing', () => {
    token(TURN_A, MSG_A, PRIOR)
    const doneASeq = done(TURN_A, MSG_A)
    announce(TURN_B)
    token(TURN_B, MSG_B, ' ')
    // Same seq as the first done(A): the sequence gate must swallow it.
    act(() => {
      const parsed = parseFrameSafe({
        type: 'done', session_id: SID, turn_id: TURN_A, message_id: MSG_A,
        stats: { tokens: 1, cost: 0 }, seq: doneASeq,
      })
      expect(parsed).not.toBeNull()
      useChatStore.getState().handleFrame(parsed as unknown as ServerFrame)
    })
    expect(useChatStore.getState().isStreaming, 'the duplicate is dropped; B is untouched').toBe(true)
    done(TURN_B, MSG_B)
    expect(assistants().map((m) => ({ turn: m.turnId, content: m.content }))).toEqual([
      { turn: TURN_A, content: PRIOR },
      { turn: TURN_B, content: ' ' },
    ])
  })

  it('guard: a done for a different session never touches the running turn of this session', () => {
    const OTHER = 'session_01JCE142C1OTHERSESSION000'
    token(TURN_A, MSG_A, PRIOR)
    announce(TURN_B)
    token(TURN_B, MSG_B, ' ')
    // A recent user send puts the active bucket inside H1-FE's mid-stream
    // grace window, so this probe exercises turn ownership at close, not the
    // unknown-sid stale-spinner cleanup (frames.ts chat.done_unknown_sid).
    act(() => useChatStore.getState().sendMessage('ownership guard probe', { clientMessageId: 'c1-guard-steer' }))
    expect(useChatStore.getState().isStreaming).toBe(true)
    act(() => {
      const parsed = parseFrameSafe({
        type: 'done', session_id: OTHER, turn_id: 'other-turn', message_id: 'other-msg',
        stats: { tokens: 1, cost: 0 }, seq: ++seq,
      })
      expect(parsed).not.toBeNull()
      useChatStore.getState().handleFrame(parsed as unknown as ServerFrame)
    })
    expect(useChatStore.getState().isStreaming).toBe(true)
    // B's open bubble was closed by the steer itself (ADR-070 §2.1
    // closedBySteer) — the guard here is that the foreign done changed
    // nothing further in this session.
    expect(assistants().find((m) => m.turnId === TURN_B)?.closedBySteer).toBe(true)
  })
})
