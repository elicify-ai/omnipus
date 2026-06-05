import { describe, it, expect, beforeEach, vi } from 'vitest'
import { checkTokenValidity, resetTokenValidationCache, VALIDATE_TTL_MS } from './authValidation'
import { ApiError } from '@/lib/api'

// #359 — deterministic coverage for the session-expiry hardening: the validate
// call is cached for VALIDATE_TTL_MS, only a confirmed 401 evicts the session, and
// transient (network/5xx) failures keep it.
describe('checkTokenValidity (#359 session-expiry hardening)', () => {
  beforeEach(() => resetTokenValidationCache())

  it('validates and returns ok on a cold cache', async () => {
    const validate = vi.fn().mockResolvedValue({})
    expect(await checkTokenValidity(validate, 1_000_000)).toBe('ok')
    expect(validate).toHaveBeenCalledTimes(1)
  })

  it('skips validation within the TTL window (no /auth/validate per navigation)', async () => {
    const validate = vi.fn().mockResolvedValue({})
    await checkTokenValidity(validate, 1_000_000)
    const verdict = await checkTokenValidity(validate, 1_000_000 + VALIDATE_TTL_MS)
    expect(verdict).toBe('ok')
    expect(validate).toHaveBeenCalledTimes(1) // cache hit — not re-called
  })

  it('re-validates once the TTL window has elapsed', async () => {
    const validate = vi.fn().mockResolvedValue({})
    await checkTokenValidity(validate, 1_000_000)
    await checkTokenValidity(validate, 1_000_000 + VALIDATE_TTL_MS + 1)
    expect(validate).toHaveBeenCalledTimes(2)
  })

  it('returns unauthorized on a confirmed 401', async () => {
    const validate = vi.fn().mockRejectedValue(new ApiError(401, 'unauthorized'))
    expect(await checkTokenValidity(validate, 1_000_000)).toBe('unauthorized')
  })

  it('returns transient (keeps the session) on network/5xx failures', async () => {
    const network = vi.fn().mockRejectedValue(new ApiError(0, 'network down'))
    expect(await checkTokenValidity(network, 1_000_000)).toBe('transient')

    const serverErr = vi.fn().mockRejectedValue(new ApiError(503, 'unavailable'))
    expect(await checkTokenValidity(serverErr, 2_000_000)).toBe('transient')
  })

  it('does not poison the cache on 401 — the next call re-validates', async () => {
    const fail = vi.fn().mockRejectedValue(new ApiError(401, 'no'))
    expect(await checkTokenValidity(fail, 1_000_000)).toBe('unauthorized')

    const ok = vi.fn().mockResolvedValue({})
    expect(await checkTokenValidity(ok, 1_000_001)).toBe('ok')
    expect(ok).toHaveBeenCalledTimes(1)
  })

  it('does not cache a transient failure — the next call re-validates', async () => {
    const transient = vi.fn().mockRejectedValue(new ApiError(0, 'network'))
    expect(await checkTokenValidity(transient, 1_000_000)).toBe('transient')

    const ok = vi.fn().mockResolvedValue({})
    expect(await checkTokenValidity(ok, 1_000_001)).toBe('ok')
    expect(ok).toHaveBeenCalledTimes(1)
  })

  it('resetTokenValidationCache forces an immediate re-validation', async () => {
    const validate = vi.fn().mockResolvedValue({})
    await checkTokenValidity(validate, 1_000_000)
    resetTokenValidationCache()
    await checkTokenValidity(validate, 1_000_000) // same now, but cache was reset
    expect(validate).toHaveBeenCalledTimes(2)
  })
})
