// rooms.ts: group chats' REST half (omnipus-system-agent K23, K24, K26;
// FR-161, FR-162, FR-166, FR-172, FR-174, FR-199, FR-204, FR-207).
//
// A room is not a session: nothing here creates, attaches or reads an agent
// session. The live transcript arrives over the existing WebSocket
// (src/store/rooms.ts); a posted message is acknowledged with a bodyless 202
// and recorded as a `room_entry` carrying the same client_message_id.
// Every body is a generated type and every response is validated against the
// generated schema (Hard Constraint #8). Failures are thrown, never turned
// into an empty list or a fabricated success.

import type { ZodType } from 'zod'
import { z } from 'zod'
import { Room as RoomSchema } from '@/lib/api/generated/schemas'
import type {
  Room,
  RoomCreateRequest,
  RoomMembersUpdateRequest,
  RoomMessageRequest,
} from '@/lib/api/generated/openapi-types'
import { ApiError, isApiError, parseServerErrorField } from '../api-error'
import { request } from './http'

/**
 * Room refusals are spec copy the user must read as written (FR-204 "a room
 * needs at least two agents", §6.13 "Several members are called <Name> — pick
 * from the list", FR-205 "Rooms don't steer agents — use <Agent>'s main
 * chat", §6.11 "room unavailable"). The app-wide ApiError replaces a 404/409/
 * 410 body with generic status text; for these room calls only, a server
 * reason in the body is kept (the #1102 precedent, IntegrationsSection).
 * Server errors (5xx) and auth/rate-limit statuses keep the generic text.
 */
const ROOM_REASON_STATUSES = new Set([404, 409, 410])

async function withRoomReason<T>(call: Promise<T>): Promise<T> {
  try {
    return await call
  } catch (error) {
    if (!isApiError(error) || !ROOM_REASON_STATUSES.has(error.status)) throw error
    const reason = parseServerErrorField(error.body)
    if (!reason) throw error
    throw new ApiError(error.status, reason, { code: error.code, field: error.field, body: error.body, cause: error })
  }
}

const roomSchema = RoomSchema as ZodType<Room>
const roomListSchema = z.array(RoomSchema) as ZodType<Room[]>

export const roomsQueryKeys = {
  all: ['rooms'] as const,
  list: (workspaceId: string) => ['rooms', 'workspace', workspaceId] as const,
  detail: (roomId: string) => ['rooms', 'room', roomId] as const,
}

/** K23: the workspace's group chats (team room, squads, ad-hoc rooms). */
export function fetchWorkspaceRooms(workspaceId: string): Promise<Room[]> {
  return withRoomReason(request<Room[]>(`/workspaces/${encodeURIComponent(workspaceId)}/rooms`, undefined, roomListSchema))
}

/** K23: one room's record (members, mutes, pause, cost, read-only reason). */
export function fetchRoom(roomId: string): Promise<Room> {
  return withRoomReason(request<Room>(`/rooms/${encodeURIComponent(roomId)}`, undefined, roomSchema))
}

/** K24: create a squad or an ad-hoc room; the team room is never created here. */
export function createRoom(workspaceId: string, body: RoomCreateRequest): Promise<Room> {
  return withRoomReason(request<Room>(
    `/workspaces/${encodeURIComponent(workspaceId)}/rooms`,
    { method: 'POST', body: JSON.stringify(body) },
    roomSchema,
  ))
}

/** K24: add, remove, mute or unmute members — room membership, never agent configuration. */
export function updateRoomMembers(roomId: string, body: RoomMembersUpdateRequest): Promise<Room> {
  return withRoomReason(request<Room>(
    `/rooms/${encodeURIComponent(roomId)}/members`,
    { method: 'PATCH', body: JSON.stringify(body) },
    roomSchema,
  ))
}

/** K24: delete one room (bodyless 204). */
export function deleteRoom(roomId: string): Promise<void> {
  return withRoomReason(request<void>(`/rooms/${encodeURIComponent(roomId)}`, { method: 'DELETE' }))
}

/**
 * K26: post a user message (bodyless 202). The same client_message_id is
 * sent again on Retry, so a message the server already recorded is never
 * recorded twice (FR-216).
 */
export function postRoomMessage(roomId: string, body: RoomMessageRequest): Promise<void> {
  return withRoomReason(request<void>(
    `/rooms/${encodeURIComponent(roomId)}/messages`,
    { method: 'POST', body: JSON.stringify(body) },
  ))
}
