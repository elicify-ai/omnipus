// D4 keeps actual start/LATEST state/LATEST message/end: four fact classes.
// D9 bounds reader500+tail100 groups and releases evicted unowned bodies.
// Chief commissioned historySpanFactStats: counts ONLY, no private-map mock.
import { afterEach, beforeEach, expect, it } from 'vitest'
import type { Message } from '@/lib/api/generated/openapi-types'
import type { SubagentMessageFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from './chat'
import { historySpanFactStats } from './chat/history-merge'
import { reset, cleanup, connect, START, RUNNING, SPAN, SID, STAMP, source, entry, page, pos, origin, expectValidPage, mockPage, activity, bucket } from '../../tests/chat-history-paging/final-gate-harness'
beforeEach(reset)
afterEach(cleanup)

it('R4-I4: live and REST progress retain only needed span facts and evict all unreferenced facts', async () => {
  const VIEW = 500, TAIL = 100
  const progressCount = VIEW + TAIL + 1
  const progress = (index: number): SubagentMessageFrame => ({
    type: 'subagent_message', session_id: SID, span_id: SPAN, child_session_id: 'fg-child',
    message_id: `i4-progress-${index}`, kind: 'progress', sender_identity: 'jim', untrusted_origin: true,
    created_at: STAMP, text: `Synthetic progress body ${index}`,
  })
  const expectBounds = (phase: string, payloadClasses: number) => {
    const stats = historySpanFactStats(SID)
    expect(stats.spans, `${phase}: instrument sees the actual known span, not constant zeros`).toBe(1)
    expect(stats.factsBySpan[SPAN], `${phase}: actual facts were recorded`).toBeGreaterThan(0)
    expect(stats.payloadFactsBySpan[SPAN], `${phase}: actual progress/end payload class was recorded`).toBeGreaterThan(0)
    // Soft assertions keep independent eviction/carrier probes executing;
    // each mismatch still fails this test and preserves raw exit1.
    expect.soft(stats.factsBySpan[SPAN], `${phase}: D4 needs only start/latest-state/latest-message/end, not visited progress history`).toBeLessThanOrEqual(4)
    expect.soft(stats.payloadFactsBySpan[SPAN], `${phase}: only latest message and any terminal result may own payload facts`).toBeLessThanOrEqual(payloadClasses)
    expect(bucket().history.readerPositions.length, `${phase}: D9 reader group bound`).toBeLessThanOrEqual(VIEW)
    expect(bucket().history.tailEntries.length, `${phase}: D9 latest-tail group bound`).toBeLessThanOrEqual(TAIL)
  }
  const expectUnreferencedZero = (phase: string) => {
    expect(Object.values(bucket().messagesById).some(row => row.spans?.some(span => span.spanId === SPAN)), `${phase}: no remaining actual row references the span`).toBe(false)
    expect.soft(historySpanFactStats(SID), `${phase}: evicted unowned facts must be released BEFORE public session release`).toStrictEqual({ spans: 0, factsBySpan: {}, payloadFactsBySpan: {} })
  }
  const unrelated = page(Array.from({ length: TAIL }, (_, i) => entry(2000 + i, { id: `i4-unrelated-${i}`, role: 'assistant', content: `Unrelated current source ${i}` })), { info: { limit: TAIL, min_rows: 20 } })
  expectValidPage(unrelated, Array.from({ length: TAIL }, (_, i) => pos(2000 + i)), TAIL)
  expect(historySpanFactStats('i4-unknown-session'), 'instrument zero is exact for an unknown session').toStrictEqual({ spans: 0, factsBySpan: {}, payloadFactsBySpan: {} })

  // LIVE frames enter the real new rememberLiveSpanFact path, not just REST.
  const wire = connect()
  wire.deliver({ type: 'token', session_id: SID, turn_id: 'i4-parent-turn', agent_id: 'mia', message_id: 'i4-live-parent', content: 'Delegating current work' }, START, RUNNING)
  for (let index = 1; index <= progressCount; index++) wire.deliver(progress(index))
  expect(activity().running.find(item => item.key === SPAN)).toMatchObject({ lifecycleState: 'running', statusLine: `Synthetic progress body ${progressCount}` })
  expectBounds('LIVE progress', 1)
  const ending = { type: 'subagent_end', session_id: SID, span_id: SPAN, status: 'success', duration_ms: 4200, final_result: 'The long helper finished' } as const
  wire.deliver(ending, { type: 'done', session_id: SID, turn_id: 'i4-parent-turn' })
  expect(activity().recentlyFinished.find(item => item.key === SPAN)).toMatchObject({ lifecycleState: 'completed', finalResult: 'The long helper finished' })
  expectBounds('LIVE end', 2)
  mockPage(unrelated)
  await useChatStore.getState().loadLatestHistory(SID, { limit: TAIL, min_rows: 20 })
  expect(bucket().history.readerPositions).toStrictEqual(unrelated.entries.map(row => row.history_position))
  expectUnreferencedZero('LIVE reader replacement')
  useChatStore.getState().releaseSessionHistory(SID)
  expect(historySpanFactStats(SID), 'public release independently releases live span memory').toStrictEqual({ spans: 0, factsBySpan: {}, payloadFactsBySpan: {} })

  // Real bounded REST traversal visits more progress sources than view+tail.
  const sources: Message[] = [source(1, START), source(2, RUNNING)]
  for (let index = 1; index <= progressCount; index++) {
    sources.push(entry(index + 2, { id: `fg-source-${index + 2}`, type: 'system', role: 'system', content: '', system_subtype: 'subagent_message', subagent_message: progress(index) }))
  }
  sources.push(source(progressCount + 3, ending))
  const context = { spans: [{ span_id: SPAN, start: { ...START, ...origin(1) }, state: { ...RUNNING, ...origin(2) }, message: { ...progress(progressCount), ...origin(progressCount + 2) }, end: { ...ending, ...origin(progressCount + 3) }, has_run: true }], duplicate_ids: [] }
  let end = sources.length, first = true
  while (end > 0) {
    const begin = Math.max(0, end - TAIL), group = sources.slice(begin, end)
    const selected = page(group, { earlier: begin > 0, later: !first, info: { mode: first ? 'latest' : 'older', limit: TAIL, min_rows: 20, context } })
    expectValidPage(selected, group.map(row => row.history_position!), group.length)
    mockPage(selected)
    if (first) await useChatStore.getState().loadLatestHistory(SID, { limit: TAIL, min_rows: 20 })
    else await useChatStore.getState().loadOlderHistory(SID, { limit: TAIL, min_rows: 20 })
    expect(bucket().history.requests[first ? 'latest' : 'older'].status).toBe('idle')
    expect(bucket().history.readerPositions.length).toBeLessThanOrEqual(VIEW)
    expect(bucket().history.tailEntries.length).toBeLessThanOrEqual(TAIL)
    end = begin; first = false
  }
  expect(bucket().history.readerPositions).toStrictEqual(sources.slice(0, VIEW).map(row => row.history_position))
  expectBounds('REST traversal', 2)
  expect(activity().recentlyFinished.find(item => item.key === SPAN)).toMatchObject({ lifecycleState: 'completed', finalResult: 'The long helper finished' })
  mockPage(unrelated)
  await useChatStore.getState().loadLatestHistory(SID, { limit: TAIL, min_rows: 20 })
  expect(bucket().history.readerPositions).toStrictEqual(unrelated.entries.map(row => row.history_position))
  expectUnreferencedZero('REST reader replacement')
  useChatStore.getState().releaseSessionHistory(SID)
  expect(historySpanFactStats(SID)).toStrictEqual({ spans: 0, factsBySpan: {}, payloadFactsBySpan: {} })
})
