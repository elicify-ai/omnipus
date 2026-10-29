// roomConversationSource — the group chat as the shared chat stack reads it
// (#1449 ADR-20261028 A1.3, A3.1). A projection over the room's own store
// (src/store/rooms.ts): no chat session, no session bucket, its own cursor
// (FR-199). Every mapping below is the room store's existing behaviour:
//
//   historyPosition  `room:<seq>`
//   olderBoundary    the room's olderBoundary, else the first entry's seq
//   loadOlder(size)  useRoomStore.loadOlder(roomId, { limit: size.limit })
//   requests.older   historyStatus / historyFailed, plus the attempt count
//   viewGeneration   advanced by a truncating snapshot
//   newer/refresh    none: the window always reaches the live tail
//   viewMode         the room's own reader mode; Return to latest needs no fetch
//   catchingUp       until this opening's attach snapshot arrived

import { useMemo, useRef } from 'react'
import type { RoomEntry } from '@/lib/api/generated/asyncapi-types'
import type { ChatMessage } from '@/store/chat'
import type { MediaAttachment, SessionHistoryState } from '@/store/chat/types'
import { emptyHistoryState } from '@/store/chat'
import { useRoomStore, type RoomLiveState } from '@/store/rooms'
import type { ConversationSource } from '@/components/chat/conversationSource'
import type { RoomListOpening } from '@/components/chat/ChatMessageList'

/** The visible wording of a failed earlier-page read (the shared boundary adds Retry). */
export const ROOM_OLDER_PAGE_ERROR = 'Could not load earlier messages.'

/**
 * One entry as a chat message. Memoised per entry OBJECT: the room store keeps
 * an unchanged entry's identity, so a memoised row never re-renders for a
 * delivery or metadata frame (ADR A1.3 row identity, T-30).
 */
const projected = new WeakMap<RoomEntry, ChatMessage>()
export function projectRoomEntry(entry: RoomEntry): ChatMessage {
  const known = projected.get(entry)
  if (known) return known
  const media: MediaAttachment[] = (entry.attachments ?? []).map((part) => ({
    type: part.type,
    url: part.url,
    filename: part.filename,
    contentType: part.content_type,
    ...(part.caption ? { caption: part.caption } : {}),
  }))
  const common = {
    id: entry.id,
    content: entry.text,
    timestamp: entry.timestamp,
    historyPosition: `room:${entry.seq}`,
    ...(media.length > 0 ? { media } : {}),
  }
  const message: ChatMessage = entry.author.kind === 'agent'
    ? {
        ...common,
        role: 'assistant',
        status: 'done',
        ...(entry.author.agent_id ? { agentId: entry.author.agent_id } : {}),
        ...(entry.after_pause ? { roomMarks: { afterPause: true } } : {}),
      }
    : { ...common, role: 'user', status: 'done' }
  projected.set(entry, message)
  return message
}

function olderRequest(room: RoomLiveState | undefined, attempt: number): SessionHistoryState['requests']['older'] {
  if (!room) return { status: 'idle', error: null, attempt: 0 }
  return {
    status: room.historyStatus === 'loading' ? 'loading' : room.historyFailed ? 'error' : 'idle',
    error: room.historyFailed ? ROOM_OLDER_PAGE_ERROR : null,
    attempt,
  }
}

/** The SessionHistoryState-shaped view of one room (what the shared list reads). */
export function roomHistory(room: RoomLiveState | undefined, attempt: number, attachedHere: boolean): SessionHistoryState {
  const base = emptyHistoryState()
  const entries = room?.entries ?? []
  const older = room?.olderBoundary ?? entries[0]?.seq
  return {
    ...base,
    viewGeneration: room?.viewGeneration ?? 0,
    viewMode: room?.viewMode ?? 'latest',
    historyLoaded: attachedHere && room?.snapshotReceived === true,
    liveControlsReady: true,
    readerPositions: entries.map((entry) => `room:${entry.seq}`),
    hasEarlier: room?.hasOlder === true,
    hasLater: false,
    olderBoundary: room?.hasOlder && older !== undefined ? `room:${older}` : null,
    newerBoundary: null,
    requests: { ...base.requests, older: olderRequest(room, attempt) },
  }
}

const done = async () => {}

export interface RoomConversation { // not-wire-format: the room source plus what the shared list needs on a group page
  source: ConversationSource
  messages: ChatMessage[]
  opening: Omit<RoomListOpening, 'tail'>
}

/**
 * The group's ConversationSource. `attachedHere` is true once this opening
 * attached the room (cached rows from an earlier visit never count as this
 * opening's live view); `readFailed` is the room record's read failure.
 */
export function useRoomConversationSource(roomId: string, attachedHere: boolean, readFailed: boolean): RoomConversation {
  const room = useRoomStore((state) => state.rooms[roomId])
  const attempt = useRoomStore((state) => state.historyAttempts[roomId] ?? 0)
  const entries = room?.entries
  const messages = useMemo(() => (entries ?? []).map(projectRoomEntry), [entries])
  // The last page size asked for: Retry repeats the same request (S-45).
  const lastLimit = useRef<number | undefined>(undefined)
  const history = useMemo(
    () => roomHistory(room, attempt, attachedHere),
    // Only the fields the history view reads: delivery and metadata frames
    // do not rebuild it.
    [entries, room?.hasOlder, room?.olderBoundary, room?.historyStatus, room?.historyFailed, room?.viewMode, room?.viewGeneration, room?.snapshotReceived, attempt, attachedHere],
  )
  const live = attachedHere && room?.snapshotReceived === true
  const source = useMemo<ConversationSource>(() => {
    const store = useRoomStore.getState
    const load = (limit: number | undefined) => {
      lastLimit.current = limit
      return store().loadOlder(roomId, limit === undefined ? undefined : { limit })
    }
    return {
      kind: 'room',
      key: `room:${roomId}`,
      sessionId: null,
      history,
      catchingUp: !live,
      capabilities: { newerWindow: false, clearMarkers: false, liveToolCalls: false },
      read: () => {
        const state = store()
        const current = state.rooms[roomId]
        if (!current) return null
        return {
          history: roomHistory(current, state.historyAttempts[roomId] ?? 0, attachedHere),
          catchingUp: !(attachedHere && current.snapshotReceived),
        }
      },
      enterReading: () => store().setViewMode(roomId, 'historical'),
      beginOlder: async (size) => {
        store().setViewMode(roomId, 'historical')
        return load(size.limit)
      },
      loadOlder: async (size) => load(size.limit),
      loadNewer: done,
      // The live tail is always loaded: following it again needs no read.
      returnToLatest: async () => store().setViewMode(roomId, 'latest'),
      refresh: done,
      retry: async (kind) => { if (kind === 'older') return load(lastLimit.current) },
      protectPositions: () => {},
      releaseProtection: () => {},
      recordGeometry: () => {},
      forgetGeometry: () => {},
    }
  }, [roomId, history, live, attachedHere])
  const openState: RoomListOpening['openState'] = readFailed ? 'failed' : live ? 'ready' : 'pending'
  const unloaded = !live && (entries?.length ?? 0) === 0
  return { source, messages, opening: { openState, unloaded } }
}
