/**
 * Shown-commit acknowledgement (FR-013).
 *
 * The initial attach never acknowledges. A catch_up_complete for the winning
 * foreground main asks ackForShownCommit with the server snapshot remembered
 * for that session. The integer bound comes only from attentionBoundOfFrame.
 * No server bound means no acknowledgement and no attach fields. A retry
 * resends the fields captured at the shown commit, never a newer snapshot.
 */
import type { Session } from '@/lib/api'
import type { AttachSessionFrame } from '@/lib/api/generated/asyncapi-types'
import { ackForShownCommit, type AckInput } from '@/lib/nav/mainAttention'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

type ForegroundCommit = {
  sessionId: string
  generation: number
  acked: boolean
  captured: Record<string, unknown> | null
}

let generation = 0
let foreground: ForegroundCommit | null = null
const snapshotBySession = new Map<string, unknown>()

function documentIsHidden(): boolean {
  return typeof document !== 'undefined' && document.hidden === true
}

/** Remember the server snapshot whose attention bound the seam may read later. */
export function noteServerAttentionFrame(sessionId: string, frame: unknown): void {
  snapshotBySession.set(sessionId, frame)
}

/** A successful visible attach becomes the foreground commit. It does not acknowledge. */
export function noteForegroundAttach(sessionId: string): void {
  generation += 1
  foreground = {
    sessionId,
    generation,
    acked: false,
    captured: null,
  }
}

function sendCaptured(sessionId: string, fields: Record<string, unknown>): boolean {
  if (Object.keys(fields).length === 0) return false
  const { connection } = useConnectionStore.getState()
  if (!connection) return false
  const frame = {
    type: 'attach_session' as const,
    session_id: sessionId,
    ...fields,
  } as AttachSessionFrame
  return connection.send(frame)
}

/**
 * catch_up_complete for sessionId. Acknowledges only a shown winning main
 * whose server frame carries an integer bound. Prefetch, hidden reconnect,
 * unknown attention, a non-main, a failed send, a missing bound, and an
 * overtaken attempt do not.
 */
export function acknowledgeShownCatchUp(sessionId: string): void {
  const current = foreground
  if (current && current.sessionId === sessionId && current.acked) return
  if (current && current.sessionId === sessionId && current.captured && !documentIsHidden()) {
    if (sendCaptured(sessionId, current.captured)) current.acked = true
    return
  }

  const hidden = documentIsHidden()
  const activeId = useSessionStore.getState().activeSessionId
  let attemptKind: AckInput['attemptKind'] = 'shown-commit'
  if (hidden) attemptKind = 'prefetch'
  else if (!current || current.sessionId !== sessionId || activeId !== sessionId) attemptKind = 'overtaken'

  const frame = snapshotBySession.get(sessionId) ?? null
  const session = { id: sessionId } as Session
  const result = ackForShownCommit({
    attemptKind,
    session,
    generation: current?.generation ?? 0,
    frame,
    newerOutcomeId: null,
    foreground: current
      ? {
          sessionId: current.sessionId,
          generation: current.generation,
          frame: snapshotBySession.get(current.sessionId) ?? null,
        }
      : null,
    viewerId: 'local',
  })
  if (!result.acknowledge || !current || current.sessionId !== sessionId) return
  current.captured = result.fields ?? {}
  if (sendCaptured(sessionId, current.captured)) current.acked = true
}
