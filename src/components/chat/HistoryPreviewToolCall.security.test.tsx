// SEC-3 / A2.2,A2.4,B72: reject a private full-entry HTTP200 through the
// REAL singleton query client. Transport/refinement/error subscription/UI are
// not replaced. Only network, environment and diagnostic sinks are observed.
import { inspect } from 'node:util'
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Message } from '@/lib/api/generated/openapi-types'
import { Message as MessageSchema } from '@/lib/api/generated/schemas'
import { ApiSchemaError, getApiSchemaErrorCount, resetApiSchemaErrorCount } from '@/lib/api/http'
import { queryClient } from '@/lib/queryClient'
import { releaseHistoryEntryReads, useHistoryEntryReadStore } from '@/lib/historyEntryReads'
import * as telemetry from '@/lib/telemetry'
import { useUiStore } from '@/store/ui'
import { HistoryPreviewToolCall } from './tools/HistoryPreviewToolCall'
import { entry, pos } from '../../../tests/chat-history-paging/fixtures'

const CANARY = 'SEC3_SYNTHETIC_PRIVATE_RESPONSE_CANARY'
const PREVIEW = 'Saved safe preview stays visible.'
const MARKER = { _history_preview: true as const, original_size_bytes: 51201, preview: PREVIEW }
const CALL = { id: 'sec3-call', tool: 'read_file', status: 'success' as const, params: { path: 'synthetic.txt' }, result: MARKER }
const sessions: string[] = []

// showHidden includes Error.message/stack and non-enumerable properties; depth
// and string limits are disabled. JSON.stringify(Error) would miss this sink.
function diagnosticText(value: unknown): string {
  return inspect(value, { depth: null, showHidden: true, customInspect: false, maxStringLength: null, maxArrayLength: null })
}

afterEach(() => {
  cleanup()
  sessions.splice(0).forEach(releaseHistoryEntryReads)
  queryClient.clear()
  useHistoryEntryReadStore.setState({ generations: {}, refused: {} })
  useUiStore.setState({ toasts: [] })
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})

it.each(['generated schema', 'full-entry refinement'] as const)(
  'SEC-3: private %s-invalid HTTP200 never reaches console or diagnostic payloads; Retry stays usable',
  async rejection => {
    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production') // Exercise real production telemetry, not its test-mode no-op.
    telemetry._resetTelemetryForTest()
    resetApiSchemaErrorCount()
    queryClient.clear()
    useUiStore.setState({ toasts: [] })
    // Distinct real endpoints avoid the shared handler's toast-dedup window.
    const sid = rejection === 'generated schema' ? 'sec3-schema-session' : 'sec3-refinement-session'
    sessions.push(sid)
    const position = pos(1)
    const path = `/api/v1/sessions/${sid}/messages/${position}`
    const consoleSinks = [vi.spyOn(console, 'error'), vi.spyOn(console, 'warn'), vi.spyOn(console, 'info'), vi.spyOn(console, 'log'), vi.spyOn(console, 'debug')]
    const errorEvents = vi.spyOn(telemetry, 'logError') // Call-through: keep production emitter real.
    const diagnosticEvents = vi.spyOn(telemetry, 'logDiagnostic')

    const captureControl = new Error('safe capture control')
    Object.defineProperty(captureControl, 'rawBody', { value: { private: CANARY }, enumerable: false })
    console.warn('sec3 sink instrument', captureControl)
    expect(diagnosticText(consoleSinks[1].mock.calls), 'instrument must see nested, non-enumerable rawBody').toContain(CANARY)
    consoleSinks.forEach(sink => sink.mockClear())

    const full: Message = entry(1, { id: 'sec3-owner', role: 'assistant', content: '', tool_calls: [
      { id: CALL.id, tool: CALL.tool, status: 'success', content_state: 'full', parameters: { path: 'synthetic.txt' }, result: { text: 'Recovered complete output.' } },
    ] })
    expect(MessageSchema.safeParse(full).success, 'valid full-response control must pass the real generated schema').toBe(true)
    const invalid = {
      ...full,
      ...(rejection === 'generated schema' ? { role: 7 } : {}),
      tool_calls: [{ ...full.tool_calls![0], parameters: { path: CANARY }, result: rejection === 'full-entry refinement'
        ? { _history_preview: true, original_size_bytes: 51201, preview: CANARY }
        : { text: CANARY } }],
    }
    expect(MessageSchema.safeParse(invalid).success, 'fixture distinguishes base-schema failure from a VALID marker rejected only by full-entry refinement').toBe(rejection === 'full-entry refinement')
    let recovered = false
    const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input), 'read and Retry address only this exact occurrence').toBe(path)
      expect(init?.credentials, 'real HTTP transport still authenticates normally').toBe('include')
      return new Response(JSON.stringify(recovered ? full : invalid), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetch)
    render(<HistoryPreviewToolCall tc={CALL} marker={MARKER} sessionId={sid} historyPosition={position} />)
    expect(fetch, 'full reads remain lazy until opening').not.toHaveBeenCalled()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('tool-call-toggle'))
    const retry = await screen.findByRole('button', { name: 'Retry' })
    expect(screen.getByRole('alert'), 'fixed recovery text, never the body or parse exception').toContainElement(screen.getByText("Couldn't load the full content.", { exact: true }))
    expect(screen.getByRole('region', { name: 'Tool output preview' })).toHaveTextContent(PREVIEW)
    expect(retry).toBeVisible()
    expect(getApiSchemaErrorCount(), 'real HTTP/refinement validation ran exactly once').toBe(1)
    expect(fetch, 'no automatic retry loop').toHaveBeenCalledTimes(1)
    const reads = queryClient.getQueryCache().findAll({ queryKey: ['history-entry', sid, position] })
    expect(reads, 'this is the application singleton, not a hand-reimplemented subscriber').toHaveLength(1)
    expect(reads[0].state.status).toBe('error')
    expect(reads[0].state.error).toBeInstanceOf(ApiSchemaError)
    await waitFor(() => expect(useUiStore.getState().toasts, 'centralized schema handler still reports the recoverable failure').toHaveLength(1))
    expect(errorEvents.mock.calls.some(([event]) => event.event === 'apiSchemaError'), 'production diagnostic path actually emitted, not an empty-sink pass').toBe(true)

    // Exercise recovery BEFORE the security assertions so a RED receipt still
    // proves fixed copy, visible preview, same-address Retry and real success.
    recovered = true
    await user.click(retry)
    expect(await screen.findByRole('region', { name: 'Tool output' })).toHaveTextContent(/^Recovered complete output\.$/)
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(queryClient.getQueryCache().findAll({ queryKey: ['history-entry', sid, position] })[0].state.status).toBe('success')

    expect(diagnosticText([errorEvents.mock.calls, diagnosticEvents.mock.calls, useUiStore.getState().toasts]), 'SEC-3: no diagnostic payload or toast may contain a rejected full-entry body').not.toContain(CANARY)
    expect(document.body.textContent, 'fixed UI never renders private rejected response data').not.toContain(CANARY)
    expect(consoleSinks.some(sink => diagnosticText(sink.mock.calls).includes(CANARY)), 'SEC-3: EVERY console argument, including nested ApiSchemaError.rawBody, must exclude the private response').toBe(false)
  },
)
