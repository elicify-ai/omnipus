import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (options: unknown) => options,
    redirect: (options: { to: string }) => Object.assign(new Error('redirect'), options),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAppState: vi.fn() }
})

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
      identity: { mode: 'platform' },
    } as Awaited<ReturnType<typeof fetchAppState>>)

    await expect(beforeLoad()).rejects.toMatchObject({ to: '/login' })
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
