/**
 * Secret requests — the value-free state behind the secure secret card
 * (omnipus-system-agent K1, K3, K4, K5; FR-061, FR-074, FR-075, FR-079;
 * spec §6.3 row "Secret card", §6.7).
 *
 * Holds what the SERVER said about each request (from the live
 * `secret_request` / `secret_request_resolved` frames, the session snapshot's
 * `secret_requests`, and the REST state read) plus this tab's own
 * value-free phase of an in-flight submit. It never holds a value: the typed
 * values live only in the card's own inputs (SecretRequestCard) and are
 * dropped when the card leaves the screen or reaches a terminal state.
 *
 * Absence is never success: a pending request that a later snapshot no
 * longer lists is marked `checkRequired`, and the card reads its true state
 * from the server instead of assuming anything.
 */
import { create } from 'zustand'
import type {
  SecretRequestFrame,
  SecretRequestResolvedFrame,
  SessionStateSecretRequest,
} from '@/lib/api/generated/asyncapi-types'
import type { SecretRequestState } from '@/lib/api/generated/openapi-types'
import type { SecretSubmitOutcome } from '@/lib/api/secrets'

/** This tab's own phase of a submit (value-free). */
export type SecretLocalPhase = 'submitting' | 'unknown' | 'rejected' // not-wire-format: in-browser submit phase, never sent

export interface SecretRequestEntry { // not-wire-format: the generated server record plus this tab's value-free submit phase
  request: SessionStateSecretRequest
  localPhase?: SecretLocalPhase
  /** Safe, value-free reason for a rejected submit. */
  localMessage?: string
  /** Committed 503 or server snapshot: saved, but continuation is unconfirmed. */
  continuationUnconfirmed?: boolean
  /** A pending request a newer snapshot no longer lists: read its state. */
  checkRequired?: boolean
  /** The person closed a terminal card in this tab. */
  dismissed?: boolean
}

interface SecretRequestStore {
  byId: Record<string, SecretRequestEntry>
  upsertFromFrame: (frame: SecretRequestFrame) => void
  markResolved: (frame: SecretRequestResolvedFrame) => void
  applyState: (state: SecretRequestState, submitOutcome?: SecretSubmitOutcome['kind']) => void
  reconcileSession: (sessionId: string, entries: readonly SessionStateSecretRequest[]) => void
  setLocalPhase: (requestId: string, phase: SecretLocalPhase | undefined, message?: string) => void
  dismiss: (requestId: string) => void
  reset: () => void
}

/** The chat a card renders in: its root session, else its own (K1). */
export function cardSessionId(request: Pick<SessionStateSecretRequest, 'session_id' | 'root_session_id'>): string {
  return request.root_session_id ?? request.session_id
}

export function isTerminalSecretState(state: SessionStateSecretRequest['state']): boolean {
  return state !== 'pending'
}

function fromFrame(frame: SecretRequestFrame): SessionStateSecretRequest {
  const { type: _type, ...rest } = frame
  void _type
  return { ...rest, state: 'pending', updated_at: new Date().toISOString() }
}

export const useSecretRequestStore = create<SecretRequestStore>((set) => ({
  byId: {},

  upsertFromFrame: (frame) =>
    set((s) => {
      const existing = s.byId[frame.request_id]
      // A late duplicate frame never reopens a request already terminal.
      if (existing && isTerminalSecretState(existing.request.state)) return s
      return { byId: { ...s.byId, [frame.request_id]: { ...existing, request: fromFrame(frame) } } }
    }),

  markResolved: (frame) =>
    set((s) => {
      const existing = s.byId[frame.request_id]
      if (!existing) return s
      return {
        byId: {
          ...s.byId,
          [frame.request_id]: {
            request: { ...existing.request, state: frame.state, updated_at: new Date().toISOString() },
            // K3 submitted only confirms storage, not the continuation.
            continuationUnconfirmed: frame.state === 'submitted' ? existing.continuationUnconfirmed : undefined,
          },
        },
      }
    }),

  applyState: (state, submitOutcome) =>
    set((s) => {
      const existing = s.byId[state.request_id]
      if (!existing) return s
      const resolved = isTerminalSecretState(state.state) && state.state !== 'submitted'
      const continuationUnconfirmed = submitOutcome === 'accepted' || resolved
        ? undefined
        : submitOutcome === 'committed-uncertain' || existing.continuationUnconfirmed
      return {
        byId: {
          ...s.byId,
          [state.request_id]: {
            request: {
              ...existing.request,
              state: state.state,
              updated_at: state.updated_at,
              ...(state.reference_names ? { reference_names: state.reference_names } : {}),
            },
            // A state read ends the submit phase, not continuation uncertainty.
            // Only an accepted submit or another terminal outcome resolves it.
            continuationUnconfirmed,
            dismissed: existing.dismissed,
          },
        },
      }
    }),

  reconcileSession: (sessionId, entries) =>
    set((s) => {
      const next = { ...s.byId }
      const listed = new Set<string>()
      for (const request of entries) {
        listed.add(request.request_id)
        const existing = next[request.request_id]
        next[request.request_id] = {
          ...existing,
          request,
          checkRequired: false,
          // M13 / K4: snapshots carry the authoritative delivery fact. An
          // absent flag on a submitted entry means confirmed (or stopped),
          // unlike a K3 resolved frame or K5 GET, which only confirm storage.
          continuationUnconfirmed: request.state === 'submitted'
            ? request.continuation_unconfirmed === true || undefined
            : isTerminalSecretState(request.state)
              ? undefined
              : existing?.continuationUnconfirmed,
          // The server's terminal state ends any local phase of this tab.
          ...(isTerminalSecretState(request.state) ? { localPhase: undefined, localMessage: undefined } : {}),
        }
      }
      for (const [id, entry] of Object.entries(next)) {
        if (listed.has(id)) continue
        if (cardSessionId(entry.request) !== sessionId && entry.request.session_id !== sessionId) continue
        if (entry.request.state === 'pending') next[id] = { ...entry, checkRequired: true }
      }
      return { byId: next }
    }),

  setLocalPhase: (requestId, phase, message) =>
    set((s) => {
      const existing = s.byId[requestId]
      if (!existing) return s
      return { byId: { ...s.byId, [requestId]: { ...existing, localPhase: phase, localMessage: message } } }
    }),

  dismiss: (requestId) =>
    set((s) => {
      const existing = s.byId[requestId]
      if (!existing || existing.continuationUnconfirmed) return s
      return { byId: { ...s.byId, [requestId]: { ...existing, dismissed: true } } }
    }),

  reset: () => set({ byId: {} }),
}))

/**
 * The cards for one chat: every pending request rooted there, then the most
 * recently changed terminal one unless this tab dismissed it (BDD-6.7).
 * A saved-but-unconfirmed warning stays visible even when a newer result
 * arrives; only confirmed success or authoritative resolution clears it.
 */
export function cardsForSession(byId: Record<string, SecretRequestEntry>, sessionId: string | null): SecretRequestEntry[] {
  if (!sessionId) return []
  const mine = Object.values(byId).filter((entry) => cardSessionId(entry.request) === sessionId)
  const pending = mine.filter((entry) => entry.request.state === 'pending')
  const terminal = mine
    .filter((entry) => isTerminalSecretState(entry.request.state))
    .sort((a, b) => a.request.updated_at.localeCompare(b.request.updated_at))
  const newest = terminal[terminal.length - 1]
  const visibleTerminal = terminal.filter((entry) => entry.continuationUnconfirmed || (entry === newest && !entry.dismissed))
  return [...visibleTerminal, ...pending.sort((a, b) => a.request.updated_at.localeCompare(b.request.updated_at))]
}
