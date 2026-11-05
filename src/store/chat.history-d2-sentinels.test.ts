// T66/A2.4: marker pass-through, byte-based browser clamp and both existing
// delegation consumers. Exact outcomes are from A2.4, not observed output.
import { expect, it } from 'vitest'
import { clampToolResult } from './chat/messages'
import { deriveDelegationEvents } from '@/lib/delegationEvents'
import { splitAnchoredDelegationEvents } from '@/lib/delegationEventPlacement'
import type { ToolCall } from '@/lib/api'

it.each([50000, 51199, 51200])('T66: an inline server result of%i encoded bytes is never shortened a second time', bytes => {
  const value = { text: 'x'.repeat(bytes - 11) } //11 compact JSON syntax bytes.
  expect(new TextEncoder().encode(JSON.stringify(value)).length).toBe(bytes)
  expect(clampToolResult(value), 'D2 aligns the browser with51200 UTF8 bytes, not50000 characters').toBe(value)
})
it('T66: byte-based clamp sees oversized multibyte JSON even below50000 characters', () => {
  const value = { text: '€'.repeat(17064) } //51192 content +11 syntax =51203.
  expect(JSON.stringify(value).length).toBeLessThan(50000)
  expect(new TextEncoder().encode(JSON.stringify(value)).length).toBe(51203)
  expect(clampToolResult(value)).toMatchObject({ _truncated_client: true, original_size_bytes: 51203 })
})
it('T66: history previews and live offload refs pass through unchanged', () => {
  const marker = { _history_preview: true, original_size_bytes: 2000000, preview: '{"content":"preview' }
  const ref = { _ref: true, ref: 'existing-live-reference', original_size_bytes: 90000, preview: 'existing offload preview' }
  expect(clampToolResult(marker)).toBe(marker)
  expect(clampToolResult(ref)).toBe(ref)
})
it('T66: history-preview bash results preserve lifecycle derivation AND their anchored placement', () => {
  const result = (status: string) => ({ _history_preview: true, original_size_bytes: 80000,
    preview: `{"sessionId":"d2-bash-session","status":"${status}","output":"${'x'.repeat(80)}` })
  const calls: ToolCall[] = [
    { id: 'd2-launch', tool: 'bash', status: 'success', params: { command: 'echo example', run_in_background: true }, result: result('running') },
    { id: 'd2-read', tool: 'bash', status: 'success', params: { action: 'read', session_id: 'd2-bash-session' }, result: result('killed') },
  ]
  const events = deriveDelegationEvents({ sessionId: 'd2-parent', messages: [{ id: 'd2-message', timestamp: '2026-10-21T10:00:00Z', toolCalls: calls }] })
  expect(events.map(event => event.kind), 'new sentinel has the same lifecycle semantics as the existing preview sentinels').toEqual(['bash_launched', 'bash_stopped'])
  const placed = splitAnchoredDelegationEvents(events, 'd2-message', { toolCalls: calls.map(call => ({ ...call, textOffset: 0 })) })
  expect(placed.byCall.get('d2-launch')?.map(event => event.kind), 'placement also reads the sentinel preview, not an unrelated call slot').toEqual(['bash_launched', 'bash_stopped'])
  expect(placed.trailing).toEqual([])
  expect(JSON.stringify(events)).not.toContain('x'.repeat(40))
})
