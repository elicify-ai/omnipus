import type { GuardInput } from '@/lib/nav/extraChatGuard'
import { findFirstSendMessage, getPendingFirstSend } from '@/store/chat/first-send'
import { useChatStore } from '@/store/chat/store'
import type { FirstSendStatus } from '@/store/chat/types'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

type GuardStatus = NonNullable<GuardInput['pending']>['status']

function asGuardStatus(status: FirstSendStatus): GuardStatus {
  if (
    status === 'sending'
    || status === 'unconfirmed'
    || status === 'retrying'
    || status === 'not_saved'
    || status === 'check_failed'
    || status === 'saved'
  ) {
    return status
  }
  // Still in flight (checking, unanswered, unfinished). Prompt; do not drop it.
  return 'unconfirmed'
}

/** Current first-send, shaped for decideNewChat. Text is the unsent message. */
export function currentNewChatInput(mainSessionId: string): GuardInput {
  const pending = getPendingFirstSend(useChatStore.getState())
  const activeSessionId = useSessionStore.getState().activeSessionId
  if (!pending) return { mainSessionId, activeSessionId, pending: null }
  const bucket = useChatStore.getState().sessionsById[pending.sessionId ?? '__pending']
  const message = findFirstSendMessage(bucket, pending.clientMessageId)
  return {
    mainSessionId,
    activeSessionId,
    pending: {
      sessionId: pending.sessionId,
      clientMessageId: pending.clientMessageId,
      text: message?.content ?? '',
      status: asGuardStatus(pending.status),
    },
  }
}

type ChatNavigate = (opts: { to: '/workspaces/$workspaceId/chat'; params: { workspaceId: string } }) => void

/**
 * Start an extra chat for this pair. Does not write a new main id.
 * Sets the workspace first so the fresh pointer lands on that workspace.
 */
export function beginPairExtra(workspaceId: string, agentId: string, navigate: ChatNavigate, closeOverlay: () => void) {
  useWorkspacesStore.getState().setActiveWorkspaceId(workspaceId)
  useSessionStore.getState().startNewSession(agentId)
  navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId } })
  closeOverlay()
}

/**
 * Same gate the composer uses before abandoning an unconfirmed first send:
 * the pending message is still the selected one in this workspace.
 */
export function unconfirmedSendStillSelected(): boolean {
  const pending = getPendingFirstSend(useChatStore.getState())
  if (!pending) return false
  const selected = useSessionStore.getState().activeSessionId
  if (selected !== '__pending') return false
  if ((useWorkspacesStore.getState().activeWorkspaceId || null) !== pending.workspaceId) return false
  const bucket = useChatStore.getState().sessionsById[selected]
  return !!findFirstSendMessage(bucket, pending.clientMessageId)
}
