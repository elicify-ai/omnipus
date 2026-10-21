// T08/T09/T34 direct REAL history actions, agreed seam K (2026-10-21).
// No production stub/test-only reducer or API mock. Only HTTP, socket and clock.
// Missing agreed exports/actions fail inside the named case, not collection.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { queryClient } from '@/lib/queryClient'
import { resetRealStores } from '../../tests/l1-room-ui/harness'
import { deferred, entry, expectValidPage, page, pos, replay, snapshot, SID, STAMP } from '../../tests/chat-history-paging/fixtures'
import { historyTransport } from '../../tests/chat-history-paging/transport'

const modules = import.meta.glob<Record<string, unknown>>(['/src/store/chat/history.ts', '/src/store/chat/store.ts'])
async function historyExport(name: string) {
  if (!modules['/src/store/chat/store.ts']) throw new Error('Fixture instrument cannot see the known existing chat store')
  const load = modules['/src/store/chat/history.ts']
  if (!load) expect.fail(`BLOCKED: ${process.cwd()}/src/store/chat/history.ts::${name} not implemented — required by agreed seam K / D6/D7/D9`)
  const exported = (await load())[name]
  if (typeof exported !== 'function') expect.fail(`BLOCKED: ${process.cwd()}/src/store/chat/history.ts::${name} not exported — required by agreed seam K`)
  return exported
}
async function action(name: string, ...args: unknown[]): Promise<void> {
  const current = useChatStore.getState()
  const method: unknown = Reflect.get(current, name)
  if (typeof method !== 'function') expect.fail(`BLOCKED: useChatStore.${name} not implemented — required by agreed seam K / D6/D7/D9`)
  await Reflect.apply(method, current, args)
}
function history() {
  const bucket = useChatStore.getState().sessionsById[SID]
  if (!bucket) throw new Error('Fixture instrument: no addressed chat bucket')
  const view: unknown = Reflect.get(bucket, 'history')
  if (view === null || typeof view !== 'object') expect.fail('BLOCKED: SessionChatState.history not implemented — required by agreed seam K')
  return view as Record<string, unknown> // not-wire-format: inspect the agreed internal store owner, never a wire clone.
}
function requestState(kind: string) {
  const states = history().requests
  if (states === null || typeof states !== 'object') throw new Error('Agreed history requests state is missing')
  return Reflect.get(states, kind) as { status: string; error: string | null; attempt: number } // not-wire-format: internal request-state observation.
}
function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}
let transport: ReturnType<typeof historyTransport> | undefined
beforeEach(() => {
  resetRealStores()
  useSessionStore.getState().setActiveSession(SID, 'mia')
})
afterEach(() => {
  transport?.connection.disconnect(); transport = undefined
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  vi.unstubAllGlobals()
})
const initialRanks = Array.from({ length: 40 }, (_, i) => 101 + i)
function initialPage() {
  const fixture = page(initialRanks.map((rank) => entry(rank)), { earlier: true })
  expectValidPage(fixture, initialRanks.map(pos), 40)
  return fixture
}
async function open() {
  vi.stubGlobal('fetch', async () => response(initialPage()))
  await action('loadLatestHistory', SID)
  expect(history().readerPositions).toStrictEqual(initialRanks.map(pos))
  await action('enterHistoryReading', SID)
}

describe('T08/T09 agreed history-slice ownership — B08/B11/B12/B34', () => {
  it('the empty history owner means unknown, not successful empty history', async () => {
    const createEmpty = await historyExport('emptyHistoryState')
    const idle = { status: 'idle', error: null, attempt: 0 }
    const expected = {
      viewGeneration: 0, viewMode: 'latest', serverState: null,
      historyLoaded: false, liveControlsReady: false, pageMetadata: null,
      readerRange: null, readerPositions: [], tailEntries: [], openedClearPositions: [],
      hasEarlier: null, hasLater: null, olderBoundary: null, newerBoundary: null,
      requests: { older: idle, newer: idle, latest: idle, refresh: idle },
    }
    expect(Reflect.apply(createEmpty, undefined, [])).toStrictEqual(expected)
    const select = await historyExport('selectSessionHistory')
    const first = Reflect.apply(select, undefined, [useChatStore.getState(), 'missing-history-bucket'])
    expect(first, 'a missing bucket is unknown, never a successful empty page').toStrictEqual(expected)
    expect(Reflect.apply(select, undefined, [useChatStore.getState(), 'missing-history-bucket']), 'missing-bucket selector reference is stable').toBe(first)
  })

  it('duplicate older triggers join one exact boundary read and commit ascending source order', async () => {
    await open()
    const returned = deferred<Response>()
    const seen = deferred<void>()
    const reads: URLSearchParams[] = []
    vi.stubGlobal('fetch', (url: RequestInfo | URL) => {
      reads.push(new URL(String(url), 'https://history.test').searchParams)
      seen.resolve(); return returned.promise
    })
    const first = action('loadOlderHistory', SID)
    const second = action('loadOlderHistory', SID)
    await seen.promise
    expect(reads.map((query) => Object.fromEntries(query))).toStrictEqual([{ before: pos(101), limit: '20', min_rows: '20' }])
    expect(requestState('older')).toMatchObject({ status: 'loading', error: null, attempt: 1 })
    expect(requestState('latest'), 'one loading action must not mutate another action record').toMatchObject({ status: 'idle', error: null })
    const older = Array.from({ length: 20 }, (_, i) => entry(81 + i))
    const fixture = page(older, { earlier: true, later: true, info: { mode: 'older', limit: 20, min_rows: 20 } })
    expectValidPage(fixture, Array.from({ length: 20 }, (_, i) => pos(81 + i)), 20)
    returned.resolve(response(fixture))
    await Promise.all([first, second])
    expect(history().readerPositions).toStrictEqual(Array.from({ length: 60 }, (_, i) => pos(81 + i)))
    expect(useChatStore.getState().messages.map((message) => message.id)).toStrictEqual(Array.from({ length: 60 }, (_, i) => `history-${81 + i}`))
    expect(requestState('older')).toMatchObject({ status: 'idle', error: null })
    expect(useChatStore.getState().sessionsById[SID].cursor, 'REST history never advances the live cursor').toBeNull()
  })

  it('a contradictory older page becomes a persistent action error without partial rows or flags', async () => {
    await open()
    const olderRanks = Array.from({ length: 20 }, (_, i) => 81 + i)
    const good = page(olderRanks.map((rank) => entry(rank)), { earlier: true, later: true, info: { mode: 'older', limit: 20, min_rows: 20 } })
    expectValidPage(good, olderRanks.map(pos), 20)
    const bad = structuredClone(good)
    bad.page_info.source_group_count = 0
    vi.stubGlobal('fetch', async () => response(bad))
    await action('loadOlderHistory', SID)
    expect(history().readerPositions).toStrictEqual(initialRanks.map(pos))
    expect(useChatStore.getState().messages.map((message) => message.id)).toStrictEqual(initialRanks.map((rank) => `history-${rank}`))
    expect(requestState('older')).toMatchObject({ status: 'error', attempt: 1 })
    expect(requestState('older').error, 'protocol failures cannot be demoted to a log or empty success').toBeTypeOf('string')
    expect(requestState('older').error).not.toBe('')
    expect(requestState('latest')).toMatchObject({ status: 'idle', error: null })
    expect(useChatStore.getState().sessionsById[SID].cursor).toBeNull()
  })

  it.each(['success', 'failure'] as const)('release/new A generation fences old older-page %s, including an abort-ignoring transport', async (outcome) => {
    await open()
    const oldGeneration = history().viewGeneration
    const returned = deferred<Response>()
    const seen = deferred<void>()
    const transportOptions: Pick<RequestInit, 'signal'> = {}
    vi.stubGlobal('fetch', (_url: RequestInfo | URL, init?: RequestInit) => {
      transportOptions.signal = init?.signal; seen.resolve(); return returned.promise
    })
    const oldRead = action('loadOlderHistory', SID)
    await seen.promise
    await action('releaseSessionHistory', SID)
    expect(transportOptions.signal?.aborted, 'D6 abort the HTTP read when possible').toBe(true)
    useSessionStore.getState().setActiveSession('history-other-generation', 'mia')
    useSessionStore.getState().setActiveSession(SID, 'mia')
    const freshRanks = Array.from({ length: 40 }, (_, i) => 501 + i)
    const fresh = page(freshRanks.map((rank) => entry(rank)), { earlier: true })
    expectValidPage(fresh, freshRanks.map(pos), 40)
    vi.stubGlobal('fetch', async () => response(fresh))
    await action('loadLatestHistory', SID)
    const obsoleteRanks = Array.from({ length: 20 }, (_, i) => 81 + i)
    const obsolete = page(obsoleteRanks.map((rank) => entry(rank)), { earlier: true, later: true, info: { mode: 'older', limit: 20, min_rows: 20 } })
    expectValidPage(obsolete, obsoleteRanks.map(pos), 20)
    returned.resolve(outcome === 'success' ? response(obsolete) : response({ error: 'obsolete page failure' }, 500))
    await oldRead
    expect(history().viewGeneration).toBeGreaterThan(oldGeneration as number)
    expect(history().readerPositions).toStrictEqual(freshRanks.map(pos))
    expect(useChatStore.getState().messages.map((message) => message.id)).toStrictEqual(freshRanks.map((rank) => `history-${rank}`))
    expect(requestState('older')).toMatchObject({ status: 'idle', error: null })
  })

  it('a current live value wins a stale latest-page overlap and is never duplicated', async () => {
    await open()
    const returned = deferred<Response>()
    const seen = deferred<void>()
    vi.stubGlobal('fetch', () => { seen.resolve(); return returned.promise })
    const read = action('returnToLatestHistory', SID)
    await seen.promise
    transport = historyTransport()
    transport.deliver({ type: 'user_message', session_id: SID, id: 'history-539', content: 'Current live value wins', timestamp: STAMP })
    const freshRanks = Array.from({ length: 40 }, (_, i) => 501 + i)
    const stale = page(freshRanks.map((rank) => entry(rank)), { earlier: true })
    expectValidPage(stale, freshRanks.map(pos), 40)
    returned.resolve(response(stale))
    await read
    expect(useChatStore.getState().messages.map(({ id, content }) => ({ id, content }))).toStrictEqual(freshRanks.map((rank) => ({
      id: `history-${rank}`, content: rank === 539 ? 'Current live value wins' : `Saved row ${rank}`,
    })))
    expect(history().viewMode).toBe('latest')
    expect(transport.socket.sent.filter((frame) => frame.type === 'message' || frame.type === 'cancel')).toStrictEqual([])
  })

  it('overlapping 100-group snapshots keep only the newest100 tail groups without evicting the historical reader', async () => {
    await open()
    transport = historyTransport()
    vi.stubGlobal('fetch', async (url: RequestInfo | URL) => {
      const query = new URL(String(url), 'https://history.test').searchParams
      expect(query.get('window_start')).toBe(pos(101)); expect(query.get('window_end')).toBe(pos(140))
      const limit = Number(query.get('limit') ?? 40)
      const before = query.get('before')
      const upper = before === null ? initialRanks.length : initialRanks.findIndex((rank) => pos(rank) === before)
      expect(upper, 'fixture must recognize the exact in-range continuation').toBeGreaterThan(0)
      const lower = Math.max(0, upper - limit)
      const selected = initialRanks.slice(lower, upper)
      const refreshed = page(selected.map((rank) => entry(rank)), { earlier: lower > 0, later: upper < initialRanks.length, info: {
        mode: 'window_refresh', limit, min_rows: 20, stop_reason: lower > 0 ? 'row_target' : 'range_end',
      } })
      expectValidPage(refreshed, selected.map(pos), selected.length)
      return response(refreshed)
    })
    for (const [index, first] of [701, 702].entries()) {
      expect(transport.connection.send({ type: 'attach_session', session_id: SID, history_limit: 100, history_min_rows: 20 })).toBe(true)
      const ranks = Array.from({ length: 100 }, (_, i) => first + i)
      const latest = page(ranks.map((rank) => entry(rank)), { earlier: true, info: { limit: 100 } })
      expectValidPage(latest, ranks.map(pos), 100)
      transport.deliver(
        { ...snapshot(latest, 'unknown_position'), seq: 200 + index },
        { type: 'session_state', session_id: SID, user_id: 'history-reader', pending_approvals: [], emitted_at: STAMP, history_state: { status: 'available' } },
        ...latest.entries.map((source) => replay(source)),
        { type: 'done', session_id: SID },
        { type: 'catch_up_complete', session_id: SID, seq: 200 + index, boot_id: 'history-boot', mode: 'snapshot' },
      )
      await action('refreshLoadedHistory', SID)
      expect(history().readerPositions, 'distant current tail never replaces the held source interval').toStrictEqual(initialRanks.map(pos))
      const tail = history().tailEntries as { id: string; historyPosition: string }[] // not-wire-format: normalized internal tail observation.
      expect(tail.map(({ id, historyPosition }) => ({ id, historyPosition }))).toStrictEqual(ranks.map((rank) => ({ id: `history-${rank}`, historyPosition: pos(rank) })))
      expect(tail).toHaveLength(100)
    }
    expect(useChatStore.getState().messages.map((message) => message.id)).toStrictEqual(initialRanks.map((rank) => `history-${rank}`))
  })

  it('release drops reader/tail/query payloads but preserves genuinely pending input identity', async () => {
    await open()
    transport = historyTransport()
    useChatStore.getState().sendMessage('Keep pending input', { clientMessageId: 'history-pending-client' })
    const pending = useChatStore.getState().messages.find((message) => message.id === 'history-pending-client')
    expect(pending, 'positive control: the real send created an owned pending bubble').toMatchObject({ id: 'history-pending-client', content: 'Keep pending input' })
    queryClient.setQueryData(['messages', SID], initialPage())
    await action('releaseSessionHistory', SID)
    expect(history().readerPositions).toStrictEqual([])
    expect(history().tailEntries).toStrictEqual([])
    expect(history().readerRange).toBeNull()
    expect(queryClient.getQueryData(['messages', SID])).toBeUndefined()
    expect(useChatStore.getState().sessionsById[SID].messagesById['history-pending-client']).toStrictEqual(pending)
    expect(transport.socket.sent.filter((frame) => frame.type === 'cancel')).toStrictEqual([])
  })
})

async function fill500() {
  const first = Array.from({ length: 40 }, (_, i) => entry(5461 + i))
  vi.stubGlobal('fetch', async () => response(page(first, { earlier: true })))
  await action('loadLatestHistory', SID)
  await action('enterHistoryReading', SID)
  for (let upper = 5460; upper >= 5020; upper -= 20) {
    const ranks = Array.from({ length: 20 }, (_, i) => upper - 19 + i)
    const fixture = page(ranks.map((rank) => entry(rank)), { earlier: true, later: true, info: { mode: 'older', limit: 20, min_rows: 20 } })
    expectValidPage(fixture, ranks.map(pos), 20)
    vi.stubGlobal('fetch', async () => response(fixture))
    await action('loadOlderHistory', SID)
  }
  expect(history().readerPositions).toStrictEqual(Array.from({ length: 500 }, (_, i) => pos(5001 + i)))
}

describe('T09/T34 agreed bounded source owner — B12/B26/B47', () => {
  it('a 501st source keeps the newly fetched predecessor and removes the safe far edge', async () => {
    await fill500()
    const fixture = page([entry(5000)], { later: true, info: { mode: 'older', limit: 20, min_rows: 20 } })
    expectValidPage(fixture, [pos(5000)], 1)
    vi.stubGlobal('fetch', async () => response(fixture))
    await action('loadOlderHistory', SID)
    expect(history().readerPositions).toStrictEqual(Array.from({ length: 500 }, (_, i) => pos(5000 + i)))
    expect(useChatStore.getState().messages.map((message) => message.id)).toStrictEqual(Array.from({ length: 500 }, (_, i) => `history-${5000 + i}`))
    expect(history().hasLater, 'evicted newer edge remains reloadable').toBe(true)
    const newer = page([entry(5500)], { earlier: true, info: { mode: 'newer', limit: 20, min_rows: 20, stop_reason: 'end' } })
    expectValidPage(newer, [pos(5500)], 1)
    const reads: URLSearchParams[] = []
    vi.stubGlobal('fetch', async (url: RequestInfo | URL) => {
      reads.push(new URL(String(url), 'https://history.test').searchParams)
      return response(newer)
    })
    await action('loadNewerHistory', SID)
    expect(reads.map((query) => Object.fromEntries(query))).toStrictEqual([{ after: pos(5499), limit: '20', min_rows: '20' }])
    expect(history().readerPositions).toStrictEqual(Array.from({ length: 500 }, (_, i) => pos(5001 + i)))
    expect(useChatStore.getState().messages.map((message) => message.id)).toStrictEqual(Array.from({ length: 500 }, (_, i) => `history-${5001 + i}`))
    expect(history().hasLater).toBe(false)
  })

  it('a refresh failing after a staged chunk retains all500 cached sources and retries the same closed range', async () => {
    await fill500()
    const oldIds = Array.from({ length: 500 }, (_, i) => `history-${5001 + i}`)
    const reads: URLSearchParams[] = []
    vi.stubGlobal('fetch', async (url: RequestInfo | URL) => {
      const query = new URL(String(url), 'https://history.test').searchParams
      reads.push(query)
      expect(query.get('window_start')).toBe(pos(5001))
      expect(query.get('window_end')).toBe(pos(5500))
      expect(query.has('after')).toBe(false)
      if (reads.length === 2) return response({ error: 'staged refresh failed' }, 500)
      const ranks = Array.from({ length: 20 }, (_, i) => 5481 + i)
      const limit = Number(query.get('limit') ?? 40)
      const chunk = page(ranks.map((rank) => entry(rank)), { earlier: true, info: {
        mode: 'window_refresh', limit, min_rows: 20, stop_reason: limit === 20 ? 'row_target' : 'byte_budget',
      } })
      chunk.page_info.soft_byte_budget = chunk.page_info.projected_bytes
      expectValidPage(chunk, ranks.map(pos), 20)
      return response(chunk)
    })
    await action('refreshLoadedHistory', SID)
    expect(reads).toHaveLength(2)
    expect(reads[1].get('before')).toBe(pos(5481))
    expect(requestState('refresh')).toMatchObject({ status: 'error', attempt: 1 })
    expect(useChatStore.getState().messages.map((message) => message.id), 'partial staging is not proof480 rows expired').toStrictEqual(oldIds)
    expect(history().readerPositions).toStrictEqual(Array.from({ length: 500 }, (_, i) => pos(5001 + i)))
    const retrySeen = deferred<void>()
    const retryResponse = deferred<Response>()
    vi.stubGlobal('fetch', (url: RequestInfo | URL) => {
      const query = new URL(String(url), 'https://history.test').searchParams
      expect(query.get('window_start')).toBe(pos(5001)); expect(query.get('window_end')).toBe(pos(5500))
      expect(query.has('before'), 'Retry restarts revalidation of the whole saved interval').toBe(false)
      retrySeen.resolve(); return retryResponse.promise
    })
    const retry = action('retryHistory', SID, 'refresh')
    await retrySeen.promise
    expect(requestState('refresh')).toMatchObject({ status: 'loading', attempt: 2 })
    retryResponse.resolve(response({ error: 'retry failed explicitly' }, 500))
    await retry
    expect(requestState('refresh')).toMatchObject({ status: 'error', attempt: 2 })
    expect(useChatStore.getState().messages.map((message) => message.id)).toStrictEqual(oldIds)
  })
})
