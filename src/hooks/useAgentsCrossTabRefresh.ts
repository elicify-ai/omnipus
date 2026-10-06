// useAgentsCrossTabRefresh — agent-picker freshness fix, pull half
// (2026-09-28, second half of the fix; GitHub issue #1009).
//
// Mirrors src/components/library/useLibraryCrossTabRefresh.ts (D-107)
// almost verbatim, for the same reason: the WS agent_created frame (the
// push half — see frames.ts's `case 'agent_created'` and the gateway's
// agent_created_broadcast.go) is fire-and-forget over a per-client send
// buffer that can be full, and the WS connection itself can drop and
// reconnect. Neither drops the picker permanently stale, but both leave a
// window where a tab that never sees the frame keeps serving a cached
// ['agents'] list that is missing the newly created agent — until
// something else invalidates it, or its 30 s default staleTime
// (queryClient.ts) elapses.
//
// This hook is the PULL half — the tab the user RETURNS to. It invalidates
// the ['agents'] query on window focus and on visibilitychange→visible,
// independent of staleTime, so a returning tab never serves a stale picker
// for the remainder of that window after regaining focus.
//
// Why an explicit invalidation rather than relying on TanStack Query's
// built-in refetchOnWindowFocus: that only refetches queries that are
// already STALE, so an agent list still inside its staleTime window could
// keep omitting the new agent for the remainder of that window after
// focus. An invalidate marks it stale NOW and refetches whatever is
// mounted.

import { useEffect } from 'react'
import { queryClient } from '@/lib/queryClient'

/**
 * Invalidate the `['agents']` query whenever this tab regains the user's
 * attention (window focus, or visibilitychange → visible).
 */
export function useAgentsCrossTabRefresh() {
  useEffect(() => {
    if (typeof window === 'undefined') return

    function invalidate() {
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
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
