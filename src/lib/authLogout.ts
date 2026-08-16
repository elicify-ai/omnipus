// ── Shared forced-logout helper ───────────────────────────────────────────────
//
// Single source of truth for the auth teardown sequence. Called by:
//   - queryClient.ts  — when a 401 response is detected on any query/mutation
//   - ws.ts           — when the server closes the WebSocket with code 1008
//                       (policy violation / auth failure)
//   - routes/_app.tsx — when the beforeLoad session-validity guard gets a
//                       confirmed 401 (D2 fix — this path used to redirect
//                       WITHOUT clearing the auth store, leaving a stale
//                       "logged in as X" in the Sidebar after the bounce)
//   - components/agents/ToolApprovalModal.tsx — when a tool-approval POST
//                       401s (D9 fix — this call bypasses React Query, so the
//                       queryClient.ts subscriber never saw it before)
//
// This is the FORCED path: the server has already decided the session is
// dead (a 401 means the omnipus-session cookie is missing/invalid; a 1008
// WS close means the handshake auth failed). There is no client-visible
// token to revoke — auth is the HttpOnly omnipus-session cookie — and no
// point round-tripping to POST /api/v1/auth/logout when the server already
// rejected the credential. (Contrast with the user-initiated Sidebar "Sign
// out" action, which DOES call POST /api/v1/auth/logout — see
// src/components/layout/Sidebar.tsx — because there the cookie is still
// valid and must be told to invalidate itself server-side. Sidebar's
// handleLogout calls clearAuth()/navigate() directly, NEVER forceLogout(),
// so a manual sign-out never stashes a LogoutReason and never shows the
// involuntary-logout banner below — see D2 fix #3.)
//
// D2 fix — "the app just vanishes with no explanation": every forced-logout
// call site above ends the same way (hard redirect, local state gone,
// half-typed composer draft lost) but historically gave the user ZERO
// messaging about why. `reason` is stashed in sessionStorage (survives the
// hash redirect; does NOT leak to a fresh tab, matching a one-shot
// explanation rather than a persistent banner) and read+cleared exactly once
// by the login screen (consumeLogoutReason(), routes/login.tsx).
//
// The two reasons map to the two distinct trigger shapes, not a server-sent
// signal (the server gives no machine-readable distinction — see the
// UAT defect write-up): a query/mutation/WS failure that happens WHILE the
// user is actively using an already-loaded screen ('elsewhere' — the DEFAULT,
// and the most likely cause, since bearer tokens are multi-device but the
// session cookie is single-slot: `pkg/gateway/rest_auth.go` overwrites it on
// every login, so the most common way an in-use session goes bad mid-turn is
// a fresh login elsewhere) vs. the proactive /_app beforeLoad validity check
// finding the session already gone BEFORE a screen ever mounted ('expired' —
// a stale credential discovered on arrival reads more naturally as "this had
// already timed out"; routes/_app.tsx passes this explicitly). queryClient.ts
// and ws.ts call forceLogout() with no argument and ride the 'elsewhere'
// default — both are mid-session failures of an already-mounted screen, the
// same shape the default is tuned for.
export type LogoutReason = 'elsewhere' | 'expired'

// sessionStorage key the login screen reads (and clears) exactly once. Only
// ever written by forceLogout() — the user-initiated Sidebar "Sign out" flow
// never touches it, so a manual sign-out never shows this messaging (D2 fix #3).
export const LOGOUT_REASON_KEY = 'omnipus_logout_reason'

// User-facing copy for each LogoutReason, single-sourced here so any future
// caller (toast, banner, login screen) renders identical wording.
export const LOGOUT_REASON_MESSAGE: Record<LogoutReason, string> = {
  elsewhere: "You've been signed out — this session ended, possibly because you signed in elsewhere.",
  expired: 'Your session expired. Please sign in again.',
}

let _logoutScheduled = false
// D9 fix — the deterministic-race flag: set synchronously the instant a
// forced logout begins, so any component whose render pass observes an
// error (e.g. the SAME 401 that triggered this) can check isForceLoggingOut()
// and skip painting its own actionable "Retry" UI for the single beat before
// the hash redirect actually swaps the route out. See QueryErrorState.tsx.
let _redirecting = false

/** True while a forced logout is in flight (between forceLogout() firing and
 *  its 2s debounce window closing). Read synchronously — NOT reactive/hooked
 *  — callers should re-check on every render rather than caching the value. */
export function isForceLoggingOut(): boolean {
  return _redirecting
}

export function forceLogout(reason: LogoutReason = 'elsewhere'): void {
  if (_logoutScheduled) return
  _logoutScheduled = true
  _redirecting = true

  if (typeof window !== 'undefined') {
    try {
      window.sessionStorage.setItem(LOGOUT_REASON_KEY, reason)
    } catch {
      // sessionStorage unavailable (private mode / storage disabled) — the
      // redirect below still fires; the login screen just won't have a
      // reason to display.
    }
  }

  // Sync the Zustand auth store if it is already loaded (avoids a circular
  // import by using a dynamic import on the hot path).
  void import('@/store/auth')
    .then(({ useAuthStore }) => {
      useAuthStore.getState().clearAuth()
    })
    .catch(() => {
      // Store not yet loaded — the redirect below still runs regardless.
    })

  // Drop every cached query/mutation along with the session. This session's
  // data (workspaces, sessions, agents, ...) belongs to a specific user;
  // leaving it cached risks a subsequent login (same tab, a different user,
  // or the same user with a changed workspace list) silently reusing STALE
  // cross-session data for as long as its staleTime window allows — see
  // DefaultWorkspaceRedirect.tsx / Sidebar.tsx's shared workspacesQueryKeys
  // entry. Dynamic import avoids a circular import: queryClient.ts imports
  // forceLogout from THIS module for its own 401 handler, so a static import
  // here would cycle back.
  void import('@/lib/queryClient')
    .then(({ queryClient }) => {
      queryClient.clear()
    })
    .catch(() => {
      // queryClient not yet loaded — nothing cached yet to worry about.
    })

  // Reset the flags after 2 seconds so a fresh login can trigger a new logout cycle.
  setTimeout(() => {
    _logoutScheduled = false
    _redirecting = false
  }, 2_000)

  // Redirect to login. TanStack Router uses hash-based navigation here.
  // The redirect fires synchronously (first call within the window); only the
  // debounce-flag reset above uses setTimeout.
  if (typeof window !== 'undefined') {
    window.location.hash = '/login'
  }
}

/**
 * Reads the stashed LogoutReason (if any) and clears it — a true one-shot
 * read. Returns null when no forced logout produced this page load (normal
 * visit, or a manual Sidebar "Sign out"). Safe to call more than once (e.g.
 * React StrictMode's double-invoked effects): the second call always sees
 * the already-cleared key and returns null without side effects.
 */
export function consumeLogoutReason(): LogoutReason | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.sessionStorage.getItem(LOGOUT_REASON_KEY)
    window.sessionStorage.removeItem(LOGOUT_REASON_KEY)
    return raw === 'elsewhere' || raw === 'expired' ? raw : null
  } catch {
    return null
  }
}
