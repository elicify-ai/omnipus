// useLibraryCrossTabRefresh — D-107's pull half (2026-09-14 fix round).
//
// Two tabs never reconciled a Library listing: the tab that performed a write
// updated its own queries, the other tab served its stale listing until a
// manual reload (UAT: rows for files deleted 17 minutes earlier, and a
// preview of a deleted file behind one of them).
//
// This hook is the PULL half — the tab the user RETURNS to. It invalidates
// the workspace's listing queries on window focus and on
// visibilitychange→visible. The WS library_changed frame (the push half, see
// chat.ts's `case 'library_changed'` and the gateway's
// library_change_broadcast.go) covers the case focus events cannot: two
// windows side by side, where neither tab ever blurs.
//
// Why an explicit invalidation rather than relying on TanStack Query's
// built-in refetchOnWindowFocus: that only refetches queries that are
// already STALE, so a listing inside its 10 s staleTime window could keep
// serving deleted rows for the remainder of that window after focus. An
// invalidate marks it stale NOW and refetches whatever is mounted.

import { useEffect } from 'react'
import { queryClient } from '@/lib/queryClient'
import { libraryQueryKeys } from '@/lib/api'

/**
 * Invalidate the Library listing queries for `workspaceId` (null = the
 * virtual root, where only the workspaces list applies) whenever this tab
 * regains the user's attention.
 */
export function useLibraryCrossTabRefresh(workspaceId: string | null) {
  useEffect(() => {
    if (typeof window === 'undefined') return

    function invalidate() {
      if (workspaceId !== null) {
        void queryClient.invalidateQueries({ queryKey: ['library', workspaceId] })
      }
      // The workspaces list carries entry_count — any write in any workspace
      // can change it, and the virtual root reads it as its whole listing.
      void queryClient.invalidateQueries({ queryKey: libraryQueryKeys.workspaces() })
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
  }, [workspaceId])
}
