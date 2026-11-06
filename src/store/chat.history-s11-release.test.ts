// S11c / B96. Real public500-group trimming and closed-window retention
// refresh. Cache payloads are not mistaken for reader-visible ownership.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { Message } from '@/lib/api/generated/openapi-types'
import { useChatStore } from './chat'
import { reset, cleanup, response } from '../../tests/chat-history-paging/final-gate-harness'
import { page } from '../../tests/chat-history-paging/fixtures'
import { A, B, P, SID, assistantRows, begin, bucket, native, pos, prose, rest, token, tool } from '../../tests/chat-history-paging/s11-fold-harness'

beforeEach(() => { reset(); begin() })
afterEach(cleanup)
const user = (rank: number) => prose(rank, `s11-independent-${rank}`, `Question ${rank}`, { role: 'user', turn_id: undefined })
async function fillWindow(sources: Message[], direction: 'older' | 'newer') {
  expect(sources).toHaveLength(500)
  // For a newer extension, really evict the one known newer source501.
  // For an older extension,500 is the true newest end and source0 exists.
  // Each return read can then honestly terminate at source1 or500.
  const first = direction === 'newer' ? 420 : 400
  const latest = [...sources.slice(first), ...(direction === 'newer' ? [user(501)] : [])]
  await rest(latest, 'latest', true, false, latest.length)
  useChatStore.getState().enterHistoryReading(SID)
  for (let end = first; end > 0; end -= 100) {
    const selected = sources.slice(Math.max(0, end - 100), end)
    await rest(selected, 'older', end > 100 || direction === 'older', true, Math.max(20, selected.length))
  }
  expect(bucket().history.readerPositions).toEqual(sources.map(source => source.history_position))
  expect(bucket().history.hasEarlier).toBe(direction === 'older')
  expect(bucket().history.hasLater).toBe(direction === 'newer')
}

async function refreshWindow(sources: Message[]) {
  const read = vi.fn(async (input: RequestInfo | URL) => {
    const query = new URL(String(input), 'http://history.test').searchParams
    expect(query.get('window_start')).toBe(sources[0].history_position)
    expect(query.get('window_end')).toBe(sources.at(-1)!.history_position)
    const before = query.get('before')
    const upper = before ? sources.findIndex(source => source.history_position === before) : sources.length
    expect(upper, 'instrument: refresh continuation belongs to the independently specified retained window').toBeGreaterThan(0)
    const lower = Math.max(0, upper - 100)
    return response(page(sources.slice(lower, upper), { earlier: lower > 0, later: upper < sources.length, info: { mode: 'window_refresh', limit: 100, min_rows: 20, stop_reason: lower ? 'row_target' : 'range_end' } }))
  })
  vi.stubGlobal('fetch', read)
  await useChatStore.getState().refreshLoadedHistory(SID)
  expect(read).toHaveBeenCalledTimes(Math.ceil(sources.length / 100))
  expect(bucket().history.requests.refresh.status).toBe('idle')
}

it.each(['leading-tool', 'key-text', 'last-text'] as const)('T81 B96: genuine501st-group trim releases only the %s part', async removed => {
  const sources = Array.from({ length: 500 }, (_, index) => user(index + 1))
  let drop: string, keptKey: string, keptBody: string, keptCalls: string[]
  if (removed === 'leading-tool') {
    sources.splice(0, 2, tool(1), prose(2, P, 'Answer.'))
    drop = pos(1); keptKey = P; keptBody = 'Answer.'; keptCalls = []
  } else if (removed === 'key-text') {
    sources.splice(0, 3, prose(1, A, 'First.'), tool(2), prose(3, B, 'Second.'))
    drop = pos(1); keptKey = B; keptBody = 'Second.'; keptCalls = ['s11-call']
  } else {
    sources.splice(497, 3, prose(498, A, 'First.'), tool(499), prose(500, B, 'Second.'))
    drop = pos(500); keptKey = A; keptBody = 'First.'; keptCalls = ['s11-call']
  }
  await fillWindow(sources, removed === 'last-text' ? 'older' : 'newer')
  expect(assistantRows(), 'instrument: the segment is folded BEFORE testing release').toHaveLength(1)
  expect(bucket().history.readerPositions).toHaveLength(500)
  await rest([user(removed === 'last-text' ? 0 : 501)], removed === 'last-text' ? 'older' : 'newer', removed !== 'last-text', removed === 'last-text')
  expect(bucket().history.readerPositions).toHaveLength(500)
  expect(bucket().history.readerPositions).not.toContain(drop)
  expect(bucket().historySourceAliases?.[drop], 'only the dropped occurrence binding is released').toBeUndefined()
  expect(assistantRows().map(row => [row.id, row.content])).toEqual([[keptKey, keptBody]])
  const survivor = bucket().messagesById[keptKey]
  expect(survivor.historyParts?.some(part => part.position === drop)).toBe(false)
  expect(survivor.tool_calls?.map(call => call.id) ?? []).toEqual(keptCalls)
  expect(survivor.tool_calls?.map(call => Reflect.get(call, 'textOffset')) ?? []).toEqual(keptCalls.map(() => removed === 'last-text' ? 6 : 0))
  if (removed === 'leading-tool') expect(bucket().toolCallOwnerMessageId?.['s11-call']).toBeUndefined()
  else expect(bucket().toolCallOwnerMessageId?.['s11-call']).toBe(keptKey)
  const returning = sources.find(source => source.history_position === drop)!
  await rest([returning], removed === 'last-text' ? 'newer' : 'older', removed === 'last-text', removed !== 'last-text')
  const expected = removed === 'leading-tool' ? [[P, 'Answer.']] : [[A, 'First.\n\nSecond.']]
  expect(assistantRows().map(row => [row.id, row.content])).toEqual(expected)
  await refreshWindow(sources)
  expect(assistantRows().map(row => [row.id, row.content]), 'a repeated bounded refresh after reloading is idempotent').toEqual(expected)
})

it.each([499, 500, 501])('T81 B96: %i accepted sources count source groups, not folded display rows', async count => {
  const sources = Array.from({ length: count }, (_, index) => user(index + 1))
  sources.splice(1, 3, prose(2, A, 'First.'), tool(3), prose(4, B, 'Second.'))
  const initial = count - 100
  await rest(sources.slice(initial), 'latest', true, false, 100)
  useChatStore.getState().enterHistoryReading(SID)
  for (let end = initial; end > 0; end -= 100) {
    const lower = Math.max(0, end - 100)
    await rest(sources.slice(lower, end), 'older', lower > 0, true, 100)
  }
  expect(bucket().history.readerPositions).toEqual(sources.slice(0, Math.min(count, 500)).map(source => source.history_position))
  expect(bucket().history.readerPositions).toHaveLength(Math.min(count, 500))
  expect(bucket().history.hasLater).toBe(count > 500)
  expect(assistantRows().map(row => [row.id, row.content])).toEqual([[A, 'First.\n\nSecond.']])
  expect([pos(2), pos(3), pos(4)].map(position => bucket().historySourceAliases?.[position])).toEqual([A, A, A])
})

it('T81 B96: a middle occurrence expires without discarding either neighbouring text part', async () => {
  const original = [prose(1, A, 'First.'), tool(2), prose(3, B, 'Second.')]
  await rest(original); useChatStore.getState().enterHistoryReading(SID)
  expect(assistantRows()).toHaveLength(1)
  // The network boundary reports one expired source inside the closed range.
  // It does not directly edit a row/part/alias or fabricate a successful fold.
  await rest([original[0], original[2]], 'refresh')
  expect(bucket().history.readerPositions).toEqual([pos(1), pos(3)])
  expect(bucket().historySourceAliases?.[pos(2)]).toBeUndefined()
  expect(assistantRows().map(row => [row.id, row.content]), 'S2a/S11b: a recorded seam stays immutable after its intervening tool occurrence expires').toEqual([[A, 'First.\n\nSecond.']])
  expect(bucket().messagesById[A].tool_calls ?? []).toEqual([])
  expect(bucket().messagesById[A].historyParts?.map(part => part.sourceId)).toEqual([A, B])
  expect(bucket().toolCallOwnerMessageId?.['s11-call']).toBeUndefined()
  expect(bucket().messagesById[A].mergedReplayIds).toEqual([B])
  await rest(original); await rest(original)
  expect(assistantRows().map(row => [row.id, row.content])).toEqual([[A, 'First.\n\nSecond.']])
  expect(bucket().messagesById[A].tool_calls?.map(call => call.id)).toEqual(['s11-call'])
})

for (const shape of ['tool-first', 'text-first'] as const) {
it.each([99, 100, 101])(`T81 B96: ${shape} %i-source tail delivery restores only retained segment parts on a historical reader`, async count => {
  await rest([user(-2), user(-1)], 'latest')
  useChatStore.getState().enterHistoryReading(SID)
  const tail = shape === 'tool-first' ? [tool(1), prose(2, P, 'Answer.'), ...Array.from({ length: count - 2 }, (_, index) => user(index + 3))]
    : [prose(1, A, 'First.'), tool(2), prose(3, P, 'Answer.'), ...Array.from({ length: count - 3 }, (_, index) => user(index + 4))]
  // Each snapshot is legally capped at100 groups. For101, overlapping cuts
  // drop the first source: T for tool-first, A for text-first, never P's text.
  const refresh = vi.fn(async (input: RequestInfo | URL) => {
    const query = new URL(String(input), 'http://history.test').searchParams
    expect([query.get('window_start'), query.get('window_end')]).toEqual([pos(-2), pos(-1)])
    return response(page([user(-2), user(-1)], { later: true, info: { mode: 'window_refresh', limit: 100, min_rows: 20, stop_reason: 'range_end' } }))
  })
  vi.stubGlobal('fetch', refresh)
  native(tail.slice(0, Math.min(count, 100)), true, [], true)
  await vi.waitFor(() => expect(bucket().history.requests.refresh.status).toBe('idle'))
  if (count === 101) {
    native(tail.slice(1), true, [], true)
    await vi.waitFor(() => expect(bucket().history.requests.refresh.status).toBe('idle'))
  }
  expect(refresh).toHaveBeenCalledTimes(count === 101 ? 2 : 1)
  expect(bucket().history.readerPositions, 'a distant current tail never replaces the historical interval').toEqual([pos(-2), pos(-1)])
  expect(bucket().history.tailEntries).toHaveLength(Math.min(count, 100))
  token(P, ' More.')
  const key = shape === 'text-first' && count < 101 ? A : P
  expect(bucket().messagesById[key]?.content, 'the real live-tail restore keeps the surviving saved body and routes the later token').toBe(shape === 'text-first' && count < 101 ? 'First.\n\nAnswer. More.' : 'Answer. More.')
  if (count === 101) {
    expect(bucket().messagesById[key]?.historyParts?.some(part => part.position === pos(1))).toBe(false)
    if (shape === 'tool-first') expect(bucket().messagesById[key]?.tool_calls ?? []).toEqual([])
    else expect(bucket().toolCallOwnerMessageId?.['s11-call']).toBe(P)
  } else expect(bucket().toolCallOwnerMessageId?.['s11-call']).toBe(key)
})
}
