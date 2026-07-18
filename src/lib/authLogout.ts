// ── Shared forced-logout helper ───────────────────────────────────────────────
//
// Single source of truth for the auth teardown sequence. Called by:
//   - queryClient.ts  — when a 401 response is detected on any query/mutation
//   - ws.ts           — when the server closes the WebSocket with code 1008
//                       (policy violation / auth failure)
//
// This is the FORCED path: the server has already decided the session is
// dead (a 401 means the omnipus-session cookie is missing/invalid; a 1008
// WS close means the handshake auth failed). There is no client-visible
// token to revoke — auth is the HttpOnly omnipus-session cookie — and no
// point round-tripping to POST /api/v1/auth/logout when the server already
// rejected the credential. (Contrast with the user-initiated Sidebar "Sign
// out" action, which DOES call POST /api/v1/auth/logout — see
// src/components/layout/Sidebar.tsx — because there the cookie is still
// valid and must be told to invalidate itself server-side.)
//
// Behavior:
//   1. Dynamically imports @/store/auth and calls clearAuth() so the Zustand
//      store reflects the logged-out state (clears the display-only
//      username) without creating a circular import. A .catch() ensures an
//      import failure does not surface as an unhandled rejection — the
//      redirect below is sufficient for the hard invariant even if this fails.
//   2. Redirects to /login synchronously (on the first call within the window).
//   3. Debounces repeated calls: a module-level flag suppresses duplicate
//      teardowns within the same 2-second window, so simultaneous triggers
//      (e.g. a 401 HTTP response + a 1008 WS close arriving at the same tick)
//      run the teardown exactly once. The redirect fires synchronously on the
//      first call; only the flag-reset uses setTimeout.

let _logoutScheduled = false

export function forceLogout(): void {
  if (_logoutScheduled) return
  _logoutScheduled = true

  // Sync the Zustand auth store if it is already loaded (avoids a circular
  // import by using a dynamic import on the hot path).
  void import('@/store/auth')
    .then(({ useAuthStore }) => {
      useAuthStore.getState().clearAuth()
    })
    .catch(() => {
      // Store not yet loaded — the redirect below still runs regardless.
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
