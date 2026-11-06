// #1452 S11 (chat-history-paging-spec, "S11 — one row per turn segment"):
// every saved source of one turn segment binds into ONE display row, exactly
// as live draws one bubble per segment. This module only changes how already
// bound reader rows map onto display rows; D4's per-record source groups and
// positions are untouched. Client-local bookkeeping, never persisted or sent.
import type { ChatMessage, HistoryDisplayPart, PositionedToolCall, SessionChatState } from './types'
import { isClearContextMarker } from '@/lib/clearMarker'
import { deriveAnchor, locateAnchor, partTexts } from './history-parts'

/** The result of folding one ordered reader interval. */
export interface SegmentFold { // not-wire-format: in-memory fold result
  /** One row per input position, in input order; folded rows repeat at each bound position. */
  rows: ChatMessage[]
  /** Display ids that moved to a new key in this fold (S11b re-key). */
  rekeys: ReadonlyMap<string, string>
  /** Tool calls whose only owning part left the reader (S11c release). */
  releasedCalls: ReadonlySet<string>
}

type SegmentKind = 'candidate' | 'transparent' | 'boundary' | 'separate'

/**
 * S11a: a row's role in segment building. Boundaries are what live ends the
 * open bubble at (a user row, a clear marker, an error, interrupted,
 * cancelled or terminal-outcome row). Silent accounting rows, persisted span
 * facts and information notices are transparent. An assistant source of an
 * unknown turn never joins (S11d) and stays its own row.
 */
function segmentKind(row: ChatMessage): SegmentKind {
  if (row.historyBoundary || row.role === 'user' || isClearContextMarker(row)) return 'boundary'
  if (row.role !== 'assistant' || row.type || row.historySpanFrame) return 'transparent'
  if (row.status === 'error' || row.status === 'interrupted') return 'boundary'
  if (!row.content && !row.tool_calls?.length) return 'transparent'
  return row.turnId ? 'candidate' : 'separate'
}

/** The distinct rows of each turn segment, in reader order. */
function turnSegments(rows: readonly ChatMessage[]): ChatMessage[][] {
  const segments: ChatMessage[][] = []
  const assigned = new Set<string>()
  let open: { rows: ChatMessage[]; turn: string; agent?: string } | undefined
  for (const row of rows) {
    if (assigned.has(row.id)) continue
    const kind = segmentKind(row)
    if (kind === 'transparent') continue
    if (kind === 'candidate' && open && open.turn === row.turnId && (!open.agent || !row.agentId || open.agent === row.agentId)) {
      open.rows.push(row)
      open.agent ??= row.agentId
      assigned.add(row.id)
      continue
    }
    open = kind === 'candidate' ? { rows: [row], turn: row.turnId!, agent: row.agentId } : undefined
    if (open) { segments.push(open.rows); assigned.add(row.id) }
  }
  return segments
}

type CallClaim = { call: PositionedToolCall; relative?: number; saved: boolean } // not-wire-format
type Unit = { part: HistoryDisplayPart; text: string; claims: CallClaim[]; row: ChatMessage; recorded: boolean; sortKey: number; seq: number } // not-wire-format

/** The source units of one row: its recorded parts, or the whole row as one source. */
function rowUnits(row: ChatMessage, rank: (position: string | undefined) => number | undefined, seq: { next: number }): Unit[] {
  const texts = row.historyParts ? partTexts(row) : undefined
  const parts: HistoryDisplayPart[] = row.historyParts && texts ? row.historyParts : [{
    sourceId: row.id, start: 0, joiner: '', text: row.historyPosition ? 'saved' : 'live',
    ...(row.historyPosition ? { position: row.historyPosition, callIds: (row.tool_calls ?? []).map(call => call.id) } : {}),
    ...(row.contentOriginalSizeBytes !== undefined ? { originalSizeBytes: row.contentOriginalSizeBytes } : {}),
  }]
  const shown = texts ?? [row.content ?? '']
  const units: Unit[] = parts.map((part, index) => ({ part, text: shown[index], claims: [], row,
    recorded: !!row.historyParts && !!texts && index > 0 && !part.joinerOpen, sortKey: Infinity, seq: seq.next++ }))
  for (const call of (row.tool_calls ?? []) as PositionedToolCall[]) {
    let index = parts.findIndex(part => part.callIds?.includes(call.id))
    const saved = index !== -1
    if (index === -1) index = parts.findIndex(part => part.anchors?.[call.id] !== undefined)
    let relative = index === -1 ? undefined : parts[index].anchors?.[call.id]
    if (index === -1 && call.textOffset !== undefined) ({ index, relative } = locateAnchor(call.textOffset, parts, shown))
    if (index === -1) index = parts.length - 1
    if (units[index].text === '') relative = 0
    // A saved call without an offset follows its own source's text.
    else if (relative === undefined && units[index].part.position) relative = units[index].text.length
    units[index].claims.push({ call, relative, saved })
  }
  // Source order: a bound part at its reader position; an unbound (live) part
  // right after the bound part before it in the same row, else before the next.
  const ranks = units.map(unit => rank(unit.part.position))
  units.forEach((unit, i) => {
    if (ranks[i] !== undefined) { unit.sortKey = ranks[i]!; return }
    const before = ranks.slice(0, i).reverse().find(value => value !== undefined)
    const after = ranks.slice(i + 1).find(value => value !== undefined)
    unit.sortKey = before !== undefined ? before + 0.5 : after !== undefined ? after - 0.5 : Infinity
  })
  return units
}

/**
 * S11b "Text and joiners": a recorded joiner is immutable; an unrecorded seam
 * gets "\n\n" after a tool-only part when text precedes it, "" between two
 * adjacent text parts and "" for the first text. Tool-only parts add no text.
 */
function seamJoiners(units: readonly Unit[]): HistoryDisplayPart[] {
  let textBefore = false
  let toolSince = false
  return units.map(unit => {
    const { joinerOpen: _open, ...part } = unit.part
    void _open
    if (unit.text === '') {
      if (unit.claims.length) toolSince = true
      return { ...part, joiner: '', joinerOpen: true as const }
    }
    const joiner = !textBefore ? '' : unit.recorded ? unit.part.joiner : toolSince ? '\n\n' : ''
    const open = !textBefore
    textBefore = true
    toolSince = false
    return open ? { ...part, joiner, joinerOpen: true as const } : { ...part, joiner }
  })
}

/** One call per id: the saved source that records it wins over a live anchor. */
function claimedCalls(units: readonly Unit[]): Map<string, number> {
  const owner = new Map<string, number>()
  units.forEach((unit, index) => {
    for (const claim of unit.claims) {
      const prior = owner.get(claim.call.id)
      if (prior === undefined || (claim.saved && !units[prior].claims.find(other => other.call.id === claim.call.id)!.saved)) owner.set(claim.call.id, index)
    }
  })
  return owner
}

interface FoldContext { // not-wire-format: inputs one segment fold reads
  bucket: SessionChatState
  retained: ReadonlySet<string>
  rank: (position: string | undefined) => number | undefined
  others: ReadonlySet<string>
  released: Set<string>
}

/** S11b: the display id of a folded segment, by the one precedence. */
function segmentKey(units: readonly Unit[], segment: readonly ChatMessage[], others: ReadonlySet<string>): { key: string; unit: Unit } {
  const text = units.find(unit => unit.text !== '')
  const unit = text ?? units[0]
  const key = unit.part.sourceId
  // S1a's occupied-key exception: an incompatible row elsewhere owns the id,
  // so the constituent keeps its client-local key.
  return { key: others.has(key) && !segment.some(row => row.id === key) ? unit.row.id : key, unit }
}

/** The stable render identity of the drawn row a folded row grows from. */
function renderKey(segment: readonly ChatMessage[], key: string, ctx: FoldContext): string | undefined {
  const drawn = segment.filter(row => ctx.bucket.messagesById[row.id])
  const keeper = drawn.find(row => row.id === key) ?? drawn[0]
  if (!keeper) return undefined
  const prior = ctx.bucket.messagesById[keeper.id]
  return keeper.historyRenderKey ?? prior.historyRenderKey ?? prior.historyPosition
}

function foldSegment(segment: readonly ChatMessage[], ctx: FoldContext): ChatMessage | undefined {
  const seq = { next: 0 }
  const all = segment.flatMap(row => rowUnits(row, ctx.rank, seq))
  const units = all.filter(unit => !unit.part.position || ctx.retained.has(unit.part.position))
    .sort((a, b) => a.sortKey - b.sortKey || a.seq - b.seq)
  const owners = claimedCalls(units)
  for (const unit of all) for (const claim of unit.claims) if (!owners.has(claim.call.id)) ctx.released.add(claim.call.id)
  if (!units.length) return undefined
  const parts = seamJoiners(units)
  const texts = units.map(unit => unit.text)
  let content = ''
  const historyParts = parts.map((part, index) => {
    if (index > 0) content += part.joiner
    const start = content.length
    content += texts[index]
    const anchors: Record<string, number> = { ...part.anchors }
    for (const claim of units[index].claims) if (owners.get(claim.call.id) === index && claim.relative !== undefined) anchors[claim.call.id] = claim.relative
    return { ...part, start, ...(Object.keys(anchors).length ? { anchors } : {}) }
  })
  const calls = units.flatMap((unit, index) => unit.claims.filter(claim => owners.get(claim.call.id) === index).map(claim => {
    const { textOffset: _offset, ...call } = claim.call
    void _offset
    return claim.relative === undefined ? call : { ...call, textOffset: deriveAnchor(index, claim.relative, historyParts, texts) }
  }))
  const { key, unit } = segmentKey(units, segment, ctx.others)
  const base = segment.find(row => row.id === key) ?? unit.row
  const aliases = [...new Set(units.filter(item => item.text !== '' && item.part.sourceId !== key && !ctx.others.has(item.part.sourceId)).map(item => item.part.sourceId))]
  const spans = [...new Map(segment.flatMap(row => row.spans ?? []).map(span => [span.spanId, span])).values()]
  const stable = renderKey(segment, key, ctx)
  // A segment holds assistant rows only (segmentKind), so the base is one.
  const folded = {
    ...base, id: key, content, historyParts, tool_calls: calls, mergedReplayIds: aliases.length ? aliases : undefined,
    contentOriginalSizeBytes: undefined, spans: spans.length ? spans : undefined,
    agentId: base.agentId ?? segment.find(row => row.agentId)?.agentId,
    // S11b: a row still streaming stays a live, streaming row.
    ...(segment.some(row => row.isStreaming) ? { isStreaming: true, status: 'streaming' as const } : {}),
  } as ChatMessage
  if (stable) folded.historyRenderKey = stable
  else delete folded.historyRenderKey
  return folded
}

/**
 * #1452 S11/S11b/S11c: fold the turn segments of one ordered reader interval
 * (one row per retained position, a bound row repeating at each of its
 * positions). Every segment of several rows, or one that lost a bound
 * position (the 500-group window, expiry, a tail-cache restore), is rebuilt
 * from its source parts: text and joiners per S11b, tool calls in source
 * order with part-relative anchors, the S11b display key, and released parts
 * dropped with their calls. Other rows are returned unchanged.
 */
export function foldTurnSegments(rows: readonly ChatMessage[], bucket: SessionChatState, extraIds: Iterable<string> = []): SegmentFold {
  const order = new Map<string, number>()
  rows.forEach(row => { if (row.historyPosition && !order.has(row.historyPosition)) order.set(row.historyPosition, order.size) })
  const rekeys = new Map<string, string>()
  const released = new Set<string>()
  const replaced = new Map<string, ChatMessage | null>()
  const segments = turnSegments(rows)
  const members = new Set(segments.flat().map(row => row.id))
  const others = new Set([...Object.keys(bucket.messagesById), ...rows.map(row => row.id), ...extraIds].filter(id => !members.has(id)))
  const ctx: FoldContext = { bucket, retained: new Set(order.keys()), rank: position => position === undefined ? undefined : order.get(position), others, released }
  for (const segment of segments) {
    const releases = segment.some(row => row.historyParts?.some(part => part.position && !order.has(part.position)))
    if (segment.length < 2 && !releases) continue
    const folded = foldSegment(segment, ctx)
    for (const row of segment) {
      replaced.set(row.id, folded ?? null)
      if (folded && row.id !== folded.id) rekeys.set(row.id, folded.id)
    }
  }
  if (!replaced.size) return { rows: [...rows], rekeys, releasedCalls: released }
  for (const id of released) if ([...replaced.values()].some(row => row?.tool_calls?.some(call => call.id === id))) released.delete(id)
  return {
    rows: rows.flatMap(row => {
      if (!replaced.has(row.id)) return [row]
      const folded = replaced.get(row.id)
      return folded ? [{ ...folded, historyPosition: row.historyPosition }] : []
    }),
    rekeys, releasedCalls: released,
  }
}

/**
 * S8 (changed by S11): a row with a part list takes its display slot at its
 * FIRST bound position, where live mints the bubble; a row without parts keeps
 * the canonical duplicate-id last-occurrence rule.
 */
export function boundDisplaySlots(rows: readonly ChatMessage[]): ChatMessage[] {
  const first = new Map<string, number>()
  rows.forEach((row, index) => { if (!first.has(row.id)) first.set(row.id, index) })
  return rows.filter((row, index) => row.historyParts ? first.get(row.id) === index : !rows.slice(index + 1).some(later => later.id === row.id))
}

/**
 * S11b re-key contract, bucket half: tool owners (live and saved), span
 * owners and the hidden live order follow a moved display id in the same
 * store update; calls whose owning part was released lose their owner.
 */
export function rekeyBucketIndexes(bucket: SessionChatState, fold: SegmentFold, rows: readonly ChatMessage[]): SessionChatState {
  if (!fold.rekeys.size && !fold.releasedCalls.size) return bucket
  const move = (id: string) => fold.rekeys.get(id) ?? id
  const byId = new Map(rows.map(row => [row.id, row]))
  return {
    ...bucket,
    toolCallOwnerMessageId: Object.fromEntries(Object.entries(bucket.toolCallOwnerMessageId ?? {}).flatMap(([call, owner]) => fold.releasedCalls.has(call) ? [] : [[call, move(owner)]])),
    spanBySpanId: Object.fromEntries(Object.entries(bucket.spanBySpanId ?? {}).map(([span, owner]) => {
      const messageId = move(owner.messageId)
      const index = messageId === owner.messageId ? -1 : byId.get(messageId)?.spans?.findIndex(item => item.spanId === span) ?? -1
      return [span, index === -1 ? { ...owner, messageId } : { messageId, spanIdx: index }]
    })),
    historyLiveOrder: bucket.historyLiveOrder ? [...new Set(bucket.historyLiveOrder.map(move))] : bucket.historyLiveOrder,
  }
}

/** Saved tool owners (call id -> row id) after a fold moved their rows. */
export function rekeyToolOwners(owners: ReadonlyMap<string, string>, fold: SegmentFold): Map<string, string> {
  return new Map([...owners].flatMap(([call, owner]) => fold.releasedCalls.has(call) ? [] : [[call, fold.rekeys.get(owner) ?? owner] as [string, string]]))
}
