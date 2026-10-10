/**
 * Shown-commit acknowledgement (FR-013, FR-047; spec BDD-13.2 and the
 * "No-write / unresolved sources" row ~L191).
 *
 * The initial attach never acknowledges. One explicit pending record per
 * SHOWN COMMIT carries everything the acknowledgement needs:
 *
 *   { sessionId, generation, bound, sent }
 *
 * I1. The record is created exactly once, at the shown commit (noteForegroundAttach),
 *     and the bound is frozen from that attach's SessionStateFrame the first
 *     time a completion observes one.
 * I2. The bound is NEVER replaced — not by a later session_state, a reconnect,
 *     a retry, or another completion. A newer shown commit creates its own new
 *     record; the old one is dropped.
 * I3. EVERY send attempt (initial, deferred-on-metadata, retry-after-failure)
 *     re-checks ALL guards at that moment: same foreground session and
 *     generation (structural — the record belongs to the current commit), tab
 *     visible, still the active chat, isMainSession, attention known. The
 *     record waits through temporary guards; a non-main or a replaced commit
 *     can never send.
 * I4. A FAILED send keeps the record for retry. Only a successful send ends
 *     it (a replaced commit drops it with the commit).
 * I5. Unknown metadata or unknown attention ⇒ no send (wait for a cache
 *     event). Never acknowledge on guesses, never invent a session.
 *
 * Final refinements (round 5): the bound is captured ONLY from the first
 * completion of THIS open's attach answer — a completion without a number
 * closes the record as boundless forever (R1); a permanent guard failure
 * (user moved on, non-main) DROPS the record instead of pausing it (R2);
 * a late completion from an OLDER open of the same session never touches
 * the current record (R3).
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
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

/** The one pending shown commit. Replaced wholesale by the next attach. */
type ShownCommit = {
  sessionId: string
  generation: number
  /** True once THIS open's attach answer began (its snapshot/session_state was noted). */
  answerSeen: boolean
  /** True after the first completion of THIS open — the one capture opportunity (R1). */
  boundClosed: boolean
  /** Frozen at that first own completion; null + boundClosed ⇒ this open never acknowledges (R1). */
  bound: number | null
  /** True only after a successful send (I4). */
  sent: boolean
}

let generation = 0
let commit: ShownCommit | null = null
const snapshotBySession = new Map<string, unknown>()
let cacheWatcher: (() => void) | null = null

function documentIsHidden(): boolean {
  return typeof document !== 'undefined' && document.hidden === true
}

/**
 * Remember a server frame of THIS open's attach answer. Both the full
 * snapshot and the session_state mark that the answer began — a completion
 * that arrives before any answer frame belongs to an OLDER open of the same
 * session and must never touch the current record (R3).
 */
export function noteServerAttentionFrame(sessionId: string, frame: unknown): void {
  snapshotBySession.set(sessionId, frame)
  if (commit && commit.sessionId === sessionId) commit.answerSeen = true
}

/**
 * The loaded session metadata the SPA already holds for this id: the
 * ['sessions'] list (fetchSessions → rawToSession) the sidebar's roster
 * reads, or the route's validated ['session-detail', id] entry — whichever
 * has resolved. Nothing here when neither has: the caller treats unknown
 * metadata as "wait", never a guess.
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

/** A successful visible attach becomes the shown commit. It does not acknowledge. */
export function noteForegroundAttach(sessionId: string): void {
  generation += 1
  commit = { sessionId, generation, answerSeen: false, boundClosed: false, bound: null, sent: false }
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
 * Watch the query cache once, so a pending acknowledgement re-checks when its
 * session's metadata (or the roster holding it) resolves.
 */
function ensurePendingAckWatcher(): void {
  if (cacheWatcher) return
  cacheWatcher = queryClient.getQueryCache().subscribe((event) => {
    const first = (event?.query?.queryKey as readonly unknown[] | undefined)?.[0]
    if (first !== 'sessions' && first !== 'session-detail') return
    if (commit && !commit.sent && commit.bound !== null) {
      acknowledgeShownCatchUp(commit.sessionId)
    }
  })
}

/**
 * catch_up_complete for sessionId — the shown commit's send attempt. Every
 * attempt re-checks every guard (I3); a failed send keeps the pending record
 * (I4); unknown metadata or attention waits for a cache event (I5). Prefetch,
 * hidden reconnect, a non-main, a missing bound, and an overtaken attempt
 * never send.
 */
export function acknowledgeShownCatchUp(sessionId: string): void {
  const current = commit
  // Same foreground session AND generation, structurally: the record belongs
  // to the current shown commit, and a newer attach replaced it wholesale (I2).
  if (!current || current.sessionId !== sessionId || current.sent) return
  // Tab visible (I3): a hidden completion is a prefetch, never a send.
  if (documentIsHidden()) return
  // R3: no answer frame of THIS open has been noted yet. If an older frame
  // for this session exists (noted before this open began), this completion
  // belongs to that OLDER open — ignore it entirely, never touch the record.
  // If NO frame exists at all, this open completed without any answer: close
  // it as boundless (R1) — it can never acknowledge.
  if (!current.answerSeen) {
    if (snapshotBySession.has(sessionId)) return
    current.boundClosed = true
    return
  }
  // R2: the user moved to another chat — a permanent guard failure. Drop the
  // record now; only a NEW foreground open may acknowledge again.
  if (useSessionStore.getState().activeSessionId !== sessionId) {
    commit = null
    return
  }

  // R1: exactly ONE capture opportunity — the first completion of THIS open.
  // A completion without a usable number closes the record as boundless: no
  // later reconnect, completion, or cache event ever fills it. Only a new
  // foreground open (a new record) can acknowledge its own number.
  if (!current.boundClosed) {
    current.bound = attentionBoundOfFrame(snapshotBySession.get(sessionId)) ?? null
    current.boundClosed = true
  }
  if (current.bound === null) return

  // Unknown metadata waits (I5): the roster or the session detail may still
  // resolve; the watcher retries on the next cache event.
  const session = loadedSession(sessionId)
  if (!session) {
    ensurePendingAckWatcher()
    return
  }
  // R2: a non-main is a permanent classification mismatch — drop the record.
  if (!isMainSession(session)) {
    commit = null
    return
  }
  // Unknown attention waits (I5): a roster refresh may make it known.
  if (sessionAttention(session) === 'unknown') {
    ensurePendingAckWatcher()
    return
  }

  // Send the frozen bound. A failed send keeps the record (I4); the next
  // completion or cache event retries through these same guards.
  if (sendCaptured(sessionId, attachAckFields(current.bound))) current.sent = true
}
