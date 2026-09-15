/**
 * chat.replay-cursor.test.ts — regression tests for the reconnect `since` cursor
 * comparison.
 *
 * The defect: advanceEventTime compared ISO-8601 timestamps as STRINGS
 * (`incoming > current`) while the server's applySinceCursor
 * (pkg/gateway/websocket.go) compares real parsed instants with `time.Parse` +
 * `.After()`. Go's RFC3339Nano strips trailing zeros from fractional seconds, so
 * two chronologically ordered instants can arrive with different fractional
 * widths:
 *
 *   "2026-09-12T10:00:00.5Z"        earlier
 *   "2026-09-12T10:00:00.5000001Z"  later
 *
 * A string compare reaches '0' (0x30) vs 'Z' (0x5A) at the first differing
 * character and concludes the LATER timestamp is smaller, so the cursor never
 * advances past it. The next reconnect sends a `since` that is too early and the
 * server dutifully replays entries the SPA already has — duplicate bubbles, the
 * exact outcome the rest of chat.ts works hard to prevent.
 *
 * These tests pin the chronological semantics. Restore `incoming > current` and
 * the "sub-millisecond" cases below fail. Note that a naive `Date.parse`-only fix
 * ALSO fails them, because JS `Date` has millisecond resolution and collapses the
 * pair above to a single value — that is why parseEventTime keeps a separate
 * sub-millisecond tiebreaker.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, advanceEventTime } from './chat'
import { useSessionStore } from './session'

const SID = 'replay-cursor-test-session'

// The exact pair named in the defect report: same instant to millisecond
// resolution, 100 nanoseconds apart in truth, and inverted by a string compare.
const EARLIER = '2026-09-12T10:00:00.5Z'
const LATER = '2026-09-12T10:00:00.5000001Z'

function resetStores() {
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
      activeSessionId: SID,
      activeAgentId: null,
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

describe('advanceEventTime — chronological, not lexicographic', () => {
  it('advances across the RFC3339Nano trailing-zero pair that a string compare inverts', () => {
    // Guard the premise: this is genuinely the case a string compare gets wrong.
    expect(LATER > EARLIER).toBe(false)
    expect(advanceEventTime(EARLIER, LATER)).toBe(LATER)
  })

  it('does not move backwards across that same pair', () => {
    expect(advanceEventTime(LATER, EARLIER)).toBe(LATER)
  })

  it('orders sub-millisecond digits that JS Date resolution alone cannot separate', () => {
    // Both parse to the same epoch millisecond, so Date.parse alone is blind here.
    expect(Date.parse(EARLIER)).toBe(Date.parse(LATER))
    const a = '2026-09-12T10:00:00.500000100Z'
    const b = '2026-09-12T10:00:00.500000200Z'
    expect(Date.parse(a)).toBe(Date.parse(b))
    expect(advanceEventTime(a, b)).toBe(b)
    expect(advanceEventTime(b, a)).toBe(b)
  })

  it('treats a bare-second timestamp as earlier than the same second with a fraction', () => {
    // RFC3339Nano omits the fraction entirely when it is zero.
    const bare = '2026-09-12T10:00:00Z'
    const fractional = '2026-09-12T10:00:00.000000001Z'
    expect(advanceEventTime(bare, fractional)).toBe(fractional)
    expect(advanceEventTime(fractional, bare)).toBe(fractional)
  })

  it('orders whole seconds and larger units normally', () => {
    const t1 = '2026-09-12T10:00:00.000Z'
    const t2 = '2026-09-12T10:00:01.000Z'
    const t3 = '2026-09-13T10:00:00.000Z'
    expect(advanceEventTime(t1, t2)).toBe(t2)
    expect(advanceEventTime(t2, t1)).toBe(t2)
    expect(advanceEventTime(t2, t3)).toBe(t3)
    expect(advanceEventTime(t3, t2)).toBe(t3)
  })

  it('keeps an identical timestamp unchanged', () => {
    expect(advanceEventTime(LATER, LATER)).toBe(LATER)
  })

  it('seeds from null and ignores empty/undefined incoming values', () => {
    expect(advanceEventTime(null, LATER)).toBe(LATER)
    expect(advanceEventTime(LATER, null)).toBe(LATER)
    expect(advanceEventTime(LATER, undefined)).toBe(LATER)
    expect(advanceEventTime(LATER, '')).toBe(LATER)
    expect(advanceEventTime(null, null)).toBe(null)
  })

  it('never advances onto an unparseable incoming value', () => {
    // Erring towards a stale cursor costs a duplicate replay; erring forwards
    // would hand the server a `since` it rejects, or worse, one that skips real
    // messages.
    expect(advanceEventTime(EARLIER, 'not-a-timestamp')).toBe(EARLIER)
  })

  it('replaces an unparseable current cursor with a parseable incoming one', () => {
    // A cursor the server cannot parse triggers a full replay anyway, so any
    // real timestamp is strictly better than keeping it.
    expect(advanceEventTime('garbage', LATER)).toBe(LATER)
  })
})

describe('reconnect cursor — end to end through handleFrame', () => {
  it('advances the bucket cursor across the trailing-zero pair', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_error',
        session_id: SID,
        entry_id: 'e1',
        timestamp: EARLIER,
        kind: 'error',
        message: 'first failure',
      })
    })
    expect(useChatStore.getState().sessionsById[SID].lastReceivedEventTime).toBe(EARLIER)

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_error',
        session_id: SID,
        entry_id: 'e2',
        timestamp: LATER,
        kind: 'error',
        message: 'second failure',
      })
    })
    expect(useChatStore.getState().sessionsById[SID].lastReceivedEventTime).toBe(LATER)
  })
})
