/**
 * rooms.history.test.ts — #1332 room history paging, SPA store half.
 *
 * Oracles: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/ROOM-HISTORY-PAGING-1332-20261018.md
 * rows T18–T22 (normative expected results), rules D2 (exclusive cursor),
 * D4 (RoomHistoryResponse: entries / has_older / optional next_before_seq)
 * and D6 (SPA merge and concurrent lifecycle). Expected values come from the
 * design's fixture arithmetic, never from current code.
 *
 * RED surfaces (missing at 35998d756, per design D6):
 *   - src/lib/api/rooms.ts::fetchRoomHistory — the GET client for
 *     getRoomHistory (contract 8a23e8b8b).
 *   - src/store/rooms.ts::loadOlder(roomId) — one in-flight older-page
 *     request per room, stale-fenced by attach/reset, navigation and
 *     superseding requests (D6 stale-response fence).
 *   - per-room historyStatus ('idle' | 'loading' | 'error') and
 *     olderBoundary (the next before_seq; undefined once terminal).
 * Each test fails at RED on the missing store surface (BLOCKED), naming it.
 *
 * SUPERSESSION note: none — this is a new capability (#1332), governed by the
 * design above; it deliberately does NOT alter the live-room rules of K45/Q2
 * (T18's invariance row pins exactly that).
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import type { RoomEntry } from '@/lib/api/generated/asyncapi-types'
import type { RoomHistoryResponse } from '@/lib/api/generated/openapi-types'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useRoomStore } from './rooms'

const ROOM = 'launch'

// The network edge: the older-page GET only. Everything else (store, frames,
// connection) is real. GREEN imports fetchRoomHistory from this module.
const fetchRoomHistory = vi.hoisted(() => vi.fn<(roomId: string, opts?: { beforeSeq?: number; limit?: number }) => Promise<RoomHistoryResponse>>())
vi.mock('@/lib/api/rooms', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  fetchRoomHistory,
}))

// The planned store surface (design D6). Fails loudly at RED.
async function loadOlder(roomId: string): Promise<void> {
  const store = useRoomStore.getState() as unknown as {
    loadOlder?: (roomId: string) => Promise<void>
  }
  if (typeof store.loadOlder !== 'function') {
    expect.fail(
      'BLOCKED: src/store/rooms.ts::loadOlder not implemented — required by ' +
        'ROOM-HISTORY-PAGING-1332-20261018.md D6 (rows T18–T22)',
    )
  }
  await store.loadOlder(roomId)
}

function historyOf(roomId: string): { status?: string; boundary?: number } {
  const room = useRoomStore.getState().rooms[roomId] as unknown as {
    historyStatus?: string
    olderBoundary?: number
  } | undefined
  return { status: room?.historyStatus, boundary: room?.olderBoundary }
}

function entry(seq: number, text = `entry-${seq}`, overrides: Partial<RoomEntry> = {}): RoomEntry {
  return {
    id: `room-message-${seq}`, room_id: ROOM, seq,
    client_message_id: `client-${seq}`, author: { kind: 'user' },
    text, addressees: [], after_pause: false, deliveries: [],
    timestamp: '2026-10-18T10:00:00Z', ...overrides,
  }
}

function snapshotFrame(seq: number, entries: RoomEntry[], hasOlder: boolean) {
  return {
    type: 'room_snapshot' as const, room_id: ROOM, seq, entries,
    members: ['mia', 'jim'], member_states: [], muted: [], paused: false,
    generation: 1, allocated_cost_micro_usd: 5_000, allocated_cost_unpriced: false,
    complete: true, has_older: hasOlder,
  }
}

function deferredPage(): { promise: Promise<RoomHistoryResponse>; resolve: (page: RoomHistoryResponse) => void; reject: (err: Error) => void } {
  let resolve!: (page: RoomHistoryResponse) => void
  let reject!: (err: Error) => void
  const promise = new Promise<RoomHistoryResponse>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function page(entries: RoomEntry[], hasOlder: boolean): RoomHistoryResponse {
  return {
    entries,
    has_older: hasOlder,
    ...(hasOlder ? { next_before_seq: entries[0]?.seq } : {}),
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  fetchRoomHistory.mockReset()
  act(() => {
    useRoomStore.setState({ rooms: {} })
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  })
})

describe('T18 — older merge, duplicate/overlap idempotence, live-state invariance (design D6)', () => {
  it('merges an accepted older page by seq: [5,9] + page [2] → [2,5,9], one of each, sorted', async () => {
    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(9, [entry(5), entry(9)], true)))
    fetchRoomHistory.mockResolvedValueOnce(page([entry(2)], false))

    await loadOlder(ROOM)

    const room = useRoomStore.getState().rooms[ROOM]!
    expect(room.entries.map((e) => e.seq)).toEqual([2, 5, 9])
    expect(room.entries.map((e) => e.id)).toEqual(['room-message-2', 'room-message-5', 'room-message-9'])
    // Terminal page: continuation closes.
    expect(historyOf(ROOM).boundary).toBeUndefined()
    expect(room.hasOlder).toBe(false)
  })

  it('reapplying a duplicate page or an overlapping retry still shows one row per seq, and the EXISTING representation wins collisions', async () => {
    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(9, [entry(9)], true)))
    const pending = deferredPage()
    fetchRoomHistory.mockReturnValueOnce(pending.promise)
    const loading = loadOlder(ROOM)
    // Overlap is legal when a live entry arrives while a valid page before
    // seq 9 is pending, not when a page violates its exclusive before_seq.
    act(() => useRoomStore.getState().applyRoomFrame({
      type: 'room_entry', room_id: ROOM, seq: 5, entry: entry(5, 'live five'),
    }))
    pending.resolve(page([entry(2, 'old two'), entry(5, 'stale five')], false))

    await loading
    await loadOlder(ROOM) // a retry / duplicate application

    const room = useRoomStore.getState().rooms[ROOM]!
    expect(room.entries.map((e) => e.seq)).toEqual([2, 5, 9])
    // The displayed (live) representation of seq 5 wins over the older page's stale copy.
    expect(room.entries.find((e) => e.seq === 5)?.text).toBe('live five')
  })

  it('history never moves the live seq, members, mutes, cost, pause, generation, snapshotReceived — nor any chat session bucket', async () => {
    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(9, [entry(5), entry(9)], true)))
    const before = useRoomStore.getState().rooms[ROOM]!
    const chatBefore = JSON.stringify(useChatStore.getState().sessionsById ?? {})
    fetchRoomHistory.mockResolvedValueOnce(page([entry(2)], false))

    await loadOlder(ROOM)

    const after = useRoomStore.getState().rooms[ROOM]!
    expect(after.seq).toBe(before.seq)
    expect(after.members).toEqual(before.members)
    expect(after.muted).toEqual(before.muted)
    expect(after.paused).toBe(before.paused)
    expect(after.generation).toBe(before.generation)
    expect(after.allocatedCostMicroUsd).toBe(before.allocatedCostMicroUsd)
    expect(after.allocatedCostUnpriced).toBe(before.allocatedCostUnpriced)
    expect(after.snapshotReceived).toBe(before.snapshotReceived)
    expect(JSON.stringify(useChatStore.getState().sessionsById ?? {})).toBe(chatBefore)
  })
})

describe('T19 — live update race while an older page is in flight (design D6)', () => {
  it('a live entry and a newer member-state applied during the flight survive the page resolution; the page cannot rewind the cursor or deliveries', async () => {
    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(9, [
      entry(5, 'five', { deliveries: [{ agent_id: 'jim', request_id: 'req-1', state: 'recorded' }] }),
      entry(9),
    ], true)))
    const pending = deferredPage()
    fetchRoomHistory.mockReturnValueOnce(pending.promise)
    const loadPromise = loadOlder(ROOM)

    // Live traffic lands while the page is in flight: a NEW entry (seq 12) and
    // a newer authoritative member-state for req-1 (seq 13 > the page's 2).
    act(() => useRoomStore.getState().applyRoomFrame({
      type: 'room_entry', room_id: ROOM, seq: 12,
      entry: entry(12, 'live twelve', { deliveries: [{ agent_id: 'jim', request_id: 'req-2', state: 'recorded' }] }),
    }))
    act(() => useRoomStore.getState().applyRoomFrame({
      type: 'room_member_state', room_id: ROOM, seq: 13,
      delivery: { agent_id: 'jim', request_id: 'req-1', state: 'admitted' },
    }))
    const highWater = useRoomStore.getState().rooms[ROOM]!.seq

    pending.resolve(page([
      entry(2, 'two', { deliveries: [{ agent_id: 'jim', request_id: 'req-1', state: 'recorded' }] }),
      entry(3),
    ], true))
    await loadPromise

    const room = useRoomStore.getState().rooms[ROOM]!
    expect(room.entries.map((e) => e.seq)).toEqual([2, 3, 5, 9, 12])
    expect(room.entries.find((e) => e.seq === 12)?.text).toBe('live twelve')
    // The reconnect cursor stays at the WS high-water — history never moves it.
    expect(room.seq).toBe(highWater)
    expect(room.seq).toBe(13)
    // The page's embedded (older) delivery must not overwrite the newer
    // authoritative member-state for the same request.
    expect(room.deliveries['req-1']?.delivery.state).toBe('admitted')
    expect(room.deliveries['req-1']?.stateSeq).toBe(13)
  })
})

describe('T20 — truncated reconnect race and stale-result fence (design D6)', () => {
  it('an in-flight older page for window A is IGNORED on success after a truncated reconnect replaces the list with window B', async () => {
    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(9, [entry(8), entry(9)], true))) // window A
    const stale = deferredPage()
    fetchRoomHistory.mockReturnValueOnce(stale.promise)
    const loadPromise = loadOlder(ROOM)

    // Truncated reconnect: window B replaces the list exactly (Q2).
    act(() => {
      useRoomStore.getState().attachRoom(ROOM)
      useRoomStore.getState().applyRoomFrame(snapshotFrame(7, [entry(6), entry(7)], true))
    })

    stale.resolve(page([entry(2), entry(3)], false)) // late A success
    await loadPromise

    expect(useRoomStore.getState().rooms[ROOM]!.entries.map((e) => e.seq)).toEqual([6, 7])
    // Fresh older paging starts before B's first entry, not A's.
    expect(historyOf(ROOM).boundary).toBe(6)
  })

  it('an in-flight older page for window A is IGNORED on FAILURE after the truncated reconnect, too', async () => {
    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(9, [entry(8), entry(9)], true)))
    const stale = deferredPage()
    fetchRoomHistory.mockReturnValueOnce(stale.promise)
    const loadPromise = loadOlder(ROOM)

    act(() => {
      useRoomStore.getState().attachRoom(ROOM)
      useRoomStore.getState().applyRoomFrame(snapshotFrame(7, [entry(6), entry(7)], true))
    })

    stale.reject(new Error('stale request failed'))
    await loadPromise

    const room = useRoomStore.getState().rooms[ROOM]!
    expect(room.entries.map((e) => e.seq)).toEqual([6, 7])
    // A stale FAILURE must not paint an error state onto the new window.
    expect(historyOf(ROOM).status).not.toBe('error')
  })

  it('navigating away and re-attaching another room invalidates the in-flight page for the previous room', async () => {
    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(9, [entry(5), entry(9)], true)))
    const stale = deferredPage()
    fetchRoomHistory.mockReturnValueOnce(stale.promise)
    const loadPromise = loadOlder(ROOM)

    // Navigation: the operator opens another room (attach + its snapshot).
    act(() => {
      useRoomStore.getState().attachRoom('other')
      useRoomStore.getState().applyRoomFrame({ ...snapshotFrame(4, [entry(1, 'other-one', { room_id: 'other' })], false), room_id: 'other' })
    })

    stale.resolve(page([entry(2)], false))
    await loadPromise

    // The stale page changed nothing in the room it was left behind on…
    expect(useRoomStore.getState().rooms[ROOM]!.entries.map((e) => e.seq)).toEqual([5, 9])
    // …and did not leak into the newly opened room either.
    expect(useRoomStore.getState().rooms.other!.entries.map((e) => e.seq)).toEqual([1])
  })
})

describe('T21 — a non-truncated reconnect keeps the unresolved older state (design D6)', () => {
  it('a cursor snapshot with absent/false has_older preserves an unresolved hasOlder and its boundary; forward entries merge', async () => {
    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(9, [entry(5), entry(9)], true)))
    fetchRoomHistory.mockResolvedValueOnce(page([entry(2)], true)) // older page, still more
    await loadOlder(ROOM)
    expect(useRoomStore.getState().rooms[ROOM]!.hasOlder).toBe(true)
    expect(historyOf(ROOM).boundary).toBe(2)

    // Reconnect catch-up that is NOT truncated: has_older absent/false.
    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(11, [entry(10), entry(11)], false)))

    const room = useRoomStore.getState().rooms[ROOM]!
    expect(room.entries.map((e) => e.seq)).toEqual([2, 5, 9, 10, 11])
    expect(room.hasOlder).toBe(true)
    expect(historyOf(ROOM).boundary).toBe(2)
  })

  it('when the old end was already reached, a reconnect retains false (never resurrects the hint)', async () => {
    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(9, [entry(5), entry(9)], true)))
    fetchRoomHistory.mockResolvedValueOnce(page([entry(1), entry(2)], false)) // terminal
    await loadOlder(ROOM)
    expect(useRoomStore.getState().rooms[ROOM]!.hasOlder).toBe(false)

    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(12, [entry(10), entry(12)], false)))
    expect(useRoomStore.getState().rooms[ROOM]!.hasOlder).toBe(false)
  })
})

describe('T22 — history never satisfies or resets the first-snapshot send gate (design D6)', () => {
  it('the gate opens on the first snapshot and a history load/success/failure leaves snapshotReceived untouched', async () => {
    act(() => {
      useRoomStore.getState().attachRoom(ROOM) // gate closed: snapshotReceived false
    })
    expect(useRoomStore.getState().rooms[ROOM]!.snapshotReceived).toBe(false)

    // History traffic during the closed gate changes nothing about the gate…
    const pending = deferredPage()
    fetchRoomHistory.mockReturnValueOnce(pending.promise)
    const loadPromise = loadOlder(ROOM)
    expect(useRoomStore.getState().rooms[ROOM]!.snapshotReceived).toBe(false)
    pending.resolve(page([entry(2)], true))
    await loadPromise
    expect(useRoomStore.getState().rooms[ROOM]!.snapshotReceived).toBe(false)

    // …the first snapshot opens it (reserved complete and has_older never block)…
    act(() => useRoomStore.getState().applyRoomFrame(snapshotFrame(9, [entry(5), entry(9)], true)))
    expect(useRoomStore.getState().rooms[ROOM]!.snapshotReceived).toBe(true)

    // …and a history FAILURE after that neither resets the gate nor blocks.
    fetchRoomHistory.mockRejectedValueOnce(new Error('history offline'))
    await loadOlder(ROOM)
    expect(useRoomStore.getState().rooms[ROOM]!.snapshotReceived).toBe(true)
    expect(historyOf(ROOM).status).toBe('error')
  })
})
