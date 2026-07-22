/**
 * ChatScreen.error-detail.test.tsx — ADR-051 disclosure parity for the
 * VIRTUALIZED / historical render path
 * (src/components/chat/ChatScreen.tsx :: VirtualAssistantMessageRow).
 *
 * Mirrors MessageItem.error-detail.test.tsx but exercises the replay path:
 *   Verbose off → disclosure ABSENT from DOM.
 *   Verbose on  → disclosure visible with the detail content.
 *   Non-error / legacy-error messages never mount it.
 *
 * Uses the PlainMessageList fallback (no ResizeObserver in jsdom), which
 * routes every message through VirtualAssistantMessageRow — the exact
 * render path a reloaded session uses.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'

vi.mock('@assistant-ui/react', () => ({
  useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
  ThreadPrimitive: {
    Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
      React.createElement('div', { className }, children),
    Viewport: React.forwardRef(
      (
        { children, className, style, 'data-testid': testId }: {
          children?: React.ReactNode; className?: string; style?: React.CSSProperties; 'data-testid'?: string
        },
        ref: React.Ref<HTMLDivElement>,
      ) => React.createElement('div', { ref, className, style, 'data-testid': testId }, children),
    ),
    Messages: () => null,
  },
  MessagePrimitive: {
    Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
      React.createElement('div', { className }, children),
    Parts: () => null,
  },
  ComposerPrimitive: {
    Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
      React.createElement('div', { className }, children),
    Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur }: {
      disabled?: boolean; placeholder?: string; className?: string;
      onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
      onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
      onBlur?: () => void;
    }) =>
      React.createElement('textarea', {
        disabled, placeholder, className, onChange, onKeyDown, onBlur,
        'data-testid': 'composer-input',
      }),
    Send: ({ disabled, children, className, 'data-testid': testId }: {
      disabled?: boolean; children?: React.ReactNode; className?: string; 'data-testid'?: string
    }) =>
      React.createElement('button', { type: 'button', disabled, className, 'data-testid': testId ?? 'chat-send' }, children),
    AddAttachment: ({ disabled, children, className }: { disabled?: boolean; children?: React.ReactNode; className?: string }) =>
      React.createElement('button', { type: 'button', disabled, className, 'data-testid': 'add-attachment' }, children),
    Attachments: () => null,
  },
  AttachmentPrimitive: {
    Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
      React.createElement('div', { className }, children),
    Name: () => null,
    Remove: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
      React.createElement('button', { type: 'button', className }, children),
    Thumb: () => null,
  },
  MessagePartPrimitive: { InProgress: () => null },
  ActionBarPrimitive: {
    Root: ({ children }: { children: React.ReactNode }) => React.createElement('div', {}, children),
    Copy: ({ children }: { children: React.ReactNode }) => React.createElement('span', {}, children),
  },
  AuiIf: () => null,
  useComposerRuntime: () => ({
    getState: () => ({ text: '' }),
    setText: vi.fn(),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
  }),
  useMessage: () => ({
    id: 'msg_streaming',
    role: 'assistant',
    status: { type: 'running' },
    content: [],
  }),
  useAttachment: vi.fn(() => ({
    id: 'att-default',
    name: 'file.txt',
    contentType: 'text/plain',
    file: undefined,
    status: { type: 'complete' },
    content: [],
  })),
  makeAssistantToolUI: () => () => null,
}))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
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
  HistoricalMessageMarkdown: ({ content }: { content: string }) =>
    React.createElement('div', { 'data-testid': 'historical-markdown' }, content),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./tools/GenericToolCall', () => ({
  GenericToolCall: ({ toolName }: { toolName: string }) =>
    React.createElement('div', { 'data-testid': 'generic-tool-call-badge' }, toolName),
}))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => React.createElement('div', {}),
}))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'test-session-error-detail'

function seedBucket(messages: ChatMessage[]): void {
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
        spanByParentCallId: {},
        trimmedCount: 0,
      },
    },
    messages,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
}

const errMsg = (overrides: Partial<ChatMessage>): ChatMessage => ({
  id: 'msg_err',
  role: 'assistant',
  content: 'Something failed.',
  timestamp: '2026-03-29T10:00:00Z',
  status: 'error',
  ...overrides,
} as ChatMessage)

beforeEach(() => {
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
  // Force PlainMessageList fallback so VirtualAssistantMessageRow renders.
  vi.unstubAllGlobals()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ;(globalThis as any).ResizeObserver = undefined
  useChatPreferencesStore.setState({ verboseChatEnabled: false })
})

afterEach(() => {
  vi.unstubAllGlobals()
  useChatPreferencesStore.setState({ verboseChatEnabled: false })
})

describe('VirtualAssistantMessageRow — ADR-051 "Technical details" disclosure (historical/replay render)', () => {
  it('does NOT mount the disclosure when verboseChatEnabled is false', async () => {
    seedBucket([errMsg({ errorCode: 'provider_rejected', errorDetail: '400 bad_request' })])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.querySelector('[data-testid="error-detail-disclosure"]')).toBeNull()
    expect(container.textContent).not.toContain('400 bad_request')
    expect(container.textContent).not.toContain('Technical details')
  })

  it('mounts the disclosure with the detail content when verboseChatEnabled is true', async () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: true })
    seedBucket([errMsg({ errorCode: 'provider_rejected', errorDetail: '400 bad_request from provider' })])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const disclosure = container.querySelector('[data-testid="error-detail-disclosure"]')
    expect(disclosure).toBeTruthy()
    expect(disclosure!.textContent).toContain('Technical details')
    expect(disclosure!.textContent).toContain('400 bad_request from provider')
  })

  it('does NOT mount the disclosure for a legacy error message (no typed errorCode)', async () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: true })
    seedBucket([errMsg({ errorCode: undefined, errorDetail: undefined })])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.querySelector('[data-testid="error-detail-disclosure"]')).toBeNull()
  })

  it('does NOT mount the disclosure on a non-error assistant message', async () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: true })
    seedBucket([
      {
        ...errMsg({ status: 'done', errorCode: 'network', errorDetail: 'leaked' }),
      },
    ])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    expect(container.querySelector('[data-testid="error-detail-disclosure"]')).toBeNull()
  })

  it('caps the rendered detail at 512 chars', async () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: true })
    seedBucket([errMsg({ errorCode: 'network', errorDetail: 'y'.repeat(1000) })])

    let container!: HTMLElement
    await act(async () => {
      const result = render(<ChatScreen />)
      container = result.container
    })

    const pre = container.querySelector('[data-testid="error-detail-disclosure"] pre')
    expect(pre).toBeTruthy()
    expect(pre!.textContent!.length).toBe(512)
  })
})
