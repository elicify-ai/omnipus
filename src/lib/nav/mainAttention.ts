/**
 * Attention projection and the shown-commit acknowledgement (FR-012, FR-013, FR-033).
 *
 * Needs me counts mains whose seam says on. Extras, helpers, titles, and an
 * unknown value never create a signal and never an acknowledgement. Only a
 * shown commit of the winning foreground is acknowledged, and only for the
 * integer attention bound the seam reads off the server frame. No server
 * bound means no acknowledgement. A client string is not a bound.
 */
import type { Session } from '@/lib/api'
import { attachAckFields, attentionBoundOfFrame, isMainSession, sessionAttention } from './sessionCoreSeam'

export type Signal = 'on' | 'off' | 'unknown'

export type Projection = { // not-wire-format: in-browser attention projection counted from seam signals for mains only; never serialized to the gateway
  byMainId: Record<string, Signal>
  distinctMainCount: number
  needsMeMainIds: string[]
}

export type AckInput = { // not-wire-format: arguments describing one shown-commit acknowledgement attempt inside the SPA; not the acknowledgement request body
  attemptKind:
    | 'shown-commit'
    | 'prefetch'
    | 'hidden-reconnect'
    | 'replay'
    | 'failed-attach'
    | 'forbidden'
    | 'overtaken'
    | 'late-success'
    | 'late-failure'
  session: Session
  generation: number
  frame: unknown
  newerOutcomeId: number | null
  foreground: { sessionId: string; generation: number; frame: unknown } | null
  viewerId: string
}

export type AckResult = { // not-wire-format: client decision of whether this viewer should acknowledge; the actual acknowledgement uses generated wire types
  acknowledge: boolean
  sessionId?: string
  attentionBound?: number
  fields?: Record<string, unknown>
}

function noAck(): AckResult {
  return { acknowledge: false }
}

export function projectMainAttention(sessions: Session[]): Projection {
  const byMainId: Record<string, Signal> = {}
  for (const session of sessions) {
    if (!isMainSession(session)) continue
    if (Object.prototype.hasOwnProperty.call(byMainId, session.id)) continue
    byMainId[session.id] = sessionAttention(session)
  }
  const needsMeMainIds = Object.keys(byMainId).filter((id) => byMainId[id] === 'on')
  return {
    byMainId,
    distinctMainCount: needsMeMainIds.length,
    needsMeMainIds,
  }
}

export function ackForShownCommit(input: AckInput): AckResult {
  if (input.attemptKind !== 'shown-commit') return noAck()
  if (!isMainSession(input.session)) return noAck()
  if (sessionAttention(input.session) === 'unknown') return noAck()
  const foreground = input.foreground
  if (foreground == null) return noAck()
  const attentionBound = attentionBoundOfFrame(input.frame)
  if (attentionBound === undefined) return noAck()
  if (attentionBoundOfFrame(foreground.frame) !== attentionBound) return noAck()
  if (foreground.sessionId !== input.session.id || foreground.generation !== input.generation) return noAck()
  return {
    acknowledge: true,
    sessionId: input.session.id,
    attentionBound,
    fields: attachAckFields(attentionBound),
  }
}
