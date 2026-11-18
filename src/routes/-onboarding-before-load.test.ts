import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    // createFileRoute is stubbed so beforeLoad can run without a router.
    // redirect and isRedirect stay the real exports (issue #1858): a lookalike
    // that merely has an own `to` is what the gate must refuse.
    createFileRoute: () => (options: unknown) => options,
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAppState: vi.fn() }
})

import { isRedirect } from '@tanstack/react-router'
import { ApiError, fetchAppState } from '@/lib/api'
import { Route } from './onboarding'

const beforeLoad = (Route as unknown as {
  beforeLoad: () => Promise<{ appStateBannerMessage: string | null; onboardingAuthMode?: string }>
}).beforeLoad

describe('onboarding route guard', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    localStorage.clear()
  })

  afterEach(() => localStorage.clear())

  it('redirects an unsigned platform user before showing onboarding', async () => {
    vi.mocked(fetchAppState).mockResolvedValue({
      onboarding_complete: false,
      identity: { mode: 'platform', signed_in: false },
    } as Awaited<ReturnType<typeof fetchAppState>>)

    // Issue #1858: signed_in false in platform mode leaves onboarding via a
    // real router redirect to /login. The target lives at options.to. A
    // rejection that merely has an own `to` is not that redirect.
    const settled = await beforeLoad().then(
      (value) => ({ kind: 'resolved' as const, value }),
      (err: unknown) => ({ kind: 'rejected' as const, err }),
    )
    expect(settled.kind).toBe('rejected')
    if (settled.kind !== 'rejected') {
      throw new Error('expected beforeLoad to reject with a real redirect to /login')
    }
    expect(isRedirect(settled.err)).toBe(true)
    if (!isRedirect(settled.err)) {
      throw new Error('expected the rejection to be a real router redirect, not a lookalike')
    }
    expect(settled.err).toBeInstanceOf(Response)
    expect(settled.err.options.to).toBe('/login')
    expect('to' in settled.err).toBe(false)
    expect(fetchAppState).toHaveBeenCalledOnce()
  })

  it('allows local first-run setup without a stored session', async () => {
    vi.mocked(fetchAppState).mockResolvedValue({
      onboarding_complete: false,
      identity: { mode: 'local' },
    } as Awaited<ReturnType<typeof fetchAppState>>)

    await expect(beforeLoad()).resolves.toEqual({ appStateBannerMessage: null, onboardingAuthMode: 'local' })
  })

  it('preserves the visible server error when setup state is unavailable', async () => {
    vi.mocked(fetchAppState).mockRejectedValue(new ApiError(503))

    await expect(beforeLoad()).resolves.toEqual({
      appStateBannerMessage: 'Could not load setup state — server returned 503',
      onboardingAuthMode: undefined,
    })
  })
})
