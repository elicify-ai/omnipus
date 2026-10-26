import type { HistoryActionKind } from './types'
import type { SessionHistoryPage, SessionHistoryEntry } from '@/lib/api/history-types'
import type { ChatMessage, SessionChatState } from './types'
import { historyMetadata, historyRowsPatch, mergeHistoryEntry, retainedPending } from './history-state'
import { isClearContextMarker } from '@/lib/clearMarker'
import { mergeRecordedSessionTokens } from './session-usage'
import { applySpanFrame } from './slices/span-frames'
import { emptySessionState } from './session'
import type { HistorySpanFrame } from '@/lib/api/sessions'
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

// Persisted span facts per session and span. Paged history can hold a span's
// start and its later state/end on different pages (or only in a page's
// bounded context), so the facts outlive one page and are re-settled whenever
// the start row is on the reader. Never serialized.
//
// Chronology (final-gate I2): facts are remembered once per READ (a page, a
// refresh) or live frame, never by re-reading rows already on the reader. A
// read stamps its facts with one tick and ranks them in page order; its page
// context is the server's latest state at the read's cut, so it ranks above the
// read's own entries; a later read or live frame ranks above an earlier one.
// Settlement applies the latest state and message by that ranking, so an older
// page loaded later cannot overwrite the state a newer read established.
//
// Bound (final-gate I4/F4, spec D9): each span keeps only the facts settlement
// reads, the latest of each kind (SpanRecord), and the memory holds only spans
// a retained row still carries: a saved span source row within the 500-group
// reader, or a span drawn on a reader or 100-row live-tail row. Every other
// span's facts are released at each history commit and whenever a live frame
// opens a new span record.
type SpanFact = { frame: HistorySpanFrame; tick: number; rank: number } // not-wire-format: in-memory settlement bookkeeping
type HistoryContext = SessionHistoryPage['page_info']['context'] | undefined // not-wire-format: alias of a generated type
type SubagentMessage = Extract<HistorySpanFrame, { type: 'subagent_message' }> // not-wire-format: alias of a generated type
// One slot per fact kind settlement applies. `statusMessage` is the latest
// message that sets the span's status line (span-frames.ts::reduceSubagentMessage),
// `lastMessage` the latest message of any other kind, which only refreshes the
// update time; the line therefore shows exactly what in-order application of
// every message would show. `ran` holds the server's proof that the child left
// the queue (a page context's has_run, F4-arch) or a recorded ran-state.
type SpanRecord = { state?: SpanFact; statusMessage?: SpanFact; lastMessage?: SpanFact; notDelivered?: SpanFact; end?: SpanFact; ran: boolean } // not-wire-format: in-memory settlement bookkeeping
type FactSlot = Exclude<keyof SpanRecord, 'ran'>
const spanFacts = new Map<string, Map<string, SpanRecord>>()
let factTick = 0
export function releaseHistorySpanFacts(sessionId?: string): void {
  if (sessionId === undefined) spanFacts.clear()
  else spanFacts.delete(sessionId)
}

const laterFact = (a: SpanFact, b: SpanFact) => a.tick - b.tick || a.rank - b.rank
/** Lifecycle states that prove the child left the queue (span-frames.ts' sticky hasRun). */
const RAN_STATES: ReadonlySet<string> = new Set(['running', 'needs_input', 'paused', 'completed'])
const setsStatusLine = (frame: SubagentMessage) => frame.kind === 'steer' || frame.kind === 'respond' || !!frame.text

function factSlots(frame: HistorySpanFrame): FactSlot[] {
  switch (frame.type) {
    case 'subagent_state': return ['state']
    case 'subagent_end': return ['end']
    case 'subagent_message':
      if (frame.kind === 'not_delivered') return ['notDelivered']
      return setsStatusLine(frame) ? ['statusMessage', 'lastMessage'] : ['lastMessage']
    default: return [] // the start lives on its own row
  }
}

function rememberSpanFact(sessionId: string, fact: SpanFact): void {
  const slots = factSlots(fact.frame)
  if (!slots.length) return
  const record = spanRecord(sessionId, fact.frame.span_id)
  for (const slot of slots) {
    const prior = record[slot]
    if (!prior || laterFact(fact, prior) > 0) record[slot] = fact
  }
  if (fact.frame.type === 'subagent_state' && RAN_STATES.has(fact.frame.state)) record.ran = true
}

function spanRecord(sessionId: string, spanId: string): SpanRecord {
  const bySpan = spanFacts.get(sessionId) ?? new Map<string, SpanRecord>()
  spanFacts.set(sessionId, bySpan)
  let record = bySpan.get(spanId)
  if (!record) bySpan.set(spanId, record = { ran: false })
  return record
}

/** The spans a retained row still carries: a saved span source row (start, state, message or end), or a span drawn on a row. */
function retainedSpanIds(rows: Iterable<ChatMessage>): Set<string> {
  const ids = new Set<string>()
  for (const row of rows) {
    if (row.role === 'assistant' && row.historySpanFrame) ids.add(row.historySpanFrame.span_id)
    for (const span of row.spans ?? []) ids.add(span.spanId)
  }
  return ids
}

/**
 * Release every span no retained row carries. A span whose live update beat
 * its start (the bucket's parked pendingSpanUpdatesBySpanId, itself capped at
 * PENDING_SPAN_UPDATE_CAP) keeps its record until that start arrives.
 */
function releaseDetachedSpanFacts(sessionId: string, rows: Iterable<ChatMessage>, parked: SessionChatState['pendingSpanUpdatesBySpanId']): void {
  const bySpan = spanFacts.get(sessionId)
  if (!bySpan) return
  const keep = retainedSpanIds(rows)
  for (const spanId of Object.keys(parked ?? {})) keep.add(spanId)
  for (const spanId of [...bySpan.keys()]) if (!keep.has(spanId)) bySpan.delete(spanId)
}

/**
 * The chronology tick of a read that starts now (architect N2). A read ranks
 * its facts by when it STARTED, so a live frame recorded while its response
 * was pending outranks that older response at commit time.
 */
export function beginHistoryReadTick(): number {
  return ++factTick
}

/**
 * Remember one read's span facts: its entries in page order, then each page
 * context in fetch order, above the entries. `tick` is the read's start tick
 * (beginHistoryReadTick); a read without one ranks as starting now.
 */
export function rememberHistoryReadFacts(sessionId: string, entries: ChatMessage[], contexts: ReadonlyArray<HistoryContext>, tick = ++factTick): void {
  entries.forEach((row, rank) => {
    if (row.role === 'assistant' && row.historySpanFrame) rememberSpanFact(sessionId, { frame: row.historySpanFrame, tick, rank })
  })
  contexts.forEach((context, i) => {
    for (const span of context?.spans ?? []) {
      if (span.has_run === true) spanRecord(sessionId, span.span_id).ran = true
      for (const frame of [span.state, span.message, span.end]) if (frame) rememberSpanFact(sessionId, { frame, tick, rank: entries.length + i })
    }
  })
}

/**
 * A live subagent_* frame is also a recorded span fact (final-gate I1/F2): the
 * live reducer updates the span on its row at once, and recording the same
 * frame here means a later history settlement (an unrelated older page, a
 * refresh) rebuilds that row WITH the live end or state instead of restoring
 * the older cached running state. Opening a new span record first releases the
 * spans no row of `bucket` still carries (I4/F4).
 */
export function rememberLiveSpanFact(sessionId: string, frame: HistorySpanFrame, bucket: SessionChatState | undefined): void {
  if (bucket && !spanFacts.get(sessionId)?.has(frame.span_id)) releaseDetachedSpanFacts(sessionId, Object.values(bucket.messagesById), bucket.pendingSpanUpdatesBySpanId)
  rememberSpanFact(sessionId, { frame, tick: ++factTick, rank: 0 })
}

/** Counts of the span-fact memory for one session; see historySpanFactStats. */
export interface HistorySpanFactStats {
  /** Spans the session's fact memory currently retains. */
  spans: number
  /** Retained facts per span id. */
  factsBySpan: Record<string, number>
  /** Retained facts per span id that carry a free-text body. */
  payloadFactsBySpan: Record<string, number>
}

const recordFacts = (record: SpanRecord) => [...new Set([record.notDelivered, record.statusMessage, record.lastMessage, record.state])].filter((fact): fact is SpanFact => !!fact)

/**
 * Read-only diagnostic of the private span-fact memory, for tests only (#1266
 * I4/F4 bound): COUNTS, never a fact body, never logged, no wire or UI surface.
 * A payload-bearing fact carries a free-text body: a message's `text`, or an
 * end's `final_result` or `message`. An unknown session reports zero spans.
 */
export function historySpanFactStats(sessionId: string): HistorySpanFactStats {
  const stats: HistorySpanFactStats = { spans: 0, factsBySpan: {}, payloadFactsBySpan: {} }
  for (const [spanId, record] of spanFacts.get(sessionId) ?? []) {
    const facts = [...recordFacts(record), ...(record.end ? [record.end] : [])]
    stats.spans += 1
    stats.factsBySpan[spanId] = facts.length
    stats.payloadFactsBySpan[spanId] = facts.filter(fact => carriesPayload(fact.frame)).length
  }
  return stats
}

function carriesPayload(frame: HistorySpanFrame): boolean {
  if (frame.type === 'subagent_message') return frame.text !== undefined
  if (frame.type === 'subagent_end') return frame.final_result !== undefined || frame.message !== undefined
  return false
}

/**
 * Settle persisted span facts onto their start rows (ADR-091 D7/I-4, #1266):
 * the span lives on its start entry's own row, as replay has always placed it.
 * It is rebuilt with the live path's own rules (applySpanFrame): the start,
 * then the latest not-delivered note, messages and state by recorded
 * chronology, then the end, which is authoritative. hasRun stays sticky across
 * every recorded state and the server's has_run proof. Rows carrying only
 * state/message/end stay silent; a span whose start is not on the reader
 * draws nothing until that page loads.
 * A span a live row already holds is left to the live row. Every commit of
 * saved rows runs it, after remembering its read (rememberHistoryReadFacts):
 * page loads and the loaded-window refresh (history.ts::refresh, I3). It then
 * releases the facts of every span neither `rows` nor `retained` (the live
 * tail kept beside the reader) still carries, except spans `parked` before
 * their start (I4/F4, D9).
 */
export function settleHistorySpans(sessionId: string, rows: ChatMessage[], retained: ChatMessage[] = [], parked?: SessionChatState['pendingSpanUpdatesBySpanId']): { rows: ChatMessage[]; index: SessionChatState['spanBySpanId'] } {
  // No recorded facts yet is not "no spans": a saved start alone still draws
  // its helper (architect N1), so settlement never short-circuits here.
  const bySpan = spanFacts.get(sessionId)
  const index: NonNullable<SessionChatState['spanBySpanId']> = {}
  const live = new Set(rows.filter(row => !row.historyPosition).flatMap(row => row.spans?.map(span => span.spanId) ?? []))
  const settled = rows.map(row => {
    const start = row.role === 'assistant' ? row.historySpanFrame : undefined
    if (start?.type !== 'subagent_start' || live.has(start.span_id)) return row
    const record = bySpan?.get(start.span_id)
    const updates = record ? recordFacts(record).sort(laterFact) : []
    let mini: SessionChatState = { ...emptySessionState(), messagesById: { [row.id]: { ...row, spans: undefined } }, messageOrder: [row.id] }
    mini = { ...mini, ...applySpanFrame(mini, start, sessionId) }
    for (const fact of [...updates, ...(record?.end ? [record.end] : [])]) {
      if (!mini.spanBySpanId?.[start.span_id]) break // ended: nothing applies after an end
      mini = { ...mini, ...applySpanFrame(mini, fact.frame, sessionId) }
    }
    const entry = mini.spanBySpanId?.[start.span_id]
    if (entry) index[start.span_id] = entry
    // The span reducers only touch the row's spans; status stays the saved one.
    const result = mini.messagesById[row.id]
    return record?.ran ? { ...result, spans: result.spans?.map(span => span.spanId === start.span_id ? { ...span, hasRun: true } : span) } : result
  })
  releaseDetachedSpanFacts(sessionId, [...settled, ...retained], parked)
  return { rows: settled, index }
}

/**
 * Saved ownership of tool calls wins (UAT RR-V-LIVE-READ). A live tool call
 * carries no message ID, so it attaches to the open text bubble, while saved
 * history may record it on its own tool-only entry. mergeHistoryEntry keeps a
 * finished live call its saved row lacks; when another saved entry owns that
 * call ID, drop it from every other row and point its live owner at the saved
 * row, so the call is drawn exactly once.
 */
export function savedToolCallOwners(entries: readonly ChatMessage[], rows: readonly ChatMessage[]): Map<string, string> {
  const owners = new Map<string, string>()
  entries.forEach((entry, i) => { for (const call of entry.tool_calls ?? []) owners.set(call.id, rows[i].id) })
  return owners
}

export function applySavedToolOwnership(rows: ChatMessage[], owners: ReadonlyMap<string, string>): ChatMessage[] {
  if (!owners.size) return rows
  return rows.map((row): ChatMessage => {
    if (row.role !== 'assistant' || !row.tool_calls) return row
    const calls = row.tool_calls.filter(call => (owners.get(call.id) ?? row.id) === row.id)
    return calls.length !== row.tool_calls.length ? { ...row, tool_calls: calls } : row
  })
}

export function savedToolOwnerPatch(ownerMap: Record<string, string> | undefined, owners: ReadonlyMap<string, string>): Record<string, string> | undefined {
  if (!ownerMap || !owners.size) return ownerMap
  return Object.fromEntries(Object.entries(ownerMap).map(([call, owner]) => [call, owners.get(call) ?? owner]))
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

export function applyHistoryPageToBucket(bucket: SessionChatState, page: SessionHistoryPage, action: HistoryActionKind, baseline: Record<string, ChatMessage> = bucket.messagesById, readTick?: number): Partial<SessionChatState> {
  const history = bucket.history
  const old = currentHistoryEntries(bucket)
  const pagePositions = page.entries.map(entry => entry.historyPosition)
  const existingPositions = new Set(history.readerPositions)
  const oldOverlap = history.readerPositions.filter(position => pagePositions.includes(position))
  const newOverlap = pagePositions.filter(position => existingPositions.has(position))
  if (oldOverlap.join() !== newOverlap.join()) throw new HistoryPageProtocolError('Contradictory history overlap.')
  if ((action === 'older' || action === 'newer') && page.entries.length && page.entries.every(entry => existingPositions.has(entry.historyPosition))) throw new HistoryPageProtocolError('History boundary did not advance.')
  // A saved source whose id a displayed reply already coalesced (the token
  // resolver's mergedReplayIds) merges onto that display row: it keeps its
  // identity and live text and takes the saved position, never a second reply
  // beside it (D4/D7, r21). Only when it is the ONE saved source that display
  // row represents (final-gate F1): a row the page also selects by its own id,
  // a row two page sources would claim, or a row already holding another saved
  // position stays itself, and each distinct saved entry keeps its own row, so
  // no saved segment collapses into another under one display identity.
  const candidates = page.entries.map(entry => bucket.messagesById[entry.id] ? undefined : coalescedOwner(bucket, entry))
  const claims = new Map<string, number>()
  page.entries.forEach((entry, i) => {
    const claimed = candidates[i]?.id ?? (bucket.messagesById[entry.id] ? entry.id : undefined)
    if (claimed) claims.set(claimed, (claims.get(claimed) ?? 0) + 1)
  })
  const incoming = page.entries.map((entry, i) => {
    const candidate = candidates[i]
    const owner = candidate && claims.get(candidate.id) === 1 && (!candidate.historyPosition || candidate.historyPosition === entry.historyPosition) ? candidate : undefined
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
  const toolOwners = savedToolCallOwners(page.entries, incoming)
  const hidden = applySavedToolOwnership(hiddenIds.flatMap(id => bucket.messagesById[id] ? [bucket.messagesById[id]] : []), toolOwners)
  rememberHistoryReadFacts(page.session_id, incoming, [page.page_info.context], readTick)
  const spans = settleHistorySpans(page.session_id, applySavedToolOwnership(finalRows, toolOwners), hidden, bucket.pendingSpanUpdatesBySpanId)
  const patch = { ...historyRowsPatch(bucket, [...spans.rows, ...hidden], positions), messageOrder: [...new Set(spans.rows.map(row => row.id))], historyLiveOrder: hiddenIds }
  patch.toolCallOwnerMessageId = savedToolOwnerPatch(patch.toolCallOwnerMessageId, toolOwners)
  patch.spanBySpanId = { ...patch.spanBySpanId, ...spans.index }
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
