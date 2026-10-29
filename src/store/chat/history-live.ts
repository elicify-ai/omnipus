// Current live execution shares the existing maps, but never appends a distant
// current tail to the historical reader's displayed order.
import type { ChatStore, HistorySliceContext, SessionChatState } from './types'
import { getMessages } from './messages'
import { historyRowsPatch } from './history-state'
import { isPendingTailMessage } from './cursor'

export function liveProjectionBucket(bucket: SessionChatState): SessionChatState {
  const recent = bucket.history.tailEntries
  const tail = [...new Set([...(bucket.historyLiveOrder ?? []), ...bucket.messageOrder.filter(id => !bucket.messagesById[id]?.historyPosition)])]
  const recentIds = new Set(recent.map(row => row.id))
  const order = [...recentIds, ...tail.filter(id => !recentIds.has(id))]
  // No canonical page yet: existing live/pending rows are the current view,
  // not an old reader interval. Never use an unrelated old range as fallback.
  if (!order.length && !bucket.history.readerPositions.length) order.push(...bucket.messageOrder)
  // #1452 S11b: a restored cached row owns its saved calls, as its row does on the reader.
  const owners = Object.fromEntries(recent.flatMap(row => (row.tool_calls ?? []).map(call => [call.id, row.id])))
  return { ...bucket, messagesById: { ...Object.fromEntries(recent.map(row => [row.id, row])), ...bucket.messagesById }, messageOrder: order,
    ...(Object.keys(owners).length ? { toolCallOwnerMessageId: { ...owners, ...bucket.toolCallOwnerMessageId } } : {}) }
}

export function withLiveHistoryBucket(withBucket: HistorySliceContext['withBucket'], sid: string | null, update: Parameters<HistorySliceContext['withBucket']>[1]): void {
  withBucket(sid, bucket => {
    if (bucket.history.viewMode !== 'historical') return update(bucket)
    const projected = liveProjectionBucket(bucket)
    return detachedLivePatch(bucket, { ...projected, ...update(projected) })
  })
}

export function currentHistoryState(state: ChatStore, sid: string | null): ChatStore {
  const bucket = sid ? state.sessionsById[sid] : undefined
  if (!sid || !bucket || bucket.history.viewMode !== 'historical') return state
  const current = liveProjectionBucket(bucket)
  return { ...state, messages: getMessages(current), sessionsById: { ...state.sessionsById, [sid]: current } }
}

export function detachedLivePatch(before: SessionChatState, projected: SessionChatState): Partial<SessionChatState> {
  // An operation-owned successful clear is an intentional view replacement.
  if (projected.history.viewMode === 'latest') return projected
  const previousTail = new Set(before.historyLiveOrder ?? [])
  const readerOrder = before.messageOrder.filter(id => !previousTail.has(id) && !isPendingTailMessage(before.messagesById[id]))
  const readerIds = new Set(readerOrder)
  const cached = new Map(before.history.tailEntries.map(row => [row.id, row]))
  const toolOwners = new Set(projected.toolCallOrder.flatMap(id => projected.toolCallOwnerMessageId?.[id] ? [projected.toolCallOwnerMessageId[id]] : []))
  const pending = projected.messageOrder.filter(id => !readerIds.has(id) && isPendingTailMessage(projected.messagesById[id]))
  const visible = [...readerOrder.filter(id => !!projected.messagesById[id]), ...pending]
  // Unchanged cached recent rows stay solely in the cache. Mutable live owners
  // share the existing maps, including a selected source's active tool owner.
  const hidden = projected.messageOrder.filter(id => !readerIds.has(id) && (previousTail.has(id) || !cached.has(id) || projected.messagesById[id] !== cached.get(id) || toolOwners.has(id))).slice(-100)
  const hiddenIds = new Set(hidden)
  const keep = [...new Set([...visible, ...hidden])]
  const rows = keep.flatMap(id => projected.messagesById[id] ? [projected.messagesById[id]] : [])
  const patch = historyRowsPatch(projected, rows, before.history.readerPositions)
  const tailCapacity = Math.max(0, 100 - hidden.length)
  const tailEntries = before.history.tailEntries.filter(row => !hiddenIds.has(row.id))
  return {
    ...projected, ...patch, messageOrder: visible, historyLiveOrder: hidden,
    history: { ...projected.history, readerPositions: before.history.readerPositions, readerRange: before.history.readerRange,
      tailEntries: tailCapacity ? tailEntries.slice(-tailCapacity) : [] },
  }
}
