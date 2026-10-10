/**
 * Shown-commit acknowledgement (FR-013).
 *
 * The initial attach never acknowledges. A catch_up_complete for the winning
 * foreground main asks ackForShownCommit with the server snapshot remembered
 * for that session. The integer bound comes only from attentionBoundOfFrame.
 * No server bound means no acknowledgement and no attach fields. A retry
 * resends the fields captured at the shown commit, never a newer snapshot.
 *
 * A shown commit whose session metadata is still unknown (the roster and the
 * session detail have not resolved — the catch-up can beat them on a direct
 * open) is DEFERRED, not dropped: the bound is captured at that shown commit
 * and the acknowledgement is sent once the metadata becomes known, with every
 * guard re-checked at that moment. Unknown metadata is never acknowledged,
 * never guessed, and a deferred ack never recaptures a newer bound.
 */
import type { Session, SessionDetail } from '@/lib/api'
import type { AttachSessionFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import {
  attachAckFields,
  attentionBoundOfFrame,
  isMainSession,
  sessionAttention,
} from '@/lib/nav/sessionCoreSeam'
import { ackForShownCommit, type AckInput } from '@/lib/nav/mainAttention'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

type ForegroundCommit = {
  sessionId: string
  generation: number
  acked: boolean
  captured: Record<string, unknown> | null
}

/** A shown commit waiting for its session's metadata to become known. */
type DeferredAck = {
  sessionId: string
  generation: number
  /** Fields frozen at the shown commit — a newer frame never replaces them. */
  captured: Record<string, unknown>
}

let generation = 0
let foreground: ForegroundCommit | null = null
const snapshotBySession = new Map<string, unknown>()
const deferredAcks = new Map<string, DeferredAck>()
let cacheWatcher: (() => void) | null = null

function documentIsHidden(): boolean {
  return typeof document !== 'undefined' && document.hidden === true
}

/** Remember the server snapshot whose attention bound the seam may read later. */
export function noteServerAttentionFrame(sessionId: string, frame: unknown): void {
  snapshotBySession.set(sessionId, frame)
}

/**
 * The loaded session metadata the SPA already holds for this id: the
 * ['sessions'] list (fetchSessions → rawToSession) the sidebar's roster
 * reads, or the route's validated ['session-detail', id] entry — whichever
 * has resolved. Nothing here when neither has: the caller must treat unknown
 * metadata as no acknowledgement (or a defer), never a guess.
 */
function loadedSession(sessionId: string): Session | null {
  const sessions = queryClient.getQueryData<Session[]>(['sessions'])
  if (Array.isArray(sessions)) {
    const listed = sessions.find((candidate) => candidate?.id === sessionId)
    if (listed) return listed
  }
  const detail = queryClient.getQueryData<SessionDetail>(['session-detail', sessionId])
  return detail?.session?.id === sessionId ? detail.session : null
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
 * overtaken attempt do not. Unknown session METADATA defers instead of
 * dropping: the bound is kept and the ack sent when the metadata is known.
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
  // FR-047: the seam needs the REAL main/attention metadata (type,
  // needs_attention) the SPA loaded for this session — a bare `{ id }` would
  // read as a non-main and never acknowledge. If neither the roster nor the
  // session detail has resolved, the metadata is unknown: freeze the bound
  // this attach returned and defer, so a cold cache cannot lose the ack.
  const session = loadedSession(sessionId)
  if (!session) {
    if (attemptKind === 'shown-commit') deferShownAck(sessionId, current, frame)
    return
  }
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

/**
 * Freeze the shown commit's bound while its session metadata is unknown.
 * Only a genuine shown commit of the foreground with an integer bound
 * defers — no bound means no acknowledgement ever, and a prefetch or
 * overtaken attempt defers nothing.
 */
function deferShownAck(sessionId: string, current: ForegroundCommit | null, frame: unknown): void {
  if (!current || current.sessionId !== sessionId) return
  const bound = attentionBoundOfFrame(frame)
  if (bound === undefined) return
  deferredAcks.set(sessionId, {
    sessionId,
    generation: current.generation,
    captured: attachAckFields(bound),
  })
  ensureDeferredAckWatcher()
}

/** Watch the query cache once, so a deferred ack re-checks when its metadata resolves. */
function ensureDeferredAckWatcher(): void {
  if (cacheWatcher) return
  cacheWatcher = queryClient.getQueryCache().subscribe((event) => {
    const first = (event?.query?.queryKey as readonly unknown[] | undefined)?.[0]
    if (first !== 'sessions' && first !== 'session-detail') return
    for (const entry of [...deferredAcks.values()]) resolveDeferredAck(entry)
  })
}

/**
 * A deferred shown commit's metadata now resolves (or never will). ONE
 * evaluation: keep waiting while the metadata is unknown; otherwise drop the
 * attempt unless EVERY shown-commit guard still holds — same foreground
 * commit and generation, tab visible, still the active chat, still a main
 * with known attention — and then send the ORIGINAL captured fields, never a
 * newer bound.
 */
function resolveDeferredAck(entry: DeferredAck): void {
  const session = loadedSession(entry.sessionId)
  if (!session) return
  deferredAcks.delete(entry.sessionId)
  const current = foreground
  if (!current || current.sessionId !== entry.sessionId || current.generation !== entry.generation) return
  if (current.acked) return
  if (documentIsHidden()) return
  if (useSessionStore.getState().activeSessionId !== entry.sessionId) return
  if (!isMainSession(session)) return
  if (sessionAttention(session) === 'unknown') return
  if (sendCaptured(entry.sessionId, entry.captured)) current.acked = true
}
