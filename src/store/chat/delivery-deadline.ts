import { produce } from 'immer'
import { busyFlagIsUnbacked } from './messages'
import type { ChatStore, SessionChatState } from './types'

// Local acknowledgement deadline only: never a turn timeout, cancel or resend.
// Founder decision Q67 (#1428).
export const DELIVERY_CONFIRM_DEADLINE_MS = 15_000

const deadlines = new Map<string, ReturnType<typeof setTimeout>>()

export function remainingDeliveryDelay(deadlineAt?: number): number {
  return deadlineAt === undefined ? DELIVERY_CONFIRM_DEADLINE_MS : Math.max(0, deadlineAt - Date.now())
}

export function cancelDeliveryDeadline(key: string): void {
  const timer = deadlines.get(key)
  if (timer !== undefined) clearTimeout(timer)
  deadlines.delete(key)
}

export function armDeliveryDeadline(key: string, expire: () => void, delay = DELIVERY_CONFIRM_DEADLINE_MS): void {
  cancelDeliveryDeadline(key)
  const timer = setTimeout(() => {
    if (deadlines.get(key) !== timer) return
    deadlines.delete(key)
    expire()
  }, Math.max(0, delay))
  deadlines.set(key, timer)
}

/**
 * A send the server failed or discarded never starts a turn. Drop the empty
 * optimistic "thinking" placeholder sendMessage put right after the user row,
 * then release the busy flag only if nothing else backs it (another running
 * turn, a streaming bubble, a running tool). A mid-turn steer has no
 * placeholder, so its discard removes nothing and never stops the running turn.
 */
export function releaseUnstartedSend(draft: SessionChatState, userMessageId: string): void {
  const idx = draft.messageOrder.indexOf(userMessageId)
  const nextId = idx === -1 ? undefined : draft.messageOrder[idx + 1]
  const next = nextId ? draft.messagesById[nextId] : undefined
  const ownsLiveTool = !!nextId && Object.values(draft.toolCallOwnerMessageId ?? {}).includes(nextId)
  const isOptimisticPlaceholder =
    !!next && next.role === 'assistant' && (next.isStreaming || next.status === 'streaming') &&
    !next.content && !next.turnId && !next.agentId && !next.tool_calls?.length && !next.spans?.length && !next.media?.length && !ownsLiveTool
  if (nextId && isOptimisticPlaceholder) {
    delete draft.messagesById[nextId]
    draft.messageOrder.splice(idx + 1, 1)
  }
  if (busyFlagIsUnbacked(draft)) draft.isStreaming = false
}

export function armMessageDeliveryDeadline(
  get: () => ChatStore,
  withBucket: (sid: string | null, update: (bucket: SessionChatState) => Partial<SessionChatState>) => void,
  sid: string,
  messageId: string,
  drain: () => void,
  delay = DELIVERY_CONFIRM_DEADLINE_MS,
): void {
  armDeliveryDeadline(`message:${sid}:${messageId}`, () => {
    const message = get().sessionsById[sid]?.messagesById[messageId]
    if (!message || message.deliveryStatus !== 'sending') return
    withBucket(sid, bucket => produce(bucket, draft => {
      const user = draft.messagesById[messageId]
      user.deliveryStatus = 'failed'
      user.deliveryUnconfirmed = true
      user.status = 'error'
      releaseUnstartedSend(draft, messageId)
    }) as Partial<SessionChatState>)
    drain()
  }, delay)
}
