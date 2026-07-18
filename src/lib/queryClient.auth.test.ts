// Regression tests for the queryClient 401 auth-error handler.
//
// Guards against:
//   - 401 response not triggering the shared forced-logout teardown
//   - 401 response being retried (retry storm — should be suppressed)
//   - 403 / non-ApiError failures NOT triggering a forced logout
//   - Debounce is delegated entirely to forceLogout() (no local double-debounce)
//
// Strategy: Create an isolated QueryClient that replicates the _handleAuthError
// and retry logic from queryClient.ts, so we can drive it without importing the
// singleton (which has module-level side effects). `forceLogout` itself (the
// token/store teardown + redirect + debounce) is mocked here and covered by its
// own dedicated regression suite in authLogout.test.ts — post ADR-044 (US-5 /
// FR-010) there is no JS-visible auth token for this handler to clear; auth is
// the omnipus-session HttpOnly cookie, and _handleAuthError's only job is to
// recognize a 401 ApiError and hand off to the shared forceLogout() teardown.
//
// Traces to: feat/level1-project-task-mgmt — SPA 401 handler regression

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { ApiError } from './api'

// ── Mock the shared forced-logout helper ─────────────────────────────────────
const mockForceLogout = vi.fn()
vi.mock('./authLogout', () => ({
  forceLogout: () => mockForceLogout(),
}))

// ── Helpers ───────────────────────────────────────────────────────────────────

/**
 * Create an isolated QueryClient that replicates the _handleAuthError
 * subscription and retry guard from queryClient.ts.
 */
function makeAuthTestClient() {
  function handleAuthError(err: unknown): void {
    if (!(err instanceof ApiError)) return
    if (err.status !== 401) return
    mockForceLogout()
  }

  const client = new QueryClient({
    defaultOptions: {
      queries: {
        // Mirror the retry guard from queryClient.ts: never retry 401/403.
        retry: (failureCount, err) =>
          !(err instanceof ApiError && (err.status === 401 || err.status === 403)) &&
          failureCount < 3,
        retryDelay: () => 0, // No delay in tests.
      },
      mutations: {
        retry: (failureCount, err) =>
          !(err instanceof ApiError && (err.status === 401 || err.status === 403)) &&
          failureCount < 3,
      },
    },
  })

  client.getQueryCache().subscribe((event) => {
    if (event.type === 'updated' && event.action.type === 'error') {
      handleAuthError(event.action.error)
    }
  })

  client.getMutationCache().subscribe((event) => {
    if (event.type === 'updated' && event.mutation.state.status === 'error') {
      handleAuthError(event.mutation.state.error)
    }
  })

  return { client }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('queryClient 401 auth-error handler', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('invokes forceLogout on ApiError(401)', async () => {
    // BDD: Given a QueryClient with the auth-error subscription,
    // When a queryFn throws ApiError(401),
    // Then the shared forceLogout() teardown is invoked.
    //
    // Traces to: queryClient.ts _handleAuthError — delegates to forceLogout() on 401
    const { client } = makeAuthTestClient()

    await expect(
      client.fetchQuery({
        queryKey: ['test-401-force-logout'],
        queryFn: () => Promise.reject(new ApiError(401)),
      }),
    ).rejects.toThrow()

    // Allow the async handler to fire.
    await new Promise((resolve) => setTimeout(resolve, 50))

    expect(mockForceLogout).toHaveBeenCalledOnce()
  })

  it('does NOT retry a query that fails with ApiError(401)', async () => {
    // BDD: Given a QueryClient with retry suppression for 401,
    // When a queryFn throws ApiError(401),
    // Then the queryFn is called exactly once (no retries).
    //
    // Traces to: queryClient.ts retry guard: !(err instanceof ApiError && err.status === 401)
    const { client } = makeAuthTestClient()

    const queryFn = vi.fn().mockRejectedValue(new ApiError(401))

    await expect(
      client.fetchQuery({
        queryKey: ['test-401-no-retry'],
        queryFn,
      }),
    ).rejects.toThrow()

    // The function must have been called exactly once — no retries.
    expect(queryFn).toHaveBeenCalledTimes(1)
  })

  it('does NOT retry a query that fails with ApiError(403)', async () => {
    // BDD: Given a QueryClient with retry suppression for 403,
    // When a queryFn throws ApiError(403),
    // Then the queryFn is called exactly once (no retries).
    //
    // Traces to: queryClient.ts retry guard: !(err instanceof ApiError && err.status === 403)
    const { client } = makeAuthTestClient()

    const queryFn = vi.fn().mockRejectedValue(new ApiError(403))

    await expect(
      client.fetchQuery({
        queryKey: ['test-403-no-retry'],
        queryFn,
      }),
    ).rejects.toThrow()

    expect(queryFn).toHaveBeenCalledTimes(1)
  })

  it('DOES retry a query that fails with a non-auth ApiError (500)', async () => {
    // BDD: Given a QueryClient with retry suppression for 401/403 only,
    // When a queryFn throws ApiError(500),
    // Then the queryFn IS retried (proves 401/403 guard is specific, not over-broad).
    //
    // Traces to: queryClient.ts retry guard — differentiation: 500 IS retried
    const { client } = makeAuthTestClient()

    // failureCount < 3 means 3 retries = 4 calls total.
    const queryFn = vi.fn().mockRejectedValue(new ApiError(500))

    await expect(
      client.fetchQuery({
        queryKey: ['test-500-retried'],
        queryFn,
        retry: (failureCount, err) =>
          !(err instanceof ApiError && (err.status === 401 || err.status === 403)) &&
          failureCount < 2, // 2 retries = 3 total calls
      }),
    ).rejects.toThrow()

    // 500 must be retried — call count must be > 1 (differentiation from 401 behavior).
    expect(queryFn.mock.calls.length).toBeGreaterThan(1)
  })

  it('invokes forceLogout again on a second, later 401 — debouncing is forceLogout\'s own responsibility, not duplicated here', async () => {
    // BDD: Given _handleAuthError has no local debounce state of its own,
    // When two separate ApiError(401) failures occur,
    // Then forceLogout() is called once per qualifying error — forceLogout's
    // OWN debounce guard (covered by authLogout.test.ts) is what suppresses a
    // rapid double-teardown, not this handler.
    //
    // Traces to: queryClient.ts _handleAuthError — unconditionally delegates
    // every 401 to forceLogout(); no duplicate debounce flag here.
    const { client } = makeAuthTestClient()

    await expect(
      client.fetchQuery({
        queryKey: ['test-401-second-a'],
        queryFn: () => Promise.reject(new ApiError(401)),
      }),
    ).rejects.toThrow()
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(mockForceLogout).toHaveBeenCalledTimes(1)

    await expect(
      client.fetchQuery({
        queryKey: ['test-401-second-b'],
        queryFn: () => Promise.reject(new ApiError(401)),
      }),
    ).rejects.toThrow()
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(mockForceLogout).toHaveBeenCalledTimes(2)
  })

  it('does NOT invoke forceLogout for non-401 ApiError (403 is auth-adjacent but different behavior)', async () => {
    // BDD: Given a QueryClient with the auth-error subscription,
    // When a queryFn throws ApiError(403) (forbidden, not unauthenticated),
    // Then forceLogout is NOT invoked.
    //
    // Traces to: queryClient.ts _handleAuthError: if (err.status !== 401) return
    const { client } = makeAuthTestClient()

    await expect(
      client.fetchQuery({
        queryKey: ['test-403-no-force-logout'],
        queryFn: () => Promise.reject(new ApiError(403)),
      }),
    ).rejects.toThrow()

    await new Promise((resolve) => setTimeout(resolve, 50))

    expect(mockForceLogout).not.toHaveBeenCalled()
  })

  it('does NOT invoke forceLogout for a plain Error (not ApiError)', async () => {
    // BDD: Given a QueryClient with the auth-error subscription,
    // When a queryFn throws a plain Error,
    // Then forceLogout is NOT invoked.
    //
    // Traces to: queryClient.ts _handleAuthError: if (!(err instanceof ApiError)) return
    const { client } = makeAuthTestClient()

    await expect(
      client.fetchQuery({
        queryKey: ['test-plain-no-auth'],
        queryFn: () => Promise.reject(new Error('network error')),
      }),
    ).rejects.toThrow('network error')

    await new Promise((resolve) => setTimeout(resolve, 50))

    expect(mockForceLogout).not.toHaveBeenCalled()
  })
})
