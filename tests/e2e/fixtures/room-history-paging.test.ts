import { afterEach, describe, expect, it, vi } from 'vitest'
import type { APIRequestContext, APIResponse, Locator, Page, Request } from '@playwright/test'
import type { components } from '../../../src/lib/api/generated/openapi-types'
import { ensurePagingRoom, PAGING_ENTRY_COUNT, PAGING_ROOM_NAME, pagingEntrySelector, scrollHistoryToTop, waitForOlderRequest } from './room-history-paging'

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

  it('replaces a complete room after a live probe so every retry starts with exactly 10,005 seeds', async () => {
    const live: Entry = { ...entry(PAGING_ENTRY_COUNT + 1, 'used'), client_message_id: 'live-probe', text: 'pager-message-live-proof' }
    const get = vi.fn(async (path: string) => {
      if (path === '/api/v1/workspaces/sales/rooms') return response([room('used', PAGING_ROOM_NAME)])
      if (path === '/api/v1/rooms/used/messages?limit=100') return response({ entries: [entry(PAGING_ENTRY_COUNT, 'used'), live], has_older: true })
      if (path.includes('/api/v1/rooms/used/messages?before_seq=')) return response({ entries: [entry(1, 'used'), entry(20, 'used')], has_older: false })
      if (path === '/api/v1/rooms/fresh/messages?limit=100') return response({ entries: [entry(PAGING_ENTRY_COUNT, 'fresh')], has_older: true })
      if (path.includes('/api/v1/rooms/fresh/messages?before_seq=')) return response({ entries: [entry(1, 'fresh'), entry(20, 'fresh')], has_older: false })
      throw new Error(`unexpected fixture request: ${path}`)
    })
    let paused = false
    let seeds = 0
    const post = vi.fn(async (path: string, options: { data: Record<string, unknown> }) => {
      if (path === '/api/v1/workspaces/sales/rooms') return response(room('fresh', `${PAGING_ROOM_NAME}-fresh`))
      expect(path, 'retry must not append to the contaminated room').toBe('/api/v1/rooms/fresh/messages')
      if (options.data.text === '/pause') {
        expect(options.data).toEqual({ client_message_id: 'paging-pause', text: '/pause', mentions: [] })
        paused = true
      } else {
        expect(paused, 'the replacement room is paused before broadcasts').toBe(true)
        seeds++
        expect(options.data).toEqual({ client_message_id: `seed-${seeds}`, text: `pager-message-${seeds}`, mentions: [] })
      }
      return response(null)
    })
    const fixture = await ensurePagingRoom({ get, post } as unknown as APIRequestContext, 'sales', {})
    expect(fixture.roomId, 'a live probe disqualifies the formerly complete room').toBe('fresh')
    expect(fixture.seeded).toBe(PAGING_ENTRY_COUNT)
    expect(seeds).toBe(PAGING_ENTRY_COUNT)
    expect(fixture.anchor).toEqual(entry(20, 'fresh'))
    expect(fixture.oldest).toEqual(entry(1, 'fresh'))
    expect(fixture.newest).toEqual(entry(PAGING_ENTRY_COUNT, 'fresh'))
    expect(post).toHaveBeenCalledTimes(PAGING_ENTRY_COUNT + 2)
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

// R3 dispatch: wait for genuine native Home completion before the one PageDown;
// never manufacture scrollTop0 to hide an unfinished keyboard animation. The
// helper is real; only browser transport and animation-frame scheduling are doubles.
function navigationFixture(top: number) {
  const element = document.createElement('div')
  element.tabIndex = -1
  element.scrollTop = top
  document.body.append(element)
  const frames: FrameRequestCallback[] = []
  const animationFrame = vi.fn((callback: FrameRequestCallback) => frames.push(callback))
  vi.stubGlobal('requestAnimationFrame', animationFrame)
  const keys: string[] = []
  const focus = vi.fn(async () => { element.focus() })
  const press = vi.fn(async (key: string) => { keys.push(key) })
  let evaluated!: () => void
  const evaluating = new Promise<void>((resolve) => { evaluated = resolve })
  const evaluate = vi.fn(async (action: (el: HTMLElement) => unknown) => {
    const result = action(element)
    evaluated()
    return await result
  })
  let syntheticScrolls = 0
  element.addEventListener('scroll', () => { syntheticScrolls++ })
  return {
    element, frames, animationFrame, keys, focus, press, evaluate, evaluating,
    syntheticScrolls: () => syntheticScrolls,
    history: { focus, evaluate } as unknown as Locator,
    page: { keyboard: { press } } as unknown as Page,
  }
}

describe('native history navigation ordering', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    document.body.replaceChildren()
  })

  it.each([1, 1_000])('waits for native Home from %ipx without forcing zero or issuing PageDown early', async (top) => {
    const fixture = navigationFixture(top)
    let ready = false
    const reading = scrollHistoryToTop(fixture.page, fixture.history).then(async () => {
      ready = true
      await fixture.page.keyboard.press('PageDown')
    })
    await fixture.evaluating
    try {
      expect(fixture.element.scrollTop, 'the helper must not fake native Home completion').toBe(top)
      expect(ready, 'a resolved key injection is not a completed scroll').toBe(false)
      expect(fixture.keys).toEqual(['Home'])
      expect(fixture.frames).toHaveLength(1)
      // Even a final fractional pixel is not the native Home destination.
      fixture.element.scrollTop = top / 2
      fixture.frames.shift()!(0)
      await Promise.resolve()
      expect(ready).toBe(false)
      expect(fixture.keys).toEqual(['Home'])
      expect(fixture.frames).toHaveLength(1)
      fixture.element.scrollTop = 0 // browser animation reaches its real target
      fixture.frames.shift()!(0)
      await reading
      expect(fixture.keys).toEqual(['Home', 'PageDown'])
      expect(fixture.focus).toHaveBeenCalledExactlyOnceWith()
      expect(fixture.evaluate).toHaveBeenCalledTimes(1)
      expect(fixture.syntheticScrolls()).toBe(0)
    } finally {
      fixture.element.scrollTop = 0
      for (const callback of fixture.frames.splice(0)) callback(0)
      await reading
    }
  })

  it('keeps genuine Home input when already at the top without scheduling a fake scroll', async () => {
    const fixture = navigationFixture(0)
    await scrollHistoryToTop(fixture.page, fixture.history)
    expect(fixture.element.scrollTop).toBe(0)
    expect(fixture.keys).toEqual(['Home'])
    expect(fixture.focus).toHaveBeenCalledExactlyOnceWith()
    expect(fixture.animationFrame).not.toHaveBeenCalled()
    expect(fixture.syntheticScrolls()).toBe(0)
  })

  it('propagates a failed focus without sending Home or pretending navigation succeeded', async () => {
    const fixture = navigationFixture(1)
    const failure = new Error('history viewport unavailable')
    fixture.focus.mockRejectedValueOnce(failure)
    await expect(scrollHistoryToTop(fixture.page, fixture.history)).rejects.toBe(failure)
    expect(fixture.press).not.toHaveBeenCalled()
    expect(fixture.evaluate).not.toHaveBeenCalled()
  })

  it('propagates a failed Home injection without evaluating or forcing the scroll position', async () => {
    const fixture = navigationFixture(1)
    const failure = new Error('native Home injection failed')
    fixture.press.mockRejectedValueOnce(failure)
    await expect(scrollHistoryToTop(fixture.page, fixture.history)).rejects.toBe(failure)
    expect(fixture.press).toHaveBeenCalledExactlyOnceWith('Home')
    expect(fixture.evaluate).not.toHaveBeenCalled()
    expect(fixture.element.scrollTop).toBe(1)
  })
})
