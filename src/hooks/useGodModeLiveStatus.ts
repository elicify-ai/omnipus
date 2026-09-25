import { useQuery } from '@tanstack/react-query'
import { fetchGodMode } from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import { useDevModeBypassKnown } from '@/hooks/useDevModeBypassKnown'

// GET /api/v1/gateway/god-mode is gated by adminWrap (withAuth →
// RequireNotBypass) — pkg/gateway/rest_god_mode.go:49. Under
// gateway.dev_mode_bypass=true, RequireNotBypass returns 503 BEFORE the
// handler runs (pkg/gateway/middleware/bypass_gate.go), every single time —
// not an outage, an expected "this surface is disabled while bypass is
// active" response. Treating that 503 the same as a transport failure
// produced a permanent, false "gateway may be offline" signal on every
// screen for any dev-mode-bypass install. A genuine failure (network down,
// 500, anything else) must still surface normally.
export function isBypassUnavailable(err: unknown): boolean {
  return isApiError(err) && err.status === 503
}

/**
 * useGodModeOn — the boolean answer to "is god-mode live right now", shared
 * by the sidebar God Mode pill and the app-shell corner dot
 * (`src/components/layout/GodModeIndicators.tsx`).
 *
 * Founder decision 2026-09-25 (revision 2, overriding the earlier amber
 * suggestion): the God Mode indicators are RED when god-mode is on and
 * INVISIBLE in every other state — off, still loading, fetch error, and
 * dev-mode bypass. There is no amber or "unknown" variant anywhere; a fetch
 * failure must NOT surface as an indicator — it shows nothing anywhere.
 * (Only an APP-STATE fetch failure has its own surface, the gateway-state
 * fetch-error banner in AppShell; a god-mode-only failure shows nothing.)
 *
 * Shares AppShell's own ['app-state'] cache entry (via
 * useDevModeBypassKnown) and the ['god-mode'] entry GodModeControl already
 * fetches on the Gateway screen — not additional network round trips. The
 * doomed-request skip (enabled: appStateResolved && !devModeBypassKnown) is
 * the 2026-09-24 fix that stopped a dev-mode-bypass install from firing a
 * real 503 (retried 3× by the query client) on every page load; see
 * useDevModeBypassKnown for the shared-cache history.
 */
export function useGodModeOn(): boolean {
  const { known: devModeBypassKnown, resolved: appStateResolved } = useDevModeBypassKnown()

  const { data: godMode, isError } = useQuery({
    queryKey: ['god-mode'],
    queryFn: fetchGodMode,
    // Only fire once app-state has resolved AND confirmed bypass is off.
    // While appState is still loading the query stays disabled (not an
    // error) and this reads false — nothing to report.
    enabled: appStateResolved && !devModeBypassKnown,
  })

  // devModeBypassKnown is the one bypass term this boolean needs: once
  // bypass is known on, the query above is disabled, but a stale cached
  // `enabled: true` from before a mid-session bypass toggle can still sit
  // in `data` — this term is what suppresses it. A bypass-gate 503 the
  // query itself hit needs no separate term here: any query error (503
  // included) already forces false through !isError below. GodModeControl
  // DOES need the distinction (its "unavailable while bypass" note is not
  // an error state) — that is what isBypassUnavailable stays exported for.
  return !devModeBypassKnown && !isError && godMode?.enabled === true
}
