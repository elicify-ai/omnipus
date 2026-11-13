// #1794 — three more body-read catches must not count an aborted request as an
// API schema error (follow-up to #1791, which fixed request() in http.ts).
//
// The three catches: fetchProvidersCatalog (providers.ts, takes no signal),
// putLibraryContent (library.ts, takes a signal: the editor's timeout path) and
// fetchLibraryContentVersioned (library.ts, takes no signal).
//
// Oracle (same as http.abort-body-read.test.ts): a body read that fails because
// the request was aborted rejects with that error and records NOTHING; every
// other read failure still records exactly one schema error and throws
// ApiSchemaError. Only fetch is doubled.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiSchemaError, getApiSchemaErrorCount, resetApiSchemaErrorCount } from './http'
import { fetchLibraryContentVersioned, putLibraryContent } from './library'
import { fetchProvidersCatalog, resetProvidersCatalogCache } from './providers'

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

const aborted = () => new DOMException('The operation was aborted.', 'AbortError')
const otherRealmAbort = () => Object.assign(new Error('aborted elsewhere'), { name: 'AbortError' })
const networkError = () => new TypeError('network error')

// One row per catch: how to call it, with an optional caller signal.
interface Site {
  name: string
  hasSignal: boolean
  call: (signal?: AbortSignal) => Promise<unknown>
}

const sites: Site[] = [
  { name: 'fetchProvidersCatalog', hasSignal: false, call: () => fetchProvidersCatalog() },
  {
    name: 'putLibraryContent',
    hasSignal: true,
    call: (signal) =>
      putLibraryContent('ws1', { path: 'notes.md', content: 'x', expect_version: 'v1' }, signal ? { signal } : undefined),
  },
  { name: 'fetchLibraryContentVersioned', hasSignal: false, call: () => fetchLibraryContentVersioned('ws1', 'notes.md') },
]

beforeEach(() => {
  document.cookie = 'csrf=abort-body-read-catches; path=/'
  resetApiSchemaErrorCount()
  resetProvidersCatalogCache()
})

afterEach(() => {
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
})

describe.each(sites)('$name — a failed body read (#1794)', (site) => {
  it('an AbortError during the body read rejects with that error and records nothing', async () => {
    const { response, fail } = streamingResponse()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response)
    const cause = aborted()

    const pending = site.call()
    fail(cause)

    await expect(pending).rejects.toBe(cause)
    expect(getApiSchemaErrorCount()).toBe(0)
  })

  it('an error named AbortError that is a plain Error (another realm) is also not counted', async () => {
    const { response, fail } = streamingResponse()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response)
    const cause = otherRealmAbort()

    const pending = site.call()
    fail(cause)

    await expect(pending).rejects.toBe(cause)
    expect(getApiSchemaErrorCount()).toBe(0)
  })

  it('a read failure with no abort still records one schema error (issue count 1)', async () => {
    const { response, fail } = streamingResponse()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response)

    const pending = site.call()
    fail(networkError())

    const error = await pending.then(() => undefined, (e: unknown) => e)
    expect(error).toBeInstanceOf(ApiSchemaError)
    expect((error as ApiSchemaError).zodIssues).toEqual([{ path: [], message: 'Response is not valid JSON' }])
    expect(getApiSchemaErrorCount()).toBe(1)
  })

  it('control: a 200 with a non-JSON body still records one schema error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('<html>oops</html>', { status: 200, headers: { 'content-type': 'text/html' } }),
    )
    const error = await site.call().then(() => undefined, (e: unknown) => e)
    expect(error).toBeInstanceOf(ApiSchemaError)
    expect(getApiSchemaErrorCount()).toBe(1)
  })
})

// Only the library write takes a caller signal: a read failure after the caller
// aborted is the abort, whatever the error is called.
describe('putLibraryContent — the caller signal (#1794)', () => {
  it('a read failure after the caller aborted rejects with that error and records nothing, even when it is not named AbortError', async () => {
    const controller = new AbortController()
    const { response, fail } = streamingResponse()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response)
    const cause = networkError()
    const site = sites[1]

    const pending = site.call(controller.signal)
    controller.abort()
    fail(cause)

    await expect(pending).rejects.toBe(cause)
    expect(getApiSchemaErrorCount()).toBe(0)
  })

  it('a read failure with a live (never aborted) signal still records one schema error', async () => {
    const controller = new AbortController()
    const { response, fail } = streamingResponse()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response)
    const site = sites[1]

    const pending = site.call(controller.signal)
    fail(networkError())

    const error = await pending.then(() => undefined, (e: unknown) => e)
    expect(error).toBeInstanceOf(ApiSchemaError)
    expect(getApiSchemaErrorCount()).toBe(1)
  })
})
