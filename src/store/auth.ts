import { create } from 'zustand'

interface AuthStore {
  token: string | null
  username: string | null
  setToken: (token: string, username: string) => void
  clearAuth: () => void
}

// Retrieves auth state from storage.
// Token prefers sessionStorage (XSS protection); falls back to localStorage.
// username remains in localStorage (less sensitive).
function getStoredAuth() {
  return {
    token: sessionStorage.getItem('omnipus_auth_token') ?? localStorage.getItem('omnipus_auth_token'),
    username: localStorage.getItem('omnipus_auth_username'),
  }
}

export const useAuthStore = create<AuthStore>((set) => ({
  ...getStoredAuth(),
  setToken: (token, username) => {
    sessionStorage.setItem('omnipus_auth_token', token) // sessionStorage for token (XSS protection)
    localStorage.setItem('omnipus_auth_username', username)
    set({ token, username })
  },
  clearAuth: () => {
    sessionStorage.removeItem('omnipus_auth_token')
    localStorage.removeItem('omnipus_auth_username')
    set({ token: null, username: null })
  },
}))
