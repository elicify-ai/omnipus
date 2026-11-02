// 6c UAT U-D1-6 (a): a tool call the backend settled to the contract status
// "interrupted" (a restart cut it off) must never reload as a running spinner.
// REAL request/validation/adapters; only global fetch/cookie are controlled.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { entry, expectValidPage, page, pos } from '../../../tests/chat-history-paging/fixtures'

const SID = 'sid-interrupted'

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn())
  Object.defineProperty(document, 'cookie', { configurable: true, get: () => '__Host-csrf=test-csrf-token' })
})
afterEach(() => {
  vi.unstubAllGlobals(); sessionStorage.clear(); Reflect.deleteProperty(document, 'cookie'); vi.resetModules()
})

// ToolCall.yaml permits both states. U-D1-6 (a) requires interruption to be
// terminal; the existing snapshot carrier presents it as error, not a new UI
// status. Keep a genuinely running call as a control against mapping both to error.
it.each([
  { saved: 'interrupted', displayed: 'error' },
  { saved: 'running', displayed: 'running' },
] as const)('GET /sessions/{id}/messages maps $saved to $displayed without dropping the call', async ({ saved, displayed }) => {
  const fixture = page([entry(1, {
    id: 'call_interrupted', type: 'tool_call', role: 'assistant', content: '', agent_id: 'mia', turn_id: 'mia-turn-16',
    tool_calls: [{ id: 'call_interrupted', tool: 'bash', status: saved, parameters: { command: 'sleep 30' }, content_state: 'full' }],
  })], { sessionId: SID })
  expectValidPage(fixture, [pos(1)], 1)
  vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify(fixture), { status: 200, headers: { 'Content-Type': 'application/json' } }))
  const { fetchSessionMessages } = await import('./sessions')
  const result = await fetchSessionMessages(SID)
  expect(result.entries.map(row => row.id), 'the validated page retains exactly the saved tool row').toEqual(['call_interrupted'])
  expect(result.entries.flatMap(row => row.tool_calls ?? []), 'only interrupted is terminal; a real running call stays running').toEqual([
    { id: 'call_interrupted', tool: 'bash', status: displayed, params: { command: 'sleep 30' }, result: undefined, duration_ms: undefined, error: undefined },
  ])
})
