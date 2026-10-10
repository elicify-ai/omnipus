// U8 participant labels — MessageItem parity (the live/standalone surface).
// F15/D4: "Live and replay must render identically for the same message id."
// MessageItem must use the SAME label helpers and the SAME reply-pairing chip
// filter as the virtualized rows (participantLabel.render.test.tsx covers
// those). These tests pin the MessageItem side of that contract.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { useChatStore } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { ChatParticipant } from '@/lib/api/generated/asyncapi-types'

const { AGENTS, WORKSPACES } = vi.hoisted(() => ({
  AGENTS: [{ id: 'agent-ava', name: 'Ava' }],
  WORKSPACES: [{ id: 'ws-main', name: 'Main' }],
}))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey: readonly unknown[] }) => {
      const head = opts.queryKey[0]
      if (head === 'agents') return { data: AGENTS, isError: false, refetch: vi.fn() }
      if (head === 'workspaces') return { data: WORKSPACES, isError: false, refetch: vi.fn() }
      return { data: [], isError: false, refetch: vi.fn() }
    },
  }
})

import { MessageItem } from './MessageItem'

function human(name: string, source: string): ChatParticipant {
  return { kind: 'human', display_name: name, source }
}

function makeMsg(overrides: Partial<ChatMessage>): ChatMessage {
  return {
    id: 'msg-1', role: 'assistant', content: 'hello', timestamp: '2026-10-11T10:00:00Z', status: 'done',
    ...overrides,
  } as ChatMessage
}

function seed(messages: ChatMessage[], toolCalls: Record<string, unknown> = {}): void {
  act(() => {
    useChatStore.setState((s) => ({
      ...s,
      messages,
      messagesById: Object.fromEntries(messages.map((m) => [m.id, m])),
      messageOrder: messages.map((m) => m.id),
      toolCalls: toolCalls as never,
    }))
  })
  useWorkspacesStore.setState({ activeWorkspaceId: 'ws-main' })
}

// MessageItem reads the whole store via useChatStore() — keep act() available.
import { act } from 'react'

beforeEach(() => {
  act(() => {
    useChatStore.setState({ toolCalls: {}, messages: [], messagesById: {}, messageOrder: [] } as never)
  })
})

describe('MessageItem — reply header', () => {
  it('renders "Ava → Alice · Telegram" for a reply carrying reply_to_participant', () => {
    const msg = makeMsg({ agentId: 'agent-ava', replyToMessageId: 'req-1', replyToParticipant: human('Alice', 'telegram') })
    const { container } = render(<MessageItem message={msg} />)
    expect(container.querySelector('[data-testid="reply-header"]')?.textContent).toBe('Ava → Alice · Telegram')
  })

  it('falls back to "Ava → reply" when reply_to_participant is absent', () => {
    const msg = makeMsg({ agentId: 'agent-ava', replyToMessageId: 'req-1' })
    const { container } = render(<MessageItem message={msg} />)
    expect(container.querySelector('[data-testid="reply-header"]')?.textContent).toBe('Ava → reply')
  })

  it('a plain assistant message keeps the plain agent label and no header', () => {
    const msg = makeMsg({ agentId: 'agent-ava' })
    const { container } = render(<MessageItem message={msg} />)
    expect(container.querySelector('[data-testid="agent-label"]')?.textContent).toBe('Ava')
    expect(container.querySelector('[data-testid="reply-header"]')).toBeNull()
  })
})

describe('MessageItem — the paired send_message chip is hidden (same helper as the rows)', () => {
  const turn = makeMsg({
    id: 'turn-1',
    content: 'Sending that over.',
    tool_calls: [
      { id: 'call-paired', tool: 'send_message', status: 'success', params: { reply_to: 'req-1' } },
      { id: 'call-unpaired', tool: 'send_message', status: 'success', params: { reply_to: 'req-9' } },
    ],
  })
  const reply = makeMsg({
    id: 'reply-1', content: 'the answer',
    replyToMessageId: 'req-1', replyToParticipant: human('Alice', 'telegram'),
  })

  it('hides only the send_message call paired with a reply bubble in the thread', () => {
    seed([turn, reply], {
      'call-paired': { id: 'call-paired', tool: 'send_message', status: 'success', params: { reply_to: 'req-1' } },
      'call-unpaired': { id: 'call-unpaired', tool: 'send_message', status: 'success', params: { reply_to: 'req-9' } },
    })
    const { container } = render(<MessageItem message={turn} />)
    const badges = [...container.querySelectorAll('[data-testid="tool-call-badge"]')]
    expect(badges).toHaveLength(1)
    expect(badges[0].textContent).toBe('send_message')
  })

  it('renders the chip when no reply bubble carries the pairing', () => {
    seed([turn], {
      'call-paired': { id: 'call-paired', tool: 'send_message', status: 'success', params: { reply_to: 'req-1' } },
      'call-unpaired': { id: 'call-unpaired', tool: 'send_message', status: 'success', params: { reply_to: 'req-9' } },
    })
    const { container } = render(<MessageItem message={turn} />)
    expect(container.querySelectorAll('[data-testid="tool-call-badge"]')).toHaveLength(2)
  })
})

describe('MessageItem — participant label on a left-side user message', () => {
  it('renders the participant label with the same helper', () => {
    const msg = makeMsg({ id: 'tg-1', role: 'user', content: 'from Telegram', participant: human('Alice', 'telegram') })
    render(<MessageItem message={msg} />)
    expect(screen.getByTestId('participant-label').textContent).toBe('Alice · Telegram')
  })

  it('renders markdown-ish display_name literally', () => {
    const msg = makeMsg({ id: 'md-1', role: 'user', content: 'hi', participant: human('**Alice** <b>X</b>', 'slack') })
    render(<MessageItem message={msg} />)
    const label = screen.getByTestId('participant-label')
    expect(label.textContent).toBe('**Alice** <b>X</b> · Slack')
    expect(label.querySelector('strong, b')).toBeNull()
  })

  it('truncates a long display_name and reveals the full text in the tooltip', () => {
    const longName = 'N'.repeat(60)
    const msg = makeMsg({ id: 'tg-2', role: 'user', content: 'hi', participant: human(longName, 'telegram') })
    render(<MessageItem message={msg} />)
    const label = screen.getByTestId('participant-label')
    expect(label.textContent?.length).toBe(40)
    expect(label.textContent?.endsWith('…')).toBe(true)
    fireEvent.mouseEnter(label)
    expect(screen.getAllByText(longName + ' · Telegram').length).toBeGreaterThan(0)
  })
})
