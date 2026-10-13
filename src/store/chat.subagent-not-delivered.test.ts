// FR-013 / U4: a `subagent_message` of kind `not_delivered` records the refusal
// on its span (live and when it beats the span's own subagent_start) and does
// not overwrite the span's status line with the refusal text.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'

const SID = 'not-delivered-session'

function appendAssistant(id: string) {
  useChatStore.getState().appendMessage({
    id, role: 'assistant', content: 'Working...', timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true,
  })
}

function startSpan(spanId: string) {
  useChatStore.getState().handleFrame({
    type: 'subagent_start', span_id: spanId, parent_call_id: `call-${spanId}`, task_label: 'audit', agent_id: 'agent-child', session_id: SID,
  })
}

function refusal(spanId: string, text: string) {
  useChatStore.getState().handleFrame({
    type: 'subagent_message', span_id: spanId, message_id: `m-${spanId}`, kind: 'not_delivered', text,
    sender_identity: 'omnipus', untrusted_origin: false, created_at: '2026-01-01T00:00:02.000Z', session_id: SID,
  })
}

beforeEach(() => {
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({ sessionsById: {}, messages: [], isStreaming: false, toolCalls: {}, toolCallOrder: [] })
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
  })
})

describe('subagent_message not_delivered', () => {
  it('records the refusal on the span and leaves the status line alone', () => {
    act(() => {
      appendAssistant('asst-1')
      startSpan('span-1')
      useChatStore.getState().handleFrame({
        type: 'subagent_message', span_id: 'span-1', message_id: 'm-p', kind: 'progress', text: 'halfway',
        sender_identity: 'agent-child', untrusted_origin: true, created_at: '2026-01-01T00:00:01.000Z', session_id: SID,
      })
      refusal('span-1', 'Report was not saved: the inbox is full.')
    })
    const span = useChatStore.getState().messages.find((m) => m.id === 'asst-1')!.spans![0]
    expect(span.notDelivered).toEqual({ text: 'Report was not saved: the inbox is full.', at: '2026-01-01T00:00:02.000Z' })
    expect(span.statusLine).toBe('halfway')
  })

  it('keeps a refusal that arrived before its span started', () => {
    act(() => {
      appendAssistant('asst-2')
      refusal('span-2', 'Report was not saved: the inbox is full.')
      startSpan('span-2')
    })
    const span = useChatStore.getState().messages.find((m) => m.id === 'asst-2')!.spans![0]
    expect(span.notDelivered?.text).toBe('Report was not saved: the inbox is full.')
    expect(span.statusLine).toBeUndefined()
  })

  it('an ordinary message never marks a span as not delivered', () => {
    act(() => {
      appendAssistant('asst-3')
      startSpan('span-3')
      useChatStore.getState().handleFrame({
        type: 'subagent_message', span_id: 'span-3', message_id: 'm-q', kind: 'question', text: 'ok?',
        sender_identity: 'agent-child', untrusted_origin: true, created_at: '2026-01-01T00:00:03.000Z', session_id: SID,
      })
    })
    expect(useChatStore.getState().messages.find((m) => m.id === 'asst-3')!.spans![0].notDelivered).toBeUndefined()
  })
})
