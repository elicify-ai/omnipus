/**
 * Above-feed kind label (FR-008, BDD-07.1).
 *
 * FR-008 AMENDED (founder decision 2026-10-11, team-lead dispatch): a main
 * session's header line reads the main's TITLE — "<Agent name> - Main
 * Session" — not the bare kind. The derivation is sessionDisplayTitle's, the
 * single one in the SPA: the server title when present (core stamps and
 * re-stamps it, 9b9ef5935), otherwise the agent's CURRENT display name, so a
 * rename shows without a reload and nothing is cached here. Extra chats keep
 * the original rule: "Extra chat", or "Extra chat — title" with an em dash
 * when there is one. Task and helper kinds pass through unchanged.
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
   * reads it; undefined resolves to sessionDisplayTitle's "Unknown agent".
   */
  agentName?: string
}

export function feedKindLabel(session: FeedKindSource): string {
  if (session.type === 'task') return 'Task run'
  if (session.type === 'delegate') return 'Helper'
  if (isMainSession(session)) {
    // FR-008 amendment: the main's title, never the bare kind. The
    // 'Main chat' fallback is unreachable (sessionDisplayTitle always
    // returns a title for a main) and only keeps the legacy word if that
    // ever changes.
    return sessionDisplayTitle(session, session.agentName, 'Main chat')
  }
  const title = session.title.trim()
  if (title === '') return 'Extra chat'
  return `Extra chat ${EM_DASH} ${title}`
}
