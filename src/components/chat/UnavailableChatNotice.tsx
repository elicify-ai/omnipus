/**
 * Shown in the composer when workspace entry has no main or welcome chat.
 * Same status-line shape as the connection notice: one sentence and Retry.
 */
import { Button } from '@/components/ui/button'
import { useSessionStore } from '@/store/session'
import { runWorkspaceEntry } from '@/store/session/workspaceEntryFlow'
import { useWorkspacesStore } from '@/store/workspacesStore'

export function UnavailableChatNotice() {
  const unavailable = useSessionStore((state) => state.workspaceEntry?.status === 'unavailable')
  const workspaceId = useWorkspacesStore((state) => state.activeWorkspaceId)
  if (!unavailable) return null
  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="unavailable-chat-notice"
      className="flex min-h-8 items-center justify-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
    >
      <span>This chat is unavailable right now</span>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => {
          if (workspaceId) void runWorkspaceEntry(workspaceId)
        }}
        className="h-8 px-[var(--space-2)] text-[var(--color-secondary)]"
      >
        Retry
      </Button>
    </div>
  )
}
