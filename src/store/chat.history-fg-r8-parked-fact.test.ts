// D4 retains actual relevant facts; D9 preserves necessary pending controls.
// A real update parked before its start still owns its one progress fact.
// Current bound ruling: <=5 facts/3 payloads, not a guessed50-item product cap.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { useChatStore } from './chat'
import { historySpanFactStats } from './chat/history-merge'
import { replay } from '../../tests/chat-history-paging/fixtures'
import { reset, cleanup, open, socket, START, SID, STAMP, entry, page, pos, expectValidPage, mockPage, bucket } from '../../tests/chat-history-paging/final-gate-harness'
beforeEach(reset)
afterEach(cleanup)
it('R8: a parked-before-start span keeps its progress fact through another start and older-page settlement', async () => {
  const P = 'r8-parked-P', Q = 'r8-unrelated-Q'
  const fillers = Array.from({ length: 20 }, (_, i) => entry(i + 2, { id: `r8-filler-${i + 2}`, role: 'assistant', content: `Independent filler ${i + 2}` }))
  open(fillers, fillers.map(row => replay(row)), { earlier: true })
  expect(bucket().history.historyLoaded, 'instrument: the actual latest page committed').toBe(true)
  socket().deliver({ type: 'subagent_message', session_id: SID, span_id: P, child_session_id: 'r8-P-child', message_id: 'r8-early-note', kind: 'progress', sender_identity: 'jim', untrusted_origin: true, created_at: STAMP, text: 'Early note', seq: 101 })
  expect(bucket().pendingSpanUpdatesBySpanId?.[P], 'instrument: P really is parked before its start').toMatchObject({ statusLine: 'Early note' })
  expect(historySpanFactStats(SID).factsBySpan[P], 'one actual supplied P message creates one pending fact').toBe(1)
  socket().deliver({ ...START, span_id: Q, parent_call_id: 'r8-Q-call', child_session_id: 'r8-Q-child', seq: 102 })
  expect(bucket().pendingSpanUpdatesBySpanId?.[P]).toMatchObject({ statusLine: 'Early note' })
  expect.soft(historySpanFactStats(SID).factsBySpan[P], 'D4/D9: another span opening cannot release P while its actual pending update still owns the fact').toBe(1)

  const older = page([entry(1, { id: 'r8-unrelated-older', role: 'user', content: 'Unrelated older input' })], { later: true, info: { mode: 'older', limit: 20, min_rows: 1 } })
  expectValidPage(older, [pos(1)], 1)
  mockPage(older)
  await useChatStore.getState().loadOlderHistory(SID)
  expect(bucket().history.requests.older.status).toBe('idle')
  expect(bucket().history.readerPositions).toStrictEqual([pos(1), ...fillers.map(row => row.history_position)])
  expect(bucket().pendingSpanUpdatesBySpanId?.[P]).toMatchObject({ statusLine: 'Early note' })
  expect.soft(historySpanFactStats(SID).factsBySpan[P], 'D4/D9: unrelated history settlement must preserve the still-owned parked P fact').toBe(1)

  socket().deliver({ ...START, span_id: P, parent_call_id: 'r8-P-call', child_session_id: 'r8-P-child', seq: 103 })
  const owner = Object.values(bucket().messagesById).find(row => row.spans?.some(span => span.spanId === P))
  expect(owner?.spans?.find(span => span.spanId === P), 'the actual start applies the parked text, not an invented status').toMatchObject({ spanId: P, statusLine: 'Early note', childSessionId: 'r8-P-child' })
  expect(bucket().pendingSpanUpdatesBySpanId?.[P], 'the started owner consumes the parked record').toBeUndefined()
  const stats = historySpanFactStats(SID)
  expect(stats.factsBySpan[P], 'the started span has an actual retained fact count, not an absent record').toBeDefined()
  expect(stats.factsBySpan[P], 'the started span keeps actual owned facts').toBeGreaterThan(0)
  expect(stats.factsBySpan[P], 'general D4/bound ruling, without prescribing start-row memo topology').toBeLessThanOrEqual(5)
  expect.soft(stats.payloadFactsBySpan[P], 'the one actual Early note payload remains retained under its now-visible span').toBe(1)
})
