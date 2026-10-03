import type { StoreApi } from 'zustand'
import { produce } from 'immer'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { generateId } from '@/lib/constants'
import type { MessageFrame } from '@/lib/api/generated/asyncapi-types'
import type { WsConnection } from '@/lib/ws'
import { applyMessageArray } from './session'
import type { ChatMessage, ChatStore, FirstSendStatus, MediaAttachment, PendingFirstSend, SessionChatState } from './types'

// Internal store wiring, not a gateway wire format.
export interface FirstSendContext {
  set: StoreApi<ChatStore>['setState']
  get: StoreApi<ChatStore>['getState']
  withBucket: (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void
}

export function findFirstSendMessage(bucket: SessionChatState | undefined, clientMessageId: string): ChatMessage | undefined {
  return bucket && Object.values(bucket.messagesById).find((message) =>
    message.role === 'user' && (message.clientMessageId === clientMessageId || message.id === clientMessageId),
  )
}

export function getPendingFirstSend(state: ChatStore): PendingFirstSend | null {
  const pending = state.pendingFirstSend
  if (!pending) return null
  // A cleared/evicted bucket no longer owns the shared pending slot.
  return findFirstSendMessage(state.sessionsById[pending.sessionId ?? '__pending'], pending.clientMessageId)
    ? pending
    : null
}

/** A bound chat ID alone is not proof that its first user entry was saved. */
export function hasFirstSendReceipt(message: ChatMessage | undefined): boolean {
  return message?.deliveryStatus === 'received' || message?.deliveryStatus === 'working'
}

/** Bound legacy chats may drain after their turn without claiming a save. */
export function firstSendBlocksQueue(state: ChatStore): boolean {
  const pending = getPendingFirstSend(state)
  return !!pending && (!pending.sessionId || pending.status !== 'unconfirmed')
}

export function removeFirstSendPlaceholder(bucket: SessionChatState, placeholderId: string): void {
  const placeholder = bucket.messagesById[placeholderId]
  if (placeholder?.role !== 'assistant' || placeholder.content.trim() || placeholder.tool_calls?.length) return
  delete bucket.messagesById[placeholderId]
  bucket.messageOrder = bucket.messageOrder.filter((id) => id !== placeholderId)
}

export function setFirstSendStatus(context: FirstSendContext, pending: PendingFirstSend, status: FirstSendStatus): void {
  const { set, get, withBucket } = context
  if (getPendingFirstSend(get())?.clientMessageId !== pending.clientMessageId) return
  set({ pendingFirstSend: { ...pending, status } })
  withBucket(pending.sessionId ?? '__pending', (bucket) => produce(bucket, (draft) => {
    const message = findFirstSendMessage(draft, pending.clientMessageId)
    if (message) {
      message.firstSendStatus = status
      message.status = status === 'not_saved' ? 'error' : 'done'
      message.deliveryStatus = hasFirstSendReceipt(message) ? 'received' : status === 'not_saved' ? 'failed' : 'sending'
    }
    removeFirstSendPlaceholder(draft, pending.assistantPlaceholderId)
    draft.isStreaming = false
    draft.isReplaying = status === 'checking_chat'
    draft.awaitingCatchUp = status === 'checking_chat'
  }) as Partial<SessionChatState>)
}

/** Recovery acknowledges a saved entry, not an active turn or a usable cursor. */
export function attachRecoveredFirstSend(context: FirstSendContext, sender?: Pick<WsConnection, 'send'>): boolean {
  const pending = getPendingFirstSend(context.get())
  if (!pending?.sessionId) return false
  const { connection, isConnected } = useConnectionStore.getState()
  const attachSender = sender ?? connection
  if (!attachSender || (!sender && !isConnected)) {
    setFirstSendStatus(context, pending, 'check_failed')
    return false
  }
  const attemptGeneration = context.get().firstSendGeneration + 1
  const checking = { ...pending, status: 'checking_chat' as const, attemptGeneration }
  context.set({ firstSendGeneration: attemptGeneration, pendingFirstSend: checking })
  setFirstSendStatus(context, checking, 'checking_chat')
  context.withBucket(pending.sessionId, () => ({
    cursor: null,
    recoveredFirstSend: { clientMessageId: pending.clientMessageId, attemptGeneration, reconciled: false },
  }))
  const sent = attachSender.send({ type: 'attach_session', session_id: pending.sessionId })
  if (!sent) {
    setFirstSendStatus(context, checking, 'check_failed')
    useConnectionStore.getState().setConnectionError('Could not check this chat — reconnect and press Retry.')
  }
  return sent
}

export function startOrdinaryFirstSend(
  context: FirstSendContext,
  outbound: MessageFrame & { client_message_id: string },
  queuedAt: string,
  workspaceId: string | null,
  attachments: MediaAttachment[],
): void {
  const payload = structuredClone(outbound)
  const attemptGeneration = context.get().firstSendGeneration + 1
  const assistantPlaceholderId = generateId()
  const pending: PendingFirstSend = {
    clientMessageId: payload.client_message_id,
    payload,
    workspaceId,
    attemptGeneration,
    sessionId: null,
    assistantPlaceholderId,
    status: 'sending',
  }
  const user: ChatMessage = {
    id: pending.clientMessageId,
    clientMessageId: pending.clientMessageId,
    session_id: '__pending',
    role: 'user',
    content: payload.content,
    timestamp: queuedAt,
    status: 'done',
    deliveryStatus: 'sending',
    firstSendStatus: 'sending',
    ...(payload.media?.length ? { mediaRefs: [...payload.media] } : {}),
    ...(attachments.length ? { media: structuredClone(attachments) } : {}),
  }
  const assistant: ChatMessage = {
    id: assistantPlaceholderId,
    session_id: '__pending',
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
  }
  context.set({ pendingFirstSend: pending, firstSendGeneration: attemptGeneration })
  context.withBucket('__pending', (bucket) => ({
    ...applyMessageArray([user, assistant], bucket),
    isStreaming: true,
    lastUserMessageAt: Date.now(),
  }))
  useSessionStore.getState().setActiveSession('__pending', payload.agent_id)
  context.get()._validateOutboundFrame(payload, '__pending')
  if (!useConnectionStore.getState().connection?.send(payload)) {
    setFirstSendStatus(context, pending, 'unconfirmed')
    useConnectionStore.getState().setConnectionError('Delivery not confirmed — reconnect and press Retry.')
  }
}

export function createFirstSendActions(context: FirstSendContext): Pick<ChatStore,
  'retryFirstSend' | 'reattachFirstSend' | 'markFirstSendDisconnected' | 'abandonPendingFirstSend' | 'generateFirstSendAgain'
> {
  const { set, get, withBucket } = context
  return {
    retryFirstSend: () => {
      const pending = getPendingFirstSend(get())
      const { connection, isConnected } = useConnectionStore.getState()
      if (!pending || !connection || !isConnected) return
      if (pending.status !== 'unconfirmed' && pending.status !== 'not_saved' && pending.status !== 'check_failed') return
      const message = findFirstSendMessage(get().sessionsById[pending.sessionId ?? '__pending'], pending.clientMessageId)
      if (pending.sessionId && hasFirstSendReceipt(message)) {
        attachRecoveredFirstSend(context)
        return
      }
      const attemptGeneration = get().firstSendGeneration + 1
      const retrying = { ...pending, status: 'retrying' as const, attemptGeneration }
      // Claim the attempt before send: another click cannot transmit concurrently.
      set({ firstSendGeneration: attemptGeneration, pendingFirstSend: retrying })
      setFirstSendStatus(context, retrying, 'retrying')
      get()._validateOutboundFrame(pending.payload, '__pending')
      if (!connection.send(pending.payload)) {
        setFirstSendStatus(context, retrying, 'unconfirmed')
        useConnectionStore.getState().setConnectionError('Delivery not confirmed — reconnect and press Retry.')
      }
    },
    reattachFirstSend: (connection) => attachRecoveredFirstSend(context, connection),
    markFirstSendDisconnected: () => {
      const pending = getPendingFirstSend(get())
      if (!pending) return
      const message = findFirstSendMessage(get().sessionsById[pending.sessionId ?? '__pending'], pending.clientMessageId)
      setFirstSendStatus(context, pending, hasFirstSendReceipt(message) ? 'check_failed' : pending.status === 'not_saved' ? 'not_saved' : 'unconfirmed')
    },
    abandonPendingFirstSend: () => {
      const pending = getPendingFirstSend(get())
      if (pending?.sessionId) {
        withBucket(pending.sessionId, (bucket) => produce(bucket, (draft) => {
          draft.recoveredFirstSend = undefined
          draft.isReplaying = false
          draft.awaitingCatchUp = false
          const message = findFirstSendMessage(draft, pending.clientMessageId)
          if (message && hasFirstSendReceipt(message)) message.firstSendStatus = 'saved'
        }) as Partial<SessionChatState>)
      }
      set((state) => {
        const sessionsById = { ...state.sessionsById }
        if (!state.pendingKickoff) delete sessionsById.__pending
        return {
          sessionsById,
          pendingFirstSend: null,
          abandonedFirstSendIds: pending
            ? [...state.abandonedFirstSendIds, pending.clientMessageId]
            : state.abandonedFirstSendIds,
          firstSendGeneration: state.firstSendGeneration + 1,
        }
      })
    },
    generateFirstSendAgain: (messageId) => {
      const sid = useSessionStore.getState().activeSessionId
      if (!sid || sid === '__pending') return
      const bucket = get().sessionsById[sid]
      const message = bucket?.messagesById[messageId]
      if (!message || (message.firstSendStatus !== 'unfinished' && message.firstSendStatus !== 'answer_not_started')) return
      if (!useConnectionStore.getState().isConnected || bucket.isStreaming || bucket.isReplaying) return
      withBucket(sid, (current) => produce(current, (draft) => {
        draft.messagesById[messageId].firstSendStatus = 'saved'
        draft.unansweredLastUserMessageId = null
      }) as Partial<SessionChatState>)
      // This is a new turn in a known session, never a same-ID delivery retry.
      get().sendMessage(message.content, { mediaRefs: message.mediaRefs, attachments: message.media })
    },
  }
}
