// T15/T33, B02/B23/B31/B51. Only fetch is doubled: real HTTP, generated
// validation, per-item degradation and adapters run. Oracles: D3/D4a/D10/D15.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { SessionHistoryResponse } from '@/lib/api/generated/openapi-types'
import { fetchSessionDetail, fetchSessionMessages } from './sessions'
import { ApiSchemaError } from './http'
import {
  SID, availableDetail, entry, expectValidDetail, expectValidPage, page, pos, unavailableDetail,
} from '../../../tests/chat-history-paging/fixtures'

let body: unknown
let calls: string[]
beforeEach(() => {
  calls = []
  vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
    calls.push(String(input))
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json', 'X-Has-Earlier-Messages': 'false' } })
  })
})
afterEach(() => { vi.unstubAllGlobals(); sessionStorage.clear() })

describe('T33 paged history adapter — B02/B23/B51', () => {
  it.each([0, 1, 19, 20, 21, 39, 40, 41, 99, 100, 101])('consumes the newest bounded suffix for %i narration groups', async (count) => {
    // B02 frozen arithmetic: default request=40, select newest min(count,40).
    const returned = Math.min(count, 40)
    const first = count - returned + 1
    const entries = Array.from({ length: returned }, (_, i) => entry(first + i))
    const fixture = page(entries, { earlier: count > 40 })
    expectValidPage(fixture, entries.map((item) => item.history_position!), returned)
    body = fixture
    const actual: unknown = await fetchSessionMessages(SID)
    expect(actual, 'D15 adapter must return bounded entries AND atomic page metadata, never the retired bare array').toMatchObject({
      entries: entries.map(({ id, content, history_position }) => ({ id, content, historyPosition: history_position })),
      has_earlier: count > 40, has_later: false, page_info: fixture.page_info,
      ...(count > 40 ? { next_before: pos(first) } : {}),
    })
    expect(calls, 'one bounded read, no lifetime enumeration on open').toStrictEqual([`/api/v1/sessions/${SID}/messages`])
  })

  it.each([2097152, 4194304, 8388608])('accepts a consistent advertised positive soft budget %i', async (budget) => {
    const fixture = page([entry(1)], { info: { soft_byte_budget: budget } })
    expectValidPage(fixture, [pos(1)], 1)
    body = fixture
    expect(await fetchSessionMessages(SID), 'D-B5 budget is a positive integer, not a one-value enum')
      .toMatchObject({ page_info: { soft_byte_budget: budget, byte_budget_exceeded: false }, entries: [{ id: 'history-1', historyPosition: pos(1) }] })
  })

  it('retains a visible position-stable placeholder for one malformed item without rejecting its valid neighbors', async () => {
    const fixture = page([entry(1), entry(2), entry(3)])
    expectValidPage(fixture, [pos(1), pos(2), pos(3)], 3)
    // D4 per-item corruption, NOT a page-metadata contradiction. Preserve its
    // valid source identity/position so neither reload nor prepend re-keys it.
    body = { ...fixture, entries: [entry(1), { ...entry(2), role: 'not-a-role' }, entry(3)] }
    const first = await fetchSessionMessages(SID)
    const second = await fetchSessionMessages(SID)
    expect(first).toMatchObject({ entries: [
      { id: 'history-1', content: 'Saved row 1', historyPosition: pos(1) },
      { id: 'history-2', role: 'system', content: 'This message could not be displayed.', historyPosition: pos(2) },
      { id: 'history-3', content: 'Saved row 3', historyPosition: pos(3) },
    ] })
    expect(second, 'same occurrence produces the same placeholder identity').toStrictEqual(first)
  })

  it('malformed sources without recorded IDs keep distinct position-derived placeholders when retention changes array indexes', async () => {
    const good = page([entry(1), entry(2), entry(3)])
    expectValidPage(good, [pos(1), pos(2), pos(3)], 3)
    const bad2 = { ...entry(2), id: undefined, role: 'not-a-role' }
    const bad3 = { ...entry(3), id: undefined, role: 'not-a-role' }
    body = { ...good, entries: [entry(1), bad2, bad3] }
    const first: unknown = await fetchSessionMessages(SID)
    expect(first).toMatchObject({ entries: [
      { id: 'history-1', historyPosition: pos(1) },
      { role: 'system', content: 'This message could not be displayed.', historyPosition: pos(2) },
      { role: 'system', content: 'This message could not be displayed.', historyPosition: pos(3) },
    ] })
    const next = page([entry(2), entry(3)], { info: { retention_generation: 'fixture-retained-2' } })
    expectValidPage(next, [pos(2), pos(3)], 2)
    body = { ...next, entries: [bad2, bad3] }
    const second: unknown = await fetchSessionMessages(SID)
    expect(second).toMatchObject({ entries: [
      { role: 'system', content: 'This message could not be displayed.', historyPosition: pos(2) },
      { role: 'system', content: 'This message could not be displayed.', historyPosition: pos(3) },
    ] })
    // not-wire-format: observation of normalized placeholder keys, not a wire declaration.
    const firstRows = Reflect.get(first as object, 'entries') as { id: string }[]
    const nextRows = Reflect.get(second as object, 'entries') as { id: string }[]
    expect(firstRows[1].id).toBeTypeOf('string'); expect(firstRows[1].id).not.toBe('')
    expect(firstRows[2].id).toBeTypeOf('string'); expect(firstRows[2].id).not.toBe('')
    expect(firstRows[1].id, 'different source positions cannot share one placeholder key').not.toBe(firstRows[2].id)
    expect(nextRows.map((row) => row.id), 'position identity survives index1→0 and2→1').toStrictEqual([firstRows[1].id, firstRows[2].id])
  })

  it('rejects a retired raw-array response rather than silently accepting full history', async () => {
    body = [entry(1)]
    await expect(fetchSessionMessages(SID)).rejects.toBeInstanceOf(ApiSchemaError)
  })
})

// The good control is independently valid. Each case changes ONE semantic fact;
// schema shape is still valid for the count/continuation mutants from CHECK.
const contradictions: { name: string; path: string[]; change: (value: SessionHistoryResponse) => void }[] = [
  { name: 'zero source groups for one selected entry', path: ['page_info', 'source_group_count'], change: (v) => { v.page_info.source_group_count = 0 } },
  { name: 'two source groups for one selected entry', path: ['page_info', 'source_group_count'], change: (v) => { v.page_info.source_group_count = 2 } },
  { name: 'zero drawn rows for one narration entry', path: ['page_info', 'row_count'], change: (v) => { v.page_info.row_count = 0 } },
  { name: 'earlier flag without cursor', path: ['next_before'], change: (v) => { v.has_earlier = true } },
  { name: 'false earlier flag with cursor', path: ['next_before'], change: (v) => { v.next_before = pos(1) } },
  { name: 'later flag without cursor', path: ['next_after'], change: (v) => { v.has_later = true } },
  { name: 'false later flag with cursor', path: ['next_after'], change: (v) => { v.next_after = pos(1) } },
  { name: 'wrong first position', path: ['page_info', 'first_position'], change: (v) => { v.page_info.first_position = pos(9) } },
  { name: 'wrong last position', path: ['page_info', 'last_position'], change: (v) => { v.page_info.last_position = pos(9) } },
  { name: 'floor larger than limit', path: ['page_info', 'min_rows'], change: (v) => { v.page_info.min_rows = 41 } },
  { name: 'byte-budget stop before two-screen floor', path: ['page_info', 'stop_reason'], change: (v) => { v.page_info.stop_reason = 'byte_budget' } },
  { name: 'false exceeded bit despite large bytes', path: ['page_info', 'byte_budget_exceeded'], change: (v) => { v.page_info.projected_bytes = 4194305 } },
  { name: 'true exceeded bit despite small bytes', path: ['page_info', 'byte_budget_exceeded'], change: (v) => { v.page_info.byte_budget_exceeded = true; v.page_info.budget_override = 'height_floor' } },
  { name: 'unneeded override', path: ['page_info', 'budget_override'], change: (v) => { v.page_info.budget_override = 'atomic_group' } },
]

describe('T15/T33 semantic metadata refusal — B23', () => {
  it.each(contradictions)('rejects $name with a field-specific protocol error', async ({ path, change }) => {
    const good = page([entry(1)])
    expectValidPage(good, [pos(1)], 1)
    body = good
    // Required positive control: this test cannot pass just because the old
    // adapter rejects EVERY envelope at its obsolete array-shape check.
    await expect(fetchSessionMessages(SID)).resolves.toMatchObject({ entries: [{ id: 'history-1' }] })
    const bad = structuredClone(good)
    change(bad)
    body = bad
    await expect(fetchSessionMessages(SID)).rejects.toMatchObject({
      name: 'ApiSchemaError', zodIssues: expect.arrayContaining([expect.objectContaining({ path })]),
    })
  })

  it('rejects a wrong addressed session without accepting any rows', async () => {
    body = page([entry(1)], { sessionId: 'another-session' })
    await expect(fetchSessionMessages(SID)).rejects.toMatchObject({
      name: 'ApiSchemaError', zodIssues: expect.arrayContaining([expect.objectContaining({ path: ['session_id'] })]),
    })
  })

  it.each([0, -1, null, 1.5])('rejects invalid wire budget %s after a valid control', async (budget) => {
    body = page([entry(1)])
    await expect(fetchSessionMessages(SID)).resolves.toMatchObject({ entries: [{ id: 'history-1' }] })
    body = { ...body as SessionHistoryResponse, page_info: { ...(body as SessionHistoryResponse).page_info, soft_byte_budget: budget } }
    await expect(fetchSessionMessages(SID)).rejects.toBeInstanceOf(ApiSchemaError)
  })
})

describe('T33 explicit detail availability — B31/B51', () => {
  it('metadata-only unavailable detail preserves routeable identity and never manufactures messages', async () => {
    const fixture = unavailableDetail()
    expectValidDetail(fixture, [], 0)
    body = fixture
    const detail: unknown = await fetchSessionDetail(SID)
    expect(detail).toMatchObject({ session: { id: SID, workspace_id: 'sales' }, history_state: fixture.history_state })
    for (const key of ['messages', 'page_info', 'has_earlier', 'has_later', 'next_before', 'next_after']) {
      expect(Object.prototype.hasOwnProperty.call(detail, key), `D10 unavailable detail must OMIT ${key}`).toBe(false)
    }
  })

  it('available detail preserves the exact bounded page metadata', async () => {
    const history = page([entry(1, { type: 'system', role: 'system', content: 'Conversation context cleared' }), entry(2)], {
      earlier: true, info: { stopped_at_clear: true, stop_reason: 'clear', latest_clear: { entry_id: 'history-1', history_position: pos(1) } },
    })
    const fixture = availableDetail(history)
    expectValidDetail(fixture, [pos(1), pos(2)], 2)
    body = fixture
    expect(await fetchSessionDetail(SID)).toMatchObject({
      history_state: { status: 'available' }, has_earlier: true, has_later: false,
      next_before: pos(1), page_info: history.page_info,
      messages: [{ id: 'history-1', historyPosition: pos(1) }, { id: 'history-2', historyPosition: pos(2) }],
    })
  })

  it('available detail without page_info is rejected, not accepted as empty history', async () => {
    body = { session: unavailableDetail().session, history_state: { status: 'available' }, messages: [], has_earlier: false, has_later: false }
    await expect(fetchSessionDetail(SID)).rejects.toMatchObject({ name: 'ApiSchemaError' })
  })

  it('unavailable detail carrying messages is rejected rather than hiding the index failure', async () => {
    body = { ...unavailableDetail(), messages: [] }
    await expect(fetchSessionDetail(SID)).rejects.toBeInstanceOf(ApiSchemaError)
  })
})
