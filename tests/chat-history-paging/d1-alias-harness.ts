// #1452 D1 test support. Real token resolver, history actions, adapters and
// replay reducer. Only socket/clock/fetch edges use existing test instruments.
import { expect, vi } from 'vitest'
import type { Message } from '@/lib/api/generated/openapi-types'
import type { SessionStateFrame } from '@/lib/api/generated/asyncapi-types'
import { useChatStore } from '@/store/chat'
import { replay } from './fixtures'
import { AVAILABLE, BOOT, SID, STAMP, bucket, complete, connect, entry, open, page, response, snapshotAt, socket } from './final-gate-harness'

export { bucket, SID, entry, page }
export const TURN = 'd1-combined-turn'
export const A = 'd1-source-A'
export const B = 'd1-source-B'
export const LIVE_A = 'Witnessed first part. '
export const LIVE_B = 'Witnessed second part.'
export const SAVED_A = 'Corrected saved first part. '
export const SAVED_B = 'Corrected saved second part.'

export function sources(a = SAVED_A, b = SAVED_B): [Message, Message] {
  return [entry(1, { id: A, role: 'assistant', agent_id: 'mia', turn_id: TURN, content: a }),
    entry(3, { id: B, role: 'assistant', agent_id: 'mia', turn_id: TURN, content: b })]
}
let sequence = 101
export function livePair(toolBetween = false, a = LIVE_A, b = LIVE_B) {
  sequence = 101 // Initial native snapshot cut is 100; live frames are contiguous.
  open([], [])
  socket().deliver({ type: 'token', session_id: SID, turn_id: TURN, agent_id: 'mia', message_id: A, content: a, seq: sequence++ })
  if (toolBetween) socket().deliver(
    { type: 'tool_call_start', session_id: SID, agent_id: 'mia', call_id: 'd1-between', tool: 'bash', params: { command: 'true' }, seq: sequence++ },
    { type: 'tool_call_result', session_id: SID, call_id: 'd1-between', tool: 'bash', result: { text: 'done' }, status: 'success', seq: sequence++ },
  )
  socket().deliver({ type: 'token', session_id: SID, turn_id: TURN, agent_id: 'mia', message_id: B, content: b, seq: sequence++ })
  expect(bucket().messagesById[A], 'instrument: real live resolver combines A and B').toMatchObject({
    content: a + (toolBetween ? '\n\n' : '') + b, turnId: TURN, agentId: 'mia', isStreaming: true,
  })
  expect(bucket().messagesById[A].mergedReplayIds).toContain(B)
  expect(bucket().messagesById[B], 'B is not separately displayed before the test action').toBeUndefined()
}
export function finish() { socket().deliver({ type: 'done', session_id: SID, turn_id: TURN, seq: sequence++ }) }

// Minimum request limit20 comes from D5; nineteen independent later user rows
// make a truthful latest page select only B from the combined assistant reply.
export function latestTail(b: Message): Message[] {
  return [b, ...Array.from({ length: 19 }, (_, i) => entry(4 + i, { id: `d1-tail-${i}`, role: 'user', content: `Later independent question ${i}` }))]
}
export async function rest(sources: Message[], kind: 'latest' | 'older' | 'refresh' = 'latest', earlier = false, later = false) {
  const selected = page(sources, { earlier, later, info: {
    mode: kind === 'refresh' ? 'window_refresh' : kind, limit: kind === 'refresh' ? 100 : 20,
    min_rows: 1, stop_reason: earlier ? 'row_target' : 'end',
  } })
  const reads = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => response(selected))
  vi.stubGlobal('fetch', reads)
  const store = useChatStore.getState()
  if (kind === 'older') await store.loadOlderHistory(SID, { limit: 20, min_rows: 1 })
  else if (kind === 'refresh') await store.refreshLoadedHistory(SID)
  else await store.loadLatestHistory(SID, { limit: 20, min_rows: 1 })
  expect(reads, 'instrument: the actual history action performed exactly one network read').toHaveBeenCalledTimes(1)
  expect(bucket().history.requests[kind].status, 'the real page must commit, not fail in the test instrument').toBe('idle')
  return reads
}
export function commitSnapshot(sources: Message[], running = false) {
  const selected = page(sources, { info: { limit: 20, min_rows: 1 } })
  const state: SessionStateFrame = { ...AVAILABLE, ...(running ? { active_turn: { turn_id: TURN, agent_id: 'mia', started_at: STAMP } } : {}) }
  const wire = connect(); wire.attach()
  wire.deliver({ ...snapshotAt(selected), seq: 106, boot_id: BOOT }, state,
    ...sources.map(source => replay(source)), { type: 'done', session_id: SID }, { ...complete(), seq: 106 })
  expect(bucket().history.historyLoaded, 'instrument: real snapshot committed').toBe(true)
}
export function assistantRows() {
  return bucket().messageOrder.map(id => bucket().messagesById[id]).filter(row => row.role === 'assistant')
}
export function readable() { return assistantRows().map(row => row.content).join('') }
