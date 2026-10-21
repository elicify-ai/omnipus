import { describe, expect, it, vi } from 'vitest'
import type { APIRequestContext, APIResponse, Page, Request } from '@playwright/test'
import type { components } from '../../../src/lib/api/generated/openapi-types'
import { ensurePagingRoom, PAGING_ENTRY_COUNT, PAGING_ROOM_NAME, pagingEntrySelector, waitForOlderRequest } from './room-history-paging'

type Entry = components['schemas']['RoomEntry']
type Room = components['schemas']['Room']

function entry(index: number, roomId = 'recovery'): Entry {
  return {
    id: `entry-${index}`, room_id: roomId, seq: index + 2,
    text: `pager-message-${index}`, client_message_id: `seed-${index}`,
    author: { kind: 'user' }, addressees: [], after_pause: false, deliveries: [], timestamp: '2026-10-20T00:00:00Z',
  }
}

function room(id: string, name: string): Room {
  return {
    id, name, workspace_id: 'sales', kind: 'adhoc', members: ['mia', 'jim'], muted: [], paused: true,
    hidden: false, generation: 1, allocated_cost_micro_usd: 0, allocated_cost_unpriced: false, needs_attention: false,
  }
}

function response(value: unknown): APIResponse {
  return { ok: () => true, status: () => 200, json: async () => value } as APIResponse
}

describe('round-3 paging fixture regressions', () => {
  it('selects exactly the server entry despite 102 ambiguous text matches', () => {
    document.body.innerHTML = '<div role="log" aria-label="Group conversation">'
      + Array.from({ length: 102 }, (_, i) => `<article data-room-entry-id="entry-${i}"><p>pager-message-20${i}</p></article>`).join('')
      + '</div>'
    const matches = document.querySelectorAll(pagingEntrySelector({ id: 'entry-20' }))
    expect(matches).toHaveLength(1)
    expect(matches[0].getAttribute('data-room-entry-id')).toBe('entry-20')
    document.body.replaceChildren()
  })
  it('reuses a complete suffixed recovery room after a partial base, with zero create/seed POSTs', async () => {
    const get = vi.fn(async (path: string) => {
      if (path === '/api/v1/workspaces/sales/rooms') return response([
        room('partial', PAGING_ROOM_NAME), room('recovery', `${PAGING_ROOM_NAME}-previous-worker`),
      ])
      if (path === '/api/v1/rooms/partial/messages?limit=100') return response({ entries: [entry(45, 'partial')], has_older: false })
      if (path === '/api/v1/rooms/recovery/messages?limit=100') return response({ entries: [entry(PAGING_ENTRY_COUNT)], has_older: true, next_before_seq: PAGING_ENTRY_COUNT + 2 })
      if (path.includes('/api/v1/rooms/recovery/messages?before_seq=')) return response({ entries: [entry(1), entry(20)], has_older: false })
      throw new Error(`unexpected fixture request: ${path}`)
    })
    const post = vi.fn(async () => { throw new Error('a complete recovery fixture must never create/re-seed') })
    const request = { get, post } as unknown as APIRequestContext
    const fixture = await ensurePagingRoom(request, 'sales', {})
    expect(fixture.roomId).toBe('recovery')
    expect(fixture.seeded).toBe(0)
    expect(fixture.anchor).toEqual(entry(20))
    expect(fixture.oldest).toEqual(entry(1))
    expect(fixture.newest).toEqual(entry(PAGING_ENTRY_COUNT))
    expect(post).not.toHaveBeenCalled()
    expect(get.mock.calls.map(([path]) => path)).toContain('/api/v1/rooms/recovery/messages?limit=100')
  })

  it('pauses a cold fixture before all 10,005 seed broadcasts and preserves ordered IDs', async () => {
    let paused = false
    let seeds = 0
    const get = vi.fn(async (path: string) => {
      if (path.endsWith('/rooms')) return response([])
      if (path.endsWith('?limit=100')) return response({ entries: [entry(PAGING_ENTRY_COUNT)], has_older: true, next_before_seq: PAGING_ENTRY_COUNT + 2 })
      return response({ entries: [entry(1), entry(20)], has_older: false })
    })
    const post = vi.fn(async (path: string, options: { data: Record<string, unknown> }) => {
      if (path.endsWith('/rooms')) return response(room('recovery', PAGING_ROOM_NAME))
      if (options.data.text === '/pause') {
        paused = true
      } else {
        expect(paused, 'seed broadcasts must not launch model turns').toBe(true)
        seeds++
        expect(options.data).toEqual({ client_message_id: `seed-${seeds}`, text: `pager-message-${seeds}`, mentions: [] })
      }
      return response(null)
    })
    const fixture = await ensurePagingRoom({ get, post } as unknown as APIRequestContext, 'sales', {})
    expect(fixture.seeded).toBe(PAGING_ENTRY_COUNT)
    expect(seeds).toBe(PAGING_ENTRY_COUNT)
    expect(post).toHaveBeenCalledTimes(PAGING_ENTRY_COUNT + 2) // create + pause + seed
  })

  it('never treats an incomplete recovery seed as complete', async () => {
    const get = vi.fn(async (path: string) => path.endsWith('/rooms')
      ? response([room('partial', `${PAGING_ROOM_NAME}-partial`)])
      : response({ entries: [entry(100, 'partial')], has_older: false }))
    const post = vi.fn(async () => { throw new Error('CREATE-REQUIRED') })
    await expect(ensurePagingRoom({ get, post } as unknown as APIRequestContext, 'sales', {})).rejects.toThrow('CREATE-REQUIRED')
    expect(post).toHaveBeenCalledExactlyOnceWith('/api/v1/workspaces/sales/rooms', {
      headers: {}, data: expect.objectContaining({ kind: 'adhoc', agent_ids: ['mia', 'jim'] }),
    })
  })

  it('captures a request emitted before the scrolling action resolves', async () => {
    let receive: ((request: Request) => void) | undefined
    const wanted = { method: () => 'GET', url: () => 'https://fixture.example/api/v1/rooms/r/messages?before_seq=6' } as Request
    const waitForRequest = vi.fn((predicate: (request: Request) => boolean) => new Promise<Request>((resolve) => {
      receive = (request) => { if (predicate(request)) resolve(request) }
    }))
    const page = { waitForRequest } as unknown as Page
    const action = vi.fn(async () => {
      expect(receive, 'the event listener is installed before the scroll can emit').toBeTypeOf('function')
      receive!(wanted)
      await Promise.resolve() // event is earlier than the action's completion
    })
    await expect(waitForOlderRequest(page, 'r', action)).resolves.toBe(wanted)
    expect(action).toHaveBeenCalledTimes(1)
    expect(waitForRequest).toHaveBeenCalledTimes(1)
  })
})
