// WU-15 fix round 1, S4 / code-reviewer F06 (ruling Q2; Hard Constraint #8).
// A room keeps every live (non-terminal) delivery state in its attach
// snapshot. More than 10,000 of them must still pass the SPA's real
// generated frame validator (parseFrameSafe); a frame it rejects is dropped
// whole and the room cannot be restored on reconnect.
import { beforeEach, describe, expect, it } from 'vitest'
import type { RoomDeliveryState, RoomSnapshotFrame } from '../../src/lib/api/generated/asyncapi-types'
import { getDroppedFrameCount, parseFrameSafe, resetDroppedFrameCount } from '../../src/lib/ws'
import { Room, RoomCreateRequest, RoomMembersUpdateRequest, RoomMessageRequest } from '../../src/lib/api/generated/schemas'

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

// Chief's accepted architect proposal: output arrays describe the whole team,
// while each create/mutation/message request retains its existing 256-id bound.
describe('whole-workspace team output above 256 members', () => {
  const members = Array.from({ length: 257 }, (_, i) => `agent-${i}`)
  const states: RoomDeliveryState[] = members.map((agent_id, i) => ({ agent_id, request_id: `d-${i}`, state: 'held' }))
  const base = { ...snapshot(states), members, muted: members }
  const entry = {
    id: 'entry-1', room_id: 'room-1', seq: 1, author: { kind: 'user' as const },
    text: '@all status', timestamp: '2026-10-16T00:00:00Z', addressees: members,
    after_pause: false, deliveries: states,
  }

  it('validates REST list/get metadata without truncating members or mutes', () => {
    const value = { id: 'room-1', workspace_id: 'sales', kind: 'team', name: 'Team',
      members, muted: members, paused: false, generation: 0, hidden: false,
      allocated_cost_micro_usd: 0, allocated_cost_unpriced: false, needs_attention: false }
    const parsed = Room.parse(value)
    expect(parsed.members).toEqual(members)
    expect(parsed.muted).toEqual(members)
  })

  it.each([
    ['snapshot', { ...base, entries: [entry] }],
    ['metadata', { ...base, type: 'room_meta', seq: 1, entries: undefined, member_states: undefined, complete: undefined }],
    ['default fanout entry', { type: 'room_entry', room_id: 'room-1', seq: 1, entry }],
  ])('accepts the full %s frame', (_name, frame) => {
    const parsed = parseFrameSafe(JSON.stringify(frame))
    expect(parsed).toEqual(JSON.parse(JSON.stringify(frame)))
  })

  it('control: creation and individual mutation requests still refuse 257 ids', () => {
    expect(RoomCreateRequest.safeParse({ kind: 'adhoc', agent_ids: members }).success).toBe(false)
    expect(RoomMembersUpdateRequest.safeParse({ action: 'mute', agent_ids: members }).success).toBe(false)
    expect(RoomMessageRequest.safeParse({ client_message_id: 'c1', text: 'hello', mentions: members }).success).toBe(false)
    expect(RoomCreateRequest.safeParse({ kind: 'adhoc', agent_ids: members.slice(0, 256) }).success).toBe(true)
    expect(RoomMembersUpdateRequest.safeParse({ action: 'mute', agent_ids: members.slice(0, 256) }).success).toBe(true)
  })
})

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
