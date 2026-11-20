// #1428 step 2 (spec 4.8): a message the server refused (`send_refused`) or the
// browser withdrew is removed from its chat: its optimistic user bubble and,
// when that bubble started a turn, the still-empty assistant placeholder right
// after it. Other turns are never touched. A pending first send that this
// message held is released. The text itself is restored by bound-send.ts.

import { produce } from 'immer'
import type { ChatStore, SessionChatState } from './types'

type WithBucket = (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void

export function withdrawMessage(
  withBucket: WithBucket,
  get: () => ChatStore,
  sessionId: string | null,
  clientMessageId: string,
): void {
  const pending = get().pendingFirstSend
  const bucketId = sessionId ?? (pending?.clientMessageId === clientMessageId ? '__pending' : null)
  withBucket(bucketId, (bucket) => produce(bucket, (draft) => {
    const index = draft.messageOrder.findIndex((id) => {
      const message = draft.messagesById[id]
      return id === clientMessageId || (message?.role === 'user' && message.clientMessageId === clientMessageId)
    })
    if (index === -1) return
    const removed = [draft.messageOrder[index]]
    const nextId = draft.messageOrder[index + 1]
    const next = nextId ? draft.messagesById[nextId] : undefined
    if (next?.role === 'assistant' && next.isStreaming && !next.content) {
      removed.push(nextId)
      draft.isStreaming = false
    }
    draft.messageOrder = draft.messageOrder.filter((id) => !removed.includes(id))
    for (const id of removed) delete draft.messagesById[id]
  }) as Partial<SessionChatState>)
  if (pending?.clientMessageId === clientMessageId) get().abandonPendingFirstSend()
}
