/**
 * queryClient.planted-cookie.test.ts — RED test, ADR-094 TDD Plan order 28
 * (TestHandleAuthError_NoForceLogoutAfterClears, round-2 MAJ-008).
 *
 * Traces to: docs/internal/specs/adr-094-preview-isolation-spec.md
 *   Order 28 (S-4.4): a 401 whose response carries planted-cookie clear
 *   lines, followed by a clean session re-check, does NOT call forceLogout
 *   (src/lib/queryClient.ts::handleAuthError, _recheckSessionValidity path);
 *   a 401 with NO clear lines still force-logs-out (control).
 *
 * THE SPA-SIDE MANIFESTATION OF "response carries clear lines": Set-Cookie is
 * never JS-readable, so the only discriminator the SPA can see is the 401
 * body envelope code planted_cookie_cleared (FR-015(4)). The test builds the
 * error through the REAL ApiError.fromResponse so the wire shape is the one
 * production parses.
 *
 * HONEST RED CLASSIFICATION (pin-with-guard): handleAuthError today
 * re-checks on every 401 and logs out only on \'unauthorized\' — so both
 * spec'd cases behave as the spec demands TODAY. This is the spec\'s own
 * established pin pattern (cf. order 25: "a PIN of controls that already
 * hold"). Its sensitivity is proven by CHECK mutations (the M-6 family /
 * a never-logout drift), not by a pre-change red.
 */

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { handleAuthError } from './queryClient'
import { ApiError } from './api-error'
import { forceLogout } from './authLogout'
import { checkTokenValidity } from '@/routes/authValidation'

vi.mock('@/lib/authLogout', () => ({ forceLogout: vi.fn() }))
vi.mock('@/routes/authValidation', () => ({
  checkTokenValidity: vi.fn(),
  // queryClient imports resetTokenValidationCache for its recheck dedup.
  resetTokenValidationCache: vi.fn(),
}))

import { checkTokenValidity as checkTokenValidityMock } from '@/routes/authValidation'
import { forceLogout as forceLogoutMock } from '@/lib/authLogout'

const PLANTED_401_BODY = {
  code: 'planted_cookie_cleared',
  error: 'planted_cookie_cleared',
  message: 'Omnipus cleared cookies set by a preview — please retry',
}

async function planted401(): Promise<ApiError> {
  return ApiError.fromResponse(
    new Response(JSON.stringify(PLANTED_401_BODY), {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    }),
  )
}

async function plain401(): Promise<ApiError> {
  return ApiError.fromResponse(
    new Response(JSON.stringify({ error: 'unauthorized' }), {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    }),
  )
}

beforeEach(() => {
  vi.mocked(checkTokenValidityMock).mockReset()
  vi.mocked(forceLogoutMock).mockClear()
})

describe('handleAuthError — planted-cookie recovery (order 28, S-4.4)', () => {
  it('S-4.4: 401 + planted clear lines + CLEAN re-check => NO forceLogout', async () => {
    vi.mocked(checkTokenValidityMock).mockResolvedValue('ok')
    const err = await planted401()
    await handleAuthError(err)
    expect(forceLogoutMock).not.toHaveBeenCalled()
  })

  it('control: plain 401 + unauthorized re-check => forceLogout fires exactly once', async () => {
    vi.mocked(checkTokenValidityMock).mockResolvedValue('unauthorized')
    const err = await plain401()
    await handleAuthError(err)
    expect(forceLogoutMock).toHaveBeenCalledTimes(1)
  })

  it('differentiation: 403 (even with the planted code) never reaches the logout path', async () => {
    vi.mocked(checkTokenValidityMock).mockResolvedValue('unauthorized')
    const err = await ApiError.fromResponse(
      new Response(JSON.stringify(PLANTED_401_BODY), {
        status: 403,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    await handleAuthError(err)
    expect(forceLogoutMock).not.toHaveBeenCalled()
  })

  it('derived guard: 401 + planted code but a GENUINELY DEAD session still logs out', async () => {
    // The recovery must never become a logout bypass: if the re-check says
    // the session is genuinely unauthorized, forceLogout is correct. This
    // row is MY derived guard (not a spec\'d case): it fails any GREEN that
    // short-circuits the re-check on the planted code alone.
    vi.mocked(checkTokenValidityMock).mockResolvedValue('unauthorized')
    const err = await planted401()
    await handleAuthError(err)
    expect(forceLogoutMock).toHaveBeenCalledTimes(1)
  })
})
