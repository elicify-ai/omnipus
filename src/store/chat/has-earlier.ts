// has-earlier.ts — compatibility setter for the hasEarlierMessages display hint.
//
// Paged history owns its continuation and clear-crossing state in the existing
// history slice. Available page/snapshot commits derive the display hint from
// generated page metadata; this setter neither reads transport carriers nor
// establishes paging boundaries, fetches earlier rows or crosses a clear marker.

import { useChatStore } from './store'

/**
 * Updates only the addressed bucket's compatibility display hint.
 * A null/undefined session id (no active session, or the '__pending'
 * optimistic-send sentinel) is a no-op — the flag belongs to a real session.
 */
export function recordSessionHasEarlier(sessionId: string | null | undefined, hasEarlier: boolean): void {
  if (!sessionId || sessionId === '__pending') return
  useChatStore.setState((state) => {
    const bucket = state.sessionsById[sessionId]
    if (!bucket || bucket.hasEarlierMessages === hasEarlier) return state
    return {
      sessionsById: {
        ...state.sessionsById,
        [sessionId]: { ...bucket, hasEarlierMessages: hasEarlier },
      },
    }
  })
}
