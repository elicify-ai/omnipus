// catchup-frames.ts: #823 catch-up redesign (BE-DESIGN.md §4/§6) — the three
// frame types Lane C's contract additions introduced (Step 0):
// session_snapshot, catch_up_complete, user_message. Kept in its own file
// (mirroring handleReplayAndStatusFrame's own "second switch" pattern,
// slices/replay-and-status-frames.ts) rather than folded into that function
// or into handleFrame's own switch — both of those are already at their
// grandfathered line-budget ceilings (scripts/budgets/functions.txt); a new
// function starts under the ordinary budget (warn>120, fail>240) instead.

import type { StoreApi } from 'zustand'
import { produce } from 'immer'
import type {
  SessionSnapshotFrame,
  CatchUpCompleteFrame,
  UserMessageFrame,
} from '@/lib/api/generated/asyncapi-types'
import { applySnapshotHistoryWipe, cursorFromTerminalFrame } from '../cursor'
import type { ChatMessage, ChatStore, SessionChatState } from '../types'

type Frame = Parameters<ChatStore['handleFrame']>[0]
interface CatchUpFrameContext {
  frame: Frame
  targetSid: string | null
  get: StoreApi<ChatStore>['getState']
  withBucket: (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void
}

export function handleCatchUpFrame({ frame, targetSid, withBucket }: CatchUpFrameContext): boolean {
  switch (frame.type) {
    // BE-DESIGN.md §4.5/§4.6/§6.2 — the server decided this connection's
    // cursor is not servable (first load, a stale/mismatched boot id, or a
    // gap the SPA itself reported — §6.2's "gap" row re-attaches, which can
    // also land here). Wipes HISTORY only — the pending tail and every
    // out-of-band field survive (applySnapshotHistoryWipe's own doc
    // comment). `session_state` always follows this frame in the real
    // attach sequence (§4.1 A6), so nothing here needs to preserve
    // pendingAsk/activeTurn itself beyond what the wipe already carries
    // forward defensively.
    case 'session_snapshot': {
      if (!targetSid) return true
      const snapshotFrame = frame as SessionSnapshotFrame
      withBucket(targetSid, (b) => ({
        ...applySnapshotHistoryWipe(b),
        cursor: cursorFromTerminalFrame(snapshotFrame, b.cursor?.bootId),
      }))
      return true
    }

    // BE-DESIGN.md §4.1/§6.2 — the definitive "catch-up is over" signal for
    // this attach, replacing the #822 replay-terminator-`done` heuristic for
    // every NEW attach this frame covers (a gateway that has not yet shipped
    // Lane A's hub never sends this frame, so the pre-existing terminator-
    // done path in slices/frames.ts is untouched and keeps covering that
    // case — see SQUAD-REPORT-BEC.md's gaps section on why the old
    // mechanism is not deleted in this pass). Mints the cursor directly from
    // the frame's own seq/boot_id (§3.4) rather than through the gap-gate,
    // since this frame is by definition the new authoritative position.
    case 'catch_up_complete': {
      if (!targetSid) return true
      const completeFrame = frame as CatchUpCompleteFrame
      withBucket(targetSid, (b) => ({
        cursor: cursorFromTerminalFrame(completeFrame, b.cursor?.bootId),
        awaitingCatchUp: false,
        isReplaying: false,
        // Real-browser regression (orchestrator round 4, scenarios c/e/f):
        // `replayCompletedForSession` (ChatScreen.tsx's own REST-fallback
        // "was replay finished for this session" flag — see its doc comment
        // there) was previously only ever set by the live `done` handler's
        // frames.ts terminal-sweep, never by this frame — even though
        // `catch_up_complete` is now THE definitive "catch-up is over"
        // signal for an attach (this file's own doc comment above). A
        // session whose turn already finished streaming into its bucket
        // BEFORE the user reattached (so `done` never re-fires during this
        // attach cycle) reattaches with this flag still stale/unset for it.
        // ChatScreen.tsx's own REST-overwrite effect is separately guarded
        // by `storeMessageCount > 0`, but the sibling judge-verdict-merge
        // effect is not — it gates ONLY on `isReplaying || storeMessageCount
        // === 0`, so a stale `replayCompletedForSession` lets a REST
        // snapshot that resolved out of order (a real, live-verified race —
        // see that effect's own doc comment) merge against a session this
        // client's WS-driven state had already fully reconstructed.
        replayCompletedForSession: targetSid,
      }))
      return true
    }

    // BE-DESIGN.md §1.2/§4.7, founder decision Q1 = YES: every tab bound to
    // this session sees the user's own message, not just the sender's. The
    // SENDER's tab already holds an optimistic bubble keyed by
    // `client_message_id` (outbound-lifecycle.ts's `buildQueuedUserMessage`
    // stamps `id: clientMessageId`) — this frame is then a no-op there (the
    // `message_status` frame drives its received/working transition, not
    // this one). A DIFFERENT tab — or this tab replaying an offline-queue
    // drain, §6.6 — has no such bubble, so insert it in arrival order, which
    // is exactly where the hub published it relative to every other frame
    // this tab has already applied (§1.1: one chokepoint, one order).
    // Idempotent on the server id.
    case 'user_message': {
      if (!targetSid) return true
      const userMsgFrame = frame as UserMessageFrame
      withBucket(targetSid, (b) => {
        const ownBubbleExists =
          !!userMsgFrame.client_message_id && !!b.messagesById[userMsgFrame.client_message_id]
        if (ownBubbleExists || b.messageOrder.includes(userMsgFrame.id)) {
          return {}
        }
        const newMsg: ChatMessage = {
          id: userMsgFrame.id,
          role: 'user',
          content: userMsgFrame.content,
          timestamp: userMsgFrame.timestamp,
          deliveryStatus: 'received',
        }
        return produce(b, (draft) => {
          draft.messagesById[newMsg.id] = newMsg
          draft.messageOrder.push(newMsg.id)
        }) as Partial<SessionChatState>
      })
      return true
    }

    default:
      return false
  }
}
