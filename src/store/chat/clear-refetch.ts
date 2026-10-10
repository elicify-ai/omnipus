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
//       carrying THIS operation's marker (the marker following that
//       operation's own user row) has been applied.
//   D2  Operation ownership: the read is keyed by the operation's
//       client_message_id (a newer /clear never shares an older in-flight
//       read), the owning intent is captured before the read is awaited, and
//       an obsolete response — one whose operation has since been replaced —
//       is ignored outright.
//
// Nothing here clears anything locally, ever: a projection that does not
// carry this operation's marker (the clear has not executed server-side yet,
// or the clear was refused — a refusal writes no marker) is merged honestly,
// and the intent survives so the next opportunity re-reads.

import { queryClient } from '@/lib/queryClient'
import { fetchSessionMessages } from '@/lib/api'
import type { Message } from '@/lib/api'
import { logDiagnostic } from '@/lib/telemetry'
import { isClearContextMarker } from '@/lib/clearMarker'
import type { ChatMessage, SessionChatState } from './types'
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
export function armClearRefresh(sid: string, clientMessageId: string, activeTurnId: string | null): void {
  pendingClearRefetches[sid] = {
    clientMessageId,
    turnIdAtArm: activeTurnId,
    armedAt: Date.now(),
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
  void runClearRefetch(sid, withBucket)
}

async function runClearRefetch(sid: string, withBucket: WithBucket): Promise<void> {
  // D2: capture the operation BEFORE awaiting anything. A newer /clear
  // replaces the session's pending intent object, which makes this read's
  // response obsolete no matter what it contains.
  const op: PendingClearRefresh | undefined = pendingClearRefetches[sid]
  if (!op) return
  try {
    // C2: a read that rejects on failure — never invalidate-plus-cache-peek.
    // D2: the query key carries the operation's client_message_id, so a
    // newer /clear's read is a fresh network read and never joins an older
    // operation's still-in-flight request.
    const fresh = await queryClient.fetchQuery({
      queryKey: ['messages', sid, 'clear', op.clientMessageId],
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
    // Keep the screen-facing transcript cache coherent with what the server
    // actually answered (a real read, not a cache peek).
    queryClient.setQueryData(['messages', sid], fresh)
    applyProjection(sid, op, fresh, withBucket)
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

function applyProjection(sid: string, op: PendingClearRefresh, fresh: Message[], withBucket: WithBucket): void {
  let markerApplied = false
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
      const merged = mergeServerProjection(b, fresh, op.armedAt)
      markerApplied = hasOwnMarker(merged, op.clientMessageId)
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
    return
  }
  if (deferred) return
  if (markerApplied) {
    // C4: the intent clears only now — a projection carrying THIS
    // operation's marker (its own user row followed by the marker) is in the
    // view.
    delete pendingClearRefetches[sid]
  } else {
    // No marker for this operation in what the server returned: the clear
    // has not executed yet (a queued mid-turn /clear) or was refused (no
    // marker is ever written). The view reflects the server either way; the
    // intent survives so the next opportunity re-reads once — or whether —
    // the clear lands.
    op.heldProjection = undefined
    logDiagnostic('clearRefetchNoMarkerYet', { sessionId: sid, operationId: op.clientMessageId })
  }
}

/**
 * D2: an intent is retired only by a projection that contains THIS
 * operation's marker — the /clear marker entry that follows THAT
 * operation's own user row (matched by the wire correlation id), never a
 * historical marker from an earlier clear.
 */
function hasOwnMarker(
  merged: { messagesById: Record<string, ChatMessage>; messageOrder: string[] },
  clientMessageId: string,
): boolean {
  const ownRowIndex = merged.messageOrder.findIndex((id) => {
    const row = merged.messagesById[id]
    return row.role === 'user' && (row.clientMessageId ?? row.id) === clientMessageId
  })
  if (ownRowIndex === -1) return false
  return merged.messageOrder.slice(ownRowIndex + 1).some((id) => isClearContextMarker(merged.messagesById[id]))
}

/** The local rows a merge must keep even though the server projection omits them. */
function preserveLocally(message: ChatMessage, armedAt: number): boolean {
  if (message.role === 'system') {
    // Client-only system rows newer than the /clear send (e.g. a /new
    // refusal). Older system rows came from the server's own earlier view —
    // if the projection drops them, that drop is the server speaking.
    const ts = Date.parse(message.timestamp)
    return Number.isNaN(ts) ? true : ts >= armedAt
  }
  if (message.role !== 'user') return false
  // D3: only genuinely UNCONFIRMED input survives. `received`/`working` are
  // durable transcript appends (MessageStatusFrame.yaml) — a confirmed
  // pre-clear row obeys the server's post-clear window like every other row
  // the projection covers. Still-streaming local bubbles, failed sends, and
  // first sends the server has not confirmed as saved are kept.
  if (message.isStreaming) return true
  if (message.status === 'error') return true
  if (message.deliveryStatus === 'queued' || message.deliveryStatus === 'sending' || message.deliveryStatus === 'failed') return true
  const unconfirmedFirstSend = message.firstSendStatus !== undefined &&
    ['sending', 'unconfirmed', 'retrying', 'checking_chat', 'check_failed', 'not_saved'].includes(message.firstSendStatus)
  if (unconfirmedFirstSend) return true
  return false
}

/**
 * C3: server rows are authoritative for what the server has; local rows the
 * server cannot know about survive the merge, appended after the server rows
 * (they happened after it answered).
 */
export function mergeServerProjection(
  bucket: SessionChatState,
  fresh: Message[],
  armedAt: number,
): { messagesById: Record<string, ChatMessage>; messageOrder: string[] } {
  const messagesById: Record<string, ChatMessage> = {}
  const messageOrder: string[] = []
  const serverClientMessageIds = new Set<string>()
  for (const row of fresh) {
    if (row.role !== 'user' && row.role !== 'assistant' && row.role !== 'system') continue
    const rowView = row as ChatMessage
    if (rowView.clientMessageId) serverClientMessageIds.add(rowView.clientMessageId)
    messagesById[row.id] = rowView
    messageOrder.push(row.id)
  }
  const serverIds = new Set(messageOrder)
  for (const id of bucket.messageOrder) {
    if (serverIds.has(id)) continue
    const local = bucket.messagesById[id]
    if (!local) continue
    if (!preserveLocally(local, armedAt)) continue
    // A local user row the server has already confirmed (its
    // client_message_id came back on a server row) is superseded — keeping it
    // would duplicate the message in the view.
    const localKey = local.clientMessageId ?? local.id
    if (local.role === 'user' && (serverClientMessageIds.has(localKey) || serverClientMessageIds.has(local.id))) continue
    messagesById[id] = local
    messageOrder.push(id)
  }
  return { messagesById, messageOrder }
}
