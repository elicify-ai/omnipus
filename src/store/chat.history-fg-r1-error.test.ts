// R1: D4 "Live streaming/control values must win overlaps with history."
// D6: current/live terminal results win overlap. Recorded end is authoritative.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { useChatStore } from './chat'
import { replay } from '../../tests/chat-history-paging/fixtures'
import { reset, cleanup, runningPage, open, socket, origin, RUNNING, START, SPAN, SID, entry, page, pos, expectValidPage, mockPage, activity, bucket } from '../../tests/chat-history-paging/final-gate-harness'
beforeEach(reset)
afterEach(cleanup)
it('R1-error: a live error end on a positioned start survives an unrelated older-page commit in real Activity', async () => {
  const fixture = runningPage()
  open(fixture.sources, [{ ...START, ...origin(2) }, { ...RUNNING, ...origin(3) }, ...fixture.later.map(row => replay(row))], { earlier: true })
  expect(bucket().history.historyLoaded).toBe(true)
  expect(activity().running.find(item => item.key === SPAN)).toMatchObject({ lifecycleState: 'running' })
  socket().deliver({ type: 'subagent_end', session_id: SID, span_id: SPAN, status: 'error', duration_ms: 4200, final_result: 'The live helper failed with this recorded explanation.' })
  const before = activity()
  expect(before.running.filter(item => item.key === SPAN), 'instrument: the real live end already removed this helper from open work').toStrictEqual([])
  expect(before.recentlyFinished.find(item => item.key === SPAN)).toMatchObject({ lifecycleState: 'failed', status: 'error', finalResult: 'The live helper failed with this recorded explanation.', durationMs: 4200 })

  const older = page([entry(1, { id: 'fg-unrelated-older', role: 'user', content: 'Earlier unrelated question' })], { later: true, info: { mode: 'older', limit: 20, min_rows: 1 } })
  expectValidPage(older, [pos(1)], 1)
  mockPage(older)
  await useChatStore.getState().loadOlderHistory(SID)
  expect(bucket().history.requests.older.status, 'the unrelated older read truly completed').toBe('idle')
  expect(bucket().history.readerPositions).toStrictEqual([pos(1), ...fixture.sources.map(row => row.history_position)])
  const after = activity()
  expect(after.running.filter(item => item.key === SPAN), 'D4/D6: older history cannot resurrect the terminal live helper as open work').toStrictEqual([])
  expect(after.runningChildren).toBe(0)
  expect(after.recentlyFinished.find(item => item.key === SPAN), 'the actual live outcome and result remain readable').toMatchObject({ lifecycleState: 'failed', status: 'error', finalResult: 'The live helper failed with this recorded explanation.', durationMs: 4200 })
  expect(bucket().messagesById['fg-source-2'].historyPosition).toBe(pos(2))
  expect(bucket().messagesById['fg-source-2'].spans?.[0]).toMatchObject({ spanId: SPAN, status: 'error', lifecycleState: 'failed', finalResult: 'The live helper failed with this recorded explanation.' })
})
