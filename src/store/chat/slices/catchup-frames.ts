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
  CatchUpCompleteFrame,
  SessionSnapshotFrame,
  UserMessageFrame,
} from '@/lib/api/generated/asyncapi-types'
import { applySnapshotHistoryWipe, cursorFromTerminalFrame } from '../cursor'
import { replayErrorRetryAttempts, replayErrorRetryTimers } from '../runtime-state'
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
      // Opus review round 3 item N4 (LOW-MEDIUM): the cursor is NOT set
      // here anymore. It used to be minted from this frame's own seq/boot_id
      // — i.e. BEFORE the transcript this snapshot promises has actually
      // been read and applied. On a failed rebuild (the gateway now sends
      // `error` + `done{stats.replay_error:true}` and unbinds instead of
      // ever reaching catch_up_complete — see the 'done' case's own
      // replay_error branch below) that left the cursor advanced to a
      // position this client never actually reconstructed anything for,
      // and the tab stuck blank with nothing to retry from. `catch_up_complete`
      // is the ONLY frame that ever fires once the rebuild has genuinely
      // succeeded (this file's own doc comment on that case), so it is now
      // the sole cursor-setter — applySnapshotHistoryWipe's own
      // `cursor: bucket.cursor` default (unchanged, preserved-as-is) applies
      // here instead, meaning a failed rebuild simply leaves the cursor
      // wherever it already was (null on a first-ever attach).
      // #823 review round 9 — carried through the wipe below (as a plain
      // field write after it, same pattern as wipedOpenTurnIds) so
      // catch_up_complete's sweep can tell, once the replay finishes,
      // whether THIS rebuild was answering a restarted gateway
      // specifically. Deliberately narrower than "any reason" —
      // retention_exceeded means old history was trimmed, not that a turn
      // was interrupted, so an orphaned last user message there must NOT
      // be treated as unanswered.
      const snapshotFrame = frame as SessionSnapshotFrame
      withBucket(targetSid, (b) => {
        const wiped = applySnapshotHistoryWipe(b)
        return { ...wiped, snapshotWasBootMismatch: snapshotFrame.reason === 'boot_mismatch' }
      })
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
      // N4: a genuinely successful catch-up resets the replay_error retry
      // counter — a LATER, unrelated failure for this session must start
      // its own backoff from scratch, not continue counting up from a
      // previous, now-resolved incident.
      delete replayErrorRetryAttempts[targetSid]
      if (replayErrorRetryTimers[targetSid]) {
        clearTimeout(replayErrorRetryTimers[targetSid])
        delete replayErrorRetryTimers[targetSid]
      }
      withBucket(targetSid, (b) => produce(b, (draft) => {
        draft.cursor = cursorFromTerminalFrame(completeFrame, draft.cursor?.bootId)
        draft.awaitingCatchUp = false
        draft.isReplaying = false
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
        draft.replayCompletedForSession = targetSid
        // Opus review round 3 item N1 (BE-DESIGN.md §6.5) — see
        // ChatMessage.confirmedUnfinished's own doc comment for the full
        // "why". This is the ONE moment session_state.active_turn (already
        // applied to draft.activeTurnId by the session_state frame that
        // always precedes catch_up_complete in a real attach, §4.1 A6) is
        // authoritative for messages that predate this catch-up: a still-open
        // assistant message whose own turn is not the confirmed active one
        // is judged, once, right here — never re-derived reactively by the
        // render layer, which cannot tell "mid-stream, never disconnected"
        // from "genuinely ended without a done()" the way this moment can.
        // CORRECTION (Opus review round 6, R-W — DO-NOT-SHIP, real-browser
        // regression): a prior pass here scoped the "skip if already done"
        // guard to exclude the last assistant message, reasoning it was the
        // only way to flag F7's snapshot-rebuilt unfinished answer. That was
        // wrong and shipped a much worse bug: `replay_message` ALWAYS sets
        // status:'done' on ANY replayed message, including a completely
        // normal, successfully finished turn (the overwhelmingly common
        // case — every reconnect/reload/tab-switch of an already-answered
        // chat). Since `activeTurnId` is null once a turn is over,
        // `turnId !== activeTurnId` is true for essentially every replayed
        // answer, so the "even if done" exception flagged "Couldn't be
        // finished · Generate again" under FULLY FINISHED answers after
        // almost any catch-up (browser-confirmed: hard reload, network cut,
        // frozen socket, chat switch-back, laptop sleep, cross-tab,
        // gateway restart — everywhere except the tab that received the
        // live `done`). Reverted to the correct, narrower rule: a replayed,
        // persisted assistant message is complete BY DEFINITION unless it
        // is still genuinely open — the server never said this turn ended
        // incomplete, it simply isn't running anymore because it finished
        // normally. (F7's zero-assistant-message scenario — a turn killed
        // before any token streamed — has no message for this flag to
        // attach to at all; verified separately via activeTurnId/
        // assistantMessages, not this per-message flag.)
        //
        // One real gap remained: a turn genuinely cut short by a gateway
        // crash (not a graceful disconnect, so BUG 1's "stop closing
        // bubbles" fix never gets a chance to run) goes through a FULL
        // session_snapshot rebuild on reconnect — and `applySnapshotHistoryWipe`
        // necessarily erases the pre-crash bubble's own isStreaming:true
        // before replay_message ever reconstructs it as 'done'. `wipedOpenTurnIds`
        // (cursor.ts) captures exactly that lost signal before the wipe, so
        // it's checked here as an explicit exception to the "done → skip"
        // rule above — narrower than reverting to "even if done" outright,
        // since it only ever matches a turn THIS bucket personally watched
        // streaming immediately before the wipe.
        const wasWipedOpen = new Set(draft.wipedOpenTurnIds ?? [])
        for (const id of draft.messageOrder) {
          const m = draft.messagesById[id]
          if (m?.role !== 'assistant') continue
          if (m.status === 'interrupted' || m.status === 'error') continue
          const wipedOpen = !!m.turnId && wasWipedOpen.has(m.turnId)
          if (m.status === 'done' && !m.isStreaming && !wipedOpen) continue
          if (m.turnId && m.turnId !== draft.activeTurnId) {
            m.confirmedUnfinished = true
          }
        }
        draft.wipedOpenTurnIds = undefined

        // #823 review round 9 (real-browser evidence, CI run 36081327151):
        // a turn killed before a single token streamed leaves NO assistant
        // message at all — the in-flight projection lived only in the
        // crashed process's memory, and only a completed turn is ever
        // persisted. confirmedUnfinished (above) has nothing to attach to
        // in that case. Narrowly scoped to the one case design §6.5/
        // scenario h actually asks for: this rebuild specifically answered
        // a restarted gateway (not any snapshot reason — retention_exceeded
        // means old history was trimmed, not that a turn was cut short),
        // the transcript's LAST entry is a user message (so nothing
        // answered it), and no turn is running now to still answer it.
        if (draft.snapshotWasBootMismatch && !draft.activeTurnId) {
          const lastId = draft.messageOrder[draft.messageOrder.length - 1]
          const lastMsg = lastId ? draft.messagesById[lastId] : undefined
          draft.unansweredLastUserMessageId = lastMsg?.role === 'user' ? lastMsg.id : null
        } else {
          draft.unansweredLastUserMessageId = null
        }
        draft.snapshotWasBootMismatch = undefined
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
