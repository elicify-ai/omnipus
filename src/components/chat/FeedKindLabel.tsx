/**
 * Persistent above-feed kind (FR-008, BDD-07.1 — mains as amended by the
 * founder decision 2026-10-11: a main's header line reads the main's title
 * "<Agent name> - Main Session", not the bare "Main chat" kind).
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
import { ListChecks } from '@phosphor-icons/react'
import { fetchAgents, fetchSessions } from '@/lib/api'
import type { Session } from '@/lib/api'
import { logDiagnostic } from '@/lib/telemetry'
import { feedKindLabel, feedParentLabel } from '@/lib/nav/chatKindLabel'
import type { FeedKindSource } from '@/lib/nav/chatKindLabel'
import { isMainSession } from '@/lib/nav/sessionCoreSeam'
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
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const sessionByWorkspace = useSessionStore((s) => s.sessionByWorkspace)
  const attachedSessionType = useSessionStore((s) => s.attachedSessionType)
  const attachedTaskTitle = useSessionStore((s) => s.attachedTaskTitle)
  const activeAgentId = useSessionStore((s) => s.activeAgentId)

  // Live agents roster — current display names only; the derivation happens
  // per render in feedKindLabel/sessionDisplayTitle.
  const { data: agents = [], isPending: agentsPending, isError: agentsError } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })

  const descriptor = activeWorkspaceId != null ? sessionByWorkspace[activeWorkspaceId] : undefined
  // #1428 (chief brief 2026-10-24): the strip names THIS tab's attached
  // session — the same session the composer and the rows render. The
  // workspace descriptor is a browser-persisted snapshot whose shared
  // localStorage copy another same-account tab can rewrite, so the live
  // attach kind outranks it, and a live attach with no matching descriptor
  // still names itself instead of rendering nothing. The descriptor keeps
  // one job: the non-main title snapshot (an extra's title is user-set).
  const descriptorMatches = descriptor != null && descriptor.id === activeSessionId
  const liveAttach = activeSessionId != null && attachedSessionType != null
  const kind: Session['type'] | undefined =
    attachedSessionType ?? (descriptorMatches ? descriptor.type : undefined)
  const source: FeedKindSource | null = descriptorMatches
    ? { id: descriptor.id, type: kind ?? descriptor.type, title: descriptor.title ?? '', agentId: descriptor.agentId }
    : liveAttach && attachedSessionType !== 'task'
      // Attached with no matching descriptor (e.g. a recordForWorkspace:false
      // attach): derive from this tab's own attach context, never from the
      // shared snapshot. A task keeps the dedicated "Task:" fallback below.
      ? { id: activeSessionId, type: attachedSessionType, title: attachedTaskTitle ?? '', agentId: activeAgentId }
      : null
  const activeIsChild = source != null && (source.type === 'delegate' || source.type === 'task')
  // Child chats only (helper / task run): the shared flat session list (same
  // key and fetch as the search modal) names the direct parent. Nothing is
  // fetched for any other session kind.
  const { data: allSessions } = useQuery({
    queryKey: ['sessions', 'flat'],
    queryFn: () => fetchSessions(undefined, undefined, { flat: true }),
    enabled: activeIsChild,
  })

  if (source != null) {
    const main = isMainSession(source)
    // A main's title is derived from the LIVE roster — never from the
    // descriptor's persisted title snapshot (it can predate a rename).
    // While the roster is still loading there is nothing honest to show.
    if (main && agentsPending) return null
    const agentName = source.agentId != null
      ? agents.find((agent) => agent.id === source.agentId)?.name
      : undefined
    const label = feedKindLabel({
      id: source.id,
      type: source.type,
      // The derivation ignores this for a main (steer 2026-10-12) — the
      // persisted snapshot is never shown there; extras keep it.
      title: source.title,
      agentName,
      agentId: source.agentId,
    })
    // A main with neither a resolvable name nor an id (a failed roster and a
    // descriptor predating agent ids) shows nothing rather than a guess.
    if (label == null) {
      noteHeaderUnavailable(source.id)
      return null
    }
    // R2-S4-03: an id fallback is the ruling's accepted stand-in, but it
    // must not read as a normal name. When the roster FAILED and its data
    // no longer names the agent, the strip says so — the same honest
    // "unavailable" signal the search modal shows for the identical trigger.
    // A failed REFETCH whose retained data still names the agent keeps the
    // plain name: nothing fell back, so there is nothing to disclaim.
    const rosterNameResolved = source.agentId != null
      && agents.some((agent) => agent.id === source.agentId && agent.name.trim() !== '')
    const namesUnavailable = main && agentsError && !rosterNameResolved
    // Founder decision 2026-10-20 ("Breadcrumb"): a child chat reads
    // "<parent title> › Helper" and the parent part opens the parent. The
    // direct parent only. Any gap — no parent_session_id, a parent that is
    // gone or not loaded, a main parent whose roster name is still loading —
    // keeps the plain kind label with no link; a name is never fabricated.
    let parentCrumb: { parent: Session, label: string } | null = null
    if (activeIsChild) {
      const parentId = allSessions?.find((s) => s.id === source.id)?.parent_session_id
      const parent = parentId ? allSessions?.find((s) => s.id === parentId) : undefined
      if (parent != null && !(parent.type === 'main' && agentsPending)) {
        const parentAgentName = parent.agent_id != null
          ? agents.find((agent) => agent.id === parent.agent_id)?.name
          : undefined
        const parentLabel = feedParentLabel(parent, parentAgentName, parent.agent_id)
        if (parentLabel != null) parentCrumb = { parent, label: parentLabel }
      }
    }
    return (
      <div
        role="status"
        aria-labelledby={labelId}
        aria-atomic="true"
        data-testid="feed-kind-label"
        className="px-[var(--space-3)] py-[var(--space-2)] bg-[var(--color-surface-2)] border-b border-[var(--color-border)] flex items-center gap-[var(--space-2)]"
      >
        <span id={labelId} className="text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)] flex-1 truncate">
          {parentCrumb != null ? <><ParentCrumbLink parent={parentCrumb.parent} label={parentCrumb.label} />{` › ${label}`}</> : null}
          {parentCrumb == null ? (namesUnavailable ? `${label} — agent names unavailable` : label) : null}
        </span>
      </div>
    )
  }

  if (attachedSessionType === 'task') {
    return (
      <div className="px-[var(--space-3)] py-[var(--space-2)] bg-[var(--color-surface-2)] border-b border-[var(--color-border)] flex items-center gap-[var(--space-2)]">
        <ListChecks size={14} className="text-[var(--color-accent)] shrink-0" />
        <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)] flex-1 truncate">
          Task: {attachedTaskTitle ?? 'Task Execution'}
        </span>
      </div>
    )
  }

  return null
}
