// S11b/S11c / B97. Subscribe to actual atomic updates, never fix stale indexes
// in a test fixture. Existing r21-a and R-J tests remain unchanged controls.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { useChatStore } from './chat'
import { reset, cleanup } from '../../tests/chat-history-paging/final-gate-harness'
import { A, B, P, T, SID, STAMP, assistantRows, begin, bucket, native, pos, prose, rest, socket, token, tool } from '../../tests/chat-history-paging/s11-fold-harness'

beforeEach(() => { reset(); begin() })
afterEach(cleanup)

it('T82 B97: a genuinely witnessed first-send key outranks an earlier persisted text ID', async () => {
  useChatStore.getState().sendMessage('Question with a client-generated reply key')
  const optimistic = assistantRows()[0]?.id ?? Object.values(bucket().messagesById).find(row => row.role === 'assistant' && row.isStreaming)?.id
  expect(optimistic, 'instrument: real send generated the optimistic placeholder').toBeDefined()
  // TokenFrame permits an omitted message_id. That first legal chunk really
  // makes the generated first-send key witness text before the real ID arrives.
  socket().deliver({ type: 'token', session_id: SID, agent_id: 'mia', turn_id: 's11-turn', content: 'Witnessed first.' })
  expect(assistantRows().map(row => row.id)).toEqual([optimistic])
  token(A, ' Continued.')
  expect(bucket().messagesById[optimistic!].mergedReplayIds).toEqual([A])
  await rest([prose(1, A, 'Saved first. Continued.')])
  expect(assistantRows().map(row => row.id), 'r21-a witnessed first-send key has highest precedence').toEqual([optimistic])
  expect(bucket().messagesById[optimistic!].content).toBe('Witnessed first. Continued.')
  expect(bucket().historySourceAliases?.[pos(1)]).toBe(optimistic)
})

it('T82 B97: an occupied incompatible key stays separate and retains its actual source identity', async () => {
  token(A, 'Old turn.', { turn_id: 'old-turn' })
  await rest([prose(1, A, 'Different saved turn.')])
  const local = bucket().historySourceAliases?.[pos(1)]
  expect(local).toBeDefined(); expect(local).not.toBe(A)
  expect(bucket().messagesById[A].content).toBe('Old turn.')
  expect(bucket().messagesById[local!].content).toBe('Different saved turn.')
  expect(bucket().messagesById[local!].historyParts?.map(part => [part.sourceId, part.position])).toEqual([[A, pos(1)]])
  await rest([prose(1, A, 'Different saved turn.'), tool(2), prose(3, B, 'Second.')])
  expect(bucket().historySourceAliases?.[pos(1)], 'occupied-key exception outranks earliest known text ID').toBe(local)
  expect(bucket().messagesById[A].content).toBe('Old turn.')
  expect(bucket().messagesById[local!].content).toBe('Different saved turn.\n\nSecond.')
})

it('T82 B97: saved provisional→P atomically rekeys bound aliases, spans and pending tool owners', () => {
  native([tool(2)], true)
  expect(bucket().historySourceAliases?.[pos(2)]).toBe(T)
  socket().deliver(
    { type: 'subagent_state', session_id: SID, span_id: 's11-span', state: 'running', created_at: STAMP },
    { type: 'subagent_start', session_id: SID, span_id: 's11-span', parent_call_id: 's11-call', task_label: 'Keep the span owner', agent_id: 'mia' },
    { type: 'tool_call_start', session_id: SID, call_id: 's11-pending-call', tool: 'bash', agent_id: 'mia', params: { command: 'pending' } },
  )
  expect(bucket().spanBySpanId?.['s11-span']?.messageId, 'instrument: actual span settled onto the provisional owner').toBe(T)
  expect(bucket().toolCallOwnerMessageId?.['s11-pending-call']).toBe(T)
  const observed: { keys: string[]; aliases: Record<string, string> | undefined; toolOwners: Record<string, string> | undefined; spanOwner: string | undefined; prose: string[]; rowAliases: string[]; live: string[]; parts: unknown }[] = []
  const unsubscribe = useChatStore.subscribe(state => {
    const b = state.sessionsById[SID]
    if (b?.messagesById[P]) observed.push({ keys: Object.keys(b.messagesById), aliases: b.historySourceAliases, toolOwners: b.toolCallOwnerMessageId, spanOwner: b.spanBySpanId?.['s11-span']?.messageId, prose: Object.keys(b.mergedReplayMessageIds ?? {}), rowAliases: b.messagesById[P].mergedReplayIds ?? [], live: b.historyLiveOrder ?? [], parts: b.messagesById[P].historyParts?.map(part => [part.sourceId, part.position]) })
  })
  try { token(P, 'Answer.') } finally { unsubscribe() }
  expect(observed.length, 'instrument: subscription saw the actual transition').toBeGreaterThan(0)
  for (const update of observed) {
    expect(update.keys).not.toContain(T)
    expect(update.aliases?.[pos(2)]).toBe(P)
    expect(update.toolOwners?.['s11-call']).toBe(P)
    expect(update.toolOwners?.['s11-pending-call']).toBe(P)
    expect(update.spanOwner).toBe(P)
    expect(update.prose).toEqual([])
    expect(update.rowAliases).toEqual([])
    expect(update.live).not.toContain(T)
    expect(update.parts, 'tool source remains T; rekey does not rewrite source identity').toEqual([[T, pos(2)], [P, undefined]])
  }
  expect(bucket().messagesById[P].spans?.map(span => span.spanId)).toEqual(['s11-span'])
  expect(bucket().pendingSpanUpdatesBySpanId?.['s11-span']).toBeUndefined()
  token(P, ' More.')
  expect(bucket().messagesById[P].content).toBe('Answer. More.')
})

it('T82 B97: J2 B learns A in one commit without leaving a stale live-tail key or prose alias', () => {
  token(B, 'Second.')
  const updates: { key: string; aliases: string[]; live: string[]; owners: string[] }[] = []
  const unsubscribe = useChatStore.subscribe(state => {
    const b = state.sessionsById[SID]
    if (b?.historySourceAliases?.[pos(1)] === A && b.messagesById[A]) updates.push({ key: b.messagesById[A].id, aliases: Object.keys(b.mergedReplayMessageIds ?? {}), live: b.historyLiveOrder ?? [], owners: [pos(1), pos(2)].map(position => b.historySourceAliases![position]) })
  })
  try { native([prose(1, A, 'First.'), tool(2)], true) } finally { unsubscribe() }
  expect(updates.length).toBeGreaterThan(0)
  for (const update of updates) {
    expect(update.key).toBe(A)
    expect(update.aliases).toEqual([B])
    expect(update.live).not.toContain(B)
    expect(update.owners).toEqual([A, A])
  }
  expect(assistantRows().map(row => [row.id, row.content])).toEqual([[A, 'First.\n\nSecond.']])
  expect(bucket().messagesById[B], 'old display key is removed, not a duplicate hidden body owner').toBeUndefined()
  expect(bucket().messagesById[A].historyParts?.map(part => part.sourceId)).toEqual([A, T, B])
})
