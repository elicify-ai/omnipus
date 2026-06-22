// Unit test for fetchCliDetect (UAT fix): the call MUST go through the authed
// request() wrapper so the bearer token rides along. The previous raw
// fetch('/api/v1/system/cli-detect') sent no auth header and 401'd, producing
// a false "Could not detect installed external CLIs" banner.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { fetchCliDetect } from './api'

let fetchSpy: ReturnType<typeof vi.fn>

beforeEach(() => {
  sessionStorage.setItem('omnipus_auth_token', 'test-token-123')
  fetchSpy = vi.fn().mockResolvedValue(
    new Response(JSON.stringify({ hasClaude: true, hasCodex: false, hasOpencode: true }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  )
  vi.stubGlobal('fetch', fetchSpy)
})

afterEach(() => {
  sessionStorage.clear()
  vi.unstubAllGlobals()
})

describe('fetchCliDetect — sends auth (UAT fix)', () => {
  it('hits the cli-detect endpoint with a Bearer Authorization header', async () => {
    const result = await fetchCliDetect()

    expect(fetchSpy).toHaveBeenCalledTimes(1)
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/system/cli-detect')

    // The bearer token must be present (the bug was a missing auth header).
    const headers = new Headers(init.headers)
    expect(headers.get('Authorization')).toBe('Bearer test-token-123')

    // Parsed + validated against the CliDetect schema.
    expect(result).toEqual({ hasClaude: true, hasCodex: false, hasOpencode: true })
  })
})
