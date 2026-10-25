// Queued input keeps its owner and remains recoverable if it cannot be sent.
import type { StoreApi } from 'zustand'
import { useUiStore } from '@/store/ui'
import { armDeliveryDeadline, DELIVERY_CONFIRM_DEADLINE_MS } from './delivery-deadline'
import { outboundTargetIsForeground } from './outbound-target'
import type { ChatStore, OutboundQueueItem, QueuedOutboundMessage } from './types'

export function bindQueuedProvisionalChat(queue: OutboundQueueItem[], target: NonNullable<QueuedOutboundMessage['target']>, clientMessageId: string): OutboundQueueItem[] {
  return queue.map(item => typeof item !== 'string' && item.target?.sessionId === null
    && item.target.agentId === target.agentId && item.target.workspaceId === target.workspaceId && item.target.draftGeneration === target.draftGeneration
    ? { ...item, target: { ...item.target, sessionId: '__pending', firstSendClientMessageId: clientMessageId } }
    : item)
}

export function bindQueuedFirstSend(queue: OutboundQueueItem[], clientMessageId: string, sessionId: string): OutboundQueueItem[] {
  return queue.map(item => typeof item !== 'string' && item.target?.firstSendClientMessageId === clientMessageId
    ? { ...item, target: { ...item.target, sessionId } }
    : item)
}

export function armQueuedDeliveryDeadline(get: () => ChatStore, set: StoreApi<ChatStore>['setState'], item: QueuedOutboundMessage): void {
  const deadlineAt = item.deadlineAt ?? Date.now() + DELIVERY_CONFIRM_DEADLINE_MS
  item.deadlineAt = deadlineAt
  armDeliveryDeadline(`queue:${item.id}`, () => {
    const waiting = [...get().outboundQueue, ...get().pendingDrainQueue].some(candidate =>
      typeof candidate !== 'string' && candidate.id === item.id && candidate.deliveryStatus !== 'failed',
    )
    if (!waiting) return
    const expire = (queue: OutboundQueueItem[]) => queue.map(candidate =>
      typeof candidate !== 'string' && candidate.id === item.id ? { ...candidate, deliveryStatus: 'failed' as const } : candidate,
    )
    set(state => ({ outboundQueue: expire(state.outboundQueue), pendingDrainQueue: expire(state.pendingDrainQueue) }))
    // The owning row remains in its chat. Navigation cannot hide the failure.
    useUiStore.getState().addToast({ message: 'Queued message not delivered. Your message was kept in its chat; return there and retry.', variant: 'error' })
    get().drainOutboundQueue()
  }, deadlineAt - Date.now())
}

export function retryQueuedMessage(get: () => ChatStore, set: StoreApi<ChatStore>['setState'], id: string): boolean {
  const item = [...get().outboundQueue, ...get().pendingDrainQueue].find((candidate): candidate is QueuedOutboundMessage =>
    typeof candidate !== 'string' && candidate.id === id && candidate.deliveryStatus === 'failed',
  )
  if (!item) return false
  // A not-yet-created chat is retried from its own composer context only.
  if (item.target && !item.target.sessionId && !outboundTargetIsForeground(item.target)) {
    useUiStore.getState().addToast({ message: 'Reopen the original chat before retrying this queued message.', variant: 'warning' })
    return true
  }
  const retry = { ...item, deliveryStatus: 'queued' as const, deadlineAt: Date.now() + DELIVERY_CONFIRM_DEADLINE_MS }
  const remove = (queue: OutboundQueueItem[]) => queue.filter(candidate => typeof candidate === 'string' || candidate.id !== id)
  set(state => ({ outboundQueue: [...remove(state.outboundQueue), retry], pendingDrainQueue: remove(state.pendingDrainQueue) }))
  armQueuedDeliveryDeadline(get, set, retry)
  get().drainOutboundQueue()
  return true
}
