// ── Shared forced-logout helper ───────────────────────────────────────────────
//
// Single source of truth for the auth teardown sequence. Called by:
//   - queryClient.ts  — when a 401 response is detected on any query/mutation
//   - ws.ts           — when the server closes the WebSocket with code 1008
//                       (policy violation / auth failure)
//
// Behavior:
//   1. Clears all omnipus_auth_* keys from sessionStorage AND localStorage.
//   2. Dynamically imports @/store/auth and calls clearAuth() so the Zustand
//      store reflects the logged-out state without creating a circular import.
//      A .catch() ensures an import failure does not surface as an unhandled
//      rejection — token removal above is sufficient for the hard invariant.
//   3. Redirects to /login synchronously (on the first call within the window).
//   4. Debounces repeated calls: a module-level flag suppresses duplicate
//      teardowns within the same 2-second window, so simultaneous triggers
//      (e.g. a 401 HTTP response + a 1008 WS close arriving at the same tick)
//      run the teardown exactly once. The redirect fires synchronously on the
//      first call; only the flag-reset uses setTimeout.

let _logoutScheduled = false

export function forceLogout(): void {
  if (_logoutScheduled) return
  _logoutScheduled = true

  // Clear auth state from both storages (matches the pattern in src/store/auth.ts).
  sessionStorage.removeItem('omnipus_auth_token')
  localStorage.removeItem('omnipus_auth_token')
  localStorage.removeItem('omnipus_auth_role')
  localStorage.removeItem('omnipus_auth_username')

  // Sync the Zustand auth store if it is already loaded (avoids a circular
  // import by using a dynamic import on the hot path).
  void import('@/store/auth')
    .then(({ useAuthStore }) => {
      useAuthStore.getState().clearAuth()
    })
    .catch(() => {
      // Store not yet loaded — token removal above is sufficient.
    })

  // Reset the flag after 2 seconds so a fresh login can trigger a new logout cycle.
  setTimeout(() => { _logoutScheduled = false }, 2_000)

  // Redirect to login. TanStack Router uses hash-based navigation here.
  // The redirect fires synchronously (first call within the window); only the
  // debounce-flag reset above uses setTimeout.
  if (typeof window !== 'undefined') {
    window.location.hash = '/login'
  }
}
