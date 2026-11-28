import type { SessionHistoryState } from './types'
import type { SessionHistoryMetadata, SessionHistoryEntry, SessionHistoryPage } from '@/lib/api/history-types'
import type { ChatMessage, MediaAttachment, SessionChatState } from './types'
import { isPendingTailMessage } from './cursor'
import { mediaDedupKey, replaceThumbnailGroups, thumbnailGroupItems } from './media-identity'

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

/**
 * One card per media item, in first-seen order; a later copy replaces the
 * earlier one. Items are the same by mediaDedupKey, never by URL alone: the
 * unavailable markers of two thumbnail groups both have an empty URL and
 * must stay two cards (B06: one group's card vanished on reload).
 */
export function unionMedia(media: readonly MediaAttachment[]): MediaAttachment[] {
  return [...new Map(media.map(item => [mediaDedupKey(item), item])).values()]
}

/**
 * The media of a combined row. Thumbnail groups resolve whole, by tool call
 * (office-preview §5.2/§5.3): a group the saved record carries replaces the
 * cached one in place, so live pages of a Retry that was never saved do not
 * survive a snapshot (B07) and a saved Retry is not shown beside its live
 * copy (B06). The baseline rule still holds per group: a group that changed
 * live since the read began (or any group, when there is no baseline to
 * compare with) is newer than the saved record and is kept. Other media is
 * unioned as before (chat-links BS-5.4).
 */
function mergeHistoryMedia(current: ChatMessage, saved: ChatMessage, baseline: ChatMessage | undefined, liveWins: boolean): MediaAttachment[] {
  const currentMedia = current.media ?? []
  const newerLive = new Set<string>()
  if (liveWins) {
    for (const group of new Set(currentMedia.flatMap(item => item.presentation === 'page_thumbnails' && item.toolCallId ? [item.toolCallId] : []))) {
      const live = thumbnailGroupItems(currentMedia, group)
      const before = baseline ? thumbnailGroupItems(baseline.media ?? [], group) : []
      if (!baseline || live.length !== before.length || live.some((item, i) => item !== before[i])) newerLive.add(group)
    }
  }
  const savedMedia = (saved.media ?? []).filter(item => !(item.presentation === 'page_thumbnails' && item.toolCallId && newerLive.has(item.toolCallId)))
  const { merged, rest } = replaceThumbnailGroups(currentMedia, savedMedia)
  return unionMedia([...merged, ...rest])
}

/**
 * Live objects changed since request start win; unchanged cached history can be refreshed.
 * mediaBaseline is what thumbnail groups are compared with; it defaults to
 * baseline, and a caller that passes no baseline to keep the live text still
 * passes the captured row here, so an unchanged group takes its saved state.
 */
export function mergeHistoryEntry(entry: SessionHistoryEntry, current: ChatMessage | undefined, baseline: ChatMessage | undefined, mediaBaseline: ChatMessage | undefined = baseline): ChatMessage {
  if (!current) return entry
  const liveWins = current !== baseline || current.isStreaming === true || current.status === 'streaming'
  const merged = { ...entry, ...(liveWins ? current : {}), id: entry.id, historyPosition: entry.historyPosition } as ChatMessage
  // #1452 D2: the saved preview size describes the saved text only. When the
  // current text wins, the row keeps the current text's own size marker.
  if (liveWins) merged.contentOriginalSizeBytes = current.contentOriginalSizeBytes
  // chat-links BS-5.4: a sent-file card arrives only with the snapshot's media
  // frame. A record without media does not mean the card is gone: REST pages
  // never carry one, and only one source of a combined row holds it.
  const saved: ChatMessage = entry
  if (saved.role === 'assistant' && current.role === 'assistant' && (saved.media?.length || current.media?.length)) {
    merged.media = mergeHistoryMedia(current, saved, mediaBaseline, liveWins)
  }
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
