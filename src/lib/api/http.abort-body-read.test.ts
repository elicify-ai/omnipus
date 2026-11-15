// #1791 — an aborted request must not be counted as an API schema error.
//
// Trace evidence (release run 43086640916): every GET /workspaces?status=active
// response in the failed attempt was valid, yet one `apiSchemaError` with
// issueCount 1 was recorded. issueCount 1 is the fixed count of the body-read
// catch in http.ts (not a Zod issue count). Cause INFERRED, not proven: a
// request cancelled while its body was being read was counted as a schema error.
//
// Oracle: an aborted body read rejects with the abort and records nothing; any
// other read failure still records one schema error. Only fetch is doubled.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { z } from 'zod'
import { ApiSchemaError, getApiSchemaErrorCount, request, resetApiSchemaErrorCount } from './http'

const schema = z.array(z.object({ id: z.string() }))

// A 200 JSON response whose body stream fails when `fail` is called.
function streamingResponse(): { response: Response; fail: (reason: unknown) => void } {
  let fail: (reason: unknown) => void = () => {}
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      fail = (reason) => controller.error(reason)
    },
  })
  return {
    response: new Response(body, { status: 200, headers: { 'content-type': 'application/json' } }),
    fail,
  }
}

beforeEach(() => {
  document.cookie = 'csrf=abort-body-read-test; path=/'
  resetApiSchemaErrorCount()
})

afterEach(() => {
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
})

describe('request — a failed body read (#1791)', () => {
  it('an AbortError during the body read rejects with that error and records nothing, even when the signal is not aborted', async () => {
    const controller = new AbortController() // never aborted: only the error name can classify this
    const { response, fail } = streamingResponse()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response)
    const cause = new DOMException('The operation was aborted.', 'AbortError')

    const pending = request('/workspaces?status=active', { signal: controller.signal }, schema)
    fail(cause)

    await expect(pending).rejects.toBe(cause)
    expect(getApiSchemaErrorCount()).toBe(0)
  })

  it('an error named AbortError from another realm (a plain Error) is also not counted', async () => {
    const { response, fail } = streamingResponse()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response)
    const cause = Object.assign(new Error('aborted elsewhere'), { name: 'AbortError' })

    const pending = request('/workspaces?status=active', undefined, schema)
    fail(cause)

    await expect(pending).rejects.toBe(cause)
    expect(getApiSchemaErrorCount()).toBe(0)
  })

  it('a read failure after the caller aborted rejects with that error and records nothing, even when it is not named AbortError', async () => {
    const controller = new AbortController()
    const { response, fail } = streamingResponse()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response)
    const cause = new TypeError('network error')

    const pending = request('/workspaces?status=active', { signal: controller.signal }, schema)
    controller.abort()
    fail(cause)

    await expect(pending).rejects.toBe(cause)
    expect(getApiSchemaErrorCount()).toBe(0)
  })

  it('a read failure with no abort still records one schema error (issue count 1)', async () => {
    const controller = new AbortController()
    const { response, fail } = streamingResponse()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response)

    const pending = request('/workspaces?status=active', { signal: controller.signal }, schema)
    fail(new TypeError('network error'))

    const error = await pending.then(() => undefined, (e: unknown) => e)
    expect(error).toBeInstanceOf(ApiSchemaError)
    expect((error as ApiSchemaError).zodIssues).toEqual([{ path: [], message: 'Response is not valid JSON' }])
    expect(getApiSchemaErrorCount()).toBe(1)
  })

  it('control: a 200 with a non-JSON body still records one schema error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('<html>oops</html>', { status: 200, headers: { 'content-type': 'text/html' } }),
    )
    const error = await request('/workspaces?status=active', undefined, schema).then(() => undefined, (e: unknown) => e)
    expect(error).toBeInstanceOf(ApiSchemaError)
    expect(getApiSchemaErrorCount()).toBe(1)
  })

  it('control: a valid body is returned and records nothing', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify([{ id: 'w1' }]), { status: 200, headers: { 'content-type': 'application/json' } }),
    )
    await expect(request('/workspaces?status=active', undefined, schema)).resolves.toEqual([{ id: 'w1' }])
    expect(getApiSchemaErrorCount()).toBe(0)
  })
})
