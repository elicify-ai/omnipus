// bugfixes3 deferred item 3 — cap indicator + stable ordering, component-
// level coverage for the "@" agent-mention menu (the skills-section
// equivalent lives in ChatScreen.skills-filter.test.tsx's "capped at 8"
// test). Both capped sections (skills, agents) used to silently hide
// overflow past the 8-row cap with no indication anything was cut off, and
// which 8 survived was API-order luck rather than deterministic. Fixed by
// sorting alphabetically by name BEFORE the cap (useSlashMenu.ts) and
// rendering a muted, non-interactive "+N more — keep typing to narrow"
// footer row (ChatScreen.tsx) whenever a section's hidden count is nonzero.
//
// This file needs its OWN scoped-agent fixture (10 chat-eligible agents,
// more than ChatScreen.agent-mention.test.tsx's 3) to actually exercise the
// cap, so it's a separate file rather than extending that one's shared
// module-level `mockAgents` (which many of that file's tests pin exact
// counts/order against).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { makeAgent } from '@/test/factories'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

import { OmnipusComposer } from './ChatScreen'

vi.mock('@assistant-ui/react', () => {
  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Messages: () => null,
    },
    MessagePrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Parts: () => null,
    },
    ComposerPrimitive: {
      Root: ({ children, className, onSubmit }: { children: React.ReactNode; className?: string; onSubmit?: (e: React.FormEvent) => void }) =>
        React.createElement('form', { className, onSubmit, 'data-testid': 'composer-form' }, children),
      Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur, 'data-testid': testId }: {
        disabled?: boolean; placeholder?: string; className?: string;
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void; 'data-testid'?: string;
      }) =>
        React.createElement('textarea', {
          disabled, placeholder, className,
          onChange, onKeyDown, onBlur,
          'data-testid': testId ?? 'chat-input',
        }),
      Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel }: {
        disabled?: boolean; children?: React.ReactNode; className?: string;
        'data-testid'?: string; 'aria-label'?: string;
      }) =>
        React.createElement('button', {
          type: 'button', disabled, className,
          'data-testid': testId ?? 'chat-send',
          'aria-label': ariaLabel,
        }, children),
      AddAttachment: ({ disabled, children, className, 'aria-label': ariaLabel }: {
        disabled?: boolean; children?: React.ReactNode; className?: string; 'aria-label'?: string
      }) =>
        React.createElement('button', { type: 'button', disabled, className, 'aria-label': ariaLabel, 'data-testid': 'add-attachment' }, children),
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Name: () => null,
      Remove: ({ children, className, 'aria-label': ariaLabel }: { children?: React.ReactNode; className?: string; 'aria-label'?: string }) =>
        React.createElement('button', { type: 'button', className, 'aria-label': ariaLabel }, children),
      Thumb: () => null,
    },
    MessagePartPrimitive: { InProgress: () => null },
    ActionBarPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Copy: ({ children }: { children: React.ReactNode }) => React.createElement('span', {}, children),
    },
    AuiIf: () => null,
    useComposerRuntime: vi.fn(() => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    })),
    useMessage: () => ({
      id: 'msg_1',
      role: 'assistant',
      status: { type: 'complete' },
      content: [],
    }),
    makeAssistantToolUI: () => () => null,
  }
})

const mockCommands = [
  { name: 'clear',  label: '/clear',  description: 'Start a new conversation', delivery: 'client', available_while_streaming: false },
]
const mockSkills: unknown[] = []

// 10 chat-eligible agents, zero-padded names ("Agent01".."Agent10") so
// lexicographic order matches numeric order — same convention as
// useSlashMenu.test.ts's own cap fixture.
const tenAgents = Array.from({ length: 10 }, (_, i) =>
  makeAgent({ id: `ag${String(i + 1).padStart(2, '0')}`, name: `Agent${String(i + 1).padStart(2, '0')}`, type: 'Main', status: 'active' }))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[] }) => {
      const key = opts?.queryKey
      if (Array.isArray(key) && key[0] === 'commands' && key[1] === 'web') {
        return { data: mockCommands, isError: false, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'skills') {
        return { data: mockSkills, isError: false, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'agents') {
        return { data: tenAgents, isError: false, refetch: vi.fn() }
      }
      return { data: [], isError: false, refetch: vi.fn() }
    },
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchProviders: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    uploadFiles: vi.fn(),
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

function resetStores() {
  act(() => {
    useChatStore.setState({
      messages: [],
      isStreaming: false,
      isReplaying: false,
      toolCalls: {},
      sessionTokens: 0,
      sessionCost: 0,
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: true,
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: 'sess_cap_footer_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

describe('"@" agent-mention menu — cap + hidden-count footer (deferred item 3)', () => {
  it('10 agents show exactly 8 alphabetically-ordered rows plus a "+2 more" footer', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })

    const rows = screen.getAllByTestId('agent-mention-item')
    expect(rows).toHaveLength(8)
    expect(rows.map((r) => r.textContent?.match(/@(\S+)/)?.[1])).toEqual([
      'Agent01', 'Agent02', 'Agent03', 'Agent04', 'Agent05', 'Agent06', 'Agent07', 'Agent08',
    ])
    expect(screen.getByTestId('slash-menu-footer')).toHaveTextContent('+2 more')
  })

  it('narrowing to a filter with ≤8 matches removes the footer', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    // "@agent1" prefix-matches only "Agent10" (the other 9 all have
    // "agent0" at that position, not "agent1").
    act(() => { fireEvent.change(input, { target: { value: '@agent1' } }) })

    expect(screen.getAllByTestId('agent-mention-item')).toHaveLength(1)
    expect(screen.queryByTestId('slash-menu-footer')).not.toBeInTheDocument()
  })

  it('the footer row is not reachable by ArrowDown — cycling wraps within the 8 visible rows only', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    act(() => { fireEvent.change(input, { target: { value: '@' } }) })

    // Cycle ArrowDown 8 times — wraps back to row 0 (Agent01). If the
    // footer were reachable, the highlight would land on it at some point
    // and no row would carry data-highlighted at that step; since the
    // footer isn't part of slashItems (and has no data-highlighted
    // attribute at all — it isn't even a candidate), every one of these 8
    // presses lands on a real, highlighted agent row.
    for (let i = 0; i < 8; i++) {
      act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })
      const highlighted = screen.getAllByTestId('agent-mention-item').find((r) => r.getAttribute('data-highlighted') === 'true')
      expect(highlighted).toBeDefined()
    }
    // After exactly 8 presses (wrapping modulo 8), we're back at row 0.
    const finalHighlighted = screen.getAllByTestId('agent-mention-item').find((r) => r.getAttribute('data-highlighted') === 'true')
    expect(finalHighlighted?.textContent).toContain('Agent01')
    // The footer itself never carries data-highlighted or role="option".
    const footer = screen.getByTestId('slash-menu-footer')
    expect(footer).not.toHaveAttribute('data-highlighted')
    expect(footer).not.toHaveAttribute('role', 'option')
  })
})
