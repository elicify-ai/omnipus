/**
 * F-b / SF-08 residual. Independent oracle: RoomHistoryResponse's contract
 * requires same-room, strictly ascending entries below the exclusive cursor.
 * Exercise the real generated response validator, HTTP client and store;
 * replace only fetch. A structurally valid but semantically invalid page must
 * preserve the room's projection and use its logged error/Retry state.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { RoomEntry, RoomSnapshotFrame } from '@/lib/api/generated/asyncapi-types'
import type { RoomHistoryResponse } from '@/lib/api/generated/openapi-types'
import { useRoomStore } from './rooms'

const ROOM = 'launch'

function entry(seq: number, roomId = ROOM): RoomEntry {
  return {
    id: `${roomId}-message-${seq}`, room_id: roomId, seq,
    client_message_id: `client-${seq}`, author: { kind: 'user' },
    text: `entry-${seq}`, addressees: [], after_pause: false,
    deliveries: [{ agent_id: 'jim', request_id: `request-${seq}`, state: 'recorded' }],
    timestamp: '2026-10-20T10:00:00Z',
  }
}

function snapshot(): RoomSnapshotFrame {
  return {
    type: 'room_snapshot', room_id: ROOM, seq: 9, entries: [entry(5), entry(9)],
    members: ['mia', 'jim'], member_states: [], muted: ['jim'], paused: true,
    generation: 1, allocated_cost_micro_usd: 5_000, allocated_cost_unpriced: false,
    complete: true, has_older: true,
  }
}

function page(entries: RoomEntry[], hasOlder = true): RoomHistoryResponse {
  return { entries, has_older: hasOlder, ...(hasOlder ? { next_before_seq: entries[0]?.seq } : {}) }
}

const fetchResponse = vi.fn<typeof fetch>()
let warn: ReturnType<typeof vi.spyOn>

function respond(response: RoomHistoryResponse): void {
  fetchResponse.mockResolvedValueOnce(new Response(JSON.stringify(response), {
    status: 200, headers: { 'Content-Type': 'application/json' },
  }))
}

beforeEach(() => {
  useRoomStore.setState(useRoomStore.getInitialState(), true)
  useRoomStore.getState().applyRoomFrame(snapshot())
  fetchResponse.mockReset()
  vi.stubGlobal('fetch', fetchResponse)
  warn = vi.spyOn(console, 'warn')
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

const INVALID: { name: string; response: RoomHistoryResponse }[] = [
  { name: 'repeated [5,9] page and boundary 5', response: page([entry(5), entry(9)]) },
  { name: 'entire page ahead of boundary 5', response: page([entry(6), entry(8)]) },
  { name: 'terminal page whose later row equals boundary 5', response: page([entry(2), entry(5)], false) },
  { name: 'older first row but a later row beyond boundary 5', response: page([entry(2), entry(6)]) },
  { name: 'wrong-room row in a nonterminal page', response: page([entry(2, 'other')]) },
  { name: 'wrong-room later row in an otherwise valid terminal page', response: page([entry(2), entry(3, 'other')], false) },
  { name: 'descending rows', response: page([entry(3), entry(2)]) },
  { name: 'duplicate sequences', response: page([entry(2), { ...entry(2), id: 'second-id-same-seq' }]) },
  { name: 'disorder after an ascending first pair', response: page([entry(1), entry(3), entry(2)]) },
]

describe('F-b — reject request-relative history protocol errors', () => {
  it.each(INVALID)('$name: keep rows, delivery projection and boundary; log and expose Retry', async ({ response }) => {
    const before = useRoomStore.getState().rooms[ROOM]!
    respond(response)
    await useRoomStore.getState().loadOlder(ROOM)

    expect(useRoomStore.getState().rooms[ROOM]).toEqual({
      ...before, historyToken: undefined, historyStatus: 'error', historyFailed: true,
    })
    expect(fetchResponse).toHaveBeenCalledTimes(1)
    expect(new URL(String(fetchResponse.mock.calls[0]?.[0]), 'http://localhost').search).toBe('?before_seq=5')
    expect(warn).toHaveBeenCalledExactlyOnceWith(
      '[rooms] could not load earlier group-chat messages',
      { roomId: ROOM, beforeSeq: 5, error: expect.any(Error) },
    )
    const context = warn.mock.calls[0]?.[1] as { error: Error }
    expect(context.error.message).toContain('contradictory older-history page')
  })

  it('Retry after a repeated boundary requests that SAME boundary and recovers with a valid sparse page', async () => {
    respond(page([entry(5), entry(9)]))
    await useRoomStore.getState().loadOlder(ROOM)
    expect(useRoomStore.getState().rooms[ROOM]?.historyStatus).toBe('error')

    respond(page([entry(1), entry(4)]))
    await useRoomStore.getState().loadOlder(ROOM)

    expect(fetchResponse.mock.calls.map(([input]) => new URL(String(input), 'http://localhost').search)).toEqual([
      '?before_seq=5', '?before_seq=5',
    ])
    const room = useRoomStore.getState().rooms[ROOM]!
    expect(room.entries.map((row) => row.seq)).toEqual([1, 4, 5, 9])
    expect([room.historyStatus, room.historyFailed, room.hasOlder, room.olderBoundary]).toEqual(['idle', false, true, 1])
  })
})

describe('controls — sparse and terminal pages are legal', () => {
  it.each([true, false])('same-room [1,4] with has_older=%s is accepted below exclusive boundary 5', async (hasOlder) => {
    respond(page([entry(1), entry(4)], hasOlder))
    await useRoomStore.getState().loadOlder(ROOM)

    const room = useRoomStore.getState().rooms[ROOM]!
    expect(room.entries.map((row) => row.seq)).toEqual([1, 4, 5, 9])
    expect([room.historyStatus, room.historyFailed, room.hasOlder, room.olderBoundary]).toEqual([
      'idle', false, hasOlder, hasOlder ? 1 : undefined,
    ])
    expect(warn).not.toHaveBeenCalled()
  })

  it('an empty terminal page is still accepted', async () => {
    respond(page([], false))
    await useRoomStore.getState().loadOlder(ROOM)

    const room = useRoomStore.getState().rooms[ROOM]!
    expect(room.entries.map((row) => row.seq)).toEqual([5, 9])
    expect([room.historyStatus, room.historyFailed, room.hasOlder, room.olderBoundary]).toEqual(['idle', false, false, undefined])
    expect(warn).not.toHaveBeenCalled()
  })

  it('with no request boundary, a same-room ascending newest page is accepted', async () => {
    useRoomStore.setState({ rooms: {} })
    respond(page([entry(5), entry(9)]))
    await useRoomStore.getState().loadOlder(ROOM)

    const room = useRoomStore.getState().rooms[ROOM]!
    expect(new URL(String(fetchResponse.mock.calls[0]?.[0]), 'http://localhost').search).toBe('')
    expect(room.entries.map((row) => row.seq)).toEqual([5, 9])
    expect([room.historyStatus, room.historyFailed, room.hasOlder, room.olderBoundary]).toEqual(['idle', false, true, 5])
    expect(warn).not.toHaveBeenCalled()
  })
})
