// Unit tests for src/store/auth.ts — the SPA's auth store post ADR-044 /
// US-5 cookie migration (FR-010).
//
// Auth is the browser-managed omnipus-session HttpOnly cookie (issued by the
// gateway at login/onboarding/register-admin) — this store never reads or
// writes a JS-visible token. It only persists the display-only `username`
// (Sidebar "logged in as X"); the real session lives exclusively in the
// cookie jar.
//
// The store reads localStorage once at module-import time (Zustand's
// `create()` initializer), so each test dynamically re-imports the module
// after seeding/clearing storage — mirrors the pattern used throughout
// api.test.ts / queryClient.auth.test.ts for the same reason.
//
// Traces to: docs/internal/specs/preview-on-main-listener-spec.md
//   — S8 (no JS token after login/onboarding), TDD #25 (src/store/auth.test.ts).

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act } from 'react'

beforeEach(() => {
  vi.resetModules()
  localStorage.clear()
  sessionStorage.clear()
})

afterEach(() => {
  localStorage.clear()
  sessionStorage.clear()
})

describe('auth store — no JS-visible token (US-5 / FR-010, S8)', () => {
  it('never writes omnipus_auth_token to sessionStorage on setUsername', async () => {
    const { useAuthStore } = await import('./auth')
    act(() => { useAuthStore.getState().setUsername('alice') })
    expect(sessionStorage.getItem('omnipus_auth_token')).toBeNull()
  })

  it('never writes omnipus_auth_token to localStorage on setUsername', async () => {
    const { useAuthStore } = await import('./auth')
    act(() => { useAuthStore.getState().setUsername('alice') })
    expect(localStorage.getItem('omnipus_auth_token')).toBeNull()
  })

  it('does not expose a token field on the store state', async () => {
    const { useAuthStore } = await import('./auth')
    act(() => { useAuthStore.getState().setUsername('alice') })
    const state = useAuthStore.getState() as unknown as Record<string, unknown>
    expect(state.token).toBeUndefined()
    expect(state.setToken).toBeUndefined()
  })

  it('leaves omnipus_auth_token untouched (still null) after clearAuth', async () => {
    const { useAuthStore } = await import('./auth')
    act(() => {
      useAuthStore.getState().setUsername('alice')
      useAuthStore.getState().clearAuth()
    })
    expect(sessionStorage.getItem('omnipus_auth_token')).toBeNull()
    expect(localStorage.getItem('omnipus_auth_token')).toBeNull()
  })
})

describe('auth store — username persistence (display-only, not a credential)', () => {
  it('initializes username from localStorage on module load', async () => {
    localStorage.setItem('omnipus_auth_username', 'bob')
    const { useAuthStore } = await import('./auth')
    expect(useAuthStore.getState().username).toBe('bob')
  })

  it('initializes username to null when localStorage is empty', async () => {
    const { useAuthStore } = await import('./auth')
    expect(useAuthStore.getState().username).toBeNull()
  })

  it('setUsername persists to localStorage and updates state', async () => {
    const { useAuthStore } = await import('./auth')
    act(() => { useAuthStore.getState().setUsername('carol') })
    expect(useAuthStore.getState().username).toBe('carol')
    expect(localStorage.getItem('omnipus_auth_username')).toBe('carol')
  })

  it('clearAuth removes username from localStorage and resets state to null', async () => {
    const { useAuthStore } = await import('./auth')
    act(() => { useAuthStore.getState().setUsername('dave') })
    act(() => { useAuthStore.getState().clearAuth() })
    expect(useAuthStore.getState().username).toBeNull()
    expect(localStorage.getItem('omnipus_auth_username')).toBeNull()
  })
})
