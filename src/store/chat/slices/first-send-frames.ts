import { produce } from 'immer'
import type { SessionStartedFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useConnectionStore } from '@/store/connection'
import { cursorFromTerminalFrame } from '../cursor'
import { emptySessionState } from '../session'
import {
  attachRecoveredFirstSend,
  findFirstSendMessage,
  getPendingFirstSend,
  hasFirstSendReceipt,
  removeFirstSendPlaceholder,
  setFirstSendStatus,
  type FirstSendContext,
} from '../first-send'
import type { ChatStore, PendingFirstSend } from '../types'

type Frame = Parameters<ChatStore['handleFrame']>[0]
// Store reducer wiring only, not a gateway wire format.
interface FirstSendFrameContext extends FirstSendContext {
  frame: Frame
  runtime: { agentIdAtLastMintSend: string | null }
}

function confirmFirstSend(context: FirstSendFrameContext, pending: PendingFirstSend, frame: SessionStartedFrame): void {
  const { set, get } = context
  const sid = frame.session_id
  const foreground = useSessionStore.getState().activeSessionId === (pending.sessionId ?? '__pending')
    && (useWorkspacesStore.getState().activeWorkspaceId || null) === pending.workspaceId
  const recovered = frame.recovered === true
  const sourceSid = pending.sessionId ?? '__pending'
  const source = get().sessionsById[sourceSid] ?? emptySessionState()
  const bucket = produce(source, (draft) => {
    for (const message of Object.values(draft.messagesById)) message.session_id = sid
    const user = findFirstSendMessage(draft, pending.clientMessageId)
    if (user) {
      user.clientMessageId = pending.clientMessageId
      user.firstSendStatus = recovered ? 'checking_chat' : 'saved'
      user.deliveryStatus = 'received'
      user.status = 'done'
    }
    if (recovered) removeFirstSendPlaceholder(draft, pending.assistantPlaceholderId)
    draft.isStreaming = !recovered
    draft.isReplaying = recovered
    draft.awaitingCatchUp = recovered
    draft.cursor = recovered ? null : frame.seq === undefined ? draft.cursor : cursorFromTerminalFrame({ seq: frame.seq, boot_id: frame.boot_id })
    draft.recoveredFirstSend = recovered
      ? { clientMessageId: pending.clientMessageId, attemptGeneration: pending.attemptGeneration, reconciled: false }
      : undefined
    if (!recovered && pending.payload.auto_approve !== undefined) draft.autoApproveEffective = pending.payload.auto_approve
  })
  set((state) => {
    const sessionsById = { ...state.sessionsById, [sid]: bucket }
    if (sourceSid !== sid) delete sessionsById[sourceSid]
    return {
      sessionsById,
      pendingFirstSend: recovered ? { ...pending, sessionId: sid, status: 'checking_chat' } : null,
      ...(foreground ? { pendingAutoApproveChoice: null } : {}),
    }
  })
  context.runtime.agentIdAtLastMintSend = null
  if (foreground) {
    const currentAgent = useSessionStore.getState().activeAgentId
    const reselected = !!pending.payload.agent_id && currentAgent !== pending.payload.agent_id
    useSessionStore.getState().setActiveSession(sid, reselected ? currentAgent : frame.agent_id ?? currentAgent)
    if (recovered) attachRecoveredFirstSend(context)
  } else {
    if (pending.workspaceId) {
      useSessionStore.getState().setWorkspaceSessionDescriptor(pending.workspaceId, {
        id: sid, type: 'chat', title: null, agentId: frame.agent_id ?? pending.payload.agent_id ?? null,
      })
    }
    if (recovered) {
      const retained = getPendingFirstSend(get())
      if (retained) setFirstSendStatus(context, retained, 'check_failed')
    }
  }
  queryClient.invalidateQueries({ queryKey: ['sessions'] })
  if (foreground && !recovered) get().drainOutboundQueue()
}

function handleFirstSendError(context: FirstSendFrameContext, frame: Extract<Frame, { type: 'error' }>): boolean {
  if (!frame.first_message_error) return false
  const pending = getPendingFirstSend(context.get())
  const message = pending && findFirstSendMessage(context.get().sessionsById[pending.sessionId ?? '__pending'], pending.clientMessageId)
  const confirmed = hasFirstSendReceipt(message ?? undefined)
  if (!frame.client_message_id) {
    useConnectionStore.getState().setConnectionError('Delivery not confirmed — the server did not identify the message. Reconnect and press Retry.')
    if (pending) setFirstSendStatus(context, pending, 'unconfirmed')
    return true
  }
  if (frame.first_message_error !== 'answer_not_started') {
    if (pending?.clientMessageId === frame.client_message_id && !confirmed) {
      setFirstSendStatus(context, pending, frame.first_message_error === 'not_saved' ? 'not_saved' : 'unconfirmed')
    }
    return true // A late tagged error must never be filed under a new chat.
  }
  if (!frame.session_id) {
    if (pending?.clientMessageId === frame.client_message_id) setFirstSendStatus(context, pending, 'unconfirmed')
    useConnectionStore.getState().setConnectionError('Delivery not confirmed — the server did not identify the saved chat. Reconnect and press Retry.')
    return true
  }
  if (pending?.clientMessageId === frame.client_message_id && !confirmed) {
    confirmFirstSend(context, pending, { type: 'session_started', session_id: frame.session_id, client_message_id: frame.client_message_id })
  }
  const bucket = context.get().sessionsById[frame.session_id]
  const user = findFirstSendMessage(bucket, frame.client_message_id)
  if (user?.firstSendStatus) {
    context.withBucket(frame.session_id, (current) => produce(current, (draft) => {
      draft.messagesById[user.id].firstSendStatus = 'answer_not_started'
      draft.messagesById[user.id].deliveryStatus = 'received'
      draft.messagesById[user.id].status = 'done'
      if (pending) removeFirstSendPlaceholder(draft, pending.assistantPlaceholderId)
      // A fresh ack may have cleared the record just before this admission error.
      for (const id of [...draft.messageOrder]) {
        const message = draft.messagesById[id]
        if (message?.role === 'assistant' && !message.content.trim() && !message.tool_calls?.length && message.isStreaming) {
          removeFirstSendPlaceholder(draft, id)
        }
      }
      draft.isStreaming = false
      draft.isReplaying = false
      draft.awaitingCatchUp = false
      draft.activeTurnId = null
      draft.activeTurnAgentId = null
      draft.unansweredLastUserMessageId = null
      draft.recoveredFirstSend = undefined
    }))
    if (pending?.clientMessageId === frame.client_message_id) context.set({ pendingFirstSend: null })
  }
  queryClient.invalidateQueries({ queryKey: ['sessions'] })
  return true
}

function reconcileRecoveredFirstSend(context: FirstSendFrameContext, pending: PendingFirstSend): boolean {
  const frame = context.frame
  if (!pending.sessionId || (frame.type !== 'user_message' && frame.type !== 'replay_message')) return false
  if (frame.type === 'replay_message' && frame.role !== 'user') return false
  if (frame.session_id !== pending.sessionId || frame.client_message_id !== pending.clientMessageId) return false
  const serverId = frame.id
  if (!serverId) return false
  const recovering = !!context.get().sessionsById[pending.sessionId]?.recoveredFirstSend
  context.withBucket(pending.sessionId, (bucket) => produce(bucket, (draft) => {
    const message = findFirstSendMessage(draft, pending.clientMessageId)
    if (!message) return
    if (message.id !== serverId) {
      delete draft.messagesById[message.id]
      draft.messageOrder = [...new Set(draft.messageOrder.map((id) => id === message.id ? serverId : id))]
    }
    draft.messagesById[serverId] = { ...message, id: serverId, clientMessageId: pending.clientMessageId, deliveryStatus: 'received' }
    if (!recovering) draft.messagesById[serverId].firstSendStatus = 'saved'
    if (draft.recoveredFirstSend) draft.recoveredFirstSend.reconciled = true
  }))
  if (!recovering) {
    context.set({ pendingFirstSend: null })
    context.get().drainOutboundQueue()
  }
  return true
}

/** Intercepts only the ordinary first-send lifecycle; kickoff and established chats remain in their existing reducers. */
export function handleFirstSendFrame(context: FirstSendFrameContext): boolean {
  const { frame, get } = context
  const pending = getPendingFirstSend(get())
  const message = pending && findFirstSendMessage(get().sessionsById[pending.sessionId ?? '__pending'], pending.clientMessageId)
  const confirmed = hasFirstSendReceipt(message ?? undefined)
  if (frame.type === 'error') {
    if (frame.first_message_error) return handleFirstSendError(context, frame)
    if (frame.client_message_id) {
      if (pending?.clientMessageId === frame.client_message_id) {
        setFirstSendStatus(context, pending, 'check_failed')
        useConnectionStore.getState().setConnectionError(frame.message)
        return true
      }
      if (get().abandonedFirstSendIds.includes(frame.client_message_id)) return true
    }
    if (pending?.sessionId && frame.session_id === pending.sessionId && pending.status === 'checking_chat') {
      setFirstSendStatus(context, pending, 'check_failed')
      useConnectionStore.getState().setConnectionError('Could not check this chat — press Retry to check again.')
      return true
    }
    return false
  }
  if (frame.type === 'session_started') {
    const clientId = frame.client_message_id
    if (!clientId) {
      // DEL-F21/F22: a `session_started` without `client_message_id` is not a
      // correlated receipt, so it must NOT bind/save an ordinary pending first
      // send. Consume the frame (nothing is established) instead of letting the
      // legacy ack tail migrate the pending bucket/agent/mode. With no unbound
      // pending first send, fall through so a workspace-setup kickoff and any
      // other ordinary no-session turn keep their existing path.
      return pending !== null && !pending.sessionId
    }
    if (!pending || pending.clientMessageId !== clientId) {
      const abandoned = get().abandonedFirstSendIds.includes(clientId)
      const user = findFirstSendMessage(get().sessionsById[frame.session_id], clientId)
      // Only this tab's abandoned or confirmed first sends bypass normal binding.
      if (!abandoned && !user?.firstSendStatus) return false
      queryClient.invalidateQueries({ queryKey: ['sessions'] })
      return true
    }
    confirmFirstSend(context, pending, frame)
    return true
  }
  if (frame.type === 'message_status' && frame.state !== 'failed'
    && frame.client_message_id !== pending?.clientMessageId
    && !findFirstSendMessage(get().sessionsById[frame.session_id], frame.client_message_id)) {
    // A late saved receipt can reveal an abandoned chat, never select it.
    queryClient.invalidateQueries({ queryKey: ['sessions'] })
    return true
  }
  if (!pending) return false
  if (frame.type === 'message_status' && frame.client_message_id === pending.clientMessageId && !confirmed) {
    if (frame.state === 'failed') {
      setFirstSendStatus(context, pending, 'unconfirmed')
    } else {
      confirmFirstSend(context, pending, {
        type: 'session_started', session_id: frame.session_id, client_message_id: frame.client_message_id,
        seq: frame.seq,
      })
      context.withBucket(frame.session_id, (bucket) => produce(bucket, (draft) => {
        const user = findFirstSendMessage(draft, pending.clientMessageId)
        if (user) user.deliveryStatus = frame.state
      }))
    }
    return true
  }
  if (frame.type === 'done' && frame.stats?.replay_error === true && frame.session_id === pending.sessionId) {
    setFirstSendStatus(context, pending, 'check_failed')
    return true
  }
  return reconcileRecoveredFirstSend(context, pending)
}

/** Completes only an explicitly recovered first-send attach, never an arbitrary unknown_position snapshot. */
export function finishRecoveredFirstSend(context: FirstSendContext, sid: string): void {
  const pending = getPendingFirstSend(context.get())
  const recovery = context.get().sessionsById[sid]?.recoveredFirstSend
  if (!pending || pending.status !== 'checking_chat' || pending.sessionId !== sid || pending.clientMessageId !== recovery?.clientMessageId || pending.attemptGeneration !== recovery.attemptGeneration) return
  if (!recovery.reconciled) {
    setFirstSendStatus(context, pending, 'check_failed')
    return
  }
  context.withBucket(sid, (bucket) => produce(bucket, (draft) => {
    const user = findFirstSendMessage(draft, pending.clientMessageId)
    const lastId = draft.messageOrder[draft.messageOrder.length - 1]
    const unanswered = !draft.activeTurnId && user?.id === lastId
    if (user) user.firstSendStatus = unanswered ? 'unfinished' : 'saved'
    draft.unansweredLastUserMessageId = unanswered && user ? user.id : null
    draft.recoveredFirstSend = undefined
    draft.isStreaming = !!draft.activeTurnId
  }))
  context.set({ pendingFirstSend: null })
  context.get().drainOutboundQueue()
}
