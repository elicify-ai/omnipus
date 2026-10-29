// S11 negatives and transparent classes. Real live handlers, REST actions and
// native replay reducers; no source-code inspection is used as a test oracle.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { Message } from '@/lib/api/generated/openapi-types'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from './chat'
import { reset, cleanup, response } from '../../tests/chat-history-paging/final-gate-harness'
import { page } from '../../tests/chat-history-paging/fixtures'
import { A, B, P, T, TURN, SID, STAMP, assistantRows, begin, bucket, empty, liveTool, native, pos, prose, rest, socket, sourceFrames, tailPadding, token, tool } from '../../tests/chat-history-paging/s11-fold-harness'

beforeEach(() => { reset(); begin() })
afterEach(cleanup)

for (const ending of ['turn_canceled', 'error', 'interrupted', 'terminal_outcome'] as const) {
  it.each(['before', 'after'] as const)(`T76 B92: ${ending} ${'%s'} a text candidate never bridges the tool-only segment`, async side => {
    const tools = [empty(1), tool(2, 's11-T1', 's11-c1'), empty(3, 's11-E2'), tool(4, 's11-T2', 's11-c2')]
    const outcome = prose(5, 's11-boundary', ending === 'turn_canceled' ? '' : 'Ended.', ending === 'turn_canceled' ? { role: 'system', type: 'turn_canceled' } : ending === 'terminal_outcome' ? { terminal_outcome: true } : { status: ending })
    const candidate = prose(side === 'after' ? 6 : 0, P, 'Independent answer.')
    const sources = side === 'after' ? [...tools, outcome, candidate] : [candidate, { ...outcome, history_position: pos(0.5) }, ...tools]
    await rest(sources)
    const toolRows = assistantRows().filter(row => row.tool_calls?.some(call => call.id === 's11-c1' || call.id === 's11-c2'))
    expect(toolRows.map(row => row.id), 'the entire no-text segment remains one tool-only row').toEqual(['s11-T1'])
    expect(toolRows[0].content).toBe('')
    expect(toolRows[0].tool_calls?.map(call => [call.id, Reflect.get(call, 'textOffset')])).toEqual([['s11-c1', 0], ['s11-c2', 0]])
    expect(toolRows[0].mergedReplayIds ?? []).toEqual([])
    expect(bucket().messagesById[P].content).toBe('Independent answer.')
    expect(bucket().historySourceAliases?.[candidate.history_position!]).toBe(P)
    expect(bucket().messageOrder.indexOf(P) < bucket().messageOrder.indexOf('s11-T1')).toBe(side === 'before')
    if (ending === 'turn_canceled') expect(assistantRows().some(row => row.id === 's11-boundary')).toBe(false)
    else expect(bucket().messagesById['s11-boundary'].content).toBe('Ended.')
  })
}

it.each(['REST', 'snapshot', 'live'] as const)('T77 B93: %s same-turn steer is an actual segment boundary', async carrier => {
  const steer = prose(3, 's11-user-steer', 'Also add a third sentence.', { role: 'user' })
  const sources = [prose(1, A, 'First.'), tool(2), steer, prose(4, B, 'Second.')]
  if (carrier === 'live') {
    token(A, 'First.'); liveTool()
    // The canonical live echo is user_message, not a replay_message outside
    // an attach. Keep its independent id/text and real public frame handler.
    socket().deliver({ type: 'user_message', session_id: SID, id: steer.id, content: steer.content!, timestamp: steer.timestamp, agent_id: 'mia' })
    token(B, 'Second.')
  } else if (carrier === 'snapshot') native(sources)
  else await rest(sources)
  expect(bucket().messageOrder.filter(id => [A, B, steer.id].includes(id))).toEqual([A, steer.id, B])
  expect(assistantRows().map(row => [row.id, row.content])).toEqual([[A, 'First.'], [B, 'Second.']])
  expect(bucket().toolCallOwnerMessageId?.['s11-call']).toBe(A)
})

const notices: { name: string; source: Message; live?: ServerFrame }[] = [
  { name: 'context_window_notice', source: prose(2, 's11-notice', 'Context notice.', { type: 'context_window_notice', role: 'system', context_window_notice: { kind: 'mid_turn', message: 'Context notice.' } }) },
  { name: 'provider_fallback', source: prose(2, 's11-notice', 'Answered by fallback because primary was unavailable.', { type: 'provider_fallback', role: 'system', model: 'fallback', unavailable_model: 'primary', unavailable_code: 'rate_limited' }), live: { type: 'provider_fallback', session_id: SID, turn_id: TURN, answered_model: 'fallback', unavailable_model: 'primary', unavailable_code: 'rate_limited' } },
  { name: 'judge_verdict', source: prose(2, 's11-notice', JSON.stringify({ id: '00000000-0000-4000-8000-000000000069', scope: 'goal', round: 1, met: true, per_criterion: [], model: 'fixture', judged_at: STAMP, judge_agent_id: 'judge', session_id: SID }), { type: 'judge_verdict', role: 'system' }) },
  { name: 'helper_event', source: prose(2, 's11-notice', 'Helper finished.', { type: 'system', role: 'system', system_subtype: 'helper_event', helper_event: { helper_session_id: 's11-helper', kind: 'finished', text: 'Helper finished.', created_at: STAMP } }) },
  { name: 'goal_outcome', source: prose(2, 's11-notice', 'Goal met.', { type: 'system', role: 'system', system_subtype: 'goal_outcome', goal_outcome: { goal_id: 's11-goal', goal_text: 'Fixture goal', ending: 'met', rounds_used: 1, max_rounds: 2, ended_at: STAMP } }) },
  { name: 'browser_handover_notice', source: prose(2, 's11-notice', 'Operator took control.', { type: 'system', role: 'system', system_subtype: 'browser_handover_notice' }) },
]
for (const notice of notices) {
  it.each(['live', 'REST', 'snapshot', 'page-edge'] as const)(`T78 B93: ${notice.name} is transparent through %s, never part of prose`, async carrier => {
    const a = prose(1, A, 'First.'), b = prose(3, B, 'Second.')
    if (carrier === 'live') {
      token(A, 'First.')
      // Strip only the history origin from this class's real shared carrier.
      const frames = notice.live ? [notice.live] : sourceFrames(notice.source).map(frame => {
        const copy = { ...frame }; if ('history_origin' in copy) delete copy.history_origin; return copy
      })
      socket().deliver(...frames); token(B, 'Second.')
      expect(assistantRows().map(row => [row.id, row.content]), 'a real live-handler closure contradicts the S11a table and must be reported, never accepted by changing this oracle').toEqual([[A, 'First.Second.']])
    } else if (carrier === 'snapshot') native([a, notice.source, b])
    else if (carrier === 'REST') await rest([a, notice.source, b])
    else {
      await rest([notice.source, b, ...tailPadding().slice(0, 18)], 'latest', true)
      useChatStore.getState().enterHistoryReading(SID)
      await rest([a], 'older', false, true)
    }
    expect(assistantRows().map(row => [row.id, row.content])).toEqual([[A, 'First.Second.']])
    const row = bucket().messagesById[A]
    expect(row.historyParts?.map(part => part.sourceId), 'notice is never a text/tool part').toEqual([A, B])
    const drawn = bucket().messageOrder.filter(id => id === A || bucket().messagesById[id].role === 'system')
    expect(drawn[0], 'S8 first-bound slot puts the information notice AFTER the combined answer').toBe(A)
    expect(drawn).toHaveLength(2)
  })
}

it.each(['REST', 'snapshot', 'page-edge'] as const)('T78 B93: %s clear marker splits same-turn text rather than becoming a transparent notice', async carrier => {
  const sources = [prose(1, A, 'First.'), prose(2, 's11-clear', 'Conversation context cleared', { role: 'system', type: 'system' }), prose(3, B, 'Second.')]
  if (carrier === 'REST') await rest(sources)
  else if (carrier === 'snapshot') native(sources)
  else {
    await rest([...sources.slice(1), ...tailPadding().slice(0, 18)], 'latest', true)
    useChatStore.getState().enterHistoryReading(SID)
    const read = vi.fn(async () => response(page([sources[0]], { later: true, info: { mode: 'older', limit: 20, min_rows: 1, stop_reason: 'end' } })))
    vi.stubGlobal('fetch', read)
    await useChatStore.getState().showEarlierHistory(SID, pos(2), { limit: 20, min_rows: 1 })
    expect(read, 'instrument: explicit clear crossing actually loaded the source before the marker').toHaveBeenCalledTimes(1)
    expect(bucket().history.requests.older.status).toBe('idle')
  }
  expect(bucket().messageOrder.filter(id => [A, 's11-clear', B].includes(id))).toEqual([A, 's11-clear', B])
  expect(assistantRows().map(row => [row.id, row.content])).toEqual([[A, 'First.'], [B, 'Second.']])
})

it.each(['REST', 'snapshot', 'live', 'page-edge'] as const)('T78 B93: %s silent accounting and owned span/delegate facts never become prose parts', async carrier => {
  const start = prose(4, 's11-span-start', '', { role: 'system', type: 'system', system_subtype: 'subagent_start', subagent_start: { type: 'subagent_start', session_id: SID, span_id: 's11-transparent-span', parent_call_id: 's11-call', task_label: 'Transparent helper', agent_id: 'jim' } })
  const state = prose(5, 's11-span-state', '', { role: 'system', type: 'system', system_subtype: 'subagent_state', subagent_state: { type: 'subagent_state', session_id: SID, span_id: 's11-transparent-span', state: 'running', created_at: STAMP } })
  const message = prose(6, 's11-span-message', '', { role: 'system', type: 'system', system_subtype: 'subagent_message', subagent_message: { type: 'subagent_message', session_id: SID, span_id: 's11-transparent-span', message_id: 's11-progress', kind: 'progress', sender_identity: 'helper', untrusted_origin: false, text: 'Progress remains a fact.', created_at: STAMP } })
  const end = prose(7, 's11-span-end', '', { role: 'system', type: 'system', system_subtype: 'subagent_end', subagent_end: { type: 'subagent_end', session_id: SID, span_id: 's11-transparent-span', parent_call_id: 's11-call', status: 'success', final_result: 'Finished.' } })
  const accounting = empty(2, 's11-accounting'), a = prose(1, A, 'First.'), t = tool(3), b = prose(8, B, 'Second.')
  const sources = [a, accounting, t, start, state, message, end, b]
  if (carrier === 'REST') await rest(sources)
  else if (carrier === 'snapshot') native(sources)
  else if (carrier === 'page-edge') {
    await rest([b, ...tailPadding()], 'latest', true)
    useChatStore.getState().enterHistoryReading(SID)
    await rest(sources.slice(0, -1), 'older', false, true)
  } else {
    token(A, 'First.'); liveTool()
    for (const fact of [accounting, start, state, message, end]) socket().deliver(...sourceFrames(fact).map(frame => {
      const copy = { ...frame }; if ('history_origin' in copy) delete copy.history_origin; return copy
    }))
    token(B, 'Second.')
  }
  expect(assistantRows().map(row => [row.id, row.content])).toEqual([[A, 'First.\n\nSecond.']])
  const owner = bucket().messagesById[A]
  if (carrier !== 'live') expect(owner.historyParts?.map(part => part.sourceId)).toEqual([A, T, B])
  // Team-lead 2026-10-29 Choice A: S11a preserves Q5/Q6's saved
  // start-row owner. Live still owns the span on A; that parity gap is
  // explicit, not permission to move a saved helper as a side effect of S11.
  const spanOwner = carrier === 'live' ? A : start.id
  expect(bucket().messageOrder.flatMap(id => bucket().messagesById[id].spans?.map(span =>
    [id, span.spanId, span.status, span.status === 'running' ? undefined : span.finalResult]) ?? []),
  'exactly one completed span, on its carrier-specific owner; no duplicate or lost fact').toEqual([[spanOwner, 's11-transparent-span', 'success', 'Finished.']])
  if (carrier !== 'live') {
    expect(bucket().historySourceAliases?.[pos(4)], 'the persisted start retains its own source-position owner').toBe(start.id)
    expect(bucket().messagesById[start.id].historyPosition).toBe(pos(4))
  }
  expect(owner.content).not.toContain('Progress remains a fact.')
})

it('T78 B93/S11c: actual outbound clear acknowledgements and done refetch use the same post-marker folding', async () => {
  token('s11-before-clear', 'Before clear.'); socket().deliver({ type: 'done', session_id: SID, turn_id: TURN })
  useChatStore.getState().sendMessage('/clear')
  const sent = socket().socket.sent.filter(frame => frame.type === 'message').at(-1)
  expect(sent?.content, 'instrument: real clear operation was sent, no fake pushed clear marker').toBe('/clear')
  expect(sent?.client_message_id).toBeTypeOf('string')
  // Frozen clear-transcript-refetch V1: keep the unrepresented live tail
  // after this operation's user row. Capture its identity before the read;
  // never accept arbitrary extra rows merely because they follow A.
  const clearIndex = bucket().messageOrder.indexOf(String(sent!.client_message_id))
  expect(clearIndex, 'instrument: the sent clear has its own local user row').toBeGreaterThanOrEqual(0)
  const tail = bucket().messageOrder.slice(clearIndex + 1)
  expect(tail, 'instrument: an idle send minted exactly one assistant placeholder').toHaveLength(1)
  expect(bucket().messagesById[tail[0]]).toMatchObject({ role: 'assistant', content: '', status: 'streaming', isStreaming: true })
  const marker = prose(0, 's11-new-clear', 'Conversation context cleared', { role: 'system', type: 'system', turn_id: undefined })
  const selected = page([marker, prose(1, A, 'First.'), tool(2), prose(3, B, 'Second.')], { rows: 4, info: { limit: 40, min_rows: 20, latest_clear: { entry_id: marker.id, history_position: pos(0) } } })
  const read = vi.fn(async () => response(selected)); vi.stubGlobal('fetch', read)
  socket().deliver({ type: 'message_status', session_id: SID, client_message_id: String(sent!.client_message_id), state: 'received' }, { type: 'done', session_id: SID, turn_id: 's11-clear-operation' })
  await vi.waitFor(() => expect(bucket().historySourceAliases?.[pos(3)], 'actual clear-refetch committed the new projection').toBe(A))
  expect(read).toHaveBeenCalledTimes(1)
  expect(assistantRows().map(row => [row.id, row.content])).toEqual([[A, 'First.\n\nSecond.']])
  expect(bucket().messageOrder, 'folded server projection, then exactly the captured live tail').toEqual([marker.id, A, tail[0]])
  const placeholder = bucket().messagesById[tail[0]]
  expect(placeholder).toMatchObject({ role: 'assistant', content: '', status: 'done', isStreaming: false })
  expect(placeholder.tool_calls ?? []).toEqual([])
  expect(placeholder.spans ?? []).toEqual([])
  expect(placeholder.historyPosition).toBeUndefined()
  expect(bucket().messagesById[A].historyParts?.map(part => part.sourceId)).toEqual([A, T, B])
  expect(bucket().toolCallOwnerMessageId?.['s11-call']).toBe(A)
})

for (const carrier of ['REST', 'snapshot'] as const) {
  it.each(['tool-X_text-Y', 'text-Y_tool-X', 'turn1_turn2', 'turn2_turn1', 'known_unknown_known'] as const)(`T79 B94: ${carrier} %s does not bridge different known producers/turns`, async kind => {
    const x = tool(1, T, 's11-call', { agent_id: 'mia' })
    const y = prose(3, P, 'Other answer.', { agent_id: 'jim' })
    const sources = kind === 'tool-X_text-Y' ? [x, y] : kind === 'text-Y_tool-X' ? [{ ...y, history_position: pos(1) }, { ...x, history_position: pos(3) }]
      : kind === 'known_unknown_known' ? [x, tool(2, 's11-unknown-agent', 's11-call-u', { agent_id: '' }), y]
        : [x, { ...y, agent_id: 'mia', turn_id: kind === 'turn1_turn2' ? 's11-turn2' : 's11-turn0' }]
    if (carrier === 'REST') await rest(sources); else native(sources)
    const xOwner = bucket().historySourceAliases?.[sources.find(source => source.id === x.id)!.history_position!]
    const yOwner = bucket().historySourceAliases?.[sources.find(source => source.id === y.id)!.history_position!]
    expect(xOwner).toBeDefined(); expect(yOwner).toBeDefined(); expect(xOwner, 'different known producers/turns cannot become one row through an unknown bridge').not.toBe(yOwner)
    expect(assistantRows().flatMap(row => row.tool_calls ?? []).filter(call => call.id === 's11-call')).toHaveLength(1)
    expect(assistantRows().filter(row => row.content === 'Other answer.')).toHaveLength(1)
  })
  it.each(['unknown-first', 'unknown-second', 'known-own-origin', 'different-prior-turn'] as const)(`T80 B95: ${carrier} %s uses only the source's own known turn`, async kind => {
    const sources = kind === 'different-prior-turn' ? [tool(1, T, 's11-old-call', { turn_id: 'old-turn' }), empty(2), prose(3, P, 'Answer.')]
      : [tool(1, T, 's11-call', { turn_id: kind === 'unknown-first' ? undefined : TURN }), prose(3, P, 'Answer.', { turn_id: kind === 'unknown-second' ? undefined : TURN })]
    if (carrier === 'REST') await rest(sources); else native(sources)
    expect(assistantRows().map(row => row.id)).toEqual(kind === 'known-own-origin' ? [P] : [T, P])
    expect(bucket().historySourceAliases?.[pos(1)]).toBe(kind === 'known-own-origin' ? P : T)
    expect(bucket().messagesById[P].content).toBe('Answer.')
  })
}
