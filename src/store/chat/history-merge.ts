import type { HistoryActionKind } from './types'
import type { SessionHistoryPage, SessionHistoryEntry } from '@/lib/api/history-types'
import type { ChatMessage, SessionChatState } from './types'
import { historyMetadata, historyRowsPatch, mergeHistoryEntry, retainedPending } from './history-state'
import { isClearContextMarker } from '@/lib/clearMarker'
import { mergeRecordedSessionTokens } from './session-usage'
import { fallbackPairKey } from './slices/provider-frames'

/** Expected contradiction in a page's relation to the current reader. */
export class HistoryPageProtocolError extends Error {}

/** A valid page cannot currently replace a protected reader edge. */
export class HistoryPageDeferredError extends Error {
  constructor() {
    super('Loading this page is paused to protect your reading position. Scroll or move focus, then retry.')
    this.name = 'HistoryPageDeferredError'
  }
}

// DOM/measurement protection is ephemeral and never serialized into chat state.
const protection = new Map<string, ReadonlySet<string>>()
export function protectHistoryPositions(sessionId: string, positions: ReadonlySet<string>): void { protection.set(sessionId, positions) }
export function releaseHistoryProtection(sessionId: string): void { protection.delete(sessionId) }

export function currentHistoryEntries(bucket: SessionChatState): SessionHistoryEntry[] {
  const byPosition = new Map(Object.values(bucket.messagesById).filter(row => row.historyPosition).map(row => [row.historyPosition!, row as SessionHistoryEntry]))
  return bucket.history.readerPositions.flatMap(position => {
    const alias = bucket.historySourceAliases?.[position]
    const row = byPosition.get(position) ?? (alias ? bucket.messagesById[alias] : undefined)
    return row ? [{ ...row, historyPosition: position } as SessionHistoryEntry] : []
  })
}

export function applyHistoryPageToBucket(bucket: SessionChatState, page: SessionHistoryPage, action: HistoryActionKind, baseline: Record<string, ChatMessage> = bucket.messagesById): Partial<SessionChatState> {
  const history = bucket.history
  const old = currentHistoryEntries(bucket)
  const pagePositions = page.entries.map(entry => entry.historyPosition)
  const existingPositions = new Set(history.readerPositions)
  const oldOverlap = history.readerPositions.filter(position => pagePositions.includes(position))
  const newOverlap = pagePositions.filter(position => existingPositions.has(position))
  if (oldOverlap.join() !== newOverlap.join()) throw new HistoryPageProtocolError('Contradictory history overlap.')
  if ((action === 'older' || action === 'newer') && page.entries.length && page.entries.every(entry => existingPositions.has(entry.historyPosition))) throw new HistoryPageProtocolError('History boundary did not advance.')
  const incoming = page.entries.map(entry => mergeHistoryEntry(entry, bucket.messagesById[entry.id], baseline[entry.id]))
  const union = action === 'older' ? [...incoming, ...old] : action === 'newer' ? [...old, ...incoming] : incoming
  const byPosition = new Map<string, ChatMessage>()
  for (const row of union) {
    if (!row.historyPosition) continue
    if (!byPosition.has(row.historyPosition)) byPosition.set(row.historyPosition, row)
  }
  // Incoming values already passed the captured-baseline/live-wins merge. Do
  // not overwrite fresh unchanged history merely because its cached row exists.
  for (const row of incoming) if (row.historyPosition) byPosition.set(row.historyPosition, row)
  let positions = [...byPosition.keys()]
  const trimmed = positions.length > 500
  if (trimmed) {
    const removed = action === 'older' ? positions.slice(500) : positions.slice(0, positions.length - 500)
    const protectedSet = protection.get(page.session_id)
    if (removed.some(position => protectedSet?.has(position))) throw new HistoryPageDeferredError()
    positions = action === 'older' ? positions.slice(0, 500) : positions.slice(-500)
  }
  const rows = positions.map(position => byPosition.get(position)!)
  const rowIds = new Set(rows.map(row => row.id))
  // Canonical duplicate IDs select their last occurrence while source positions
  // remain distinct; no lexicographic cursor order is inferred.
  const finalRows = rows.filter((row, index) => !rows.slice(index + 1).some(later => later.id === row.id))
  const hiddenIds = action === 'latest' ? [] : (bucket.historyLiveOrder ?? [])
  const confirmedClients = new Set(finalRows.flatMap(row => row.clientMessageId ? [row.clientMessageId] : []))
  for (const pending of retainedPending(bucket)) {
    if (!hiddenIds.includes(pending.id) && !rowIds.has(pending.id) && !confirmedClients.has(pending.clientMessageId ?? pending.id)) finalRows.push(pending)
  }
  if (action === 'latest') {
    for (const current of Object.values(bucket.messagesById)) {
      if (!current.historyPosition && !baseline[current.id] && !finalRows.some(row => row.id === current.id)) finalRows.push(current)
    }
  }
  const hidden = hiddenIds.flatMap(id => bucket.messagesById[id] ? [bucket.messagesById[id]] : [])
  const patch = { ...historyRowsPatch(bucket, [...finalRows, ...hidden], positions), messageOrder: [...new Set(finalRows.map(row => row.id))], historyLiveOrder: hiddenIds }
  patch.historySourceAliases = Object.fromEntries(positions.flatMap(position => {
    const row = byPosition.get(position)
    return row && patch.messagesById?.[row.id] ? [[position, row.id]] : []
  }))
  const hasEarlier = action === 'older' || action === 'latest' ? page.has_earlier : (trimmed || history.hasEarlier === true)
  const hasLater = action === 'newer' || action === 'latest' ? page.has_later : (trimmed || history.hasLater === true)
  // PF=A / seams Q4: a REST-loaded fallback note seeds the existing D17
  // once-per-chat pair guard, exactly as its replay carrier does, but only
  // when BOTH recorded models are known. Never inferred from the note text.
  const knownPairs = incoming.flatMap(row => row.providerFallbackNote?.answeredModel && row.providerFallbackNote.unavailableModel
    ? [fallbackPairKey(row.providerFallbackNote.unavailableModel, row.providerFallbackNote.answeredModel)] : [])
  return {
    ...patch, historyNotice: undefined, historyNeedsSnapshot: false, hasEarlierMessages: hasEarlier,
    ...(knownPairs.length ? { providerFallbackPairsSeen: new Set([...(bucket.providerFallbackPairsSeen ?? []), ...knownPairs]) } : {}),
    sessionTokens: mergeRecordedSessionTokens(bucket.sessionTokens, page.session_tokens_total),
    history: {
      ...patch.history!, historyLoaded: true, serverState: { status: 'available' }, pageMetadata: historyMetadata(page),
      viewMode: action === 'latest' ? 'latest' : history.viewMode, hasEarlier, hasLater,
      olderBoundary: positions[0] ?? null, newerBoundary: positions.at(-1) ?? null,
      tailEntries: action === 'latest' ? page.entries.slice(-100) : history.tailEntries,
    },
  }
}

export function reachedClosedClear(bucket: SessionChatState): SessionHistoryEntry | undefined {
  const first = currentHistoryEntries(bucket)[0]
  return first && isClearContextMarker(first) && !bucket.history.openedClearPositions.includes(first.historyPosition) ? first : undefined
}
