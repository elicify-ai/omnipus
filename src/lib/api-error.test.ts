// Unit tests for ApiError + isApiError discriminator.
//
// Coverage groups:
//   1. Constructor + default messages
//   2. Status-class predicates (isAuthError / isNotFound / isRateLimited /
//      isServerError / isNetworkError)
//   3. isApiError discriminator
//   4. fromResponse — JSON body parse path
//   5. fromResponse — text body fallback
//   6. Network-error case (status 0)

import { describe, it, expect } from 'vitest'
import { ApiError, isApiError } from './api-error'

describe('ApiError constructor + defaults', () => {
  it('uses provided userMessage when non-empty', () => {
    const err = new ApiError(404, 'Agent not found')
    expect(err.userMessage).toBe('Agent not found')
    // Legacy compat: Error.message is "${status}: ${userMessage}" so existing
    // string-matchers (err.message.startsWith('401') etc) still work.
    expect(err.message).toBe('404: Agent not found')
    expect(err.status).toBe(404)
    expect(err.name).toBe('ApiError')
  })

  it('falls back to status-class default when userMessage is empty', () => {
    const err = new ApiError(429, '')
    expect(err.userMessage).toMatch(/Too many requests/i)
    expect(err.message).toMatch(/^429:/)
  })

  it('falls back to status-class default when userMessage is whitespace', () => {
    const err = new ApiError(401, '   ')
    expect(err.userMessage).toMatch(/session has expired/i)
  })

  it('captures optional code, body, and cause', () => {
    const cause = new Error('original')
    const err = new ApiError(409, 'last admin', {
      code: 'last_admin',
      body: '{"error":"last admin","code":"last_admin"}',
      cause,
    })
    expect(err.code).toBe('last_admin')
    expect(err.body).toBe('{"error":"last admin","code":"last_admin"}')
    expect(err.cause).toBe(cause)
  })

  it('formats network errors (status 0) without a status prefix', () => {
    // Transport failures don't have a real HTTP status; the legacy Error.message
    // shape doesn't apply, so the message is the bare userMessage.
    const err = new ApiError(0, 'Network unavailable. Check your connection.')
    expect(err.message).toBe('Network unavailable. Check your connection.')
    expect(err.status).toBe(0)
  })
})

describe('ApiError predicates', () => {
  it('isAuthError() is true for 401 and 403', () => {
    expect(new ApiError(401, 'x').isAuthError()).toBe(true)
    expect(new ApiError(403, 'x').isAuthError()).toBe(true)
    expect(new ApiError(404, 'x').isAuthError()).toBe(false)
    expect(new ApiError(500, 'x').isAuthError()).toBe(false)
  })

  it('isNotFound() is true only for 404', () => {
    expect(new ApiError(404, 'x').isNotFound()).toBe(true)
    expect(new ApiError(403, 'x').isNotFound()).toBe(false)
    expect(new ApiError(410, 'x').isNotFound()).toBe(false)
  })

  it('isRateLimited() is true only for 429', () => {
    expect(new ApiError(429, 'x').isRateLimited()).toBe(true)
    expect(new ApiError(503, 'x').isRateLimited()).toBe(false)
  })

  it('isServerError() is true for any 5xx', () => {
    expect(new ApiError(500, 'x').isServerError()).toBe(true)
    expect(new ApiError(502, 'x').isServerError()).toBe(true)
    expect(new ApiError(503, 'x').isServerError()).toBe(true)
    expect(new ApiError(599, 'x').isServerError()).toBe(true)
    expect(new ApiError(499, 'x').isServerError()).toBe(false)
    expect(new ApiError(600, 'x').isServerError()).toBe(false)
  })

  it('isNetworkError() is true only for status 0', () => {
    expect(new ApiError(0, 'x').isNetworkError()).toBe(true)
    expect(new ApiError(404, 'x').isNetworkError()).toBe(false)
  })
})

describe('isApiError discriminator', () => {
  it('returns true for ApiError instances', () => {
    expect(isApiError(new ApiError(500, 'boom'))).toBe(true)
  })

  it('returns false for plain Error', () => {
    expect(isApiError(new Error('not an api error'))).toBe(false)
  })

  it('returns false for non-Error values', () => {
    expect(isApiError(null)).toBe(false)
    expect(isApiError(undefined)).toBe(false)
    expect(isApiError('string error')).toBe(false)
    expect(isApiError({ status: 401, message: 'fake' })).toBe(false)
  })
})

describe('ApiError.fromResponse — JSON body', () => {
  // For known status codes the user-facing message is fixed
  // (defaultUserMessage) so the SPA cannot leak server-internal phrasing
  // into a toast. The raw server text is still preserved on `body` for
  // diagnostics. See comment in api-error.ts::fromResponse.
  it('uses status-default userMessage for known 4xx, preserves parsed body', async () => {
    const res = new Response('{"error":"invalid credentials"}', {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    })
    const err = await ApiError.fromResponse(res)
    expect(err.status).toBe(401)
    expect(err.userMessage).toMatch(/session has expired/i)
    expect(err.body).toBe('{"error":"invalid credentials"}')
  })

  it('parses {code, message} — code goes to .code, status default wins for known statuses', async () => {
    const res = new Response('{"code":"RATE_LIMITED","message":"slow down"}', {
      status: 429,
    })
    const err = await ApiError.fromResponse(res)
    expect(err.code).toBe('RATE_LIMITED')
    expect(err.userMessage).toMatch(/too many requests/i)
  })

  it('prefers JSON.error over JSON.message for unknown statuses', async () => {
    const res = new Response('{"error":"first","message":"second"}', { status: 400 })
    const err = await ApiError.fromResponse(res)
    expect(err.userMessage).toBe('first')
  })
})

describe('ApiError.fromResponse — text fallback', () => {
  it('uses status-default for 5xx and preserves raw text in body', async () => {
    const res = new Response('something broke', { status: 500 })
    const err = await ApiError.fromResponse(res)
    expect(err.status).toBe(500)
    expect(err.userMessage).toMatch(/server is unavailable/i)
    expect(err.body).toBe('something broke')
  })

  it('returns plain text in userMessage for unknown status codes', async () => {
    const res = new Response('something broke', { status: 418 })
    const err = await ApiError.fromResponse(res)
    expect(err.status).toBe(418)
    expect(err.userMessage).toBe('something broke')
  })

  it('falls back to default message when body is empty', async () => {
    const res = new Response('', { status: 503 })
    const err = await ApiError.fromResponse(res)
    expect(err.status).toBe(503)
    // Status-class default for any 5xx.
    expect(err.userMessage).toMatch(/server is unavailable/i)
  })

  it('handles 404 with empty body using its own default', async () => {
    const res = new Response('', { status: 404 })
    const err = await ApiError.fromResponse(res)
    expect(err.userMessage).toMatch(/not found/i)
  })
})

describe('ApiError extends Error properly', () => {
  it('is catchable as Error', () => {
    let caught: unknown
    try {
      throw new ApiError(500, 'oops')
    } catch (e) {
      caught = e
    }
    expect(caught instanceof Error).toBe(true)
    expect(caught instanceof ApiError).toBe(true)
  })

  it('preserves a cause through Error.cause', () => {
    const cause = new TypeError('fetch failed')
    const err = new ApiError(0, 'network down', { cause })
    expect(err.cause).toBe(cause)
  })
})

// H3-FE: Body size cap and binary content sniff.
describe('ApiError.fromResponse — body size cap + binary sniff (H3-FE)', () => {
  it('rejects body declared >4 KiB via Content-Length and uses status default', async () => {
    const headers = new Headers({
      'Content-Type': 'text/plain',
      'Content-Length': String(4 * 1024 + 1),
    })
    const res = new Response('x'.repeat(4 * 1024 + 1), { status: 502, headers })
    const err = await ApiError.fromResponse(res)
    expect(err.status).toBe(502)
    // Must use the status-class default, not the body text
    expect(err.userMessage).toMatch(/server is unavailable/i)
  })

  it('rejects body exceeding 4 KiB after read (no Content-Length) and uses status default', async () => {
    // No Content-Length header — the cap must apply after reading.
    const bigBody = 'a'.repeat(4 * 1024 + 1)
    const res = new Response(bigBody, { status: 503 })
    const err = await ApiError.fromResponse(res)
    expect(err.status).toBe(503)
    expect(err.userMessage).toMatch(/server is unavailable/i)
  })

  it('accepts a body exactly at 4 KiB boundary', async () => {
    const body = 'z'.repeat(4 * 1024)
    const res = new Response(body, {
      status: 400,
      headers: { 'Content-Type': 'text/plain' },
    })
    const err = await ApiError.fromResponse(res)
    expect(err.status).toBe(400)
    // Body is text so should use it as userMessage
    expect(err.userMessage).toBe(body)
  })

  it('rejects non-text content-type and uses status default', async () => {
    const res = new Response('\x00\x01\x02\x03', {
      status: 502,
      headers: { 'Content-Type': 'application/octet-stream' },
    })
    const err = await ApiError.fromResponse(res)
    expect(err.status).toBe(502)
    expect(err.userMessage).toMatch(/server is unavailable/i)
  })

  it('rejects binary body (>5% non-printable bytes) and uses status default', async () => {
    // Build a string with ~10% non-printable bytes (NUL chars)
    const sample = Array.from({ length: 100 }, (_, i) => (i % 10 === 0 ? '\x00' : 'a')).join('')
    const res = new Response(sample, {
      status: 500,
      // No explicit content-type so binary sniff applies
    })
    const err = await ApiError.fromResponse(res)
    expect(err.status).toBe(500)
    expect(err.userMessage).toMatch(/server is unavailable/i)
  })

  it('accepts a text body with no Content-Type (printable ASCII), uses 5xx default for known status', async () => {
    const res = new Response('Something broke in the proxy', { status: 502 })
    const err = await ApiError.fromResponse(res)
    expect(err.status).toBe(502)
    expect(err.userMessage).toMatch(/server is unavailable/i)
    expect(err.body).toBe('Something broke in the proxy')
  })

  it('falls back to status default for a body that is all non-printable', async () => {
    const binary = '\x01\x02\x03\x04\x05\x06\x07\x08\x0e\x0f'.repeat(10)
    const res = new Response(binary, { status: 500 })
    const err = await ApiError.fromResponse(res)
    expect(err.userMessage).toMatch(/server is unavailable/i)
  })
})
