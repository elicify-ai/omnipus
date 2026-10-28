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
    row.historyParts = [{ sourceId: row.id, start: 0, joiner: '', text: row.historyPosition ? 'saved' : 'live', ...(row.historyPosition ? { position: row.historyPosition } : {}) }]
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
 * S10: the effective anchor of an offset recorded against `oldTexts` once
 * the parts show `newTexts`. An anchor at a part's end, or inside the joiner
 * after it, belongs to that part's end, never to the next part; it is
 * derived as start(part) + min(relative offset, length(part)).
 */
function remapAnchor(offset: number, parts: readonly HistoryDisplayPart[], oldTexts: readonly string[], newTexts: readonly string[]): number {
  let oldStart = 0
  let newStart = 0
  for (let i = 0; i < parts.length; i++) {
    if (i > 0) { oldStart += parts[i].joiner.length; newStart += parts[i].joiner.length }
    const oldEnd = oldStart + oldTexts[i].length
    const nextStart = i + 1 < parts.length ? oldEnd + parts[i + 1].joiner.length : Infinity
    if (offset <= oldEnd || offset < nextStart || i === parts.length - 1) return newStart + Math.min(Math.max(0, offset - oldStart), newTexts[i].length)
    oldStart = oldEnd
    newStart += newTexts[i].length
  }
  return offset
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

/** The row with its live tool anchors re-derived after its part texts changed (S10). */
export function remapToolAnchors(row: ChatMessage, parts: readonly HistoryDisplayPart[], oldTexts: readonly string[], newTexts: readonly string[]): ChatMessage {
  if (row.role !== 'assistant' || !row.tool_calls?.length || oldTexts.every((text, i) => text.length === newTexts[i].length)) return row
  return { ...row, tool_calls: row.tool_calls.map(call => {
    const offset = (call as PositionedToolCall).textOffset
    return offset === undefined ? call : { ...call, textOffset: remapAnchor(offset, parts, oldTexts, newTexts) }
  }) }
}

/** Remap a live (unbaked) call's start snapshot into the row's new text (S10). */
export function remapStartSnapshot(snapshot: string, content: string, parts: readonly HistoryDisplayPart[], oldTexts: readonly string[], newTexts: readonly string[]): string {
  return content.slice(0, remapAnchor(snapshot.length, parts, oldTexts, newTexts))
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
