// #1452 D1 (chat-history-paging-spec, "Amendment 2026-10-27 (founder, #1452
// D1)", rules S1-S10): a display row that combines several saved sources
// keeps a body-free ordered part list, so a later saved record binds to its
// own part instead of to the whole row. Client-local bookkeeping only: never
// persisted, never sent.
import type { ChatMessage, HistoryDisplayPart, PositionedToolCall } from './types'
import type { SessionHistoryEntry } from '@/lib/api/history-types'
import { logDiagnostic } from '@/lib/telemetry'

/** The source ids a row displays: its recorded parts, otherwise its own id and coalesced aliases. */
export function rowSourceIds(row: ChatMessage): string[] {
  return row.historyParts ? row.historyParts.map(part => part.sourceId) : [row.id, ...(row.mergedReplayIds ?? [])]
}

/**
 * S7/S2a: record the part a combine site is about to append, with the joiner
 * it actually inserts. Call BEFORE the content append. A row without a part
 * list first records its existing text as part 0 (its own id).
 */
export function recordCombinedPart(row: ChatMessage, sourceId: string, joiner: string): void {
  if (!row.historyParts) {
    row.historyParts = [{ sourceId: row.id, start: 0, joiner: '', text: row.historyPosition ? 'saved' : 'live',
      ...(row.historyPosition ? { position: row.historyPosition } : {}),
      // S9: a saved preview's size moves from the row onto its own part.
      ...(row.contentOriginalSizeBytes !== undefined ? { originalSizeBytes: row.contentOriginalSizeBytes } : {}) }]
    row.contentOriginalSizeBytes = undefined
  }
  row.historyParts.push({ sourceId, start: (row.content ?? '').length + joiner.length, joiner, text: 'live' })
}

/** The part list a freshly minted row for one source starts with. */
export function singlePart(sourceId: string, position?: string): HistoryDisplayPart[] {
  return [{ sourceId, start: 0, joiner: '', text: position ? 'saved' : 'live', ...(position ? { position } : {}) }]
}

/**
 * Each part's currently shown text, or undefined when the stored offsets no
 * longer describe the row's text (a catch-up `replace` token rewrote it). The
 * inconsistency is reported content-free; the caller then merges the whole
 * row exactly as before part tracking existed.
 */
export function partTexts(row: ChatMessage): string[] | undefined {
  const parts = row.historyParts
  if (!parts?.length) return undefined
  const content = row.content ?? ''
  const texts: string[] = []
  for (let i = 0; i < parts.length; i++) {
    const next = parts[i + 1]
    const end = next ? next.start - next.joiner.length : content.length
    const valid = (i > 0 || parts[0].start === 0) && parts[i].start <= end && end <= content.length &&
      (!next || content.slice(end, next.start) === next.joiner)
    if (!valid) {
      logDiagnostic('chatHistoryPartsInconsistent', { parts: parts.length })
      return undefined
    }
    texts.push(content.slice(parts[i].start, end))
  }
  return texts
}

/**
 * S10 "Record part-relative": the part an offset in the row's text belongs
 * to, and the offset within that part. An anchor at a part's end, or inside
 * the joiner after it, belongs to that part's end, never to the next part.
 */
export function locateAnchor(offset: number, parts: readonly HistoryDisplayPart[], texts: readonly string[]): { index: number; relative: number } {
  let start = 0
  for (let i = 0; i < parts.length; i++) {
    if (i > 0) start += parts[i].joiner.length
    const end = start + texts[i].length
    const next = i + 1 < parts.length ? end + parts[i + 1].joiner.length : Infinity
    if (offset <= end || offset < next || i === parts.length - 1) return { index: i, relative: offset <= end ? Math.max(0, offset - start) : texts[i].length }
    start = end
  }
  return { index: 0, relative: 0 }
}

/** S10 "Derive, never shift": start(part) + min(relative offset, length(part)) for the texts shown now. */
export function deriveAnchor(index: number, relative: number, parts: readonly HistoryDisplayPart[], texts: readonly string[]): number {
  let start = 0
  for (let i = 0; i < index; i++) start += texts[i].length + parts[i + 1].joiner.length
  return start + Math.min(relative, texts[index].length)
}

/** The row's text and part offsets for `texts`, joined with each part's recorded joiner (S2/S2a). */
export function joinParts(parts: readonly HistoryDisplayPart[], texts: readonly string[]): { content: string; historyParts: HistoryDisplayPart[] } {
  let content = ''
  const historyParts = parts.map((part, i) => {
    if (i > 0) content += part.joiner
    const start = content.length
    content += texts[i]
    return { ...part, start }
  })
  return { content, historyParts }
}

/**
 * S10: record, body-free, the part-relative anchor of every tool call that
 * has an offset in the current texts and no recorded anchor yet. `offsets`
 * are in the coordinates of `texts` (the row's stored text before a change).
 */
export function recordPartAnchors(parts: readonly HistoryDisplayPart[], texts: readonly string[], offsets: ReadonlyMap<string, number>): HistoryDisplayPart[] {
  const recorded = new Set(parts.flatMap(part => Object.keys(part.anchors ?? {})))
  const next = parts.map(part => ({ ...part, ...(part.anchors ? { anchors: { ...part.anchors } } : {}) }))
  for (const [callId, offset] of offsets) {
    if (recorded.has(callId)) continue
    const { index, relative } = locateAnchor(offset, parts, texts)
    next[index].anchors = { ...next[index].anchors, [callId]: relative }
  }
  return next
}

/** The derived anchor of a call from its recorded part-relative anchor, for the texts shown. */
export function partAnchor(callId: string, parts: readonly HistoryDisplayPart[], texts: readonly string[]): number | undefined {
  const index = parts.findIndex(part => part.anchors?.[callId] !== undefined)
  return index === -1 ? undefined : deriveAnchor(index, parts[index].anchors![callId], parts, texts)
}

/** The row with every anchored tool call's stored offset derived for its stored (collapsed) text. */
export function stampPartAnchors(row: ChatMessage, parts: readonly HistoryDisplayPart[], texts: readonly string[]): ChatMessage {
  if (row.role !== 'assistant' || !row.tool_calls?.length) return row
  return { ...row, tool_calls: row.tool_calls.map(call => {
    const offset = partAnchor(call.id, parts, texts)
    return offset === undefined ? call : { ...call, textOffset: offset } as PositionedToolCall
  }) }
}

/** The live offsets a combined row's calls are anchored at now: stored offsets, else live start snapshots. */
export function currentCallOffsets(row: ChatMessage, owned: readonly string[], textAtToolCallStart: Readonly<Record<string, string>>): Map<string, number> {
  const offsets = new Map<string, number>()
  for (const call of (row.tool_calls ?? []) as PositionedToolCall[]) if (call.textOffset !== undefined) offsets.set(call.id, call.textOffset)
  for (const callId of owned) if (!offsets.has(callId) && textAtToolCallStart[callId] !== undefined) offsets.set(callId, textAtToolCallStart[callId].length)
  return offsets
}

/**
 * S1a: a saved record may bind to a row only with the same role, and the
 * same turn and producer whenever both sides know them.
 */
export function compatibleSource(row: ChatMessage, entry: SessionHistoryEntry): boolean {
  return row.role === entry.role && (!entry.turnId || !row.turnId || entry.turnId === row.turnId) &&
    (!entry.agentId || !row.agentId || entry.agentId === row.agentId)
}

/** S1a: the client-local display key of a record whose own id is occupied by an incompatible row. */
export function localHistoryKey(position: string): string {
  return `history-local-${position}`
}
