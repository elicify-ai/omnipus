/**
 * Attention projection and the shown-commit acknowledgement (FR-012, FR-013, FR-033).
 *
 * Needs me counts mains whose seam says on. Extras, helpers, titles, and an
 * unknown value never create a signal and never an acknowledgement. Only a
 * shown commit of the winning foreground bound is acknowledged, and only for
 * that observed bound.
 */
import type { Session } from '@/lib/api'
import { attachAckFields, isMainSession, sessionAttention } from './sessionCoreSeam'

export type Signal = 'on' | 'off' | 'unknown'

export type Projection = {
  byMainId: Record<string, Signal>
  distinctMainCount: number
  needsMeMainIds: string[]
}

export type AckInput = {
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
  observedBound: string | null
  newerOutcomeId: string | null
  foreground: { sessionId: string; generation: number; observedBound: string } | null
  viewerId: string
}

export type AckResult = {
  acknowledge: boolean
  sessionId?: string
  observedBound?: string
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

function isWinningShownCommit(input: AckInput): input is AckInput & { observedBound: string } {
  if (input.attemptKind !== 'shown-commit') return false
  if (!isMainSession(input.session)) return false
  if (sessionAttention(input.session) === 'unknown') return false
  const foreground = input.foreground
  if (foreground == null) return false
  if (input.observedBound == null || input.observedBound === '') return false
  return (
    foreground.sessionId === input.session.id
    && foreground.generation === input.generation
    && foreground.observedBound === input.observedBound
  )
}

export function ackForShownCommit(input: AckInput): AckResult {
  if (!isWinningShownCommit(input)) return noAck()
  const sessionId = input.session.id
  const observedBound = input.observedBound
  return {
    acknowledge: true,
    sessionId,
    observedBound,
    fields: attachAckFields({ sessionId, observedBound }),
  }
}
