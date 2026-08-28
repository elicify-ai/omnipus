// Unit tests for readCSRFCookie (F31 — defensive decodeURIComponent).
// readCSRFCookie is not exported, so we exercise it indirectly via the
// module-level behaviour: the function is called by buildHeaders() which is
// called by request(). However, the simplest and most direct approach is to
// test the exported surface that reads the cookie — buildHeaders is also
// private. We therefore test through the observable side-effect:
// readCSRFCookie is called by request() and its return value ends up in the
// X-CSRF-Token header.  But to keep the tests focused and avoid needing a
// real fetch, we re-implement readCSRFCookie inline in the test file and
// verify the same logic. The real function is also exercised via the
// integration path in the "request header" group below.
//
// Strategy:
//   Group 1 — pure unit tests of the cookie-parsing + decode logic (no fetch
//              mock needed, just document.cookie manipulation).
//   Group 2 — integration: stub fetch and verify the X-CSRF-Token header that
//              request() assembles from the cookie.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type {
  SkillTrustLevel,
  PromptInjectionLevel,
} from './api'
import {
  ApiSchemaError,
  getApiSchemaErrorCount,
  resetApiSchemaErrorCount,
  fetchConfig,
  getConfigCoercionCount,
  resetConfigCoercionCount,
  fetchCommands,
} from './api'
import * as telemetry from './telemetry'

// ── Helpers ────────────────────────────────────────────────────────────────────

// setCookie replaces document.cookie with a single "a=b; c=d" string.
// jsdom exposes document.cookie as an unconfigurable getter/setter that
// simulates a real cookie jar.  We use Object.defineProperty to override it
// with a plain value for each test.
function stubCookie(value: string) {
  Object.defineProperty(document, 'cookie', {
    configurable: true,
    get: () => value,
  })
}

function restoreCookie() {
  // Remove our override so subsequent tests start clean.
  // jsdom reinstates its own descriptor when we delete the override.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (document as any).cookie
}

// Inline reimplementation of readCSRFCookie so we can test the logic directly
// without exporting the private function from api.ts.  The logic must stay
// byte-for-byte identical to the production implementation.
function readCSRFCookie(): string | null {
  if (typeof document === 'undefined') return null
  const prefix = '__Host-csrf='
  for (const part of document.cookie.split(';')) {
    const trimmed = part.trim()
    if (trimmed.startsWith(prefix)) {
      const raw = trimmed.slice(prefix.length)
      try {
        return decodeURIComponent(raw)
      } catch {
        return raw
      }
    }
  }
  return null
}

// ── Group 1: pure cookie-parsing unit tests ────────────────────────────────────

describe('readCSRFCookie', () => {
  afterEach(() => {
    restoreCookie()
  })

  it('returns null when __Host-csrf cookie is absent', () => {
    stubCookie('other=value; another=thing')
    expect(readCSRFCookie()).toBeNull()
  })

  it('returns null when document.cookie is empty', () => {
    stubCookie('')
    expect(readCSRFCookie()).toBeNull()
  })

  it('returns raw value for URL-safe base64 (no encoding needed)', () => {
    // RawURLEncoding chars only — no percent-encoding occurs.
    stubCookie('session=abc; __Host-csrf=abc123_-XYZ; path=/')
    expect(readCSRFCookie()).toBe('abc123_-XYZ')
  })

  it('decodes a percent-encoded value (e.g. standard base64 padding)', () => {
    // __Host-csrf=abc%3D%3D → decodes to abc==
    stubCookie('__Host-csrf=abc%3D%3D')
    expect(readCSRFCookie()).toBe('abc==')
  })

  it('decodes a value with plus sign encoding', () => {
    // %2B decodes to +
    stubCookie('__Host-csrf=tok%2Bvalue')
    expect(readCSRFCookie()).toBe('tok+value')
  })

  it('falls back to raw string on malformed percent-encoding', () => {
    // %ZZ is not a valid percent-encoded sequence — decodeURIComponent throws.
    stubCookie('__Host-csrf=abc%ZZ')
    expect(readCSRFCookie()).toBe('abc%ZZ')
  })

  it('handles lone percent sign at end without throwing', () => {
    stubCookie('__Host-csrf=tok%')
    expect(readCSRFCookie()).toBe('tok%')
  })

  it('picks the correct cookie when multiple are present', () => {
    stubCookie('a=1; __Host-csrf=correct_token; b=2')
    expect(readCSRFCookie()).toBe('correct_token')
  })

  it('handles leading whitespace around cookie pairs', () => {
    stubCookie('  __Host-csrf=spaced_token  ')
    // trim() is applied to each part, so leading/trailing spaces around the
    // pair are stripped before the prefix match.
    expect(readCSRFCookie()).toBe('spaced_token')
  })
})

// ── Group 2: integration — X-CSRF-Token header is set from decoded cookie ──────
//
// We import the api module so the real readCSRFCookie runs, stub fetch, and
// assert that the header value matches the decoded cookie, not the raw one.

describe('api request: X-CSRF-Token header uses decoded cookie value', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify([]), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )
    vi.stubGlobal('fetch', fetchSpy)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    restoreCookie()
  })

  it('sends decoded CSRF value in X-CSRF-Token when cookie is percent-encoded', async () => {
    // Set a percent-encoded cookie value.
    stubCookie('__Host-csrf=abc%3D%3D')

    // Import dynamically so the module uses our stubbed document.cookie.
    const { fetchAgents } = await import('./api')
    await fetchAgents()

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const headers = new Headers(init.headers as HeadersInit)
    expect(headers.get('X-CSRF-Token')).toBe('abc==')
  })

  it('sends raw CSRF value in X-CSRF-Token when cookie is not encoded', async () => {
    stubCookie('__Host-csrf=rawtoken_123')

    const { fetchAgents } = await import('./api')
    await fetchAgents()

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const headers = new Headers(init.headers as HeadersInit)
    expect(headers.get('X-CSRF-Token')).toBe('rawtoken_123')
  })
})

// ── US-5 / FR-010 — cookie auth: credentials:'include', no Authorization
// header, CSRF read fresh per request, 403-CSRF → GET-then-retry-once ──────────
//
// Traces to: docs/internal/specs/preview-on-main-listener-spec.md
//   S8 (no JS token), S9/S10 (cookie + bearer paths), S11/S12 (CSRF required +
//   POST-first recovery), TDD #18 (TestCSRFPostFirstRecovery).

describe('api request: cookie auth (US-5 / FR-010)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    localStorage.clear()
    restoreCookie()
  })

  it('sends credentials:"include" on a GET request', async () => {
    fetchSpy.mockResolvedValueOnce(makeOkResponse([]))
    const { fetchAgents } = await import('./api')
    await fetchAgents()

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(init.credentials).toBe('include')
  })

  it('sends credentials:"include" on a state-changing POST request', async () => {
    stubCookie('__Host-csrf=test-csrf-token')
    fetchSpy.mockResolvedValueOnce(makeOkResponse({ success: true }))
    const { changePassword } = await import('./api')
    await changePassword('old-pw', 'new-pw')

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(init.credentials).toBe('include')
  })

  it('never sends an Authorization header, even when a legacy token key is present in storage', async () => {
    // Simulate a pre-migration leftover key (e.g. from a browser that never
    // reloaded after upgrade) — the SPA must not read it (US-5 Non-Behavior:
    // MUST NOT store/read the auth token).
    sessionStorage.setItem('omnipus_auth_token', 'leftover-token')
    localStorage.setItem('omnipus_auth_token', 'leftover-token')
    fetchSpy.mockResolvedValueOnce(makeOkResponse([]))

    const { fetchAgents } = await import('./api')
    await fetchAgents()

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const headers = new Headers(init.headers as HeadersInit)
    expect(headers.get('Authorization')).toBeNull()
  })

  it('reads the CSRF cookie fresh on each request — a value change between two calls is reflected on the second, not cached from the first', async () => {
    // A Response body can only be read once — use a fresh instance per call
    // (a shared mockResolvedValue would make the second .json() call fail).
    fetchSpy
      .mockResolvedValueOnce(makeOkResponse({ success: true }))
      .mockResolvedValueOnce(makeOkResponse({ success: true }))
    const { changePassword } = await import('./api')

    stubCookie('__Host-csrf=first-token')
    await changePassword('old-pw', 'new-pw')
    const [, init1] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(new Headers(init1.headers as HeadersInit).get('X-CSRF-Token')).toBe('first-token')

    // Cookie rotates (e.g. server re-minted it) — no caller-side action other
    // than the cookie jar changing.
    stubCookie('__Host-csrf=second-token')
    await changePassword('old-pw', 'new-pw2')
    const [, init2] = fetchSpy.mock.calls[1] as [string, RequestInit]
    expect(new Headers(init2.headers as HeadersInit).get('X-CSRF-Token')).toBe('second-token')
  })

  it('r4-MAJ-004: a 403 with a CSRF-specific body triggers a GET re-mint then retries the original request once, succeeding', async () => {
    stubCookie('__Host-csrf=stale-token')
    const csrfFailure = new Response(JSON.stringify({ error: 'csrf token mismatch' }), {
      status: 403,
      headers: { 'Content-Type': 'application/json' },
    })
    fetchSpy
      .mockResolvedValueOnce(csrfFailure) // original POST — rejected
      .mockResolvedValueOnce(makeOkResponse({ onboarding_complete: true })) // GET /state remint trigger
      .mockResolvedValueOnce(makeOkResponse({ success: true })) // retried POST — succeeds

    const { changePassword } = await import('./api')
    const result = await changePassword('old-pw', 'new-pw')

    expect(result).toEqual({ success: true })
    expect(fetchSpy).toHaveBeenCalledTimes(3)
    const [url1, init1] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const [url2] = fetchSpy.mock.calls[1] as [string, RequestInit]
    const [url3, init3] = fetchSpy.mock.calls[2] as [string, RequestInit]
    expect(url1).toContain('/api/v1/auth/change-password')
    expect((init1.method ?? '').toUpperCase()).toBe('POST')
    expect(url2).toContain('/api/v1/state')
    expect(url3).toContain('/api/v1/auth/change-password')
    expect((init3.method ?? '').toUpperCase()).toBe('POST')
  })

  it('does not loop more than once: a CSRF 403 that persists after the retry surfaces the failure', async () => {
    stubCookie('__Host-csrf=stale-token')
    const csrfFailure = new Response(JSON.stringify({ error: 'csrf cookie missing' }), {
      status: 403,
      headers: { 'Content-Type': 'application/json' },
    })
    fetchSpy
      .mockResolvedValueOnce(csrfFailure) // original POST — rejected
      .mockResolvedValueOnce(makeOkResponse({ onboarding_complete: true })) // GET remint
      .mockResolvedValueOnce(csrfFailure) // retried POST — STILL rejected

    const { changePassword } = await import('./api')
    await expect(changePassword('old-pw', 'new-pw')).rejects.toMatchObject({ status: 403 })

    // Exactly 3 calls (original + remint GET + one retry) — never a second retry.
    expect(fetchSpy).toHaveBeenCalledTimes(3)
  })

  it('does NOT retry a generic (non-CSRF) 403 — e.g. a permission/RBAC denial', async () => {
    stubCookie('__Host-csrf=test-csrf-token')
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'forbidden' }), { status: 403, headers: { 'Content-Type': 'application/json' } }),
    )

    const { changePassword } = await import('./api')
    await expect(changePassword('old-pw', 'new-pw')).rejects.toMatchObject({ status: 403 })

    // Only the original call — no GET re-mint, no retry, for a 403 whose body
    // doesn't indicate a CSRF failure.
    expect(fetchSpy).toHaveBeenCalledTimes(1)
  })

  it('does not retry a CSRF 403 on a safe GET (only state-changing methods retry)', async () => {
    stubCookie('__Host-csrf=test-csrf-token')
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'csrf cookie missing' }), { status: 403, headers: { 'Content-Type': 'application/json' } }),
    )

    const { fetchAgents } = await import('./api')
    await expect(fetchAgents()).rejects.toMatchObject({ status: 403 })

    // GET is not state-changing — no retry logic applies, whatever the body says.
    expect(fetchSpy).toHaveBeenCalledTimes(1)
  })
})

// ── Security admin helpers ─────────────────────────────────────────────────────
//
// Each test verifies: URL, method, headers (CSRF on state-changing), body, and
// error-path throwing a typed error on non-2xx.

function makeOkResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function make400Response(errText: string): Response {
  return new Response(errText, { status: 400 })
}

// make204Response builds a real HTTP 204 No Content response — no body, no
// JSON content-type. This is what the gateway returns from DELETE/PUT handlers
// that have nothing to send back. Calling .json() on such a Response throws
// ("Unexpected end of JSON input"); the request() helper must short-circuit
// before that throw so the mutation resolves successfully (C1).
function make204Response(): Response {
  // `new Response(null, { status: 204 })` is the spec-correct way to model a
  // no-content response: the body is null and .json() would reject.
  return new Response(null, { status: 204 })
}

// make202Response models the real gateway response for POST /tasks/{id}/runs
// ("Run now", ADR-050 RD7): 202 Accepted, no body — the run is dispatched
// asynchronously (observe progress via the task_run_status WS frame or a
// GET /tasks/{id}/runs refetch, never this response).
function make202Response(): Response {
  return new Response(null, { status: 202 })
}

describe('Security API helpers', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookie('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookie()
    vi.resetModules()
  })

  // ── fetchPendingRestart ────────────────────────────────────────────────────

  describe('fetchPendingRestart', () => {
    it('GET /api/v1/config/pending-restart — happy path', async () => {
      const payload = [{ key: 'security.prompt_guard', applied_value: 'low', persisted_value: 'high' }]
      fetchSpy.mockResolvedValueOnce(makeOkResponse(payload))

      const { fetchPendingRestart } = await import('./api')
      const result = await fetchPendingRestart()

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/config/pending-restart')
      expect((init.method ?? 'GET').toUpperCase()).toBe('GET')
      expect(result).toEqual(payload)
    })
  })

  // ── fetchSkillTrust / updateSkillTrust ────────────────────────────────────

  describe('fetchSkillTrust', () => {
    it('GET /api/v1/security/skill-trust — returns level', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ level: 'warn_unverified' as SkillTrustLevel }))

      const { fetchSkillTrust } = await import('./api')
      const result = await fetchSkillTrust()

      const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/skill-trust')
      expect(result.level).toBe('warn_unverified')
    })
  })

  describe('updateSkillTrust', () => {
    it('PUT /api/v1/security/skill-trust — sends CSRF and correct body', async () => {
      fetchSpy.mockResolvedValueOnce(
        makeOkResponse({ saved: true, requires_restart: true, applied_level: 'block_unverified' }),
      )

      const { updateSkillTrust } = await import('./api')
      await updateSkillTrust('block_unverified')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/skill-trust')
      expect((init.method ?? '').toUpperCase()).toBe('PUT')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual({ level: 'block_unverified' })
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('invalid level'))

      const { updateSkillTrust } = await import('./api')
      await expect(updateSkillTrust('allow_all')).rejects.toThrow('400')
    })
  })

  // ── fetchPromptGuardLevel / updatePromptGuardLevel ────────────────────────

  describe('fetchPromptGuardLevel', () => {
    it('GET /api/v1/security/prompt-guard — returns level', async () => {
      // PromptGuardResponse requires {level, requires_restart} per contracts/openapi.yaml
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ level: 'medium' as PromptInjectionLevel, requires_restart: false }))

      const { fetchPromptGuardLevel } = await import('./api')
      const result = await fetchPromptGuardLevel()

      expect(result.level).toBe('medium')
    })
  })

  describe('updatePromptGuardLevel', () => {
    it('PUT /api/v1/security/prompt-guard — sends CSRF and level body', async () => {
      fetchSpy.mockResolvedValueOnce(
        makeOkResponse({ saved: true, requires_restart: false, applied_level: 'high' }),
      )

      const { updatePromptGuardLevel } = await import('./api')
      await updatePromptGuardLevel('high')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/prompt-guard')
      expect((init.method ?? '').toUpperCase()).toBe('PUT')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual({ level: 'high' })
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('invalid level'))

      const { updatePromptGuardLevel } = await import('./api')
      await expect(updatePromptGuardLevel('low')).rejects.toThrow('400')
    })
  })

  // ── fetchSandboxConfig / updateSandboxConfig ──────────────────────────────

  describe('fetchSandboxConfig', () => {
    it('GET /api/v1/security/sandbox-config — returns config', async () => {
      // 'enforce' is a valid SandboxMode per contracts/openapi.yaml (off|permissive|enforce)
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ mode: 'enforce', allowed_paths: ['/tmp'] }))

      const { fetchSandboxConfig } = await import('./api')
      const result = await fetchSandboxConfig()

      const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/sandbox-config')
      expect(result.mode).toBe('enforce')
    })
  })

  describe('updateSandboxConfig', () => {
    it('PUT /api/v1/security/sandbox-config — sends CSRF and body', async () => {
      // 'permissive' is a valid SandboxMode per contracts/openapi.yaml (off|permissive|enforce)
      const body = { mode: 'permissive' as const, allowed_paths: ['/tmp'], ssrf: { enabled: true, allow_internal: ['127.0.0.1'] } }
      fetchSpy.mockResolvedValueOnce(makeOkResponse(body))

      const { updateSandboxConfig } = await import('./api')
      await updateSandboxConfig(body)

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/sandbox-config')
      expect((init.method ?? '').toUpperCase()).toBe('PUT')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual(body)
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('invalid config'))

      const { updateSandboxConfig } = await import('./api')
      // @ts-expect-error — deliberately pass an invalid mode to verify error handling
      await expect(updateSandboxConfig({ mode: 'bad' })).rejects.toThrow('400')
    })
  })

  // ── 204 No Content handling (C1) ──────────────────────────────────────────
  //
  // Successful DELETE/PUT handlers that return 204 have no body. The request()
  // helper must resolve without calling res.json() (which would throw on an
  // empty body and make the mutation appear to fail). These tests exercise the
  // real production code path (not a mock of request()) with a real 204 Response.

  describe('204 No Content responses (C1)', () => {
    it('deleteTask resolves successfully on a real 204 (no body)', async () => {
      fetchSpy.mockResolvedValueOnce(make204Response())

      const { deleteTask } = await import('./api')
      // Must NOT throw "Unexpected end of JSON input" — a 204 is a success.
      await expect(deleteTask('task-1')).resolves.toBeUndefined()

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/tasks/task-1')
      expect((init.method ?? '').toUpperCase()).toBe('DELETE')
    })

    it('deleteSkill resolves successfully on a real 204', async () => {
      fetchSpy.mockResolvedValueOnce(make204Response())

      const { deleteSkill } = await import('./api')
      await expect(deleteSkill('my-skill')).resolves.toBeUndefined()
    })

    it('deleteMcpServer resolves successfully on a real 204', async () => {
      fetchSpy.mockResolvedValueOnce(make204Response())

      const { deleteMcpServer } = await import('./api')
      await expect(deleteMcpServer('srv-1')).resolves.toBeUndefined()
    })

    it('still throws a typed error when a delete returns 4xx', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('task is running'))

      const { deleteTask } = await import('./api')
      await expect(deleteTask('task-1')).rejects.toThrow('400')
    })
  })

  // ── Task run history (ADR-050 / task-run-history-spec §4.1) ────────────────

  describe('fetchTaskRuns', () => {
    it('GET /api/v1/tasks/{id}/runs — returns the TaskRun array', async () => {
      const runs = [
        {
          run_id: 'run-2',
          task_id: 'task-1',
          occurrence_ms: null,
          status: 'done',
          result: 'Finished.',
          session_id: 'sess-2',
          kind: 'manual',
          started_at: '2026-07-20T09:00:00Z',
          ended_at: '2026-07-20T09:05:00Z',
        },
        {
          run_id: 'run-1',
          task_id: 'task-1',
          occurrence_ms: 1784620800000,
          status: 'failed',
          result: 'Boom.',
          session_id: 'sess-1',
          kind: 'scheduled',
          started_at: '2026-07-19T09:00:00Z',
          ended_at: '2026-07-19T09:05:00Z',
        },
      ]
      fetchSpy.mockResolvedValueOnce(makeOkResponse(runs))

      const { fetchTaskRuns } = await import('./api')
      const result = await fetchTaskRuns('task-1')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/tasks/task-1/runs')
      expect((init.method ?? 'GET').toUpperCase()).toBe('GET')
      expect(result).toEqual(runs)
    })

    it('throws ApiSchemaError when a run fails TaskRun schema validation', async () => {
      // Missing required `session_id` — schema-invalid.
      fetchSpy.mockResolvedValueOnce(makeOkResponse([{
        run_id: 'run-1',
        task_id: 'task-1',
        occurrence_ms: null,
        status: 'done',
        kind: 'manual',
        started_at: '2026-07-20T09:00:00Z',
        ended_at: '2026-07-20T09:05:00Z',
      }]))

      const { fetchTaskRuns, ApiSchemaError: ApiSchemaErrorCtor } = await import('./api')
      await expect(fetchTaskRuns('task-1')).rejects.toBeInstanceOf(ApiSchemaErrorCtor)
    })

    it('propagates a typed error on 404 (unknown task)', async () => {
      fetchSpy.mockResolvedValueOnce(new Response('not found', { status: 404 }))

      const { fetchTaskRuns } = await import('./api')
      await expect(fetchTaskRuns('missing-task')).rejects.toThrow('404')
    })
  })

  describe('runTaskNow', () => {
    it('POST /api/v1/tasks/{id}/runs — sends {occurrence_ms} when provided and resolves on 202', async () => {
      fetchSpy.mockResolvedValueOnce(make202Response())

      const { runTaskNow } = await import('./api')
      await expect(runTaskNow('task-1', 1784620800000)).resolves.toBeUndefined()

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/tasks/task-1/runs')
      expect((init.method ?? '').toUpperCase()).toBe('POST')
      expect(JSON.parse(init.body as string)).toEqual({ occurrence_ms: 1784620800000 })
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
    })

    it('sends {occurrence_ms: null} when explicitly passed null (re-run signal, not omission)', async () => {
      fetchSpy.mockResolvedValueOnce(make202Response())

      const { runTaskNow } = await import('./api')
      await runTaskNow('task-1', null)

      const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(JSON.parse(init.body as string)).toEqual({ occurrence_ms: null })
    })

    it('sends an empty body when occurrenceMs is omitted (re-run a normal/once task)', async () => {
      fetchSpy.mockResolvedValueOnce(make202Response())

      const { runTaskNow } = await import('./api')
      await runTaskNow('task-1')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/tasks/task-1/runs')
      expect((init.method ?? '').toUpperCase()).toBe('POST')
      expect(init.body).toBeUndefined()
    })

    it('throws a typed error on 404 (unknown task)', async () => {
      fetchSpy.mockResolvedValueOnce(new Response('not found', { status: 404 }))

      const { runTaskNow } = await import('./api')
      await expect(runTaskNow('missing-task')).rejects.toThrow('404')
    })

    it('throws a typed error on 503 (executor unavailable)', async () => {
      fetchSpy.mockResolvedValueOnce(new Response('gateway degraded', { status: 503 }))

      const { runTaskNow } = await import('./api')
      await expect(runTaskNow('task-1')).rejects.toThrow('503')
    })
  })

})

// ── US-8/FR-015 — isPreviewEnabled accessor ─────────────────────────────────────
//
// preview_enabled is an optional bool where undefined semantically means
// "true" (old gateway versions that predate the field always served
// previews). Reading the field directly risks treating undefined as falsy;
// the accessor encapsulates this polarity safely. Renamed from the retired
// isPreviewListenerEnabled/preview_listener_enabled (ADR-044: the separate
// preview listener/port/origin are gone — /preview/ lives on the main
// gateway listener, gated only by this live boolean).
//
// Traces to: docs/internal/specs/preview-on-main-listener-spec.md — US-8, FR-015

describe('isPreviewEnabled', () => {
  it('returns true when info is undefined (old gateway — no field present)', async () => {
    const { isPreviewEnabled } = await import('./api')
    expect(isPreviewEnabled(undefined)).toBe(true)
  })

  it('returns true when preview_enabled is undefined (field absent on new gateway)', async () => {
    const { isPreviewEnabled } = await import('./api')
    // Cast: AboutInfo requires version/go_version/os/arch/uptime_seconds in type,
    // but the function only reads preview_enabled — partial is safe here.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect(isPreviewEnabled({ preview_enabled: undefined } as any)).toBe(true)
  })

  it('returns true when preview_enabled is explicitly true', async () => {
    const { isPreviewEnabled } = await import('./api')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect(isPreviewEnabled({ preview_enabled: true } as any)).toBe(true)
  })

  it('returns false when preview_enabled is explicitly false', async () => {
    const { isPreviewEnabled } = await import('./api')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect(isPreviewEnabled({ preview_enabled: false } as any)).toBe(false)
  })

  it('differentiation: true and false inputs produce different outputs', async () => {
    // Anti-shortcut: proves the function is not always returning true or false.
    const { isPreviewEnabled } = await import('./api')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const whenEnabled = isPreviewEnabled({ preview_enabled: true } as any)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const whenDisabled = isPreviewEnabled({ preview_enabled: false } as any)
    expect(whenEnabled).toBe(true)
    expect(whenDisabled).toBe(false)
    expect(whenEnabled).not.toBe(whenDisabled)
  })
})

// ── AboutInfoSchema — preview_enabled replaces preview_port/preview_origin/
// preview_listener_enabled (US-8/FR-015) ─────────────────────────────────────

describe('fetchAboutInfo / AboutInfoSchema — preview_enabled contract (US-8)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function aboutResponse(overrides: Record<string, unknown> = {}) {
    return new Response(
      JSON.stringify({
        version: '1.0.0',
        go_version: 'go1.22.3',
        os: 'linux',
        arch: 'amd64',
        uptime_seconds: 42,
        ...overrides,
      }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    )
  }

  it('accepts a response with preview_enabled: true', async () => {
    fetchSpy.mockResolvedValueOnce(aboutResponse({ preview_enabled: true }))
    const { fetchAboutInfo } = await import('./api')
    const info = await fetchAboutInfo()
    expect(info.preview_enabled).toBe(true)
  })

  it('accepts a response with preview_enabled: false', async () => {
    fetchSpy.mockResolvedValueOnce(aboutResponse({ preview_enabled: false }))
    const { fetchAboutInfo } = await import('./api')
    const info = await fetchAboutInfo()
    expect(info.preview_enabled).toBe(false)
  })

  it('accepts a response with preview_enabled absent (old-gateway back-compat)', async () => {
    fetchSpy.mockResolvedValueOnce(aboutResponse())
    const { fetchAboutInfo } = await import('./api')
    const info = await fetchAboutInfo()
    expect(info.preview_enabled).toBeUndefined()
  })

  it('does NOT surface the retired preview_port/preview_origin/preview_listener_enabled fields', async () => {
    // A gateway that still sent the old fields (shouldn't happen post-migration,
    // but proves the schema doesn't silently resurrect them) — extra JSON keys
    // are simply ignored by z.object(), not merged into the parsed result.
    fetchSpy.mockResolvedValueOnce(
      aboutResponse({ preview_port: 5001, preview_origin: 'https://old.example.com', preview_listener_enabled: true }),
    )
    const { fetchAboutInfo } = await import('./api')
    const info = await fetchAboutInfo()
    expect((info as unknown as Record<string, unknown>).preview_port).toBeUndefined()
    expect((info as unknown as Record<string, unknown>).preview_origin).toBeUndefined()
    expect((info as unknown as Record<string, unknown>).preview_listener_enabled).toBeUndefined()
  })
})

// ── ApiSchemaError ─────────────────────────────────────────────────────────────
//
// Traces to: CLAUDE.md hard-constraint #8 — every API response that is
// schema-validated must surface mismatches as ApiSchemaError (not silently
// discard data). Tests cover constructor fields, instanceof check, and the
// module-level error counter.

describe('ApiSchemaError', () => {
  beforeEach(() => {
    resetApiSchemaErrorCount()
  })

  it('is an instance of Error', () => {
    const err = new ApiSchemaError(
      '/api/v1/agents',
      [{ path: ['name'], message: 'Required' }],
      { id: 1 }
    )
    expect(err).toBeInstanceOf(Error)
    expect(err).toBeInstanceOf(ApiSchemaError)
  })

  it('sets name to ApiSchemaError', () => {
    const err = new ApiSchemaError('/api/v1/agents', [], {})
    expect(err.name).toBe('ApiSchemaError')
  })

  it('stores endpoint, zodIssues, and rawBody', () => {
    const issues = [{ path: ['role'], message: 'Invalid enum value' }]
    const raw = { id: 'abc', role: 'superadmin' }
    const err = new ApiSchemaError('/api/v1/users', issues, raw)

    expect(err.endpoint).toBe('/api/v1/users')
    expect(err.zodIssues).toEqual(issues)
    expect(err.rawBody).toBe(raw)
  })

  it('message includes the endpoint and first issue message', () => {
    const err = new ApiSchemaError(
      '/api/v1/sessions',
      [{ path: ['id'], message: 'Expected string, received number' }],
      { id: 42 }
    )
    expect(err.message).toContain('/api/v1/sessions')
    expect(err.message).toContain('Expected string, received number')
  })

  it('message handles empty zodIssues gracefully', () => {
    const err = new ApiSchemaError('/api/v1/agents', [], null)
    expect(err.message).toContain('/api/v1/agents')
    expect(err.message).toContain('unknown')
  })

  it('rawBody can be null', () => {
    const err = new ApiSchemaError('/test', [{ path: [], message: 'bad' }], null)
    expect(err.rawBody).toBeNull()
  })

  it('rawBody can be a primitive', () => {
    const err = new ApiSchemaError('/test', [{ path: [], message: 'bad' }], 'not-an-object')
    expect(err.rawBody).toBe('not-an-object')
  })
})

// ── getApiSchemaErrorCount / resetApiSchemaErrorCount ─────────────────────────
//
// The counter is module-level state. Because Vitest re-imports the module once
// per test file (not once per test), we reset it explicitly in beforeEach.
// Direct counter manipulation is not possible from tests, so we exercise the
// counter via the real request() path with a Zod schema that fails.

describe('getApiSchemaErrorCount / resetApiSchemaErrorCount', () => {
  beforeEach(() => {
    resetApiSchemaErrorCount()
  })

  it('starts at 0 after reset', () => {
    expect(getApiSchemaErrorCount()).toBe(0)
  })

  it('reset after multiple calls still returns 0', () => {
    resetApiSchemaErrorCount()
    resetApiSchemaErrorCount()
    expect(getApiSchemaErrorCount()).toBe(0)
  })
})

// ── Schema validation through real request() call ───────────────────────────────
//
// These tests verify that request() with an explicit Zod schema:
// 1. Throws ApiSchemaError when the response body fails validation
// 2. Increments _apiSchemaErrorCount on failure
// 3. Throws ApiSchemaError when the response body is not valid JSON
//
// Tests use the generated LoginResponse schema (a simple well-understood shape)
// and the live fetchAgents/fetchExecAllowlist functions which now pass schemas.

describe('request() with Zod schema — validation errors', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  function stubCookie2(value: string) {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => value,
    })
  }

  function restoreCookie2() {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (document as any).cookie
  }

  beforeEach(() => {
    resetApiSchemaErrorCount()
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookie2('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookie2()
    vi.resetModules()
  })

  it('fetchAgents: throws ApiSchemaError when body fails Agent schema validation', async () => {
    // Return a valid JSON body that fails Agent schema (missing required fields)
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify([{ id: 'a', name: 'bad' }]), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    )

    const { fetchAgents, ApiSchemaError: ApiSchemaErrorClass, getApiSchemaErrorCount: count } = await import('./api')
    await expect(fetchAgents()).rejects.toBeInstanceOf(ApiSchemaErrorClass)
    expect(count()).toBe(1)
  })

  it('fetchAgents: increments _apiSchemaErrorCount on validation failure', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify([{ invalid: true }]), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    )

    const { fetchAgents, getApiSchemaErrorCount: count } = await import('./api')
    try {
      await fetchAgents()
    } catch {
      // expected
    }
    expect(count()).toBe(1)
  })

  it('login: throws ApiSchemaError when body fails LoginResponse schema', async () => {
    // Return a body missing the required `token` field
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ role: 'admin', username: 'alice' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    )

    const { login, ApiSchemaError: ApiSchemaErrorClass } = await import('./api')
    await expect(login('alice', 'pass')).rejects.toBeInstanceOf(ApiSchemaErrorClass)
  })

  it('login: returns valid data when schema passes', async () => {
    // LoginResponse.token enforces exact-72-char `omnipus_<hex64>` format.
    const validToken = 'omnipus_' + 'a'.repeat(64)
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ token: validToken, role: 'admin', username: 'alice' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    )

    const { login, getApiSchemaErrorCount: count } = await import('./api')
    const result = await login('alice', 'pass')
    expect(result.token).toBe(validToken)
    expect(count()).toBe(0)
  })

  it('request() with schema throws ApiSchemaError when body is not JSON (HTML 200)', async () => {
    // Simulate a server returning an HTML error page with HTTP 200 (misconfigured reverse proxy)
    fetchSpy.mockResolvedValueOnce(
      new Response('<!DOCTYPE html><html><body>502 Bad Gateway</body></html>', {
        status: 200,
        headers: { 'Content-Type': 'text/html' },
      })
    )

    const { fetchExecAllowlist, ApiSchemaError: ApiSchemaErrorClass, getApiSchemaErrorCount: count } = await import('./api')
    await expect(fetchExecAllowlist()).rejects.toBeInstanceOf(ApiSchemaErrorClass)
    expect(count()).toBe(1)
  })
})

// ── fetchSessionMessages renames parameters → params ─────────────────────────────
//
// Regression test for Problem 1. The wire ToolCall schema emits `parameters`
// (matching Go json tag `json:"parameters,omitempty"`). The SPA ToolCall type
// uses `params`. Without the rawToToolCall() transform, params was `undefined`
// and ToolCallBadge rendered `JSON.stringify(undefined, null, 2)` = "undefined".
//
// This test stubs fetch to return the wire shape (with `parameters`) and
// asserts that the returned Message[].tool_calls[].params is correctly populated.

describe('fetchSessionMessages: wire parameters → SPA params transform', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  function stubCookieLocal(value: string) {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => value,
    })
  }

  function restoreCookieLocal() {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (document as any).cookie
  }

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    // fetchSessionMessages is a GET, no CSRF needed — but setting the cookie
    // avoids CSRF guard triggering on any incidental state-changing helpers.
    stubCookieLocal('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookieLocal()
    vi.resetModules()
  })

  it('renames tool_calls[].parameters to tool_calls[].params', async () => {
    // Wire payload: ToolCall uses `parameters`, not `params`.
    const wirePayload = [
      {
        id: 'msg-1',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'here is the result',
        timestamp: '2026-05-18T10:00:00Z',
        status: 'ok',
        tool_calls: [
          {
            id: 'tc-1',
            tool: 'foo_tool',
            status: 'success',
            parameters: { x: 1, y: 'hello' },
          },
        ],
      },
    ]

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchSessionMessages } = await import('./api')
    const messages = await fetchSessionMessages('sid-abc')

    // The returned message should have the transformed tool call.
    expect(messages).toHaveLength(1)
    expect(messages[0].tool_calls).toHaveLength(1)
    // params must equal the wire `parameters` value — NOT undefined.
    expect(messages[0].tool_calls![0].params).toEqual({ x: 1, y: 'hello' })
    // The raw `parameters` key must NOT appear on the SPA type.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect((messages[0].tool_calls![0] as any).parameters).toBeUndefined()
  })

  it('returns empty params ({}) when wire parameters field is absent', async () => {
    // Wire ToolCall with no parameters field — params should default to {}.
    const wirePayload = [
      {
        id: 'msg-2',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'done',
        timestamp: '2026-05-18T10:01:00Z',
        tool_calls: [
          {
            id: 'tc-2',
            tool: 'bar_tool',
            status: 'success',
          },
        ],
      },
    ]

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchSessionMessages } = await import('./api')
    const messages = await fetchSessionMessages('sid-xyz')

    expect(messages[0].tool_calls![0].params).toEqual({})
  })

  it('maps wire status "ok" → SPA status "done"', async () => {
    const wirePayload = [
      {
        id: 'msg-3',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'test',
        timestamp: '2026-05-18T10:02:00Z',
        status: 'ok',
      },
    ]

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchSessionMessages } = await import('./api')
    const messages = await fetchSessionMessages('sid-status')

    expect(messages[0].status).toBe('done')
  })

  it('maps wire tool_call status "denied" → SPA status "cancelled"', async () => {
    const wirePayload = [
      {
        id: 'msg-4',
        agent_id: 'agent-1',
        role: 'assistant',
        content: '',
        timestamp: '2026-05-18T10:03:00Z',
        tool_calls: [
          { id: 'tc-denied', tool: 'restricted_tool', status: 'denied' },
        ],
      },
    ]

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchSessionMessages } = await import('./api')
    const messages = await fetchSessionMessages('sid-denied')

    expect(messages[0].tool_calls![0].status).toBe('cancelled')
  })

  it('carries per-message wire agent_id onto SPA message.agentId (handover replay)', async () => {
    // Regression for the handover reload bug: cold-load (REST) transcripts must
    // expose each message's authoring agent so the row renders under its true
    // author. Mia's pre-handover turn stays Mia; Jim's post-handover turn is Jim.
    const wirePayload = [
      {
        id: 'm-mia',
        agent_id: 'mia',
        role: 'assistant',
        content: 'I will hand this to Jim.',
        timestamp: '2026-05-18T10:00:00Z',
        status: 'ok',
      },
      {
        id: 'm-jim',
        agent_id: 'jim',
        role: 'assistant',
        content: 'On it.',
        timestamp: '2026-05-18T10:01:00Z',
        status: 'ok',
      },
    ]

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchSessionMessages } = await import('./api')
    const messages = await fetchSessionMessages('sid-handover')

    expect(messages).toHaveLength(2)
    expect(messages[0].agentId).toBe('mia')
    expect(messages[1].agentId).toBe('jim')
  })

  // Regression guards for the 2026-05-21 production bug: Message.yaml's
  // `type` enum was missing "tool_call" and "turn_canceled". A jim session
  // with 44 tool_call entries failed the SPA's Zod validation with
  // "Backend response failed validation". After the schema fix these
  // shapes must round-trip without ApiSchemaError.

  it('accepts type:"tool_call" entries (regression for 2026-05-21 bug)', async () => {
    const wirePayload = [
      {
        id: 'call_abc',
        type: 'tool_call',
        agent_id: 'jim',
        timestamp: '2026-05-21T04:20:00Z',
        tool_calls: [
          {
            id: 'call_abc',
            tool: 'write_file',
            status: 'success',
            parameters: { path: '/tmp/x.txt' },
          },
        ],
      },
    ]
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages } = await import('./api')
    const messages = await fetchSessionMessages('sid-toolcall')
    expect(messages).toHaveLength(1)
    expect(messages[0].id).toBe('call_abc')
  })

  it('accepts type:"turn_canceled" entries with cancel-specific fields', async () => {
    // Includes the cancel-specific fields added to Message.yaml in this
    // branch (turn_id, canceled_by_user, canceled_by_channel, cancel_method,
    // descendants_canceled). These were silently stripped by Zod's non-strict
    // object default; now they're modelled in the schema.
    const wirePayload = [
      {
        id: 'cancel_xyz',
        type: 'turn_canceled',
        agent_id: 'mia',
        timestamp: '2026-05-21T04:25:00Z',
        turn_id: 'turn-T3',
        canceled_by_user: 'admin',
        canceled_by_channel: 'webchat',
        cancel_method: 'graceful',
        descendants_canceled: ['turn-T3-sub-1'],
      },
    ]
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages } = await import('./api')
    const messages = await fetchSessionMessages('sid-cancel')
    expect(messages).toHaveLength(1)
    expect(messages[0].id).toBe('cancel_xyz')
  })

  it('degrades an unknown entry type to a placeholder instead of rejecting the whole list (Issue 3 / library-uat HIGH)', async () => {
    // Updated for the per-item resilience fix. Previously this asserted
    // fetchSessionMessages REJECTED the whole array on a single out-of-enum
    // `type` value — that all-or-nothing behaviour is exactly the bug the
    // live UAT reproduced (one malformed attachment `type` made an entire
    // session permanently unloadable, Retry included). The contract-drift
    // signal is preserved (still counted via getApiSchemaErrorCount, so a
    // future EntryType added without a schema update is NOT silently
    // invisible) but a single bad row must no longer take the whole
    // transcript down — see the block comment above placeholderMessage()
    // in src/lib/api.ts for the full rationale.
    const wirePayload = [
      {
        id: 'msg_x',
        type: 'wholly_unknown_entry_kind',
        agent_id: 'jim',
        timestamp: '2026-05-21T10:00:00Z',
      },
    ]
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages, getApiSchemaErrorCount: count } = await import('./api')
    const messages = await fetchSessionMessages('sid-unknown')
    expect(messages).toHaveLength(1)
    expect(messages[0].role).toBe('system')
    expect(messages[0].content).toBe('This message could not be displayed.')
    expect(count()).toBe(1)
  })

  it('keeps the valid messages and replaces only the malformed one when a list is mixed (Issue 3 / library-uat HIGH)', async () => {
    // Reproduces the exact live-server shape from the UAT finding: an
    // attachment `type` of "document", which is outside the Attachment
    // enum (image|audio|video|file). Sits between two otherwise-valid
    // messages so we can assert order, count, and content are all correct.
    const wirePayload = [
      {
        id: 'msg-before',
        agent_id: 'agent-1',
        role: 'user',
        content: 'here is a file',
        timestamp: '2026-07-20T10:00:00Z',
        status: 'ok',
      },
      {
        id: 'msg-bad-attachment',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'got it',
        timestamp: '2026-07-20T10:00:01Z',
        status: 'ok',
        attachments: [
          { type: 'document', path: '/work/spec.docx', size: 1024, mime_type: 'application/msword' },
        ],
      },
      {
        id: 'msg-after',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'anything else?',
        timestamp: '2026-07-20T10:00:02Z',
        status: 'ok',
      },
    ]
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages, getApiSchemaErrorCount: count } = await import('./api')
    const messages = await fetchSessionMessages('sid-mixed')

    // All three positions are present — the bad one is a visible placeholder,
    // not a silently vanished row (see the judgment-call comment in api.ts).
    expect(messages).toHaveLength(3)
    expect(messages[0].id).toBe('msg-before')
    expect(messages[0].content).toBe('here is a file')
    expect(messages[1].id).toBe('msg-bad-attachment')
    expect(messages[1].role).toBe('system')
    expect(messages[1].content).toBe('This message could not be displayed.')
    expect(messages[2].id).toBe('msg-after')
    expect(messages[2].content).toBe('anything else?')

    // Counted through the existing _recordApiSchemaError path (no parallel counter).
    expect(count()).toBe(1)
  })

  it('leaves a fully valid list completely unaffected — no drops counted', async () => {
    const wirePayload = [
      {
        id: 'msg-a',
        agent_id: 'agent-1',
        role: 'user',
        content: 'hello',
        timestamp: '2026-07-20T10:00:00Z',
        status: 'ok',
      },
      {
        id: 'msg-b',
        agent_id: 'agent-1',
        role: 'assistant',
        content: 'hi there',
        timestamp: '2026-07-20T10:00:01Z',
        status: 'ok',
        attachments: [
          { type: 'image', path: '/work/photo.png', size: 2048, mime_type: 'image/png' },
        ],
      },
    ]
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages, getApiSchemaErrorCount: count } = await import('./api')
    const messages = await fetchSessionMessages('sid-all-valid')
    expect(messages).toHaveLength(2)
    expect(messages.map((m) => m.id)).toEqual(['msg-a', 'msg-b'])
    expect(count()).toBe(0)
  })

  it('still throws ApiSchemaError when the body is not an array at all (genuine contract break, not a list-item defect)', async () => {
    // Scoping guard: per-item recovery is for LIST responses only. A
    // response that isn't even shaped like a list (e.g. an error object)
    // is a real contract break and must still fail loudly.
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'not a list' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionMessages, ApiSchemaError } = await import('./api')
    await expect(fetchSessionMessages('sid-not-array')).rejects.toBeInstanceOf(ApiSchemaError)
  })
})

// ── fetchSessionDetail: per-item message resilience (Issue 3 / library-uat HIGH) ──
//
// GET /sessions/{id} (SessionDetail) nests the SAME messages array inside its
// body. Mirrors the fetchSessionMessages coverage above: the `session`
// object (single-object response) still fails loudly, while the nested
// `messages` list degrades per-item.

describe('fetchSessionDetail: per-item message resilience', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  function stubCookieLocal(value: string) {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => value,
    })
  }

  function restoreCookieLocal() {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (document as any).cookie
  }

  const validSession = {
    id: 'sid-detail',
    agent_id: 'agent-1',
    title: 'Test session',
    status: 'active',
    created_at: '2026-07-20T09:00:00Z',
    updated_at: '2026-07-20T09:00:00Z',
    stats: {
      tokens_in: 0,
      tokens_out: 0,
      tokens_total: 0,
      cost: 0,
      tool_calls: 0,
      message_count: 2,
    },
    channel: 'webchat',
    partitions: ['2026-07-20'],
  }

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookieLocal('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookieLocal()
    vi.resetModules()
  })

  it('keeps the valid session and messages, replacing only the malformed message', async () => {
    const wirePayload = {
      session: validSession,
      messages: [
        {
          id: 'msg-ok',
          agent_id: 'agent-1',
          role: 'user',
          content: 'hi',
          timestamp: '2026-07-20T10:00:00Z',
          status: 'ok',
        },
        {
          id: 'msg-bad',
          agent_id: 'agent-1',
          role: 'assistant',
          content: 'here',
          timestamp: '2026-07-20T10:00:01Z',
          status: 'ok',
          attachments: [
            { type: 'document', path: '/work/spec.docx', size: 1024, mime_type: 'application/msword' },
          ],
        },
      ],
    }
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionDetail, getApiSchemaErrorCount: count } = await import('./api')
    const detail = await fetchSessionDetail('sid-detail')
    expect(detail.session.id).toBe('sid-detail')
    expect(detail.messages).toHaveLength(2)
    expect(detail.messages[0].id).toBe('msg-ok')
    expect(detail.messages[1].id).toBe('msg-bad')
    expect(detail.messages[1].role).toBe('system')
    expect(detail.messages[1].content).toBe('This message could not be displayed.')
    expect(count()).toBe(1)
  })

  it('still throws ApiSchemaError when the session object itself is malformed (single-object response — not degraded)', async () => {
    // Scoping guard: the `session` field is a single object, not a list.
    // Per-item recovery must NOT extend to it — a malformed session is a
    // real contract break and should still fail loudly.
    const wirePayload = {
      session: { ...validSession, id: undefined }, // id is required — malformed
      messages: [],
    }
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wirePayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const { fetchSessionDetail, ApiSchemaError } = await import('./api')
    await expect(fetchSessionDetail('sid-bad-session')).rejects.toBeInstanceOf(ApiSchemaError)
  })
})

// ── updateConfig sends wire shape with gateway.host (not bind_address) ──────────
//
// Regression test for Problem 2. Before the fix, updateConfig serialised the
// SPA-flat Config shape directly. The backend expected `gateway.host` but
// received `gateway.bind_address`. This caused silent data loss.
//
// This test asserts that updateConfig translates the SPA-shaped request body
// to the wire-shaped JSON before sending.

describe('updateConfig: sends wire shape to backend', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  function stubCookieLocal2(value: string) {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => value,
    })
  }

  function restoreCookieLocal2() {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (document as any).cookie
  }

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookieLocal2('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookieLocal2()
    vi.resetModules()
  })

  it('translates gateway.bind_address → gateway.host in the PUT request body', async () => {
    // Stub fetch to return a minimal valid raw config response (the server
    // echoes back the full config after applying the change).
    const rawConfigResponse = {
      gateway: { host: '0.0.0.0', port: 8080 },
      security: { policy_mode: 'deny', exec_approval: 'ask' },
      storage: { retention: { session_days: 90 } },
    }

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(rawConfigResponse), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { updateConfig } = await import('./api')
    await updateConfig({ gateway: { bind_address: '0.0.0.0', port: 8080 } })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const body = JSON.parse(init.body as string) as Record<string, unknown>

    // The body must contain gateway.host, NOT gateway.bind_address.
    const gw = body.gateway as Record<string, unknown>
    expect(gw.host).toBe('0.0.0.0')
    expect(gw.bind_address).toBeUndefined()
  })

  it('translates data.session_retention_days → storage.retention.session_days', async () => {
    const rawConfigResponse = {
      gateway: { host: '127.0.0.1', port: 8080 },
      security: { policy_mode: 'deny', exec_approval: 'ask' },
      storage: { retention: { session_days: 30 } },
    }

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(rawConfigResponse), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { updateConfig } = await import('./api')
    await updateConfig({ data: { session_retention_days: 30 } })

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const body = JSON.parse(init.body as string) as Record<string, unknown>

    // storage.retention.session_days must be present; data.session_retention_days must not.
    const storage = body.storage as Record<string, unknown>
    const retention = storage.retention as Record<string, unknown>
    expect(retention.session_days).toBe(30)
    expect(body.data).toBeUndefined()
  })

  it('does not include dev_mode_bypass in the PUT body (blocked server-side)', async () => {
    const rawConfigResponse = {
      gateway: { host: '127.0.0.1', port: 8080 },
      security: { policy_mode: 'deny', exec_approval: 'ask' },
      storage: { retention: { session_days: 90 } },
    }

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(rawConfigResponse), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { updateConfig } = await import('./api')
    await updateConfig({
      gateway: { bind_address: '127.0.0.1', port: 8080, dev_mode_bypass: true },
    })

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const body = JSON.parse(init.body as string) as Record<string, unknown>
    const gw = body.gateway as Record<string, unknown>
    // dev_mode_bypass must never appear in the wire request body.
    expect(gw.dev_mode_bypass).toBeUndefined()
  })
})

// ── BUG 2 regression — fetchCredentials string[]→{key}[] transform ─────────────
//
// The backend returns string[] (key names only). fetchCredentials must transform
// each string into a CredentialKey object so SecuritySection.tsx can render cred.key.
// Before fix-T, no transform existed and cred.key was undefined at runtime.

describe('fetchCredentials: string[] → CredentialKey[] transform (fix-T BUG 2)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookie('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookie()
    vi.resetModules()
  })

  it('transforms string[] wire response to CredentialKey[] with .key property', async () => {
    const wireResponse = ['ANTHROPIC_API_KEY', 'OPENAI_API_KEY', 'GITHUB_TOKEN']
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireResponse), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchCredentials } = await import('./api')
    const result = await fetchCredentials()

    expect(result).toEqual([
      { key: 'ANTHROPIC_API_KEY' },
      { key: 'OPENAI_API_KEY' },
      { key: 'GITHUB_TOKEN' },
    ])
    // Each entry must have a .key so SecuritySection.tsx renders correctly.
    for (const entry of result) {
      expect(typeof entry.key).toBe('string')
      expect(entry.key.length).toBeGreaterThan(0)
    }
  })

  it('returns an empty array when wire response is []', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchCredentials } = await import('./api')
    const result = await fetchCredentials()

    expect(result).toEqual([])
  })

  it('throws ApiSchemaError when wire response is not an array', async () => {
    // The Zod schema validates string[]; any non-array response must fail.
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ keys: ['foo'] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchCredentials, getApiSchemaErrorCount, resetApiSchemaErrorCount } = await import('./api')
    resetApiSchemaErrorCount()
    await expect(fetchCredentials()).rejects.toThrow()
    expect(getApiSchemaErrorCount()).toBe(1)
  })
})

// ── BUG 3 regression — enableChannel/disableChannel ChannelEnabledResponse ────
//
// The backend returns {id, enabled} (ChannelEnabledResponse), not a full ChannelEntry.
// Before fix-T, the SPA Zod schema expected ChannelEntry (name, transport, description)
// and threw ApiSchemaError on every channel toggle.

describe('enableChannel / disableChannel: ChannelEnabledResponse validation (fix-T BUG 3)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookie('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookie()
    vi.resetModules()
  })

  it('enableChannel accepts {id, enabled} response and returns ChannelEnabledResponse', async () => {
    const wire = { id: 'telegram', enabled: true }
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wire), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { enableChannel } = await import('./api')
    const result = await enableChannel('telegram')

    expect(result.id).toBe('telegram')
    expect(result.enabled).toBe(true)
    // Ensure the request was PUT
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/channels/telegram/enable')
    expect((init.method ?? '').toUpperCase()).toBe('PUT')
  })

  it('disableChannel accepts {id, enabled:false} response and returns ChannelEnabledResponse', async () => {
    const wire = { id: 'discord', enabled: false }
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wire), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { disableChannel } = await import('./api')
    const result = await disableChannel('discord')

    expect(result.id).toBe('discord')
    expect(result.enabled).toBe(false)
    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/channels/discord/disable')
  })

  it('enableChannel throws ApiSchemaError when backend returns a full ChannelEntry (old bug)', async () => {
    // Simulate the old incorrect backend response — a full ChannelEntry without
    // the required `enabled` field as a top-level field matching ChannelEnabledResponse.
    // ChannelEnabledResponse requires {id: string, enabled: boolean}; a ChannelEntry
    // response that happens to have those fields should still pass, but a response
    // missing `id` must fail.
    const badWire = { name: 'Telegram', transport: 'telegram', description: 'Telegram channel', enabled: true }
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(badWire), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { enableChannel, getApiSchemaErrorCount, resetApiSchemaErrorCount } = await import('./api')
    resetApiSchemaErrorCount()
    // ChannelEnabledResponse requires `id` (string) — a response without it fails Zod.
    await expect(enableChannel('telegram')).rejects.toThrow()
    expect(getApiSchemaErrorCount()).toBe(1)
  })
})

// ── rawToFrontendConfig / frontendToRawConfig round-trip for default_model ──
//
// Regression guard: the agents.defaults default model was previously not
// threaded through the mapping functions, causing it to be silently dropped
// when settings were read from the backend or saved back. ADR-068 D14.1: it is
// now the exact (provider, model) pair at agents.defaults.default_model.
//
// Traces to: hotfix/v0.1.1 Wave 4 — api round-trip; ADR-068 T068-07

describe('rawToFrontendConfig: preserves agents.defaults.default_model', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  function stubCookieLocal3(value: string) {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => value,
    })
  }

  function restoreCookieLocal3() {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (document as any).cookie
  }

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookieLocal3('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookieLocal3()
    vi.resetModules()
  })

  it('rawToFrontendConfig preserves agents.defaults.default_model', async () => {
    // Traces to: hotfix/v0.1.1 — agents.defaults fields must survive rawToFrontendConfig
    const wireConfig = {
      gateway: { host: '127.0.0.1', port: 8080 },
      security: { policy_mode: 'deny', exec_approval: 'ask' },
      storage: { retention: { session_days: 90 } },
      agents: {
        defaults: {
          default_model: { provider: 'anthropic', model: 'claude-3-haiku' },
        },
      },
    }

    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireConfig), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchConfig } = await import('./api')
    const config = await fetchConfig()

    expect(config.agents?.defaults?.default_model).toEqual({ provider: 'anthropic', model: 'claude-3-haiku' })
  })

  it('frontendToRawConfig round-trips default_model without dropping it', async () => {
    // Traces to: hotfix/v0.1.1 — agents.defaults must survive the full fetchConfig→updateConfig round-trip
    const wireConfig = {
      gateway: { host: '127.0.0.1', port: 8080 },
      security: { policy_mode: 'deny', exec_approval: 'ask' },
      storage: { retention: { session_days: 90 } },
      agents: {
        defaults: {
          default_model: { provider: 'anthropic', model: 'claude-3-haiku' },
        },
      },
    }

    // Mock GET /config response
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireConfig), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    // Mock PUT /config response (echo back the same config)
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireConfig), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchConfig, updateConfig } = await import('./api')
    const fetchedConfig = await fetchConfig()

    // Confirm the fetched config has the pair.
    expect(fetchedConfig.agents?.defaults?.default_model).toEqual({ provider: 'anthropic', model: 'claude-3-haiku' })

    // Send the config back via updateConfig — the round-trip must preserve the fields
    // in the wire body sent to the backend.
    await updateConfig({ agents: fetchedConfig.agents })

    // Inspect the PUT request body (second fetch call).
    const [, putInit] = fetchSpy.mock.calls[1] as [string, RequestInit]
    const putBody = JSON.parse(putInit.body as string) as Record<string, unknown>

    // The wire body must contain agents.defaults with the pair intact.
    const putAgents = putBody.agents as Record<string, unknown>
    const putDefaults = putAgents?.defaults as Record<string, unknown>
    expect(putDefaults?.default_model).toEqual({ provider: 'anthropic', model: 'claude-3-haiku' })
  })
})

// ── rotateGatewayToken schema validation ──────────────────────────────────────
//
// Verifies that rotateGatewayToken() enforces the RotateTokenResponse Zod schema:
// - rejects responses where `token` is the wrong type or malformed
// - accepts a well-formed 72-character `omnipus_<hex64>` token
//
// Traces to: hotfix/v0.1.1 Wave 4 — rotateGatewayToken schema validates response

describe('rotateGatewayToken: schema validation', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  function stubCookieLocal4(value: string) {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => value,
    })
  }

  function restoreCookieLocal4() {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (document as any).cookie
  }

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookieLocal4('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookieLocal4()
    vi.resetModules()
  })

  it('rejects when token is a number (wrong type) — throws ApiSchemaError', async () => {
    // { token: 123 } fails RotateTokenResponse — token must be a string.
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ token: 123 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { rotateGatewayToken, ApiSchemaError: ApiSchemaErrorClass } = await import('./api')
    await expect(rotateGatewayToken()).rejects.toBeInstanceOf(ApiSchemaErrorClass)
  })

  it('rejects when token field is missing — throws ApiSchemaError', async () => {
    // {} fails RotateTokenResponse — token is required.
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { rotateGatewayToken, ApiSchemaError: ApiSchemaErrorClass } = await import('./api')
    await expect(rotateGatewayToken()).rejects.toBeInstanceOf(ApiSchemaErrorClass)
  })

  it('resolves with {token} when a valid 72-char omnipus_<hex64> token is returned', async () => {
    // Valid token: 'omnipus_' (8 chars) + 64 lowercase hex chars = 72 chars total.
    const validToken = 'omnipus_' + 'a'.repeat(64)
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ token: validToken }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { rotateGatewayToken } = await import('./api')
    const result = await rotateGatewayToken()
    expect(result.token).toBe(validToken)
  })

  it('rejects when token is a valid 72-char string but does not match omnipus_<hex64> pattern', async () => {
    // Length 72, but wrong format: no "omnipus_" prefix and not lowercase hex.
    const wrongFormat = 'X'.repeat(72)
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ token: wrongFormat }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { rotateGatewayToken, ApiSchemaError: ApiSchemaErrorClass } = await import('./api')
    await expect(rotateGatewayToken()).rejects.toBeInstanceOf(ApiSchemaErrorClass)
  })
})

// ── validEnum / _configCoercionCount integration tests ────────────────────────
//
// Verifies that rawToFrontendConfig calls validEnum which increments _configCoercionCount
// when the backend returns an invalid enum value for security.policy_mode.
// Also verifies that valid enum values do NOT increment the counter.

describe('validEnum / _configCoercionCount', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
  })

  it('increments counter when backend returns invalid enum value for security.policy_mode', async () => {
    // Simulate backend returning "garbage" for security.policy_mode —
    // not one of the valid values: allow | deny.
    const wireConfig = {
      gateway: { host: '127.0.0.1', port: 8080 },
      security: { policy_mode: 'garbage' },
    }
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireConfig), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchConfig, getConfigCoercionCount, resetConfigCoercionCount } = await import('./api')
    resetConfigCoercionCount()

    const config = await fetchConfig()

    // The coercion counter must have been incremented by at least 1 (for policy_mode).
    expect(getConfigCoercionCount()).toBeGreaterThan(0)
    // The invalid value must be replaced by the fallback ("deny").
    expect(config.security.policy_mode).toBe('deny')
  })

  it('does NOT increment counter when backend returns a valid enum value for security.policy_mode', async () => {
    // "allow" is a valid value — no coercion should occur.
    const wireConfig = {
      gateway: { host: '127.0.0.1', port: 8080 },
      security: { policy_mode: 'allow' },
    }
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireConfig), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchConfig, getConfigCoercionCount, resetConfigCoercionCount } = await import('./api')
    resetConfigCoercionCount()

    const config = await fetchConfig()

    // No coercion should have occurred.
    expect(getConfigCoercionCount()).toBe(0)
    // The valid value must be preserved.
    expect(config.security.policy_mode).toBe('allow')
  })

  it('increments counter once per invalid enum field — differentiation test', async () => {
    // Two different invalid enum values — counter should increment twice (once per field).
    const wireConfig = {
      gateway: { host: '127.0.0.1', port: 8080 },
      security: { policy_mode: 'invalid_policy', exec_approval: 'invalid_exec' },
    }
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireConfig), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { fetchConfig, getConfigCoercionCount, resetConfigCoercionCount } = await import('./api')
    resetConfigCoercionCount()

    await fetchConfig()

    // Two invalid enum values should produce count ≥ 2 (one per field).
    expect(getConfigCoercionCount()).toBeGreaterThanOrEqual(2)
  })
})

// ── castString / castNumber / castOptionalNumber coercion telemetry (#146) ───
//
// Wave 2 added castString/castNumber/castOptionalNumber alongside validEnum to
// runtime-check select Config scalar fields — including security-relevant
// guardrails (security.exec_timeout_seconds,
// max_background_seconds, rate_limits.*) — but had ZERO direct test coverage
// of a wrong-shaped wire value actually reaching any of the three functions,
// and the only production signal for a coercion was a silently-incremented
// counter (no toast, no console line, no telemetry event — a CRITICAL finding
// from the 7-reviewer gate's silent-failure-hunter). recordCoercion() now
// fixes the silence by calling logError() in production (mirroring
// _recordApiSchemaError's DEV-console.warn / PROD-logError split). These
// tests exercise the real fetchConfig()→rawToFrontendConfig() path with
// wrong-typed wire values for security-relevant fields and assert: (a) a safe
// fallback, (b) logError is called with diagnostic context, (c) the coercion
// counter increments.

describe('castString/castNumber/castOptionalNumber: wrong-shaped value coercion + production telemetry', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  // NOTE: this block deliberately uses the file-top-level static imports of
  // fetchConfig/getConfigCoercionCount/resetConfigCoercionCount (and of the
  // `telemetry` namespace) rather than the `await import('./api')` pattern
  // used elsewhere in this file. Several earlier describe blocks call
  // `vi.resetModules()` in their afterEach; a dynamic `await import('./api')`
  // AFTER one of those resets loads a fresh module graph whose OWN internal
  // `import { logError } from './telemetry'` binding resolves against a
  // fresh telemetry module instance — a different object than the one this
  // file's static `import * as telemetry from './telemetry'` captured at
  // file-load time. vi.spyOn(telemetry, 'logError') would then spy on a
  // stale, disconnected instance and silently observe zero calls even
  // though the real logError() demonstrably ran (visible via its
  // console.error side effect). Staying on the static imports keeps
  // `fetchConfig` and `telemetry` on the same original module instance so
  // the spy actually intercepts the call.
  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    resetConfigCoercionCount()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
    vi.restoreAllMocks()
    sessionStorage.clear()
    resetConfigCoercionCount()
  })

  function mockConfigResponse(wireConfig: Record<string, unknown>) {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify(wireConfig), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
  }

  it('castOptionalNumber: security.exec_timeout_seconds="unlimited" (wrong-typed) falls back to undefined, increments the counter, and calls logError in production', async () => {
    // "unlimited" is exactly the kind of wrong-shaped-but-truthy value that
    // used to pass straight through cast<T>() before Wave 2 — a string where
    // a numeric guardrail was expected. (ADR-053 D12 retired the former
    // daily_cost_cap field this test exercised; repurposed to the surviving
    // exec_timeout_seconds castOptionalNumber guardrail so the coercion +
    // production-telemetry coverage is preserved.)
    mockConfigResponse({
      gateway: { host: '127.0.0.1', port: 8080 },
      security: { policy_mode: 'deny', exec_approval: 'ask', exec_timeout_seconds: 'unlimited' },
    })

    // Force the production (non-DEV) branch of recordCoercion so logError
    // fires instead of the DEV console.warn — mirrors the established
    // pattern in src/lib/ws.test.ts's metadata-drift-detector suite.
    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production')
    const logErrorSpy = vi.spyOn(telemetry, 'logError')

    const config = await fetchConfig()

    // (a) safe fallback — an optional guardrail field drops to "unset"
    // rather than passing a corrupted string through.
    expect(config.security.exec_timeout_seconds).toBeUndefined()
    // (c) counter increments.
    expect(getConfigCoercionCount()).toBeGreaterThan(0)
    // (b) logError called with diagnostic context — field name + what was
    // substituted, not just "something got coerced somewhere".
    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: 'configCoercion',
        field: 'security.exec_timeout_seconds',
        coercedValue: JSON.stringify('unlimited'),
      }),
    )
  })

  it('castNumber: gateway.port="8080" (string, wrong-typed) falls back to the default port, increments the counter, and calls logError in production', async () => {
    mockConfigResponse({
      gateway: { host: '127.0.0.1', port: '8080' },
      security: { policy_mode: 'deny', exec_approval: 'ask' },
    })

    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production')
    const logErrorSpy = vi.spyOn(telemetry, 'logError')

    const config = await fetchConfig()

    // (a) safe fallback — a string that happens to look numeric is still
    // wrong-typed and must NOT be silently accepted as the wire value; the
    // hardcoded 8080 fallback is used instead.
    expect(config.gateway.port).toBe(8080)
    expect(typeof config.gateway.port).toBe('number')
    // (c) counter increments.
    expect(getConfigCoercionCount()).toBeGreaterThan(0)
    // (b) logError called with diagnostic context.
    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: 'configCoercion',
        field: 'gateway.port',
        coercedValue: JSON.stringify('8080'),
        fallbackValue: JSON.stringify(8080),
      }),
    )
  })

  it('castString: gateway.host=12345 (number, wrong-typed) falls back to the default bind address, increments the counter, and calls logError in production', async () => {
    mockConfigResponse({
      gateway: { host: 12345, port: 8080 },
      security: { policy_mode: 'deny', exec_approval: 'ask' },
    })

    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production')
    const logErrorSpy = vi.spyOn(telemetry, 'logError')

    const config = await fetchConfig()

    // (a) safe fallback.
    expect(config.gateway.bind_address).toBe('127.0.0.1')
    // (c) counter increments.
    expect(getConfigCoercionCount()).toBeGreaterThan(0)
    // (b) logError called with diagnostic context.
    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: 'configCoercion',
        field: 'gateway.host',
        coercedValue: JSON.stringify(12345),
      }),
    )
  })

  it('multiple wrong-typed rate_limits fields each produce a distinct logError call with their own field label', async () => {
    // Two guardrail fields corrupted at once — verifies field names are not
    // conflated into a single generic message (the code-simplifier finding
    // this fix addresses: "not just 'something got coerced somewhere'").
    mockConfigResponse({
      gateway: { host: '127.0.0.1', port: 8080 },
      security: {
        policy_mode: 'deny',
        exec_approval: 'ask',
        rate_limits: { max_tokens_per_day: 'unlimited', max_cost_per_day: {} },
      },
    })

    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production')
    const logErrorSpy = vi.spyOn(telemetry, 'logError')

    const config = await fetchConfig()

    expect(config.security.rate_limits?.max_tokens_per_day).toBeUndefined()
    expect(config.security.rate_limits?.max_cost_per_day).toBeUndefined()
    expect(getConfigCoercionCount()).toBeGreaterThanOrEqual(2)

    const fields = logErrorSpy.mock.calls.map((call) => (call[0] as { field?: string }).field)
    expect(fields).toContain('security.rate_limits.max_tokens_per_day')
    expect(fields).toContain('security.rate_limits.max_cost_per_day')
  })

  it('DEV builds do NOT call logError for a coercion — console.warn only (production/DEV split preserved)', async () => {
    // Regression guard for the split this fix introduces: DEV must keep its
    // existing console.warn-only behaviour (no telemetry event), matching
    // _recordApiSchemaError's established DEV/PROD split above. (Repurposed
    // from the retired daily_cost_cap to exec_timeout_seconds per ADR-053 D12.)
    mockConfigResponse({
      gateway: { host: '127.0.0.1', port: 8080 },
      security: { policy_mode: 'deny', exec_approval: 'ask', exec_timeout_seconds: 'unlimited' },
    })

    vi.stubEnv('DEV', true)
    vi.stubEnv('MODE', 'development')
    const logErrorSpy = vi.spyOn(telemetry, 'logError')
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const config = await fetchConfig()

    expect(config.security.exec_timeout_seconds).toBeUndefined()
    expect(getConfigCoercionCount()).toBeGreaterThan(0)
    expect(logErrorSpy).not.toHaveBeenCalled()
    expect(warnSpy).toHaveBeenCalled()
    const warned = warnSpy.mock.calls.some((call) => String(call[0]).includes('security.exec_timeout_seconds'))
    expect(warned).toBe(true)
  })
})

// ── Skill marketplace: searchSkills / installSkillBySlug ─────────────────────

describe('fetchCommands: dropped-item production telemetry (slash-palette silent-empty bugfix)', () => {
  // Same static-top-level-import rationale as the castString/castNumber
  // block above (comment there explains why): `fetchCommands` and the
  // `telemetry` namespace must stay bound to the file's original module
  // instances, or `vi.spyOn(telemetry, 'logError')` silently observes zero
  // calls against a disconnected module graph.
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
    vi.restoreAllMocks()
    sessionStorage.clear()
  })

  // Payload: one structurally valid SlashCommand + one missing the required
  // `delivery` enum field — the exact shape of a backend/SPA contract drift
  // that SlashCommandSchema.safeParse rejects per-item (fetchCommands must
  // not let one bad item hide the whole list).
  const payloadWithOneBadCommand = [
    { name: 'new', label: '/new', description: 'Start a new conversation', delivery: 'client' },
    { name: 'broken', label: '/broken', description: 'Malformed backend entry' },
  ]

  it('DEV builds keep the console.warn-only behaviour — no logError call (production/DEV split preserved)', async () => {
    fetchSpy.mockResolvedValueOnce(makeOkResponse(payloadWithOneBadCommand))
    vi.stubEnv('DEV', true)
    vi.stubEnv('MODE', 'development')
    const logErrorSpy = vi.spyOn(telemetry, 'logError')
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const result = await fetchCommands('web')

    expect(result.map((c) => c.name)).toEqual(['new'])
    expect(logErrorSpy).not.toHaveBeenCalled()
    expect(warnSpy).toHaveBeenCalled()
  })

  it('production builds call logError with the dropped-count diagnostic — bugfix: previously silent in production (DEV-only console.warn gate)', async () => {
    fetchSpy.mockResolvedValueOnce(makeOkResponse(payloadWithOneBadCommand))
    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production')
    const logErrorSpy = vi.spyOn(telemetry, 'logError')
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const result = await fetchCommands('web')

    // Safe fallback preserved: the good command survives, the bad one drops.
    expect(result.map((c) => c.name)).toEqual(['new'])
    // The bug: this used to have NO production-visible signal at all.
    expect(warnSpy).not.toHaveBeenCalled()
    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: 'commandSchemaDrop',
        surface: 'web',
        droppedCount: 1,
        totalCount: 2,
      }),
    )
  })

  it('does not call logError when every command validates', async () => {
    fetchSpy.mockResolvedValueOnce(makeOkResponse([payloadWithOneBadCommand[0]]))
    vi.stubEnv('DEV', false)
    vi.stubEnv('MODE', 'production')
    const logErrorSpy = vi.spyOn(telemetry, 'logError')

    const result = await fetchCommands('web')

    expect(result).toHaveLength(1)
    expect(logErrorSpy).not.toHaveBeenCalled()
  })
})

describe('Skill registry helpers (ClawHub search + install-by-slug)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookie('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookie()
    vi.resetModules()
  })

  describe('fetchSkills (tolerant installed-skills list)', () => {
    it('keeps valid skills and drops a malformed one (one bad skill must not hide the whole list)', async () => {
      const payload = [
        { id: 'good', name: 'Good', version: '2.1.0', verified: false, status: 'active', source: 'global' },
        // Structurally invalid: missing required version/verified/status.
        { id: 'bad', name: 'Bad' },
      ]
      fetchSpy.mockResolvedValueOnce(makeOkResponse(payload))

      const { fetchSkills } = await import('./api')
      const result = await fetchSkills()

      // Must NOT throw; the valid skill survives, the bad one is dropped.
      expect(result.map((s) => s.id)).toEqual(['good'])
    })

    it('accepts a non-semver version like "1.0" (ClawHub versions are arbitrary)', async () => {
      const payload = [
        { id: 'cw', name: 'ClawHub Skill', version: '1.0', verified: false, status: 'active', source: 'global' },
      ]
      fetchSpy.mockResolvedValueOnce(makeOkResponse(payload))

      const { fetchSkills } = await import('./api')
      const result = await fetchSkills()

      expect(result).toHaveLength(1)
      expect(result[0].version).toBe('1.0')
    })
  })

  describe('searchSkills', () => {
    it('GET /api/v1/skills/search — URL-encodes q and sends limit', async () => {
      const payload = [
        {
          slug: 'web-search',
          display_name: 'Web Search',
          summary: 'Search the web.',
          version: '1.4.0',
          score: 0.9,
          registry_name: 'clawhub',
          owner_handle: 'acme',
        },
      ]
      fetchSpy.mockResolvedValueOnce(makeOkResponse(payload))

      const { searchSkills } = await import('./api')
      const result = await searchSkills('web search', 5)

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/skills/search')
      expect(url).toContain('q=web+search')
      expect(url).toContain('limit=5')
      expect((init.method ?? 'GET').toUpperCase()).toBe('GET')
      expect(result).toEqual(payload)
    })

    it('defaults limit to 20 when omitted', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse([]))
      const { searchSkills } = await import('./api')
      await searchSkills('files')
      const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('limit=20')
    })

    it('propagates a 502 as a typed ApiError', async () => {
      fetchSpy.mockResolvedValueOnce(new Response('registry down', { status: 502 }))
      const { searchSkills } = await import('./api')
      await expect(searchSkills('web')).rejects.toThrow('502')
    })
  })

  describe('installSkillBySlug', () => {
    const okSkill = {
      id: 'web-search',
      name: 'web-search',
      version: '1.4.0',
      status: 'active',
      verified: false,
    }

    it('POST /api/v1/skills/install — sends {slug} body + CSRF', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse(okSkill))
      const { installSkillBySlug } = await import('./api')
      const skill = await installSkillBySlug('web-search')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/skills/install')
      expect((init.method ?? '').toUpperCase()).toBe('POST')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual({ slug: 'web-search' })
      expect(skill.id).toBe('web-search')
    })

    it('includes version when provided', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse(okSkill))
      const { installSkillBySlug } = await import('./api')
      await installSkillBySlug('web-search', '1.4.0')
      const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(JSON.parse(init.body as string)).toEqual({ slug: 'web-search', version: '1.4.0' })
    })

    it('propagates a 409 (already installed) as a typed ApiError', async () => {
      fetchSpy.mockResolvedValueOnce(new Response('already installed', { status: 409 }))
      const { installSkillBySlug } = await import('./api')
      await expect(installSkillBySlug('web-search')).rejects.toThrow('409')
    })
  })
})

// ── fetchWorkspaceInstructions / updateWorkspaceInstructions ──────────────────
//
// Verifies that the workspace instructions helpers:
// 1. fetchWorkspaceInstructions: GET /workspaces/{id}/instructions — URL encodes
//    the id, validates the WorkspaceInstructionsResponse schema, returns content.
// 2. updateWorkspaceInstructions: PUT with correct body + CSRF header, returns
//    validated WorkspaceInstructionsResponse.
// 3. Both throw ApiSchemaError when the backend returns a body that does not
//    match WorkspaceInstructionsResponse.

describe('fetchWorkspaceInstructions / updateWorkspaceInstructions', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  function stubCookieLocal5(value: string) {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => value,
    })
  }

  function restoreCookieLocal5() {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (document as any).cookie
  }

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookieLocal5('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookieLocal5()
    vi.resetModules()
  })

  describe('fetchWorkspaceInstructions', () => {
    it('GET /api/v1/workspaces/{id}/instructions — returns content', async () => {
      const wire = { content: '# Project Instructions\n\nUse TypeScript.' }
      fetchSpy.mockResolvedValueOnce(makeOkResponse(wire))

      const { fetchWorkspaceInstructions } = await import('./api')
      const result = await fetchWorkspaceInstructions('ws-abc')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/workspaces/ws-abc/instructions')
      expect((init.method ?? 'GET').toUpperCase()).toBe('GET')
      expect(result.content).toBe('# Project Instructions\n\nUse TypeScript.')
    })

    it('URL-encodes workspace id with special characters', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ content: '' }))

      const { fetchWorkspaceInstructions } = await import('./api')
      await fetchWorkspaceInstructions('ws/with spaces')

      const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/workspaces/ws%2Fwith%20spaces/instructions')
    })

    it('returns empty content when workspace has no instructions file', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ content: '' }))

      const { fetchWorkspaceInstructions } = await import('./api')
      const result = await fetchWorkspaceInstructions('ws-empty')

      expect(result.content).toBe('')
    })

    it('throws ApiSchemaError when response is missing content field', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ text: 'wrong field' }))

      const { fetchWorkspaceInstructions, ApiSchemaError: ApiSchemaErrorClass } = await import('./api')
      await expect(fetchWorkspaceInstructions('ws-abc')).rejects.toBeInstanceOf(ApiSchemaErrorClass)
    })

    it('throws typed error on 404', async () => {
      fetchSpy.mockResolvedValueOnce(new Response('not found', { status: 404 }))

      const { fetchWorkspaceInstructions } = await import('./api')
      await expect(fetchWorkspaceInstructions('ws-missing')).rejects.toThrow('404')
    })
  })

  describe('updateWorkspaceInstructions', () => {
    it('PUT /api/v1/workspaces/{id}/instructions — sends CSRF and content body', async () => {
      const wire = { content: 'Use TypeScript. Prefer functional components.' }
      fetchSpy.mockResolvedValueOnce(makeOkResponse(wire))

      const { updateWorkspaceInstructions } = await import('./api')
      const result = await updateWorkspaceInstructions('ws-abc', 'Use TypeScript. Prefer functional components.')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/workspaces/ws-abc/instructions')
      expect((init.method ?? '').toUpperCase()).toBe('PUT')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual({ content: 'Use TypeScript. Prefer functional components.' })
      expect(result.content).toBe('Use TypeScript. Prefer functional components.')
    })

    it('sends empty string to clear instructions', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ content: '' }))

      const { updateWorkspaceInstructions } = await import('./api')
      const result = await updateWorkspaceInstructions('ws-abc', '')

      const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(JSON.parse(init.body as string)).toEqual({ content: '' })
      expect(result.content).toBe('')
    })

    it('throws ApiSchemaError when PUT response is missing content field', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ saved: true }))

      const { updateWorkspaceInstructions, ApiSchemaError: ApiSchemaErrorClass } = await import('./api')
      await expect(updateWorkspaceInstructions('ws-abc', 'text')).rejects.toBeInstanceOf(ApiSchemaErrorClass)
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(new Response('content too long', { status: 400 }))

      const { updateWorkspaceInstructions } = await import('./api')
      await expect(updateWorkspaceInstructions('ws-abc', 'x')).rejects.toThrow('400')
    })
  })
})

