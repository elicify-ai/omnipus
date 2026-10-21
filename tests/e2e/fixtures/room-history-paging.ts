import type { APIRequestContext, Page, Request } from '@playwright/test'
import type { components } from '../../../src/lib/api/generated/openapi-types'

type Room = components['schemas']['Room']
type RoomEntry = components['schemas']['RoomEntry']
type RoomHistory = components['schemas']['RoomHistoryResponse']

export const PAGING_ENTRY_COUNT = 10_005
export const PAGING_ROOM_NAME = 'Paging-history-replay'
export const PAGING_ANCHOR_TEXT = 'pager-message-20'
export const PAGING_OLDEST_TEXT = 'pager-message-1'
export const PAGING_NEWEST_TEXT = `pager-message-${PAGING_ENTRY_COUNT}`

async function body<T>(response: Awaited<ReturnType<APIRequestContext['get']>>, operation: string): Promise<T> {
  if (!response.ok()) throw new Error(`${operation} failed (${response.status()}): ${await response.text()}`)
  return await response.json() as T
}

/** Existing REST endpoints only. Both base and recovery identities are eligible
 * for reuse; replacement Playwright workers reconstruct this from the server. */
export async function ensurePagingRoom(
  request: APIRequestContext,
  workspaceId: string,
  headers: Record<string, string>,
): Promise<{ roomId: string; anchor: RoomEntry; oldest: RoomEntry; newest: RoomEntry; seeded: number; seedMs: number }> {
  const started = Date.now()
  const rooms = await body<Room[]>(await request.get(`/api/v1/workspaces/${workspaceId}/rooms`, { headers }), 'list paging rooms')
  const candidates = rooms.filter((r) => r.name === PAGING_ROOM_NAME || r.name.startsWith(`${PAGING_ROOM_NAME}-`))
  let complete: Room | undefined
  for (const room of candidates) {
    const latest = await body<RoomHistory>(await request.get(`/api/v1/rooms/${room.id}/messages?limit=100`, { headers }), 'probe paging seed')
    if (latest.entries.some((entry) => entry.client_message_id === `seed-${PAGING_ENTRY_COUNT}` && entry.text === PAGING_NEWEST_TEXT)) {
      complete = room
      break
    }
  }
  let room = complete
  let seeded = 0
  if (!room) {
    room = await body<Room>(await request.post(`/api/v1/workspaces/${workspaceId}/rooms`, {
      headers,
      data: { name: candidates.length ? `${PAGING_ROOM_NAME}-${Date.now()}` : PAGING_ROOM_NAME, kind: 'adhoc', agent_ids: ['mia', 'jim'] },
    }), 'create paging room')
    // Pause through the existing room command BEFORE seeding. Paging needs
    // retained messages, not 20,010 agent requests/turns caused by broadcast
    // seeds. Paused rooms still record incoming/live messages (FR-170).
    const paused = await request.post(`/api/v1/rooms/${room.id}/messages`, {
      headers, data: { client_message_id: 'paging-pause', text: '/pause', mentions: [] },
    })
    if (!paused.ok()) throw new Error(`pause paging room failed (${paused.status()}): ${await paused.text()}`)
    for (let i = 1; i <= PAGING_ENTRY_COUNT; i++) {
      const response = await request.post(`/api/v1/rooms/${room.id}/messages`, {
        headers, data: { client_message_id: `seed-${i}`, text: `pager-message-${i}`, mentions: [] },
      })
      if (!response.ok()) throw new Error(`seed ${i} failed (${response.status()}): ${await response.text()}`)
      seeded++
    }
  }
  // Read retained history without joining the WebSocket/store or changing the
  // browser's displayed window. This also verifies first/anchor/last seed IDs.
  const newestPage = await body<RoomHistory>(await request.get(`/api/v1/rooms/${room.id}/messages?limit=100`, { headers }), 'read newest seed')
  const newest = newestPage.entries.find((entry) => entry.client_message_id === `seed-${PAGING_ENTRY_COUNT}` && entry.text === PAGING_NEWEST_TEXT)
  if (!newest) throw new Error('complete paging fixture is missing its newest seed')
  // Sequence positions may have gaps (older fixtures also had agent replies).
  // Walk existing REST pages to recover IDs instead of inventing arithmetic
  // cursors. This does not populate the browser's displayed room store.
  let page = newestPage
  let anchor: RoomEntry | undefined
  let oldest: RoomEntry | undefined
  let lastBoundary = Number.POSITIVE_INFINITY
  for (;;) {
    anchor ??= page.entries.find((entry) => entry.client_message_id === 'seed-20' && entry.text === PAGING_ANCHOR_TEXT)
    oldest ??= page.entries.find((entry) => entry.client_message_id === 'seed-1' && entry.text === PAGING_OLDEST_TEXT)
    if (anchor && oldest) break
    const boundary = page.entries[0]?.seq
    if (!page.has_older || boundary === undefined || boundary >= lastBoundary) {
      throw new Error('complete paging fixture is missing its oldest/anchor seed, or history made no backward progress')
    }
    lastBoundary = boundary
    page = await body<RoomHistory>(await request.get(`/api/v1/rooms/${room.id}/messages?before_seq=${boundary}&limit=1000`, { headers }), 'read anchor seeds')
  }
  return { roomId: room.id, anchor, oldest, newest, seeded, seedMs: Date.now() - started }
}

/** Arm before invoking the scroll: Page.waitForRequest does not replay events. */
export async function waitForOlderRequest(page: Page, roomId: string, scroll: () => Promise<void>): Promise<Request> {
  const pending = page.waitForRequest((request) => {
    const url = new URL(request.url())
    return request.method() === 'GET' && url.pathname === `/api/v1/rooms/${roomId}/messages` && url.searchParams.has('before_seq')
  }, { timeout: 30_000 })
  await scroll()
  return await pending
}

/** Server entry identity, never substring text (20 also matches 2000, etc.). */
export function pagingEntrySelector(entry: Pick<RoomEntry, 'id'>): string {
  return `[data-room-entry-id=${JSON.stringify(entry.id)}]`
}

export function pagingEntry(page: Page, entry: Pick<RoomEntry, 'id'>) {
  return page.getByRole('log', { name: 'Group conversation' }).locator(pagingEntrySelector(entry))
}
