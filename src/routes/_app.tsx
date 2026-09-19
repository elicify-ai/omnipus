import { createFileRoute, redirect } from '@tanstack/react-router'
import { AppShell } from '@/components/layout/AppShell'
import { fetchAppState, validateToken, type AppState } from '@/lib/api'
import { forceLogout } from '@/lib/authLogout'
import { hasStoredSession } from '@/store/auth'
import { checkTokenValidity, resetTokenValidationCache } from './authValidation'

// Re-exported so the login flow (and tests) can reset the validation cache (#359).
export { resetTokenValidationCache }

// Pathless layout route — wraps all app screens in AppShell
// Landing page (/landing) is a sibling, NOT nested here, so it renders without the shell
// /onboarding is also a sibling — no AppShell, no beforeLoad
export const Route = createFileRoute('/_app')({
  beforeLoad: async () => {
    // First check onboarding state — if not complete, redirect to onboarding
    let state: AppState | undefined
    try {
      state = await fetchAppState()
    } catch (err) {
      console.error('[app] Failed to fetch app state:', err)
      // State endpoint failed — proceed to auth check (may redirect to login).
      // This is ALSO where an older backend lands: its /state response has
      // no `identity` field, which fails the AppState Zod schema (identity
      // is required — ADR-0010) and throws here exactly like any other
      // fetch failure, so `state` stays undefined and the fallback path
      // below runs unchanged.
    }
    if (state && !state.onboarding_complete) {
      throw redirect({ to: '/onboarding' })
    }

    // ADR-0010 / login-and-onboarding-spec.md §2.4 — collapse the boot path.
    // GET /api/v1/state already carries `identity.signed_in`, so a signed-in
    // caller never needs the separate GET /api/v1/auth/validate round trip
    // this route used to await in series. `state?.identity` (not just
    // `state`) is the guard: `identity` is a required AppState field on the
    // current contract, so a real gateway response either has it or the
    // request already threw above (caught, `state` stays undefined) — but an
    // OLDER backend's response can still resolve here without one, so this
    // checks for the field itself, not just a successful fetch.
    if (state?.identity) {
      if (!state.identity.signed_in) {
        // The server already told us: not signed in. No point asking
        // /auth/validate too — same destination, one less round trip.
        throw redirect({ to: '/login' })
      }
      // Signed in per the boot request itself — proceed into the app
      // without the separate validate call.
      return
    }

    // Onboarding is complete and `identity` is unavailable (fetchAppState
    // failed above — network error — or an older backend whose response has
    // no `identity` field). Fall back to exactly today's behaviour: auth is
    // the omnipus-session HttpOnly cookie (US-5 / FR-010); the SPA has no
    // JS-visible signal of whether one exists, so it asks the server rather
    // than pre-checking local storage for the cookie itself (there is
    // nothing to check — the cookie is invisible to JS). validateToken()
    // rides the cookie automatically (credentials:'include' in
    // src/lib/api.ts); a fresh install or expired/missing session comes
    // back 401.
    //
    // One thing IS checkable locally first: whether this browser has EVER
    // signed in at all (hasStoredSession(), src/store/auth.ts —
    // omnipus_auth_username is only ever written by a successful login). A
    // browser that has never signed in has no session to validate, so asking
    // the server would be a GUARANTEED 401 on every single first paint —
    // noisy in normal operation and useless as a signal for spotting a real
    // failure. Skip the round trip entirely for that case and go straight to
    // /login, same as a manual sign-out (no forceLogout()/banner — there is
    // no session being forced out). A RETURNING user — including one whose
    // session has genuinely expired — still always goes through
    // checkTokenValidity() below; hasStoredSession() only reports whether a
    // login ever happened, not whether it's still valid.
    if (!hasStoredSession()) {
      throw redirect({ to: '/login' })
    }

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
