/**
 * MessageItem cost removal tests.
 *
 * Asserts that MessageItem renders NO dollar sign or cost display —
 * even when a message carries a cost field.
 */
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MessageItem } from './MessageItem'
import type { ChatMessage } from '@/store/chat'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn().mockResolvedValue([]) }
})

vi.mock('@/store/chat', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/chat')>()
  return {
    ...actual,
    useChatStore: vi.fn((selector?: (state: Record<string, unknown>) => unknown) => {
      const state = { toolCalls: {} }
      return selector ? selector(state as unknown as Parameters<typeof selector>[0]) : state
    }),
  }
})

const mockAssistantMessage: ChatMessage = {
  id: 'msg-1',
  role: 'assistant',
  content: 'Hello, how can I help?',
  timestamp: '2026-06-01T10:00:00Z',
  status: 'done',
  cost: 0.005,
}

const mockUserMessage: ChatMessage = {
  id: 'msg-2',
  role: 'user',
  content: 'Hi there',
  timestamp: '2026-06-01T09:59:00Z',
  status: 'done',
  cost: 0.001,
}

function renderMessage(message: ChatMessage) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MessageItem message={message} />
    </QueryClientProvider>,
  )
}

describe('MessageItem — no cost display', () => {
  it('renders no dollar sign for assistant messages with cost', () => {
    const { container } = renderMessage(mockAssistantMessage)
    expect(container.textContent).not.toMatch(/\$\d/)
    expect(container.textContent).not.toMatch(/\$0/)
    expect(container.textContent).not.toMatch(/<\$/)
  })

  it('renders no dollar sign for user messages with cost', () => {
    const { container } = renderMessage(mockUserMessage)
    expect(container.textContent).not.toMatch(/\$\d/)
  })

  it('renders no CurrencyDollar icon', () => {
    const { container } = renderMessage(mockAssistantMessage)
    expect(container.querySelector('[data-phosphor-icon="CurrencyDollar"]')).toBeNull()
  })

  it('still renders message content correctly', () => {
    renderMessage(mockAssistantMessage)
    expect(screen.getByText('Hello, how can I help?')).toBeInTheDocument()
  })
})
