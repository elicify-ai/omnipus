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

/** The unconfirmed send the dialog opened against. Confirm may abandon only this id. */
export type CapturedPending = { clientMessageId: string; workspaceId: string | null }

/** Snapshot taken when the guard opens, not whatever occupies the slot later. */
export function captureUnconfirmedTarget(): CapturedPending | null {
  const pending = getPendingFirstSend(useChatStore.getState())
  if (!pending || pending.sessionId) return null
  return { clientMessageId: pending.clientMessageId, workspaceId: pending.workspaceId }
}

/**
 * The deleted composer gate: the captured id is still the message in the
 * selected bucket, in the same workspace. A newer message that reused the
 * pending slot does not match.
 */
export function pendingIdStillSelected(captured: CapturedPending | null): boolean {
  if (!captured) return false
  const selected = useSessionStore.getState().activeSessionId
  const bucket = selected ? useChatStore.getState().sessionsById[selected] : undefined
  return (useWorkspacesStore.getState().activeWorkspaceId || null) === captured.workspaceId
    && !!findFirstSendMessage(bucket, captured.clientMessageId)
}

/**
 * Start an extra chat for this pair. Does not write a new main id.
 * Sets the workspace first so the fresh pointer lands on that workspace.
 * `confirm` passes the dialog choice and this row's owner together: a bare
 * agent id is treated as "not yet confirmed" and will not abandon a delivery.
 */
export function beginPairExtra(
  workspaceId: string,
  agentId: string,
  navigate: ChatNavigate,
  closeOverlay: () => void,
  mode: 'start' | 'confirm' = 'start',
  captured?: CapturedPending | null,
) {
  useWorkspacesStore.getState().setActiveWorkspaceId(workspaceId)
  if (mode === 'confirm') {
    useSessionStore.getState().startNewSession({
      choice: 'confirm',
      agentId,
      clientMessageId: captured?.clientMessageId,
    })
  } else {
    useSessionStore.getState().startNewSession(agentId)
  }
  navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId } })
  closeOverlay()
}

