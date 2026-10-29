// RED — #1449 US-6, S-43, S-44, DS-9 rows 1 and 4–5.
// Oracle: docs/internal/specs/1449-group-chat-spec.md. The shared fill asks for
// pages of 20 to 100 entries. The network client is the only double.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act } from 'react'
import type { RoomEntry } from '@/lib/api/generated/asyncapi-types'
import type { RoomHistoryResponse } from '@/lib/api/generated/openapi-types'
import { useRoomStore } from './rooms'

const ROOM = 'launch'

const fetchRoomHistory = vi.hoisted(() => vi.fn<(roomId: string, opts?: { beforeSeq?: number; limit?: number }) => Promise<RoomHistoryResponse>>())
vi.mock('@/lib/api/rooms', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  fetchRoomHistory,
}))

function entry(seq: number): RoomEntry {
  return {
    id: `room-message-${seq}`, room_id: ROOM, seq,
    client_message_id: `client-${seq}`, author: { kind: 'user' },
    text: `entry-${seq}`, addressees: [], after_pause: false, deliveries: [],
    timestamp: '2026-10-28T10:00:00Z',
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  fetchRoomHistory.mockReset()
  act(() => useRoomStore.setState({ rooms: {} }))
})

describe('group history pages (US-6, DS-9)', () => {
  it('S-44 requests a page whose limit is between 20 and 100', async () => {
    fetchRoomHistory.mockResolvedValue({ entries: [entry(1)], has_older: false })
    act(() => useRoomStore.getState().applyRoomFrame({
      type: 'room_snapshot', room_id: ROOM, seq: 5,
      entries: [entry(5)], members: ['mia', 'jim'], member_states: [],
      muted: [], paused: false, generation: 1,
      allocated_cost_micro_usd: 0, allocated_cost_unpriced: false,
      complete: true, has_older: true,
    }))

    await useRoomStore.getState().loadOlder(ROOM)

    expect(fetchRoomHistory).toHaveBeenCalledTimes(1)
    const opts = fetchRoomHistory.mock.calls[0]?.[1]
    expect(opts?.beforeSeq, 'S-44: the cursor is the earliest displayed seq').toBe(5)
    expect(typeof opts?.limit).toBe('number')
    expect(opts!.limit!).toBeGreaterThanOrEqual(20)
    expect(opts!.limit!).toBeLessThanOrEqual(100)
  })

  it('S-43 makes no history call when the server says there is nothing older', async () => {
    act(() => useRoomStore.getState().applyRoomFrame({
      type: 'room_snapshot', room_id: ROOM, seq: 2,
      entries: [entry(1), entry(2)], members: ['mia', 'jim'], member_states: [],
      muted: [], paused: false, generation: 1,
      allocated_cost_micro_usd: 0, allocated_cost_unpriced: false,
      complete: true, has_older: false,
    }))

    await useRoomStore.getState().loadOlder(ROOM)

    expect(fetchRoomHistory).not.toHaveBeenCalled()
  })
})
