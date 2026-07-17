import { create } from 'zustand'

// Auth is browser-managed via the `omnipus-session` HttpOnly cookie (US-5 /
// FR-010) — the SPA never reads or writes a JS-visible auth token. This store
// only tracks the display-only `username` (e.g. the Sidebar profile row's
// "logged in as X"); the real session lives exclusively in the browser's
// cookie jar and is validated server-side on every request (credentials:
// 'include' in src/lib/api.ts) and, on route transitions, via
// src/routes/authValidation.ts + src/routes/_app.tsx.
//
// username stays in localStorage (not sessionStorage) — it is not a secret,
// and persisting it cross-tab/cross-restart lets the UI show "logged in as X"
// immediately on load without waiting on a round trip. If the cookie is
// actually missing/expired, the first authenticated request 401s and the
// existing forceLogout() path (src/lib/authLogout.ts) clears this too.
interface AuthStore {
  username: string | null
  setUsername: (username: string) => void
  clearAuth: () => void
}

function getStoredUsername(): string | null {
  return localStorage.getItem('omnipus_auth_username')
}

export const useAuthStore = create<AuthStore>((set) => ({
  username: getStoredUsername(),
  setUsername: (username) => {
    localStorage.setItem('omnipus_auth_username', username)
    set({ username })
  },
  clearAuth: () => {
    localStorage.removeItem('omnipus_auth_username')
    set({ username: null })
  },
}))
