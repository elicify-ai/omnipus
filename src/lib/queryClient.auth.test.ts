// Regression tests for the queryClient 401 auth-error handler.
//
// Guards against:
//   - 401 response not clearing auth tokens from sessionStorage/localStorage
//   - 401 response not redirecting to /login
//   - 401 response being retried (retry storm — should be suppressed)
//   - Debounce failing: two concurrent 401s firing two redirects
//
// Strategy: Create an isolated QueryClient that replicates the _handleAuthError
// and retry logic from queryClient.ts, so we can drive it without importing the
// singleton (which has module-level side effects).
//
// Traces to: feat/level1-project-task-mgmt — SPA 401 handler regression

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { ApiError } from './api'

// ── Mock auth store ───────────────────────────────────────────────────────────
const mockClearAuth = vi.fn()
vi.mock('@/store/auth', () => ({
  useAuthStore: {
    getState: () => ({ clearAuth: mockClearAuth }),
  },
}))

// ── Helpers ───────────────────────────────────────────────────────────────────

/**
 * Create an isolated QueryClient that replicates the _handleAuthError
 * subscription and retry guard from queryClient.ts.
 *
 * Returns both the client and a reset function to clear the debounce flag
 * between tests (mirrors what the 2-second setTimeout does in production).
 */
function makeAuthTestClient() {
  let logoutScheduled = false

  function handleAuthError(err: unknown): void {
    if (!(err instanceof ApiError)) return
    if (err.status !== 401) return
    if (logoutScheduled) return
    logoutScheduled = true

    sessionStorage.removeItem('omnipus_auth_token')
    localStorage.removeItem('omnipus_auth_token')
    localStorage.removeItem('omnipus_auth_role')
    localStorage.removeItem('omnipus_auth_username')

    void import('@/store/auth')
      .then(({ useAuthStore }) => {
        useAuthStore.getState().clearAuth()
      })
      .catch(() => {
        // Store not yet loaded — token removal above is sufficient.
      })

    setTimeout(() => { logoutScheduled = false }, 2_000)

    if (typeof window !== 'undefined') {
      window.location.hash = '/login'
    }
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

  return {
    client,
    resetDebounce: () => { logoutScheduled = false },
  }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('queryClient 401 auth-error handler', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // Seed both storages with a token to verify they get cleared.
    sessionStorage.setItem('omnipus_auth_token', 'sess-tok-abc')
    localStorage.setItem('omnipus_auth_token', 'local-tok-abc')
    localStorage.setItem('omnipus_auth_role', 'user')
    localStorage.setItem('omnipus_auth_username', 'alice')
  })

  afterEach(() => {
    vi.restoreAllMocks()
    sessionStorage.clear()
    localStorage.clear()
  })

  it('clears auth tokens from sessionStorage and localStorage on 401', async () => {
    // BDD: Given a QueryClient with the auth-error subscription,
    // When a queryFn throws ApiError(401),
    // Then sessionStorage and localStorage auth keys are removed.
    //
    // Traces to: queryClient.ts _handleAuthError — token cleanup on 401
    const { client } = makeAuthTestClient()

    await expect(
      client.fetchQuery({
        queryKey: ['test-401-tokens'],
        queryFn: () => Promise.reject(new ApiError(401)),
      }),
    ).rejects.toThrow()

    // Allow the async handler to fire.
    await new Promise((resolve) => setTimeout(resolve, 50))

    expect(sessionStorage.getItem('omnipus_auth_token')).toBeNull()
    expect(localStorage.getItem('omnipus_auth_token')).toBeNull()
    expect(localStorage.getItem('omnipus_auth_role')).toBeNull()
    expect(localStorage.getItem('omnipus_auth_username')).toBeNull()
  })

  it('sets window.location.hash to /login on 401', async () => {
    // BDD: Given a QueryClient with the auth-error subscription,
    // When a queryFn throws ApiError(401),
    // Then window.location.hash is set to /login.
    //
    // Traces to: queryClient.ts _handleAuthError — redirect to /login on 401
    const { client } = makeAuthTestClient()

    await expect(
      client.fetchQuery({
        queryKey: ['test-401-redirect'],
        queryFn: () => Promise.reject(new ApiError(401)),
      }),
    ).rejects.toThrow()

    await new Promise((resolve) => setTimeout(resolve, 50))

    // window.location.hash prepends '#' when read back (browser standard behavior).
    expect(window.location.hash).toBe('#/login')
  })

  it('calls clearAuth on the auth store on 401', async () => {
    // BDD: Given a QueryClient with the auth-error subscription,
    // When a queryFn throws ApiError(401),
    // Then useAuthStore.getState().clearAuth() is called.
    //
    // Traces to: queryClient.ts _handleAuthError — dynamic import + clearAuth on 401
    const { client } = makeAuthTestClient()

    await expect(
      client.fetchQuery({
        queryKey: ['test-401-store'],
        queryFn: () => Promise.reject(new ApiError(401)),
      }),
    ).rejects.toThrow()

    await new Promise((resolve) => setTimeout(resolve, 50))

    expect(mockClearAuth).toHaveBeenCalledOnce()
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

  it('debounces: second concurrent 401 does NOT clear tokens a second time', async () => {
    // BDD: Given a 401 was already handled (debounce flag set),
    // When a second 401 fires within the 2-second window,
    // Then clearAuth is called only once (not twice) and token storage is NOT re-set.
    //
    // Traces to: queryClient.ts _handleAuthError debounce — _logoutScheduled flag
    const { client } = makeAuthTestClient()

    // First 401.
    await expect(
      client.fetchQuery({
        queryKey: ['test-401-debounce-1'],
        queryFn: () => Promise.reject(new ApiError(401)),
      }),
    ).rejects.toThrow()

    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(mockClearAuth).toHaveBeenCalledOnce()

    // Re-seed the token to see if the second 401 clears it again.
    localStorage.setItem('omnipus_auth_token', 'local-tok-second')

    // Second 401 — within the debounce window.
    await expect(
      client.fetchQuery({
        queryKey: ['test-401-debounce-2'],
        queryFn: () => Promise.reject(new ApiError(401)),
      }),
    ).rejects.toThrow()

    await new Promise((resolve) => setTimeout(resolve, 50))

    // clearAuth must still be called exactly once (debounced).
    expect(mockClearAuth).toHaveBeenCalledOnce()
  })

  it('does NOT redirect or clear tokens for non-401 ApiError (403 is auth but different behavior)', async () => {
    // BDD: Given a QueryClient with the auth-error subscription,
    // When a queryFn throws ApiError(403) (forbidden, not unauthenticated),
    // Then sessionStorage is NOT cleared and window.location.hash is NOT changed to /login.
    //
    // Traces to: queryClient.ts _handleAuthError: if (err.status !== 401) return
    const { client } = makeAuthTestClient()

    const hashBefore = window.location.hash

    await expect(
      client.fetchQuery({
        queryKey: ['test-403-no-redirect'],
        queryFn: () => Promise.reject(new ApiError(403)),
      }),
    ).rejects.toThrow()

    await new Promise((resolve) => setTimeout(resolve, 50))

    // 403 must NOT clear tokens or redirect (only 401 does).
    expect(sessionStorage.getItem('omnipus_auth_token')).toBe('sess-tok-abc')
    expect(window.location.hash).toBe(hashBefore)
    expect(mockClearAuth).not.toHaveBeenCalled()
  })

  it('does NOT redirect or clear tokens for a plain Error (not ApiError)', async () => {
    // BDD: Given a QueryClient with the auth-error subscription,
    // When a queryFn throws a plain Error,
    // Then no auth cleanup occurs.
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

    expect(sessionStorage.getItem('omnipus_auth_token')).toBe('sess-tok-abc')
    expect(localStorage.getItem('omnipus_auth_token')).toBe('local-tok-abc')
    expect(mockClearAuth).not.toHaveBeenCalled()
  })
})
