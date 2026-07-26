// Regression tests for the queryClient MUTATION retry predicate.
//
// S3 UAT finding (plan-creation flow): a single Create click on the New Plan
// form fired up to 4 identical POSTs (t=0, +1s, +3s, +7s) — all deterministic
// 400s — before the error toast ever appeared (~7.2s later). Root cause:
// `shouldRetryMutation` previously mirrored the QUERY retry predicate exactly
// (only 401/403/404 excluded), so an ordinary validation 400 was retried like
// a transient failure. Fixed in src/lib/queryClient.ts: mutations now exclude
// ANY 4xx, not just 401/403/404 — EXCEPT 408/429 (see the follow-up below).
//
// Follow-up regression (code review on the fix above): the blanket 4xx
// exclusion over-corrected, discarding 408/429 too — both are documented,
// retryable congestion signals on THIS backend (see
// `RETRYABLE_MUTATION_4XX_STATUSES`'s doc comment in queryClient.ts for the
// checked, not assumed, evidence: pkg/gateway/rest_tasks.go:1164's "409 —
// dispatch cap exhausted (retryable congestion)" comment turned out to sit on
// an endpoint that ALSO returns 409 for hard plan-state conflicts with no way
// for the client to tell them apart, so 409 stays excluded; but
// pkg/gateway/rest_clivalidate.go:133's 429 — with an explicit
// `Retry-After` header — is unambiguous). The assertions below were
// FLIPPED for 408/429 accordingly (see the comment on each).
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
// / `mutationRetryDelay`

import { describe, it, expect } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { ApiError, ApiSchemaError } from './api'
import { shouldRetryMutation, shouldRetryQuery, mutationRetryDelay } from './queryClient'

describe('shouldRetryMutation — unit', () => {
  it('does NOT retry a 400 (deterministic validation error, e.g. invalid owner_agent_id)', () => {
    // BDD: Given a plan-create mutation rejected with ApiError(400),
    // When the retry predicate is evaluated on the first failure,
    // Then it returns false — a 400 can never succeed by resending the exact
    // same request, so it must not be retried.
    expect(shouldRetryMutation(0, new ApiError(400))).toBe(false)
  })

  it('does NOT retry 409, 413, or 422 — non-idempotent-unsafe/deterministic conflicts and validation errors', () => {
    // 409 is DELIBERATELY still excluded even though some 409s on this
    // backend are genuine congestion (agent.ErrDispatchCapReached): the very
    // same PATCH /tasks/{id} endpoint also returns 409 for a hard plan-state
    // conflict (agent.ErrPlanNotExecuting/ErrPlanStateUnresolvable), and the
    // JSON error body carries no field distinguishing the two — blanket-
    // retrying would blindly retry the state-conflict case too, and a 409
    // meaning "already exists" must never be retried on a non-idempotent
    // create. 413/422 are ordinary deterministic client errors.
    expect(shouldRetryMutation(0, new ApiError(409))).toBe(false)
    expect(shouldRetryMutation(0, new ApiError(413))).toBe(false)
    expect(shouldRetryMutation(0, new ApiError(422))).toBe(false)
  })

  it('DOES retry 408 (Request Timeout) — FLIPPED from the prior blanket-4xx-exclusion pin', () => {
    // FLIPPED (regression fix): 408 is an unambiguous "the server gave up
    // waiting, try again" signal, not a validation error — retrying is safe
    // and correct regardless of which endpoint returned it.
    expect(shouldRetryMutation(0, new ApiError(408))).toBe(true)
  })

  it('DOES retry 429 (Too Many Requests) — FLIPPED from the prior blanket-4xx-exclusion pin', () => {
    // FLIPPED (regression fix): the prior test asserted `toBe(false)` here,
    // pinning the over-correction as if it were correct. 429 means
    // rate-limited/congested, not rejected as invalid — this backend's own
    // HandleSystemCliValidate in-flight cap (pkg/gateway/rest_clivalidate.go)
    // returns exactly this status, with a `Retry-After` header, precisely so
    // the caller retries shortly.
    expect(shouldRetryMutation(0, new ApiError(429))).toBe(true)
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

  it('a mutation that always 429s (dispatch-cap congestion) IS retried end-to-end', async () => {
    // BDD: Given a QueryClient wired with the real `shouldRetryMutation`,
    // When the board's task-move mutation is rejected with ApiError(429)
    // (e.g. rest_clivalidate.go's in-flight cap, or any future dispatch-cap
    // 429), Then the mutationFn is retried — this is the exact scenario the
    // blanket-4xx-exclusion regression silently broke.
    const client = new QueryClient({
      defaultOptions: { mutations: { retry: shouldRetryMutation, retryDelay: 0 } },
    })
    const mutationFn = makeRejectingMutationFn(
      new ApiError(429, 'too many concurrent validations in flight; retry shortly'),
    )

    const mutation = client.getMutationCache().build(client, { mutationFn: mutationFn.fn })
    await expect(mutation.execute(undefined)).rejects.toThrow()

    expect(mutationFn.callCount()).toBeGreaterThan(1)
  })

  it('a mutation that always 409s (dispatch-cap OR plan-state conflict, indistinguishable) is attempted exactly once', async () => {
    // BDD: Given the SAME PATCH /tasks/{id} 409 the DoD requires checking
    // (see shouldRetryMutation's doc comment), When it always fails, Then it
    // is attempted exactly once — proving 409 was correctly left OUT of the
    // retryable set despite some 409s on this backend being genuine
    // congestion, because the client cannot tell which one it got.
    const client = new QueryClient({
      defaultOptions: { mutations: { retry: shouldRetryMutation, retryDelay: 0 } },
    })
    const mutationFn = makeRejectingMutationFn(new ApiError(409, 'dispatch cap exhausted'))

    const mutation = client.getMutationCache().build(client, { mutationFn: mutationFn.fn })
    await expect(mutation.execute(undefined)).rejects.toThrow()

    expect(mutationFn.callCount()).toBe(1)
  })
})

describe('mutationRetryDelay — honours Retry-After', () => {
  it('uses ApiError.retryAfterMs verbatim when present, ignoring the attempt number', () => {
    // BDD: Given a 429 response whose Retry-After header parsed to 5000ms,
    // When mutationRetryDelay computes the delay for ANY attempt number,
    // Then it returns exactly 5000 — the server's stated wait, not a guess.
    const err = new ApiError(429, 'rate limited', { retryAfterMs: 5000 })
    expect(mutationRetryDelay(0, err)).toBe(5000)
    expect(mutationRetryDelay(2, err)).toBe(5000)
  })

  it('falls back to exponential backoff when retryAfterMs is absent', () => {
    const err = new ApiError(500, 'server unavailable')
    expect(mutationRetryDelay(0, err)).toBe(1000)
    expect(mutationRetryDelay(1, err)).toBe(2000)
    expect(mutationRetryDelay(2, err)).toBe(4000)
  })

  it('falls back to exponential backoff for a non-ApiError', () => {
    expect(mutationRetryDelay(0, new Error('network error'))).toBe(1000)
  })

  it('ignores a zero/negative retryAfterMs (already-expired Retry-After) and falls back', () => {
    // ApiError.fromResponse itself never constructs a non-positive
    // retryAfterMs (parseRetryAfterMs returns undefined instead), but
    // mutationRetryDelay's own guard is defense-in-depth against a
    // hand-constructed ApiError carrying one directly.
    const err = new ApiError(429, 'rate limited', { retryAfterMs: 0 })
    expect(mutationRetryDelay(0, err)).toBe(1000)
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
