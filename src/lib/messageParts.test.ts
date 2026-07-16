import { describe, it, expect } from 'vitest'
import { splitMessageParts } from './messageParts'
import type { PositionedToolCall } from '@/store/chat'

function call(id: string, textOffset?: number): PositionedToolCall {
  return {
    id,
    tool: `tool_${id}`,
    params: {},
    status: 'success',
    ...(textOffset !== undefined ? { textOffset } : {}),
  }
}

describe('splitMessageParts', () => {
  it('interleaves a single tool call between two text segments (the core streaming-parity case)', () => {
    // Mirrors handleFrame sequence: token('Let me check. ') -> tool_call_start
    // -> tool_call_result -> token('The answer is 42.') -> done.
    const content = 'Let me check. \n\nThe answer is 42.'
    const tc1 = call('tc1', 'Let me check. '.length)

    const parts = splitMessageParts(content, [tc1])

    expect(parts).toEqual([
      { type: 'text', text: 'Let me check. ' },
      { type: 'tool', call: tc1 },
      { type: 'text', text: '\n\nThe answer is 42.' },
    ])
  })

  it('interleaves two calls separated by distinct text segments, in offset order', () => {
    // Pinned against chat.tool-call-offset.test.ts's exact fixture:
    // content 'A\n\nB\n\nC', tc1 offset 1 ('A'.length), tc2 offset 4 ('A\n\nB'.length).
    const content = 'A\n\nB\n\nC'
    const tc1 = call('tc1', 1)
    const tc2 = call('tc2', 4)

    const parts = splitMessageParts(content, [tc1, tc2])

    expect(parts).toEqual([
      { type: 'text', text: 'A' },
      { type: 'tool', call: tc1 },
      { type: 'text', text: '\n\nB' },
      { type: 'tool', call: tc2 },
      { type: 'text', text: '\n\nC' },
    ])
  })

  it('back-to-back calls sharing the same offset render adjacently, no empty text slice between them, array order preserved on the tie', () => {
    const content = 'A\n\nB'
    const tc1 = call('tc1', 1)
    const tc2 = call('tc2', 1)

    const parts = splitMessageParts(content, [tc1, tc2])

    expect(parts).toEqual([
      { type: 'text', text: 'A' },
      { type: 'tool', call: tc1 },
      { type: 'tool', call: tc2 },
      { type: 'text', text: '\n\nB' },
    ])
  })

  it('a tie is broken by input array order even when that is NOT id-sorted order', () => {
    const content = 'X'
    const second = call('second', 0)
    const first = call('first', 0)

    // Input order is [second, first] — output must keep that order for the tie.
    const parts = splitMessageParts(content, [second, first])

    expect(parts).toEqual([
      { type: 'tool', call: second },
      { type: 'tool', call: first },
      { type: 'text', text: 'X' },
    ])
  })

  it('all-undefined offsets fall back to pure old behavior: all text first, then all tool calls in array order', () => {
    const content = 'Hello'
    const tc1 = call('tc1')
    const tc2 = call('tc2')

    const parts = splitMessageParts(content, [tc1, tc2])

    expect(parts).toEqual([
      { type: 'text', text: 'Hello' },
      { type: 'tool', call: tc1 },
      { type: 'tool', call: tc2 },
    ])
  })

  it('offset 0 places the tool call before any text', () => {
    const content = 'Hello'
    const tc1 = call('tc1', 0)

    const parts = splitMessageParts(content, [tc1])

    expect(parts).toEqual([
      { type: 'tool', call: tc1 },
      { type: 'text', text: 'Hello' },
    ])
  })

  it('an offset beyond content length is clamped and the call renders after all text, without throwing', () => {
    const content = 'Hi'
    const tc1 = call('tc1', 9999)

    expect(() => splitMessageParts(content, [tc1])).not.toThrow()
    const parts = splitMessageParts(content, [tc1])

    expect(parts).toEqual([
      { type: 'text', text: 'Hi' },
      { type: 'tool', call: tc1 },
    ])
  })

  it('a negative offset is clamped to 0 rather than crashing or producing a negative-length slice', () => {
    const content = 'Hi'
    const tc1 = call('tc1', -5)

    expect(() => splitMessageParts(content, [tc1])).not.toThrow()
    const parts = splitMessageParts(content, [tc1])

    expect(parts).toEqual([
      { type: 'tool', call: tc1 },
      { type: 'text', text: 'Hi' },
    ])
  })

  it('empty content with tool calls renders only the tool calls, no empty text parts', () => {
    const tc1 = call('tc1', 0)
    const tc2 = call('tc2')

    const parts = splitMessageParts('', [tc1, tc2])

    expect(parts).toEqual([
      { type: 'tool', call: tc1 },
      { type: 'tool', call: tc2 },
    ])
  })

  it('empty content and no tool calls renders an empty parts array', () => {
    expect(splitMessageParts('', [])).toEqual([])
  })

  it('content with no tool calls renders a single text part', () => {
    expect(splitMessageParts('just text', [])).toEqual([
      { type: 'text', text: 'just text' },
    ])
  })

  it('a mix of positioned and unpositioned calls: positioned ones interleave, unpositioned ones land after all text', () => {
    const content = 'A\n\nB'
    const positioned = call('positioned', 1)
    const legacy = call('legacy') // no offset — reconnect edge fallback

    const parts = splitMessageParts(content, [legacy, positioned])

    expect(parts).toEqual([
      { type: 'text', text: 'A' },
      { type: 'tool', call: positioned },
      { type: 'text', text: '\n\nB' },
      { type: 'tool', call: legacy },
    ])
  })
})
