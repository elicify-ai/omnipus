/**
 * Display title for a session (founder UI feedback 2026-10-11, point 4;
 * honest-unavailable amendment + team-lead steer 2026-10-12 — combined
 * review S4-04/S2-05).
 *
 * For a MAIN session the stored title is IGNORED ENTIRELY (architect ruling
 * via the chief): the SPA ALWAYS derives "<Agent name> - Main Session" from
 * the LIVE agent list, so no title copied at attach time — or persisted in
 * the browser — can ever survive an agent rename. The derivation is
 * byte-identical to what the server shows on a fresh read
 * (pkg/gateway/session_title.go::withDisplayTitle resolves the agent's
 * CURRENT name and pkg/session/auto_title.go::MainSessionTitle appends
 * " - Main Session"), so deriving is preferring the fresh server title.
 *
 * On a main, in order:
 *   1. The agent's CURRENT display name from the live roster.
 *   2. The AGENT ID — exactly the server's own empty-name fallback
 *      (withDisplayTitle falls back to m.AgentID when the name does not
 *      resolve). Honest and stable, never a fabricated placeholder word
 *      ("Unknown agent" is gone).
 *   3. null — there is neither a name nor an id: nothing honest to show.
 *      The caller renders its existing loading or unavailable presentation
 *      (the sessionCoreSeam rule: a missing value is unavailable, never
 *      guessed).
 *
 * Non-main sessions keep the stored title and their caller's existing
 * fallback — core owns auto-titling of user sessions, and this module must
 * not change it.
 *
 * The derivation is a pure function of (session, current agent name, agent
 * id): callers pass the live agents data on every render, so a rename shows
 * the new name without a reload and nothing here caches a title (team-lead
 * steer, founder ruling 2026-10-11).
 */
import type { Session } from '@/lib/api'
import { isMainSession } from './sessionCoreSeam'

/** The fixed tail of a main session's title (" - Main Session" appended to the agent name). */
const MAIN_SUFFIX = ' - Main Session'

export function sessionDisplayTitle(
  session: Pick<Session, 'type' | 'title'>,
  agentName: string | undefined,
  fallback: string,
  /** The owner agent's id — the server's own empty-name fallback for a main. */
  agentId?: string | null,
): string | null {
  if (isMainSession(session)) {
    // Steer 2026-10-12: session.title is not read at all on a main —
    // a stored copy can predate a rename, and the live derivation IS the
    // fresh server title.
    const name = agentName?.trim()
    if (name) return `${name}${MAIN_SUFFIX}`
    const id = agentId?.trim()
    if (id) return `${id}${MAIN_SUFFIX}`
    return null
  }
  if (session.title.trim() !== '') return session.title
  return fallback
}
