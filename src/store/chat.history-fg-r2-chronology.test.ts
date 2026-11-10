// D4: relevant real persisted state/lifecycle wins; bounded context contains
// actual start/LATEST state facts. D6 preserves current/live values on merge.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { useChatStore } from './chat'
import { replay } from '../../tests/chat-history-paging/fixtures'
import { reset, cleanup, START, RUNNING, SPAN, SID, STAMP, source, open, origin, entry, page, pos, expectValidPage, mockPage, activity, bucket } from '../../tests/chat-history-paging/final-gate-harness'
beforeEach(reset)
afterEach(cleanup)
it('R2: earlier REST queued facts cannot overwrite the later socket running state when its start loads', async () => {
  const start = source(1, START)
  const queued = { ...RUNNING, state: 'queued' as const, created_at: '2026-10-21T09:59:00Z' }
  const queuedSource = source(2, queued)
  const latestState = source(3, RUNNING)
  const later = Array.from({ length: 19 }, (_, i) => entry(4 + i, { id: `r2-later-${i}`, role: 'assistant', content: `Later source ${i}` }))
  const context = { spans: [{ span_id: SPAN, start: { ...START, ...origin(1) }, state: { ...RUNNING, ...origin(3) }, has_run: true }], duplicate_ids: [] }
  open([latestState, ...later], [{ ...RUNNING, ...origin(3) }, ...later.map(row => replay(row))], { earlier: true, info: { context } })
  expect(bucket().history.historyLoaded).toBe(true)
  expect(bucket().messagesById['fg-source-1'], 'instrument: the old start is not on the latest page yet').toBeUndefined()
  const older = page([start, queuedSource], { later: true, info: { mode: 'older', limit: 20, min_rows: 2, context } })
  expectValidPage(older, [pos(1), pos(2)], 2)
  mockPage(older)
  await useChatStore.getState().loadOlderHistory(SID)
  expect(bucket().history.requests.older.status).toBe('idle')
  expect(bucket().history.readerPositions).toStrictEqual([pos(1), pos(2), pos(3), ...later.map(row => row.history_position)])
  const view = activity()
  expect(view.running.find(item => item.key === SPAN), 'D4/D6: actual later running state remains authoritative over earlier queued REST state').toMatchObject({ kind: 'agent', lifecycleState: 'running', status: 'running', childSessionId: 'fg-child' })
  expect(view.runningChildren).toBe(1)
  expect(view.recentlyFinished.filter(item => item.key === SPAN)).toStrictEqual([])
  expect(bucket().messagesById['fg-source-1'].spans?.[0]).toMatchObject({ spanId: SPAN, lifecycleState: 'running', hasRun: true, lastUpdateAt: STAMP })
})
