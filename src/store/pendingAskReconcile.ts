// pendingAskReconcile — askuserquestion-tool-spec v3 US-6 S1/FR-9.
//
// Dedicated reconcile function for AskUserQuestion cards on a WS
// `session_state` reconnect snapshot, mirroring the pattern of
// src/store/toolApproval.ts::reconcileWithSessionState (a single, unit-tested
// function owns the snapshot-vs-local semantics instead of inline handler
// code). Unlike tool approvals, pendingAsk lives per-session inside the chat
// store's session buckets, so this is a PURE function: it computes the
// per-session `pendingAsk` changes and the chat store's `session_state` case
// applies them via `withBucket`.
//
// Semantics:
// - Hydrate: every card in the snapshot is written into its session's bucket
//   (the server copy wins — it may carry newer server-side state, e.g.
//   `auto_resolved` entries added while this tab was disconnected).
// - Clear: a LOCALLY pending card the snapshot no longer lists resolved while
//   we were disconnected (the terminal frame was missed) — clear it so the
//   question zone and composer lock don't stick around for a dead card.
//   Locally TERMINAL cards are never cleared: the collapsed §0.6 record
//   stays in the thread.
// - Race with a live frame: if a live terminal `ask_user_question` frame for
//   the SAME card already landed (local status answered/cancelled), a
//   snapshot still listing that card as pending is stale — answered/cancelled
//   are terminal server-side, a card never returns to pending. Keep the
//   terminal record; do NOT resurrect the question zone.
// - Idempotent: applying the returned changes and reconciling the same
//   snapshot again yields the identical state.

import type { AskUserQuestionCard } from '@/lib/api/generated/asyncapi-types'

/** Structural subset of a chat-store session bucket this function reads. */
export interface PendingAskBucket {
  pendingAsk?: AskUserQuestionCard | null
}

/**
 * Computes the per-session `pendingAsk` updates for a `session_state`
 * snapshot: `card` entries to hydrate, `null` entries to clear. Sessions
 * absent from the result are untouched. Pure — no store access.
 */
export function reconcilePendingAsks(
  snapshotCards: readonly AskUserQuestionCard[],
  sessionsById: Record<string, PendingAskBucket>,
): Record<string, AskUserQuestionCard | null> {
  const changes: Record<string, AskUserQuestionCard | null> = {}
  const snapshotIds = new Set(snapshotCards.map((c) => c.card_id))

  // Hydrate snapshot cards (server copy wins), except over a local terminal
  // record of the SAME card — the live terminal frame won that race.
  for (const card of snapshotCards) {
    const local = sessionsById[card.session_id]?.pendingAsk
    if (local && local.card_id === card.card_id && local.status !== 'pending') continue
    changes[card.session_id] = card
  }

  // Clear locally pending cards the snapshot no longer knows. A session
  // already receiving a hydration is skipped — its stale local card is
  // replaced by the snapshot card, not cleared.
  for (const [sid, bucket] of Object.entries(sessionsById)) {
    if (Object.prototype.hasOwnProperty.call(changes, sid)) continue
    const local = bucket.pendingAsk
    if (local && local.status === 'pending' && !snapshotIds.has(local.card_id)) {
      changes[sid] = null
    }
  }

  return changes
}
