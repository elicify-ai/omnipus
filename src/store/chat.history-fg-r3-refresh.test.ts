// D7: "Keep the reader's actual source rows, coalescing aliases" and refresh
// public projections after complete; D4 requires real persisted end parity.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useChatStore } from './chat'
import { deferred } from '../../tests/chat-history-paging/fixtures'
import { reset, cleanup, START, RUNNING, SPAN, SID, source, open, origin, connect, AVAILABLE, snapshotAt, complete, page, pos, expectValidPage, response, activity, bucket } from '../../tests/chat-history-paging/final-gate-harness'
beforeEach(reset)
afterEach(cleanup)
it('R3: automatic historical-window refresh keeps the reconstructed helper and its recorded final Activity result', async () => {
  const end = { type: 'subagent_end', session_id: SID, span_id: SPAN, status: 'success', duration_ms: 4200, final_result: 'Recorded helper answer remains readable' } as const
  const sources = [source(1, START), source(2, RUNNING), source(3, end)]
  const frames = [{ ...START, ...origin(1) }, { ...RUNNING, ...origin(2) }, { ...end, ...origin(3) }]
  const selected = open(sources, frames)
  expect(activity().recentlyFinished.find(item => item.key === SPAN)).toMatchObject({ lifecycleState: 'completed', status: 'success', finalResult: 'Recorded helper answer remains readable' })
  useChatStore.getState().enterHistoryReading(SID)
  expect(bucket().history.viewMode).toBe('historical')
  const refreshResponse = deferred<Response>()
  const fetch = vi.fn(async () => refreshResponse.promise)
  vi.stubGlobal('fetch', fetch)
  const wire = connect(); wire.attach()
  wire.deliver(snapshotAt(selected), AVAILABLE, ...frames, { type: 'done', session_id: SID }, complete())
  expect(bucket().history.requests.refresh.status, 'instrument: COMPLETE really started the automatic loaded-window refresh').toBe('loading')
  expect(fetch).toHaveBeenCalledTimes(1)
  // This joins the existing request, not a replacement reconstruction path.
  const pending = useChatStore.getState().refreshLoadedHistory(SID)
  expect(fetch).toHaveBeenCalledTimes(1)
  const refreshed = page(sources, { info: { mode: 'window_refresh', limit: 100, min_rows: 20 } })
  expectValidPage(refreshed, [pos(1), pos(2), pos(3)], 3)
  refreshResponse.resolve(response(refreshed))
  await pending
  expect(bucket().history.requests.refresh.status, 'the real automatic refresh succeeded').toBe('idle')
  expect(bucket().history.readerPositions).toStrictEqual([pos(1), pos(2), pos(3)])
  const view = activity()
  expect(view.recentlyFinished.find(item => item.key === SPAN), 'D4/D7: successful refresh cannot silently erase the actual retained helper/result').toMatchObject({ kind: 'agent', lifecycleState: 'completed', status: 'success', finalResult: 'Recorded helper answer remains readable', durationMs: 4200 })
  expect(view.running.filter(item => item.key === SPAN)).toStrictEqual([])
  expect(view.runningChildren).toBe(0)
  expect(bucket().messagesById['fg-source-1'].spans).toHaveLength(1)
  expect(bucket().messagesById['fg-source-1'].spans?.[0]).toMatchObject({ spanId: SPAN, status: 'success', lifecycleState: 'completed', finalResult: 'Recorded helper answer remains readable' })
})
