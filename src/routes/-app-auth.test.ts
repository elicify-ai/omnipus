// D2 — _app.tsx beforeLoad auth-check branch (routes/_app.tsx:38-53).
//
// Coverage gap closed: previously this branch (a confirmed 401 from
// checkTokenValidity) redirected to /login WITHOUT clearing the auth store —
// the Sidebar's "logged in as X" survived the bounce because forceLogout()
// was never called here, only queryClient.ts's 401 handler and ws.ts's 1008
// handler called it. This suite drives a real 'unauthorized' verdict through
// beforeLoad and asserts forceLogout('expired') fires before the redirect.
//
// Sibling to the existing #27 coverage in routes/-login.test.tsx (which tests
// the ONBOARDING branch of the same beforeLoad — fetchAppState resolving
// onboarding_complete:false). That suite hard-mocks checkTokenValidity to
// always resolve 'ok', so it structurally cannot reach this branch — hence a
// dedicated file with its own mock of ./authValidation.

import { describe, it, expect, vi, beforeEach } from 'vitest'

// --- Router mock — mirrors -login.test.tsx's redirect-throwing shim ---
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: (_path: string) => (opts: unknown) => opts,
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
  })

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
  })

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
