// Regression tests for F4 (SILENT-FAILURES-rate-limits-dd25339bf.md):
// "a refused view is retried blindly, then shown as a generic failure".
//
// The view-result query (BasePreview.tsx) and the query-fence embed
// (KbQueryFenceEmbed.tsx) inherited the app-wide query retryDelay curve
// (2s/4s/8s, fixed) which IGNORES the server's `Retry-After` header. The
// knowledge rate limiter's Retry-After can be up to 60s, so all three blind
// retries land inside ~14s and are refused again.
//
// `rateLimitAwareQueryRetryDelay` fixes this for the two call sites that opt
// into it: honour `ApiError.retryAfterMs` when the failure is a 429, capped
// at `RATE_LIMITED_QUERY_RETRY_DELAY_CAP_MS` so the UI is never silently
// stuck waiting the full server-stated window with nothing shown. Every
// other error (network, 5xx, or a 429 with no Retry-After) falls back to the
// EXACT SAME curve `shouldRetryQuery`'s paired default retryDelay already
// uses — this must not regress non-429 retry timing.
//
// `rateLimitedRetryDelayMs` is the single source of truth for BOTH the
// retryDelay function above AND the UI's own "retrying in Ns" display — so
// the two can never say different numbers (the task's own contradiction
// rule: "the retry timing must not contradict what's shown to the user").

import { describe, it, expect } from 'vitest'
import { ApiError } from './api-error'
import {
  rateLimitAwareQueryRetryDelay,
  rateLimitedRetryDelayMs,
  RATE_LIMITED_QUERY_RETRY_DELAY_CAP_MS,
} from './queryClient'

describe('rateLimitedRetryDelayMs — single source of truth for the capped wait', () => {
  it('returns the server Retry-After verbatim when it is under the cap', () => {
    const err = new ApiError(429, 'rate limited', { retryAfterMs: 5_000 })
    expect(rateLimitedRetryDelayMs(err)).toBe(5_000)
  })

  it('caps a large Retry-After (e.g. the knowledge limiter up to 60s) at the sensible ceiling', () => {
    const err = new ApiError(429, 'at most 60 per 1m0s. Retry in 59s.', { retryAfterMs: 59_000 })
    const delay = rateLimitedRetryDelayMs(err)
    expect(delay).toBe(RATE_LIMITED_QUERY_RETRY_DELAY_CAP_MS)
    // The cap itself must actually be "sensible" — never the full 60s the
    // server can state, and never so long the UI looks frozen.
    expect(RATE_LIMITED_QUERY_RETRY_DELAY_CAP_MS).toBeLessThanOrEqual(20_000)
    expect(RATE_LIMITED_QUERY_RETRY_DELAY_CAP_MS).toBeGreaterThan(0)
  })

  it('returns undefined for a non-429 error — callers fall back to their own curve', () => {
    expect(rateLimitedRetryDelayMs(new ApiError(500, 'server unavailable'))).toBeUndefined()
  })

  it('returns undefined for a 429 with no retryAfterMs', () => {
    expect(rateLimitedRetryDelayMs(new ApiError(429, 'rate limited'))).toBeUndefined()
  })

  it('returns undefined for a non-ApiError', () => {
    expect(rateLimitedRetryDelayMs(new Error('boom'))).toBeUndefined()
  })
})

describe('rateLimitAwareQueryRetryDelay — the actual retryDelay wired into the two knowledge queries', () => {
  it('THE DEFECT: honours a 429 Retry-After instead of the blind fixed curve', () => {
    // BDD: Given the knowledge limiter refused a request and said "retry in
    // 45s" (Retry-After: 45), When the query retries, Then the delay used
    // is close to that 45s (capped), NOT the fixed exponential curve's ~1s.
    const err = new ApiError(429, 'at most 60 per 1m0s. Retry in 45s.', { retryAfterMs: 45_000 })
    const delay = rateLimitAwareQueryRetryDelay(0, err)
    // This is the exact reproduction of F4: the OLD behaviour (the plain
    // exponential curve) would return 1000ms here — far too fast, and the
    // three retries would land inside ~14s of a up-to-60s window.
    expect(delay).not.toBe(1_000)
    expect(delay).toBe(RATE_LIMITED_QUERY_RETRY_DELAY_CAP_MS)
  })

  it('does NOT change retry delay timing for non-429 errors — same curve as shouldRetryQuery default', () => {
    const err = new ApiError(500, 'server unavailable')
    expect(rateLimitAwareQueryRetryDelay(0, err)).toBe(1_000)
    expect(rateLimitAwareQueryRetryDelay(1, err)).toBe(2_000)
    expect(rateLimitAwareQueryRetryDelay(2, err)).toBe(4_000)
  })

  it('does NOT change retry delay timing for a network failure (status 0)', () => {
    expect(rateLimitAwareQueryRetryDelay(0, new ApiError(0, 'offline'))).toBe(1_000)
  })

  it('falls back to the exponential curve for a 429 with no Retry-After header', () => {
    expect(rateLimitAwareQueryRetryDelay(0, new ApiError(429, 'rate limited'))).toBe(1_000)
  })
})
