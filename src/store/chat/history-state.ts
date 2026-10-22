import type { SessionHistoryState } from './types'
import type { SessionHistoryMetadata, SessionHistoryEntry, SessionHistoryPage } from '@/lib/api/history-types'
import type { ChatMessage, SessionChatState } from './types'
import { isPendingTailMessage } from './cursor'

export function emptyHistoryState(): SessionHistoryState {
  return {
    viewGeneration: 0, viewMode: 'latest', serverState: null, historyLoaded: false, liveControlsReady: false,
    pageMetadata: null, readerRange: null, readerPositions: [], tailEntries: [], openedClearPositions: [],
    hasEarlier: null, hasLater: null, olderBoundary: null, newerBoundary: null,
    requests: Object.fromEntries(['older', 'newer', 'latest', 'refresh'].map(kind => [kind, { status: 'idle', error: null, attempt: 0 }])) as SessionHistoryState['requests'],
  }
}
export function historyMetadata(page: SessionHistoryPage): SessionHistoryMetadata {
  const { session_id, entries, ...metadata } = page
  void session_id; void entries
  return metadata
}

/** Live objects changed since request start win; unchanged cached history can be refreshed. */
export function mergeHistoryEntry(entry: SessionHistoryEntry, current: ChatMessage | undefined, baseline: ChatMessage | undefined): ChatMessage {
  if (!current) return entry
  const liveWins = current !== baseline || current.isStreaming === true || current.status === 'streaming'
  const merged = { ...entry, ...(liveWins ? current : {}), id: entry.id, historyPosition: entry.historyPosition } as ChatMessage
  if (entry.role === 'assistant' && current.role === 'assistant' && (entry.tool_calls || current.tool_calls)) {
    const calls = new Map((entry.tool_calls ?? []).map(call => [call.id, call]))
    for (const call of current.tool_calls ?? []) {
      const incoming = calls.get(call.id)
      if (call.status === 'cancelled' || liveWins || (call.status !== 'running' && (!incoming || incoming.status === 'running'))) calls.set(call.id, call)
    }
    merged.tool_calls = [...calls.values()]
  }
  return merged
}

export function retainedPending(bucket: SessionChatState): ChatMessage[] {
  const owners = new Set(bucket.toolCallOrder.flatMap(id => bucket.toolCallOwnerMessageId?.[id] ? [bucket.toolCallOwnerMessageId[id]] : []))
  return [...new Set([...bucket.messageOrder, ...(bucket.historyLiveOrder ?? [])])].flatMap(id => {
    const row = bucket.messagesById[id]
    const recovered = row?.role === 'user' && bucket.recoveredFirstSend && (row.clientMessageId ?? row.id) === bucket.recoveredFirstSend.clientMessageId
    return row && (isPendingTailMessage(row) || row.isStreaming || recovered || ((bucket.isStreaming || bucket.activeTurnId) && owners.has(id))) ? [row] : []
  })
}

/** One body owner (the existing message maps), bounded aliases and whole-group cleanup. */
export function historyRowsPatch(bucket: SessionChatState, rows: ChatMessage[], positions: string[]): Partial<SessionChatState> {
  const messagesById = Object.fromEntries(rows.map(row => [row.id, row]))
  const ids = new Set(rows.map(row => row.id))
  const calls = new Set(rows.flatMap(row => row.tool_calls?.map(call => call.id) ?? []))
  const keepCalls = Object.fromEntries(Object.entries(bucket.toolCalls).filter(([id]) => calls.has(id) || (bucket.toolCallOwnerMessageId?.[id] && ids.has(bucket.toolCallOwnerMessageId[id]))))
  const first = positions[0], last = positions.at(-1)
  return {
    messagesById, messageOrder: [...new Set(rows.map(row => row.id))], historyLiveOrder: [],
    historySourceAliases: Object.fromEntries(positions.flatMap(position => {
      const row = rows.find(item => item.historyPosition === position)
      const id = row?.id ?? bucket.historySourceAliases?.[position]
      return id && ids.has(id) ? [[position, id]] : []
    })),
    toolCalls: keepCalls,
    toolCallOrder: bucket.toolCallOrder.filter(id => id in keepCalls),
    toolCallOwnerMessageId: Object.fromEntries(Object.entries(bucket.toolCallOwnerMessageId ?? {}).filter(([, owner]) => ids.has(owner))),
    textAtToolCallStart: Object.fromEntries(Object.entries(bucket.textAtToolCallStart).filter(([id]) => id in keepCalls)),
    mergedReplayMessageIds: Object.fromEntries(rows.flatMap(row => (row.mergedReplayIds ?? []).map(id => [id, true as const]))),
    spanBySpanId: Object.fromEntries(Object.entries(bucket.spanBySpanId ?? {}).filter(([, owner]) => ids.has(owner.messageId))),
    history: { ...bucket.history, readerPositions: positions, readerRange: first && last ? { window_start: first, window_end: last } : null },
  }
}
