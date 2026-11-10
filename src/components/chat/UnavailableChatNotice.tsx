/**
 * Shown when workspace entry is unavailable or a restore failed.
 * Retains the committed chat with send off; Retry rechecks the target workspace.
 */
import { Button } from '@/components/ui/button'
import { ErrorState } from '@/components/ui/error-state'
import { useSessionStore } from '@/store/session'
import { runWorkspaceEntry } from '@/store/session/workspaceEntryFlow'
import { useWorkspacesStore } from '@/store/workspacesStore'

export function UnavailableChatNotice() {
  const entry = useSessionStore((state) => state.workspaceEntry)
  const workspaceId = useWorkspacesStore((state) => state.activeWorkspaceId)
  const status = entry?.status
  if (status !== 'unavailable' && status !== 'failed-attempt') return null
  const retryEntry = () => {
    if (!workspaceId) return
    void (entry?.linkedSessionId ? runWorkspaceEntry(workspaceId, entry.linkedSessionId) : runWorkspaceEntry(workspaceId))
  }
  if (entry?.status === 'unavailable' && (entry.mainSessionError || entry.linkedSessionId)) {
    return (
      <div data-testid="unavailable-chat-notice">
        <ErrorState
          message={entry.linkedSessionId
            ? 'This linked conversation is unavailable. It may have been deleted or moved. Choose another chat, or Retry to check the link again.'
            : entry.mainSessionError!}
          onRetry={workspaceId ? retryEntry : undefined}
          className="px-[var(--space-3)] py-[var(--space-2)] text-center"
        />
      </div>
    )
  }
  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="unavailable-chat-notice"
      className="flex min-h-8 items-center justify-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
    >
      <span>{status === 'failed-attempt' ? 'Could not restore your last conversation. Retry to try again.' : 'This chat is unavailable right now'}</span>
      {workspaceId && (
        <Button
          variant="ghost"
          size="sm"
          onClick={retryEntry}
          className="h-8 px-[var(--space-2)] text-[var(--color-secondary)]"
        >
          Retry
        </Button>
      )}
    </div>
  )
}
