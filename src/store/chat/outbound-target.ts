// Local send ownership. Navigation never supplies a replacement destination.
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { ChatStore, OutboundTarget } from './types'
import { useChatStore } from './store'
import { queryClient } from '@/lib/queryClient'
import type { components } from '@/lib/api/generated/openapi-types'

export function loadedOutboundSession(sessionId: string): Pick<components['schemas']['Session'], 'id' | 'agent_id' | 'workspace_id'> | undefined {
  const detail = queryClient.getQueryData<components['schemas']['SessionDetail']>(['session-detail', sessionId])
  if (detail?.session?.id === sessionId) return detail.session
  const sessions = queryClient.getQueryData<components['schemas']['Session'][]>(['sessions'])
  return Array.isArray(sessions) ? sessions.find(candidate => candidate.id === sessionId) : undefined
}

export function captureOutboundTarget(state: ChatStore): OutboundTarget {
  const { activeSessionId, activeAgentId } = useSessionStore.getState()
  return {
    sessionId: activeSessionId,
    agentId: activeAgentId,
    workspaceId: useWorkspacesStore.getState().activeWorkspaceId || null,
    ...(activeSessionId === null ? { draftGeneration: state.firstSendGeneration } : {}),
    ...(activeSessionId === '__pending' && state.pendingFirstSend
      ? { firstSendClientMessageId: state.pendingFirstSend.clientMessageId }
      : {}),
  }
}

/** A provisional chat may bind only through its own correlated first message. */
export function resolveOutboundTarget(target: OutboundTarget, state: ChatStore): OutboundTarget {
  if (!target.firstSendClientMessageId) return target
  for (const [sid, bucket] of Object.entries(state.sessionsById)) {
    if (sid === '__pending') continue
    if (Object.values(bucket.messagesById).some(message =>
      message.role === 'user' && (message.clientMessageId ?? message.id) === target.firstSendClientMessageId,
    )) return { ...target, sessionId: sid }
  }
  return target
}

export function outboundTargetIsForeground(target: OutboundTarget): boolean {
  const session = useSessionStore.getState()
  if (target.sessionId) return target.sessionId === session.activeSessionId
    && (target.sessionId !== '__pending' || !target.firstSendClientMessageId || target.firstSendClientMessageId === useChatStore.getState().pendingFirstSend?.clientMessageId)
  if (target.draftGeneration !== undefined && target.draftGeneration !== useChatStore.getState().firstSendGeneration) return false
  return session.activeSessionId === null && target.agentId === session.activeAgentId
    && target.workspaceId === (useWorkspacesStore.getState().activeWorkspaceId || null)
}
