// D2 — _app.tsx beforeLoad auth-check branch (routes/_app.tsx:38-53).
//
// Coverage gap closed: previously this branch (a confirmed 401 from
// checkTokenValidity) redirected to /login WITHOUT clearing the auth store —
// the Sidebar's "logged in as X" survived the bounce because forceLogout()
// was never called here, only queryClient.ts's 401 handler and ws.ts's 1008
// handler called it. This suite drives a real 'unauthorized' verdict through
// beforeLoad and asserts forceLogout('expired') fires before the redirect.
//
// This file owns the branch on its own, with its own mock of
// ./authValidation. (It used to be the sibling of an onboarding-branch test in
// routes/-login.test.tsx; that suite tested the password login and was deleted
// with it — ADR-0008 ruling 2.)

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// --- Router mock — mirrors -login.test.tsx's redirect-throwing shim ---
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: unknown) => opts,
    redirect: (opts: unknown) => {
      const err = Object.assign(new Error('redirect'), { isRedirect: true, ...(opts as object) })
      return err
    },
  }
})

// --- API mock — onboarding already complete, so beforeLoad reaches the auth check ---
const mockFetchAppState = vi.fn()
const mockValidateToken = vi.fn()
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: () => mockFetchAppState(),
    validateToken: () => mockValidateToken(),
  }
})

// --- authValidation mock — the seam under test: force each verdict directly ---
const mockCheckTokenValidity = vi.fn()
vi.mock('./authValidation', () => ({
  checkTokenValidity: (...args: unknown[]) => mockCheckTokenValidity(...args),
  resetTokenValidationCache: vi.fn(),
}))

// --- authLogout mock — assert the call, don't exercise the real redirect/storage side effects ---
const mockForceLogout = vi.fn()
vi.mock('@/lib/authLogout', () => ({
  forceLogout: (...args: unknown[]) => mockForceLogout(...args),
}))

async function getBeforeLoad(): Promise<() => Promise<void>> {
  const appMod = await import('./_app')
  const routeOptions = (appMod.Route as unknown) as {
    options?: { beforeLoad?: () => Promise<void> }
    beforeLoad?: () => Promise<void>
  }
  const beforeLoad = routeOptions.beforeLoad ?? routeOptions.options?.beforeLoad
  if (typeof beforeLoad !== 'function') {
    throw new Error('[BLOCKED] beforeLoad not found on Route — seam moved, update this test')
  }
  return beforeLoad
}

describe('_app.tsx beforeLoad — auth-check branch (D2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.resetModules()
    mockFetchAppState.mockResolvedValue({ onboarding_complete: true })
    // These tests are all about a RETURNING user (checkTokenValidity's
    // verdict handling) — the boot-401 fix below adds an earlier guard that
    // skips checkTokenValidity entirely for a browser that has never signed
    // in (hasStoredSession(), src/store/auth.ts). Seed the "has signed in
    // before" hint so these tests keep reaching the branch they exist to
    // cover; the never-signed-in case has its own dedicated describe block.
    localStorage.setItem('omnipus_auth_username', 'admin')
  })

  afterEach(() => {
    localStorage.clear()
  })

  // Explicit 60000ms (not this file's implicit 30000ms testTimeout): this is
  // the FIRST test in the file, so its `await import('./_app')` inside
  // getBeforeLoad() pays the one-time TanStack Router plugin transform cost
  // vitest.config.ts already documents for this exact file ("importing a
  // route/screen module triggers the router plugin's transform across every
  // route file... 20-30s on a cold run" — -app-auth is named there by name,
  // issue #616). That cost falls inside the test BODY, so hookTimeout's 60s
  // margin (granted to beforeAll/beforeEach) never covers it, only
  // testTimeout's 30s does — confirmed present on unmodified origin/main,
  // deterministic in isolation, unrelated to the WP5/WP3 seam patches this
  // fix landed alongside. The other 9 tests in this file pay no such cost
  // (the transform is warm afterward, even though vi.resetModules() in
  // beforeEach clears vitest's own module registry each time) and keep the
  // file's default timeout.
  it('calls forceLogout("expired") BEFORE throwing the redirect on a confirmed 401 ("unauthorized" verdict)', async () => {
    mockCheckTokenValidity.mockResolvedValue('unauthorized')
    const beforeLoad = await getBeforeLoad()

    const callOrder: string[] = []
    mockForceLogout.mockImplementation(() => { callOrder.push('forceLogout') })

    let thrown: unknown = null
    try {
      await beforeLoad()
    } catch (err) {
      callOrder.push('redirect-thrown')
      thrown = err
    }

    expect(mockForceLogout).toHaveBeenCalledOnce()
    expect(mockForceLogout).toHaveBeenCalledWith('expired')
    expect(thrown).not.toBeNull()
    expect((thrown as { to: string }).to).toBe('/login')
    // forceLogout must run before the redirect is thrown — it clears the
    // stale auth-store state that the old code path left behind.
    expect(callOrder).toEqual(['forceLogout', 'redirect-thrown'])
  }, 60000)

  it('does NOT call forceLogout when the verdict is "ok"', async () => {
    mockCheckTokenValidity.mockResolvedValue('ok')
    const beforeLoad = await getBeforeLoad()
    await expect(beforeLoad()).resolves.toBeUndefined()
    expect(mockForceLogout).not.toHaveBeenCalled()
  })

  it('does NOT call forceLogout when the verdict is "transient" (network hiccup keeps the session)', async () => {
    mockCheckTokenValidity.mockResolvedValue('transient')
    const beforeLoad = await getBeforeLoad()
    await expect(beforeLoad()).resolves.toBeUndefined()
    expect(mockForceLogout).not.toHaveBeenCalled()
  })
})

// ---------------------------------------------------------------------------
// Boot-401 fix: GET /api/v1/auth/validate used to fire unconditionally on
// every first paint into an _app/* route, even for a browser that has NEVER
// signed in — a guaranteed 401 on every fresh install/browser, which is both
// noisy (an "error" that's actually normal) and useless as a signal for
// spotting a REAL failure. hasStoredSession() (src/store/auth.ts) is a "has
// this browser ever completed a login" hint backed by
// localStorage['omnipus_auth_username'] (written only by a successful
// login/onboarding, per store/auth.ts's setUsername). beforeLoad now checks
// it BEFORE calling checkTokenValidity(validateToken) at all.
//
// validateToken() itself is not double-mocked here the way checkTokenValidity
// is elsewhere in this file — the assertion that matters is whether
// checkTokenValidity (the thing that actually calls validateToken) is invoked
// at all, which is exactly what a "no failed request" claim needs: if it's
// never called, validateToken() never runs, so GET /api/v1/auth/validate
// never fires.
// ---------------------------------------------------------------------------
describe('_app.tsx beforeLoad — skip validateToken for a never-signed-in browser (boot-401 fix)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.resetModules()
    mockFetchAppState.mockResolvedValue({ onboarding_complete: true })
    localStorage.clear()
  })

  afterEach(() => {
    localStorage.clear()
  })

  it('never calls checkTokenValidity (so validateToken/GET /auth/validate never fires) when no session was ever stored', async () => {
    // localStorage is empty — hasStoredSession() must be false.
    const beforeLoad = await getBeforeLoad()

    let thrown: unknown = null
    try {
      await beforeLoad()
    } catch (err) {
      thrown = err
    }

    expect(mockCheckTokenValidity).not.toHaveBeenCalled()
    // Still ends up at /login — just without ever asking the server.
    expect(thrown).not.toBeNull()
    expect((thrown as { to: string }).to).toBe('/login')
  })

  it('does NOT call forceLogout for a never-signed-in browser — there is no session being forced out, so no involuntary-logout banner', async () => {
    // Deliberately set the verdict checkTokenValidity WOULD return if it were
    // (wrongly) reached — this is what makes the test discriminate: without
    // the hasStoredSession() guard, beforeLoad calls checkTokenValidity,
    // gets 'unauthorized', and DOES call forceLogout('expired'). With the
    // guard, checkTokenValidity is never reached at all, so this verdict is
    // never consulted.
    mockCheckTokenValidity.mockResolvedValue('unauthorized')
    const beforeLoad = await getBeforeLoad()
    try {
      await beforeLoad()
    } catch {
      // redirect throw is expected; assertion below is what this test checks.
    }
    expect(mockForceLogout).not.toHaveBeenCalled()
  })

  it('DOES call checkTokenValidity for a returning user (hasStoredSession true) even when the session is still valid', async () => {
    localStorage.setItem('omnipus_auth_username', 'admin')
    mockCheckTokenValidity.mockResolvedValue('ok')
    const beforeLoad = await getBeforeLoad()

    await expect(beforeLoad()).resolves.toBeUndefined()
    expect(mockCheckTokenValidity).toHaveBeenCalledOnce()
  })

  it('DOES call checkTokenValidity for a returning user whose session has genuinely expired, and still forces them out', async () => {
    localStorage.setItem('omnipus_auth_username', 'admin')
    mockCheckTokenValidity.mockResolvedValue('unauthorized')
    const beforeLoad = await getBeforeLoad()

    let thrown: unknown = null
    try {
      await beforeLoad()
    } catch (err) {
      thrown = err
    }

    expect(mockCheckTokenValidity).toHaveBeenCalledOnce()
    expect(mockForceLogout).toHaveBeenCalledWith('expired')
    expect((thrown as { to: string }).to).toBe('/login')
  })
})

// ---------------------------------------------------------------------------
// ADR-0010 / login-and-onboarding-spec.md §2.4 — the boot path collapses to
// ONE request when GET /api/v1/state carries `identity`: the separate GET
// /api/v1/auth/validate round trip (checkTokenValidity/validateToken) must
// never fire when `identity.signed_in` already answered the question. An
// OLDER backend's /state response has no `identity` field at all — that must
// keep today's exact hasStoredSession()/checkTokenValidity() behaviour
// (already covered by the two describe blocks above, whose mocks resolve
// `{ onboarding_complete: true }` with no `identity` key).
// ---------------------------------------------------------------------------
describe('_app.tsx beforeLoad — identity-driven boot path (WP1/WP5a)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.resetModules()
    localStorage.clear()
  })

  afterEach(() => {
    localStorage.clear()
  })

  it('skips checkTokenValidity entirely when identity.signed_in is true', async () => {
    mockFetchAppState.mockResolvedValue({
      onboarding_complete: true,
      identity: { mode: 'local', edition: 'core', signed_in: true, account: { label: 'a', email_masked: 'a', org: null } },
    })
    const beforeLoad = await getBeforeLoad()

    await expect(beforeLoad()).resolves.toBeUndefined()
    expect(mockCheckTokenValidity).not.toHaveBeenCalled()
    expect(mockForceLogout).not.toHaveBeenCalled()
  })

  it('redirects to /login without calling checkTokenValidity when identity.signed_in is false', async () => {
    mockFetchAppState.mockResolvedValue({
      onboarding_complete: true,
      identity: { mode: 'local', edition: 'core', signed_in: false, blocked_reason: 'signed_out' },
    })
    const beforeLoad = await getBeforeLoad()

    let thrown: unknown = null
    try {
      await beforeLoad()
    } catch (err) {
      thrown = err
    }

    expect(mockCheckTokenValidity).not.toHaveBeenCalled()
    expect((thrown as { to: string }).to).toBe('/login')
  })

  it('falls back to hasStoredSession()/checkTokenValidity() when identity is absent (older backend)', async () => {
    // No `identity` key at all — the exact shape an older backend's /state
    // response has, and the exact shape the two describe blocks above mock.
    mockFetchAppState.mockResolvedValue({ onboarding_complete: true })
    localStorage.setItem('omnipus_auth_username', 'admin')
    mockCheckTokenValidity.mockResolvedValue('ok')
    const beforeLoad = await getBeforeLoad()

    await expect(beforeLoad()).resolves.toBeUndefined()
    expect(mockCheckTokenValidity).toHaveBeenCalledOnce()
  })
})
