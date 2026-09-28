// -login.return.test.tsx — side-panel-shell-spec.md §12 #18 (SP-27, SC-004).
//
// RED: login.tsx navigates to `/` after a completed onboarded sign-in
// (login.tsx handleSubmit, the else branch). The spec requires the original
// hash URL — workspace AND panel — to survive the login redirect. A signed-out
// visit to `#/workspaces/ws-9/chat?panel=calendar` must land back there, never
// on `/`.
//
// Oracle: §8.2 "Sign-in return (SP-27)" and US-7's shared-link scenario.
//
// F-B2 (CHECK part B, mutation-proved): a mutant removing the '//'
// protocol-relative rejection in the GREEN-side return-target sanitizer
// (src/routes/-loginReturn.ts::safeAppPath) stayed green — the pack had no
// negative case. SP-27 + security: the return target must be a SAME-ORIGIN
// INTERNAL route; a hostile or self target must land on the safe default,
// never off-origin. The negatives below pin the sanitizer at unit level
// (the flow binding is the happy-path test above: driving the flow with a
// hostile hash cannot distinguish "rejected by the sanitizer" from "return
// not implemented" on a tree where sign-in lands on '/' unconditionally).
// The safe-default value ('/') comes from the CHECK ruling, not the
// implementation — the module does not exist on this tree to read.

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: { component: React.ComponentType }) => opts,
    useNavigate: () => mockNavigate,
  }
})

vi.mock('framer-motion', () => ({
  motion: new Proxy({}, {
    get: (_target: object, prop: string) =>
      React.forwardRef(({ children, ...props }: Record<string, unknown>, ref: unknown) =>
        React.createElement(prop as string, { ...props, ref }, children as React.ReactNode),
      ),
  }),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => children,
}))

const mockLogin = vi.fn()
const mockFetchAppState = vi.fn()
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    login: (...args: unknown[]) => mockLogin(...args),
    fetchAppState: () => mockFetchAppState(),
    isApiError: actual.isApiError,
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/test-avatar.svg' }))
vi.mock('./authValidation', () => ({ resetTokenValidationCache: vi.fn() }))
vi.mock('@/store/auth', () => ({
  useAuthStore: (selector: (s: { setUsername: ReturnType<typeof vi.fn> }) => unknown) =>
    selector({ setUsername: vi.fn() }),
}))

// Top-level await import (not a beforeAll hook): the route-file import graph
// takes >60 s on a loaded machine — longer than vitest's default hookTimeout,
// which killed the import as a false "Hook timed out". Module import time has
// no such timeout. vi.mock calls are hoisted above this, so mocks still apply.
let LoginComponent: React.ComponentType
{
  const mod = await import('./login')
  LoginComponent = (mod.Route as unknown as { component: React.ComponentType }).component
}

describe('sign-in returns to the opened link (§12 #18, SP-27)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockLogin.mockResolvedValue({ token: 'tok', role: 'admin', username: 'dana' })
    mockFetchAppState.mockResolvedValue({ onboarding_complete: true })
    window.location.hash = '#/workspaces/ws-9/chat?panel=calendar'
  })

  it('RED — an onboarded sign-in lands on the linked workspace + panel, not /', async () => {
    if (!LoginComponent) throw new Error('LoginComponent not loaded')
    render(<LoginComponent />)
    fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'dana' } })
    fireEvent.change(document.getElementById('login-password')!, { target: { value: 'secret' } })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => expect(mockNavigate).toHaveBeenCalled())
    const dest = JSON.stringify(mockNavigate.mock.calls)
    expect(dest).toContain('ws-9')
    expect(dest).toContain('calendar')
    expect(mockNavigate).not.toHaveBeenCalledWith({ to: '/' })
  })
})

// --- F-B2: hostile return targets (SP-27 + security) ---
// The sanitizer module is GREEN-side; the variable-specifier dynamic import
// keeps this file compiling on both trees (a literal import of a missing
// module is a typecheck error). Absent here → every case fails BLOCKED,
// naming the missing unit — never an unexplained error.

const LOGIN_RETURN_MODULE = './-loginReturn'
let safeAppPathFn: ((raw: string) => string) | null = null
try {
  const mod = (await import(LOGIN_RETURN_MODULE)) as Record<string, unknown>
  if (typeof mod.safeAppPath === 'function') {
    safeAppPathFn = mod.safeAppPath as (raw: string) => string
  }
} catch {
  // absent on the pre-GREEN tree — the gate below states it
}

function requireSafeAppPath(): (raw: string) => string {
  if (!safeAppPathFn) {
    throw new Error(
      'BLOCKED: the SP-27 return-target sanitizer (src/routes/-loginReturn.ts::safeAppPath — the GREEN-side module named by CHECK part B finding F-B2) is not implemented on this tree — ' +
        'required by SP-27 + security: the sign-in return target must be a SAME-ORIGIN INTERNAL route; hostile targets land on the safe default, never off-origin',
    )
  }
  return safeAppPathFn
}

describe('F-B2 — return-target sanitizer rejects hostile and self targets (SP-27 + security)', () => {
  // The safe default is the app root. Property guard alongside the exact
  // value: the result must carry NONE of the hostile markers, whichever
  // form the rejection takes.
  const HOSTILE_MARKERS = [/evil\.example/, /javascript:/, /%2F%2F/i, /^\/\//, /^\\/]

  function assertInternalDefault(safe: string) {
    expect(safe).toBe('/')
    for (const marker of HOSTILE_MARKERS) expect(marker.test(safe)).toBe(false)
  }

  it('RED/BLOCKED — a protocol-relative "//evil.example" never leaves the origin (F-B2 mutant case)', () => {
    const safe = requireSafeAppPath()
    assertInternalDefault(safe('//evil.example'))
  })

  it('RED/BLOCKED — a backslash variant "/\\evil.example" never leaves the origin', () => {
    const safe = requireSafeAppPath()
    assertInternalDefault(safe('/\\evil.example'))
  })

  it('RED/BLOCKED — an absolute external "https://evil.example" never leaves the origin', () => {
    const safe = requireSafeAppPath()
    assertInternalDefault(safe('https://evil.example'))
  })

  it('RED/BLOCKED — a script scheme "javascript:alert(1)" never reaches the router', () => {
    const safe = requireSafeAppPath()
    assertInternalDefault(safe('javascript:alert(1)'))
  })

  it('RED/BLOCKED — an encoded protocol-relative "%2F%2Fevil.example" never leaves the origin', () => {
    const safe = requireSafeAppPath()
    assertInternalDefault(safe('%2F%2Fevil.example'))
  })

  it('RED/BLOCKED — the login page itself as target cannot self-loop ("/login")', () => {
    const safe = requireSafeAppPath()
    assertInternalDefault(safe('/login'))
  })
})

// --- N1 (gate round 2): the FLOW must use the sanitizer ---
// The F-B2 block pins the sanitizer UNIT; pr-test-analyzer's N1 asks for the
// FLOW binding: sign-in from a hostile hash through the real login form.
// The hostile hash is the live input (sessionStorage is empty — cleared in
// the block's beforeEach), so GREEN's consumeLoginReturn() falls through to
// the validated hash fallback and a mutant bypassing the validation in the
// fallback navigates to the hostile target directly.
//
// STATUS: on this pre-GREEN tree sign-in lands on '/' unconditionally, so
// these cases pass here for the WRONG reason (vacuous green — documented);
// failability is proven at the GREEN head by one production mutation
// (currentHashPath returning the raw hash), reverted. At GREEN they hold
// for the right reason: the sanitizer rejected the hash.
describe('N1 — sign-in from a hostile hash lands on / through the real flow (SP-27 + security)', () => {
  beforeEach(() => {
    window.sessionStorage.removeItem('omnipus_login_return')
  })

  async function signInFromHash(hash: string): Promise<void> {
    window.location.hash = hash
    if (!LoginComponent) throw new Error('LoginComponent not loaded')
    render(<LoginComponent />)
    fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'dana' } })
    fireEvent.change(document.getElementById('login-password')!, { target: { value: 'secret' } })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))
    await waitFor(() => expect(mockNavigate).toHaveBeenCalled())
  }

  it('a protocol-relative hostile hash ("#//evil.example") lands on /, never off-origin', async () => {
    await signInFromHash('#//evil.example')
    expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
    expect(JSON.stringify(mockNavigate.mock.calls)).not.toContain('evil.example')
  })

  it('a backslash hostile hash ("#/\\evil.example") lands on /, never off-origin', async () => {
    await signInFromHash('#/\\evil.example')
    expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
    expect(JSON.stringify(mockNavigate.mock.calls)).not.toContain('evil.example')
  })
})
