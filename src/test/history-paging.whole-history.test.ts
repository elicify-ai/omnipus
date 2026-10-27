// T33 / B02/B23/B51, D4/D10/D15/D-C1. The ACTUAL RED-E2E-owned helper
// (9c602f0d3) runs with generated parsing; only the HTTP process edge is fake.
// Expected ordered rows, two explicit clear crossings and hidden-old canary
// come from the spec. Exact helper error text is characterization of that
// extracted TEST helper, not a new user-facing product-copy requirement.
import { describe, expect, it, vi } from 'vitest'
import { ZodError } from 'zod'
import {
  assertSessionHistoryExcludes, sessionMessages,
} from '../../tests/e2e/fixtures/session-history'
import type { SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import { entry, expectValidPage, page, pos, SID } from '../../tests/chat-history-paging/fixtures'

const ORIGIN = 'https://history.test'
function reply(body: unknown, status = 200) {
  return { ok: () => status >= 200 && status < 300, status: () => status,
    text: async () => JSON.stringify(body), json: async () => structuredClone(body) }
}
function readerFor(bodies: unknown[]) {
  const urls: string[] = []
  const get = vi.fn(async (url: string) => {
    urls.push(url)
    if (urls.length > bodies.length) throw new Error('Fixture instrument: unexpected extra history request')
    return reply(bodies[urls.length - 1])
  })
  return { reader: { request: { get } }, urls }
}
function segments(canary = 'Oldest saved user row') {
  const sources = Array.from({ length: 9 }, (_, i) => entry(i + 1))
  sources[0] = entry(1, { content: canary })
  sources[2] = entry(3, { type: 'system', role: 'system', content: 'Conversation context cleared' })
  sources[6] = entry(7, { type: 'system', role: 'system', content: 'Conversation context cleared' })
  const latestClear = { entry_id: 'history-7', history_position: pos(7) }
  const latest = page(sources.slice(6), { earlier: true, info: {
    limit: 100, min_rows: 20, stop_reason: 'clear', stopped_at_clear: true, latest_clear: latestClear,
  } })
  const middle = page(sources.slice(2, 6), { earlier: true, later: true, info: {
    mode: 'older', limit: 100, min_rows: 20, stop_reason: 'clear', stopped_at_clear: true, latest_clear: latestClear,
  } })
  const oldest = page(sources.slice(0, 2), { later: true, info: { mode: 'older', limit: 100, min_rows: 20, latest_clear: latestClear } })
  expectValidPage(latest, [pos(7), pos(8), pos(9)], 3)
  expectValidPage(middle, [pos(3), pos(4), pos(5), pos(6)], 4)
  expectValidPage(oldest, [pos(1), pos(2)], 2)
  return { sources, pages: [latest, middle, oldest] }
}

describe('T33 whole-retained history oracle — B02/B23/B51', () => {
  it('enumerates every retained page and both clear barriers in ascending occurrence order', async () => {
    const fixture = segments()
    const network = readerFor(fixture.pages)
    expect(await sessionMessages(network.reader, ORIGIN, SID)).toStrictEqual(fixture.sources)
    expect(network.urls.map((url) => {
      const parsed = new URL(url)
      expect(parsed.origin).toBe(ORIGIN)
      expect(parsed.pathname).toBe(`/api/v1/sessions/${SID}/messages`)
      return Object.fromEntries(parsed.searchParams)
    })).toStrictEqual([
      { limit: '100', min_rows: '20' },
      { limit: '100', min_rows: '20', before: pos(7) },
      { limit: '100', min_rows: '20', before: pos(3) },
    ])
  })

  it('preserves repeated display IDs belonging to distinct source occurrences', async () => {
    const first = entry(1, { id: 'same-recorded-id' })
    const last = entry(2, { id: 'same-recorded-id' })
    const fixture = page([first, last], { info: { limit: 100, context: { spans: [], duplicate_ids: [
      { kind: 'message', id: 'same-recorded-id', canonical_position: pos(2), occurrence_count: 2 },
    ] } } })
    expectValidPage(fixture, [pos(1), pos(2)], 2)
    const network = readerFor([fixture])
    expect(await sessionMessages(network.reader, ORIGIN, SID), 'D2 keep source positions separate from display aliases').toStrictEqual([first, last])
    expect(network.urls).toHaveLength(1)
  })

  it('a true empty terminal page returns [] after exactly one validated request', async () => {
    const fixture = page([], { info: { limit: 100 } })
    expectValidPage(fixture, [], 0)
    const network = readerFor([fixture])
    expect(await sessionMessages(network.reader, ORIGIN, SID)).toStrictEqual([])
    expect(network.urls).toHaveLength(1)
  })

  it('a forbidden output in the oldest clear segment defeats the actual parent-wake absence oracle', async () => {
    const canary = 'ISOLATION-CANARY-FROM-OLDEST-SEGMENT'
    const fixture = segments(canary)
    const network = readerFor(fixture.pages)
    await expect(assertSessionHistoryExcludes(network.reader, ORIGIN, SID, canary))
      .rejects.toEqual(new Error('another session retained forbidden parent-wake output'))
    expect(network.urls).toHaveLength(3)
  })

  it('the parent-wake absence oracle returns all rows when the forbidden value is genuinely absent', async () => {
    const fixture = segments()
    const network = readerFor(fixture.pages)
    expect(await assertSessionHistoryExcludes(network.reader, ORIGIN, SID, 'ABSENT-CANARY')).toStrictEqual(fixture.sources)
    expect(network.urls).toHaveLength(3)
  })

  it('rejects the retired bare array instead of turning it into a successful empty list', async () => {
    const network = readerFor([[entry(1)]])
    await expect(sessionMessages(network.reader, ORIGIN, SID)).rejects.toBeInstanceOf(ZodError)
    expect(network.urls).toHaveLength(1)
  })

  it('preserves an HTTP failure as an explicit failure, never an empty transcript', async () => {
    const reader = { request: { get: vi.fn(async () => reply({ error: 'read failed' }, 500)) } }
    await expect(sessionMessages(reader, ORIGIN, SID)).rejects.toEqual(new Error('GET session history failed: 500 {"error":"read failed"}'))
    expect(reader.request.get.mock.calls).toStrictEqual([[`${ORIGIN}/api/v1/sessions/${SID}/messages?limit=100&min_rows=20`]])
  })
})

const badPages: { name: string; message: string; mutate: (body: SessionHistoryResponse) => void }[] = [
  { name: 'wrong addressed session', message: 'history enumeration received the wrong session', mutate: (v) => { v.session_id = 'foreign-session' } },
  { name: 'wrong mode', message: 'history enumeration received the wrong page mode', mutate: (v) => { v.page_info.mode = 'older' } },
  { name: 'wrong source count', message: 'history page has contradictory counts', mutate: (v) => { v.page_info.source_group_count = 0 } },
  { name: 'zero rows for a narration source', message: 'history page has contradictory counts', mutate: (v) => { v.page_info.row_count = 0 } },
  { name: 'two rows for one narration source', message: 'history page has contradictory counts', mutate: (v) => { v.page_info.row_count = 2 } },
  { name: 'floor greater than limit', message: 'history page has contradictory counts', mutate: (v) => { v.page_info.limit = 20; v.page_info.min_rows = 21 } },
  { name: 'earlier without cursor', message: 'history page has contradictory continuation', mutate: (v) => { v.has_earlier = true } },
  { name: 'cursor with false earlier flag', message: 'history page has contradictory continuation', mutate: (v) => { v.next_before = pos(1) } },
  { name: 'later without cursor', message: 'history page has contradictory continuation', mutate: (v) => { v.has_later = true } },
  { name: 'byte stop below floor', message: 'history page byte budget cut below the height floor', mutate: (v) => { v.page_info.stop_reason = 'byte_budget' } },
  { name: 'false byte exceeded bit', message: 'history page has contradictory byte accounting', mutate: (v) => { v.page_info.projected_bytes = 4194305 } },
  { name: 'wrong first endpoint', message: 'history page endpoints contradict its source entries', mutate: (v) => { v.page_info.first_position = pos(9) } },
]

describe('T33 helper semantic refusal, with a valid independent control — B23', () => {
  it.each(badPages)('rejects $name rather than allowing an absence proof', async ({ message, mutate }) => {
    const good = page([entry(1)], { info: { limit: 100 } })
    expectValidPage(good, [pos(1)], 1)
    const control = readerFor([good])
    expect(await sessionMessages(control.reader, ORIGIN, SID)).toStrictEqual([entry(1)])
    const bad = structuredClone(good)
    mutate(bad)
    const network = readerFor([bad])
    await expect(sessionMessages(network.reader, ORIGIN, SID)).rejects.toEqual(new Error(message))
    expect(network.urls).toHaveLength(1)
  })

  it('overlapping exclusive pages fail before duplicate content can support an all-history claim', async () => {
    const fixture = segments()
    const overlap = page([entry(7)], { later: true, info: { mode: 'older', limit: 100 } })
    expectValidPage(overlap, [pos(7)], 1)
    const network = readerFor([fixture.pages[0], overlap])
    await expect(sessionMessages(network.reader, ORIGIN, SID)).rejects.toEqual(new Error('exclusive history pages overlap an already-read occurrence'))
    expect(network.urls).toHaveLength(2)
  })
})
