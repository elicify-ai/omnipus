// Regression tests for the queryClient MUTATION retry predicate.
//
// S3 UAT finding (plan-creation flow): a single Create click on the New Plan
// form fired up to 4 identical POSTs (t=0, +1s, +3s, +7s) — all deterministic
// 400s — before the error toast ever appeared (~7.2s later). Root cause:
// `shouldRetryMutation` previously mirrored the QUERY retry predicate exactly
// (only 401/403/404 excluded), so an ordinary validation 400 was retried like
// a transient failure. Fixed in src/lib/queryClient.ts: mutations now exclude
// ANY 4xx, not just 401/403/404.
//
// Unlike queryClient.retry.test.ts / queryClient.auth.test.ts (which hand-
// replicate the predicate to avoid importing the side-effectful singleton),
// this suite imports the REAL exported `shouldRetryMutation` /
// `shouldRetryQuery` functions directly — they're pure functions with no
// module-init side effects, so there's no isolation reason to duplicate them,
// and importing the real code means this suite can't silently drift from
// production behaviour the way a hand-copied predicate could.
//
// Traces to: src/lib/queryClient.ts `shouldRetryMutation` / `shouldRetryQuery`

import { describe, it, expect } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { ApiError, ApiSchemaError } from './api'
import { shouldRetryMutation, shouldRetryQuery } from './queryClient'

describe('shouldRetryMutation — unit', () => {
  it('does NOT retry a 400 (deterministic validation error, e.g. invalid owner_agent_id)', () => {
    // BDD: Given a plan-create mutation rejected with ApiError(400),
    // When the retry predicate is evaluated on the first failure,
    // Then it returns false — a 400 can never succeed by resending the exact
    // same request, so it must not be retried.
    expect(shouldRetryMutation(0, new ApiError(400))).toBe(false)
  })

  it('does NOT retry other 4xx statuses either (409, 413, 422, 429) — not just 401/403/404', () => {
    // Differentiation: proves the guard is "any 4xx", not the old
    // 401/403/404-only allowlist that let 400/409/413/422/429 through.
    expect(shouldRetryMutation(0, new ApiError(409))).toBe(false)
    expect(shouldRetryMutation(0, new ApiError(413))).toBe(false)
    expect(shouldRetryMutation(0, new ApiError(422))).toBe(false)
    expect(shouldRetryMutation(0, new ApiError(429))).toBe(false)
  })

  it('does NOT retry 401/403/404 (still excluded, as before)', () => {
    expect(shouldRetryMutation(0, new ApiError(401))).toBe(false)
    expect(shouldRetryMutation(0, new ApiError(403))).toBe(false)
    expect(shouldRetryMutation(0, new ApiError(404))).toBe(false)
  })

  it('does NOT retry ApiSchemaError', () => {
    const schemaErr = new ApiSchemaError('/plans', [{ path: [], message: 'bad' }], null)
    expect(shouldRetryMutation(0, schemaErr)).toBe(false)
  })

  it('DOES retry a 500 (server error may be transient) — preserved behaviour', () => {
    // BDD: Given a mutation rejected with ApiError(500),
    // Then it IS retried — the finding explicitly says "retrying 5xx/network
    // errors is defensible — keep that if it is deliberate." It is.
    expect(shouldRetryMutation(0, new ApiError(500))).toBe(true)
  })

  it('DOES retry a 503 (server error) — preserved behaviour', () => {
    expect(shouldRetryMutation(0, new ApiError(503))).toBe(true)
  })

  it('DOES retry a network failure (status 0, request never reached the server) — preserved behaviour', () => {
    expect(shouldRetryMutation(0, new ApiError(0))).toBe(true)
  })

  it('stops retrying after 3 failures even for a retryable (500) error', () => {
    expect(shouldRetryMutation(2, new ApiError(500))).toBe(true)
    expect(shouldRetryMutation(3, new ApiError(500))).toBe(false)
  })

  it('retries a plain (non-ApiError) Error up to 3 times', () => {
    expect(shouldRetryMutation(0, new Error('network error'))).toBe(true)
    expect(shouldRetryMutation(2, new Error('network error'))).toBe(true)
    expect(shouldRetryMutation(3, new Error('network error'))).toBe(false)
  })
})

describe('shouldRetryQuery — unchanged (differentiates from the mutation fix)', () => {
  // These pin that the QUERY predicate was deliberately left alone: only
  // 401/403/404 are excluded, every other 4xx (including 400) still retries.
  // If this ever changes, it should be a conscious decision, not a side
  // effect of the mutation fix above.
  it('still retries a 400 for queries (queries are idempotent GETs; not the S3 finding)', () => {
    expect(shouldRetryQuery(0, new ApiError(400))).toBe(true)
  })

  it('still excludes 401/403/404 for queries', () => {
    expect(shouldRetryQuery(0, new ApiError(401))).toBe(false)
    expect(shouldRetryQuery(0, new ApiError(403))).toBe(false)
    expect(shouldRetryQuery(0, new ApiError(404))).toBe(false)
  })
})

describe('shouldRetryMutation — integration (real TanStack QueryClient + real predicate)', () => {
  it('a mutation that always 400s is attempted exactly once — no retry storm', async () => {
    // BDD: Given a QueryClient wired with the real `shouldRetryMutation`,
    // When a mutationFn always rejects with ApiError(400) (e.g. createPlan
    // with an invalid owner_agent_id),
    // Then the mutationFn is called exactly once — reproducing the S3 UAT
    // finding's "up to 4 identical POSTs" would show callCount > 1 here.
    const client = new QueryClient({
      defaultOptions: { mutations: { retry: shouldRetryMutation, retryDelay: 0 } },
    })
    const mutationFn = makeRejectingMutationFn(new ApiError(400, 'invalid owner_agent_id'))

    const mutation = client.getMutationCache().build(client, { mutationFn: mutationFn.fn })
    await expect(mutation.execute(undefined)).rejects.toThrow()

    expect(mutationFn.callCount()).toBe(1)
  })

  it('a mutation that always 500s IS retried (server error, transient)', async () => {
    const client = new QueryClient({
      defaultOptions: { mutations: { retry: shouldRetryMutation, retryDelay: 0 } },
    })
    const mutationFn = makeRejectingMutationFn(new ApiError(500, 'server unavailable'))

    const mutation = client.getMutationCache().build(client, { mutationFn: mutationFn.fn })
    await expect(mutation.execute(undefined)).rejects.toThrow()

    // failureCount < 3 retries => up to 4 total calls; assert MORE than one
    // to prove retry is happening (differentiation from the 400 case above).
    expect(mutationFn.callCount()).toBeGreaterThan(1)
  })
})

// Small helper: a mutationFn that always rejects with the given error, with a
// call counter — avoids pulling in vi.fn()'s mock-call-array bookkeeping for
// what's a simple "did this run more than once" check.
function makeRejectingMutationFn(err: unknown): { fn: () => Promise<never>; callCount: () => number } {
  let calls = 0
  return {
    fn: () => {
      calls += 1
      return Promise.reject(err)
    },
    callCount: () => calls,
  }
}
