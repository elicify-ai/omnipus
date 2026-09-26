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
} from '@/lib/api/generated/asyncapi-types'
import { getMessages } from '../messages'
import { applyMessageArray } from '../session'
import type { ChatMessage, ChatStore, ProviderRetryEventData, SessionChatState } from '../types'

type Frame = Parameters<ChatStore['handleFrame']>[0]

interface ProviderFrameContext {
  frame: Frame
  targetSid: string | null
  get: StoreApi<ChatStore>['getState']
  withBucket: (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void
}

/**
 * §6 exact template — "Answered by the Fallback model ({X}) because {Y} was
 * unavailable." (never "backup"). Both model names are required by the
 * template; a frame missing either is a contract violation the SPA renders
 * nothing for (no invented copy, no "undefined" text) — logged and dropped.
 */
export function assembleFallbackNoteMessage(
  answeredModel: string | undefined,
  unavailableModel: string | undefined,
): string | null {
  if (typeof answeredModel !== 'string' || answeredModel.length === 0) return null
  if (typeof unavailableModel !== 'string' || unavailableModel.length === 0) return null
  return `Answered by the Fallback model (${answeredModel}) because ${unavailableModel} was unavailable.`
}

/**
 * Live path: the SPA has no transcript-entry id for a just-received
 * `provider_fallback` frame, so the synthetic note message gets a generated
 * id. No live→replay dedup key exists — and none is needed: a reconnect's
 * session_snapshot wipes history and the persisted note replays from its
 * carrier, so exactly one instance ever renders (spec FB-2/MIN-103).
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
          ...(movedWithinProvider && !movedAcrossProviders
            ? { previousProvider: prev.provider, previousModel: prev.model }
            : {}),
        }
        return { providerRetryEvent: next }
      })
      return true
    }

    case 'provider_fallback': {
      if (!targetSid) return true
      const fallbackFrame = frame as ProviderFallbackFrame
      const message = assembleFallbackNoteMessage(fallbackFrame.answered_model, fallbackFrame.unavailable_model)
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
      withBucket(targetSid, (b) => ({
        providerRetryEvent: null,
        ...applyMessageArray([...getMessages(b), note], b),
      }))
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
        : assembleFallbackNoteMessage(noteFrame.answered_model, noteFrame.unavailable_model)
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
        return applyMessageArray([...getMessages(b), note], b)
      })
      return true
    }

    default:
      void get
      return false
  }
}
