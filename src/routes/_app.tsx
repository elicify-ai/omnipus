import { createFileRoute, redirect } from '@tanstack/react-router'
import { AppShell } from '@/components/layout/AppShell'
import { fetchAppState, validateToken } from '@/lib/api'
import { forceLogout } from '@/lib/authLogout'
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
      // D2 fix: this branch used to `throw redirect(...)` directly, which
      // navigates to /login but never clears the Zustand auth store — the
      // Sidebar kept showing "logged in as X" (stale) through the bounce.
      // Route through the shared forceLogout() so this path clears the
      // store AND stashes a LogoutReason for the login screen, exactly like
      // the queryClient 401 / WS 1008 paths. forceLogout's own debounce
      // (authLogout.ts) makes it safe to call this on every re-run of
      // beforeLoad (e.g. TanStack Router's defaultPreload:'intent' re-running
      // this on link hover). The explicit `throw redirect` below is kept
      // too — it is the router-native abort-navigation signal beforeLoad is
      // expected to throw; it's redundant with forceLogout's own
      // window.location.hash write (both land on /login), but dropping it
      // would leave beforeLoad falling through to `component: AppShell`
      // instead of aborting the in-flight route resolution.
      forceLogout('expired')
      throw redirect({ to: '/login' })
    }
    // 'ok' or 'transient' → proceed into the app.
  },
  component: AppShell,
})
