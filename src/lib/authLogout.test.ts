// Unit tests for src/lib/authLogout.ts — forceLogout(), the FORCED (server-
// already-rejected) teardown path invoked by queryClient.ts on a 401 and by
// ws.ts on a WS close code 1008.
//
// Post ADR-044 (US-5 / FR-010): auth is the omnipus-session HttpOnly
// cookie — there is no JS-visible token for forceLogout to clear anymore.
// It clears the display-only `username` via the Zustand auth store and
// redirects to /login. The debounce guard (suppress a second teardown
// within 2s) is unchanged behavior and still covered here.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act } from 'react'

beforeEach(() => {
  vi.resetModules()
  localStorage.clear()
  sessionStorage.clear()
  window.location.hash = ''
})

afterEach(() => {
  localStorage.clear()
  sessionStorage.clear()
  window.location.hash = ''
})

describe('forceLogout', () => {
  it('clears the display-only username via the auth store and redirects to /login', async () => {
    const { useAuthStore } = await import('@/store/auth')
    act(() => { useAuthStore.getState().setUsername('alice') })
    expect(useAuthStore.getState().username).toBe('alice')

    const { forceLogout } = await import('./authLogout')
    act(() => { forceLogout() })
    // Let the dynamic import('@/store/auth').then(clearAuth) microtask settle.
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })

    expect(useAuthStore.getState().username).toBeNull()
    expect(localStorage.getItem('omnipus_auth_username')).toBeNull()
    expect(window.location.hash).toBe('#/login')
  })

  it('runs cleanly with no prior session (empty storage) — no JS token to depend on', async () => {
    const { forceLogout } = await import('./authLogout')
    expect(() => { act(() => { forceLogout() }) }).not.toThrow()
    expect(window.location.hash).toBe('#/login')
  })

  it('debounces: a second call within the 2s window does not re-run the teardown', async () => {
    const { useAuthStore } = await import('@/store/auth')
    const { forceLogout } = await import('./authLogout')

    act(() => { useAuthStore.getState().setUsername('alice') })
    act(() => { forceLogout() })
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
    expect(useAuthStore.getState().username).toBeNull()

    // Simulate a fresh login happening after the first logout completes but
    // still within the 2s debounce window, then a second forced-logout
    // trigger (e.g. a duplicate 401 + WS 1008 arriving at the same tick).
    act(() => { useAuthStore.getState().setUsername('bob') })
    act(() => { forceLogout() })
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })

    // If the debounce guard were broken, this second call would have run
    // clearAuth() again and wiped 'bob'.
    expect(useAuthStore.getState().username).toBe('bob')
  })
})
