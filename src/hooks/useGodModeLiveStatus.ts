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

export type GodModeLiveStatus = 'on' | 'off' | 'unknown'

/**
 * useGodModeLiveStatus — the tri-state answer to "is god-mode live right
 * now", shared by the sidebar God Mode pill and the sidebar-open-button dot
 * (`src/components/layout/GodModeIndicators.tsx`), which replaced the
 * app-wide GodModeActiveBanner (founder decision 2026-09-25).
 *
 *   'on'      — the query succeeded and reported enabled=true.
 *   'unknown' — the query FAILED (non-bypass error): the app cannot tell
 *               whether god-mode is on, and silence here would read exactly
 *               like "sandboxing is confirmed on". Callers must surface this
 *               (the pill's warning variant), never collapse it to 'off'.
 *   'off'     — everything else: confirmed off, still loading, or the god
 *               mode endpoint is known-unavailable under dev_mode_bypass.
 *
 * Shares AppShell's own ['app-state'] cache entry (via useDevModeBypassKnown)
 * and the ['god-mode'] entry GodModeControl already fetches on the Gateway
 * screen — not additional network round trips. The doomed-request skip
 * (enabled: appStateResolved && !devModeBypassKnown) is the 2026-09-24 fix
 * that stopped a dev-mode-bypass install from firing a real 503 (retried 3×
 * by the query client) on every page load; see useDevModeBypassKnown for the
 * shared-cache history.
 */
export function useGodModeLiveStatus(): GodModeLiveStatus {
  const { known: devModeBypassKnown, resolved: appStateResolved } = useDevModeBypassKnown()

  const { data: godMode, isError, error } = useQuery({
    queryKey: ['god-mode'],
    queryFn: fetchGodMode,
    // Only fire once app-state has resolved AND confirmed bypass is off.
    // While appState is still loading the query stays disabled (not an
    // error) and the status reads 'off' — there is nothing yet to report
    // either way.
    enabled: appStateResolved && !devModeBypassKnown,
  })

  // Known from appState (the common case — no doomed request was even made)
  // OR a genuine 503 the query itself hit (defense in depth, e.g. a late
  // toggle of dev_mode_bypass mid-session before appState refetches). Must
  // be checked BEFORE isError: under bypass the endpoint 503s by design, and
  // reading that as 'unknown' would put a false alarm on every screen for
  // the lifetime of any dev-mode-bypass install.
  const bypassUnavailable = devModeBypassKnown || isBypassUnavailable(error)

  if (bypassUnavailable) return 'off'
  if (isError) return 'unknown'
  return godMode?.enabled === true ? 'on' : 'off'
}
