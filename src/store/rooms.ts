// rooms.ts: the live state of group chats (omnipus-system-agent K45, K27;
// FR-199, FR-201, FR-216, FR-217; §6.11).
//
// A room is not a session (FR-199): its frames never touch the chat store's
// session buckets, the active session or any agent-session cursor. Each room
// keeps its OWN reconnect cursor (`seq`) — the highest room sequence applied —
// which only ever moves forward, so a stale or replayed frame can never
// rewind it (K45, SC-019). `attachRoom` asks the existing hub connection for
// everything after that cursor.
//
// Room frames arrive through slices/room-frames.ts, which consumes them
// before the chat store's session routing (a room frame's `seq` is a room
// sequence, not a session sequence).

import { create } from 'zustand'
import type {
  RoomDeliveryState,
  RoomEntry,
  RoomEntryFrame,
  RoomMemberStateFrame,
  RoomMetaFrame,
  RoomSnapshotFrame,
} from '@/lib/api/generated/asyncapi-types'
import { useConnectionStore } from '@/store/connection'

/**
 * One tracked request (room message × member). `order` is the room sequence
 * of the entry that created it — or, for a request the transcript does not
 * (yet) carry, the sequence at which it was first seen — so "the member's
 * most recent request" (§6.11) is decidable whatever order updates arrive in.
 */
export interface TrackedDelivery { // not-wire-format: a generated RoomDeliveryState plus its local recency rank
  delivery: RoomDeliveryState
  order: number
  /** Room sequence of the state shown; an older state never replaces it. */
  stateSeq: number
}

export interface RoomLiveState { // not-wire-format: in-browser projection of the generated room frames
  /** Highest room sequence applied — the reconnect cursor. */
  seq: number
  /** Ordered by seq; one entry per room sequence. */
  entries: RoomEntry[]
  members: string[]
  muted: string[]
  paused: boolean
  generation: number
  readOnlyReason?: string
  allocatedCostMicroUsd: number
  allocatedCostUnpriced: boolean
  /** A snapshot has arrived since the latest attach; complete is reserved (ruling Q2). */
  snapshotReceived: boolean
  /** Older history was omitted from the displayed snapshot. */
  hasOlder: boolean
  deliveries: Record<string, TrackedDelivery>
}

type RoomFrame = RoomSnapshotFrame | RoomEntryFrame | RoomMemberStateFrame | RoomMetaFrame

interface RoomStore {
  rooms: Record<string, RoomLiveState>
  /** Attach a room on the existing hub connection, resuming after its cursor. */
  attachRoom: (roomId: string) => boolean
  applyRoomFrame: (frame: RoomFrame) => void
}

function emptyRoom(): RoomLiveState {
  return {
    seq: 0, entries: [], members: [], muted: [], paused: false, generation: 0,
    allocatedCostMicroUsd: 0, allocatedCostUnpriced: false, snapshotReceived: false, hasOlder: false, deliveries: {},
  }
}

/**
 * `authoritative` states (member_states, room_member_state) replace a known
 * request's state; an entry's embedded deliveries only seed requests not seen
 * yet — a replayed entry carries the state as of its writing and must never
 * undo a newer live update.
 */
function track(
  deliveries: Record<string, TrackedDelivery>,
  delivery: RoomDeliveryState,
  seq: number,
  authoritative: boolean,
): void {
  const known = deliveries[delivery.request_id]
  if (!known) {
    deliveries[delivery.request_id] = { delivery, order: seq, stateSeq: seq }
    return
  }
  const order = Math.min(known.order, seq)
  const replaces = authoritative && seq >= known.stateSeq
  deliveries[delivery.request_id] = replaces ? { delivery, order, stateSeq: seq } : { ...known, order }
}

function mergeEntries(current: RoomEntry[], incoming: RoomEntry[]): RoomEntry[] {
  const bySeq = new Map(current.map((entry) => [entry.seq, entry]))
  for (const entry of incoming) bySeq.set(entry.seq, entry)
  return [...bySeq.values()].sort((a, b) => a.seq - b.seq)
}

function trackEntryDeliveries(deliveries: Record<string, TrackedDelivery>, entries: RoomEntry[]): void {
  for (const entry of entries) for (const delivery of entry.deliveries) track(deliveries, delivery, entry.seq, false)
}

function metaOf(frame: RoomSnapshotFrame | RoomMetaFrame): Partial<RoomLiveState> {
  return {
    members: frame.members, muted: frame.muted, paused: frame.paused, generation: frame.generation,
    readOnlyReason: frame.read_only_reason,
    allocatedCostMicroUsd: frame.allocated_cost_micro_usd,
    allocatedCostUnpriced: frame.allocated_cost_unpriced,
  }
}

function reduce(room: RoomLiveState, frame: RoomFrame): RoomLiveState {
  const deliveries = { ...room.deliveries }
  const seq = Math.max(room.seq, frame.seq)
  switch (frame.type) {
    case 'room_snapshot': {
      trackEntryDeliveries(deliveries, frame.entries)
      for (const delivery of frame.member_states) track(deliveries, delivery, frame.seq, true)
      return {
        ...room, ...metaOf(frame), seq, deliveries,
        // A cursor snapshot extends the list; truncation starts a new, gap-free window (Q2).
        entries: mergeEntries(frame.has_older ? [] : room.entries, frame.entries),
        snapshotReceived: true,
        hasOlder: frame.has_older === true,
      }
    }
    case 'room_entry': {
      trackEntryDeliveries(deliveries, [frame.entry])
      return { ...room, seq, deliveries, entries: mergeEntries(room.entries, [frame.entry]) }
    }
    case 'room_member_state':
      track(deliveries, frame.delivery, frame.seq, true)
      return { ...room, seq, deliveries }
    case 'room_meta':
      // Metadata older than what is applied must not undo a newer state.
      return frame.seq < room.seq ? room : { ...room, ...metaOf(frame), seq }
  }
}

export const useRoomStore = create<RoomStore>()((set, get) => ({
  rooms: {},
  attachRoom: (roomId) => {
    const connection = useConnectionStore.getState().connection
    if (!connection) return false
    // Keep cached entries/cursor, but wait for this attach's first snapshot before sending.
    set((state) => ({
      rooms: { ...state.rooms, [roomId]: { ...(state.rooms[roomId] ?? emptyRoom()), snapshotReceived: false } },
    }))
    return connection.send({ type: 'attach_room', room_id: roomId, after_seq: get().rooms[roomId]?.seq ?? 0 })
  },
  applyRoomFrame: (frame) => {
    set((state) => ({
      rooms: { ...state.rooms, [frame.room_id]: reduce(state.rooms[frame.room_id] ?? emptyRoom(), frame) },
    }))
  },
}))

/**
 * §6.11 "Several outstanding requests for one member are tracked per request
 * id; the member's displayed state is the most recent one." Ties go to the
 * request tracked last.
 */
export function latestDeliveryByMember(room: RoomLiveState | undefined): Record<string, RoomDeliveryState> {
  const latest: Record<string, TrackedDelivery> = {}
  for (const tracked of Object.values(room?.deliveries ?? {})) {
    const current = latest[tracked.delivery.agent_id]
    if (!current || tracked.order >= current.order) latest[tracked.delivery.agent_id] = tracked
  }
  return Object.fromEntries(Object.entries(latest).map(([agentId, tracked]) => [agentId, tracked.delivery]))
}
