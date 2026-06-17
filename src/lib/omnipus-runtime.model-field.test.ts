/**
 * omnipus-runtime.model-field.test.ts
 *
 * Wave 2 / FR-014: per-turn model field on replay.
 *
 * The AssistantUI external-store runtime adapter reads each message from the
 * chat store and converts it to a `ThreadMessageLike` for rendering. After
 * Wave 1B lands the per-turn `model` field on the wire transcript, the
 * adapter must surface that field on the resulting `ThreadMessageLike` so
 * the UI can render "this turn was produced by model X".
 *
 * Spec §18 Q6: legacy turns (no `model` field recorded) MUST NOT show any
 * model info. No placeholder text, no "(model not recorded)" string — the
 * UI just doesn't render the model line for those turns.
 *
 * Traces to: docs/internal/specs/phase-1-chat-model-and-errors.md
 *   - §13 FR-013 (per-turn model on assistant message)
 *   - §13 FR-014 (UI conditionally shows model field)
 *   - §18 Q6 (no placeholder for legacy)
 *   - §11 Dataset 5 (transcript model record test rows)
 *
 * Approach: drive `convertMessage` directly (it's a pure function).
 * The `useOmnipusRuntime` hook is exercised in
 * `omnipus-runtime.attachments.test.tsx` — these tests focus on the
 * per-message conversion output (where the model field is added).
 */

import { describe, it, expect } from 'vitest'
import { convertMessage } from './omnipus-runtime'
import type { ChatMessage } from '@/store/chat'

// convertMessage is pure given its args. We hand-build the call signature
// with empty tool-call state (no tool calls, no live state) — these tests
// are about the model-field surface, not the tool-call interleaving.

const EMPTY_TOOL_CALLS = {} as never
const EMPTY_TOOL_CALL_ORDER: string[] = []
const EMPTY_TEXT_AT_TOOL_CALL_START = {}

function makeAssistantMsg(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: 'a1',
    role: 'assistant',
    content: 'hello',
    timestamp: '2026-06-17T10:00:01Z',
    status: 'done',
    ...overrides,
  } as ChatMessage
}

describe('convertMessage — per-turn model field (FR-014)', () => {
  it('exposes the assistant model on metadata.custom for turns that have it recorded', () => {
    // New assistant turn carries `model: "z-ai/glm-5.2"` from the wire
    // transcript. The runtime adapter must surface it on the resulting
    // ThreadMessageLike so the UI can render it.
    const msg = makeAssistantMsg({ model: 'z-ai/glm-5.2' })
    const out = convertMessage(
      msg,
      EMPTY_TOOL_CALLS,
      EMPTY_TOOL_CALL_ORDER,
      EMPTY_TEXT_AT_TOOL_CALL_START,
      false,
    )
    const custom = (out as { metadata?: { custom?: { model?: string } } }).metadata?.custom
    expect(custom?.model).toBe('z-ai/glm-5.2')
  })

  it('omits metadata.custom for legacy turns (no model recorded)', () => {
    // Spec §18 Q6: legacy turns (no model field) MUST NOT show any
    // model info. The runtime adapter must NOT inject a placeholder
    // string — metadata.custom must be absent (or its model field
    // must be undefined).
    const msg = makeAssistantMsg()
    const out = convertMessage(
      msg,
      EMPTY_TOOL_CALLS,
      EMPTY_TOOL_CALL_ORDER,
      EMPTY_TEXT_AT_TOOL_CALL_START,
      false,
    )
    const custom = (out as { metadata?: { custom?: { model?: string } } }).metadata?.custom
    expect(custom?.model).toBeUndefined()
  })

  it('treats empty-string model the same as absent (no placeholder rendered)', () => {
    // Edge case: a server bug or test fixture could send model: "".
    // The runtime must NOT expose "" — the renderer treats that the
    // same as absent (per spec §11 Dataset 5 row 3).
    const msg = makeAssistantMsg({ model: '' })
    const out = convertMessage(
      msg,
      EMPTY_TOOL_CALLS,
      EMPTY_TOOL_CALL_ORDER,
      EMPTY_TEXT_AT_TOOL_CALL_START,
      false,
    )
    const custom = (out as { metadata?: { custom?: { model?: string } } }).metadata?.custom
    expect(custom?.model).toBeUndefined()
  })

  it('treats whitespace-only model the same as absent', () => {
    // Belt-and-suspenders: trim() catches " " (whitespace) the same as "".
    const msg = makeAssistantMsg({ model: '   ' })
    const out = convertMessage(
      msg,
      EMPTY_TOOL_CALLS,
      EMPTY_TOOL_CALL_ORDER,
      EMPTY_TEXT_AT_TOOL_CALL_START,
      false,
    )
    const custom = (out as { metadata?: { custom?: { model?: string } } }).metadata?.custom
    expect(custom?.model).toBeUndefined()
  })

  it('does not put a model field on user messages', () => {
    // Per-turn model record is an assistant-only concept. User messages
    // must not carry a model metadata.
    const userMsg: ChatMessage = {
      id: 'u1',
      role: 'user',
      content: 'hi',
      timestamp: '2026-06-17T10:00:00Z',
      status: 'done',
    } as ChatMessage
    const out = convertMessage(
      userMsg,
      EMPTY_TOOL_CALLS,
      EMPTY_TOOL_CALL_ORDER,
      EMPTY_TEXT_AT_TOOL_CALL_START,
      false,
    )
    const custom = (out as { metadata?: { custom?: { model?: string } } }).metadata?.custom
    expect(custom?.model).toBeUndefined()
  })

  it('does not put a model field on system messages', () => {
    // Per-turn model record is an assistant-only concept. System messages
    // must not carry a model metadata.
    const systemMsg: ChatMessage = {
      id: 's1',
      role: 'system',
      content: 'system banner',
      timestamp: '2026-06-17T10:00:00Z',
      status: 'done',
    } as ChatMessage
    const out = convertMessage(
      systemMsg,
      EMPTY_TOOL_CALLS,
      EMPTY_TOOL_CALL_ORDER,
      EMPTY_TEXT_AT_TOOL_CALL_START,
      false,
    )
    const custom = (out as { metadata?: { custom?: { model?: string } } }).metadata?.custom
    expect(custom?.model).toBeUndefined()
  })
})
