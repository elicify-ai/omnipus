// clear-refetch.ts — FR-030/031 (U10b) SPA half, the transcript re-read owed
// to a /clear (R3, rebuilt after review round 1).
//
// A successful /clear moves the session's context/display window server-side
// and appends ONE chat-view marker entry ("Conversation context cleared",
// type system, role system — the saved server reference
// core-clear-reference-67345b1d7/clear_session.go). Nothing pushes that
// marker live, so the view can only reflect the server by READING the
// transcript. The rules this module implements:
//
//   C1  The refresh intent belongs to the /clear OPERATION, not to "the next
//       done in this session": it is armed when a /clear is actually sent on
//       the wire (every send path — normal, offline-queue drain, Retry/resend
//       — calls armClearRefresh), bound to that send's client_message_id and
//       the turn active at send-time, and inspected BEFORE the done handler
//       can drain another message whose send would arm a different operation.
//   C2  The re-read is queryClient.fetchQuery — a read that REJECTS on
//       failure. On failure nothing stale is applied and the intent survives
//       for the next opportunity (a later turn end).
//   C3  The fetched projection is MERGED into the bucket: server rows are
//       authoritative for what the server has, while unsaved/failed/pending
//       user messages (their Retry state included) and client-only system
//       rows newer than the /clear send survive.
//   C4  A busy bucket (streaming/replaying) defers the application — the
//       fetched projection is held on the intent and applied once the bucket
//       goes idle (the same turn-end seam flushes it). The intent clears only
//       after the post-clear projection, marker included, is applied.
//
// Nothing here clears anything locally, ever: a projection that does not
// contain the marker (the clear has not executed server-side yet, or the
// clear was refused — a refusal writes no marker) is merged honestly, and the
// intent survives so the next turn end re-reads.

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
 * busy. Called from the same turn-end seam; a no-op when nothing is held or
 * the bucket is still busy (never abandoned — the next turn end retries).
 */
export function flushHeldClearProjection(sid: string, withBucket: WithBucket): void {
  const intent = pendingClearRefetches[sid]
  if (!intent?.heldProjection) return
  applyProjection(sid, intent, intent.heldProjection, withBucket)
}

async function runClearRefetch(sid: string, withBucket: WithBucket): Promise<void> {
  try {
    // C2: a read that rejects on failure — never invalidate-plus-cache-peek,
    // which hands back stale cache and hides the failure.
    const fresh = await queryClient.fetchQuery({
      queryKey: ['messages', sid],
      queryFn: () => fetchSessionMessages(sid),
      staleTime: 0,
    })
    const intent = pendingClearRefetches[sid]
    if (!intent) return
    applyProjection(sid, intent, fresh, withBucket)
  } catch (error) {
    // C2/C4: the failure is observable (diagnostic), nothing stale is
    // applied, and the intent survives — the next turn end re-reads.
    logDiagnostic('clearRefetchFailed', {
      sessionId: sid,
      errorName: error instanceof Error ? error.name : 'unknown',
    })
  }
}

function applyProjection(sid: string, intent: PendingClearRefresh, fresh: Message[], withBucket: WithBucket): void {
  let markerApplied = false
  withBucket(sid, (b) => {
    // C4: busy buckets keep their live state; the flush retries at the next
    // turn end rather than abandoning the work.
    if (b.isStreaming || b.isReplaying) {
      intent.heldProjection = fresh
      return {}
    }
    const merged = mergeServerProjection(b, fresh, intent.armedAt)
    markerApplied = merged.hasMarker
    return { messagesById: merged.messagesById, messageOrder: merged.messageOrder }
  })
  if (markerApplied) {
    // C4: the intent clears only now — the post-clear projection, marker
    // included, is in the view.
    delete pendingClearRefetches[sid]
  } else {
    // No marker in what the server returned: the clear has not executed yet
    // (a queued mid-turn /clear) or was refused (no marker is ever written).
    // The view reflects the server either way; the intent survives so the
    // next turn end re-reads once — or whether — the clear lands.
    intent.heldProjection = undefined
    logDiagnostic('clearRefetchNoMarkerYet', { sessionId: sid })
  }
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
  // Unsaved, failed and pending user messages — and their Retry state: a
  // bubble still streaming locally, a failed send, one with delivery still
  // in flight, or a first send the server has not confirmed as saved.
  if (message.isStreaming) return true
  if (message.status === 'error') return true
  if (message.deliveryStatus !== undefined) return true
  if (message.firstSendStatus !== undefined && message.firstSendStatus !== 'saved') return true
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
): { messagesById: Record<string, ChatMessage>; messageOrder: string[]; hasMarker: boolean } {
  const messagesById: Record<string, ChatMessage> = {}
  const messageOrder: string[] = []
  let hasMarker = false
  const serverClientMessageIds = new Set<string>()
  for (const row of fresh) {
    if (row.role !== 'user' && row.role !== 'assistant' && row.role !== 'system') continue
    if (isClearContextMarker(row)) hasMarker = true
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
  return { messagesById, messageOrder, hasMarker }
}
