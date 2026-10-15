// WU-15 fix round 1, S4 / code-reviewer F06 (ruling Q2; Hard Constraint #8).
// A room keeps every live (non-terminal) delivery state in its attach
// snapshot. More than 10,000 of them must still pass the SPA's real
// generated frame validator (parseFrameSafe); a frame it rejects is dropped
// whole and the room cannot be restored on reconnect.
import { beforeEach, describe, expect, it } from 'vitest'
import type { RoomDeliveryState, RoomSnapshotFrame } from '../../src/lib/api/generated/asyncapi-types'
import { getDroppedFrameCount, parseFrameSafe, resetDroppedFrameCount } from '../../src/lib/ws'

function liveStates(n: number): RoomDeliveryState[] {
  const out: RoomDeliveryState[] = []
  for (let i = 0; i < n; i++) {
    out.push({ agent_id: i % 2 === 0 ? 'mia' : 'jim', request_id: `request-${i}`, state: 'held' })
  }
  return out
}

function snapshot(states: RoomDeliveryState[]): RoomSnapshotFrame {
  return {
    type: 'room_snapshot', room_id: 'room-1', seq: states.length + 1, entries: [],
    members: ['mia', 'jim'], muted: [], member_states: states, paused: false, generation: 0,
    allocated_cost_micro_usd: 0, allocated_cost_unpriced: false, complete: true,
  }
}

describe('room snapshot with more than 10,000 live states (S4)', () => {
  beforeEach(() => resetDroppedFrameCount())

  it('passes the generated validator and keeps every live state', () => {
    const frame = snapshot(liveStates(10_001))
    const parsed = parseFrameSafe(JSON.stringify(frame)) as RoomSnapshotFrame | null
    expect(parsed, 'S4: a snapshot with 10,001 live states was dropped').not.toBeNull()
    expect(parsed?.member_states).toHaveLength(10_001)
    expect(getDroppedFrameCount()).toBe(0)
  })

  it('control: the validator still rejects a malformed state', () => {
    const states = liveStates(3)
    const bad = [...states, { agent_id: 'mia', request_id: 'x', state: 'not-a-state' }] as unknown as RoomDeliveryState[]
    expect(parseFrameSafe(JSON.stringify(snapshot(bad)))).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })
})
