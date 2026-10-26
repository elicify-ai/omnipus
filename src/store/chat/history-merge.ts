import type { HistoryActionKind } from './types'
import type { SessionHistoryPage, SessionHistoryEntry } from '@/lib/api/history-types'
import type { ChatMessage, SessionChatState } from './types'
import { historyMetadata, historyRowsPatch, mergeHistoryEntry, retainedPending } from './history-state'
import { isClearContextMarker } from '@/lib/clearMarker'
import { mergeRecordedSessionTokens } from './session-usage'
import { applySpanFrame } from './slices/span-frames'
import { emptySessionState } from './session'
import type { HistorySpanFrame } from '@/lib/api/sessions'

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

// Persisted span facts per session and span, keyed by source position. Paged
// history can hold a span's start and its later state/end on different pages
// (or only in a page's bounded context), so the facts outlive one page and are
// re-settled whenever the start row is on the reader. Never serialized.
const SPAN_FACT_SPAN_CAP = 200
const spanFacts = new Map<string, Map<string, Map<string, HistorySpanFrame>>>()
export function releaseHistorySpanFacts(sessionId?: string): void {
  if (sessionId === undefined) spanFacts.clear()
  else spanFacts.delete(sessionId)
}

function rememberSpanFact(sessionId: string, frame: HistorySpanFrame): void {
  const bySpan = spanFacts.get(sessionId) ?? new Map<string, Map<string, HistorySpanFrame>>()
  spanFacts.set(sessionId, bySpan)
  const facts = bySpan.get(frame.span_id) ?? new Map<string, HistorySpanFrame>()
  if (!bySpan.has(frame.span_id)) {
    bySpan.set(frame.span_id, facts)
    if (bySpan.size > SPAN_FACT_SPAN_CAP) bySpan.delete(bySpan.keys().next().value!)
  }
  facts.set(frame.history_origin?.position ?? `${frame.type}:${JSON.stringify(frame)}`, frame)
}

const FACT_ORDER: Record<HistorySpanFrame['type'], number> = { subagent_start: 0, subagent_state: 1, subagent_message: 2, subagent_end: 3 }

/**
 * Settle persisted span facts onto their start rows (ADR-091 D7/I-4, #1266):
 * the span lives on its start entry's own row, as replay has always placed it,
 * and its state/message/end come from facts on the reader in page order, then
 * the page context's latest facts. Rows carrying only state/message/end stay
 * silent; a span whose start is not on the reader draws nothing until that
 * page loads. One rule set with the live path (applySpanFrame), so no fact
 * misses its span. A span a live row already holds is left to the live row.
 */
function settleHistorySpans(sessionId: string, rows: ChatMessage[], positions: string[], context: SessionHistoryPage['page_info']['context'] | undefined): { rows: ChatMessage[]; index: SessionChatState['spanBySpanId'] } {
  for (const row of rows) if (row.role === 'assistant' && row.historySpanFrame) rememberSpanFact(sessionId, row.historySpanFrame)
  for (const span of context?.spans ?? []) for (const fact of [span.start, span.state, span.message, span.end]) if (fact) rememberSpanFact(sessionId, fact)
  const bySpan = spanFacts.get(sessionId)
  const index: NonNullable<SessionChatState['spanBySpanId']> = {}
  if (!bySpan) return { rows, index }
  const order = new Map(positions.map((position, i) => [position, i]))
  const live = new Set(rows.filter(row => !row.historyPosition).flatMap(row => row.spans?.map(span => span.spanId) ?? []))
  const settled = rows.map(row => {
    const start = row.role === 'assistant' ? row.historySpanFrame : undefined
    if (start?.type !== 'subagent_start' || live.has(start.span_id)) return row
    const facts = [...(bySpan.get(start.span_id)?.values() ?? [])].filter(fact => fact.type !== 'subagent_start')
    const onReader = facts.filter(fact => order.has(fact.history_origin?.position ?? '')).sort((a, b) => order.get(a.history_origin!.position)! - order.get(b.history_origin!.position)!)
    const contextOnly = facts.filter(fact => !order.has(fact.history_origin?.position ?? '')).sort((a, b) => FACT_ORDER[a.type] - FACT_ORDER[b.type])
    let mini: SessionChatState = { ...emptySessionState(), messagesById: { [row.id]: { ...row, spans: undefined } }, messageOrder: [row.id] }
    mini = { ...mini, ...applySpanFrame(mini, start, sessionId) }
    for (const fact of [...onReader, ...contextOnly]) {
      if (!mini.spanBySpanId?.[start.span_id]) break // ended: nothing applies after an end
      mini = { ...mini, ...applySpanFrame(mini, fact, sessionId) }
    }
    const entry = mini.spanBySpanId?.[start.span_id]
    if (entry) index[start.span_id] = entry
    // The span reducers only touch the row's spans; status stays the saved one.
    return mini.messagesById[row.id]
  })
  return { rows: settled, index }
}

export function currentHistoryEntries(bucket: SessionChatState): SessionHistoryEntry[] {
  const byPosition = new Map(Object.values(bucket.messagesById).filter(row => row.historyPosition).map(row => [row.historyPosition!, row as SessionHistoryEntry]))
  return bucket.history.readerPositions.flatMap(position => {
    const alias = bucket.historySourceAliases?.[position]
    const row = byPosition.get(position) ?? (alias ? bucket.messagesById[alias] : undefined)
    return row ? [{ ...row, historyPosition: position } as SessionHistoryEntry] : []
  })
}

/** The display row that coalesced this saved source's id, same turn and producer. */
function coalescedOwner(bucket: SessionChatState, entry: SessionHistoryEntry): ChatMessage | undefined {
  const owners = Object.values(bucket.messagesById).filter(row =>
    row.role === entry.role && row.mergedReplayIds?.includes(entry.id) &&
    (!entry.turnId || !row.turnId || entry.turnId === row.turnId) && (!entry.agentId || !row.agentId || entry.agentId === row.agentId))
  return owners.length === 1 ? owners[0] : undefined
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
  const incoming = page.entries.map(entry => {
    // A saved source whose id a displayed reply already coalesced (the token
    // resolver's mergedReplayIds) merges onto that display row: it keeps its
    // identity and live text and takes the saved position, never a second
    // reply beside it (D4/D7, r21).
    const owner = bucket.messagesById[entry.id] ? undefined : coalescedOwner(bucket, entry)
    return owner ? mergeHistoryEntry({ ...entry, id: owner.id }, owner, baseline[owner.id]) : mergeHistoryEntry(entry, bucket.messagesById[entry.id], baseline[entry.id])
  })
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
  const spans = settleHistorySpans(page.session_id, finalRows, positions, page.page_info.context)
  const patch = { ...historyRowsPatch(bucket, [...spans.rows, ...hidden], positions), messageOrder: [...new Set(spans.rows.map(row => row.id))], historyLiveOrder: hiddenIds }
  patch.spanBySpanId = { ...patch.spanBySpanId, ...spans.index }
  patch.historySourceAliases = Object.fromEntries(positions.flatMap(position => {
    const row = byPosition.get(position)
    return row && patch.messagesById?.[row.id] ? [[position, row.id]] : []
  }))
  const hasEarlier = action === 'older' || action === 'latest' ? page.has_earlier : (trimmed || history.hasEarlier === true)
  const hasLater = action === 'newer' || action === 'latest' ? page.has_later : (trimmed || history.hasLater === true)
  return {
    ...patch, historyNotice: undefined, historyNeedsSnapshot: false, hasEarlierMessages: hasEarlier,
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
