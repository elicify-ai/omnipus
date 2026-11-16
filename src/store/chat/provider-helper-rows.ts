// provider-helper-rows.ts: #1537 (+ rider #1850) — the main chat's named
// helper rows and the per-chat server-clock estimate.
//
// helper-model-notices spec: a helper's `provider_retry` / `provider_status`
// frame is published twice — to the helper's own chat (no identity fields,
// handled exactly like any chat's own notice) and to the top chat with
// `child_session_id` + `agent_id` set. Only the top-chat copy lands here.
// Each helper TURN (helper chat + turn id) gets its own row, so a helper's
// notice never replaces, clears or restarts the chat's own notice slots or
// another helper's row (FR-007, the #1850 fix).

import { logDiagnostic } from '@/lib/telemetry'
import type { ProviderRetryFrame, ProviderStatusFrame } from '@/lib/api/generated/asyncapi-types'
import type {
  ProviderHelperNotice,
  ProviderHelperRow,
  ProviderRetryEventData,
  ProviderServerClock,
  ProviderStatusEventData,
  SessionChatState,
} from './types'

export type ProviderFrameIdentity =
  | { kind: 'own' }
  | { kind: 'helper'; childSessionId: string; agentId: string }
  | { kind: 'malformed' }

/**
 * Wire rule (contract `ProviderRetryFrame` / `ProviderStatusFrame`):
 * `child_session_id` and `agent_id` are both present or both absent. A frame
 * with neither is about the chat's own turn; a frame with only one (or an
 * empty value) is a contract violation the caller logs and ignores — it is
 * never treated as an own frame.
 */
export function providerFrameIdentity(frame: { child_session_id?: unknown; agent_id?: unknown }): ProviderFrameIdentity {
  const child = frame.child_session_id
  const agent = frame.agent_id
  if (child == null && agent == null) return { kind: 'own' }
  if (typeof child === 'string' && child.length > 0 && typeof agent === 'string' && agent.length > 0) {
    return { kind: 'helper', childSessionId: child, agentId: agent }
  }
  return { kind: 'malformed' }
}

/** Row key: helper chat + helper turn. NUL cannot occur in either id. */
export function helperRowKey(childSessionId: string, turnId: string): string {
  return `${childSessionId}\u0000${turnId}`
}

/** JavaScript Date's limit: |ms| above this is an Invalid Date. */
const DATE_LIMIT_MS = 8_640_000_000_000_000

function isDateMs(ms: number): boolean {
  return Number.isFinite(ms) && Math.abs(ms) <= DATE_LIMIT_MS
}

/**
 * FR-008 (#1537 Q12 A, S1) — the server-time sample of one `session_state`.
 * The gateway stamps `emitted_at_ms` and `emitted_at` from one clock reading,
 * `emitted_at` truncated to whole seconds. So `emitted_at_ms` is used only
 * when it lies in that second, [emitted_at, emitted_at + 1000 ms); any other
 * value (or an unparseable `emitted_at`) falls back to `emitted_at`.
 */
export function sessionStateClockSample(
  emittedAt: string | undefined,
  emittedAtMs: number | undefined,
): string | number | undefined {
  if (typeof emittedAtMs !== 'number') return emittedAt
  const textMs = typeof emittedAt === 'string' ? Date.parse(emittedAt) : Number.NaN
  if (!Number.isFinite(textMs)) return emittedAt
  const secondStartMs = Math.floor(textMs / 1000) * 1000
  return emittedAtMs >= secondStartMs && emittedAtMs < secondStartMs + 1000 ? emittedAtMs : emittedAt
}

/**
 * FR-008 — fold one server-time sample (a frame's `sent_at`, or a
 * `session_state` sample from `sessionStateClockSample`; received at client
 * time `clientMs`) into the chat's estimate. Keeps the larger
 * server-minus-client offset, so the estimate never moves backwards. A
 * sample or client time that is not a valid Date (#1537 S1) changes nothing.
 */
export function noteServerClock(
  clock: ProviderServerClock | null | undefined,
  serverSample: string | number | undefined,
  clientMs: number,
): ProviderServerClock | null {
  const current = clock ?? null
  if (typeof serverSample !== 'string' && typeof serverSample !== 'number') return current
  const serverMs = typeof serverSample === 'number' ? serverSample : Date.parse(serverSample)
  if (!isDateMs(serverMs) || !isDateMs(clientMs)) return current
  if (current && current.serverMs - current.clientMs >= serverMs - clientMs) return current
  return { serverMs, clientMs }
}

/**
 * The render facts of one `provider_retry` frame. `prev` is the previous
 * retry of the same slot (own slot or one helper row): the MIN-104
 * model-switch qualifier applies only when the chain moved within one
 * provider in the same turn.
 */
export function buildRetryEventData(
  retryFrame: ProviderRetryFrame,
  prev: ProviderRetryEventData | null | undefined,
  receivedAt: string,
): ProviderRetryEventData {
  const sameTurn = prev != null && prev.turnId === retryFrame.turn_id
  const movedWithinProvider = sameTurn && prev.provider === retryFrame.provider && prev.model !== retryFrame.model
  const movedAcrossProviders = sameTurn && prev.provider !== retryFrame.provider
  return {
    turnId: retryFrame.turn_id,
    provider: retryFrame.provider,
    model: retryFrame.model,
    retryAt: retryFrame.retry_at,
    sentAt: retryFrame.sent_at,
    receivedAt,
    attempt: retryFrame.attempt,
    maxAttempts: retryFrame.max_attempts,
    ...(typeof retryFrame.error_code === 'string' && retryFrame.error_code.length > 0
      ? { errorCode: retryFrame.error_code }
      : {}),
    ...(movedWithinProvider && !movedAcrossProviders
      ? { previousProvider: prev.provider, previousModel: prev.model }
      : {}),
  }
}

/** The render facts of one `provider_status` `waiting` frame. */
export function buildWaitingEventData(statusFrame: ProviderStatusFrame, receivedAt: string): ProviderStatusEventData {
  return {
    state: 'waiting',
    turnId: statusFrame.turn_id,
    silentSince: statusFrame.silent_since ?? statusFrame.sent_at,
    ...(typeof statusFrame.stall_at === 'string' && statusFrame.stall_at.length > 0
      ? { stallAt: statusFrame.stall_at }
      : {}),
    ...(typeof statusFrame.attempt === 'number' && typeof statusFrame.max_attempts === 'number'
      ? { attempt: statusFrame.attempt, maxAttempts: statusFrame.max_attempts }
      : {}),
    sentAt: statusFrame.sent_at,
    receivedAt,
  }
}

type HelperIdentity = Extract<ProviderFrameIdentity, { kind: 'helper' }>

/**
 * Set (or with `notice: undefined`, remove) the row of one helper turn. A
 * new turn's row is appended: array order is first-notice order (FR-009).
 * While an attach is open, the key is recorded as delivered (FR-014).
 */
function writeRow(
  b: SessionChatState,
  identity: HelperIdentity,
  turnId: string,
  notice: ProviderHelperNotice | null | undefined,
): Partial<SessionChatState> {
  const key = helperRowKey(identity.childSessionId, turnId)
  const rows = b.providerHelperRows ?? []
  const index = rows.findIndex((row) => row.key === key)
  let nextRows: ProviderHelperRow[]
  if (notice === undefined) {
    nextRows = index === -1 ? rows : rows.filter((_, i) => i !== index)
  } else {
    const row: ProviderHelperRow = { key, childSessionId: identity.childSessionId, agentId: identity.agentId, turnId, notice }
    nextRows = index === -1 ? [...rows, row] : rows.map((existing, i) => (i === index ? row : existing))
  }
  const attachKeys = b.providerHelperAttachKeys
  return {
    providerHelperRows: nextRows,
    ...(attachKeys != null && notice !== undefined && !attachKeys.includes(key)
      ? { providerHelperAttachKeys: [...attachKeys, key] }
      : {}),
  }
}

/** A helper's top-chat `provider_retry` copy: the row shows the countdown. */
export function reduceHelperRetry(
  b: SessionChatState,
  retryFrame: ProviderRetryFrame,
  identity: HelperIdentity,
  receivedAt: string,
): Partial<SessionChatState> {
  const key = helperRowKey(identity.childSessionId, retryFrame.turn_id)
  const prevNotice = b.providerHelperRows?.find((row) => row.key === key)?.notice
  const prev = prevNotice?.kind === 'retry' ? prevNotice.retry : null
  return writeRow(b, identity, retryFrame.turn_id, { kind: 'retry', retry: buildRetryEventData(retryFrame, prev, receivedAt) })
}

/**
 * A helper's top-chat `provider_status` copy (FR-006):
 * - `waiting`: the row shows the silence notice;
 * - `responding`: the helper's model answered — the row clears (hidden,
 *   keeps its first-notice position for a later notice of the same turn);
 * - `recovered`: the row shows the named recovered line; no system message
 *   is added to the main chat;
 * - `ended`: that helper turn is over — its row (and only its row) goes.
 */
export function reduceHelperStatus(
  b: SessionChatState,
  statusFrame: ProviderStatusFrame,
  identity: HelperIdentity,
  receivedAt: string,
  sessionId: string,
): Partial<SessionChatState> {
  const turnId = statusFrame.turn_id
  switch (statusFrame.state) {
    case 'waiting':
      return writeRow(b, identity, turnId, { kind: 'waiting', waiting: buildWaitingEventData(statusFrame, receivedAt) })
    case 'responding':
      return writeRow(b, identity, turnId, null)
    case 'recovered': {
      const retries = statusFrame.retries
      if (typeof retries !== 'number' || !Number.isFinite(retries) || retries < 1) {
        // Contract violation (retries is what the line exists to say):
        // render nothing rather than invented copy.
        logDiagnostic('chatProviderStatusSkipped', { sessionId, frameState: statusFrame.state, helper: true })
        return writeRow(b, identity, turnId, null)
      }
      return writeRow(b, identity, turnId, { kind: 'recovered', retries })
    }
    case 'ended':
      return writeRow(b, identity, turnId, undefined)
    default:
      logDiagnostic('chatProviderStatusSkipped', { sessionId, frameState: (statusFrame as { state?: string }).state, helper: true })
      return {}
  }
}

/** FR-014 — an attach's `session_state` opens a new delivered-key window. */
export function beginHelperAttach(): Partial<SessionChatState> {
  return { providerHelperAttachKeys: [] }
}

/**
 * FR-014 — at a successful `catch_up_complete`: a full snapshot or
 * `live_only` attach replaces the named rows with exactly those its
 * projection delivered; an incremental attach keeps relying on replayed
 * `ended` frames. Either way the window closes. Superseded completions never
 * reach the reducer (history-replay drops them), and a failed attach never
 * completes, so neither changes the rows.
 */
export function completeHelperAttach(b: SessionChatState, mode: string | undefined): Partial<SessionChatState> {
  const delivered = b.providerHelperAttachKeys
  if (delivered == null) return {}
  if (mode !== 'snapshot' && mode !== 'live_only') return { providerHelperAttachKeys: null }
  const rows = b.providerHelperRows ?? []
  const kept = rows.filter((row) => delivered.includes(row.key))
  return { providerHelperAttachKeys: null, ...(kept.length !== rows.length ? { providerHelperRows: kept } : {}) }
}
