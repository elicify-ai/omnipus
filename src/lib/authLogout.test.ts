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

// ---------------------------------------------------------------------------
// D2 — logout messaging: forceLogout() stashes a LogoutReason so the login
// screen can explain WHY an involuntary logout happened. consumeLogoutReason()
// is the one-shot read+clear the login screen uses.
// ---------------------------------------------------------------------------
describe('D2 — LogoutReason stash/consume', () => {
  it('forceLogout() with no argument stashes the "elsewhere" default (queryClient.ts / ws.ts call it bare)', async () => {
    const { forceLogout, LOGOUT_REASON_KEY } = await import('./authLogout')
    act(() => { forceLogout() })
    expect(sessionStorage.getItem(LOGOUT_REASON_KEY)).toBe('elsewhere')
  })

  it('forceLogout("expired") stashes "expired" (the _app.tsx beforeLoad call site)', async () => {
    const { forceLogout, LOGOUT_REASON_KEY } = await import('./authLogout')
    act(() => { forceLogout('expired') })
    expect(sessionStorage.getItem(LOGOUT_REASON_KEY)).toBe('expired')
  })

  it('consumeLogoutReason() reads the stashed reason and clears it (one-shot)', async () => {
    const { forceLogout, consumeLogoutReason, LOGOUT_REASON_KEY } = await import('./authLogout')
    act(() => { forceLogout('expired') })
    expect(sessionStorage.getItem(LOGOUT_REASON_KEY)).toBe('expired')

    expect(consumeLogoutReason()).toBe('expired')
    // Cleared — a second read (e.g. React StrictMode's double-invoked effect)
    // must not see it again.
    expect(sessionStorage.getItem(LOGOUT_REASON_KEY)).toBeNull()
    expect(consumeLogoutReason()).toBeNull()
  })

  it('consumeLogoutReason() returns null on a normal page load — no forced logout happened', async () => {
    const { consumeLogoutReason } = await import('./authLogout')
    expect(consumeLogoutReason()).toBeNull()
  })

  it('consumeLogoutReason() returns null and clears an unrecognized stored value (defensive decode)', async () => {
    const { consumeLogoutReason, LOGOUT_REASON_KEY } = await import('./authLogout')
    sessionStorage.setItem(LOGOUT_REASON_KEY, 'garbage-value')
    expect(consumeLogoutReason()).toBeNull()
    expect(sessionStorage.getItem(LOGOUT_REASON_KEY)).toBeNull()
  })

  it('a manual sign-out (clearAuth() called directly, mirroring Sidebar.tsx) never stashes a reason', async () => {
    // Sidebar.tsx's handleLogout calls useAuthStore.getState().clearAuth() and
    // navigate() directly — it NEVER calls forceLogout(). Simulate that here:
    // no LogoutReason should appear, so the login screen shows no "scary"
    // involuntary-logout banner for a deliberate sign-out (D2 fix #3).
    const { useAuthStore } = await import('@/store/auth')
    const { consumeLogoutReason } = await import('./authLogout')
    act(() => { useAuthStore.getState().setUsername('alice') })
    act(() => { useAuthStore.getState().clearAuth() })
    expect(consumeLogoutReason()).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// D9 — deterministic race flag: isForceLoggingOut() lets a component suppress
// its own error paint for the beat before the redirect actually swaps routes.
// ---------------------------------------------------------------------------
describe('D9 — isForceLoggingOut() race flag', () => {
  it('is false before any forced logout', async () => {
    const { isForceLoggingOut } = await import('./authLogout')
    expect(isForceLoggingOut()).toBe(false)
  })

  it('flips true synchronously the instant forceLogout() is called', async () => {
    const { forceLogout, isForceLoggingOut } = await import('./authLogout')
    expect(isForceLoggingOut()).toBe(false)
    act(() => { forceLogout() })
    // No await — must be true on the SAME tick forceLogout() returns, since
    // a racing component's synchronous render pass needs to observe it.
    expect(isForceLoggingOut()).toBe(true)
  })

  it('resets to false after the 2s debounce window closes', async () => {
    vi.useFakeTimers()
    try {
      const { forceLogout, isForceLoggingOut } = await import('./authLogout')
      act(() => { forceLogout() })
      expect(isForceLoggingOut()).toBe(true)
      act(() => { vi.advanceTimersByTime(2_000) })
      expect(isForceLoggingOut()).toBe(false)
    } finally {
      vi.useRealTimers()
    }
  })
})

// ---------------------------------------------------------------------------
// Landing-race bugfix: forceLogout() now also drops every cached
// query/mutation (queryClient.clear(), via the SAME dynamic-import pattern
// already used for @/store/auth, for the same circular-import reason —
// queryClient.ts imports forceLogout from this module). Without this, a
// subsequent login (same tab, possibly a different user) could silently
// reuse this session's cached data (e.g. the workspaces list — see
// DefaultWorkspaceRedirect.tsx / Sidebar.tsx's shared workspacesQueryKeys
// entry) for as long as its staleTime window allowed.
// ---------------------------------------------------------------------------
describe('forceLogout — clears the query cache (landing-race bugfix)', () => {
  it('drops every cached query so a subsequent login cannot reuse stale cross-session data', async () => {
    const { queryClient } = await import('@/lib/queryClient')
    queryClient.setQueryData(['test-cache-key'], 'stale-value')
    expect(queryClient.getQueryData(['test-cache-key'])).toBe('stale-value')

    const { forceLogout } = await import('./authLogout')
    act(() => { forceLogout() })
    // Let the dynamic import('@/lib/queryClient').then(clear) microtask settle.
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })

    expect(queryClient.getQueryData(['test-cache-key'])).toBeUndefined()
  })

  it('runs cleanly with an empty cache — nothing to clear', async () => {
    const { queryClient } = await import('@/lib/queryClient')
    expect(queryClient.getQueryCache().getAll()).toHaveLength(0)

    const { forceLogout } = await import('./authLogout')
    expect(() => { act(() => { forceLogout() }) }).not.toThrow()
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
  })
})
