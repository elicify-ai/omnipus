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

    // Onboarding is complete — require login token
    const token = sessionStorage.getItem('omnipus_auth_token') ?? localStorage.getItem('omnipus_auth_token')
    if (!token) {
      throw redirect({ to: '/login' })
    }
    // Validate the token (cached + transient-tolerant — see authValidation.ts).
    // Only a CONFIRMED 401 evicts the session; a network/5xx hiccup keeps it.
    const verdict = await checkTokenValidity(validateToken)
    if (verdict === 'unauthorized') {
      sessionStorage.removeItem('omnipus_auth_token')
      localStorage.removeItem('omnipus_auth_token')
      console.warn('[auth] Token validation failed (401) — redirecting to login')
      throw redirect({ to: '/login' })
    }
    // 'ok' or 'transient' → proceed into the app.
  },
  component: AppShell,
})
