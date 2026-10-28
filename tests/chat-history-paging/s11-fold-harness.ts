// S11 test instruments. Expected keys/text/offsets are supplied as literals by
// tests; this file constructs only inputs and drives real public store actions.
import { expect, vi } from 'vitest'
import type { Message } from '@/lib/api/generated/openapi-types'
import type { JudgeVerdictFrame, ServerFrame, SessionStateFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import { entry, expectValidPage, page, pos, replay, snapshot, STAMP } from './fixtures'
import { AVAILABLE, BOOT, SID, bucket, connect, response, socket } from './final-gate-harness'

export { SID, STAMP, BOOT, bucket, pos, socket }
export const TURN = 's11-turn'
export const A = 's11-A', B = 's11-B', P = 's11-P', T = 's11-T', E = 's11-E'
export const prose = (rank: number, id: string, text: string, extra: Partial<Message> = {}) => entry(rank, { id, role: 'assistant', agent_id: 'mia', turn_id: TURN, content: text, ...extra })
export const empty = (rank: number, id = E, extra: Partial<Message> = {}) => prose(rank, id, '', extra)
export const tool = (rank: number, id = T, callId = 's11-call', extra: Partial<Message> = {}) => prose(rank, id, '', { type: 'tool_call', tool_calls: [{ id: callId, tool: 'bash', status: 'success', content_state: 'full', parameters: { command: 'echo fixture' }, result: { text: 'fixture' } }], ...extra })
export const textToolText = () => [prose(1, A, 'First.'), tool(2), prose(3, B, 'Second.')]
export const toolFirst = () => [empty(1), tool(2), prose(3, P, 'Answer.')]

export function sourceFrames(source: Message): ServerFrame[] {
  const history_origin = { position: source.history_position!, entry_id: source.id, ...(source.turn_id ? { turn_id: source.turn_id } : {}) }
  // A genuine tool-only source emits no synthetic turn-bearing replay_message.
  if (source.type === 'tool_call' && !source.content) return (source.tool_calls ?? []).flatMap(call => [
    { type: 'tool_call_start' as const, session_id: SID, call_id: call.id, tool: call.tool, params: call.parameters ?? {}, ...(source.agent_id ? { agent_id: source.agent_id } : {}), history_origin },
    { type: 'tool_call_result' as const, session_id: SID, call_id: call.id, tool: call.tool, status: call.status === 'cancelled' ? 'cancelled' as const : call.status === 'error' ? 'error' as const : 'success' as const, result: call.result ?? null, ...(source.agent_id ? { agent_id: source.agent_id } : {}), history_origin },
  ])
  if (source.type === 'context_window_notice') return [{ type: 'context_window_notice', session_id: SID, entry_id: source.id, timestamp: source.timestamp, agent_id: source.agent_id, turn_id: source.turn_id!, notice: source.context_window_notice!, history_origin }]
  if (source.type === 'provider_fallback') return [{ type: 'replay_provider_fallback', session_id: SID, entry_id: source.id, timestamp: source.timestamp, message: source.content!, answered_model: source.model, unavailable_model: source.unavailable_model, unavailable_code: source.unavailable_code, history_origin }]
  if (source.type === 'judge_verdict') {
    const verdict: JudgeVerdictFrame = JSON.parse(source.content!)
    return [{ ...verdict, type: 'judge_verdict', session_id: SID, history_origin }]
  }
  if (source.system_subtype === 'browser_handover_notice') return [{ type: 'browser_handover_notice', session_id: SID, message_id: source.id, text: source.content!, history_origin }]
  if (source.system_subtype === 'helper_event') return [{ type: 'helper_event', session_id: SID, message_id: source.id, helper_event: source.helper_event!, history_origin }]
  if (source.system_subtype === 'goal_outcome') return [{ type: 'goal_outcome', session_id: SID, message_id: source.id, outcome: source.goal_outcome!, history_origin }]
  if (source.subagent_start) return [{ ...source.subagent_start, session_id: SID, history_origin }]
  if (source.subagent_state) return [{ ...source.subagent_state, session_id: SID, history_origin }]
  if (source.subagent_message) return [{ ...source.subagent_message, session_id: SID, history_origin }]
  if (source.subagent_end) return [{ ...source.subagent_end, session_id: SID, history_origin }]
  if (source.type === 'turn_canceled') return [{ ...replay(source), role: 'turn_canceled', history_origin }]
  return [{ ...replay(source), history_origin }]
}
export function rowWeight(sources: Message[]) {
  // D4's cancellation source is retained context, never a logical drawn row.
  return sources.reduce((sum, source) => sum + (source.type === 'turn_canceled' ? 0 : (source.content || !source.tool_calls?.length ? 1 : 0) + (source.tool_calls?.length ?? 0)), 0)
}
let cut = 100
export function begin() {
  cut = 100
  const wire = connect(); wire.attach()
  wire.deliver({ ...snapshot(page([], { info: { limit: 20, min_rows: 1 } })), seq: cut, boot_id: BOOT }, AVAILABLE,
    { type: 'done', session_id: SID }, { type: 'catch_up_complete', session_id: SID, seq: cut, boot_id: BOOT, mode: 'snapshot' })
  return wire
}
export function snapshotStart(sources: Message[], active = false, extraFrames: ServerFrame[] = [], earlier = false) {
  const limit = Math.max(20, sources.length)
  const selected = page(sources, { earlier, rows: rowWeight(sources), info: { limit, min_rows: 1 } })
  expectValidPage(selected, sources.map(source => source.history_position!), rowWeight(sources))
  cut += 10
  const state: SessionStateFrame = { ...AVAILABLE, ...(active ? { active_turn: { turn_id: TURN, agent_id: 'mia', started_at: STAMP } } : {}) }
  expect(socket().connection.send({ type: 'attach_session', session_id: SID, history_limit: limit, history_min_rows: 1 }), 'instrument: exact legal snapshot sizing sent on real connection').toBe(true)
  socket().deliver({ ...snapshot(selected), seq: cut, boot_id: BOOT }, state, ...sources.flatMap(sourceFrames), ...extraFrames, { type: 'done', session_id: SID })
  return () => socket().deliver({ type: 'catch_up_complete', session_id: SID, seq: cut, boot_id: BOOT, mode: 'snapshot' })
}
export function native(sources: Message[], active = false, extras: ServerFrame[] = [], earlier = false) {
  snapshotStart(sources, active, extras, earlier)()
  expect(bucket().cursor?.seq, 'instrument: real native snapshot committed, not schema/row-count rejection').toBe(cut)
  expect(bucket().history.historyLoaded).toBe(true)
  return cut
}
export async function rest(sources: Message[], mode: 'latest' | 'older' | 'newer' | 'refresh' = 'latest', earlier = false, later = false, limit = 20) {
  const selected = page(sources, { earlier, later, rows: rowWeight(sources), info: { mode: mode === 'refresh' ? 'window_refresh' : mode, limit: mode === 'refresh' ? 100 : limit, min_rows: mode === 'refresh' ? 20 : 1, stop_reason: mode === 'refresh' ? 'range_end' : (mode === 'newer' ? later : earlier) ? 'row_target' : 'end' } })
  expectValidPage(selected, sources.map(source => source.history_position!), rowWeight(sources))
  const read = vi.fn(async () => response(selected)); vi.stubGlobal('fetch', read)
  const store = useChatStore.getState()
  if (mode === 'refresh') await store.refreshLoadedHistory(SID)
  else if (mode === 'older') await store.loadOlderHistory(SID, { limit, min_rows: 1 })
  else if (mode === 'newer') await store.loadNewerHistory(SID, { limit, min_rows: 1 })
  else await store.loadLatestHistory(SID, { limit, min_rows: 1 })
  expect(read, 'instrument: actual network read, not a disabled edge action').toHaveBeenCalledTimes(1)
  expect(bucket().history.requests[mode].status, 'instrument: page committed without a protocol/setup error').toBe('idle')
}
export function assistantRows() {
  return bucket().messageOrder.map(id => bucket().messagesById[id]).filter(row => row.role === 'assistant' && (row.content || row.tool_calls?.length))
}
export function segment(key: string, text: string, sourceIds: string[], positions: string[], calls: string[], offsets: number[], aliases: string[] = []) {
  expect(assistantRows().map(row => row.id), 'S11: exact drawn assistant row inventory').toEqual([key])
  const row = bucket().messagesById[key]
  expect(row.content, 'independent exact text and joiners').toBe(text)
  expect(row.tool_calls?.map(call => call.id) ?? [], 'one call per real source, in source order').toEqual(calls)
  expect(row.tool_calls?.map(call => Reflect.get(call, 'textOffset')) ?? [], 'S10/S11: anchors derive from owning parts').toEqual(offsets)
  expect(row.historyParts?.map(part => part.sourceId), 'transparent accounting/facts are never parts').toEqual(sourceIds)
  expect(row.historyParts?.map(part => part.position)).toEqual(positions)
  expect(positions.map(position => bucket().historySourceAliases?.[position])).toEqual(positions.map(() => key))
  expect(row.mergedReplayIds ?? [], 'only registered prose IDs, never a tool source or call ID').toEqual(aliases)
  expect(Object.keys(bucket().mergedReplayMessageIds ?? {}).sort()).toEqual([...aliases].sort())
  expect(calls.map(call => bucket().toolCallOwnerMessageId?.[call])).toEqual(calls.map(() => key))
}
export function token(id: string, content: string, extras: Record<string, unknown> = {}) {
  socket().deliver({ type: 'token', session_id: SID, agent_id: 'mia', turn_id: TURN, message_id: id, content, ...extras })
}
export function liveTool(call = 's11-call') {
  socket().deliver({ type: 'tool_call_start', session_id: SID, call_id: call, agent_id: 'mia', tool: 'bash', params: { command: 'echo fixture' } },
    { type: 'tool_call_result', session_id: SID, call_id: call, tool: 'bash', status: 'success', result: { text: 'fixture' } })
}
export function done() { socket().deliver({ type: 'done', session_id: SID, turn_id: TURN }) }
export function tailPadding() { return Array.from({ length: 19 }, (_, i) => entry(1000 + i, { id: `s11-padding-tail-${i}`, role: 'user' })) }
export function headPadding() { return Array.from({ length: 19 }, (_, i) => entry(-1000 + i, { id: `s11-padding-head-${i}`, role: 'user' })) }
