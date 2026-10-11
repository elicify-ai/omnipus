/**
 * Display title for a session (founder UI feedback 2026-10-11, point 4).
 *
 * A main session must never read "Untitled": the server title wins when
 * present (core stamps "<Agent> - Main Session" server-side and re-stamps on
 * an agent rename); otherwise — an older main, or the window before the
 * server stamp arrives — the SAME fixed string is derived on the client from
 * the agent's CURRENT display name, ASCII hyphen with spaces. The derivation
 * is a pure function of (session, current agent name): callers pass the live
 * agents data on every render, so a rename shows the new name without a
 * reload and nothing here caches a title (team-lead steer, founder ruling
 * 2026-10-11). Non-main sessions keep their caller's existing fallback —
 * core owns auto-titling of user sessions, and this module must not change
 * it.
 */
import type { Session } from '@/lib/api'
import { isMainSession } from './sessionCoreSeam'

/** The fixed tail of a main session's title (" - Main Session" appended to the agent name). */
const MAIN_SUFFIX = ' - Main Session'

/** Used only when the agent's display name cannot be resolved — never "Untitled" on a main. */
const UNKNOWN_AGENT = 'Unknown agent'

export function sessionDisplayTitle(
  session: Pick<Session, 'type' | 'title'>,
  agentName: string | undefined,
  fallback: string,
): string {
  if (session.title.trim() !== '') return session.title
  if (isMainSession(session)) {
    const name = agentName?.trim()
    return `${name && name !== '' ? name : UNKNOWN_AGENT}${MAIN_SUFFIX}`
  }
  return fallback
}
