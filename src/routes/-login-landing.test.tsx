// Integration coverage: login.tsx's post-login cache invalidation vs.
// DefaultWorkspaceRedirect's "/" landing (components/workspaces/DefaultWorkspaceRedirect.tsx).
//
// Landing-race bugfix: login.tsx's handleSubmit navigates to "/" unconditionally;
// "/" mounts DefaultWorkspaceRedirect, which performs a SECOND, independent,
// effect-driven navigation once its own workspaces query resolves. That query's
// key (workspacesQueryKeys.list({status:'active'})) is SHARED with
// Sidebar.tsx's 30s poll. Before this fix, a successful login invalidated only
// the ['commands'] cache (see the sibling suite in -login.test.tsx); the
// workspaces cache was never told the session had changed.
//
// Why this needs a REAL, PERSISTENT QueryClient across MULTIPLE sign-ins
// (not a fresh QueryClient per test, as -DefaultWorkspaceRedirect.test.tsx
// uses): a single sign-in-and-land run starts from an EMPTY cache, fetches
// once, and lands correctly regardless of whether invalidation happens — a
// single run cannot distinguish "explicitly invalidated" from "cache
// happened to be empty". Only a LATER sign-in that reuses an
// already-populated, still-fresh (staleTime:30_000) cache entry can tell the
// two apart: without invalidation, TanStack Query v5 serves the OLD
// sign-in's cached data outright (no network call at all — see
// shouldFetchOnMount/isStale in query-core) because it is neither stale by
// time nor marked invalidated. Hence ten consecutive iterations sharing one
// queryClient, exactly like one browser tab across ten sign-in cycles.

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'

// --- Router mock — shared useNavigate() spy for BOTH login.tsx and
// DefaultWorkspaceRedirect (both call useNavigate() from this module) ---
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: (_path: string) => (opts: { component: React.ComponentType }) => opts,
    useNavigate: () => mockNavigate,
    redirect: (opts: unknown) => Object.assign(new Error('redirect'), { isRedirect: true, ...(opts as object) }),
  }
})

// --- Framer Motion mock — strip animations so no async DOM leftovers ---
vi.mock('framer-motion', () => ({
  motion: new Proxy(
    {},
    {
      get: (_target: object, prop: string) =>
        React.forwardRef(({ children, ...props }: Record<string, unknown>, ref: unknown) =>
          React.createElement(prop as string, { ...props, ref }, children as React.ReactNode)),
    },
  ),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => children,
}))

// --- API mock — covers both login.tsx's and DefaultWorkspaceRedirect's imports ---
const mockLogin = vi.fn()
const mockFetchAppState = vi.fn()
const mockFetchWorkspaces = vi.fn()
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    login: (...args: unknown[]) => mockLogin(...args),
    fetchAppState: () => mockFetchAppState(),
    fetchWorkspaces: (...args: unknown[]) => mockFetchWorkspaces(...args),
    validateToken: vi.fn().mockResolvedValue({ valid: true }),
    isApiError: actual.isApiError,
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/test-avatar.svg' }))

vi.mock('./authValidation', () => ({
  resetTokenValidationCache: vi.fn(),
  checkTokenValidity: vi.fn().mockResolvedValue('ok'),
}))

vi.mock('@/store/auth', () => ({
  useAuthStore: (selector: (s: { setUsername: ReturnType<typeof vi.fn> }) => unknown) =>
    selector({ setUsername: vi.fn() }),
}))

let LoginComponent: React.ComponentType | null = null
let WorkspaceRedirect: React.ComponentType<{ tab?: 'chat' | 'board' | 'calendar' }> | null = null

beforeAll(async () => {
  const loginMod = await import('./login')
  LoginComponent = (loginMod.Route as unknown as { component: React.ComponentType }).component
  const redirectMod = await import('@/components/workspaces/DefaultWorkspaceRedirect')
  WorkspaceRedirect = redirectMod.DefaultWorkspaceRedirect
})

function signIn() {
  if (!LoginComponent) throw new Error('LoginComponent not loaded')
  const utils = render(<LoginComponent />)
  fireEvent.change(screen.getByLabelText(/username/i), { target: { value: 'admin' } })
  fireEvent.change(document.getElementById('login-password')!, { target: { value: 'secret' } })
  fireEvent.click(screen.getByRole('button', { name: /sign in/i }))
  return utils
}

function landOnRoot() {
  if (!WorkspaceRedirect) throw new Error('DefaultWorkspaceRedirect not loaded')
  return render(
    <QueryClientProvider client={queryClient}>
      <WorkspaceRedirect />
    </QueryClientProvider>,
  )
}

describe('Bugfix: repeated sign-ins land on the CURRENT default workspace (landing race)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockFetchAppState.mockResolvedValue({ onboarding_complete: true })
    mockLogin.mockResolvedValue({ token: 'tok', role: 'admin', username: 'admin' })
    // Isolate this test from any other suite that shares the real singleton.
    queryClient.clear()
  })

  afterEach(() => {
    cleanup()
    queryClient.clear()
  })

  it('ten consecutive sign-ins each redirect to THAT sign-in\'s default workspace, not a stale cached one', async () => {
    for (let i = 1; i <= 10; i++) {
      const workspaceId = `ws-${i}`
      mockFetchWorkspaces.mockResolvedValue([
        {
          id: workspaceId,
          name: `Workspace ${i}`,
          is_default: true,
          status: 'active',
          pinned: false,
          pin_order: 0,
          task_count: 0,
        },
      ])

      const { unmount: unmountLogin } = signIn()
      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
      })
      unmountLogin()
      mockNavigate.mockClear()

      const { unmount: unmountRedirect } = landOnRoot()
      await waitFor(
        () => {
          expect(mockNavigate).toHaveBeenCalledWith({
            to: '/workspaces/$workspaceId/chat',
            params: { workspaceId },
            replace: true,
          })
        },
        // A generous per-iteration timeout — no real timers/retries are
        // exercised in the passing case, this just guards against a hang if
        // the bug regresses (stuck on the error/spinner state indefinitely).
        { timeout: 3000 },
      )
      unmountRedirect()
      mockNavigate.mockClear()
    }
  })
})
