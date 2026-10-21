/**
 * rooms.history-continuation.test.ts — SF-08 (Train 3 gate round 2): a
 * contradictory older-history page is a VISIBLE error, never silent success.
 *
 * Oracle: contracts/components/schemas/RoomHistoryResponse.yaml — the
 * conditional continuation rules are producer/consumer invariants:
 * "next_before_seq is present if and only if has_older is true and equals the
 * first returned entry's seq"; "an empty page is entries: [] with has_older:
 * false and no next_before_seq". The generated schema checks structure only,
 * so src/store/rooms.ts::loadOlder must refuse a page that breaks the
 * invariant through the room's existing error/Retry path: keep the displayed
 * rows, the original boundary and hasOlder, log the diagnostic, and let Retry
 * repeat the SAME boundary (design D6's invalid-response rule).
 *
 * Every invalid-page case below fails on the pre-fix store, which took the
 * success branch (status 'idle', boundary overwritten or dropped, no
 * diagnostic) and could loop on the same page forever.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act } from 'react'
import type { RoomEntry } from '@/lib/api/generated/asyncapi-types'
import type { RoomHistoryResponse } from '@/lib/api/generated/openapi-types'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useRoomStore } from './rooms'

const ROOM = 'launch'

const fetchRoomHistory = vi.hoisted(() => vi.fn<(roomId: string, opts?: { beforeSeq?: number; limit?: number }) => Promise<RoomHistoryResponse>>())
vi.mock('@/lib/api/rooms', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  fetchRoomHistory,
}))

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

/** A contract-VALID page: cursor present iff has_older, equal to the first seq. */
function validPage(entries: RoomEntry[], hasOlder: boolean): RoomHistoryResponse {
  return {
    entries,
    has_older: hasOlder,
    ...(hasOlder ? { next_before_seq: entries[0]?.seq } : {}),
  }
}

function roomState(): { status?: string; boundary?: number; hasOlder?: boolean; failed?: boolean; seqs: number[] } {
  const room = useRoomStore.getState().rooms[ROOM]
  return {
    status: room?.historyStatus,
    boundary: room?.olderBoundary,
    hasOlder: room?.hasOlder,
    failed: room?.historyFailed,
    seqs: room?.entries.map((e) => e.seq) ?? [],
  }
}

let warnSpy: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  vi.clearAllMocks()
  fetchRoomHistory.mockReset()
  warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
  act(() => {
    useRoomStore.setState({ rooms: {} })
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    // The displayed window [5, 9]; the older boundary is 5.
    useRoomStore.getState().applyRoomFrame(snapshotFrame(9, [entry(5), entry(9)], true))
  })
})

afterEach(() => {
  warnSpy.mockRestore()
})

describe('SF-08 — a contradictory older-history page is a visible error, never success', () => {
  it('has_older true with NO entries: keeps rows, boundary and hint, shows the error state, logs, and Retry repeats the SAME boundary', async () => {
    fetchRoomHistory.mockResolvedValueOnce({ entries: [], has_older: true })

    await act(async () => { await useRoomStore.getState().loadOlder(ROOM) })

    // Nothing was applied and nothing was silently ended: the room looks
    // exactly as before the request, plus a recoverable error.
    expect(roomState()).toMatchObject({ seqs: [5, 9], hasOlder: true, boundary: 5, status: 'error', failed: true })
    // The contradiction is diagnosed, not swallowed: the warn carries the
    // thrown cause in its context object ({ roomId, beforeSeq, error }).
    expect(warnSpy).toHaveBeenCalledTimes(1)
    const context = warnSpy.mock.calls[0]?.[1] as { error?: unknown } | undefined
    expect(context?.error).toBeInstanceOf(Error)
    expect((context?.error as Error).message).toContain('contradictory older-history page')

    // Recoverable: Retry asks for the SAME boundary and a valid page recovers.
    fetchRoomHistory.mockResolvedValueOnce(validPage([entry(2)], true))
    await act(async () => { await useRoomStore.getState().loadOlder(ROOM) })

    expect(fetchRoomHistory).toHaveBeenNthCalledWith(1, ROOM, { beforeSeq: 5 })
    expect(fetchRoomHistory).toHaveBeenNthCalledWith(2, ROOM, { beforeSeq: 5 })
    expect(roomState()).toMatchObject({ seqs: [2, 5, 9], hasOlder: true, boundary: 2, status: 'idle', failed: false })
  })

  it('has_older true with entries but NO cursor: refused — the cursor is never invented from the first entry', async () => {
    fetchRoomHistory.mockResolvedValueOnce({ entries: [entry(2), entry(3)], has_older: true })

    await act(async () => { await useRoomStore.getState().loadOlder(ROOM) })

    expect(roomState()).toMatchObject({ seqs: [5, 9], hasOlder: true, boundary: 5, status: 'error', failed: true })
    expect(warnSpy).toHaveBeenCalledTimes(1)
  })

  it('has_older false WITH a cursor: refused — a terminal page must omit next_before_seq', async () => {
    fetchRoomHistory.mockResolvedValueOnce({ entries: [entry(2)], has_older: false, next_before_seq: 2 })

    await act(async () => { await useRoomStore.getState().loadOlder(ROOM) })

    // "No older history" must not be concluded from a contradictory page.
    expect(roomState()).toMatchObject({ seqs: [5, 9], hasOlder: true, boundary: 5, status: 'error', failed: true })
    expect(warnSpy).toHaveBeenCalledTimes(1)
  })

  it('cursor that does NOT equal the first returned entry seq: refused', async () => {
    fetchRoomHistory.mockResolvedValueOnce({ entries: [entry(2), entry(3)], has_older: true, next_before_seq: 3 })

    await act(async () => { await useRoomStore.getState().loadOlder(ROOM) })

    expect(roomState()).toMatchObject({ seqs: [5, 9], hasOlder: true, boundary: 5, status: 'error', failed: true })
    expect(warnSpy).toHaveBeenCalledTimes(1)
  })
})

describe('control — contract-valid pages are still accepted (the check is not over-eager)', () => {
  it('a valid non-terminal page (cursor = first entry seq) merges and moves the boundary to the cursor', async () => {
    fetchRoomHistory.mockResolvedValueOnce(validPage([entry(2), entry(3)], true))

    await act(async () => { await useRoomStore.getState().loadOlder(ROOM) })

    expect(roomState()).toMatchObject({ seqs: [2, 3, 5, 9], hasOlder: true, boundary: 2, status: 'idle', failed: false })
    expect(warnSpy).not.toHaveBeenCalled()
  })

  it('a valid terminal page (has_older false, no cursor) ends history', async () => {
    fetchRoomHistory.mockResolvedValueOnce(validPage([entry(2)], false))

    await act(async () => { await useRoomStore.getState().loadOlder(ROOM) })

    expect(roomState()).toMatchObject({ seqs: [2, 5, 9], hasOlder: false, boundary: undefined, status: 'idle', failed: false })
    expect(warnSpy).not.toHaveBeenCalled()
  })

  it('an empty terminal page (entries [] with has_older false, no cursor) ends history', async () => {
    fetchRoomHistory.mockResolvedValueOnce({ entries: [], has_older: false })

    await act(async () => { await useRoomStore.getState().loadOlder(ROOM) })

    expect(roomState()).toMatchObject({ seqs: [5, 9], hasOlder: false, boundary: undefined, status: 'idle', failed: false })
    expect(warnSpy).not.toHaveBeenCalled()
  })
})
