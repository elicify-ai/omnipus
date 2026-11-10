import React from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { fetchAppState, login, navigate } = vi.hoisted(() => ({
  fetchAppState: vi.fn(),
  login: vi.fn(),
  navigate: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (options: Record<string, unknown>) => options,
    useNavigate: () => navigate,
  }
})

vi.mock('framer-motion', () => ({
  motion: new Proxy({}, {
    get: (_target: object, element: string) =>
      React.forwardRef(({ children, ...props }: Record<string, unknown>, ref: unknown) =>
        React.createElement(element, { ...props, ref }, children as React.ReactNode),
      ),
  }),
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAppState,
  login,
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/test-avatar.svg' }))
vi.mock('./authValidation', () => ({
  checkTokenValidity: vi.fn(),
  resetTokenValidationCache: vi.fn(),
}))
vi.mock('@/store/auth', () => ({
  hasStoredSession: vi.fn(() => true),
  useAuthStore: (selector: (state: { setUsername: ReturnType<typeof vi.fn> }) => unknown) =>
    selector({ setUsername: vi.fn() }),
}))

import { authenticatedBeforeLoad } from './-authenticatedBeforeLoad'
import { Route as FullScreenLayoutRoute } from './_fullscreen'
import { Route as LoginRoute } from './login'

type TestRoute = {
  beforeLoad?: typeof authenticatedBeforeLoad
  component: React.ComponentType
}

describe('SP-27 full-screen panel sign-in return', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    window.sessionStorage.clear()
    fetchAppState
      .mockResolvedValueOnce({ onboarding_complete: true, identity: { signed_in: false } })
      .mockResolvedValueOnce({ onboarding_complete: true, identity: { signed_in: true } })
    login.mockResolvedValue({ token: 'token', role: 'admin', username: 'dana' })
  })

  it('uses the shared guard, signs in, and returns to the same full-screen panel', async () => {
    const layout = FullScreenLayoutRoute as unknown as TestRoute
    expect(layout.beforeLoad).toBe(authenticatedBeforeLoad)
    const target = '/panel/library?workspace=workspace-a&path=Notes%2FCurrent.md&popout=popout-a'

    await expect(layout.beforeLoad?.({ location: { href: target } })).rejects.toBeDefined()

    const Login = (LoginRoute as unknown as TestRoute).component
    render(<Login />)
    fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'dana' } })
    fireEvent.change(document.getElementById('login-password')!, { target: { value: 'secret' } })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: target }))
  })
})
