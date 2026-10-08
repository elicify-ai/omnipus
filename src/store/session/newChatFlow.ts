/**
 * startNewSession behind decideNewChat (FR-005).
 *
 * An unconfirmed first send with no real session id prompts. Declining keeps
 * that delivery and the saved pointer. Confirming abandons it and starts an
 * extra without replacing the main pointer. `__pending` is never saved.
 */
import type { AgentKind } from '@/lib/api'
import { decideNewChat } from '@/lib/nav/extraChatGuard'
import { useWorkspacesStore } from '@/store/workspacesStore'
import {
  abandonRegisteredPendingFirstSend,
  clearPendingAutoApproveOnSessionChange,
  readPendingFirstSend,
  useSessionStore,
} from '@/store/session'

export type NewChatPrompt = {
  action: 'prompt'
  mainSessionId: string
  clientMessageId: string
  started: false
}

function choiceOf(arg: unknown): 'decline' | 'confirm' | undefined {
  if (!arg || typeof arg !== 'object' || !('choice' in arg)) return undefined
  const choice = (arg as { choice?: unknown }).choice
  return choice === 'decline' || choice === 'confirm' ? choice : undefined
}

export function runStartNewSession(
  agentOrChoice?: string | null | { choice?: 'decline' | 'confirm' },
  agentType?: AgentKind | null,
): void {
  const state = useSessionStore.getState()
  const workspaceId = useWorkspacesStore.getState().activeWorkspaceId
  const agentId = state.activeAgentId
  const pairKey = workspaceId && agentId ? `${workspaceId}::${agentId}` : ''
  const mainSessionId = pairKey ? (state.mainPointerByPair[pairKey] ?? '') : ''
  const decision = decideNewChat({
    mainSessionId,
    activeSessionId: state.activeSessionId,
    pending: readPendingFirstSend(),
    choice: choiceOf(agentOrChoice),
  })

  if (decision.action === 'prompt') {
    const prompt: NewChatPrompt = {
      action: 'prompt',
      mainSessionId: decision.mainSessionId,
      clientMessageId: decision.clientMessageId,
      started: false,
    }
    useSessionStore.setState({ newChatPrompt: prompt })
    return
  }
  if (decision.action === 'decline') {
    useSessionStore.setState({ newChatPrompt: null })
    return
  }

  clearPendingAutoApproveOnSessionChange()
  abandonRegisteredPendingFirstSend()
  useSessionStore.setState({ newChatPrompt: null })
  const hint = typeof agentOrChoice === 'string' ? agentOrChoice : undefined
  // A new extra keeps the pair's owner. It does not clear the saved pointer
  // and it does not replace the main pointer the sidebar row uses.
  useSessionStore.getState().setActiveSession(null, hint, agentType ?? undefined)
}
