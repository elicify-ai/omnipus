/**
 * Attention projection (FR-012, FR-013, FR-033).
 *
 * Needs me counts mains whose seam says on. Extras, helpers, titles, and an
 * unknown value never create a signal. The shown-commit acknowledgement is
 * NOT decided here: src/store/session/foregroundAck.ts is its single live
 * implementation (M6 — this module's former parallel copy was deleted; the
 * acknowledgement pins live against the live path in
 * src/store/session/foregroundAck.shown-commit.test.ts).
 */
import type { Session } from '@/lib/api'
import { isMainSession, sessionAttention } from './sessionCoreSeam'

export type Signal = 'on' | 'off' | 'unknown'

export type Projection = { // not-wire-format: in-browser attention projection counted from seam signals for mains only; never serialized to the gateway
  byMainId: Record<string, Signal>
  distinctMainCount: number
  needsMeMainIds: string[]
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

