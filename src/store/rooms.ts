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
//
// Older history (#1332, ROOM-HISTORY-PAGING-1332-20261018.md D6): `loadOlder`
// pages backward through GET getRoomHistory from the earliest displayed
// entry. A page is merged by seq with the CURRENT list (displayed entries win
// collisions) and never moves the reconnect cursor, members, mutes, pause,
// generation, cost or the first-snapshot send gate. A page that contradicts
// the response contract's continuation invariant (has_older ⇔ next_before_seq
// = first entry seq; empty ⇒ terminal) is a visible error with Retry, never
// success. One older-page request is in flight per room; a new attach (any
// room), a truncating snapshot or leaving the room invalidates it, and its
// late success or failure changes nothing.
//
// #1449 (ADR-20261028 A1.3): the shared chat list reads a room through a
// ConversationSource. For it the store also keeps, per room, the reader's
// view mode (following the latest entry or reading earlier ones), a view
// generation that advances when a truncating snapshot replaces the window,
// the number of attempts of the current older-page request, a page `limit`
// for older pages, the invitation-context status from `room_meta`, and a
// count of received snapshots (a re-attach after a reconnect). All new
// fields are optional and default to the previous behaviour.

import { create } from 'zustand'
import type {
  RoomDeliveryState,
  RoomEntry,
  RoomEntryFrame,
  RoomMemberStateFrame,
  RoomMetaFrame,
  RoomSnapshotFrame,
} from '@/lib/api/generated/asyncapi-types'
import type { RoomHistoryResponse } from '@/lib/api/generated/openapi-types'
import { fetchRoomHistory } from '@/lib/api/rooms'
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
  /** Older history exists before the earliest displayed entry. */
  hasOlder: boolean
  /** #1332: the older-page request state for this room. */
  historyStatus: 'idle' | 'loading' | 'error'
  /** #1332: the last older-page request failed (kept while its Retry is loading). */
  historyFailed: boolean
  /** #1332: the next before_seq; undefined once the old end is reached (or unknown). */
  olderBoundary?: number
  /** #1332: token of the one in-flight older-page request; undefined when none. */
  historyToken?: number
  deliveries: Record<string, TrackedDelivery>
  /** #1449: the reader follows the newest entry ('latest', default) or reads earlier ones. */
  viewMode?: 'latest' | 'historical'
  /** #1449: advances when a truncating snapshot replaces the window (default 0). */
  viewGeneration?: number
  /** #1449: how many snapshots arrived (each attach answers with one); a re-attach advances it. */
  snapshotCount?: number
  /** #1449 FR-045: the invitation summary's status, from `room_meta`/the room record. */
  invitationContextStatus?: InvitationContextStatus
}

/** #1449 FR-041: the invitation summary lifecycle (generated `Room.invitation_context_status`). */
export type InvitationContextStatus = NonNullable<RoomMetaFrame['invitation_context_status']>

type RoomFrame = RoomSnapshotFrame | RoomEntryFrame | RoomMemberStateFrame | RoomMetaFrame

interface RoomStore {
  rooms: Record<string, RoomLiveState>
  /** Attach a room on the existing hub connection, resuming after its cursor. */
  attachRoom: (roomId: string) => boolean
  applyRoomFrame: (frame: RoomFrame) => void
  /**
   * #1332: load the page before the earliest displayed entry (one in flight per room).
   * #1449: `limit` sizes the page (the shared fill asks for 20-100 entries).
   */
  loadOlder: (roomId: string, opts?: { limit?: number }) => Promise<void>
  /** #1449: the reader follows the latest entry or reads earlier ones (UI state, like a session's). */
  setViewMode: (roomId: string, mode: 'latest' | 'historical') => void
  /** #1449: attempts of the current older-page request per room (0 when none failed). */
  historyAttempts: Record<string, number>
  /** #1332: the room is no longer shown — invalidate its in-flight older-page request. */
  abandonOlder: (roomId: string) => void
}

function emptyRoom(): RoomLiveState {
  return {
    seq: 0, entries: [], members: [], muted: [], paused: false, generation: 0,
    allocatedCostMicroUsd: 0, allocatedCostUnpriced: false, snapshotReceived: false, hasOlder: false,
    historyStatus: 'idle', historyFailed: false, deliveries: {},
  }
}

/** Drop a room's in-flight older-page request: its late result is ignored. */
function withoutHistoryRequest(room: RoomLiveState): RoomLiveState {
  if (room.historyToken === undefined) return room
  return { ...room, historyToken: undefined, historyStatus: room.historyFailed ? 'error' : 'idle' }
}

let lastHistoryToken = 0

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

/**
 * RoomHistoryResponse's conditional continuation rules are producer/consumer
 * invariants (contracts/components/schemas/RoomHistoryResponse.yaml; #1332):
 * `next_before_seq` is present if and only if `has_older` is true and equals
 * the first returned entry's seq; an empty page is `entries: []` with
 * `has_older: false` and no cursor. Each entry must also belong to the
 * requested room, precede its exclusive cursor and be strictly ascending.
 * The generated schema checks structure only, so a broken invariant is a
 * protocol failure — never successful empty history (which could loop on
 * the same boundary) and never a
 * silently invented cursor (loadOlder's error/Retry path surfaces it).
 */
function pageContradictsContinuation(page: RoomHistoryResponse, roomId: string, beforeSeq?: number): boolean {
  if (page.entries.some((entry, index) =>
    entry.room_id !== roomId ||
    (beforeSeq !== undefined && entry.seq >= beforeSeq) ||
    (index > 0 && entry.seq <= page.entries[index - 1]!.seq),
  )) return true
  if (!page.has_older) return page.next_before_seq !== undefined
  return page.entries.length === 0 || page.next_before_seq !== page.entries[0]?.seq
}

function trackEntryDeliveries(deliveries: Record<string, TrackedDelivery>, entries: RoomEntry[]): void {
  for (const entry of entries) for (const delivery of entry.deliveries) track(deliveries, delivery, entry.seq, false)
}

function metaOf(frame: RoomSnapshotFrame | RoomMetaFrame): Partial<RoomLiveState> {
  const invitation = frame.type === 'room_meta' ? frame.invitation_context_status : undefined
  return {
    ...(invitation ? { invitationContextStatus: invitation } : {}),
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
      const base = { ...room, ...metaOf(frame), seq, deliveries, snapshotReceived: true, snapshotCount: (room.snapshotCount ?? 0) + 1 }
      if (frame.has_older === true) {
        // Truncation starts a new, gap-free window (Q2): exactly the snapshot,
        // older paging restarts before its first entry, and a page requested
        // for the previous window is invalidated (#1332 D6).
        const entries = mergeEntries([], frame.entries)
        return {
          ...withoutHistoryRequest(base), entries, hasOlder: true,
          olderBoundary: entries[0]?.seq, historyFailed: false,
          historyStatus: 'idle', historyToken: undefined,
          // #1449: the shared list starts a fresh view of the new window.
          viewGeneration: (room.viewGeneration ?? 0) + 1, viewMode: 'latest',
        }
      }
      if (room.entries.length === 0) {
        // First view of the room: nothing was left out (absent = false).
        return { ...base, entries: mergeEntries([], frame.entries), hasOlder: false, olderBoundary: undefined }
      }
      // A cursor catch-up only proves nothing was omitted AFTER the cursor —
      // not that the beginning is loaded: merge, and keep the old-end state.
      return { ...base, entries: mergeEntries(room.entries, frame.entries) }
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
  historyAttempts: {},
  setViewMode: (roomId, mode) => {
    set((state) => {
      const room = state.rooms[roomId]
      if (!room || (room.viewMode ?? 'latest') === mode) return state
      return { rooms: { ...state.rooms, [roomId]: { ...room, viewMode: mode } } }
    })
  },
  attachRoom: (roomId) => {
    // Keep cached entries/cursor, but wait for this attach's first snapshot
    // before sending. Any attach (this room or another) invalidates every
    // in-flight older-page request (#1332 D6 stale-response fence).
    set((state) => {
      const rooms: Record<string, RoomLiveState> = {}
      for (const [id, room] of Object.entries(state.rooms)) rooms[id] = withoutHistoryRequest(room)
      rooms[roomId] = { ...(rooms[roomId] ?? emptyRoom()), snapshotReceived: false }
      return { rooms }
    })
    const connection = useConnectionStore.getState().connection
    if (!connection) return false
    return connection.send({ type: 'attach_room', room_id: roomId, after_seq: get().rooms[roomId]?.seq ?? 0 })
  },
  applyRoomFrame: (frame) => {
    set((state) => ({
      rooms: { ...state.rooms, [frame.room_id]: reduce(state.rooms[frame.room_id] ?? emptyRoom(), frame) },
    }))
  },
  loadOlder: async (roomId, opts) => {
    const room = get().rooms[roomId] ?? emptyRoom()
    if (room.historyToken !== undefined) return // one in flight per room
    // The old end is known reached: nothing to request.
    if (room.snapshotReceived && !room.hasOlder) return
    const beforeSeq = room.olderBoundary ?? room.entries[0]?.seq
    const token = ++lastHistoryToken
    set((state) => ({
      rooms: { ...state.rooms, [roomId]: { ...(state.rooms[roomId] ?? emptyRoom()), historyToken: token, historyStatus: 'loading' } },
    }))
    let page: RoomHistoryResponse | undefined
    let failed = false
    try {
      const limit = opts?.limit
      page = await fetchRoomHistory(roomId, {
        ...(beforeSeq === undefined ? {} : { beforeSeq }),
        ...(limit === undefined ? {} : { limit }),
      })
      if (!page || !Array.isArray(page.entries)) {
        failed = true
      } else if (pageContradictsContinuation(page, roomId, beforeSeq)) {
        // A contradictory page must reach the room's visible error/Retry path
        // with rows, boundary and hasOlder intact — not the success branch.
        throw new Error(
          `contradictory older-history page for room ${roomId}: ` +
            `has_older=${String(page.has_older)} entries=${page.entries.length} ` +
            `next_before_seq=${String(page.next_before_seq)}`,
        )
      }
    } catch (error) {
      failed = true
      console.warn('[rooms] could not load earlier group-chat messages', { roomId, beforeSeq, error })
    }
    set((state) => {
      const current = state.rooms[roomId]
      // Invalidated (attach, truncation, leaving the room): change nothing.
      if (!current || current.historyToken !== token) return state
      if (failed || !page) {
        return {
          rooms: { ...state.rooms, [roomId]: { ...current, historyToken: undefined, historyStatus: 'error', historyFailed: true } },
          historyAttempts: { ...state.historyAttempts, [roomId]: (state.historyAttempts[roomId] ?? 0) + 1 },
        }
      }
      const deliveries = { ...current.deliveries }
      trackEntryDeliveries(deliveries, page.entries)
      return {
        historyAttempts: { ...state.historyAttempts, [roomId]: 0 },
        rooms: {
          ...state.rooms,
          [roomId]: {
            ...current,
            deliveries,
            // Displayed entries win collisions with the older page.
            entries: mergeEntries(page.entries, current.entries),
            hasOlder: page.has_older,
            olderBoundary: page.next_before_seq,
            historyToken: undefined,
            historyStatus: 'idle',
            historyFailed: false,
          },
        },
      }
    })
  },
  abandonOlder: (roomId) => {
    set((state) => {
      const room = state.rooms[roomId]
      if (!room || room.historyToken === undefined) return state
      return { rooms: { ...state.rooms, [roomId]: withoutHistoryRequest(room) } }
    })
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
