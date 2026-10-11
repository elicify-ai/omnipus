// Founder UI feedback 2026-10-11, point 1 — the MessageItem reply path
// (the standalone/embed renderer, ChatThread's row) must agree with the
// ChatScreen rows on the LABEL (1c): the small AgentMark beside the name (or
// beside the U8 reply header), no status word.
//
// The streaming INDICATOR (1b) stays this path's pre-existing static
// "Thinking..." line: the shared useReplySlotPhase reads the app-wide
// connection store, whose unbound default (isConnected: false) paints an
// embed with "Unavailable/reconnecting" — a connection-state semantic the
// standalone path has never bound. Changing that is a design decision, not
// part of this dispatch (reported as a note).
//
// Harness mirrors participantLabel.messageItem.test.tsx (useQuery mocked,
// MessageItem rendered directly).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { ChatParticipant } from '@/lib/api/generated/asyncapi-types'

const { AGENTS, WORKSPACES } = vi.hoisted(() => ({
  AGENTS: [{ id: 'agent-ava', name: 'Ava', figure: 'Omnipus', role: 'general' }],
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

// MessageItem reads the whole store via useChatStore() — keep act() available.
import { act } from 'react'

function human(name: string, source: string): ChatParticipant {
  return { kind: 'human', display_name: name, source }
}

function makeMsg(overrides: Partial<ChatMessage>): ChatMessage {
  return {
    id: 'msg-1', role: 'assistant', content: 'hello', timestamp: '2026-10-11T10:00:00Z', status: 'done',
    ...overrides,
  } as ChatMessage
}

beforeEach(() => {
  act(() => {
    useChatStore.setState({ toolCalls: {}, messages: [], messagesById: {}, messageOrder: [] } as never)
  })
  useWorkspacesStore.setState({ activeWorkspaceId: 'ws-main' })
})

describe('MessageItem — the reply label carries the small agent mark (1c)', () => {
  it('label is mark + name, no status word', () => {
    const msg = makeMsg({ agentId: 'agent-ava' })
    const { container } = render(<MessageItem message={msg} />)
    const avatarWrap = container.querySelector('[data-testid="agent-label-avatar"]')
    expect(avatarWrap).not.toBeNull()
    const mark = avatarWrap?.querySelector('[data-testid="agent-icon"]')
    expect(mark).not.toBeNull()
    expect(mark).toHaveAttribute('data-size', '18')
    const label = container.querySelector('[data-testid="agent-label"]')
    expect(label?.textContent).toBe('Ava')
    expect(avatarWrap?.nextElementSibling).toBe(label)
    expect(container.textContent).not.toContain('Idle')
  })

  it('reply header: the mark sits beside "Ava → Alice · Telegram"', () => {
    const msg = makeMsg({ agentId: 'agent-ava', replyToMessageId: 'req-1', replyToParticipant: human('Alice', 'telegram') })
    const { container } = render(<MessageItem message={msg} />)
    const avatarWrap = container.querySelector('[data-testid="agent-label-avatar"]')
    expect(avatarWrap).not.toBeNull()
    const header = container.querySelector('[data-testid="reply-header"]')
    expect(header?.textContent).toBe('Ava → Alice · Telegram')
    expect(avatarWrap?.nextElementSibling).toBe(header)
  })

  it('a settled reply shows no status word', () => {
    const msg = makeMsg({ agentId: 'agent-ava', content: 'Done long ago' })
    const { container } = render(<MessageItem message={msg} />)
    expect(container.querySelector('[data-testid="reply-status-indicator"]')).toBeNull()
    expect(container.textContent).not.toContain('Idle')
  })
})
