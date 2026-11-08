// #1568 F5 — the API-client boundary for an enable's start-failure reason.
//
// The backend answers a failed ordinary enable with the contract's 500
// ErrorResponse whose `error` field names the start failure (issue #1568
// Expected: "Enable reports a start failure truthfully … never a plain
// success while the adapter is not running"). The panel can only show that
// reason if the REAL client preserves it: src/lib/api/channels.ts::
// withEnableErrorText must map a 500 body's `error` text into
// ApiError.userMessage instead of the generic server-unavailable sentence.
//
// Oracle: a 500 ErrorResponse body's error text reaches the rejection's
// userMessage byte-for-byte, on an ApiError with status 500. Only the fetch
// transport is doubled; the client, withEnableErrorText, the HTTP layer and
// response handling are real. (The check's 1568-m4 mutant — bypassing the
// reason mapping — survives the panel control, which mocks enableChannel;
// this test is the boundary that kills it.)

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api-error'
import { enableChannel } from './channels'

// The start-failure reason a fixed #1568 backend sends in ErrorResponse.error
// (the Configure outcome's not-running wording family the issue suggests).
const START_FAILURE_REASON =
  'Saved, but the connection is not running: the platform rejected the credential (401).'
// The generic sentence the HTTP layer produces when the reason is lost —
// this must never be what the caller receives for a 500 ErrorResponse.
const GENERIC_PREFIX = /^5\d{2}: /

beforeEach(() => {
  document.cookie = 'csrf=channel-enable-error-test; path=/'
})

afterEach(() => {
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
})

describe('enableChannel — a 500 ErrorResponse keeps its reason in userMessage (#1568 F5)', () => {
  it('rejects with an ApiError whose userMessage is the body\'s error text, byte-for-byte', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      new Response(JSON.stringify({ error: START_FAILURE_REASON }), {
        status: 500,
        headers: { 'content-type': 'application/json' },
      }),
    )

    const error = await enableChannel('discord').then(
      () => undefined,
      (e: unknown) => e,
    )

    // The request boundary: one bodyless ordinary enable.
    expect(fetchSpy).toHaveBeenCalledTimes(1)
    expect(String(fetchSpy.mock.calls[0]?.[0])).toBe('/api/v1/channels/discord/enable')

    // ORACLE: the reason survives the client, not the generic sentence.
    expect(error).toBeInstanceOf(ApiError)
    const apiError = error as ApiError
    expect(apiError.status).toBe(500)
    expect(apiError.userMessage,
      `a 500 ErrorResponse's reason must reach ApiError.userMessage; got ${JSON.stringify(apiError.userMessage)}`)
      .toBe(START_FAILURE_REASON)
    expect(apiError.userMessage).not.toMatch(GENERIC_PREFIX)
  })

  it('still rejects with the untouched ApiError when a 500 carries no ErrorResponse body', async () => {
    // Boundary of the mapping: a 500 whose body is not an ErrorResponse keeps
    // the HTTP layer's own message — the helper must not invent a reason.
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      new Response('internal error', { status: 500 }),
    )

    const error = await enableChannel('discord').then(
      () => undefined,
      (e: unknown) => e,
    )

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).status).toBe(500)
    expect((error as ApiError).userMessage).toBeTruthy()
    expect((error as ApiError).userMessage).not.toBe(START_FAILURE_REASON)
  })
})
