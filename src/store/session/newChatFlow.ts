/**
 * startNewSession behind decideNewChat (FR-005).
 *
 * An unconfirmed first send with no real session id prompts. Declining keeps
 * that delivery and the saved pointer. Confirming abandons it and starts an
 * extra without replacing the main pointer. `__pending` is never saved.
 *
 * M7: this flow is the ONE owner of the new-chat decision. The sidebar asks
 * it and renders the outcome (the dialog reads the store's `newChatPrompt`);
 * it never re-decides. A confirm names the clientMessageId the open dialog
 * was asked about, and if that message is no longer the pending one, nothing
 * is abandoned and the refusal is VISIBLE (a warning toast) — never a silent
 * no-op.
 */
import type { AgentKind } from '@/lib/api'
import { decideNewChat } from '@/lib/nav/extraChatGuard'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { resumeWorkspaceEntryQueue, supersedeNavigationIntent } from '@/store/session/workspaceEntryFlow'
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
  /** The agent the question was asked for — the sidebar row whose dialog it is. */
  agentId: string | null
}

/** A string is the extra's owner. An object is the dialog choice, plus that owner. */
export type NewChatStartArg =
  | string
  | null
  | { choice?: 'decline' | 'confirm'; agentId?: string | null; clientMessageId?: string }

/**
 * What runStartNewSession actually DID — callers navigate only on
 * 'started'. 'prompt' opened the dialog (store `newChatPrompt`), 'kept'
 * answered "keep this chat", 'refused' declined a stale confirm visibly.
 */
export type NewChatOutcome = 'started' | 'prompt' | 'kept' | 'refused'

function choiceOf(arg: unknown): 'decline' | 'confirm' | undefined {
  if (!arg || typeof arg !== 'object' || !('choice' in arg)) return undefined
  const choice = (arg as { choice?: unknown }).choice
  return choice === 'decline' || choice === 'confirm' ? choice : undefined
}

function agentHintOf(arg: NewChatStartArg | undefined): string | undefined {
  if (typeof arg === 'string') return arg
  if (arg && typeof arg === 'object' && typeof arg.agentId === 'string') return arg.agentId
  return undefined
}

/** Dialog confirm names the id it opened against. A bare confirm means "the one pending now". */
function capturedClientMessageId(arg: NewChatStartArg | undefined): string | undefined {
  if (!arg || typeof arg !== 'object' || typeof arg.clientMessageId !== 'string') return undefined
  return arg.clientMessageId
}

export function runStartNewSession(
  agentOrChoice?: NewChatStartArg,
  agentType?: AgentKind | null,
): NewChatOutcome {
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
      agentId: agentHintOf(agentOrChoice) ?? state.activeAgentId ?? null,
    }
    useSessionStore.setState({ newChatPrompt: prompt })
    return 'prompt'
  }
  if (decision.action === 'decline') {
    useSessionStore.setState({ newChatPrompt: null })
    return 'kept'
  }
  // A dialog opened against an older id must not discard the message that
  // has since reused the pending slot. A confirm with no captured id still
  // abandons whatever is pending now. M7: the stale case is never silent —
  // the prompt closes and a warning toast says why nothing started.
  const capturedId = capturedClientMessageId(agentOrChoice)
  if (decision.action === 'confirm' && capturedId && readPendingFirstSend()?.clientMessageId !== capturedId) {
    useSessionStore.setState({ newChatPrompt: null })
    useUiStore.getState().addToast({
      message: 'That message is no longer pending, so nothing was abandoned.',
      variant: 'warning',
    })
    return 'refused'
  }

  // A deliberately new chat wins over a failed or still-loading restore.
  // Do not clear these gates on prompt/decline: that would reopen the old target.
  supersedeNavigationIntent()
  clearPendingAutoApproveOnSessionChange()
  abandonRegisteredPendingFirstSend()
  useSessionStore.setState({
    newChatPrompt: null,
    workspaceEntry: null,
    resolvingSessionForWorkspace: {},
  })
  const hint = agentHintOf(agentOrChoice)
  // A new extra keeps the pair's owner. It does not clear the saved pointer
  // and it does not replace the main pointer the sidebar row uses.
  useSessionStore.getState().setActiveSession(null, hint, agentType ?? undefined)
  resumeWorkspaceEntryQueue()
  return 'started'
}

/**
 * Close the outstanding "Start a new chat?" question without answering it
 * (the dialog's "Keep this chat"). Only ever clears the prompt — never
 * starts an extra and never touches the delivery — so a state change while
 * the dialog was open cannot turn a dismiss into an action.
 */
export function dismissNewChatPrompt(): void {
  useSessionStore.setState({ newChatPrompt: null })
}
