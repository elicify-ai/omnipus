// T62b/A2.2: malformed markers cross a real Message adapter and real native
// history-origin tool-result transport, never just a standalone Zod schema.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { ServerFrame } from './generated/asyncapi-types'
import { getApiSchemaErrorCount, resetApiSchemaErrorCount } from './http'
import { fetchSessionMessages } from './sessions'
import { getDroppedFrameCount, resetDroppedFrameCount } from '@/lib/ws'
import { entry, page, pos, replay } from '../../../tests/chat-history-paging/fixtures'
import { AVAILABLE, bucket, cleanup, complete, connect, reset, SID, snapshotAt } from '../../../tests/chat-history-paging/final-gate-harness'

const bad = [
  ['missing size', { _history_preview: true, preview: 'INVALID_MARKER_CANARY' }],
  ['wrong discriminator type', { _history_preview: 'true', original_size_bytes: 51201, preview: 'INVALID_MARKER_CANARY' }],
  ['extra field', { _history_preview: true, original_size_bytes: 51201, preview: 'INVALID_MARKER_CANARY', extra: true }],
  ['multibyte over4096 bytes', { _history_preview: true, original_size_bytes: 51201, preview: '€'.repeat(2048) }],
] as const
beforeEach(() => { reset(); resetApiSchemaErrorCount(); resetDroppedFrameCount() })
afterEach(cleanup)
function source(result: Record<string, unknown>) {
  return entry(1, { id: 'nested-marker-source', role: 'assistant', content: '', tool_calls: [
    { id: 'nested-marker-call', tool: 'read_file', status: 'success', content_state: 'full', parameters: {}, result },
  ] })
}
for (const [name, result] of bad) {
  it(`T62b: REST Message degrades ${name} to the existing positioned placeholder and diagnostic`, async () => {
    vi.stubGlobal('fetch', async () => new Response(JSON.stringify(page([source(result)])), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    const before = getApiSchemaErrorCount()
    const actual = await fetchSessionMessages(SID)
    expect(actual.entries).toHaveLength(1)
    expect(actual.entries[0]).toMatchObject({ id: 'nested-marker-source', role: 'system', content: 'This message could not be displayed.', historyPosition: pos(1) })
    expect(getApiSchemaErrorCount(), 'real malformed-item diagnostic counter advances').toBeGreaterThan(before)
    expect(JSON.stringify(actual.entries)).not.toContain('INVALID_MARKER_CANARY')
  })
  it(`T62b: native history-origin result degrades ${name}, not a normal result or silent frame drop`, () => {
    const selected = page([source(result)], { rows: 1 })
    const wire = connect(); wire.attach()
    const before = getApiSchemaErrorCount() + getDroppedFrameCount()
    const frames: ServerFrame[] = [
      { ...snapshotAt(selected), seq: 100 }, AVAILABLE, replay(source(result)),
      { type: 'tool_call_start', session_id: SID, call_id: 'nested-marker-call', tool: 'read_file', params: {}, history_origin: { position: pos(1), entry_id: 'nested-marker-source' } },
      { type: 'tool_call_result', session_id: SID, call_id: 'nested-marker-call', tool: 'read_file', result, status: 'success', history_origin: { position: pos(1), entry_id: 'nested-marker-source' } },
      { type: 'done', session_id: SID }, complete(),
    ]
    // Deliberately malformed input bypasses ONLY TestSocket.receive's positive
    // fixture pre-validator. Actual WsConnection parsing/reduction stays real.
    frames.forEach(frame => wire.socket.onmessage?.(new MessageEvent('message', { data: JSON.stringify(frame) })))
    wire.flush()
    expect(bucket().history.historyLoaded, 'one bad result must not reject the whole valid snapshot').toBe(true)
    const rows = bucket().messageOrder.map(id => bucket().messagesById[id])
    expect(rows.find(row => row.historyPosition === pos(1))?.content, 'bad item is visible rather than disappearing with a dropped frame').toBe('This message could not be displayed.')
    expect(getApiSchemaErrorCount() + getDroppedFrameCount()).toBeGreaterThan(before)
    expect(JSON.stringify(rows)).not.toContain('INVALID_MARKER_CANARY')
  })
}
it('T62b control: ordinary non-marker object survives the real Message adapter unchanged', async () => {
  const value = { text: 'ordinary structured output', extra: true }
  vi.stubGlobal('fetch', async () => new Response(JSON.stringify(page([source(value)])), { status: 200, headers: { 'Content-Type': 'application/json' } }))
  const actual = await fetchSessionMessages(SID)
  expect(actual.entries[0].tool_calls?.[0].result).toEqual(value)
  expect(getApiSchemaErrorCount()).toBe(0)
})
