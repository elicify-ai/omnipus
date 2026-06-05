import { createFileRoute, redirect } from '@tanstack/react-router'
import { AppShell } from '@/components/layout/AppShell'
import { fetchAppState, validateToken, isApiError } from '@/lib/api'

// #359: validateToken() used to run as a raw, uncached fetch on EVERY route
// transition. Combined with the per-login bearer-token rotation (rest_auth.go:354)
// and the fact that the old guard cleared auth on ANY error, a transient 401 (a
// brief reload window) or a network blip logged the user out within minutes — the
// reported "~2-3 min session expiry". Two changes harden this:
//   1. Cache a successful validation for VALIDATE_TTL_MS so navigation does not
//      re-hit /auth/validate on every transition (shrinks the spurious-401 surface).
//   2. Only clear the token + redirect on a CONFIRMED 401. Transient failures
//      (network status 0, 5xx) keep the session — a hiccup must not kick the user out.
const VALIDATE_TTL_MS = 30_000
let lastValidatedAt = 0

// resetTokenValidationCache forces a fresh /auth/validate on the next guarded
// navigation (used by the login success handler; exported for tests).
export function resetTokenValidationCache() {
  lastValidatedAt = 0
}

// Pathless layout route — wraps all app screens in AppShell
// Landing page (/landing) is a sibling, NOT nested here, so it renders without the shell
// /onboarding is also a sibling — no AppShell, no beforeLoad
export const Route = createFileRoute('/_app')({
  beforeLoad: async () => {
    // First check onboarding state — if not complete, redirect to onboarding
    let state: { onboarding_complete: boolean } | undefined
    try {
      state = await fetchAppState()
    } catch (err) {
      console.error('[app] Failed to fetch app state:', err)
      // State endpoint failed — proceed to auth check (may redirect to login)
    }
    if (state && !state.onboarding_complete) {
      throw redirect({ to: '/onboarding' })
    }

    // Onboarding is complete — require login token
    const token = sessionStorage.getItem('omnipus_auth_token') ?? localStorage.getItem('omnipus_auth_token')
    if (!token) {
      throw redirect({ to: '/login' })
    }
    // Validate token by calling /auth/validate — but only when the cached result
    // has gone stale (see VALIDATE_TTL_MS note above), to avoid hammering the
    // endpoint on every route transition.
    if (Date.now() - lastValidatedAt <= VALIDATE_TTL_MS) {
      return
    }
    try {
      await validateToken()
      lastValidatedAt = Date.now()
    } catch (err) {
      // Only a CONFIRMED 401 means the token is actually invalid — clear it and
      // redirect to login. Transient failures (network status 0, 5xx) must NOT
      // log the user out; keep the session and let the next check decide.
      if (isApiError(err) && err.status === 401) {
        sessionStorage.removeItem('omnipus_auth_token')
        localStorage.removeItem('omnipus_auth_token')
        lastValidatedAt = 0
        console.warn('[auth] Token validation failed (401):', err)
        throw redirect({ to: '/login' })
      }
      // Transient/unknown error — do not evict the session on a hiccup.
      console.warn('[auth] Token validation skipped (transient error):', err)
    }
  },
  component: AppShell,
})
