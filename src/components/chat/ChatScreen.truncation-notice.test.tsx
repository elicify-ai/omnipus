/**
 * ChatScreen.truncation-notice.test.tsx — ADR-087 D1/WP F.
 *
 * A cut-off answer (the provider's output-token limit truncated it before it
 * finished) must show a "(cut off at the output limit)" suffix in the same
 * muted footer slot that already renders "(interrupted)" for a cancelled
 * turn — see ADR-087 (Truncation is an outcome, not a silence) §3 D1 and
 * `src/lib/truncation.ts`'s `getMessageStatusSuffix`.
 *
 * This test seeds `ChatMessage`s directly into the chat store (the same
 * shape `rawToMessage`/the WS replay reducer produce) and renders the real
 * `ChatScreen`, forcing the ResizeObserver-unavailable `PlainMessageList`
 * fallback so `VirtualAssistantMessageRow` renders every message
 * deterministically without jsdom geometry patching — same technique as
 * `ChatScreen.f4-ghost-thinking-indicator.test.tsx` (mock scaffolding
 * copied from there).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, screen } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'

vi.mock('@assistant-ui/react', () => {
  return {
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
  }
})

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
    fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }),
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
    React.createElement('div', { 'data-testid': 'tool-call-badge' }, toolName),
}))
vi.mock('./IframePreview', () => ({ IframePreview: () => null }))
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

const SID = 'test-session-truncation-notice'

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
  vi.unstubAllGlobals()
  // Forces PlainMessageList (ResizeObserver-unavailable fallback), which
  // renders every message through VirtualAssistantMessageRow directly, with
  // no jsdom-geometry patching needed. See file header.
  ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = undefined

  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ADR-087 D1 — truncation notice (historical/virtualized row + InterruptedMessageMarkers)', () => {
  it('shows "(cut off at the output limit)" for a max_output_tokens truncated assistant message', async () => {
    const now = new Date().toISOString()
    const msg: ChatMessage = {
      id: 'msg_cutoff',
      role: 'assistant',
      content: 'The answer starts here and then stops abruptly',
      timestamp: now,
      status: 'done',
      truncated: true,
      truncationReason: 'max_output_tokens',
    }
    seedBucket([msg])

    await act(async () => {
      render(<ChatScreen />)
    })

    // The E2E-detectable fallback (InterruptedMessageMarkers' cut-off pass).
    expect(screen.getByTestId('truncated-marker')).toBeInTheDocument()
    // At least one visible occurrence of the exact suffix text (row footer
    // and/or the marker fallback — both render it).
    expect(screen.getAllByText('(cut off at the output limit)').length).toBeGreaterThan(0)
    expect(screen.queryByText('(interrupted)')).toBeNull()
    expect(screen.queryByTestId('interrupted-marker')).toBeNull()
  })

  it('D1 precedence: an interrupted AND truncated(max_output_tokens) message shows only "(interrupted)"', async () => {
    const now = new Date().toISOString()
    const msg: ChatMessage = {
      id: 'msg_both',
      role: 'assistant',
      content: 'Cancelled after streaming this much',
      timestamp: now,
      status: 'interrupted',
      truncated: true,
      truncationReason: 'max_output_tokens',
    }
    seedBucket([msg])

    await act(async () => {
      render(<ChatScreen />)
    })

    expect(screen.getByTestId('interrupted-marker')).toBeInTheDocument()
    expect(screen.queryByTestId('truncated-marker')).toBeNull()
    expect(screen.queryByText('(cut off at the output limit)')).toBeNull()
  })

  it('D4a: a truncated entry with EMPTY content still renders its row (not collapsed/hidden) with the suffix', async () => {
    const now = new Date().toISOString()
    const msg: ChatMessage = {
      id: 'msg_empty_cutoff',
      role: 'assistant',
      content: '',
      timestamp: now,
      status: 'done',
      truncated: true,
      truncationReason: 'max_output_tokens',
    }
    seedBucket([msg])

    await act(async () => {
      render(<ChatScreen />)
    })

    // The bubble row itself must still mount (not skipped for empty content).
    expect(document.querySelector('[data-message-id="msg_empty_cutoff"]')).not.toBeNull()
    expect(screen.getByTestId('truncated-marker')).toBeInTheDocument()
  })
})
