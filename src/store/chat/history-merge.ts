import type { HistoryActionKind } from './types'
import type { SessionHistoryPage, SessionHistoryEntry } from '@/lib/api/history-types'
import type { ChatMessage, SessionChatState } from './types'
import { historyMetadata, historyRowsPatch, mergeHistoryEntry, retainedPending } from './history-state'
import { isClearContextMarker } from '@/lib/clearMarker'

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
  if (oldOverlap.join() !== newOverlap.join()) throw new Error('Contradictory history overlap.')
  if ((action === 'older' || action === 'newer') && page.entries.length && page.entries.every(entry => existingPositions.has(entry.historyPosition))) throw new Error('History boundary did not advance.')
  const incoming = page.entries.map(entry => mergeHistoryEntry(entry, bucket.messagesById[entry.id], baseline[entry.id]))
  const union = action === 'older' ? [...incoming, ...old] : action === 'newer' ? [...old, ...incoming] : incoming
  const byPosition = new Map<string, ChatMessage>()
  for (const row of union) {
    if (!row.historyPosition) continue
    // A current/live overlapping row beats stale historical material.
    if (!byPosition.has(row.historyPosition) || old.includes(row as SessionHistoryEntry)) byPosition.set(row.historyPosition, row)
  }
  let positions = [...byPosition.keys()]
  const trimmed = positions.length > 500
  if (trimmed) {
    const removed = action === 'older' ? positions.slice(500) : positions.slice(0, positions.length - 500)
    const protectedSet = protection.get(page.session_id)
    if (removed.some(position => protectedSet?.has(position))) return {} // bounded deferred commit, never evict the reader
    positions = action === 'older' ? positions.slice(0, 500) : positions.slice(-500)
  }
  const rows = positions.map(position => byPosition.get(position)!)
  const rowIds = new Set(rows.map(row => row.id))
  // Canonical duplicate IDs select their last occurrence while source positions
  // remain distinct; no lexicographic cursor order is inferred.
  const finalRows = rows.filter((row, index) => !rows.slice(index + 1).some(later => later.id === row.id))
  for (const pending of retainedPending(bucket)) if (!rowIds.has(pending.id)) finalRows.push(pending)
  const patch = historyRowsPatch(bucket, finalRows, positions)
  const hasEarlier = action === 'older' || action === 'latest' ? page.has_earlier : (trimmed || history.hasEarlier === true)
  const hasLater = action === 'newer' || action === 'latest' ? page.has_later : (trimmed || history.hasLater === true)
  return {
    ...patch, historyNotice: undefined, hasEarlierMessages: hasEarlier,
    history: {
      ...patch.history!, historyLoaded: true, serverState: { status: 'available' }, pageMetadata: historyMetadata(page),
      viewMode: action === 'latest' ? 'latest' : 'historical', hasEarlier, hasLater,
      olderBoundary: positions[0] ?? null, newerBoundary: positions.at(-1) ?? null,
      tailEntries: action === 'latest' ? page.entries.slice(-100) : history.tailEntries,
    },
  }
}

export function reachedClosedClear(bucket: SessionChatState): SessionHistoryEntry | undefined {
  const first = currentHistoryEntries(bucket)[0]
  return first && isClearContextMarker(first) && !bucket.history.openedClearPositions.includes(first.historyPosition) ? first : undefined
}
