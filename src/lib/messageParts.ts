// Pure helper for interleaving a finalized/replayed assistant message's text
// with its tool calls, in the order they actually happened.
//
// Bug this fixes (root-caused in the store half, commit 5d6c1e0e): once a
// stream finishes (or a session replays), the finished bubble used to render
// ALL of `message.content` first, then ALL of `message.tool_calls` — the
// live interleaving the user watched while streaming collapsed into two flat
// groups. The store now stamps every baked tool call with `textOffset`
// (chars into the FINAL `message.content` where the call started — see
// `PositionedToolCall` / `stampToolCallOffset` in src/store/chat.ts for the
// exact contract). This module turns `(content, toolCalls)` into an ordered
// list of text/tool parts a renderer can map over directly.
//
// Home: co-located in src/lib alongside the other pure chat-formatting
// helpers (formatDuration.ts, humanizeToolName.ts, toolVisibility.ts) rather
// than inside a component file — it's consumed by two independent renderers
// (ChatScreen.tsx's VirtualAssistantMessageRow and MessageItem.tsx) and has
// no JSX or store dependency of its own, so a plain lib module is the
// better fit than co-locating with either call site.

import type { PositionedToolCall } from '@/store/chat'

export interface MessageTextPart {
  type: 'text'
  text: string
}

export interface MessageToolPart {
  type: 'tool'
  call: PositionedToolCall
}

export type MessagePart = MessageTextPart | MessageToolPart

/**
 * Split `content` and interleave `toolCalls` into an ordered array of text
 * and tool parts, matching the order the assistant actually streamed them.
 *
 * Semantics (pinned by messageParts.test.ts):
 *  - Calls are sorted by `(textOffset ?? Infinity)` ascending; ties (e.g.
 *    two calls that started back-to-back with no text between them) keep
 *    their relative order from the input array — `Array.prototype.sort` is
 *    stable per spec (ES2019+), so a plain comparator sort is sufficient.
 *  - Each defined offset is clamped to `[0, content.length]` before use —
 *    defensive against a corrupt/out-of-range offset reaching the renderer;
 *    this must never throw, only render at the nearest valid boundary.
 *  - Content is split at the (sorted, clamped) offsets and emitted as
 *    alternating text/tool parts. Empty text slices (e.g. two calls sharing
 *    one offset, or a call at offset 0) are skipped — never an empty
 *    text part in the output.
 *  - Calls with NO offset (legacy bakes, or a reconnect edge where the
 *    starting snapshot was never recorded — see `stampToolCallOffset`'s doc
 *    comment) are appended after the final text slice, in their original
 *    array order — this is the pre-fix "text first, then tool calls"
 *    fallback, preserved exactly for calls this store genuinely cannot
 *    place.
 */
export function splitMessageParts(
  content: string,
  toolCalls: readonly PositionedToolCall[],
): MessagePart[] {
  const parts: MessagePart[] = []

  const positioned: { call: PositionedToolCall; offset: number }[] = []
  const unpositioned: PositionedToolCall[] = []
  for (const call of toolCalls) {
    if (call.textOffset === undefined) {
      unpositioned.push(call)
    } else {
      const clamped = Math.min(Math.max(call.textOffset, 0), content.length)
      positioned.push({ call, offset: clamped })
    }
  }

  // Stable sort — ties preserve input (call) order.
  positioned.sort((a, b) => a.offset - b.offset)

  let cursor = 0
  for (const { call, offset } of positioned) {
    if (offset > cursor) {
      parts.push({ type: 'text', text: content.slice(cursor, offset) })
      cursor = offset
    }
    parts.push({ type: 'tool', call })
  }
  if (cursor < content.length) {
    parts.push({ type: 'text', text: content.slice(cursor) })
  }
  for (const call of unpositioned) {
    parts.push({ type: 'tool', call })
  }

  return parts
}
