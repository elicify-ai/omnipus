/**
 * Above-feed kind label (FR-008, BDD-07.1).
 *
 * FR-008 AMENDED (founder decision 2026-10-11, team-lead dispatch): a main
 * session's header line reads the main's TITLE — "<Agent name> - Main
 * Session" — not the bare kind. The derivation is sessionDisplayTitle's, the
 * single one in the SPA: for a main it ALWAYS derives from the agent's
 * CURRENT display name (team-lead steer 2026-10-12 — the stored title is
 * ignored entirely, so nothing cached at attach time or persisted in the
 * browser survives a rename), and when the roster can supply neither a name
 * nor the agent id, the label is null and the caller renders its honest
 * loading/unavailable presentation (S4-04: never a fabricated "Unknown
 * agent"). Extra chats keep the original rule: "Extra chat", or "Extra chat
 * — title" with an em dash when there is one. Task and helper kinds pass
 * through unchanged.
 */
import type { Session } from '@/lib/api'
import { isMainSession } from './sessionCoreSeam'
import { sessionDisplayTitle } from './sessionDisplayTitle'

const EM_DASH = '—'

/** The fields the label reads. A full Session satisfies this; the workspace descriptor does too once a null title is coerced to ''. */
export interface FeedKindSource { // not-wire-format: SPA-only fields the above-feed kind label reads; a Session satisfies it, but this shape is never a request or response
  id: string
  type: Session['type']
  title: string
  /**
   * The owner agent's CURRENT display name, threaded by the caller from the
   * live agents data (shared ['agents'] cache). Only a main's derived title
   * reads it; when it is missing the derivation falls back to agentId, and
   * with neither it yields null (the caller's honest unavailable state).
   */
  agentName?: string
  /** The owner agent's id — sessionDisplayTitle's server-aligned empty-name fallback for a main. */
  agentId?: string | null
}

export function feedKindLabel(session: FeedKindSource): string | null {
  if (session.type === 'task') return 'Task run'
  if (session.type === 'delegate') return 'Helper'
  if (isMainSession(session)) {
    // FR-008 amendment: the main's title, never the bare kind. A null here
    // means the roster is still loading (or failed with no id to fall back
    // on) — the caller shows nothing rather than a fabricated name.
    return sessionDisplayTitle(session, session.agentName, 'Main chat', session.agentId)
  }
  const title = session.title.trim()
  if (title === '') return 'Extra chat'
  return `Extra chat ${EM_DASH} ${title}`
}
