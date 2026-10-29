// D2 component tests mount the real application. HTTP/WebSocket/viewport are
// the existing process-edge instruments; renderers/store/cache stay real.
import { act, waitFor } from '@testing-library/react'
import { expect } from 'vitest'
import type { Message } from '@/lib/api/generated/openapi-types'
import { useChatStore } from '@/store/chat'
import { MAIN_ID, json } from '../l1-room-ui/harness'
import { entry, page, pos } from './fixtures'
import { openHistory } from './application'

export { MAIN_ID, json, pos }
export const TOOL_ID = 'd2-preview-tool'
export const FULL_TEXT = 'FULL_START-' + 'x'.repeat(60000) + '-FULL_END'
export const FULL_RESULT = { content: FULL_TEXT }
// Oracle: A2.1 bytes are standard compact JSON UTF8, not the product's encoder.
export const ENCODED_RESULT = JSON.stringify(FULL_RESULT)
export const MARKER = { _history_preview: true, original_size_bytes: new TextEncoder().encode(ENCODED_RESULT).length, preview: ENCODED_RESULT.slice(0, 4096) }
export const FULL_SOURCE: Message = entry(1, { id: 'd2-preview-owner', role: 'assistant', content: '', tool_calls: [
  { id: TOOL_ID, tool: 'read_file', status: 'success', content_state: 'full', parameters: { path: 'fixture.txt' }, result: FULL_RESULT },
] })
export function toolSource(result: Record<string, unknown> = MARKER): Message {
  return { ...FULL_SOURCE, tool_calls: FULL_SOURCE.tool_calls!.map(call => ({ ...call, result })) }
}
export type PreviewApp = Awaited<ReturnType<typeof openHistory>>
export async function replaceRows(app: PreviewApp, sources: Message[], mode: 'latest' | 'window_refresh' = 'latest') {
  const selected = page(sources, { sessionId: MAIN_ID, rows: sources.reduce((n, source) => n + (source.content || !source.tool_calls?.length ? 1 : 0) + (source.tool_calls?.length ?? 0), 0), info: { mode, limit: mode === 'latest' ? 20 : 100, min_rows: 1 } })
  app.network.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(selected))
  await act(async () => {
    if (mode === 'latest') await useChatStore.getState().loadLatestHistory(MAIN_ID, { limit: 20, min_rows: 1 })
    else await useChatStore.getState().refreshLoadedHistory(MAIN_ID)
  })
  await waitFor(() => expect(useChatStore.getState().sessionsById[MAIN_ID]?.history.requests[mode === 'latest' ? 'latest' : 'refresh'].status).toBe('idle'))
}
export async function openPreviewApp(sources: Message[] = [toolSource()], options: Parameters<typeof openHistory>[1] = {}) {
  const app = await openHistory(page([entry(90, { content: 'D2 application ready' })], { sessionId: MAIN_ID }), options)
  await replaceRows(app, sources)
  return app
}
export const fullPath = (position = pos(1)) => `/api/v1/sessions/${MAIN_ID}/messages/${position}`
export const fullReads = (app: PreviewApp, position = pos(1)) => app.network.requests.filter(r => r.method === 'GET' && r.path === fullPath(position))
export function previewToggle(index = 0) {
  // Specialized small-result cards and GenericToolCall both use the existing
  // catalogued disclosure semantics inside the real tool ownership wrapper.
  const toggle = document.querySelectorAll<HTMLElement>('[data-history-tool-row] button[aria-expanded]')[index]
  expect(toggle, 'real tool disclosure is reachable').toBeDefined()
  return toggle
}
