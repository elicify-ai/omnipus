// -login.return.test.tsx — side-panel-shell-spec.md §12 #18 (SP-27, SC-004).
//
// RED: login.tsx navigates to `/` after a completed onboarded sign-in
// (login.tsx handleSubmit, the else branch). The spec requires the original
// hash URL — workspace AND panel — to survive the login redirect. A signed-out
// visit to `#/workspaces/ws-9/chat?panel=calendar` must land back there, never
// on `/`.
//
// Oracle: §8.2 "Sign-in return (SP-27)" and US-7's shared-link scenario.

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
