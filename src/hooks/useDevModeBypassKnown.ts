import { useQuery } from '@tanstack/react-query'
import { fetchAppState } from '@/lib/api'

export interface DevModeBypassQuery {
  /** True only once AppState has resolved AND reports dev_mode_bypass:true. */
  known: boolean
  /** True once the underlying `['app-state']` query has a value (success or not-yet-erred-away undefined check is the caller's job via `appState`). */
  resolved: boolean
}

/**
 * useDevModeBypassKnown — the single source of "is gateway.dev_mode_bypass
 * confirmed on right now", shared by every caller that needs to skip a
 * request known to be doomed under bypass (adminWrap-gated endpoints —
 * RequireNotBypass returns 503 before the handler ever runs, see
 * pkg/gateway/rest.go's adminWrap doc comment).
 *
 * Reads the app-wide `['app-state']` query (same cache entry AppShell
 * already fetches — this is not a second network round trip on any page
 * that renders AppShell). `known` reads `false` while AppState is still
 * loading or has failed to load — an unresolved fetch must NOT be treated
 * as "bypass confirmed off": that would let a caller fire its own doomed
 * request during the brief window before AppState answers. `resolved`
 * distinguishes that window (AppState hasn't answered yet — `false`) from
 * a completed fetch, whatever it reported (`true`) — a caller computing its
 * own `isLoading` needs both, not just `known`.
 *
 * Extracted from GodModeControl.tsx (commit 671a68ad6), which had this
 * exact `useQuery(['app-state'], fetchAppState)` + `=== true` block
 * duplicated twice in the same file — the second copy is exactly how a
 * THIRD caller would have drifted from the other two instead of sharing one
 * definition.
 */
export function useDevModeBypassKnown(): DevModeBypassQuery {
  const { data: appState } = useQuery({
    queryKey: ['app-state'],
    queryFn: fetchAppState,
    staleTime: 60_000,
  })
  return {
    known: appState?.dev_mode_bypass === true,
    resolved: appState !== undefined,
  }
}
