// Spec test row 172 — K27/K45 generated outbound union, §6.11 states.
// These are EXISTING-CONTRACT INSTRUMENT CONTROLS, not RED proof of a room UI.
// The UI/store tests supply the missing-consumer RED. Real parser and real
// WsConnection; only browser socket is fake. No hand-written schema.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { waitFor } from '@testing-library/react'
import type { AttachRoomFrame, RoomDeliveryState } from './api/generated/asyncapi-types'
import { parseFrameSafe, getDroppedFrameCount, resetDroppedFrameCount } from './ws'
import type { WsConnection } from './ws'
import {
  connectSocket, delivery, entry, Network, resetRealStores, ROOM_ID, snapshot, stopHarness,
} from '../../tests/l1-room-ui/harness'

let connection: WsConnection | undefined
beforeEach(() => { resetRealStores(); new Network(); resetDroppedFrameCount(); connection = undefined })
afterEach(() => stopHarness(connection))

const states: RoomDeliveryState['state'][] = [
  'recorded', 'awaiting_approval', 'admitted', 'held', 'working', 'waiting_on_user',
  'answered', 'refused', 'failed', 'interrupted', 'unavailable', 'cancelled', 'unknown',
]

describe('K27/K45 instrument controls — generated parser fidelity', () => {
  it.each(states)('[§6.11; K27; test 172] parses the complete %s delivery without losing request identity', (state) => {
    const frame = { type: 'room_member_state' as const, room_id: ROOM_ID, seq: 2, delivery: delivery(state) }
    expect(parseFrameSafe(JSON.stringify(frame))).toEqual(frame)
    expect(getDroppedFrameCount()).toBe(0)
  })

  it('[K45; test 172] snapshot retains transcript, mutes, generation, state, cost and completeness', () => {
    const frame = snapshot({
      seq: 2, entries: [entry()], member_states: [delivery('held')],
      muted: ['jim'], paused: true, generation: 1,
      allocated_cost_unpriced: true, complete: false,
    })
    expect(parseFrameSafe(JSON.stringify(frame))).toEqual(frame)
    expect(getDroppedFrameCount()).toBe(0)
  })

  it('[K26/K45; test 172] a live entry retains author, mentions and after-pause identity', () => {
    const frame = { type: 'room_entry' as const, room_id: ROOM_ID, seq: 2, entry: entry({
      seq: 2, author: { kind: 'agent', agent_id: 'jim' },
      text: '@Mia done', addressees: ['mia'], after_pause: true,
    }) }
    expect(parseFrameSafe(frame)).toEqual(frame)
  })

  it.each([
    { field: 'state', change: { delivery: { ...delivery('held'), state: 'busy' } } },
    { field: 'request_id', change: { delivery: { agent_id: 'jim', state: 'held' } } },
    { field: 'seq', change: { seq: null } },
  ])('[K27/K45 negative instrument control] malformed $field is rejected, not downgraded to working', ({ change }) => {
    const valid = { type: 'room_member_state', room_id: ROOM_ID, seq: 2, delivery: delivery('held') }
    expect(parseFrameSafe(valid)).toEqual(valid)
    expect(parseFrameSafe({ ...valid, ...change })).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it.each(['members', 'muted', 'entries', 'member_states'] as const)(
    '[K45 negative instrument control] null %s is rejected while an empty array is valid', (field) => {
      const valid = snapshot({ [field]: [] })
      expect(parseFrameSafe(valid)).toEqual(valid)
      expect(parseFrameSafe({ ...valid, [field]: null })).toBeNull()
      expect(getDroppedFrameCount()).toBe(1)
    },
  )

  it('[K45 direction negative control] an inbound attach_room on the server socket is dropped; a genuine room snapshot is delivered', async () => {
    const linked = connectSocket(); connection = linked.connection
    const wrongDirection: AttachRoomFrame = { type: 'attach_room', room_id: ROOM_ID, after_seq: 0 }
    act(() => linked.socket.onmessage?.(new MessageEvent('message', { data: JSON.stringify(wrongDirection) })))
    await waitFor(() => expect(getDroppedFrameCount()).toBe(1))
    // A real outbound room frame is our positive control for the same transport.
    await linked.emit(snapshot())
    expect(getDroppedFrameCount()).toBe(1)
  })
})
