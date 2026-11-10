import { useSessionStore } from '@/store/session'
import type { NewChatOutcome } from '@/store/session/newChatFlow'
import { useWorkspacesStore } from '@/store/workspacesStore'

type ChatNavigate = (opts: { to: '/workspaces/$workspaceId/chat'; params: { workspaceId: string } }) => void

/**
 * Start an extra chat for this pair. Does not write a new main id.
 * Switches the workspace only when an extra actually STARTED (C4, R3 delta
 * re-review): the active workspace and the route must stay one tuple, so a
 * 'prompt' outcome ("Keep this chat" — the dialog opens), a 'kept' decline
 * or a 'refused' stale confirm leaves the user on the workspace the route
 * shows. Switching before the decision (the old behaviour) split them: the
 * route showed the old workspace while the @-recipient pair, which reads
 * `activeWorkspaceId` at send time, addressed the row's workspace.
 *
 * M7: the new-chat DECISION belongs to newChatFlow alone — this entry
 * executes it and navigates only when an extra actually started. A 'prompt'
 * outcome keeps the user here so the row's dialog (driven by the store's
 * `newChatPrompt`) can show; a 'refused' confirm — the pending message
 * changed while the dialog was open — stays put with the flow's toast.
 *
 * `confirm` passes the id the open dialog was asked about: the flow abandons
 * only that message, never whatever occupies the slot later.
 */
export function beginPairExtra(
  workspaceId: string,
  agentId: string,
  navigate: ChatNavigate,
  closeOverlay: () => void,
  mode: 'start' | 'confirm' = 'start',
  capturedClientMessageId?: string,
): NewChatOutcome {
  const outcome = mode === 'confirm'
    ? useSessionStore.getState().startNewSession({
        choice: 'confirm',
        agentId,
        clientMessageId: capturedClientMessageId,
      })
    : useSessionStore.getState().startNewSession(agentId)
  if (outcome !== 'started') return outcome
  // Started: NOW the pointer switch lands the fresh session on this
  // workspace, immediately before the route follows it.
  useWorkspacesStore.getState().setActiveWorkspaceId(workspaceId)
  navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId } })
  closeOverlay()
  return outcome
}
