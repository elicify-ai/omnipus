// D4 bounded context carries PROVEN has_run. Delegation surface D10 hides only
// queued work that NEVER ran; real proof must retain Started/terminal events.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { useChatStore } from './chat'
import { deriveDelegationEvents } from '@/lib/delegationEvents'
import { reset, cleanup, START, RUNNING, SPAN, SID, source, entry, page, pos, origin, expectValidPage, mockPage, activity, bucket } from '../../tests/chat-history-paging/final-gate-harness'
beforeEach(reset)
afterEach(cleanup)
it('R7: bounded actual has_run proof preserves Started and terminal delegation events after queued-then-failed history', async () => {
  const queuedResult = { session_id: 'fg-child', generation: 1, is_3p: false, state: 'queued', queue_position: 1 }
  const parent = entry(1, { id: 'r7-parent', role: 'assistant', content: 'Delegating the audit', tool_calls: [{ id: 'fg-parent-call', tool: 'delegate', parameters: { action: 'run', agent_id: 'jim', label: 'Read the retained pages', task: 'Audit the source' }, status: 'success', content_state: 'full', result: queuedResult }] })
  const start = source(2, START)
  const failed = { ...RUNNING, state: 'failed' as const }
  const end = { type: 'subagent_end', session_id: SID, span_id: SPAN, status: 'error', duration_ms: 4200, final_result: 'Actual recorded failure after running' } as const
  const selected = page([parent, start], { rows: 3, info: { context: { spans: [{ span_id: SPAN, start: { ...START, ...origin(2) }, state: { ...failed, ...origin(100) }, end: { ...end, ...origin(101) }, has_run: true }], duplicate_ids: [] } } })
  expectValidPage(selected, [pos(1), pos(2)], 3)
  mockPage(selected)
  await useChatStore.getState().loadLatestHistory(SID)
  expect(bucket().history.historyLoaded, 'the actual context-carrying page was loaded').toBe(true)
  expect(bucket().messagesById['r7-parent'].tool_calls?.[0], 'instrument: the actual queued launch survived the REST adapter').toMatchObject({ id: 'fg-parent-call', tool: 'delegate', result: queuedResult })
  const view = activity()
  expect(view.recentlyFinished.find(item => item.key === SPAN)).toMatchObject({ lifecycleState: 'failed', status: 'error', finalResult: 'Actual recorded failure after running' })
  const messages = bucket().messageOrder.map(id => bucket().messagesById[id])
  const events = deriveDelegationEvents({ sessionId: SID, messages: messages.map(row => ({ id: row.id, timestamp: row.timestamp, spans: row.spans, toolCalls: row.tool_calls })), agentNames: { jim: 'Jim' } })
  expect(events.map(event => event.kind), 'D4/D10: explicit actual execution proof prevents classifying this failed queued helper as never ran').toStrictEqual(['started', 'stopped'])
  expect(bucket().messagesById['fg-source-2'].spans?.[0]).toMatchObject({ spanId: SPAN, status: 'error', lifecycleState: 'failed', hasRun: true, finalResult: 'Actual recorded failure after running' })
})
