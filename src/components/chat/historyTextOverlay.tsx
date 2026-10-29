// #1452 D2 A2.4 "Shortened text" with D1 S9/S10: a saved chat message whose
// text the history page shortened shows its preview followed by "Show the
// rest (size)". Selecting it reads that occurrence once and shows the full
// text in place. In a combined reply this is per part: each shortened part
// has its own control and reads only its own position.
//
// Expansion is a render-time overlay. The stored row keeps the preview; the
// full text lives only in the query cache (src/lib/historyEntryReads.ts) and
// replaces just that part's span while rendering. Tool anchors are derived
// from the part-relative anchors recorded on the row (S10), never shifted, so
// expanding parts in either order gives the same placement.
import { useMemo } from 'react'
import { Button } from '@/components/ui/button'
import type { MessagePart, MessageTextPart, MessageToolPart } from '@/lib/messageParts'
import { humanByteSize } from '@/lib/historyPreview'
import { useHistoryEntryRead, useHistoryEntryReads } from '@/lib/historyEntryReads'
import { useChatStore, type ChatMessage, type PositionedToolCall } from '@/store/chat'
import { deriveAnchor, joinParts, locateAnchor, partAnchor, partTexts } from '@/store/chat/history-parts'
import { HistoryEntryReadOutcome } from './historyEntryOutcome'

export interface MessageMorePart { // not-wire-format: render-layer marker for a shortened text span; never serialized
  type: 'more'
  position: string
  originalSizeBytes: number
}
export type DisplayPart = MessagePart | MessageMorePart

interface ShortenedSpan { index: number; position: string; size: number } // not-wire-format: render-layer bookkeeping

/** Which spans of the row's stored text are saved previews that can load their full text. */
function shortenedSpans(message: ChatMessage, stored: readonly string[] | undefined): ShortenedSpan[] {
  if (message.historyParts) {
    if (!stored) return []
    return message.historyParts.flatMap((part, index) =>
      part.text === 'saved' && part.position && part.originalSizeBytes !== undefined ? [{ index, position: part.position, size: part.originalSizeBytes }] : [])
  }
  return message.historyPosition && message.contentOriginalSizeBytes !== undefined && !message.isStreaming
    ? [{ index: 0, position: message.historyPosition, size: message.contentOriginalSizeBytes }] : []
}

/** Text, tool and control parts in order. Equal offsets put a control before a tool. */
function splitDisplayParts(content: string, calls: readonly PositionedToolCall[], marks: readonly { offset: number; part: MessageMorePart }[]): DisplayPart[] {
  const clamp = (offset: number) => Math.min(Math.max(offset, 0), content.length)
  const items: { offset: number; order: number; part: MessageToolPart | MessageMorePart }[] = [
    ...marks.map(mark => ({ offset: clamp(mark.offset), order: 0, part: mark.part })),
    ...calls.flatMap(call => call.textOffset === undefined ? [] : [{ offset: clamp(call.textOffset), order: 1, part: { type: 'tool' as const, call } }]),
  ].sort((a, b) => a.offset - b.offset || a.order - b.order)
  const parts: DisplayPart[] = []
  let cursor = 0
  for (const item of items) {
    if (item.offset > cursor) {
      parts.push({ type: 'text', text: content.slice(cursor, item.offset) } satisfies MessageTextPart)
      cursor = item.offset
    }
    parts.push(item.part)
  }
  if (cursor < content.length) parts.push({ type: 'text', text: content.slice(cursor) })
  for (const call of calls) if (call.textOffset === undefined) parts.push({ type: 'tool', call })
  return parts
}

function buildDisplayParts(message: ChatMessage, calls: readonly PositionedToolCall[], stored: readonly string[] | undefined, spans: readonly ShortenedSpan[], full: readonly (string | undefined)[]): DisplayPart[] {
  const more = (span: ShortenedSpan): MessageMorePart => ({ type: 'more', position: span.position, originalSizeBytes: span.size })
  const parts = message.historyParts
  if (parts && stored) {
    const shown = stored.map((text, index) => full[spans.findIndex(span => span.index === index)] ?? text)
    const expanded = full.some(text => text !== undefined)
    const content = expanded ? joinParts(parts, shown).content : message.content ?? ''
    const derived = expanded ? calls.map(call => {
      const anchored = partAnchor(call.id, parts, shown)
      if (anchored !== undefined) return { ...call, textOffset: anchored }
      if (call.textOffset === undefined) return call
      const { index, relative } = locateAnchor(call.textOffset, parts, stored)
      return { ...call, textOffset: deriveAnchor(index, relative, parts, shown) }
    }) : calls
    const marks = spans.flatMap((span, i) => full[i] !== undefined ? [] : [{ offset: deriveAnchor(span.index, Infinity, parts, shown), part: more(span) }])
    return splitDisplayParts(content, derived, marks)
  }
  const content = full[0] ?? message.content ?? ''
  return splitDisplayParts(content, calls, spans.length && full[0] === undefined ? [{ offset: content.length, part: more(spans[0]) }] : [])
}

/** The parts a saved or live message renders: its text slices, tool calls and "Show the rest" controls. */
export function useHistoryDisplayParts(message: ChatMessage, calls: readonly PositionedToolCall[], sessionId: string): DisplayPart[] {
  const stored = useMemo(() => message.historyParts ? partTexts(message) : undefined, [message])
  const spans = useMemo(() => shortenedSpans(message, stored), [message, stored])
  const expandedKey = useChatStore(state => spans.map(span => state.sessionsById[sessionId]?.historyExpandedText?.[span.position] ? '1' : '0').join(''))
  const reads = useHistoryEntryReads(sessionId, spans.map((span, i) => ({ position: span.position, enabled: expandedKey[i] === '1' })))
  const full = spans.map((_, i) => {
    const content = expandedKey[i] === '1' ? reads[i]?.data?.content : undefined
    return typeof content === 'string' ? content : undefined
  })
  // Value key over the shown full texts: an equal-length corrected suffix is a different key.
  const fullKey = full.map(text => text === undefined ? '\u0002' : text).join('\u0001')
  // `full` is captured by its value key (fullKey) rather than by array identity.
  return useMemo(() => buildDisplayParts(message, calls, stored, spans, full), [message, calls, stored, spans, fullKey])
}

const NO_CALLS: readonly PositionedToolCall[] = []

/** A user bubble's shown text (its full text once loaded) and its "Show the rest" control, if shortened. */
export function useHistoryUserText(message: ChatMessage, sessionId: string): { content: string; more: MessageMorePart | undefined } {
  const parts = useHistoryDisplayParts(message, NO_CALLS, sessionId)
  return useMemo(() => ({
    content: parts.flatMap(part => part.type === 'text' ? [part.text] : []).join(''),
    more: parts.find((part): part is MessageMorePart => part.type === 'more'),
  }), [parts])
}

/** The saved occurrence whose full result a tool call's row reads: its own part's position, else the row's. */
export function historyCallPosition(message: ChatMessage, callId: string): string | undefined {
  return message.historyParts?.find(part => part.callIds?.includes(callId))?.position ?? message.historyPosition
}

/** The control after a shortened text span; while its read runs or fails, the preview stays above it. */
export function HistoryShowRest({ sessionId, position, originalSizeBytes }: { sessionId: string; position: string; originalSizeBytes: number }) {
  const expanded = useChatStore(state => state.sessionsById[sessionId]?.historyExpandedText?.[position] === true)
  const read = useHistoryEntryRead(sessionId, position, expanded)
  if (!expanded) {
    return (
      <Button type="button" variant="link" onClick={() => useChatStore.getState().expandHistoryText(sessionId, position)}
        className="h-auto rounded-none px-[var(--space-0)] text-[length:var(--type-caption-size)] font-body">
        Show the rest ({humanByteSize(originalSizeBytes)})
      </Button>
    )
  }
  return <HistoryEntryReadOutcome read={read} incomplete={read.isSuccess && typeof read.data?.content !== 'string'} />
}
