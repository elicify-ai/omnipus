import { createFileRoute, redirect } from '@tanstack/react-router'
import { AppShell } from '@/components/layout/AppShell'
import { fetchAppState, validateToken } from '@/lib/api'
import { checkTokenValidity, resetTokenValidationCache } from './authValidation'

// Re-exported so the login flow (and tests) can reset the validation cache (#359).
export { resetTokenValidationCache }

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

    // Onboarding is complete — require an authenticated session. Auth is the
    // omnipus-session HttpOnly cookie (US-5 / FR-010): the SPA has no
    // JS-visible signal of whether one exists, so it always asks the server
    // rather than pre-checking local storage (there is nothing to check —
    // the cookie is invisible to JS). validateToken() rides the cookie
    // automatically (credentials:'include' in src/lib/api.ts); a fresh
    // install or expired/missing session comes back 401.
    //
    // checkTokenValidity is cached + transient-tolerant (see
    // authValidation.ts) — only a CONFIRMED 401 evicts the session; a
    // network/5xx hiccup keeps it.
    const verdict = await checkTokenValidity(validateToken)
    if (verdict === 'unauthorized') {
      console.warn('[auth] Session validation failed (401) — redirecting to login')
      throw redirect({ to: '/login' })
    }
    // 'ok' or 'transient' → proceed into the app.
  },
  component: AppShell,
})
