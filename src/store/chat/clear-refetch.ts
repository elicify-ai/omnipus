// clear-refetch.ts — FR-030/031 (U10b) SPA half, the transcript re-read owed
// to a /clear (R3, round 3).
//
// A successful /clear moves the session's context/display window server-side
// and appends ONE chat-view marker entry ("Conversation context cleared",
// type system, role system — the saved server reference
// core-clear-reference-67345b1d7/clear_session.go). Nothing pushes that
// marker live, so the view can only reflect the server by READING the
// transcript. The rules this module implements (review rounds 1–3):
//
//   C1  The refresh intent belongs to the /clear OPERATION, not to "the next
//       done in this session": it is armed when a /clear is actually sent on
//       the wire (every send path — normal, offline-queue drain, Retry/resend
//       — calls armClearRefresh), bound to that send's client_message_id and
//       the turn active at send-time, and inspected BEFORE the done handler
//       can drain another message whose send would arm a different operation.
//   C2  The re-read is queryClient.fetchQuery — a read that REJECTS on
//       failure. On failure nothing stale is applied and the intent survives
//       for the next opportunity (a later turn end, or the screen's history
//       Retry via retryClearTranscriptRead).
//   C3  The fetched projection is MERGED into the bucket: server rows are
//       authoritative for what the server has, while genuinely unconfirmed
//       user input (still sending, failed, or an unconfirmed first send) and
//       client-only system rows newer than the /clear send survive. A
//       durably-appended (received/working) pre-clear row obeys the server's
//       post-clear window like any other server-covered row.
//   C4  A busy bucket (streaming/replaying) DEFERS the application: the
//       fetched projection is held on the intent — never erased — and
//       applied at the next busy→idle transition (turn done/error, the
//       replay-clear timer, setReplaying(false), catch_up_complete) without
//       a new network read. The intent clears only after a projection
//       carrying a marker the view did not have at arm time — THIS
//       operation's marker — has been applied.
//   D2  Operation ownership: the read is keyed by the operation's
//       client_message_id (a newer /clear never shares an older in-flight
//       read), the owning intent is captured before the read is awaited, and
//       an obsolete response — one whose operation has since been replaced —
//       is ignored outright.
//
//   E1  The intent is retired only by the WIRE (architect ruling S3-02 — the
//       wire is the source of truth, three signals, NO timer):
//         1. Refused / not performed: message_status {state: 'failed'} for
//            the /clear's client_message_id (any refusal path) — handled in
//            frames.ts, which calls forgetClearRefreshForSend; 'discarded'
//            likewise. The explanatory reply stays an ordinary row; this
//            module never parses text.
//         2. Done: the fresh projection carries a marker unknown at arm time
//            (below) — the intent clears once it is applied.
//         3. Held (stopped agent, busy-turn wait): NO terminal signal. The
//            row stays queued with NO deadline — no wall-clock bound, no
//            read-count bound, no toast. A held clear that later executes
//            still finds its intent armed and refreshes; a journaled failed
//            replayed by reconnect catch-up retires through signal 1.
//       A session that is deleted drops its intent too (forgetClearRefresh).
//   E2  No branch fails silently. A history Retry that reads but cannot
//       settle the operation says so; an application failure is shown once;
//       a READ failure surfaces as the screen's own history-error state
//       (V3 below — the shared ['messages', sid] query state, pinned by
//       clear-row-settle.test.ts). The diagnostics stay for triage.
//
// Nothing here clears anything locally, ever: a projection that does not
// carry a marker unknown at arm time (the clear has not executed
// server-side yet, or the clear was refused — a refusal writes no marker)
// is applied to NOTHING, and the intent survives so the next opportunity
// re-reads. The screen moves only when the server's own fresh view
// (starting at the marker) says it moved.

import { queryClient } from '@/lib/queryClient'
import { fetchSessionMessages } from '@/lib/api'
import type { Message, SessionHistoryPage } from '@/lib/api'
import { applyHistoryPageToBucket } from './history-merge'
import { advanceHistoryEntryGenerations } from '@/lib/historyEntryReads'
import { abortHistoryReads } from './history'
import { logDiagnostic } from '@/lib/telemetry'
import { useUiStore } from '@/store/ui'
import type { ChatMessage, SessionChatState } from './types'
import { isPendingTailMessage } from './cursor'
import { pendingClearRefetches, type PendingClearRefresh } from './runtime-state'

type WithBucket = (
  sid: string | null,
  updater: (bucket: SessionChatState) => Partial<SessionChatState>,
) => void

/**
 * Records the transcript re-read owed to a /clear that just went out on the
 * wire. Called from EVERY send path that can put a /clear on the wire (the
 * normal sendMessage branches and the Retry/resend path); a send that never
 * reached the connection arms nothing.
 */
// E1's former read bound (MAX_UNRESOLVED_READS) and wall-clock bound
// (CLEAR_CONFIRM_TIMEOUT_MS) are DELETED with ruling S3-02: there is NO read
// bound and NO wall-clock bound. A refused /clear retires through the wire's
// message_status failed (signal 1), a done one through its marker, and a
// HELD one stays queued with no deadline — timing it out is what produced
// the false "did not confirm" toast this ruling deletes.
function tell(message: string): void {
  useUiStore.getState().addToast({ message, variant: 'warning' })
}

/**
 * Retires a session's refresh intent — and (when `op` is given) only if that
 * operation still owns the slot: a newer /clear's own operation is never
 * touched by an older one's settle (S3-11's kept ownership semantics; the
 * latent timer-clear half of S3-11 is gone with the deleted bounded-wait
 * timer — there is no timer left to clear wrongly).
 */
function dropIntent(sid: string, op?: PendingClearRefresh): void {
  if (op ? pendingClearRefetches[sid] === op : true) delete pendingClearRefetches[sid]
}

/** Drops a session's refresh intent — called when the session itself is deleted. */
export function forgetClearRefresh(sid: string): void {
  dropIntent(sid)
}

/**
 * C1 + ruling S3-02 signal 1: a /clear the server settled WITHOUT executing —
 * DISCARDED (stop before delivery) or FAILED (any refusal path: the busy-turn
 * not-performed case, a helper-session refusal, a plain failure) — will never
 * write its marker. The transcript re-read it armed would only spin and then
 * claim the clear was "not confirmed" after the server already answered on
 * the wire. Retires the intent only when it still belongs to THIS send; a
 * newer /clear keeps its own operation. Called from the message_status frame
 * handler (frames.ts), which shows the failed row honestly.
 */
export function forgetClearRefreshForSend(sid: string, clientMessageId: string): void {
  if (pendingClearRefetches[sid]?.clientMessageId === clientMessageId) dropIntent(sid)
}

/** The operation's own /clear user row: the optimistic bubble (its id IS the
 * client_message_id) or the server echo (clientMessageId match). Same
 * backward scan mergeServerProjection uses — one implementation, no drift. */
function findClearRowId(bucket: SessionChatState, clientMessageId: string): string | null {
  for (let i = bucket.messageOrder.length - 1; i >= 0; i--) {
    const m = bucket.messagesById[bucket.messageOrder[i]]
    if (m && m.role === 'user' && (m.clientMessageId ?? m.id) === clientMessageId) return bucket.messageOrder[i]
  }
  return null
}

/**
 * R2-S3-06: reads that settle nothing are free for this many turn ends - a
 * clear that executes normally is found within the first two or three (the
 * stopped turn's own done, then the clear's reply turn). Past this, the
 * re-read backs off so a clear that can never be found (it never reached the
 * server, or its marker is not recognised) stops costing a full transcript
 * read per turn end. Nothing is given up (ruling S3-02: no deadline, no user
 * surface): the intent stays armed and the read still repeats, doubling the
 * gap up to MAX_TURN_ENDS_BETWEEN_READS; a user Retry always reads at once.
 */
export const FREE_UNRESOLVED_READS = 5
export const MAX_TURN_ENDS_BETWEEN_READS = 16

function skipBackedOffRead(sid: string, op: PendingClearRefresh): boolean {
  const reads = op.unresolvedReads ?? 0
  if (reads < FREE_UNRESOLVED_READS) return false
  const gap = Math.min(2 ** (reads - FREE_UNRESOLVED_READS + 1), MAX_TURN_ENDS_BETWEEN_READS)
  op.turnEndsSinceRead = (op.turnEndsSinceRead ?? 0) + 1
  if (op.turnEndsSinceRead < gap) {
    // One line per run of skipped turn ends, so a late-applied clear can be traced.
    if (op.turnEndsSinceRead === 1) logDiagnostic('clearRefetchBackoffSkip', { sessionId: sid, operationId: op.clientMessageId, reads, gap })
    return true
  }
  op.turnEndsSinceRead = 0
  return false
}

/**
 * E1/E2: a re-read that could not settle the operation (no own user row, or
 * no marker for it yet). There is NO bound (ruling S3-02): the read is a
 * diagnostic counter only, the intent stays armed, and every later turn end
 * re-reads until a wire signal resolves the operation — the marker (signal
 * 2) or message_status failed (signal 1). A user-initiated Retry that
 * cannot settle says so immediately instead of going quiet.
 */
function noteUnresolved(sid: string, op: PendingClearRefresh, userInitiated: boolean, joinedInFlight = false): void {
  // A read that merely JOINED a fetch already running on the shared key was
  // not a real look at the post-clear transcript, so it is not a miss and
  // must not spend the free reads (R3-SPA-SFH-05).
  if (!joinedInFlight) op.unresolvedReads = (op.unresolvedReads ?? 0) + 1
  logDiagnostic('clearRefetchNoMarkerYet', { sessionId: sid, operationId: op.clientMessageId, reads: op.unresolvedReads })
  if (userInitiated) tell("Can't confirm the /clear result yet. It will be checked again after the next reply.")
}

export function armClearRefresh(sid: string, clientMessageId: string, activeTurnId: string | null, knownMarkerIds: string[] = []): void {
  pendingClearRefetches[sid] = {
    clientMessageId,
    turnIdAtArm: activeTurnId,
    armedAt: Date.now(),
    knownMarkerIds,
  }
}

/**
 * Turn-end seam (the frames done/error cases). Inspects the intent BEFORE
 * the caller drains the offline queue — a drained message's send may arm a
 * different operation, and this turn's settle must never consume that one.
 * A turn whose id matches the turn active at arm-time is that turn's OWN
 * done (a /clear queued mid-turn executes only after it ends), so it cannot
 * consume the refresh; the reply turn that follows can.
 */
export function settleClearRefetchAfterTurn(sid: string, doneTurnId: string | undefined, withBucket: WithBucket): void {
  // C1: inspect-and-hold before any drain; flush any projection a previous
  // read held back now that this turn's bucket writes have gone through.
  flushHeldClearProjection(sid, withBucket)
  const intent = pendingClearRefetches[sid]
  if (!intent) return
  // A turn whose id matches the turn active at arm-time is that turn's OWN
  // done (a /clear queued mid-turn executes only after it ends), so it
  // cannot consume the refresh; the reply turn that follows can. Refusals
  // need no special case here any more (ruling S3-02): they arrive as
  // message_status failed in frames.ts, which retires the intent directly,
  // whatever turn id the explanatory reply's done carries.
  if (doneTurnId !== undefined && intent.turnIdAtArm !== null && doneTurnId === intent.turnIdAtArm) return
  if (skipBackedOffRead(sid, intent)) return
  void runClearRefetch(sid, withBucket)
}

/**
 * Applies a projection a previous read held back because the bucket was
 * busy. Called from every busy→idle seam (turn end, replay clear, catch-up
 * completion); a no-op when nothing is held or the bucket is still busy —
 * never abandoned, never re-read.
 */
export function flushHeldClearProjection(sid: string, withBucket: WithBucket): void {
  const intent = pendingClearRefetches[sid]
  if (!intent?.heldProjection) return
  applyProjection(sid, intent, intent.heldProjection, withBucket)
}

/**
 * D5: the recovery control (the screen's history Retry) repeats the WHOLE
 * recovery for the retained operation — a fresh, rejecting read plus
 * application — without resending /clear. No-op when no operation is owed a
 * refresh.
 */
export function retryClearTranscriptRead(sid: string, withBucket: WithBucket): void {
  if (!pendingClearRefetches[sid]) return
  void runClearRefetch(sid, withBucket, true)
}

async function runClearRefetch(sid: string, withBucket: WithBucket, userInitiated = false): Promise<void> {
  // D2: capture the operation BEFORE awaiting anything. A newer /clear
  // replaces the session's pending intent object, which makes this read's
  // response obsolete no matter what it contains.
  const op: PendingClearRefresh | undefined = pendingClearRefetches[sid]
  if (!op) return
  try {
    // C2: a read that rejects on failure — never invalidate-plus-cache-peek.
    // V3: the read runs on the SHARED history key, so its failure surfaces as
    // the screen's own history-error state (with Retry) and its success
    // publishes into the history cache directly.
    // D2 (marker keying): a read that JOINS an already-in-flight fetch on the
    // shared key may be served by data computed before this operation's clear
    // executed — with the old own-user-row correlation gone, there is no way
    // to tell whose marker such a response carries, so it cannot retire this
    // operation (the intent survives and the next opportunity re-reads).
    const joinedInFlight = queryClient.getQueryState(['messages', sid])?.fetchStatus === 'fetching'
    const fresh = await queryClient.fetchQuery({
      queryKey: ['messages', sid],
      queryFn: () => fetchSessionMessages(sid),
      staleTime: 0,
    })
    // D2: obsolete response — the operation that started this read no longer
    // owns the slot (a newer /clear replaced it). Ignore it outright; the
    // newer operation's own settle re-reads.
    if (pendingClearRefetches[sid] !== op) {
      logDiagnostic('clearRefetchSuperseded', { sessionId: sid, operationId: op.clientMessageId })
      return
    }
    applyProjection(sid, op, fresh, withBucket, userInitiated, joinedInFlight)
  } catch (error) {
    // C2/D5: a READ failure is distinct from an application failure. Nothing
    // stale is applied, the diagnostic carries the operation and the error's
    // substance, and the intent survives — the next turn end or the screen's
    // history Retry re-reads.
    const status = typeof (error as { status?: number })?.status === 'number' ? (error as { status: number }).status : undefined
    logDiagnostic('clearRefetchReadFailed', {
      sessionId: sid,
      operationId: op.clientMessageId,
      status,
      message: error instanceof Error ? error.message : String(error),
    })
  }
}

function applyProjection(sid: string, op: PendingClearRefresh, fresh: SessionHistoryPage, withBucket: WithBucket, userInitiated = false, joinedInFlight = false): void {
  // D2, re-keyed ON THE MARKER (founder ruling 2026-10-11): the server's
  // chat-view read starts at the session's LAST /clear marker and
  // deliberately keeps no link between a /clear row and the marker it
  // produced (pkg/session/transcript_view.go::sinceLastClear — a Stop can
  // land entries between the command row and the marker, so "the row just
  // before the marker" is not a reliable link and none is kept). The
  // marker itself, matched by IDENTITY, is therefore the only honest
  // signal: the projection is a post-clear view for this operation only
  // when it carries a marker the view did not already have at arm time,
  // and it was not served by a read that predates the operation (the
  // joinedInFlight guard). Anything else — a queued /clear whose row is
  // still in the view, a refusal (no marker is ever written), a stale read
  // on a previously cleared session whose only markers predate the
  // operation, or a response a still-running earlier fetch served — is not
  // this operation's view: apply NOTHING (the live view is untouched, so
  // local-only rows above the /clear bubble can never vanish — audit D2),
  // and the intent survives for the next opportunity.
  if (joinedInFlight || !projectionCarriesFreshMarker(fresh, op)) {
    noteUnresolved(sid, op, userInitiated, joinedInFlight)
    return
  }
  let deferred = false
  let committed = false
  try {
    withBucket(sid, (b) => {
      // C4: a busy bucket defers — the projection is HELD on the operation
      // (never erased here) and applied at the next busy→idle transition.
      if (b.isStreaming || b.isReplaying) {
        deferred = true
        op.heldProjection = fresh
        return {}
      }
      const merged = mergeServerProjection(b, fresh.entries, op)
      abortHistoryReads(sid)
      const patch = applyHistoryPageToBucket(b, fresh, 'latest')
      committed = true
      return { ...patch, messagesById: merged.messagesById, messageOrder: merged.messageOrder,
        history: { ...patch.history!, viewGeneration: b.history.viewGeneration + 1, openedClearPositions: [] } }
    })
  } catch (error) {
    // D5: an APPLICATION failure is its own phase — the read succeeded, the
    // projection is kept on the operation for the next opportunity.
    op.heldProjection = fresh
    logDiagnostic('clearRefetchApplyFailed', {
      sessionId: sid,
      operationId: op.clientMessageId,
      message: error instanceof Error ? error.message : String(error),
    })
    if (!op.applyFailureShown) {
      op.applyFailureShown = true
      tell("Couldn't refresh this chat after /clear. It will be retried after the next reply.")
    }
    return
  }
  if (deferred) return
  // #1452 D2 A2.4: an authoritative commit voids older full-content reads.
  if (committed) advanceHistoryEntryGenerations(sid, fresh.entries.map(entry => entry.historyPosition))
  // C4: the intent clears only now — the projection carried this
  // operation's marker (a marker unknown at arm time) and it is in the
  // view: the fresh view starts at the divider.
  dropIntent(sid, op)
}

/**
 * D2 (founder ruling 2026-10-11): an intent is retired only by a projection
 * carrying a /clear marker that was NOT in the session's view when the
 * operation was armed. The server's chat view starts at the LAST marker, so
 * a marker unknown at arm time can only be the one this operation's clear
 * produced (a newer /clear replaces the intent outright — the D2 superseded
 * check); every marker known at arm belongs to an earlier clear, and a
 * projection holding only those predates this operation.
 */
function projectionCarriesFreshMarker(fresh: SessionHistoryPage, op: PendingClearRefresh): boolean {
  if (fresh.page_info.mode !== 'latest') return false
  const known = new Set(op.knownMarkerIds ?? [])
  const marker = fresh.page_info.latest_clear
  return !!marker && !known.has(marker.entry_id)
}


/**
 * V1: the view after an operation's projection is applied = the server
 * projection + the LIVE TAIL — every local row positioned AFTER this
 * operation's own /clear user row in the bucket's current order, that the
 * projection does not already contain (matched by server id, else
 * clientMessageId), in local order, whatever its role or delivery status
 * (queued/sending/failed/received/working/done, assistant rows included).
 * Rows at or BEFORE the /clear row are never re-appended: confirmed
 * pre-clear input obeys the server's post-clear window exactly like every
 * other row the projection covers.
 */
export function mergeServerProjection(
  bucket: SessionChatState,
  fresh: Message[],
  op: PendingClearRefresh,
): { messagesById: Record<string, ChatMessage>; messageOrder: string[] } {
  const messagesById: Record<string, ChatMessage> = {}
  const messageOrder: string[] = []
  const projectionIds = new Set<string>()
  const projectionClientMessageIds = new Set<string>()
  for (const row of fresh) {
    if (row.role !== 'user' && row.role !== 'assistant' && row.role !== 'system') continue
    const rowView = row as ChatMessage
    projectionIds.add(row.id)
    if (rowView.clientMessageId) projectionClientMessageIds.add(rowView.clientMessageId)
    messagesById[row.id] = rowView
    messageOrder.push(row.id)
  }
  // The operation's own /clear user row: the optimistic bubble (its id IS
  // the client_message_id) or the server echo (clientMessageId match).
  const clearRowId = findClearRowId(bucket, op.clientMessageId)
  const clearRowIndex = clearRowId ? bucket.messageOrder.indexOf(clearRowId) : -1
  if (clearRowIndex !== -1) {
    for (let i = clearRowIndex + 1; i < bucket.messageOrder.length; i++) {
      const id = bucket.messageOrder[i]
      const local = bucket.messagesById[id]
      if (!local) continue
      if (projectionIds.has(id)) continue
      if (local.clientMessageId && projectionClientMessageIds.has(local.clientMessageId)) continue
      if (local.role === 'user' && projectionClientMessageIds.has(local.id)) continue
      messagesById[id] = local
      messageOrder.push(id)
    }
  } else {
    // C12 (delta re-review, SFH F7): the /clear row could not be found. No
    // concrete path is known to lose it between the send and the projection,
    // but if one ever does, the old behaviour silently dropped every local
    // row from the merged view — including genuinely unconfirmed user input
    // the server has no window for yet. Keep the unconfirmed tail (still
    // sending / failed / queued — isPendingTailMessage) after the server's
    // fresh view instead; it is the same live-tail rule the found-row path
    // applies, minus the rows the /clear row orders.
    for (const id of bucket.messageOrder) {
      const local = bucket.messagesById[id]
      if (!local || !isPendingTailMessage(local)) continue
      if (projectionIds.has(id)) continue
      if (local.clientMessageId && projectionClientMessageIds.has(local.clientMessageId)) continue
      if (local.role === 'user' && projectionClientMessageIds.has(local.id)) continue
      messagesById[id] = local
      messageOrder.push(id)
    }
  }
  return { messagesById, messageOrder }
}
