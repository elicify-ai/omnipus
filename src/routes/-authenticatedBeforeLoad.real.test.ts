// Real guard integration: only fetch is replaced. The routes, token verdict,
// forceLogout and auth store all run as they do in the app.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AppState } from '@/lib/api/generated/openapi-types'

const RETURN_TO = '/workspaces/ws-9/chat?panel=calendar'
const STATE_URL = '/api/v1/state'
const VALIDATE_URL = '/api/v1/auth/validate'

type StateReply = 'unavailable' | 'signed-out'

function replaceNetwork(stateReply: StateReply) {
  const fetchAtNetwork = vi.fn(async (url: string | URL) => {
    if (String(url) === STATE_URL) {
      if (stateReply === 'unavailable') return new Response(null, { status: 503 })
      const state: AppState = {
        onboarding_complete: true,
        identity: { mode: 'local', edition: 'core', signed_in: false, blocked_reason: 'signed_out' },
      }
      return new Response(JSON.stringify(state), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    if (String(url) === VALIDATE_URL) return new Response(null, { status: 401 })
    throw new Error(`Unexpected request: ${String(url)}`)
  })
  vi.stubGlobal('fetch', fetchAtNetwork)
  return fetchAtNetwork
}

async function realRouteGuard(layout: 'app' | 'fullscreen') {
  const route = layout === 'app' ? (await import('./_app')).Route : (await import('./_fullscreen')).Route
  const beforeLoad = (route as unknown as {
    options: { beforeLoad?: (context: { location: { href: string } }) => Promise<void> }
  }).options.beforeLoad
  if (!beforeLoad) throw new Error(`Missing real beforeLoad on ${layout} route`)
  return () => beforeLoad({ location: { href: RETURN_TO } })
}

async function expectForcedExpiredLogout(layout: 'app' | 'fullscreen') {
  localStorage.setItem('omnipus_auth_username', 'admin')
  const fetchAtNetwork = replaceNetwork('unavailable')
  const beforeLoad = await realRouteGuard(layout)
  const { isForceLoggingOut, LOGOUT_REASON_KEY } = await import('@/lib/authLogout')

  await expect(beforeLoad()).rejects.toMatchObject({ status: 307, options: { to: '/login' } })
  // Both effects are synchronous inside forceLogout, before the guard throws.
  expect(isForceLoggingOut()).toBe(true)
  expect(sessionStorage.getItem(LOGOUT_REASON_KEY)).toBe('expired')
  expect(sessionStorage.getItem('omnipus_login_return')).toBe(RETURN_TO)
  expect(fetchAtNetwork.mock.calls.map(([url]) => String(url))).toEqual([STATE_URL, VALIDATE_URL])
  // Store cleanup uses a dynamic import; wait for it rather than racing it.
  const { useAuthStore } = await import('@/store/auth')
  await vi.waitFor(() => expect(useAuthStore.getState().username).toBeNull())
  expect(localStorage.getItem('omnipus_auth_username')).toBeNull()
}

describe('real authenticated layout guard — network-only seam', () => {
  beforeEach(() => {
    vi.resetModules()
    localStorage.clear()
    sessionStorage.clear()
    window.location.hash = '#/'
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
    sessionStorage.clear()
  })

  it('calls the real forceLogout("expired") before /login on an _app confirmed 401', async () => {
    await expectForcedExpiredLogout('app')
  }, 60_000)

  it('skips validation and redirects to /login when this browser never signed in', async () => {
    const fetchAtNetwork = replaceNetwork('unavailable')
    const beforeLoad = await realRouteGuard('app')
    await expect(beforeLoad()).rejects.toMatchObject({ status: 307, options: { to: '/login' } })
    expect(fetchAtNetwork.mock.calls.map(([url]) => String(url))).toEqual([STATE_URL])
    expect(sessionStorage.getItem('omnipus_logout_reason')).toBeNull()
    expect(sessionStorage.getItem('omnipus_login_return')).toBe(RETURN_TO)
  })

  it('forces an expired returning user out of the _fullscreen layout on a confirmed 401', async () => {
    await expectForcedExpiredLogout('fullscreen')
  })

  it('redirects identity.signed_in=false to /login without validating or forced logout', async () => {
    const fetchAtNetwork = replaceNetwork('signed-out')
    const beforeLoad = await realRouteGuard('app')
    await expect(beforeLoad()).rejects.toMatchObject({ status: 307, options: { to: '/login' } })
    expect(fetchAtNetwork.mock.calls.map(([url]) => String(url))).toEqual([STATE_URL])
    expect(sessionStorage.getItem('omnipus_logout_reason')).toBeNull()
    expect(sessionStorage.getItem('omnipus_login_return')).toBe(RETURN_TO)
  })
})
