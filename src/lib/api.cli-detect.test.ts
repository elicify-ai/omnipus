// Unit tests for fetchCliDetect + fetchCliValidate (external-executor-cli-
// path-detection spec). fetchCliDetect (UAT fix): the call MUST go through
// the request() wrapper so the omnipus-session cookie rides along
// (credentials:'include' — US-5 / FR-010). The previous raw
// fetch('/api/v1/system/cli-detect') sent no credentials and 401'd,
// producing a false "Could not detect installed external CLIs" banner.
//
// CliDetect was restructured from booleans (hasClaude/hasCodex/hasOpencode)
// to per-CLI objects `{ claude, codex, opencode }: CliDetectEntry` — each
// carrying `{ installed, path, source }` — so the SPA can prefill the real
// resolved path instead of a static placeholder (FR-001/FR-002).

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { fetchCliDetect, fetchCliValidate } from './api'

let fetchSpy: ReturnType<typeof vi.fn>

const DETECT_BODY = {
  claude: { installed: true, path: '/usr/local/bin/claude', source: 'path' },
  codex: { installed: false, path: null, source: null },
  opencode: { installed: true, path: '/home/dev/.local/bin/opencode', source: 'well-known' },
}

// cli-validate is a state-changing POST, so the client-side CSRF gate in
// request() requires the __Host-csrf cookie to be present (double-submit
// cookie, issue #97) — mirrors the pattern in api.providers.test.ts.
function stubCookie(value: string) {
  Object.defineProperty(document, 'cookie', {
    configurable: true,
    get: () => value,
  })
}

function restoreCookie() {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (document as any).cookie
}

beforeEach(() => {
  stubCookie('__Host-csrf=test-csrf-token')
  fetchSpy = vi.fn().mockResolvedValue(
    new Response(JSON.stringify(DETECT_BODY), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  )
  vi.stubGlobal('fetch', fetchSpy)
})

afterEach(() => {
  vi.unstubAllGlobals()
  restoreCookie()
})

describe('fetchCliDetect — sends auth (UAT fix)', () => {
  it('hits the cli-detect endpoint with credentials included (cookie auth)', async () => {
    const result = await fetchCliDetect()

    expect(fetchSpy).toHaveBeenCalledTimes(1)
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/system/cli-detect')

    // The omnipus-session cookie must ride along (the bug was a missing
    // credentials mode, equivalent to the old missing-auth-header bug).
    expect(init.credentials).toBe('include')

    // Parsed + validated against the restructured per-CLI CliDetect schema.
    expect(result).toEqual(DETECT_BODY)
  })
})

describe('fetchCliValidate — POST /system/cli-validate (FR-006/FR-013)', () => {
  it('POSTs { cli, cli_path } and returns the classified CliValidateResponse', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(
        JSON.stringify({ ok: true, reason: 'ok', resolved_path: '/usr/local/bin/claude', version: '1.2.3', detail: 'OK' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const result = await fetchCliValidate('claude-code', '/usr/local/bin/claude')

    expect(fetchSpy).toHaveBeenCalledTimes(1)
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/system/cli-validate')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body as string)).toEqual({ cli: 'claude-code', cli_path: '/usr/local/bin/claude' })

    expect(result).toEqual({
      ok: true,
      reason: 'ok',
      resolved_path: '/usr/local/bin/claude',
      version: '1.2.3',
      detail: 'OK',
    })
  })

  it('forwards an AbortSignal so callers can cancel an in-flight validate (debounce)', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ ok: false, reason: 'missing-binary', detail: 'not found' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const controller = new AbortController()

    await fetchCliValidate('codex', '/nope/codex', { signal: controller.signal })

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(init.signal).toBe(controller.signal)
  })
})
