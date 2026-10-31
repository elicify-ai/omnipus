// #1452 gate SEC-3: a history response the browser rejects must not carry the
// saved conversation into shared diagnostic errors or the console. The error
// keeps its classification (an ApiSchemaError, a recoverable read with Retry).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { entry, page, pos, SID, session } from '../../tests/chat-history-paging/fixtures'

const CONTENT = 'SEC3_CANARY_CONTENT'
const PARAM = 'SEC3_CANARY_PARAM'
const RESULT = 'SEC3_CANARY_RESULT'
// The JSON parser quotes only the first characters of a body that is not JSON.
const SHORT = 'ZQ9K'
const CANARIES = [CONTENT, PARAM, RESULT, 'SEC3_CANARY', SHORT]

/** Every string reachable from a value, own non-enumerable properties (an Error's message, stack) included. */
function reachableText(value: unknown, seen = new Set<unknown>()): string {
  if (value === null || value === undefined) return ''
  if (typeof value !== 'object' && typeof value !== 'function') return String(value)
  if (seen.has(value)) return ''
  seen.add(value)
  return Object.getOwnPropertyNames(value).map(name => {
    try { return `${name}:${reachableText((value as Record<string, unknown>)[name], seen)}` } catch { return '' }
  }).join('|')
}

function expectNoCanary(value: unknown, what: string) {
  const text = reachableText(value)
  for (const canary of CANARIES) expect(text, `${what} must not contain saved history text`).not.toContain(canary)
}

/** A saved assistant entry whose full-entry response still carries a valid preview marker (rejected by the full-entry refinement). */
function markedEntry() {
  return entry(1, {
    role: 'assistant', content: CONTENT,
    tool_calls: [{ id: 'sec3-call', tool: 'read_file', status: 'success', content_state: 'full', parameters: { path: PARAM }, result: { _history_preview: true, original_size_bytes: 70000, preview: RESULT } }],
  })
}

function respond(body: string) {
  vi.mocked(fetch).mockResolvedValueOnce(new Response(body, { status: 200, headers: { 'Content-Type': 'application/json' } }))
}

describe('SEC-3: rejected history responses stay out of diagnostics', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
  })
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    vi.resetModules()
  })

  it('full entry: a schema-rejected 200 is a body-free ApiSchemaError, logged and toasted without its content, with Retry kept', async () => {
    const body = JSON.stringify(markedEntry())
    expect(body, 'positive control: the response carries every canary').toContain(RESULT)
    respond(body)
    const { fetchSessionHistoryEntry, ApiSchemaError } = await import('./api')
    const { queryClient } = await import('./queryClient')
    const { useUiStore } = await import('@/store/ui')
    const error = await queryClient.fetchQuery({ queryKey: ['sec3-full'], queryFn: ({ signal }) => fetchSessionHistoryEntry(SID, pos(1), signal), retry: false }).then(() => undefined, (failure: unknown) => failure)

    expect(error).toBeInstanceOf(ApiSchemaError)
    expectNoCanary(error, 'the thrown error')
    const logged = vi.mocked(console.error).mock.calls.filter(call => call[0] === '[apiSchemaError]')
    expect(logged, 'the shared schema-error logger still reports the rejection').toHaveLength(1)
    expectNoCanary(vi.mocked(console.error).mock.calls, 'console.error arguments')
    expectNoCanary(vi.mocked(console.warn).mock.calls, 'console.warn arguments')
    await vi.waitFor(() => expect(useUiStore.getState().toasts.length).toBeGreaterThan(0))
    expectNoCanary(useUiStore.getState().toasts, 'the toast')

    const { classifyReadFailure, HistoryEntryReadOutcome } = await import('@/components/chat/historyEntryOutcome')
    expect(classifyReadFailure(error)).toBe('recoverable')
    const refetch = vi.fn()
    render(<HistoryEntryReadOutcome read={{ isError: true, error: error as Error, fetchStatus: 'idle', isSuccess: false, refetch }} />)
    screen.getByRole('button', { name: /retry/i }).click()
    expect(refetch).toHaveBeenCalledTimes(1)
  })

  it('full entry: a body that is not JSON keeps no fragment of it in the error', async () => {
    respond(`${SHORT} ${CONTENT} {"tool_calls":[`)
    const { fetchSessionHistoryEntry, ApiSchemaError } = await import('./api')
    const error = await fetchSessionHistoryEntry(SID, pos(1)).then(() => undefined, (failure: unknown) => failure)
    expect(error).toBeInstanceOf(ApiSchemaError)
    expectNoCanary(error, 'the thrown error')
  })

  it('history page: rejected page metadata keeps the entries out of the error', async () => {
    const rejected = { ...page([markedEntry()]), page_info: { mode: 'latest' } }
    respond(JSON.stringify(rejected))
    const { fetchSessionMessages, ApiSchemaError } = await import('./api')
    const error = await fetchSessionMessages(SID).then(() => undefined, (failure: unknown) => failure)
    expect(error).toBeInstanceOf(ApiSchemaError)
    expectNoCanary(error, 'the thrown error')
  })

  it('history page: an inconsistent page keeps the entries out of the error', async () => {
    const inconsistent = { ...page([markedEntry()]), session_id: 'another-session' }
    respond(JSON.stringify(inconsistent))
    const { fetchSessionMessages, ApiSchemaError } = await import('./api')
    const error = await fetchSessionMessages(SID).then(() => undefined, (failure: unknown) => failure)
    expect(error).toBeInstanceOf(ApiSchemaError)
    expectNoCanary(error, 'the thrown error')
  })

  it('session detail: a rejected detail keeps its embedded history out of the error', async () => {
    const { entries, session_id, ...metadata } = page([markedEntry()])
    void session_id
    respond(JSON.stringify({ session: { ...session(), id: 42 }, history_state: { status: 'available' }, messages: entries, ...metadata }))
    const { fetchSessionDetail, ApiSchemaError } = await import('./api')
    const error = await fetchSessionDetail(SID).then(() => undefined, (failure: unknown) => failure)
    expect(error).toBeInstanceOf(ApiSchemaError)
    expectNoCanary(error, 'the thrown error')
  })
})
