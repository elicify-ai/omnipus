// U8 participant labels — render contract on the chat thread (F15/F17, D4).
//
// Rows (both list paths route through the same row components, so these cover
// the virtualized path; MessageItem — the live/standalone surface — has its
// own parity file). Design of record:
//   ARCHITECT-ANSWER-PARTICIPANT.md §D4 + founder ruling F17:
//   own messages  → right side, NO label (participant absent — the backend
//                   stamps participants only on server-written entries);
//   other humans  → LEFT side, "Alice · Telegram";
//   agent participants → live agent name by agent.agent_id (fallback
//                   display_name), + " · workspace" when the participant's
//                   workspace differs from the current one;
//   replies       → header "Ava → Alice · Telegram" ("… → reply" fallback);
//   display_name  → PLAIN TEXT, ellipsis ~40 chars, full text in a tooltip;
//   reply pairing → the send_message tool chip whose reply_to matches a
//                   reply bubble's reply_to_message_id is HIDDEN (F17 (1)).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { ChatParticipant } from '@/lib/api/generated/asyncapi-types'

// ── Query fixtures (served to every useQuery by key) ──────────────────────────
// vi.hoisted: the vi.mock factories below run before the module body, so the
// fixtures they serve must be hoisted with them.

const { AGENTS, WORKSPACES } = vi.hoisted(() => ({
  AGENTS: [
    { id: 'agent-ava', name: 'Ava' },
    { id: 'agent-mia', name: 'Mia' },
  ],
  WORKSPACES: [
    { id: 'ws-main', name: 'Main' },
    { id: 'ws-atlas', name: 'Atlas' },
  ],
}))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey: readonly unknown[] }) => {
      const head = opts.queryKey[0]
      if (head === 'agents') return { data: AGENTS, isError: false, refetch: vi.fn() }
      if (head === 'workspaces') return { data: WORKSPACES, isError: false, refetch: vi.fn() }
      return { data: queryDataWithEmptyHistory(opts.queryKey), isError: false, refetch: vi.fn() }
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

import { MockButton } from '@/test/assistantUiMock'

// The composer context (OmnipusComposer → useComposerRuntime) needs the same
// mock surface the other ChatScreen tests pass: composer primitives + the
// composer/thread runtime hooks.
vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock({
  ThreadPrimitive: {
    Viewport: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
              React.createElement('div', { className }, children),
  },
  ComposerPrimitive: {
    Root: ({ children, className, onSubmit }: { children?: React.ReactNode; className?: string; onSubmit?: (e: React.FormEvent) => void }) =>
              React.createElement('form', { className, onSubmit, 'data-testid': 'composer-form' }, children),
    Input: (props: Record<string, unknown>) => React.createElement('textarea', props),
    Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel }: {
              disabled?: boolean; children?: React.ReactNode; className?: string;
              'data-testid'?: string; 'aria-label'?: string;
            }) =>
              React.createElement(MockButton, {
                type: 'button', disabled, className,
                'data-testid': testId ?? 'chat-send',
                'aria-label': ariaLabel, children}),
    AddAttachment: ({ disabled, children, className, 'aria-label': ariaLabel }: {
              disabled?: boolean; children?: React.ReactNode; className?: string; 'aria-label'?: string;
            }) =>
              React.createElement(MockButton, {
                type: 'button', disabled, className, 'aria-label': ariaLabel, 'data-testid': 'add-attachment', children}),
  },
  AttachmentPrimitive: {
    Remove: ({ children, className, 'aria-label': ariaLabel }: {
              children?: React.ReactNode; className?: string; 'aria-label'?: string;
            }) =>
              React.createElement(MockButton, {
                type: 'button', className, 'aria-label': ariaLabel, children}),
  },
  ActionBarPrimitive: {
    Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
              React.createElement('div', { className }, children),
  },
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
}))

import { emptyHistoryPage, queryDataWithEmptyHistory, type HistoryReadPort } from '../../../tests/chat-history-paging/legacy-fixtures'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue(AGENTS),
    fetchWorkspaces: vi.fn().mockResolvedValue(WORKSPACES),
    fetchSessionMessages: vi.fn<HistoryReadPort>(async (sessionId) => emptyHistoryPage(sessionId)),
    fetchAboutInfo: vi.fn().mockResolvedValue({}),
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    isApiError: vi.fn().mockReturnValue(false),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) => {
    return React.createElement('div', { 'data-testid': 'historical-markdown' }, content)
  },
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./tools/GenericToolCall', () => ({
  GenericToolCall: ({ toolName }: { toolName: string }) => {
    return React.createElement('div', { 'data-testid': 'tool-call-badge' }, toolName)
  },
}))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => React.createElement('div', {}),
}))
vi.mock('./shiki-highlighter', () => ({
  SyntaxHighlighter: ({ children }: { children?: React.ReactNode }) => React.createElement('pre', {}, children),
  CopyCodeHeader: () => null,
}))
vi.mock('./image-lightbox', () => ({ ImageLightbox: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

class StubResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
void StubResizeObserver

// ── Import AFTER the mocks ─────────────────────────────────────────────────────
import { ChatScreen } from './ChatScreen'

// ── Harness ────────────────────────────────────────────────────────────────────

const SID = 'sess-participant-render'

function human(name: string, source: string): ChatParticipant {
  return { kind: 'human', display_name: name, source }
}

function seedStore(messages: ChatMessage[]): void {
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: false,
        isReplaying: false,
        replayCompletedForSession: SID,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
        rateLimitEvent: null,
        lastUserMessageAt: null,
        cancelStage: null,
        lastReceivedEventTime: null,
        trimmedCount: 0,
      },
    },
    messages,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-ava' })
  useWorkspacesStore.setState({ activeWorkspaceId: 'ws-main' })
}

async function renderThread(messages: ChatMessage[]): Promise<{ container: HTMLElement }> {
  seedStore(messages)
  const originalGBCR = Element.prototype.getBoundingClientRect
  Element.prototype.getBoundingClientRect = vi.fn().mockReturnValue({
    height: 600, width: 800, top: 0, left: 0, bottom: 600, right: 800, x: 0, y: 0,
    toJSON() { return this },
  } as DOMRect)
  try {
    let container!: HTMLElement
    await act(async () => {
      container = render(<ChatScreen />).container
      const scrollEl = container.querySelector('[data-testid="virtualized-message-list"]')
      if (scrollEl) {
        Object.defineProperty(scrollEl, 'clientHeight', { configurable: true, value: 600 })
        Object.defineProperty(scrollEl, 'scrollHeight', { configurable: true, value: 8_000 })
        Object.defineProperty(scrollEl, 'scrollTop', { configurable: true, writable: true, value: 7_400 })
      }
    })
    return { container }
  } finally {
    Element.prototype.getBoundingClientRect = originalGBCR
  }
}

beforeEach(() => {
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
  // NO ResizeObserver stub: the feature-detect (ChatScreen's
  // VirtualizedMessageList) then takes PlainMessageList, which renders every
  // row directly — the same row components the virtualized path mounts, minus
  // jsdom's missing layout engine. Assertions below rely on rows being present.
})

afterEach(() => {
  vi.unstubAllGlobals()
})

// ── Tests ──────────────────────────────────────────────────────────────────────

describe('F17 (1)/(2) — own message vs left-side participants', () => {
  it('the web user’s own message (no participant) keeps today’s right-side bubble with NO label', async () => {
    const own: ChatMessage = {
      id: 'own-1', role: 'user', content: 'my own message', timestamp: '2026-10-11T10:00:00Z', status: 'done',
    }
    const { container } = await renderThread([own])

    // Today's bubble, unchanged: same testid, still right-aligned.
    const ownRow = container.querySelector('[data-testid="user-message"]')
    expect(ownRow).not.toBeNull()
    expect(ownRow?.className).toContain('flex-row-reverse')
    // No label machinery anywhere: no participant row, no label, no guessed name.
    expect(container.querySelector('[data-testid="participant-message"]')).toBeNull()
    expect(container.querySelector('[data-testid="participant-label"]')).toBeNull()
    expect(screen.queryByText('Dan · Web')).toBeNull()
  })

  it('a user entry with a human participant renders LEFT-side with "Alice · Telegram"', async () => {
    const theirs: ChatMessage = {
      id: 'tg-1', role: 'user', content: 'hello from Telegram', timestamp: '2026-10-11T10:01:00Z', status: 'done',
      participant: human('Alice', 'telegram'),
    } as ChatMessage
    const { container } = await renderThread([theirs])

    const row = container.querySelector('[data-testid="participant-message"]')
    expect(row).not.toBeNull()
    // LEFT side: the row is NOT mirrored.
    expect(row?.className).not.toContain('flex-row-reverse')
    expect(row?.getAttribute('data-message-role')).toBe('user')
    expect(row?.getAttribute('data-message-id')).toBe('tg-1')
    // The label — D4 row 1.
    expect(container.querySelector('[data-testid="participant-label"]')?.textContent).toBe('Alice · Telegram')
    // And it is NOT the own-bubble surface.
    expect(container.querySelector('[data-testid="user-message"]')).toBeNull()
  })

  it('labels render literally — markdown/HTML in display_name is plain text (never markup)', async () => {
    const theirs: ChatMessage = {
      id: 'md-1', role: 'user', content: 'hi', timestamp: '2026-10-11T10:02:00Z', status: 'done',
      participant: human('**Alice** <b>X</b>', 'slack'),
    } as ChatMessage
    const { container } = await renderThread([theirs])

    const label = container.querySelector('[data-testid="participant-label"]')
    expect(label?.textContent).toBe('**Alice** <b>X</b> · Slack')
    // No markup was parsed out of the name.
    expect(label?.querySelector('strong, b, em, i')).toBeNull()
  })
})

describe('D4 row 2 — agent participants', () => {
  it('resolves the LIVE agent name by agent.agent_id (not display_name)', async () => {
    const theirs: ChatMessage = {
      id: 'peer-1', role: 'user', content: 'peer request text', timestamp: '2026-10-11T10:03:00Z', status: 'done',
      participant: { kind: 'agent', display_name: 'stale-name', agent: { agent_id: 'agent-mia', workspace_id: 'ws-main' } },
    } as ChatMessage
    const { container } = await renderThread([theirs])
    expect(container.querySelector('[data-testid="participant-label"]')?.textContent).toBe('Mia')
  })

  it('falls back to display_name when the agent is not in the catalog', async () => {
    const theirs: ChatMessage = {
      id: 'peer-2', role: 'user', content: 'peer request text', timestamp: '2026-10-11T10:04:00Z', status: 'done',
      participant: { kind: 'agent', display_name: 'Ghost', agent: { agent_id: 'agent-ghost', workspace_id: 'ws-main' } },
    } as ChatMessage
    const { container } = await renderThread([theirs])
    expect(container.querySelector('[data-testid="participant-label"]')?.textContent).toBe('Ghost')
  })

  it('adds the workspace name when the participant’s workspace differs from the current one', async () => {
    const theirs: ChatMessage = {
      id: 'peer-3', role: 'user', content: 'peer request text', timestamp: '2026-10-11T10:05:00Z', status: 'done',
      participant: { kind: 'agent', display_name: 'stale-name', agent: { agent_id: 'agent-mia', workspace_id: 'ws-atlas' } },
    } as ChatMessage
    const { container } = await renderThread([theirs])
    expect(container.querySelector('[data-testid="participant-label"]')?.textContent).toBe('Mia · Atlas')
  })

  it('same workspace — no workspace suffix', async () => {
    const theirs: ChatMessage = {
      id: 'peer-4', role: 'user', content: 'peer request text', timestamp: '2026-10-11T10:06:00Z', status: 'done',
      participant: { kind: 'agent', display_name: 'stale-name', agent: { agent_id: 'agent-mia', workspace_id: 'ws-main' } },
    } as ChatMessage
    const { container } = await renderThread([theirs])
    expect(container.querySelector('[data-testid="participant-label"]')?.textContent).toBe('Mia')
  })
})

describe('F17 (3)/(4) — two bots on one platform show the platform only', () => {
  it('two humans via two telegram-bound bots both label with just the platform', async () => {
    const msgs: ChatMessage[] = [
      { id: 'bot-a', role: 'user', content: 'from bot one', timestamp: '2026-10-11T10:07:00Z', status: 'done', participant: human('Alice', 'telegram') } as ChatMessage,
      { id: 'bot-b', role: 'user', content: 'from bot two', timestamp: '2026-10-11T10:08:00Z', status: 'done', participant: human('Bob', 'telegram') } as ChatMessage,
    ]
    const { container } = await renderThread(msgs)
    const labels = [...container.querySelectorAll('[data-testid="participant-label"]')].map((el) => el.textContent)
    expect(labels).toContain('Alice · Telegram')
    expect(labels).toContain('Bob · Telegram')
  })
})

describe('D4 rows 5-6 — the reply header', () => {
  it('a reply with reply_to_participant shows "Ava → Alice · Telegram"', async () => {
    const reply: ChatMessage = {
      id: 'reply-1', role: 'assistant', content: 'the answer', timestamp: '2026-10-11T10:09:00Z', status: 'done',
      agentId: 'agent-ava', replyToMessageId: 'req-1', replyToParticipant: human('Alice', 'telegram'),
    } as ChatMessage
    const { container } = await renderThread([reply])
    const header = container.querySelector('[data-testid="reply-header"]')
    expect(header?.textContent).toBe('Ava → Alice · Telegram')
  })

  it('reply_to_message_id without reply_to_participant falls back to "Ava → reply"', async () => {
    const reply: ChatMessage = {
      id: 'reply-2', role: 'assistant', content: 'the answer', timestamp: '2026-10-11T10:10:00Z', status: 'done',
      agentId: 'agent-ava', replyToMessageId: 'req-1',
    } as ChatMessage
    const { container } = await renderThread([reply])
    expect(container.querySelector('[data-testid="reply-header"]')?.textContent).toBe('Ava → reply')
  })

  it('a plain assistant message still shows the plain author label (no header)', async () => {
    const plain: ChatMessage = {
      id: 'asst-1', role: 'assistant', content: 'ordinary answer', timestamp: '2026-10-11T10:11:00Z', status: 'done',
      agentId: 'agent-ava',
    }
    const { container } = await renderThread([plain])
    expect(container.querySelector('[data-testid="agent-label"]')?.textContent).toBe('Ava')
    expect(container.querySelector('[data-testid="reply-header"]')).toBeNull()
  })

  it('a long recipient name ellipsizes at ~40 chars and the tooltip carries the full text', async () => {
    const longName = 'N'.repeat(60)
    const reply: ChatMessage = {
      id: 'reply-3', role: 'assistant', content: 'the answer', timestamp: '2026-10-11T10:12:00Z', status: 'done',
      agentId: 'agent-ava', replyToMessageId: 'req-1', replyToParticipant: human(longName, 'telegram'),
    } as ChatMessage
    const { container } = await renderThread([reply])

    const header = container.querySelector('[data-testid="reply-header"]')
    expect(header).not.toBeNull()
    // The visible target is truncated to the cap (39 chars + ellipsis).
    const target = header?.querySelector('[data-testid="reply-target"]')
    expect(target?.textContent?.length).toBe(40)
    expect(target?.textContent?.endsWith('…')).toBe(true)
    // Hover reveals the FULL text in the catalogued tooltip.
    fireEvent.mouseEnter(header?.querySelector('[data-testid="reply-target-trigger"]') as Element)
    expect(screen.getAllByText(longName + ' · Telegram').length).toBeGreaterThan(0)
  })
})

describe('F17 (1) — the send_message chip paired with a reply bubble is hidden', () => {
  const replyPair: ChatMessage[] = [
    {
      id: 'turn-1', role: 'assistant', content: 'Sending that over.', timestamp: '2026-10-11T10:13:00Z', status: 'done',
      agentId: 'agent-ava',
      tool_calls: [
        { id: 'call-paired', tool: 'send_message', status: 'success', params: { reply_to: 'req-1' } },
        { id: 'call-unpaired', tool: 'send_message', status: 'success', params: { reply_to: 'req-9' } },
      ],
    },
    {
      id: 'reply-4', role: 'assistant', content: 'the answer', timestamp: '2026-10-11T10:14:00Z', status: 'done',
      agentId: 'agent-ava', replyToMessageId: 'req-1', replyToParticipant: human('Alice', 'telegram'),
    },
  ] as unknown as ChatMessage[]

  it('the send_message call whose reply_to matches the reply bubble disappears; unpaired stays', async () => {
    const { container } = await renderThread(replyPair)
    const badges = [...container.querySelectorAll('[data-testid="tool-call-badge"]')]
    // Two send_message calls went in; only the UNPAIRED one may render.
    expect(badges).toHaveLength(1)
    expect(badges[0].textContent).toBe('send_message')
  })

  it('without a reply bubble in the thread nothing is hidden (render-only, data untouched)', async () => {
    const onlyCalls = replyPair.slice(0, 1)
    const { container } = await renderThread(onlyCalls)
    expect(container.querySelectorAll('[data-testid="tool-call-badge"]')).toHaveLength(2)
  })
})
