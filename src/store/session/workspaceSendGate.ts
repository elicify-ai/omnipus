import { workspaceEntryBlocksSend } from '@/lib/nav/workspaceEntry'
import { logDiagnostic } from '@/lib/telemetry'
import { useSessionStore } from '@/store/session'
import { runWorkspaceEntry } from '@/store/session/workspaceEntryFlow'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'

/** One gate/feedback contract for fresh sends, queued sends and retries. */
export function blockSendForWorkspaceEntry(operation: 'send' | 'drain' | 'resend'): boolean {
  const state = useSessionStore.getState()
  const workspaceId = useWorkspacesStore.getState().activeWorkspaceId
  if (!workspaceEntryBlocksSend(state, workspaceId)) return false

  const resolving = !!(workspaceId && state.resolvingSessionForWorkspace[workspaceId])
  const entryStatus = state.workspaceEntry?.status ?? null
  const fields = { operation, workspaceId, entryStatus, resolving, sessionId: state.activeSessionId }
  console.warn('[chat] workspace entry blocks send', fields)
  logDiagnostic('chatWorkspaceEntrySendBlocked', fields)
  useUiStore.getState().addToast({
    message: resolving
      ? 'Restoring your conversation…'
      : entryStatus === 'failed-attempt'
        ? 'Could not restore your last conversation. Retry to try again.'
        : 'This chat is unavailable right now',
    variant: 'warning',
    ...(!resolving && workspaceId ? {
      action: { label: 'Retry', onClick: () => { void runWorkspaceEntry(workspaceId) } },
    } : {}),
  })
  return true
}
