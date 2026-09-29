import { redirect } from '@tanstack/react-router'
import { fetchAppState, validateToken, type AppState } from '@/lib/api'
import { forceLogout } from '@/lib/authLogout'
import { hasStoredSession } from '@/store/auth'
import { checkTokenValidity } from './authValidation'
import { captureLoginReturn } from './-loginReturn'

type AuthenticatedRouteContext = {
  location?: { href?: string }
}

/** Shared sign-in/onboarding gate for every authenticated chrome layout. */
export async function authenticatedBeforeLoad(routeContext: AuthenticatedRouteContext): Promise<void> {
  const rememberCurrentLocation = () => captureLoginReturn(routeContext.location?.href)
  let state: AppState | undefined
  try {
    state = await fetchAppState()
  } catch (error) {
    console.error('[app] Failed to fetch app state:', error)
  }

  if (state && !state.onboarding_complete) throw redirect({ to: '/onboarding' })
  if (state?.identity) {
    if (!state.identity.signed_in) {
      rememberCurrentLocation()
      throw redirect({ to: '/login' })
    }
    return
  }

  if (!hasStoredSession()) {
    rememberCurrentLocation()
    throw redirect({ to: '/login' })
  }

  const verdict = await checkTokenValidity(validateToken)
  if (verdict === 'unauthorized') {
    console.warn('[auth] Session validation failed (401) — redirecting to login')
    rememberCurrentLocation()
    forceLogout('expired')
    throw redirect({ to: '/login' })
  }
}
