// has-earlier.ts — chat-state half of the has_earlier_messages flag
// (founder ruling 2026-10-11, interim until #1266).
//
// The server stamps every chat-view carrier with whether the view starts at
// the session's last /clear marker (cleared context): SessionDetail
// (REST detail), SessionSnapshotFrame (attach replay) and the
// X-Has-Earlier-Messages response header on GET /sessions/{id}/messages.
// Each carrier's consumer funnels the flag HERE, so the chat view reads one
// bucket field no matter which path loaded the history.
//
// The flag renders exactly one quiet line above the clear divider
// (components/chat/ClearContextDivider.tsx) — it is never a control and
// never a pager; reaching the earlier messages is #1266, not this flag.

import { useChatStore } from './store'

/**
 * Records one carrier's has_earlier_messages value into the session's chat
 * bucket. A null/undefined session id (no active session, or the '__pending'
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
