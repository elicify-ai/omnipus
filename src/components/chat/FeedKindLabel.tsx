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
import { fetchAgents } from '@/lib/api'
import { feedKindLabel } from '@/lib/nav/chatKindLabel'
import { isMainSession } from '@/lib/nav/sessionCoreSeam'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

export function FeedKindLabel() {
  const labelId = useId()
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const sessionByWorkspace = useSessionStore((s) => s.sessionByWorkspace)
  const attachedSessionType = useSessionStore((s) => s.attachedSessionType)
  const attachedTaskTitle = useSessionStore((s) => s.attachedTaskTitle)

  // Live agents roster — current display names only; the derivation happens
  // per render in feedKindLabel/sessionDisplayTitle.
  const { data: agents = [], isPending: agentsPending } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })

  const descriptor = activeWorkspaceId != null ? sessionByWorkspace[activeWorkspaceId] : undefined
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
    if (label == null) return null
    return (
      <div
        role="status"
        aria-labelledby={labelId}
        aria-atomic="true"
        data-testid="feed-kind-label"
        className="px-[var(--space-3)] py-[var(--space-2)] bg-[var(--color-surface-2)] border-b border-[var(--color-border)] flex items-center gap-[var(--space-2)]"
      >
        <span id={labelId} className="text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)] flex-1 truncate">
          {label}
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
