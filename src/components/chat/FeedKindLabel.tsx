/**
 * Persistent above-feed kind (FR-008, BDD-07.1).
 *
 * Words come from feedKindLabel on the workspace descriptor, and only when
 * that descriptor is the active session. Replay does not change them.
 * A status role takes its accessible name from the author, not from its
 * text, so the visible line is the name via aria-labelledby.
 * When no descriptor matches, a task session keeps the previous "Task:" line
 * so a transcript opened without a workspace pointer still says it is a task.
 */
import { useId } from 'react'
import { ListChecks } from '@phosphor-icons/react'
import { feedKindLabel } from '@/lib/nav/chatKindLabel'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

export function FeedKindLabel() {
  const labelId = useId()
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const sessionByWorkspace = useSessionStore((s) => s.sessionByWorkspace)
  const attachedSessionType = useSessionStore((s) => s.attachedSessionType)
  const attachedTaskTitle = useSessionStore((s) => s.attachedTaskTitle)

  const descriptor = activeWorkspaceId != null ? sessionByWorkspace[activeWorkspaceId] : undefined
  if (descriptor != null && activeSessionId != null && descriptor.id === activeSessionId) {
    const label = feedKindLabel({
      id: descriptor.id,
      type: descriptor.type,
      title: descriptor.title ?? '',
    })
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
