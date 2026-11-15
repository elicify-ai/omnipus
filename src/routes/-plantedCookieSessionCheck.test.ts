/**
 * -plantedCookieSessionCheck.test.ts — #950 review F5 (frontend half): a
 * planted-cookie 401 on a GET must NOT be treated as an expired session.
 *
 * Spec: docs/internal/specs/adr-094-preview-isolation-spec.md, FR-015 + the
 * typed-error bullet + the "Planted cookie cleared" UI-states row. The
 * gateway's clear set never deletes the genuine host-only Path=/ session
 * cookie; it marks THIS request's credential read failed and answers a GET
 * with 401 + code planted_cookie_cleared. That 401 therefore says nothing
 * about whether the session is valid — only the next, now-clean request can.
 * "A 401 carrying clear lines followed by a clean re-check MUST NOT call
 * forceLogout", and recovery must never become a logout bypass for a
 * genuinely dead session.
 *
 * The defect (RED): src/routes/authValidation.ts::checkTokenValidity maps
 * EVERY 401 to 'unauthorized'. When the validate call itself is the planted
 * request — a page reload's route guard (-authenticatedBeforeLoad.ts), or
 * handleAuthError's re-check while the preview tab is still re-planting — the
 * user is force-logged-out with "Your session expired" and sent to /login,
 * the opposite of the one-click Retry recovery.
 *
 * Seam: only fetch is replaced. checkTokenValidity, handleAuthError,
 * forceLogout, the route guard and request() all run as in the app.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const STATE_URL = '/api/v1/state'
const VALIDATE_URL = '/api/v1/auth/validate'
const RETURN_TO = '/workspaces/ws-9/chat'

const PLANTED_401_BODY = {
  code: 'planted_cookie_cleared',
  error: 'planted_cookie_cleared',
  message: 'Omnipus cleared cookies set by a preview — please retry',
}

type Reply = 'planted' | 'plain401' | 'ok' | 'unavailable'

function respond(reply: Reply, url: string): Response {
  switch (reply) {
    case 'planted':
      return new Response(JSON.stringify(PLANTED_401_BODY), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })
    case 'plain401':
      return new Response(JSON.stringify({ error: 'unauthorized: missing Bearer token' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })
    case 'unavailable':
      return new Response(null, { status: 503 })
    case 'ok':
      if (url === VALIDATE_URL) {
        return new Response(JSON.stringify({ valid: true, username: 'admin', role: 'admin' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      throw new Error(`no ok fixture for ${url}`)
  }
}

/** Per-URL reply queues; the last reply repeats. */
function replaceNetwork(plan: Record<string, Reply[]>) {
  const seen: Record<string, number> = {}
  const fetchAtNetwork = vi.fn(async (input: string | URL) => {
    const url = String(input)
    const queue = plan[url]
    if (!queue) throw new Error(`Unexpected request: ${url}`)
    const n = seen[url] ?? 0
    seen[url] = n + 1
    return respond(queue[Math.min(n, queue.length - 1)], url)
  })
  vi.stubGlobal('fetch', fetchAtNetwork)
  return fetchAtNetwork
}

/**
 * forceLogout() clears the stored username through an un-awaited dynamic
 * import. Wait for it inside the test that triggered it, so it cannot land in
 * the NEXT test after that test has signed the browser in.
 */
async function waitForLogoutCleanup(): Promise<void> {
  const { useAuthStore } = await import('@/store/auth')
  await vi.waitFor(() => expect(useAuthStore.getState().username).toBeNull())
  await vi.waitFor(() => expect(localStorage.getItem('omnipus_auth_username')).toBeNull())
}

async function plantedApiError() {
  const { ApiError } = await import('@/lib/api-error')
  return ApiError.fromResponse(respond('planted', VALIDATE_URL))
}

beforeEach(() => {
  vi.resetModules()
  localStorage.clear()
  sessionStorage.clear()
  window.location.hash = '#/'
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  localStorage.clear()
  sessionStorage.clear()
})

describe('checkTokenValidity — a planted-cookie 401 is inconclusive, not unauthorized', () => {
  it('a validate call answered with the planted-cookie 401 is NOT a confirmed-invalid session', async () => {
    const { checkTokenValidity, resetTokenValidationCache } = await import('./authValidation')
    resetTokenValidationCache()
    const err = await plantedApiError()
    const verdict = await checkTokenValidity(() => Promise.reject(err), 5_000_000)
    expect(verdict).not.toBe('unauthorized')
    expect(verdict).toBe('transient')
  })

  it('the inconclusive verdict is not cached as ok — the next call re-validates', async () => {
    const { checkTokenValidity, resetTokenValidationCache } = await import('./authValidation')
    resetTokenValidationCache()
    const err = await plantedApiError()
    await checkTokenValidity(() => Promise.reject(err), 5_000_000)
    const validate = vi.fn().mockResolvedValue({})
    expect(await checkTokenValidity(validate, 5_000_001)).toBe('ok')
    expect(validate).toHaveBeenCalledTimes(1)
  })

  it('control: an ordinary 401 is still a confirmed-invalid session', async () => {
    const { checkTokenValidity, resetTokenValidationCache } = await import('./authValidation')
    const { ApiError } = await import('@/lib/api-error')
    resetTokenValidationCache()
    const err = await ApiError.fromResponse(respond('plain401', VALIDATE_URL))
    expect(await checkTokenValidity(() => Promise.reject(err), 5_000_000)).toBe('unauthorized')
  })
})

describe('handleAuthError (real re-check over the network seam)', () => {
  async function setup() {
    const { handleAuthError } = await import('@/lib/queryClient')
    const { isForceLoggingOut, LOGOUT_REASON_KEY } = await import('@/lib/authLogout')
    const { resetTokenValidationCache } = await import('./authValidation')
    resetTokenValidationCache()
    return { handleAuthError, isForceLoggingOut, LOGOUT_REASON_KEY }
  }

  it('planted 401, clean re-check => no forced logout', async () => {
    const { handleAuthError, isForceLoggingOut, LOGOUT_REASON_KEY } = await setup()
    const fetchAtNetwork = replaceNetwork({ [VALIDATE_URL]: ['ok'] })
    await handleAuthError(await plantedApiError())
    expect(fetchAtNetwork).toHaveBeenCalledTimes(1)
    expect(isForceLoggingOut()).toBe(false)
    expect(sessionStorage.getItem(LOGOUT_REASON_KEY)).toBeNull()
  })

  it('planted 401 whose re-check is ALSO planted (preview still open) => no forced logout', async () => {
    const { handleAuthError, isForceLoggingOut, LOGOUT_REASON_KEY } = await setup()
    const fetchAtNetwork = replaceNetwork({ [VALIDATE_URL]: ['planted'] })
    await handleAuthError(await plantedApiError())
    expect(fetchAtNetwork).toHaveBeenCalledTimes(1)
    expect(isForceLoggingOut()).toBe(false)
    expect(sessionStorage.getItem(LOGOUT_REASON_KEY)).toBeNull()
  })

  it('guard: planted 401 but the clean re-check says the session is dead => forced logout (no bypass)', async () => {
    const { handleAuthError, isForceLoggingOut } = await setup()
    replaceNetwork({ [VALIDATE_URL]: ['plain401'] })
    await handleAuthError(await plantedApiError())
    expect(isForceLoggingOut()).toBe(true)
    await waitForLogoutCleanup()
  })
})

describe('authenticated route guard on reload with a planted cookie (real guard, network seam)', () => {
  async function guard() {
    const route = (await import('./_app')).Route as unknown as {
      options: { beforeLoad?: (context: { location: { href: string } }) => Promise<void> }
    }
    const beforeLoad = route.options.beforeLoad
    if (!beforeLoad) throw new Error('Missing real beforeLoad on _app route')
    return () => beforeLoad({ location: { href: RETURN_TO } })
  }

  it('/state and /auth/validate both answer the planted 401 => the app loads, no forced logout, no /login redirect', async () => {
    localStorage.setItem('omnipus_auth_username', 'admin')
    const fetchAtNetwork = replaceNetwork({ [STATE_URL]: ['planted'], [VALIDATE_URL]: ['planted'] })
    const beforeLoad = await guard()
    const { isForceLoggingOut, LOGOUT_REASON_KEY } = await import('@/lib/authLogout')

    await expect(beforeLoad()).resolves.toBeUndefined()
    expect(fetchAtNetwork.mock.calls.map(([u]) => String(u))).toEqual([STATE_URL, VALIDATE_URL])
    expect(isForceLoggingOut()).toBe(false)
    expect(sessionStorage.getItem(LOGOUT_REASON_KEY)).toBeNull()
    expect(localStorage.getItem('omnipus_auth_username')).toBe('admin')
  })

  it('pin: /state planted, /auth/validate clean (clear lines applied) => the app loads', async () => {
    localStorage.setItem('omnipus_auth_username', 'admin')
    replaceNetwork({ [STATE_URL]: ['planted'], [VALIDATE_URL]: ['ok'] })
    const beforeLoad = await guard()
    const { isForceLoggingOut } = await import('@/lib/authLogout')
    await expect(beforeLoad()).resolves.toBeUndefined()
    expect(isForceLoggingOut()).toBe(false)
  })

  it('control: an ordinary 401 on /auth/validate still forces the expired logout and redirects to /login', async () => {
    localStorage.setItem('omnipus_auth_username', 'admin')
    replaceNetwork({ [STATE_URL]: ['unavailable'], [VALIDATE_URL]: ['plain401'] })
    const beforeLoad = await guard()
    const { isForceLoggingOut, LOGOUT_REASON_KEY } = await import('@/lib/authLogout')
    await expect(beforeLoad()).rejects.toMatchObject({ status: 307, options: { to: '/login' } })
    expect(isForceLoggingOut()).toBe(true)
    expect(sessionStorage.getItem(LOGOUT_REASON_KEY)).toBe('expired')
    await waitForLogoutCleanup()
  })
})
