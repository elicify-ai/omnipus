// Regression tests for the queryClient 401 auth-error handler.
//
// UAT defect fixed here: a 401 from ANY single query/mutation forced a full
// logout with a FALSE "your session ended, possibly because you signed in
// elsewhere" explanation, even while GET /api/v1/auth/validate confirmed the
// session was fine at that same moment (e.g. GET /providers 401ing for a
// resource-scoped authorization reason). The PREVIOUS version of this test
// file hand-replicated the old (incorrect) blanket-forceLogout-on-any-401
// logic in a local `handleAuthError` copy rather than importing the real
// handler from queryClient.ts — so it exercised a fork of production logic
// that could never fail when the real behaviour changed, and it actively
// pinned the very defect being fixed here. It is replaced below with tests
// against the REAL exported `handleAuthError`, matching the precedent this
// file already sets for `shouldRetryQuery`/`shouldRetryMutation` (exported,
// per their own doc comment, "so tests can exercise the REAL predicate
// instead of hand-maintaining a parallel copy that can silently drift").
//
// Still-valid coverage preserved from the old file (re-verified against the
// real handler/predicates rather than copies):
//   - 401 does NOT trigger a forced logout when the session is confirmed
//     valid or the confirmation is inconclusive (NEW — this is the fix).
//   - A CONFIRMED-invalid session (validate itself 401s) still forces a
//     logout — the genuine expired-session case must keep working, mirroring
//     the -app-auth.test.ts contract for the sibling beforeLoad guard.
//   - 403 / non-ApiError failures never trigger a forced logout.
//   - 401/403 are still not retried (shouldRetryQuery/shouldRetryMutation) —
//     unaffected by this fix; verified via the REAL exported predicates.
//
// `forceLogout` (the real teardown: store clear, sessionStorage reason,
// redirect, debounce) and `checkTokenValidity`/`resetTokenValidationCache`
// (the real session-confirmation call, which would otherwise hit the network)
// are mocked — each has its own dedicated regression suite
// (authLogout.test.ts, and authValidation's own coverage via
// -app-auth.test.ts).
//
// IMPORTANT: everything under test is imported STATICALLY at module scope
// (no `vi.resetModules()` + per-test dynamic `import()`). Vitest's module
// registry gives a fresh `resetModules()` a BRAND NEW copy of every module in
// the graph, including `./api` — so a dynamically re-imported `queryClient.ts`
// would run `err instanceof ApiError` against a DIFFERENT `ApiError` class
// object than the one this file's own `new ApiError(...)` calls construct
// with. That mismatch silently makes every `instanceof ApiError` check false
// (caught live while writing this suite — every test failed with mocks never
// invoked, root-caused to exactly this). Static imports keep one shared
// module instance for the whole file, matching how the real app only ever
// loads `./api` once.
//
// Traces to: feat/level1-project-task-mgmt — SPA 401 handler regression
// (original suite); UAT false-logout-explanation defect (this revision).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ApiError } from './api'

// ── Mock the shared forced-logout helper ─────────────────────────────────────
const mockForceLogout = vi.fn()
vi.mock('./authLogout', () => ({
  forceLogout: (...args: unknown[]) => mockForceLogout(...args),
}))

// ── Mock the session-confirmation call (the seam this fix adds) ──────────────
const mockCheckTokenValidity = vi.fn()
const mockResetTokenValidationCache = vi.fn()
vi.mock('@/routes/authValidation', () => ({
  checkTokenValidity: (...args: unknown[]) => mockCheckTokenValidity(...args),
  resetTokenValidationCache: (...args: unknown[]) => mockResetTokenValidationCache(...args),
}))

// Static import of the REAL implementation under test — see the file header
// for why this must NOT be a per-test dynamic import alongside resetModules().
import { handleAuthError, shouldRetryQuery, shouldRetryMutation } from './queryClient'

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('queryClient 401 auth-error handler', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('does NOT force logout on a 401 while the session is confirmed still valid — the UAT defect', async () => {
    // BDD: Given GET /auth/validate confirms the session is fine ('ok'),
    // When a query/mutation fails with ApiError(401) (a resource-scoped
    // authorization decision, NOT session death),
    // Then forceLogout is NOT called — no redirect to /login, no fabricated
    // "session ended" explanation.
    //
    // Traces to: queryClient.ts handleAuthError — the exact repro from the
    // UAT report (GET /providers 401s while GET /auth/validate returns 200).
    mockCheckTokenValidity.mockResolvedValue('ok')

    await handleAuthError(new ApiError(401))

    expect(mockResetTokenValidationCache).toHaveBeenCalledOnce()
    expect(mockCheckTokenValidity).toHaveBeenCalledOnce()
    expect(mockForceLogout).not.toHaveBeenCalled()
  })

  it('forces logout on a 401 when the session is CONFIRMED invalid (validate also fails)', async () => {
    // BDD: Given GET /auth/validate itself returns a confirmed 401
    // ('unauthorized' verdict),
    // When a query/mutation fails with ApiError(401),
    // Then forceLogout IS called — the genuine expired-session case must
    // keep logging the user out promptly.
    mockCheckTokenValidity.mockResolvedValue('unauthorized')

    await handleAuthError(new ApiError(401))

    expect(mockForceLogout).toHaveBeenCalledOnce()
  })

  it('does NOT force logout on a 401 when the validity recheck is inconclusive (transient network/5xx)', async () => {
    // BDD: Given the /auth/validate recheck itself fails transiently (not a
    // confirmed 401 — e.g. a network hiccup or 5xx),
    // Then forceLogout is NOT called — an inconclusive check must not evict
    // a possibly-still-valid session.
    mockCheckTokenValidity.mockResolvedValue('transient')

    await handleAuthError(new ApiError(401))

    expect(mockForceLogout).not.toHaveBeenCalled()
  })

  it('does NOT invoke forceLogout for non-401 ApiError (403), and never even rechecks validity', async () => {
    // BDD: Given a 403 (forbidden, not unauthenticated),
    // Then the handler returns before ever consulting session validity —
    // 403 was never part of the "is the session dead" question.
    mockCheckTokenValidity.mockResolvedValue('unauthorized')

    await handleAuthError(new ApiError(403))

    expect(mockCheckTokenValidity).not.toHaveBeenCalled()
    expect(mockForceLogout).not.toHaveBeenCalled()
  })

  it('does NOT invoke forceLogout for a plain Error (not ApiError)', async () => {
    await handleAuthError(new Error('network error'))

    expect(mockCheckTokenValidity).not.toHaveBeenCalled()
    expect(mockForceLogout).not.toHaveBeenCalled()
  })

  it('does NOT invoke forceLogout for a 503 (e.g. /gateway/god-mode, /config/pending-restart) — pinned so this cannot silently regress', async () => {
    // BDD: Given a 503 ApiError (a status this app already relies on NOT
    // triggering a forced logout — see queryClient.ts's top comment
    // contrasting 503 with 401), never even consults session validity.
    mockCheckTokenValidity.mockResolvedValue('unauthorized')

    await handleAuthError(new ApiError(503))

    expect(mockCheckTokenValidity).not.toHaveBeenCalled()
    expect(mockForceLogout).not.toHaveBeenCalled()
  })

  it('coalesces concurrent 401s into a SINGLE validity recheck (no stampede of /auth/validate calls)', async () => {
    // BDD: Given several queries 401 at once (e.g. multiple panels on one
    // screen all losing authorization together),
    // When handleAuthError runs for each concurrently,
    // Then only ONE checkTokenValidity call is in flight — the second caller
    // rides the first's pending promise instead of firing its own request.
    let resolveCheck: (v: string) => void = () => {}
    mockCheckTokenValidity.mockReturnValue(
      new Promise((resolve) => {
        resolveCheck = resolve
      }),
    )

    const p1 = handleAuthError(new ApiError(401))
    const p2 = handleAuthError(new ApiError(401))

    expect(mockCheckTokenValidity).toHaveBeenCalledOnce()
    resolveCheck('ok')
    await Promise.all([p1, p2])

    expect(mockForceLogout).not.toHaveBeenCalled()
  })
})

describe('queryClient retry predicate — 401/403 exclusion unaffected by the auth-confirmation fix', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shouldRetryQuery still returns false for 401/403, true for 500 (real predicate, not a hand-copy)', () => {
    expect(shouldRetryQuery(0, new ApiError(401))).toBe(false)
    expect(shouldRetryQuery(0, new ApiError(403))).toBe(false)
    expect(shouldRetryQuery(0, new ApiError(500))).toBe(true)
  })

  it('shouldRetryMutation still returns false for 401/403 (real predicate, not a hand-copy)', () => {
    expect(shouldRetryMutation(0, new ApiError(401))).toBe(false)
    expect(shouldRetryMutation(0, new ApiError(403))).toBe(false)
  })
})
