import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { act, type ReactElement } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MessageItem } from './MessageItem'
import { useChatStore } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'

// test_chat_message_component_user (test #2)
// test_chat_message_component_assistant (test #3)
// test_thinking_indicator (test #4)
// test_streaming_cursor (test #5)
// Traces to: wave5a-wire-ui-spec.md — Scenario: User message appears optimistically
//             wave5a-wire-ui-spec.md — Scenario: Streaming response completes with markdown rendering
//             wave5a-wire-ui-spec.md — Scenario: Thinking indicator shows before first token
//             wave5a-wire-ui-spec.md — Scenario: Cancel preserves partial with "(interrupted)" label

function makeQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderWithQuery(ui: ReactElement) {
  return render(
    <QueryClientProvider client={makeQueryClient()}>{ui}</QueryClientProvider>
  )
}

beforeEach(() => {
  act(() => {
    useChatStore.setState({ toolCalls: {}, pendingApprovals: [] })
  })
})

const makeMsg = (overrides: Partial<ChatMessage>): ChatMessage => ({
  id: 'msg_1',
  session_id: 'sess_1',
  role: 'user',
  content: 'Hello',
  timestamp: '2026-03-29T10:00:00Z',
  status: 'done',
  ...overrides,
})

describe('MessageItem — user message', () => {
  it('renders user message content', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: User message appears optimistically
    renderWithQuery(<MessageItem message={makeMsg({ role: 'user', content: 'Hello, world!' })} />)
    expect(screen.getByText('Hello, world!')).toBeInTheDocument()
  })

  it('renders timestamp for user message', () => {
    renderWithQuery(<MessageItem message={makeMsg({ role: 'user', timestamp: '2026-03-29T10:30:00Z' })} />)
    // Time formatted as HH:MM
    expect(document.querySelector('span')?.textContent).toBeTruthy()
  })
})

describe('MessageItem — assistant message with markdown', () => {
  it('renders markdown headings for assistant messages', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Streaming completes with markdown
    // Dataset: Message Rendering row 2
    renderWithQuery(
      <MessageItem
        message={makeMsg({ role: 'assistant', content: '# Main Heading' })}
      />
    )
    expect(screen.getByRole('heading', { level: 1 })).toBeInTheDocument()
  })

  it('renders markdown code blocks', () => {
    // Dataset: Message Rendering row 3
    renderWithQuery(
      <MessageItem
        message={makeMsg({ role: 'assistant', content: "```python\nprint('hello')\n```" })}
      />
    )
    expect(document.querySelector('pre')).toBeTruthy()
  })

  it('does not crash with empty content', () => {
    // Dataset: Message Rendering row 5 — empty content
    expect(() =>
      renderWithQuery(<MessageItem message={makeMsg({ role: 'assistant', content: '' })} />)
    ).not.toThrow()
  })
})

describe('MessageItem — thinking indicator (test #4)', () => {
  it('shows "Thinking..." when assistant message is streaming with empty content', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Thinking indicator shows before first token
    renderWithQuery(
      <MessageItem
        message={makeMsg({
          role: 'assistant',
          content: '',
          isStreaming: true,
          status: 'streaming',
        })}
      />
    )
    expect(screen.getByText(/thinking/i)).toBeInTheDocument()
  })

  it('does not show "Thinking..." once tokens arrive (content non-empty)', () => {
    renderWithQuery(
      <MessageItem
        message={makeMsg({
          role: 'assistant',
          content: 'Hello',
          isStreaming: true,
          status: 'streaming',
        })}
      />
    )
    expect(screen.queryByText(/thinking/i)).toBeNull()
  })
})

describe('MessageItem — streaming cursor (test #5)', () => {
  it('does not render a pulsing cursor (removed in favor of the rotating thinking indicator)', () => {
    const { container } = renderWithQuery(
      <MessageItem
        message={makeMsg({
          role: 'assistant',
          content: 'Hello world',
          isStreaming: true,
        })}
      />
    )
    const cursor = container.querySelector('.animate-pulse')
    expect(cursor).toBeFalsy()
  })
})

describe('MessageItem — system/compaction message', () => {
  it('renders compaction entry as centered pill message', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Compaction entries render as system messages (AC5)
    renderWithQuery(
      <MessageItem
        message={makeMsg({
          role: 'system',
          content: 'Context compacted — older messages summarized',
        })}
      />
    )
    expect(screen.getByText(/context compacted/i)).toBeInTheDocument()
  })
})

describe('MessageItem — interrupted status', () => {
  it('shows "(interrupted)" label for interrupted messages', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel during streaming — AC5 partial preserved
    renderWithQuery(
      <MessageItem
        message={makeMsg({
          role: 'assistant',
          content: 'Here is the analysis of...',
          status: 'interrupted',
        })}
      />
    )
    expect(screen.getByText(/interrupted/i)).toBeInTheDocument()
  })
})

describe('MessageItem — XSS safety', () => {
  it('renders XSS token content as escaped text', () => {
    // Dataset: WebSocket Message Parsing row 5 — XSS content must not execute
    renderWithQuery(
      <MessageItem
        message={makeMsg({
          role: 'assistant',
          content: '<script>alert(1)</script>',
          status: 'done',
        })}
      />
    )
    // Script tag must not be executable
    expect(document.querySelectorAll('script')).toHaveLength(0)
  })
})
