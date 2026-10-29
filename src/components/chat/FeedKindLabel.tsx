/**
 * The chat's kind label, rendered in the workspace header line's right-end
 * slot (FR-008, BDD-07.1 — mains as amended by the founder decision
 * 2026-10-11: a main's header line reads the main's title "<Agent name> -
 * Main Session", not the bare "Main chat" kind).
 *
 * #1449 D13 (founder 2026-10-28): the grey bar this used to render above the
 * feed — with its background and its bottom border — is GONE. The label now
 * renders as small muted text inside ChatHeaderSlot, in the same header line
 * as the workspace strip. The role, the accessible name and the test hook are
 * unchanged, so the line still reads the same to a screen reader.
 *
 * Two details the narrow header line forced:
 *   - the identity truncates, so its title carries the FULL text (the
 *     breadcrumb, the main title, and the honest-failure warning). It is also
 *     the one part of the slot the workspace strip may take back
 *     (`data-header-yield`, read by useWorkspaceHeaderMode): the label is a
 *     grid whose identity track is minmax(0, 1fr), so the identity's width is
 *     not part of the slot's automatic minimum and the header line shrinks the
 *     identity, never the strip, the warning or the participant control. A
 *     flex row of the same items kept the whole text width as that minimum, so
 *     the slot could not give any width back;
 *   - the honest-failure warning is a SEPARATE element outside the
 *     truncating identity, so a clipped identity can never take the warning
 *     with it, and it is a COMPACT glyph rather than the sentence: the
 *     header slot is capped, so the sentence could not fit beside the
 *     identity and the participant control. The glyph's explanation lives in
 *     the catalogued Tooltip — a named, focusable trigger that opens on
 *     focus and on a tap and closes on Escape — and the status region still
 *     names both parts. The trigger is a real icon button, so a finger gets
 *     the token-sized touch box; the visible glyph stays 14px.
 *
 * Words come from feedKindLabel on the workspace descriptor, and only when
 * that descriptor is the active session. Replay does not change them.
 * A status role takes its accessible name from the author, not from its
 * text, so the visible line is the name via aria-labelledby.
 * When no descriptor matches, a task session keeps the previous "Task:" line
 * so a transcript opened without a workspace pointer still says it is a task.
 *
 * The agent name is resolved from the SHARED ['agents'] query cache — the
 * same key the Sessions and Search modal and the Usage screen read — so an
 * agent rename re-derives the main's title here on the next render, with no
 * reload and no cached copy (founder ruling 2026-10-11).
 *
 * Honest states (combined-review S4-04/S2-05, steer 2026-10-12): the
 * derivation IGNORES the descriptor's title for a main — it is a copy taken
 * at attach time and persisted in the browser, so it can predate a rename;
 * the live roster (or the agent id) decides instead. While the agents query
 * is still in flight a MAIN renders no line at all (this component's
 * existing "nothing to say" presentation) instead of a fabricated name, and
 * a failed query falls back to the agent id — the server's own empty-name
 * fallback (pkg/gateway/session_title.go::withDisplayTitle) — never
 * "Unknown agent". A non-main descriptor keeps its snapshot title —
 * extra-chat titles are user-set and unaffected by agent renames.
 */
import { useId } from 'react'
import { useQuery } from '@tanstack/react-query'
import { CaretLeft, ListChecks, WarningCircle } from '@phosphor-icons/react'
import { fetchAgents, fetchSessions } from '@/lib/api'
import type { Session } from '@/lib/api'
import { logDiagnostic } from '@/lib/telemetry'
import { feedKindLabel, feedParentLabel } from '@/lib/nav/chatKindLabel'
import { isMainSession } from '@/lib/nav/sessionCoreSeam'
import { Tooltip } from '@/components/ui/tooltip'
import { IconButton } from '@/components/ui/icon-button'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { ParentCrumbLink } from './ParentCrumbLink'

// A header that cannot be named is left empty on purpose (nothing honest to
// show); this leaves a trace, once per session, so the empty line is debuggable.
const reportedUnavailable = new Set<string>()
function noteHeaderUnavailable(sessionId: string): void {
  if (reportedUnavailable.has(sessionId)) return
  reportedUnavailable.add(sessionId)
  logDiagnostic('chatHeaderLabelUnavailable', { sessionId })
}

export function FeedKindLabel() {
  const labelId = useId()
  const warningId = useId()
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const sessionByWorkspace = useSessionStore((s) => s.sessionByWorkspace)
  const attachedSessionType = useSessionStore((s) => s.attachedSessionType)

  // Live agents roster — current display names only; the derivation happens
  // per render in feedKindLabel/sessionDisplayTitle.
  const { data: agents = [], isPending: agentsPending, isError: agentsError } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })

  const descriptor = activeWorkspaceId != null ? sessionByWorkspace[activeWorkspaceId] : undefined
  const activeIsChild = descriptor != null && descriptor.id === activeSessionId
    && (descriptor.type === 'delegate' || descriptor.type === 'task')
  // Child chats only (helper / task run): the shared flat session list (same
  // key and fetch as the search modal) names the direct parent. Nothing is
  // fetched for any other session kind.
  const { data: allSessions } = useQuery({
    queryKey: ['sessions', 'flat'],
    queryFn: () => fetchSessions(undefined, undefined, { flat: true }),
    enabled: activeIsChild,
  })

  if (descriptor != null && activeSessionId != null && descriptor.id === activeSessionId) {
    const main = isMainSession(descriptor)
    // A main's title is derived from the LIVE roster — never from the
    // descriptor's persisted title snapshot (it can predate a rename).
    // While the roster is still loading there is nothing honest to show.
    if (main && agentsPending) return null
    const agentName = descriptor.agentId != null
      ? agents.find((agent) => agent.id === descriptor.agentId)?.name
      : undefined
    const label = feedKindLabel({
      id: descriptor.id,
      type: descriptor.type,
      // The derivation ignores this for a main (steer 2026-10-12) — the
      // persisted snapshot is never shown there; extras keep it.
      title: descriptor.title ?? '',
      agentName,
      agentId: descriptor.agentId,
    })
    // A main with neither a resolvable name nor an id (a failed roster and a
    // descriptor predating agent ids) shows nothing rather than a guess.
    if (label == null) {
      noteHeaderUnavailable(descriptor.id)
      return null
    }
    // R2-S4-03: an id fallback is the ruling's accepted stand-in, but it
    // must not read as a normal name. When the roster FAILED and its data
    // no longer names the agent, the strip says so — the same honest
    // "unavailable" signal the search modal shows for the identical trigger.
    // A failed REFETCH whose retained data still names the agent keeps the
    // plain name: nothing fell back, so there is nothing to disclaim.
    const rosterNameResolved = descriptor.agentId != null
      && agents.some((agent) => agent.id === descriptor.agentId && agent.name.trim() !== '')
    const namesUnavailable = main && agentsError && !rosterNameResolved
    // Founder decision 2026-10-20 ("Breadcrumb"), re-shaped by #1449 D13
    // (2026-10-28): a child chat reads "‹ <parent title> / Helper" and the
    // parent part opens the parent. The direct parent only. Any gap — no
    // parent_session_id, a parent that is gone or not loaded, a main parent
    // whose roster name is still loading — keeps the plain kind label with no
    // link; a name is never fabricated.
    let parentCrumb: { parent: Session, label: string } | null = null
    if (activeIsChild) {
      const parentId = allSessions?.find((s) => s.id === descriptor.id)?.parent_session_id
      const parent = parentId ? allSessions?.find((s) => s.id === parentId) : undefined
      if (parent != null && !(parent.type === 'main' && agentsPending)) {
        const parentAgentName = parent.agent_id != null
          ? agents.find((agent) => agent.id === parent.agent_id)?.name
          : undefined
        const parentLabel = feedParentLabel(parent, parentAgentName, parent.agent_id)
        if (parentLabel != null) parentCrumb = { parent, label: parentLabel }
      }
    }
    // The identity line and the honest-failure warning are SEPARATE elements:
    // the identity truncates, the warning must never be clipped away with it.
    // The warning carries its own id, so the status region's accessible name
    // still reads the whole line ("<id> - Main Session — agent names
    // unavailable"), and the identity's title carries the full text too.
    //
    // The warning's VISIBLE form is a compact glyph, not the sentence: the
    // header slot is capped (a quarter of the header line), so a 27-character
    // sentence cannot fit beside the identity and the participant control —
    // it overflowed the line at narrow widths. The glyph always fits.
    //
    // Its EXPLANATION is the catalogued Tooltip's job (design-system rule
    // 14): the glyph's trigger is named and focusable, so a sighted keyboard
    // user reaches the same words a screen reader gets — the bubble opens on
    // focus and on a tap and closes on Escape. The trigger is a SIBLING of
    // the labelled warning text, never a child of it: a named element inside
    // the region's name source would repeat itself in the accessible name.
    const identityText = parentCrumb != null ? `${parentCrumb.label} / ${label}` : label
    const warningText = namesUnavailable ? ' — agent names unavailable' : ''
    return (
      <div
        role="status"
        aria-labelledby={namesUnavailable ? `${labelId} ${warningId}` : labelId}
        aria-atomic="true"
        data-testid="feed-kind-label"
        className="grid min-w-0 grid-flow-col grid-cols-[minmax(0,1fr)] items-center gap-[var(--space-1)]"
      >
        <span
          id={labelId}
          title={`${identityText}${warningText}`}
          data-header-yield=""
          className="min-w-0 max-w-[32ch] truncate text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
        >
          {parentCrumb != null ? <><CaretLeft size={14} className="inline-block align-middle" aria-hidden="true" /><ParentCrumbLink parent={parentCrumb.parent} label={parentCrumb.label} />{` / ${label}`}</> : null}
          {parentCrumb == null ? label : null}
        </span>
        {namesUnavailable && (
          <>
            <span id={warningId} className="sr-only">{warningText}</span>
            {/* The wrapper keeps the no-shrink rule: the Tooltip's own span is
                a plain inline-flex item, and a squeezed header line must
                never clip the warning. */}
            <span className="flex shrink-0 items-center">
              {/* The warning is a REAL control, not a bare glyph: the
                  catalogued IconButton inside the Tooltip's interactive mode
                  (design-system rule 14) gives it a named, focusable trigger
                  and a touch-sized box on a coarse pointer — the same
                  composition the "Add participant" control beside it uses.
                  The GLYPH stays compact (14px) and the slot's width cap
                  still holds; only the hit box grows, and only for a finger.

                  The trigger is a SIBLING of the labelled warning text, never
                  a child of it: a named element inside the region's name
                  source would repeat itself in the accessible name. */}
              <Tooltip
                content="Agent names could not be loaded. Reload the page to try again."
                side="bottom"
                interactive
              >
                <IconButton
                  aria-label="Agent names unavailable"
                  data-testid="feed-kind-warning"
                  size="sm"
                  variant="ghost"
                  className="shrink-0 text-[var(--color-warning)] hover:text-[var(--color-warning)] active:text-[var(--color-warning)] pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]"
                >
                  <WarningCircle size={14} aria-hidden="true" />
                </IconButton>
              </Tooltip>
            </span>
          </>
        )}
      </div>
    )
  }

  if (attachedSessionType === 'task') {
    return <TaskKindLabel />
  }

  return null
}

/**
 * The task transcript's identity line ("Task: <title>"), on its own.
 *
 * Split out so a surface that needs ONLY this identity — the standalone
 * session shell (the /sessions/{id} fallback, which has no workspace header
 * line) — can say what the transcript is without starting the roster and
 * session-list queries the full label needs. No query, no dangling work.
 */
export function TaskKindLabel() {
  const attachedSessionType = useSessionStore((s) => s.attachedSessionType)
  const attachedTaskTitle = useSessionStore((s) => s.attachedTaskTitle)
  if (attachedSessionType !== 'task') return null
  const taskText = `Task: ${attachedTaskTitle ?? 'Task Execution'}`
  return (
    <div className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-center gap-[var(--space-1)]">
      <ListChecks size={14} className="text-[var(--color-accent)]" />
      <span
        title={taskText}
        data-header-yield=""
        className="min-w-0 max-w-[32ch] truncate text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
      >
        {taskText}
      </span>
    </div>
  )
}
