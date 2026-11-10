// useAgentsCrossTabRefresh — shell freshness, pull half (T-11 / FR-010).
//
// AppShell is the only production caller. The composer agent picker used to
// own this hook; that picker is gone, and a refresh that lived on it stopped
// the moment the picker unmounted. The shell stays mounted for the whole
// signed-in app, so focus and visibility refresh the roster and the workspace
// membership list even while both queries are still inside their stale time.
//
// Mirrors src/components/library/useLibraryCrossTabRefresh.ts (D-107). The
// WS agent_created frame (frames.ts `case 'agent_created'`, gateway
// agent_created_broadcast.go) is fire-and-forget over a per-client send
// buffer that can be full, and the WS connection itself can drop and
// reconnect. Either leaves a window where a tab that never sees the frame
// keeps a cached ['agents'] list, or a cached workspace membership list,
// until something else invalidates it or the 30 s default staleTime
// (queryClient.ts) elapses.
//
// This hook is the PULL half — the tab the user RETURNS to. It invalidates
// ['agents'] and the active-workspace membership query on window focus and
// on visibilitychange→visible, independent of staleTime. A hidden tab does
// not invalidate: visibilitychange only counts when the document is visible.
//
// Why an explicit invalidation rather than TanStack Query's built-in
// refetchOnWindowFocus: that only refetches queries that are already STALE,
// so a list still inside its staleTime window could keep omitting a new
// agent or a new member for the rest of that window after focus. An
// invalidate marks both stale NOW and refetches whatever is mounted.
// Membership is the existing workspace list
// (workspacesQueryKeys.list({ status: 'active' })), not a second store.

import { useEffect } from 'react'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'

/**
 * Invalidate the agent roster and the active workspace membership list
 * whenever this tab regains the user's attention (window focus, or
 * visibilitychange → visible). Hidden tabs do not invalidate.
 */
export function useAgentsCrossTabRefresh() {
  useEffect(() => {
    if (typeof window === 'undefined') return

    function invalidate() {
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
      void queryClient.invalidateQueries({
        queryKey: workspacesQueryKeys.list({ status: 'active' }),
      })
    }

    function onVisibilityChange() {
      if (document.visibilityState === 'visible') invalidate()
    }

    window.addEventListener('focus', invalidate)
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => {
      window.removeEventListener('focus', invalidate)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [])
}
