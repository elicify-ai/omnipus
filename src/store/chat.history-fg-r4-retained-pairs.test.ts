// D9 explicitly permits reader<=500 SOURCE GROUPS, plus a tail<=100 groups.
// D4: actual recorded end/lifecycle is authoritative. 201*2=402 is the user-
// supplied valid retained window, not the implementation's private cache cap.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { useChatStore } from './chat'
import { reset, cleanup, START, SID, source, page, pos, expectValidPage, mockPage, activity, bucket } from '../../tests/chat-history-paging/final-gate-harness'
beforeEach(reset)
afterEach(cleanup)
it('R4: all 201 helper ends and results survive while all 402 source groups remain in the allowed reader', async () => {
  const expected = Array.from({ length: 201 }, (_, i) => ({ spanId: `r4-span-${i + 1}`, startRank: i * 2 + 1, finalResult: `Recorded result for helper ${i + 1}` }))
  const sources = expected.flatMap(item => [
    source(item.startRank, { ...START, span_id: item.spanId, parent_call_id: `r4-call-${item.startRank}`, child_session_id: `r4-child-${item.startRank}` }),
    source(item.startRank + 1, { type: 'subagent_end', session_id: SID, span_id: item.spanId, status: 'success', duration_ms: 4200, final_result: item.finalResult }),
  ])
  let end = sources.length
  let first = true
  while (end > 0) {
    // At most100 groups per real request; full earlier pages hit their row
    // target, the final2-source retained-end page may be underfilled.
    const begin = Math.max(0, end - 100)
    const group = sources.slice(begin, end)
    const selected = page(group, { earlier: begin > 0, later: !first, info: { mode: first ? 'latest' : 'older', limit: 100, min_rows: 20 } })
    expectValidPage(selected, group.map(row => row.history_position!), group.length)
    mockPage(selected)
    if (first) await useChatStore.getState().loadLatestHistory(SID, { limit: 100, min_rows: 20 })
    else await useChatStore.getState().loadOlderHistory(SID, { limit: 100, min_rows: 20 })
    expect(bucket().history.requests[first ? 'latest' : 'older'].status).toBe('idle')
    end = begin; first = false
  }
  expect(bucket().history.historyLoaded).toBe(true)
  expect(bucket().history.readerPositions, 'instrument: NONE of these 402 groups has left the explicit500-group reader').toStrictEqual(sources.map(row => row.history_position))
  expect(bucket().history.readerPositions.length).toBe(402)
  expect(bucket().history.readerPositions.length, 'D9 source-group bound, not an invented memo/byte limit').toBeLessThanOrEqual(500)
  expect(bucket().history.tailEntries.length, 'D9 separately bounded latest tail').toBeLessThanOrEqual(100)
  const view = activity()
  expect(view.running.filter(item => item.kind === 'agent'), 'D4/D9: retained recorded ends cannot be discarded and presented as open helpers').toStrictEqual([])
  expect(view.runningChildren).toBe(0)
  // Activity's recent list has its own presentation cap. Audit EVERY retained
  // owner in the actual store instead of falsely demanding201 visible rows.
  for (const item of expected) {
    const row = bucket().messagesById[`fg-source-${item.startRank}`]
    expect(row.historyPosition).toBe(pos(item.startRank))
    expect(row.spans, `retained helper ${item.spanId} has one owner`).toHaveLength(1)
    expect(row.spans?.[0], `recorded end/result for retained ${item.spanId} cannot be evicted as disposable cache`).toMatchObject({ spanId: item.spanId, status: 'success', finalResult: item.finalResult, durationMs: 4200 })
  }
})
