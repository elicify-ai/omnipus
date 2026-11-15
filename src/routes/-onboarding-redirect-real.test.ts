// Issue #1858. Oracles are the dispatch requirements, not the current catch.
// redirect() and isRedirect() are the real @tanstack/react-router exports.
// fetchAppState is the only mock (GET /state is the network edge).
//
// Today's 5xx banner copy (requirement 5, "keep today's banner behaviour")
// is the string already pinned by -onboarding-before-load.test.ts:
// "Could not load setup state — server returned <status>" (em dash, U+2014).

import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { isRedirect, redirect } from '@tanstack/react-router'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAppState: vi.fn() }
})

import { ApiError, fetchAppState, type AppState } from '@/lib/api'
import { Route } from './onboarding'

// Requirement 1 and 6: an unsigned platform caller goes to the login route.
const LOGIN_PATH = '/login'
// Requirement 4: a finished onboarding goes to the app root.
const HOME_PATH = '/'
// hasStoredSession() reads this key. Hosted SSO leaves it unset (requirement 2).
const STORED_SESSION_KEY = 'omnipus_auth_username'
const FETCH_FAILURE_LOG = 'onboarding.app_state_fetch_failed'

type GuardValue = {
  appStateBannerMessage: string | null
  onboardingAuthMode?: string
}

type Outcome =
  | { kind: 'resolved'; value: GuardValue }
  | { kind: 'redirect'; to: unknown }
  | { kind: 'rejected'; name: string; message: string; to: unknown }

type Seen = {
  outcome: Outcome
  loggedFetchFailure: boolean
  stateFetches: number
}

let beforeLoad: () => Promise<GuardValue>
let errorSpy: ReturnType<typeof vi.spyOn>

function serverBanner(status: number): string {
  return `Could not load setup state — server returned ${status}`
}

function buildState(input: {
  mode?: string
  omitIdentity?: boolean
  edition?: 'core' | 'desktop' | 'hosted'
  signedIn?: boolean | null | string | number
  omitSignedIn?: boolean
  onboardingComplete?: boolean
}): AppState {
  const state: Record<string, unknown> = {
    onboarding_complete: input.onboardingComplete ?? false,
  }
  if (!input.omitIdentity) {
    const identity: Record<string, unknown> = {
      mode: input.mode ?? 'platform',
      edition: input.edition ?? 'hosted',
    }
    if (!input.omitSignedIn) identity.signed_in = input.signedIn ?? false
    state.identity = identity
  }
  return state as unknown as AppState
}

function setStoredSession(): void {
  localStorage.setItem(STORED_SESSION_KEY, 'owner')
}

function loggedFetchFailure(): boolean {
  return errorSpy.mock.calls.some((call: readonly unknown[]) => call[0] === FETCH_FAILURE_LOG)
}

async function settle(): Promise<Outcome> {
  try {
    const value = await beforeLoad()
    return { kind: 'resolved', value }
  } catch (err) {
    if (isRedirect(err)) return { kind: 'redirect', to: err.options.to }
    const error = err instanceof Error ? err : null
    const to =
      err !== null && typeof err === 'object' && 'to' in err
        ? (err as { to: unknown }).to
        : undefined
    return {
      kind: 'rejected',
      name: error?.name ?? typeof err,
      message: error?.message ?? String(err),
      to,
    }
  }
}

async function observe(): Promise<Seen> {
  const outcome = await settle()
  return {
    outcome,
    loggedFetchFailure: loggedFetchFailure(),
    stateFetches: vi.mocked(fetchAppState).mock.calls.length,
  }
}

function resolved(mode: string | undefined, banner: string | null = null): Seen {
  return {
    outcome: {
      kind: 'resolved',
      value: { appStateBannerMessage: banner, onboardingAuthMode: mode },
    },
    loggedFetchFailure: false,
    stateFetches: 1,
  }
}

function redirected(to: string): Seen {
  return {
    outcome: { kind: 'redirect', to },
    loggedFetchFailure: false,
    stateFetches: 1,
  }
}

function fetchFailed(banner: string | null): Seen {
  return {
    outcome: {
      kind: 'resolved',
      value: { appStateBannerMessage: banner, onboardingAuthMode: undefined },
    },
    loggedFetchFailure: true,
    stateFetches: 1,
  }
}

beforeAll(() => {
  const route = Route as unknown as {
    beforeLoad?: () => Promise<GuardValue>
    options?: { beforeLoad?: () => Promise<GuardValue> }
  }
  const fn = route.options?.beforeLoad ?? route.beforeLoad
  if (typeof fn !== 'function') {
    throw new Error(
      'BLOCKED: onboarding Route.beforeLoad is not exposed — required by issue #1858',
    )
  }
  beforeLoad = fn
})

beforeEach(() => {
  localStorage.clear()
  vi.mocked(fetchAppState).mockReset()
  errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
})

afterEach(() => {
  errorSpy.mockRestore()
  localStorage.clear()
})

describe('onboarding beforeLoad real redirect (issue #1858)', () => {
  it("uses the real router redirect: a Response that isRedirect accepts, with no own 'to'", () => {
    const thrown = redirect({ to: LOGIN_PATH })
    expect(thrown).toBeInstanceOf(Response)
    expect(isRedirect(thrown)).toBe(true)
    expect(thrown.options.to).toBe(LOGIN_PATH)
    // The bug hides when tests fake redirect as a plain object that has 'to'.
    expect('to' in thrown).toBe(false)
  })

  describe('platform mode, signed_in false, no stored session', () => {
    it('rejects with a real redirect to /login and does not log a fetch failure (hosted)', async () => {
      vi.mocked(fetchAppState).mockResolvedValue(
        buildState({ mode: 'platform', edition: 'hosted', signedIn: false }),
      )

      await expect(observe()).resolves.toEqual(redirected(LOGIN_PATH))
    })

    it('rejects with a real redirect to /login when edition is core — the gate is mode, not edition', async () => {
      vi.mocked(fetchAppState).mockResolvedValue(
        buildState({ mode: 'platform', edition: 'core', signedIn: false }),
      )

      await expect(observe()).resolves.toEqual(redirected(LOGIN_PATH))
    })
  })

  describe('platform mode, signed_in true, no stored session (hosted SSO)', () => {
    it("resolves onboardingAuthMode 'platform' and does not redirect or log a fetch failure", async () => {
      vi.mocked(fetchAppState).mockResolvedValue(
        buildState({ mode: 'platform', edition: 'hosted', signedIn: true }),
      )

      await expect(observe()).resolves.toEqual(resolved('platform'))
    })
  })

  describe('platform mode, signed_in true, stored session present', () => {
    it("resolves onboardingAuthMode 'platform' and does not redirect", async () => {
      setStoredSession()
      vi.mocked(fetchAppState).mockResolvedValue(
        buildState({ mode: 'platform', edition: 'hosted', signedIn: true }),
      )

      await expect(observe()).resolves.toEqual(resolved('platform'))
    })
  })

  describe('onboarding_complete true', () => {
    it('propagates a real redirect to / and does not log a fetch failure (hosted, signed out)', async () => {
      vi.mocked(fetchAppState).mockResolvedValue(
        buildState({
          mode: 'platform',
          edition: 'hosted',
          signedIn: false,
          onboardingComplete: true,
        }),
      )

      await expect(observe()).resolves.toEqual(redirected(HOME_PATH))
    })

    it('propagates a real redirect to / for local mode as well', async () => {
      vi.mocked(fetchAppState).mockResolvedValue(
        buildState({
          mode: 'local',
          edition: 'core',
          signedIn: false,
          onboardingComplete: true,
        }),
      )

      await expect(observe()).resolves.toEqual(redirected(HOME_PATH))
    })
  })

  describe('local mode and unknown mode are not gated', () => {
    it.each([
      { label: 'signed_in false, no session', signedIn: false, session: false },
      { label: 'signed_in false, stored session', signedIn: false, session: true },
      { label: 'signed_in true, no session', signedIn: true, session: false },
      { label: 'signed_in true, stored session', signedIn: true, session: true },
    ])('resolves local mode with no gate ($label)', async ({ signedIn, session }) => {
      if (session) setStoredSession()
      vi.mocked(fetchAppState).mockResolvedValue(
        buildState({ mode: 'local', edition: 'core', signedIn }),
      )

      await expect(observe()).resolves.toEqual(resolved('local'))
    })

    it('resolves local mode with no gate even when edition is hosted', async () => {
      vi.mocked(fetchAppState).mockResolvedValue(
        buildState({ mode: 'local', edition: 'hosted', signedIn: false }),
      )

      await expect(observe()).resolves.toEqual(resolved('local'))
    })

    it.each([
      { label: 'identity omitted', state: buildState({ omitIdentity: true }), mode: undefined },
      { label: "mode ''", state: buildState({ mode: '' }), mode: '' },
      { label: "mode 'Platform'", state: buildState({ mode: 'Platform', signedIn: false }), mode: 'Platform' },
      { label: "mode 'enterprise'", state: buildState({ mode: 'enterprise', signedIn: false }), mode: 'enterprise' },
    ])('resolves unknown mode with no gate ($label)', async ({ state, mode }) => {
      vi.mocked(fetchAppState).mockResolvedValue(state)

      await expect(observe()).resolves.toEqual(resolved(mode))
    })
  })

  describe('fetch failures keep today\'s banner behaviour', () => {
    it.each([500, 503])(
      'resolves a banner naming the %s status and logs a fetch failure',
      async (status) => {
        vi.mocked(fetchAppState).mockRejectedValue(new ApiError(status))

        await expect(observe()).resolves.toEqual(fetchFailed(serverBanner(status)))
      },
    )

    it('resolves no banner for status 499 (just below 5xx) and still logs the failure', async () => {
      vi.mocked(fetchAppState).mockRejectedValue(new ApiError(499))

      await expect(observe()).resolves.toEqual(fetchFailed(null))
    })

    it('resolves no banner for a network error and still logs the failure', async () => {
      vi.mocked(fetchAppState).mockRejectedValue(new TypeError('Failed to fetch'))

      await expect(observe()).resolves.toEqual(fetchFailed(null))
    })

    it('resolves no banner for a transport ApiError (status 0)', async () => {
      vi.mocked(fetchAppState).mockRejectedValue(new ApiError(0))

      await expect(observe()).resolves.toEqual(fetchFailed(null))
    })
  })

  describe('platform mode accepts only strict boolean signed_in true', () => {
    const nonTrue = [
      { label: 'false', signedIn: false as boolean | null | string | number | undefined, omitSignedIn: false },
      { label: 'missing', signedIn: undefined, omitSignedIn: true },
      { label: 'null', signedIn: null, omitSignedIn: false },
      { label: "string 'true'", signedIn: 'true', omitSignedIn: false },
      { label: 'number 1', signedIn: 1, omitSignedIn: false },
    ]

    it.each(nonTrue)(
      'redirects to /login when signed_in is $label and there is no stored session',
      async ({ signedIn, omitSignedIn }) => {
        vi.mocked(fetchAppState).mockResolvedValue(
          buildState({ mode: 'platform', edition: 'hosted', signedIn, omitSignedIn }),
        )

        await expect(observe()).resolves.toEqual(redirected(LOGIN_PATH))
      },
    )

    it.each(nonTrue)(
      'redirects to /login when signed_in is $label even if a stored session hint is present',
      async ({ signedIn, omitSignedIn }) => {
        setStoredSession()
        vi.mocked(fetchAppState).mockResolvedValue(
          buildState({ mode: 'platform', edition: 'hosted', signedIn, omitSignedIn }),
        )

        await expect(observe()).resolves.toEqual(redirected(LOGIN_PATH))
      },
    )
  })

  describe('a thrown value with to that is not a router redirect', () => {
    it('treats a plain object that has to as a fetch failure, not navigation', async () => {
      vi.mocked(fetchAppState).mockRejectedValue({ to: '/not-a-real-route', message: 'not a redirect' })

      await expect(observe()).resolves.toEqual(fetchFailed(null))
    })

    it('treats an Error that has to as a fetch failure, not navigation', async () => {
      vi.mocked(fetchAppState).mockRejectedValue(
        Object.assign(new Error('upstream blew up'), { to: '/not-a-real-route' }),
      )

      await expect(observe()).resolves.toEqual(fetchFailed(null))
    })

    it('keeps the 5xx banner when an ApiError also has a to property', async () => {
      vi.mocked(fetchAppState).mockRejectedValue(
        Object.assign(new ApiError(503), { to: '/login' }),
      )

      await expect(observe()).resolves.toEqual(fetchFailed(serverBanner(503)))
    })
  })
})
