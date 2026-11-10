// RED — omnipus-system-agent-spec K23/K24/K26, WU-15 SPA API half.
// Oracles: contract methods/paths/statuses + FRs, not observed implementation.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { Room, RoomCreateRequest, RoomMembersUpdateRequest, RoomMessageRequest } from './generated/openapi-types'
import { ApiError } from '@/lib/api-error'
import {
  Network, json, requiredExport, resetRealStores, room, stopHarness, MAIN_ID, ROOM_ID, WS,
} from '../../../tests/l1-room-ui/harness'

// not-wire-format: the implementer's named public function API; payloads are generated.
interface RoomsApi {
  fetchWorkspaceRooms(id: string): Promise<Room[]>
  fetchRoom(id: string): Promise<Room>
  createRoom(id: string, request: RoomCreateRequest): Promise<Room>
  updateRoomMembers(id: string, request: RoomMembersUpdateRequest): Promise<Room>
  deleteRoom(id: string): Promise<unknown>
  postRoomMessage(id: string, request: RoomMessageRequest): Promise<unknown>
  roomsQueryKeys: { list(id: string): readonly unknown[]; detail(id: string): readonly unknown[] }
}

async function api(): Promise<RoomsApi> {
  const path = '/src/lib/api/rooms.ts'
  const names = ['fetchWorkspaceRooms', 'fetchRoom', 'createRoom', 'updateRoomMembers', 'deleteRoom', 'postRoomMessage', 'roomsQueryKeys'] as const
  const exports = await Promise.all(names.map((name) => requiredExport(path, name, 'K23/K24/K26; FR-161/207')))
  return Object.fromEntries(names.map((name, index) => [name, exports[index]])) as unknown as RoomsApi
}

let net: Network
beforeEach(() => { resetRealStores(); net = new Network() })
afterEach(() => stopHarness())

function wireCalls() {
  return net.requests.map(({ method, path, body, credentials }) => ({ method, path, body, credentials }))
}

describe('K23/K24/K26 room API — real client, fetch boundary only', () => {
  it('[FR-174; K23] lists only the requested workspace via its room endpoint', async () => {
    const client = await api()
    const expected = [room({ kind: 'team', name: 'Team room' }), room({ id: 'research', name: 'Research', kind: 'adhoc' })]
    net.rooms = expected
    expect(await client.fetchWorkspaceRooms(WS)).toEqual(expected)
    expect(wireCalls()).toEqual([{ method: 'GET', path: `/api/v1/workspaces/${WS}/rooms`, body: null, credentials: 'include' }])
  })

  it('[FR-199; K23] reads the selected Room without creating or attaching an agent session', async () => {
    const client = await api()
    expect(await client.fetchRoom(ROOM_ID)).toEqual(room())
    expect(wireCalls()).toEqual([{ method: 'GET', path: `/api/v1/rooms/${ROOM_ID}`, body: null, credentials: 'include' }])
  })

  it('[FR-161; BDD-17.2; K24] creates a named two-agent squad with the exact generated body', async () => {
    const client = await api()
    const request: RoomCreateRequest = { kind: 'squad', name: 'Launch', agent_ids: ['mia', 'jim'] }
    const created = room({ members: ['mia', 'jim'] })
    net.respond('POST', `/api/v1/workspaces/${WS}/rooms`, () => json(created, 201))
    expect(await client.createRoom(WS, request)).toEqual(created)
    expect(wireCalls()).toEqual([{ method: 'POST', path: `/api/v1/workspaces/${WS}/rooms`, body: request, credentials: 'include' }])
    expect(net.requests[0]?.headers.get('X-CSRF-Token')).toBe('room-red-test')
  })

  it('[FR-207; BDD-17.24; K24] creates an ad-hoc room with the owner, picked agent and origin main id', async () => {
    const client = await api()
    const request: RoomCreateRequest = { kind: 'adhoc', agent_ids: ['mia', 'jim'], origin_session_id: MAIN_ID }
    const created = room({ kind: 'adhoc', members: ['mia', 'jim'], origin_session_id: MAIN_ID })
    net.respond('POST', `/api/v1/workspaces/${WS}/rooms`, () => json(created, 201))
    expect(await client.createRoom(WS, request)).toEqual(created)
    expect(wireCalls()).toEqual([{ method: 'POST', path: `/api/v1/workspaces/${WS}/rooms`, body: request, credentials: 'include' }])
  })

  it.each(['add', 'remove', 'mute', 'unmute'] as const)(
    '[FR-158/161/208; K24] sends %s to the membership endpoint, not agent configuration', async (action) => {
      const client = await api()
      const request: RoomMembersUpdateRequest = { action, agent_ids: ['researcher2'] }
      const updated = room({ muted: action === 'mute' ? ['researcher2'] : [] })
      net.respond('PATCH', `/api/v1/rooms/${ROOM_ID}/members`, () => json(updated))
      expect(await client.updateRoomMembers(ROOM_ID, request)).toEqual(updated)
      expect(wireCalls()).toEqual([{ method: 'PATCH', path: `/api/v1/rooms/${ROOM_ID}/members`, body: request, credentials: 'include' }])
    },
  )

  it('[FR-172; K24] deletes only the chosen room and accepts the bodyless 204', async () => {
    const client = await api()
    net.respond('DELETE', `/api/v1/rooms/${ROOM_ID}`, () => new Response(null, { status: 204 }))
    await client.deleteRoom(ROOM_ID)
    expect(wireCalls()).toEqual([{ method: 'DELETE', path: `/api/v1/rooms/${ROOM_ID}`, body: null, credentials: 'include' }])
  })

  it('[FR-166; §6.13; K26] posts stable mention ids and the client id, accepting bodyless 202', async () => {
    const client = await api()
    const request: RoomMessageRequest = { client_message_id: 'message-stable-1', text: '@Jim Smith review', mentions: ['jim'] }
    await client.postRoomMessage(ROOM_ID, request)
    expect(wireCalls()).toEqual([{ method: 'POST', path: `/api/v1/rooms/${ROOM_ID}/messages`, body: request, credentials: 'include' }])
  })

  it('[FR-204; BDD-17.20] surfaces the two-agent refusal, never a successful empty result', async () => {
    const client = await api()
    net.respond('POST', `/api/v1/workspaces/${WS}/rooms`, () => json({ error: 'a room needs at least two agents' }, 422))
    const attempt = client.createRoom(WS, { kind: 'adhoc', agent_ids: ['jim'] })
    await expect(attempt).rejects.toBeInstanceOf(ApiError)
    await expect(attempt).rejects.toMatchObject({ status: 422, userMessage: 'a room needs at least two agents' })
    expect(net.writes()).toHaveLength(1)
  })

  it('[FR-204; BDD-17.20] preserves the specific refusal when removing a member would leave one', async () => {
    const client = await api()
    net.respond('PATCH', `/api/v1/rooms/${ROOM_ID}/members`, () => json({ error: 'a room needs at least two agents — delete the room instead' }, 422))
    const attempt = client.updateRoomMembers(ROOM_ID, { action: 'remove', agent_ids: ['jim'] })
    await expect(attempt).rejects.toBeInstanceOf(ApiError)
    await expect(attempt).rejects.toMatchObject({ status: 422, userMessage: 'a room needs at least two agents — delete the room instead' })
  })

  it('[§6.3; FR-174] a failed list rejects with its HTTP status instead of returning no rooms', async () => {
    const client = await api()
    net.respond('GET', `/api/v1/workspaces/${WS}/rooms`, () => json({ error: 'storage failure' }, 500))
    const attempt = client.fetchWorkspaceRooms(WS)
    await expect(attempt).rejects.toBeInstanceOf(ApiError)
    await expect(attempt).rejects.toMatchObject({ status: 500 })
  })

  it('[§6.3; K26] a failed message write rejects; HTTP acceptance is not fabricated', async () => {
    const client = await api()
    net.respond('POST', `/api/v1/rooms/${ROOM_ID}/messages`, () => json({ error: 'room unavailable' }, 404))
    const attempt = client.postRoomMessage(ROOM_ID, { client_message_id: 'stable-failed-1', text: 'status?', mentions: [] })
    await expect(attempt).rejects.toBeInstanceOf(ApiError)
    await expect(attempt).rejects.toMatchObject({ status: 404, userMessage: 'room unavailable' })
    expect(net.writes()).toHaveLength(1)
  })

  it('[FR-162; K23] cache keys isolate workspace lists and room details', async () => {
    const client = await api()
    expect(client.roomsQueryKeys.list('sales')).not.toEqual(client.roomsQueryKeys.list('marketing'))
    expect(client.roomsQueryKeys.detail('launch')).not.toEqual(client.roomsQueryKeys.detail('other-room'))
    expect(client.roomsQueryKeys.list('launch')).not.toEqual(client.roomsQueryKeys.detail('launch'))
    expect(net.requests).toEqual([])
  })
})
