/**
 * omnipus-runtime.issue-617-isError.test.ts
 *
 * C2 hardening (second review wave on branch fix/615-617-618-hardening): the
 * `isError: resolved.status === "error"` assignment in omnipus-runtime.ts
 * exists at TWO live construction sites — pushHistoryParts's single
 * interleave loop, plus buildContentParts's own live-tool-call site — and
 * IS the #617 fix for the live AssistantUI runtime path; everything else in
 * that file is plumbing. No existing test imported or exercised them:
 * `convertMessage` is already exported and unit-tested by
 * omnipus-runtime.model-field.test.ts, but only for the per-turn `model`
 * field, never for a tool-call part's `isError`. Deleting either line would
 * leave the whole SPA test suite green while every tool call in a
 * live/streamed thread silently reverted to rendering failures as
 * successes.
 *
 * F5 (later pass, same wave): pushHistoryParts used to also carry a THIRD,
 * `textAtToolCallStart === undefined` fallback construction site with its
 * own copy of this same `isError` line. That third site was dead code — its
 * sole caller (buildContentParts) always passes a defined `Record` (see
 * pushHistoryParts's own doc comment) — so it was unreachable from
 * `convertMessage` and could never have been exercised by a test regardless
 * of coverage effort. It has since been deleted along with the rest of that
 * unreachable branch; this file's coverage of the two REAL sites was
 * already complete before that deletion and needed no changes.
 *
 * Approach mirrors omnipus-runtime.model-field.test.ts: drive `convertMessage`
 * directly (it's a pure function) rather than mounting the full
 * useOmnipusRuntime hook.
 */

import { describe, it, expect } from 'vitest'
import { convertMessage } from './omnipus-runtime'
import type { ChatMessage, PositionedToolCall } from '@/store/chat'
import type { ToolCall } from '@/lib/api'

type StoreToolCall = ToolCall & { call_id: string }

function makeAssistantMsg(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: 'a1',
    role: 'assistant',
    content: 'done',
    timestamp: '2026-08-11T10:00:01Z',
    status: 'done',
    ...overrides,
  } as ChatMessage
}

/** Finds the sole tool-call part in a convertMessage() output's content array. */
function findToolCallPart(content: unknown): { toolCallId: string; isError?: boolean } {
  const parts = content as Array<{ type: string; toolCallId?: string; isError?: boolean }>
  const found = parts.find((p) => p.type === 'tool-call')
  if (!found) throw new Error('no tool-call part found in convertMessage output')
  return found as { toolCallId: string; isError?: boolean }
}

describe('convertMessage — issue #617 isError derivation (history path, pushHistoryParts)', () => {
  // isLastAssistant=false routes through pushHistoryParts's (sole, since F5)
  // interleave loop — buildContentParts always passes a defined
  // textAtToolCallStart object, even {} (see pushHistoryParts's own doc
  // comment for why that parameter is required, not optional).
  const EMPTY_TEXT_AT_TOOL_CALL_START = {}
  const EMPTY_TOOL_CALL_ORDER: string[] = []

  it('a history tool call with status:"error" yields isError:true', () => {
    const msg = makeAssistantMsg({
      tool_calls: [
        { id: 'tc1', tool: 'bash', params: {}, status: 'error' } as PositionedToolCall,
      ],
    })
    const out = convertMessage(msg, {}, EMPTY_TOOL_CALL_ORDER, EMPTY_TEXT_AT_TOOL_CALL_START, false)
    expect(findToolCallPart(out.content).isError).toBe(true)
  })

  it('a history tool call with status:"cancelled" yields isError:false (not conflated with a failure)', () => {
    const msg = makeAssistantMsg({
      tool_calls: [
        { id: 'tc2', tool: 'bash', params: {}, status: 'cancelled' } as PositionedToolCall,
      ],
    })
    const out = convertMessage(msg, {}, EMPTY_TOOL_CALL_ORDER, EMPTY_TEXT_AT_TOOL_CALL_START, false)
    expect(findToolCallPart(out.content).isError).toBe(false)
  })

  it('a history tool call with status:"success" yields isError:false', () => {
    const msg = makeAssistantMsg({
      tool_calls: [
        { id: 'tc3', tool: 'bash', params: {}, status: 'success' } as PositionedToolCall,
      ],
    })
    const out = convertMessage(msg, {}, EMPTY_TOOL_CALL_ORDER, EMPTY_TEXT_AT_TOOL_CALL_START, false)
    expect(findToolCallPart(out.content).isError).toBe(false)
  })

  it('prefers the live store toolCalls map over the message-baked entry (resolved = toolCalls[tc.id] ?? tc)', () => {
    // The message's own baked tool_calls entry says 'success', but the store's
    // live toolCalls map (passed separately) has since been corrected/updated
    // to 'error' — pushHistoryParts resolves via the store map first.
    const msg = makeAssistantMsg({
      tool_calls: [
        { id: 'tc4', tool: 'bash', params: {}, status: 'success' } as PositionedToolCall,
      ],
    })
    const storeToolCalls: Record<string, StoreToolCall> = {
      tc4: { id: 'tc4', call_id: 'tc4', tool: 'bash', params: {}, status: 'error' },
    }
    const out = convertMessage(msg, storeToolCalls, EMPTY_TOOL_CALL_ORDER, EMPTY_TEXT_AT_TOOL_CALL_START, false)
    expect(findToolCallPart(out.content).isError).toBe(true)
  })
})

describe('convertMessage — issue #617 isError derivation (live path, buildContentParts)', () => {
  // isLastAssistant=true with a live (not-yet-baked) tool call in
  // toolCallOrder/toolCalls — buildContentParts's own interleave loop
  // (not pushHistoryParts) constructs this tool-call part.
  it('a live (in-progress-turn) tool call with status:"error" yields isError:true', () => {
    const msg = makeAssistantMsg({ tool_calls: undefined })
    const toolCalls: Record<string, StoreToolCall> = {
      tc_live: { id: 'tc_live', call_id: 'tc_live', tool: 'bash', params: {}, status: 'error' },
    }
    const out = convertMessage(msg, toolCalls, ['tc_live'], {}, true)
    expect(findToolCallPart(out.content).isError).toBe(true)
  })

  it('a live tool call with status:"success" yields isError:false', () => {
    const msg = makeAssistantMsg({ tool_calls: undefined })
    const toolCalls: Record<string, StoreToolCall> = {
      tc_live_ok: { id: 'tc_live_ok', call_id: 'tc_live_ok', tool: 'bash', params: {}, status: 'success' },
    }
    const out = convertMessage(msg, toolCalls, ['tc_live_ok'], {}, true)
    expect(findToolCallPart(out.content).isError).toBe(false)
  })

  it('a live tool call with status:"cancelled" yields isError:false', () => {
    const msg = makeAssistantMsg({ tool_calls: undefined })
    const toolCalls: Record<string, StoreToolCall> = {
      tc_live_cancel: { id: 'tc_live_cancel', call_id: 'tc_live_cancel', tool: 'bash', params: {}, status: 'cancelled' },
    }
    const out = convertMessage(msg, toolCalls, ['tc_live_cancel'], {}, true)
    expect(findToolCallPart(out.content).isError).toBe(false)
  })
})
