// ADR-031 login cleanup tests — US-11 / FR-017
//
// #25: login screen renders NO "Set up Omnipus for the first time" button and no Rocket icon.
// #26: not-onboarded admin who logs in is still redirected to /onboarding (retained post-login redirect).
// #27: _app.tsx beforeLoad redirects a not-onboarded user to /onboarding before the auth check.

import React from 'react'
import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

// --- Router mock ---
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: (_path: string) => (opts: { component: React.ComponentType }) => opts,
    useNavigate: () => mockNavigate,
    redirect: (opts: unknown) => {
      // Mimic TanStack Router's redirect throwing behaviour so callers can catch it.
      const err = Object.assign(new Error('redirect'), { isRedirect: true, ...(opts as object) })
      return err
    },
  }
})

// --- Framer Motion mock — strip animations so no async DOM leftovers ---
vi.mock('framer-motion', () => {
  return {
    motion: new Proxy(
      {},
      {
        get: (_target: object, prop: string) => {
          return React.forwardRef(
            ({ children, ...props }: Record<string, unknown>, ref: unknown) =>
              React.createElement(prop as string, { ...props, ref }, children as React.ReactNode)
          )
        },
      }
    ),
    AnimatePresence: ({ children }: { children: React.ReactNode }) => children,
  }
})

// --- API mock ---
const mockLogin = vi.fn()
const mockFetchAppState = vi.fn()
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    login: (...args: unknown[]) => mockLogin(...args),
    fetchAppState: () => mockFetchAppState(),
    validateToken: vi.fn().mockResolvedValue({ valid: true }),
    isApiError: actual.isApiError,
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/test-avatar.svg' }))

// --- authValidation mock (no-op reset) ---
vi.mock('./authValidation', () => ({
  resetTokenValidationCache: vi.fn(),
  checkTokenValidity: vi.fn().mockResolvedValue('ok'),
}))

// --- auth store mock ---
vi.mock('@/store/auth', () => ({
  useAuthStore: (selector: (s: { setUsername: ReturnType<typeof vi.fn> }) => unknown) =>
    selector({ setUsername: vi.fn() }),
}))

// Load the login component once for all tests (avoids repeated transform cost).
let LoginComponent: React.ComponentType | null = null
beforeAll(async () => {
  const mod = await import('./login')
  LoginComponent = ((mod.Route as unknown) as { component: React.ComponentType }).component
})

function renderLogin() {
  if (!LoginComponent) throw new Error('LoginComponent not loaded')
  return render(<LoginComponent />)
}

// ---------------------------------------------------------------------------
// #25 — No re-onboard button or Rocket icon on the login screen
// ---------------------------------------------------------------------------
describe('#25 — login screen has no re-onboard button', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('does not render "Set up Omnipus for the first time" button', () => {
    renderLogin()
    expect(
      screen.queryByText(/set up omnipus for the first time/i)
    ).toBeNull()
  })

  it('does not render any element with role=button containing "set up" text', () => {
    renderLogin()
    const buttons = screen.queryAllByRole('button')
    const reOnboardButton = buttons.find((btn) =>
      /set up/i.test(btn.textContent ?? '')
    )
    expect(reOnboardButton).toBeUndefined()
  })

  it('renders the Sign in submit button', () => {
    renderLogin()
    expect(screen.getByRole('button', { name: /sign in/i })).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// #26 — Post-login redirect to /onboarding is retained
// ---------------------------------------------------------------------------
describe('#26 — not-onboarded admin is redirected to /onboarding after login', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockLogin.mockResolvedValue({ token: 'tok-1', role: 'admin', username: 'admin' })
    mockFetchAppState.mockResolvedValue({ onboarding_complete: false })
  })

  it('navigates to /onboarding when onboarding_complete is false', async () => {
    renderLogin()

    fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'admin' } })
    // Use id-based selector to avoid ambiguity with the "Show password" aria-label.
    fireEvent.change(document.getElementById('login-password')!, { target: { value: 'secret' } })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => {
      expect(mockFetchAppState).toHaveBeenCalledOnce()
      expect(mockNavigate).toHaveBeenCalledWith({ to: '/onboarding' })
    })
  })

  it('navigates to / (home) when onboarding_complete is true', async () => {
    mockFetchAppState.mockResolvedValue({ onboarding_complete: true })
    renderLogin()

    fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'admin' } })
    // Use id-based selector to avoid ambiguity with the "Show password" aria-label.
    fireEvent.change(document.getElementById('login-password')!, { target: { value: 'secret' } })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
    })
  })
})

// ---------------------------------------------------------------------------
// #27 — _app.tsx beforeLoad redirects not-onboarded user to /onboarding
//
// _app.tsx's beforeLoad is an async function that (a) fetches app state and
// (b) if onboarding is incomplete, throws a redirect object.  We test the
// seam by calling the beforeLoad function directly with fetchAppState mocked.
// ---------------------------------------------------------------------------
describe('#27 — _app beforeLoad redirects to /onboarding before auth check', () => {
  it('throws a redirect to /onboarding when onboarding_complete is false', async () => {
    // Mock fetchAppState at the module level before importing _app.
    mockFetchAppState.mockResolvedValue({ onboarding_complete: false })

    // Dynamically import so the mock is already in place.
    const appMod = await import('./_app')
    const routeOptions = (appMod.Route as unknown) as {
      options?: { beforeLoad?: () => Promise<void> }
      beforeLoad?: () => Promise<void>
    }

    // TanStack Router embeds beforeLoad on the route config object.
    // Depending on the version the property may live at different depths.
    const beforeLoad =
      routeOptions.beforeLoad ??
      routeOptions.options?.beforeLoad

    // [I6] HARDENED: `beforeLoad` MUST be resolvable.  A quiet console.warn +
    // early-return would turn this into a false-green if the seam moves (e.g.
    // TanStack Router changes how beforeLoad is exposed).  The test must fail
    // loudly if we can no longer reach the guard, because the onboarding-before-
    // auth safety property is CRITICAL to the security model.
    //
    // Traces to: connectors-providers-redesign-spec.md §7 I6 gap / MIN-002 / R2-06.
    expect(
      typeof beforeLoad,
      '[BLOCKED] _app.tsx beforeLoad not found on Route — the seam has moved. ' +
        'Find the new location of beforeLoad in _app.tsx and update this test. ' +
        'Do NOT revert to a console.warn escape hatch: the onboarding-before-auth ' +
        'guard (MIN-002) must always be exercised by this test.'
    ).toBe('function')

    // TypeScript non-null assertion: the expect().toBe('function') above ensures
    // beforeLoad is a function at runtime; we reassert here for the type-checker.
    if (typeof beforeLoad !== 'function') {
      throw new Error('[BLOCKED] beforeLoad is not a function — test gate failed above should have caught this')
    }

    // Auth is cookie-based now (no JS-visible token) — checkTokenValidity is
    // mocked to resolve 'ok' unconditionally, so it too would let the call
    // through. The onboarding branch MUST still fire FIRST regardless:
    // because fetchAppState resolves with onboarding_complete:false, the call
    // should throw (or return a redirect) before ever reaching the auth check.
    let thrown: unknown = null
    try {
      await beforeLoad()
    } catch (err) {
      thrown = err
    }

    // The thrown value should be the redirect sentinel produced by TanStack Router.
    expect(thrown).not.toBeNull()
    // Our mock redirect returns an object with `to` set.
    expect((thrown as { to: string }).to).toBe('/onboarding')
  })
})
