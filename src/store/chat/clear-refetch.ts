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
//   E1  The intent is BOUNDED. A refused /clear writes no marker, and a read
//       can keep missing the operation's own row; neither may leave the refresh
//       armed forever (every later turn would re-read the whole transcript).
//       After MAX_UNRESOLVED_READS re-reads that settle nothing, the intent is
//       dropped and the user is told. A session that is deleted drops its
//       intent too (forgetClearRefresh). And because the reads only run on
//       turn ends, a /clear the server NEVER settles (the lost-clear case,
//       R1#1) is bounded by wall-clock time as well: CLEAR_CONFIRM_TIMEOUT_MS
//       after the send, the operation is dropped, the user is told, and the
//       row resolves to 'unconfirmed' — no screen change.
//   E2  No branch fails silently. A history Retry that reads but cannot settle
//       the operation, an application failure, and each expiry bound show a
//       toast; a READ failure surfaces as the screen's own history-error state
//       (V3 below — the shared ['messages', sid] query state, pinned by
//       clear-row-settle.test.ts); the row itself resolves on every settle
//       outcome (C1 — refused, discarded, failed, expired, timed out), never
//       left wearing the offline "will be sent automatically" look forever.
//       The diagnostics stay for triage.
//
// Nothing here clears anything locally, ever: a projection that does not
// carry a marker unknown at arm time (the clear has not executed
// server-side yet, or the clear was refused — a refusal writes no marker)
// is applied to NOTHING, and the intent survives so the next opportunity
// re-reads. The screen moves only when the server's own fresh view
// (starting at the marker) says it moved.

import { produce } from 'immer'
import { queryClient } from '@/lib/queryClient'
import { fetchSessionMessages } from '@/lib/api'
import type { Message } from '@/lib/api'
import { logDiagnostic } from '@/lib/telemetry'
import { useUiStore } from '@/store/ui'
import { isClearContextMarker, isClearNotPerformedReply } from '@/lib/clearMarker'
import type { ChatMessage, SessionChatState } from './types'
import { isPendingTailMessage } from './cursor'
import { clearConfirmTimers, pendingClearRefetches, type PendingClearRefresh } from './runtime-state'

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
/** Re-reads allowed to settle an operation before the intent is dropped (E1). */
export const MAX_UNRESOLVED_READS = 3

/**
 * E1/R1#1 (delta re-review, HIGH — SPA side): the reads above only run on
 * turn ends, so a /clear the server never settles (the lost-clear case —
 * stopThenClear pushed the command into a worker that then exited) would
 * leave the row pending forever with no further turn to count reads. This
 * wall-clock bound retires the operation and resolves the row instead. It is
 * comfortably beyond the server's own worst path — pkg/agent's
 * clearStopSettleTimeout is 30 s and the not-performed reply rides the
 * settle — so a slow-but-alive clear is never mistaken for a lost one.
 */
export const CLEAR_CONFIRM_TIMEOUT_MS = 90_000

function tell(message: string): void {
  useUiStore.getState().addToast({ message, variant: 'warning' })
}

/**
 * The one bucket writer the bounded-wait timer resolves rows through,
 * registered once by the chat store (store.ts). This module cannot import
 * the store (store.ts imports this module — an import cycle), which is why
 * every other entry point here takes `withBucket` as a parameter; the timer
 * callback has no caller to receive one from.
 */
let clearRefetchBucketWriter: WithBucket | null = null
export function setClearRefetchBucketWriter(writer: WithBucket): void {
  clearRefetchBucketWriter = writer
}

/** Clears a session's bounded-wait timer (no-op when none is armed). */
function clearConfirmTimer(sid: string): void {
  const timer = clearConfirmTimers[sid]
  if (timer) {
    clearTimeout(timer)
    delete clearConfirmTimers[sid]
  }
}

/**
 * Retires a session's refresh intent: the bounded-wait timer goes with it,
 * and (when `op` is given) only if that operation still owns the slot — a
 * newer /clear's own operation is never touched by an older one's settle.
 */
function dropIntent(sid: string, op?: PendingClearRefresh): void {
  clearConfirmTimer(sid)
  if (op ? pendingClearRefetches[sid] === op : true) delete pendingClearRefetches[sid]
}

/** Drops a session's refresh intent — called when the session itself is deleted. */
export function forgetClearRefresh(sid: string): void {
  dropIntent(sid)
}

/**
 * C1: a /clear the server DISCARDED (stop before delivery) will never write
 * its marker — the transcript re-read it armed would only spin to expiry and
 * then claim the clear was "not confirmed" after the server already answered
 * with the discard. Retires the intent only when it still belongs to THIS
 * send; a newer /clear keeps its own operation. Called from the
 * message_status frame handler.
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
 * C1: resolves the operation's still-queued /clear row to a terminal,
 * honest state ('not-performed' | 'unconfirmed'). Only a QUEUED row moves —
 * a row the discard/failure path already resolved keeps its outcome, and a
 * resolved row never re-enters the pending tail (cursor.ts's
 * PENDING_TAIL_STATUSES is deliberately left at queued/sending/failed).
 */
function resolveClearRow(sid: string, clientMessageId: string, deliveryStatus: 'not-performed' | 'unconfirmed', withBucket: WithBucket): void {
  withBucket(sid, (b) => {
    const rowId = findClearRowId(b, clientMessageId)
    const row = rowId ? b.messagesById[rowId] : undefined
    if (!rowId || !row || row.deliveryStatus !== 'queued') return {}
    return produce(b, (draft) => {
      const target = draft.messagesById[rowId]
      target.deliveryStatus = deliveryStatus
      target.status = 'done'
    }) as Partial<SessionChatState>
  })
}

/**
 * C1: the server's refusal reply ("/clear was not performed: …") is a plain
 * assistant turn — tokens + done (pkg/gateway/webchat_channel.go::Send) — so
 * the settle seam fires for it. A refusal writes NO marker, so without this
 * check the operation would spin MAX_UNRESOLVED_READS full-transcript reads
 * and then tell the user "the server did not confirm your /clear" — false:
 * the server answered, on the screen, right above. Resolves the queued row
 * to 'not-performed' and retires the operation.
 */
function resolveClearRowOnRefusal(sid: string, op: PendingClearRefresh, withBucket: WithBucket): boolean {
  let refused = false
  withBucket(sid, (b) => {
    const rowId = findClearRowId(b, op.clientMessageId)
    const row = rowId ? b.messagesById[rowId] : undefined
    if (!rowId || !row || row.deliveryStatus !== 'queued') return {}
    // The refusal reply renders ABOVE the queued row, not below it: a
    // brand-new assistant bubble is inserted BEFORE the pending tail
    // (frames.ts's resolveTokenBubbleByMessageId step-3 mint routes through
    // insertHistoryMessageId), and replayed history lands there the same
    // way. Scan the segment between the row and the USER row above it —
    // bounded there so an EARLIER /clear's refusal, still on screen further
    // up, can never resolve a newer operation's row.
    const rowIndex = b.messageOrder.indexOf(rowId)
    for (let j = rowIndex - 1; j >= 0; j--) {
      const m = b.messagesById[b.messageOrder[j]]
      if (!m) continue
      if (m.role === 'user') break
      if (isClearNotPerformedReply(m)) {
        refused = true
        break
      }
    }
    return {}
  })
  if (!refused) return false
  resolveClearRow(sid, op.clientMessageId, 'not-performed', withBucket)
  dropIntent(sid, op)
  logDiagnostic('clearRefetchRefused', { sessionId: sid, operationId: op.clientMessageId })
  return true
}

/**
 * R1#1: the bounded wait fired — the server never confirmed (no marker) nor
 * refused (no discard, no failure, no not-performed reply) this /clear.
 * Resolve the row to 'unconfirmed', tell the user once, retire the
 * operation. NO screen change: nothing is wiped and no marker is invented —
 * if the clear did execute after all, the next attach/rebuild shows the
 * server's own fresh view.
 */
function onConfirmTimeout(sid: string): void {
  delete clearConfirmTimers[sid]
  const op = pendingClearRefetches[sid]
  if (!op) return
  if (clearRefetchBucketWriter) resolveClearRow(sid, op.clientMessageId, 'unconfirmed', clearRefetchBucketWriter)
  logDiagnostic('clearRefetchUnconfirmedTimeout', { sessionId: sid, operationId: op.clientMessageId, afterMs: CLEAR_CONFIRM_TIMEOUT_MS })
  tell("The server did not confirm your /clear, so this chat's context may not have been cleared. Send /clear again if you still want a fresh start.")
  dropIntent(sid)
}

/**
 * E1/E2: a re-read that could not settle the operation (no own user row, or no
 * marker for it). Counts toward expiry; at the bound the intent is dropped,
 * the row resolves to 'unconfirmed' (C1 — never pending forever), and the
 * user is told the clear was not confirmed. A user-initiated Retry that
 * still cannot settle says so immediately.
 */
function noteUnresolved(sid: string, op: PendingClearRefresh, userInitiated: boolean, withBucket: WithBucket): void {
  op.unresolvedReads = (op.unresolvedReads ?? 0) + 1
  if (op.unresolvedReads >= MAX_UNRESOLVED_READS) {
    dropIntent(sid, op)
    resolveClearRow(sid, op.clientMessageId, 'unconfirmed', withBucket)
    logDiagnostic('clearRefetchExpired', { sessionId: sid, operationId: op.clientMessageId, reads: op.unresolvedReads })
    tell("The server did not confirm your /clear, so this chat's context may not have been cleared. Send /clear again if you still want a fresh start.")
    return
  }
  if (userInitiated) tell("Can't confirm the /clear result yet. It will be checked again after the next reply.")
}

export function armClearRefresh(sid: string, clientMessageId: string, activeTurnId: string | null, knownMarkerIds: string[] = []): void {
  // A replaced operation's bound never fires on the newer row: one timer
  // slot per session, re-armed here.
  clearConfirmTimer(sid)
  pendingClearRefetches[sid] = {
    clientMessageId,
    turnIdAtArm: activeTurnId,
    armedAt: Date.now(),
    knownMarkerIds,
  }
  clearConfirmTimers[sid] = setTimeout(() => onConfirmTimeout(sid), CLEAR_CONFIRM_TIMEOUT_MS)
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
  // C1: the refusal check runs BEFORE the turnIdAtArm early return below —
  // the not-performed reply's done can carry the STOPPED turn's id
  // (pkg/gateway/webchat_channel.go::Send reuses the recorded turn identity
  // for the fallback delivery), which would otherwise skip this settle
  // entirely and leave the row queued forever behind a reply that is already
  // on screen.
  if (resolveClearRowOnRefusal(sid, intent, withBucket)) return
  if (doneTurnId !== undefined && intent.turnIdAtArm !== null && doneTurnId === intent.turnIdAtArm) return
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

function applyProjection(sid: string, op: PendingClearRefresh, fresh: Message[], withBucket: WithBucket, userInitiated = false, joinedInFlight = false): void {
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
    logDiagnostic('clearRefetchNoMarkerYet', { sessionId: sid, operationId: op.clientMessageId })
    noteUnresolved(sid, op, userInitiated, withBucket)
    return
  }
  let deferred = false
  try {
    withBucket(sid, (b) => {
      // C4: a busy bucket defers — the projection is HELD on the operation
      // (never erased here) and applied at the next busy→idle transition.
      if (b.isStreaming || b.isReplaying) {
        deferred = true
        op.heldProjection = fresh
        return {}
      }
      const merged = mergeServerProjection(b, fresh, op)
      return { messagesById: merged.messagesById, messageOrder: merged.messageOrder }
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
function projectionCarriesFreshMarker(fresh: Message[], op: PendingClearRefresh): boolean {
  const known = new Set(op.knownMarkerIds ?? [])
  return fresh.some((row) => isClearContextMarker(row) && !known.has(row.id))
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
