// provider-frames.ts: provider-messages spec §8 — the three failover-chain
// frames: `provider_retry` (live countdown source), `provider_fallback`
// (live note + retry-slot clear), and `replay_provider_fallback` (the
// persisted note's replay carrier). Extracted into its own handler — the
// catchup-frames.ts pattern: the main switch in
// slices/frames.ts::handleFrame is a grandfathered budget entry, so new
// frame families get their own switch instead of growing it.

import type { StoreApi } from 'zustand'
import { generateId } from '@/lib/constants'
import { logDiagnostic } from '@/lib/telemetry'
import type {
  ProviderFallbackFrame,
  ProviderFallbackNote,
  ProviderRetryFrame,
  ProviderStatusFrame,
} from '@/lib/api/generated/asyncapi-types'
import { getMessages } from '../messages'
import { applyMessageArray } from '../session'
import type {
  ChatMessage,
  ChatStore,
  ProviderRetryEventData,
  ProviderStatusEventData,
  SessionChatState,
} from '../types'

type Frame = Parameters<ChatStore['handleFrame']>[0]

interface ProviderFrameContext {
  frame: Frame
  targetSid: string | null
  get: StoreApi<ChatStore>['getState']
  withBucket: (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void
}

/**
 * §6 fallback-note sentence. The wording source for BOTH frame paths — the
 * live `provider_fallback` note and a replay carrier that arrives without a
 * `message` — so live and reloaded sessions read identically (spec US-6/2,
 * FB-2; Q70 live/replay consistency).
 *
 * Two wordings, keyed to the frame's generated `unavailable_code`:
 * - `model_not_found` (the bad_model hop, pkg/agent/fallback_note.go::
 *   fallbackUnavailableCode): the sentence says plainly that the model was
 *   not found or is not offered by its provider — the same sentence the
 *   backend saves (fallbackNoteText's bad-model branch). The live
 *   `provider_fallback` frame carries the code but no `message` field, so
 *   the sentence necessarily exists in both Go and TS.
 * - every other code (and older frames without the code): the plain §6
 *   "was unavailable" line.
 *
 * Both model names are required by the template; a frame missing either is a
 * contract violation the SPA renders nothing for (no invented copy, no
 * "undefined" text) — logged and dropped.
 */
export function assembleFallbackNoteMessage(
  answeredModel: string | undefined,
  unavailableModel: string | undefined,
  unavailableCode?: ProviderFallbackFrame['unavailable_code'],
): string | null {
  if (typeof answeredModel !== 'string' || answeredModel.length === 0) return null
  if (typeof unavailableModel !== 'string' || unavailableModel.length === 0) return null
  if (unavailableCode === 'model_not_found') {
    return `Answered by the Fallback model (${answeredModel}) because ${unavailableModel} was not found or is not offered by its provider.`
  }
  return `Answered by the Fallback model (${answeredModel}) because ${unavailableModel} was unavailable.`
}

/**
 * The (unavailable, answered) pair key for the D17 once-per-CHAT-per-pair
 * guard. NUL separator — model ids cannot contain NUL, so the join is
 * collision-free where a literal `|` would not be (('ab','c') vs ('a','bc')).
 */
export function fallbackPairKey(unavailableModel: string, answeredModel: string): string {
  return `${unavailableModel}\u0000${answeredModel}`
}

/**
 * Live path: the SPA has no transcript-entry id for a just-received
 * `provider_fallback` frame, so the synthetic note message gets a generated
 * id. The backend emits the frame on EVERY same-pair fallback (a later
 * iteration re-frames — §7.4/D17) but queues the transcript note only when
 * the pair is fresh, so the live view dedupes on the (unavailable, answered)
 * pair (fallbackPairKey) — the store-level half of D17's once-per-CHAT-per-
 * pair rule. A replay seed (below) stops a live duplicate after a load; a
 * reconnect's session_snapshot resets the set, and the persisted note's
 * carrier re-seeds it (spec FB-2/MIN-103/D17).
 */
export function buildFallbackNoteMessage(params: {
  id: string
  timestamp: string
  message: string
  pickNewModelHint?: boolean
}): ChatMessage {
  return {
    id: params.id,
    role: 'system',
    content: params.message,
    timestamp: params.timestamp,
    status: 'done',
    isStreaming: false,
    providerFallbackNote: {
      message: params.message,
      ...(params.pickNewModelHint ? { pickNewModelHint: true } : {}),
    },
  }
}

/**
 * Handle the failover-chain frames. Returns true when the frame was
 * consumed (so the main switch never sees it). Dispatched from
 * handleFrame AFTER handleReplayAndStatusFrame/handleCatchUpFrame return
 * false for these types, so replay bookkeeping order is unchanged.
 */
export function handleProviderFrame({ frame, targetSid, get, withBucket }: ProviderFrameContext): boolean {
  switch (frame.type) {
    case 'provider_retry': {
      if (!targetSid) return true
      // C-11 — the receipt time is captured on the CLIENT clock at reducer
      // entry, before any await/scheduling, so the indicator's
      // serverNow = sent_at + (clientNow − receivedAt) derivation has its
      // anchor. Never compared against sent_at directly.
      const receivedAt = new Date().toISOString()
      const retryFrame = frame as ProviderRetryFrame
      withBucket(targetSid, (b) => {
        const prev = b.providerRetryEvent
        // MIN-104 — the model-switch qualifier applies only when the chain
        // MOVED within one provider (openrouter/model-a → openrouter/model-b).
        // A same-model retry (attempt 2 of the same candidate) and a
        // cross-provider move render the plain provider line; a new turn's
        // first frame has no previous candidate at all.
        const sameTurn = prev != null && prev.turnId === retryFrame.turn_id
        const movedWithinProvider = sameTurn
          && prev.provider === retryFrame.provider
          && prev.model !== retryFrame.model
        const movedAcrossProviders = sameTurn && prev.provider !== retryFrame.provider
        const next: ProviderRetryEventData = {
          turnId: retryFrame.turn_id,
          provider: retryFrame.provider,
          model: retryFrame.model,
          retryAt: retryFrame.retry_at,
          sentAt: retryFrame.sent_at,
          receivedAt,
          attempt: retryFrame.attempt,
          maxAttempts: retryFrame.max_attempts,
          // Issues #1280/#1478 wording round — the cause class the indicator
          // renders in plain English (never the raw code). Optional on the
          // type only for forward/backward compat with frames that pre-date
          // the field; the contract requires it since #1280.
          ...(typeof retryFrame.error_code === 'string' && retryFrame.error_code.length > 0
            ? { errorCode: retryFrame.error_code }
            : {}),
          ...(movedWithinProvider && !movedAcrossProviders
            ? { previousProvider: prev.provider, previousModel: prev.model }
            : {}),
        }
        return { providerRetryEvent: next }
      })
      return true
    }

    case 'provider_status': {
      if (!targetSid) return true
      const statusFrame = frame as ProviderStatusFrame
      // The store manages the notice slot only; the recovered note and the
      // stale retry-line clear are handled here so the reducer stays the one
      // place that owns these slots' invariants.
      if (statusFrame.state === 'waiting') {
        // C-11 — the receipt anchor, captured at reducer entry (same rule as
        // the provider_retry case above).
        const receivedAt = new Date().toISOString()
        withBucket(targetSid, (b) => ({
          providerStatusEvent: {
            state: 'waiting',
            turnId: statusFrame.turn_id,
            silentSince: statusFrame.silent_since ?? statusFrame.sent_at,
            ...(typeof statusFrame.stall_at === 'string' && statusFrame.stall_at.length > 0
              ? { stallAt: statusFrame.stall_at }
              : {}),
            sentAt: statusFrame.sent_at,
            receivedAt,
          } satisfies ProviderStatusEventData,
          // The wait this turn's last provider_retry frame announced has
          // clearly FIRED — a call is running now (silence is measured inside
          // it). A countdown line frozen at "Retrying now…" under the waiting
          // notice would describe nothing, so the same-turn stale slot goes.
          ...(b.providerRetryEvent?.turnId === statusFrame.turn_id ? { providerRetryEvent: null } : {}),
        }))
        return true
      }
      if (statusFrame.state === 'responding') {
        // Data arrives again / the call returned / a retry was scheduled —
        // the waiting notice goes. Deliberately does NOT touch
        // providerRetryEvent: the backend emits responding when a retry is
        // SCHEDULED (provider_status.go::retryScheduled), exactly when the
        // retry line must stay up.
        withBucket(targetSid, () => ({ providerStatusEvent: null }))
        return true
      }
      if (statusFrame.state === 'recovered') {
        // The call succeeded after automatic retries (#1280). The notice
        // goes, a stale "Retrying now…" line cannot survive a success, and
        // the chat gets the short recovered note (docs/troubleshooting.md:
        // "the chat notes that the connection came back and after how many
        // retries").
        const retries = statusFrame.retries
        if (typeof retries !== 'number' || !Number.isFinite(retries) || retries < 1) {
          // Contract violation (retries is what the note exists to say).
          // Render nothing rather than invented copy.
          logDiagnostic('chatProviderStatusSkipped', { sessionId: targetSid, frameState: statusFrame.state })
          withBucket(targetSid, () => ({ providerStatusEvent: null, providerRetryEvent: null }))
          return true
        }
        const note: ChatMessage = {
          id: generateId(),
          role: 'system',
          content: `Connection came back — the model answered after ${retries} ${retries === 1 ? 'retry' : 'retries'}.`,
          timestamp: new Date().toISOString(),
          status: 'done',
          isStreaming: false,
        }
        withBucket(targetSid, (b) => ({
          providerStatusEvent: null,
          providerRetryEvent: null,
          ...applyMessageArray([...getMessages(b), note], b),
        }))
        return true
      }
      // Unknown state (forward compat: a newer backend, or a corrupted
      // frame the WS zod schema somehow passed). Skip + log; render nothing.
      logDiagnostic('chatProviderStatusSkipped', { sessionId: targetSid, frameState: (statusFrame as { state?: string }).state })
      return true
    }

    case 'provider_fallback': {
      if (!targetSid) return true
      const fallbackFrame = frame as ProviderFallbackFrame
      const message = assembleFallbackNoteMessage(
        fallbackFrame.answered_model,
        fallbackFrame.unavailable_model,
        fallbackFrame.unavailable_code,
      )
      if (message == null) {
        // Contract violation (both model names are required by the §6
        // template). Render nothing rather than invented copy.
        logDiagnostic('chatProviderFallbackNoteSkipped', { sessionId: targetSid, turnId: fallbackFrame.turn_id })
        withBucket(targetSid, () => ({ providerRetryEvent: null }))
        return true
      }
      const pickNewModelHint = fallbackFrame.unavailable_code === 'model_retired'
      const note = buildFallbackNoteMessage({
        id: generateId(),
        timestamp: new Date().toISOString(),
        message,
        ...(pickNewModelHint ? { pickNewModelHint } : {}),
      })
      const pairKey = fallbackPairKey(fallbackFrame.unavailable_model, fallbackFrame.answered_model)
      withBucket(targetSid, (b) => {
        // D17 once-per-CHAT-per-pair guard: the backend re-frames every
        // same-pair fallback, so only a pair NOT already noted in this chat
        // (live or replay-seeded) appends a note. The retry-slot clear is
        // deliberately OUTSIDE the guard — every frame still clears it; the
        // guard dedupes the visible note, never the frame.
        if (b.providerFallbackPairsSeen?.has(pairKey)) return { providerRetryEvent: null }
        return {
          providerRetryEvent: null,
          providerFallbackPairsSeen: new Set(b.providerFallbackPairsSeen).add(pairKey),
          ...applyMessageArray([...getMessages(b), note], b),
        }
      })
      return true
    }

    case 'replay_provider_fallback': {
      if (!targetSid) return true
      const noteFrame = frame as ProviderFallbackNote
      // The persisted carrier's `message` is the server-assembled §6 line —
      // the same text the live path assembled from the frame's models, so
      // live and reloaded sessions read identically (spec US-6/2, FB-2).
      const message = typeof noteFrame.message === 'string' && noteFrame.message.length > 0
        ? noteFrame.message
        : assembleFallbackNoteMessage(noteFrame.answered_model, noteFrame.unavailable_model, noteFrame.unavailable_code)
      if (message == null) {
        logDiagnostic('chatProviderFallbackNoteSkipped', { sessionId: targetSid, entryId: noteFrame.entry_id })
        return true
      }
      const pickNewModelHint = noteFrame.unavailable_code === 'model_retired'
      const note = buildFallbackNoteMessage({
        // The transcript entry's own id is the dedup key — stable across
        // every future replay of this session.
        id: noteFrame.entry_id,
        timestamp: noteFrame.timestamp,
        message,
        ...(pickNewModelHint ? { pickNewModelHint } : {}),
      })
      withBucket(targetSid, (b) => {
        // Idempotence guard: the same transcript entry must never push a
        // second row (a gap re-attach that re-reads an already-applied seq
        // range, or a defensive double-delivery).
        if (b.messagesById[noteFrame.entry_id] != null) return {}
        const patch = applyMessageArray([...getMessages(b), note], b)
        // Seed the D17 pair guard so a later live frame for a pair replay
        // already rendered adds no duplicate (dispatch Finding 1: live must
        // start consistent with what replay showed). Carriers that predate
        // model fields (never produced today — buildReplayFallbackNote always
        // sets them) seed nothing rather than guessing a pair.
        if (noteFrame.answered_model != null && noteFrame.unavailable_model != null) {
          return {
            ...patch,
            providerFallbackPairsSeen: new Set(b.providerFallbackPairsSeen).add(
              fallbackPairKey(noteFrame.unavailable_model, noteFrame.answered_model),
            ),
          }
        }
        return patch
      })
      return true
    }

    default:
      void get
      return false
  }
}
