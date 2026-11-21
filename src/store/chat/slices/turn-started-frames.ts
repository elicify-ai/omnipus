// turn-started-frames.ts: #1428 step 2, UAT-15 option A. The server names a
// turn the moment it starts (live-only, unnumbered turn_started), so a page
// knows the running turn's id before its first token. A typed /stop-redirect
// or Stop then captures that id (lib/running-turn-id.ts reads activeTurnId),
// and the reconnect reconciliation can tell an applied redirect's NEW turn
// from the one the command meant to stop (spec 4.7, F-tt).
//
// Own pre-switch handler (bound-send-frames.ts pattern): the main switch in
// slices/frames.ts::handleFrame is a grandfathered budget entry.

import type { SessionStateActiveTurn } from '@/lib/api/generated/asyncapi-types'
import type { ChatStore, SessionChatState } from '../types'
import { isTurnFinished } from '../routing'

type Frame = Parameters<ChatStore['handleFrame']>[0]
type WithBucket = (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void

/**
 * A turn the server says is running in `sessionId` (session_state.active_turn
 * or turn_started). S2: a turn this client already finished (its own done was
 * processed, routing.ts::markTurnFinished) is ignored outright; re-applying it
 * would set isStreaming / activeTurnId again with no second done to clear it.
 */
export function applyAnnouncedTurn(sessionId: string, activeTurn: SessionStateActiveTurn, withBucket: WithBucket): void {
  if (isTurnFinished(sessionId, activeTurn.turn_id)) return
  withBucket(sessionId, () => ({
    isStreaming: true,
    activeTurnId: activeTurn.turn_id,
    activeTurnAgentId: activeTurn.agent_id,
  }))
}

/** Returns true when the frame was a turn_started, applied to its own chat's bucket. */
export function handleTurnStartedFrame(frame: Frame, withBucket: WithBucket): boolean {
  if (frame.type !== 'turn_started') return false
  applyAnnouncedTurn(frame.session_id, frame.active_turn, withBucket)
  return true
}
