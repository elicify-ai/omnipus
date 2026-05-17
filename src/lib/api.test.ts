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
  UserRole,
  DMScope,
} from './api'
import { ApiSchemaError, getApiSchemaErrorCount, resetApiSchemaErrorCount } from './api'

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
    // Provide a valid auth token so getAuthHeaders() doesn't skip the header.
    sessionStorage.setItem('omnipus_auth_token', 'test-bearer')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
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

describe('Security API helpers', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    sessionStorage.setItem('omnipus_auth_token', 'test-bearer')
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

  // ── fetchAuditLogToggle / updateAuditLog ──────────────────────────────────

  describe('fetchAuditLogToggle', () => {
    it('GET /api/v1/security/audit-log — returns enabled flag', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ enabled: true }))

      const { fetchAuditLogToggle } = await import('./api')
      const result = await fetchAuditLogToggle()

      const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/audit-log')
      expect(result.enabled).toBe(true)
    })
  })

  describe('updateAuditLog', () => {
    it('PUT /api/v1/security/audit-log — sends CSRF and body', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ saved: true, requires_restart: false, applied_enabled: true }))

      const { updateAuditLog } = await import('./api')
      await updateAuditLog(true)

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/audit-log')
      expect((init.method ?? '').toUpperCase()).toBe('PUT')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual({ enabled: true })
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('bad request'))

      const { updateAuditLog } = await import('./api')
      await expect(updateAuditLog(false)).rejects.toThrow('400')
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

  // ── fetchRateLimits / updateRateLimits ────────────────────────────────────

  describe('fetchRateLimits', () => {
    it('GET /api/v1/security/rate-limits — returns current limits', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ daily_cost_cap_usd: 5, max_agent_llm_calls_per_hour: 100 }))

      const { fetchRateLimits } = await import('./api')
      const result = await fetchRateLimits()

      const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/rate-limits')
      expect(result.daily_cost_cap_usd).toBe(5)
    })
  })

  describe('updateRateLimits', () => {
    it('PUT /api/v1/security/rate-limits — sends CSRF and body', async () => {
      const body = { daily_cost_cap_usd: 10, max_agent_llm_calls_per_hour: 50 }
      fetchSpy.mockResolvedValueOnce(makeOkResponse(body))

      const { updateRateLimits } = await import('./api')
      await updateRateLimits(body)

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/rate-limits')
      expect((init.method ?? '').toUpperCase()).toBe('PUT')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual(body)
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('bad limits'))

      const { updateRateLimits } = await import('./api')
      await expect(updateRateLimits({ daily_cost_cap_usd: -1 })).rejects.toThrow('400')
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
      const body = { mode: 'permissive', allowed_paths: ['/tmp'], ssrf: { enabled: true, allow_internal: ['127.0.0.1'] } }
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
      await expect(updateSandboxConfig({ mode: 'bad' })).rejects.toThrow('400')
    })
  })

  // ── fetchSessionScope / updateSessionScope ────────────────────────────────

  describe('fetchSessionScope', () => {
    it('GET /api/v1/security/session-scope — returns dm_scope', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ dm_scope: 'per-peer' as DMScope }))

      const { fetchSessionScope } = await import('./api')
      const result = await fetchSessionScope()

      const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/session-scope')
      expect(result.dm_scope).toBe('per-peer')
    })
  })

  describe('updateSessionScope', () => {
    it('PUT /api/v1/security/session-scope — sends CSRF and dm_scope body', async () => {
      fetchSpy.mockResolvedValueOnce(
        makeOkResponse({ saved: true, requires_restart: true, applied_dm_scope: 'per-peer' }),
      )

      const { updateSessionScope } = await import('./api')
      await updateSessionScope('per-peer')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/session-scope')
      expect((init.method ?? '').toUpperCase()).toBe('PUT')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual({ dm_scope: 'per-peer' })
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('invalid scope'))

      const { updateSessionScope } = await import('./api')
      await expect(updateSessionScope('main')).rejects.toThrow('400')
    })
  })

  // ── fetchRetention / updateRetention ──────────────────────────────────────

  describe('fetchRetention', () => {
    it('GET /api/v1/security/retention — returns policy', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ session_days: 90, disabled: false }))

      const { fetchRetention } = await import('./api')
      const result = await fetchRetention()

      const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/retention')
      expect(result.session_days).toBe(90)
    })
  })

  describe('updateRetention', () => {
    it('PUT /api/v1/security/retention — sends CSRF and body', async () => {
      // The real handler returns flat {saved, requires_restart, session_days, disabled}
      // (not nested applied: {...}).  The schema requires all four fields.
      fetchSpy.mockResolvedValueOnce(
        makeOkResponse({ saved: true, requires_restart: false, session_days: 30, disabled: false }),
      )

      const { updateRetention } = await import('./api')
      await updateRetention({ session_days: 30 })

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/retention')
      expect((init.method ?? '').toUpperCase()).toBe('PUT')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual({ session_days: 30 })
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('invalid retention'))

      const { updateRetention } = await import('./api')
      await expect(updateRetention({ session_days: -1 })).rejects.toThrow('400')
    })
  })

  // ── triggerRetentionSweep ─────────────────────────────────────────────────

  describe('triggerRetentionSweep', () => {
    it('POST /api/v1/security/retention/sweep — sends CSRF', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ removed: 5 }))

      const { triggerRetentionSweep } = await import('./api')
      const result = await triggerRetentionSweep()

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/security/retention/sweep')
      expect((init.method ?? '').toUpperCase()).toBe('POST')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(result.removed).toBe(5)
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('sweep failed'))

      const { triggerRetentionSweep } = await import('./api')
      await expect(triggerRetentionSweep()).rejects.toThrow('400')
    })
  })

  // ── fetchUsers ────────────────────────────────────────────────────────────

  describe('fetchUsers', () => {
    it('GET /api/v1/users — returns user list', async () => {
      const payload = [{ username: 'alice', role: 'admin' as UserRole, has_password: true, has_active_token: false }]
      fetchSpy.mockResolvedValueOnce(makeOkResponse(payload))

      const { fetchUsers } = await import('./api')
      const result = await fetchUsers()

      const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/users')
      expect(result[0].username).toBe('alice')
    })
  })

  // ── createUser ────────────────────────────────────────────────────────────

  describe('createUser', () => {
    it('POST /api/v1/users — sends CSRF and body, returns {username, role}', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ username: 'bob', role: 'user' }))

      const { createUser } = await import('./api')
      const result = await createUser({ username: 'bob', role: 'user', password: 'secret' })

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/users')
      expect((init.method ?? '').toUpperCase()).toBe('POST')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual({ username: 'bob', role: 'user', password: 'secret' })
      expect(result).toEqual({ username: 'bob', role: 'user' })
    })

    it('throws when server unexpectedly returns a token field', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ username: 'bob', role: 'user', token: 'oops' }))

      const { createUser } = await import('./api')
      await expect(createUser({ username: 'bob', role: 'user', password: 'secret' })).rejects.toThrow(
        'unexpected token in create response',
      )
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('username taken'))

      const { createUser } = await import('./api')
      await expect(createUser({ username: 'dup', role: 'user', password: 'x' })).rejects.toThrow('400')
    })
  })

  // ── deleteUser ────────────────────────────────────────────────────────────

  describe('deleteUser', () => {
    it('DELETE /api/v1/users/{username} — sends CSRF', async () => {
      // UserDeleteResponse requires {username, deleted} per contracts/openapi.yaml
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ username: 'alice', deleted: true }))

      const { deleteUser } = await import('./api')
      await deleteUser('alice')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/users/alice')
      expect((init.method ?? '').toUpperCase()).toBe('DELETE')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('cannot delete last admin'))

      const { deleteUser } = await import('./api')
      await expect(deleteUser('alice')).rejects.toThrow('400')
    })
  })

  // ── resetUserPassword ─────────────────────────────────────────────────────

  describe('resetUserPassword', () => {
    it('PUT /api/v1/users/{username}/password — sends CSRF and body', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ username: 'alice', password_reset: true }))

      const { resetUserPassword } = await import('./api')
      await resetUserPassword('alice', 'newpass')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/users/alice/password')
      expect((init.method ?? '').toUpperCase()).toBe('PUT')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual({ password: 'newpass' })
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('weak password'))

      const { resetUserPassword } = await import('./api')
      await expect(resetUserPassword('alice', 'x')).rejects.toThrow('400')
    })
  })

  // ── updateUserRole ────────────────────────────────────────────────────────

  describe('updateUserRole', () => {
    it('PATCH /api/v1/users/{username}/role — sends CSRF and role body', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse({ username: 'alice', role: 'admin' }))

      const { updateUserRole } = await import('./api')
      await updateUserRole('alice', 'admin')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/users/alice/role')
      expect((init.method ?? '').toUpperCase()).toBe('PATCH')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual({ role: 'admin' })
    })

    it('throws typed error on 400', async () => {
      fetchSpy.mockResolvedValueOnce(make400Response('invalid role'))

      const { updateUserRole } = await import('./api')
      await expect(updateUserRole('alice', 'user')).rejects.toThrow('400')
    })
  })

  // ── retentionMode helper ────────────────────────────────────────────────────
  describe('retentionMode', () => {
    it('returns "default" when session_days is 0 and disabled is false', async () => {
      const { retentionMode } = await import('./api')
      expect(retentionMode({ session_days: 0, disabled: false })).toBe('default')
    })

    it('returns "default" when both fields are absent', async () => {
      const { retentionMode } = await import('./api')
      expect(retentionMode({})).toBe('default')
    })

    it('returns "custom" when session_days > 0 and disabled is false', async () => {
      const { retentionMode } = await import('./api')
      expect(retentionMode({ session_days: 30, disabled: false })).toBe('custom')
    })

    it('returns "forever" when disabled is true', async () => {
      const { retentionMode } = await import('./api')
      expect(retentionMode({ session_days: 0, disabled: true })).toBe('forever')
    })

    it('returns "forever" when disabled is true even with session_days > 0 (disabled takes precedence)', async () => {
      const { retentionMode } = await import('./api')
      expect(retentionMode({ session_days: 99, disabled: true })).toBe('forever')
    })
  })
})

// ── F-34 — isPreviewListenerEnabled accessor ───────────────────────────────────
//
// preview_listener_enabled is an optional bool where undefined semantically
// means "true" (old gateway versions that predate the field always ran the
// preview listener). Reading the field directly risks treating undefined as
// falsy; the accessor encapsulates this polarity safely.
//
// Traces to: docs/specs/chat-served-iframe-preview-spec.md — F-34 polarity accessor

describe('isPreviewListenerEnabled', () => {
  it('returns true when info is undefined (old gateway — no field present)', async () => {
    // Traces to: chat-served-iframe-preview-spec.md — F-34: undefined → true
    const { isPreviewListenerEnabled } = await import('./api')
    expect(isPreviewListenerEnabled(undefined)).toBe(true)
  })

  it('returns true when preview_listener_enabled is undefined (field absent on new gateway)', async () => {
    // Traces to: chat-served-iframe-preview-spec.md — F-34: field absent → true
    const { isPreviewListenerEnabled } = await import('./api')
    // Cast: AboutInfo requires version/go_version/os/arch/uptime_seconds in type,
    // but the function only reads preview_listener_enabled — partial is safe here.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect(isPreviewListenerEnabled({ preview_listener_enabled: undefined } as any)).toBe(true)
  })

  it('returns true when preview_listener_enabled is explicitly true', async () => {
    // Traces to: chat-served-iframe-preview-spec.md — F-34: true → true
    const { isPreviewListenerEnabled } = await import('./api')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect(isPreviewListenerEnabled({ preview_listener_enabled: true } as any)).toBe(true)
  })

  it('returns false when preview_listener_enabled is explicitly false', async () => {
    // Traces to: chat-served-iframe-preview-spec.md — F-34: false → false
    const { isPreviewListenerEnabled } = await import('./api')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect(isPreviewListenerEnabled({ preview_listener_enabled: false } as any)).toBe(false)
  })

  it('differentiation: true and false inputs produce different outputs', async () => {
    // Anti-shortcut: proves the function is not always returning true or false.
    const { isPreviewListenerEnabled } = await import('./api')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const whenEnabled = isPreviewListenerEnabled({ preview_listener_enabled: true } as any)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const whenDisabled = isPreviewListenerEnabled({ preview_listener_enabled: false } as any)
    expect(whenEnabled).toBe(true)
    expect(whenDisabled).toBe(false)
    expect(whenEnabled).not.toBe(whenDisabled)
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

// ── Schema validation through real request() call (fix-C) ─────────────────────
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
    sessionStorage.setItem('omnipus_auth_token', 'test-bearer')
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
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ token: 'tok', role: 'admin', username: 'alice' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    )

    const { login, getApiSchemaErrorCount: count } = await import('./api')
    const result = await login('alice', 'pass')
    expect(result.token).toBe('tok')
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
