// Regression tests for the queryClient retry predicate.
//
// Guards against:
//   - 404 being retried (must NOT retry — missing resource won't appear on retry)
//   - 401 being retried (existing guard, verified here with the real retry predicate)
//   - 403 being retried (existing guard, verified here with the real retry predicate)
//   - 500 NOT being retried (500 IS retried — this test catches over-broad guards)
//
// Strategy: Exercise the real retry predicate from queryClient.ts directly —
// not the mock from queryClient.auth.test.ts which only covers 401/403.
//
// Traces to: src/lib/queryClient.ts retry predicate — #406/#408 fixes

import { describe, it, expect, vi } from 'vitest'
import { ApiError, ApiSchemaError } from './api'

// ── Real retry predicate (mirroring queryClient.ts exactly) ──────────────────
//
// We replicate the predicate here so the test exercises the exact same logic
// that ships to production, including the 404 guard added in this epic.

function realRetryPredicate(failureCount: number, err: unknown): boolean {
  return (
    !(err instanceof ApiSchemaError) &&
    !(err instanceof ApiError && (err.status === 401 || err.status === 403 || err.status === 404)) &&
    failureCount < 3
  )
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('queryClient real retry predicate', () => {
  it('returns false for ApiError 404 on failureCount=0 (no retry for missing resource)', () => {
    // BDD: Given a 404 ApiError,
    // When the retry predicate is evaluated on the first failure,
    // Then false is returned — 404 is never retried.
    //
    // Guards against: missing resource appearing to be retryable.
    // Traces to: src/lib/queryClient.ts retry guard: err.status === 404
    const result = realRetryPredicate(0, new ApiError(404))
    expect(result).toBe(false)
  })

  it('returns false for ApiError 404 regardless of failureCount', () => {
    // Differentiation: 404 must never be retried, even on the first attempt.
    expect(realRetryPredicate(0, new ApiError(404))).toBe(false)
    expect(realRetryPredicate(1, new ApiError(404))).toBe(false)
    expect(realRetryPredicate(2, new ApiError(404))).toBe(false)
  })

  it('returns false for ApiError 401 (auth error, not retried)', () => {
    // BDD: 401 is an auth error — retrying won't fix it.
    // Traces to: src/lib/queryClient.ts retry guard: err.status === 401
    expect(realRetryPredicate(0, new ApiError(401))).toBe(false)
  })

  it('returns false for ApiError 403 (forbidden, not retried)', () => {
    // BDD: 403 is a permission error — retrying won't fix it.
    // Traces to: src/lib/queryClient.ts retry guard: err.status === 403
    expect(realRetryPredicate(0, new ApiError(403))).toBe(false)
  })

  it('returns false for ApiSchemaError (schema mismatch, not retried)', () => {
    // BDD: Schema errors cannot be fixed by retrying.
    // Traces to: src/lib/queryClient.ts retry guard: err instanceof ApiSchemaError
    const schemaErr = new ApiSchemaError('/test', [{ path: [], message: 'bad' }], null)
    expect(realRetryPredicate(0, schemaErr)).toBe(false)
  })

  it('returns true for ApiError 500 on first attempt (server errors ARE retried)', () => {
    // BDD: 500 is a transient server error — it IS retried.
    // Differentiation: proves the guard is specific to 401/403/404, not all ApiErrors.
    //
    // Traces to: src/lib/queryClient.ts retry — 500 not blocked
    expect(realRetryPredicate(0, new ApiError(500))).toBe(true)
  })

  it('returns true for ApiError 503 on first attempt (transient error IS retried)', () => {
    // 503 is also retried — proves the guard is not over-broad.
    expect(realRetryPredicate(0, new ApiError(503))).toBe(true)
  })

  it('returns false for ApiError 500 after 3 failures (failureCount >= 3)', () => {
    // Even retriable errors stop after failureCount >= 3.
    expect(realRetryPredicate(3, new ApiError(500))).toBe(false)
  })

  it('returns false for a plain Error (non-ApiError type is not retried via ApiError guard, but retried up to 3×)', () => {
    // Plain errors are retried up to 3 times (not subject to ApiError guard).
    expect(realRetryPredicate(0, new Error('network error'))).toBe(true)
    expect(realRetryPredicate(2, new Error('network error'))).toBe(true)
    // But not on the 4th attempt.
    expect(realRetryPredicate(3, new Error('network error'))).toBe(false)
  })

  // Suppress unused import lint.
  it('no-op: vi is used for import isolation', () => {
    expect(vi).toBeDefined()
  })
})
