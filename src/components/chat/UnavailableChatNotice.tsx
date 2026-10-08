/**
 * Shown when workspace entry is unavailable or a restore failed.
 * Retains the committed chat with send off; Retry rechecks the target workspace.
 */
import { Button } from '@/components/ui/button'
import { useSessionStore } from '@/store/session'
import { runWorkspaceEntry } from '@/store/session/workspaceEntryFlow'
import { useWorkspacesStore } from '@/store/workspacesStore'

export function UnavailableChatNotice() {
  const status = useSessionStore((state) => state.workspaceEntry?.status)
  const workspaceId = useWorkspacesStore((state) => state.activeWorkspaceId)
  if (status !== 'unavailable' && status !== 'failed-attempt') return null
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
          onClick={() => {
            void runWorkspaceEntry(workspaceId)
          }}
          className="h-8 px-[var(--space-2)] text-[var(--color-secondary)]"
        >
          Retry
        </Button>
      )}
    </div>
  )
}
